package main

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining/idle"
)

// TestIdleGate_NilProbe locks the legacy posture: when --idle-only
// is off (gate is nil), shouldPause is always (false, "").
func TestIdleGate_NilProbe(t *testing.T) {
	var g *idleGate
	pause, reason := g.shouldPause()
	if pause || reason != "" {
		t.Fatalf("nil gate: want (false, \"\"), got (%v, %q)", pause, reason)
	}

	g2 := buildIdleGate(nil)
	if g2 != nil {
		t.Fatalf("buildIdleGate(nil): want nil, got %#v", g2)
	}
}

// TestIdleGate_BusyVsIdle drives the gate through a fake probe
// and verifies that:
//   - while the GPU is busy, shouldPause returns (true, reason)
//   - after the grace window with the GPU below threshold, the
//     gate flips to (false, "")
//   - the reason string surfaces the percentage so a `--plain`
//     log line is self-explanatory.
func TestIdleGate_BusyVsIdle(t *testing.T) {
	var gpu atomic.Int32
	gpu.Store(80)
	// idle.Probe floors Interval to 1s in resolveDefaults() to
	// keep production nvidia-smi load sane; the test has to
	// match that floor or we'd only see the immediate first
	// sample. We pin Interval=1s + GracePeriod=200ms so the
	// busy→idle transition takes ~1.2s of wall clock to land.
	probe := &idle.Probe{
		ThresholdPct: 10,
		GracePeriod:  200 * time.Millisecond,
		Interval:     1 * time.Second,
		Sampler: func(_ context.Context) (idle.Reading, error) {
			return idle.Reading{GPUPct: int(gpu.Load()), Source: "fake"}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go probe.Run(ctx)
	// Wait for the immediate first sample to land.
	time.Sleep(50 * time.Millisecond)

	gate := buildIdleGate(probe)
	pause, reason := gate.shouldPause()
	if !pause {
		t.Fatalf("busy GPU: want pause=true, got false (reason=%q)", reason)
	}
	if !strings.Contains(reason, "80%") {
		t.Fatalf("reason should mention utilization %%, got %q", reason)
	}

	gpu.Store(2)
	// 1s tick + 200ms grace = ~1.2s minimum; we wait 1.5s for
	// scheduler slack on a busy CI box.
	time.Sleep(1500 * time.Millisecond)
	pause, reason = gate.shouldPause()
	if pause {
		t.Fatalf("idle GPU after grace: want pause=false, got true (reason=%q)", reason)
	}
	if reason != "" {
		t.Fatalf("idle GPU after grace: want empty reason, got %q", reason)
	}
}

// TestIdleGate_FailsOpen exercises the conservative fallback: when
// the underlying probe can't produce readings (nvidia-smi missing
// / driver locked), the gate must NOT pause, so a host without a
// working sampler doesn't sit out forever.
func TestIdleGate_FailsOpen(t *testing.T) {
	probe := &idle.Probe{
		ThresholdPct: 10,
		GracePeriod:  100 * time.Millisecond,
		Interval:     1 * time.Second,
		Sampler: func(_ context.Context) (idle.Reading, error) {
			return idle.Reading{}, &fakeProbeErr{msg: "nvidia-smi: not found"}
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go probe.Run(ctx)
	time.Sleep(50 * time.Millisecond)

	gate := buildIdleGate(probe)
	pause, reason := gate.shouldPause()
	if pause || reason != "" {
		t.Fatalf("sampler error: want (pause=false, reason=\"\"), got (%v, %q)", pause, reason)
	}
}

type fakeProbeErr struct{ msg string }

func (e *fakeProbeErr) Error() string { return e.msg }

// TestApplyServiceMode_DefaultsForcePlain checks the contract that
// --service implies --plain even when the operator didn't pass it,
// because the rotating log file would otherwise fill with ANSI
// escape sequences from the panel renderer.
func TestApplyServiceMode_DefaultsForcePlain(t *testing.T) {
	tmp := t.TempDir() + "/svc.log"
	cf := &ConsumerFlags{Service: true, LogFile: tmp, LogSize: 1, LogKeep: 1}
	cfg := Config{}
	plain, closer, err := applyServiceMode(cf, &cfg)
	if err != nil {
		t.Fatalf("applyServiceMode: %v", err)
	}
	defer closer.Close()
	if !plain {
		t.Fatal("--service should force plain=true")
	}
	if closer == nil {
		t.Fatal("closer must not be nil under --service")
	}
}

// TestApplyServiceMode_NoOpReturnsHonestPlain confirms that on
// the default path (no --service / --log-file) we leave the
// config-resolved plain choice untouched and return a safe
// no-op closer.
func TestApplyServiceMode_NoOpReturnsHonestPlain(t *testing.T) {
	cf := &ConsumerFlags{}
	cfg := Config{Plain: true}
	plain, closer, err := applyServiceMode(cf, &cfg)
	if err != nil {
		t.Fatalf("applyServiceMode: %v", err)
	}
	if !plain {
		t.Fatal("config Plain=true should propagate")
	}
	if closer == nil {
		t.Fatal("expected a no-op closer, got nil")
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("close on no-op: %v", err)
	}
}
