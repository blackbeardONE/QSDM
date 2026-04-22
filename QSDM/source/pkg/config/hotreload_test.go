package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func writeTOMLConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestHotReloader_CreateAndCurrent(t *testing.T) {
	dir := t.TempDir()
	path := writeTOMLConfig(t, dir, `[network]
port = 9000
`)
	initial := &Config{NetworkPort: 9000, ConfigFileUsed: path}
	hr, err := NewHotReloader(HotReloadConfig{FilePath: path, PollInterval: 50 * time.Millisecond}, initial)
	if err != nil {
		t.Fatalf("NewHotReloader: %v", err)
	}
	if hr.Current().NetworkPort != 9000 {
		t.Fatal("expected port 9000")
	}
}

func TestHotReloader_DetectsChange(t *testing.T) {
	dir := t.TempDir()
	path := writeTOMLConfig(t, dir, `[network]
port = 9000
`)
	initial := &Config{NetworkPort: 9000}
	hr, _ := NewHotReloader(HotReloadConfig{FilePath: path, PollInterval: 50 * time.Millisecond}, initial)

	// No change yet
	changed, _ := hr.CheckAndReload()
	if changed {
		t.Fatal("no change expected on first check")
	}

	// Modify file
	time.Sleep(10 * time.Millisecond)
	writeTOMLConfig(t, dir, `[network]
port = 9999
`)

	changed, err := hr.CheckAndReload()
	if err != nil {
		t.Fatalf("CheckAndReload: %v", err)
	}
	if !changed {
		t.Fatal("expected change after file modification")
	}
	if hr.Current().NetworkPort != 9999 {
		t.Fatalf("expected port 9999, got %d", hr.Current().NetworkPort)
	}
	if hr.ReloadCount() != 1 {
		t.Fatalf("expected 1 reload, got %d", hr.ReloadCount())
	}
}

func TestHotReloader_Callbacks(t *testing.T) {
	dir := t.TempDir()
	path := writeTOMLConfig(t, dir, `[network]
port = 8000
`)
	initial := &Config{NetworkPort: 8000}
	hr, _ := NewHotReloader(HotReloadConfig{FilePath: path, PollInterval: 50 * time.Millisecond}, initial)

	var mu sync.Mutex
	var callbackPorts []int
	hr.OnReload(func(cfg *Config) {
		mu.Lock()
		callbackPorts = append(callbackPorts, cfg.NetworkPort)
		mu.Unlock()
	})

	time.Sleep(10 * time.Millisecond)
	writeTOMLConfig(t, dir, `[network]
port = 8888
`)

	hr.CheckAndReload()

	mu.Lock()
	if len(callbackPorts) != 1 || callbackPorts[0] != 8888 {
		t.Fatalf("expected callback with port 8888, got %v", callbackPorts)
	}
	mu.Unlock()
}

func TestHotReloader_NoChangeNoReload(t *testing.T) {
	dir := t.TempDir()
	path := writeTOMLConfig(t, dir, `[network]
port = 5000
`)
	initial := &Config{NetworkPort: 5000}
	hr, _ := NewHotReloader(HotReloadConfig{FilePath: path, PollInterval: 50 * time.Millisecond}, initial)

	for i := 0; i < 5; i++ {
		changed, _ := hr.CheckAndReload()
		if changed {
			t.Fatal("expected no change")
		}
	}

	if hr.ReloadCount() != 0 {
		t.Fatalf("expected 0 reloads, got %d", hr.ReloadCount())
	}
}

func TestHotReloader_StartStop(t *testing.T) {
	dir := t.TempDir()
	path := writeTOMLConfig(t, dir, `[network]
port = 7000
`)
	initial := &Config{NetworkPort: 7000}
	hr, _ := NewHotReloader(HotReloadConfig{FilePath: path, PollInterval: 20 * time.Millisecond}, initial)

	hr.Start()
	time.Sleep(50 * time.Millisecond) // let it poll once

	writeTOMLConfig(t, dir, `[network]
port = 7777
`)

	time.Sleep(100 * time.Millisecond)
	hr.Stop()

	if hr.ReloadCount() < 1 {
		t.Fatal("expected at least 1 background reload")
	}
	if hr.Current().NetworkPort != 7777 {
		t.Fatalf("expected 7777 after background reload, got %d", hr.Current().NetworkPort)
	}
}

func TestHotReloader_NoFilePath(t *testing.T) {
	_, err := NewHotReloader(HotReloadConfig{}, nil)
	if err == nil {
		t.Fatal("expected error for no file path")
	}
}

