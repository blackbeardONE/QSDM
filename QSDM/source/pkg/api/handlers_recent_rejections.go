package api

// Recent §4.6 attestation rejections READ endpoint.
//
//	GET /api/v1/attest/recent-rejections
//	    [?cursor=<seq>] [?limit=N] [?kind=...] [?reason=...]
//	    [?arch=...]    [?since=<unix-secs>]
//
// Per-event detail companion to the Prometheus
// `qsdm_attest_archspoof_rejected_total{reason}` and
// `qsdm_attest_hashrate_rejected_total{arch}` counters: the
// metrics layer answers "how many" by reason/arch label, this
// endpoint answers "who, when, with what claimed gpu_arch /
// gpu_name / leaf-cert subject". Operators correlate watcher
// bursts against this read path during incident response.
//
// Why a separate handler (vs piggy-backing on slash receipts):
//
//   - Slash receipts are keyed by tx_id with a 404/200 lifecycle;
//     rejections have no external key — they are an append-only
//     stream observed entirely by the validator.
//   - Slash receipts live in pkg/chain (consensus-state-adjacent);
//     rejection records live in pkg/mining/attest/recentrejects
//     (verifier-time only, never persisted to chain).
//   - The wire shape is fundamentally different: outcome-based
//     for slashes, evidence-based here.
//
// 503 vs 404 vs empty:
//
//   - 503 means "this node has no recent-rejections store wired"
//     (v1-only deployment, or governance feature flag off).
//   - 200 with empty Records[] means "store wired, no rejections
//     observed since boot (or all evicted)". Distinct from 503
//     so dashboards can show "0 rejections in window" without
//     falsely warning about an unconfigured node.
//   - There is no 404 — the endpoint is collection-only; an
//     unknown cursor returns 200 with empty Records[] and
//     HasMore=false (the caller has paged past the end).

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RecentRejectionView is the wire shape for one record on
// GET /api/v1/attest/recent-rejections.
//
// Field set is grouped by §4.6 origin:
//
//   - Always populated: Seq, RecordedAt, Kind.
//   - Archspoof-* kinds populate Reason; hashrate kinds leave
//     it empty and populate Arch instead.
//   - GPUName is HMAC-only; CertSubject is CC-only. Both can
//     be empty when the underlying verifier did not surface
//     them (graceful — the metrics counter still fired).
//
// JSON tag names below are the public contract; reordering
// fields here is fine, renaming any of them is a breaking
// change.
type RecentRejectionView struct {
	Seq         uint64    `json:"seq"`
	RecordedAt  time.Time `json:"recorded_at"`
	Kind        string    `json:"kind"`
	Reason      string    `json:"reason,omitempty"`
	Arch        string    `json:"arch,omitempty"`
	Height      uint64    `json:"height,omitempty"`
	MinerAddr   string    `json:"miner_addr,omitempty"`
	GPUName     string    `json:"gpu_name,omitempty"`
	CertSubject string    `json:"cert_subject,omitempty"`
	Detail      string    `json:"detail,omitempty"`
}

// RecentRejectionsListPageView is the wire shape for one page
// of list results. Records reuses RecentRejectionView so
// clients can decode via a single struct.
//
// NextCursor / HasMore mirror the EnrollmentListPageView
// idiom — pass NextCursor back unchanged on the next call;
// HasMore is true iff there is at least one more match after
// the last record returned.
type RecentRejectionsListPageView struct {
	Records      []RecentRejectionView `json:"records"`
	NextCursor   uint64                `json:"next_cursor,omitempty"`
	HasMore      bool                  `json:"has_more"`
	TotalMatches uint64                `json:"total_matches"`
	// EchoedFilters are the parsed filters the server applied
	// to this page. Empty fields are omitted; the client uses
	// this to confirm what the server interpreted (e.g. that a
	// typo'd `kind` was rejected, not silently dropped).
	EchoedFilters RecentRejectionsEchoedFilters `json:"filters,omitempty"`
}

// RecentRejectionsEchoedFilters is the server-acknowledged
// filter set for the current page. Always serialised together
// with the page so clients can audit "did the server actually
// filter on what I asked, or did it ignore an unknown query
// parameter?".
type RecentRejectionsEchoedFilters struct {
	Kind   string `json:"kind,omitempty"`
	Reason string `json:"reason,omitempty"`
	Arch   string `json:"arch,omitempty"`
	Since  int64  `json:"since,omitempty"`
}

// RecentRejectionLister is the narrow read-only interface this
// handler depends on. Concrete implementation lives in the
// adapter installed by internal/v2wiring; pkg/api stays free of
// pkg/mining/attest/recentrejects imports.
type RecentRejectionLister interface {
	List(opts RecentRejectionListOptions) RecentRejectionListPage
}

// RecentRejectionListOptions is the pkg/api-side mirror of
// recentrejects.ListOptions. Identical field set; redeclared
// here so pkg/api need not import the store package.
type RecentRejectionListOptions struct {
	Cursor       uint64
	Limit        int
	Kind         string
	Reason       string
	Arch         string
	SinceUnixSec int64
}

// RecentRejectionListPage is the adapter-side return shape. The
// adapter translates recentrejects.ListPage into this — keeps
// pkg/api free of the concrete store import.
type RecentRejectionListPage struct {
	Records      []RecentRejectionView
	NextCursor   uint64
	HasMore      bool
	TotalMatches uint64
}

type recentRejectionListerHolder struct {
	mu     sync.RWMutex
	lister RecentRejectionLister
}

var recentRejectionHolder = &recentRejectionListerHolder{}

