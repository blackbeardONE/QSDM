package mempool

import (
	"fmt"
	"testing"
	"time"
)

func makeTx(id string, fee float64) *Tx {
	return &Tx{ID: id, Sender: "alice", Recipient: "bob", Amount: 1, Fee: fee}
}

func TestMempool_AddAndPop(t *testing.T) {
	m := New(DefaultConfig())
	m.Add(makeTx("tx1", 1.0))
	m.Add(makeTx("tx2", 5.0))
	m.Add(makeTx("tx3", 2.0))

	if m.Size() != 3 {
		t.Fatalf("expected 3, got %d", m.Size())
	}

	tx, ok := m.Pop()
	if !ok || tx.ID != "tx2" {
		t.Fatalf("expected tx2 (highest fee), got %v", tx)
	}
	tx, ok = m.Pop()
	if !ok || tx.ID != "tx3" {
		t.Fatalf("expected tx3, got %v", tx)
	}
	tx, ok = m.Pop()
	if !ok || tx.ID != "tx1" {
		t.Fatalf("expected tx1, got %v", tx)
	}
	_, ok = m.Pop()
	if ok {
		t.Fatal("expected empty mempool")
	}
}

func TestMempool_Duplicate(t *testing.T) {
	m := New(DefaultConfig())
	m.Add(makeTx("tx1", 1.0))
	if err := m.Add(makeTx("tx1", 2.0)); err != ErrDuplicateTx {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestMempool_EvictLowestFee(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxSize = 3
	m := New(cfg)

	m.Add(makeTx("tx1", 1.0))
	m.Add(makeTx("tx2", 2.0))
	m.Add(makeTx("tx3", 3.0))

	// tx4 has higher fee than tx1, should evict tx1
	if err := m.Add(makeTx("tx4", 5.0)); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if m.Size() != 3 {
		t.Fatalf("expected 3, got %d", m.Size())
	}
	if _, ok := m.Get("tx1"); ok {
		t.Fatal("tx1 should have been evicted")
	}
}

func TestMempool_RejectLowFee(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxSize = 2
	m := New(cfg)

	m.Add(makeTx("tx1", 5.0))
	m.Add(makeTx("tx2", 3.0))

	err := m.Add(makeTx("tx3", 1.0))
	if err == nil {
		t.Fatal("expected error for low-fee tx in full pool")
	}
}

func TestMempool_Remove(t *testing.T) {
	m := New(DefaultConfig())
	m.Add(makeTx("tx1", 1.0))
	m.Add(makeTx("tx2", 2.0))

	if !m.Remove("tx1") {
		t.Fatal("expected Remove to return true")
	}
	if m.Size() != 1 {
		t.Fatalf("expected 1, got %d", m.Size())
	}
	if m.Remove("tx1") {
		t.Fatal("double remove should return false")
	}
}

func TestMempool_Peek(t *testing.T) {
	m := New(DefaultConfig())
	m.Add(makeTx("tx1", 1.0))
	m.Add(makeTx("tx2", 5.0))

	tx, ok := m.Peek()
	if !ok || tx.ID != "tx2" {
		t.Fatalf("expected tx2, got %v", tx)
	}
	if m.Size() != 2 {
		t.Fatal("Peek should not remove")
	}
}

func TestMempool_EvictStale(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxTxAge = 10 * time.Millisecond
	m := New(cfg)

	m.Add(makeTx("tx1", 1.0))
	time.Sleep(15 * time.Millisecond)
	m.Add(makeTx("tx2", 2.0))

	evicted := m.EvictStale()
	if evicted != 1 {
		t.Fatalf("expected 1 eviction, got %d", evicted)
	}
	if m.Size() != 1 {
		t.Fatalf("expected 1 remaining, got %d", m.Size())
	}
	if _, ok := m.Get("tx1"); ok {
		t.Fatal("tx1 should be evicted")
	}
}

func TestMempool_Drain(t *testing.T) {
	m := New(DefaultConfig())
	for i := 0; i < 10; i++ {
		m.Add(makeTx(fmt.Sprintf("tx%d", i), float64(i)))
	}

	batch := m.Drain(3)
	if len(batch) != 3 {
		t.Fatalf("expected 3, got %d", len(batch))
	}
	// Highest fees first: 9, 8, 7
	if batch[0].ID != "tx9" || batch[1].ID != "tx8" || batch[2].ID != "tx7" {
		t.Fatalf("unexpected drain order: %s, %s, %s", batch[0].ID, batch[1].ID, batch[2].ID)
	}
	if m.Size() != 7 {
		t.Fatalf("expected 7 remaining, got %d", m.Size())
	}
}

func TestMempool_Stats(t *testing.T) {
	m := New(DefaultConfig())
	m.Add(makeTx("tx1", 10.0))
	stats := m.Stats()
	if stats["size"].(int) != 1 {
		t.Fatalf("unexpected size: %v", stats["size"])
	}
	if stats["top_fee"].(float64) != 10.0 {
		t.Fatalf("unexpected top fee: %v", stats["top_fee"])
	}
}

func TestMempool_RestoreTransactions(t *testing.T) {
	m := New(DefaultConfig())
	txs := []*Tx{makeTx("a", 1.0), makeTx("b", 2.0)}
	m.RestoreTransactions(txs)
	if m.Size() != 2 {
		t.Fatalf("expected 2 restored, got %d", m.Size())
	}
	m.RestoreTransactions(txs)
	if m.Size() != 2 {
		t.Fatalf("duplicate restore should not grow pool, got %d", m.Size())
	}
}

func TestMempool_AdmissionChecker(t *testing.T) {
	m := New(DefaultConfig())
	m.SetAdmissionChecker(func(tx *Tx) error {
		if tx.ID == "blocked" {
			return fmt.Errorf("admission denied")
		}
		return nil
	})
	if err := m.Add(makeTx("blocked", 1.0)); err == nil {
		t.Fatal("expected admission error")
	}
	if err := m.Add(makeTx("ok", 1.0)); err != nil {
		t.Fatalf("expected ok: %v", err)
	}
}

func TestMempool_StartStop(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxTxAge = 5 * time.Millisecond
	cfg.EvictInterval = 10 * time.Millisecond
	m := New(cfg)

	m.Add(makeTx("tx1", 1.0))
	m.Start()
	time.Sleep(30 * time.Millisecond)
	m.Stop()

	if m.Size() != 0 {
		t.Fatalf("expected 0 after eviction, got %d", m.Size())
	}
}
