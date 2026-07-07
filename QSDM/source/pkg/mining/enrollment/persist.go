package enrollment

// persist.go — file-backed snapshot for InMemoryState.
//
// The on-chain enrollment registry is the source-of-truth for
// "which NodeIDs may submit v2 attestations" (hmac.Registry
// implementation lives in registry.go). Without persistence,
// every validator restart wipes the registry and forces every
// operator to re-enroll, which (a) breaks the testnet UX,
// (b) re-debits the stake from the operator's wallet (or
// fails because it was already debited and the AccountStore
// remembers).
//
// This file pairs with pkg/chain/persist.go: AccountStore is
// snapshotted after each block seal, and so is this state.
// Together they are the minimum set required for a clean
// "stop validator → start validator" cycle to reach the same
// state machine the prior process left behind.
//
// Format: one JSON object with two top-level keys:
//
//	{
//	  "records": [EnrollmentRecord, ...],
//	  "seen_evidence_hex": ["<64-char hex>", ...]
//	}
//
// We deliberately do NOT serialise the byGPUActive index — it
// is an O(n) projection of records (gpu_uuid → node_id for
// every Active() record) and rebuilding it on Load avoids
// the "snapshot inconsistent with itself" failure mode where
// the persisted index drifts from the persisted records.
//
// Atomic write: Save writes through same-directory temp files and replaces
// <path>. A crash before replacement leaves <path> intact. A separately
// replaced <path>.last-good file permits verified recovery from storage-level
// corruption without silently discarding miner enrollment state.

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/blackbeardONE/QSDM/pkg/fileutil"
)

// stateSnapshot is the on-disk shape of an InMemoryState.
// Exported field names use JSON tags so the file format is
// stable across the InMemoryState struct's internal layout
// (e.g. if we add a private cache field later, the snapshot
// schema does not move).
type stateSnapshot struct {
	Records         []EnrollmentRecord `json:"records"`
	SeenEvidenceHex []string           `json:"seen_evidence_hex,omitempty"`
}

// Save writes a snapshot of the receiver to path. The file is
// 0o644 (operator-readable). Returns nil if path is empty
// (caller opted out of persistence — same convention as
// internal/v2wiring's optional path fields).
//
// Concurrency: takes s.mu. If the receiver is being mutated
// concurrently by ApplyEnroll / SlashStake / etc., the
// snapshot reflects a consistent point in time but other
// goroutines may continue mutating immediately after the
// lock is released. Callers that need a strict happens-before
// edge should drive Save from a single serialised hook (e.g.
// BlockProducer.OnSealedBlock).
func (s *InMemoryState) Save(path string) error {
	if s == nil {
		return errors.New("enrollment: Save on nil InMemoryState")
	}
	if path == "" {
		return nil
	}

	s.mu.Lock()
	snap := stateSnapshot{
		Records:         make([]EnrollmentRecord, 0, len(s.byNodeID)),
		SeenEvidenceHex: make([]string, 0, len(s.seenEvidence)),
	}
	for _, rec := range s.byNodeID {
		if rec == nil {
			continue
		}
		snap.Records = append(snap.Records, *rec)
	}
	for h := range s.seenEvidence {
		snap.SeenEvidenceHex = append(snap.SeenEvidenceHex, hex.EncodeToString(h[:]))
	}
	s.mu.Unlock()

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("enrollment: marshal snapshot: %w", err)
	}
	if err := fileutil.WriteFileAtomic(path+".last-good", data, 0o644); err != nil {
		return fmt.Errorf("enrollment: save last-good %s: %w", path, err)
	}
	if err := fileutil.WriteFileAtomic(path, data, 0o644); err != nil {
		return fmt.Errorf("enrollment: save %s: %w", path, err)
	}
	return nil
}

// Load reads a snapshot from path and merges it into the
// receiver. Returns (loadedRecords, nil) on success, or
// (0, nil) if the file does not exist (the canonical
// "fresh chain, no prior enrollments" case).
//
// Load REQUIRES the receiver to be empty — it is intended as
// boot-time hydration, not a live merge. Loading into a
// non-empty state would re-introduce records that may have
// been ApplyUnenroll-ed since the snapshot was taken, so
// the safer posture is to refuse and let the caller decide
// (clear-and-load vs. fail-fast).
//
// The byGPUActive index is rebuilt from scratch: each Active()
// record claims its GPU UUID. Records that were already
// revoked / un-enrolled at snapshot time do NOT contribute to
// the active-GPU index, matching the live ApplyEnroll
// invariant.
func (s *InMemoryState) Load(path string) (int, error) {
	if s == nil {
		return 0, errors.New("enrollment: Load on nil InMemoryState")
	}
	if path == "" {
		return 0, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("enrollment: read %s: %w", path, err)
	}
	var snap stateSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		primaryErr := err
		backupPath := path + ".last-good"
		backup, backupErr := os.ReadFile(backupPath)
		if backupErr != nil {
			return 0, fmt.Errorf("enrollment: parse %s: %w", path, primaryErr)
		}
		if backupErr = json.Unmarshal(backup, &snap); backupErr != nil {
			return 0, fmt.Errorf("enrollment: parse %s: %w (last-good snapshot is also invalid: %v)", path, primaryErr, backupErr)
		}
		if backupErr = fileutil.WriteFileAtomic(path, backup, 0o644); backupErr != nil {
			return 0, fmt.Errorf("enrollment: restore %s from last-good: %w", path, backupErr)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.byNodeID) != 0 {
		return 0, fmt.Errorf("enrollment: Load called on a non-empty state (have %d records); call before any ApplyEnroll runs", len(s.byNodeID))
	}
	for i := range snap.Records {
		rec := snap.Records[i]
		if rec.NodeID == "" {
			continue
		}
		cp := rec
		s.byNodeID[rec.NodeID] = &cp
		if cp.Active() {
			s.byGPUActive[cp.GPUUUID] = cp.NodeID
		}
	}
	for _, h := range snap.SeenEvidenceHex {
		raw, err := hex.DecodeString(h)
		if err != nil {
			return len(snap.Records), fmt.Errorf("enrollment: parse evidence hash %q: %w", h, err)
		}
		if len(raw) != 32 {
			return len(snap.Records), fmt.Errorf("enrollment: evidence hash %q has %d bytes, want 32", h, len(raw))
		}
		var key [32]byte
		copy(key[:], raw)
		s.seenEvidence[key] = true
	}
	return len(snap.Records), nil
}