// SetRecentRejectionLister installs (or removes, when
// lister==nil) the process-wide lister the GET handler uses.
// internal/v2wiring calls this at boot with a thin adapter
// over the bounded recentrejects.Store; tests can call it
// with a fake.
func SetRecentRejectionLister(lister RecentRejectionLister) {
	recentRejectionHolder.mu.Lock()
	defer recentRejectionHolder.mu.Unlock()
	recentRejectionHolder.lister = lister
}

func currentRecentRejectionLister() RecentRejectionLister {
	recentRejectionHolder.mu.RLock()
	defer recentRejectionHolder.mu.RUnlock()
	return recentRejectionHolder.lister
}

// MaxRecentRejectionListLimit caps server-side page size.
// Mirrors recentrejects.MaxListLimit so client clamping and
// server clamping agree (the store's clamp is the authoritative
// floor; this constant is just for docs/tests).
const MaxRecentRejectionListLimit = 500

// Closed-enum allowlists for the Kind / Reason / Arch query
// parameters. Bad values surface as 400 immediately — same
// posture as the enrollment list handler's `phase` validation,
// so a typo'd filter does not silently degrade to "no filter
// applied".
var (
	recentRejectionKinds = map[string]struct{}{
		"archspoof_unknown_arch":         {},
		"archspoof_gpu_name_mismatch":    {},
		"archspoof_cc_subject_mismatch":  {},
		"hashrate_out_of_band":           {},
	}
	recentRejectionReasons = map[string]struct{}{
		"unknown_arch":        {},
		"gpu_name_mismatch":   {},
		"cc_subject_mismatch": {},
	}
	recentRejectionArches = map[string]struct{}{
		"ada":             {},
		"hopper":          {},
		"blackwell":       {},
		"blackwell_ultra": {},
		"rubin":           {},
		"rubin_ultra":     {},
		"unknown":         {},
	}
)

// RecentRejectionsHandler serves
// GET /api/v1/attest/recent-rejections.
//
// Query parameters:
//
//	cursor : optional uint64. Exclusive lower bound on Seq.
//	limit  : optional. Defaults to recentrejects.DefaultListLimit.
//	         Clamped to [1, MaxRecentRejectionListLimit].
//	kind   : optional. One of the closed-enum kinds (see
//	         recentRejectionKinds).
//	reason : optional. One of the closed-enum reasons.
//	arch   : optional. One of the canonical NVIDIA arches plus
//	         "unknown".
//	since  : optional unix-seconds. Drops records strictly older.
//
// 200 OK with RecentRejectionsListPageView on success.
// 400 on malformed cursor/limit/since or unknown filter value.
// 405 on non-GET.
// 503 when the lister is not wired.
func (h *Handlers) RecentRejectionsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	lister := currentRecentRejectionLister()
	if lister == nil {
		writeMiningUnavailable(w,
			"v2 recent-rejections store not configured on this node")
		return
	}

	q := r.URL.Query()
	opts := RecentRejectionListOptions{}

	if rawCursor := q.Get("cursor"); rawCursor != "" {
		n, err := strconv.ParseUint(rawCursor, 10, 64)
		if err != nil {
			http.Error(w,
				"cursor must be a non-negative integer (the Seq of the last record from the previous page)",
				http.StatusBadRequest)
			return
		}
		opts.Cursor = n
	}

	if rawLimit := q.Get("limit"); rawLimit != "" {
		n, err := strconv.Atoi(rawLimit)
		if err != nil || n < 0 {
			http.Error(w, "limit must be a non-negative integer",
				http.StatusBadRequest)
			return
		}
		if n > MaxRecentRejectionListLimit {
			n = MaxRecentRejectionListLimit
		}
		opts.Limit = n
	}

	rawKind := q.Get("kind")
	if rawKind != "" {
		if _, ok := recentRejectionKinds[rawKind]; !ok {
			http.Error(w,
				"kind must be one of: archspoof_unknown_arch, archspoof_gpu_name_mismatch, archspoof_cc_subject_mismatch, hashrate_out_of_band",
				http.StatusBadRequest)
			return
		}
		opts.Kind = rawKind
	}

	rawReason := q.Get("reason")
	if rawReason != "" {
		if _, ok := recentRejectionReasons[rawReason]; !ok {
			http.Error(w,
				"reason must be one of: unknown_arch, gpu_name_mismatch, cc_subject_mismatch",
				http.StatusBadRequest)
			return
		}
		opts.Reason = rawReason
	}

	rawArch := q.Get("arch")
	if rawArch != "" {
		if _, ok := recentRejectionArches[rawArch]; !ok {
			http.Error(w,
				"arch must be one of: ada, hopper, blackwell, blackwell_ultra, rubin, rubin_ultra, unknown",
				http.StatusBadRequest)
			return
		}
		opts.Arch = rawArch
	}

	if rawSince := q.Get("since"); rawSince != "" {
		n, err := strconv.ParseInt(rawSince, 10, 64)
		if err != nil || n < 0 {
			http.Error(w,
				"since must be a non-negative unix-seconds timestamp",
				http.StatusBadRequest)
			return
		}
		opts.SinceUnixSec = n
	}

	page := lister.List(opts)

	view := RecentRejectionsListPageView{
		Records:      page.Records,
		NextCursor:   page.NextCursor,
		HasMore:      page.HasMore,
		TotalMatches: page.TotalMatches,
		EchoedFilters: RecentRejectionsEchoedFilters{
			Kind:   opts.Kind,
			Reason: opts.Reason,
			Arch:   opts.Arch,
			Since:  opts.SinceUnixSec,
		},
	}
	if view.Records == nil {
		view.Records = []RecentRejectionView{}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(view)
}
