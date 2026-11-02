package chain

import (
	"errors"
	"testing"
)

func makeEvidenceManager(t *testing.T) (*EvidenceManager, *ValidatorSet) {
	t.Helper()
	return makeEvidenceManagerForValidator(t, "v1")
}

func makeEvidenceManagerForValidator(t *testing.T, validator string) (*EvidenceManager, *ValidatorSet) {
	t.Helper()
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	if err := vs.Register(validator, 500); err != nil {
		t.Fatal(err)
	}
	return NewEvidenceManager(vs), vs
}

func makeProofEvidence(t *testing.T, height uint64, round uint32) (ConsensusEvidence, string) {
	t.Helper()
	signer, offender := newBFTKey(t)
	ev, err := BuildEquivocationEvidence(
		offender,
		signedExhibit(t, signer, offender, BFTWirePrevote, height, round, "a"),
		signedExhibit(t, signer, offender, BFTWirePrevote, height, round, "b"),
	)
	if err != nil {
		t.Fatalf("build proof evidence: %v", err)
	}
	return ev, offender
}

func TestEvidenceManager_ForkWitnessRecordedWithoutSlash(t *testing.T) {
	em, vs := makeEvidenceManager(t)
	rec, err := em.Process(ConsensusEvidence{
		Type:        EvidenceForkWitness,
		Height:      3,
		Round:       0,
		BlockHashes: []string{"hash-a", "hash-b"},
		Details:     "TryAppendExternalBlock conflict smoke",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Processed || rec.SlashEvent != nil {
		t.Fatalf("expected processed record without slash, got %#v", rec)
	}
	v, _ := vs.GetValidator("v1")
	if v.SlashCount != 0 {
		t.Fatalf("fork witness must not slash, slashCount=%d", v.SlashCount)
	}
}

func TestEvidenceManager_SubmitEvidenceBestEffortDuplicateIgnored(t *testing.T) {
	ev, offender := makeProofEvidence(t, 99, 0)
	em, _ := makeEvidenceManagerForValidator(t, offender)
	em.SubmitEvidenceBestEffort(ev)
	em.SubmitEvidenceBestEffort(ev)
	stats := em.Stats()
	if stats["total"] != 1 {
		t.Fatalf("expected one evidence record, got %+v", stats)
	}
}

func TestEvidenceManager_ProcessEquivocation(t *testing.T) {
	ev, offender := makeProofEvidence(t, 10, 1)
	em, vs := makeEvidenceManagerForValidator(t, offender)
	rec, err := em.Process(ev)
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	if !rec.Processed || rec.SlashEvent == nil {
		t.Fatal("expected processed with slash event")
	}
	v, _ := vs.GetValidator(offender)
	if v.SlashCount != 1 {
		t.Fatalf("expected slash count 1, got %d", v.SlashCount)
	}
}

// This test previously asserted the OPPOSITE -- that a bare invalid_vote
// accusation is accepted and processed -- which encoded the vulnerability as
// expected behaviour. Any peer could then slash an arbitrary validator by
// gossiping a non-empty Details string. The expectation is inverted here so
// the old behaviour cannot be reintroduced as a "fix" for a failing test.
// Stake-level consequences are covered in evidence_invalid_vote_test.go.
func TestEvidenceManager_ProcessInvalidVote(t *testing.T) {
	em, _ := makeEvidenceManager(t)
	rec, err := em.Process(ConsensusEvidence{
		Type:      EvidenceInvalidVote,
		Validator: "v1",
		Height:    11,
		Round:     2,
		Details:   "signed malformed commit",
	})
	if !errors.Is(err, ErrEvidenceInvalidVoteUnprovable) {
		t.Fatalf("expected ErrEvidenceInvalidVoteUnprovable, got %v", err)
	}
	if rec != nil && rec.Processed {
		t.Fatal("unprovable evidence must not be marked processed")
	}
}

func TestEvidenceManager_DuplicateEvidence(t *testing.T) {
	ev, offender := makeProofEvidence(t, 10, 1)
	em, _ := makeEvidenceManagerForValidator(t, offender)
	if _, err := em.Process(ev); err != nil {
		t.Fatal(err)
	}
	if _, err := em.Process(ev); err == nil {
		t.Fatal("expected duplicate evidence error")
	}
}

func TestEvidenceManager_ValidateErrors(t *testing.T) {
	em, _ := makeEvidenceManager(t)
	_, err := em.Process(ConsensusEvidence{Type: EvidenceEquivocation, Validator: "v1", BlockHashes: []string{"h1"}})
	if err == nil {
		t.Fatal("expected insufficient hash error")
	}
	// Not "missing details" any more: invalid_vote is refused outright, so
	// this asserts the sentinel rather than passing for a stale reason.
	_, err = em.Process(ConsensusEvidence{Type: EvidenceInvalidVote, Validator: "v1"})
	if !errors.Is(err, ErrEvidenceInvalidVoteUnprovable) {
		t.Fatalf("expected ErrEvidenceInvalidVoteUnprovable, got %v", err)
	}
}

func TestEvidenceManager_StatsAndList(t *testing.T) {
	ev, offender := makeProofEvidence(t, 1, 0)
	em, _ := makeEvidenceManagerForValidator(t, offender)
	_, _ = em.Process(ev)
	_, _ = em.Process(ConsensusEvidence{
		Type:      EvidenceInvalidVote,
		Validator: offender,
		Height:    2,
		Round:     0,
		Details:   "bad signature",
	})
	stats := em.Stats()
	if stats["total"] != 2 {
		t.Fatalf("expected total=2, got %d", stats["total"])
	}
	if len(em.List()) != 2 {
		t.Fatal("expected 2 records in list")
	}
}

func TestEvidenceManager_SlashesStakingDelegation(t *testing.T) {
	ev, offender := makeProofEvidence(t, 3, 0)
	em, vs := makeEvidenceManagerForValidator(t, offender)
	as := NewAccountStore()
	as.Credit("del", 1000)
	sl := NewStakingLedger()
	if err := sl.Delegate(as, "del", offender, 200); err != nil {
		t.Fatal(err)
	}
	em.SetStakingLedger(sl)
	if sl.DelegatedPower(offender) != 200 {
		t.Fatalf("delegated: %v", sl.DelegatedPower(offender))
	}
	_, err := em.Process(ev)
	if err != nil {
		t.Fatal(err)
	}
	if got := sl.DelegatedPower(offender); got > 191 || got < 189 {
		t.Fatalf("expected ~190 delegated after 5%% slash, got %v", got)
	}
	v, _ := vs.GetValidator(offender)
	if v.Status != ValidatorJailed {
		t.Fatalf("expected jailed validator, got %s", v.Status)
	}
}
