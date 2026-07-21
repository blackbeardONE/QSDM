package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/edgepool"
)

const settingsSchemaVersion = 1

type controlSettings struct {
	SchemaVersion int           `json:"schema_version"`
	Role          string        `json:"role"`
	AutoStart     bool          `json:"auto_start"`
	Agent         agentSettings `json:"agent"`
	Relay         relaySettings `json:"relay"`
}

type agentSettings struct {
	RelayURL  string `json:"relay_url"`
	WorkerID  string `json:"worker_id"`
	CPU       bool   `json:"cpu"`
	GPU       bool   `json:"gpu"`
	RAM       bool   `json:"ram"`
	CPUShare  int    `json:"cpu_share"`
	GPUShare  int    `json:"gpu_share"`
	RAMShare  int    `json:"ram_share"`
	PollDelay int    `json:"poll_delay_seconds"`
}

type relaySettings struct {
	Port          int    `json:"port"`
	AllowLAN      bool   `json:"allow_lan"`
	AdvertisedURL string `json:"advertised_url"`
	CPUShare      int    `json:"cpu_share"`
	GPUShare      int    `json:"gpu_share"`
	RAMShare      int    `json:"ram_share"`
}

type controlPaths struct {
	ConfigDir    string
	SettingsFile string
	ControlToken string
	PoolDir      string
	AgentToken   string
	MotherToken  string
	MotherConfig string
	AgentLog     string
	GPUHelper    string
	Executable   string
}

type motherHiveConfig struct {
	SchemaVersion  int    `json:"schema_version"`
	RelayURL       string `json:"relay_url"`
	TokenFile      string `json:"token_file"`
	ConnectionMode string `json:"connection_mode,omitempty"`
	MotherID       string `json:"mother_id,omitempty"`
	MotherName     string `json:"mother_name,omitempty"`
	MotherContext  string `json:"mother_context,omitempty"`
}

func defaultControlPaths() (controlPaths, error) {
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return controlPaths{}, fmt.Errorf("find user settings directory: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return controlPaths{}, fmt.Errorf("find Edge Control executable: %w", err)
	}
	configDir := filepath.Join(configRoot, "QSDM", "edge-control")
	poolDir := filepath.Join(configRoot, "QSDM", "edge-pool")
	return controlPaths{
		ConfigDir:    configDir,
		SettingsFile: filepath.Join(configDir, "settings.json"),
		ControlToken: filepath.Join(configDir, "control.token"),
		PoolDir:      poolDir,
		AgentToken:   filepath.Join(poolDir, "agent.token"),
		MotherToken:  filepath.Join(poolDir, "mother-hive.token"),
		MotherConfig: filepath.Join(poolDir, "mother-hive.json"),
		AgentLog:     filepath.Join(poolDir, "agent.log"),
		GPUHelper:    findGPUHelper(filepath.Dir(executable)),
		Executable:   executable,
	}, nil
}

func defaultSettings() controlSettings {
	return controlSettings{
		SchemaVersion: settingsSchemaVersion,
		Role:          "agent",
		Agent: agentSettings{
			WorkerID:  defaultWorkerID(),
			CPU:       true,
			RAM:       true,
			CPUShare:  50,
			GPUShare:  25,
			RAMShare:  25,
			PollDelay: 5,
		},
		Relay: relaySettings{
			Port:          7740,
			AdvertisedURL: defaultLANRelayURL(7740),
			CPUShare:      50,
			GPUShare:      40,
			RAMShare:      25,
		},
	}
}

