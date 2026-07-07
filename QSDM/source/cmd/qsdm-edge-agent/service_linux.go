//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/edgepool"
)

const edgeAgentServiceName = "qsdm-edge-agent.service"
const edgeRelayServiceName = "qsdm-edge-relay.service"
const edgeCoordinatorServiceName = "qsdm-edge-coordinator.service"

func installAgentService(sourceConfigPath string) (string, error) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return "", errors.New("systemd is required to install the persistent Linux agent service")
	}
	config := agentFileConfig{}
	if err := readStrictJSON(sourceConfigPath, &config); err != nil {
		return "", fmt.Errorf("read agent configuration: %w", err)
	}
	if agentRelayURL(config) == "" || strings.TrimSpace(config.TokenFile) == "" {
		return "", errors.New("agent configuration must contain relay and token_file")
	}
	resources, err := parseResources(config.Resources)
	if err != nil {
		return "", err
	}
	if config.WorkerID == "" {
		config.WorkerID = defaultWorkerID()
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find user home: %w", err)
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user configuration directory: %w", err)
	}
	stateDir := filepath.Join(configDir, "QSDM", "edge-pool")
	binDir := filepath.Join(homeDir, ".local", "bin")
	libDir := filepath.Join(homeDir, ".local", "lib", "qsdm")
	unitDir := filepath.Join(configDir, "systemd", "user")
	for _, directory := range []string{stateDir, binDir, libDir, unitDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return "", fmt.Errorf("create %s: %w", directory, err)
		}
	}

	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find agent executable: %w", err)
	}
	installedExecutable := filepath.Join(binDir, "qsdm-edge-agent")
	if err := copyFileAtomic(executable, installedExecutable, 0o755); err != nil {
		return "", fmt.Errorf("install agent executable: %w", err)
	}

	token, err := edgepool.LoadTokenFile(config.TokenFile)
	if err != nil {
		return "", err
	}
	installedToken := filepath.Join(stateDir, "edge-pool.token")
	if err := installToken(installedToken, token); err != nil {
		return "", err
	}
	config.TokenFile = installedToken

	if containsAgentResource(resources, edgepool.ResourceGPU) {
		if strings.TrimSpace(config.GPUHelper) == "" {
			return "", errors.New("GPU resource is enabled but gpu_helper is not configured")
		}
		installedHelper := filepath.Join(libDir, "qsdm-edge-gpu-helper")
		if err := copyFileAtomic(config.GPUHelper, installedHelper, 0o755); err != nil {
			return "", fmt.Errorf("install GPU helper: %w", err)
		}
		config.GPUHelper = installedHelper
	}
	if config.LogFile == "" || !filepath.IsAbs(config.LogFile) {
		config.LogFile = filepath.Join(configDir, "QSDM", "edge-agent.log")
	}
	installedConfig := filepath.Join(stateDir, "agent.json")
	if err := writeJSONAtomic(installedConfig, config, 0o600); err != nil {
		return "", fmt.Errorf("install agent configuration: %w", err)
	}

	unit := fmt.Sprintf(`[Unit]
Description=QSDM outbound-only pooled edge worker
Documentation=https://qsdm.tech/docs/#/edge-pool
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s agent --config %s --silent
Restart=always
RestartSec=5s
TimeoutStopSec=30s
KillSignal=SIGTERM
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
RestrictRealtime=true
LockPersonality=true
SystemCallArchitectures=native
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
UMask=0077

[Install]
WantedBy=default.target
`, systemdQuote(installedExecutable), systemdQuote(installedConfig))
	unitPath := filepath.Join(unitDir, edgeAgentServiceName)
	if err := writeBytesAtomic(unitPath, []byte(unit), 0o600); err != nil {
		return "", fmt.Errorf("install systemd user unit: %w", err)
	}
	if output, err := runSystemctl("daemon-reload"); err != nil {
		return "", fmt.Errorf("systemd user daemon-reload failed: %s: %w", output, err)
	}
	if output, err := runSystemctl("enable", "--now", edgeAgentServiceName); err != nil {
		return "", fmt.Errorf("systemd could not enable the agent service: %s: %w", output, err)
	}
	return fmt.Sprintf("QSDM edge agent service installed and started. Config: %s", installedConfig), nil
}

