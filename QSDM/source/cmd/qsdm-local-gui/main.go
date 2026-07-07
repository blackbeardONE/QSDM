// qsdm-local-gui is a local-only operator console for a Windows home node.
// It serves a small browser UI on 127.0.0.1:<random-port> and controls only
// local processes/services: the loopback validator, the QSDMMiner service, and
// the outbound home gateway.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/buildinfo"
)

//go:embed all:web
var webFS embed.FS

const (
	listenHost     = "127.0.0.1"
	validatorAPI   = "http://127.0.0.1:8080"
	validatorReady = "http://127.0.0.1:8080/api/v1/health/ready"
	dashboardURL   = "http://127.0.0.1:8081/"
	defaultRelay   = "https://api.qsdm.tech"
	defaultSlot    = "home-validator"

	heartbeatGrace = 90 * time.Second
)

var gatewayProcessNames = []string{
	"qsdm-home-gateway",
	"qsdm-home-gateway-hive",
	"qsdm-home-gateway-hive.new",
}

var validatorProcessNames = []string{
	"qsdm",
	"qsdm-new",
	"qsdm-sqlite",
	"qsdm-sqlite-next",
	"qsdm-local-validator",
	"qsdm-local-validator-sqlite*",
	"qsdm-local-validator-next",
	"qsdm-local-validator-hive",
	"qsdm-local-validator-hive.new",
}

type state struct {
	root            string
	localRoot       string
	validatorScript string
	gatewayScript   string

	tokenMu  sync.RWMutex
	token    string
	lastBeat atomic.Int64

	openBrowser bool
}

type snapshot struct {
	Root       string            `json:"root"`
	Admin      adminSnapshot     `json:"admin"`
	Validator  validatorSnapshot `json:"validator"`
	Miner      minerSnapshot     `json:"miner"`
	Gateway    gatewaySnapshot   `json:"gateway"`
	Exposure   exposureSnapshot  `json:"exposure"`
	Links      linksSnapshot     `json:"links"`
	CheckedAt  string            `json:"checked_at"`
	GUIVersion string            `json:"gui_version"`
}

type adminSnapshot struct {
	Platform string `json:"platform"`
	Elevated bool   `json:"elevated"`
}

type validatorSnapshot struct {
	Running        bool           `json:"running"`
	Ready          bool           `json:"ready"`
	ConfiguredMode string         `json:"configured_mode"`
	ActiveMode     string         `json:"active_mode"`
	Error          string         `json:"error,omitempty"`
	NodeID         string         `json:"node_id,omitempty"`
	Role           string         `json:"role,omitempty"`
	ChainTip       int64          `json:"chain_tip,omitempty"`
	Peers          int64          `json:"peers,omitempty"`
	Uptime         string         `json:"uptime,omitempty"`
	V2Active       bool           `json:"v2_active"`
	Processes      []processInfo  `json:"processes"`
	Listeners      []listenerInfo `json:"listeners"`
}

type minerSnapshot struct {
	Service   serviceStatus `json:"service"`
	Processes []processInfo `json:"processes"`
	LogPath   string        `json:"log_path"`
}

type gatewaySnapshot struct {
	Running     bool          `json:"running"`
	Slot        string        `json:"slot"`
	Relay       string        `json:"relay"`
	PublicURL   string        `json:"public_url"`
	PublicOK    bool          `json:"public_ok"`
	PublicCode  int           `json:"public_code,omitempty"`
	PublicError string        `json:"public_error,omitempty"`
	ChainTip    int64         `json:"chain_tip,omitempty"`
	Processes   []processInfo `json:"processes"`
}

type exposureSnapshot struct {
	Safe      bool           `json:"safe"`
	Summary   string         `json:"summary"`
	Listeners []listenerInfo `json:"listeners"`
}

type linksSnapshot struct {
	APIStatus       string `json:"api_status"`
	Dashboard       string `json:"dashboard"`
	PublicGateway   string `json:"public_gateway"`
	MinerConfigPath string `json:"miner_config_path"`
}

type processInfo struct {
	Name string `json:"name"`
	PID  int    `json:"pid"`
	Path string `json:"path,omitempty"`
}

