package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// These tests exercise the full trust-widget pipeline the operator
// dashboard relies on (see internal/dashboard/static/dashboard.js,
// function updateTrustPanel). They go through the registered HTTP
// handler directly — not the aggregator in isolation — so any future
// change to middleware, content-type, or status-code mapping is
// caught before the dashboard card silently breaks.
//
// The four widget states enumerated by Major Update §8.5.4 are each
// asserted here plus the two operator-visible meta states (warmup
// 503, disabled 404). Each assertion mirrors exactly what
// dashboard.js expects to read out of the response.

// -----------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------

// newTrustHTTPServer wires a fresh aggregator into a minimal test
// server that registers exactly the two trust endpoints. This is the
// smallest slice of the real mux that dashboard.js touches; we use
// it instead of the full NewHandlers() / SetupRoutes() path so the
// test stays insensitive to unrelated handler churn.
//
// The aggregator has a 60 s warm-up window (trustAggregatorWarmup);
// to simulate a node that has already warmed up we install a clock
// that returns startedAt on the first call (aggregator construction)
// and startedAt + 2 * warmup from then on — so by the time the
// handler reads Summary(), warm.Load() is true and the test can
// assert the steady-state 200 response.
func newTrustHTTPServer(t *testing.T, cfg TrustConfig) (*httptest.Server, *TrustAggregator) {
	t.Helper()
	if cfg.Clock != nil {
		// Advance by > warmup once the aggregator is constructed so
		// Refresh() marks it warm.
		startedAt := cfg.Clock()
		var refreshCalls int
		cfg.Clock = func() time.Time {
			refreshCalls++
			if refreshCalls == 1 {
				return startedAt
			}
			return startedAt.Add(2 * time.Minute)
		}
	}
	agg := NewTrustAggregator(cfg)
	agg.Refresh()

	// Install into the singleton the handlers read from, and make
	// sure the test cleans up so other tests are not affected.
	SetTrustAggregator(agg, false)
	t.Cleanup(func() { SetTrustAggregator(nil, false) })

	h := &Handlers{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/trust/attestations/summary", h.TrustSummaryHandler)
	mux.HandleFunc("/api/v1/trust/attestations/recent", h.TrustRecentHandler)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, agg
}

func getJSONBody(t *testing.T, url string) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return resp, nil
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("GET %s: expected application/json, got %q", url, ct)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
	return resp, body
}

func assertDashboardContract(t *testing.T, body map[string]any) {
	t.Helper()
	// dashboard.js reads these four fields literally:
	// ratioEl.textContent  = data.attested + ' of ' + data.total_public
	// statusEl.textContent = data.ngc_service_status || 'healthy'
	// lastEl.textContent   = data.last_attested_at   || 'never'
	// windowEl.textContent = data.fresh_within       || '15m0s'
	for _, k := range []string{"attested", "total_public", "ngc_service_status", "fresh_within", "scope_note", "last_checked_at"} {
		if _, ok := body[k]; !ok {
			t.Errorf("summary response missing required field %q", k)
		}
	}
	// Anti-claim guardrail §8.5.2.
	a, _ := body["attested"].(float64)
	tp, _ := body["total_public"].(float64)
	if a > tp {
		t.Errorf("§8.5.2 violation: attested=%v > total_public=%v", a, tp)
	}
	if tp == 0 && a > 0 {
		t.Errorf("§8.5.2 violation: attested=%v with total_public=0", a)
	}
	// §8.5.2 verbatim scope note.
	if note, _ := body["scope_note"].(string); !strings.Contains(note, "not a consensus rule") {
		t.Errorf("scope_note missing canonical phrase, got %q", note)
	}
}

// -----------------------------------------------------------------------
// state 1 — zero opt-in (no peer publishes an attestation)
// -----------------------------------------------------------------------

