package v2wiring_test

// v2wiring_recentrejects_test.go: integration coverage for the
// new §4.6 recent-rejections ring. Validates that
// v2wiring.Wire():
//
//  1. Constructs a recentrejects.Store and exposes it on
//     Wired.RecentRejections.
//  2. Installs the same store as the api.RecentRejectionLister
//     so the GET /api/v1/attest/recent-rejections handler reads
//     the live producer-side state (no second instance, no
//     stale alias).
//  3. Installs the same store as the mining.RejectionRecorder
//     so the verifier hot path feeds it (validated via direct
//     handle round trip — the verifier-level call sites are
//     covered separately in pkg/mining/* unit tests).
//  4. The 503-fallback contract holds: a node booted WITHOUT
//     v2wiring.Wire() returns 503 from the read endpoint instead
//     of either crashing or returning empty results.
//
// Why integration here vs unit elsewhere:
//
//   - The store's eviction / filter logic is unit-tested in
//     pkg/mining/attest/recentrejects/recentrejects_test.go.
//   - The handler's request parsing / 4xx paths are unit-tested
//     in pkg/api/handlers_recent_rejections_test.go.
//   - What is NOT covered there: does Wire() actually surface
//     the same store under both interfaces? A future refactor
//     that splits the producer-side and consumer-side stores
//     would silently break operator dashboards unless this
//     test failed loudly.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/recentrejects"
)

// TestWire_RecentRejections_StoreSurfacesThroughHandler drives
// the end-to-end consumer path:
//
//	Wire().RecentRejections.Record(...)         (producer-side)
//	    → recentRejectionListerAdapter (set on api by Wire)
//	    → RecentRejectionsHandler                (consumer-side)
//	    → 200 OK with the record visible.
//
// A drift between producer-side and consumer-side wiring (e.g.
// Wire constructs a fresh store for the handler instead of
// reusing the one fed by the verifier) makes this test fail
// with empty Records[].
func TestWire_RecentRejections_StoreSurfacesThroughHandler(t *testing.T) {
	r := buildRig(t, 20)

	if r.w.RecentRejections == nil {
		t.Fatal("Wire did not populate Wired.RecentRejections")
	}

	// Write directly into the store the producer-side adapter
	// also writes into. This exercises the consumer adapter +
	// handler without staging a forged proof through the full
	// verifier path (covered by pkg/mining unit tests).
	rec := recentrejects.Rejection{
		Kind:       recentrejects.KindArchSpoofUnknown,
		Reason:     "unknown_arch",
		Arch:       "rubin",
		RecordedAt: time.Date(2026, 4, 29, 8, 0, 0, 0, time.UTC),
		Height:     1234,
		MinerAddr:  "qsdm1miner-spoofer",
		Detail:     "archcheck: arch \"rubin\" not in allowlist",
	}
	if seq := r.w.RecentRejections.Record(rec); seq == 0 {
		t.Fatal("store.Record returned seq=0 (nil store?)")
	}

	// Read through the production HTTP handler.
	h := &api.Handlers{}
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/attest/recent-rejections", nil)
	w := httptest.NewRecorder()
	h.RecentRejectionsHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("handler status: got %d, want 200; body=%s",
			w.Code, w.Body.String())
	}
	var view api.RecentRejectionsListPageView
	if err := json.NewDecoder(w.Body).Decode(&view); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(view.Records) != 1 {
		t.Fatalf("records: got %d, want 1: %+v", len(view.Records), view.Records)
	}
	got := view.Records[0]
	if got.Kind != string(recentrejects.KindArchSpoofUnknown) {
		t.Errorf("kind: got %q", got.Kind)
	}
	if got.Reason != "unknown_arch" {
		t.Errorf("reason: got %q", got.Reason)
	}
	if got.Arch != "rubin" {
		t.Errorf("arch: got %q", got.Arch)
	}
	if got.Height != 1234 {
		t.Errorf("height: got %d, want 1234", got.Height)
	}
	if got.MinerAddr != "qsdm1miner-spoofer" {
		t.Errorf("miner_addr: got %q", got.MinerAddr)
	}
	if got.Seq == 0 {
		t.Errorf("seq must be >0; got %d", got.Seq)
	}
}

