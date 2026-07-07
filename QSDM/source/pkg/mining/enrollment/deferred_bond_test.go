package enrollment

import (
	"bytes"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mining"
)

func deferredBondTestPayload(t *testing.T) EnrollPayload {
	t.Helper()
	p := EnrollPayload{
		Kind:      PayloadKindEnroll,
		NodeID:    "deferred-rig-01",
		GPUUUID:   "GPU-deferred-0001",
		HMACKey:   bytes.Repeat([]byte{0x42}, MinHMACKeyLen),
		BondMode:  BondModeMiningRewards,
		StakeDust: 0,
	}
	nonce, _, err := FindDeferredBondWork(p)
	if err != nil {
		t.Fatalf("FindDeferredBondWork: %v", err)
	}
	p.WorkNonce = nonce
	return p
}

func TestValidateEnrollFieldsDeferredBond(t *testing.T) {
	p := deferredBondTestPayload(t)
	if err := ValidateEnrollFields(p, "owner"); err != nil {
		t.Fatalf("valid deferred enrollment rejected: %v", err)
	}
	if err := ValidateEnrollAgainstState(p, 0, NewInMemoryState()); err != nil {
		t.Fatalf("zero-balance deferred enrollment rejected: %v", err)
	}

	for {
		p.WorkNonce++
		if ValidateDeferredBondWork(p) != nil {
			break
		}
	}
	if err := ValidateEnrollFields(p, "owner"); err == nil {
		t.Fatal("invalid deferred enrollment work was accepted")
	}
}

func TestAccrueBondFromRewardDeterministic(t *testing.T) {
	state := NewInMemoryState()
	for _, rec := range []EnrollmentRecord{
		{
			NodeID: "rig-b", Owner: "owner", GPUUUID: "GPU-b",
			BondMode: BondModeMiningRewards, RequiredStakeDust: mining.MinEnrollStakeDust,
		},
		{
			NodeID: "rig-a", Owner: "owner", GPUUUID: "GPU-a",
			BondMode: BondModeMiningRewards, RequiredStakeDust: mining.MinEnrollStakeDust,
		},
	} {
		if err := state.ApplyEnroll(rec); err != nil {
			t.Fatalf("ApplyEnroll(%s): %v", rec.NodeID, err)
		}
	}

	locked := state.AccrueBondFromReward("owner", mining.MinEnrollStakeDust+123)
	if locked != mining.MinEnrollStakeDust+123 {
		t.Fatalf("locked=%d, want %d", locked, mining.MinEnrollStakeDust+123)
	}
	a, _ := state.Lookup("rig-a")
	b, _ := state.Lookup("rig-b")
	if a.StakeDust != mining.MinEnrollStakeDust || b.StakeDust != 123 {
		t.Fatalf("lexical allocation mismatch: rig-a=%d rig-b=%d", a.StakeDust, b.StakeDust)
	}
}
