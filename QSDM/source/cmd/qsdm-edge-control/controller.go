package main

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/edgepool"
	"github.com/pbnjay/memory"
)

const (
	agentCPUCeiling = uint64(500_000)
	agentGPUCeiling = uint64(10_000_000)
	relayCPUCeiling = uint64(1_000_000)
	relayGPUCeiling = uint64(10_000_000)
)

type eventLog struct {
	mu      sync.Mutex
	entries []string
}

func (e *eventLog) Write(data []byte) (int, error) {
	message := string(data)
	if len(message) > 2048 {
		message = message[:2048]
	}
	message = trimLogMessage(message)
	if message == "" {
		return len(data), nil
	}
	e.mu.Lock()
	e.entries = append(e.entries, message)
	if len(e.entries) > 100 {
		e.entries = append([]string(nil), e.entries[len(e.entries)-100:]...)
	}
	e.mu.Unlock()
	return len(data), nil
}

func (e *eventLog) add(message string) {
	_, _ = e.Write([]byte(time.Now().UTC().Format("2006-01-02 15:04:05 ") + message))
}

func (e *eventLog) snapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.entries...)
}

type systemSnapshot struct {
	Hostname    string                   `json:"hostname"`
	CPUThreads  int                      `json:"cpu_threads"`
	TotalRAMMiB uint64                   `json:"total_ram_mib"`
	GPUReady    bool                     `json:"gpu_ready"`
	GPUs        []edgepool.GPUCapability `json:"gpus"`
	GPUMessage  string                   `json:"gpu_message"`
}

type connectionSnapshot struct {
	AgentPaired       bool   `json:"agent_paired"`
	RelayTokensReady  bool   `json:"relay_tokens_ready"`
	MotherConfigured  bool   `json:"mother_configured"`
	MotherConfigPath  string `json:"mother_config_path"`
	GPUHelperDetected bool   `json:"gpu_helper_detected"`
}

type controllerSnapshot struct {
	Version     string               `json:"version"`
	Running     bool                 `json:"running"`
	ActiveRole  string               `json:"active_role"`
	StartedAt   string               `json:"started_at,omitempty"`
	LastError   string               `json:"last_error,omitempty"`
	Settings    controlSettings      `json:"settings"`
	System      systemSnapshot       `json:"system"`
	Connections connectionSnapshot   `json:"connections"`
	Relay       *edgepool.PoolStatus `json:"relay,omitempty"`
	Activity    []string             `json:"activity"`
}

type pairingCodes struct {
	AgentCode      string `json:"agent_code"`
	FederationCode string `json:"federation_code,omitempty"`
	RelayURL       string `json:"relay_url"`
}

type motherPairingCode struct {
	Code       string `json:"code"`
	MotherID   string `json:"mother_id"`
	MotherName string `json:"mother_name"`
}

type controller struct {
	mu         sync.Mutex
	paths      controlPaths
	version    string
	settings   controlSettings
	system     systemSnapshot
	events     *eventLog
	autoStart  func(bool, string) error
	running    bool
	activeRole string
	startedAt  time.Time
	lastError  string
	cancel     context.CancelFunc
	finished   chan struct{}
	runID      uint64
	relay      *edgepool.Relay
}

func newController(paths controlPaths, settings controlSettings, appVersion string) *controller {
	return &controller{
		paths:     paths,
		version:   appVersion,
		settings:  settings,
		system:    inspectSystem(paths),
		events:    &eventLog{},
		autoStart: configureAutoStart,
	}
}

func inspectSystem(paths controlPaths) systemSnapshot {
	hostname, _ := os.Hostname()
	totalRAM := memory.TotalMemory() / 1024 / 1024
	snapshot := systemSnapshot{
		Hostname:    hostname,
		CPUThreads:  runtime.NumCPU(),
		TotalRAMMiB: totalRAM,
		GPUs:        []edgepool.GPUCapability{},
	}
	if paths.GPUHelper == "" {
		snapshot.GPUMessage = "NVIDIA helper not installed"
		return snapshot
	}
	gpus, err := edgepool.DetectNVIDIAGPUs(paths.GPUHelper)
	if err != nil {
		snapshot.GPUMessage = err.Error()
		return snapshot
	}
	snapshot.GPUReady = len(gpus) > 0
	snapshot.GPUs = gpus
	if !snapshot.GPUReady {
		snapshot.GPUMessage = "No compatible NVIDIA GPU detected"
	}
	return snapshot
}