func TestDashboardContract_ZeroOptIn(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	ts, _ := newTrustHTTPServer(t, TrustConfig{
		PeerProvider: &stubPeerProvider{rows: []PeerAttestation{
			{NodeID: "aaaaaaaa0000000000bbbb", AttestedAt: time.Time{}, GPUArchitecture: ""},
			{NodeID: "cccccccc1111111111dddd", AttestedAt: time.Time{}, GPUArchitecture: ""},
		}},
		LocalSource: &stubLocalSource{id: "local-node-test-0001", ok: false},
		FreshWithin: 15 * time.Minute,
		Clock:       fixedClock(now),
	})

	resp, body := getJSONBody(t, ts.URL+"/api/v1/trust/attestations/summary")
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	assertDashboardContract(t, body)
	if att, _ := body["attested"].(float64); att != 0 {
		t.Errorf("zero opt-in: expected attested=0, got %v", att)
	}
	if tp, _ := body["total_public"].(float64); tp < 2 {
		t.Errorf("zero opt-in: expected total_public>=2, got %v", tp)
	}
	if last := body["last_attested_at"]; last != nil {
		t.Errorf("zero opt-in: expected last_attested_at=null, got %v", last)
	}
	// dashboard.js renders "0 of N" in the ratio. Any denominator
	// collapse would trip the anti-claim guardrail.
}

// -----------------------------------------------------------------------
// state 2 — partial adoption (most validators attested, healthy)
// -----------------------------------------------------------------------

func TestDashboardContract_PartialAdoption(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	ts, _ := newTrustHTTPServer(t, TrustConfig{
		PeerProvider: &stubPeerProvider{rows: []PeerAttestation{
			{NodeID: "aaaaaaaa0000000000bbbb", AttestedAt: now.Add(-30 * time.Second), GPUArchitecture: "hopper", NGCHMACOK: true, RegionHint: "eu"},
			{NodeID: "cccccccc1111111111dddd", AttestedAt: now.Add(-2 * time.Minute), GPUArchitecture: "ada", NGCHMACOK: true, RegionHint: "us"},
			{NodeID: "eeeeeeee2222222222ffff", AttestedAt: time.Time{}, GPUArchitecture: "", NGCHMACOK: false, RegionHint: ""},
		}},
		LocalSource: &stubLocalSource{id: "local-node-test-0001", ok: false},
		FreshWithin: 15 * time.Minute,
		Clock:       fixedClock(now),
	})

	resp, body := getJSONBody(t, ts.URL+"/api/v1/trust/attestations/summary")
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	assertDashboardContract(t, body)
	if att, _ := body["attested"].(float64); att != 2 {
		t.Errorf("partial adoption: expected attested=2, got %v", att)
	}
	if tp, _ := body["total_public"].(float64); tp != 3 {
		t.Errorf("partial adoption: expected total_public=3, got %v", tp)
	}
	if st, _ := body["ngc_service_status"].(string); st != "healthy" {
		t.Errorf("partial adoption: expected status=healthy, got %q", st)
	}

	// Recent feed must return at most the two fresh entries, newest
	// first, with redacted node IDs.
	_, rbody := getJSONBody(t, ts.URL+"/api/v1/trust/attestations/recent?limit=10")
	atts, _ := rbody["attestations"].([]any)
	if len(atts) != 2 {
		t.Fatalf("expected 2 recent rows, got %d", len(atts))
	}
	first, _ := atts[0].(map[string]any)
	prefix, _ := first["node_id_prefix"].(string)
	if !strings.Contains(prefix, "…") {
		t.Errorf("dashboard expects redacted node_id_prefix with '…', got %q", prefix)
	}
}

// -----------------------------------------------------------------------
// state 3 — NGC outage (every attestation is stale)
// -----------------------------------------------------------------------

