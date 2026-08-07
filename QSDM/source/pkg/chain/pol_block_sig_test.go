package chain

import (
	"errors"
	"testing"
)

// --- Block producer signatures -------------------------------------------

func TestSignBlock_roundTrip(t *testing.T) {
	signer, addr := newBFTKey(t)
	b := &Block{Height: 4, PrevHash: "prev", StateRoot: "sr", ProducerID: addr}
	b.Hash = computeBlockHash(b)

	if err := SignBlock(b, signer); err != nil {
		t.Fatalf("SignBlock: %v", err)
	}
	if b.ProducerID != addr {
		t.Fatalf("producer identity should be the signer's derived address, got %s", b.ProducerID)
	}
	if err := VerifyBlockSignature(b); err != nil {
		t.Fatalf("honest block should verify: %v", err)
	}
}

// TestVerifyBlockSignature_cannotImpersonateProducer is the regression test
// for unauthenticated block production. propagation.go accepted any block
// whose Hash recomputed correctly, and ProducerID was an unauthenticated
// string — so an attacker could fabricate a block, name any validator as
// producer, compute the matching hash, and have it accepted.
func TestVerifyBlockSignature_cannotImpersonateProducer(t *testing.T) {
	attacker, _ := newBFTKey(t)
	_, victim := newBFTKey(t)

	b := &Block{Height: 4, PrevHash: "prev", StateRoot: "sr", ProducerID: victim}
	b.Hash = computeBlockHash(b)

	// Attacker signs, then forcibly re-labels the block as the victim's.
	if err := SignBlock(b, attacker); err != nil {
		t.Fatal(err)
	}
	b.ProducerID = victim
	b.Hash = computeBlockHash(b)

	err := VerifyBlockSignature(b)
	if err == nil {
		t.Fatal("a block signed by one key must not verify as produced by another validator")
	}
	if !errors.Is(err, ErrBlockBadSignature) {
		t.Fatalf("want ErrBlockBadSignature, got %v", err)
	}
}

// Mutating any hashed field after signing must invalidate the signature.
func TestVerifyBlockSignature_detectsContentTampering(t *testing.T) {
	signer, addr := newBFTKey(t)
	b := &Block{Height: 4, PrevHash: "prev", StateRoot: "sr", ProducerID: addr}
	b.Hash = computeBlockHash(b)
	if err := SignBlock(b, signer); err != nil {
		t.Fatal(err)
	}

	b.StateRoot = "tampered"
	if err := VerifyBlockSignature(b); err == nil {
		t.Fatal("mutating the state root must invalidate the block signature")
	}
}

func TestSignBlock_refusesStaleHash(t *testing.T) {
	signer, addr := newBFTKey(t)
	b := &Block{Height: 4, PrevHash: "prev", StateRoot: "sr", ProducerID: addr}
	b.Hash = "deadbeef" // not the real hash
	if err := SignBlock(b, signer); err == nil {
		t.Fatal("signing a block with a stale hash must be refused")
	}
}

func TestVerifyBlockSignature_unsignedPolicy(t *testing.T) {
	b := &Block{Height: 1, ProducerID: "someone"}
	b.Hash = computeBlockHash(b)

	SetRequireSignedBlocks(false)
	SetSignedBlockActivationHeight(0)
	t.Cleanup(func() {
		SetRequireSignedBlocks(false)
		SetSignedBlockActivationHeight(0)
	})
	if err := VerifyBlockSignature(b); err != nil {
		t.Fatalf("unsigned block should pass while enforcement is off: %v", err)
	}

	SetRequireSignedBlocks(true)
	if err := VerifyBlockSignature(b); !errors.Is(err, ErrBlockUnsigned) {
		t.Fatalf("want ErrBlockUnsigned once enforcement is on, got %v", err)
	}
	SetSignedBlockActivationHeight(5)
	if err := VerifyBlockSignature(b); err != nil {
		t.Fatalf("unsigned historical block should pass below activation: %v", err)
	}
	b.Height = 5
	b.Hash = computeBlockHash(b)
	if err := VerifyBlockSignature(b); !errors.Is(err, ErrBlockUnsigned) {
		t.Fatalf("unsigned block at activation must fail, got %v", err)
	}
}

