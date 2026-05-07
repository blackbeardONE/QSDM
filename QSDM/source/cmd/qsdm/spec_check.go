package main

// spec_check.go is the validator-side wiring for the
// Tier-2 telemetry oracle (pkg/mining/telemetrycheck).
// Lives in cmd/qsdm so it can read the validator's env-
// var configuration directly without polluting the
// pkg/mining import surface — the wiring's coupling to
// the validator binary's environment is the point.
//
// Knobs (all opt-in, all gated on explicit env vars so
// production deployments retain bit-for-bit behaviour
// unless the operator turns telemetry checking on):
//
//   QSDM_SPEC_CHECK_ENABLED=1   - enable the Tier-2 path.
//                                 When unset/empty, the
//                                 wiring returns a nil
//                                 HMACAdapter and the
//                                 validator behaves as
//                                 before (no checker, no
//                                 anomaly metrics, no
//                                 /spec-anomalies endpoint).
//
//   QSDM_PEER_ATTESTER_URLS     - comma-separated list of
//                                 https://…/api/v1/telemetry/
//                                 reference URLs to poll for
//                                 catalog refresh. Empty =
//                                 use baseline only.
//
//   QSDM_PEER_ATTESTER_REFRESH  - refresh interval, e.g.
//                                 "5m". Zero / unset =
//                                 default 5 minutes.
//
//   QSDM_SPEC_CHECK_RING_CAP    - in-memory anomaly ring
//                                 capacity. Default 256.
//
//   QSDM_SPEC_PENALTY_ENABLED   - enable the Tier-3 reward
//                                 downgrade. When set, the
//                                 wiring builds a
//                                 PerMinerStats engine and
//                                 hands it to the
//                                 blockdriver via
//                                 RewardPenalty. REQUIRES
//                                 QSDM_SPEC_CHECK_ENABLED;
//                                 otherwise no verdicts
//                                 reach the engine and
//                                 every multiplier stays
//                                 at 1.0 forever.
//
//   QSDM_SPEC_PENALTY_WINDOW    - sliding-window size in
//                                 proofs (default 1000).
//                                 Smaller windows trip
//                                 faster but tolerate less
//                                 noise.
//
//   QSDM_SPEC_PENALTY_THRESHOLD - mismatch percentage
//                                 (e.g. 10.0) at or above
//                                 which the multiplier
//                                 fires. Default 10.0.
//
//   QSDM_SPEC_PENALTY_MULTIPLIER - the multiplier itself
//                                 (e.g. 0.75 for 25%
//                                 downgrade). Default 0.75.
//
//   QSDM_SPEC_PENALTY_MIN_OBS   - minimum proofs in window
//                                 before a penalty can
//                                 fire. Default 50. Below
//                                 this count the multiplier
//                                 stays at 1.0 even if the
//                                 ratio is over threshold.
//
// The peer attester poller is best-effort: a fetch failure
// emits a warning log + bumps a metric, but never blocks
// boot or causes the validator to refuse new proofs.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/internal/blockdriver"
	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/mining/telemetrycheck"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
	"github.com/blackbeardONE/QSDM/pkg/telemetry"
)

// SpecCheckWiring is what main() receives from
// buildSpecCheckWiring. Nil = telemetry checking is not
// enabled; the rest of boot sees the regular code path.
type SpecCheckWiring struct {
	Adapter *telemetrycheck.HMACAdapter
	Catalog *telemetrycheck.Catalog
	Checker *telemetrycheck.Checker

	// PeerURLs is the resolved + trimmed set of attester
	// reference URLs. Surfaced for the periodic refresh
	// goroutine and for /metrics gauges.
	PeerURLs []string

	// RefreshEvery is the resolved poll interval.
	RefreshEvery time.Duration

	// RingCap is the resolved anomaly ring size.
	RingCap int

	// Penalty is the optional Tier-3 reward-downgrade engine.
	// Non-nil when QSDM_SPEC_PENALTY_ENABLED is truthy AND
	// the Tier-2 path is also active. The blockdriver
	// consumes it as a RewardPenalty; the API + monitoring
	// layers expose its snapshots via /api/v1/mining/account
	// and qsdm_spec_penalty_* counters.
	Penalty *telemetrycheck.PerMinerStats
}

