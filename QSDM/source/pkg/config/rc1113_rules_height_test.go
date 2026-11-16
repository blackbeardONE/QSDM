package config

// The rc1113 rules height (chain.SetRC1113RulesHeight) comes from [consensus] rc1113_rules_activation_height or
// QSDM_RC1113_RULES_ACTIVATION_HEIGHT; unset is 0 = never. The planned value is 1,230,000 (owner, 2026-11-15).

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRC1113RulesActivationHeight_ConfigKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "qsdm.toml")
	if err := os.WriteFile(path, []byte("[consensus]\nrc1113_rules_activation_height = 1230000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_FILE", path)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.RC1113RulesActivationHeight != 1230000 {
		t.Fatalf("config key not read: got %d", cfg.RC1113RulesActivationHeight)
	}
}

func TestRC1113RulesActivationHeight_DefaultNeverAndEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "qsdm.toml")
	if err := os.WriteFile(path, []byte("[consensus]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_FILE", path)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.RC1113RulesActivationHeight != 0 {
		t.Fatalf("default must be 0 (never), got %d", cfg.RC1113RulesActivationHeight)
	}
	t.Setenv("QSDM_RC1113_RULES_ACTIVATION_HEIGHT", "1230000")
	if cfg, err = LoadConfig(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.RC1113RulesActivationHeight != 1230000 {
		t.Fatalf("env not applied: got %d", cfg.RC1113RulesActivationHeight)
	}
}
