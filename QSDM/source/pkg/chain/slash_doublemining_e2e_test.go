package chain

// End-to-end test for the double-mining slash path:
//
//   1. Enroll a miner with a known HMAC key. The enrollment
//      bonds 10 CELL of stake.
//   2. The (compromised) miner signs TWO distinct, valid v2
//      proofs at the same (Epoch, Height) — equivocation.
//   3. A slasher catches both proofs, builds a doublemining
//      Evidence blob, and submits a slash tx.
//   4. The chain runs the full applier path:
//        - decode slash payload
//        - lookup enrollment record
//        - re-run hmac.Verifier on BOTH proofs (via
//          doublemining.Verifier)
//        - both proofs accepted, distinct, same height → offence
//        - SlashStake drains the bond
//        - slasher gets RewardBPS share, rest is burned
//        - evidence-fingerprint marked seen → replay locked
//
// Companion to slash_forgedattest_e2e_test.go; the same
// consensus-critical pipeline is now exercised against the
// second concrete EvidenceVerifier to land in the v2 protocol.

import (
	"encoding/hex"
	"errors"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing/doublemining"
)

// ----- e2e helpers ------------------------------------------------

// dmFixture mirrors forgedFixture but wires the doublemining
// verifier in addition to the forgedattest verifier through
// doublemining.NewProductionSlashingDispatcher. The same
// enrollment / accounts state is used; only the dispatcher
// differs.
type dmFixture struct {
	accounts     *AccountStore
	state        *enrollment.InMemoryState
	dispatcher   *slashing.Dispatcher
	slasher      *SlashApplier
	enrollKey    []byte
	enrolledID   string
	enrolledUUID string
	offender     string
	slasherAddr  string
}

func buildDoubleMiningFixture(t *testing.T, rewardBPS uint16) *dmFixture {
	t.Helper()

	accounts := NewAccountStore()
	accounts.Credit("offender-addr-dm", 100.0)
	accounts.Credit("slasher-addr-dm", 5.0)

	state := enrollment.NewInMemoryState()
	enrollAp := NewEnrollmentApplier(accounts, state)

	enrolledID := "rig-dm-09"
	enrolledUUID := "GPU-fedcba98-7654-3210-fedc-ba9876543210"
	enrollKey := make([]byte, 32)
	for i := range enrollKey {
		enrollKey[i] = 0xCD
	}

	stakeDust := uint64(10 * 100_000_000)
	pl := enrollment.EnrollPayload{
		Kind:      enrollment.PayloadKindEnroll,
		NodeID:    enrolledID,
		GPUUUID:   enrolledUUID,
		HMACKey:   enrollKey,
		StakeDust: stakeDust,
	}
	raw, err := enrollment.EncodeEnrollPayload(pl)
	if err != nil {
		t.Fatalf("encode enroll: %v", err)
	}
	enrollTx := &mempool.Tx{
		Sender:     "offender-addr-dm",
		Nonce:      0,
		Fee:        0.01,
		ContractID: enrollment.ContractID,
		Payload:    raw,
	}
	if err := enrollAp.ApplyEnrollmentTx(enrollTx, 100); err != nil {
		t.Fatalf("apply enroll: %v", err)
	}

	registry := enrollment.NewStateBackedRegistry(state)
	disp, err := doublemining.NewProductionSlashingDispatcher(registry, nil, 0, 0)
	if err != nil {
		t.Fatalf("NewProductionSlashingDispatcher: %v", err)
	}
	slasher := NewSlashApplier(accounts, state, disp, rewardBPS)

	return &dmFixture{
		accounts:     accounts,
		state:        state,
		dispatcher:   disp,
		slasher:      slasher,
		enrollKey:    enrollKey,
		enrolledID:   enrolledID,
		enrolledUUID: enrolledUUID,
		offender:     "offender-addr-dm",
		slasherAddr:  "slasher-addr-dm",
	}
}

// buildEquivocatingProof constructs an honest v2 proof signed
// with the enrollment key, parameterised by `seed` so callers
// can produce two distinct proofs at the same (Epoch, Height).
func buildEquivocatingProof(t *testing.T, fx *dmFixture, seed byte) mining.Proof {
	t.Helper()

	var nonce [32]byte
	for i := range nonce {
		nonce[i] = byte(i + 1) ^ seed
	}
	var batchRoot, mix [32]byte
	for i := range batchRoot {
		batchRoot[i] = byte(0x10+i) ^ seed
		mix[i] = byte(0xE0-i) ^ seed
	}
	minerAddr := "qsdm1offenderdm"
	issuedAt := int64(1_700_000_000)

	p := mining.Proof{
		Version:    mining.ProtocolVersionV2,
		Epoch:      1,
		Height:     200,
		HeaderHash: [32]byte{0xDD},
		BatchRoot:  batchRoot,
		BatchCount: 1,
		Nonce:      [16]byte{seed, 0x99},
		MixDigest:  mix,
		MinerAddr:  minerAddr,
		Attestation: mining.Attestation{
			Type:     mining.AttestationTypeHMAC,
			GPUArch:  "ada",
			Nonce:    nonce,
			IssuedAt: issuedAt,
		},
	}

	b := hmac.Bundle{
		ChallengeBind: hmac.HexChallengeBind(minerAddr, batchRoot, mix),
		ComputeCap:    "8.9",
		CUDAVersion:   "12.8",
		DriverVer:     "572.16",
		GPUName:       "NVIDIA GeForce RTX 4090",
		GPUUUID:       fx.enrolledUUID,
		IssuedAt:      issuedAt,
		NodeID:        fx.enrolledID,
		Nonce:         hex.EncodeToString(nonce[:]),
	}
	signed, err := b.Sign(fx.enrollKey)
	if err != nil {
		t.Fatalf("sign equivocating bundle: %v", err)
	}
	bundleB64, err := signed.MarshalBase64()
	if err != nil {
		t.Fatalf("marshal equivocating bundle: %v", err)
	}
	p.Attestation.BundleBase64 = bundleB64
	return p
}