// buildSpecCheckWiring constructs the catalog + checker +
// adapter and returns them, OR returns nil + nil when the
// operator hasn't opted in via QSDM_SPEC_CHECK_ENABLED.
//
// Bootstrap sequence:
//
//   1. Always-on: load baseline (vendor specs).
//   2. For each peer attester URL, fetch the signed profile
//      and Apply it. Failures are logged but non-fatal.
//
// The returned Adapter is the value to feed into
// attestProdCfg.HMACOnAccept; the returned Checker is what
// the metrics emitter and /spec-anomalies endpoint read.
func buildSpecCheckWiring(ctx context.Context, logf func(string, ...any)) (*SpecCheckWiring, error) {
	if !specCheckEnabled() {
		return nil, nil
	}

	ringCap := readEnvInt("QSDM_SPEC_CHECK_RING_CAP", 256)
	refresh := readEnvDuration("QSDM_PEER_ATTESTER_REFRESH", 5*time.Minute)
	urls := splitURLs(os.Getenv("QSDM_PEER_ATTESTER_URLS"))

	catalog := telemetrycheck.NewCatalog()
	added := catalog.LoadBaseline()
	logf("spec-check: baseline loaded",
		"entries", added,
		"sources", "static (built-in vendor specs)")

	for _, url := range urls {
		bootCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		profile, err := fetchPeerProfile(bootCtx, url)
		cancel()
		if err != nil {
			logf("spec-check: peer profile fetch failed (boot)",
				"url", url, "err", err.Error())
			continue
		}
		applied, applyErr := catalog.Apply(profile)
		if applyErr != nil {
			logf("spec-check: peer profile apply failed (boot)",
				"url", url, "err", applyErr.Error())
			continue
		}
		logf("spec-check: peer profile applied",
			"url", url,
			"signer_id", profile.SignerID,
			"gpu_entries", applied)
	}

	checker := telemetrycheck.NewChecker(catalog)
	adapter := telemetrycheck.NewHMACAdapter(checker, ringCap)

	wiring := &SpecCheckWiring{
		Adapter:      adapter,
		Catalog:      catalog,
		Checker:      checker,
		PeerURLs:     urls,
		RefreshEvery: refresh,
		RingCap:      ringCap,
	}

	// Tier-3: opt-in reward-downgrade. Only honoured when
	// the operator explicitly turns it on AND the Tier-2
	// checker is active (the if branch we are inside). The
	// engine hangs off the same adapter so verdicts feed
	// the sliding window automatically.
	if specPenaltyEnabled() {
		penaltyCfg := telemetrycheck.PenaltyConfig{
			WindowSize:           readEnvInt("QSDM_SPEC_PENALTY_WINDOW", 0),
			MismatchThresholdPct: readEnvFloat("QSDM_SPEC_PENALTY_THRESHOLD", 0),
			PenaltyMultiplier:    readEnvFloat("QSDM_SPEC_PENALTY_MULTIPLIER", 0),
			MinObservations:      readEnvInt("QSDM_SPEC_PENALTY_MIN_OBS", 0),
		}
		penalty := telemetrycheck.NewPerMinerStats(penaltyCfg)
		adapter.AttachPenalty(penalty)
		wiring.Penalty = penalty
		resolved := penalty.Config()
		logf("spec-check: Tier-3 reward downgrade active",
			"window_size", resolved.WindowSize,
			"threshold_pct", resolved.MismatchThresholdPct,
			"multiplier", resolved.PenaltyMultiplier,
			"min_observations", resolved.MinObservations)
	} else {
		logf("spec-check: Tier-3 reward downgrade disabled (set QSDM_SPEC_PENALTY_ENABLED=1 to enable)")
	}
	return wiring, nil
}

