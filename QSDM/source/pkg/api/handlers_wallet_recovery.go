package api

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

type recoveryCapsuleSubmitterHolder struct {
	mu   sync.RWMutex
	pool MempoolSubmitter
}

var recoveryCapsuleMempoolGlobal = &recoveryCapsuleSubmitterHolder{}

func SetRecoveryCapsuleMempool(pool MempoolSubmitter) {
	recoveryCapsuleMempoolGlobal.mu.Lock()
	defer recoveryCapsuleMempoolGlobal.mu.Unlock()
	recoveryCapsuleMempoolGlobal.pool = pool
}

func currentRecoveryCapsuleMempool() MempoolSubmitter {
	recoveryCapsuleMempoolGlobal.mu.RLock()
	defer recoveryCapsuleMempoolGlobal.mu.RUnlock()
	return recoveryCapsuleMempoolGlobal.pool
}

type recoveryCapsuleStateProvider interface {
	GetByLocator(locator string) (chain.RecoveryCapsuleState, bool)
	GetByOwner(owner string) (chain.RecoveryCapsuleState, bool)
	StateRoot() string
}

type recoveryCapsuleProviderHolder struct {
	mu       sync.RWMutex
	provider recoveryCapsuleStateProvider
}

var recoveryCapsuleProviderGlobal = &recoveryCapsuleProviderHolder{}

func SetRecoveryCapsuleStateProvider(provider recoveryCapsuleStateProvider) {
	recoveryCapsuleProviderGlobal.mu.Lock()
	defer recoveryCapsuleProviderGlobal.mu.Unlock()
	recoveryCapsuleProviderGlobal.provider = provider
}

func currentRecoveryCapsuleStateProvider() recoveryCapsuleStateProvider {
	recoveryCapsuleProviderGlobal.mu.RLock()
	defer recoveryCapsuleProviderGlobal.mu.RUnlock()
	return recoveryCapsuleProviderGlobal.provider
}

type QSDMRecoveryCapsuleEnvelope struct {
	Action    chain.RecoveryCapsuleAction `json:"action"`
	Signature string                      `json:"signature"`
	PublicKey string                      `json:"public_key"`
}

type QSDMRecoveryCapsuleSubmitResponse struct {
	ActionID      string `json:"action_id"`
	Sender        string `json:"sender"`
	Locator       string `json:"locator"`
	Status        string `json:"status"`
	MempoolStatus string `json:"mempool_status"`
}

type QSDMRecoveryCapsuleNonceResponse struct {
	Runtime     string `json:"runtime"`
	Source      string `json:"source"`
	Sender      string `json:"sender"`
	ActionNonce uint64 `json:"action_nonce"`
	Present     bool   `json:"present"`
}

type QSDMRecoveryCapsuleResponse struct {
	Runtime   string                     `json:"runtime"`
	Source    string                     `json:"source"`
	StateRoot string                     `json:"state_root"`
	State     chain.RecoveryCapsuleState `json:"recovery"`
}

func recoveryCapsuleEnvelopeTx(env QSDMRecoveryCapsuleEnvelope) (*mempool.Tx, error) {
	payload, err := json.Marshal(env.Action)
	if err != nil {
		return nil, err
	}
	return &mempool.Tx{
		ID:         env.Action.ID,
		Sender:     env.Action.Sender,
		Nonce:      env.Action.Nonce,
		Payload:    payload,
		ContractID: chain.RecoveryCapsuleContractID,
		Signature:  env.Signature,
		PublicKey:  env.PublicKey,
		AddedAt:    time.Now().UTC(),
	}, nil
}

func (h *Handlers) QSDMRecoveryCapsuleNonceHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	sender := strings.TrimSpace(r.URL.Query().Get("sender"))
	if err := ValidateAddress(sender); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid sender address")
		return
	}
	probe := currentMiningAccountProbe()
	if probe == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "consensus account state is not configured")
		return
	}
	_, nonce, present := probe.BalanceOf(sender)
	writeJSONResponse(w, http.StatusOK, QSDMRecoveryCapsuleNonceResponse{
		Runtime: "qsdm-native", Source: "chain", Sender: sender,
		ActionNonce: nonce, Present: present,
	})
}