func loadSettings(path string) (controlSettings, error) {
	settings := defaultSettings()
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return controlSettings{}, fmt.Errorf("read Edge Control settings: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil {
		return controlSettings{}, fmt.Errorf("decode Edge Control settings: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return controlSettings{}, err
	}
	applySettingDefaults(&settings)
	if err := validateSettings(settings); err != nil {
		return controlSettings{}, fmt.Errorf("validate Edge Control settings: %w", err)
	}
	return settings, nil
}

func saveSettings(path string, settings controlSettings) error {
	settings.SchemaVersion = settingsSchemaVersion
	if err := validateSettings(settings); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(path, append(raw, '\n'))
}

func applySettingDefaults(settings *controlSettings) {
	defaults := defaultSettings()
	if settings.SchemaVersion == 0 {
		settings.SchemaVersion = settingsSchemaVersion
	}
	if settings.Role == "" {
		settings.Role = defaults.Role
	}
	if settings.Agent.WorkerID == "" {
		settings.Agent.WorkerID = defaults.Agent.WorkerID
	}
	if settings.Agent.CPUShare == 0 {
		settings.Agent.CPUShare = defaults.Agent.CPUShare
	}
	if settings.Agent.GPUShare == 0 {
		settings.Agent.GPUShare = defaults.Agent.GPUShare
	}
	if settings.Agent.RAMShare == 0 {
		settings.Agent.RAMShare = defaults.Agent.RAMShare
	}
	if settings.Agent.PollDelay == 0 {
		settings.Agent.PollDelay = defaults.Agent.PollDelay
	}
	if settings.Relay.Port == 0 {
		settings.Relay.Port = defaults.Relay.Port
	}
	if settings.Relay.AdvertisedURL == "" {
		settings.Relay.AdvertisedURL = defaultLANRelayURL(settings.Relay.Port)
	}
	if settings.Relay.CPUShare == 0 {
		settings.Relay.CPUShare = defaults.Relay.CPUShare
	}
	if settings.Relay.GPUShare == 0 {
		settings.Relay.GPUShare = defaults.Relay.GPUShare
	}
	if settings.Relay.RAMShare == 0 {
		settings.Relay.RAMShare = defaults.Relay.RAMShare
	}
}

func validateSettings(settings controlSettings) error {
	if settings.SchemaVersion != settingsSchemaVersion {
		return fmt.Errorf("unsupported settings version %d", settings.SchemaVersion)
	}
	if settings.Role != "agent" && settings.Role != "relay" {
		return errors.New("choose Agent or Relay")
	}
	if err := edgepool.ValidateWorkerID(settings.Agent.WorkerID); err != nil {
		return fmt.Errorf("computer name: %w", err)
	}
	if settings.Role == "agent" && !settings.Agent.CPU && !settings.Agent.GPU && !settings.Agent.RAM {
		return errors.New("select at least one Agent resource")
	}
	for name, value := range map[string]int{
		"Agent CPU share": settings.Agent.CPUShare,
		"Agent GPU share": settings.Agent.GPUShare,
		"Agent RAM share": settings.Agent.RAMShare,
		"Relay CPU limit": settings.Relay.CPUShare,
		"Relay GPU limit": settings.Relay.GPUShare,
		"Relay RAM limit": settings.Relay.RAMShare,
	} {
		if value < 1 || value > 100 {
			return fmt.Errorf("%s must be between 1 and 100", name)
		}
	}
	if settings.Agent.PollDelay < 1 || settings.Agent.PollDelay > 300 {
		return errors.New("Agent rest interval must be between 1 and 300 seconds")
	}
	if settings.Relay.Port < 1024 || settings.Relay.Port > 65535 {
		return errors.New("Relay port must be between 1024 and 65535")
	}
	if strings.TrimSpace(settings.Agent.RelayURL) != "" {
		if _, err := validateRelayURL(settings.Agent.RelayURL, false); err != nil {
			return fmt.Errorf("Agent Relay address: %w", err)
		}
	}
	if settings.Relay.AllowLAN {
		parsed, err := validateRelayURL(settings.Relay.AdvertisedURL, true)
		if err != nil {
			return fmt.Errorf("Relay network address: %w", err)
		}
		host := parsed.Hostname()
		if host == "0.0.0.0" || host == "::" || strings.EqualFold(host, "localhost") {
			return errors.New("Relay network address must use this computer's private IP address or hostname")
		}
	}
	return nil
}

func validateRelayURL(value string, requireReachableHost bool) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return nil, errors.New("use a complete http:// or https:// address")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("address cannot contain a query or fragment")
	}
	if requireReachableHost {
		host := parsed.Hostname()
		if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
			return nil, errors.New("use a reachable private address, not a wildcard address")
		}
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("settings must contain exactly one JSON object")
		}
		return fmt.Errorf("invalid trailing settings data: %w", err)
	}
	return nil
}

func defaultWorkerID() string {
	hostname, _ := os.Hostname()
	hostname = strings.ToLower(hostname)
	var builder strings.Builder
	for _, char := range hostname {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('-')
		}
	}
	workerID := strings.Trim(builder.String(), "-._")
	if workerID == "" {
		workerID = fmt.Sprintf("qsdm-worker-%d", os.Getpid())
	}
	if len(workerID) > 64 {
		workerID = workerID[:64]
	}
	return workerID
}

func writePrivateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		_ = os.Remove(path + ".new")
		file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		if _, err = file.Write(data); err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err != nil {
			return err
		}
		return closeErr
	}
	temporary := path + ".new"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func findGPUHelper(executableDir string) string {
	name := "qsdm-edge-gpu-helper"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	candidates := []string{
		filepath.Join(executableDir, name),
		filepath.Join(executableDir, "resources", "edge", name),
		filepath.Join(filepath.Dir(executableDir), "edge", name),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func defaultLANRelayURL(port int) string {
	host := "127.0.0.1"
	interfaces, _ := net.Interfaces()
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagUp == 0 || networkInterface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, _ := networkInterface.Addrs()
		for _, address := range addresses {
			var ip net.IP
			switch value := address.(type) {
			case *net.IPNet:
				ip = value.IP
			case *net.IPAddr:
				ip = value.IP
			}
			if ip4 := ip.To4(); ip4 != nil && ip4.IsPrivate() {
				host = ip4.String()
				return fmt.Sprintf("http://%s:%d", host, port)
			}
		}
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}
