package chain

import (
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

func TestSyncValidatorStakesFromCommittedTip_ReappliesBondAfterAccountSync(t *testing.T) {
	pid := "producer-1"
	as := NewAccountStore()
	as.Credit(pid, 10_000)

	vs := NewValidatorSet(DefaultValidatorSetConfig())
	if err := vs.Register(pid, 100); err != nil {
		t.Fatal(err)
	}

	pool := mempool.New(mempool.DefaultConfig())
	cfg := DefaultProducerConfig()
	cfg.ProducerID = pid
	bp := NewBlockProducer(pool, as, cfg)

	addTx := func(id string, nonce uint64) {
		t.Helper()
		if err := pool.Add(&mempool.Tx{
			ID: id, Sender: pid, Recipient: "bob", Amount: 1, Fee: 0.01, Nonce: nonce, GasLimit: 21_000,
		}); err != nil {
			t.Fatal(err)
		}
	}
	addTx("t0", 0)
	if _, err := bp.ProduceBlock(); err != nil {
		t.Fatal(err)
	}
	addTx("t1", 1)
	if _, err := bp.ProduceBlock(); err != nil {
		t.Fatal(err)
	}

	SyncValidatorStakesFromCommittedTip(vs, as, bp, nil)
	v1, _ := vs.GetValidator(pid)
	stakeAfterBond := v1.Stake

	SyncValidatorStakesFromCommittedTip(vs, as, bp, nil)
	v2, _ := vs.GetValidator(pid)
	if v2.Stake != stakeAfterBond {
		t.Fatalf("expected stable stake after repeat sync+bond re-apply, got %v want %v", v2.Stake, stakeAfterBond)
	}
}
