package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseNodeRole(t *testing.T) {
	cases := []struct {
		in      string
		want    NodeRole
		wantErr bool
	}{
		{"", NodeRoleValidator, false},
		{"validator", NodeRoleValidator, false},
		{"VALIDATOR", NodeRoleValidator, false},
		{" validator ", NodeRoleValidator, false},
		{"primary", NodeRoleValidator, false},
		{"vps", NodeRoleValidator, false},
		{"miner", NodeRoleMiner, false},
		{"MINER", NodeRoleMiner, false},
		{"gpu", NodeRoleMiner, false},
		{"archive", "", true},
		{"full", "", true},
		{"???", "", true},
	}
	for _, c := range cases {
		got, err := ParseNodeRole(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseNodeRole(%q): expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseNodeRole(%q) unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("ParseNodeRole(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNodeRole_Helpers(t *testing.T) {
	if !NodeRole("").IsValidator() {
		t.Error("empty role should resolve to validator")
	}
	if NodeRole("").IsMiner() {
		t.Error("empty role should not be miner")
	}
	if !NodeRoleValidator.IsValidator() || NodeRoleValidator.IsMiner() {
		t.Error("NodeRoleValidator helpers wrong")
	}
	if NodeRoleMiner.IsValidator() || !NodeRoleMiner.IsMiner() {
		t.Error("NodeRoleMiner helpers wrong")
	}
	if !NodeRoleValidator.IsValid() || !NodeRoleMiner.IsValid() {
		t.Error("canonical roles must be valid")
	}
	if NodeRole("archive").IsValid() {
		t.Error("bogus role must not be valid")
	}
	if NodeRoleFromBoolMining(true) != NodeRoleMiner {
		t.Error("mining=true should yield miner")
	}
	if NodeRoleFromBoolMining(false) != NodeRoleValidator {
		t.Error("mining=false should yield validator")
	}
}

func baseValidConfig() *Config {
	return &Config{
		NodeRole:       NodeRoleValidator,
		MiningEnabled:  false,
		NetworkPort:    4001,
		DashboardPort:  8081,
		LogViewerPort:  9000,
		APIPort:        8080,
		StorageType:    "file",
		InitialBalance: 0,
		APIRateLimitMaxRequests: 100,
	}
}

func TestValidate_NodeRole_DefaultsToValidator(t *testing.T) {
	c := baseValidConfig()
	c.NodeRole = ""
	if err := c.Validate(); err != nil {
		t.Fatalf("validate with empty role: %v", err)
	}
	if c.NodeRole != NodeRoleValidator {
		t.Fatalf("Validate should default role to validator, got %q", c.NodeRole)
	}
}

func TestValidate_NodeRole_MinerRequiresMiningEnabled(t *testing.T) {
	c := baseValidConfig()
	c.NodeRole = NodeRoleMiner
	c.MiningEnabled = false
	err := c.Validate()
	if err == nil {
		t.Fatal("expected error: miner role with mining_enabled=false")
	}
	if !strings.Contains(err.Error(), "mining_enabled") {
		t.Fatalf("expected error to mention mining_enabled, got %v", err)
	}
}

func TestValidate_NodeRole_ValidatorRejectsMiningEnabled(t *testing.T) {
	c := baseValidConfig()
	c.NodeRole = NodeRoleValidator
	c.MiningEnabled = true
	err := c.Validate()
	if err == nil {
		t.Fatal("expected error: validator role with mining_enabled=true")
	}
	if !strings.Contains(err.Error(), "validators must not mine") {
		t.Fatalf("expected error to mention validators must not mine, got %v", err)
	}
}

func TestValidate_NodeRole_Invalid(t *testing.T) {
	c := baseValidConfig()
	c.NodeRole = NodeRole("archive")
	err := c.Validate()
	if err == nil {
		t.Fatal("expected error: invalid role")
	}
	if !strings.Contains(err.Error(), "invalid node.role") {
		t.Fatalf("expected error to mention invalid node.role, got %v", err)
	}
}

func TestValidate_NodeRole_MinerHappy(t *testing.T) {
	c := baseValidConfig()
	c.NodeRole = NodeRoleMiner
	c.MiningEnabled = true
	if err := c.Validate(); err != nil {
		t.Fatalf("miner+mining_enabled=true should be valid: %v", err)
	}
}

func TestLoadConfigFile_TOML_NodeRole(t *testing.T) {
	p := filepath.Join(t.TempDir(), "node.toml")
	content := `
[node]
role = "miner"
mining_enabled = true

[network]
port = 4001

[storage]
type = "file"

[monitoring]
dashboard_port = 8081
log_viewer_port = 9000

[api]
port = 8080
`
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatalf("write toml: %v", err)
	}
	cfg := &Config{}
	if err := loadConfigFile(p, cfg); err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	if cfg.NodeRole != NodeRoleMiner {
		t.Fatalf("NodeRole = %q, want miner", cfg.NodeRole)
	}
	if !cfg.MiningEnabled {
		t.Fatal("MiningEnabled should be true")
	}
}

func TestLoadConfigFile_YAML_NodeRole(t *testing.T) {
	p := filepath.Join(t.TempDir(), "node.yaml")
	content := `
node:
  role: validator
  mining_enabled: false
network:
  port: 4001
storage:
  type: file
monitoring:
  dashboard_port: 8081
  log_viewer_port: 9000
api:
  port: 8080
`
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	cfg := &Config{}
	if err := loadConfigFile(p, cfg); err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	if !cfg.NodeRole.IsValidator() {
		t.Fatalf("NodeRole = %q, want validator", cfg.NodeRole)
	}
	if cfg.MiningEnabled {
		t.Fatal("MiningEnabled should be false")
	}
}

func TestApplyEnvOverrides_NodeRole(t *testing.T) {
	t.Setenv("QSDM_NODE_ROLE", "miner")
	t.Setenv("QSDM_MINING_ENABLED", "true")
	cfg := &Config{NodeRole: NodeRoleValidator, MiningEnabled: false}
	applyEnvOverrides(cfg)
	if cfg.NodeRole != NodeRoleMiner {
		t.Fatalf("NodeRole = %q, want miner", cfg.NodeRole)
	}
	if !cfg.MiningEnabled {
		t.Fatal("MiningEnabled should be true after env override")
	}
}

func TestApplyEnvOverrides_NodeRole_LegacyEnvAccepted(t *testing.T) {
	t.Setenv("QSDM_NODE_ROLE", "miner")
	t.Setenv("QSDM_MINING_ENABLED", "1")
	cfg := &Config{NodeRole: NodeRoleValidator, MiningEnabled: false}
	applyEnvOverrides(cfg)
	if cfg.NodeRole != NodeRoleMiner {
		t.Fatalf("NodeRole (legacy env) = %q, want miner", cfg.NodeRole)
	}
	if !cfg.MiningEnabled {
		t.Fatal("MiningEnabled should be true after legacy env override")
	}
}
