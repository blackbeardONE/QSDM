package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The Proof-of-Entanglement parent rules are a separate, later activation
// from signed consensus. The default must stay zero: a non-zero default would
// reject every current wallet transfer (they carry no parents) the moment
// this binary shipped.
func TestPoEActivationHeight_DefaultTOMLYAMLAndEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qsdm.toml")
	if err := os.WriteFile(path, []byte("[consensus]\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CONFIG_FILE", path)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.PoEActivationHeight != 0 {
		t.Fatalf("default must be 0 (never), got %d", cfg.PoEActivationHeight)
	}

	if err := os.WriteFile(path, []byte("[consensus]\npoe_activation_height = 1000000\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if cfg, err = LoadConfig(); err != nil || cfg.PoEActivationHeight != 1_000_000 {
		t.Fatalf("toml key not read: %d %v", cfg.PoEActivationHeight, err)
	}

	yamlPath := filepath.Join(dir, "qsdm.yaml")
	if err := os.WriteFile(yamlPath, []byte("consensus:\n  poe_activation_height: 1000001\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CONFIG_FILE", yamlPath)
	if cfg, err = LoadConfig(); err != nil || cfg.PoEActivationHeight != 1_000_001 {
		t.Fatalf("yaml key not read: %d %v", cfg.PoEActivationHeight, err)
	}

	t.Setenv("QSDM_POE_ACTIVATION_HEIGHT", "1000002")
	if cfg, err = LoadConfig(); err != nil || cfg.PoEActivationHeight != 1_000_002 {
		t.Fatalf("env override not applied: %d %v", cfg.PoEActivationHeight, err)
	}
}
