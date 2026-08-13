package chain

import (
	"errors"
	"fmt"
	"testing"
)

// registerVictim builds a validator set holding one staked validator, plus the
// evidence manager that gossip feeds.
func registerVictim(t *testing.T, stake float64) (*EvidenceManager, *ValidatorSet, string) {
	t.Helper()
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	const victim = "validator-victim"
	if err := vs.Register(victim, stake); err != nil {
		t.Fatalf("register victim: %v", err)
	}
	return NewEvidenceManager(vs), vs, victim
}

func stakeOf(t *testing.T, vs *ValidatorSet, addr string) float64 {
	t.Helper()
	v, ok := vs.GetValidator(addr)
	if !ok {
		t.Fatalf("validator %q missing", addr)
	}
	return v.Stake
}

// The core of the vulnerability: an unauthenticated peer could destroy a
// validator's stake with a non-empty string. Assert on the STAKE, not just on
// the returned error -- a rejection that still slashed would pass a
// error-only test.
func TestInvalidVoteEvidence_RejectedWithoutSlashing(t *testing.T) {
	em, vs, victim := registerVictim(t, 1000)
	before := stakeOf(t, vs, victim)

	rec, err := em.Process(ConsensusEvidence{
		Type:      EvidenceInvalidVote,
		Validator: victim,
		Details:   "x", // the entire previous validation was Details != ""
	})

	if !errors.Is(err, ErrEvidenceInvalidVoteUnprovable) {
		t.Fatalf("expected ErrEvidenceInvalidVoteUnprovable, got %v", err)
	}
	if after := stakeOf(t, vs, victim); after != before {
		t.Fatalf("stake was slashed by unproven evidence: %v -> %v", before, after)
	}
	v, _ := vs.GetValidator(victim)
	if v.Status == ValidatorJailed {
		t.Fatal("victim was jailed by unproven evidence")
	}
	if v.SlashCount != 0 {
		t.Fatalf("expected no slash events, got SlashCount=%d", v.SlashCount)
	}
	if rec != nil && rec.Processed {
		t.Fatal("record must not be marked processed")
	}
	if rec != nil && rec.SlashEvent != nil {
		t.Fatal("no slash event may be recorded")
	}
	if len(vs.SlashLog()) != 0 {
		t.Fatalf("slash log must stay empty, got %d entries", len(vs.SlashLog()))
	}
}

// evidenceID hashes Details, so varying one character produced a fresh ID that
// defeated dedupe. Repeated submissions therefore compounded 5% losses without
// bound. Nothing may be lost across many attempts.
func TestInvalidVoteEvidence_RepeatedAttemptsCannotCompound(t *testing.T) {
	em, vs, victim := registerVictim(t, 1000)
	before := stakeOf(t, vs, victim)

	for i := 0; i < 50; i++ {
		_, err := em.Process(ConsensusEvidence{
			Type:      EvidenceInvalidVote,
			Validator: victim,
			Height:    uint64(i),
			Details:   fmt.Sprintf("unique-%d", i), // fresh evidence ID each time
		})
		if !errors.Is(err, ErrEvidenceInvalidVoteUnprovable) {
			t.Fatalf("attempt %d: expected rejection, got %v", i, err)
		}
	}

	if after := stakeOf(t, vs, victim); after != before {
		t.Fatalf("repeated unproven accusations drained stake: %v -> %v", before, after)
	}
	if len(vs.SlashLog()) != 0 {
		t.Fatalf("slash log must stay empty, got %d entries", len(vs.SlashLog()))
	}
}

// Rejection must not depend on Details being empty, since that was the old
// (and only) gate.
func TestInvalidVoteEvidence_RejectedRegardlessOfFields(t *testing.T) {
	em, vs, victim := registerVictim(t, 1000)
	cases := []ConsensusEvidence{
		{Type: EvidenceInvalidVote, Validator: victim},
		{Type: EvidenceInvalidVote, Validator: victim, Details: "detailed accusation"},
		{Type: EvidenceInvalidVote, Validator: victim, Details: "d", Height: 42, Round: 7},
		{Type: EvidenceInvalidVote, Validator: victim, Details: "d", BlockHashes: []string{"a", "b"}},
	}
	for i, ev := range cases {
		if _, err := em.Process(ev); !errors.Is(err, ErrEvidenceInvalidVoteUnprovable) {
			t.Errorf("case %d: expected rejection, got %v", i, err)
		}
	}
	if len(vs.SlashLog()) != 0 {
		t.Fatalf("slash log must stay empty, got %d entries", len(vs.SlashLog()))
	}
}

// The genuinely provable offence must keep working, so this change removes an
// attack rather than the ability to report faults.
func TestEquivocationEvidence_StillSlashesWithConflictingHashes(t *testing.T) {
	em, vs, victim := registerVictim(t, 1000)
	before := stakeOf(t, vs, victim)

	if _, err := em.Process(ConsensusEvidence{
		Type:        EvidenceEquivocation,
		Validator:   victim,
		BlockHashes: []string{"hash-a", "hash-b"},
		Details:     "conflicting proposals",
	}); err != nil {
		t.Fatalf("equivocation evidence must still be accepted: %v", err)
	}
	if after := stakeOf(t, vs, victim); after >= before {
		t.Fatalf("equivocation should still slash: %v -> %v", before, after)
	}
}

// fork_witness is informational and must be unaffected.
func TestForkWitnessEvidence_StillAcceptedWithoutSlashing(t *testing.T) {
	em, vs, victim := registerVictim(t, 1000)
	before := stakeOf(t, vs, victim)

	if _, err := em.Process(ConsensusEvidence{
		Type:        EvidenceForkWitness,
		Validator:   victim,
		Details:     "observed fork",
		BlockHashes: []string{"hash-a", "hash-b"},
	}); err != nil {
		t.Fatalf("fork_witness must still be accepted: %v", err)
	}
	if after := stakeOf(t, vs, victim); after != before {
		t.Fatalf("fork_witness must not slash: %v -> %v", before, after)
	}
}

// An unrecognised type must still be refused, so rejection is specific rather
// than a side effect of a broken switch.
func TestUnknownEvidenceType_StillRejected(t *testing.T) {
	em, _, victim := registerVictim(t, 1000)
	_, err := em.Process(ConsensusEvidence{
		Type:      EvidenceType("totally-made-up"),
		Validator: victim,
		Details:   "d",
	})
	if err == nil {
		t.Fatal("unknown evidence type must be rejected")
	}
	if errors.Is(err, ErrEvidenceInvalidVoteUnprovable) {
		t.Fatal("unknown types must not report the invalid_vote sentinel")
	}
}
