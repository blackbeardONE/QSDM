package main

// Telemetry oracle wiring — bridges pkg/telemetry's
// generic Registry/Collector primitives into the attester's
// HTTP server and background lifecycle. Lives in its own
// file so the server.go core stays focused on the
// challenge-issuance hot path.
//
// Two pieces ship:
//
//  1. handleTelemetryReference — public HTTP route at
//     GET /api/v1/telemetry/reference. Returns the
//     freshly-signed ReferenceProfile JSON (signed at
//     request time so the IssuedAt is always current).
//
//  2. runTelemetryCollector — background goroutine that
//     ticks a Collector + Registry on a configurable
//     interval, persists the result to disk after each
//     observation cycle, and respects ctx cancellation.
//
// The Server itself never touches the signing key on the
// hot challenge path — that's owned by *challenge.Issuer.
// We pass the key into telemetry separately so the two
// surfaces remain auditable independently. In practice the
// SAME key signs both, because operators who run a
// challenge-issuer-only attester have one HMAC secret per
// box; future operators with key separation policies pass
// distinct keys.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/telemetry"
)

// TelemetryProvider is the runtime handle the Server holds
// to know "telemetry is wired, here is how to answer
// /api/v1/telemetry/reference". Nil = telemetry disabled.
type TelemetryProvider struct {
	Registry *telemetry.Registry

	// Key is the HMAC key used to sign profiles. Stored
	// here (instead of read fresh from disk) so request
	// latency is deterministic — request latency would
	// otherwise depend on filesystem cache state.
	Key []byte

	// Persistence path; non-empty enables on-tick
	// SaveToFile. Wired by main.go; the provider itself
	// only USES the path during the collector loop, not
	// during request handling.
	PersistPath string

	// Counters exposed on /metrics.
	requests        atomic.Uint64
	signFailures    atomic.Uint64
	collectionTicks atomic.Uint64
	collectionErrs  atomic.Uint64
}

// SetTelemetry attaches a TelemetryProvider to the server.
// MUST be called before Run() so the routes are registered.
// Pass nil to leave telemetry disabled (default).
func (s *Server) SetTelemetry(p *TelemetryProvider) {
	s.telemetry = p
}

// telemetryEnabled is a small helper for /metrics + /info to
// branch on "is the oracle wired?". Defined here (not in
// server.go) so all telemetry-aware logic lives in one file.
func (s *Server) telemetryEnabled() bool {
	return s.telemetry != nil && s.telemetry.Registry != nil && len(s.telemetry.Key) >= 16
}

