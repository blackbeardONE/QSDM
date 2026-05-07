package telemetrycheck

// penalty.go is the Tier-3 layer that converts the
// advisory verdict stream from the Tier-2 checker into a
// per-miner reward multiplier. Sits one layer above the
// Checker: every Verdict the checker emits is fed into a
// PerMinerStats sliding window keyed on miner_addr, and
// reward-time queries against that window return a
// multiplier in [0.0, 1.0] that the blockdriver applies
// to the miner's share of the next sealed block.
//
// Design choices and their rationale:
//
//   1. PER-MINER WINDOW, NOT GLOBAL THRESHOLD. The
//      threshold compares each miner's recent mismatch
//      ratio against a governance constant. A globally-
//      thresholded "5% of all proofs are mismatches"
//      would punish honest miners alongside a single bad
//      actor; per-miner isolation keeps the penalty
//      local to the source of the noise.
//
//   2. PROOF-COUNT WINDOW, NOT TIME WINDOW. A spoofer
//      that mismatches 1000 times then goes silent
//      should keep facing the penalty until they prove
//      themselves clean over their next 1000 proofs.
//      Time-based decay would let a high-volume burst
//      mode rotate through penalty cooldowns; proof-
//      count keeps the cost commensurate with how much
//      noise the actor produced.
//
//   3. ONLY MAJOR MISMATCHES COUNT. The Verdict's
//      Mismatches[].Severity field is consulted —
//      `minor` rules (driver_ver_format) increment the
//      window count but don't increment the mismatch
//      count. Punishing a malformed driver string the
//      same as a "RTX 3050 with CC 9.0" lie would be
//      both noisier and harder to defend politically.
//
//   4. UNKNOWN_SKU IS NEUTRAL. A miner running a brand-
//      new GPU is not a bad actor — they are just an
//      early adopter. Until peer attesters publish a
//      profile for the SKU, the catalog has no opinion;
//      the right answer is "wait and see," not "throttle
//      their rewards."
//
//   5. SINGLE-STEP MULTIPLIER. The penalty is binary:
//      below threshold → 1.0 (full reward), at or
//      above threshold → MismatchPenaltyMultiplier
//      (default 0.75). A graduated step (e.g. 0.9 at
//      5%, 0.7 at 15%, 0.5 at 30%) is conceivable but
//      the binary form is easier to predict, audit, and
//      explain to a miner who got flagged. Future
//      governance can refine the curve once we have
//      production data on the noise floor.
//
// Concurrency: PerMinerStats uses one RWMutex per
// instance plus per-miner sub-locks to keep contention
// down. Hot path (Update + Multiplier query) takes
// at most one short-held write lock. Pure reads from
// the metrics emitter are also wait-free.

import (
	"sort"
	"sync"
	"time"
)

// PenaltyConfig configures the Tier-3 penalty engine.
// Zero values resolve to safe defaults via Resolve();
// callers SHOULD always go through Resolve() rather
// than reading fields directly so a future addition
// (e.g. graduated steps) doesn't silently no-op when
// the operator forgot to set it.
type PenaltyConfig struct {
	// WindowSize is the number of recent proofs per
	// miner the threshold is computed over. Zero → 1000.
	// MUST be >= 10 (otherwise a single mismatch
	// instantly trips the threshold; the "warmup"
	// would be too coarse).
	WindowSize int

	// MismatchThresholdPct is the major-mismatch ratio
	// (mismatches / window_size, *100) at or above
	// which the penalty kicks in. Zero → 10.0. MUST be
	// in (0.0, 100.0]. Values outside that range
	// resolve to the default.
	MismatchThresholdPct float64

	// PenaltyMultiplier is the share-multiplier
	// applied when the miner is over-threshold. Zero →
	// 0.75 (=25% reward downgrade). MUST be in
	// (0.0, 1.0]. A value of 0.0 would zero the reward,
	// which is functionally a hard slash and not the
	// posture this layer represents — values <= 0 are
	// clamped to the default.
	PenaltyMultiplier float64

	// MinObservations is the minimum number of proofs
	// in a miner's window before any penalty can fire.
	// Below this count, Multiplier returns 1.0
	// regardless of mismatches. Protects miners
	// against an unlucky early-warmup mismatch
	// dominating their early ratio. Zero → 50.
	MinObservations int
}