// --- POL round certificates ----------------------------------------------

func TestSignRoundCertificate_roundTripAndTamper(t *testing.T) {
	signer, addr := newBFTKey(t)
	c := &RoundCertificate{
		Height: 12, Round: 1, Proposer: addr, BlockHash: "bh",
		CommitDigest: "cd", ValidatorSet: []string{"v1", "v2"},
		CommitCount: 2, NilCommitCount: 0,
	}
	if err := SignRoundCertificate(c, signer); err != nil {
		t.Fatalf("SignRoundCertificate: %v", err)
	}
	if err := VerifyRoundCertificate(c); err != nil {
		t.Fatalf("honest certificate should verify: %v", err)
	}

	// The validator set is covered, so a certificate cannot be replayed
	// against a different set.
	c.ValidatorSet = []string{"v1", "v2", "v3"}
	if err := VerifyRoundCertificate(c); err == nil {
		t.Fatal("mutating the validator set must invalidate the certificate")
	}
}

func TestVerifyRoundCertificate_cannotImpersonateSigner(t *testing.T) {
	attacker, _ := newBFTKey(t)
	_, victim := newBFTKey(t)

	c := &RoundCertificate{Height: 12, Round: 1, Proposer: victim, BlockHash: "bh", CommitDigest: "cd"}
	if err := SignRoundCertificate(c, attacker); err != nil {
		t.Fatal(err)
	}
	c.Signer = victim // claim someone else issued it

	if err := VerifyRoundCertificate(c); err == nil {
		t.Fatal("a certificate signed by one key must not verify as issued by another validator")
	}
}

// --- Prevote-lock proofs --------------------------------------------------

func TestSignPrevoteLockProof_coversPrevotes(t *testing.T) {
	signer, addr := newBFTKey(t)
	p := &PrevoteLockProof{
		Height: 5, Round: 2, LockedBlockHash: "locked",
		Prevotes: []BlockVote{
			{Validator: "v1", BlockHash: "locked", Height: 5, Round: 2},
			{Validator: "v2", BlockHash: "locked", Height: 5, Round: 2},
		},
	}
	if err := SignPrevoteLockProof(p, signer); err != nil {
		t.Fatalf("SignPrevoteLockProof: %v", err)
	}
	if p.Signer != addr {
		t.Fatalf("signer should be the derived address, got %s", p.Signer)
	}
	if err := VerifyPrevoteLockProof(p); err != nil {
		t.Fatalf("honest proof should verify: %v", err)
	}

	// Injecting a fabricated prevote must break the signature — the
	// prevotes are the substance of the proof, and this bundle's verdict
	// gates block production on followers.
	p.Prevotes = append(p.Prevotes, BlockVote{Validator: "v3", BlockHash: "locked", Height: 5, Round: 2})
	if err := VerifyPrevoteLockProof(p); err == nil {
		t.Fatal("injecting a prevote must invalidate the lock proof")
	}
}

func TestVerifyPrevoteLockProof_unsignedPolicy(t *testing.T) {
	p := &PrevoteLockProof{Height: 5, Round: 2, LockedBlockHash: "locked"}

	SetRequireSignedCertificates(false)
	SetSignedCertificateActivationHeight(0)
	t.Cleanup(func() {
		SetRequireSignedCertificates(false)
		SetSignedCertificateActivationHeight(0)
	})
	if err := VerifyPrevoteLockProof(p); err != nil {
		t.Fatalf("unsigned proof should pass while enforcement is off: %v", err)
	}

	SetRequireSignedCertificates(true)
	if err := VerifyPrevoteLockProof(p); !errors.Is(err, ErrCertUnsigned) {
		t.Fatalf("want ErrCertUnsigned once enforcement is on, got %v", err)
	}
	SetSignedCertificateActivationHeight(6)
	if err := VerifyPrevoteLockProof(p); err != nil {
		t.Fatalf("unsigned historical proof should pass below activation: %v", err)
	}
	p.Height = 6
	if err := VerifyPrevoteLockProof(p); !errors.Is(err, ErrCertUnsigned) {
		t.Fatalf("unsigned proof at activation must fail, got %v", err)
	}
}
