package api

// Mining-slashing HTTP endpoint (v2 protocol §8). One POST
// route lets any peer submit a `qsdm/slash/v1` transaction
// through the public API without having to talk peer-to-peer:
//
//	POST /api/v1/mining/slash      → slash payload
//
// Symmetric to the enrollment endpoints
// (handlers_enrollment.go). The handler:
//
//   1. Reconstructs the mempool.Tx exactly (no payload mutation)
//      so the tx hash the sender computed off-line stays valid.
//   2. Verifies the ContractID matches slashing.ContractID
//      (defence in depth; the admission gate also checks).
//   3. Performs a stateless decode + ValidateSlashFields run
//      so the client gets a 400 for malformed payloads before
//      the pool is ever consulted.
//   4. Calls the configured Mempool.Add. The mempool admission
//      gate (slashing.AdmissionChecker, wired by the operator
//      via mempool.SetAdmissionChecker) runs the same stateless
//      validators a second time — this is intentional defence
//      in depth, not redundancy: P2P-arriving slash txs only
//      hit the admission gate, and we want a single canonical
//      validation surface either way.
//
// Stateful checks (registry lookup, evidence verifier dispatch,
// stake debit) intentionally stay in
// chain.SlashApplier.ApplySlashTx at block time — this endpoint
// is ADMISSION ONLY. A 202 here means "the tx was accepted into
// the pool", NOT "the slash will succeed at block time".
//
// Why a separate submitter holder (rather than reusing
// enrollmentHolder): clear-eyed separation of concerns. An
// operator might wire enrollment but not slashing (e.g. a
// validator running v1+v2 enrollment but deferring on-chain
// slashing). Two holders means each path can independently
// be active or 503.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
)

type slashSubmitterHolder struct {
	mu   sync.RWMutex
	pool MempoolSubmitter
}

var slashHolder = &slashSubmitterHolder{}

// SetSlashMempool installs (or removes, when pool==nil) the
// process-wide MempoolSubmitter used by the /api/v1/mining/slash
// HTTP handler. Validators call this once at startup after
// constructing the live mempool. Calling again replaces the
// prior pool — process restarts get clean state.
func SetSlashMempool(pool MempoolSubmitter) {
	slashHolder.mu.Lock()
	defer slashHolder.mu.Unlock()
	slashHolder.pool = pool
}

func currentSlashMempool() MempoolSubmitter {
	slashHolder.mu.RLock()
	defer slashHolder.mu.RUnlock()
	return slashHolder.pool
}

// SlashSubmitRequest is the wire shape for
// POST /api/v1/mining/slash.
//
// The client builds the canonical slash payload locally
// (e.g. via slashing.EncodeSlashPayload) and base64-encodes
// the raw bytes into PayloadB64. The Tx envelope carries the
// signed-tx fields the AccountStore needs (Sender, Nonce, Fee,
// ID). ContractID is REQUIRED and must equal
// slashing.ContractID; the handler rejects everything else.
//
// Why base64 (not hex, not nested JSON): the canonical payload
// IS already canonical JSON. Wrapping it again as JSON would
// re-encode it through Go's encoder and lose the byte-for-byte
// canonical form that the signature was computed over. base64
// preserves the bytes exactly while still being valid JSON.
type SlashSubmitRequest struct {
	ID         string  `json:"id"`
	Sender     string  `json:"sender"`
	Nonce      uint64  `json:"nonce"`
	Fee        float64 `json:"fee"`
	GasLimit   int64   `json:"gas_limit,omitempty"`
	ContractID string  `json:"contract_id"`
	PayloadB64 string  `json:"payload_b64"`
}

// SlashSubmitResponse is the success body — minimal on
// purpose. A 202 means the tx is in the pool; clients poll
// the chain (or a TxReceipt endpoint) to discover when/if
// it lands.
type SlashSubmitResponse struct {
	TxID   string `json:"tx_id"`
	Status string `json:"status"`
}

// SlashSubmitHandler serves POST /api/v1/mining/slash.
func (h *Handlers) SlashSubmitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pool := currentSlashMempool()
	if pool == nil {
		writeMiningUnavailable(w, "slashing mempool not configured on this node")
		return
	}

	defer r.Body.Close()
	var req SlashSubmitRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	if req.Sender == "" {
		http.Error(w, "sender required", http.StatusBadRequest)
		return
	}
	if req.ContractID != slashing.ContractID {
		http.Error(w,
			fmt.Sprintf("contract_id must be %q, got %q",
				slashing.ContractID, req.ContractID),
			http.StatusBadRequest)
		return
	}
	if req.PayloadB64 == "" {
		http.Error(w, "payload_b64 required", http.StatusBadRequest)
		return
	}
	payload, err := base64.StdEncoding.DecodeString(req.PayloadB64)
	if err != nil {
		http.Error(w, "payload_b64 not valid base64: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Defence in depth: decode + stateless validate here so
	// the client gets a clear 400 for malformed payloads
	// before the pool is consulted. The admission gate runs
	// the same checks again — that's not redundant, it's the
	// single canonical surface for P2P-arriving txs (which
	// don't hit this handler at all).
	parsed, err := slashing.DecodeSlashPayload(payload)
	if err != nil {
		http.Error(w, "payload not a valid slash envelope: "+err.Error(),
			http.StatusBadRequest)
		return
	}
	if err := slashing.ValidateSlashFields(parsed, req.Sender); err != nil {
		http.Error(w, "slash payload invalid: "+err.Error(), http.StatusBadRequest)
		return
	}

	tx := &mempool.Tx{
		ID:         req.ID,
		Sender:     req.Sender,
		Nonce:      req.Nonce,
		Fee:        req.Fee,
		GasLimit:   req.GasLimit,
		ContractID: req.ContractID,
		Payload:    payload,
		AddedAt:    time.Now(),
	}

	if err := pool.Add(tx); err != nil {
		// Map known mempool errors to HTTP-meaningful codes.
		// ErrDuplicateTx is 409 Conflict; ErrMempoolFull is
		// 503 Service Unavailable; admission-gate validation
		// errors (anything else from the gate) are 400 Bad
		// Request because they describe submitter-attributable
		// flaws.
		switch {
		case errors.Is(err, mempool.ErrDuplicateTx):
			http.Error(w, "tx already in pool", http.StatusConflict)
			return
		case errors.Is(err, mempool.ErrMempoolFull):
			http.Error(w, "mempool full; retry later", http.StatusServiceUnavailable)
			return
		default:
			http.Error(w, "rejected: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(SlashSubmitResponse{
		TxID:   tx.ID,
		Status: "accepted",
	})
}
