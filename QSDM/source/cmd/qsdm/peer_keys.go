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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/telemetry"
)

// DefaultPeerProfileMaxAge is the freshness window applied
// when key pinning is on but the operator has not explicitly
// configured QSDM_PEER_ATTESTER_MAX_AGE. Chosen so a polling
// interval of 5 minutes (the default
// QSDM_PEER_ATTESTER_REFRESH) plus several missed polls and
// reasonable clock skew comfortably fit inside without
// rejecting legitimate refreshes, while still catching a
// replay attacker who serves yesterday's profile forever.
const DefaultPeerProfileMaxAge = 1 * time.Hour

// DefaultPeerProfileSkewTolerance is the symmetric clock-
// skew grace window. Allows profiles signed up to this far
// in the future and extends the past-side cutoff by the
// same amount. 5 minutes is generous enough for boxes that
// have not run NTP recently but tight enough that a real
// replay still trips the gate.
const DefaultPeerProfileSkewTolerance = 5 * time.Minute

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

	// maxAge is the freshness window. A profile whose
	// IssuedAt is older than `now - maxAge` is rejected
	// regardless of signature validity. Zero disables
	// the freshness gate (legacy posture; applies only
	// when no pin is configured OR the operator
	// explicitly sets the env var to "0").
	maxAge time.Duration

	// skewTolerance is the symmetric grace window allowed
	// in BOTH directions: a profile signed up to
	// skewTolerance seconds in the future is accepted
	// (clock-skew between attester and validator), and
	// the maxAge cutoff is extended by skewTolerance on
	// the past side too. Defaults to 5 minutes.
	skewTolerance time.Duration

	acceptedTotal    atomic.Uint64
	rejectedUnknown  atomic.Uint64
	rejectedBadSig   atomic.Uint64
	rejectedUnsigned atomic.Uint64
	rejectedStale    atomic.Uint64
	acceptedUnpinned atomic.Uint64
}

// NewPeerKeyRegistry returns an empty (no pinning) registry.
// Callers add pins via Add or LoadFromEnv. The freshness
// window is left at zero (disabled) until LoadPeerKeysFromEnv
// or SetMaxAge configures it.
func NewPeerKeyRegistry() *PeerKeyRegistry {
	return &PeerKeyRegistry{
		keys:          map[string][]byte{},
		skewTolerance: DefaultPeerProfileSkewTolerance,
	}
}

