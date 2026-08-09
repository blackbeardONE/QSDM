package api

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// Signed submission for qsdm/staking/v1 validator bonding.
//
// Why this endpoint has to exist at all: there is no generic contract-tx
// submission path. POST /api/v1/transactions builds plain transfers, and
// wallet.TransactionData — the envelope /wallet/submit-signed accepts —
// carries no ContractID or Payload field, so a contract transaction cannot
// travel over it. Every contract needing submission has a bespoke signed
// endpoint; this is staking's, and it mirrors
// /api/v1/tasks/actions/submit-signed deliberately so the two share
// security properties rather than each inventing their own.
//
// Without it the whole chain-derived-membership line of work is
// unreachable: bonding is what populates the staking ledger, membership is
// derived from that ledger, and a home node can only join by bonding.

// StakingEnvelope is a self-custody signed staking request.
//
// Sender is the delegator and is bound to PublicKey by the same derivation
// the transaction path uses (sender == hex(sha256(public_key))), so an
// envelope can only ever bond the signer's own funds.
type StakingEnvelope struct {
	ID           string  `json:"id"`
	Sender       string  `json:"sender"`
	Action       string  `json:"action"`
	Validator    string  `json:"validator"`
	Amount       float64 `json:"amount"`
	UnbondBlocks uint64  `json:"unbond_blocks,omitempty"`
	Nonce        uint64  `json:"nonce,omitempty"`
	Timestamp    string  `json:"timestamp"`
	Signature    string  `json:"signature"`
	PublicKey    string  `json:"public_key,omitempty"`
}

type stakingMempoolHolder struct {
	mu   sync.RWMutex
	pool MempoolSubmitter
}

var stakingMempool = &stakingMempoolHolder{}

// SetStakingMempool installs the mempool staking submissions are admitted
// to. Until it is set the endpoint reports 503 rather than accepting a
// submission it cannot deliver.
func SetStakingMempool(pool MempoolSubmitter) {
	stakingMempool.mu.Lock()
	defer stakingMempool.mu.Unlock()
	stakingMempool.pool = pool
}

func currentStakingMempool() MempoolSubmitter {
	stakingMempool.mu.RLock()
	defer stakingMempool.mu.RUnlock()
	return stakingMempool.pool
}

// StakingSubmissionReady reports whether staking submissions can be
// accepted.
func StakingSubmissionReady() bool { return currentStakingMempool() != nil }

// validateStakingEnvelope checks structure before any crypto work, so a
// malformed request costs a parse rather than a signature verification.
func validateStakingEnvelope(env StakingEnvelope) error {
	if strings.TrimSpace(env.ID) == "" {
		return errors.New("envelope.id is required")
	}
	if strings.TrimSpace(env.Sender) == "" {
		return errors.New("envelope.sender is required")
	}
	if strings.TrimSpace(env.Validator) == "" {
		return errors.New("envelope.validator is required")
	}
	if env.Amount <= 0 {
		return errors.New("envelope.amount must be positive")
	}
	switch env.Action {
	case chain.StakingActionDelegate, chain.StakingActionUnbond:
	default:
		return fmt.Errorf("envelope.action must be %q or %q",
			chain.StakingActionDelegate, chain.StakingActionUnbond)
	}
	if strings.TrimSpace(env.Signature) == "" {
		return errors.New("envelope.signature is required")
	}
	if strings.TrimSpace(env.PublicKey) == "" {
		return errors.New("envelope.public_key is required")
	}
	return nil
}

// verifyStakingEnvelope binds the sender to the key and checks the
// signature over the canonical unsigned form.
//
// Identical in shape to verifyQSDMTaskActionEnvelope on purpose: the sender
// MUST equal hex(sha256(public_key)), so a valid signature by some other key
// cannot authorise bonding from another account.
func (h *Handlers) verifyStakingEnvelope(env StakingEnvelope) error {
	if h.walletService == nil {
		return errors.New(msgWalletServiceUnavailable)
	}
	pubBytes, err := hex.DecodeString(env.PublicKey)
	if err != nil {
		return errors.New("envelope.public_key is not valid hex")
	}
	derivedAddr := hex.EncodeToString(sha256Sum(pubBytes))
	if derivedAddr != env.Sender {
		return errors.New("envelope.sender does not match hex(sha256(public_key))")
	}

	sigBytes, err := hex.DecodeString(env.Signature)
	if err != nil {
		return errors.New("envelope.signature is not valid hex")
	}

	unsigned := env
	unsigned.Signature = ""
	unsigned.PublicKey = ""
	canonical, err := json.Marshal(unsigned)
	if err != nil {
		return errors.New("failed to canonicalise envelope")
	}
	ok, verr := h.walletService.VerifySignature(canonical, sigBytes, pubBytes)
	if verr != nil || !ok {
		return errors.New("signature does not verify under envelope.public_key")
	}
	return nil
}

// stakingMempoolTx converts a verified envelope into the chain transaction.
//
// Amount is deliberately left at zero on the envelope: ApplyStakingTx
// debits the delegator's account itself, and refuses a transaction carrying
// tx.Amount precisely so funds cannot move twice. The bonded amount travels
// in the payload.
func stakingMempoolTx(env StakingEnvelope) (*mempool.Tx, error) {
	payload, err := chain.EncodeStakingPayload(chain.StakingPayload{
		Action:       env.Action,
		Validator:    env.Validator,
		Amount:       env.Amount,
		UnbondBlocks: env.UnbondBlocks,
	})
	if err != nil {
		return nil, err
	}
	return &mempool.Tx{
		ID:         env.ID,
		Sender:     env.Sender,
		Amount:     0,
		Nonce:      env.Nonce,
		Payload:    payload,
		ContractID: chain.StakingContractID,
	}, nil
}

// StakingSubmitSignedHandler accepts POST /api/v1/staking/submit-signed.
func (h *Handlers) StakingSubmitSignedHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var env StakingEnvelope
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&env); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := validateStakingEnvelope(env); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, SanitizeString(err.Error(), 256))
		return
	}
	if err := h.verifyStakingEnvelope(env); err != nil {
		// Deliberately 401, not 400: the request was well-formed but the
		// caller failed to prove control of the sender account.
		writeErrorResponse(w, http.StatusUnauthorized, SanitizeString(err.Error(), 256))
		return
	}

	pool := currentStakingMempool()
	if pool == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable,
			"staking submission is not configured on this node")
		return
	}

	tx, err := stakingMempoolTx(env)
	if err != nil {
		writeErrorResponse(w, http.StatusBadRequest,
			SanitizeString("payload rejected: "+err.Error(), 256))
		return
	}

	if err := pool.Add(tx); err != nil {
		if errors.Is(err, mempool.ErrDuplicateTx) {
			// Idempotent: re-submitting the same envelope is a no-op, not
			// a second bond.
			writeJSONResponse(w, http.StatusOK, map[string]interface{}{
				"status":      "duplicate",
				"id":          env.ID,
				"contract_id": chain.StakingContractID,
			})
			return
		}
		writeErrorResponse(w, http.StatusBadRequest,
			SanitizeString("admission rejected: "+err.Error(), 256))
		return
	}

	writeJSONResponse(w, http.StatusAccepted, map[string]interface{}{
		"status":      "submitted",
		"id":          env.ID,
		"sender":      env.Sender,
		"action":      env.Action,
		"validator":   env.Validator,
		"amount":      env.Amount,
		"contract_id": chain.StakingContractID,
		"note": "bond takes effect once the transaction is committed; " +
			"validator membership is re-derived on each committed height",
	})
}
