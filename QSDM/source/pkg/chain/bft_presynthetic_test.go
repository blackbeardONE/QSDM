package chain

import (
	"testing"
	"time"
)

func TestRunSyntheticBFTRoundWithExecutor(t *testing.T) {
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
	if err := RunSyntheticBFTRoundWithExecutor(ex, vs, blk); err != nil {
		t.Fatalf("second call should noop committed height: %v", err)
	}
}

func TestRunSyntheticBFTRoundRefusesToImpersonateValidators(t *testing.T) {
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
