package telemetry

// Registry is the long-lived in-memory accumulator that
// turns one-shot Collect snapshots into the persisted
// ReferenceProfile. It owns:
//
//   - per-UUID GPUObservation state (merged across
//     observations and across attester restarts)
//   - operator metadata (SignerID, HostNote, CollectorKind)
//     that the published profile inherits
//   - the on-disk JSON file that survives restarts
//   - thread-safe Apply / Snapshot semantics so the
//     collector goroutine and HTTP handler don't trip on
//     each other
//
// The registry deliberately does NOT hold the HMAC signer
// key. Signing is performed at Snapshot time by the caller
// (cmd/qsdm-attester wires this to the same key it uses
// for /api/v1/mining/challenge), so a future migration to a
// hardware-token-backed signer changes one wiring file
// instead of every persistence path.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Registry is safe for concurrent use. The zero value is
// invalid (no SignerID); always construct via NewRegistry.
type Registry struct {
	mu sync.RWMutex

	// Static metadata, set at construction. Guarded by mu
	// only because Snapshot needs a consistent read of
	// (perGPU + metadata) — there's no actual mutation
	// post-construction.
	signerID      string
	hostNote      string
	collectorKind string
	schema        int

	// perGPU is the authoritative state. Keyed by UUID.
	perGPU map[string]*GPUObservation

	// applies is a counter of total Apply() calls; useful
	// for /metrics. Atomic so /metrics doesn't take the
	// mutex.
	applies atomic.Uint64

	// loadedFrom is the path the in-memory state was
	// hydrated from on construction (empty if none). Used
	// in /info to surface "is persistence configured?".
	loadedFrom string
}

// NewRegistry constructs an empty registry with the supplied
// metadata. To hydrate from disk afterwards, call
// LoadFromFile separately so a non-existent file path is
// not a constructor-time fatal error.
func NewRegistry(signerID, hostNote, collectorKind string) (*Registry, error) {
	if signerID == "" {
		return nil, errors.New("telemetry: NewRegistry requires non-empty signerID")
	}
	return &Registry{
		signerID:      signerID,
		hostNote:      hostNote,
		collectorKind: collectorKind,
		schema:        SchemaVersion,
		perGPU:        make(map[string]*GPUObservation),
	}, nil
}

// SignerID returns the registry's signer ID. Read-only post
// construction.
func (r *Registry) SignerID() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.signerID
}

// HostNote returns the registry's host note.
func (r *Registry) HostNote() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.hostNote
}

// CollectorKind returns the registry's collector identifier.
func (r *Registry) CollectorKind() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.collectorKind
}

// Apply merges one collector snapshot into the registry's
// state. Returns (changed, error): changed is true when at
// least one persisted field actually moved (so the caller
// can skip a no-op disk write). UUID is required; an
// empty-UUID snapshot is silently ignored (returns false,
// nil) to make collector loops resilient to a transient
// nvidia-smi parse error.
func (r *Registry) Apply(snap GPUObservation) (bool, error) {
	if snap.UUID == "" {
		return false, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now().Unix()
	existing, ok := r.perGPU[snap.UUID]
	if !ok {
		existing = &GPUObservation{UUID: snap.UUID}
		r.perGPU[snap.UUID] = existing
	}
	changed := existing.MergeWith(snap, now)
	r.applies.Add(1)
	return changed, nil
}

// ApplyAll is a convenience for the typical collector loop
// "I got N GPUs in one snapshot". Returns (anyChanged,
// firstErr): firstErr never fires today (Apply only
// returns nil for non-error paths) but the API leaves room
// for a future Apply that validates per-snapshot
// invariants.
func (r *Registry) ApplyAll(snaps []GPUObservation) (bool, error) {
	any := false
	for _, s := range snaps {
		changed, err := r.Apply(s)
		if err != nil {
			return any, err
		}
		any = any || changed
	}
	return any, nil
}

// Snapshot builds a fresh ReferenceProfile from the current
// state. Callers Sign() the result before serving over HTTP.
// IssuedAt defaults to time.Now() — pass a non-zero override
// for deterministic tests.
func (r *Registry) Snapshot(issuedAtOverride int64) *ReferenceProfile {
	r.mu.RLock()
	defer r.mu.RUnlock()

	issuedAt := issuedAtOverride
	if issuedAt == 0 {
		issuedAt = time.Now().Unix()
	}
	gpus := make([]GPUObservation, 0, len(r.perGPU))
	for _, g := range r.perGPU {
		// Defensive DEEP copy so the consumer can mutate
		// (e.g. truncate version sets) without poisoning
		// the registry's internal state. Struct copy
		// alone is insufficient because []string slice
		// headers alias the backing array.
		gpus = append(gpus, deepCopyObservation(*g))
	}
	sort.SliceStable(gpus, func(i, j int) bool { return gpus[i].UUID < gpus[j].UUID })

	return &ReferenceProfile{
		SchemaVersion: r.schema,
		SignerID:      r.signerID,
		HostNote:      r.hostNote,
		IssuedAt:      issuedAt,
		CollectorKind: r.collectorKind,
		GPUs:          gpus,
	}
}

// deepCopyObservation duplicates every string slice inside
// g so mutation of the returned value cannot reach back
// into the registry's per-GPU pointer state. Sized at the
// observation type's slice fields — extend if you add new
// slice fields to GPUObservation.
func deepCopyObservation(g GPUObservation) GPUObservation {
	if len(g.DriverVersionsSeen) > 0 {
		cp := make([]string, len(g.DriverVersionsSeen))
		copy(cp, g.DriverVersionsSeen)
		g.DriverVersionsSeen = cp
	}
	if len(g.CUDAVersionsSeen) > 0 {
		cp := make([]string, len(g.CUDAVersionsSeen))
		copy(cp, g.CUDAVersionsSeen)
		g.CUDAVersionsSeen = cp
	}
	if len(g.VBIOSVersionsSeen) > 0 {
		cp := make([]string, len(g.VBIOSVersionsSeen))
		copy(cp, g.VBIOSVersionsSeen)
		g.VBIOSVersionsSeen = cp
	}
	return g
}

// SignedSnapshot is a Snapshot + Sign in one call. Returns
// nil + error on signing failure.
func (r *Registry) SignedSnapshot(issuedAtOverride int64, key []byte) (*ReferenceProfile, error) {
	p := r.Snapshot(issuedAtOverride)
	if err := p.Sign(key); err != nil {
		return nil, err
	}
	return p, nil
}

// Counters returns (apply_calls, gpu_count). Useful for
// /metrics. Cheap — uses the atomic for apply_calls and
// the read lock only for the gpu count.
func (r *Registry) Counters() (uint64, int) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.applies.Load(), len(r.perGPU)
}

