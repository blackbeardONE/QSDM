package telemetrycheck

import (
	"sync"
	"testing"
	"time"
)

// helper: a Verdict with the given kind. For Mismatch
// kinds, the second arg controls whether the mismatch
// is "major" — only major mismatches contribute to
// the penalty threshold per Tier-3 design.
func mkVerdict(kind VerdictKind, major bool) Verdict {
	v := Verdict{Kind: kind}
	if kind == VerdictMismatch {
		sev := "minor"
		if major {
			sev = "major"
		}
		v.Mismatches = []FieldMismatch{{Field: "test", Severity: sev}}
	}
	return v
}

func TestPenaltyConfig_Resolve_Defaults(t *testing.T) {
	cfg := PenaltyConfig{}.Resolve()
	if cfg.WindowSize != DefaultPenaltyWindowSize {
		t.Errorf("WindowSize = %d, want %d", cfg.WindowSize, DefaultPenaltyWindowSize)
	}
	if cfg.MismatchThresholdPct != DefaultPenaltyMismatchThresholdPct {
		t.Errorf("MismatchThresholdPct = %v, want %v", cfg.MismatchThresholdPct, DefaultPenaltyMismatchThresholdPct)
	}
	if cfg.PenaltyMultiplier != DefaultPenaltyMultiplier {
		t.Errorf("PenaltyMultiplier = %v, want %v", cfg.PenaltyMultiplier, DefaultPenaltyMultiplier)
	}
	if cfg.MinObservations != DefaultPenaltyMinObservations {
		t.Errorf("MinObservations = %d, want %d", cfg.MinObservations, DefaultPenaltyMinObservations)
	}
}

func TestPenaltyConfig_Resolve_OutOfRange(t *testing.T) {
	cases := []PenaltyConfig{
		{WindowSize: 5},                    // below 10
		{MismatchThresholdPct: -1},         // negative
		{MismatchThresholdPct: 200},        // > 100
		{PenaltyMultiplier: 0},             // zero
		{PenaltyMultiplier: 1.5},           // > 1
		{MinObservations: 0},
	}
	for _, c := range cases {
		got := c.Resolve()
		if got.WindowSize < 10 || got.MismatchThresholdPct <= 0 ||
			got.PenaltyMultiplier <= 0 || got.MinObservations < 1 {
			t.Errorf("Resolve(%+v) returned invalid %+v", c, got)
		}
	}
}

func TestPenaltyConfig_Resolve_Idempotent(t *testing.T) {
	cfg := PenaltyConfig{
		WindowSize: 500, MismatchThresholdPct: 5.0,
		PenaltyMultiplier: 0.5, MinObservations: 25,
	}.Resolve()
	again := cfg.Resolve()
	if again != cfg {
		t.Errorf("Resolve not idempotent: %+v vs %+v", cfg, again)
	}
}

func TestPerMinerStats_BelowMinObs_ReturnsOne(t *testing.T) {
	p := NewPerMinerStats(PenaltyConfig{
		WindowSize: 100, MinObservations: 50,
		MismatchThresholdPct: 1.0, PenaltyMultiplier: 0.5,
	})
	now := time.Now()
	for i := 0; i < 49; i++ {
		p.Update("alice", mkVerdict(VerdictMismatch, true), now)
	}
	if got := p.MultiplierFor("alice"); got != 1.0 {
		t.Errorf("Multiplier with 49 mismatches (below MinObs=50) = %v, want 1.0", got)
	}
	snap := p.Snapshot("alice")
	if !snap.BelowMinObs {
		t.Errorf("BelowMinObs not set: %+v", snap)
	}
	if snap.OverThreshold {
		t.Errorf("OverThreshold should not fire below MinObs: %+v", snap)
	}
}

func TestPerMinerStats_AboveThreshold_PenaltyApplies(t *testing.T) {
	p := NewPerMinerStats(PenaltyConfig{
		WindowSize: 100, MinObservations: 50,
		MismatchThresholdPct: 10.0, PenaltyMultiplier: 0.75,
	})
	now := time.Now()
	for i := 0; i < 90; i++ {
		p.Update("alice", mkVerdict(VerdictMatch, false), now)
	}
	for i := 0; i < 10; i++ {
		p.Update("alice", mkVerdict(VerdictMismatch, true), now)
	}
	got := p.MultiplierFor("alice")
	if got != 0.75 {
		t.Errorf("Multiplier with 10/100 mismatches = %v, want 0.75", got)
	}
	snap := p.Snapshot("alice")
	if !snap.OverThreshold {
		t.Errorf("OverThreshold should be true: %+v", snap)
	}
	if snap.MismatchCount != 10 {
		t.Errorf("MismatchCount = %d, want 10", snap.MismatchCount)
	}
	if snap.MatchCount != 90 {
		t.Errorf("MatchCount = %d, want 90", snap.MatchCount)
	}
	if snap.WindowFilled != 100 {
		t.Errorf("WindowFilled = %d, want 100", snap.WindowFilled)
	}
	if snap.MismatchPct != 10.0 {
		t.Errorf("MismatchPct = %v, want 10.0", snap.MismatchPct)
	}
}

