package chain

import (
	"errors"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/crypto"
)

// signedExhibit builds an authenticated BFT exhibit from a real key.
func signedExhibit(t *testing.T, signer *crypto.Dilithium, validator, kind string, h uint64, r uint32, value string) SignedVoteExhibit {
	t.Helper()
	x := SignedVoteExhibit{Kind: kind, Height: h, Round: r, Validator: validator, BlockHash: value}
	switch kind {
	case BFTWirePropose:
		m := BFTWireProposeMsg{Height: h, Round: r, Proposer: validator, BlockHash: value}
		if err := SignPropose(&m, signer); err != nil {
			t.Fatal(err)
		}
		x.BodyHash = proposeBodyHash(m.Block)
		x.Auth = m.Auth
	case BFTWirePrevote:
		m := BFTWirePrevoteMsg{Height: h, Round: r, Validator: validator, BlockHash: value}
		if err := SignPrevote(&m, signer); err != nil {
			t.Fatal(err)
		}
		x.Auth = m.Auth
	case BFTWirePrecommit:
		m := BFTWirePrecommitMsg{Height: h, Round: r, Validator: validator, BlockHash: value}
		if err := SignPrecommit(&m, signer); err != nil {
			t.Fatal(err)
		}
		x.Auth = m.Auth
	default:
		t.Fatalf("unsupported kind %q", kind)
	}
	return x
}

func TestEquivocationProof_realEquivocationVerifies(t *testing.T) {
	signer, addr := newBFTKey(t)
	a := signedExhibit(t, signer, addr, BFTWirePrevote, 9, 1, "value-a")
	b := signedExhibit(t, signer, addr, BFTWirePrevote, 9, 1, "value-b")

	proof := &EquivocationProof{VoteA: a, VoteB: b}
	if err := proof.Verify(addr); err != nil {
		t.Fatalf("genuine equivocation should verify: %v", err)
	}
}

func TestEquivocationProof_signedProposeEquivocationVerifies(t *testing.T) {
	signer, addr := newBFTKey(t)
	a := signedExhibit(t, signer, addr, BFTWirePropose, 12, 2, "proposal-a")
	b := signedExhibit(t, signer, addr, BFTWirePropose, 12, 2, "proposal-b")

	ev, err := BuildEquivocationEvidence(addr, a, b)
	if err != nil {
		t.Fatalf("signed propose equivocation should build: %v", err)
	}
	if ev.Proof == nil {
		t.Fatal("signed propose evidence must carry its proof")
	}
	if err := validateEvidence(ev); err != nil {
		t.Fatalf("signed propose evidence should validate: %v", err)
	}

	tampered := *ev.Proof
	tampered.VoteA.BodyHash = "not-the-signed-body"
	if err := tampered.Verify(addr); err == nil {
		t.Fatal("tampering with a proposal body hash must invalidate the proof")
	}
}

// TestEquivocationProof_cannotFrameAnInnocentValidator is the regression
// test for fabricated equivocation evidence. validateEvidence used to accept
// an accusation on asserted fields alone: a validator name plus two differing
// hashes, with nothing binding those hashes to the accused. Any peer could
// therefore drive a slash against a validator that never equivocated.
func TestEquivocationProof_cannotFrameAnInnocentValidator(t *testing.T) {
	attacker, _ := newBFTKey(t)
	_, victim := newBFTKey(t)

	// Attacker signs two conflicting votes with their OWN key while naming
	// the victim as the validator.
	a := SignedVoteExhibit{Kind: BFTWirePrevote, Height: 9, Round: 1, Validator: victim, BlockHash: "value-a"}
	ma := BFTWirePrevoteMsg{Height: 9, Round: 1, Validator: victim, BlockHash: "value-a"}
	if err := SignPrevote(&ma, attacker); err != nil {
		t.Fatal(err)
	}
	a.Auth = ma.Auth

	b := SignedVoteExhibit{Kind: BFTWirePrevote, Height: 9, Round: 1, Validator: victim, BlockHash: "value-b"}
	mb := BFTWirePrevoteMsg{Height: 9, Round: 1, Validator: victim, BlockHash: "value-b"}
	if err := SignPrevote(&mb, attacker); err != nil {
		t.Fatal(err)
	}
	b.Auth = mb.Auth

	proof := &EquivocationProof{VoteA: a, VoteB: b}
	err := proof.Verify(victim)
	if err == nil {
		t.Fatal("votes signed by the attacker must not prove the victim equivocated")
	}
	if !errors.Is(err, ErrEvidenceProofInvalid) {
		t.Fatalf("want ErrEvidenceProofInvalid, got %v", err)
	}
}

