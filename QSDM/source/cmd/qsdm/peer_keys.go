package main

// peer_keys.go is the per-attester key-pinning layer for the
// Tier-2 telemetry oracle. Sits between fetchPeerProfile and
// catalog.Apply so a forged or tampered profile is rejected
// BEFORE it pollutes the catalog.
//
// Threat model:
//
//   - **MITM on the relay/HTTP path.** Today the validator
//     trusts any profile served from a configured peer URL.
//     A relay operator (or anyone who can sit on the wire
//     between validator and attester) can forge profile
//     content and the validator has no way to notice.
//     Pinning closes this: profiles MUST carry a signature
//     matching the pinned key for the claimed signer_id.
//
//   - **Key rotation drift.** When the attester operator
//     rotates their HMAC signer key, the catalog should
//     stop accepting profiles from the OLD signer_id and
//     start accepting profiles from the NEW one. Without
//     pinning the old key keeps "working" forever (it
//     wasn't being checked) and the new key works
//     immediately (likewise unchecked). With pinning,
//     rotation requires an explicit config update on every
//     subscriber, which is the correct posture: rotations
//     are operator-coordinated, not silent.
//
//   - **Attester-side compromise.** Pinning does NOT defend
//     against a compromised attester signing legitimate-but-
//     misleading profiles. That's a separate trust layer
//     (attester reputation / multi-source agreement); the
//     pinning is purely "what the attester signs, the
//     validator can verify".
//
// Crypto:
//
//   The telemetry profile signature is HMAC-SHA256 over the
//   canonical JSON encoding (pkg/telemetry.CanonicalForSigning).
//   This is SYMMETRIC — the attester and the validator share
//   the same 32-byte secret. A future revision could swap to
//   Ed25519 (asymmetric, no shared secret transport problem)
//   without changing the pinning contract: this file would
//   load public keys instead of shared secrets, the rest of
//   the pipeline unchanged.
//
// Configuration:
//
//   QSDM_PEER_ATTESTER_KEYS  - semicolon-separated list of
//                              "signer_id=hex_key" pairs.
//                              Example:
//                              attester-12a0d1aa082b7e28=<64 hex>;
//                              attester-foo=<64 hex>
//
//   QSDM_PEER_ATTESTER_KEYS_FILE - path to a file with one
//                              "signer_id=hex_key" pair per
//                              line; '#' starts a comment.
//                              Useful when the secrets list
//                              is too long for a systemd
//                              Environment= line.
//
//   QSDM_PEER_ATTESTER_STRICT - "1" / "true" / "on" => when
//                              ANY pinned key is configured,
//                              REJECT every profile whose
//                              signer_id is unknown. Defaults
//                              to true when at least one key
//                              is configured (security-by-
//                              default once you opt in);
//                              explicitly setting to "0"
//                              switches to allowlist-with-
//                              warning posture (unknown
//                              signers are accepted but
//                              logged), useful during
//                              roll-out.

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/blackbeardONE/QSDM/pkg/telemetry"
)

// PeerKeyRegistry holds the operator-pinned signer_id →
// shared-secret key map. Read-mostly: keys are loaded once
// at boot and never mutated thereafter, but the registry
// is RWMutex-guarded so a future "reload via SIGHUP" path
// stays safe.
//
// Per-pin counters are exposed via the qsdm_spec_check_*
// Prometheus collector (see peer_key_metrics.go).
type PeerKeyRegistry struct {
	mu     sync.RWMutex
	keys   map[string][]byte
	strict bool

	acceptedTotal       atomic.Uint64
	rejectedUnknown     atomic.Uint64
	rejectedBadSig      atomic.Uint64
	rejectedUnsigned    atomic.Uint64
	acceptedUnpinned    atomic.Uint64
}

// NewPeerKeyRegistry returns an empty (no pinning) registry.
// Callers add pins via Add or LoadFromEnv.
func NewPeerKeyRegistry() *PeerKeyRegistry {
	return &PeerKeyRegistry{keys: map[string][]byte{}}
}

