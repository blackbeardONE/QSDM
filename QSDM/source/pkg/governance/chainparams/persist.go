package chainparams

// persist.go — filesystem-backed snapshot persistence for the
// in-memory ParamStore reference implementation.
//
// # Why this exists
//
// store.go ships an InMemoryParamStore whose own doc-comment
// notes: "implementations MUST persist the active and pending
// state across node restarts. The in-memory reference
// implementation here does NOT persist (that's a node-side
// responsibility, the same way enrollment.InMemoryState is
// non-persistent and the persistence layer wraps it)."
//
// This file is that wrapper. It does NOT implement a different
// ParamStore: it ships free functions that a host can call to
// save / load a *InMemoryParamStore against a path. That mirrors
// the pkg/chain/staking_persist.go shape (LoadOrNewStakingLedger
// / SaveStakingLedger) and keeps the consensus-critical store
// type free of disk I/O concerns. The host (internal/v2wiring)
// orchestrates when to persist — the natural commit boundary is
// the post-seal hook, after Promote runs.
//
// # Snapshot format
//
// Canonical JSON with a leading version field:
//
//	{
//	  "version": 1,
//	  "saved_at": "2026-04-28T16:20:00Z",
//	  "active": {"reward_bps": 2500, "auto_revoke_min_stake_dust": 1000000000},
//	  "pending": [
//	    {"param":"reward_bps","value":3000,"effective_height":12345,
//	     "submitted_at_height":12000,"authority":"alice","memo":"..."}
//	  ]
//	}
//
// Active values for parameters that are NOT in the on-disk
// snapshot fall back to the registry default (set by
// NewInMemoryParamStore at construction). Active values FOR
// parameters that are in the snapshot but NOT in the current
// registry are silently skipped — that's the forward/backward
// compat behaviour: a binary that drops a parameter from the
// registry can still load a snapshot from a binary that had it.
//
// Pending entries for parameters not in the registry are also
// dropped (a Stage call would now reject them anyway).
//
// # Atomicity
//
// SaveSnapshot writes through a `<path>.tmp` file then renames
// over `<path>`. On Windows the rename can fail if `<path>`
// exists, so we Remove before Rename. Same posture as
// staking_persist.go.
//
// # Concurrency
//
// SaveSnapshot reads the store via its existing AllActive() /
// AllPending() methods, both of which take the store's RLock
// internally. There is a tiny window between the two reads
// where a Stage could land — but in production SaveSnapshot is
// called from the SealedBlockHook, which runs after every
// applier mutation for the just-sealed block has completed and
// before the next block's applier runs, so the window is
// effectively closed by the chain's serial apply path.

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"
)

// SnapshotVersion is the on-disk format version. Bumping it is
// a breaking change to operator deployments — a node restarting
// against a snapshot from a newer binary refuses to load rather
// than silently corrupt state.
const SnapshotVersion = 1

// snapshotDoc is the on-disk JSON shape. JSON tags are the wire
// contract; renaming them is a breaking change.
type snapshotDoc struct {
	Version int                `json:"version"`
	SavedAt string             `json:"saved_at"`
	Active  map[string]uint64  `json:"active"`
	Pending []snapshotPending  `json:"pending"`
}

// snapshotPending mirrors ParamChange but with explicit JSON
// tags so the wire format is decoupled from the in-memory
// struct's field names (which are not exported with JSON tags).
type snapshotPending struct {
	Param             string `json:"param"`
	Value             uint64 `json:"value"`
	EffectiveHeight   uint64 `json:"effective_height"`
	SubmittedAtHeight uint64 `json:"submitted_at_height"`
	Authority         string `json:"authority,omitempty"`
	Memo              string `json:"memo,omitempty"`
}

