// Package idle provides a small, CGO-free GPU-utilization probe used
// by the consumer miner to gate mining on "is the GPU otherwise busy
// right now?".
//
// Motivation: a desktop user who installs the QSDM miner does NOT want
// the miner to drag the GPU under their game / video call / video
// editing workload. The reference miner is a Solve-loop that keeps the
// GPU pinned at high utilization, so we need a way to step aside when
// the user is actually using their machine and resume when they're
// not.
//
// Implementation: we shell out to `nvidia-smi --query-gpu=...` exactly
// like pkg/telemetry/collector.go does, so this package keeps the
// no-CGO build invariant of the rest of the project. The trade-off is
// per-probe cost (~10-50ms on a healthy host); we mitigate that by
// only probing every Probe.Interval (default 5s).
//
// The package is deliberately conservative:
//
//   - "Idle" means the GPU is below ThresholdPct AND has been below
//     for at least GracePeriod. The grace period prevents us from
//     thrashing in/out of mining when the user opens a Chrome tab
//     that briefly spikes utilization to 5% and back.
//   - When nvidia-smi is unavailable (not installed, locked driver,
//     timeout) the probe returns ok=false. The miner treats that as
//     "I don't know — keep mining" rather than "go to sleep", which
//     is the safer default for a CPU-fallback / non-NVIDIA host that
//     has --idle-only set anyway.
//
// This package depends ONLY on the stdlib so it can be unit-tested
// with a fake exec.Command stub.
package idle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Reading is one snapshot of GPU utilization. Returned by Probe.Probe.
// Pct fields are 0..100; a -1 indicates "field unavailable" so a caller
// can distinguish "GPU is genuinely 0%" from "the host doesn't report
// memory utilization on this driver version".
type Reading struct {
	// At is the wall-clock time the probe completed.
	At time.Time

	// GPUPct is the SM/graphics utilization for the busiest GPU
	// observed in this probe. Multi-GPU hosts report the
	// max-over-cards value because if any GPU is busy, gating
	// mining is appropriate (we don't try to mine on the idle
	// card while the busy one is rendering — that would still
	// pull the PCIe bus and CPU thread that the user's workload
	// needs).
	GPUPct int

	// MemPct is memory-controller utilization. Less reliable
	// than GPUPct on idle desktop drivers (often hovers at 5-15%
	// for the desktop compositor) so the gating logic uses
	// GPUPct as the primary signal and MemPct only as a tiebreak.
	MemPct int

	// Source identifies the probe path that produced this
	// reading (e.g. "nvidia-smi"). Empty when the reading
	// represents a probe failure.
	Source string

	// Err is the underlying probe error, if any. When non-nil,
	// GPUPct/MemPct are zero and callers should treat the
	// reading as "unknown" rather than "0% idle".
	Err error
}

// Probe periodically samples GPU utilization and answers IsIdle()
// with a hysteresis-aware verdict. It is safe for concurrent use.
type Probe struct {
	// ThresholdPct is the GPU% below which we consider the GPU
	// "idle". Zero falls back to DefaultThresholdPct.
	ThresholdPct int

	// GracePeriod is how long the GPU must have been below
	// ThresholdPct before we report IsIdle()=true. Zero falls
	// back to DefaultGracePeriod.
	GracePeriod time.Duration

	// Interval is how often the background sampler polls the
	// GPU. Zero falls back to DefaultInterval. Lowering this
	// makes mining resume faster after the user closes their
	// game; raising it reduces the per-probe cost on a host
	// where nvidia-smi is slow.
	Interval time.Duration

	// Sampler is the per-call sampler. Defaults to
	// nvidiaSMISampler. Tests can inject a fake.
	Sampler func(ctx context.Context) (Reading, error)

	// OnReading, if non-nil, is invoked on every successful
	// probe. The miner uses this to log idle-state transitions
	// in --plain / --service mode without having to wire the
	// probe into the event channel.
	OnReading func(Reading)

	mu          sync.Mutex
	last        Reading
	idleSince   time.Time
	started     bool
	failureKind string
}

// Default tuning constants. Conservative on the side of "let the user
// have their machine"; ops who want aggressive resume can override
// from the command line.
const (
	DefaultThresholdPct = 10
	DefaultGracePeriod  = 60 * time.Second
	DefaultInterval     = 5 * time.Second
)

// resolveDefaults clamps the configured fields to sensible values.
// Negative thresholds, zero intervals etc. are silently floored
// rather than rejected so a typo on the CLI doesn't refuse to start.
func (p *Probe) resolveDefaults() (threshold int, grace, interval time.Duration) {
	threshold = p.ThresholdPct
	if threshold <= 0 {
		threshold = DefaultThresholdPct
	}
	if threshold > 100 {
		threshold = 100
	}
	grace = p.GracePeriod
	if grace <= 0 {
		grace = DefaultGracePeriod
	}
	interval = p.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	if interval < time.Second {
		interval = time.Second
	}
	return
}

