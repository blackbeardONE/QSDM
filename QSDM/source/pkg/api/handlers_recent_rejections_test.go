package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeRecentRejectionLister is an in-memory test double for
// RecentRejectionLister. The handler tests do not need to
// exercise the diff/eviction logic of the real store
// (covered separately in
// pkg/mining/attest/recentrejects/recentrejects_test.go); they
// only validate request parsing, filter forwarding, and
// response shape.
type fakeRecentRejectionLister struct {
	records  []RecentRejectionView
	lastOpts RecentRejectionListOptions
}

func (f *fakeRecentRejectionLister) List(opts RecentRejectionListOptions) RecentRejectionListPage {
	f.lastOpts = opts

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	matched := []RecentRejectionView{}
	for _, r := range f.records {
		if r.Seq <= opts.Cursor {
			continue
		}
		if opts.Kind != "" && r.Kind != opts.Kind {
			continue
		}
		if opts.Reason != "" && r.Reason != opts.Reason {
			continue
		}
		if opts.Arch != "" && r.Arch != opts.Arch {
			continue
		}
		if opts.SinceUnixSec > 0 && r.RecordedAt.Unix() < opts.SinceUnixSec {
			continue
		}
		matched = append(matched, r)
	}

	page := RecentRejectionListPage{}
	if len(matched) > limit {
		page.Records = matched[:limit]
		page.HasMore = true
	} else {
		page.Records = matched
	}
	page.TotalMatches = uint64(len(page.Records))
	if len(page.Records) > 0 {
		page.NextCursor = page.Records[len(page.Records)-1].Seq
	}
	return page
}

func sampleRecord(seq uint64, kind, reason, arch string, t time.Time) RecentRejectionView {
	return RecentRejectionView{
		Seq:        seq,
		RecordedAt: t,
		Kind:       kind,
		Reason:     reason,
		Arch:       arch,
		Height:     1000 + seq,
		MinerAddr:  fmt.Sprintf("miner-%d", seq),
		Detail:     fmt.Sprintf("rejection #%d", seq),
	}
}

// -----------------------------------------------------------------------------
// happy paths
// -----------------------------------------------------------------------------

