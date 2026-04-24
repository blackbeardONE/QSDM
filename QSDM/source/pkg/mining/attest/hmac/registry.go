package hmac

// This file defines the three collaborator interfaces the
// nvidia-hmac-v1 Verifier delegates to, plus in-memory reference
// implementations. Production will replace the in-memory impls
// with on-chain registry lookups (Phase 2c-ii) and a persistent
// nonce ring-buffer (Phase 2c-iii), but the interfaces stay
// unchanged so the swap is drop-in.
//
// Why three interfaces and not one: the verifier itself is ~100
// lines of the 9-step flow from spec §3.2.2. Each collaborator
// has a single, testable responsibility (who's registered; what
// nonces have we seen; which GPUs are denied). Tests can inject
// failures at any one collaborator without rebuilding the others,
// which keeps the test matrix small and orthogonal.

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// -----------------------------------------------------------------------------
// Registry
// -----------------------------------------------------------------------------

// Entry is one registered (node_id, gpu_uuid, hmac_key) tuple. The
// HMACKey is the raw secret — length is a registry-implementation
// detail (in-memory impl enforces 32 bytes minimum to match the
// reference enrollment flow). The verifier treats it as opaque
// bytes and hands it to crypto/hmac.
type Entry struct {
	NodeID  string
	GPUUUID string
	HMACKey []byte
}

// Registry is the subset of the on-chain operator registry the
// verifier needs. Implementations MUST be safe for concurrent use
// because the verifier is called from many validator goroutines.
//
// Lookup returns ErrNodeNotRegistered if the node_id was never
// enrolled, ErrNodeRevoked if the node_id was once enrolled but
// has since been revoked, and (entry, nil) on success. These are
// distinct sentinels so dashboards can tell "unknown attacker"
// apart from "legitimate operator who misbehaved and was
// deregistered."
type Registry interface {
	Lookup(nodeID string) (*Entry, error)
}

// Registry sentinels. Wrapped by the verifier with
// mining.ErrAttestationSignatureInvalid so downstream metrics can
// group all "attestation rejected" reasons together while finer
// dashboards can still errors.Is against these specific values.
var (
	ErrNodeNotRegistered = errors.New("hmac: node_id not in operator registry")
	ErrNodeRevoked       = errors.New("hmac: node_id revoked")
)

// InMemoryRegistry is the reference Registry implementation used
// by tests and by local-mode (single-validator) development
// networks. It is NOT for production — there is no persistence
// and no on-chain state synchronisation.
//
// Safe for concurrent use.
type InMemoryRegistry struct {
	mu       sync.RWMutex
	entries  map[string]*Entry
	revoked  map[string]struct{}
}

// NewInMemoryRegistry returns an empty registry ready for
// Enroll/Revoke calls.
func NewInMemoryRegistry() *InMemoryRegistry {
	return &InMemoryRegistry{
		entries: make(map[string]*Entry),
		revoked: make(map[string]struct{}),
	}
}

// Enroll registers a new (node_id, gpu_uuid, hmac_key) tuple.
// Returns an error if node_id is empty, already enrolled, or
// hmac_key is shorter than 32 bytes. Enforcing a minimum key
// length here is defence-in-depth — the enrollment transaction
// handler already validates keys, but we want a test-time stub
// that matches the production invariant.
func (r *InMemoryRegistry) Enroll(nodeID, gpuUUID string, hmacKey []byte) error {
	if nodeID == "" {
		return errors.New("hmac: Enroll requires non-empty node_id")
	}
	if gpuUUID == "" {
		return errors.New("hmac: Enroll requires non-empty gpu_uuid")
	}
	if len(hmacKey) < 32 {
		return errors.New("hmac: Enroll requires hmac_key of at least 32 bytes")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[nodeID]; exists {
		return errors.New("hmac: node_id already enrolled")
	}
	if _, revoked := r.revoked[nodeID]; revoked {
		return errors.New("hmac: node_id has a prior revocation, cannot re-enroll in this stub")
	}
	keyCopy := make([]byte, len(hmacKey))
	copy(keyCopy, hmacKey)
	r.entries[nodeID] = &Entry{
		NodeID:  nodeID,
		GPUUUID: gpuUUID,
		HMACKey: keyCopy,
	}
	return nil
}

// Revoke marks a previously-enrolled node_id as revoked. Further
// Lookup calls return ErrNodeRevoked. This is deliberately
// one-way in the in-memory stub — production re-enrollment needs
// a 30-day stake-unlock cycle (spec §5.4) which the stub does not
// model.
func (r *InMemoryRegistry) Revoke(nodeID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, nodeID)
	r.revoked[nodeID] = struct{}{}
}