// PenaltyConfigDefaults are the canonical defaults.
// Exposed as a constant block so the validator's
// /info / /metrics output can echo them verbatim.
const (
	DefaultPenaltyWindowSize          = 1000
	DefaultPenaltyMismatchThresholdPct = 10.0
	DefaultPenaltyMultiplier          = 0.75
	DefaultPenaltyMinObservations     = 50
)

// Resolve returns a PenaltyConfig with all zero / out-
// of-range fields filled in from the package defaults.
// Idempotent: Resolve(Resolve(c)) == Resolve(c).
func (c PenaltyConfig) Resolve() PenaltyConfig {
	out := c
	if out.WindowSize < 10 {
		out.WindowSize = DefaultPenaltyWindowSize
	}
	if out.MismatchThresholdPct <= 0 || out.MismatchThresholdPct > 100 {
		out.MismatchThresholdPct = DefaultPenaltyMismatchThresholdPct
	}
	if out.PenaltyMultiplier <= 0 || out.PenaltyMultiplier > 1 {
		out.PenaltyMultiplier = DefaultPenaltyMultiplier
	}
	if out.MinObservations < 1 {
		out.MinObservations = DefaultPenaltyMinObservations
	}
	return out
}

// MismatchPenalty is the contract the blockdriver
// queries at reward-time. Implementations MUST be
// concurrency-safe and MUST NOT block on I/O — the
// blockdriver's tick() iterates the drained queue
// in O(n_miners) holding the queue lock briefly, and
// a slow Multiplier call would be a denial-of-service
// vector against block production.
//
// MultiplierFor returns the share multiplier for
// minerAddr in [0.0, 1.0]:
//
//   - 1.0  → no penalty (the common case)
//   - <1.0 → reduce reward share by this factor.
//
// The companion Snapshot returns the per-miner state
// (window size, mismatch count, computed ratio,
// current multiplier) so the operator endpoint
// /api/v1/mining/account can explain to a flagged
// miner exactly why their rewards dropped.
type MismatchPenalty interface {
	MultiplierFor(minerAddr string) float64
	Snapshot(minerAddr string) PenaltySnapshot
	AllMiners() []string
}

// PenaltySnapshot is the per-miner explanation. JSON
// tag names below are the public contract; renaming
// them is a breaking change for the dashboard tile
// and the /api/v1/mining/account response.
type PenaltySnapshot struct {
	MinerAddr        string  `json:"miner_addr"`
	WindowSize       int     `json:"window_size"`
	WindowFilled     int     `json:"window_filled"`
	MismatchCount    int     `json:"mismatch_count"`
	UnknownSKUCount  int     `json:"unknown_sku_count"`
	MatchCount       int     `json:"match_count"`
	MismatchPct      float64 `json:"mismatch_pct"`
	ThresholdPct     float64 `json:"threshold_pct"`
	OverThreshold    bool    `json:"over_threshold"`
	BelowMinObs      bool    `json:"below_min_observations"`
	Multiplier       float64 `json:"multiplier"`
	LastObservedAt   int64   `json:"last_observed_at,omitempty"`
}

// PerMinerStats is the concrete MismatchPenalty
// implementation backed by an in-memory sliding
// window per miner. Construct via NewPerMinerStats;
// the zero value is NOT usable.
type PerMinerStats struct {
	cfg PenaltyConfig

	mu      sync.RWMutex
	miners  map[string]*minerWindow
}

// minerWindow is the per-miner ring buffer. Holds the
// last cfg.WindowSize verdict kinds in insertion
// order. Atomic counts let Multiplier compute the
// ratio without scanning the buffer.
type minerWindow struct {
	mu sync.Mutex

	// kinds is a byte-coded ring of VerdictKinds:
	//   0 = match, 1 = mismatch_major, 2 = mismatch_minor,
	//   3 = unknown_sku, 4 = skipped
	// Encoded as bytes (not strings) to keep memory
	// per miner bounded — at WindowSize=1000 this is
	// ~1KB per active miner.
	kinds []byte

	// head points at the slot AFTER the newest entry.
	head int
	// size is min(len(kinds), cfg.WindowSize).
	size int

	matches      int
	mismatches   int // major-only
	unknownSKUs  int
	minorOnly    int // mismatch with NO major fields
	skipped      int

	lastObservedAt int64
}