// Honest protocol behaviour must never read as equivocation.
func TestEquivocationProof_rejectsNonEquivocatingExhibits(t *testing.T) {
	signer, addr := newBFTKey(t)

	t.Run("different heights", func(t *testing.T) {
		a := signedExhibit(t, signer, addr, BFTWirePrevote, 9, 1, "a")
		b := signedExhibit(t, signer, addr, BFTWirePrevote, 10, 1, "b")
		if err := (&EquivocationProof{VoteA: a, VoteB: b}).Verify(addr); err == nil {
			t.Fatal("voting at different heights is legal")
		}
	})
	t.Run("different rounds", func(t *testing.T) {
		a := signedExhibit(t, signer, addr, BFTWirePrevote, 9, 1, "a")
		b := signedExhibit(t, signer, addr, BFTWirePrevote, 9, 2, "b")
		if err := (&EquivocationProof{VoteA: a, VoteB: b}).Verify(addr); err == nil {
			t.Fatal("voting differently across rounds is legal")
		}
	})
	t.Run("prevote vs precommit", func(t *testing.T) {
		a := signedExhibit(t, signer, addr, BFTWirePrevote, 9, 1, "a")
		b := signedExhibit(t, signer, addr, BFTWirePrecommit, 9, 1, "b")
		if err := (&EquivocationProof{VoteA: a, VoteB: b}).Verify(addr); err == nil {
			t.Fatal("a prevote and a precommit are different message kinds, not equivocation")
		}
	})
	t.Run("agreeing votes", func(t *testing.T) {
		a := signedExhibit(t, signer, addr, BFTWirePrevote, 9, 1, "same")
		b := signedExhibit(t, signer, addr, BFTWirePrevote, 9, 1, "same")
		if err := (&EquivocationProof{VoteA: a, VoteB: b}).Verify(addr); err == nil {
			t.Fatal("two identical votes show no conflict")
		}
	})
}

func TestBuildEquivocationEvidence_refusesUnprovableAccusation(t *testing.T) {
	signer, addr := newBFTKey(t)
	a := signedExhibit(t, signer, addr, BFTWirePrevote, 9, 1, "a")
	b := signedExhibit(t, signer, addr, BFTWirePrevote, 9, 1, "a") // agreeing

	if _, err := BuildEquivocationEvidence(addr, a, b); err == nil {
		t.Fatal("builder must refuse to assemble evidence it cannot prove")
	}

	good := signedExhibit(t, signer, addr, BFTWirePrevote, 9, 1, "b")
	ev, err := BuildEquivocationEvidence(addr, a, good)
	if err != nil {
		t.Fatalf("genuine equivocation should build: %v", err)
	}
	if ev.Proof == nil {
		t.Fatal("built evidence must carry its proof")
	}
	if err := validateEvidence(ev); err != nil {
		t.Fatalf("proof-carrying evidence should validate: %v", err)
	}
}

// Missing equivocation proof is always fatal. The old rollout flags are kept
// for diagnostics only and must not reopen proofless slashing.
func TestValidateEvidence_requiresProofAlways(t *testing.T) {
	signer, addr := newBFTKey(t)
	_, other := newBFTKey(t)

	bare := ConsensusEvidence{
		Type: EvidenceEquivocation, Validator: addr,
		Height: 9, Round: 1, BlockHashes: []string{"a", "b"},
	}

	t.Cleanup(func() {
		SetRequireEvidenceProof(false)
		SetEvidenceProofActivationHeight(0)
	})
	for _, require := range []bool{false, true} {
		SetRequireEvidenceProof(require)
		for _, activation := range []uint64{0, 10} {
			SetEvidenceProofActivationHeight(activation)
			if err := validateEvidence(bare); !errors.Is(err, ErrEvidenceProofMissing) {
				t.Fatalf("require=%v activation=%d: want ErrEvidenceProofMissing, got %v", require, activation, err)
			}
		}
	}

	// A proof naming someone else is rejected even with the old policy flag off.
	SetRequireEvidenceProof(false)
	mismatched := bare
	mismatched.Proof = &EquivocationProof{
		VoteA: signedExhibit(t, signer, other, BFTWirePrevote, 9, 1, "a"),
		VoteB: signedExhibit(t, signer, other, BFTWirePrevote, 9, 1, "b"),
	}
	if err := validateEvidence(mismatched); err == nil {
		t.Fatal("a proof about a different validator must be rejected")
	}
}
