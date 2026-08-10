package chain

import (
	"errors"
	"testing"
	"time"
)

func TestPolFollower_CanExtendFromTip(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	_ = vs.Register("v1", 100)
	f := NewPolFollower(vs, 2.0/3.0)
	f.SetAnchorFinality(true)
	f.RecordLocalSealedBlock(3, "sr3")
	if f.CanExtendFromTip(3, "sr3") {
		t.Fatal("expected extend blocked before publish")
	}
	f.MarkLocalRoundCertificatePublished(3)
	if !f.CanExtendFromTip(3, "sr3") {
		t.Fatal("expected extend allowed after publish")
	}
}

func TestPolFollower_IngestPrevoteLockProof_Quorum(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	_ = vs.Register("v1", 100)
	_ = vs.Register("v2", 100)
	f := NewPolFollower(vs, 2.0/3.0)

	p := &PrevoteLockProof{
		Height:          3,
		Round:           0,
		LockedBlockHash: "h1",
		Prevotes: []BlockVote{
			{Validator: "v1", BlockHash: "h1", Height: 3, Round: 0, Type: VotePreVote, Timestamp: time.Now()},
			{Validator: "v2", BlockHash: "h1", Height: 3, Round: 0, Type: VotePreVote, Timestamp: time.Now()},
		},
	}
	if err := f.IngestPrevoteLockProof(p); err != nil {
		t.Fatal(err)
	}
	got, ok := f.GetPrevoteLockProof(3)
	if !ok || got.LockedBlockHash != "h1" {
		t.Fatalf("expected stored proof, got ok=%v %#v", ok, got)
	}
}

func TestPolFollowerRejectsInvalidArtifactSignatures(t *testing.T) {
	SetRequireSignedCertificates(false)
	SetSignedCertificateActivationHeight(0)
	t.Cleanup(func() {
		SetRequireSignedCertificates(false)
		SetSignedCertificateActivationHeight(0)
	})

	signer, address := newBFTKey(t)
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	if err := vs.Register(address, 100); err != nil {
		t.Fatal(err)
	}
	follower := NewPolFollower(vs, 2.0/3.0)
	proof := &PrevoteLockProof{
		Height: 2, Round: 0, LockedBlockHash: "root",
		Prevotes: []BlockVote{{Validator: address, BlockHash: "root", Height: 2, Round: 0, Type: VotePreVote}},
	}
	if err := SignPrevoteLockProof(proof, signer); err != nil {
		t.Fatal(err)
	}
	proof.Auth.Signature[0] ^= 0xff
	if err := follower.IngestPrevoteLockProof(proof); !errors.Is(err, ErrCertBadSignature) {
		t.Fatalf("invalid signed lock proof must be rejected, got %v", err)
	}

	cert := &RoundCertificate{
		Height: 2, Round: 0, Proposer: address, BlockHash: "root",
		CommitDigest: "digest", ValidatorSet: []string{address}, CommitCount: 1,
	}
	if err := SignRoundCertificate(cert, signer); err != nil {
		t.Fatal(err)
	}
	cert.Auth.Signature[0] ^= 0xff
	if err := follower.IngestRoundCertificate(cert); !errors.Is(err, ErrCertBadSignature) {
		t.Fatalf("invalid signed certificate must be rejected, got %v", err)
	}
}

func TestPolFollowerRejectsSignedBundleThatClaimsOtherValidators(t *testing.T) {
	signer, address := newBFTKey(t)
	_, otherAddress := newBFTKey(t)
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	for _, validator := range []string{address, otherAddress} {
		if err := vs.Register(validator, 100); err != nil {
			t.Fatal(err)
		}
	}
	follower := NewPolFollower(vs, 2.0/3.0)

	proof := &PrevoteLockProof{
		Height: 3, Round: 0, LockedBlockHash: "root",
		Prevotes: []BlockVote{
			{Validator: address, BlockHash: "root", Height: 3, Round: 0, Type: VotePreVote},
			{Validator: otherAddress, BlockHash: "root", Height: 3, Round: 0, Type: VotePreVote},
		},
	}
	if err := SignPrevoteLockProof(proof, signer); err != nil {
		t.Fatal(err)
	}
	if err := follower.IngestPrevoteLockProof(proof); err == nil {
		t.Fatal("one signer must not authenticate another validator's prevote")
	}

	cert := &RoundCertificate{
		Height: 3, Round: 0, Proposer: address, BlockHash: "root",
		CommitDigest: "digest", ValidatorSet: []string{address, otherAddress}, CommitCount: 2,
	}
	if err := SignRoundCertificate(cert, signer); err != nil {
		t.Fatal(err)
	}
	if err := follower.IngestRoundCertificate(cert); err == nil {
		t.Fatal("one signer must not authenticate a multi-validator certificate")
	}
}

