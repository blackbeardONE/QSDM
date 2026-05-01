package api

// Mining-slash receipt READ endpoint (v2 protocol §8 — read
// counterpart to handlers_slashing.go).
//
//	GET /api/v1/mining/slash/{tx_id}
//
// Lets a slash submitter look up the outcome of a slash they
// previously POSTed:
//
//   - "applied": chain accepted the evidence, drained the
//     stake, paid the reward; receipt carries the exact
//     amounts and the post-slash auto-revoke flag.
//   - "rejected": chain rejected the slash at the applier
//     stage (verifier failed, evidence already seen, fee
//     invalid, ...); receipt carries the reason tag and a
//     human-readable error string.
//
// The endpoint is THE answer to "did my slash work?" without
// having to subscribe to the chain event stream from boot or
// scrape Prometheus counters and back-correlate by height.
//
// Why a sanitised wire shape (SlashReceiptView) and not just
// json.Marshal(chain.SlashReceipt):
//
//   - chain.SlashReceipt is a chain-internal struct. The wire
//     shape MUST be stable across binary upgrades; a wire view
//     under our own control is the right place to enforce
//     that.
//   - JSON tag names below are the API contract. Re-ordering
//     fields here is fine; renaming any of them is a breaking
//     change.
//
// 503 vs 404: matches the same convention as
// handlers_enrollment_query.go. 503 means "this node has no
// receipt store wired" (v1-only deployment). 404 means "the
// store exists; we have no record of that tx_id" (either the
// id is wrong or the receipt was evicted under FIFO pressure
// — the chain.SlashReceiptStore is bounded for OOM safety).

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// SlashReceiptView is the wire shape for
// GET /api/v1/mining/slash/{tx_id}.
//
// JSON tag set is the public API. New fields are additive at
// the end with omitempty where a zero value is unambiguous.
type SlashReceiptView struct {
	TxID                    string    `json:"tx_id"`
	Outcome                 string    `json:"outcome"`
	RecordedAt              time.Time `json:"recorded_at"`
	Height                  uint64    `json:"height"`
	Slasher                 string    `json:"slasher,omitempty"`
	NodeID                  string    `json:"node_id,omitempty"`
	EvidenceKind            string    `json:"evidence_kind,omitempty"`
	SlashedDust             uint64    `json:"slashed_dust,omitempty"`
	RewardedDust            uint64    `json:"rewarded_dust,omitempty"`
	BurnedDust              uint64    `json:"burned_dust,omitempty"`
	AutoRevoked             bool      `json:"auto_revoked,omitempty"`
	AutoRevokeRemainingDust uint64    `json:"auto_revoke_remaining_dust,omitempty"`
	RejectReason            string    `json:"reject_reason,omitempty"`
	Err                     string    `json:"error,omitempty"`
}

// SlashReceiptStore is the narrow read-only interface this
// handler depends on. Concrete implementations live in
// pkg/chain (in-memory bounded store), but pkg/api MUST stay
// independent of chain types — same dependency-inversion
// reasoning as the EnrollmentRegistry interface in
// handlers_enrollment_query.go. The wire-shape conversion
// happens inside the adapter installed by internal/v2wiring,
// so the handler is purely an HTTP shell.
//
// Lookup must return (zero, false) for "not found".
// Returning ok=true with a non-empty TxID signals "found".
type SlashReceiptStore interface {
	Lookup(txID string) (SlashReceiptView, bool)
}

type slashReceiptStoreHolder struct {
	mu    sync.RWMutex
	store SlashReceiptStore
}

var slashReceiptHolder = &slashReceiptStoreHolder{}

// SetSlashReceiptStore installs (or removes, when
// store==nil) the process-wide receipt store the GET handler
// uses. internal/v2wiring calls this at boot with a chain
// adapter; tests can call it with a fake.
func SetSlashReceiptStore(store SlashReceiptStore) {
	slashReceiptHolder.mu.Lock()
	defer slashReceiptHolder.mu.Unlock()
	slashReceiptHolder.store = store
}

func currentSlashReceiptStore() SlashReceiptStore {
	slashReceiptHolder.mu.RLock()
	defer slashReceiptHolder.mu.RUnlock()
	return slashReceiptHolder.store
}

// -----------------------------------------------------------------------------
// SlashReceiptLister — paginated read for the dashboard tile (2026-05-01)
// -----------------------------------------------------------------------------
//
// Why a SEPARATE interface rather than extending SlashReceiptStore:
//
//   - SlashReceiptStore's Lookup contract is older + stable, used
//     by the v1 GET /api/v1/mining/slash/{tx_id} endpoint. Adding
//     a List method to the same interface would force every
//     existing fake (incl. the test doubles in this package) to
//     implement it, even though the v1 endpoint never calls it.
//   - The split mirrors the recentrejects precedent
//     (handlers_recent_rejections.go's RecentRejectionLister vs.
//     the never-grown-into-a-store equivalent). Future
//     dashboards / CLIs can depend on the narrower interface
//     they actually need.
//   - The two interfaces are typically satisfied by ONE concrete
//     adapter (the chain receipt store), so production wiring
//     pays no extra surface area.

// SlashReceiptListOptions echoes the filter knobs supported by
// chain.SlashReceiptStore.List but in api-package types so
// dependent handlers (dashboard tile, future v1 list endpoint)
// don't have to import chain. The dashboard handler validates
// Outcome/EvidenceKind against fixed allowlists BEFORE
// forwarding so a typo'd filter returns 400 rather than
// silently passing through as "no filter".
type SlashReceiptListOptions struct {
	Limit        int
	Outcome      string
	EvidenceKind string
	SinceUnixSec int64
}