// verdict kinds → bytes
const (
	winMatch         = byte(0)
	winMajorMismatch = byte(1)
	winMinorMismatch = byte(2)
	winUnknownSKU    = byte(3)
	winSkipped       = byte(4)
)

// NewPerMinerStats returns a ready-to-use sliding-
// window penalty engine bound to cfg (resolved via
// PenaltyConfig.Resolve). The returned value is safe
// for concurrent use.
func NewPerMinerStats(cfg PenaltyConfig) *PerMinerStats {
	return &PerMinerStats{
		cfg:    cfg.Resolve(),
		miners: make(map[string]*minerWindow, 16),
	}
}

// Config returns a copy of the resolved config.
// Useful for /info echoes.
func (p *PerMinerStats) Config() PenaltyConfig {
	return p.cfg
}

// Update folds one Verdict into the per-miner
// window. Called from the HMACAdapter on every
// accepted v2 proof's verdict. minerAddr is the proof's
// miner_addr field; an empty string is silently
// dropped (no useful identity to track).
//
// `now` is taken as a parameter rather than time.Now()
// for testability and so the test suite can drive
// deterministic time without a clock interface.
func (p *PerMinerStats) Update(minerAddr string, v Verdict, now time.Time) {
	if minerAddr == "" {
		return
	}
	w := p.getOrCreate(minerAddr)
	kind := classifyVerdict(v)
	w.push(kind, now.Unix(), p.cfg.WindowSize)
}

// MultiplierFor returns the reward share multiplier
// for minerAddr. Hot path: blockdriver calls this once
// per miner per sealed block.
func (p *PerMinerStats) MultiplierFor(minerAddr string) float64 {
	if minerAddr == "" {
		return 1.0
	}
	p.mu.RLock()
	w, ok := p.miners[minerAddr]
	p.mu.RUnlock()
	if !ok {
		return 1.0
	}
	return p.computeMultiplier(w)
}

// Snapshot returns the explanatory state for one
// miner. If the miner has no recorded verdicts, returns
// a snapshot with zero counts and Multiplier=1.0.
func (p *PerMinerStats) Snapshot(minerAddr string) PenaltySnapshot {
	if minerAddr == "" {
		return PenaltySnapshot{Multiplier: 1.0, ThresholdPct: p.cfg.MismatchThresholdPct, WindowSize: p.cfg.WindowSize}
	}
	p.mu.RLock()
	w, ok := p.miners[minerAddr]
	p.mu.RUnlock()
	if !ok {
		return PenaltySnapshot{
			MinerAddr:    minerAddr,
			WindowSize:   p.cfg.WindowSize,
			ThresholdPct: p.cfg.MismatchThresholdPct,
			Multiplier:   1.0,
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return p.snapshotLocked(minerAddr, w)
}

// AllMiners returns every minerAddr the engine has
// observed. Sorted ascending for deterministic /metrics
// and dashboard output.
func (p *PerMinerStats) AllMiners() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, 0, len(p.miners))
	for k := range p.miners {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SnapshotAll returns a snapshot per miner. Used by
// /metrics and the dashboard list view. Returned slice
// is sorted by MinerAddr for stable ordering.
func (p *PerMinerStats) SnapshotAll() []PenaltySnapshot {
	addrs := p.AllMiners()
	out := make([]PenaltySnapshot, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, p.Snapshot(a))
	}
	return out
}

// PenalisedCount returns the number of miners
// currently over the threshold (i.e. multiplier < 1.0).
// Cheap aggregate for /metrics.
func (p *PerMinerStats) PenalisedCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	n := 0
	for _, w := range p.miners {
		if p.computeMultiplier(w) < 1.0 {
			n++
		}
	}
	return n
}

// ----- internals -----

func (p *PerMinerStats) getOrCreate(addr string) *minerWindow {
	p.mu.RLock()
	w, ok := p.miners[addr]
	p.mu.RUnlock()
	if ok {
		return w
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if w, ok := p.miners[addr]; ok {
		return w
	}
	w = &minerWindow{
		kinds: make([]byte, p.cfg.WindowSize),
	}
	p.miners[addr] = w
	return w
}

func (p *PerMinerStats) computeMultiplier(w *minerWindow) float64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size < p.cfg.MinObservations {
		return 1.0
	}
	if w.size == 0 {
		return 1.0
	}
	pct := 100.0 * float64(w.mismatches) / float64(w.size)
	if pct >= p.cfg.MismatchThresholdPct {
		return p.cfg.PenaltyMultiplier
	}
	return 1.0
}

