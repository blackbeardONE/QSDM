package preflight

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeStatus is a tiny re-encoding of the shape /api/v1/status returns.
// We deliberately avoid importing pkg/api so this test cannot accidentally
// regress when StatusResponse grows new fields — the preflight code is
// supposed to ignore everything outside the `mining` block.
type fakeStatus struct {
	ChainTip uint64       `json:"chain_tip"`
	Network  string       `json:"network"`
	Mining   *fakeMining  `json:"mining,omitempty"`
	Extra    string       `json:"extra,omitempty"` // unrelated field; verifies we tolerate unknown keys
}

type fakeMining struct {
	ProtocolVersionsAccepted []uint32 `json:"protocol_versions_accepted"`
	ForkV2Height             uint64   `json:"fork_v2_height,omitempty"`
	ForkV2Active             bool     `json:"fork_v2_active"`
	ForkV2TCHeight           uint64   `json:"fork_v2_tc_height,omitempty"`
	ForkV2TCActive           bool     `json:"fork_v2_tc_active"`
	AttestationTypesRequired []string `json:"attestation_types_required,omitempty"`
	MinEnrollStakeDust       uint64   `json:"min_enroll_stake_dust,omitempty"`
}

func newFakeValidator(t *testing.T, body fakeStatus, code int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestCheck_V2ActiveValidator_V1Caller_Refuses(t *testing.T) {
	srv := newFakeValidator(t, fakeStatus{
		ChainTip: 42_000,
		Network:  "QSDM · CELL",
		Mining: &fakeMining{
			ProtocolVersionsAccepted: []uint32{2},
			ForkV2Height:             0,
			ForkV2Active:             true,
			AttestationTypesRequired: []string{"nvidia-cc-v1", "nvidia-hmac-v1"},
			MinEnrollStakeDust:       1_000_000_000,
		},
	}, 200)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r := Check(ctx, srv.Client(), srv.URL, false /* claimingV2 */)
	if r.Decision != DecisionRefuseV1 {
		t.Fatalf("decision = %v, want refuse-v1", r.Decision)
	}
	if !r.ForkV2Active {
		t.Fatalf("ForkV2Active = false on a v2-active validator")
	}
	if !r.HasMiningBlock {
		t.Fatalf("HasMiningBlock = false even though /status returned one")
	}
	if r.MinEnrollStakeDust != 1_000_000_000 {
		t.Fatalf("MinEnrollStakeDust = %d, want 1_000_000_000", r.MinEnrollStakeDust)
	}
	if !strings.Contains(FormatDecision(r, false), "REFUSING TO MINE") {
		t.Fatalf("FormatDecision banner missing the refusal headline:\n%s", FormatDecision(r, false))
	}
	if !strings.Contains(FormatDecision(r, true), "WARNING: --allow-v1 override set") {
		t.Fatalf("FormatDecision with override=true does not warn the operator")
	}
}

func TestCheck_V2ActiveValidator_V2Caller_Proceeds(t *testing.T) {
	srv := newFakeValidator(t, fakeStatus{
		ChainTip: 42_000,
		Network:  "QSDM · CELL",
		Mining: &fakeMining{
			ProtocolVersionsAccepted: []uint32{2},
			ForkV2Active:             true,
			MinEnrollStakeDust:       1_000_000_000,
		},
	}, 200)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r := Check(ctx, srv.Client(), srv.URL, true /* claimingV2 */)
	if r.Decision != DecisionProceedV2 {
		t.Fatalf("decision = %v, want proceed-v2", r.Decision)
	}
}

func TestCheck_V1Validator_V1Caller_Proceeds(t *testing.T) {
	srv := newFakeValidator(t, fakeStatus{
		ChainTip: 100,
		Network:  "QSDM · LOCAL",
		Mining: &fakeMining{
			ProtocolVersionsAccepted: []uint32{1},
			ForkV2Active:             false,
		},
	}, 200)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r := Check(ctx, srv.Client(), srv.URL, false)
	if r.Decision != DecisionProceedV1 {
		t.Fatalf("decision = %v, want proceed-v1", r.Decision)
	}
}

func TestCheck_OldValidator_NoMiningBlock_ProceedsWithWarning(t *testing.T) {
	// Pre-v0.3.2 validator: no `mining` field at all.
	srv := newFakeValidator(t, fakeStatus{
		ChainTip: 5,
		Network:  "QSDM · CELL",
		Mining:   nil,
	}, 200)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r := Check(ctx, srv.Client(), srv.URL, false)
	if r.Decision != DecisionProceedV1 {
		t.Fatalf("decision = %v, want proceed-v1 (fail-open on missing posture)", r.Decision)
	}
	if r.HasMiningBlock {
		t.Fatal("HasMiningBlock should be false when /status response lacks the mining field")
	}
	if !r.ValidatorReachable {
		t.Fatal("ValidatorReachable should be true on a 200 response even if mining block is missing")
	}
	if !strings.Contains(FormatDecision(r, false), "does not advertise") {
		t.Fatalf("FormatDecision should mention older validator:\n%s", FormatDecision(r, false))
	}
}

func TestCheck_NetworkError_ProceedsWithWarning(t *testing.T) {
	// Point at a port that nothing is listening on (port 1 is the
	// canonical TCP-only sentinel; the dial will fail fast).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client := &http.Client{Timeout: 1 * time.Second}
	r := Check(ctx, client, "http://127.0.0.1:1", false)
	if r.Decision != DecisionProceedV1 {
		t.Fatalf("decision = %v, want proceed-v1 (fail-open on probe error)", r.Decision)
	}
	if r.ProbeErr == nil {
		t.Fatal("ProbeErr should be non-nil after a dial failure")
	}
	if !strings.Contains(FormatDecision(r, false), "could not probe validator") {
		t.Fatalf("FormatDecision missing probe-failure message:\n%s", FormatDecision(r, false))
	}
}

func TestCheck_HTTPError_ProceedsWithWarning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("boom"))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r := Check(ctx, srv.Client(), srv.URL, false)
	if r.Decision != DecisionProceedV1 {
		t.Fatalf("decision = %v, want proceed-v1 on HTTP 500", r.Decision)
	}
	if r.ProbeErr == nil {
		t.Fatal("ProbeErr should be set on a non-200 response")
	}
}

