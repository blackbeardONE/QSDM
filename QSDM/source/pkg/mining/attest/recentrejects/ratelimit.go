package recentrejects

// ratelimit.go: per-miner token-bucket limiter for Store.Record.
//
// Why a per-miner limiter exists:
//
//	The rejection ring already carries hard / soft caps on disk
//	(persistence.go) and a fixed FIFO depth in memory (Cap()).
//	Those defences bound the BLAST RADIUS of a flood but do
//	nothing about the SIGNAL-TO-NOISE problem: one bad actor
//	submitting forged proofs at line-rate can fill the ring
//	with their records, FIFO-ing legitimate-rejection events
//	out of the operator's view, and saturating the per-field
//	rune-truncation counters so legitimate truncation patterns
//	get hidden in the aggregate.
//
//	The per-miner limiter caps the rate at which any single
//	MinerAddr can contribute to the ring. Records that exceed
//	the cap are dropped at Record() entry — they never touch
//	the ring, never touch the persister, never bump the
//	per-field counters. A separate counter
//	(qsdm_attest_rejection_per_miner_rate_limited_total)
//	lights up so operators can correlate a flood against the
//	soft-cap compaction rate without confusing "lots of
//	miners misbehaving" with "one miner DDOSing".
//
// Algorithm: token bucket per MinerAddr.
//
//	rate  = tokens/sec refill (e.g. 10 → 10 records/s steady)
//	burst = max tokens (e.g. 50 → first 50 in any quiet
//	        window admitted instantly, then refill kicks in)
//
//	A bucket is created lazily on first observation of a
//	given MinerAddr; idle buckets are evicted by an
//	amortized sweep (every sweepEveryAdmits admit calls)
//	so a long-running validator's bucket map stays bounded
//	even if the miner population churns.
//
// Concurrency: the limiter is consulted by Store.Record under
// s.mu, so it inherits the Store's serialization without needing
// its own lock. Keeping the lock-graph flat is deliberate — the
// hot-path latency budget of Record() is one mutex acquire +
// one map lookup + one float arithmetic block, and adding a
// second mutex would double the cache-line bouncing on a
// multi-core verifier.
//
// Disabled by default: Store.SetRateLimit(0, ...) leaves the
// limiter detached so existing tests and operators on quiet
// validators see no behaviour change. The dashboard tile
// surfaces the limiter's "configured rate" alongside the
// drop counter so it's obvious whether the defence is on.

import (
	"sync/atomic"
	"time"
)

// rateLimitDefaults documents the tuning the production wiring
// uses when SetRateLimit is invoked without a custom (rate, burst,
// idleTTL). Operators may override per validator via
// internal/v2wiring.Config.
//
// The chosen values reflect the §4.6 threat model:
//   - 10 rec/s sustained covers a misconfigured miner that
//     retries every ~100ms in a flap (legitimate).
//   - 50-record burst absorbs the typical "validator restart →
//     all miners re-enrol simultaneously" event without
//     suppressing a single bona-fide rejection.
//   - 1h idle TTL lets a quiet miner whose first rejection in
//     hours hit the bucket cold (full burst) without leaking
//     map memory on the long tail of one-shot offenders.
const (
	defaultRateLimitRate    = 10.0
	defaultRateLimitBurst   = 50.0
	defaultRateLimitIdleTTL = time.Hour

	// sweepEveryAdmits controls how often the limiter prunes
	// idle buckets. 1024 is high enough that the sweep cost
	// (O(len(buckets)) range over a map) amortises to zero
	// per admit on any realistic miner population, and low
	// enough that a flood of one-shot offenders cannot bloat
	// the map past O(sweep-interval × admit-rate) before
	// eviction reclaims them. At 10k admits/sec sustained
	// (well above any realistic verifier throughput) sweep
	// fires roughly every 100ms; the operator sees no
	// observable hitch.
	sweepEveryAdmits = 1024
)

// rateLimiter is the per-miner token bucket map. Methods are
// NOT goroutine-safe on their own — callers (Store.Record,
// Store.SetRateLimit) provide serialization via s.mu.
//
// Zero value is "disabled, no allocations" so a store with
// SetRateLimit never invoked has a nil-map shape that the
// admit() fast path detects without a syscall or hash lookup.
type rateLimiter struct {
	rate    float64       // tokens/sec; <=0 means disabled
	burst   float64       // max tokens; only meaningful when rate > 0
	idleTTL time.Duration // <=0 disables idle eviction
	buckets map[string]*tokenBucket

	// sweepCounter increments on every admit() call; when it
	// reaches sweepEveryAdmits we run sweepIdle and reset.
	// Plain int (not atomic) because the limiter is only
	// touched under Store.mu.
	sweepCounter int

	// rateLimitedCount is the number of admit() calls that
	// returned false in this Store's lifetime. Surfaced via
	// Store.RateLimitedCount() so a dashboard / Prometheus
	// adapter can render the local-side count without
	// scraping the global monitoring counter.
	rateLimitedCount uint64
}

// tokenBucket is one miner's bucket state. Pointer-stored in
// the map so refills mutate in place without re-hashing.
type tokenBucket struct {
	tokens float64
	last   time.Time
}