// SaveToFile atomically writes the current registry state
// to path (file rename pattern). Empty path = no-op
// (persistence disabled). Permissions: 0o644 — the file
// only contains public information, no key material.
//
// The on-disk format is a stripped ReferenceProfile (no
// Signature, no IssuedAt) — those are computed at /info
// query time. Keeping them OUT of the persisted file
// guarantees we can never accidentally serve a stale-by-
// hours signature: every published profile is freshly
// signed at the moment of the request.
func (r *Registry) SaveToFile(path string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("telemetry: mkdir for %s: %w", path, err)
	}
	r.mu.RLock()
	gpus := make([]GPUObservation, 0, len(r.perGPU))
	for _, g := range r.perGPU {
		gpus = append(gpus, *g)
	}
	persisted := persistedRegistry{
		SchemaVersion: r.schema,
		SignerID:      r.signerID,
		HostNote:      r.hostNote,
		CollectorKind: r.collectorKind,
		GPUs:          gpus,
	}
	r.mu.RUnlock()

	sort.SliceStable(persisted.GPUs, func(i, j int) bool {
		return persisted.GPUs[i].UUID < persisted.GPUs[j].UUID
	})

	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return fmt.Errorf("telemetry: marshal: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("telemetry: write tmp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("telemetry: rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

// LoadFromFile hydrates the registry from a previously-
// persisted file. A non-existent path is NOT an error —
// returns (loaded=false, nil) so an attester boots cleanly
// on its first run. A malformed file IS an error — the
// caller should refuse to start rather than silently
// dropping the operator's accumulated history.
func (r *Registry) LoadFromFile(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("telemetry: read %s: %w", path, err)
	}
	var persisted persistedRegistry
	if err := json.Unmarshal(raw, &persisted); err != nil {
		return false, fmt.Errorf("telemetry: parse %s: %w", path, err)
	}
	if persisted.SchemaVersion == 0 {
		// Legacy/empty file. Treat as non-fatal so an
		// operator can recover by deleting the file.
		return false, fmt.Errorf("telemetry: %s missing schema_version (delete to reset)", path)
	}
	if persisted.SchemaVersion > SchemaVersion {
		return false, fmt.Errorf("telemetry: %s schema_version %d > supported %d (binary too old)",
			path, persisted.SchemaVersion, SchemaVersion)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// SignerID / HostNote / CollectorKind from disk are
	// IGNORED — the live attester's identity always wins.
	// This means an operator can rename their attester or
	// swap collectors without the persisted file getting in
	// the way; only the per-GPU history is preserved.
	for i := range persisted.GPUs {
		g := persisted.GPUs[i]
		if g.UUID == "" {
			continue
		}
		// Defensive copy into a fresh pointer so caller
		// mutations of the local var don't reach in.
		clone := g
		r.perGPU[g.UUID] = &clone
	}
	r.loadedFrom = path
	return true, nil
}

// LoadedFrom returns the path the registry was hydrated
// from on construction (empty if none). Surfaced via
// /info so operators can verify persistence is configured.
func (r *Registry) LoadedFrom() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.loadedFrom
}

// persistedRegistry is the on-disk shape. Held in its own
// type (not anonymous) so a future migration knows exactly
// which field set the old format had — no surprises.
type persistedRegistry struct {
	SchemaVersion int              `json:"schema_version"`
	SignerID      string           `json:"signer_id"`
	HostNote      string           `json:"host_note,omitempty"`
	CollectorKind string           `json:"collector_kind,omitempty"`
	GPUs          []GPUObservation `json:"gpus"`
}
