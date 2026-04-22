package chain

import "testing"

func makeEvidenceManager(t *testing.T) (*EvidenceManager, *ValidatorSet) {
	t.Helper()
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	if err := vs.Register("v1", 500); err != nil {
		t.Fatal(err)
	}
	return NewEvidenceManager(vs), vs
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
	em, _ := makeEvidenceManager(t)
	ev := ConsensusEvidence{
		Type:        EvidenceEquivocation,
		Validator:   "v1",
		Height:      99,
		Round:       0,
		BlockHashes: []string{"a", "b"},
	}
	em.SubmitEvidenceBestEffort(ev)
	em.SubmitEvidenceBestEffort(ev)
	stats := em.Stats()
	if stats["total"] != 1 {
		t.Fatalf("expected one evidence record, got %+v", stats)
	}
}

func TestEvidenceManager_ProcessEquivocation(t *testing.T) {
	em, vs := makeEvidenceManager(t)
	rec, err := em.Process(ConsensusEvidence{
		Type:        EvidenceEquivocation,
		Validator:   "v1",
		Height:      10,
		Round:       1,
		BlockHashes: []string{"h1", "h2"},
	})
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	if !rec.Processed || rec.SlashEvent == nil {
		t.Fatal("expected processed with slash event")
	}
	v, _ := vs.GetValidator("v1")
	if v.SlashCount != 1 {
		t.Fatalf("expected slash count 1, got %d", v.SlashCount)
	}
}

func TestEvidenceManager_ProcessInvalidVote(t *testing.T) {
	em, _ := makeEvidenceManager(t)
	rec, err := em.Process(ConsensusEvidence{
		Type:      EvidenceInvalidVote,
		Validator: "v1",
		Height:    11,
		Round:     2,
		Details:   "signed malformed commit",
	})
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	if !rec.Processed {
		t.Fatal("expected processed")
	}
}

func TestEvidenceManager_DuplicateEvidence(t *testing.T) {
	em, _ := makeEvidenceManager(t)
	ev := ConsensusEvidence{
		Type:        EvidenceEquivocation,
		Validator:   "v1",
		Height:      10,
		Round:       1,
		BlockHashes: []string{"h1", "h2"},
	}
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
	_, err = em.Process(ConsensusEvidence{Type: EvidenceInvalidVote, Validator: "v1"})
	if err == nil {
		t.Fatal("expected missing details error")
	}
}

func TestEvidenceManager_StatsAndList(t *testing.T) {
	em, _ := makeEvidenceManager(t)
	_, _ = em.Process(ConsensusEvidence{
		Type:        EvidenceEquivocation,
		Validator:   "v1",
		Height:      1,
		Round:       0,
		BlockHashes: []string{"a", "b"},
	})
	_, _ = em.Process(ConsensusEvidence{
		Type:      EvidenceInvalidVote,
		Validator: "v1",
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
	em, vs := makeEvidenceManager(t)
	as := NewAccountStore()
	as.Credit("del", 1000)
	sl := NewStakingLedger()
	if err := sl.Delegate(as, "del", "v1", 200); err != nil {
		t.Fatal(err)
	}
	em.SetStakingLedger(sl)
	if sl.DelegatedPower("v1") != 200 {
		t.Fatalf("delegated: %v", sl.DelegatedPower("v1"))
	}
	_, err := em.Process(ConsensusEvidence{
		Type:        EvidenceEquivocation,
		Validator:   "v1",
		Height:      3,
		Round:       0,
		BlockHashes: []string{"a", "b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := sl.DelegatedPower("v1"); got > 191 || got < 189 {
		t.Fatalf("expected ~190 delegated after 5%% slash, got %v", got)
	}
	v, _ := vs.GetValidator("v1")
	if v.Status != ValidatorJailed {
		t.Fatalf("expected jailed validator, got %s", v.Status)
	}
}

