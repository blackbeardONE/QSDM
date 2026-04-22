package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/bridge"
)

// --- Bridge API request/response types ---

type LockAssetRequest struct {
	SourceChain    string  `json:"source_chain"`
	TargetChain    string  `json:"target_chain"`
	Asset          string  `json:"asset"`
	Amount         float64 `json:"amount"`
	Recipient      string  `json:"recipient"`
	ExpiryMinutes  int     `json:"expiry_minutes"`
}

type RedeemAssetRequest struct {
	Secret string `json:"secret"`
}

type InitiateSwapRequest struct {
	InitiatorChain    string  `json:"initiator_chain"`
	ParticipantChain  string  `json:"participant_chain"`
	InitiatorAsset    string  `json:"initiator_asset"`
	ParticipantAsset  string  `json:"participant_asset"`
	InitiatorAmount   float64 `json:"initiator_amount"`
	ParticipantAmount float64 `json:"participant_amount"`
	InitiatorAddress  string  `json:"initiator_address"`
	ParticipantAddress string `json:"participant_address"`
	ExpiryMinutes     int     `json:"expiry_minutes"`
}

type CompleteSwapRequest struct {
	Secret string `json:"secret"`
}

// --- Bridge Lock/Redeem/Refund Handlers ---

func (h *Handlers) BridgeLockAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.bridgeProtocol == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "bridge protocol not available")
		return
	}
	if !h.enforceNvidiaLock(w) {
		return
	}

	var req LockAssetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.SourceChain == "" || req.TargetChain == "" || req.Asset == "" || req.Recipient == "" {
		writeErrorResponse(w, http.StatusBadRequest, "source_chain, target_chain, asset, and recipient are required")
		return
	}
	if req.Amount <= 0 {
		writeErrorResponse(w, http.StatusBadRequest, "amount must be positive")
		return
	}
	expiry := time.Duration(req.ExpiryMinutes) * time.Minute
	if expiry <= 0 {
		expiry = 60 * time.Minute
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	lock, err := h.bridgeProtocol.LockAsset(ctx, req.SourceChain, req.TargetChain, req.Asset, req.Amount, req.Recipient, expiry)
	if err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	bridge.JournalLockEvent(h.storage, "created", lock)
	if h.bridgeRelay != nil {
		h.bridgeRelay.PublishLockEvent("lock_created", lock, h.nodeID)
	}
	h.logger.Info("Bridge lock created", "lock_id", lock.ID, "amount", lock.Amount, "asset", lock.Asset)
	writeJSONResponse(w, http.StatusCreated, map[string]interface{}{
		"lock_id":      lock.ID,
		"status":       string(lock.Status),
		"secret_hash":  lock.SecretHash,
		"secret":       lock.Secret,
		"expires_at":   lock.ExpiresAt,
		"source_chain": lock.SourceChain,
		"target_chain": lock.TargetChain,
		"amount":       lock.Amount,
		"_warning":     "secret is returned once; store it securely for redemption",
	})
}

func (h *Handlers) BridgeRedeemAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.bridgeProtocol == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "bridge protocol not available")
		return
	}

	lockID := strings.TrimPrefix(r.URL.Path, "/api/v1/bridge/locks/")
	lockID = strings.TrimSuffix(lockID, "/redeem")
	if lockID == "" {
		writeErrorResponse(w, http.StatusBadRequest, "lock_id required in URL path")
		return
	}

	var req RedeemAssetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Secret == "" {
		writeErrorResponse(w, http.StatusBadRequest, "secret is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if err := h.bridgeProtocol.RedeemAsset(ctx, lockID, req.Secret); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	if lock, err := h.bridgeProtocol.GetLock(lockID); err == nil {
		bridge.JournalLockEvent(h.storage, "redeemed", lock)
		if h.bridgeRelay != nil {
			h.bridgeRelay.PublishLockEvent("lock_redeemed", lock, h.nodeID)
		}
	}
	h.logger.Info("Bridge lock redeemed", "lock_id", lockID)
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"lock_id": lockID,
		"status":  "redeemed",
	})
}

