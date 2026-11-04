package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The stream Ed25519 session-key retirement is a consensus rule with its own
// height. The default must stay zero so shipping the binary changes nothing.
func TestStreamEd25519RetirementHeight_DefaultTOMLYAMLAndEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qsdm.toml")
	if err := os.WriteFile(path, []byte("[consensus]\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CONFIG_FILE", path)
	t.Setenv("QSDM_STREAM_ED25519_RETIREMENT_HEIGHT", "")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.StreamEd25519RetirementHeight != 0 {
		t.Fatalf("default must be 0 (never), got %d", cfg.StreamEd25519RetirementHeight)
	}

	if err := os.WriteFile(path, []byte("[consensus]\nstream_ed25519_retirement_height = 900000\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if cfg, err = LoadConfig(); err != nil || cfg.StreamEd25519RetirementHeight != 900_000 {
		t.Fatalf("toml key not read: %d %v", cfg.StreamEd25519RetirementHeight, err)
	}

	yamlPath := filepath.Join(dir, "qsdm.yaml")
	if err := os.WriteFile(yamlPath, []byte("consensus:\n  stream_ed25519_retirement_height: 900001\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CONFIG_FILE", yamlPath)
	if cfg, err = LoadConfig(); err != nil || cfg.StreamEd25519RetirementHeight != 900_001 {
		t.Fatalf("yaml key not read: %d %v", cfg.StreamEd25519RetirementHeight, err)
	}

	t.Setenv("QSDM_STREAM_ED25519_RETIREMENT_HEIGHT", "900002")
	if cfg, err = LoadConfig(); err != nil || cfg.StreamEd25519RetirementHeight != 900_002 {
		t.Fatalf("env override not applied: %d %v", cfg.StreamEd25519RetirementHeight, err)
	}
}
