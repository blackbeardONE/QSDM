package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidate_StrictSecrets_rejectsShort(t *testing.T) {
	c := &Config{
		NetworkPort:             4001,
		DashboardPort:           8081,
		LogViewerPort:           9000,
		APIPort:                 8080,
		StorageType:             "file",
		StrictProductionSecrets: true,
		NGCIngestSecret:         "short",
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for short NGC ingest secret")
	}
}

func TestValidate_StrictSecrets_rejectsCharming123Prefix(t *testing.T) {
	c := &Config{
		NetworkPort:             4001,
		DashboardPort:           8081,
		LogViewerPort:           9000,
		APIPort:                 8080,
		StorageType:             "file",
		StrictProductionSecrets: true,
		NGCIngestSecret:         "Charming1234567890", // 18 chars, demo prefix
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for charming123-prefixed secret")
	}
}

func TestValidate_StrictSecrets_okLongRandom(t *testing.T) {
	c := &Config{
		NetworkPort:             4001,
		DashboardPort:           8081,
		LogViewerPort:           9000,
		APIPort:                 8080,
		StorageType:             "file",
		StrictProductionSecrets: true,
		NGCIngestSecret:         "not-the-demo-value-ok-16",
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadConfigFile_TOML_StrictSecrets(t *testing.T) {
	p := filepath.Join(t.TempDir(), "node.toml")
	content := `
[network]
port = 4001

[api]
port = 8080
strict_secrets = true
`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{}
	if err := loadConfigFile(p, cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.StrictProductionSecrets {
		t.Fatal("expected StrictProductionSecrets from TOML strict_secrets")
	}
}

func TestValidate_NvidiaLockGateP2PRequiresLock(t *testing.T) {
	c := &Config{
		NetworkPort:        4001,
		DashboardPort:      8081,
		LogViewerPort:      9000,
		APIPort:            8080,
		StorageType:        "file",
		NvidiaLockEnabled:  false,
		NvidiaLockGateP2P:  true,
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error when gate_p2p without nvidia_lock")
	}
}

func TestApplyEnvOverrides_StrictSecrets(t *testing.T) {
	t.Setenv("QSDM_STRICT_SECRETS", "")
	cfg := &Config{}
	applyEnvOverrides(cfg)
	if cfg.StrictProductionSecrets {
		t.Fatal("expected false when env empty")
	}
	t.Setenv("QSDM_STRICT_SECRETS", "1")
	cfg2 := &Config{}
	applyEnvOverrides(cfg2)
	if !cfg2.StrictProductionSecrets {
		t.Fatal("expected true for QSDM_STRICT_SECRETS=1")
	}
}

func TestResolvedSubmeshConfigPath_relativeToMainConfig(t *testing.T) {
	base := filepath.Join(t.TempDir(), "repo", "qsdm.toml")
	cfg := &Config{
		ConfigFileUsed:      base,
		SubmeshConfigPath:   "config/micropayments.toml",
	}
	want := filepath.Join(filepath.Dir(base), "config", "micropayments.toml")
	if got := cfg.ResolvedSubmeshConfigPath(); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestValidate_SubmeshConfig_missingFile(t *testing.T) {
	c := &Config{
		NetworkPort:       4001,
		DashboardPort:     8081,
		LogViewerPort:     9000,
		APIPort:           8080,
		StorageType:       "file",
		SubmeshConfigPath: filepath.Join(t.TempDir(), "nope.toml"),
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for missing submesh file")
	}
}

func TestApplyEnvOverrides_APIRateLimit(t *testing.T) {
	t.Setenv("QSDM_API_RATE_LIMIT_MAX", "250")
	t.Setenv("QSDM_API_RATE_LIMIT_WINDOW", "2m")
	cfg := &Config{}
	applyDefaults(cfg)
	applyEnvOverrides(cfg)
	if cfg.APIRateLimitMaxRequests != 250 {
		t.Fatalf("max: %d", cfg.APIRateLimitMaxRequests)
	}
	if cfg.APIRateLimitWindow != 2*time.Minute {
		t.Fatalf("window: %v", cfg.APIRateLimitWindow)
	}
}

func TestApplyDefaults_logViewerDoesNotClashWithAPI(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	if cfg.APIPort != 8080 {
		t.Fatalf("APIPort: %d", cfg.APIPort)
	}
	if cfg.LogViewerPort != 9000 {
		t.Fatalf("LogViewerPort: %d (want 9000, distinct from API)", cfg.LogViewerPort)
	}
}

func TestValidate_APIRateLimit_tooHigh(t *testing.T) {
	c := &Config{
		NetworkPort:             4001,
		DashboardPort:           8081,
		LogViewerPort:           9000,
		APIPort:                 8080,
		StorageType:             "file",
		APIRateLimitMaxRequests: 20_000_000,
		APIRateLimitWindow:      time.Minute,
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for excessive rate limit max")
	}
}