func (c *controller) updateSettings(settings controlSettings) error {
	applySettingDefaults(&settings)
	if err := validateSettings(settings); err != nil {
		return err
	}
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return errors.New("stop Edge Control before changing its settings")
	}
	oldAutoStart := c.settings.AutoStart
	c.mu.Unlock()
	if settings.AutoStart != oldAutoStart {
		if err := c.autoStart(settings.AutoStart, c.paths.Executable); err != nil {
			return fmt.Errorf("configure start at sign-in: %w", err)
		}
	}
	if err := saveSettings(c.paths.SettingsFile, settings); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	c.mu.Lock()
	c.settings = settings
	c.lastError = ""
	c.mu.Unlock()
	c.events.add("Settings saved")
	return nil
}

func (c *controller) start() error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return errors.New("Edge Control is already running")
	}
	settings := c.settings
	c.mu.Unlock()

	if err := validateSettings(settings); err != nil {
		return err
	}
	var relay *edgepool.Relay
	var runner func(context.Context) error
	var err error
	switch settings.Role {
	case "agent":
		runner, err = c.prepareAgent(settings.Agent)
	case "relay":
		relay, runner, err = c.prepareRelay(settings.Relay)
	default:
		err = errors.New("choose Agent or Relay")
	}
	if err != nil {
		c.setError(err)
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		cancel()
		return errors.New("Edge Control is already running")
	}
	c.runID++
	runID := c.runID
	c.running = true
	c.activeRole = settings.Role
	c.startedAt = time.Now().UTC()
	c.lastError = ""
	c.cancel = cancel
	c.finished = finished
	c.relay = relay
	c.mu.Unlock()
	c.events.add(fmt.Sprintf("%s started", friendlyRole(settings.Role)))

	go func() {
		runErr := runner(ctx)
		c.mu.Lock()
		if c.runID == runID {
			c.running = false
			c.cancel = nil
			c.relay = nil
			if runErr != nil && ctx.Err() == nil {
				c.lastError = runErr.Error()
				c.events.add("Stopped: " + runErr.Error())
			} else {
				c.events.add(fmt.Sprintf("%s stopped", friendlyRole(settings.Role)))
			}
		}
		c.mu.Unlock()
		close(finished)
	}()

	select {
	case <-finished:
		c.mu.Lock()
		message := c.lastError
		c.mu.Unlock()
		if message == "" {
			message = "Edge Control stopped during startup"
		}
		return errors.New(message)
	case <-time.After(250 * time.Millisecond):
		return nil
	}
}

func (c *controller) stop() error {
	c.mu.Lock()
	if !c.running || c.cancel == nil {
		c.mu.Unlock()
		return nil
	}
	cancel := c.cancel
	finished := c.finished
	c.mu.Unlock()
	cancel()
	select {
	case <-finished:
		return nil
	case <-time.After(10 * time.Second):
		return errors.New("Edge Control did not stop within 10 seconds")
	}
}

func (c *controller) prepareAgent(settings agentSettings) (func(context.Context) error, error) {
	if stringsTrim(settings.RelayURL) == "" {
		return nil, errors.New("paste the Agent pairing code from the Relay first")
	}
	token, err := edgepool.LoadTokenFile(c.paths.AgentToken)
	if err != nil {
		return nil, errors.New("paste the Agent pairing code from the Relay first")
	}
	resources := make([]edgepool.ResourceKind, 0, 3)
	if settings.CPU {
		resources = append(resources, edgepool.ResourceCPU)
	}
	if settings.RAM {
		resources = append(resources, edgepool.ResourceRAM)
	}
	if settings.GPU {
		if !c.system.GPUReady || c.paths.GPUHelper == "" {
			return nil, errors.New("GPU sharing is enabled, but a compatible NVIDIA GPU and helper were not detected")
		}
		resources = append(resources, edgepool.ResourceGPU)
	}
	logger, closeLog, err := c.agentLogger()
	if err != nil {
		return nil, err
	}
	ramMiB := scaleRAM(c.system.TotalRAMMiB, settings.RAMShare)
	agent, err := edgepool.NewAgent(edgepool.AgentConfig{
		RelayURL:      settings.RelayURL,
		Token:         token,
		WorkerID:      settings.WorkerID,
		AgentVersion:  c.version,
		Resources:     resources,
		RAMMiB:        ramMiB,
		CPUUnits:      scaleUnits(agentCPUCeiling, settings.CPUShare),
		GPUUnits:      scaleUnits(agentGPUCeiling, settings.GPUShare),
		GPUHelperPath: c.paths.GPUHelper,
		PollInterval:  time.Duration(settings.PollDelay) * time.Second,
		Logger:        logger,
	})
	if err != nil {
		closeLog()
		return nil, err
	}
	return func(ctx context.Context) error {
		defer closeLog()
		return agent.Run(ctx)
	}, nil
}