func (h *Handlers) BridgeRefundAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.bridgeProtocol == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "bridge protocol not available")
		return
	}

	lockID := strings.TrimPrefix(r.URL.Path, "/api/v1/bridge/locks/")
	lockID = strings.TrimSuffix(lockID, "/refund")
	if lockID == "" {
		writeErrorResponse(w, http.StatusBadRequest, "lock_id required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if err := h.bridgeProtocol.RefundAsset(ctx, lockID); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	if lock, err := h.bridgeProtocol.GetLock(lockID); err == nil {
		bridge.JournalLockEvent(h.storage, "refunded", lock)
		if h.bridgeRelay != nil {
			h.bridgeRelay.PublishLockEvent("lock_refunded", lock, h.nodeID)
		}
	}
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"lock_id": lockID,
		"status":  "refunded",
	})
}

func (h *Handlers) BridgeListLocks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.bridgeProtocol == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "bridge protocol not available")
		return
	}

	locks := h.bridgeProtocol.ListLocks()
	out := make([]map[string]interface{}, 0, len(locks))
	for _, l := range locks {
		out = append(out, map[string]interface{}{
			"lock_id":      l.ID,
			"status":       string(l.Status),
			"source_chain": l.SourceChain,
			"target_chain": l.TargetChain,
			"asset":        l.Asset,
			"amount":       l.Amount,
			"recipient":    l.Recipient,
			"expires_at":   l.ExpiresAt,
		})
	}
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"locks": out,
		"count": len(out),
	})
}

func (h *Handlers) BridgeGetLock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.bridgeProtocol == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "bridge protocol not available")
		return
	}

	lockID := strings.TrimPrefix(r.URL.Path, "/api/v1/bridge/locks/")
	if lockID == "" {
		writeErrorResponse(w, http.StatusBadRequest, "lock_id required")
		return
	}

	lock, err := h.bridgeProtocol.GetLock(lockID)
	if err != nil {
		writeErrorResponse(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"lock_id":      lock.ID,
		"status":       string(lock.Status),
		"source_chain": lock.SourceChain,
		"target_chain": lock.TargetChain,
		"asset":        lock.Asset,
		"amount":       lock.Amount,
		"recipient":    lock.Recipient,
		"secret_hash":  lock.SecretHash,
		"locked_at":    lock.LockedAt,
		"expires_at":   lock.ExpiresAt,
	})
}

// --- Atomic Swap Handlers ---

func (h *Handlers) SwapInitiate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.atomicSwap == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "atomic swap protocol not available")
		return
	}
	if !h.enforceNvidiaLock(w) {
		return
	}

	var req InitiateSwapRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.InitiatorChain == "" || req.ParticipantChain == "" {
		writeErrorResponse(w, http.StatusBadRequest, "initiator_chain and participant_chain are required")
		return
	}
	if req.InitiatorAmount <= 0 || req.ParticipantAmount <= 0 {
		writeErrorResponse(w, http.StatusBadRequest, "amounts must be positive")
		return
	}
	expiry := time.Duration(req.ExpiryMinutes) * time.Minute
	if expiry <= 0 {
		expiry = 60 * time.Minute
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	swap, err := h.atomicSwap.InitiateSwap(ctx,
		req.InitiatorChain, req.ParticipantChain,
		req.InitiatorAsset, req.ParticipantAsset,
		req.InitiatorAmount, req.ParticipantAmount,
		req.InitiatorAddress, req.ParticipantAddress,
		expiry)
	if err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	bridge.JournalSwapEvent(h.storage, "initiated", swap)
	if h.bridgeRelay != nil {
		h.bridgeRelay.PublishSwapEvent("swap_initiated", swap, h.nodeID)
	}
	h.logger.Info("Atomic swap initiated", "swap_id", swap.ID)
	writeJSONResponse(w, http.StatusCreated, map[string]interface{}{
		"swap_id":               swap.ID,
		"status":                string(swap.Status),
		"initiator_secret":      swap.InitiatorSecret,
		"initiator_secret_hash": swap.InitiatorSecretHash,
		"expires_at":            swap.ExpiresAt,
		"_warning":              "initiator_secret is returned once; store it securely",
	})
}

