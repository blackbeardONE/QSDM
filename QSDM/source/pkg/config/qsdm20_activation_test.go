package config

import (
	"os"
	"path/filepath"
	"testing"
)

// QSDM-20 tokens are a consensus rule with their own activation height. The
// default must stay zero so shipping the binary changes nothing.
func TestQSDM20ActivationHeight_DefaultTOMLYAMLAndEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qsdm.toml")
	if err := os.WriteFile(path, []byte("[consensus]\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CONFIG_FILE", path)
	t.Setenv("QSDM_QSDM20_ACTIVATION_HEIGHT", "")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.QSDM20ActivationHeight != 0 {
		t.Fatalf("default must be 0 (off), got %d", cfg.QSDM20ActivationHeight)
	}

	if err := os.WriteFile(path, []byte("[consensus]\nqsdm20_activation_height = 910000\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if cfg, err = LoadConfig(); err != nil || cfg.QSDM20ActivationHeight != 910_000 {
		t.Fatalf("toml key not read: %d %v", cfg.QSDM20ActivationHeight, err)
	}

	yamlPath := filepath.Join(dir, "qsdm.yaml")
	if err := os.WriteFile(yamlPath, []byte("consensus:\n  qsdm20_activation_height: 910001\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CONFIG_FILE", yamlPath)
	if cfg, err = LoadConfig(); err != nil || cfg.QSDM20ActivationHeight != 910_001 {
		t.Fatalf("yaml key not read: %d %v", cfg.QSDM20ActivationHeight, err)
	}

	t.Setenv("QSDM_QSDM20_ACTIVATION_HEIGHT", "910002")
	if cfg, err = LoadConfig(); err != nil || cfg.QSDM20ActivationHeight != 910_002 {
		t.Fatalf("env override not applied: %d %v", cfg.QSDM20ActivationHeight, err)
	}
}
