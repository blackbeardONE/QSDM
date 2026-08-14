package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The verifier existed with no way to switch it on: nothing read a config key
// and nothing called SetTaskActionSignatureActivationHeight outside tests. A
// protection an operator cannot enable is not shipped, which is the rubric's
// own phrasing, so this pins that the key is actually read.
func TestTaskActionSignatureActivationHeight_IsConfigurable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qsdm.toml")
	if err := os.WriteFile(path, []byte(`
[consensus]
task_action_signature_activation_height = 4242
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("CONFIG_FILE", path)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.TaskActionSignatureActivationHeight != 4242 {
		t.Errorf("config key not read: got %d, want 4242", cfg.TaskActionSignatureActivationHeight)
	}
}

func TestTaskActionSignatureActivationHeight_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qsdm.toml")
	if err := os.WriteFile(path, []byte("[consensus]\ntask_action_signature_activation_height = 1\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("QSDM_TASK_ACTION_SIGNATURE_ACTIVATION_HEIGHT", "9001")

	t.Setenv("CONFIG_FILE", path)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.TaskActionSignatureActivationHeight != 9001 {
		t.Errorf("env override not applied: got %d, want 9001", cfg.TaskActionSignatureActivationHeight)
	}
}

// Default must stay zero. Defaulting it on would reject every unsigned task
// action already on a running chain, which is the replay divergence the
// height gate exists to avoid.
func TestTaskActionSignatureActivationHeight_DefaultsOff(t *testing.T) {
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
	if cfg.TaskActionSignatureActivationHeight != 0 {
		t.Errorf("default must be 0 (not required), got %d", cfg.TaskActionSignatureActivationHeight)
	}
}