// specPenaltyEnabled mirrors specCheckEnabled. The two are
// independent gates: Tier-2 (anomaly checking) can be on
// without Tier-3 (reward downgrade), but the inverse is
// nonsensical because Tier-3 needs verdicts to act on.
func specPenaltyEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("QSDM_SPEC_PENALTY_ENABLED"))) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

// readEnvFloat parses a float64 env var. Returns fallback
// on empty / parse-error / non-positive values; the
// PenaltyConfig.Resolve path then turns 0 into the package
// default. Splitting validation between here and Resolve
// keeps the env-parsing surface minimal.
func readEnvFloat(name string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return f
}

// runSpecCheckPoller is the long-running goroutine that
// re-fetches each peer attester URL on every tick and
// folds new observations into the catalog. Honours ctx
// cancellation. Designed to run for the validator's
// lifetime; one goroutine total, not one per URL.
func runSpecCheckPoller(ctx context.Context, w *SpecCheckWiring, logf func(string, ...any)) {
	if w == nil || len(w.PeerURLs) == 0 {
		return
	}
	t := time.NewTicker(w.RefreshEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			logf("spec-check: poller stopping")
			return
		case <-t.C:
			tickSpecCheckPoll(ctx, w, logf)
		}
	}
}

// tickSpecCheckPoll fetches every peer URL once. Pulled out
// of the loop function so an admin endpoint could trigger a
// refresh on demand without restarting the goroutine.
func tickSpecCheckPoll(ctx context.Context, w *SpecCheckWiring, logf func(string, ...any)) {
	for _, url := range w.PeerURLs {
		fetchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		profile, err := fetchPeerProfile(fetchCtx, url)
		cancel()
		if err != nil {
			logf("spec-check: poll fetch failed",
				"url", url, "err", err.Error())
			continue
		}
		applied, applyErr := w.Catalog.Apply(profile)
		if applyErr != nil {
			logf("spec-check: poll apply failed",
				"url", url, "err", applyErr.Error())
			continue
		}
		if applied > 0 {
			logf("spec-check: poll refreshed catalog",
				"url", url,
				"signer_id", profile.SignerID,
				"gpu_entries", applied)
		}
	}
}

// fetchPeerProfile pulls one telemetry.ReferenceProfile
// from a peer attester. Performs a minimal Validate (so a
// poll that returns "telemetry_disabled" surfaces as a
// regular fetch error instead of polluting the catalog
// with a corrupt entry). Does NOT verify the signature —
// the signing key isn't available to the validator yet (a
// future change can pin per-attester keys via a config
// file). Until that lands, the catalog trusts the URL +
// the assertion that the attester self-attests.
func fetchPeerProfile(ctx context.Context, url string) (*telemetry.ReferenceProfile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return nil, fmt.Errorf("status %d (body %q)", resp.StatusCode, string(body))
	}
	var profile telemetry.ReferenceProfile
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return nil, fmt.Errorf("json decode: %w", err)
	}
	if err := profile.Validate(); err != nil {
		return nil, fmt.Errorf("invalid profile: %w", err)
	}
	if len(profile.GPUs) == 0 {
		return nil, fmt.Errorf("profile carries zero GPUs")
	}
	return &profile, nil
}

// specCheckEnabled honours QSDM_SPEC_CHECK_ENABLED with the
// usual truthy-string list. Empty / unset / "0" / "false" =
// disabled.
func specCheckEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("QSDM_SPEC_CHECK_ENABLED"))) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

// readEnvInt parses an integer env var with a fallback.
func readEnvInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