func TestPerMinerStats_BelowThreshold_NoPenalty(t *testing.T) {
	p := NewPerMinerStats(PenaltyConfig{
		WindowSize: 100, MinObservations: 50,
		MismatchThresholdPct: 10.0, PenaltyMultiplier: 0.5,
	})
	now := time.Now()
	for i := 0; i < 95; i++ {
		p.Update("alice", mkVerdict(VerdictMatch, false), now)
	}
	for i := 0; i < 5; i++ {
		p.Update("alice", mkVerdict(VerdictMismatch, true), now)
	}
	if got := p.MultiplierFor("alice"); got != 1.0 {
		t.Errorf("Multiplier with 5/100 mismatches (under 10%%) = %v, want 1.0", got)
	}
}

func TestPerMinerStats_MinorMismatch_DoesNotCountTowardThreshold(t *testing.T) {
	p := NewPerMinerStats(PenaltyConfig{
		WindowSize: 100, MinObservations: 50,
		MismatchThresholdPct: 10.0, PenaltyMultiplier: 0.5,
	})
	now := time.Now()
	for i := 0; i < 50; i++ {
		p.Update("alice", mkVerdict(VerdictMatch, false), now)
	}
	for i := 0; i < 50; i++ {
		// Minor-only mismatches: NOT counted toward threshold.
		p.Update("alice", mkVerdict(VerdictMismatch, false), now)
	}
	if got := p.MultiplierFor("alice"); got != 1.0 {
		t.Errorf("Multiplier with 50%% MINOR mismatches = %v, want 1.0", got)
	}
	snap := p.Snapshot("alice")
	if snap.MismatchCount != 0 {
		t.Errorf("MismatchCount (major-only) = %d, want 0", snap.MismatchCount)
	}
}

func TestPerMinerStats_UnknownSKU_NotCountedAsMismatch(t *testing.T) {
	p := NewPerMinerStats(PenaltyConfig{
		WindowSize: 100, MinObservations: 50,
		MismatchThresholdPct: 10.0, PenaltyMultiplier: 0.5,
	})
	now := time.Now()
	for i := 0; i < 100; i++ {
		p.Update("alice", mkVerdict(VerdictUnknownSKU, false), now)
	}
	if got := p.MultiplierFor("alice"); got != 1.0 {
		t.Errorf("Multiplier with 100%% unknown_sku = %v, want 1.0", got)
	}
	snap := p.Snapshot("alice")
	if snap.UnknownSKUCount != 100 {
		t.Errorf("UnknownSKUCount = %d, want 100", snap.UnknownSKUCount)
	}
	if snap.MismatchCount != 0 {
		t.Errorf("MismatchCount with unknown_sku stream = %d, want 0", snap.MismatchCount)
	}
}

func TestPerMinerStats_RingEviction(t *testing.T) {
	p := NewPerMinerStats(PenaltyConfig{
		WindowSize: 100, MinObservations: 50,
		MismatchThresholdPct: 10.0, PenaltyMultiplier: 0.5,
	})
	now := time.Now()
	for i := 0; i < 100; i++ {
		p.Update("alice", mkVerdict(VerdictMismatch, true), now)
	}
	if got := p.MultiplierFor("alice"); got != 0.5 {
		t.Fatalf("Pre-eviction multiplier = %v, want 0.5", got)
	}
	for i := 0; i < 100; i++ {
		p.Update("alice", mkVerdict(VerdictMatch, false), now)
	}
	if got := p.MultiplierFor("alice"); got != 1.0 {
		t.Errorf("Post-eviction multiplier = %v, want 1.0", got)
	}
	snap := p.Snapshot("alice")
	if snap.MismatchCount != 0 {
		t.Errorf("MismatchCount after full eviction = %d, want 0", snap.MismatchCount)
	}
	if snap.MatchCount != 100 {
		t.Errorf("MatchCount after full eviction = %d, want 100", snap.MatchCount)
	}
}

func TestPerMinerStats_PerMinerIsolation(t *testing.T) {
	p := NewPerMinerStats(PenaltyConfig{
		WindowSize: 100, MinObservations: 50,
		MismatchThresholdPct: 10.0, PenaltyMultiplier: 0.5,
	})
	now := time.Now()
	for i := 0; i < 100; i++ {
		p.Update("alice", mkVerdict(VerdictMismatch, true), now)
	}
	for i := 0; i < 100; i++ {
		p.Update("bob", mkVerdict(VerdictMatch, false), now)
	}
	if got := p.MultiplierFor("alice"); got != 0.5 {
		t.Errorf("alice multiplier = %v, want 0.5", got)
	}
	if got := p.MultiplierFor("bob"); got != 1.0 {
		t.Errorf("bob multiplier = %v, want 1.0", got)
	}
}

