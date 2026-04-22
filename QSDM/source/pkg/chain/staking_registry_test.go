package chain

import (
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

func TestCountProducerBlocks(t *testing.T) {
	pid := "p1"
	as := NewAccountStore()
	as.Credit(pid, 1_000)
	pool := mempool.New(mempool.DefaultConfig())
	cfg := DefaultProducerConfig()
	cfg.ProducerID = pid
	bp := NewBlockProducer(pool, as, cfg)
	_ = pool.Add(&mempool.Tx{ID: "a", Sender: pid, Recipient: "x", Amount: 1, Fee: 0.01, Nonce: 0, GasLimit: 21_000})
	if _, err := bp.ProduceBlock(); err != nil {
		t.Fatal(err)
	}
	_ = pool.Add(&mempool.Tx{ID: "b", Sender: pid, Recipient: "x", Amount: 1, Fee: 0.01, Nonce: 1, GasLimit: 21_000})
	if _, err := bp.ProduceBlock(); err != nil {
		t.Fatal(err)
	}
	c := countProducerBlocks(bp)
	if c[pid] != 2 {
		t.Fatalf("want 2 producer blocks, got %v", c)
	}
}

func TestSyncValidatorStakesFromCommittedChain_Idempotent(t *testing.T) {
	pid := "val-a"
	as := NewAccountStore()
	as.Credit(pid, 5_000)
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	if err := vs.Register(pid, 100); err != nil {
		t.Fatal(err)
	}
	pool := mempool.New(mempool.DefaultConfig())
	cfg := DefaultProducerConfig()
	cfg.ProducerID = pid
	bp := NewBlockProducer(pool, as, cfg)
	_ = pool.Add(&mempool.Tx{ID: "a", Sender: pid, Recipient: "x", Amount: 1, Fee: 0.01, Nonce: 0, GasLimit: 21_000})
	if _, err := bp.ProduceBlock(); err != nil {
		t.Fatal(err)
	}
	SyncValidatorStakesFromCommittedChain(vs, as, bp, 0.1)
	v1, _ := vs.GetValidator(pid)
	SyncValidatorStakesFromCommittedChain(vs, as, bp, 0.1)
	v2, _ := vs.GetValidator(pid)
	if v1.Stake != v2.Stake {
		t.Fatalf("stake drift %v vs %v", v1.Stake, v2.Stake)
	}
}