// ----- the e2e tests ----------------------------------------------

// TestSlashE2E_DoubleMining_DrainsStakeAndRewardsSlasher is the
// canonical happy-path: an enrolled miner produces two distinct,
// valid proofs at the same height. The slasher catches both, the
// chain proves the equivocation, the bond is drained, and the
// slasher's reward share is credited.
func TestSlashE2E_DoubleMining_DrainsStakeAndRewardsSlasher(t *testing.T) {
	t.Parallel()
	const rewardBPS uint16 = 1000 // 10% reward
	fx := buildDoubleMiningFixture(t, rewardBPS)

	preStake := fx.lookupStake(t)
	preSlasherBalance := fx.balanceOf(fx.slasherAddr)
	if preStake == 0 {
		t.Fatalf("fixture invariant: enrolled stake should be non-zero")
	}

	pa := buildEquivocatingProof(t, fx, 0xA)
	pb := buildEquivocatingProof(t, fx, 0xB)

	ev := doublemining.Evidence{
		ProofA: pa,
		ProofB: pb,
		Memo:   "caught two valid proofs at h=200",
	}
	evBlob, err := doublemining.EncodeEvidence(ev)
	if err != nil {
		t.Fatalf("encode evidence: %v", err)
	}

	slashTx := &mempool.Tx{
		Sender:     fx.slasherAddr,
		Nonce:      0,
		Fee:        0.01,
		ContractID: slashing.ContractID,
		Payload: mustEncodeSlashPayload(t, slashing.SlashPayload{
			NodeID:          fx.enrolledID,
			EvidenceKind:    slashing.EvidenceKindDoubleMining,
			EvidenceBlob:    evBlob,
			SlashAmountDust: doublemining.DefaultMaxSlashDust,
			Memo:            "e2e-dm",
		}),
	}

	if err := fx.slasher.ApplySlashTx(slashTx, 250); err != nil {
		t.Fatalf("ApplySlashTx: %v", err)
	}

	// Assertion 1: stake fully drained.
	if got := fx.lookupStake(t); got != 0 {
		t.Errorf("stake post-slash = %d dust, want 0 (full drain)", got)
	}

	// Assertion 2: slasher rewarded.
	expectRewardDust := preStake * uint64(rewardBPS) / 10000
	expectNetBalance := dustToBalance(expectRewardDust) - 0.01
	gotNetBalance := fx.balanceOf(fx.slasherAddr) - preSlasherBalance
	const tol = 1e-9
	if diff := gotNetBalance - expectNetBalance; diff > tol || diff < -tol {
		t.Errorf("slasher net balance change = %v CELL, want ~%v CELL (reward %d dust - fee 0.01)",
			gotNetBalance, expectNetBalance, expectRewardDust)
	}

	// Assertion 3: replay protection.
	dupTx := &mempool.Tx{
		Sender:     fx.slasherAddr,
		Nonce:      1,
		Fee:        0.01,
		ContractID: slashing.ContractID,
		Payload:    slashTx.Payload,
	}
	if err := fx.slasher.ApplySlashTx(dupTx, 251); err == nil {
		t.Errorf("expected duplicate double-mining slash to be rejected")
	}

	// Assertion 4: pair-order invariance — submitting the
	// SAME equivocation in (b, a) order must hit the same
	// fingerprint and be rejected. Defence-in-depth on
	// EncodeEvidence's order canonicalisation.
	swappedBlob, err := doublemining.EncodeEvidence(doublemining.Evidence{
		ProofA: pb,
		ProofB: pa,
		Memo:   "caught two valid proofs at h=200",
	})
	if err != nil {
		t.Fatalf("encode swapped evidence: %v", err)
	}
	swappedTx := &mempool.Tx{
		Sender:     fx.slasherAddr,
		Nonce:      2,
		Fee:        0.01,
		ContractID: slashing.ContractID,
		Payload: mustEncodeSlashPayload(t, slashing.SlashPayload{
			NodeID:          fx.enrolledID,
			EvidenceKind:    slashing.EvidenceKindDoubleMining,
			EvidenceBlob:    swappedBlob,
			SlashAmountDust: doublemining.DefaultMaxSlashDust,
			Memo:            "e2e-dm",
		}),
	}
	if err := fx.slasher.ApplySlashTx(swappedTx, 252); err == nil {
		t.Errorf("expected order-swapped duplicate slash to be rejected " +
			"(EncodeEvidence must canonicalise pair order)")
	}

	// Assertion 5: auto-revoke. The full-drain slash leaves the
	// offender with 0 stake — strictly below MinEnrollStakeDust
	// — so SlashApplier.RevokeIfUnderBonded must have moved the
	// record into the unbond window with the gpu_uuid binding
	// released for re-enrollment.
	rec, err := fx.state.Lookup(fx.enrolledID)
	if err != nil || rec == nil {
		t.Fatalf("post-slash record lookup: rec=%v err=%v", rec, err)
	}
	if rec.Active() {
		t.Error("post-slash record should not be Active() (auto-revoke expected)")
	}
	if rec.RevokedAtHeight != 250 {
		t.Errorf("RevokedAtHeight: got %d, want 250", rec.RevokedAtHeight)
	}
	if owner, _ := fx.state.GPUUUIDBound(fx.enrolledUUID); owner != "" {
		t.Errorf("gpu_uuid binding should be released after auto-revoke, got %q", owner)
	}
}

