package bridge

import (
	"encoding/json"
	"time"
)

// StorageBackend is the interface a persistence layer must implement for bridge event journaling.
type StorageBackend interface {
	StoreTransaction(tx []byte) error
}

// JournalLockEvent writes a lock state-change event to persistent storage.
func JournalLockEvent(storage StorageBackend, eventType string, lock *Lock) {
	if storage == nil || lock == nil {
		return
	}
	evt := map[string]interface{}{
		"type":         "bridge_lock_" + eventType,
		"lock_id":      lock.ID,
		"status":       string(lock.Status),
		"source_chain": lock.SourceChain,
		"target_chain": lock.TargetChain,
		"asset":        lock.Asset,
		"amount":       lock.Amount,
		"recipient":    lock.Recipient,
		"secret_hash":  lock.SecretHash,
		"locked_at":    lock.LockedAt.Format(time.RFC3339),
		"expires_at":   lock.ExpiresAt.Format(time.RFC3339),
		"recorded_at":  time.Now().Format(time.RFC3339),
	}
	data, _ := json.Marshal(evt)
	_ = storage.StoreTransaction(data)
}

// JournalSwapEvent writes a swap state-change event to persistent storage.
func JournalSwapEvent(storage StorageBackend, eventType string, swap *Swap) {
	if storage == nil || swap == nil {
		return
	}
	evt := map[string]interface{}{
		"type":               "bridge_swap_" + eventType,
		"swap_id":            swap.ID,
		"status":             string(swap.Status),
		"initiator_chain":    swap.InitiatorChain,
		"participant_chain":  swap.ParticipantChain,
		"initiator_asset":    swap.InitiatorAsset,
		"participant_asset":  swap.ParticipantAsset,
		"initiator_amount":   swap.InitiatorAmount,
		"participant_amount": swap.ParticipantAmount,
		"created_at":         swap.CreatedAt.Format(time.RFC3339),
		"expires_at":         swap.ExpiresAt.Format(time.RFC3339),
		"recorded_at":        time.Now().Format(time.RFC3339),
	}
	data, _ := json.Marshal(evt)
	_ = storage.StoreTransaction(data)
}