// Lookup implements Registry. Returns a copy of the stored Entry
// so callers cannot mutate registry state through the returned
// pointer.
func (r *InMemoryRegistry) Lookup(nodeID string) (*Entry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, revoked := r.revoked[nodeID]; revoked {
		return nil, ErrNodeRevoked
	}
	e, ok := r.entries[nodeID]
	if !ok {
		return nil, ErrNodeNotRegistered
	}
	keyCopy := make([]byte, len(e.HMACKey))
	copy(keyCopy, e.HMACKey)
	return &Entry{
		NodeID:  e.NodeID,
		GPUUUID: e.GPUUUID,
		HMACKey: keyCopy,
	}, nil
}

// -----------------------------------------------------------------------------
// NonceStore
// -----------------------------------------------------------------------------

// NonceStore tracks which challenge nonces a given node has
// already used, over a window of 2*FRESHNESS_WINDOW (spec §6.3).
// Reusing a nonce in a second proof → reject. The store is keyed
// by (nodeID, nonce) because different operators can legitimately
// be served identical nonce bytes from different validators;
// binding to nodeID keeps the rejection scoped.
//
// Implementations MUST be safe for concurrent use.
type NonceStore interface {
	// Seen returns true iff (nodeID, nonce) was recorded within
	// the retention window AND is not yet evicted.
	Seen(nodeID string, nonce [32]byte) bool
	// Record marks (nodeID, nonce) as used at the given time.
	// Implementations evict entries older than their retention
	// window on Record / Seen calls.
	Record(nodeID string, nonce [32]byte, at time.Time)
}

// InMemoryNonceStore is the reference NonceStore. It holds
// entries for `retention`; older entries are lazily evicted on
// Seen/Record calls.
//
// Safe for concurrent use.
type InMemoryNonceStore struct {
	mu        sync.Mutex
	retention time.Duration
	seen      map[nonceKey]time.Time
}

type nonceKey struct {
	nodeID string
	nonce  [32]byte
}

// NewInMemoryNonceStore returns a NonceStore that retains
// observations for 2*FRESHNESS_WINDOW (120s at current ratified
// values). Pass a different retention for testing.
func NewInMemoryNonceStore(retention time.Duration) *InMemoryNonceStore {
	return &InMemoryNonceStore{
		retention: retention,
		seen:      make(map[nonceKey]time.Time),
	}
}

// Seen implements NonceStore. It does NOT perform eviction —
// eviction is driven by Record, which has an authoritative
// caller-supplied timestamp. Mixing time.Now() into Seen would
// break tests that pin a synthetic clock (and would leak a second
// clock source into a function whose only job is a map lookup).
func (s *InMemoryNonceStore) Seen(nodeID string, nonce [32]byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.seen[nonceKey{nodeID: nodeID, nonce: nonce}]
	return ok
}

// Record implements NonceStore. It evicts entries older than
// retention relative to `at` before inserting the new entry, so
// the map stays bounded without a background goroutine.
func (s *InMemoryNonceStore) Record(nodeID string, nonce [32]byte, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictLocked(at)
	s.seen[nonceKey{nodeID: nodeID, nonce: nonce}] = at
}

// evictLocked drops entries older than retention. Called from
// Record (which has a caller-supplied `now`) so eviction uses a
// single consistent clock source.
func (s *InMemoryNonceStore) evictLocked(now time.Time) {
	cutoff := now.Add(-s.retention)
	for k, t := range s.seen {
		if t.Before(cutoff) {
			delete(s.seen, k)
		}
	}
}

// -----------------------------------------------------------------------------
// DenyList
// -----------------------------------------------------------------------------

// DenyList reports whether a GPU name string should be rejected
// outright (spec §5.3). Used by the verifier as a governance kill
// switch for known-compromised card models. The match is
// substring-based and case-insensitive — governance adds
// human-readable fragments like "RTX 2060 driver-bypass-2025"
// rather than exact model strings.
type DenyList interface {
	Denied(gpuName string) bool
}

// EmptyDenyList never denies anything. Matches the genesis state
// per spec §5.3. Used as the default when the verifier is
// constructed without an explicit deny-list.
type EmptyDenyList struct{}

// Denied implements DenyList.
func (EmptyDenyList) Denied(string) bool { return false }

// SubstringDenyList is a simple DenyList backed by a slice of
// case-insensitive substrings. Primarily a test vehicle; in
// production the deny-list is sourced from on-chain governance
// state and wrapped behind a different implementation.
type SubstringDenyList struct {
	Substrings []string
}

// Denied implements DenyList.
func (d SubstringDenyList) Denied(gpuName string) bool {
	hay := strings.ToLower(gpuName)
	for _, s := range d.Substrings {
		if s == "" {
			continue
		}
		if strings.Contains(hay, strings.ToLower(s)) {
			return true
		}
	}
	return false
}
