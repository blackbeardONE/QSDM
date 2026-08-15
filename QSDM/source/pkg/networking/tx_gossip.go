package networking

import (
	"encoding/json"
	"fmt"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
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
	// Banned peers are dropped before validation or mempool admission.
	// This ingress penalizes invalid transactions via RecordEvent, but
	// nothing read the resulting ban, so a peer that had already been
	// banned for flooding invalid txs kept getting every message processed.
	if ti.rep != nil && ti.rep.IsBanned(peerID) {
		return chain.GossipRejected, fmt.Errorf("tx gossip refused: peer %s is banned", peerID)
	}

	var stx chain.SignedTx
	if err := json.Unmarshal(payload, &stx); err == nil && stx.Tx != nil {
		return ti.handleSignedTx(peerID, payload, &stx)
	}

	var env enrollment.SignedEnvelope
	if err := json.Unmarshal(payload, &env); err == nil && env.ContractID == enrollment.SignedContractID && env.ID != "" {
		return ti.handleEnrollmentEnvelope(peerID, payload, env)
	}

	if ti.rep != nil {
		ti.rep.RecordEvent(peerID, EventInvalidTx, 0)
	}
	return chain.GossipRejected, fmt.Errorf("invalid gossip payload")
}

func (ti *TxGossipIngress) handleEnrollmentEnvelope(peerID string, payload []byte, env enrollment.SignedEnvelope) (chain.GossipVerdict, error) {
	if err := enrollment.VerifySignedEnvelope(env); err != nil {
		if ti.rep != nil {
			ti.rep.RecordEvent(peerID, EventInvalidTx, 0)
		}
		return chain.GossipRejected, fmt.Errorf("invalid enrollment gossip signature: %w", err)
	}
	tx, err := env.ToTransaction()
	if err != nil {
		if ti.rep != nil {
			ti.rep.RecordEvent(peerID, EventInvalidTx, 0)
		}
		return chain.GossipRejected, fmt.Errorf("invalid enrollment gossip transaction: %w", err)
	}
	if ti.pool == nil {
		return chain.GossipRejected, fmt.Errorf("transaction mempool unavailable")
	}
	if err := ti.pool.Add(tx); err != nil {
		if ti.rep != nil {
			ti.rep.RecordEvent(peerID, EventInvalidTx, 0)
		}
		return chain.GossipRejected, fmt.Errorf("enrollment gossip admission failed: %w", err)
	}
	if ti.rep != nil {
		ti.rep.RecordEvent(peerID, EventValidTx, 0)
	}
	walletp2p.NoteIngested(tx.ID)
	if ti.relay != nil && len(payload) > 0 {
		_ = ti.relay.MaybePublish(tx.ID, payload)
	}
	return chain.GossipAccepted, nil
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
//
// This is the LIVE ingress path (libp2p.go:258). HandlePeerMessage, which carries
// the same dispatch, has zero production callers -- so the ban check that lived
// only there was never enforced on anything, and a peer banned for flooding
// invalid transactions kept having every message admitted. Same shape as the
// duplicated block-hash derivation in chain/propagation.go: a second copy of a
// dispatch that drifted from the original.
//
// The check is deliberately NOT hoisted to the top of the function, and this
// deliberately does NOT delegate to HandlePeerMessage, because the two have
// different inputs. HandlePeerMessage is the transaction ingress: every payload
// reaching it is meant to be a transaction, so it refuses a banned peer before
// decoding and penalises anything that fails to decode. TryConsumeGossip is a
// filter in the pubsub receive loop and sees EVERY topic -- blocks, BFT votes,
// evidence. Refusing before the decode would let a tx-reputation ban swallow a
// banned peer's block gossip too, and delegating would call
// RecordEvent(EventInvalidTx) on every honest block message, penalising good
// peers until they were banned in turn.
//
// So the ban is consulted only once the payload is known to be transaction
// shaped. That restores exactly the protection that was intended -- a banned
// peer's transactions are not admitted -- without widening the ban's blast
// radius to traffic it was never scored on.
func (ti *TxGossipIngress) TryConsumeGossip(peerID string, payload []byte) bool {
	if ti == nil {
		return false
	}
	banned := ti.rep != nil && ti.rep.IsBanned(peerID)

	var stx chain.SignedTx
	if err := json.Unmarshal(payload, &stx); err == nil && stx.Tx != nil {
		if banned {
			// Consumed, not passed on: returning false would hand the payload
			// to the legacy byte handler, which admits it with no ban check at
			// all -- the drop has to happen here or it does not happen.
			return true
		}
		verdict, _ := ti.handleSignedTx(peerID, payload, &stx)
		return verdict == chain.GossipAccepted || verdict == chain.GossipQuarantined
	}
	var env enrollment.SignedEnvelope
	if err := json.Unmarshal(payload, &env); err != nil || env.ContractID != enrollment.SignedContractID || env.ID == "" {
		return false
	}
	if banned {
		return true
	}
	verdict, _ := ti.handleEnrollmentEnvelope(peerID, payload, env)
	return verdict == chain.GossipAccepted
}
