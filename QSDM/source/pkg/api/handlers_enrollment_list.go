package api

// Mining-enrollment LIST endpoint (paginated).
//
//	GET /api/v1/mining/enrollments
//	    [?cursor=<node_id>] [?limit=N] [?phase=active|pending_unbond|revoked]
//
// Companion to the per-record query handler in
// handlers_enrollment_query.go. Lets watchers, indexers, and
// dashboards page through the on-chain enrollment registry
// without having to know every node_id in advance.
//
// Pagination model (cursor, not offset):
//
//   - cursor is the EXCLUSIVE lower bound on node_id, sorted
//     lexicographically.
//   - Empty cursor starts from the lexicographic beginning.
//   - The response carries `next_cursor` and `has_more`.
//     Pass `next_cursor` back unchanged on the next call.
//
// Why cursor and not offset:
//
//   The registry mutates while a client pages. Offset pages
//   would silently skip or duplicate records when one is
//   inserted or revoked between calls. node_id-cursor pages
//   are stable: a newly-inserted node_id either lands inside
//   a future page (if greater than the cursor) or has been
//   visited already, depending on lexicographic ordering.
//
// Why a separate Lister interface and setter (vs growing
// EnrollmentRegistry):
//
//   The query endpoint's Lookup-only interface stays minimal —
//   tests for that handler use a fake that doesn't have to
//   implement anything else. The lister-only interface is
//   the same surgical-dependency pattern, scaled to the new
//   call. The production *enrollment.InMemoryState satisfies
//   both, so internal/v2wiring registers it twice (once per
//   holder) and operators get one source of truth without
//   introducing a fan-out abstraction here.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"

	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

// EnrollmentLister is the narrow read-only interface this
// handler depends on. Concrete implementation is
// *enrollment.InMemoryState. Returning a ListPage by value
// matches the chain-side method signature exactly so the
// adapter layer is zero work — the struct ferries straight
// through.
type EnrollmentLister interface {
	List(opts enrollment.ListOptions) enrollment.ListPage
}

type enrollmentListerHolder struct {
	mu     sync.RWMutex
	lister EnrollmentLister
}

var enrollmentListHolder = &enrollmentListerHolder{}

// SetEnrollmentLister installs (or removes, when lister==nil)
// the process-wide lister the GET-list handler uses.
// internal/v2wiring calls this at boot with the same
// *enrollment.InMemoryState already registered as the
// query-side EnrollmentRegistry.
func SetEnrollmentLister(lister EnrollmentLister) {
	enrollmentListHolder.mu.Lock()
	defer enrollmentListHolder.mu.Unlock()
	enrollmentListHolder.lister = lister
}

func currentEnrollmentLister() EnrollmentLister {
	enrollmentListHolder.mu.RLock()
	defer enrollmentListHolder.mu.RUnlock()
	return enrollmentListHolder.lister
}

// CurrentEnrollmentLister returns the process-wide
// EnrollmentLister, or nil if SetEnrollmentLister has not
// been called yet (i.e. v1-only deployment, or
// internal/v2wiring hasn't run). Exported because the
// internal/dashboard package needs to detect "feature
// unavailable" before rendering its enrollment-overview
// tile — same pattern as CurrentRecentRejectionLister and
// CurrentSlashReceiptLister.
func CurrentEnrollmentLister() EnrollmentLister {
	return currentEnrollmentLister()
}

// EnrollmentListPageView is the wire shape for one page of
// list results. JSON tags are the public contract; field
// reordering is fine, renaming is breaking.
//
// Records reuses EnrollmentRecordView (same shape as the
// per-record query) so clients get one canonical record
// representation across both endpoints — no second
// serialiser to keep in sync.
type EnrollmentListPageView struct {
	Records      []EnrollmentRecordView `json:"records"`
	NextCursor   string                 `json:"next_cursor,omitempty"`
	HasMore      bool                   `json:"has_more"`
	TotalMatches uint64                 `json:"total_matches"`
	Phase        string                 `json:"phase,omitempty"`
}

// EnrollmentListHandler serves GET /api/v1/mining/enrollments.
//
// Query parameters:
//
//	cursor : optional. Exclusive lower bound on node_id.
//	limit  : optional. Defaults to enrollment.DefaultListLimit.
//	         Clamped to [1, enrollment.MaxListLimit].
//	phase  : optional. One of "active", "pending_unbond",
//	         "revoked". Empty omits the filter.
//
// 200 OK with EnrollmentListPageView on success.
// 400 on malformed limit or unknown phase.
// 405 on non-GET.
// 503 when the lister is not wired (v1-only deployment).
func (h *Handlers) EnrollmentListHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	lister := currentEnrollmentLister()
	if lister == nil {
		writeMiningUnavailable(w, "v2 enrollment lister not configured on this node")
		return
	}

	q := r.URL.Query()
	opts := enrollment.ListOptions{
		Cursor: q.Get("cursor"),
	}

	if rawLimit := q.Get("limit"); rawLimit != "" {
		n, err := strconv.Atoi(rawLimit)
		if err != nil || n < 0 {
			http.Error(w, "limit must be a non-negative integer", http.StatusBadRequest)
			return
		}
		opts.Limit = n
	}

	rawPhase := q.Get("phase")
	switch rawPhase {
	case "":
		// PhaseAny — no filter.
	case string(enrollment.PhaseActive),
		string(enrollment.PhasePendingUnbond),
		string(enrollment.PhaseRevoked):
		opts.Phase = enrollment.ListPhase(rawPhase)
	default:
		http.Error(w,
			"phase must be one of: active, pending_unbond, revoked",
			http.StatusBadRequest)
		return
	}

	// Sanity bound on the cursor too — same justification as
	// the tx_id cap on /api/v1/mining/slash/{tx_id}.
	if len(opts.Cursor) > enrollment.MaxNodeIDLen {
		http.Error(w, "cursor too long", http.StatusBadRequest)
		return
	}

	page := lister.List(opts)

	view := EnrollmentListPageView{
		Records:      make([]EnrollmentRecordView, 0, len(page.Records)),
		NextCursor:   page.NextCursor,
		HasMore:      page.HasMore,
		TotalMatches: page.TotalMatches,
		Phase:        rawPhase,
	}
	for i := range page.Records {
		// viewFromRecord wants a pointer; the ListPage carries
		// values, so address the slice element directly. The
		// returned view is a copy, so the slice element is
		// never mutated through the view.
		view.Records = append(view.Records, viewFromRecord(&page.Records[i]))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(view)
}
