package chain

import (
	"testing"
	"time"
)

func TestRunSyntheticBFTRoundWithExecutor(t *testing.T) {
	before := CurrentSyntheticPresealStats()
	signer, address := newBFTKey(t)
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	if err := vs.Register(address, DefaultValidatorSetConfig().MinStake); err != nil {
		t.Fatal(err)
	}
	bc := NewBFTConsensus(vs, DefaultConsensusConfig())
	ex := NewBFTExecutor(bc)
	ex.SetVoteSigner(signer)
	sr := "proposal-root-1"
	blk := &Block{
		Height: 1, PrevHash: "", Timestamp: time.Unix(1700000000, 0),
		Transactions: nil, StateRoot: sr, TotalFees: 0, GasUsed: 0, ProducerID: "node",
	}
	blk.Hash = computeBlockHash(blk)
	if err := RunSyntheticBFTRoundWithExecutor(ex, vs, blk); err != nil {
		t.Fatal(err)
	}
	if !bc.IsCommitted(1) {
		t.Fatal("expected height 1 committed")
	}
	after := CurrentSyntheticPresealStats()
	if after.Attempts != before.Attempts+1 || after.Commits != before.Commits+1 {
		t.Fatalf("synthetic stats = %+v, before %+v", after, before)
	}
	if ex.PeerVoteCommitCount() != 0 {
		t.Fatal("synthetic preseal must not increment peer-vote commits")
	}
	if err := RunSyntheticBFTRoundWithExecutor(ex, vs, blk); err != nil {
		t.Fatalf("second call should noop committed height: %v", err)
	}
	afterNoop := CurrentSyntheticPresealStats()
	if afterNoop.Attempts != after.Attempts+1 || afterNoop.Commits != after.Commits {
		t.Fatalf("idempotent synthetic stats = %+v, before %+v", afterNoop, after)
	}
}

func TestRunSyntheticBFTRoundRefusesToImpersonateValidators(t *testing.T) {
	before := CurrentSyntheticPresealStats()
	signer, address := newBFTKey(t)
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	if err := vs.Register(address, DefaultValidatorSetConfig().MinStake); err != nil {
		t.Fatal(err)
	}
	if err := vs.Register("another-validator", DefaultValidatorSetConfig().MinStake); err != nil {
		t.Fatal(err)
	}
	ex := NewBFTExecutor(NewBFTConsensus(vs, DefaultConsensusConfig()))
	ex.SetVoteSigner(signer)
	blk := &Block{Height: 1, StateRoot: "root"}
	if err := RunSyntheticBFTRoundWithExecutor(ex, vs, blk); err == nil {
		t.Fatal("synthetic round must not impersonate a validator whose key is unavailable")
	}
	after := CurrentSyntheticPresealStats()
	if after.RejectedMultivalidator != before.RejectedMultivalidator+1 {
		t.Fatalf("multi-validator rejection counter = %d, want %d",
			after.RejectedMultivalidator, before.RejectedMultivalidator+1)
	}
}

func TestValidateSyntheticBFTSignerRequiresSingleton(t *testing.T) {
	signer, _ := newBFTKey(t)
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	ex := NewBFTExecutor(NewBFTConsensus(vs, DefaultConsensusConfig()))
	ex.SetVoteSigner(signer)
	if _, err := ValidateSyntheticBFTSigner(ex, vs); err == nil {
		t.Fatal("synthetic signer validation must reject an empty validator set")
	}
}