func (c *controller) prepareRelay(settings relaySettings) (*edgepool.Relay, func(context.Context) error, error) {
	if err := c.ensureFederationV2MotherToken(settings); err != nil {
		return nil, nil, err
	}
	agentToken, motherToken, err := c.ensureRelayTokens()
	if err != nil {
		return nil, nil, err
	}
	host := "127.0.0.1"
	if settings.AllowLAN {
		host = "0.0.0.0"
	}
	relay, err := edgepool.NewRelay(edgepool.RelayConfig{
		ListenAddress:    fmt.Sprintf("%s:%d", host, settings.Port),
		AgentToken:       agentToken,
		MotherToken:      motherToken,
		StateDir:         c.paths.PoolDir,
		CPUUnits:         relayCPUCeiling,
		GPUUnits:         relayGPUCeiling,
		RAMMiB:           edgepool.MaxRAMMiB,
		CPUPercent:       settings.CPUShare,
		GPUPercent:       settings.GPUShare,
		RAMPercent:       settings.RAMShare,
		MaxVerifications: 2,
	})
	if err != nil {
		return nil, nil, err
	}
	return relay, relay.Serve, nil
}

func (c *controller) ensureFederationV2MotherToken(settings relaySettings) error {
	if !settings.AllowLAN || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(settings.AdvertisedURL)), "https://") {
		return nil
	}
	markerPath := filepath.Join(c.paths.PoolDir, "federation-v2.ready")
	if _, err := os.Stat(markerPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect federation credential migration: %w", err)
	}
	if err := os.MkdirAll(c.paths.PoolDir, 0o700); err != nil {
		return err
	}
	token, err := edgepool.GenerateToken()
	if err != nil {
		return fmt.Errorf("rotate Mother Hive key for federation v2: %w", err)
	}
	if err := edgepool.RevokeAllMotherTenants(c.paths.PoolDir, time.Now().UTC()); err != nil {
		return fmt.Errorf("revoke derived Mother Hive identities before key rotation: %w", err)
	}
	if err := edgepool.WriteTokenFile(c.paths.MotherToken, token); err != nil {
		return fmt.Errorf("persist federation v2 Mother Hive key: %w", err)
	}
	if err := writePrivateFile(markerPath, []byte("QSDM-EDGE-FEDERATION-v2\n")); err != nil {
		return fmt.Errorf("persist federation v2 migration marker: %w", err)
	}
	c.events.add("Mother Hive key rotated for federation v2; older derived Hive identities were revoked")
	return nil
}

func (c *controller) ensureRelayTokens() ([]byte, []byte, error) {
	if err := os.MkdirAll(c.paths.PoolDir, 0o700); err != nil {
		return nil, nil, err
	}
	agentToken, err := ensureToken(c.paths.AgentToken)
	if err != nil {
		return nil, nil, fmt.Errorf("prepare Agent pairing key: %w", err)
	}
	motherToken, err := ensureToken(c.paths.MotherToken)
	if err != nil {
		return nil, nil, fmt.Errorf("prepare Mother Hive key: %w", err)
	}
	return agentToken, motherToken, nil
}

func ensureToken(path string) ([]byte, error) {
	token, err := edgepool.LoadTokenFile(path)
	if err == nil {
		return token, nil
	}
	if !errors.Is(rootFileError(err), os.ErrNotExist) {
		return nil, err
	}
	token, err = edgepool.GenerateToken()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := edgepool.WriteTokenFile(path, token); err != nil {
		return nil, err
	}
	return token, nil
}

func (c *controller) pairAgent(code string) error {
	payload, token, err := decodePairingCode(code, "agent")
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return errors.New("stop the Agent before changing its Relay")
	}
	settings := c.settings
	c.mu.Unlock()
	if err := os.MkdirAll(c.paths.PoolDir, 0o700); err != nil {
		return err
	}
	if err := edgepool.WriteTokenFile(c.paths.AgentToken, token); err != nil {
		return err
	}
	settings.Role = "agent"
	settings.Agent.RelayURL = payload.RelayURL
	if err := saveSettings(c.paths.SettingsFile, settings); err != nil {
		return err
	}
	c.mu.Lock()
	c.settings = settings
	c.lastError = ""
	c.mu.Unlock()
	c.events.add("Agent paired with Relay")
	return nil
}

