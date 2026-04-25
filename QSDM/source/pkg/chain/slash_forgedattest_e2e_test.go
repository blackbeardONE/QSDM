package chain

// End-to-end test for the forged-attestation slash path:
//
//   1. Enroll a miner with a known HMAC key. The enrollment
//      bonds 10 CELL of stake.
//   2. An attacker (or a buggy validator) builds a v2 proof
//      whose HMAC is signed with the WRONG key — a forgery.
//   3. A slasher catches the forged proof, builds a
//      forgedattest.Evidence blob, and submits a slash tx.
//   4. The chain runs the full applier path:
//        - decode slash payload
//        - lookup enrollment record
//        - re-run hmac.Verifier (via forgedattest.Verifier)
//        - verifier rejects → offence proven
//        - SlashStake drains the bond
//        - slasher gets RewardBPS share, rest is burned
//        - evidence-fingerprint marked seen → replay locked
//
// This test is the consensus-critical integration check: every
// piece of the v2 slashing pipeline must work end-to-end against
// the actual forgedattest.Verifier (no test fakes), against
// state.MarkEvidenceSeen, against AccountStore, and against the
// shared enrollment.InMemoryState.

import (
	"encoding/hex"
	"errors"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing/forgedattest"
)

// ----- e2e helpers ------------------------------------------------

// forgedFixture spins up an end-to-end environment with a real
// forgedattest.Verifier wired against the on-chain enrollment
// state via enrollment.NewStateBackedRegistry. Tests can mutate
// the returned proof to introduce specific faults.
type forgedFixture struct {
	accounts    *AccountStore
	state       *enrollment.InMemoryState
	dispatcher  *slashing.Dispatcher
	slasher     *SlashApplier
	enrollKey   []byte
	enrolledID  string
	enrolledUUID string
	offender    string
	slasherAddr string
}

func buildForgedFixture(t *testing.T, rewardBPS uint16) *forgedFixture {
	t.Helper()

	accounts := NewAccountStore()
	accounts.Credit("offender-addr", 100.0) // miner under attack
	accounts.Credit("slasher-addr", 5.0)    // slasher posting the slash tx

	state := enrollment.NewInMemoryState()
	enrollAp := NewEnrollmentApplier(accounts, state)

	enrolledID := "rig-77"
	enrolledUUID := "GPU-01234567-89ab-cdef-0123-456789abcdef"
	enrollKey := make([]byte, 32)
	for i := range enrollKey {
		enrollKey[i] = 0xAB
	}

	stakeDust := uint64(10 * 100_000_000) // 10 CELL bond
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
		Sender:     "offender-addr",
		Nonce:      0,
		Fee:        0.01,
		ContractID: enrollment.ContractID,
		Payload:    raw,
	}
	if err := enrollAp.ApplyEnrollmentTx(enrollTx, 100); err != nil {
		t.Fatalf("apply enroll: %v", err)
	}

	// Wire the production slashing dispatcher against the
	// state-backed registry. This is the same code path the
	// validator binary will use.
	registry := enrollment.NewStateBackedRegistry(state)
	disp, err := forgedattest.NewProductionSlashingDispatcher(registry, nil, 0)
	if err != nil {
		t.Fatalf("NewProductionSlashingDispatcher: %v", err)
	}

	slasher := NewSlashApplier(accounts, state, disp, rewardBPS)

	return &forgedFixture{
		accounts:     accounts,
		state:        state,
		dispatcher:   disp,
		slasher:      slasher,
		enrollKey:    enrollKey,
		enrolledID:   enrolledID,
		enrolledUUID: enrolledUUID,
		offender:     "offender-addr",
		slasherAddr:  "slasher-addr",
	}
}

