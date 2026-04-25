package chain

import (
	"errors"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
)

func buildAwareWithSlash(t *testing.T, verifierCap uint64, rewardBPS uint16) (*EnrollmentAwareApplier, *enrollment.InMemoryState) {
	t.Helper()
	accounts := NewAccountStore()
	accounts.Credit("offender-addr", 100.0)
	accounts.Credit("slasher-addr", 10.0)

	state := enrollment.NewInMemoryState()
	enrollAp := NewEnrollmentApplier(accounts, state)

	disp := slashing.NewDispatcher()
	disp.Register(testAcceptVerifier{
		kind: slashing.EvidenceKindForgedAttestation,
		cap:  verifierCap,
	})
	slasher := NewSlashApplier(accounts, state, disp, rewardBPS)

	aware := NewEnrollmentAwareApplier(accounts, enrollAp)
	aware.SetHeightFn(func() uint64 { return 500 })
	aware.SetSlashApplier(slasher)

	// Seed an enrolled miner.
	payload := enrollment.EnrollPayload{
		Kind:      enrollment.PayloadKindEnroll,
		NodeID:    "rig-a",
		GPUUUID:   "GPU-AAA",
		HMACKey:   make([]byte, 32),
		StakeDust: 10 * 100_000_000,
	}
	for i := range payload.HMACKey {
		payload.HMACKey[i] = 0xCD
	}
	raw, err := enrollment.EncodeEnrollPayload(payload)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	enrollTx := &mempool.Tx{
		Sender:     "offender-addr",
		Nonce:      0,
		Fee:        0.01,
		ContractID: enrollment.ContractID,
		Payload:    raw,
	}
	if err := aware.ApplyTx(enrollTx); err != nil {
		t.Fatalf("apply enroll via aware: %v", err)
	}
	return aware, state
}

func TestEnrollmentAwareApplier_RoutesSlashTx(t *testing.T) {
	aware, state := buildAwareWithSlash(t, 5*100_000_000, 5000)

	slashPayload := slashing.SlashPayload{
		NodeID:          "rig-a",
		EvidenceKind:    slashing.EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte("proof-of-forgery"),
		SlashAmountDust: 10 * 100_000_000,
	}
	raw, err := slashing.EncodeSlashPayload(slashPayload)
	if err != nil {
		t.Fatalf("encode slash: %v", err)
	}
	slashTx := &mempool.Tx{
		Sender:     "slasher-addr",
		Nonce:      0,
		Fee:        0.002,
		ContractID: slashing.ContractID,
		Payload:    raw,
	}

	if err := aware.ApplyTx(slashTx); err != nil {
		t.Fatalf("aware.ApplyTx(slash): %v", err)
	}

	rec, _ := state.Lookup("rig-a")
	if rec.StakeDust != 5*100_000_000 {
		t.Errorf("offender stake after routed slash: got %d, want %d",
			rec.StakeDust, 5*100_000_000)
	}
	slasherAcc, _ := aware.Accounts().Get("slasher-addr")
	want := 10.0 - 0.002 + 2.5
	if absDiff(slasherAcc.Balance, want) > 1e-9 {
		t.Errorf("slasher balance via routing: got %.8f, want %.8f",
			slasherAcc.Balance, want)
	}
}

func TestEnrollmentAwareApplier_SlashingNotWiredRejects(t *testing.T) {
	accounts := NewAccountStore()
	state := enrollment.NewInMemoryState()
	enrollAp := NewEnrollmentApplier(accounts, state)
	aware := NewEnrollmentAwareApplier(accounts, enrollAp)
	aware.SetHeightFn(func() uint64 { return 1 })

	raw, err := slashing.EncodeSlashPayload(slashing.SlashPayload{
		NodeID:          "rig-a",
		EvidenceKind:    slashing.EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte("e"),
		SlashAmountDust: 1,
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	tx := &mempool.Tx{
		Sender:     "x",
		Nonce:      0,
		Fee:        0.001,
		ContractID: slashing.ContractID,
		Payload:    raw,
	}
	err = aware.ApplyTx(tx)
	if err == nil || !errors.Is(err, ErrSlashingNotWired) {
		t.Errorf("expected ErrSlashingNotWired, got %v", err)
	}
}

func TestEnrollmentAwareApplier_ChainReplay_PreservesSlasher(t *testing.T) {
	aware, liveState := buildAwareWithSlash(t, 5*100_000_000, 5000)

	clone, ok := aware.ChainReplayClone().(*EnrollmentAwareApplier)
	if !ok || clone == nil {
		t.Fatalf("clone wrong type: %T", clone)
	}
	if clone.slasher == nil {
		t.Fatal("clone did not preserve slasher")
	}
	if clone.slasher.Accounts == aware.slasher.Accounts {
		t.Error("clone slasher shares Accounts pointer with live — should be a deep copy")
	}
	if clone.slasher.Dispatcher != aware.slasher.Dispatcher {
		t.Error("clone slasher should share the Dispatcher (stateless)")
	}
	if clone.slasher.RewardBPS != aware.slasher.RewardBPS {
		t.Errorf("clone RewardBPS: got %d, want %d",
			clone.slasher.RewardBPS, aware.slasher.RewardBPS)
	}

	// Mutate the clone's slasher state — apply a slash on the clone.
	slashPayload := slashing.SlashPayload{
		NodeID:          "rig-a",
		EvidenceKind:    slashing.EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte("clone-evidence"),
		SlashAmountDust: 3 * 100_000_000,
	}
	raw, _ := slashing.EncodeSlashPayload(slashPayload)
	slashTx := &mempool.Tx{
		Sender:     "slasher-addr",
		Nonce:      0,
		Fee:        0.001,
		ContractID: slashing.ContractID,
		Payload:    raw,
	}
	if err := clone.ApplyTx(slashTx); err != nil {
		t.Fatalf("clone apply slash: %v", err)
	}

	// Live state must NOT have been touched by the clone's mutation.
	liveRec, _ := liveState.Lookup("rig-a")
	if liveRec.StakeDust != 10*100_000_000 {
		t.Errorf("live state leaked from clone mutation: offender stake=%d",
			liveRec.StakeDust)
	}
}