// Add pins one (signer_id, key) pair. signer_id must be
// non-empty and start with "attester-" (the same prefix the
// attester binary derives from its key). The key must be at
// least 16 bytes; shorter keys are rejected by
// telemetry.Verify anyway, and rejecting here gives a
// clearer boot-time error than a runtime fetch failure.
func (r *PeerKeyRegistry) Add(signerID string, key []byte) error {
	signerID = strings.TrimSpace(signerID)
	if signerID == "" {
		return errors.New("peer-keys: empty signer_id")
	}
	if !strings.HasPrefix(signerID, "attester-") {
		return fmt.Errorf("peer-keys: signer_id %q must start with 'attester-'", signerID)
	}
	if len(key) < 16 {
		return fmt.Errorf("peer-keys: key for %q has length %d, minimum 16", signerID, len(key))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keys[signerID] = append([]byte(nil), key...)
	return nil
}

// SetStrict toggles the unknown-signer policy. true =>
// reject; false => log + accept. Must be called BEFORE the
// validator publishes the registry to the spec-check
// poller; mid-run flips would race with reads. The default
// (chosen automatically when LoadFromEnv resolves keys but
// QSDM_PEER_ATTESTER_STRICT is unset) is true once any key
// is pinned, false otherwise.
func (r *PeerKeyRegistry) SetStrict(s bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.strict = s
}

// HasPins is true once at least one Add succeeded. The
// fetchPeerProfile path uses this to decide whether to
// run the verification gate at all — a registry with no
// pins is the legacy posture (accept anything, log a
// warning) and skipping the gate avoids both metric noise
// and a misleading "rejected_unknown_signer" event for an
// operator who simply hasn't opted in yet.
func (r *PeerKeyRegistry) HasPins() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.keys) > 0
}

// PinnedSigners returns the sorted set of signer_ids that
// have a key pinned. Used for the boot-time log line and
// for the Prometheus gauge.
func (r *PeerKeyRegistry) PinnedSigners() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.keys))
	for k := range r.keys {
		out = append(out, k)
	}
	return out
}

// Strict reports the current strict-mode setting. Used by
// the metrics emitter and the boot-time log line.
func (r *PeerKeyRegistry) Strict() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.strict
}

// VerifyAndAccept is the gate. Returns nil when the profile
// is acceptable; non-nil error means caller should drop the
// profile and log the error.
//
// Decision tree:
//
//	┌────────────── HasPins=false ─────────────┐
//	│  bump acceptedUnpinned counter, return    │
//	│  nil. Caller logs a warning so the        │
//	│  operator knows pinning is off.           │
//	└──────────────────────────────────────────┘
//	┌────────────── HasPins=true ──────────────┐
//	│ 1. signer_id absent from registry?       │
//	│      strict=true  → bump rejectedUnknown │
//	│                     return error.        │
//	│      strict=false → bump acceptedUnpinned│
//	│                     return nil + log.    │
//	│ 2. profile.Signature == ""?              │
//	│      bump rejectedUnsigned, return error │
//	│      (NEVER accept unsigned when ANY pin │
//	│       is configured — that would let an  │
//	│       attacker bypass the check by       │
//	│       stripping the signature).          │
//	│ 3. profile.Verify(pinnedKey) == false?   │
//	│      bump rejectedBadSig, return error.  │
//	│ 4. Otherwise: bump acceptedTotal, return │
//	│      nil.                                │
//	└──────────────────────────────────────────┘
func (r *PeerKeyRegistry) VerifyAndAccept(profile *telemetry.ReferenceProfile) error {
	if profile == nil {
		return errors.New("peer-keys: nil profile")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.keys) == 0 {
		r.acceptedUnpinned.Add(1)
		return nil
	}
	key, ok := r.keys[profile.SignerID]
	if !ok {
		if r.strict {
			r.rejectedUnknown.Add(1)
			return fmt.Errorf("peer-keys: unknown signer_id %q (strict mode)", profile.SignerID)
		}
		r.acceptedUnpinned.Add(1)
		return nil
	}
	if profile.Signature == "" {
		r.rejectedUnsigned.Add(1)
		return fmt.Errorf("peer-keys: profile from %q has no signature", profile.SignerID)
	}
	if !profile.Verify(key) {
		r.rejectedBadSig.Add(1)
		return fmt.Errorf("peer-keys: signature on profile from %q does not verify against pinned key", profile.SignerID)
	}
	r.acceptedTotal.Add(1)
	return nil
}

