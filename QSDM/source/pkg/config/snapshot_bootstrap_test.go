package config

import (
	"strings"
	"testing"
)

func TestSnapshotBootstrapValidate(t *testing.T) {
	h, r, p := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	good := SnapshotBootstrapConfig{Enabled: true, CheckpointHeight: 10, CheckpointBlockHash: h, CheckpointStateRoot: r}
	for name, c := range map[string]SnapshotBootstrapConfig{
		"default off":      {},
		"on":               good,
		"on with producer": {Enabled: true, CheckpointHeight: 10, CheckpointBlockHash: h, CheckpointStateRoot: r, CheckpointProducerID: p},
	} {
		if err := c.Validate(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for name, c := range map[string]SnapshotBootstrapConfig{
		"off with checkpoint": {CheckpointHeight: 10},
		"off with producer":   {CheckpointProducerID: p},
		"height zero":         {Enabled: true, CheckpointBlockHash: h, CheckpointStateRoot: r},
		"short hash":          {Enabled: true, CheckpointHeight: 1, CheckpointBlockHash: "abc", CheckpointStateRoot: r},
		"upper-case root":     {Enabled: true, CheckpointHeight: 1, CheckpointBlockHash: h, CheckpointStateRoot: strings.ToUpper(r)},
		"peer id producer":    {Enabled: true, CheckpointHeight: 1, CheckpointBlockHash: h, CheckpointStateRoot: r, CheckpointProducerID: "12D3KooWxyz"},
	} {
		if err := c.Validate(); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

func TestSnapshotBootstrapEnvAndTOML(t *testing.T) {
	c := SnapshotBootstrapTOML{Enabled: true, CheckpointHeight: 3, CheckpointBlockHash: " " + strings.Repeat("a", 64) + " ", CheckpointStateRoot: strings.Repeat("b", 64)}.toConfig()
	if c.CheckpointBlockHash != strings.Repeat("a", 64) {
		t.Fatalf("toConfig did not trim: %q", c.CheckpointBlockHash)
	}
	t.Setenv("QSDM_SNAPSHOT_BOOTSTRAP", "0")
	t.Setenv("QSDM_SNAPSHOT_CHECKPOINT_HEIGHT", "42")
	t.Setenv("QSDM_SNAPSHOT_CHECKPOINT_PRODUCER_ID", strings.Repeat("d", 64))
	applySnapshotBootstrapEnv(&c)
	if c.Enabled || c.CheckpointHeight != 42 || c.CheckpointProducerID != strings.Repeat("d", 64) {
		t.Fatalf("env not applied: %+v", c)
	}
	if err := c.Validate(); err == nil {
		t.Fatal("env turned the feature off but left a checkpoint; must be refused")
	}
	t.Setenv("QSDM_SNAPSHOT_CHECKPOINT_HEIGHT", "x")
	var bad SnapshotBootstrapConfig
	applySnapshotBootstrapEnv(&bad)
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "QSDM_SNAPSHOT_CHECKPOINT_HEIGHT") {
		t.Fatalf("malformed env height: %v", err)
	}
}