func TestRecentRejectionsHandler_HappyPath(t *testing.T) {
	t0 := time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC)
	lister := &fakeRecentRejectionLister{
		records: []RecentRejectionView{
			sampleRecord(1, "archspoof_unknown_arch", "unknown_arch", "rubin", t0),
			sampleRecord(2, "hashrate_out_of_band", "", "hopper", t0.Add(time.Second)),
		},
	}
	SetRecentRejectionLister(lister)
	t.Cleanup(func() { SetRecentRejectionLister(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/attest/recent-rejections", nil)
	rec := httptest.NewRecorder()
	h.RecentRejectionsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var view RecentRejectionsListPageView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode body: %v\n%s", err, rec.Body.String())
	}
	if len(view.Records) != 2 {
		t.Errorf("records: got %d, want 2: %+v", len(view.Records), view.Records)
	}
	if view.Records[0].Seq != 1 || view.Records[1].Seq != 2 {
		t.Errorf("seq order: %+v", view.Records)
	}
}

func TestRecentRejectionsHandler_EmptyStore_Returns200(t *testing.T) {
	lister := &fakeRecentRejectionLister{}
	SetRecentRejectionLister(lister)
	t.Cleanup(func() { SetRecentRejectionLister(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/attest/recent-rejections", nil)
	rec := httptest.NewRecorder()
	h.RecentRejectionsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	var view RecentRejectionsListPageView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if view.Records == nil {
		t.Error("records must be non-nil empty array")
	}
	if view.HasMore {
		t.Error("empty store: has_more should be false")
	}
}

// -----------------------------------------------------------------------------
// 503 / 405 paths
// -----------------------------------------------------------------------------

func TestRecentRejectionsHandler_NotConfiguredReturns503(t *testing.T) {
	SetRecentRejectionLister(nil)
	t.Cleanup(func() { SetRecentRejectionLister(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/attest/recent-rejections", nil)
	rec := httptest.NewRecorder()
	h.RecentRejectionsHandler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRecentRejectionsHandler_RejectsNonGET(t *testing.T) {
	SetRecentRejectionLister(&fakeRecentRejectionLister{})
	t.Cleanup(func() { SetRecentRejectionLister(nil) })

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method,
			"/api/v1/attest/recent-rejections", nil)
		rec := httptest.NewRecorder()
		(&Handlers{}).RecentRejectionsHandler(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: got %d, want 405", method, rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != http.MethodGet {
			t.Errorf("%s allow header: got %q", method, got)
		}
	}
}

// -----------------------------------------------------------------------------
// filter validation
// -----------------------------------------------------------------------------

func TestRecentRejectionsHandler_RejectsBogusKind(t *testing.T) {
	SetRecentRejectionLister(&fakeRecentRejectionLister{})
	t.Cleanup(func() { SetRecentRejectionLister(nil) })

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/attest/recent-rejections?kind=bogus", nil)
	rec := httptest.NewRecorder()
	(&Handlers{}).RecentRejectionsHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRecentRejectionsHandler_RejectsBogusReason(t *testing.T) {
	SetRecentRejectionLister(&fakeRecentRejectionLister{})
	t.Cleanup(func() { SetRecentRejectionLister(nil) })

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/attest/recent-rejections?reason=mystery", nil)
	rec := httptest.NewRecorder()
	(&Handlers{}).RecentRejectionsHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestRecentRejectionsHandler_RejectsBogusArch(t *testing.T) {
	SetRecentRejectionLister(&fakeRecentRejectionLister{})
	t.Cleanup(func() { SetRecentRejectionLister(nil) })

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/attest/recent-rejections?arch=volta", nil)
	rec := httptest.NewRecorder()
	(&Handlers{}).RecentRejectionsHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestRecentRejectionsHandler_RejectsNegativeLimit(t *testing.T) {
	SetRecentRejectionLister(&fakeRecentRejectionLister{})
	t.Cleanup(func() { SetRecentRejectionLister(nil) })

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/attest/recent-rejections?limit=-1", nil)
	rec := httptest.NewRecorder()
	(&Handlers{}).RecentRejectionsHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestRecentRejectionsHandler_RejectsBadCursor(t *testing.T) {
	SetRecentRejectionLister(&fakeRecentRejectionLister{})
	t.Cleanup(func() { SetRecentRejectionLister(nil) })

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/attest/recent-rejections?cursor=xyz", nil)
	rec := httptest.NewRecorder()
	(&Handlers{}).RecentRejectionsHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestRecentRejectionsHandler_RejectsBadSince(t *testing.T) {
	SetRecentRejectionLister(&fakeRecentRejectionLister{})
	t.Cleanup(func() { SetRecentRejectionLister(nil) })

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/attest/recent-rejections?since=-5", nil)
	rec := httptest.NewRecorder()
	(&Handlers{}).RecentRejectionsHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

// -----------------------------------------------------------------------------
// filter forwarding
// -----------------------------------------------------------------------------

func TestRecentRejectionsHandler_ForwardsFilters(t *testing.T) {
	lister := &fakeRecentRejectionLister{}
	SetRecentRejectionLister(lister)
	t.Cleanup(func() { SetRecentRejectionLister(nil) })

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/attest/recent-rejections?cursor=42&limit=50&kind=hashrate_out_of_band&arch=hopper&since=1714291200",
		nil)
	rec := httptest.NewRecorder()
	(&Handlers{}).RecentRejectionsHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	if lister.lastOpts.Cursor != 42 {
		t.Errorf("cursor: got %d", lister.lastOpts.Cursor)
	}
	if lister.lastOpts.Limit != 50 {
		t.Errorf("limit: got %d", lister.lastOpts.Limit)
	}
	if lister.lastOpts.Kind != "hashrate_out_of_band" {
		t.Errorf("kind: got %q", lister.lastOpts.Kind)
	}
	if lister.lastOpts.Arch != "hopper" {
		t.Errorf("arch: got %q", lister.lastOpts.Arch)
	}
	if lister.lastOpts.SinceUnixSec != 1714291200 {
		t.Errorf("since: got %d", lister.lastOpts.SinceUnixSec)
	}
}

func TestRecentRejectionsHandler_ClampsLimit(t *testing.T) {
	lister := &fakeRecentRejectionLister{}
	SetRecentRejectionLister(lister)
	t.Cleanup(func() { SetRecentRejectionLister(nil) })

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/attest/recent-rejections?limit=99999", nil)
	rec := httptest.NewRecorder()
	(&Handlers{}).RecentRejectionsHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	if lister.lastOpts.Limit != MaxRecentRejectionListLimit {
		t.Errorf("limit clamp: got %d, want %d",
			lister.lastOpts.Limit, MaxRecentRejectionListLimit)
	}
}

func TestRecentRejectionsHandler_EchoedFilters(t *testing.T) {
	lister := &fakeRecentRejectionLister{
		records: []RecentRejectionView{
			sampleRecord(1, "archspoof_unknown_arch", "unknown_arch", "rubin",
				time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC)),
		},
	}
	SetRecentRejectionLister(lister)
	t.Cleanup(func() { SetRecentRejectionLister(nil) })

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/attest/recent-rejections?reason=unknown_arch", nil)
	rec := httptest.NewRecorder()
	(&Handlers{}).RecentRejectionsHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"filters"`) {
		t.Errorf("body should echo filters: %s", body)
	}
	if !strings.Contains(body, `"reason":"unknown_arch"`) {
		t.Errorf("filters should echo reason=unknown_arch: %s", body)
	}
}

// -----------------------------------------------------------------------------
// content-type
// -----------------------------------------------------------------------------

func TestRecentRejectionsHandler_ContentTypeJSON(t *testing.T) {
	SetRecentRejectionLister(&fakeRecentRejectionLister{})
	t.Cleanup(func() { SetRecentRejectionLister(nil) })

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/attest/recent-rejections", nil)
	rec := httptest.NewRecorder()
	(&Handlers{}).RecentRejectionsHandler(rec, req)
	got := rec.Header().Get("Content-Type")
	if !strings.Contains(got, "application/json") {
		t.Errorf("content-type: got %q, want application/json", got)
	}
}

// -----------------------------------------------------------------------------
// IsKnownRecentRejectionKind / KnownRecentRejectionKinds —
// shared closed-enum surface for in-process consumers
// (primarily internal/dashboard's attest-rejections tile).
// -----------------------------------------------------------------------------

// TestIsKnownRecentRejectionKind_AcceptsAllAllowlistedValues
// pins the exhaustive set so a future allowlist trim or
// rename surfaces in dashboard land too — the dashboard
// imports this predicate as its single source of truth.
func TestIsKnownRecentRejectionKind_AcceptsAllAllowlistedValues(t *testing.T) {
	want := []string{
		"archspoof_unknown_arch",
		"archspoof_gpu_name_mismatch",
		"archspoof_cc_subject_mismatch",
		"hashrate_out_of_band",
	}
	for _, k := range want {
		if !IsKnownRecentRejectionKind(k) {
			t.Errorf("kind %q: not accepted by IsKnownRecentRejectionKind", k)
		}
	}
}

func TestIsKnownRecentRejectionKind_EmptyStringIsPermissive(t *testing.T) {
	// Empty input is the "no filter" case — the predicate
	// must return true so callers can chain it without a
	// special case for the bare-call path.
	if !IsKnownRecentRejectionKind("") {
		t.Error("empty string must be accepted (no-filter case)")
	}
}

func TestIsKnownRecentRejectionKind_RejectsTyposAndCaseVariants(t *testing.T) {
	bad := []string{
		"archspoof_unknown_arc",   // typo of *_arch
		"hashrate",                // valid prefix only
		"ARCHSPOOF_UNKNOWN_ARCH",  // case-sensitive enum
		"archspoof_unknown_arch ", // trailing whitespace
		"unknown",                 // valid arch, not a kind
	}
	for _, k := range bad {
		if IsKnownRecentRejectionKind(k) {
			t.Errorf("kind %q: unexpectedly accepted", k)
		}
	}
}

func TestKnownRecentRejectionKinds_StableOrderingAndCompleteness(t *testing.T) {
	// Order is the dashboard-tile dropdown's display order.
	// Reordering would change the UX without a CHANGELOG
	// note; pin it here so a future allowlist mutation
	// surfaces as a test failure.
	want := []string{
		"archspoof_unknown_arch",
		"archspoof_gpu_name_mismatch",
		"archspoof_cc_subject_mismatch",
		"hashrate_out_of_band",
	}
	got := KnownRecentRejectionKinds()
	if len(got) != len(want) {
		t.Fatalf("len(KnownRecentRejectionKinds())=%d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("KnownRecentRejectionKinds()[%d] = %q, want %q",
				i, got[i], w)
		}
	}
	// Snapshot is independent — mutating the returned slice
	// MUST NOT corrupt the underlying allowlist.
	got[0] = "MUTATED"
	if KnownRecentRejectionKinds()[0] != "archspoof_unknown_arch" {
		t.Error("returned slice aliases the underlying allowlist; defensive copy missing")
	}
}
