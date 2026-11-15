package chain

import (
	"bytes"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

// The block-time sweep every node runs removes a deferred-bond enrollment that
// earned nothing in a week (rc1112), publishes it, counts it and credits
// nobody; below the height it does nothing.
func TestSweepMaturedEnrollmentsPrunesUnfundedDeferred(t *testing.T) {
	const h = 3_000_000
	prev := enrollment.DeferredPruneHeight()
	enrollment.SetDeferredPruneActivationHeight(h)
	t.Cleanup(func() { enrollment.SetDeferredPruneActivationHeight(prev) })

	owner := strings.Repeat("e", 64)
	state := enrollment.NewInMemoryState()
	for _, r := range []enrollment.EnrollmentRecord{
		{NodeID: "free", Owner: owner, GPUUUID: "GPU-free", HMACKey: bytes.Repeat([]byte{1}, 32),
			BondMode: enrollment.BondModeMiningRewards, RequiredStakeDust: mining.MinEnrollStakeDust, EnrolledAtHeight: 100},
		{NodeID: "earning", Owner: owner, GPUUUID: "GPU-earning", HMACKey: bytes.Repeat([]byte{2}, 32), StakeDust: 1,
			BondMode: enrollment.BondModeMiningRewards, RequiredStakeDust: mining.MinEnrollStakeDust, EnrolledAtHeight: 100},
	} {
		if err := state.ApplyEnroll(r); err != nil {
			t.Fatal(err)
		}
	}
	accounts := NewAccountStore()
	pub := &recordingPublisher{}
	a := NewEnrollmentApplier(accounts, state)
	a.Publisher = pub

	rootBefore, acctBefore := state.StateRoot(), accounts.StateRoot()
	if _, err := a.SweepMaturedEnrollments(h - 1); err != nil {
		t.Fatal(err)
	}
	if state.StateRoot() != rootBefore || len(pub.enroll) != 0 {
		t.Fatal("below the height the sweep changed the enrollments")
	}

	before := DeferredEnrollmentsPruned()
	if _, err := a.SweepMaturedEnrollments(h); err != nil {
		t.Fatal(err)
	}
	if rec, _ := state.Lookup("free"); rec != nil {
		t.Fatal("the unfunded deferred enrollment is still there")
	}
	if rec, _ := state.Lookup("earning"); rec == nil {
		t.Fatal("the earning enrollment was removed")
	}
	if DeferredEnrollmentsPruned() != before+1 {
		t.Fatal("the removal was not counted")
	}
	if len(pub.enroll) != 1 || pub.enroll[0].Kind != EnrollmentEventDeferredPruned || pub.enroll[0].NodeID != "free" ||
		pub.enroll[0].Owner != owner || pub.enroll[0].Height != h {
		t.Fatalf("events %+v", pub.enroll)
	}
	if accounts.StateRoot() != acctBefore {
		t.Fatal("the removal credited or debited an account")
	}
}
