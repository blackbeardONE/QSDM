package chain

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
)

// requireEvidenceProof, when set, makes a cryptographic proof mandatory for
// equivocation evidence. Defaults to false so a validator set mid-rollout
// (where not every peer signs its votes yet) can still report equivocation.
// An invalid proof is rejected either way.
var requireEvidenceProof atomic.Bool
var evidenceProofActivationHeight atomic.Uint64

// SetRequireEvidenceProof controls whether equivocation evidence must carry
// a verifiable proof. Enable once every validator signs its votes; until
// then, unproven accusations remain accepted and are forgeable.
func SetRequireEvidenceProof(require bool) { requireEvidenceProof.Store(require) }

// RequireEvidenceProof reports the current policy.
func RequireEvidenceProof() bool { return requireEvidenceProof.Load() }

// SetEvidenceProofActivationHeight sets the first height where bare
// equivocation accusations are rejected. Zero means immediate enforcement.
func SetEvidenceProofActivationHeight(height uint64) {
	evidenceProofActivationHeight.Store(height)
}

// EvidenceProofActivationHeight reports the configured activation height.
func EvidenceProofActivationHeight() uint64 { return evidenceProofActivationHeight.Load() }

func evidenceProofRequiredAt(height uint64) bool {
	if !RequireEvidenceProof() {
		return false
	}
	activation := EvidenceProofActivationHeight()
	return activation == 0 || height >= activation
}

// Cryptographic equivocation proofs.
//
// validateEvidence accepts equivocation evidence on the strength of asserted
// fields alone: a Validator name and two differing block hashes. Nothing tied
// those hashes to the accused, so any peer could submit
// {Validator: victim, BlockHashes: ["a","b"]} and drive a slash against a
// validator that never equivocated.
//
// Now that BFT votes are authenticated (bft_sig.go), equivocation can be
// *proven* rather than asserted: exhibit two conflicting votes of the same
// kind, at the same height and round, each carrying a valid signature from
// the accused's own key. That is self-contained evidence any node can
// re-verify without trusting the reporter.

// ErrEvidenceProofMissing is returned when proof is required but absent.
var ErrEvidenceProofMissing = errors.New("chain: equivocation evidence carries no cryptographic proof")

// ErrEvidenceProofInvalid is returned when a supplied proof does not
// establish equivocation by the accused validator.
var ErrEvidenceProofInvalid = errors.New("chain: equivocation proof is invalid")

// SignedVoteExhibit is one authenticated vote used as evidence.
type SignedVoteExhibit struct {
	Kind      string      `json:"kind"` // BFTWirePrevote or BFTWirePrecommit
	Height    uint64      `json:"height"`
	Round     uint32      `json:"round"`
	Validator string      `json:"validator"`
	BlockHash string      `json:"block_hash"`
	Auth      BFTWireAuth `json:"auth"`
}

// verify checks the exhibit's own signature.
func (x SignedVoteExhibit) verify() error {
	switch x.Kind {
	case BFTWirePrevote:
		return VerifyPrevote(BFTWirePrevoteMsg{
			Height: x.Height, Round: x.Round,
			Validator: x.Validator, BlockHash: x.BlockHash, Auth: x.Auth,
		})
	case BFTWirePrecommit:
		return VerifyPrecommit(BFTWirePrecommitMsg{
			Height: x.Height, Round: x.Round,
			Validator: x.Validator, BlockHash: x.BlockHash, Auth: x.Auth,
		})
	default:
		return fmt.Errorf("%w: unsupported exhibit kind %q", ErrEvidenceProofInvalid, x.Kind)
	}
}

// EquivocationProof is two conflicting authenticated votes from one
// validator at the same height and round.
type EquivocationProof struct {
	VoteA SignedVoteExhibit `json:"vote_a"`
	VoteB SignedVoteExhibit `json:"vote_b"`
}

// Verify establishes that `accused` equivocated. It is deliberately strict:
// every field that would let two honest votes look like equivocation must
// match, and the two vote values must genuinely differ.
func (p *EquivocationProof) Verify(accused string) error {
	if p == nil {
		return ErrEvidenceProofMissing
	}
	a, b := p.VoteA, p.VoteB

	if !strings.EqualFold(a.Validator, accused) || !strings.EqualFold(b.Validator, accused) {
		return fmt.Errorf("%w: exhibits do not both name the accused validator", ErrEvidenceProofInvalid)
	}
	if a.Kind != b.Kind {
		// A prevote and a precommit for different values is normal
		// protocol behaviour, not equivocation.
		return fmt.Errorf("%w: exhibits are different vote kinds", ErrEvidenceProofInvalid)
	}
	if a.Height != b.Height || a.Round != b.Round {
		// Voting differently at different heights/rounds is legal.
		return fmt.Errorf("%w: exhibits are not from the same height and round", ErrEvidenceProofInvalid)
	}
	if a.BlockHash == b.BlockHash {
		return fmt.Errorf("%w: exhibits agree, so no equivocation is shown", ErrEvidenceProofInvalid)
	}
	if equalAuth(a.Auth, b.Auth) {
		// Identical signatures over different values is impossible for an
		// honest signer and indicates a copy-paste forgery attempt.
		return fmt.Errorf("%w: exhibits share one signature", ErrEvidenceProofInvalid)
	}

	if err := a.verify(); err != nil {
		return fmt.Errorf("%w: first exhibit: %v", ErrEvidenceProofInvalid, err)
	}
	if err := b.verify(); err != nil {
		return fmt.Errorf("%w: second exhibit: %v", ErrEvidenceProofInvalid, err)
	}
	return nil
}

// BuildEquivocationEvidence assembles proof-carrying equivocation evidence
// from two observed conflicting votes, verifying the proof before returning
// so a caller cannot accidentally publish an unprovable accusation.
func BuildEquivocationEvidence(accused string, a, b SignedVoteExhibit) (ConsensusEvidence, error) {
	proof := &EquivocationProof{VoteA: a, VoteB: b}
	if err := proof.Verify(accused); err != nil {
		return ConsensusEvidence{}, err
	}
	return ConsensusEvidence{
		Type:        EvidenceEquivocation,
		Validator:   accused,
		Height:      a.Height,
		Round:       a.Round,
		BlockHashes: []string{a.BlockHash, b.BlockHash},
		Proof:       proof,
	}, nil
}
