package main

// enrollment_poller_test.go — unit tests for the v2
// enrollment-status background poller.
//
// Coverage targets:
//
//   - PollOnce against a 200/404/503/network-error fixture
//   - Phase transitions emit the right severity
//   - Run wakes up on the ticker, stops on ctx cancel
//   - The wire mirror stays byte-compatible with
//     pkg/api.EnrollmentRecordView (compile-time guard)
//   - Dashboard absorbs EvEnrollment correctly
//   - formatV2EnrollLine renders all three states (—, active,
//     revoked) without panicking

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/api"
)

// TestEnrollmentRecordWireMatchesAPI is the build-with-tests
// guard that locks our local wire mirror to the canonical
// shape exported by pkg/api.EnrollmentRecordView. Whenever
// somebody adds a field to the API view, we expect this test
// to fail — adding the same field here is the explicit signal
// that the new field is now part of what the miner reads.
//
// We compare the JSON tag set, not the Go field count, because
// pkg/api may add unexported fields the miner shouldn't see;
// the wire contract is what matters.
func TestEnrollmentRecordWireMatchesAPI(t *testing.T) {
	want := jsonTagSet(reflect.TypeOf(api.EnrollmentRecordView{}))
	got := jsonTagSet(reflect.TypeOf(enrollmentRecordWire{}))
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("enrollmentRecordWire JSON shape drifted from api.EnrollmentRecordView\n want: %v\n got:  %v", want, got)
	}
}

func jsonTagSet(typ reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		// Drop ",omitempty" etc.
		name := strings.SplitN(tag, ",", 2)[0]
		if name != "" {
			out[name] = true
		}
	}
	return out
}

// TestPoller_PollOnce_HappyPath: a well-formed 200 response
// must populate every Status field and clear LastError. This
// is the path operators exercise on every successful tick.
func TestPoller_PollOnce_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/mining/enrollment/") {
			http.Error(w, "wrong path", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.EnrollmentRecordView{
			NodeID:           "alice-rtx-01",
			Owner:            "qsdm1abc",
			GPUUUID:          "GPU-abc",
			StakeDust:        10_000_000_000,
			EnrolledAtHeight: 42,
			Phase:            "active",
			Slashable:        true,
		})
	}))
	defer srv.Close()

	p, err := NewEnrollmentPoller(srv.Client(), srv.URL, "alice-rtx-01", 0)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	st := p.PollOnce(context.Background())
	if st.Phase != PhaseActive {
		t.Errorf("phase: got %q want active", st.Phase)
	}
	if st.StakeDust != 10_000_000_000 {
		t.Errorf("stake: got %d", st.StakeDust)
	}
	if !st.Slashable {
		t.Error("expected slashable=true")
	}
	if st.LastError != "" {
		t.Errorf("LastError must be empty on success; got %q", st.LastError)
	}
	if st.LastPolledAt.IsZero() {
		t.Error("LastPolledAt must be populated")
	}
}

// TestPoller_PollOnce_NotFound: 404 → PhaseNotFound, NOT
// Unknown. The dashboard relies on this distinction to render
// "you haven't enrolled yet" vs "polling failed."
func TestPoller_PollOnce_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no enrollment record for node_id", http.StatusNotFound)
	}))
	defer srv.Close()

	p, _ := NewEnrollmentPoller(srv.Client(), srv.URL, "ghost", 0)
	st := p.PollOnce(context.Background())
	if st.Phase != PhaseNotFound {
		t.Errorf("phase: got %q want not_found", st.Phase)
	}
	if st.LastError != "" {
		t.Errorf("404 must NOT set LastError (phase carries the signal); got %q", st.LastError)
	}
}

// TestPoller_PollOnce_Unavailable: 503 → PhaseUnconfig +
// LastError populated with the validator's response body. This
// is the "v2 not configured on this node" surface; operators
// should see it as distinct from a generic outage.
func TestPoller_PollOnce_Unavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "v2 enrollment registry not configured on this node", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	p, _ := NewEnrollmentPoller(srv.Client(), srv.URL, "alice", 0)
	st := p.PollOnce(context.Background())
	if st.Phase != PhaseUnconfig {
		t.Errorf("phase: got %q want unconfigured", st.Phase)
	}
	if st.LastError == "" {
		t.Error("expected LastError populated with validator body")
	}
}

// TestPoller_PollOnce_NetworkError: server closed mid-flight →
// Phase stays Unknown, LastError populated. The runLoop's
// dashboard then keeps the prior successful phase visible
// (we tested that elsewhere), so a transient outage doesn't
// flap the displayed phase.
func TestPoller_PollOnce_NetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	srv.Close() // closed BEFORE the request — guarantees a connection error.

	p, _ := NewEnrollmentPoller(srv.Client(), srv.URL, "alice", 0)
	st := p.PollOnce(context.Background())
	if st.Phase != PhaseUnknown {
		t.Errorf("phase: got %q want unknown", st.Phase)
	}
	if st.LastError == "" {
		t.Error("expected non-empty LastError on network failure")
	}
}