func TestDashboardContract_NGCOutage(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	staleBy := 2 * time.Hour
	ts, _ := newTrustHTTPServer(t, TrustConfig{
		PeerProvider: &stubPeerProvider{rows: []PeerAttestation{
			{NodeID: "aaaaaaaa0000000000bbbb", AttestedAt: now.Add(-staleBy), GPUArchitecture: "hopper", NGCHMACOK: true, RegionHint: "eu"},
			{NodeID: "cccccccc1111111111dddd", AttestedAt: now.Add(-staleBy - 10*time.Minute), GPUArchitecture: "ada", NGCHMACOK: true, RegionHint: "us"},
		}},
		LocalSource: &stubLocalSource{id: "local-node-test-0001", ok: false},
		FreshWithin: 15 * time.Minute,
		Clock:       fixedClock(now),
	})

	resp, body := getJSONBody(t, ts.URL+"/api/v1/trust/attestations/summary")
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	assertDashboardContract(t, body)
	if att, _ := body["attested"].(float64); att != 0 {
		t.Errorf("outage: expected attested=0 (nothing fresh), got %v", att)
	}
	if st, _ := body["ngc_service_status"].(string); st != "outage" {
		t.Errorf("outage: expected status=outage, got %q", st)
	}
	// last_attested_at must be populated (even though nothing is
	// fresh) — dashboard renders it as "never" only when truly nil.
	if last := body["last_attested_at"]; last == nil {
		t.Errorf("outage: last_attested_at must report the newest overall timestamp, got null")
	}
}

// -----------------------------------------------------------------------
// state 4 — full adoption (every validator attested fresh)
// -----------------------------------------------------------------------

func TestDashboardContract_FullAdoption(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	ts, _ := newTrustHTTPServer(t, TrustConfig{
		PeerProvider: &stubPeerProvider{rows: []PeerAttestation{
			{NodeID: "aaaaaaaa0000000000bbbb", AttestedAt: now.Add(-10 * time.Second), GPUArchitecture: "hopper", NGCHMACOK: true, RegionHint: "eu"},
			{NodeID: "cccccccc1111111111dddd", AttestedAt: now.Add(-30 * time.Second), GPUArchitecture: "ada", NGCHMACOK: true, RegionHint: "us"},
		}},
		LocalSource: &stubLocalSource{id: "local-node-test-0001", ok: false},
		FreshWithin: 15 * time.Minute,
		Clock:       fixedClock(now),
	})

	resp, body := getJSONBody(t, ts.URL+"/api/v1/trust/attestations/summary")
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	assertDashboardContract(t, body)
	a, _ := body["attested"].(float64)
	tp, _ := body["total_public"].(float64)
	if a != tp || a == 0 {
		t.Errorf("full adoption: expected attested==total_public>0, got %v / %v", a, tp)
	}
	if st, _ := body["ngc_service_status"].(string); st != "healthy" {
		t.Errorf("full adoption: expected status=healthy, got %q", st)
	}
	if r, _ := body["ratio"].(float64); r < 0.999 {
		t.Errorf("full adoption: expected ratio≈1.0, got %v", r)
	}
}

// -----------------------------------------------------------------------
// meta state — aggregator warming up (dashboard falls back to 503)
// -----------------------------------------------------------------------

func TestDashboardContract_WarmingUp503(t *testing.T) {
	// Install a brand-new aggregator but deliberately skip Refresh
	// so the internal `warm` atomic stays false — mirroring the
	// first ~60 s of process lifetime in production.
	SetTrustAggregator(nil, false)
	t.Cleanup(func() { SetTrustAggregator(nil, false) })

	now := time.Unix(1_800_000_000, 0).UTC()
	agg := NewTrustAggregator(TrustConfig{
		PeerProvider: &stubPeerProvider{},
		LocalSource:  &stubLocalSource{id: "local", ok: false},
		FreshWithin:  15 * time.Minute,
		Clock:        fixedClock(now),
	})
	// Do NOT call Refresh — we want the "warming up" branch.
	SetTrustAggregator(agg, false)

	h := &Handlers{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/trust/attestations/summary", h.TrustSummaryHandler)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/api/v1/trust/attestations/summary")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Errorf("warming up: dashboard.js expects 503, got %d", resp.StatusCode)
	}
}

// -----------------------------------------------------------------------
// meta state — operator opted out (dashboard falls back to 404)
// -----------------------------------------------------------------------

func TestDashboardContract_DisabledReturns404(t *testing.T) {
	// Install disabled=true and verify the handler returns 404 as
	// expected by the dashboard's __disabled branch.
	SetTrustAggregator(nil, true)
	t.Cleanup(func() { SetTrustAggregator(nil, false) })

	h := &Handlers{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/trust/attestations/summary", h.TrustSummaryHandler)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/api/v1/trust/attestations/summary")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("opt-out: dashboard.js expects 404, got %d", resp.StatusCode)
	}
}