// SetMaxAge configures the freshness window. A non-zero
// value enables the gate: VerifyAndAccept rejects any
// profile whose IssuedAt is more than maxAge + skewTolerance
// seconds in the past, OR more than skewTolerance seconds in
// the future. Zero disables the gate. Negative panics.
func (r *PeerKeyRegistry) SetMaxAge(maxAge time.Duration) {
	if maxAge < 0 {
		panic("peer-keys: SetMaxAge with negative duration")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.maxAge = maxAge
}

// SetSkewTolerance overrides the default skew window.
// Mostly useful in tests; production uses the default.
func (r *PeerKeyRegistry) SetSkewTolerance(d time.Duration) {
	if d < 0 {
		panic("peer-keys: SetSkewTolerance with negative duration")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skewTolerance = d
}

// MaxAge returns the configured freshness window. Zero means
// the gate is disabled.
func (r *PeerKeyRegistry) MaxAge() time.Duration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.maxAge
}

// SkewTolerance returns the configured clock-skew grace
// window. Zero means no slack on either edge.
func (r *PeerKeyRegistry) SkewTolerance() time.Duration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.skewTolerance
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
// Decision tree (in order — first match wins):
//
//	┌─ Step 0 (signature gate, if HasPins=true) ─┐
//	│ 1. signer_id absent from registry?         │
//	│      strict=true  → rejectedUnknown        │
//	│      strict=false → acceptedUnpinned + log │
//	│ 2. profile.Signature == ""?                │
//	│      rejectedUnsigned (NEVER accept        │
//	│      unsigned when ANY pin is configured — │
//	│      that would let an attacker bypass the │
//	│      check by stripping the signature).    │
//	│ 3. profile.Verify(pinnedKey) == false?     │
//	│      rejectedBadSig.                       │
//	└────────────────────────────────────────────┘
//	┌─ Step 1 (freshness gate, if maxAge > 0) ───┐
//	│ 4. profile.IssuedAt > now + skewTolerance? │
//	│      rejectedStale (clock skew far enough  │
//	│      to look like a future-dated replay).  │
//	│ 5. profile.IssuedAt < now - (maxAge        │
//	│       + skewTolerance)?                    │
//	│      rejectedStale (replay of an old       │
//	│      profile).                             │
//	└────────────────────────────────────────────┘
//	┌─ Step 2 (terminal) ────────────────────────┐
//	│ 6. acceptedSigned (HasPins=true) OR        │
//	│    acceptedUnpinned (HasPins=false).       │
//	└────────────────────────────────────────────┘
//
// VerifyAndAccept is called from the boot wiring + the
// periodic poll, both single-goroutine paths, but the
// registry is RLocked so future fan-out is safe.
func (r *PeerKeyRegistry) VerifyAndAccept(profile *telemetry.ReferenceProfile) error {
	return r.verifyAndAcceptAt(profile, time.Now())
}

// verifyAndAcceptAt is the test seam — public callers go
// through VerifyAndAccept which fixes `now = time.Now()`.
// Tests pass a synthetic `now` so we can exercise both ends
// of the freshness window without sleep tricks.
func (r *PeerKeyRegistry) verifyAndAcceptAt(profile *telemetry.ReferenceProfile, now time.Time) error {
	if profile == nil {
		return errors.New("peer-keys: nil profile")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	hasPins := len(r.keys) > 0

	// Step 0 — signature gate. Skipped entirely when no
	// pin is configured (legacy posture).
	if hasPins {
		key, ok := r.keys[profile.SignerID]
		if !ok {
			if r.strict {
				r.rejectedUnknown.Add(1)
				return fmt.Errorf("peer-keys: unknown signer_id %q (strict mode)", profile.SignerID)
			}
			// Unknown signer in non-strict mode falls
			// through the freshness gate just like
			// the no-pin path, because the freshness
			// concern is independent of whether we
			// know who signed it.
		} else {
			if profile.Signature == "" {
				r.rejectedUnsigned.Add(1)
				return fmt.Errorf("peer-keys: profile from %q has no signature", profile.SignerID)
			}
			if !profile.Verify(key) {
				r.rejectedBadSig.Add(1)
				return fmt.Errorf("peer-keys: signature on profile from %q does not verify against pinned key", profile.SignerID)
			}
		}
	}

	// Step 1 — freshness gate. Independent of pinning so
	// an operator who wants replay protection but not
	// signature pinning gets a useful tier on its own.
	if r.maxAge > 0 {
		issued := time.Unix(profile.IssuedAt, 0)
		// Future-dated: signed-in-the-future. Allow
		// up to skewTolerance, reject anything beyond.
		if issued.After(now.Add(r.skewTolerance)) {
			r.rejectedStale.Add(1)
			return fmt.Errorf("peer-keys: profile from %q dated %s is %s in the future (max skew %s)",
				profile.SignerID,
				issued.UTC().Format(time.RFC3339),
				issued.Sub(now).Round(time.Second),
				r.skewTolerance)
		}
		// Past-dated: too old. The cutoff is maxAge
		// extended by skewTolerance so a freshly-signed
		// profile from a slow-clocked attester is not
		// rejected at the trailing edge.
		cutoff := now.Add(-(r.maxAge + r.skewTolerance))
		if issued.Before(cutoff) {
			r.rejectedStale.Add(1)
			return fmt.Errorf("peer-keys: profile from %q dated %s is %s old (max %s + skew %s)",
				profile.SignerID,
				issued.UTC().Format(time.RFC3339),
				now.Sub(issued).Round(time.Second),
				r.maxAge,
				r.skewTolerance)
		}
	}

	// Step 2 — terminal. Bucketed so the operator can
	// distinguish "accepted because we verified it" from
	// "accepted because we don't know who signed it".
	if hasPins {
		if _, known := r.keys[profile.SignerID]; known {
			r.acceptedTotal.Add(1)
			return nil
		}
	}
	r.acceptedUnpinned.Add(1)
	return nil
}

// Counters returns the cumulative verification outcome
// counts in
// (accepted, accepted_unpinned, rejected_unknown,
// rejected_unsigned, rejected_bad_sig, rejected_stale)
// order. Read-only; safe for concurrent calls.
func (r *PeerKeyRegistry) Counters() (uint64, uint64, uint64, uint64, uint64, uint64) {
	return r.acceptedTotal.Load(),
		r.acceptedUnpinned.Load(),
		r.rejectedUnknown.Load(),
		r.rejectedUnsigned.Load(),
		r.rejectedBadSig.Load(),
		r.rejectedStale.Load()
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

		// Default freshness window kicks in once any pin
		// is configured — same trigger as strict mode,
		// because both are part of the "I'm taking trust
		// seriously" posture. Explicit env var override
		// takes precedence; "0" disables the gate.
		maxAge, err := resolvePeerProfileMaxAge()
		if err != nil {
			return nil, 0, err
		}
		reg.SetMaxAge(maxAge)

		// Skew tolerance: same env-var grammar as maxAge
		// (Go duration literal OR bare seconds). Empty =
		// package default (5 minutes); explicit "0"
		// disables clock-skew slack so an operator with
		// tight time sync can detect even small replays
		// at the trailing edge.
		skew, err := resolvePeerProfileSkew()
		if err != nil {
			return nil, 0, err
		}
		reg.SetSkewTolerance(skew)
	}
	return reg, added, nil
}

// resolvePeerProfileSkew consults QSDM_PEER_ATTESTER_SKEW
// using the same grammar as MAX_AGE. Empty / unset returns
// DefaultPeerProfileSkewTolerance.
func resolvePeerProfileSkew() (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv("QSDM_PEER_ATTESTER_SKEW"))
	if raw == "" {
		return DefaultPeerProfileSkewTolerance, nil
	}
	switch strings.ToLower(raw) {
	case "0", "off", "false", "no":
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		if secs, secErr := strconv.Atoi(raw); secErr == nil && secs >= 0 {
			return time.Duration(secs) * time.Second, nil
		}
		return 0, fmt.Errorf("peer-keys: QSDM_PEER_ATTESTER_SKEW=%q parse: %w", raw, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("peer-keys: QSDM_PEER_ATTESTER_SKEW=%q must be non-negative", raw)
	}
	return d, nil
}

// resolvePeerProfileMaxAge consults QSDM_PEER_ATTESTER_MAX_AGE
// (a Go duration literal: "1h", "30m", "0", "12h"). Empty /
// unset returns the package default. "0" / "off" / "false"
// disables the freshness gate explicitly.
func resolvePeerProfileMaxAge() (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv("QSDM_PEER_ATTESTER_MAX_AGE"))
	if raw == "" {
		return DefaultPeerProfileMaxAge, nil
	}
	switch strings.ToLower(raw) {
	case "0", "off", "false", "no":
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		// Fallback: also accept a bare integer interpreted
		// as seconds, since systemd Environment= lines
		// often carry "3600" rather than "1h".
		if secs, secErr := strconv.Atoi(raw); secErr == nil && secs >= 0 {
			return time.Duration(secs) * time.Second, nil
		}
		return 0, fmt.Errorf("peer-keys: QSDM_PEER_ATTESTER_MAX_AGE=%q parse: %w", raw, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("peer-keys: QSDM_PEER_ATTESTER_MAX_AGE=%q must be non-negative", raw)
	}
	return d, nil
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