func (h *Handlers) QSDMRecoveryCapsuleSubmitSignedHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	pool := currentRecoveryCapsuleMempool()
	if pool == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "wallet recovery capsule submission is not configured")
		return
	}
	var env QSDMRecoveryCapsuleEnvelope
	decoder := json.NewDecoder(io.LimitReader(r.Body, chain.RecoveryCapsuleMaxPayloadBytes*2))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&env); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid wallet recovery capsule envelope: "+SanitizeString(err.Error(), 256))
		return
	}
	tx, err := recoveryCapsuleEnvelopeTx(env)
	if err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "could not encode wallet recovery capsule action")
		return
	}
	if err := chain.VerifyRecoveryCapsuleTx(tx); err != nil {
		writeErrorResponse(w, http.StatusUnprocessableEntity, SanitizeString(err.Error(), 512))
		return
	}
	probe := currentMiningAccountProbe()
	if probe == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "consensus account state is not configured")
		return
	}
	_, expectedNonce, present := probe.BalanceOf(tx.Sender)
	if !present {
		writeErrorResponse(w, http.StatusUnprocessableEntity, "wallet must already exist in consensus account state before enabling legacy recovery")
		return
	}
	if tx.Nonce != expectedNonce {
		writeErrorResponse(w, http.StatusUnprocessableEntity, SanitizeString(
			"stale wallet recovery action nonce: got "+strconv.FormatUint(tx.Nonce, 10)+
				", current consensus nonce is "+strconv.FormatUint(expectedNonce, 10), 512))
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
	writeJSONResponse(w, http.StatusOK, QSDMRecoveryCapsuleSubmitResponse{
		ActionID: env.Action.ID, Sender: env.Action.Sender, Locator: env.Action.Locator,
		Status: "accepted", MempoolStatus: status,
	})
}

func (h *Handlers) QSDMRecoveryCapsuleLookupHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	provider := currentRecoveryCapsuleStateProvider()
	if provider == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "wallet recovery capsule state is not configured")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	owner := strings.TrimSpace(r.URL.Query().Get("owner"))
	if owner == "" {
		writeErrorResponse(w, http.StatusBadRequest, "owner query parameter is required")
		return
	}
	if err := ValidateAddress(owner); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid owner address")
		return
	}
	state, ok := provider.GetByOwner(owner)
	if !ok {
		writeErrorResponse(w, http.StatusNotFound, "wallet recovery capsule not found")
		return
	}
	writeJSONResponse(w, http.StatusOK, QSDMRecoveryCapsuleResponse{
		Runtime: "qsdm-native", Source: "chain", StateRoot: provider.StateRoot(), State: state,
	})
}

func (h *Handlers) QSDMRecoveryCapsuleRouteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	provider := currentRecoveryCapsuleStateProvider()
	if provider == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "wallet recovery capsule state is not configured")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	locator := strings.TrimSpace(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/wallet/recovery/capsules/"), "/"))
	decoded, err := url.PathUnescape(locator)
	locatorBytes, hexErr := hex.DecodeString(decoded)
	if err != nil || hexErr != nil || len(locatorBytes) != 32 || decoded != strings.ToLower(decoded) {
		writeErrorResponse(w, http.StatusBadRequest, "invalid wallet recovery capsule locator")
		return
	}
	state, ok := provider.GetByLocator(decoded)
	if !ok {
		writeErrorResponse(w, http.StatusNotFound, "wallet recovery capsule not found")
		return
	}
	writeJSONResponse(w, http.StatusOK, QSDMRecoveryCapsuleResponse{
		Runtime: "qsdm-native", Source: "chain", StateRoot: provider.StateRoot(), State: state,
	})
}
