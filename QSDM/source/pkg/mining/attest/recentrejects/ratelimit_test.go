package recentrejects

// ratelimit_test.go: behavioural tests for the per-miner
// token-bucket limiter. Covers the dimensions the design
// promised:
//
//   - Disabled-by-default (existing callers see no behaviour
//     change).
//   - Empty MinerAddr always admitted (envelope-parse-failure
//     case must not lose visibility).
//   - Burst absorbed without drops.
//   - Sustained over-rate triggers drops; refill at the
//     configured rate brings the bucket back online.
//   - Multiple miners are independent (one flooder cannot
//     starve another's budget).
//   - Idle TTL evicts stale buckets (memory-bound).
//   - Re-configure carries token state forward.
//   - Drop fires the RateLimitRecorder interface.
//   - Drop does NOT advance Seq (cursor-pagination invariant).
//   - Drop does NOT touch the persister (forensic-record
//     stability).
//
// Each test installs a deterministic nowFn driving a synthetic
// clock so the assertions are reproducible regardless of
// machine wall-clock jitter.

import (
	"sync"
	"testing"
	"time"
)

// rateLimitClock is a deterministic time source for these
// tests. Callers advance it explicitly via tick(d) — wall
// clock is never observed, so a slow CI runner cannot perturb
// the assertions.
type rateLimitClock struct {
	mu  sync.Mutex
	now time.Time
}

func newRateLimitClock(t time.Time) *rateLimitClock { return &rateLimitClock{now: t} }

func (c *rateLimitClock) tick(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *rateLimitClock) NowFn() func() time.Time {
	return func() time.Time {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.now
	}
}

// captureRateLimitRecorder is a fake MetricsRecorder with the
// RateLimitRecorder extension. Used by the
// "drop fires telemetry" test; SetMetricsRecorder is restored
// in t.Cleanup so test order cannot leak state.
type captureRateLimitRecorder struct {
	mu    sync.Mutex
	drops []string
}

func (c *captureRateLimitRecorder) ObserveField(string, int, bool) {}

func (c *captureRateLimitRecorder) RecordRateLimited(addr string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.drops = append(c.drops, addr)
}

func (c *captureRateLimitRecorder) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := append([]string(nil), c.drops...)
	return out
}

// installRateLimitRecorder swaps in the fake and restores the
// previous recorder on t.Cleanup. Ensures parallel-safe test
// ordering even with the t.Run subtest convention.
func installRateLimitRecorder(t *testing.T, r MetricsRecorder) {
	t.Helper()
	prev := currentMetricsRecorder()
	SetMetricsRecorder(r)
	t.Cleanup(func() { SetMetricsRecorder(prev) })
}

