package dashboard

// Tests for handleSlashReceipts — the dashboard's slashing
// tile data endpoint. Coverage:
//
//   - Method gating (405 on non-GET).
//   - Limit query-parameter parsing + clamping.
//   - "v1-only deployment" path: lister not wired, the
//     handler still returns 200 with Available=false and a
//     metrics snapshot so the tile renders gracefully.
//   - "v2 deployment" path: lister wired, records returned
//     newest-first.
//   - Closed-enum filter validation: bogus outcome /
//     evidence_kind returns 400 with a helpful message.
//   - Filter passthrough: outcome / evidence_kind / since
//     are forwarded to the lister verbatim.
//   - Echoed-filters block omitted on a bare call (keeps
//     the wire payload tight).
//   - Metrics snapshot reflects the qsdm_slash_* counters
//     set via the package-level Record* entry points.
//
// The tests use a tiny in-memory fakeSlashReceiptLister
// because the real chain.SlashReceiptStore lives in pkg/chain
// (a higher-level package) and pulling it in here would
// invert the dependency arrow. The interface contract is
// the same one v2wiring uses, so the fake exercises the same
// code path the production wiring does.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// fakeSlashReceiptLister is a process-wide fake the tests
// install via api.SetSlashReceiptLister. List ignores most
// of opts and just returns the canned page; lastOpts captures
// what the handler passed so tests can assert on filter
// propagation.
type fakeSlashReceiptLister struct {
	page     api.SlashReceiptListPage
	lastOpts api.SlashReceiptListOptions
}

func (f *fakeSlashReceiptLister) List(opts api.SlashReceiptListOptions) api.SlashReceiptListPage {
	f.lastOpts = opts
	return f.page
}

// withCleanSlashReceiptWiring resets the package-level lister
// + slashing counters at test start AND end so neighbouring
// tests in this package don't observe carry-over state.
func withCleanSlashReceiptWiring(t *testing.T) {
	t.Helper()
	api.SetSlashReceiptLister(nil)
	monitoring.ResetSlashMetricsForTest()
	t.Cleanup(func() {
		api.SetSlashReceiptLister(nil)
		monitoring.ResetSlashMetricsForTest()
	})
}

// sampleReceipt returns a populated SlashReceiptView for a
// given tx id + outcome. RecordedAt is t0 + i*time.Hour so
// tests have a deterministic ordering signal.
func sampleReceipt(i int, txID, outcome, kind, slasher string, t0 time.Time) api.SlashReceiptView {
	return api.SlashReceiptView{
		TxID:                    txID,
		Outcome:                 outcome,
		RecordedAt:              t0.Add(time.Duration(i) * time.Hour),
		Height:                  uint64(100 + i),
		Slasher:                 slasher,
		NodeID:                  "rig-" + txID,
		EvidenceKind:            kind,
		SlashedDust:             100_000_000,
		RewardedDust:            10_000_000,
		BurnedDust:              90_000_000,
		AutoRevoked:             outcome == "applied",
		AutoRevokeRemainingDust: 0,
	}
}

// -----------------------------------------------------------
// Method gating + clamping
// -----------------------------------------------------------

func TestHandleSlashReceipts_MethodNotAllowed(t *testing.T) {
	withCleanSlashReceiptWiring(t)
	d := newTestDashboard()

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/api/mining/slash-receipts", nil)
		w := httptest.NewRecorder()
		d.handleSlashReceipts(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("method=%s: status = %d, want %d", method, w.Code, http.StatusMethodNotAllowed)
		}
		if got := w.Header().Get("Allow"); got != http.MethodGet {
			t.Errorf("method=%s: Allow header = %q, want %q", method, got, http.MethodGet)
		}
	}
}