func uninstallAgentService() error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	_, _ = runSystemctl("disable", "--now", edgeAgentServiceName)
	unitPath := filepath.Join(configDir, "systemd", "user", edgeAgentServiceName)
	if err := os.Remove(unitPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if output, err := runSystemctl("daemon-reload"); err != nil {
		return fmt.Errorf("systemd user daemon-reload failed: %s: %w", output, err)
	}
	fmt.Println("QSDM edge agent service removed. Configuration and token were retained.")
	return nil
}

func installCoordinatorService(options coordinatorServiceOptions) (string, error) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return "", errors.New("systemd is required to install the persistent Linux relay service")
	}
	if options.MaxVerifications <= 0 || options.MaxVerifications > 16 {
		return "", errors.New("max-verifications must be between 1 and 16")
	}
	for name, value := range map[string]int{
		"cpu-percent": options.CPUPercent,
		"gpu-percent": options.GPUPercent,
		"ram-percent": options.RAMPercent,
	} {
		if value < 1 || value > 100 {
			return "", fmt.Errorf("%s must be between 1 and 100", name)
		}
	}
	agentToken, err := edgepool.LoadTokenFile(options.AgentTokenFile)
	if err != nil {
		return "", err
	}
	motherToken, err := edgepool.LoadTokenFile(options.MotherTokenFile)
	if err != nil {
		return "", err
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find user home: %w", err)
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user configuration directory: %w", err)
	}
	stateRoot := filepath.Join(configDir, "QSDM", "edge-pool")
	relayState := filepath.Join(stateRoot, "coordinator")
	binDir := filepath.Join(homeDir, ".local", "bin")
	unitDir := filepath.Join(configDir, "systemd", "user")
	for _, directory := range []string{stateRoot, relayState, binDir, unitDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return "", fmt.Errorf("create %s: %w", directory, err)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find relay executable: %w", err)
	}
	installedExecutable := filepath.Join(binDir, "qsdm-edge-agent")
	if err := copyFileAtomic(executable, installedExecutable, 0o755); err != nil {
		return "", fmt.Errorf("install relay executable: %w", err)
	}
	installedAgentToken := filepath.Join(stateRoot, "agent.token")
	if err := installToken(installedAgentToken, agentToken); err != nil {
		return "", err
	}
	installedMotherToken := filepath.Join(stateRoot, "mother-hive.token")
	if err := installToken(installedMotherToken, motherToken); err != nil {
		return "", err
	}

	arguments := []string{
		systemdQuote(installedExecutable),
		"relay",
		"--listen", systemdQuote(options.Listen),
		"--agent-token-file", systemdQuote(installedAgentToken),
		"--mother-token-file", systemdQuote(installedMotherToken),
		"--state-dir", systemdQuote(relayState),
		"--cpu-units", fmt.Sprint(options.CPUUnits),
		"--gpu-units", fmt.Sprint(options.GPUUnits),
		"--ram-mib", fmt.Sprint(options.RAMMiB),
		"--cpu-percent", fmt.Sprint(options.CPUPercent),
		"--gpu-percent", fmt.Sprint(options.GPUPercent),
		"--ram-percent", fmt.Sprint(options.RAMPercent),
		"--max-verifications", fmt.Sprint(options.MaxVerifications),
	}
	if options.ID != "" {
		arguments = append(arguments, "--id", systemdQuote(options.ID))
	}
	if options.AllowLAN {
		arguments = append(arguments, "--allow-lan")
	}
	unit := fmt.Sprintf(`[Unit]
Description=QSDM Agent resource relay for QSDM Hive
Documentation=https://qsdm.tech/docs/#/edge-pool
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s
Restart=always
RestartSec=5s
TimeoutStopSec=30s
KillSignal=SIGTERM
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
RestrictRealtime=true
LockPersonality=true
SystemCallArchitectures=native
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
UMask=0077

[Install]
WantedBy=default.target
`, strings.Join(arguments, " "))
	unitPath := filepath.Join(unitDir, edgeRelayServiceName)
	if err := writeBytesAtomic(unitPath, []byte(unit), 0o600); err != nil {
		return "", fmt.Errorf("install systemd relay unit: %w", err)
	}
	_, _ = runSystemctl("disable", "--now", edgeCoordinatorServiceName)
	if output, err := runSystemctl("daemon-reload"); err != nil {
		return "", fmt.Errorf("systemd user daemon-reload failed: %s: %w", output, err)
	}
	if output, err := runSystemctl("enable", "--now", edgeRelayServiceName); err != nil {
		return "", fmt.Errorf("systemd could not enable the relay service: %s: %w", output, err)
	}
	return fmt.Sprintf("QSDM edge relay service installed and started on %s. State: %s", options.Listen, relayState), nil
}