// Run drives the background sampler until ctx is cancelled. Caller
// should `go probe.Run(ctx)` once at startup; subsequent IsIdle()
// calls read the latest snapshot lock-free.
//
// If the sampler reports an error, the previous "idle/busy" verdict
// is held — we explicitly do NOT flip to "idle" just because we
// stopped getting readings. That matches the conservative posture:
// when uncertain, don't mine over the user's workload.
func (p *Probe) Run(ctx context.Context) {
	_, _, interval := p.resolveDefaults()
	sampler := p.Sampler
	if sampler == nil {
		sampler = nvidiaSMISampler
	}

	tick := time.NewTicker(interval)
	defer tick.Stop()

	doSample := func() {
		sctx, cancel := context.WithTimeout(ctx, interval)
		defer cancel()
		r, err := sampler(sctx)
		now := time.Now()
		r.At = now
		p.mu.Lock()
		defer p.mu.Unlock()
		if err != nil {
			p.last = Reading{At: now, Err: err}
			p.failureKind = err.Error()
			return
		}
		p.last = r
		p.failureKind = ""
		threshold, _, _ := p.resolveDefaults()
		if r.GPUPct >= 0 && r.GPUPct < threshold {
			if p.idleSince.IsZero() {
				p.idleSince = now
			}
		} else {
			p.idleSince = time.Time{}
		}
		p.started = true
		if cb := p.OnReading; cb != nil {
			cb(r)
		}
	}

	doSample()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			doSample()
		}
	}
}

// IsIdle reports whether the GPU has been below ThresholdPct for at
// least GracePeriod. Returns ok=false if the probe hasn't produced
// any successful readings yet, OR if the most recent reading was an
// error — callers should treat ok=false as "I don't know" and pick
// their own default (the consumer miner picks "keep mining").
func (p *Probe) IsIdle(now time.Time) (idle, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.started || p.last.Err != nil {
		return false, false
	}
	threshold, grace, _ := p.resolveDefaults()
	if p.last.GPUPct < 0 || p.last.GPUPct >= threshold {
		return false, true
	}
	if p.idleSince.IsZero() {
		return false, true
	}
	if now.Sub(p.idleSince) < grace {
		return false, true
	}
	return true, true
}

// Last returns the most recent reading (or the zero value if the
// probe has not yet sampled). Callers may use this to surface
// utilization in their dashboard / metrics endpoint.
func (p *Probe) Last() Reading {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last
}

// FailureReason returns the most recent sampler error message in
// human form, or "" when the probe is healthy. Useful for a
// "--idle-only is on but nvidia-smi keeps failing" diagnostic in
// the dashboard footer.
func (p *Probe) FailureReason() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.failureKind
}

// nvidiaSMISampler is the production sampler. It invokes:
//
//	nvidia-smi --query-gpu=utilization.gpu,utilization.memory \
//	           --format=csv,noheader,nounits
//
// and parses one row per GPU, returning the busiest card. The
// implementation deliberately avoids exec.LookPath: nvidia-smi is
// either on PATH or it isn't, and the stderr from a "not found"
// error message is sufficient for the user-facing diagnostic.
func nvidiaSMISampler(ctx context.Context) (Reading, error) {
	cmd := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=utilization.gpu,utilization.memory",
		"--format=csv,noheader,nounits")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		if stderrStr != "" {
			return Reading{}, fmt.Errorf("nvidia-smi: %w (stderr=%q)", err, stderrStr)
		}
		return Reading{}, fmt.Errorf("nvidia-smi: %w", err)
	}
	return parseUtilCSV(stdout.Bytes())
}

func parseUtilCSV(raw []byte) (Reading, error) {
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return Reading{}, errors.New("nvidia-smi: empty output")
	}
	maxGPU, maxMem := -1, -1
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 2 {
			continue
		}
		gpu, gOK := parsePct(fields[0])
		mem, mOK := parsePct(fields[1])
		if !gOK && !mOK {
			continue
		}
		if gOK && gpu > maxGPU {
			maxGPU = gpu
		}
		if mOK && mem > maxMem {
			maxMem = mem
		}
	}
	if maxGPU < 0 && maxMem < 0 {
		return Reading{}, fmt.Errorf("nvidia-smi: no parseable rows in %q", strings.TrimSpace(string(raw)))
	}
	return Reading{
		GPUPct: maxGPU,
		MemPct: maxMem,
		Source: "nvidia-smi",
	}, nil
}

// parsePct parses one nvidia-smi nounits cell. nvidia-smi sometimes
// prints "[N/A]" for fields the driver doesn't expose (e.g.
// memory-utilization on certain Jetson SKUs); we map those to (0,
// false) so the caller treats the field as missing rather than 0%.
func parsePct(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "[") {
		return 0, false
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	return v, true
}