func (h *Handlers) SwapParticipate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.atomicSwap == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "atomic swap protocol not available")
		return
	}

	swapID := strings.TrimPrefix(r.URL.Path, "/api/v1/bridge/swaps/")
	swapID = strings.TrimSuffix(swapID, "/participate")
	if swapID == "" {
		writeErrorResponse(w, http.StatusBadRequest, "swap_id required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	swap, err := h.atomicSwap.ParticipateInSwap(ctx, swapID)
	if err != nil {
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	bridge.JournalSwapEvent(h.storage, "participated", swap)
	if h.bridgeRelay != nil {
		h.bridgeRelay.PublishSwapEvent("swap_participated", swap, h.nodeID)
	}
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"swap_id":                 swap.ID,
		"status":                  string(swap.Status),
		"participant_secret":      swap.ParticipantSecret,
		"participant_secret_hash": swap.ParticipantSecretHash,
		"_warning":                "participant_secret is returned once; store it securely",
	})
}

func (h *Handlers) SwapComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.atomicSwap == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "atomic swap protocol not available")
		return
	}

	swapID := strings.TrimPrefix(r.URL.Path, "/api/v1/bridge/swaps/")
	swapID = strings.TrimSuffix(swapID, "/complete")
	if swapID == "" {
		writeErrorResponse(w, http.StatusBadRequest, "swap_id required")
		return
	}

	var req CompleteSwapRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Secret == "" {
		writeErrorResponse(w, http.StatusBadRequest, "secret is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if err := h.atomicSwap.CompleteSwap(ctx, swapID, req.Secret); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	if s, err := h.atomicSwap.GetSwap(swapID); err == nil {
		bridge.JournalSwapEvent(h.storage, "completed", s)
		if h.bridgeRelay != nil {
			h.bridgeRelay.PublishSwapEvent("swap_completed", s, h.nodeID)
		}
	}
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"swap_id": swapID,
		"status":  "completed",
	})
}

func (h *Handlers) SwapRefund(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.atomicSwap == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "atomic swap protocol not available")
		return
	}

	swapID := strings.TrimPrefix(r.URL.Path, "/api/v1/bridge/swaps/")
	swapID = strings.TrimSuffix(swapID, "/refund")
	if swapID == "" {
		writeErrorResponse(w, http.StatusBadRequest, "swap_id required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if err := h.atomicSwap.RefundSwap(ctx, swapID); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	if s, err := h.atomicSwap.GetSwap(swapID); err == nil {
		bridge.JournalSwapEvent(h.storage, "refunded", s)
		if h.bridgeRelay != nil {
			h.bridgeRelay.PublishSwapEvent("swap_refunded", s, h.nodeID)
		}
	}
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"swap_id": swapID,
		"status":  "refunded",
	})
}

func (h *Handlers) SwapList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.atomicSwap == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "atomic swap protocol not available")
		return
	}

	swaps := h.atomicSwap.ListSwaps()
	out := make([]map[string]interface{}, 0, len(swaps))
	for _, s := range swaps {
		out = append(out, map[string]interface{}{
			"swap_id":            s.ID,
			"status":             string(s.Status),
			"initiator_chain":    s.InitiatorChain,
			"participant_chain":  s.ParticipantChain,
			"initiator_amount":   s.InitiatorAmount,
			"participant_amount": s.ParticipantAmount,
			"created_at":         s.CreatedAt,
			"expires_at":         s.ExpiresAt,
		})
	}
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"swaps": out,
		"count": len(out),
	})
}

func (h *Handlers) SwapGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.atomicSwap == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "atomic swap protocol not available")
		return
	}

	swapID := strings.TrimPrefix(r.URL.Path, "/api/v1/bridge/swaps/")
	if swapID == "" {
		writeErrorResponse(w, http.StatusBadRequest, "swap_id required")
		return
	}

	swap, err := h.atomicSwap.GetSwap(swapID)
	if err != nil {
		writeErrorResponse(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"swap_id":            swap.ID,
		"status":             string(swap.Status),
		"initiator_chain":    swap.InitiatorChain,
		"participant_chain":  swap.ParticipantChain,
		"initiator_asset":    swap.InitiatorAsset,
		"participant_asset":  swap.ParticipantAsset,
		"initiator_amount":   swap.InitiatorAmount,
		"participant_amount": swap.ParticipantAmount,
		"created_at":         swap.CreatedAt,
		"expires_at":         swap.ExpiresAt,
	})
}
