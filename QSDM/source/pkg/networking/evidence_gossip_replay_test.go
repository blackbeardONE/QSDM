package networking

import (
	"encoding/json"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/crypto"
)

// A genuine proof is not a licence to slash repeatedly.
//
// EquivocationProof.Verify establishes that two exhibits are a real
// equivocation by the accused, but it was blind to the ConsensusEvidence
// wrapped around it -- nothing tied the envelope's Height, Round or
// BlockHashes to the votes inside. evidenceID hashes only those outer fields,
// so the same proof resubmitted under different height/round/hash values
// produced a fresh dedupe ID and slashed the accused again. ValidatorSet.Slash
// refuses only ValidatorExited, not ValidatorJailed, so the replay had no
// bound.
//
// The proof does not even have to be stolen: a validator's one genuine
// equivocation is public the moment it is reported, and every peer that saw it
// can then grind that validator and its delegators toward zero.
func TestEvidenceGossip_GenuineProofCannotBeReplayed(t *testing.T) {
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
	eg := NewEvidenceGossipIngress(em, NewReputationTracker(DefaultReputationConfig()), DefaultEvidenceGossipConfig())

	ev, err := chain.BuildEquivocationEvidence(
		offender,
		signedGossipExhibit(t, signer, offender, 9, 1, "value-a"),
		signedGossipExhibit(t, signer, offender, 9, 1, "value-b"),
	)
	if err != nil {
		t.Fatalf("building proof-carrying evidence: %v", err)
	}

	// The honest report lands once and slashes once. That is correct.
	payload, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := eg.HandlePeerMessage("honest-peer", payload); err != nil {
		t.Fatalf("the genuine report must be accepted: %v", err)
	}
	v, ok := vs.GetValidator(offender)
	if !ok {
		t.Fatal("offender vanished from the validator set")
	}
	if v.SlashCount != 1 {
		t.Fatalf("expected exactly one slash from the honest report, got %d", v.SlashCount)
	}

	// Now replay the SAME proof under rewritten envelope fields. Each variant
	// used to mint a fresh evidence ID and take another bite.
	replays := []struct {
		name   string
		mutate func(*chain.ConsensusEvidence)
	}{
		{"identical resubmission", func(e *chain.ConsensusEvidence) {}},
		{"rewritten height", func(e *chain.ConsensusEvidence) { e.Height = 10 }},
		{"rewritten round", func(e *chain.ConsensusEvidence) { e.Round = 2 }},
		{"rewritten block hashes", func(e *chain.ConsensusEvidence) {
			e.BlockHashes = []string{"invented-a", "invented-b"}
		}},
		{"padded details", func(e *chain.ConsensusEvidence) { e.Details = "grind attempt" }},
		{"extra block hash", func(e *chain.ConsensusEvidence) {
			e.BlockHashes = []string{"value-a", "value-b", "value-c"}
		}},
	}
	for _, r := range replays {
		t.Run(r.name, func(t *testing.T) {
			// Re-read before each case. Subtests share vs/em, so comparing
			// every case against one snapshot taken before the loop made a
			// single failing case appear to fail in every later case, with
			// identical numbers -- misattributing damage to variants that
			// were actually refused.
			v, ok := vs.GetValidator(offender)
			if !ok {
				t.Fatal("offender vanished from the validator set")
			}
			before := *v

			replay := ev
			replay.BlockHashes = append([]string(nil), ev.BlockHashes...)
			r.mutate(&replay)

			raw, err := json.Marshal(replay)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			// A DISTINCT peer per case, deliberately. The reputation tracker
			// bans a peer after enough protocol violations, and
			// HandlePeerMessage checks the ban first -- so reusing one peer ID
			// made later cases pass because the peer was banned rather than
			// because the replay was caught. This test passed for that reason
			// before the peer IDs were split, which is exactly the
			// form-over-substance failure the rubric names.
			peer := "attacker-" + r.name
			// t.Error, not t.Fatal: the stake assertions below are the point.
			if err := eg.HandlePeerMessage(peer, raw); err == nil {
				t.Error("a replayed proof must be refused")
			}

			after, ok := vs.GetValidator(offender)
			if !ok {
				t.Fatal("offender vanished from the validator set")
			}
			if after.Stake != before.Stake {
				t.Errorf("replay took more stake: %v -> %v", before.Stake, after.Stake)
			}
			if after.SlashCount != before.SlashCount {
				t.Errorf("replay slashed again: count %d -> %d", before.SlashCount, after.SlashCount)
			}
		})
	}
}
