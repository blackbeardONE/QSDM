package enrollment

import (
	"bytes"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mining"
)

func withDeferredPruneHeight(t *testing.T, h uint64) {
	t.Helper()
	prev := DeferredPruneHeight()
	SetDeferredPruneActivationHeight(h)
	t.Cleanup(func() { SetDeferredPruneActivationHeight(prev) })
}

func dpOwner(c string) string { return strings.Repeat(c, 64) }

func dpRecord(node, gpu, owner string, mode BondMode, stake, enrolledAt uint64) EnrollmentRecord {
	return EnrollmentRecord{NodeID: node, Owner: owner, GPUUUID: gpu, HMACKey: bytes.Repeat([]byte{0x5A}, 32),
		StakeDust: stake, BondMode: mode, RequiredStakeDust: mining.MinEnrollStakeDust, EnrolledAtHeight: enrolledAt}
}

func dpState(t *testing.T, recs ...EnrollmentRecord) *InMemoryState {
	t.Helper()
	s := NewInMemoryState()
	for _, r := range recs {
		if err := s.ApplyEnroll(r); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// The rule's boundaries: one block short of a week nothing goes; at a week
// exactly the unfunded deferred record goes; below the activation height
// nothing ever goes, however old.
func TestDeferredPruneBoundaries(t *testing.T) {
	const h = 1_000_000
	withDeferredPruneHeight(t, h)
	const enrolled = h + 10
	s := dpState(t, dpRecord("free-1", "GPU-free-1", dpOwner("a"), BondModeMiningRewards, 0, enrolled))

	if got := s.PruneUnfundedDeferred(enrolled + DeferredPruneAfterBlocks - 1); len(got) != 0 {
		t.Fatalf("one block short of a week: pruned %v", got)
	}
	got := s.PruneUnfundedDeferred(enrolled + DeferredPruneAfterBlocks)
	if len(got) != 1 || got[0].NodeID != "free-1" || got[0].GPUUUID != "GPU-free-1" || got[0].EnrolledAtHeight != enrolled {
		t.Fatalf("at a week: %+v", got)
	}
	if rec, _ := s.Lookup("free-1"); rec != nil {
		t.Fatal("the pruned record is still there")
	}
	if bound, _ := s.GPUUUIDBound("GPU-free-1"); bound != "" {
		t.Fatalf("the GPU is still bound to %q", bound)
	}

	old := dpState(t, dpRecord("free-old", "GPU-old", dpOwner("b"), BondModeMiningRewards, 0, 10))
	if got := old.PruneUnfundedDeferred(h - 1); len(got) != 0 {
		t.Fatalf("below the activation height: pruned %v", got)
	}
	if got := old.PruneUnfundedDeferred(h); len(got) != 1 {
		t.Fatalf("an old unfunded record goes at the activation height itself: %v", got)
	}

	withDeferredPruneHeight(t, 0)
	never := dpState(t, dpRecord("free-x", "GPU-x", dpOwner("c"), BondModeMiningRewards, 0, 10))
	if got := never.PruneUnfundedDeferred(1 << 60); len(got) != 0 || DeferredPruneActiveAt(1<<60) {
		t.Fatalf("with no activation height the rule never applies: %v", got)
	}
}

// Who is kept: a record that earned one dust, an upfront-bonded record (even
// with zero stake after a slash), and a revoked record (the unbond sweep's).
// Several unfunded records go in node_id order.
func TestDeferredPruneWhoIsKept(t *testing.T) {
	const h = 500_000
	withDeferredPruneHeight(t, h)
	s := dpState(t,
		dpRecord("z-free", "GPU-z", dpOwner("a"), BondModeMiningRewards, 0, 1),
		dpRecord("a-free", "GPU-a", dpOwner("a"), BondModeMiningRewards, 0, 1),
		dpRecord("earned", "GPU-e", dpOwner("b"), BondModeMiningRewards, 1, 1),
		dpRecord("bonded", "GPU-b", dpOwner("c"), BondModeUpfront, mining.MinEnrollStakeDust, 1),
		dpRecord("bonded-slashed", "GPU-bs", dpOwner("c"), "", 0, 1),
		dpRecord("revoked", "GPU-r", dpOwner("d"), BondModeMiningRewards, 0, 1),
	)
	if err := s.ApplyUnenroll("revoked", 2); err != nil {
		t.Fatal(err)
	}
	got := s.PruneUnfundedDeferred(h + DeferredPruneAfterBlocks)
	if len(got) != 2 || got[0].NodeID != "a-free" || got[1].NodeID != "z-free" {
		t.Fatalf("pruned %+v, want a-free and z-free in order", got)
	}
	for _, keep := range []string{"earned", "bonded", "bonded-slashed", "revoked"} {
		if rec, _ := s.Lookup(keep); rec == nil {
			t.Fatalf("%s was removed", keep)
		}
	}
	if got := s.PruneUnfundedDeferred(h + 10*DeferredPruneAfterBlocks); len(got) != 0 {
		t.Fatalf("a second pass removed %v", got)
	}
}

// After removal the GPU and the node_id can enroll again, and the new record
// is a fresh one with its own week.
func TestDeferredPruneReenroll(t *testing.T) {
	const h = 700_000
	withDeferredPruneHeight(t, h)
	s := dpState(t, dpRecord("node-1", "GPU-Same-1", dpOwner("a"), BondModeMiningRewards, 0, h))
	at := uint64(h + DeferredPruneAfterBlocks)
	if got := s.PruneUnfundedDeferred(at); len(got) != 1 {
		t.Fatalf("pruned %v", got)
	}
	if err := s.ApplyEnroll(dpRecord("node-1", "GPU-Same-1", dpOwner("b"), BondModeMiningRewards, 0, at)); err != nil {
		t.Fatalf("re-enrolling the same node_id and GPU: %v", err)
	}
	if bound, _ := s.GPUUUIDBoundFold("gpu-same-1"); bound != "node-1" {
		t.Fatalf("the re-enrolled GPU is bound to %q", bound)
	}
	if got := s.PruneUnfundedDeferred(at + DeferredPruneAfterBlocks - 1); len(got) != 0 {
		t.Fatalf("the new record has its own week: pruned %v", got)
	}
}

// Every node that applies the same history reaches the same state root, and
// below the height the state root never moves.
func TestDeferredPruneStateRootDeterministic(t *testing.T) {
	const h = 900_000
	withDeferredPruneHeight(t, h)
	mk := func() *InMemoryState {
		return dpState(t,
			dpRecord("n3", "GPU-3", dpOwner("a"), BondModeMiningRewards, 0, 1),
			dpRecord("n1", "GPU-1", dpOwner("b"), BondModeMiningRewards, 0, 1),
			dpRecord("n2", "GPU-2", dpOwner("c"), BondModeMiningRewards, 7, 1),
		)
	}
	a, b := mk(), mk()
	rootBelow := a.StateRoot()
	a.PruneUnfundedDeferred(h - 1)
	if a.StateRoot() != rootBelow {
		t.Fatal("the state root moved below the activation height")
	}
	a.PruneUnfundedDeferred(h)
	b.PruneUnfundedDeferred(h)
	if a.StateRoot() != b.StateRoot() || a.StateRoot() == rootBelow {
		t.Fatalf("roots: %s / %s (before %s)", a.StateRoot(), b.StateRoot(), rootBelow)
	}
	if a.Count() != 1 {
		t.Fatalf("count %d, want 1 (the earning record)", a.Count())
	}
}
