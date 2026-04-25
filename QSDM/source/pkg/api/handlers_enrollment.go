package api

// Mining-enrollment HTTP endpoints (v2 protocol §7). Two
// symmetric POSTs let an enrolled-or-prospective operator
// submit `qsdm/enroll/v1` transactions through the public API
// without having to talk peer-to-peer:
//
//	POST /api/v1/mining/enroll      → enroll payload
//	POST /api/v1/mining/unenroll    → unenroll payload
//
// Both endpoints accept the SAME wire shape: a JSON envelope
// that carries a fully-formed mempool.Tx body plus an optional
// `payload_b64` field for the (already-encoded) enrollment
// payload bytes. The handler:
//
//   1. Reconstructs the mempool.Tx exactly (no payload mutation,
//      no field defaulting beyond AddedAt) so the tx hash the
//      sender computed off-line stays valid.
//   2. Verifies the ContractID matches enrollment.ContractID
//      (defence in depth; the admission gate also checks).
//   3. Verifies the payload kind matches the route — an enroll
//      payload posted to /unenroll is a client error, not a
//      consensus rejection, so we surface 400 before the pool
//      sees it.
//   4. Calls the configured Mempool.Add. The mempool admission
//      gate (enrollment.AdmissionChecker, wired by the operator
//      via mempool.SetAdmissionChecker) does the heavy lifting:
//      stateless field validation runs once, here, with proper
//      attribution.
//
// Stateful checks (balance, node_id uniqueness) intentionally
// stay in EnrollmentApplier.ApplyEnrollmentTx at block time —
// this endpoint is ADMISSION ONLY. A 200 here means "the tx
// was accepted into the pool", NOT "the enrollment will
// succeed at block time".

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

// MempoolSubmitter is the narrow interface this handler depends
// on. Real wiring uses *mempool.Mempool; tests inject fakes.
type MempoolSubmitter interface {
	Add(tx *mempool.Tx) error
}

type enrollmentSubmitterHolder struct {
	mu   sync.RWMutex
	pool MempoolSubmitter
}

var enrollmentHolder = &enrollmentSubmitterHolder{}

// SetEnrollmentMempool installs (or removes, when pool==nil)
// the process-wide MempoolSubmitter used by the enroll/unenroll
// HTTP handlers. Validators call this once at startup after
// constructing the live mempool.
func SetEnrollmentMempool(pool MempoolSubmitter) {
	enrollmentHolder.mu.Lock()
	defer enrollmentHolder.mu.Unlock()
	enrollmentHolder.pool = pool
}

func currentEnrollmentMempool() MempoolSubmitter {
	enrollmentHolder.mu.RLock()
	defer enrollmentHolder.mu.RUnlock()
	return enrollmentHolder.pool
}

// EnrollmentSubmitRequest is the wire shape for both
// POST /api/v1/mining/enroll and POST /api/v1/mining/unenroll.
//
// The client builds the canonical enrollment payload locally
// (e.g. via enrollment.EncodeEnrollPayload) and base64-encodes
// the raw bytes into PayloadB64. The Tx envelope carries the
// signed-tx fields the AccountStore needs (Sender, Nonce, Fee,
// ID). ContractID is REQUIRED and must equal
// enrollment.ContractID; the handler rejects everything else.
//
// Why base64 (not hex, not nested JSON): the canonical payload
// IS already canonical JSON. Wrapping it again as JSON would
// re-encode it through Go's encoder and lose the byte-for-byte
// canonical form that the signature was computed over. base64
// preserves the bytes exactly while still being valid JSON.
type EnrollmentSubmitRequest struct {
	ID         string  `json:"id"`
	Sender     string  `json:"sender"`
	Nonce      uint64  `json:"nonce"`
	Fee        float64 `json:"fee"`
	GasLimit   int64   `json:"gas_limit,omitempty"`
	ContractID string  `json:"contract_id"`
	PayloadB64 string  `json:"payload_b64"`
}

// EnrollmentSubmitResponse is the success body — minimal on
// purpose. A 200 means the tx is in the pool; clients poll the
// chain (or a TxReceipt endpoint) to discover when/if it lands.
type EnrollmentSubmitResponse struct {
	TxID   string `json:"tx_id"`
	Status string `json:"status"`
}

// EnrollmentSubmitHandler serves POST /api/v1/mining/enroll.
// The expected payload kind is enrollment.PayloadKindEnroll.
func (h *Handlers) EnrollmentSubmitHandler(w http.ResponseWriter, r *http.Request) {
	h.serveEnrollmentSubmit(w, r, enrollment.PayloadKindEnroll)
}

// UnenrollmentSubmitHandler serves POST /api/v1/mining/unenroll.
// The expected payload kind is enrollment.PayloadKindUnenroll.
func (h *Handlers) UnenrollmentSubmitHandler(w http.ResponseWriter, r *http.Request) {
	h.serveEnrollmentSubmit(w, r, enrollment.PayloadKindUnenroll)
}

func (h *Handlers) serveEnrollmentSubmit(w http.ResponseWriter, r *http.Request, expectedKind enrollment.PayloadKind) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pool := currentEnrollmentMempool()
	if pool == nil {
		writeMiningUnavailable(w, "enrollment mempool not configured on this node")
		return
	}

	defer r.Body.Close()
	var req EnrollmentSubmitRequest
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
	if req.ContractID != enrollment.ContractID {
		http.Error(w, fmt.Sprintf("contract_id must be %q, got %q", enrollment.ContractID, req.ContractID), http.StatusBadRequest)
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

	// Defence in depth: the admission gate also peeks the kind,
	// but doing it here lets us route enroll vs unenroll cleanly
	// and return a much better error when a client posts the
	// wrong payload to the wrong endpoint.
	gotKind, err := enrollment.PeekKind(payload)
	if err != nil {
		http.Error(w, "payload not a valid enrollment kind envelope: "+err.Error(), http.StatusBadRequest)
		return
	}
	if gotKind != expectedKind {
		http.Error(w, fmt.Sprintf("payload kind %q does not match endpoint (expected %q)", gotKind, expectedKind), http.StatusBadRequest)
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
		// Request because they describe miner-attributable
		// flaws. Internal errors (none expected today) would
		// fall through to 500.
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
	_ = json.NewEncoder(w).Encode(EnrollmentSubmitResponse{
		TxID:   tx.ID,
		Status: "accepted",
	})
}
