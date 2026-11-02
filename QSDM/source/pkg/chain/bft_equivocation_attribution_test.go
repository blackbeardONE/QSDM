package chain

import "testing"

// An inbound propose is only attributable to the validator it names when it
// carries a verified signature. With require_signed_votes off -- which is what
// both shipped bring-up scripts set (deploy/bring-up-validator.sh,
// deploy/install-ubuntu-vps.sh) -- checkInboundAuth accepts an unsigned
// propose without checking the Proposer field at all.
//
// So any peer that can reach the gossip topic could send two unsigned proposes
// at the same height and round, both naming the round's legitimate proposer
// and carrying different block hashes. The node would "detect" equivocation,
// manufacture evidence against a validator that did nothing, and
// EvidenceManager.Process would slash its bond and its delegators'.
//
// That is the same defect class as the nvidia-hmac-v1 slashing gate: evidence
// anyone can fabricate must not move money. Detection must fail closed.
func TestEquivocation_UnsignedProposesDoNotSlash(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	for _, addr := range []string{"v1", "v2", "v3"} {
		if err := vs.Register(addr, 100); err != nil {
			t.Fatalf("register %s: %v", addr, err)
		}
	}
	bc := NewBFTConsensus(vs, DefaultConsensusConfig())
	em := NewEvidenceManager(vs)
	ex := NewBFTExecutor(bc)
	ex.SetEvidenceManager(em)

	victim, err := bc.ProposerForRound(0)
	if err != nil {
		t.Fatalf("proposer for round 0: %v", err)
	}
	v, ok := vs.GetValidator(victim)
	if !ok {
		t.Fatalf("victim %q is not in the validator set", victim)
	}
	before := *v // GetValidator hands back live state; copy before mutating.

	// Neither propose is signed. The attacker never had the victim's key.
	b1, _ := MarshalBFTWire(BFTWirePropose, BFTWireProposeMsg{
		Height: 3, Round: 0, Proposer: victim, BlockHash: "aa"})
	if err := ex.ApplyInbound(b1); err != nil {
		t.Fatalf("first unsigned propose: %v", err)
	}
	b2, _ := MarshalBFTWire(BFTWirePropose, BFTWireProposeMsg{
		Height: 3, Round: 0, Proposer: victim, BlockHash: "bb"})
	if err := ex.ApplyInbound(b2); err == nil {
		t.Fatal("the conflicting propose should still be refused as a round conflict")
	}

	// The conflict is a real reason to drop the message. It is not a reason
	// to take the named validator's money.
	if n := len(em.List()); n != 0 {
		t.Errorf("unsigned proposes must not produce slashable evidence, got %d record(s)", n)
	}
	after, ok := vs.GetValidator(victim)
	if !ok {
		t.Fatalf("victim %q vanished from the validator set", victim)
	}
	if after.Status != before.Status {
		t.Errorf("victim status moved %v -> %v on unauthenticated evidence", before.Status, after.Status)
	}
	if after.Stake != before.Stake {
		t.Errorf("victim stake moved %v -> %v on unauthenticated evidence", before.Stake, after.Stake)
	}
	if after.SlashCount != before.SlashCount || after.TotalSlashed != before.TotalSlashed {
		t.Errorf("victim slashed (%d/%v -> %d/%v) by two messages any peer could send",
			before.SlashCount, before.TotalSlashed, after.SlashCount, after.TotalSlashed)
	}
	if after.Status == ValidatorJailed {
		t.Error("victim was jailed by two messages any peer could send")
	}
}

// The fix must not disable honest detection: a proposer that really does sign
// two conflicting proposes is attributable, and still gets reported.
func TestEquivocation_SignedProposesStillSlash(t *testing.T) {
	signer, addr := newBFTKey(t)

	vs := NewValidatorSet(DefaultValidatorSetConfig())
	if err := vs.Register(addr, 100); err != nil {
		t.Fatalf("register %s: %v", addr, err)
	}
	bc := NewBFTConsensus(vs, DefaultConsensusConfig())
	em := NewEvidenceManager(vs)
	ex := NewBFTExecutor(bc)
	ex.SetEvidenceManager(em)

	prop, err := bc.ProposerForRound(0)
	if err != nil {
		t.Fatalf("proposer for round 0: %v", err)
	}
	if prop != addr {
		t.Fatalf("expected the sole validator %q to propose round 0, got %q", addr, prop)
	}

	send := func(hash string) error {
		m := BFTWireProposeMsg{Height: 3, Round: 0, Proposer: addr, BlockHash: hash}
		if err := SignPropose(&m, signer); err != nil {
			t.Fatalf("sign propose %s: %v", hash, err)
		}
		if !m.Auth.Signed() {
			t.Fatalf("propose %s did not come back signed", hash)
		}
		b, err := MarshalBFTWire(BFTWirePropose, m)
		if err != nil {
			t.Fatalf("marshal %s: %v", hash, err)
		}
		return ex.ApplyInbound(b)
	}

	if err := send("aa"); err != nil {
		t.Fatalf("first signed propose: %v", err)
	}
	if err := send("bb"); err == nil {
		t.Fatal("expected the conflicting signed propose to error")
	}

	lst := em.List()
	if len(lst) != 1 {
		t.Fatalf("signed equivocation must still be reported, got %d record(s)", len(lst))
	}
	if lst[0].Evidence.Type != EvidenceEquivocation {
		t.Fatalf("evidence type %v", lst[0].Evidence.Type)
	}
	if lst[0].Evidence.Validator != addr {
		t.Fatalf("evidence names %q, want %q", lst[0].Evidence.Validator, addr)
	}
	if lst[0].Evidence.Proof == nil {
		t.Fatal("signed proposer equivocation evidence must carry a proof")
	}
	if err := lst[0].Evidence.Proof.Verify(addr); err != nil {
		t.Fatalf("recorded proof should verify: %v", err)
	}
	if err := lst[0].Evidence.Proof.VerifyBinding(lst[0].Evidence); err != nil {
		t.Fatalf("recorded proof should match its evidence envelope: %v", err)
	}
}