// TestWire_RecentRejections_GPUNameSurfaces locks the
// end-to-end "gpu_name reaches the operator" invariant
// introduced with the archcheck.RejectionDetail wrapper. The
// store accepts a Rejection with GPUName populated and the
// HTTP handler surfaces it byte-for-byte on the wire view —
// any future refactor that drops the field anywhere along the
// chain (store → adapter → view → handler) makes this test
// fail loudly.
func TestWire_RecentRejections_GPUNameSurfaces(t *testing.T) {
	r := buildRig(t, 20)

	r.w.RecentRejections.Record(recentrejects.Rejection{
		Kind:    recentrejects.KindArchSpoofGPUNameMismatch,
		Reason:  "gpu_name_mismatch",
		Arch:    "hopper",
		GPUName: "NVIDIA GeForce RTX 4090",
		Detail:  "lazy spoof: claimed hopper, smi reported Ada",
	})

	h := &api.Handlers{}
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/attest/recent-rejections", nil)
	w := httptest.NewRecorder()
	h.RecentRejectionsHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("handler status: got %d, body=%s", w.Code, w.Body.String())
	}
	var view api.RecentRejectionsListPageView
	if err := json.NewDecoder(w.Body).Decode(&view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(view.Records) != 1 {
		t.Fatalf("records: got %d", len(view.Records))
	}
	if view.Records[0].GPUName != "NVIDIA GeForce RTX 4090" {
		t.Errorf("GPUName must surface end-to-end; got %q",
			view.Records[0].GPUName)
	}
	if view.Records[0].CertSubject != "" {
		t.Errorf("CertSubject must stay empty on HMAC path; got %q",
			view.Records[0].CertSubject)
	}
}

// TestWire_RecentRejections_CertSubjectSurfaces — CC-path
// counterpart to the GPUName test.
func TestWire_RecentRejections_CertSubjectSurfaces(t *testing.T) {
	r := buildRig(t, 20)

	r.w.RecentRejections.Record(recentrejects.Rejection{
		Kind:        recentrejects.KindArchSpoofCCSubjectMismatch,
		Reason:      "cc_subject_mismatch",
		Arch:        "hopper",
		CertSubject: "NVIDIA GeForce RTX 4090",
		Detail:      "leaf cert subject contradicts claimed arch",
	})

	h := &api.Handlers{}
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/attest/recent-rejections", nil)
	w := httptest.NewRecorder()
	h.RecentRejectionsHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("handler status: got %d", w.Code)
	}
	var view api.RecentRejectionsListPageView
	if err := json.NewDecoder(w.Body).Decode(&view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(view.Records) != 1 {
		t.Fatalf("records: got %d", len(view.Records))
	}
	if view.Records[0].CertSubject != "NVIDIA GeForce RTX 4090" {
		t.Errorf("CertSubject must surface end-to-end; got %q",
			view.Records[0].CertSubject)
	}
	if view.Records[0].GPUName != "" {
		t.Errorf("GPUName must stay empty on CC path; got %q",
			view.Records[0].GPUName)
	}
}

// TestWire_RecentRejections_KindFilterRoundTrip validates that
// the production wiring path filters at the store layer (not
// just the handler validation layer). A bug where the adapter
// forgets to forward opts.Kind into the store would make a
// kind-filtered query return every record.
func TestWire_RecentRejections_KindFilterRoundTrip(t *testing.T) {
	r := buildRig(t, 20)

	r.w.RecentRejections.Record(recentrejects.Rejection{
		Kind: recentrejects.KindArchSpoofUnknown, Reason: "unknown_arch", Arch: "rubin",
	})
	r.w.RecentRejections.Record(recentrejects.Rejection{
		Kind: recentrejects.KindHashrateOutOfBand, Arch: "hopper",
	})

	h := &api.Handlers{}
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/attest/recent-rejections?kind=hashrate_out_of_band", nil)
	w := httptest.NewRecorder()
	h.RecentRejectionsHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("handler status: got %d, body=%s", w.Code, w.Body.String())
	}
	var view api.RecentRejectionsListPageView
	if err := json.NewDecoder(w.Body).Decode(&view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(view.Records) != 1 {
		t.Fatalf("filtered records: got %d, want 1: %+v", len(view.Records), view.Records)
	}
	if view.Records[0].Kind != string(recentrejects.KindHashrateOutOfBand) {
		t.Errorf("filter mismatch: got kind=%q", view.Records[0].Kind)
	}
	if view.EchoedFilters.Kind != "hashrate_out_of_band" {
		t.Errorf("echoed filter: got %q", view.EchoedFilters.Kind)
	}
}