func TestRateLimit_DisabledByDefault_AdmitsEverything(t *testing.T) {
	clk := newRateLimitClock(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	s := NewStore(0, clk.NowFn())
	// 10000 records in zero elapsed time — would saturate
	// any reasonable rate setting. With the limiter disabled
	// (the default) every one must produce a non-zero Seq.
	for i := 0; i < 10000; i++ {
		seq := s.Record(Rejection{
			Kind:      KindArchSpoofUnknown,
			MinerAddr: "0xfloodminer",
		})
		if seq == 0 {
			t.Fatalf("record %d dropped with limiter disabled", i)
		}
	}
	if got := s.RateLimitedCount(); got != 0 {
		t.Errorf("RateLimitedCount with limiter disabled = %d, want 0", got)
	}
	if cfg := s.RateLimitConfig(); cfg != nil {
		t.Errorf("RateLimitConfig with limiter disabled = %+v, want nil", cfg)
	}
}

func TestRateLimit_EmptyMinerAddr_AlwaysAdmitted(t *testing.T) {
	clk := newRateLimitClock(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	s := NewStore(0, clk.NowFn())
	// Configure an aggressively low rate to make sure the
	// "empty addr bypasses the limiter" path is what's
	// admitting these records, not the rate itself.
	s.SetRateLimit(0.001, 1, time.Hour)

	for i := 0; i < 100; i++ {
		seq := s.Record(Rejection{
			Kind:      KindArchSpoofUnknown,
			MinerAddr: "", // explicitly empty
		})
		if seq == 0 {
			t.Fatalf("record %d with empty MinerAddr dropped", i)
		}
	}
	if got := s.RateLimitedCount(); got != 0 {
		t.Errorf("RateLimitedCount with empty addrs = %d, want 0", got)
	}
}

func TestRateLimit_BurstAbsorbed_ThenSustainedDrops(t *testing.T) {
	clk := newRateLimitClock(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	s := NewStore(0, clk.NowFn())
	// 10/s rate, 5-record burst, 1h idle TTL. Means the
	// FIRST 5 from a fresh miner go through immediately;
	// the next admission must wait for refill (1 token
	// every 100ms at rate=10).
	s.SetRateLimit(10.0, 5.0, time.Hour)

	admitted, dropped := 0, 0
	for i := 0; i < 12; i++ {
		seq := s.Record(Rejection{
			Kind:      KindArchSpoofUnknown,
			MinerAddr: "0xflood",
		})
		if seq != 0 {
			admitted++
		} else {
			dropped++
		}
	}
	if admitted != 5 {
		t.Errorf("admitted = %d, want 5 (burst)", admitted)
	}
	if dropped != 7 {
		t.Errorf("dropped = %d, want 7 (12 - burst 5)", dropped)
	}
	if got := s.RateLimitedCount(); got != uint64(dropped) {
		t.Errorf("RateLimitedCount = %d, want %d", got, dropped)
	}
}

func TestRateLimit_RefillAtConfiguredRate_ResumesAdmission(t *testing.T) {
	clk := newRateLimitClock(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	s := NewStore(0, clk.NowFn())
	// 10/s rate, 1-record burst → very tight: one record
	// then immediate drops. After 100ms (one refill period)
	// exactly one more record should admit.
	s.SetRateLimit(10.0, 1.0, time.Hour)

	first := s.Record(Rejection{Kind: KindArchSpoofUnknown, MinerAddr: "0xflood"})
	if first == 0 {
		t.Fatal("first record dropped (burst==1 should admit one)")
	}
	second := s.Record(Rejection{Kind: KindArchSpoofUnknown, MinerAddr: "0xflood"})
	if second != 0 {
		t.Fatal("second record admitted (bucket should be empty)")
	}

	// Tick 100ms — one token refilled at rate=10/s.
	clk.tick(100 * time.Millisecond)
	third := s.Record(Rejection{Kind: KindArchSpoofUnknown, MinerAddr: "0xflood"})
	if third == 0 {
		t.Fatal("third record dropped after 100ms refill")
	}

	// Immediately after admission, the bucket is empty
	// again (we drained the one refilled token); next
	// record without further refill must drop.
	fourth := s.Record(Rejection{Kind: KindArchSpoofUnknown, MinerAddr: "0xflood"})
	if fourth != 0 {
		t.Fatal("fourth record admitted (should be dropped post-refill drain)")
	}
}

func TestRateLimit_PerMinerIndependence(t *testing.T) {
	clk := newRateLimitClock(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	s := NewStore(0, clk.NowFn())
	s.SetRateLimit(1.0, 1.0, time.Hour)

	// First record from miner A admits (cold-start full burst).
	if seq := s.Record(Rejection{Kind: KindArchSpoofUnknown, MinerAddr: "0xA"}); seq == 0 {
		t.Fatal("miner A first record dropped")
	}
	// Second record from miner A drops (bucket exhausted).
	if seq := s.Record(Rejection{Kind: KindArchSpoofUnknown, MinerAddr: "0xA"}); seq != 0 {
		t.Fatal("miner A second record admitted")
	}
	// Miner B's first record must admit independently of
	// miner A's exhaustion. This is the SIGNAL-TO-NOISE
	// guarantee: one flooder cannot starve other miners'
	// rejection visibility.
	if seq := s.Record(Rejection{Kind: KindArchSpoofUnknown, MinerAddr: "0xB"}); seq == 0 {
		t.Fatal("miner B first record dropped (bucket cross-contamination)")
	}
}

func TestRateLimit_IdleTTL_EvictsStaleBuckets(t *testing.T) {
	clk := newRateLimitClock(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	s := NewStore(0, clk.NowFn())
	s.SetRateLimit(1.0, 1.0, 10*time.Minute)

	// Seed three miners.
	for _, addr := range []string{"0xA", "0xB", "0xC"} {
		s.Record(Rejection{Kind: KindArchSpoofUnknown, MinerAddr: addr})
	}
	if cfg := s.RateLimitConfig(); cfg == nil || cfg.ActiveBuckets != 3 {
		t.Fatalf("active buckets after seeding = %v, want 3", cfg)
	}

	// Advance past idle TTL, then force a sweep. Production
	// uses an amortised sweep every 1024 admits; the
	// SweepRateLimitIdleForTest hook lets us assert the
	// eviction logic without driving a thousand admits.
	clk.tick(11 * time.Minute)
	s.SweepRateLimitIdleForTest(clk.NowFn()())

	if cfg := s.RateLimitConfig(); cfg == nil || cfg.ActiveBuckets != 0 {
		t.Errorf("active buckets after sweep = %v, want 0", cfg)
	}

	// A new admission after the sweep must work — i.e. the
	// limiter recovers cleanly to a cold-start rather than
	// staying broken. Cold-start gives a fresh full-burst
	// allowance, so the first record admits.
	if seq := s.Record(Rejection{Kind: KindArchSpoofUnknown, MinerAddr: "0xA"}); seq == 0 {
		t.Fatal("miner A admission post-sweep dropped (cold-start broken)")
	}
}

func TestRateLimit_Reconfigure_PreservesTokenState(t *testing.T) {
	clk := newRateLimitClock(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	s := NewStore(0, clk.NowFn())
	s.SetRateLimit(10.0, 5.0, time.Hour)

	// Drain the bucket.
	for i := 0; i < 5; i++ {
		s.Record(Rejection{Kind: KindArchSpoofUnknown, MinerAddr: "0xA"})
	}
	if seq := s.Record(Rejection{Kind: KindArchSpoofUnknown, MinerAddr: "0xA"}); seq != 0 {
		t.Fatal("post-drain record admitted (sanity check failed)")
	}

	// Tighten the rate. The carrying-state guarantee says
	// the existing bucket retains its (now ≈0) tokens; a
	// re-configure that flushed state would let an
	// unbounded burst through during the rate-tightening
	// window, defeating the point.
	s.SetRateLimit(1.0, 1.0, time.Hour)
	if seq := s.Record(Rejection{Kind: KindArchSpoofUnknown, MinerAddr: "0xA"}); seq != 0 {
		t.Error("re-configure flushed bucket state (admitted what should have been dropped)")
	}
}

func TestRateLimit_Reconfigure_DisableFreesMap(t *testing.T) {
	clk := newRateLimitClock(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	s := NewStore(0, clk.NowFn())
	s.SetRateLimit(10.0, 5.0, time.Hour)

	for _, addr := range []string{"0xA", "0xB", "0xC"} {
		s.Record(Rejection{Kind: KindArchSpoofUnknown, MinerAddr: addr})
	}
	// Sanity: limiter is on.
	if cfg := s.RateLimitConfig(); cfg == nil {
		t.Fatal("limiter unexpectedly nil after configure")
	}
	// Disable.
	s.SetRateLimit(0, 0, 0)
	if cfg := s.RateLimitConfig(); cfg != nil {
		t.Errorf("RateLimitConfig after disable = %+v, want nil", cfg)
	}
	// Re-enabling starts cold (admission as if first time).
	s.SetRateLimit(1.0, 1.0, time.Hour)
	if seq := s.Record(Rejection{Kind: KindArchSpoofUnknown, MinerAddr: "0xA"}); seq == 0 {
		t.Error("post-re-enable cold-start admission dropped")
	}
}

func TestRateLimit_DropFiresRecorder(t *testing.T) {
	cap := &captureRateLimitRecorder{}
	installRateLimitRecorder(t, cap)

	clk := newRateLimitClock(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	s := NewStore(0, clk.NowFn())
	s.SetRateLimit(10.0, 1.0, time.Hour)

	s.Record(Rejection{Kind: KindArchSpoofUnknown, MinerAddr: "0xA"}) // admit
	s.Record(Rejection{Kind: KindArchSpoofUnknown, MinerAddr: "0xA"}) // drop
	s.Record(Rejection{Kind: KindArchSpoofUnknown, MinerAddr: "0xA"}) // drop

	got := cap.snapshot()
	if len(got) != 2 {
		t.Fatalf("recorder drops = %d, want 2 (got %v)", len(got), got)
	}
	for _, addr := range got {
		if addr != "0xA" {
			t.Errorf("recorder drop addr = %q, want %q", addr, "0xA")
		}
	}
}

func TestRateLimit_Drop_DoesNotAdvanceSeqOrTouchRing(t *testing.T) {
	clk := newRateLimitClock(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	s := NewStore(0, clk.NowFn())
	s.SetRateLimit(10.0, 1.0, time.Hour)

	// First record admits (Seq=1).
	first := s.Record(Rejection{Kind: KindArchSpoofUnknown, MinerAddr: "0xA"})
	if first != 1 {
		t.Fatalf("first record Seq = %d, want 1", first)
	}
	// 5 drops in a row.
	for i := 0; i < 5; i++ {
		if got := s.Record(Rejection{Kind: KindArchSpoofUnknown, MinerAddr: "0xA"}); got != 0 {
			t.Errorf("drop %d returned Seq %d, want 0", i, got)
		}
	}
	// Ring has exactly the one admitted record. Drops must
	// NOT have leaked Seq numbers — invariant for the
	// /api/v1/attest/recent-rejections cursor pagination,
	// which assumes contiguous monotonic Seq across the
	// persisted+in-memory union.
	if got := s.Len(); got != 1 {
		t.Errorf("ring depth after 1 admit + 5 drops = %d, want 1", got)
	}

	// Tick to refill, then a second admit. Seq must be 2,
	// not 7 — drops did not advance Seq.
	clk.tick(200 * time.Millisecond)
	second := s.Record(Rejection{Kind: KindArchSpoofUnknown, MinerAddr: "0xA"})
	if second != 2 {
		t.Errorf("second admitted Seq = %d, want 2 (drops should not advance Seq)", second)
	}
}

// limiterTestPersister is a tiny stand-in for the real
// FilePersister used to assert that drops never invoke
// Append. We reuse the existing noopPersister-friendly
// surface but track every Append call.
type limiterTestPersister struct {
	mu     sync.Mutex
	calls  int
	loaded []Rejection
}

func (p *limiterTestPersister) Append(r Rejection) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return nil
}
func (p *limiterTestPersister) LoadAll() ([]Rejection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Rejection(nil), p.loaded...), nil
}
func (p *limiterTestPersister) Close() error { return nil }
func (p *limiterTestPersister) appendCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func TestRateLimit_Drop_DoesNotInvokePersister(t *testing.T) {
	clk := newRateLimitClock(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	s := NewStore(0, clk.NowFn())
	persist := &limiterTestPersister{}
	s.SetPersister(persist)
	s.SetRateLimit(10.0, 1.0, time.Hour)

	// 1 admit, 4 drops.
	for i := 0; i < 5; i++ {
		s.Record(Rejection{Kind: KindArchSpoofUnknown, MinerAddr: "0xA"})
	}

	if got := persist.appendCount(); got != 1 {
		t.Errorf("persister Append calls = %d, want 1 (drops must not persist)", got)
	}
}

func TestRateLimitConfig_ReflectsBootSetting(t *testing.T) {
	clk := newRateLimitClock(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	s := NewStore(0, clk.NowFn())
	s.SetRateLimit(7.5, 30.0, 45*time.Minute)

	cfg := s.RateLimitConfig()
	if cfg == nil {
		t.Fatal("RateLimitConfig returned nil with limiter active")
	}
	if cfg.Rate != 7.5 {
		t.Errorf("Rate = %v, want 7.5", cfg.Rate)
	}
	if cfg.Burst != 30.0 {
		t.Errorf("Burst = %v, want 30", cfg.Burst)
	}
	if cfg.IdleTTL != 45*time.Minute {
		t.Errorf("IdleTTL = %v, want 45m", cfg.IdleTTL)
	}
	if cfg.ActiveBuckets != 0 {
		t.Errorf("ActiveBuckets = %d, want 0 (no admits yet)", cfg.ActiveBuckets)
	}
	if cfg.RateLimitedTot != 0 {
		t.Errorf("RateLimitedTot = %d, want 0", cfg.RateLimitedTot)
	}
}

func TestRateLimit_NilStore_Safe(t *testing.T) {
	var s *Store
	// Every public limiter method must be no-op-safe on a
	// nil receiver, matching the rest of the Store API.
	s.SetRateLimit(10, 5, time.Minute)
	if got := s.RateLimitedCount(); got != 0 {
		t.Errorf("nil RateLimitedCount = %d, want 0", got)
	}
	if got := s.RateLimitConfig(); got != nil {
		t.Errorf("nil RateLimitConfig = %+v, want nil", got)
	}
	s.SweepRateLimitIdleForTest(time.Now())
}

func TestRateLimit_BurstZero_DerivesDefault(t *testing.T) {
	clk := newRateLimitClock(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	s := NewStore(0, clk.NowFn())
	// burst=0 → derive (rate*5, clamped to >=1). With
	// rate=2 the derived burst is 10.
	s.SetRateLimit(2.0, 0, time.Hour)
	cfg := s.RateLimitConfig()
	if cfg == nil || cfg.Burst != 10.0 {
		t.Errorf("derived burst with rate=2 = %v, want 10", cfg)
	}
}

func TestRateLimit_BurstSubOne_ClampedToOne(t *testing.T) {
	clk := newRateLimitClock(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	s := NewStore(0, clk.NowFn())
	// rate=0.05 → derived burst 0.25 < 1 → clamped to 1.
	// Defends against a footgun where a tiny rate would
	// otherwise produce a bucket that can never admit.
	s.SetRateLimit(0.05, 0, time.Hour)
	cfg := s.RateLimitConfig()
	if cfg == nil || cfg.Burst != 1.0 {
		t.Errorf("clamped burst with rate=0.05 = %v, want 1", cfg)
	}
}
