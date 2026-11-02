package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

func transitionEnrollmentFixture(t *testing.T) *enrollment.InMemoryState {
	t.Helper()
	state := enrollment.NewInMemoryState()
	if err := state.ApplyEnroll(enrollment.EnrollmentRecord{NodeID: "rig-transition-test", Owner: "alice", GPUUUID: "GPU-TRANSITION", HMACKey: bytes.Repeat([]byte{7}, 32), StakeDust: 100, EnrolledAtHeight: 1}); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestTransitionPersistedSuffixRestoresEnrollmentRoot(t *testing.T) {
	previous := chain.EnrollmentStateRootActivationHeight()
	chain.SetEnrollmentStateRootActivationHeight(625000)
	t.Cleanup(func() { chain.SetEnrollmentStateRootActivationHeight(previous) })
	accounts := chain.NewAccountStore()
	accounts.Credit("alice", 100)
	state := transitionEnrollmentFixture(t)
	p := &producerpolicy.Transition{EffectiveHeight: 659977}
	aware := chain.NewEnrollmentAwareApplier(accounts, chain.NewEnrollmentApplier(accounts, state))
	aware.SetStateRootHeight(659977)
	suffix := &chain.Block{Height: 659977, StateRoot: aware.StateRoot()}
	legacy, err := evaluatePersistedState(accounts, []*chain.Block{suffix})
	if err != nil {
		t.Fatal(err)
	}
	if legacy.stateRoot == suffix.StateRoot {
		t.Fatal("regression fixture did not expose omitted enrollment root")
	}
	path := filepath.Join(t.TempDir(), "enrollment.json")
	if err := state.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded := enrollment.NewInMemoryState()
	if count, err := loadTransitionEnrollmentSnapshot(loaded, path); err != nil || count != 1 {
		t.Fatalf("load count=%d error=%v", count, err)
	}
	restored, err := evaluateTransitionPersistedState(accounts, []*chain.Block{suffix}, loaded, p)
	if err != nil {
		t.Fatal(err)
	}
	if restored.stateRoot != suffix.StateRoot {
		t.Fatalf("suffix root changed on restart: got %s want %s", restored.stateRoot, suffix.StateRoot)
	}
	if _, err := loaded.SlashStake("rig-transition-test", 1); err != nil {
		t.Fatal(err)
	}
	tampered, err := evaluateTransitionPersistedState(accounts, []*chain.Block{suffix}, loaded, p)
	if err != nil {
		t.Fatal(err)
	}
	if tampered.stateRoot == suffix.StateRoot {
		t.Fatal("changed enrollment snapshot matched suffix root")
	}
	checkpoint := &chain.Block{Height: 659976, StateRoot: accounts.StateRoot()}
	old, err := evaluateTransitionPersistedState(accounts, []*chain.Block{checkpoint}, state, p)
	if err != nil {
		t.Fatal(err)
	}
	if old.stateRoot != checkpoint.StateRoot {
		t.Fatal("immutable checkpoint root was rewritten")
	}
}

func TestTransitionEnrollmentSnapshotRefusesAutomaticRepair(t *testing.T) {
	path := filepath.Join(t.TempDir(), "enrollment.json")
	state := transitionEnrollmentFixture(t)
	if err := state.Save(path); err != nil {
		t.Fatal(err)
	}
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".last-good", good, 0600); err != nil {
		t.Fatal(err)
	}
	corrupt := []byte("{broken")
	if err := os.WriteFile(path, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTransitionEnrollmentSnapshot(enrollment.NewInMemoryState(), path); err == nil || !strings.Contains(err.Error(), "automatic last-good recovery is disabled") {
		t.Fatalf("expected explicit recovery error, got %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, corrupt) {
		t.Fatal("corrupt primary was silently replaced")
	}
	if _, err := loadTransitionEnrollmentSnapshot(enrollment.NewInMemoryState(), path+".missing"); err == nil {
		t.Fatal("missing enrollment snapshot accepted")
	}
	for _, invalid := range []string{"null", "true", "{}", `{"records":"not-an-array"}`} {
		if err := os.WriteFile(path, []byte(invalid), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadTransitionEnrollmentSnapshot(enrollment.NewInMemoryState(), path); err == nil {
			t.Fatalf("invalid snapshot accepted: %s", invalid)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != invalid {
			t.Fatal("invalid schema was silently replaced")
		}
	}
}
