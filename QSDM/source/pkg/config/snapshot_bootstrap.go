package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/envcompat"
)

// SnapshotBootstrapTOML is the file form of the verified snapshot bootstrap
// (QSDM/docs/docs/SNAPSHOT_BOOTSTRAP.md):
//
//	[snapshot_bootstrap]
//	enabled = true
//	checkpoint_height = <N>
//	checkpoint_block_hash = "<64 hex>"
//	checkpoint_state_root = "<64 hex>"
//	checkpoint_producer_id = "<64 hex ML-DSA-87 producer address>"   # optional
//
// Off (the default) changes nothing: the boot path is byte-for-byte the one
// it was, and qsdm --snapshot-import refuses to run. On, the trusted
// checkpoint is used twice:
//   - qsdm --snapshot-import installs a snapshot into an empty follower state
//     directory only if its tip is exactly this checkpoint (height, block hash
//     and state root), the tip is signed (ML-DSA-87 producer_auth) by an
//     authorized producer, the journal hash-links back to genesis and the
//     account/enrollment files reproduce the state root;
//   - every boot refuses (exit 78) a restored chain whose block at
//     checkpoint_height has a different hash or state root.
type SnapshotBootstrapTOML struct {
	Enabled              bool   `toml:"enabled" yaml:"enabled"`
	CheckpointHeight     uint64 `toml:"checkpoint_height" yaml:"checkpoint_height"`
	CheckpointBlockHash  string `toml:"checkpoint_block_hash" yaml:"checkpoint_block_hash"`
	CheckpointStateRoot  string `toml:"checkpoint_state_root" yaml:"checkpoint_state_root"`
	CheckpointProducerID string `toml:"checkpoint_producer_id" yaml:"checkpoint_producer_id"`
}

// SnapshotBootstrapConfig is the parsed form held on Config.
type SnapshotBootstrapConfig struct {
	Enabled bool
	// CheckpointHeight, CheckpointBlockHash and CheckpointStateRoot are the
	// trusted checkpoint, pinned out of band (never taken from the snapshot).
	CheckpointHeight    uint64
	CheckpointBlockHash string
	CheckpointStateRoot string
	// CheckpointProducerID optionally pins the producer that must have signed
	// the checkpoint block. Empty: the producer transition, else [consensus]
	// authorized_block_producers, decides.
	CheckpointProducerID string

	// envErr is a malformed environment override, reported by Validate.
	envErr error
}

func (t SnapshotBootstrapTOML) toConfig() SnapshotBootstrapConfig {
	return SnapshotBootstrapConfig{
		Enabled:              t.Enabled,
		CheckpointHeight:     t.CheckpointHeight,
		CheckpointBlockHash:  strings.TrimSpace(t.CheckpointBlockHash),
		CheckpointStateRoot:  strings.TrimSpace(t.CheckpointStateRoot),
		CheckpointProducerID: strings.TrimSpace(t.CheckpointProducerID),
	}
}

// applySnapshotBootstrapEnv applies QSDM_SNAPSHOT_BOOTSTRAP (bool),
// QSDM_SNAPSHOT_CHECKPOINT_HEIGHT, QSDM_SNAPSHOT_CHECKPOINT_BLOCK_HASH,
// QSDM_SNAPSHOT_CHECKPOINT_STATE_ROOT and QSDM_SNAPSHOT_CHECKPOINT_PRODUCER_ID.
// A set variable replaces the file value.
func applySnapshotBootstrapEnv(c *SnapshotBootstrapConfig) {
	if strings.TrimSpace(os.Getenv("QSDM_SNAPSHOT_BOOTSTRAP")) != "" {
		c.Enabled = envcompat.Truthy("QSDM_SNAPSHOT_BOOTSTRAP", "QSDM_SNAPSHOT_BOOTSTRAP")
	}
	if v := strings.TrimSpace(os.Getenv("QSDM_SNAPSHOT_CHECKPOINT_HEIGHT")); v != "" {
		h, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			c.envErr = fmt.Errorf("QSDM_SNAPSHOT_CHECKPOINT_HEIGHT %q: %w", v, err)
			return
		}
		c.CheckpointHeight = h
	}
	if v := strings.TrimSpace(os.Getenv("QSDM_SNAPSHOT_CHECKPOINT_BLOCK_HASH")); v != "" {
		c.CheckpointBlockHash = v
	}
	if v := strings.TrimSpace(os.Getenv("QSDM_SNAPSHOT_CHECKPOINT_STATE_ROOT")); v != "" {
		c.CheckpointStateRoot = v
	}
	if v := strings.TrimSpace(os.Getenv("QSDM_SNAPSHOT_CHECKPOINT_PRODUCER_ID")); v != "" {
		c.CheckpointProducerID = v
	}
}

var hex64Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Validate checks the snapshot bootstrap keys. Off with no checkpoint keys is
// always valid (the default). A checkpoint that is set while the feature is
// off is refused, so an operator never believes a checkpoint is enforced
// when it is not.
func (c SnapshotBootstrapConfig) Validate() error {
	if c.envErr != nil {
		return fmt.Errorf("[snapshot_bootstrap] %w", c.envErr)
	}
	if !c.Enabled {
		if c.CheckpointHeight != 0 || c.CheckpointBlockHash != "" || c.CheckpointStateRoot != "" || c.CheckpointProducerID != "" {
			return fmt.Errorf("[snapshot_bootstrap] checkpoint keys are set but enabled is false: the checkpoint is enforced only when enabled; set enabled = true or clear the keys")
		}
		return nil
	}
	if c.CheckpointHeight == 0 {
		return fmt.Errorf("[snapshot_bootstrap] enabled = true requires checkpoint_height >= 1 (env QSDM_SNAPSHOT_CHECKPOINT_HEIGHT)")
	}
	if !hex64Pattern.MatchString(c.CheckpointBlockHash) {
		return fmt.Errorf("[snapshot_bootstrap] checkpoint_block_hash %q must be 64 lower-case hex characters", c.CheckpointBlockHash)
	}
	if !hex64Pattern.MatchString(c.CheckpointStateRoot) {
		return fmt.Errorf("[snapshot_bootstrap] checkpoint_state_root %q must be 64 lower-case hex characters", c.CheckpointStateRoot)
	}
	if c.CheckpointProducerID != "" && !hex64Pattern.MatchString(c.CheckpointProducerID) {
		return fmt.Errorf("[snapshot_bootstrap] checkpoint_producer_id %q must be an ML-DSA-87 producer address (64 lower-case hex characters)", c.CheckpointProducerID)
	}
	return nil
}
