package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Verifies the Major Update §8.5 trust surface knobs round-trip from
// TOML, YAML, and environment variables, and that Validate() installs
// the documented defaults when the fields are left unset. The landing
// page and dashboard widgets both depend on this contract so operators
// can opt out (return 404) or tune the freshness / refresh cadence
// without recompiling the node.

func TestLoadConfigFile_TOML_Trust(t *testing.T) {
	p := filepath.Join(t.TempDir(), "trust.toml")
	content := `
[node]
role = "validator"

[network]
port = 4001

[storage]
type = "file"

[monitoring]
dashboard_port = 8081
log_viewer_port = 9000

[api]
port = 8080

[trust]
disabled = false
fresh_within = "5m"
refresh_interval = "3s"
region_hint = "eu"
`
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatalf("write toml: %v", err)
	}
	cfg := &Config{}
	if err := loadConfigFile(p, cfg); err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	if cfg.TrustEndpointsDisabled {
		t.Fatal("TrustEndpointsDisabled should be false")
	}
	if cfg.TrustFreshWithin != 5*time.Minute {
		t.Fatalf("TrustFreshWithin = %v, want 5m", cfg.TrustFreshWithin)
	}
	if cfg.TrustRefreshInterval != 3*time.Second {
		t.Fatalf("TrustRefreshInterval = %v, want 3s", cfg.TrustRefreshInterval)
	}
	if cfg.TrustRegionHint != "eu" {
		t.Fatalf("TrustRegionHint = %q, want eu", cfg.TrustRegionHint)
	}
}

func TestLoadConfigFile_YAML_Trust_Disabled(t *testing.T) {
	p := filepath.Join(t.TempDir(), "trust.yaml")
	content := `
node:
  role: validator
network:
  port: 4001
storage:
  type: file
monitoring:
  dashboard_port: 8081
  log_viewer_port: 9000
api:
  port: 8080
trust:
  disabled: true
  region_hint: us
`
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	cfg := &Config{}
	if err := loadConfigFile(p, cfg); err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	if !cfg.TrustEndpointsDisabled {
		t.Fatal("TrustEndpointsDisabled should be true (opt-out via config)")
	}
	if cfg.TrustRegionHint != "us" {
		t.Fatalf("TrustRegionHint = %q, want us", cfg.TrustRegionHint)
	}
}

func TestApplyEnvOverrides_Trust(t *testing.T) {
	t.Setenv("QSDM_TRUST_DISABLED", "1")
	t.Setenv("QSDM_TRUST_FRESH_WITHIN", "30m")
	t.Setenv("QSDM_TRUST_REFRESH_INTERVAL", "20s")
	t.Setenv("QSDM_TRUST_REGION", "apac")
	cfg := &Config{}
	applyEnvOverrides(cfg)
	if !cfg.TrustEndpointsDisabled {
		t.Fatal("QSDM_TRUST_DISABLED=1 should disable trust endpoints")
	}
	if cfg.TrustFreshWithin != 30*time.Minute {
		t.Fatalf("TrustFreshWithin = %v, want 30m", cfg.TrustFreshWithin)
	}
	if cfg.TrustRefreshInterval != 20*time.Second {
		t.Fatalf("TrustRefreshInterval = %v, want 20s", cfg.TrustRefreshInterval)
	}
	if cfg.TrustRegionHint != "apac" {
		t.Fatalf("TrustRegionHint = %q, want apac", cfg.TrustRegionHint)
	}
}

func TestApplyEnvOverrides_Trust_LegacyQSDMPLUSAlias(t *testing.T) {
	// The env override path must still accept the legacy QSDMPLUS_*
	// names during the rebrand deprecation window, matching every other
	// knob in this package.
	t.Setenv("QSDMPLUS_TRUST_DISABLED", "true")
	cfg := &Config{}
	applyEnvOverrides(cfg)
	if !cfg.TrustEndpointsDisabled {
		t.Fatal("QSDMPLUS_TRUST_DISABLED=true should disable trust endpoints (legacy alias)")
	}
}

func TestApplyDefaults_Trust_InstallsProductionDefaults(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	if cfg.TrustFreshWithin != 15*time.Minute {
		t.Fatalf("default TrustFreshWithin = %v, want 15m", cfg.TrustFreshWithin)
	}
	if cfg.TrustRefreshInterval != 10*time.Second {
		t.Fatalf("default TrustRefreshInterval = %v, want 10s", cfg.TrustRefreshInterval)
	}
	if cfg.TrustEndpointsDisabled {
		t.Fatal("default TrustEndpointsDisabled should be false (opt-in transparency)")
	}
}