func TestHandleSlashReceipts_LimitClamping(t *testing.T) {
	withCleanSlashReceiptWiring(t)
	d := newTestDashboard()

	lister := &fakeSlashReceiptLister{}
	api.SetSlashReceiptLister(lister)

	// Over-cap: clamps to dashboardSlashReceiptsMaxLimit (200).
	req := httptest.NewRequest(http.MethodGet,
		"/api/mining/slash-receipts?limit=99999", nil)
	w := httptest.NewRecorder()
	d.handleSlashReceipts(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if lister.lastOpts.Limit != 200 {
		t.Errorf("Limit clamp: got %d, want 200", lister.lastOpts.Limit)
	}

	// Negative: 400.
	req = httptest.NewRequest(http.MethodGet,
		"/api/mining/slash-receipts?limit=-5", nil)
	w = httptest.NewRecorder()
	d.handleSlashReceipts(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("negative limit: status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	// Non-integer: 400.
	req = httptest.NewRequest(http.MethodGet,
		"/api/mining/slash-receipts?limit=abc", nil)
	w = httptest.NewRecorder()
	d.handleSlashReceipts(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("non-integer limit: status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// -----------------------------------------------------------
// v1-only deployment path
// -----------------------------------------------------------

func TestHandleSlashReceipts_NoListerWired_AvailableFalse(t *testing.T) {
	withCleanSlashReceiptWiring(t)
	d := newTestDashboard()
	// Sanity: lister IS nil for this test.
	if api.CurrentSlashReceiptLister() != nil {
		t.Fatal("setup: expected nil lister")
	}

	// Bump some counters so the test asserts the metrics
	// block still surfaces even when the store is missing.
	monitoring.RecordSlashApplied("forged-attestation", 1_000_000_000)
	monitoring.RecordSlashRejected(monitoring.SlashRejectReasonVerifier)

	req := httptest.NewRequest(http.MethodGet,
		"/api/mining/slash-receipts", nil)
	w := httptest.NewRecorder()
	d.handleSlashReceipts(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (handler must render gracefully even without lister)",
			w.Code, http.StatusOK)
	}

	var got dashboardSlashReceiptsView
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Available {
		t.Error("Available = true with no lister; want false")
	}
	if got.Records == nil {
		t.Error("Records = nil; want [] so JSON decodes to an empty slice")
	}
	if got.Limit != dashboardSlashReceiptsDefaultLimit {
		t.Errorf("Limit = %d, want %d", got.Limit, dashboardSlashReceiptsDefaultLimit)
	}
	// Metrics block must still surface (counters set above).
	var appliedForged uint64
	for _, lc := range got.Metrics.AppliedByKind {
		if lc.Label == "forged-attestation" {
			appliedForged = lc.Value
		}
	}
	if appliedForged != 1 {
		t.Errorf("Metrics.AppliedByKind[forged-attestation] = %d, want 1", appliedForged)
	}
	var rejectedVerifier uint64
	for _, lc := range got.Metrics.RejectedByReason {
		if lc.Label == monitoring.SlashRejectReasonVerifier {
			rejectedVerifier = lc.Value
		}
	}
	if rejectedVerifier != 1 {
		t.Errorf("Metrics.RejectedByReason[verifier_failed] = %d, want 1", rejectedVerifier)
	}
}

// -----------------------------------------------------------
// v2 deployment path: happy path
// -----------------------------------------------------------

func TestHandleSlashReceipts_HappyPath(t *testing.T) {
	withCleanSlashReceiptWiring(t)
	d := newTestDashboard()

	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	lister := &fakeSlashReceiptLister{
		page: api.SlashReceiptListPage{
			Records: []api.SlashReceiptView{
				// Lister is documented to return NEWEST-FIRST; the
				// fake honours that — handler must NOT re-reverse.
				sampleReceipt(2, "tx-newest", "applied", "forged-attestation", "alice", t0),
				sampleReceipt(1, "tx-mid", "rejected", "double-mining", "bob", t0),
				sampleReceipt(0, "tx-oldest", "applied", "freshness-cheat", "alice", t0),
			},
			TotalMatches: 3,
		},
	}
	api.SetSlashReceiptLister(lister)

	req := httptest.NewRequest(http.MethodGet,
		"/api/mining/slash-receipts?limit=10", nil)
	w := httptest.NewRecorder()
	d.handleSlashReceipts(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var got dashboardSlashReceiptsView
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !got.Available {
		t.Error("Available = false with lister wired; want true")
	}
	if len(got.Records) != 3 {
		t.Fatalf("len(Records) = %d, want 3", len(got.Records))
	}
	wantOrder := []string{"tx-newest", "tx-mid", "tx-oldest"}
	for i, rec := range got.Records {
		if rec.TxID != wantOrder[i] {
			t.Errorf("Records[%d].TxID = %q, want %q (handler must preserve newest-first)",
				i, rec.TxID, wantOrder[i])
		}
	}
	if got.TotalMatches != 3 {
		t.Errorf("TotalMatches = %d, want 3", got.TotalMatches)
	}
	if got.Limit != 10 {
		t.Errorf("Limit echo = %d, want 10", got.Limit)
	}
	// Bare call (no filters) → no filters block.
	if got.Filters != nil {
		t.Errorf("Filters block = %+v on bare call; want nil (omitempty)", got.Filters)
	}

	if lister.lastOpts.Limit != 10 {
		t.Errorf("lister.lastOpts.Limit = %d, want 10", lister.lastOpts.Limit)
	}
	if lister.lastOpts.Outcome != "" || lister.lastOpts.EvidenceKind != "" || lister.lastOpts.SinceUnixSec != 0 {
		t.Errorf("lister.lastOpts had unexpected filters: %+v", lister.lastOpts)
	}
}

// -----------------------------------------------------------
// Closed-enum filter validation
// -----------------------------------------------------------

func TestHandleSlashReceipts_BogusOutcome_400(t *testing.T) {
	withCleanSlashReceiptWiring(t)
	d := newTestDashboard()
	api.SetSlashReceiptLister(&fakeSlashReceiptLister{})

	req := httptest.NewRequest(http.MethodGet,
		"/api/mining/slash-receipts?outcome=maybe", nil)
	w := httptest.NewRecorder()
	d.handleSlashReceipts(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("bogus outcome: status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "outcome must be one of") {
		t.Errorf("bogus outcome: body = %q; want allowlist hint", w.Body.String())
	}
}

func TestHandleSlashReceipts_BogusEvidenceKind_400(t *testing.T) {
	withCleanSlashReceiptWiring(t)
	d := newTestDashboard()
	api.SetSlashReceiptLister(&fakeSlashReceiptLister{})

	req := httptest.NewRequest(http.MethodGet,
		"/api/mining/slash-receipts?evidence_kind=triple-mining", nil)
	w := httptest.NewRecorder()
	d.handleSlashReceipts(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("bogus evidence_kind: status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "evidence_kind must be one of") {
		t.Errorf("bogus evidence_kind: body = %q; want allowlist hint", w.Body.String())
	}
}

func TestHandleSlashReceipts_BogusSince_400(t *testing.T) {
	withCleanSlashReceiptWiring(t)
	d := newTestDashboard()
	api.SetSlashReceiptLister(&fakeSlashReceiptLister{})

	for _, raw := range []string{"abc", "-1", "1." + url.QueryEscape("0")} {
		req := httptest.NewRequest(http.MethodGet,
			"/api/mining/slash-receipts?since="+raw, nil)
		w := httptest.NewRecorder()
		d.handleSlashReceipts(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("since=%q: status = %d, want %d", raw, w.Code, http.StatusBadRequest)
		}
	}
}

// -----------------------------------------------------------
// Filter passthrough + echo
// -----------------------------------------------------------

func TestHandleSlashReceipts_FilterPassthrough(t *testing.T) {
	withCleanSlashReceiptWiring(t)
	d := newTestDashboard()

	lister := &fakeSlashReceiptLister{}
	api.SetSlashReceiptLister(lister)

	req := httptest.NewRequest(http.MethodGet,
		"/api/mining/slash-receipts?outcome=rejected&evidence_kind=forged-attestation&since=1700000000", nil)
	w := httptest.NewRecorder()
	d.handleSlashReceipts(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if lister.lastOpts.Outcome != "rejected" {
		t.Errorf("lister.lastOpts.Outcome = %q, want rejected", lister.lastOpts.Outcome)
	}
	if lister.lastOpts.EvidenceKind != "forged-attestation" {
		t.Errorf("lister.lastOpts.EvidenceKind = %q, want forged-attestation", lister.lastOpts.EvidenceKind)
	}
	if lister.lastOpts.SinceUnixSec != 1700000000 {
		t.Errorf("lister.lastOpts.SinceUnixSec = %d, want 1700000000", lister.lastOpts.SinceUnixSec)
	}

	// Echoed filters block must be present and accurate.
	var got dashboardSlashReceiptsView
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Filters == nil {
		t.Fatal("Filters block missing on filtered call")
	}
	if got.Filters.Outcome != "rejected" || got.Filters.EvidenceKind != "forged-attestation" || got.Filters.Since != 1700000000 {
		t.Errorf("Filters echo = %+v; want {rejected, forged-attestation, 1700000000}", *got.Filters)
	}
}