type listenerInfo struct {
	Address   string `json:"address"`
	Port      int    `json:"port"`
	PID       int    `json:"pid"`
	LocalOnly bool   `json:"local_only"`
}

type serviceStatus struct {
	Supported bool   `json:"supported"`
	Installed bool   `json:"installed"`
	State     string `json:"state"`
	Raw       string `json:"raw,omitempty"`
	BinPath   string `json:"binPath,omitempty"`
	Name      string `json:"name"`
}

type actionResult struct {
	OK     bool   `json:"ok"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
	PID    int    `json:"pid,omitempty"`
}

func main() {
	os.Exit(run())
}

func run() int {
	root := findRoot()
	st := &state{
		root:            root,
		localRoot:       filepath.Join(root, "source", ".cache", "local-validator"),
		validatorScript: filepath.Join(root, "scripts", "start_local_validator.ps1"),
		gatewayScript:   filepath.Join(root, "scripts", "start_home_gateway.ps1"),
		openBrowser:     os.Getenv("QSDM_LOCAL_GUI_NO_OPEN") == "",
		token:           mustToken(),
	}

	ln, err := net.Listen("tcp", listenHost+":0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "qsdm-local-gui: listen: %v\n", err)
		return 1
	}
	port := ln.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://127.0.0.1:%d/?t=%s", port, st.token)
	if urlFile := os.Getenv("QSDM_LOCAL_GUI_URL_FILE"); urlFile != "" {
		if err := os.MkdirAll(filepath.Dir(urlFile), 0o755); err == nil {
			_ = os.WriteFile(urlFile, []byte(url+"\n"), 0o600)
		}
	}

	mux := http.NewServeMux()
	st.routes(mux)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	st.lastBeat.Store(time.Now().Unix())
	if os.Getenv("QSDM_LOCAL_GUI_STAY_OPEN") == "" {
		go st.heartbeatReaper(srv)
	}

	fmt.Printf("%s ready at %s\n", buildinfo.Short("qsdm-local-gui"), url)
	if st.openBrowser {
		openInBrowser(url)
	}

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "qsdm-local-gui: serve: %v\n", err)
		return 1
	}
	return 0
}

func (s *state) routes(mux *http.ServeMux) {
	subFS, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", s.installCookie(http.FileServer(http.FS(subFS))))
	mux.Handle("/api/snapshot", s.tokenGate(http.HandlerFunc(s.handleSnapshot)))
	mux.Handle("/api/validator/start", s.tokenGate(http.HandlerFunc(s.handleValidatorStart)))
	mux.Handle("/api/validator/stop", s.tokenGate(http.HandlerFunc(s.handleValidatorStop)))
	mux.Handle("/api/validator/restart", s.tokenGate(http.HandlerFunc(s.handleValidatorRestart)))
	mux.Handle("/api/miner/start", s.tokenGate(http.HandlerFunc(s.handleMinerStart)))
	mux.Handle("/api/miner/stop", s.tokenGate(http.HandlerFunc(s.handleMinerStop)))
	mux.Handle("/api/gateway/start", s.tokenGate(http.HandlerFunc(s.handleGatewayStart)))
	mux.Handle("/api/gateway/stop", s.tokenGate(http.HandlerFunc(s.handleGatewayStop)))
	mux.Handle("/api/admin/relaunch", s.tokenGate(http.HandlerFunc(s.handleAdminRelaunch)))
	mux.Handle("/api/log", s.tokenGate(http.HandlerFunc(s.handleLog)))
	mux.Handle("/api/heartbeat", s.tokenGate(http.HandlerFunc(s.handleHeartbeat)))
	mux.Handle("/api/quit", s.tokenGate(http.HandlerFunc(s.handleQuit)))
}

func (s *state) tokenGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.tokenMu.RLock()
		want := s.token
		s.tokenMu.RUnlock()
		got := r.Header.Get("X-QSDM-Token")
		if got == "" {
			if c, err := r.Cookie("qsdm-local-gui-token"); err == nil {
				got = c.Value
			}
		}
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		s.lastBeat.Store(time.Now().Unix())
		next.ServeHTTP(w, r)
	})
}

func (s *state) installCookie(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if t := r.URL.Query().Get("t"); t != "" {
			s.tokenMu.RLock()
			want := s.token
			s.tokenMu.RUnlock()
			if subtle.ConstantTimeCompare([]byte(t), []byte(want)) == 1 {
				http.SetCookie(w, &http.Cookie{
					Name:     "qsdm-local-gui-token",
					Value:    t,
					Path:     "/",
					HttpOnly: true,
					SameSite: http.SameSiteStrictMode,
					MaxAge:   3600,
				})
				s.lastBeat.Store(time.Now().Unix())
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *state) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.snapshot(r.Context()))
}

func (s *state) handleValidatorStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	out, err := runCommand(r.Context(), 45*time.Second, "powershell.exe", s.validatorLauncherArgs()...)
	writeAction(w, out, err)
}

func (s *state) handleValidatorStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	out, err := s.stopValidator(r.Context())
	writeAction(w, out, err)
}

func (s *state) handleValidatorRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	stopOut, _ := s.stopValidator(r.Context())
	time.Sleep(1200 * time.Millisecond)
	startOut, err := runCommand(r.Context(), 45*time.Second, "powershell.exe", s.validatorLauncherArgs()...)
	writeAction(w, strings.TrimSpace(stopOut+"\n"+startOut), err)
}

func (s *state) handleMinerStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	out, err := runCommand(r.Context(), 20*time.Second, "sc.exe", "start", "QSDMMiner")
	if err != nil && serviceAlreadyInState(out, "1056", "already been started") {
		writeJSON(w, http.StatusOK, actionResult{OK: true, Output: out})
		return
	}
	writeAction(w, out, err)
}

func (s *state) handleMinerStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	out, err := runCommand(r.Context(), 20*time.Second, "sc.exe", "stop", "QSDMMiner")
	if err != nil && serviceAlreadyInState(out, "1062", "service has not been started") {
		writeJSON(w, http.StatusOK, actionResult{OK: true, Output: out})
		return
	}
	writeAction(w, out, err)
}

func (s *state) handleGatewayStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if len(queryProcesses(gatewayProcessNames, s.localRoot)) > 0 {
		writeJSON(w, http.StatusOK, actionResult{OK: true, Output: "home gateway already running"})
		return
	}
	if _, err := os.Stat(s.gatewayScript); err != nil {
		writeJSON(w, http.StatusFailedDependency, actionResult{OK: false, Error: err.Error()})
		return
	}
	if err := os.MkdirAll(s.localRoot, 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, actionResult{OK: false, Error: err.Error()})
		return
	}
	stdout, err := os.OpenFile(filepath.Join(s.localRoot, "home-gateway.out.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, actionResult{OK: false, Error: err.Error()})
		return
	}
	stderr, err := os.OpenFile(filepath.Join(s.localRoot, "home-gateway.err.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		_ = stdout.Close()
		writeJSON(w, http.StatusInternalServerError, actionResult{OK: false, Error: err.Error()})
		return
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass",
		"-File", s.gatewayScript, "-Relay", defaultRelay, "-Slot", defaultSlot)
	cmd.Dir = s.root
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		writeJSON(w, http.StatusInternalServerError, actionResult{OK: false, Error: err.Error()})
		return
	}
	go func() {
		_ = cmd.Wait()
		_ = stdout.Close()
		_ = stderr.Close()
	}()
	writeJSON(w, http.StatusOK, actionResult{OK: true, PID: cmd.Process.Pid, Output: "home gateway starting"})
}

func (s *state) handleGatewayStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	out, err := stopProcessesByNameAndPath(r.Context(), gatewayProcessNames, s.localRoot)
	writeAction(w, out, err)
}

func (s *state) handleAdminRelaunch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if runtime.GOOS != "windows" {
		writeJSON(w, http.StatusBadRequest, actionResult{OK: false, Error: "admin relaunch is only supported on Windows"})
		return
	}
	if isElevated() {
		writeJSON(w, http.StatusOK, actionResult{OK: true, Output: "GUI is already running with administrator rights"})
		return
	}
	script := filepath.Join(s.root, "scripts", "start_local_gui_admin.ps1")
	if _, err := os.Stat(script); err != nil {
		writeJSON(w, http.StatusFailedDependency, actionResult{OK: false, Error: err.Error()})
		return
	}
	psArgs := fmt.Sprintf("-NoProfile -ExecutionPolicy Bypass -NoExit -File %s -QsdmRoot %s -NoElevate",
		powershellQuote(script), powershellQuote(s.root))
	out, err := runCommand(r.Context(), 20*time.Second, "powershell.exe",
		"-NoProfile", "-ExecutionPolicy", "Bypass", "-Command",
		fmt.Sprintf("Start-Process -FilePath 'powershell.exe' -Verb RunAs -ArgumentList %s", powershellQuote(psArgs)))
	if err != nil {
		writeAction(w, out, err)
		return
	}
	writeJSON(w, http.StatusOK, actionResult{OK: true, Output: "Windows administrator prompt requested"})
}

func (s *state) handleLog(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	path := s.logPath(kind)
	lines, err := tailFile(path, 220)
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusOK, map[string]any{"path": path, "lines": []string{fmt.Sprintf("(no log at %s)", path)}})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": path, "lines": lines})
}

func (s *state) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	s.lastBeat.Store(time.Now().Unix())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *state) handleQuit(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	go func() {
		time.Sleep(200 * time.Millisecond)
		os.Exit(0)
	}()
}

func (s *state) heartbeatReaper(srv *http.Server) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for range t.C {
		last := time.Unix(s.lastBeat.Load(), 0)
		if time.Since(last) > heartbeatGrace {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = srv.Shutdown(ctx)
			cancel()
			os.Exit(0)
		}
	}
}

func (s *state) snapshot(ctx context.Context) snapshot {
	valProcesses := queryProcesses(validatorProcessNames, s.localRoot)
	valListeners := queryListeners([]int{4001, 8080, 8081})
	mode := s.validatorModeConfig()
	val := validatorSnapshot{
		Processes:      valProcesses,
		Listeners:      valListeners,
		ConfiguredMode: mode.Mode,
		ActiveMode:     activeValidatorMode(s.localRoot, valProcesses),
	}
	val.Ready = httpStatusOK(ctx, validatorReady, 3*time.Second)
	if api, err := fetchMap(ctx, validatorAPI+"/api/v1/status", 3*time.Second); err == nil {
		val.Running = true
		val.NodeID = stringField(api, "node_id")
		val.Role = stringField(api, "node_role")
		val.Uptime = stringField(api, "uptime")
		val.ChainTip = intField(api, "chain_tip")
		val.Peers = intField(api, "peers")
		if mining, ok := api["mining"].(map[string]any); ok {
			val.V2Active = boolField(mining, "fork_v2_active")
		}
	} else {
		val.Error = err.Error()
	}
	if len(valProcesses) > 0 {
		val.Running = true
	}

	miner := minerSnapshot{
		Service:   queryService("QSDMMiner"),
		Processes: queryProcesses([]string{"qsdmminer", "qsdmminer-console"}, ""),
		LogPath:   defaultMinerLogPath(),
	}

	gwProcesses := queryProcesses(gatewayProcessNames, s.localRoot)
	gw := gatewaySnapshot{
		Running:   len(gwProcesses) > 0,
		Slot:      defaultSlot,
		Relay:     defaultRelay,
		PublicURL: defaultRelay + "/attest/" + defaultSlot + "/api/v1/status",
		Processes: gwProcesses,
	}
	if body, code, err := fetchMapWithStatus(ctx, gw.PublicURL, 5*time.Second); err == nil {
		gw.PublicOK = code == http.StatusOK
		gw.PublicCode = code
		gw.ChainTip = intField(body, "chain_tip")
	} else {
		gw.PublicError = err.Error()
		gw.PublicCode = code
	}

	exp := exposureSnapshot{Listeners: valListeners}
	exp.Safe = exposureSafe(valListeners)
	if exp.Safe {
		exp.Summary = "validator, API, and dashboard are bound to 127.0.0.1 only"
	} else {
		exp.Summary = "one or more local services may be listening outside 127.0.0.1"
	}

	return snapshot{
		Root:      s.root,
		Admin:     adminSnapshot{Platform: runtime.GOOS, Elevated: isElevated()},
		Validator: val,
		Miner:     miner,
		Gateway:   gw,
		Exposure:  exp,
		Links: linksSnapshot{
			APIStatus:       validatorAPI + "/api/v1/status",
			Dashboard:       dashboardURL,
			PublicGateway:   gw.PublicURL,
			MinerConfigPath: filepath.Join(userHome(), ".qsdm", "miner.toml"),
		},
		CheckedAt:  time.Now().Format(time.RFC3339),
		GUIVersion: buildinfo.Short("qsdm-local-gui"),
	}
}

func (s *state) stopValidator(ctx context.Context) (string, error) {
	return stopProcessesByNameAndPath(ctx, validatorProcessNames, s.localRoot)
}

func (s *state) logPath(kind string) string {
	runDir := filepath.Join(s.localRoot, s.validatorModeConfig().runDirName())
	switch kind {
	case "validator_err":
		return filepath.Join(runDir, "stderr.autostart.log")
	case "validator_launcher":
		return filepath.Join(runDir, "launcher.log")
	case "gateway":
		return filepath.Join(s.localRoot, "home-gateway.err.log")
	case "gateway_out":
		return filepath.Join(s.localRoot, "home-gateway.out.log")
	case "gateway_err":
		return filepath.Join(s.localRoot, "home-gateway.err.log")
	case "miner":
		return defaultMinerLogPath()
	default:
		return filepath.Join(runDir, "stdout.autostart.log")
	}
}

type validatorModeConfig struct {
	Mode           string `json:"mode"`
	ChainSyncURLs  string `json:"chainSyncUrls"`
	BootstrapPeers string `json:"bootstrapPeers"`
	PublicP2P      bool   `json:"publicP2P"`
}

func (c validatorModeConfig) runDirName() string {
	if c.Mode == "networked" {
		return "run-networked"
	}
	return "run-v2"
}

func (s *state) validatorModeConfig() validatorModeConfig {
	cfg := validatorModeConfig{Mode: "solo", ChainSyncURLs: defaultRelay + "/api/v1"}
	b, err := os.ReadFile(filepath.Join(s.localRoot, "validator-mode.json"))
	if err != nil {
		return cfg
	}
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	var stored validatorModeConfig
	if json.Unmarshal(b, &stored) != nil || stored.Mode != "networked" {
		return cfg
	}
	if strings.TrimSpace(stored.ChainSyncURLs) == "" {
		stored.ChainSyncURLs = cfg.ChainSyncURLs
	}
	return stored
}

func (s *state) validatorLauncherArgs() []string {
	args := []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", s.validatorScript}
	cfg := s.validatorModeConfig()
	if cfg.Mode != "networked" {
		return args
	}
	args = append(args, "-Networked", "-ChainSyncUrls", cfg.ChainSyncURLs)
	if strings.TrimSpace(cfg.BootstrapPeers) != "" {
		args = append(args, "-BootstrapPeers", cfg.BootstrapPeers)
	}
	if cfg.PublicP2P {
		args = append(args, "-PublicP2P")
	}
	return args
}

func activeValidatorMode(localRoot string, processes []processInfo) string {
	running := make(map[int]struct{}, len(processes))
	for _, process := range processes {
		running[process.PID] = struct{}{}
	}
	for _, candidate := range []struct {
		mode string
		dir  string
	}{{"networked", "run-networked"}, {"solo", "run-v2"}} {
		b, err := os.ReadFile(filepath.Join(localRoot, candidate.dir, "qsdm.autostart.pid"))
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err == nil {
			if _, ok := running[pid]; ok {
				return candidate.mode
			}
		}
	}
	return "unknown"
}

func runCommand(parent context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), ctx.Err()
	}
	return string(out), err
}

func stopProcessesByNameAndPath(ctx context.Context, names []string, pathPrefix string) (string, error) {
	if runtime.GOOS != "windows" {
		return "", errors.New("process stop is currently implemented for Windows")
	}
	nameArgs := make([]string, 0, len(names))
	for _, n := range names {
		nameArgs = append(nameArgs, "'"+psQuote(n)+"'")
	}
	ps := "$root = '" + psQuote(filepath.Clean(pathPrefix)) + "'; " +
		"Get-Process -Name " + strings.Join(nameArgs, ",") + " -ErrorAction SilentlyContinue | " +
		"Where-Object { $_.Path -and $_.Path.StartsWith($root, [System.StringComparison]::OrdinalIgnoreCase) } | " +
		"ForEach-Object { Write-Output ('stopping ' + $_.ProcessName + ' pid=' + $_.Id); Stop-Process -Id $_.Id -Force }"
	return runCommand(ctx, 15*time.Second, "powershell.exe", "-NoProfile", "-Command", ps)
}

func queryProcesses(names []string, pathPrefix string) []processInfo {
	if runtime.GOOS != "windows" || len(names) == 0 {
		return nil
	}
	nameArgs := make([]string, 0, len(names))
	for _, n := range names {
		nameArgs = append(nameArgs, "'"+psQuote(n)+"'")
	}
	ps := "Get-Process -Name " + strings.Join(nameArgs, ",") +
		" -ErrorAction SilentlyContinue | Select-Object ProcessName,Id,Path | ConvertTo-Csv -NoTypeInformation"
	out, _ := exec.Command("powershell.exe", "-NoProfile", "-Command", ps).CombinedOutput()
	if len(out) == 0 {
		return nil
	}
	r := csv.NewReader(strings.NewReader(string(out)))
	rows, err := r.ReadAll()
	if err != nil || len(rows) < 2 {
		return nil
	}
	var outRows []processInfo
	var prefix string
	if strings.TrimSpace(pathPrefix) != "" {
		prefix = strings.ToLower(filepath.Clean(pathPrefix))
	}
	for _, row := range rows[1:] {
		if len(row) < 3 {
			continue
		}
		pid, _ := strconv.Atoi(strings.TrimSpace(row[1]))
		path := strings.TrimSpace(row[2])
		if prefix != "" && path != "" && !strings.HasPrefix(strings.ToLower(filepath.Clean(path)), prefix) {
			continue
		}
		outRows = append(outRows, processInfo{Name: row[0], PID: pid, Path: path})
	}
	return outRows
}

func queryListeners(ports []int) []listenerInfo {
	if len(ports) == 0 {
		return nil
	}
	want := map[int]struct{}{}
	for _, p := range ports {
		want[p] = struct{}{}
	}
	out, err := exec.Command("netstat", "-ano").Output()
	if err != nil {
		return nil
	}
	var listeners []listenerInfo
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || strings.ToUpper(fields[0]) != "TCP" || strings.ToUpper(fields[3]) != "LISTENING" {
			continue
		}
		addr, port, ok := splitNetstatAddr(fields[1])
		if !ok {
			continue
		}
		if _, yes := want[port]; !yes {
			continue
		}
		pid, _ := strconv.Atoi(fields[4])
		listeners = append(listeners, listenerInfo{
			Address:   addr,
			Port:      port,
			PID:       pid,
			LocalOnly: addr == "127.0.0.1" || addr == "::1" || strings.EqualFold(addr, "localhost"),
		})
	}
	return listeners
}

func splitNetstatAddr(s string) (string, int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0, false
	}
	if strings.HasPrefix(s, "[") {
		end := strings.LastIndex(s, "]:")
		if end < 0 {
			return "", 0, false
		}
		port, err := strconv.Atoi(s[end+2:])
		return strings.Trim(s[:end+1], "[]"), port, err == nil
	}
	i := strings.LastIndexByte(s, ':')
	if i < 0 {
		return "", 0, false
	}
	port, err := strconv.Atoi(s[i+1:])
	return s[:i], port, err == nil
}

func exposureSafe(listeners []listenerInfo) bool {
	if len(listeners) == 0 {
		return false
	}
	for _, l := range listeners {
		if !l.LocalOnly {
			return false
		}
	}
	return true
}

func queryService(name string) serviceStatus {
	if runtime.GOOS != "windows" {
		return serviceStatus{Supported: false, Name: name}
	}
	out, err := exec.Command("sc.exe", "query", name).CombinedOutput()
	st := serviceStatus{Supported: true, Raw: string(out), Name: name}
	if err != nil {
		return st
	}
	st.Installed = true
	for _, line := range strings.Split(string(out), "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "STATE") {
			parts := strings.Fields(l)
			if len(parts) >= 4 {
				st.State = parts[3]
			}
		}
	}
	if qc, err := exec.Command("sc.exe", "qc", name).CombinedOutput(); err == nil {
		for _, line := range strings.Split(string(qc), "\n") {
			l := strings.TrimSpace(line)
			if strings.HasPrefix(l, "BINARY_PATH_NAME") {
				if i := strings.Index(l, ":"); i > 0 {
					st.BinPath = strings.TrimSpace(l[i+1:])
				}
			}
		}
	}
	return st
}

func serviceAlreadyInState(out, code, phrase string) bool {
	low := strings.ToLower(out)
	return strings.Contains(out, "FAILED "+code) || strings.Contains(low, strings.ToLower(phrase))
}

func powershellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func fetchMap(ctx context.Context, url string, timeout time.Duration) (map[string]any, error) {
	body, code, err := fetchMapWithStatus(ctx, url, timeout)
	if err != nil {
		return nil, err
	}
	if code < 200 || code > 299 {
		return nil, fmt.Errorf("HTTP %d", code)
	}
	return body, nil
}

func fetchMapWithStatus(ctx context.Context, url string, timeout time.Duration) (map[string]any, int, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&m)
	return m, resp.StatusCode, nil
}

func httpStatusOK(ctx context.Context, url string, timeout time.Duration) bool {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func stringField(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
}

func boolField(m map[string]any, k string) bool {
	v, _ := m[k].(bool)
	return v
}

func intField(m map[string]any, k string) int64 {
	switch v := m[k].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case json.Number:
		n, _ := v.Int64()
		return n
	default:
		return 0
	}
}

func tailFile(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := st.Size()
	const chunk int64 = 4096
	var (
		buf   []byte
		lines []string
		off   = size
	)
	for off > 0 && len(lines) <= n {
		read := chunk
		if off < read {
			read = off
		}
		off -= read
		piece := make([]byte, read)
		if _, err := f.ReadAt(piece, off); err != nil && err != io.EOF {
			return nil, err
		}
		buf = append(piece, buf...)
		lines = strings.Split(string(buf), "\n")
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, nil
}

func defaultMinerLogPath() string {
	return filepath.Join(userHome(), ".qsdm", "miner.log")
}

func userHome() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "."
	}
	return home
}

func findRoot() string {
	if v := envOrFlag("QSDM_LOCAL_GUI_ROOT", "--root", ""); v != "" {
		if abs, err := filepath.Abs(v); err == nil {
			return abs
		}
		return v
	}
	var starts []string
	if wd, err := os.Getwd(); err == nil {
		starts = append(starts, wd)
	}
	if exe, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(exe))
	}
	for _, start := range starts {
		dir, _ := filepath.Abs(start)
		for i := 0; i < 10; i++ {
			if fileExists(filepath.Join(dir, "qsdm.yaml")) &&
				fileExists(filepath.Join(dir, "scripts", "start_local_validator.ps1")) {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "."
}

func envOrFlag(envName, flagName, def string) string {
	if v := os.Getenv(envName); v != "" {
		return v
	}
	for i, a := range os.Args {
		if a == flagName && i+1 < len(os.Args) {
			return os.Args[i+1]
		}
		if strings.HasPrefix(a, flagName+"=") {
			return strings.TrimPrefix(a, flagName+"=")
		}
	}
	return def
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func mustToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func writeAction(w http.ResponseWriter, out string, err error) {
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, actionResult{OK: false, Output: out, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, actionResult{OK: true, Output: out})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func openInBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

func psQuote(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