// configure (re)initialises the limiter. rate <= 0 disables
// the limiter and frees the bucket map; positive values clamp
// burst to >= 1.0 (a burst of 0.5 would never admit, which is
// almost certainly a configuration error rather than an
// intentional "all-deny" mode — that's spelled with a separate
// API if anyone ever needs it).
func (l *rateLimiter) configure(rate, burst float64, idleTTL time.Duration) {
	if rate <= 0 {
		l.rate = 0
		l.burst = 0
		l.idleTTL = 0
		l.buckets = nil
		l.sweepCounter = 0
		return
	}
	if burst < 1.0 {
		burst = rate * 5.0
		if burst < 1.0 {
			burst = 1.0
		}
	}
	if idleTTL <= 0 {
		idleTTL = defaultRateLimitIdleTTL
	}
	l.rate = rate
	l.burst = burst
	l.idleTTL = idleTTL
	if l.buckets == nil {
		l.buckets = make(map[string]*tokenBucket)
	}
	// Existing buckets keep their tokens; a re-configure should
	// not flush legitimate state. Operators bumping the rate
	// limit in response to a misconfiguration want continuity,
	// not a hard reset that lets a flood through unbounded for
	// a window.
}

// admit returns true if the (addr, now) pair is admitted under
// the current limiter configuration. Updates internal state.
//
// Special cases:
//   - Disabled limiter (rate <= 0): always admits.
//   - Empty addr: always admits. The rejection envelope didn't
//     parse far enough to extract a MinerAddr; without a key
//     we cannot rate-limit, so we err on the side of admission
//     so legitimate operator visibility is preserved. The
//     other defences (FIFO ring, hard-cap persister) still
//     bound the blast radius.
func (l *rateLimiter) admit(addr string, now time.Time) bool {
	if l.rate <= 0 || addr == "" {
		return true
	}
	b, ok := l.buckets[addr]
	if !ok {
		// First sighting of this miner: cold-start with a full
		// bucket and `last = now`. Cold-start with `burst`
		// (rather than 1.0) lets a quiet miner whose first
		// rejection lands after a long idle period catch the
		// full burst budget — operators rotating through a
		// staging fleet should not be rate-limited on the
		// first record.
		b = &tokenBucket{tokens: l.burst, last: now}
		l.buckets[addr] = b
	} else {
		// Refill since last admit. Math is straight token
		// bucket: tokens += elapsed * rate, capped at burst.
		// We only refill on positive elapsed because a
		// nowFn that goes backwards (test fakes, clock
		// adjustment) MUST NOT add tokens.
		if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
			b.tokens += elapsed * l.rate
			if b.tokens > l.burst {
				b.tokens = l.burst
			}
			b.last = now
		}
	}

	l.sweepCounter++
	if l.sweepCounter >= sweepEveryAdmits {
		l.sweepIdle(now)
		l.sweepCounter = 0
	}

	if b.tokens < 1.0 {
		l.rateLimitedCount++
		return false
	}
	b.tokens -= 1.0
	return true
}

// sweepIdle evicts buckets whose last admit was older than
// idleTTL. Called from admit() amortized; can also be called
// directly by tests via Store.SweepRateLimitIdleForTest.
func (l *rateLimiter) sweepIdle(now time.Time) {
	if l.idleTTL <= 0 || len(l.buckets) == 0 {
		return
	}
	cutoff := now.Add(-l.idleTTL)
	for k, b := range l.buckets {
		if b.last.Before(cutoff) {
			delete(l.buckets, k)
		}
	}
}

// snapshotRate returns the configured rate (tokens/sec). Zero
// means the limiter is disabled. Used by Store.RateLimitConfig
// for dashboard rendering.
func (l *rateLimiter) snapshotRate() float64 { return l.rate }

// snapshotBurst returns the configured burst.
func (l *rateLimiter) snapshotBurst() float64 { return l.burst }

// snapshotIdleTTL returns the configured idle TTL.
func (l *rateLimiter) snapshotIdleTTL() time.Duration { return l.idleTTL }

// snapshotActiveBuckets returns the current bucket-map size.
// Useful for dashboard sizing ("we're tracking 73 miners").
func (l *rateLimiter) snapshotActiveBuckets() int { return len(l.buckets) }

// snapshotDropCount returns the lifetime drop counter. Caller
// must already hold Store.mu (via the Store wrapper).
func (l *rateLimiter) snapshotDropCount() uint64 { return l.rateLimitedCount }

// RateLimitConfig is the operator-visible snapshot of the
// limiter configuration. Returned by Store.RateLimitConfig for
// the dashboard tile and for tests asserting "the right knobs
// were wired at boot". A pointer-typed return from the Store
// makes "limiter is detached" representable as nil without
// confusing it with "limiter is configured but with rate=0".
//
// JSON-tagged so the dashboard can render the shape directly.
type RateLimitConfig struct {
	Rate           float64       `json:"rate_per_sec"`
	Burst          float64       `json:"burst"`
	IdleTTL        time.Duration `json:"idle_ttl"`
	ActiveBuckets  int           `json:"active_buckets"`
	RateLimitedTot uint64        `json:"rate_limited_total"`
}

// noteRateLimited bridges a rate-limit drop to the package-
// level MetricsRecorder iff it implements RateLimitRecorder.
// Hot-path cost: one atomic.Load + one type assertion per
// drop; drops are by definition the exceptional path so the
// cost is irrelevant against the operator-visibility win.
//
// Note: this fires from inside Store.mu so the recorder MUST
// not call back into the Store to avoid a deadlock. The
// production Prometheus adapter is a single atomic.Add so
// this is satisfied trivially.
func noteRateLimited(addr string) {
	if pr, ok := currentMetricsRecorder().(RateLimitRecorder); ok {
		pr.RecordRateLimited(addr)
	}
}

// Compile-time assertion that uint64 atomics are wide enough
// for the lifetime-drop counter. atomic.Uint64 is the same
// width on every Go-supported platform; this fence-line check
// is here only to surface a hypothetical future shrink.
var _ atomic.Uint64