func TestHotReloader_BadFileContent(t *testing.T) {
	dir := t.TempDir()
	path := writeTOMLConfig(t, dir, `[network]
port = 5000
`)
	initial := &Config{NetworkPort: 5000}
	hr, _ := NewHotReloader(HotReloadConfig{FilePath: path, PollInterval: 50 * time.Millisecond}, initial)

	// Corrupt the file
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("not valid toml { [ broken"), 0644)

	_, err := hr.CheckAndReload()
	if err == nil {
		t.Fatal("expected error for corrupt config file")
	}
	// Original config should be preserved
	if hr.Current().NetworkPort != 5000 {
		t.Fatal("config should not change on failed reload")
	}
}

func TestHotReloader_UsesConfigFileUsed(t *testing.T) {
	dir := t.TempDir()
	path := writeTOMLConfig(t, dir, `[network]
port = 6000
`)
	initial := &Config{NetworkPort: 6000, ConfigFileUsed: path}
	hr, err := NewHotReloader(HotReloadConfig{PollInterval: 50 * time.Millisecond}, initial)
	if err != nil {
		t.Fatalf("expected to pick up path from ConfigFileUsed: %v", err)
	}
	if hr.Current().NetworkPort != 6000 {
		t.Fatal("expected 6000")
	}
}

func TestHotReloaderPolicy_DenylistBlocksChange(t *testing.T) {
	oldCfg := &Config{NetworkPort: 9000}
	newCfg := &Config{NetworkPort: 9001}
	changed := changedTopLevelKeys(oldCfg, newCfg)
	if len(changed) == 0 {
		t.Fatal("expected changed key")
	}
	err := validateReloadPolicy(oldCfg, newCfg, ReloadPolicy{
		Denylist: []string{changed[0]},
	})
	if err == nil {
		t.Fatal("expected denylist block")
	}
}

func TestHotReloaderPolicy_RequireRestartBlocksChange(t *testing.T) {
	oldCfg := &Config{NetworkPort: 7000}
	newCfg := &Config{NetworkPort: 7001}
	changed := changedTopLevelKeys(oldCfg, newCfg)
	err := validateReloadPolicy(oldCfg, newCfg, ReloadPolicy{
		RequireRestart: []string{changed[0]},
	})
	if err == nil {
		t.Fatal("expected require-restart block")
	}
}

func TestHotReloaderPolicy_AllowlistStrict(t *testing.T) {
	oldCfg := &Config{NetworkPort: 7000}
	newCfg := &Config{NetworkPort: 7001}
	err := validateReloadPolicy(oldCfg, newCfg, ReloadPolicy{
		Allowlist: []string{"some_other_key"},
		Strict:    true,
	})
	if err == nil {
		t.Fatal("expected strict allowlist block")
	}
}

func TestHotReloaderPolicy_AllowlistNonStrictPermits(t *testing.T) {
	oldCfg := &Config{NetworkPort: 7000}
	newCfg := &Config{NetworkPort: 7001}
	err := validateReloadPolicy(oldCfg, newCfg, ReloadPolicy{
		Allowlist: []string{"some_other_key"},
		Strict:    false,
	})
	if err != nil {
		t.Fatalf("expected non-strict allowlist to permit, got %v", err)
	}
}

func TestHotReloader_DryRunReload_NoMutation(t *testing.T) {
	dir := t.TempDir()
	path := writeTOMLConfig(t, dir, `[network]
port = 4000
`)
	initial := &Config{NetworkPort: 4000}
	hr, _ := NewHotReloader(HotReloadConfig{FilePath: path, PollInterval: 50 * time.Millisecond}, initial)

	changed, keys, polErr, loadErr := hr.DryRunReload()
	if changed || len(keys) != 0 || polErr != nil || loadErr != nil {
		t.Fatalf("unexpected dry-run on unchanged file: ch=%v keys=%v pol=%v load=%v", changed, keys, polErr, loadErr)
	}
	if hr.ReloadCount() != 0 {
		t.Fatal("dry-run must not bump reload count")
	}

	time.Sleep(10 * time.Millisecond)
	writeTOMLConfig(t, dir, `[network]
port = 4001
`)
	changed, keys, polErr, loadErr = hr.DryRunReload()
	if !changed || loadErr != nil {
		t.Fatalf("expected file changed and load ok, got ch=%v load=%v", changed, loadErr)
	}
	if len(keys) == 0 {
		t.Fatal("expected changed keys")
	}
	if polErr != nil {
		t.Fatalf("unexpected policy err: %v", polErr)
	}
	if hr.Current().NetworkPort != 4000 {
		t.Fatal("dry-run must not apply new port")
	}
	if hr.ReloadCount() != 0 {
		t.Fatal("reload count still 0 after dry-run only")
	}
	info := hr.LastDryRunInfo()
	if info["last_dry_run_at"].(time.Time).IsZero() {
		t.Fatal("expected LastDryRunInfo to record dry-run time")
	}
	if info["last_file_changed"] != true || info["last_load_ok"] != true || info["last_policy_ok"] != true {
		t.Fatalf("unexpected LastDryRunInfo: %#v", info)
	}
}
