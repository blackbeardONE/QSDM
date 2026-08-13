package networking

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/crypto"
)

// signedGossipExhibit builds an authenticated vote exhibit from a real key,
// mirroring pkg/chain's test helper (which is unexported).
func signedGossipExhibit(t *testing.T, signer *crypto.Dilithium, validator string, h uint64, r uint32, value string) chain.SignedVoteExhibit {
	t.Helper()
	m := chain.BFTWirePrevoteMsg{Height: h, Round: r, Validator: validator, BlockHash: value}
	if err := chain.SignPrevote(&m, signer); err != nil {
		t.Fatalf("sign prevote %s: %v", value, err)
	}
	return chain.SignedVoteExhibit{
		Kind: chain.BFTWirePrevote, Height: h, Round: r,
		Validator: validator, BlockHash: value, Auth: m.Auth,
	}
}

// A peer's accusation is not evidence. HandlePeerMessage decodes a
// ConsensusEvidence straight off the wire and hands it to
// EvidenceManager.Process, which slashes the named validator's bond and its
// delegators'. Ban, dedupe and rate-limit checks all run first, and none of
// them authenticate the accusation, so without a proof requirement one
// message from any connected peer destroys any validator's stake.
//
// The proof requirement is deliberately unconditional. RequireEvidenceProof is
// bound to cfg.RequireSignedVotes, which defaults off and is pinned false by
// both shipped bring-up scripts, so a config-gated check would leave this open
// on every default deployment.
func TestEvidenceGossip_ProoflessAccusationsCannotSlash(t *testing.T) {
	cases := []struct {
		name string
		ev   chain.ConsensusEvidence
	}{
		{
			name: "equivocation with no proof",
			ev: chain.ConsensusEvidence{
				Type: chain.EvidenceEquivocation, Validator: "v1",
				Height: 9, Round: 1,
				BlockHashes: []string{"attacker-a", "attacker-b"},
				Details:     "peer says so", Timestamp: time.Now(),
			},
		},
		{
			name: "invalid_vote, which has no proof mechanism at all",
			ev: chain.ConsensusEvidence{
				Type: chain.EvidenceInvalidVote, Validator: "v1",
				Height: 9, Round: 1,
				Details: "attacker says so", Timestamp: time.Now(),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			em, vs := chainEvidenceManager(t)
			eg := NewEvidenceGossipIngress(em, NewReputationTracker(DefaultReputationConfig()), DefaultEvidenceGossipConfig())

			v, ok := vs.GetValidator("v1")
			if !ok {
				t.Fatal("v1 is not registered")
			}
			before := *v // GetValidator returns live state; copy it.

			payload, err := json.Marshal(tc.ev)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			// t.Error, not t.Fatal: the assertions below are the ones that
			// matter -- whether the bond actually moved -- and they must run
			// even when the refusal itself regresses.
			if err := eg.HandlePeerMessage("attacker", payload); err == nil {
				t.Error("an unproven accusation from a peer must be refused")
			}

			after, ok := vs.GetValidator("v1")
			if !ok {
				t.Fatal("v1 vanished from the validator set")
			}
			if after.Stake != before.Stake {
				t.Errorf("stake moved %v -> %v on an unproven peer accusation", before.Stake, after.Stake)
			}
			if after.Status != before.Status {
				t.Errorf("status moved %v -> %v on an unproven peer accusation", before.Status, after.Status)
			}
			if after.SlashCount != before.SlashCount {
				t.Errorf("slash count moved %d -> %d on an unproven peer accusation", before.SlashCount, after.SlashCount)
			}
		})
	}
}

// The guard must not deafen the node to real reports: equivocation carrying
// two conflicting votes signed by the accused is verifiable without trusting
// the sender, and is still acted on.
func TestEvidenceGossip_ProvenEquivocationStillSlashes(t *testing.T) {
	signer := crypto.NewDilithium()
	if signer == nil {
		t.Skip("ML-DSA signer unavailable in this build")
	}
	t.Cleanup(signer.Free)
	offender := chain.BFTValidatorAddress(signer.GetPublicKey())

	em, vs := chainEvidenceManager(t)
	if err := vs.Register(offender, 500); err != nil {
		t.Fatalf("register offender: %v", err)
	}
	v, ok := vs.GetValidator(offender)
	if !ok {
		t.Fatal("offender is not registered")
	}
	before := *v

	ev, err := chain.BuildEquivocationEvidence(
		offender,
		signedGossipExhibit(t, signer, offender, 9, 1, "value-a"),
		signedGossipExhibit(t, signer, offender, 9, 1, "value-b"),
	)
	if err != nil {
		t.Fatalf("building proof-carrying evidence: %v", err)
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	eg := NewEvidenceGossipIngress(em, NewReputationTracker(DefaultReputationConfig()), DefaultEvidenceGossipConfig())
	if err := eg.HandlePeerMessage("honest-peer", payload); err != nil {
		t.Fatalf("proven equivocation must still be accepted over gossip: %v", err)
	}

	after, ok := vs.GetValidator(offender)
	if !ok {
		t.Fatal("offender vanished from the validator set")
	}
	if after.Stake >= before.Stake {
		t.Errorf("proven equivocation should slash: %v -> %v", before.Stake, after.Stake)
	}
}

// A proof that does not verify against the accused is worth no more than no
// proof: an attacker can attach any bytes it likes.
func TestEvidenceGossip_ForgedProofIsRefused(t *testing.T) {
	signer := crypto.NewDilithium()
	if signer == nil {
		t.Skip("ML-DSA signer unavailable in this build")
	}
	t.Cleanup(signer.Free)
	attacker := chain.BFTValidatorAddress(signer.GetPublicKey())

	em, vs := chainEvidenceManager(t)
	v, ok := vs.GetValidator("v1")
	if !ok {
		t.Fatal("v1 is not registered")
	}
	before := *v

	// Real, self-consistent equivocation -- by the ATTACKER's own key --
	// re-labelled to accuse the victim.
	ev, err := chain.BuildEquivocationEvidence(
		attacker,
		signedGossipExhibit(t, signer, attacker, 9, 1, "value-a"),
		signedGossipExhibit(t, signer, attacker, 9, 1, "value-b"),
	)
	if err != nil {
		t.Fatalf("building attacker-signed evidence: %v", err)
	}
	ev.Validator = "v1"

	payload, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	eg := NewEvidenceGossipIngress(em, NewReputationTracker(DefaultReputationConfig()), DefaultEvidenceGossipConfig())
	if err := eg.HandlePeerMessage("attacker", payload); err == nil {
		t.Fatal("a proof signed by someone other than the accused must be refused")
	}

	after, ok := vs.GetValidator("v1")
	if !ok {
		t.Fatal("v1 vanished from the validator set")
	}
	if after.Stake != before.Stake || after.Status != before.Status {
		t.Errorf("victim moved %v/%v -> %v/%v on a proof it did not sign",
			before.Stake, before.Status, after.Stake, after.Status)
	}
}
