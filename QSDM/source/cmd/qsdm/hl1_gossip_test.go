package main

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
	"github.com/blackbeardONE/QSDM/pkg/networking"
)

// hl1FakeNet is a txTopicWirer that records what was installed and replays
// the pkg/networking libp2p.go handleMessages dispatch for the tx topic:
//
//	n.mu.Lock(); txIng := n.txGossip; handler := n.msgHandler; n.mu.Unlock()
//	if txIng != nil && txIng.TryConsumeGossip(peer, data) { continue }
//	n.mu.Lock(); if handler != nil { handler(data) }; n.mu.Unlock()
type hl1FakeNet struct {
	mu            sync.Mutex
	txGossip      *networking.TxGossipIngress
	msgHandler    func([]byte)
	ingressCalls  int
	handlerCalls  int
	lastHandlerID string
}

func (n *hl1FakeNet) SetTxGossipIngress(ing *networking.TxGossipIngress) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ingressCalls++
	n.txGossip = ing
}

func (n *hl1FakeNet) SetMessageHandler(h func([]byte)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.handlerCalls++
	n.msgHandler = h
}

func (n *hl1FakeNet) deliver(peer string, data []byte) {
	n.mu.Lock()
	txIng := n.txGossip
	handler := n.msgHandler
	n.mu.Unlock()
	if txIng != nil && txIng.TryConsumeGossip(peer, data) {
		return
	}
	n.mu.Lock()
	if handler != nil {
		handler(data)
	}
	n.mu.Unlock()
}

// hl1GossipFixture builds a real pool and a follower-grade ingress with no
// signature verifier, and two tx-topic payloads: a SignedTx with the correct
// nonce and fee, and an enrollment SignedEnvelope.
func hl1GossipFixture(t *testing.T) (*mempool.Mempool, *networking.TxGossipIngress, [][]byte) {
	t.Helper()
	accounts := chain.NewAccountStore()
	accounts.Credit("gossip-sender", 100)
	pool := mempool.New(mempool.DefaultConfig())
	ing := networking.NewTxGossipIngress(
		chain.NewGossipValidator(nil, chain.NewTxValidator(accounts), chain.DefaultGossipValidationConfig()),
		pool, nil)
	stx, err := json.Marshal(chain.SignedTx{Tx: &mempool.Tx{
		ID: "gossip-tx-1", Sender: "gossip-sender", Recipient: "gossip-recipient",
		Amount: 1, Fee: 0.01, Nonce: 0, ContractID: chain.WalletTransferContractID,
	}})
	if err != nil {
		t.Fatal(err)
	}
	env, err := json.Marshal(enrollment.SignedEnvelope{
		ID: "gossip-enroll-1", Sender: "gossip-sender", Nonce: 0, Fee: 0.01,
		ContractID: enrollment.SignedContractID, PayloadB64: "e30=", Signature: "c2ln",
	})
	if err != nil {
		t.Fatal(err)
	}
	return pool, ing, [][]byte{stx, env}
}

func TestHL1WireTxGossipProducerRoleDropsTxTopic(t *testing.T) {
	pool, ing, msgs := hl1GossipFixture(t)
	n := &hl1FakeNet{}
	before := hl1TxGossipDropped.Load()
	wireTxGossip(n, true, ing, func([]byte) { t.Fatal("legacy handler installed in the producer role") })

	if n.ingressCalls != 0 || n.txGossip != nil {
		t.Fatalf("producer role called SetTxGossipIngress %d time(s)", n.ingressCalls)
	}
	if n.handlerCalls != 1 || n.msgHandler == nil {
		t.Fatalf("producer role installed %d handler(s)", n.handlerCalls)
	}
	if got := hl1TxGossipIngressInstalled.Load(); got != 0 {
		t.Fatalf("hl1_tx_gossip_ingress_installed = %d, want 0", got)
	}
	for _, m := range msgs {
		n.deliver("peer-A", m)
	}
	if pool.Size() != 0 {
		t.Fatalf("producer pool Size() = %d, want 0", pool.Size())
	}
	if got := hl1TxGossipDropped.Load() - before; got != 2 {
		t.Fatalf("hl1_tx_gossip_dropped_total rose by %d, want 2", got)
	}
	// The drop handler takes no lock: it must not block while the network
	// holds its own mutex around the call (L7).
	n.mu.Lock()
	n.msgHandler([]byte("x"))
	n.mu.Unlock()
}

func TestHL1WireTxGossipFollowerRoleControl(t *testing.T) {
	pool, ing, msgs := hl1GossipFixture(t)
	n := &hl1FakeNet{}
	legacy := 0
	before := hl1TxGossipDropped.Load()
	wireTxGossip(n, false, ing, func([]byte) { legacy++ })
	defer hl1TxGossipIngressInstalled.Store(0)

	if n.ingressCalls != 1 || n.txGossip != ing || n.handlerCalls != 1 {
		t.Fatalf("follower wiring: ingress=%d handler=%d", n.ingressCalls, n.handlerCalls)
	}
	if got := hl1TxGossipIngressInstalled.Load(); got != 1 {
		t.Fatalf("hl1_tx_gossip_ingress_installed = %d, want 1", got)
	}
	n.deliver("peer-A", msgs[0])
	if pool.Size() != 1 {
		t.Fatalf("follower pool Size() = %d, want 1 (the test must be able to see a gossiped tx)", pool.Size())
	}
	if hl1TxGossipDropped.Load() != before {
		t.Fatal("follower role counted a drop")
	}
	if legacy != 0 {
		t.Fatalf("accepted gossip also reached the legacy handler %d time(s)", legacy)
	}
}

func TestHL1MetricsCollectorNames(t *testing.T) {
	d := &hl1DurableTip{}
	got := map[string]float64{}
	for _, m := range hl1MetricsCollector(d)() {
		got[m.Name] = m.Value
	}
	for _, name := range []string{
		"hl1_persist_hook_seconds", "hl1_served_watermark", "hl1_durable_tip",
		"hl1_tx_gossip_ingress_installed", "hl1_tx_gossip_dropped_total", "hl1_unexpected_tx_family_total",
	} {
		if _, ok := got[name]; !ok {
			t.Fatalf("metric %s missing: %v", name, got)
		}
	}
	if got["hl1_durable_tip"] != -1 {
		t.Fatalf("unset durable tip exported as %v, want -1", got["hl1_durable_tip"])
	}
	d.Store(7)
	for _, m := range hl1MetricsCollector(d)() {
		if m.Name == "hl1_durable_tip" && m.Value != 7 {
			t.Fatalf("hl1_durable_tip = %v, want 7", m.Value)
		}
	}
}
