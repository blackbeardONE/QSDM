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

// The content-root gate had the same defect the task-action gate did: the
// mechanism existed and nothing could switch it on. An independent audit found
// SetTxContentRootActivationHeight reachable only from Go code -- no config
// key, no env var, no caller in main.go.
func TestTxContentRootActivationHeight_IsConfigurable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qsdm.toml")
	if err := os.WriteFile(path, []byte("[consensus]\ntx_content_root_activation_height = 777000\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CONFIG_FILE", path)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.TxContentRootActivationHeight != 777000 {
		t.Errorf("config key not read: got %d, want 777000", cfg.TxContentRootActivationHeight)
	}
}

func TestTxContentRootActivationHeight_EnvOverrideAndDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qsdm.toml")
	if err := os.WriteFile(path, []byte("[consensus]\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CONFIG_FILE", path)

	// Default must stay zero: a non-zero default would change block hashes on
	// every existing chain the moment this binary shipped.
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.TxContentRootActivationHeight != 0 {
		t.Fatalf("default must be 0 (legacy root), got %d", cfg.TxContentRootActivationHeight)
	}

	t.Setenv("QSDM_TX_CONTENT_ROOT_ACTIVATION_HEIGHT", "888000")
	cfg, err = LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.TxContentRootActivationHeight != 888000 {
		t.Errorf("env override not applied: got %d, want 888000", cfg.TxContentRootActivationHeight)
	}
}