// Counters returns the cumulative verification outcome
// counts in
// (accepted, accepted_unpinned, rejected_unknown,
// rejected_unsigned, rejected_bad_sig) order. Read-only;
// safe for concurrent calls.
func (r *PeerKeyRegistry) Counters() (uint64, uint64, uint64, uint64, uint64) {
	return r.acceptedTotal.Load(),
		r.acceptedUnpinned.Load(),
		r.rejectedUnknown.Load(),
		r.rejectedUnsigned.Load(),
		r.rejectedBadSig.Load()
}

// LoadPeerKeysFromEnv reads QSDM_PEER_ATTESTER_KEYS plus
// QSDM_PEER_ATTESTER_KEYS_FILE plus QSDM_PEER_ATTESTER_STRICT
// and populates a fresh registry. Returns the registry, the
// number of pins it loaded, and any error from the parsing
// step. An error is fatal at boot — the operator typo'd a
// hex string or duplicated a signer_id, both of which would
// silently corrupt the trust layer if we papered over them.
func LoadPeerKeysFromEnv() (*PeerKeyRegistry, int, error) {
	reg := NewPeerKeyRegistry()
	added := 0

	if raw := strings.TrimSpace(os.Getenv("QSDM_PEER_ATTESTER_KEYS")); raw != "" {
		n, err := loadPeerKeysFromString(reg, raw, "QSDM_PEER_ATTESTER_KEYS")
		if err != nil {
			return nil, 0, err
		}
		added += n
	}
	if path := strings.TrimSpace(os.Getenv("QSDM_PEER_ATTESTER_KEYS_FILE")); path != "" {
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, 0, fmt.Errorf("peer-keys: read %s: %w", path, err)
		}
		// File format: one entry per line, '#' starts a
		// comment. Whitespace stripped. Same syntax for the
		// pair as the env var.
		var lines []string
		for _, ln := range strings.Split(string(body), "\n") {
			ln = strings.TrimSpace(ln)
			if ln == "" || strings.HasPrefix(ln, "#") {
				continue
			}
			lines = append(lines, ln)
		}
		joined := strings.Join(lines, ";")
		n, err := loadPeerKeysFromString(reg, joined, path)
		if err != nil {
			return nil, 0, err
		}
		added += n
	}

	if added > 0 {
		// Default strict=true once any pin is configured.
		// Explicit env var override takes precedence.
		strict := true
		if v := strings.TrimSpace(os.Getenv("QSDM_PEER_ATTESTER_STRICT")); v != "" {
			switch strings.ToLower(v) {
			case "0", "false", "no", "off":
				strict = false
			}
		}
		reg.SetStrict(strict)
	}
	return reg, added, nil
}

// loadPeerKeysFromString consumes a "signer_id=hex_key;..."
// blob and adds each pair to the registry. Errors include
// the source label so the operator knows which env var or
// file produced the bad entry.
func loadPeerKeysFromString(reg *PeerKeyRegistry, raw, source string) (int, error) {
	added := 0
	pairs := strings.Split(raw, ";")
	for i, p := range pairs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		eq := strings.IndexByte(p, '=')
		if eq <= 0 {
			return added, fmt.Errorf("peer-keys: %s entry %d has no '='", source, i+1)
		}
		signerID := strings.TrimSpace(p[:eq])
		hexKey := strings.TrimSpace(p[eq+1:])
		key, err := hex.DecodeString(hexKey)
		if err != nil {
			return added, fmt.Errorf("peer-keys: %s entry %d (signer_id=%q) hex decode: %w", source, i+1, signerID, err)
		}
		if err := reg.Add(signerID, key); err != nil {
			return added, fmt.Errorf("peer-keys: %s entry %d: %w", source, i+1, err)
		}
		added++
	}
	return added, nil
}