func (c *controller) getPairingCodes() (pairingCodes, error) {
	c.mu.Lock()
	settings := c.settings.Relay
	c.mu.Unlock()
	if err := c.ensureFederationV2MotherToken(settings); err != nil {
		return pairingCodes{}, err
	}
	agentToken, motherToken, err := c.ensureRelayTokens()
	if err != nil {
		return pairingCodes{}, err
	}
	relayURL := fmt.Sprintf("http://127.0.0.1:%d", settings.Port)
	if settings.AllowLAN {
		relayURL = settings.AdvertisedURL
	}
	agentCode, err := encodePairingCode("agent", relayURL, agentToken)
	if err != nil {
		return pairingCodes{}, err
	}
	federationCode := ""
	if strings.HasPrefix(strings.ToLower(relayURL), "https://") {
		federationCode, err = encodeFederationPairingCode(relayURL, motherToken, c.system.Hostname)
		if err != nil {
			return pairingCodes{}, err
		}
	}
	return pairingCodes{
		AgentCode:      agentCode,
		FederationCode: federationCode,
		RelayURL:       relayURL,
	}, nil
}

func (c *controller) createMotherPairingCode(name string) (motherPairingCode, error) {
	c.mu.Lock()
	settings := c.settings.Relay
	c.mu.Unlock()
	if err := c.ensureFederationV2MotherToken(settings); err != nil {
		return motherPairingCode{}, err
	}
	_, motherToken, err := c.ensureRelayTokens()
	if err != nil {
		return motherPairingCode{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "QSDM Hive"
	}
	relayURL := fmt.Sprintf("http://127.0.0.1:%d", settings.Port)
	if settings.AllowLAN {
		relayURL = settings.AdvertisedURL
	}
	code, tenant, err := encodeLocalMotherPairingCode(relayURL, c.paths.PoolDir, motherToken, name)
	if err != nil {
		return motherPairingCode{}, err
	}
	c.events.add("Created pairing code for " + tenant.MotherName)
	return motherPairingCode{Code: code, MotherID: tenant.MotherID, MotherName: tenant.MotherName}, nil
}

func (c *controller) connectLocalMother() error {
	_, motherToken, err := c.ensureRelayTokens()
	if err != nil {
		return err
	}
	c.mu.Lock()
	port := c.settings.Relay.Port
	c.mu.Unlock()
	name := c.system.Hostname + " QSDM Hive"
	relayURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if config, ok := reusableLocalMotherConfig(c.paths.MotherConfig, c.paths.PoolDir, motherToken); ok {
		if config.RelayURL != relayURL {
			config.RelayURL = relayURL
			if err := writeMotherHiveConfig(c.paths.MotherConfig, config); err != nil {
				return err
			}
		}
		c.events.add("QSDM Hive is already connected on this computer")
		return nil
	}
	encodedContext, scopedToken, tenant, err := edgepool.CreateMotherTenantCredential(
		c.paths.PoolDir,
		motherToken,
		name,
		time.Now().UTC(),
	)
	if err != nil {
		return err
	}
	tokenPath := filepath.Join(c.paths.PoolDir, "hive-mother-"+tenant.MotherID+".token")
	if err := edgepool.WriteTokenFile(tokenPath, scopedToken); err != nil {
		return err
	}
	config := motherHiveConfig{
		SchemaVersion:  1,
		RelayURL:       relayURL,
		TokenFile:      tokenPath,
		ConnectionMode: "private-multi-hive",
		MotherID:       tenant.MotherID,
		MotherName:     tenant.MotherName,
		MotherContext:  encodedContext,
	}
	if err := writeMotherHiveConfig(c.paths.MotherConfig, config); err != nil {
		return err
	}
	c.events.add("Mother Hive connected on this computer")
	return nil
}

func reusableLocalMotherConfig(path, stateDir string, motherToken []byte) (motherHiveConfig, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return motherHiveConfig{}, false
	}
	var config motherHiveConfig
	if json.Unmarshal(raw, &config) != nil ||
		config.SchemaVersion != 1 ||
		config.ConnectionMode != "private-multi-hive" ||
		config.MotherID == "" ||
		config.MotherName == "" ||
		config.MotherContext == "" {
		return motherHiveConfig{}, false
	}
	contextValue, err := edgepool.DecodeMotherContext(config.MotherContext)
	if err != nil || contextValue.MotherID != config.MotherID || contextValue.MotherName != config.MotherName {
		return motherHiveConfig{}, false
	}
	expectedToken, _, err := edgepool.DeriveMotherToken(motherToken, config.MotherContext)
	if err != nil {
		return motherHiveConfig{}, false
	}
	storedToken, err := edgepool.LoadTokenFile(config.TokenFile)
	if err != nil || !hmac.Equal(storedToken, expectedToken) {
		return motherHiveConfig{}, false
	}
	tenants, err := edgepool.ListMotherTenants(stateDir)
	if err != nil {
		return motherHiveConfig{}, false
	}
	for _, tenant := range tenants {
		if tenant.MotherID == config.MotherID && tenant.RevokedAt == "" {
			return config, true
		}
	}
	return motherHiveConfig{}, false
}