// buildForgedProof constructs a v2 mining proof whose HMAC is
// signed with the WRONG key — i.e. a forgery. The bundle's
// node_id matches the enrolled node_id (so the slash payload's
// NodeID binding holds), but the MAC will fail re-verification
// against the registered key.
func buildForgedProof(t *testing.T, fx *forgedFixture) mining.Proof {
	t.Helper()

	var nonce [32]byte
	for i := range nonce {
		nonce[i] = byte(i + 1)
	}
	var batchRoot, mix [32]byte
	for i := range batchRoot {
		batchRoot[i] = byte(0x10 + i)
		mix[i] = byte(0xE0 - i)
	}
	minerAddr := "qsdm1offender"
	issuedAt := int64(1_700_000_000)

	p := mining.Proof{
		Version:    mining.ProtocolVersionV2,
		Epoch:      1,
		Height:     200,
		HeaderHash: [32]byte{0xCC},
		BatchRoot:  batchRoot,
		BatchCount: 1,
		Nonce:      [16]byte{0x99},
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

	// Sign with the WRONG key — this is the forgery.
	rogueKey := make([]byte, 32)
	for i := range rogueKey {
		rogueKey[i] = 0x55 // distinct from enrollKey's 0xAB
	}
	signed, err := b.Sign(rogueKey)
	if err != nil {
		t.Fatalf("sign forged bundle: %v", err)
	}
	bundleB64, err := signed.MarshalBase64()
	if err != nil {
		t.Fatalf("marshal forged bundle: %v", err)
	}
	p.Attestation.BundleBase64 = bundleB64
	return p
}

// ----- the e2e tests ----------------------------------------------

// TestSlashE2E_ForgedHMAC_DrainsStakeAndRewardsSlasher is the
// full happy-path test for the forged-attestation slash flow.
// It asserts every consensus-relevant side effect in one place,
// rather than across half a dozen unit tests, because the value
// of an end-to-end test is exactly that "every piece works
// together."
func TestSlashE2E_ForgedHMAC_DrainsStakeAndRewardsSlasher(t *testing.T) {
	t.Parallel()
	const rewardBPS uint16 = 1000 // 10% reward
	fx := buildForgedFixture(t, rewardBPS)

	// Capture pre-state for delta assertions.
	preStake := fx.lookupStake(t)
	preSlasherBalance := fx.balanceOf(fx.slasherAddr)

	if preStake == 0 {
		t.Fatalf("fixture invariant: enrolled stake should be non-zero")
	}

	forged := buildForgedProof(t, fx)

	// The slasher knows it's an HMAC mismatch — they observed the
	// chain accept a proof that re-verifies as forged.
	ev := forgedattest.Evidence{
		Proof:      forged,
		FaultClass: forgedattest.FaultHMACMismatch,
		Memo:       "caught HMAC mismatch in mempool replay",
	}
	evBlob, err := forgedattest.EncodeEvidence(ev)
	if err != nil {
		t.Fatalf("encode evidence: %v", err)
	}

	// Slash up to the bonded stake. The applier will clamp at
	// the actually-bonded amount.
	slashTx := &mempool.Tx{
		Sender:     fx.slasherAddr,
		Nonce:      0,
		Fee:        0.01,
		ContractID: slashing.ContractID,
		Payload: mustEncodeSlashPayload(t, slashing.SlashPayload{
			NodeID:          fx.enrolledID,
			EvidenceKind:    slashing.EvidenceKindForgedAttestation,
			EvidenceBlob:    evBlob,
			SlashAmountDust: forgedattest.DefaultMaxSlashDust,
			Memo:            "e2e",
		}),
	}

	if err := fx.slasher.ApplySlashTx(slashTx, 250); err != nil {
		t.Fatalf("ApplySlashTx: %v", err)
	}

	// Assertion 1: stake drained on the offender's record.
	postStake := fx.lookupStake(t)
	if postStake != 0 {
		t.Errorf("stake post-slash = %d dust, want 0 (full drain)", postStake)
	}

	// Assertion 2: slasher rewarded with rewardBPS share.
	// rewardDust = preStake * rewardBPS / 10000
	// Net balance change = reward - fee (the fee was debited
	// up-front and burned regardless of outcome).
	expectRewardDust := preStake * uint64(rewardBPS) / 10000
	expectNetBalance := dustToBalance(expectRewardDust) - 0.01
	gotNetBalance := fx.balanceOf(fx.slasherAddr) - preSlasherBalance
	const tol = 1e-9
	if diff := gotNetBalance - expectNetBalance; diff > tol || diff < -tol {
		t.Errorf("slasher net balance change = %v CELL, want ~%v CELL (reward %d dust - fee 0.01)",
			gotNetBalance, expectNetBalance, expectRewardDust)
	}

	// Assertion 3: evidence is marked seen — a duplicate slash
	// must be rejected by the chain-side replay defence.
	dupTx := &mempool.Tx{
		Sender:     fx.slasherAddr,
		Nonce:      1,
		Fee:        0.01,
		ContractID: slashing.ContractID,
		Payload:    slashTx.Payload, // same evidence blob
	}
	dupErr := fx.slasher.ApplySlashTx(dupTx, 251)
	if dupErr == nil {
		t.Errorf("expected duplicate slash to be rejected")
	}

	// Assertion 4: the offender's record was auto-revoked into
	// the unbond window. Post-slash stake (0) is strictly below
	// MinEnrollStakeDust, so SlashApplier.RevokeIfUnderBonded
	// must have transitioned the record. Closes the
	// "slash-to-zero, keep mining for free" loophole.
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
	if rec.UnbondMaturesAtHeight == 0 {
		t.Error("UnbondMaturesAtHeight not set on auto-revoke")
	}
	if owner, _ := fx.state.GPUUUIDBound(fx.enrolledUUID); owner != "" {
		t.Errorf("gpu_uuid binding should be released after auto-revoke, got %q", owner)
	}
}

// TestSlashE2E_HonestProof_RejectedAsBogusEvidence asserts that
// a slasher who submits a *valid* proof as forged-attestation
// evidence has their slash tx rejected with
// ErrEvidenceVerification — the chain refuses to slash an
// honest miner. The slasher's fee + nonce are still consumed
// (matches the existing fee-burn model in pkg/chain).
func TestSlashE2E_HonestProof_RejectedAsBogusEvidence(t *testing.T) {
	t.Parallel()
	fx := buildForgedFixture(t, 0)

	// Build an HONEST proof: signed with the enrolled key.
	var nonce [32]byte
	for i := range nonce {
		nonce[i] = byte(i + 1)
	}
	var batchRoot, mix [32]byte
	for i := range batchRoot {
		batchRoot[i] = byte(0x10 + i)
		mix[i] = byte(0xE0 - i)
	}
	minerAddr := "qsdm1offender"
	issuedAt := int64(1_700_000_000)

	p := mining.Proof{
		Version:    mining.ProtocolVersionV2,
		Epoch:      1,
		Height:     200,
		HeaderHash: [32]byte{0xCC},
		BatchRoot:  batchRoot,
		BatchCount: 1,
		Nonce:      [16]byte{0x99},
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
	signed, err := b.Sign(fx.enrollKey) // enrolled key — honest
	if err != nil {
		t.Fatalf("sign honest bundle: %v", err)
	}
	bundleB64, err := signed.MarshalBase64()
	if err != nil {
		t.Fatalf("marshal honest bundle: %v", err)
	}
	p.Attestation.BundleBase64 = bundleB64

	evBlob, err := forgedattest.EncodeEvidence(forgedattest.Evidence{
		Proof:      p,
		FaultClass: forgedattest.FaultHMACMismatch,
	})
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
			EvidenceKind:    slashing.EvidenceKindForgedAttestation,
			EvidenceBlob:    evBlob,
			SlashAmountDust: 1_000_000_000,
		}),
	}
	err = fx.slasher.ApplySlashTx(slashTx, 250)
	if err == nil {
		t.Fatalf("expected slash rejection; honest proof must not be slashable")
	}
	if !errors.Is(err, slashing.ErrEvidenceVerification) {
		t.Fatalf("error %v does not wrap ErrEvidenceVerification", err)
	}

	// Stake must be untouched.
	if got := fx.lookupStake(t); got != preStake {
		t.Errorf("stake mutated on rejected slash: pre=%d post=%d", preStake, got)
	}
}

// ----- helpers -----------------------------------------------------

// lookupStake returns the offender's currently-bonded stake.
func (fx *forgedFixture) lookupStake(t *testing.T) uint64 {
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

// balanceOf returns the named account's CELL balance, or 0 if
// the account doesn't exist yet.
func (fx *forgedFixture) balanceOf(addr string) float64 {
	acc, ok := fx.accounts.Get(addr)
	if !ok {
		return 0
	}
	return acc.Balance
}

func mustEncodeSlashPayload(t *testing.T, p slashing.SlashPayload) []byte {
	t.Helper()
	raw, err := slashing.EncodeSlashPayload(p)
	if err != nil {
		t.Fatalf("encode slash payload: %v", err)
	}
	return raw
}

