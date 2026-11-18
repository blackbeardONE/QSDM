package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// PULSE routes:
//
//	POST /api/v1/pulse/submit-signed   a signed PULSE tx (join, leave, settle, reclaim)
//	GET  /api/v1/pulse/state?pool=<id> the rule's parameters and one pool's chain state
//	GET  /api/v1/pulse/member?owner=<address>
//
// Until the PULSE rule is active, submit answers 409 and the state route
// reports active=false.

type pulseStateProvider interface {
	MemberPool(owner string) (string, bool)
	Members(pool string) []string
	EscrowedProofs(pool string) map[string]chain.PulseEscrowView
	Owed(pool string) map[string]uint64
	LastSeq(pool string) uint64
	Stats() chain.PulseStats
}

var pulseGlobal struct {
	mu       sync.RWMutex
	pool     MempoolSubmitter
	provider pulseStateProvider
	tip      func() uint64
}

// SetPulseMempool installs the mempool PULSE txs are submitted to.
func SetPulseMempool(pool MempoolSubmitter) {
	pulseGlobal.mu.Lock()
	defer pulseGlobal.mu.Unlock()
	pulseGlobal.pool = pool
}

// SetPulseStateProvider installs the PULSE chain state and the tip height.
func SetPulseStateProvider(p pulseStateProvider, tip func() uint64) {
	pulseGlobal.mu.Lock()
	defer pulseGlobal.mu.Unlock()
	pulseGlobal.provider = p
	pulseGlobal.tip = tip
}

func pulseCurrent() (MempoolSubmitter, pulseStateProvider, func() uint64) {
	pulseGlobal.mu.RLock()
	defer pulseGlobal.mu.RUnlock()
	return pulseGlobal.pool, pulseGlobal.provider, pulseGlobal.tip
}

// QSDMPulseEnvelope is a signed PULSE tx as sent over HTTP.
type QSDMPulseEnvelope struct {
	Sender    string          `json:"sender"`
	Nonce     uint64          `json:"nonce"`
	Fee       float64         `json:"fee"`
	Payload   json.RawMessage `json:"payload"`
	PublicKey string          `json:"public_key"`
	Signature string          `json:"signature"`
}

// PulseEnvelopeTx turns an envelope into the mempool tx the signature covers.
func PulseEnvelopeTx(env QSDMPulseEnvelope) *mempool.Tx {
	return &mempool.Tx{
		ID:     "pulse-" + strings.ToLower(env.Sender) + "-" + strconv.FormatUint(env.Nonce, 10),
		Sender: env.Sender, Nonce: env.Nonce, Fee: env.Fee, Payload: []byte(env.Payload),
		ContractID: chain.PulseContractID, PublicKey: env.PublicKey, Signature: env.Signature,
		AddedAt: time.Now().UTC(),
	}
}

// QSDMPulseSubmitSignedHandler accepts a signed PULSE tx into the mempool.
func (h *Handlers) QSDMPulseSubmitSignedHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	pool, _, tip := pulseCurrent()
	if pool == nil || tip == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "PULSE submission is not configured")
		return
	}
	if !chain.PulseActiveAt(tip() + 1) {
		writeErrorResponse(w, http.StatusConflict, "the PULSE rule is not active yet")
		return
	}
	var env QSDMPulseEnvelope
	dec := json.NewDecoder(io.LimitReader(r.Body, 256<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid PULSE envelope: "+SanitizeString(err.Error(), 256))
		return
	}
	tx := PulseEnvelopeTx(env)
	if _, err := chain.DecodePulseTx(tx); err != nil {
		writeErrorResponse(w, http.StatusUnprocessableEntity, SanitizeString(err.Error(), 512))
		return
	}
	status := "submitted"
	if err := pool.Add(tx); err != nil {
		if errors.Is(err, mempool.ErrDuplicateTx) {
			status = "duplicate"
		} else {
			writeErrorResponse(w, http.StatusUnprocessableEntity, SanitizeString(err.Error(), 512))
			return
		}
	}
	writeJSONResponse(w, http.StatusOK, map[string]string{"tx_id": tx.ID, "status": "accepted", "mempool_status": status})
}

// QSDMPulseStateHandler reports the rule and one pool's chain state.
func (h *Handlers) QSDMPulseStateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	_, prov, tip := pulseCurrent()
	params := chain.PulseParamsSnapshot()
	out := map[string]any{
		"activation_height": params.ActivationHeight, "max_fee_bps": params.MaxFeeBPS,
		"payee_epoch_cap_dust": params.PayeeEpochCapDust, "reclaim_after_blocks": params.ReclaimAfterBlocks,
	}
	pools := make([]map[string]any, 0, len(params.Pools))
	for _, p := range params.Pools {
		pools = append(pools, map[string]any{"id": string(p.ID), "max_fee_bps": p.MaxFeeBPS})
	}
	out["pools"] = pools
	if tip != nil {
		out["active"] = chain.PulseActiveAt(tip() + 1)
		out["tip"] = tip()
	}
	if id := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("pool"))); id != "" && prov != nil {
		if err := ValidateAddress(id); err != nil {
			writeErrorResponse(w, http.StatusBadRequest, "invalid pool id")
			return
		}
		esc := prov.EscrowedProofs(id)
		ids := make([]string, 0, len(esc))
		var total uint64
		for k, v := range esc {
			ids = append(ids, k)
			total += v.Dust
		}
		sort.Strings(ids)
		proofs := make([]map[string]any, 0, len(ids))
		for _, k := range ids {
			v := esc[k]
			proofs = append(proofs, map[string]any{"proof_id": k, "owner": v.Owner, "dust": v.Dust, "height": v.Height})
		}
		po := map[string]any{"id": id, "last_seq": prov.LastSeq(id), "escrowed": proofs,
			"escrowed_dust": total, "owed": prov.Owed(id),
			// The pool's members: a settlement may name only these as weight owners (2026-11-18).
			"members": prov.Members(id)}
		// The pool signs its settle txs from its own account: its next nonce.
		if probe := currentMiningAccountProbe(); probe != nil {
			_, nonce, present := probe.BalanceOf(id)
			po["account_nonce"] = nonce
			po["account_present"] = present
		}
		out["pool"] = po
	}
	if prov != nil {
		out["stats"] = prov.Stats()
	}
	writeJSONResponse(w, http.StatusOK, out)
}

// QSDMPulseMemberHandler reports the pool an owner has joined.
func (h *Handlers) QSDMPulseMemberHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	_, prov, _ := pulseCurrent()
	if prov == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "PULSE state is not configured")
		return
	}
	owner := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("owner")))
	if err := ValidateAddress(owner); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid owner address")
		return
	}
	pool, ok := prov.MemberPool(owner)
	writeJSONResponse(w, http.StatusOK, map[string]any{"owner": owner, "member": ok, "pool": pool})
}
