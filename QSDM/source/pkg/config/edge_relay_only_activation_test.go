package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The HL2 edge relay-only rule is a consensus rule with its own activation.
// The default must stay zero so shipping the binary changes nothing.
func TestEdgeRelayOnlyActivationHeight_DefaultTOMLYAMLAndEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qsdm.toml")
	if err := os.WriteFile(path, []byte("[consensus]\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CONFIG_FILE", path)
	t.Setenv("QSDM_EDGE_RELAY_ONLY_ACTIVATION_HEIGHT", "")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.EdgeRelayOnlyActivationHeight != 0 {
		t.Fatalf("default must be 0 (never), got %d", cfg.EdgeRelayOnlyActivationHeight)
	}

	if err := os.WriteFile(path, []byte("[consensus]\nedge_relay_only_activation_height = 900000\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if cfg, err = LoadConfig(); err != nil || cfg.EdgeRelayOnlyActivationHeight != 900_000 || cfg.PoEActivationHeight != 0 {
		t.Fatalf("toml key not read: %d %v", cfg.EdgeRelayOnlyActivationHeight, err)
	}

	yamlPath := filepath.Join(dir, "qsdm.yaml")
	if err := os.WriteFile(yamlPath, []byte("consensus:\n  edge_relay_only_activation_height: 900001\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CONFIG_FILE", yamlPath)
	if cfg, err = LoadConfig(); err != nil || cfg.EdgeRelayOnlyActivationHeight != 900_001 {
		t.Fatalf("yaml key not read: %d %v", cfg.EdgeRelayOnlyActivationHeight, err)
	}

	t.Setenv("QSDM_EDGE_RELAY_ONLY_ACTIVATION_HEIGHT", "900002")
	if cfg, err = LoadConfig(); err != nil || cfg.EdgeRelayOnlyActivationHeight != 900_002 {
		t.Fatalf("env override not applied: %d %v", cfg.EdgeRelayOnlyActivationHeight, err)
	}
}
