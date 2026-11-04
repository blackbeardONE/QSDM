package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Synthetic checkpoint values: no real block has them.
var (
	snapTestHash     = strings.Repeat("1a", 32)
	snapTestRoot     = strings.Repeat("2b", 32)
	snapTestProducer = strings.Repeat("3c", 32)
)

func loadSnapTestConfig(t *testing.T, name, body string) *Config {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{}
	if err := loadConfigFile(p, cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// The [snapshot_bootstrap] table loads from TOML and from YAML into the same
// trimmed values, without disturbing the other tables; a file without it
// leaves the bootstrap off.
func TestSnapshotBootstrap_FileLoads(t *testing.T) {
	want := SnapshotBootstrapConfig{Enabled: true, CheckpointHeight: 4321, CheckpointBlockHash: snapTestHash,
		CheckpointStateRoot: snapTestRoot, CheckpointProducerID: snapTestProducer}

	fromTOML := loadSnapTestConfig(t, "node.toml", `[network]
port = 14001

[snapshot_bootstrap]
enabled = true
checkpoint_height = 4321
checkpoint_block_hash = " `+snapTestHash+` "
checkpoint_state_root = "`+snapTestRoot+`"
checkpoint_producer_id = "`+snapTestProducer+`"
`)
	if fromTOML.SnapshotBootstrap != want || fromTOML.NetworkPort != 14001 {
		t.Fatalf("toml: %+v (port %d), want %+v", fromTOML.SnapshotBootstrap, fromTOML.NetworkPort, want)
	}
	if err := fromTOML.SnapshotBootstrap.Validate(); err != nil {
		t.Fatal(err)
	}

	fromYAML := loadSnapTestConfig(t, "node.yaml", "snapshot_bootstrap:\n  enabled: true\n  checkpoint_height: 4321\n"+
		"  checkpoint_block_hash: \""+snapTestHash+"\"\n  checkpoint_state_root: \""+snapTestRoot+"\"\n"+
		"  checkpoint_producer_id: \""+snapTestProducer+"\"\n")
	if fromYAML.SnapshotBootstrap != want {
		t.Fatalf("yaml: %+v, want %+v", fromYAML.SnapshotBootstrap, want)
	}

	off := loadSnapTestConfig(t, "off.toml", "[network]\nport = 14001\n")
	if off.SnapshotBootstrap != (SnapshotBootstrapConfig{}) {
		t.Fatalf("no table must leave the bootstrap off: %+v", off.SnapshotBootstrap)
	}
	if err := off.SnapshotBootstrap.Validate(); err != nil {
		t.Fatal(err)
	}
}

// A blank or unset variable keeps the file value; a set one replaces it.
func TestSnapshotBootstrap_EnvKeepsOrReplacesFile(t *testing.T) {
	for _, k := range []string{"QSDM_SNAPSHOT_BOOTSTRAP", "QSDM_SNAPSHOT_CHECKPOINT_HEIGHT", "QSDM_SNAPSHOT_CHECKPOINT_BLOCK_HASH",
		"QSDM_SNAPSHOT_CHECKPOINT_STATE_ROOT", "QSDM_SNAPSHOT_CHECKPOINT_PRODUCER_ID"} {
		t.Setenv(k, "  ")
	}
	file := SnapshotBootstrapConfig{Enabled: true, CheckpointHeight: 7, CheckpointBlockHash: snapTestHash, CheckpointStateRoot: snapTestRoot}
	c := file
	applySnapshotBootstrapEnv(&c)
	if c != file {
		t.Fatalf("blank variables changed the file values: %+v", c)
	}
	t.Setenv("QSDM_SNAPSHOT_CHECKPOINT_HEIGHT", " 9 ")
	t.Setenv("QSDM_SNAPSHOT_CHECKPOINT_BLOCK_HASH", snapTestRoot)
	t.Setenv("QSDM_SNAPSHOT_BOOTSTRAP", "on")
	applySnapshotBootstrapEnv(&c)
	if !c.Enabled || c.CheckpointHeight != 9 || c.CheckpointBlockHash != snapTestRoot || c.CheckpointStateRoot != snapTestRoot {
		t.Fatalf("set variables not applied: %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

// Config.Validate wires the check in: a malformed [snapshot_bootstrap] fails
// the whole load, and the default passes.
func TestSnapshotBootstrap_ConfigValidateWired(t *testing.T) {
	c := p2pBaseValidConfig()
	if err := c.Validate(); err != nil {
		t.Fatalf("default: %v", err)
	}
	c.SnapshotBootstrap = SnapshotBootstrapConfig{Enabled: true, CheckpointHeight: 5, CheckpointBlockHash: "abc", CheckpointStateRoot: snapTestRoot}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "[snapshot_bootstrap] checkpoint_block_hash") {
		t.Fatalf("Validate: %v", err)
	}
	c.SnapshotBootstrap.CheckpointBlockHash = snapTestHash
	if err := c.Validate(); err != nil {
		t.Fatalf("valid checkpoint: %v", err)
	}
}
