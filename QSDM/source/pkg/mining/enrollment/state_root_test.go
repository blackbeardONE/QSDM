package enrollment

import (
	"bytes"
	"crypto/sha256"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mining"
)

func TestInMemoryStateRootEmptyIsLegacyNeutral(t *testing.T) {
	var nilState *InMemoryState
	if got := nilState.Count(); got != 0 {
		t.Fatalf("nil state Count = %d, want 0", got)
	}
	if got := nilState.StateRoot(); got != "" {
		t.Fatalf("nil state root = %q, want empty", got)
	}

	state := NewInMemoryState()
	if got := state.Count(); got != 0 {
		t.Fatalf("empty state Count = %d, want 0", got)
	}
	if got := state.StateRoot(); got != "" {
		t.Fatalf("empty state root = %q, want empty", got)
	}
}

func TestInMemoryStateRootTracksRecordsEvidenceAndStake(t *testing.T) {
	state := NewInMemoryState()
	if err := state.ApplyEnroll(rootTestRecord("node-b", "GPU-B", mining.MinEnrollStakeDust)); err != nil {
		t.Fatalf("apply enroll: %v", err)
	}

	root1 := state.StateRoot()
	if root1 == "" {
		t.Fatal("non-empty enrollment state should have a root")
	}
	if got := state.Count(); got != 1 {
		t.Fatalf("Count after record = %d, want 1", got)
	}

	clone := state.Clone().(*InMemoryState)
	if got := clone.StateRoot(); got != root1 {
		t.Fatalf("clone root = %q, want %q", got, root1)
	}

	evidenceHash := sha256.Sum256([]byte("bad-bundle-evidence"))
	if ok := state.MarkEvidenceSeen(evidenceHash); !ok {
		t.Fatal("expected evidence hash to be new")
	}
	root2 := state.StateRoot()
	if root2 == root1 {
		t.Fatal("state root did not change after recording slash evidence")
	}
	if got := state.Count(); got != 2 {
		t.Fatalf("Count after record+evidence = %d, want 2", got)
	}

	if _, err := state.SlashStake("node-b", 1); err != nil {
		t.Fatalf("slash stake: %v", err)
	}
	root3 := state.StateRoot()
	if root3 == root2 {
		t.Fatal("state root did not change after stake mutation")
	}
	if got := clone.StateRoot(); got != root1 {
		t.Fatalf("snapshot root changed after live mutation: got %q, want %q", got, root1)
	}
}

func TestInMemoryStateRootIsOrderIndependent(t *testing.T) {
	a := NewInMemoryState()
	b := NewInMemoryState()
	for _, rec := range []EnrollmentRecord{
		rootTestRecord("node-b", "GPU-B", mining.MinEnrollStakeDust),
		rootTestRecord("node-a", "GPU-A", mining.MinEnrollStakeDust+1),
	} {
		if err := a.ApplyEnroll(rec); err != nil {
			t.Fatalf("apply enroll a: %v", err)
		}
	}
	for _, rec := range []EnrollmentRecord{
		rootTestRecord("node-a", "GPU-A", mining.MinEnrollStakeDust+1),
		rootTestRecord("node-b", "GPU-B", mining.MinEnrollStakeDust),
	} {
		if err := b.ApplyEnroll(rec); err != nil {
			t.Fatalf("apply enroll b: %v", err)
		}
	}

	h1 := sha256.Sum256([]byte("first"))
	h2 := sha256.Sum256([]byte("second"))
	a.MarkEvidenceSeen(h2)
	a.MarkEvidenceSeen(h1)
	b.MarkEvidenceSeen(h1)
	b.MarkEvidenceSeen(h2)

	if got, want := a.StateRoot(), b.StateRoot(); got != want {
		t.Fatalf("roots differ by insertion order:\n a=%s\n b=%s", got, want)
	}
}

func rootTestRecord(nodeID, gpuUUID string, stakeDust uint64) EnrollmentRecord {
	return EnrollmentRecord{
		NodeID:            nodeID,
		Owner:             "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		OperatorPublicKey: "operator-" + nodeID,
		GPUUUID:           gpuUUID,
		HMACKey:           bytes.Repeat([]byte{0x42}, MinHMACKeyLen),
		StakeDust:         stakeDust,
		BondMode:          BondModeUpfront,
		RequiredStakeDust: mining.MinEnrollStakeDust,
		EnrolledAtHeight:  42,
		Memo:              "root-test",
	}
}