func writeMotherHiveConfig(path string, config motherHiveConfig) error {
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(path, append(raw, '\n'))
}

func (c *controller) revokeMother(motherID string) error {
	now := time.Now().UTC()
	if err := edgepool.RevokeMotherTenant(c.paths.PoolDir, motherID, now); err != nil {
		return err
	}
	c.mu.Lock()
	relay := c.relay
	c.mu.Unlock()
	cancelled := 0
	if relay != nil {
		var err error
		cancelled, err = relay.CancelComputeJobsForMother(motherID, now)
		if err != nil {
			return err
		}
	}
	c.events.add(fmt.Sprintf("Revoked Mother Hive %s and cancelled %d unfinished jobs", motherID, cancelled))
	return nil
}

func (c *controller) snapshot() controllerSnapshot {
	c.mu.Lock()
	running := c.running
	activeRole := c.activeRole
	startedAt := c.startedAt
	lastError := c.lastError
	settings := c.settings
	relay := c.relay
	c.mu.Unlock()
	var relayStatus *edgepool.PoolStatus
	if relay != nil {
		status := relay.Status()
		relayStatus = &status
	}
	return controllerSnapshot{
		Version:    c.version,
		Running:    running,
		ActiveRole: activeRole,
		StartedAt:  formatTime(startedAt),
		LastError:  lastError,
		Settings:   settings,
		System:     c.system,
		Connections: connectionSnapshot{
			AgentPaired:       tokenFileReady(c.paths.AgentToken) && settings.Agent.RelayURL != "",
			RelayTokensReady:  tokenFileReady(c.paths.AgentToken) && tokenFileReady(c.paths.MotherToken),
			MotherConfigured:  motherConfigReady(c.paths.MotherConfig),
			MotherConfigPath:  c.paths.MotherConfig,
			GPUHelperDetected: c.paths.GPUHelper != "",
		},
		Relay:    relayStatus,
		Activity: c.events.snapshot(),
	}
}

func (c *controller) setError(err error) {
	c.mu.Lock()
	c.lastError = err.Error()
	c.mu.Unlock()
	c.events.add("Could not start: " + err.Error())
}

func (c *controller) agentLogger() (*log.Logger, func(), error) {
	if err := os.MkdirAll(filepath.Dir(c.paths.AgentLog), 0o700); err != nil {
		return nil, nil, err
	}
	file, err := os.OpenFile(c.paths.AgentLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, err
	}
	return log.New(io.MultiWriter(c.events, file), "", log.LstdFlags|log.LUTC), func() { _ = file.Close() }, nil
}

func scaleUnits(ceiling uint64, percent int) uint64 {
	value := ceiling * uint64(percent) / 100
	if value == 0 {
		return 1
	}
	return value
}

func scaleRAM(totalMiB uint64, percent int) uint64 {
	value := totalMiB * uint64(percent) / 100
	if value > edgepool.MaxRAMMiB {
		value = edgepool.MaxRAMMiB
	}
	if value < 32 {
		value = 32
	}
	return value
}

func tokenFileReady(path string) bool {
	_, err := edgepool.LoadTokenFile(path)
	return err == nil
}

func motherConfigReady(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var config motherHiveConfig
	return json.Unmarshal(raw, &config) == nil && config.SchemaVersion == 1 && tokenFileReady(config.TokenFile)
}

func rootFileError(err error) error {
	for err != nil {
		var pathErr *os.PathError
		if errors.As(err, &pathErr) {
			return pathErr.Err
		}
		break
	}
	return err
}

func friendlyRole(role string) string {
	if role == "relay" {
		return "Relay"
	}
	return "Agent"
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
}

func trimLogMessage(value string) string {
	for len(value) > 0 && (value[len(value)-1] == '\n' || value[len(value)-1] == '\r') {
		value = value[:len(value)-1]
	}
	return value
}

func stringsTrim(value string) string {
	for len(value) > 0 && (value[0] == ' ' || value[0] == '\t' || value[0] == '\n' || value[0] == '\r') {
		value = value[1:]
	}
	for len(value) > 0 {
		last := value[len(value)-1]
		if last != ' ' && last != '\t' && last != '\n' && last != '\r' {
			break
		}
		value = value[:len(value)-1]
	}
	return value
}
