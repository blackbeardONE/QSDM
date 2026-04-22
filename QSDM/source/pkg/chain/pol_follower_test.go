package chain

import (
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
