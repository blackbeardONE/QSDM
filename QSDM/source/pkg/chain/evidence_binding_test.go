package chain

import (
	"strings"
	"testing"
)

// VerifyBinding must be tested DIRECTLY, not through a deduplicating caller.
//
// A prior round of this work claimed binding and the proof-keyed evidenceID
// were "independently load-bearing", having neutered each and watched
// different cases fail. That was true when measured, and stopped being true
// the moment evidenceID changed: once a proven offence is keyed on
// type|validator|fingerprint, every envelope mutation collapses onto the
// original submission's ID and is rejected by plain dedupe before binding is
// ever consulted. Neutering VerifyBinding then broke nothing, so nothing was
// testing it -- the experiment was simply never re-run after the change it
// was measuring.
//
// These tests call the validators directly with a fresh envelope, so dedupe
// cannot stand in for the check under test.
func TestVerifyBinding_RejectsEnvelopeThatOverstatesTheProof(t *testing.T) {
	signer, offender := newBFTKey(t)
	honest, err := BuildEquivocationEvidence(
		offender,
		signedExhibit(t, signer, offender, BFTWirePrevote, 9, 1, "value-a"),
		signedExhibit(t, signer, offender, BFTWirePrevote, 9, 1, "value-b"),
	)
	if err != nil {
		t.Fatalf("building proof-carrying evidence: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*ConsensusEvidence)
	}{
		{"height the votes do not cover", func(e *ConsensusEvidence) { e.Height = 10 }},
		{"round the votes do not cover", func(e *ConsensusEvidence) { e.Round = 2 }},
		{"hashes the accused never signed", func(e *ConsensusEvidence) {
			e.BlockHashes = []string{"invented-a", "invented-b"}
		}},
		{"one real hash, one invented", func(e *ConsensusEvidence) {
			e.BlockHashes = []string{"value-a", "invented-b"}
		}},
		{"a superset of the signed hashes", func(e *ConsensusEvidence) {
			e.BlockHashes = []string{"value-a", "value-b", "value-c"}
		}},
		{"fewer hashes than the proof shows", func(e *ConsensusEvidence) {
			e.BlockHashes = []string{"value-a", "value-a"}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := honest
			ev.BlockHashes = append([]string(nil), honest.BlockHashes...)
			tc.mutate(&ev)

			// The proof itself is genuine and still verifies -- that is the
			// point. Only the envelope lies.
			if err := ev.Proof.Verify(ev.Validator); err != nil {
				t.Fatalf("the proof should still be valid on its own: %v", err)
			}
			if err := ev.Proof.VerifyBinding(ev); err == nil {
				t.Error("VerifyBinding accepted an envelope the proof does not support")
			}
			// Both consumers must apply it, so neither can be relied on to
			// cover for the other.
			if err := validateEvidence(ev); err == nil {
				t.Error("validateEvidence accepted an envelope the proof does not support")
			}
			if err := ValidateUntrustedEvidence(ev); err == nil {
				t.Error("ValidateUntrustedEvidence accepted an envelope the proof does not support")
			}
		})
	}

	// The honest envelope must still pass all three, or the checks above are
	// satisfied by rejecting everything.
	if err := honest.Proof.VerifyBinding(honest); err != nil {
		t.Errorf("the honest envelope must bind: %v", err)
	}
	if err := validateEvidence(honest); err != nil {
		t.Errorf("the honest envelope must validate: %v", err)
	}
	if err := ValidateUntrustedEvidence(honest); err != nil {
		t.Errorf("the honest envelope must pass the untrusted gate: %v", err)
	}
}

// Which exhibit is VoteA is not part of the offence. Verify and VerifyBinding
// are both symmetric in A/B, so if the fingerprint were not canonicalised an
// attacker could resubmit one genuine proof with the exhibits swapped, mint a
// second evidenceID, and slash again -- and two honest reporters who saw the
// same equivocation in opposite orders would disagree on its identity.
func TestEvidenceID_ExhibitOrderIsNotPartOfTheIdentity(t *testing.T) {
	signer, offender := newBFTKey(t)
	a := signedExhibit(t, signer, offender, BFTWirePrevote, 9, 1, "value-a")
	b := signedExhibit(t, signer, offender, BFTWirePrevote, 9, 1, "value-b")

	forward, err := BuildEquivocationEvidence(offender, a, b)
	if err != nil {
		t.Fatalf("forward order: %v", err)
	}
	reversed, err := BuildEquivocationEvidence(offender, b, a)
	if err != nil {
		t.Fatalf("reversed order: %v", err)
	}

	if got, want := evidenceID(reversed), evidenceID(forward); got != want {
		t.Errorf("swapping the exhibits changed the evidence identity:\n  forward  %s\n  reversed %s", want, got)
	}
	if forward.Proof.fingerprint() != reversed.Proof.fingerprint() {
		t.Error("swapping the exhibits changed the proof fingerprint")
	}

	// Guard the other direction: a genuinely different offence must NOT
	// collapse onto the same identity.
	other, err := BuildEquivocationEvidence(
		offender,
		signedExhibit(t, signer, offender, BFTWirePrevote, 11, 0, "other-a"),
		signedExhibit(t, signer, offender, BFTWirePrevote, 11, 0, "other-b"),
	)
	if err != nil {
		t.Fatalf("second offence: %v", err)
	}
	if evidenceID(other) == evidenceID(forward) {
		t.Error("two distinct equivocations collapsed onto one evidence ID")
	}
}

// The swapped proof must be refused end to end, not merely deduped by hash
// equality in isolation.
func TestEvidenceManager_SwappedExhibitsCannotSlashTwice(t *testing.T) {
	signer, offender := newBFTKey(t)
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	if err := vs.Register(offender, 1000); err != nil {
		t.Fatalf("register: %v", err)
	}
	em := NewEvidenceManager(vs)

	a := signedExhibit(t, signer, offender, BFTWirePrevote, 9, 1, "value-a")
	b := signedExhibit(t, signer, offender, BFTWirePrevote, 9, 1, "value-b")
	forward, err := BuildEquivocationEvidence(offender, a, b)
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	reversed, err := BuildEquivocationEvidence(offender, b, a)
	if err != nil {
		t.Fatalf("reversed: %v", err)
	}

	if _, err := em.Process(forward); err != nil {
		t.Fatalf("the genuine report must be accepted: %v", err)
	}
	v, ok := vs.GetValidator(offender)
	if !ok {
		t.Fatal("offender vanished")
	}
	afterFirst := *v

	_, err = em.Process(reversed)
	if err == nil {
		t.Error("the same offence with exhibits swapped must not be processed again")
	} else if !strings.Contains(strings.ToLower(err.Error()), "duplicate") {
		t.Logf("refused for a non-duplicate reason, which is acceptable: %v", err)
	}

	after, ok := vs.GetValidator(offender)
	if !ok {
		t.Fatal("offender vanished")
	}
	if after.Stake != afterFirst.Stake {
		t.Errorf("swapped resubmission took more stake: %v -> %v", afterFirst.Stake, after.Stake)
	}
	if after.SlashCount != afterFirst.SlashCount {
		t.Errorf("swapped resubmission slashed again: %d -> %d", afterFirst.SlashCount, after.SlashCount)
	}
}

// Signing is randomized (FIPS 204 6.1 randomized variant, see
// pkg/crypto/dilithium_circl.go), so one vote signed twice yields two
// different, both-valid signatures. If evidence identity depended on those
// bytes, the same offence re-signed would look like a new offence and slash
// again -- 1000 -> 950 -> 902.5. Identity must follow what the votes say.
func TestEvidenceID_ResigningTheSameOffenceIsNotANewOffence(t *testing.T) {
	signer, offender := newBFTKey(t)

	build := func() ConsensusEvidence {
		t.Helper()
		ev, err := BuildEquivocationEvidence(
			offender,
			signedExhibit(t, signer, offender, BFTWirePrevote, 9, 1, "value-a"),
			signedExhibit(t, signer, offender, BFTWirePrevote, 9, 1, "value-b"),
		)
		if err != nil {
			t.Fatalf("building evidence: %v", err)
		}
		return ev
	}

	first, second := build(), build()

	// Guard the premise: if signing were deterministic this test would pass
	// for the wrong reason, and would keep passing if identity regressed to
	// keying on signature bytes.
	if equalAuth(first.Proof.VoteA.Auth, second.Proof.VoteA.Auth) {
		t.Skip("signing is deterministic in this build; the re-signing case cannot arise")
	}

	if first.Proof.fingerprint() != second.Proof.fingerprint() {
		t.Error("re-signing the same votes changed the proof fingerprint")
	}
	if evidenceID(first) != evidenceID(second) {
		t.Errorf("re-signing the same offence changed its identity: first %s, second %s",
			evidenceID(first), evidenceID(second))
	}

	// And end to end: the second submission must not take more stake.
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	if err := vs.Register(offender, 1000); err != nil {
		t.Fatalf("register: %v", err)
	}
	em := NewEvidenceManager(vs)
	if _, err := em.Process(first); err != nil {
		t.Fatalf("the genuine report must be accepted: %v", err)
	}
	v, ok := vs.GetValidator(offender)
	if !ok {
		t.Fatal("offender vanished")
	}
	afterFirst := *v

	if _, err := em.Process(second); err == nil {
		t.Error("the same offence, re-signed, must not be processed again")
	}
	after, ok := vs.GetValidator(offender)
	if !ok {
		t.Fatal("offender vanished")
	}
	if after.Stake != afterFirst.Stake {
		t.Errorf("re-signed resubmission took more stake: %v -> %v", afterFirst.Stake, after.Stake)
	}
	if after.SlashCount != afterFirst.SlashCount {
		t.Errorf("re-signed resubmission slashed again: %d -> %d", afterFirst.SlashCount, after.SlashCount)
	}
}

// Identity must still separate genuinely different offences, or the fix above
// would suppress real slashes instead of duplicate ones.
func TestEvidenceID_DistinctOffencesStayDistinct(t *testing.T) {
	signer, offender := newBFTKey(t)
	base, err := BuildEquivocationEvidence(offender,
		signedExhibit(t, signer, offender, BFTWirePrevote, 9, 1, "value-a"),
		signedExhibit(t, signer, offender, BFTWirePrevote, 9, 1, "value-b"))
	if err != nil {
		t.Fatal(err)
	}

	others := map[string]ConsensusEvidence{}
	mk := func(name string, kind string, h uint64, r uint32, va, vb string) {
		t.Helper()
		ev, err := BuildEquivocationEvidence(offender,
			signedExhibit(t, signer, offender, kind, h, r, va),
			signedExhibit(t, signer, offender, kind, h, r, vb))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		others[name] = ev
	}
	mk("different height", BFTWirePrevote, 10, 1, "value-a", "value-b")
	mk("different round", BFTWirePrevote, 9, 2, "value-a", "value-b")
	mk("different values", BFTWirePrevote, 9, 1, "value-c", "value-d")
	mk("one shared value", BFTWirePrevote, 9, 1, "value-a", "value-c")
	mk("different vote kind", BFTWirePrecommit, 9, 1, "value-a", "value-b")

	baseID := evidenceID(base)
	for name, ev := range others {
		if evidenceID(ev) == baseID {
			t.Errorf("%s collapsed onto the base offence's identity", name)
		}
	}

	// A second offender signing the identical votes is a different offence.
	otherSigner, otherOffender := newBFTKey(t)
	byOther, err := BuildEquivocationEvidence(otherOffender,
		signedExhibit(t, otherSigner, otherOffender, BFTWirePrevote, 9, 1, "value-a"),
		signedExhibit(t, otherSigner, otherOffender, BFTWirePrevote, 9, 1, "value-b"))
	if err != nil {
		t.Fatal(err)
	}
	if evidenceID(byOther) == baseID {
		t.Error("two different validators' equivocations collapsed onto one identity")
	}
}