func TestPolFollowerRejectsDuplicateValidatorClaims(t *testing.T) {
	SetRequireSignedCertificates(false)
	SetSignedCertificateActivationHeight(0)
	t.Cleanup(func() {
		SetRequireSignedCertificates(false)
		SetSignedCertificateActivationHeight(0)
	})

	_, address := newBFTKey(t)
	_, otherAddress := newBFTKey(t)
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	for _, validator := range []string{address, otherAddress} {
		if err := vs.Register(validator, 100); err != nil {
			t.Fatal(err)
		}
	}
	follower := NewPolFollower(vs, 2.0/3.0)
	proof := &PrevoteLockProof{
		Height: 4, LockedBlockHash: "root",
		Prevotes: []BlockVote{
			{Validator: address, BlockHash: "root", Height: 4, Type: VotePreVote},
			{Validator: address, BlockHash: "root", Height: 4, Type: VotePreVote},
		},
	}
	if err := follower.IngestPrevoteLockProof(proof); err == nil {
		t.Fatal("duplicate prevotes must not multiply one validator's stake")
	}

	cert := &RoundCertificate{
		Height: 4, CommitDigest: "digest", ValidatorSet: []string{address, address}, CommitCount: 2,
	}
	if err := follower.IngestRoundCertificate(cert); err == nil {
		t.Fatal("duplicate certificate validators must be rejected")
	}
}

func TestPolFollower_IngestPrevoteLockProof_InsufficientStake(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	_ = vs.Register("v1", 100)
	_ = vs.Register("v2", 100)
	f := NewPolFollower(vs, 2.0/3.0)

	p := &PrevoteLockProof{
		Height:          3,
		Round:           0,
		LockedBlockHash: "h1",
		Prevotes: []BlockVote{
			{Validator: "v1", BlockHash: "h1", Height: 3, Round: 0, Type: VotePreVote, Timestamp: time.Now()},
		},
	}
	if err := f.IngestPrevoteLockProof(p); err == nil {
		t.Fatal("expected error for insufficient prevote stake")
	}
}

func TestPolFollower_IngestRoundCertificate(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	_ = vs.Register("v1", 100)
	_ = vs.Register("v2", 100)
	f := NewPolFollower(vs, 2.0/3.0)

	c := &RoundCertificate{
		Height:         9,
		Round:          0,
		Proposer:       "v1",
		BlockHash:      "b",
		CommitDigest:   "abc123",
		ValidatorSet:   []string{"v1", "v2"},
		CommitCount:    2,
		NilCommitCount: 0,
	}
	if err := f.IngestRoundCertificate(c); err != nil {
		t.Fatal(err)
	}
	got, ok := f.GetRoundCertificate(9)
	if !ok || got.BlockHash != "b" {
		t.Fatalf("cert: ok=%v %#v", ok, got)
	}
}

func TestPolFollower_AllowFinalize_Anchor(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	_ = vs.Register("v1", 100)
	f := NewPolFollower(vs, 2.0/3.0)
	f.RecordLocalSealedBlock(1, "sr1")
	f.SetAnchorFinality(true)
	if f.AllowFinalize(1, "sr1") {
		t.Fatal("expected finalize blocked before POL publish")
	}
	f.MarkLocalRoundCertificatePublished(1)
	if !f.AllowFinalize(1, "sr1") {
		t.Fatal("expected finalize allowed after local POL publish mark")
	}
}

func TestPolFollower_IngestRoundCertificate_ConflictsLocal(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	_ = vs.Register("v1", 100)
	_ = vs.Register("v2", 100)
	f := NewPolFollower(vs, 2.0/3.0)
	f.RecordLocalSealedBlock(2, "local-root")
	c := &RoundCertificate{
		Height:         2,
		Round:          0,
		Proposer:       "v1",
		BlockHash:      "other-root",
		CommitDigest:   "digest1",
		ValidatorSet:   []string{"v1", "v2"},
		CommitCount:    2,
		NilCommitCount: 0,
	}
	if err := f.IngestRoundCertificate(c); err == nil {
		t.Fatal("expected fork conflict error")
	}
	if !f.HasConflict(2) {
		t.Fatal("expected conflict flag")
	}
}

func TestPolFollower_IngestRoundCertificate_UnknownValidator(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	_ = vs.Register("v1", 100)
	f := NewPolFollower(vs, 2.0/3.0)

	c := &RoundCertificate{
		Height:         9,
		Round:          0,
		Proposer:       "v1",
		BlockHash:      "b",
		CommitDigest:   "abc123",
		ValidatorSet:   []string{"v1", "ghost"},
		CommitCount:    2,
		NilCommitCount: 0,
	}
	if err := f.IngestRoundCertificate(c); err == nil {
		t.Fatal("expected error for unknown validator")
	}
}
