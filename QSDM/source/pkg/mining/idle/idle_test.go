package idle

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestParseUtilCSV_HappyPath(t *testing.T) {
	r, err := parseUtilCSV([]byte("12, 4\n38, 18\n"))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if r.GPUPct != 38 {
		t.Fatalf("max GPU pct: want 38, got %d", r.GPUPct)
	}
	if r.MemPct != 18 {
		t.Fatalf("max mem pct: want 18, got %d", r.MemPct)
	}
	if r.Source != "nvidia-smi" {
		t.Fatalf("source: want nvidia-smi, got %q", r.Source)
	}
}

func TestParseUtilCSV_NA(t *testing.T) {
	r, err := parseUtilCSV([]byte("[N/A], [N/A]\n3, 0\n"))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if r.GPUPct != 3 {
		t.Fatalf("GPU pct: want 3, got %d", r.GPUPct)
	}
	if r.MemPct != 0 {
		t.Fatalf("mem pct: want 0, got %d", r.MemPct)
	}
}

func TestParseUtilCSV_Empty(t *testing.T) {
	if _, err := parseUtilCSV(nil); err == nil {
		t.Fatal("expected error on empty input")
	}
	if _, err := parseUtilCSV([]byte("\n  \n")); err == nil {
		t.Fatal("expected error on whitespace input")
	}
}

func TestParseUtilCSV_AllNA(t *testing.T) {
	if _, err := parseUtilCSV([]byte("[N/A], [N/A]\n")); err == nil {
		t.Fatal("expected error when no row has parseable fields")
	}
}

func TestProbe_IdleAfterGrace(t *testing.T) {
	gpu := 0
	called := 0
	p := &Probe{
		ThresholdPct: 10,
		GracePeriod:  50 * time.Millisecond,
		Interval:     5 * time.Millisecond,
		Sampler: func(_ context.Context) (Reading, error) {
			called++
			return Reading{GPUPct: gpu, MemPct: 1, Source: "fake"}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	time.Sleep(20 * time.Millisecond)

	if idle, ok := p.IsIdle(time.Now()); idle || !ok {
		t.Fatalf("inside grace window: want (idle=false, ok=true), got (idle=%v, ok=%v)", idle, ok)
	}
	time.Sleep(80 * time.Millisecond)
	if idle, ok := p.IsIdle(time.Now()); !idle || !ok {
		t.Fatalf("after grace window: want (idle=true, ok=true), got (idle=%v, ok=%v)", idle, ok)
	}
	if called == 0 {
		t.Fatal("sampler never called")
	}
}

func TestProbe_BusyResetsIdleClock(t *testing.T) {
	type sample struct {
		gpu int
	}
	samples := []sample{{0}, {0}, {50}, {0}, {0}, {0}, {0}, {0}, {0}}
	idx := 0
	p := &Probe{
		ThresholdPct: 10,
		GracePeriod:  30 * time.Millisecond,
		Interval:     5 * time.Millisecond,
		Sampler: func(_ context.Context) (Reading, error) {
			s := samples[idx%len(samples)]
			idx++
			return Reading{GPUPct: s.gpu}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	time.Sleep(20 * time.Millisecond)
	if idle, _ := p.IsIdle(time.Now()); idle {
		t.Fatalf("after busy spike, idle clock should be reset; got idle=true")
	}
	time.Sleep(120 * time.Millisecond)
	if idle, _ := p.IsIdle(time.Now()); !idle {
		t.Fatalf("after sustained idle, want idle=true")
	}
}

func TestProbe_SamplerErrorHoldsVerdict(t *testing.T) {
	p := &Probe{
		ThresholdPct: 10,
		GracePeriod:  20 * time.Millisecond,
		Interval:     5 * time.Millisecond,
		Sampler: func(_ context.Context) (Reading, error) {
			return Reading{}, errors.New("nvidia-smi: not found")
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	time.Sleep(30 * time.Millisecond)
	idle, ok := p.IsIdle(time.Now())
	if idle || ok {
		t.Fatalf("with sampler errors: want (idle=false, ok=false), got (idle=%v, ok=%v)", idle, ok)
	}
	if got := p.FailureReason(); got == "" {
		t.Fatal("expected non-empty FailureReason")
	}
}

func TestProbe_DefaultsClampInsane(t *testing.T) {
	p := &Probe{
		ThresholdPct: -50,
		GracePeriod:  -1,
		Interval:     0,
	}
	threshold, grace, interval := p.resolveDefaults()
	if threshold != DefaultThresholdPct {
		t.Fatalf("threshold: want default %d, got %d", DefaultThresholdPct, threshold)
	}
	if grace != DefaultGracePeriod {
		t.Fatalf("grace: want default %s, got %s", DefaultGracePeriod, grace)
	}
	if interval != DefaultInterval {
		t.Fatalf("interval: want default %s, got %s", DefaultInterval, interval)
	}
	p2 := &Probe{ThresholdPct: 999, Interval: 50 * time.Millisecond}
	threshold, _, interval = p2.resolveDefaults()
	if threshold != 100 {
		t.Fatalf("threshold over 100 should clamp to 100; got %d", threshold)
	}
	if interval != time.Second {
		t.Fatalf("interval below 1s should floor to 1s; got %s", interval)
	}
}
