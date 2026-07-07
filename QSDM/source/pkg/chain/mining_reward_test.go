package chain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

func deferredRewardFixture(t *testing.T) (*EnrollmentAwareApplier, *AccountStore, *enrollment.InMemoryState) {
	t.Helper()
	accounts := NewAccountStore()
	accounts.Credit(MiningRewardFunderAddress, 100)
	state := enrollment.NewInMemoryState()
	if err := state.ApplyEnroll(enrollment.EnrollmentRecord{
		NodeID: "reward-rig", Owner: "miner", GPUUUID: "GPU-reward",
		BondMode:          enrollment.BondModeMiningRewards,
		RequiredStakeDust: mining.MinEnrollStakeDust,
	}); err != nil {
		t.Fatalf("ApplyEnroll: %v", err)
	}
	ea := NewEnrollmentApplier(accounts, state)
	aware := NewEnrollmentAwareApplier(accounts, ea)
	aware.SetHeightFn(func() uint64 { return enrollment.DeferredBondActivationHeight })
	return aware, accounts, state
}

func protocolReward(nonce uint64, amount float64) *mempool.Tx {
	return &mempool.Tx{
		ID: "reward", Sender: MiningRewardFunderAddress, Recipient: "miner",
		Amount: amount, Nonce: nonce, ContractID: MiningRewardContractID,
	}
}

func TestMiningRewardBuildsBondBeforeLiquidCredit(t *testing.T) {
	aware, accounts, state := deferredRewardFixture(t)
	if err := aware.ApplyTx(protocolReward(0, 4)); err != nil {
		t.Fatalf("first reward: %v", err)
	}
	rec, _ := state.Lookup("reward-rig")
	if rec.StakeDust != 4*dustPerCELL {
		t.Fatalf("first locked stake=%d, want %d", rec.StakeDust, 4*dustPerCELL)
	}
	if miner, ok := accounts.Get("miner"); ok && miner.Balance != 0 {
		t.Fatalf("first reward became liquid: %+v", miner)
	}

	if err := aware.ApplyTx(protocolReward(1, 8)); err != nil {
		t.Fatalf("second reward: %v", err)
	}
	rec, _ = state.Lookup("reward-rig")
	if !rec.FullyBonded() || rec.StakeDust != mining.MinEnrollStakeDust {
		t.Fatalf("bond not filled: %+v", rec)
	}
	miner, ok := accounts.Get("miner")
	if !ok || miner.Balance != 2 {
		t.Fatalf("liquid overflow balance=%+v ok=%v, want 2 CELL", miner, ok)
	}

	if err := aware.ApplyTx(protocolReward(2, 3)); err != nil {
		t.Fatalf("third reward: %v", err)
	}
	miner, _ = accounts.Get("miner")
	if miner.Balance != 5 {
		t.Fatalf("fully bonded reward balance=%.8f, want 5", miner.Balance)
	}
}

func TestMiningRewardFailureRollsBackBond(t *testing.T) {
	aware, _, state := deferredRewardFixture(t)
	err := aware.ApplyTx(protocolReward(9, 4))
	if err == nil {
		t.Fatal("bad funder nonce was accepted")
	}
	rec, _ := state.Lookup("reward-rig")
	if rec.StakeDust != 0 {
		t.Fatalf("failed reward mutated bond: %d", rec.StakeDust)
	}
}

func TestDeferredEnrollmentCreatesZeroBalanceAccount(t *testing.T) {
	pk, sk, err := mldsa87.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pub, _ := pk.MarshalBinary()
	sum := sha256.Sum256(pub)
	sender := hex.EncodeToString(sum[:])
	payload := enrollment.EnrollPayload{
		Kind: enrollment.PayloadKindEnroll, NodeID: "zero-balance-rig",
		GPUUUID: "GPU-zero-balance", HMACKey: bytes.Repeat([]byte{0x31}, 32),
		BondMode: enrollment.BondModeMiningRewards,
	}
	workNonce, _, err := enrollment.FindDeferredBondWork(payload)
	if err != nil {
		t.Fatalf("FindDeferredBondWork: %v", err)
	}
	payload.WorkNonce = workNonce
	raw, err := enrollment.EncodeEnrollPayload(payload)
	if err != nil {
		t.Fatalf("EncodeEnrollPayload: %v", err)
	}
	tx := &mempool.Tx{
		ID: "zero-balance-enroll", Sender: sender, Nonce: 0, Fee: 0,
		ContractID: enrollment.SignedContractID, Payload: raw,
	}
	env, _ := enrollment.EnvelopeFromTransaction(tx)
	canonical, _ := env.CanonicalBytes()
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(sk, canonical, nil, true, sig); err != nil {
		t.Fatalf("SignTo: %v", err)
	}
	tx.PublicKey = hex.EncodeToString(pub)
	tx.Signature = hex.EncodeToString(sig)

	accounts := NewAccountStore()
	state := enrollment.NewInMemoryState()
	applier := NewEnrollmentApplier(accounts, state)
	if err := applier.ApplyEnrollmentTx(tx, enrollment.DeferredBondActivationHeight); err != nil {
		t.Fatalf("ApplyEnrollmentTx: %v", err)
	}
	acc, ok := accounts.Get(sender)
	if !ok || acc.Balance != 0 || acc.Nonce != 1 {
		t.Fatalf("zero-balance account=%+v ok=%v", acc, ok)
	}
	rec, _ := state.Lookup("zero-balance-rig")
	if rec == nil || rec.StakeDust != 0 || rec.NormalizedBondMode() != enrollment.BondModeMiningRewards {
		t.Fatalf("deferred enrollment record=%+v", rec)
	}
}

func TestDeferredEnrollmentRejectsLegacyUnsignedContract(t *testing.T) {
	payload := enrollment.EnrollPayload{
		Kind: enrollment.PayloadKindEnroll, NodeID: "unsigned-zero-balance-rig",
		GPUUUID: "GPU-unsigned-zero-balance", HMACKey: bytes.Repeat([]byte{0x41}, 32),
		BondMode: enrollment.BondModeMiningRewards,
	}
	workNonce, _, err := enrollment.FindDeferredBondWork(payload)
	if err != nil {
		t.Fatalf("FindDeferredBondWork: %v", err)
	}
	payload.WorkNonce = workNonce
	raw, err := enrollment.EncodeEnrollPayload(payload)
	if err != nil {
		t.Fatalf("EncodeEnrollPayload: %v", err)
	}
	tx := &mempool.Tx{
		ID: "unsigned-zero-balance-enroll", Sender: "unsigned-sender", Nonce: 0, Fee: 0,
		ContractID: enrollment.ContractID, Payload: raw,
	}

	applier := NewEnrollmentApplier(NewAccountStore(), enrollment.NewInMemoryState())
	err = applier.ApplyEnrollmentTx(tx, enrollment.DeferredBondActivationHeight)
	if !errors.Is(err, enrollment.ErrLegacyContractDisabled) {
		t.Fatalf("legacy deferred enrollment error = %v, want ErrLegacyContractDisabled", err)
	}
}
