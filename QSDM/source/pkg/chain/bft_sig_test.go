package chain

import (
	"errors"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/crypto"
)

// newBFTKey returns an ML-DSA-87 signer and the self-certifying validator
// address derived from its public key.
func newBFTKey(t *testing.T) (*crypto.Dilithium, string) {
	t.Helper()
	d := crypto.NewDilithium()
	if d == nil {
		t.Skip("ML-DSA signer unavailable in this build")
	}
	t.Cleanup(d.Free)
	return d, BFTValidatorAddress(d.GetPublicKey())
}

func TestBFTPrevote_signAndVerifyRoundTrip(t *testing.T) {
	signer, addr := newBFTKey(t)
	m := BFTWirePrevoteMsg{Height: 7, Round: 2, Validator: addr, BlockHash: "root-abc"}
	if err := SignPrevote(&m, signer); err != nil {
		t.Fatalf("SignPrevote: %v", err)
	}
	if !m.Auth.Signed() {
		t.Fatal("prevote should carry an authenticator after signing")
	}
	if err := VerifyPrevote(m); err != nil {
		t.Fatalf("honest prevote should verify: %v", err)
	}
}

// TestBFTPrevote_cannotForgeAnotherValidator is the regression test for the
// core consensus forgery hole: BFT wire messages carried no signature, so
// any peer on the bft gossip topic could submit prevotes and precommits
// naming any validator and manufacture a quorum.
func TestBFTPrevote_cannotForgeAnotherValidator(t *testing.T) {
	attacker, _ := newBFTKey(t)
	_, victim := newBFTKey(t)

	// Attacker signs correctly — with their own key, naming the victim.
	m := BFTWirePrevoteMsg{Height: 7, Round: 2, Validator: victim, BlockHash: "root-abc"}
	if err := SignPrevote(&m, attacker); err != nil {
		t.Fatal(err)
	}

	err := VerifyPrevote(m)
	if err == nil {
		t.Fatal("a vote signed by one key must not authenticate another validator")
	}
	if !errors.Is(err, ErrBFTBadSignature) {
		t.Fatalf("want ErrBFTBadSignature, got %v", err)
	}
}

// A vote is bound to its height, round, kind and value: none can be mutated
// after signing, and a prevote cannot be replayed as a precommit.
func TestBFTVote_digestBindsEveryConsensusField(t *testing.T) {
	signer, addr := newBFTKey(t)
	base := BFTWirePrevoteMsg{Height: 7, Round: 2, Validator: addr, BlockHash: "root-abc"}
	if err := SignPrevote(&base, signer); err != nil {
		t.Fatal(err)
	}

	t.Run("height", func(t *testing.T) {
		m := base
		m.Height = 8
		if err := VerifyPrevote(m); err == nil {
			t.Fatal("height must be covered by the signature")
		}
	})
	t.Run("round", func(t *testing.T) {
		m := base
		m.Round = 3
		if err := VerifyPrevote(m); err == nil {
			t.Fatal("round must be covered by the signature")
		}
	})
	t.Run("vote value", func(t *testing.T) {
		m := base
		m.BlockHash = "root-xyz"
		if err := VerifyPrevote(m); err == nil {
			t.Fatal("vote value must be covered by the signature")
		}
	})
	t.Run("kind", func(t *testing.T) {
		// Replay the signed prevote's authenticator on a precommit.
		pc := BFTWirePrecommitMsg{
			Height: base.Height, Round: base.Round,
			Validator: base.Validator, BlockHash: base.BlockHash,
			Auth: base.Auth,
		}
		if err := VerifyPrecommit(pc); err == nil {
			t.Fatal("a prevote signature must not authenticate a precommit")
		}
	})
}

// A proposal's attached block body is covered, so an attacker cannot swap
// the block under a validly-signed proposal header.
func TestBFTPropose_bodySwapDetected(t *testing.T) {
	signer, addr := newBFTKey(t)
	body := &Block{Height: 3, ProducerID: addr, StateRoot: "sr"}
	body.Hash = computeBlockHash(body)

	m := BFTWireProposeMsg{Height: 3, Round: 0, Proposer: addr, BlockHash: "sr", Block: body}
	if err := SignPropose(&m, signer); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPropose(m); err != nil {
		t.Fatalf("honest proposal should verify: %v", err)
	}

	evil := &Block{Height: 3, ProducerID: addr, StateRoot: "sr"}
	evil.Hash = "0000000000000000000000000000000000000000000000000000000000000000"
	m.Block = evil
	if err := VerifyPropose(m); err == nil {
		t.Fatal("swapping the proposed block body must invalidate the signature")
	}
}

func TestBFTVote_unsignedIsRejectedWhenRequired(t *testing.T) {
	m := BFTWirePrevoteMsg{Height: 1, Round: 0, Validator: "someone", BlockHash: "x"}
	if err := VerifyPrevote(m); !errors.Is(err, ErrBFTUnsigned) {
		t.Fatalf("want ErrBFTUnsigned for a bare message, got %v", err)
	}
}

// Executor policy: unsigned messages pass only while enforcement is off, but
// a present-yet-invalid signature is always fatal.
func TestBFTExecutor_inboundAuthPolicy(t *testing.T) {
	e := &BFTExecutor{}

	called := false
	verify := func() error { called = true; return ErrBFTBadSignature }

	// Enforcement off + unsigned => allowed, verifier not consulted.
	if err := e.checkInboundAuth(1, false, verify); err != nil {
		t.Fatalf("unsigned message should pass while enforcement is off: %v", err)
	}
	if called {
		t.Fatal("verifier should not run for an unsigned message")
	}

	// Enforcement off + signed-but-bad => still rejected.
	if err := e.checkInboundAuth(1, true, verify); !errors.Is(err, ErrBFTBadSignature) {
		t.Fatalf("an invalid signature must be fatal regardless of policy, got %v", err)
	}

	// Enforcement on + unsigned => rejected.
	e.SetRequireSignedVotes(true)
	if err := e.checkInboundAuth(1, false, verify); !errors.Is(err, ErrBFTUnsigned) {
		t.Fatalf("want ErrBFTUnsigned once enforcement is on, got %v", err)
	}

	e.SetSignedVoteActivationHeight(10)
	if err := e.checkInboundAuth(9, false, verify); err != nil {
		t.Fatalf("historical unsigned vote below activation should pass: %v", err)
	}
	if err := e.checkInboundAuth(10, false, verify); !errors.Is(err, ErrBFTUnsigned) {
		t.Fatalf("unsigned vote at activation must fail, got %v", err)
	}
}

// End-to-end through the executor: a forged prevote must never reach
// BFTConsensus.PreVote.
func TestBFTExecutor_rejectsForgedPrevoteOnTheWire(t *testing.T) {
	attacker, _ := newBFTKey(t)
	_, victim := newBFTKey(t)

	vs := NewValidatorSet(DefaultValidatorSetConfig())
	e := NewBFTExecutor(NewBFTConsensus(vs, DefaultConsensusConfig()))

	m := BFTWirePrevoteMsg{Height: 1, Round: 0, Validator: victim, BlockHash: "v"}
	if err := SignPrevote(&m, attacker); err != nil {
		t.Fatal(err)
	}
	payload, err := MarshalBFTWire(BFTWirePrevote, m)
	if err != nil {
		t.Fatal(err)
	}

	if err := e.ApplyInbound(payload); err == nil {
		t.Fatal("executor must refuse a prevote signed by a key that does not derive the named validator")
	}
}