func TestPerMinerStats_UnseenMiner_ReturnsOne(t *testing.T) {
	p := NewPerMinerStats(PenaltyConfig{})
	if got := p.MultiplierFor("ghost"); got != 1.0 {
		t.Errorf("Unseen miner multiplier = %v, want 1.0", got)
	}
	snap := p.Snapshot("ghost")
	if snap.Multiplier != 1.0 {
		t.Errorf("Unseen miner snapshot.Multiplier = %v, want 1.0", snap.Multiplier)
	}
}

func TestPerMinerStats_EmptyMinerAddr_NoOp(t *testing.T) {
	p := NewPerMinerStats(PenaltyConfig{})
	now := time.Now()
	p.Update("", mkVerdict(VerdictMismatch, true), now)
	if got := p.MultiplierFor(""); got != 1.0 {
		t.Errorf("Empty addr multiplier = %v, want 1.0", got)
	}
	if len(p.AllMiners()) != 0 {
		t.Errorf("Empty-addr Update created entry: %v", p.AllMiners())
	}
}

func TestPerMinerStats_AllMiners_Sorted(t *testing.T) {
	p := NewPerMinerStats(PenaltyConfig{MinObservations: 1})
	now := time.Now()
	for _, a := range []string{"qsdm1charlie", "qsdm1alice", "qsdm1bob"} {
		p.Update(a, mkVerdict(VerdictMatch, false), now)
	}
	got := p.AllMiners()
	want := []string{"qsdm1alice", "qsdm1bob", "qsdm1charlie"}
	if len(got) != len(want) {
		t.Fatalf("AllMiners len = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("AllMiners[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestPerMinerStats_PenalisedCount(t *testing.T) {
	p := NewPerMinerStats(PenaltyConfig{
		WindowSize: 100, MinObservations: 50,
		MismatchThresholdPct: 10.0, PenaltyMultiplier: 0.5,
	})
	now := time.Now()
	for i := 0; i < 100; i++ {
		p.Update("bad1", mkVerdict(VerdictMismatch, true), now)
		p.Update("bad2", mkVerdict(VerdictMismatch, true), now)
		p.Update("good", mkVerdict(VerdictMatch, false), now)
	}
	if got := p.PenalisedCount(); got != 2 {
		t.Errorf("PenalisedCount = %d, want 2", got)
	}
}

func TestPerMinerStats_ConcurrentSafety(t *testing.T) {
	p := NewPerMinerStats(PenaltyConfig{
		WindowSize: 200, MinObservations: 50,
	})
	now := time.Now()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				addr := "qsdm1miner"
				p.Update(addr, mkVerdict(VerdictMatch, false), now)
				_ = p.MultiplierFor(addr)
				_ = p.Snapshot(addr)
			}
		}(g)
	}
	wg.Wait()
	snap := p.Snapshot("qsdm1miner")
	// Window cap is 200; matches saturate it.
	if snap.WindowFilled != 200 {
		t.Errorf("WindowFilled after 4000 pushes = %d, want 200", snap.WindowFilled)
	}
}

func TestNoopPenalty_AlwaysOne(t *testing.T) {
	np := NoopPenalty()
	if got := np.MultiplierFor("anyone"); got != 1.0 {
		t.Errorf("Noop MultiplierFor = %v, want 1.0", got)
	}
	if snap := np.Snapshot("anyone"); snap.Multiplier != 1.0 {
		t.Errorf("Noop Snapshot.Multiplier = %v, want 1.0", snap.Multiplier)
	}
	if got := np.AllMiners(); got != nil {
		t.Errorf("Noop AllMiners = %v, want nil", got)
	}
}

func TestPerMinerStats_LastObservedAt(t *testing.T) {
	p := NewPerMinerStats(PenaltyConfig{MinObservations: 1})
	t1 := time.Unix(1700000000, 0)
	p.Update("alice", mkVerdict(VerdictMatch, false), t1)
	if got := p.Snapshot("alice").LastObservedAt; got != t1.Unix() {
		t.Errorf("LastObservedAt = %d, want %d", got, t1.Unix())
	}
	t2 := time.Unix(1700000123, 0)
	p.Update("alice", mkVerdict(VerdictMatch, false), t2)
	if got := p.Snapshot("alice").LastObservedAt; got != t2.Unix() {
		t.Errorf("LastObservedAt after 2nd update = %d, want %d", got, t2.Unix())
	}
}
