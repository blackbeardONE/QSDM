package chain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
)

func TestBFTMembershipPolicyBindsSignedIdentityToSnapshot(t *testing.T) {
	signer, address := newBFTKey(t)
	other, otherAddress := newBFTKey(t)
	membership := membershipForBFTSigners(t, 10, signer)
	schedule, err := NewConsensusMembershipSchedule([]ConsensusMembership{membership})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBFTMembershipPolicy(schedule, 10)
	if err != nil {
		t.Fatal(err)
	}
	root, err := policy.MembershipRootForHeight(10)
	if err != nil {
		t.Fatal(err)
	}

	msg := BFTWirePrevoteMsg{
		Height:         10,
		Round:          2,
		Validator:      address,
		BlockHash:      "state-root-a",
		MembershipRoot: root,
	}
	if err := SignPrevote(&msg, signer); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPrevote(msg); err != nil {
		t.Fatalf("honest signed vote should verify: %v", err)
	}
	if err := policy.ValidateSignedMember(msg.Height, msg.MembershipRoot, msg.Validator, msg.Auth); err != nil {
		t.Fatalf("listed signer should pass membership validation: %v", err)
	}

	if err := policy.ValidateSignedMember(msg.Height, strings.Repeat("0", 64), msg.Validator, msg.Auth); !errors.Is(err, ErrBFTMembershipRoot) {
		t.Fatalf("wrong root error = %v, want ErrBFTMembershipRoot", err)
	}
	if err := policy.ValidateSignedMember(msg.Height, root, msg.Validator, BFTWireAuth{PublicKey: other.GetPublicKey(), Signature: []byte{1}}); !errors.Is(err, ErrBFTMembershipKeyMismatch) {
		t.Fatalf("wrong member key error = %v, want ErrBFTMembershipKeyMismatch", err)
	}
	if err := policy.ValidateSignedMember(msg.Height, root, otherAddress, BFTWireAuth{PublicKey: other.GetPublicKey(), Signature: []byte{1}}); !errors.Is(err, ErrBFTMembershipUnauthorized) {
		t.Fatalf("outsider error = %v, want ErrBFTMembershipUnauthorized", err)
	}
	if err := policy.ValidateSignedMember(9, "wrong", otherAddress, BFTWireAuth{}); err != nil {
		t.Fatalf("policy must retain pre-activation compatibility, got %v", err)
	}
}

func TestBFTMembershipPolicyUsesScheduledRootAtEachHeight(t *testing.T) {
	first, _ := newBFTKey(t)
	second, _ := newBFTKey(t)
	genesis := membershipForBFTSigners(t, 10, first)
	upgrade := membershipForBFTSigners(t, 20, first, second)
	schedule, err := NewConsensusMembershipSchedule([]ConsensusMembership{upgrade, genesis})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBFTMembershipPolicy(schedule, 10)
	if err != nil {
		t.Fatal(err)
	}

	before, err := policy.MembershipRootForHeight(9)
	if err != nil {
		t.Fatal(err)
	}
	atActivation, err := policy.MembershipRootForHeight(10)
	if err != nil {
		t.Fatal(err)
	}
	beforeUpgrade, err := policy.MembershipRootForHeight(19)
	if err != nil {
		t.Fatal(err)
	}
	afterUpgrade, err := policy.MembershipRootForHeight(20)
	if err != nil {
		t.Fatal(err)
	}
	if before != "" {
		t.Fatalf("pre-activation root = %q, want empty", before)
	}
	if atActivation == "" || atActivation != beforeUpgrade {
		t.Fatalf("root before scheduled upgrade = %q then %q, want stable non-empty root", atActivation, beforeUpgrade)
	}
	if afterUpgrade == atActivation {
		t.Fatal("membership upgrade did not change the active root")
	}
}