func uninstallCoordinatorService() error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	for _, serviceName := range []string{edgeRelayServiceName, edgeCoordinatorServiceName} {
		_, _ = runSystemctl("disable", "--now", serviceName)
		unitPath := filepath.Join(configDir, "systemd", "user", serviceName)
		if err := os.Remove(unitPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if output, err := runSystemctl("daemon-reload"); err != nil {
		return fmt.Errorf("systemd user daemon-reload failed: %s: %w", output, err)
	}
	fmt.Println("QSDM edge relay service removed. State and tokens were retained.")
	return nil
}

func showCoordinatorServiceStatus() error {
	command := exec.Command("systemctl", "--user", "--no-pager", "--full", "status", edgeRelayServiceName)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}

func showAgentServiceStatus() error {
	command := exec.Command("systemctl", "--user", "--no-pager", "--full", "status", edgeAgentServiceName)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}

func runSystemctl(args ...string) (string, error) {
	commandArgs := append([]string{"--user"}, args...)
	output, err := exec.Command("systemctl", commandArgs...).CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

func copyFileAtomic(source, destination string, mode os.FileMode) error {
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return err
	}
	if sourceInfo.IsDir() {
		return errors.New("source is a directory")
	}
	if destinationInfo, statErr := os.Stat(destination); statErr == nil && os.SameFile(sourceInfo, destinationInfo) {
		return os.Chmod(destination, mode)
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".qsdm-install-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := io.Copy(temporary, input); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, destination)
}

func installToken(path string, token []byte) error {
	if existing, err := edgepool.LoadTokenFile(path); err == nil {
		if !bytes.Equal(existing, token) {
			return fmt.Errorf("refusing to replace the installed edge-pool token at %s with a different token", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) && !strings.Contains(err.Error(), "no such file") {
		return fmt.Errorf("inspect installed edge-pool token: %w", err)
	}
	if err := edgepool.WriteTokenFile(path, token); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("secure installed token: %w", err)
	}
	return nil
}

func writeJSONAtomic(path string, value any, mode os.FileMode) error {
	raw, err := jsonMarshalIndent(value)
	if err != nil {
		return err
	}
	return writeBytesAtomic(path, raw, mode)
}

func writeBytesAtomic(path string, raw []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".qsdm-write-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(raw); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func jsonMarshalIndent(value any) ([]byte, error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func containsAgentResource(resources []edgepool.ResourceKind, target edgepool.ResourceKind) bool {
	for _, resource := range resources {
		if resource == target {
			return true
		}
	}
	return false
}

func systemdQuote(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	value = strings.ReplaceAll(value, "%", "%%")
	return "\"" + value + "\""
}
