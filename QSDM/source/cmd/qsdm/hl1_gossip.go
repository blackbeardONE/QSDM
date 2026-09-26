package main

// hl1_gossip.go (HL1 WP9): tx gossip is closed in the producer role (design
// rev 4 §2 (d2), Appendix C safety-P1-1).
//
// In the producer role no TxGossipIngress is installed, so no peer can add a
// transaction to the producer's pool through the qsdm-transactions topic, and
// the tx-topic message handler only counts and drops. The §4.3 fingerprint
// classification relies on this: the pool then holds only the driver's own
// txs and signed wallet transfers. The follower role is unchanged. Block, BFT,
// POL and evidence gossip use other topics and are unaffected, and outbound
// publishing through the tx gossip relay is unchanged.

import (
	"sync"
	"sync/atomic"

	"github.com/blackbeardONE/QSDM/pkg/networking"
)

// txTopicWirer is the tx-topic install surface of *networking.Network.
type txTopicWirer interface {
	SetTxGossipIngress(ing *networking.TxGossipIngress)
	SetMessageHandler(handler func(msg []byte))
}

var _ txTopicWirer = (*networking.Network)(nil)

var (
	// hl1TxGossipDropped is hl1_tx_gossip_dropped_total.
	hl1TxGossipDropped atomic.Uint64
	// hl1TxGossipIngressInstalled is hl1_tx_gossip_ingress_installed: 1
	// once the follower-role ingress is installed, otherwise 0.
	hl1TxGossipIngressInstalled atomic.Int64
	hl1TxGossipLogOnce          sync.Once
)

// hl1DropTxTopic is the producer-role tx-topic handler. It runs under the
// network's n.mu (pkg/networking libp2p.go handleMessages), so it performs one
// atomic increment and takes no lock (L7).
func hl1DropTxTopic([]byte) { hl1TxGossipDropped.Add(1) }

// wireTxGossip installs the tx-topic handlers. It replaces the two former
// install points in main (net.SetTxGossipIngress and net.SetMessageHandler).
//   - Producer role: SetTxGossipIngress is never called, and the handler is
//     hl1DropTxTopic.
//   - Follower role: the ingress, then the legacy handler, as before.
func wireTxGossip(n txTopicWirer, producerRole bool, ing *networking.TxGossipIngress, legacy func([]byte)) {
	if producerRole {
		n.SetMessageHandler(hl1DropTxTopic)
		hl1TxGossipIngressInstalled.Store(0)
		hl1TxGossipLogOnce.Do(func() {
			hl1Logger().Info("hl1: producer role: tx gossip ingress not installed; tx topic dropped")
		})
		return
	}
	n.SetTxGossipIngress(ing)
	n.SetMessageHandler(legacy)
	hl1TxGossipIngressInstalled.Store(1)
}