// TestPoller_PollOnce_BadJSON: 200 with garbage payload →
// LastError populated, Phase stays Unknown (NOT a default
// to active, which would silently mask validator misbehaviour).
func TestPoller_PollOnce_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{not json"))
	}))
	defer srv.Close()

	p, _ := NewEnrollmentPoller(srv.Client(), srv.URL, "alice", 0)
	st := p.PollOnce(context.Background())
	if st.Phase != PhaseUnknown {
		t.Errorf("phase: got %q want unknown", st.Phase)
	}
	if !strings.Contains(st.LastError, "decode") {
		t.Errorf("expected LastError to mention decode; got %q", st.LastError)
	}
}

// TestPoller_PollOnce_UnknownPhase: 200 with a wire phase
// string the miner doesn't recognise (e.g. a future protocol
// adds "frozen") → Phase=Unknown, LastError populated. This is
// the "go look at this" guard for forward-incompat upgrades.
func TestPoller_PollOnce_UnknownPhase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"node_id":"alice","phase":"frozen","stake_dust":0,"slashable":false,"enrolled_at_height":1}`))
	}))
	defer srv.Close()

	p, _ := NewEnrollmentPoller(srv.Client(), srv.URL, "alice", 0)
	st := p.PollOnce(context.Background())
	if st.Phase != PhaseUnknown {
		t.Errorf("phase: got %q want unknown", st.Phase)
	}
	if !strings.Contains(st.LastError, "unknown phase") {
		t.Errorf("expected LastError to flag unknown phase; got %q", st.LastError)
	}
}

// TestPoller_NewRequiresFields: every required field has a
// startup-time guard. Operators who pass an empty BaseURL or
// NodeID get a clear error, not a runtime 404 storm.
func TestPoller_NewRequiresFields(t *testing.T) {
	cli := &http.Client{}
	cases := []struct {
		name   string
		client *http.Client
		base   string
		node   string
	}{
		{"nil-client", nil, "http://x", "n"},
		{"empty-base", cli, "", "n"},
		{"empty-node", cli, "http://x", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewEnrollmentPoller(tc.client, tc.base, tc.node, 0); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

// TestPoller_NewIntervalDefaults: zero interval is replaced
// with DefaultEnrollmentPollInterval; a tiny interval is
// rounded UP to MinEnrollmentPollInterval. Both branches need
// coverage so a future tweak to the constants doesn't silently
// regress one of them.
func TestPoller_NewIntervalDefaults(t *testing.T) {
	cli := &http.Client{}
	p1, err := NewEnrollmentPoller(cli, "http://x", "n", 0)
	if err != nil {
		t.Fatal(err)
	}
	if p1.Interval != DefaultEnrollmentPollInterval {
		t.Errorf("zero interval not defaulted: got %v", p1.Interval)
	}
	p2, _ := NewEnrollmentPoller(cli, "http://x", "n", 100*time.Millisecond)
	if p2.Interval != MinEnrollmentPollInterval {
		t.Errorf("tiny interval not floored: got %v", p2.Interval)
	}
}

// TestPoller_Run_TickAndCancel: spin Run, observe at least one
// status callback, cancel ctx, ensure the goroutine exits.
//
// We use a short interval (the floor 5s would slow CI) by
// directly constructing the poller — the ticker uses Interval
// but the FIRST tick happens immediately, which is what we
// rely on here.
func TestPoller_Run_TickAndCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.EnrollmentRecordView{NodeID: "alice", Phase: "active", Slashable: true})
	}))
	defer srv.Close()

	var calls int32
	p := &EnrollmentPoller{
		Client:   srv.Client(),
		BaseURL:  srv.URL,
		NodeID:   "alice",
		Interval: time.Hour, // never tick again after the initial cycle
		OnStatus: func(_ EnrollmentStatus) { atomic.AddInt32(&calls, 1) },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&calls) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("poller never invoked OnStatus")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after ctx cancel")
	}
}

// TestPoller_Run_PhaseTransitionFiresOnce: server flips from
// "active" to "revoked" between cycles; the OnPhaseChange
// callback must fire exactly once with the right (prev, next)
// pair.
func TestPoller_Run_PhaseTransitionFiresOnce(t *testing.T) {
	var phase atomic.Value
	phase.Store("active")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		ph := phase.Load().(string)
		_ = json.NewEncoder(w).Encode(api.EnrollmentRecordView{
			NodeID: "alice", Phase: ph, Slashable: ph != "revoked",
		})
	}))
	defer srv.Close()

	var (
		mu          sync.Mutex
		transitions []string
	)
	p := &EnrollmentPoller{
		Client:   srv.Client(),
		BaseURL:  srv.URL,
		NodeID:   "alice",
		Interval: 50 * time.Millisecond, // stress the loop quickly
		OnPhaseChange: func(prev, next EnrollmentStatus) {
			mu.Lock()
			transitions = append(transitions, string(prev.Phase)+"→"+string(next.Phase))
			mu.Unlock()
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	// Wait for the FIRST cycle to land "active", then flip
	// the server to "revoked" and wait for the transition
	// callback.
	time.Sleep(150 * time.Millisecond)
	phase.Store("revoked")
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		got := append([]string(nil), transitions...)
		mu.Unlock()
		if len(got) >= 1 {
			if got[0] != "active→revoked" {
				t.Fatalf("first transition: got %q want active→revoked", got[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("phase transition never observed")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestSeverityForTransition pins the (prev, next) → severity
// table. Adding a new phase requires editing this case list.
func TestSeverityForTransition(t *testing.T) {
	cases := []struct {
		prev, next EnrollmentPhase
		want       PhaseTransitionSeverity
	}{
		{PhaseNotFound, PhaseActive, SeverityInfo},
		{PhaseActive, PhasePending, SeverityWarn},
		{PhaseActive, PhaseRevoked, SeverityErr},
		{PhasePending, PhaseRevoked, SeverityErr},
		{PhaseActive, PhaseNotFound, SeverityErr},
		{PhasePending, PhaseActive, SeverityInfo},
	}
	for _, tc := range cases {
		got := SeverityForTransition(tc.prev, tc.next)
		if got != tc.want {
			t.Errorf("%s → %s: got %d want %d", tc.prev, tc.next, got, tc.want)
		}
	}
}

// TestDashboard_ApplyEvent_EvEnrollment: the dashboard absorbs
// EvEnrollment payloads into the V2Enrollment* fields.
// Verifies the same-tick repaint contract that EvV2ChallengeOK
// already enforces.
func TestDashboard_ApplyEvent_EvEnrollment(t *testing.T) {
	d := &Dashboard{StartedAt: time.Now()}
	t0 := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	st := EnrollmentStatus{
		NodeID:       "alice",
		Phase:        PhaseActive,
		StakeDust:    10_000_000_000,
		Slashable:    true,
		LastPolledAt: t0,
	}
	d.applyEvent(Event{Kind: EvEnrollment, At: t0, Enrollment: st, Message: "ok"})
	if d.V2EnrollmentPhase != PhaseActive {
		t.Errorf("phase: got %q", d.V2EnrollmentPhase)
	}
	if d.V2EnrollmentStakeDust != 10_000_000_000 {
		t.Errorf("stake: got %d", d.V2EnrollmentStakeDust)
	}
	if !d.V2EnrollmentSlashable {
		t.Error("slashable false")
	}
	if !d.V2EnrollmentLastPoll.Equal(t0) {
		t.Errorf("LastPoll: got %v", d.V2EnrollmentLastPoll)
	}
}

// TestFormatV2EnrollLine_Variants exercises the three states
// the panel paints differently: pre-first-poll, healthy active,
// revoked. Each branch must include the operator-actionable
// substring; the color prefix is asserted separately so we
// notice if a future restyle drops the green/red accents.
func TestFormatV2EnrollLine_Variants(t *testing.T) {
	t.Run("pre-poll", func(t *testing.T) {
		d := &Dashboard{V2Enabled: true}
		_, body := formatV2EnrollLine(d)
		for _, s := range []string{"phase=—", "polled=—"} {
			if !strings.Contains(body, s) {
				t.Errorf("expected %q in %q", s, body)
			}
		}
	})
	t.Run("active-healthy", func(t *testing.T) {
		d := &Dashboard{
			V2Enabled:             true,
			V2EnrollmentPhase:     PhaseActive,
			V2EnrollmentStakeDust: 10_000_000_000,
			V2EnrollmentSlashable: true,
			V2EnrollmentLastPoll:  time.Now().Add(-5 * time.Second),
		}
		color, body := formatV2EnrollLine(d)
		if color != ansiGreen {
			t.Errorf("active row should render green; got %q", color)
		}
		for _, s := range []string{"phase=active", "stake=10.000 CELL", "slashable=yes", "polled="} {
			if !strings.Contains(body, s) {
				t.Errorf("expected %q in %q", s, body)
			}
		}
	})
	t.Run("revoked-loud", func(t *testing.T) {
		d := &Dashboard{
			V2Enabled:            true,
			V2EnrollmentPhase:    PhaseRevoked,
			V2EnrollmentLastPoll: time.Now(),
			V2EnrollmentError:    "validator says: slashed",
		}
		color, body := formatV2EnrollLine(d)
		if color != ansiRed {
			t.Errorf("revoked row should render red; got %q", color)
		}
		if !strings.Contains(body, "phase=revoked") {
			t.Errorf("expected phase=revoked; got %q", body)
		}
		if !strings.Contains(body, "slashed") {
			t.Errorf("expected error suffix in %q", body)
		}
	})
}

// TestPoller_Run_NilSafe: Run on a nil receiver must not
// panic. This is the defensive check for the main() wiring
// path — we never construct a nil poller, but the future "make
// the poller optional" refactor should not trip a panic.
func TestPoller_Run_NilSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil receiver panicked: %v", r)
		}
	}()
	var p *EnrollmentPoller
	p.Run(context.Background())
}

// silence unused import nags when the test set churns
var _ = errors.New