// TestSlashE2E_DoubleMining_SingleProof_Rejected asserts that
// submitting two BYTE-IDENTICAL proofs does not pass the
// equivocation check — there is no second proof to equivocate
// with. The encoder rejects the pair up front; the chain never
// even sees the slash payload.
func TestSlashE2E_DoubleMining_SingleProof_Rejected(t *testing.T) {
	t.Parallel()
	fx := buildDoubleMiningFixture(t, 0)
	p := buildEquivocatingProof(t, fx, 0xA)

	_, err := doublemining.EncodeEvidence(doublemining.Evidence{ProofA: p, ProofB: p})
	if err == nil {
		t.Fatalf("encode of identical pair should fail")
	}
}

// TestSlashE2E_DoubleMining_OneForged_Rejected asserts that a
// slasher who pairs an honest proof with a forged one cannot
// drain the offender's stake through this verifier. The chain
// rejects with ErrEvidenceVerification and the bond is intact.
// (The forged proof IS slashable, but only through
// forgedattest.Verifier.)
func TestSlashE2E_DoubleMining_OneForged_Rejected(t *testing.T) {
	t.Parallel()
	fx := buildDoubleMiningFixture(t, 0)

	pa := buildEquivocatingProof(t, fx, 0xA)
	pb := buildEquivocatingProof(t, fx, 0xB)

	// Corrupt ProofB's HMAC after signing.
	bundle, err := hmac.ParseBundle(pb.Attestation.BundleBase64)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tampered := []byte(bundle.HMAC)
	if tampered[len(tampered)-1] == '0' {
		tampered[len(tampered)-1] = '1'
	} else {
		tampered[len(tampered)-1] = '0'
	}
	bundle.HMAC = string(tampered)
	enc, err := bundle.MarshalBase64()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	pb.Attestation.BundleBase64 = enc

	evBlob, err := doublemining.EncodeEvidence(doublemining.Evidence{ProofA: pa, ProofB: pb})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	preStake := fx.lookupStake(t)
	slashTx := &mempool.Tx{
		Sender:     fx.slasherAddr,
		Nonce:      0,
		Fee:        0.01,
		ContractID: slashing.ContractID,
		Payload: mustEncodeSlashPayload(t, slashing.SlashPayload{
			NodeID:          fx.enrolledID,
			EvidenceKind:    slashing.EvidenceKindDoubleMining,
			EvidenceBlob:    evBlob,
			SlashAmountDust: doublemining.DefaultMaxSlashDust,
		}),
	}
	err = fx.slasher.ApplySlashTx(slashTx, 250)
	if err == nil {
		t.Fatalf("expected slash rejection on one-forged-proof pair")
	}
	if !errors.Is(err, slashing.ErrEvidenceVerification) {
		t.Fatalf("error %v does not wrap ErrEvidenceVerification", err)
	}
	if got := fx.lookupStake(t); got != preStake {
		t.Errorf("stake mutated on rejected slash: pre=%d post=%d", preStake, got)
	}
}

// ----- helpers -----------------------------------------------------

func (fx *dmFixture) lookupStake(t *testing.T) uint64 {
	t.Helper()
	rec, err := fx.state.Lookup(fx.enrolledID)
	if err != nil {
		t.Fatalf("state lookup: %v", err)
	}
	if rec == nil {
		return 0
	}
	return rec.StakeDust
}

func (fx *dmFixture) balanceOf(addr string) float64 {
	acc, ok := fx.accounts.Get(addr)
	if !ok {
		return 0
	}
	return acc.Balance
}