func TestBFTExecutorMembershipGateBindsOutboundAndInboundWire(t *testing.T) {
	member, memberAddress := newBFTKey(t)
	outsider, outsiderAddress := newBFTKey(t)
	membership := membershipForBFTSigners(t, 10, member)
	schedule, err := NewConsensusMembershipSchedule([]ConsensusMembership{membership})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBFTMembershipPolicy(schedule, 10)
	if err != nil {
		t.Fatal(err)
	}
	root, err := policy.MembershipRootForHeight(10)
	if err != nil {
		t.Fatal(err)
	}

	validators := NewValidatorSet(DefaultValidatorSetConfig())
	if err := validators.Register(memberAddress, 100); err != nil {
		t.Fatal(err)
	}
	executor := NewBFTExecutor(NewBFTConsensus(validators, DefaultConsensusConfig()))
	executor.SetMembershipPolicy(policy)
	executor.SetVoteSigner(member)
	var published []byte
	executor.SetPublisher(func(payload []byte) error {
		published = append([]byte(nil), payload...)
		return nil
	})

	if err := executor.BroadcastPrevote(10, 0, memberAddress, "state-root-a"); err != nil {
		t.Fatalf("member outbound prevote: %v", err)
	}
	kind, raw, err := UnmarshalBFTWire(published)
	if err != nil {
		t.Fatal(err)
	}
	if kind != BFTWirePrevote {
		t.Fatalf("published kind = %q, want %q", kind, BFTWirePrevote)
	}
	var outbound BFTWirePrevoteMsg
	if err := unmarshalBFTPayload(raw, &outbound); err != nil {
		t.Fatal(err)
	}
	if outbound.MembershipRoot != root {
		t.Fatalf("outbound root = %q, want %q", outbound.MembershipRoot, root)
	}
	if err := VerifyPrevote(outbound); err != nil {
		t.Fatalf("outbound signature should include the membership root: %v", err)
	}
	if err := executor.ValidateInboundAuthentication(published); err != nil {
		t.Fatalf("relay validation listed member: %v", err)
	}

	outsiderMsg := BFTWirePrevoteMsg{
		Height:         10,
		Round:          0,
		Validator:      outsiderAddress,
		BlockHash:      "state-root-a",
		MembershipRoot: root,
	}
	if err := SignPrevote(&outsiderMsg, outsider); err != nil {
		t.Fatal(err)
	}
	outsiderPayload, err := MarshalBFTWire(BFTWirePrevote, outsiderMsg)
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.ValidateInboundAuthentication(outsiderPayload); !errors.Is(err, ErrBFTMembershipUnauthorized) {
		t.Fatalf("relay validation outsider error = %v, want ErrBFTMembershipUnauthorized", err)
	}
	if err := executor.ApplyInbound(outsiderPayload); !errors.Is(err, ErrBFTMembershipUnauthorized) {
		t.Fatalf("apply outsider error = %v, want ErrBFTMembershipUnauthorized", err)
	}

	unsigned := BFTWirePrevoteMsg{
		Height:         10,
		Round:          0,
		Validator:      memberAddress,
		BlockHash:      "state-root-a",
		MembershipRoot: root,
	}
	unsignedPayload, err := MarshalBFTWire(BFTWirePrevote, unsigned)
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.ValidateInboundAuthentication(unsignedPayload); !errors.Is(err, ErrBFTUnsigned) {
		t.Fatalf("unsigned active vote error = %v, want ErrBFTUnsigned", err)
	}

	stats := executor.MembershipStats()
	if !stats.Enabled || stats.ActivationHeight != 10 || stats.Accepted != 1 || stats.Rejected != 3 {
		t.Fatalf("membership stats = %+v, want enabled gate with three rejected messages", stats)
	}
	if got, want := stats.SnapshotHeights, []uint64{10}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("membership snapshot heights = %v, want %v", got, want)
	}

	outsiderExecutor := NewBFTExecutor(NewBFTConsensus(NewValidatorSet(DefaultValidatorSetConfig()), DefaultConsensusConfig()))
	outsiderExecutor.SetMembershipPolicy(policy)
	outsiderExecutor.SetVoteSigner(outsider)
	if err := outsiderExecutor.BroadcastPrevote(10, 0, outsiderAddress, "state-root-a"); !errors.Is(err, ErrBFTMembershipUnauthorized) {
		t.Fatalf("outsider outbound error = %v, want ErrBFTMembershipUnauthorized", err)
	}
}

func TestBFTMembershipRootIsCoveredByVoteSignature(t *testing.T) {
	signer, address := newBFTKey(t)
	msg := BFTWirePrevoteMsg{
		Height:         12,
		Round:          3,
		Validator:      address,
		BlockHash:      "state-root-a",
		MembershipRoot: strings.Repeat("a", 64),
	}
	if err := SignPrevote(&msg, signer); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPrevote(msg); err != nil {
		t.Fatalf("honest membership-bound vote should verify: %v", err)
	}
	msg.MembershipRoot = strings.Repeat("b", 64)
	if err := VerifyPrevote(msg); !errors.Is(err, ErrBFTBadSignature) {
		t.Fatalf("mutated membership root error = %v, want ErrBFTBadSignature", err)
	}
}

