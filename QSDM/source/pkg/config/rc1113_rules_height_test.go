package config

// The rc1113 rules height (chain.SetRC1113RulesHeight) comes from [consensus] rc1113_rules_activation_height or
// QSDM_RC1113_RULES_ACTIVATION_HEIGHT; unset (or 0) is the compiled release default 1,230,000 (owner, 2026-11-15).

import (
	"os"
	"path/filepath"
	"testing"
)

func rc1113WriteConfig(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "qsdm.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_FILE", path)
}

func TestRC1113RulesActivationHeight_ConfigKey(t *testing.T) {
	rc1113WriteConfig(t, "[consensus]\nrc1113_rules_activation_height = 1235000\n")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.RC1113RulesActivationHeight != 1235000 {
		t.Fatalf("config key not read: got %d", cfg.RC1113RulesActivationHeight)
	}
}

func TestRC1113RulesActivationHeight_DefaultAndEnv(t *testing.T) {
	if RC1113RulesDefaultActivationHeight != 1230000 {
		t.Fatalf("release default is %d, want 1230000", RC1113RulesDefaultActivationHeight)
	}
	rc1113WriteConfig(t, "[consensus]\n")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.RC1113RulesActivationHeight != 1230000 {
		t.Fatalf("unset key: got %d, want the release default 1230000", cfg.RC1113RulesActivationHeight)
	}
	rc1113WriteConfig(t, "[consensus]\nrc1113_rules_activation_height = 0\n")
	if cfg, err = LoadConfig(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.RC1113RulesActivationHeight != 1230000 {
		t.Fatalf("explicit 0: got %d, want the release default 1230000 (a node cannot switch the rules off)", cfg.RC1113RulesActivationHeight)
	}
	t.Setenv("QSDM_RC1113_RULES_ACTIVATION_HEIGHT", "1240000")
	if cfg, err = LoadConfig(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.RC1113RulesActivationHeight != 1240000 {
		t.Fatalf("env not applied: got %d", cfg.RC1113RulesActivationHeight)
	}
}

func TestRC1113RulesActivationHeight_LoadConfigFileDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "qsdm.toml")
	if err := os.WriteFile(path, []byte("[consensus]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.RC1113RulesActivationHeight != 1230000 {
		t.Fatalf("LoadConfigFile: got %d, want 1230000", cfg.RC1113RulesActivationHeight)
	}
}