func TestCheck_BadJSON_ProceedsWithWarning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("this is not JSON"))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r := Check(ctx, srv.Client(), srv.URL, false)
	if r.Decision != DecisionProceedV1 {
		t.Fatalf("decision = %v, want proceed-v1 on bad JSON", r.Decision)
	}
}

func TestBuildStatusURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"http://host:8080", "http://host:8080/api/v1/status"},
		{"http://host:8080/", "http://host:8080/api/v1/status"},
		{"http://host:8080/api/v1", "http://host:8080/api/v1/status"},
		{"http://host:8080/api/v1/", "http://host:8080/api/v1/status"},
		{"https://api.qsdm.tech", "https://api.qsdm.tech/api/v1/status"},
		{"https://api.qsdm.tech/api/v1/", "https://api.qsdm.tech/api/v1/status"},
	}
	for _, c := range cases {
		if got := buildStatusURL(c.in); got != c.want {
			t.Errorf("buildStatusURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatDustAsCell(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0"},
		{1_000_000_000, "10"},                 // 10 CELL exactly
		{1, "0.00000001"},                     // 1 dust
		{100_000_000, "1"},                    // 1 CELL
		{12_345_678_901, "123.45678901"},      // mixed
		{99_999_999, "0.99999999"},            // sub-1 CELL
	}
	for _, c := range cases {
		if got := formatDustAsCell(c.in); got != c.want {
			t.Errorf("formatDustAsCell(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