func TestEquivocationProofRejectsVotesFromDifferentMembershipRoots(t *testing.T) {
	signer, address := newBFTKey(t)
	left := BFTWirePrevoteMsg{
		Height:         18,
		Round:          1,
		Validator:      address,
		BlockHash:      "state-root-a",
		MembershipRoot: strings.Repeat("a", 64),
	}
	right := BFTWirePrevoteMsg{
		Height:         left.Height,
		Round:          left.Round,
		Validator:      address,
		BlockHash:      "state-root-b",
		MembershipRoot: strings.Repeat("b", 64),
	}
	if err := SignPrevote(&left, signer); err != nil {
		t.Fatal(err)
	}
	if err := SignPrevote(&right, signer); err != nil {
		t.Fatal(err)
	}
	proof := EquivocationProof{
		VoteA: SignedVoteExhibit{
			Kind: BFTWirePrevote, Height: left.Height, Round: left.Round,
			Validator: left.Validator, BlockHash: left.BlockHash, MembershipRoot: left.MembershipRoot, Auth: left.Auth,
		},
		VoteB: SignedVoteExhibit{
			Kind: BFTWirePrevote, Height: right.Height, Round: right.Round,
			Validator: right.Validator, BlockHash: right.BlockHash, MembershipRoot: right.MembershipRoot, Auth: right.Auth,
		},
	}
	if err := proof.Verify(address); !errors.Is(err, ErrEvidenceProofInvalid) {
		t.Fatalf("mixed-root equivocation proof error = %v, want ErrEvidenceProofInvalid", err)
	}
}

func TestEquivocationProofFingerprintPreservesRootlessCompatibility(t *testing.T) {
	signer, address := newBFTKey(t)
	proof := &EquivocationProof{
		VoteA: signedExhibit(t, signer, address, BFTWirePrevote, 22, 1, "state-root-a"),
		VoteB: signedExhibit(t, signer, address, BFTWirePrevote, 22, 1, "state-root-b"),
	}
	if got, want := proof.fingerprint(), legacyRootlessEquivocationFingerprint(proof); got != want {
		t.Fatalf("rootless evidence fingerprint = %s, want legacy %s", got, want)
	}

	membershipBound := *proof
	membershipBound.VoteA.MembershipRoot = strings.Repeat("c", 64)
	membershipBound.VoteB.MembershipRoot = membershipBound.VoteA.MembershipRoot
	if membershipBound.fingerprint() == proof.fingerprint() {
		t.Fatal("membership root must separate evidence identities")
	}
}

func legacyRootlessEquivocationFingerprint(p *EquivocationProof) string {
	digests := make([]string, 0, 2)
	for _, vote := range []SignedVoteExhibit{p.VoteA, p.VoteB} {
		h := sha256.New()
		writeLenPrefixed(h, []byte(vote.Kind))
		writeLenPrefixed(h, []byte(strings.ToLower(vote.Validator)))
		writeUint64Prefixed(h, vote.Height)
		writeUint64Prefixed(h, uint64(vote.Round))
		writeLenPrefixed(h, []byte(vote.BlockHash))
		writeLenPrefixed(h, []byte(vote.BodyHash))
		digests = append(digests, hex.EncodeToString(h.Sum(nil)))
	}
	sort.Strings(digests)

	outer := sha256.New()
	for _, digest := range digests {
		writeLenPrefixed(outer, []byte(digest))
	}
	return hex.EncodeToString(outer.Sum(nil))
}
func membershipForBFTSigners(t *testing.T, effectiveHeight uint64, signers ...BFTSigner) ConsensusMembership {
	t.Helper()
	members := make([]ConsensusMember, 0, len(signers))
	for index, signer := range signers {
		publicKey := signer.GetPublicKey()
		members = append(members, ConsensusMember{
			Address:               BFTValidatorAddress(publicKey),
			ConsensusPublicKeyHex: hex.EncodeToString(publicKey),
			P2PPeerID:             testPeerID(byte(index + 1)),
			VotingPower:           1,
		})
	}
	membership := ConsensusMembership{
		SchemaVersion:   ConsensusMembershipSchemaVersion,
		NetworkID:       "qsdm-bft-membership-test",
		EffectiveHeight: effectiveHeight,
		Members:         members,
	}
	if err := membership.Validate(); err != nil {
		t.Fatalf("test membership validation: %v", err)
	}
	return membership
}

func unmarshalBFTPayload(raw []byte, value interface{}) error {
	return json.Unmarshal(raw, value)
}
