package networking

import (
	"encoding/json"
	"fmt"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/walletp2p"
)

// TxGossipIngress validates inbound transaction gossip before local admission.
type TxGossipIngress struct {
	validator *chain.GossipValidator
	pool      *mempool.Mempool
	rep       *ReputationTracker
	relay     *TxGossipRelay
}

// NewTxGossipIngress creates an inbound gossip handler.
func NewTxGossipIngress(validator *chain.GossipValidator, pool *mempool.Mempool, rep *ReputationTracker) *TxGossipIngress {
	return &TxGossipIngress{validator: validator, pool: pool, rep: rep}
}

// SetTxGossipRelay attaches optional egress relay (re-broadcast accepted gossip).
func (ti *TxGossipIngress) SetTxGossipRelay(r *TxGossipRelay) {
	ti.relay = r
}

// HandlePeerMessage validates a signed transaction gossip payload.
func (ti *TxGossipIngress) HandlePeerMessage(peerID string, payload []byte) (chain.GossipVerdict, error) {
	var stx chain.SignedTx
	if err := json.Unmarshal(payload, &stx); err != nil {
		if ti.rep != nil {
			ti.rep.RecordEvent(peerID, EventInvalidTx, 0)
		}
		return chain.GossipRejected, fmt.Errorf("invalid gossip payload: %w", err)
	}
	return ti.handleSignedTx(peerID, payload, &stx)
}

func (ti *TxGossipIngress) handleSignedTx(peerID string, payload []byte, stx *chain.SignedTx) (chain.GossipVerdict, error) {
	if stx == nil || stx.Tx == nil {
		if ti.rep != nil {
			ti.rep.RecordEvent(peerID, EventInvalidTx, 0)
		}
		return chain.GossipRejected, fmt.Errorf("nil signed transaction")
	}
	verdict, err := ti.validator.HandleIncoming(ti.pool, stx)
	if ti.rep != nil {
		switch verdict {
		case chain.GossipAccepted:
			ti.rep.RecordEvent(peerID, EventValidTx, 0)
		case chain.GossipRejected:
			ti.rep.RecordEvent(peerID, EventInvalidTx, 0)
		}
	}
	if verdict == chain.GossipAccepted && stx.Tx != nil && stx.Tx.ID != "" {
		walletp2p.NoteIngested(stx.Tx.ID)
	}
	if verdict == chain.GossipAccepted && ti.relay != nil && stx.Tx != nil && len(payload) > 0 {
		_ = ti.relay.MaybePublish(stx.Tx.ID, payload)
	}
	return verdict, err
}

// TryConsumeGossip returns true when the payload decodes as a signed tx and the gossip
// path admitted or quarantined it, so legacy byte handlers should not reprocess the message.
func (ti *TxGossipIngress) TryConsumeGossip(peerID string, payload []byte) bool {
	if ti == nil {
		return false
	}
	var stx chain.SignedTx
	if err := json.Unmarshal(payload, &stx); err != nil {
		return false
	}
	if stx.Tx == nil {
		return false
	}
	verdict, _ := ti.handleSignedTx(peerID, payload, &stx)
	return verdict == chain.GossipAccepted || verdict == chain.GossipQuarantined
}