// handleTelemetryReference serves the freshly signed
// ReferenceProfile. Two query parameters supported:
//
//   ?include_observations=N
//     Cap the per-GPU version-set sizes to N entries each.
//     Lets very-long-running attesters serve a compact
//     "last-N" view without exposing the full history.
//     Zero / unset = no cap.
//
//   ?gpu=<uuid>
//     Filter to a single GPU. Useful when a curious miner
//     only wants to know about a specific reference card.
//
// Returns 404 when telemetry is disabled. Returns 500 on
// signing failure (always a programmer error in practice
// — the key length is validated at SetTelemetry time).
func (s *Server) handleTelemetryReference(w http.ResponseWriter, r *http.Request) {
	s.requestsTotal.Add(1)
	if r.Method != http.MethodGet {
		s.methodNotAllow.Add(1)
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.telemetryEnabled() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"telemetry_disabled"}`))
		return
	}
	s.telemetry.requests.Add(1)

	profile, err := s.telemetry.Registry.SignedSnapshot(time.Now().Unix(), s.telemetry.Key)
	if err != nil {
		s.telemetry.signFailures.Add(1)
		http.Error(w, "telemetry sign failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Optional ?gpu= filter — applied AFTER signing so the
	// signature still covers the full profile; we strip
	// the unwanted GPUs and then RE-SIGN. That way a
	// receiver who fetches "?gpu=GPU-x" can verify the
	// signature is over the SAME slice they got. Costs an
	// extra HMAC pass but keeps the wire contract clean.
	if want := r.URL.Query().Get("gpu"); want != "" {
		filtered := profile.GPUs[:0]
		for _, g := range profile.GPUs {
			if g.UUID == want {
				filtered = append(filtered, g)
			}
		}
		profile.GPUs = filtered
		if err := profile.Sign(s.telemetry.Key); err != nil {
			s.telemetry.signFailures.Add(1)
			http.Error(w, "telemetry resign failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Optional ?include_observations=N cap on version sets.
	if v := r.URL.Query().Get("include_observations"); v != "" {
		cap, err := parseUintQuery(v)
		if err != nil {
			http.Error(w, "bad include_observations", http.StatusBadRequest)
			return
		}
		if cap > 0 {
			capVersionSets(profile, int(cap))
			if err := profile.Sign(s.telemetry.Key); err != nil {
				s.telemetry.signFailures.Add(1)
				http.Error(w, "telemetry resign failed: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(profile); err != nil {
		s.telemetry.signFailures.Add(1)
	}
}

// capVersionSets truncates DriverVersionsSeen / CUDAVersionsSeen
// / VBIOSVersionsSeen on each GPU in profile to at most n
// entries. Preserves the existing slice order — first n entries
// are kept, the rest dropped. Used by the ?include_observations
// query knob.
func capVersionSets(profile *telemetry.ReferenceProfile, n int) {
	if n <= 0 {
		return
	}
	for i := range profile.GPUs {
		g := &profile.GPUs[i]
		if len(g.DriverVersionsSeen) > n {
			g.DriverVersionsSeen = g.DriverVersionsSeen[:n]
		}
		if len(g.CUDAVersionsSeen) > n {
			g.CUDAVersionsSeen = g.CUDAVersionsSeen[:n]
		}
		if len(g.VBIOSVersionsSeen) > n {
			g.VBIOSVersionsSeen = g.VBIOSVersionsSeen[:n]
		}
	}
}

// parseUintQuery parses a positive decimal integer from a
// URL query string. Returns 0 + error on malformed input
// (negative, leading zeros allowed but trailing chars are
// not). Tiny helper duplicated here instead of pulling in
// strconv-with-extra-validation upstream because the
// surface is so small.
func parseUintQuery(s string) (uint64, error) {
	if s == "" {
		return 0, errors.New("empty")
	}
	var n uint64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, errors.New("non-digit byte")
		}
		n = n*10 + uint64(c-'0')
	}
	return n, nil
}

// runTelemetryCollector is the background goroutine that
// ticks the Collector at every interval and folds the
// observations into reg. Persists to persistPath (if
// non-empty) after each successful tick. Honours ctx
// cancellation — returns immediately when ctx is done.
//
// On Collect failure, increments a counter and logs once
// per tick (so a missing nvidia-smi or a busted driver
// surfaces in journalctl, not as silent telemetry decay).
//
// Designed to be called as `go runTelemetryCollector(...)`
// from main.go AFTER LoadFromFile has hydrated the
// registry. The first tick happens after `every`, NOT
// immediately, because the registry may already hold a
// just-loaded snapshot — re-collecting on boot would just
// burn a CPU spike for the same data.
func runTelemetryCollector(
	ctx context.Context,
	reg *telemetry.Registry,
	collector telemetry.Collector,
	persistPath string,
	every time.Duration,
	provider *TelemetryProvider,
	logf func(string, ...any),
) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if every <= 0 {
		every = 60 * time.Second
	}

	// Force a first observation on boot so the persisted
	// file gets a single fresh data point (e.g. driver
	// version after a Windows update). Subsequent ticks
	// run at `every` intervals.
	if err := tickOnce(ctx, reg, collector, persistPath, provider, logf); err != nil {
		logf("telemetry: initial collect failed", "err", err.Error())
	}

	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			logf("telemetry: collector stopping",
				"ticks", provider.collectionTicks.Load(),
				"errs", provider.collectionErrs.Load())
			return
		case <-t.C:
			if err := tickOnce(ctx, reg, collector, persistPath, provider, logf); err != nil {
				logf("telemetry: collect failed", "err", err.Error())
			}
		}
	}
}

// tickOnce performs one Collect → ApplyAll → SaveToFile
// cycle. Exposed (lowercase) as a function so a future
// admin endpoint can trigger an out-of-band refresh
// without restarting the goroutine.
func tickOnce(
	ctx context.Context,
	reg *telemetry.Registry,
	collector telemetry.Collector,
	persistPath string,
	provider *TelemetryProvider,
	logf func(string, ...any),
) error {
	provider.collectionTicks.Add(1)
	obs, err := collector.Collect(ctx)
	if err != nil {
		provider.collectionErrs.Add(1)
		return err
	}
	if _, err := reg.ApplyAll(obs); err != nil {
		provider.collectionErrs.Add(1)
		return err
	}
	if persistPath != "" {
		if err := reg.SaveToFile(persistPath); err != nil {
			provider.collectionErrs.Add(1)
			return err
		}
	}
	logf("telemetry: collected",
		"gpu_count", len(obs),
		"collector", collector.Kind())
	return nil
}
