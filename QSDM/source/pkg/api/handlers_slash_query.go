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
