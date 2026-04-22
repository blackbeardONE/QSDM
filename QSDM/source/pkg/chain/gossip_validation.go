package chain

import (
	"fmt"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// GossipValidationConfig controls inbound transaction gossip validation.
type GossipValidationConfig struct {
	MinFee          float64
	MaxFutureNonce  uint64
	QuarantineTTL   time.Duration
	MaxQuarantine   int
}

// DefaultGossipValidationConfig returns conservative defaults.
func DefaultGossipValidationConfig() GossipValidationConfig {
	return GossipValidationConfig{
		MinFee:         0.000001,
		MaxFutureNonce: 8,
		QuarantineTTL:  10 * time.Minute,
		MaxQuarantine:  5000,
	}
}

// QuarantineEntry stores transactions delayed from admission.
type QuarantineEntry struct {
	TxID       string    `json:"tx_id"`
	Sender     string    `json:"sender"`
	Reason     string    `json:"reason"`
	ReceivedAt time.Time `json:"received_at"`
	RetryAfter time.Time `json:"retry_after"`
}

// GossipVerdict indicates what happened to an incoming tx.
type GossipVerdict string

const (
	GossipAccepted    GossipVerdict = "accepted"
	GossipQuarantined GossipVerdict = "quarantined"
	GossipRejected    GossipVerdict = "rejected"
)

// GossipValidator validates and triages inbound gossiped transactions.
type GossipValidator struct {
	mu         sync.RWMutex
	cfg        GossipValidationConfig
	sig        *SigVerifier
	txv        *TxValidator
	quarantine map[string]QuarantineEntry // txID -> entry
}

// NewGossipValidator creates a gossip validator.
func NewGossipValidator(sig *SigVerifier, txv *TxValidator, cfg GossipValidationConfig) *GossipValidator {
	if cfg.QuarantineTTL <= 0 {
		cfg.QuarantineTTL = 10 * time.Minute
	}
	if cfg.MaxFutureNonce == 0 {
		cfg.MaxFutureNonce = 8
	}
	if cfg.MaxQuarantine <= 0 {
		cfg.MaxQuarantine = 5000
	}
	return &GossipValidator{
		cfg:        cfg,
		sig:        sig,
		txv:        txv,
		quarantine: make(map[string]QuarantineEntry),
	}
}

// HandleIncoming validates a signed tx and either admits it, quarantines it, or rejects it.
func (gv *GossipValidator) HandleIncoming(pool *mempool.Mempool, stx *SignedTx) (GossipVerdict, error) {
	if stx == nil || stx.Tx == nil {
		return GossipRejected, fmt.Errorf("nil signed transaction")
	}
	tx := stx.Tx

	if gv.sig != nil {
		if err := gv.sig.Verify(stx); err != nil {
			return GossipRejected, fmt.Errorf("signature verification failed: %w", err)
		}
	}
	if tx.Fee < gv.cfg.MinFee {
		return GossipRejected, fmt.Errorf("fee below floor: got %.8f, min %.8f", tx.Fee, gv.cfg.MinFee)
	}

	expected := gv.txv.PendingNonce(tx.Sender)
	if tx.Nonce < expected {
		return GossipRejected, fmt.Errorf("nonce too low: expected >= %d, got %d", expected, tx.Nonce)
	}
	if tx.Nonce > expected+gv.cfg.MaxFutureNonce {
		return GossipRejected, fmt.Errorf("nonce too far in future: expected <= %d, got %d", expected+gv.cfg.MaxFutureNonce, tx.Nonce)
	}

	// Future nonces are quarantined until missing nonces arrive.
	if tx.Nonce > expected {
		gv.quarantineTx(tx, "future nonce")
		return GossipQuarantined, nil
	}

	if err := gv.txv.ValidateAndAdd(pool, tx); err != nil {
		return GossipRejected, err
	}
	return GossipAccepted, nil
}

func (gv *GossipValidator) quarantineTx(tx *mempool.Tx, reason string) {
	gv.mu.Lock()
	defer gv.mu.Unlock()
	if len(gv.quarantine) >= gv.cfg.MaxQuarantine {
		// best-effort eviction of one arbitrary entry
		for id := range gv.quarantine {
			delete(gv.quarantine, id)
			break
		}
	}
	now := time.Now()
	gv.quarantine[tx.ID] = QuarantineEntry{
		TxID:       tx.ID,
		Sender:     tx.Sender,
		Reason:     reason,
		ReceivedAt: now,
		RetryAfter: now.Add(gv.cfg.QuarantineTTL),
	}
}

// QuarantineSize returns number of quarantined txs.
func (gv *GossipValidator) QuarantineSize() int {
	gv.mu.RLock()
	defer gv.mu.RUnlock()
	return len(gv.quarantine)
}

// QuarantineEntries returns a snapshot of quarantine entries.
func (gv *GossipValidator) QuarantineEntries() []QuarantineEntry {
	gv.mu.RLock()
	defer gv.mu.RUnlock()
	out := make([]QuarantineEntry, 0, len(gv.quarantine))
	for _, e := range gv.quarantine {
		out = append(out, e)
	}
	return out
}

// PurgeExpired removes expired quarantine entries.
func (gv *GossipValidator) PurgeExpired() int {
	gv.mu.Lock()
	defer gv.mu.Unlock()
	now := time.Now()
	removed := 0
	for id, e := range gv.quarantine {
		if now.After(e.RetryAfter) {
			delete(gv.quarantine, id)
			removed++
		}
	}
	return removed
}