// SaveSnapshot writes the current state of `store` to `path`
// atomically (temp file + rename). Returns nil on store==nil
// or path=="" (the "persistence disabled" no-op). Returns an
// error on any I/O or marshal failure; the on-disk file is
// either fully old or fully new — never half-written.
//
// SaveSnapshot is safe to call concurrently with reads against
// the store (AllActive / AllPending take RLock internally).
// Calling SaveSnapshot concurrently with itself against the
// same path is racy and SHOULD NOT be done — the host is
// expected to serialise saves through the chain's apply path.
func SaveSnapshot(store ParamStore, path string) error {
	if store == nil || path == "" {
		return nil
	}
	doc := snapshotDoc{
		Version: SnapshotVersion,
		SavedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Active:  store.AllActive(),
		Pending: nil,
	}
	for _, p := range store.AllPending() {
		doc.Pending = append(doc.Pending, snapshotPending{
			Param:             p.Param,
			Value:             p.Value,
			EffectiveHeight:   p.EffectiveHeight,
			SubmittedAtHeight: p.SubmittedAtHeight,
			Authority:         p.Authority,
			Memo:              p.Memo,
		})
	}
	// Sort pending for byte-stable output: same store contents
	// produce the same on-disk file, simplifying integrity
	// checks and snapshot-diff workflows.
	sort.Slice(doc.Pending, func(i, j int) bool {
		if doc.Pending[i].EffectiveHeight != doc.Pending[j].EffectiveHeight {
			return doc.Pending[i].EffectiveHeight < doc.Pending[j].EffectiveHeight
		}
		return doc.Pending[i].Param < doc.Pending[j].Param
	})

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("chainparams: marshal snapshot: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return fmt.Errorf("chainparams: write tmp snapshot: %w", err)
	}
	// Windows rename-over-existing fails; Remove first.
	// The window between Remove and Rename is non-critical:
	// a crash there leaves <path> missing and <path>.tmp
	// containing the new snapshot, which LoadOrNew handles
	// (it falls back to defaults on missing path; an
	// operator can manually rename .tmp → final).
	_ = os.Remove(path)
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("chainparams: rename snapshot: %w", err)
	}
	return nil
}

// LoadOrNew constructs an InMemoryParamStore from a snapshot
// file at `path`. Behaviour by file state:
//
//   - File missing → returns a fresh store seeded with registry
//     defaults (NewInMemoryParamStore output) and nil error.
//     This is the "first boot" path; the host will call
//     SaveSnapshot at the next sealed block.
//   - File present, parses, version matches → returns a store
//     whose actives + pendings reflect the snapshot. Active
//     values for params NOT in the snapshot remain at their
//     registry default. Active values for params in the
//     snapshot but NOT in the registry are dropped silently.
//     Pending entries for unknown params are also dropped.
//   - File present, parses, version does NOT match → returns
//     a clear error so the operator can decide whether to
//     migrate or wipe the snapshot. We refuse to silently
//     downgrade — wrong version means wrong shape.
//   - File present, JSON malformed → returns a clear error.
//
// On any error path, the receiver is left as nil (never a
// half-loaded store).
//
// LoadOrNew is intentionally a free function rather than a
// constructor method on InMemoryParamStore: it lives in the
// persistence layer and the in-memory store stays free of
// I/O concerns.
func LoadOrNew(path string) (*InMemoryParamStore, error) {
	if path == "" {
		return NewInMemoryParamStore(), nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewInMemoryParamStore(), nil
		}
		return nil, fmt.Errorf("chainparams: read snapshot %q: %w", path, err)
	}
	var doc snapshotDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("chainparams: parse snapshot %q: %w", path, err)
	}
	if doc.Version != SnapshotVersion {
		return nil, fmt.Errorf(
			"chainparams: snapshot %q has unsupported version %d (want %d) — refusing to load to avoid state corruption",
			path, doc.Version, SnapshotVersion)
	}

	store := NewInMemoryParamStore()
	// Load actives. Restrict to registry-known names;
	// unknown ones are forward/backward compat noise.
	for name, value := range doc.Active {
		spec, ok := Lookup(name)
		if !ok {
			continue
		}
		// Bounds-check on load: a snapshot from a binary that
		// allowed wider bounds, replayed against a binary
		// that tightened them, must NOT silently re-stage an
		// out-of-range value as active. Clamp to the new
		// registry's default in that case.
		if err := spec.CheckBounds(value); err != nil {
			store.SetForTesting(name, spec.DefaultValue)
			continue
		}
		store.SetForTesting(name, value)
	}
	// Load pendings. Skip unknown / out-of-bounds entries with
	// the same forward-compat posture; do NOT panic on bad
	// data — operators expect snapshot replays to "do their
	// best" and report success.
	for _, p := range doc.Pending {
		spec, ok := Lookup(p.Param)
		if !ok {
			continue
		}
		if err := spec.CheckBounds(p.Value); err != nil {
			continue
		}
		if p.EffectiveHeight == 0 {
			// Stage requires non-zero EffectiveHeight; a
			// zero here is corrupt. Drop silently rather
			// than refuse the whole load.
			continue
		}
		change := ParamChange{
			Param:             p.Param,
			Value:             p.Value,
			EffectiveHeight:   p.EffectiveHeight,
			SubmittedAtHeight: p.SubmittedAtHeight,
			Authority:         p.Authority,
			Memo:              p.Memo,
		}
		// Stage performs validation again; we already pre-
		// filtered, but the double-check keeps state sane in
		// the face of registry edge cases.
		_, _, _ = store.Stage(change)
	}
	return store, nil
}