// TestWire_RecentRejections_PaginationRoundTrip drives a
// multi-page walk through the handler. Catches a class of bugs
// where the cursor is forwarded but never echoed back, or where
// the lister adapter forgets to populate NextCursor.
func TestWire_RecentRejections_PaginationRoundTrip(t *testing.T) {
	r := buildRig(t, 20)

	for i := 0; i < 7; i++ {
		r.w.RecentRejections.Record(recentrejects.Rejection{
			Kind:   recentrejects.KindArchSpoofUnknown,
			Reason: "unknown_arch",
			Arch:   "rubin",
		})
	}

	// Page 1: limit=4 ⇒ 4 records, has_more=true.
	h := &api.Handlers{}
	req1 := httptest.NewRequest(http.MethodGet,
		"/api/v1/attest/recent-rejections?limit=4", nil)
	w1 := httptest.NewRecorder()
	h.RecentRejectionsHandler(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("page1 status: got %d, body=%s", w1.Code, w1.Body.String())
	}
	var p1 api.RecentRejectionsListPageView
	if err := json.NewDecoder(w1.Body).Decode(&p1); err != nil {
		t.Fatalf("page1 decode: %v", err)
	}
	if len(p1.Records) != 4 {
		t.Fatalf("page1: got %d records, want 4", len(p1.Records))
	}
	if !p1.HasMore {
		t.Error("page1 has_more should be true")
	}

	// Page 2: cursor=p1.NextCursor ⇒ remaining 3, has_more=false.
	url2 := "/api/v1/attest/recent-rejections?limit=4&cursor=" +
		uintToStr(p1.NextCursor)
	req2 := httptest.NewRequest(http.MethodGet, url2, nil)
	w2 := httptest.NewRecorder()
	h.RecentRejectionsHandler(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("page2 status: got %d, body=%s", w2.Code, w2.Body.String())
	}
	var p2 api.RecentRejectionsListPageView
	if err := json.NewDecoder(w2.Body).Decode(&p2); err != nil {
		t.Fatalf("page2 decode: %v", err)
	}
	if len(p2.Records) != 3 {
		t.Fatalf("page2: got %d records, want 3", len(p2.Records))
	}
	if p2.HasMore {
		t.Error("page2 has_more should be false (drained)")
	}
	if p2.Records[0].Seq != p1.NextCursor+1 {
		t.Errorf("page2 first seq: got %d, want %d",
			p2.Records[0].Seq, p1.NextCursor+1)
	}
}

// TestWire_RecentRejections_NotConfiguredReturns503 mirrors the
// posture of TestWire_SlashReceipt_NotConfiguredReturns503: a
// node booted WITHOUT v2wiring.Wire() has no lister installed,
// and the handler must say so distinctly from "store is empty".
func TestWire_RecentRejections_NotConfiguredReturns503(t *testing.T) {
	api.SetRecentRejectionLister(nil)
	t.Cleanup(func() { api.SetRecentRejectionLister(nil) })

	h := &api.Handlers{}
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/attest/recent-rejections", nil)
	w := httptest.NewRecorder()
	h.RecentRejectionsHandler(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503; body=%s",
			w.Code, w.Body.String())
	}
}

// uintToStr renders a uint64 as decimal. Tiny helper to avoid
// pulling strconv in just for one call site.
func uintToStr(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
