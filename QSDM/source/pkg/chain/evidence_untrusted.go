package chain

import (
	"errors"
	"fmt"
)

// Evidence that arrives from an untrusted source must prove itself.
//
// validateEvidence encodes what the *type* requires. It is not sufficient for
// evidence that arrives over gossip, because there the entire message is
// attacker-controlled: networking.EvidenceGossipIngress.HandlePeerMessage
// JSON-decodes a ConsensusEvidence off the wire and hands it to
// EvidenceManager.Process, which calls vs.Slash and sl.SlashDelegated. Ban,
// dedupe and per-peer rate-limit checks run first, but nothing authenticates
// the accusation itself, so "peer X says validator V equivocated" is worth
// exactly as much as "attacker says validator V equivocated".
//
// The distinction that matters is the source, not the type:
//
//   - Locally observed evidence is attributable by construction. The node saw
//     the offence itself, having verified the messages that carry it (see
//     BFTExecutor.maybeRecordProposerEquivocation, which since the attribution
//     fix only reports conflicts drawn from *signed* proposes).
//
//   - Gossiped evidence is a claim by a stranger. It is only worth acting on
//     if it carries a proof this node can re-verify without trusting the
//     sender -- which is precisely what EquivocationProof is for.
//
// So the gossip path demands a proof unconditionally. It deliberately does NOT
// consult RequireEvidenceProof: that flag is bound to cfg.RequireSignedVotes
// (cmd/qsdm/main.go) and defaults off, and both shipped bring-up scripts pin
// it false, so honouring it here would leave the hole open on every default
// deployment. The rollout concern that flag exists for -- a validator set
// where not every peer signs its votes yet -- is a reason to tolerate
// unproven *local* reports, never a reason to let a stranger take a
// validator's bond.

// ErrEvidenceUntrustedUnproven is returned when evidence from an untrusted
// source (gossip) carries no cryptographic proof binding the offence to the
// accused.
var ErrEvidenceUntrustedUnproven = errors.New("chain: evidence from an untrusted source carries no proof of the accusation")

// ValidateUntrustedEvidence reports whether evidence received from a peer may
// be processed. Callers must apply it BEFORE EvidenceManager.Process; it is
// additive to validateEvidence, not a replacement for it.
func ValidateUntrustedEvidence(ev ConsensusEvidence) error {
	switch ev.Type {
	case EvidenceForkWitness:
		// Records conflicting blocks and slashes nobody: Process returns
		// before reaching Slash. Nothing to prove, because nothing is taken.
		return nil

	case EvidenceEquivocation:
		if ev.Proof == nil {
			return fmt.Errorf("%w: equivocation from a peer must exhibit two conflicting signed votes", ErrEvidenceUntrustedUnproven)
		}
		if err := ev.Proof.Verify(ev.Validator); err != nil {
			return fmt.Errorf("equivocation proof does not establish the offence: %w", err)
		}
		if err := ev.Proof.VerifyBinding(ev); err != nil {
			return fmt.Errorf("equivocation proof does not match its envelope: %w", err)
		}
		return nil

	case EvidenceInvalidVote:
		// Rejected by validateEvidence as unprovable in principle -- there is
		// no proof type that expresses "cast an invalid vote". Restated here
		// so the untrusted path does not silently depend on that rejection
		// staying in place.
		return fmt.Errorf("%w: invalid_vote cannot be proven and is refused outright", ErrEvidenceUntrustedUnproven)

	default:
		// Unknown types must not become a bypass if one is added later
		// without revisiting this file.
		return fmt.Errorf("%w: unsupported evidence type %q from a peer", ErrEvidenceUntrustedUnproven, ev.Type)
	}
}