func (p *PerMinerStats) snapshotLocked(addr string, w *minerWindow) PenaltySnapshot {
	pct := 0.0
	if w.size > 0 {
		pct = 100.0 * float64(w.mismatches) / float64(w.size)
	}
	belowMinObs := w.size < p.cfg.MinObservations
	overThreshold := !belowMinObs && pct >= p.cfg.MismatchThresholdPct
	mult := 1.0
	if overThreshold {
		mult = p.cfg.PenaltyMultiplier
	}
	return PenaltySnapshot{
		MinerAddr:       addr,
		WindowSize:      p.cfg.WindowSize,
		WindowFilled:    w.size,
		MismatchCount:   w.mismatches,
		UnknownSKUCount: w.unknownSKUs,
		MatchCount:      w.matches,
		MismatchPct:     pct,
		ThresholdPct:    p.cfg.MismatchThresholdPct,
		OverThreshold:   overThreshold,
		BelowMinObs:     belowMinObs,
		Multiplier:      mult,
		LastObservedAt:  w.lastObservedAt,
	}
}

// classifyVerdict folds a Verdict into one of the five
// internal byte codes. Major mismatches are the only
// kind that contributes to the threshold; minor-only
// mismatches and unknown_sku occupy distinct buckets so
// the operator-facing snapshot can explain why a miner
// is or isn't penalised even when their proof flow is
// noisy.
func classifyVerdict(v Verdict) byte {
	switch v.Kind {
	case VerdictMatch:
		return winMatch
	case VerdictUnknownSKU:
		return winUnknownSKU
	case VerdictSkipped:
		return winSkipped
	case VerdictMismatch:
		if v.HasMajor() {
			return winMajorMismatch
		}
		return winMinorMismatch
	}
	return winSkipped
}

// push appends a new verdict-kind to the ring,
// evicting the oldest if the ring is at capacity.
// Updates the per-bucket counters in lockstep so
// computeMultiplier doesn't have to scan.
func (w *minerWindow) push(kind byte, ts int64, cap int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size == cap {
		// Evict the slot we are about to overwrite.
		evicted := w.kinds[w.head]
		w.dec(evicted)
	} else {
		w.size++
	}
	w.kinds[w.head] = kind
	w.head = (w.head + 1) % cap
	w.inc(kind)
	w.lastObservedAt = ts
}

func (w *minerWindow) inc(kind byte) {
	switch kind {
	case winMatch:
		w.matches++
	case winMajorMismatch:
		w.mismatches++
	case winMinorMismatch:
		w.minorOnly++
	case winUnknownSKU:
		w.unknownSKUs++
	case winSkipped:
		w.skipped++
	}
}

func (w *minerWindow) dec(kind byte) {
	switch kind {
	case winMatch:
		if w.matches > 0 {
			w.matches--
		}
	case winMajorMismatch:
		if w.mismatches > 0 {
			w.mismatches--
		}
	case winMinorMismatch:
		if w.minorOnly > 0 {
			w.minorOnly--
		}
	case winUnknownSKU:
		if w.unknownSKUs > 0 {
			w.unknownSKUs--
		}
	case winSkipped:
		if w.skipped > 0 {
			w.skipped--
		}
	}
}

// noopPenalty is the MismatchPenalty implementation
// returned when Tier-3 is disabled. Always returns 1.0
// so the blockdriver's call site can be unconditional.
type noopPenalty struct{}

func (noopPenalty) MultiplierFor(string) float64 { return 1.0 }
func (noopPenalty) Snapshot(addr string) PenaltySnapshot {
	return PenaltySnapshot{MinerAddr: addr, Multiplier: 1.0}
}
func (noopPenalty) AllMiners() []string { return nil }

// NoopPenalty returns a MismatchPenalty that never
// penalises anyone. The blockdriver wires this when the
// validator did not opt into Tier-3, so the call site
// stays branch-free.
func NoopPenalty() MismatchPenalty { return noopPenalty{} }
