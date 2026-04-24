package challenge

// Issuer is the validator-side component that mints fresh
// challenges on demand. It is separate from the HTTP handler so
// the handler stays stateless and trivially testable.
//
// The Issuer remembers the challenges it has minted for a window
// of 2 * FreshnessWindow — this is what lets a different
// validator (which did not mint this challenge) still verify its
// authenticity: every peer accepts any signature from a known
// signer-id within the freshness window, and ALSO cross-checks
// against its own nonce store if the miner re-uses a nonce
// scoped to (signer_id, nonce).

import (
	"crypto/rand"
	"errors"
	"sync"
	"time"
)

// Issuer hands out (nonce, issued_at, signature) triples. Each
// Issuer is backed by exactly one Signer; in a multi-signer setup
// (e.g. rotating keys) run multiple Issuers behind a selector.
//
// The internal store is capped by retention, so the Issuer does
// NOT grow unbounded under sustained miner traffic. Lookup cost
// is O(log n) via a map; eviction cost is O(n) in the very cold
// path when n exceeds eviction-amortisation thresholds.
//
// Concurrency: all public methods are safe for concurrent use.
type Issuer struct {
	signer    Signer
	now       func() time.Time
	retention time.Duration
	rand      func(b []byte) error

	mu    sync.Mutex
	seen  map[[32]byte]time.Time // nonce -> issuance time
}

// IssuerOption customises Issuer construction. Tests override Now
// and Rand for deterministic output; production leaves both at
// their defaults (time.Now + crypto/rand.Read).
type IssuerOption func(*Issuer)

// WithClock installs a custom clock source. Used by tests to pin
// a wall-clock time. Nil clock means time.Now.
func WithClock(clock func() time.Time) IssuerOption {
	return func(i *Issuer) {
		if clock != nil {
			i.now = clock
		}
	}
}

// WithRand installs a custom randomness source. Used by tests to
// pin deterministic nonce bytes. Nil means crypto/rand.Read.
func WithRand(r func(b []byte) error) IssuerOption {
	return func(i *Issuer) {
		if r != nil {
			i.rand = r
		}
	}
}

// WithRetention overrides the default 2 * FreshnessWindow
// retention. Zero means use the default.
func WithRetention(d time.Duration) IssuerOption {
	return func(i *Issuer) {
		if d > 0 {
			i.retention = d
		}
	}
}

// NewIssuer constructs an Issuer. The signer must be non-nil.
func NewIssuer(signer Signer, opts ...IssuerOption) (*Issuer, error) {
	if signer == nil {
		return nil, errors.New("challenge: NewIssuer requires non-nil signer")
	}
	if signer.SignerID() == "" {
		return nil, errors.New("challenge: signer has empty SignerID")
	}
	const defaultRetention = 120 * time.Second // 2 × FreshnessWindow
	i := &Issuer{
		signer:    signer,
		now:       time.Now,
		retention: defaultRetention,
		rand: func(b []byte) error {
			_, err := rand.Read(b)
			return err
		},
		seen: make(map[[32]byte]time.Time),
	}
	for _, opt := range opts {
		opt(i)
	}
	return i, nil
}

// Issue mints a fresh challenge, records it in the seen-map, and
// returns it. Repeated calls produce distinct nonces — duplicate
// nonces are statistically impossible under a 256-bit entropy
// budget but the method still dedupes defensively so a broken
// PRNG does not collapse the seen-map silently.
func (i *Issuer) Issue() (Challenge, error) {
	now := i.now()
	var nonce [32]byte
	for attempt := 0; attempt < 4; attempt++ {
		if err := i.rand(nonce[:]); err != nil {
			return Challenge{}, err
		}
		i.mu.Lock()
		i.evictLocked(now)
		if _, collision := i.seen[nonce]; !collision {
			i.seen[nonce] = now
			i.mu.Unlock()
			ch := Challenge{
				Nonce:    nonce,
				IssuedAt: now.Unix(),
				SignerID: i.signer.SignerID(),
			}
			sig, err := i.signer.Sign(ch.SigningBytes())
			if err != nil {
				// Roll back the reservation so a signer failure
				// does not waste a nonce slot.
				i.mu.Lock()
				delete(i.seen, nonce)
				i.mu.Unlock()
				return Challenge{}, err
			}
			ch.Signature = sig
			return ch, nil
		}
		i.mu.Unlock()
	}
	return Challenge{}, errors.New("challenge: PRNG produced 4 collisions in a row — giving up")
}

// Mint is an alias for Issue preserved for readability at call
// sites where "issue a challenge" reads awkwardly.
func (i *Issuer) Mint() (Challenge, error) { return i.Issue() }

// RecentlyIssued reports whether this Issuer minted (nonce, _)
// within the retention window. Used by the HTTP layer on the
// challenge-consumer side to cross-check a replayed nonce even
// within the signature-valid window. A different Issuer cannot
// answer authoritatively — it will simply return false — so
// callers should consult the Issuer that owns the relevant
// signer_id.
func (i *Issuer) RecentlyIssued(nonce [32]byte) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.evictLocked(i.now())
	_, ok := i.seen[nonce]
	return ok
}

// Retention returns the configured retention window. Tests and
// callers tuning the freshness window use this to reason about
// replay cache lifetimes.
func (i *Issuer) Retention() time.Duration { return i.retention }

// evictLocked drops entries older than retention relative to
// `now`. Caller MUST hold i.mu.
func (i *Issuer) evictLocked(now time.Time) {
	cutoff := now.Add(-i.retention)
	for k, t := range i.seen {
		if t.Before(cutoff) {
			delete(i.seen, k)
		}
	}
}