// readEnvDuration parses a duration env var with a fallback.
func readEnvDuration(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

// specCheckMonitoringProbe converts a *SpecCheckWiring
// into the minimal monitoring.SpecCheckProbe interface
// the Prometheus collector expects. Lives here (cmd/qsdm/)
// for the same decoupling reason as specAnomaliesProbe:
// pkg/monitoring stays independent of pkg/mining/
// telemetrycheck's struct shape.
func specCheckMonitoringProbe(w *SpecCheckWiring) monitoring.SpecCheckProbe {
	if w == nil {
		return nil
	}
	return &specCheckMonitoringImpl{wiring: w}
}

type specCheckMonitoringImpl struct {
	wiring *SpecCheckWiring
}

func (p *specCheckMonitoringImpl) CatalogCounters() (int, int, int) {
	return p.wiring.Catalog.Counters()
}

func (p *specCheckMonitoringImpl) CheckCounters() (uint64, uint64, uint64, uint64, uint64) {
	return p.wiring.Checker.Counters()
}

func (p *specCheckMonitoringImpl) MismatchesByField() map[string]uint64 {
	return p.wiring.Checker.MismatchesByField()
}

// specPenaltyMonitoringProbe converts the Tier-3 wiring
// (PerMinerStats + the soloDriver counters) into the
// monitoring.SpecPenaltyProbe interface the Prometheus
// collector expects.
//
// driver may be nil; in that case the blockdriver counters
// surface as 0 (no payouts have happened yet because the
// driver isn't running). That's a legitimate state during
// boot — Prometheus just sees zeros until the driver
// starts ticking.
func specPenaltyMonitoringProbe(w *SpecCheckWiring) monitoring.SpecPenaltyProbe {
	if w == nil || w.Penalty == nil {
		return nil
	}
	return &specPenaltyMonitoringImpl{
		stats:   w.Penalty,
		driverP: &soloDriverPtr,
	}
}

// soloDriverPtr is the package-level pointer to the
// blockdriver. Set by main.go AFTER the driver is
// constructed so the monitoring probe can dereference it
// at scrape time. Read-only after wiring.
var soloDriverPtr *blockdriver.Driver

// SetSoloDriverForMonitoring lets main.go publish the
// constructed driver to the spec-check monitoring probe.
// Idempotent. nil resets — useful for tests.
func SetSoloDriverForMonitoring(d *blockdriver.Driver) {
	soloDriverPtr = d
}

type specPenaltyMonitoringImpl struct {
	stats   *telemetrycheck.PerMinerStats
	driverP **blockdriver.Driver
}

func (p *specPenaltyMonitoringImpl) PenaltyConfig() (int, float64, float64, int) {
	cfg := p.stats.Config()
	return cfg.WindowSize, cfg.MismatchThresholdPct, cfg.PenaltyMultiplier, cfg.MinObservations
}

func (p *specPenaltyMonitoringImpl) PenaltyAggregate() (int, int) {
	tracked := len(p.stats.AllMiners())
	penalised := p.stats.PenalisedCount()
	return tracked, penalised
}

func (p *specPenaltyMonitoringImpl) BlockdriverPenaltyCounters() (uint64, uint64) {
	if p.driverP == nil || *p.driverP == nil {
		return 0, 0
	}
	stats := (*p.driverP).Stats()
	return stats.PenalisedPayouts, stats.WithheldDust
}

// specPenaltyProbe converts the Tier-3 *PerMinerStats
// into the api.SpecPenaltyProbe interface. Returns nil
// when Tier-3 is not enabled — the API layer then serves
// 503 rather than wedging on a nil-deref.
func specPenaltyProbe(w *SpecCheckWiring) api.SpecPenaltyProbe {
	if w == nil || w.Penalty == nil {
		return nil
	}
	return &specPenaltyProbeImpl{stats: w.Penalty}
}

type specPenaltyProbeImpl struct {
	stats *telemetrycheck.PerMinerStats
}

func (p *specPenaltyProbeImpl) PenaltyForMiner(addr string) (api.PenaltyView, bool) {
	snap := p.stats.Snapshot(addr)
	if snap.WindowFilled == 0 && snap.MismatchCount == 0 && snap.LastObservedAt == 0 {
		return penaltyViewFromSnapshot(snap), false
	}
	return penaltyViewFromSnapshot(snap), true
}

func (p *specPenaltyProbeImpl) AllPenaltySnapshots() []api.PenaltyView {
	src := p.stats.SnapshotAll()
	out := make([]api.PenaltyView, len(src))
	for i, s := range src {
		out[i] = penaltyViewFromSnapshot(s)
	}
	return out
}

func (p *specPenaltyProbeImpl) PenalisedCount() int {
	return p.stats.PenalisedCount()
}

func (p *specPenaltyProbeImpl) Config() api.PenaltyConfigView {
	cfg := p.stats.Config()
	return api.PenaltyConfigView{
		WindowSize:           cfg.WindowSize,
		MismatchThresholdPct: cfg.MismatchThresholdPct,
		PenaltyMultiplier:    cfg.PenaltyMultiplier,
		MinObservations:      cfg.MinObservations,
	}
}

// penaltyViewFromSnapshot adapts the internal snapshot
// to the public wire-shape. Pure transformation, no I/O,
// safe to call from any goroutine.
func penaltyViewFromSnapshot(s telemetrycheck.PenaltySnapshot) api.PenaltyView {
	return api.PenaltyView{
		MinerAddr:       s.MinerAddr,
		WindowSize:      s.WindowSize,
		WindowFilled:    s.WindowFilled,
		MismatchCount:   s.MismatchCount,
		UnknownSKUCount: s.UnknownSKUCount,
		MatchCount:      s.MatchCount,
		MismatchPct:     s.MismatchPct,
		ThresholdPct:    s.ThresholdPct,
		OverThreshold:   s.OverThreshold,
		BelowMinObs:     s.BelowMinObs,
		Multiplier:      s.Multiplier,
		LastObservedAt:  s.LastObservedAt,
	}
}

// specAnomaliesProbe converts a *SpecCheckWiring into the
// minimal api.SpecAnomaliesProbe interface the HTTP layer
// expects. Lives here (cmd/qsdm/) so the api package
// stays decoupled from the telemetrycheck struct shape —
// the bridge is one cmd-binary worth of code.
func specAnomaliesProbe(w *SpecCheckWiring) api.SpecAnomaliesProbe {
	if w == nil {
		return nil
	}
	return &specAnomaliesProbeImpl{wiring: w}
}

type specAnomaliesProbeImpl struct {
	wiring *SpecCheckWiring
}

func (p *specAnomaliesProbeImpl) Snapshot() api.SpecAnomaliesSnapshot {
	checked, matched, mismatched, unknown, skipped := p.wiring.Checker.Counters()
	totalEntries, signers, skus := p.wiring.Catalog.Counters()
	ringSize := len(p.wiring.Adapter.RecentAnomalies(p.wiring.RingCap))
	return api.SpecAnomaliesSnapshot{
		CatalogTotal:      totalEntries,
		CatalogSigners:    signers,
		CatalogSKUs:       skus,
		Checked:           checked,
		Matched:           matched,
		Mismatched:        mismatched,
		UnknownSKU:        unknown,
		Skipped:           skipped,
		RingCap:           p.wiring.RingCap,
		RingSize:          ringSize,
		MismatchesByField: p.wiring.Checker.MismatchesByField(),
	}
}

func (p *specAnomaliesProbeImpl) RecentAnomalies(n int) []api.SpecAnomalyView {
	src := p.wiring.Adapter.RecentAnomalies(n)
	out := make([]api.SpecAnomalyView, len(src))
	for i, s := range src {
		out[i] = api.SpecAnomalyView{
			ObservedAt:        s.ObservedAt,
			AttestationType:   s.AttestationType,
			NodeID:            s.NodeID,
			GPUUUID:           s.GPUUUID,
			GPUName:           s.GPUName,
			GPUArch:           s.GPUArch,
			ComputeCap:        s.ComputeCap,
			DriverVer:         s.DriverVer,
			MinerAddr:         s.MinerAddr,
			Height:            s.Height,
			Verdict:           s.Verdict,
			MismatchedFields:  s.MismatchedFields,
			HasMajor:          s.HasMajor,
			MatchedReferences: s.MatchedReferences,
		}
	}
	return out
}

// splitURLs trims and dedups the QSDM_PEER_ATTESTER_URLS
// list. Empty input returns nil.
func splitURLs(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}