// SlashReceiptListPage is one page of List() results with
// page-level metadata. Records are NEWEST-FIRST (the natural
// order for an operator tile). TotalMatches counts the
// matched records visible to this page (i.e. page count + 1
// when HasMore is true; not a global count of every match in
// the whole store, which would require an unbounded scan).
type SlashReceiptListPage struct {
	Records      []SlashReceiptView
	TotalMatches uint64
	HasMore      bool
}

// SlashReceiptLister is the narrow read-only interface the
// dashboard tile depends on. Concrete implementation is the
// thin adapter installed by internal/v2wiring; pkg/api stays
// free of pkg/chain imports.
type SlashReceiptLister interface {
	List(opts SlashReceiptListOptions) SlashReceiptListPage
}

type slashReceiptListerHolder struct {
	mu     sync.RWMutex
	lister SlashReceiptLister
}

var slashReceiptListerHldr = &slashReceiptListerHolder{}

// SetSlashReceiptLister installs (or removes, when
// lister==nil) the process-wide lister. internal/v2wiring
// calls this at boot with a thin adapter over the bounded
// chain.SlashReceiptStore; tests can call it with a fake.
//
// Distinct from SetSlashReceiptStore (Lookup-only): operators
// running a v1-only deployment can wire neither, and both
// the dashboard tile and the GET /api/v1/mining/slash/{tx_id}
// endpoint will return 503 with a descriptive message rather
// than fabricating empty / not-found responses.
func SetSlashReceiptLister(lister SlashReceiptLister) {
	slashReceiptListerHldr.mu.Lock()
	defer slashReceiptListerHldr.mu.Unlock()
	slashReceiptListerHldr.lister = lister
}

func currentSlashReceiptLister() SlashReceiptLister {
	slashReceiptListerHldr.mu.RLock()
	defer slashReceiptListerHldr.mu.RUnlock()
	return slashReceiptListerHldr.lister
}

// CurrentSlashReceiptLister returns the process-wide lister,
// or nil if SetSlashReceiptLister has not been called
// (i.e. v1-only deployment, or the v2 chain store has not
// been wired by internal/v2wiring at boot). Exported because
// the dashboard package needs to detect "feature unavailable"
// before rendering its tile.
//
// NOTE: package-level access pattern matches
// CurrentRecentRejectionLister exactly, including the nil-
// return-on-missing semantics. Callers should print
// "feature not configured" to the operator rather than
// fabricating empty list output.
func CurrentSlashReceiptLister() SlashReceiptLister {
	return currentSlashReceiptLister()
}

// IsKnownSlashOutcome reports whether s is one of the closed
// outcome values the dashboard / list filter allow. Used by
// the dashboard handler to validate query parameters. Mirrors
// IsKnownRecentRejectionKind's role for the rejection ring.
func IsKnownSlashOutcome(s string) bool {
	switch s {
	case "applied", "rejected":
		return true
	default:
		return false
	}
}

// KnownSlashOutcomes returns the closed-set outcome strings in
// stable order. Used by the dashboard tile to populate the
// outcome-filter dropdown without duplicating the allowlist.
func KnownSlashOutcomes() []string {
	return []string{"applied", "rejected"}
}

// IsKnownSlashEvidenceKind reports whether s is one of the
// closed evidence-kind strings the dashboard accepts. Mirrors
// the slashing.EvidenceKind constants but kept here so
// pkg/api does not import pkg/mining/slashing for a plain
// string-set check (the chain store already converts
// EvidenceKind to string at insertion).
func IsKnownSlashEvidenceKind(s string) bool {
	switch s {
	case "forged-attestation", "double-mining", "freshness-cheat":
		return true
	default:
		return false
	}
}

// KnownSlashEvidenceKinds returns the closed-set kind strings
// in stable order. Used by the dashboard tile's evidence-kind
// dropdown.
func KnownSlashEvidenceKinds() []string {
	return []string{"forged-attestation", "double-mining", "freshness-cheat"}
}

// SlashReceiptHandler serves
// GET /api/v1/mining/slash/{tx_id}.
//
// 200 OK: tx found, body is a SlashReceiptView.
// 404: store reachable but no receipt for this tx_id.
// 405: non-GET method.
// 400: empty or malformed tx_id (path component required).
// 503: node has no receipt store wired (v1-only deployment).
//
// The route is mounted on the trailing-slash prefix so any
// path-escaped tx id round-trips (matches the EnrollmentQuery
// idiom).
func (h *Handlers) SlashReceiptHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	store := currentSlashReceiptStore()
	if store == nil {
		writeMiningUnavailable(w, "v2 slash receipt store not configured on this node")
		return
	}

	const prefix = "/api/v1/mining/slash/"
	rawID := strings.TrimPrefix(r.URL.Path, prefix)
	rawID = strings.TrimSuffix(rawID, "/")
	if rawID == "" || strings.Contains(rawID, "/") {
		http.Error(w, "tx_id required as path component", http.StatusBadRequest)
		return
	}
	// Sanity bound on tx id length. The mempool currently
	// accepts arbitrary-length ids; capping the API at 256
	// bytes prevents a path-of-doom attack on the lookup
	// table without constraining any honest client.
	if len(rawID) > 256 {
		http.Error(w, "tx_id too long", http.StatusBadRequest)
		return
	}

	view, ok := store.Lookup(rawID)
	if !ok {
		http.Error(w, "no slash receipt for tx_id (unknown or evicted)",
			http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(view)
}
