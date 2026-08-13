package chain

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
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

// ErrEvidenceInvalidVoteUnprovable is returned for invalid_vote evidence,
// which is rejected unconditionally. The accusation carries no verifiable
// witness -- an invalid vote is simply dropped by its receiver, leaving
// nothing a third party can re-check -- and the only proof type in this
// package establishes equivocation, a different offence. See the
// EvidenceInvalidVote branch in validateEvidence.
var ErrEvidenceInvalidVoteUnprovable = errors.New("chain: invalid_vote evidence is unprovable and is not accepted")

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

// VerifyBinding checks that the proof actually proves what the surrounding
// envelope claims.
//
// Verify establishes that the two exhibits are a genuine equivocation by the
// accused, but it is blind to the ConsensusEvidence wrapped around it: nothing
// tied the envelope's Height, Round or BlockHashes to the votes inside. Since
// evidenceID hashes only those outer fields, one genuine proof could be
// resubmitted under arbitrary height/round/hash values, each variant producing
// a fresh dedupe ID and each one slashing the accused again. ValidatorSet.Slash
// refuses only ValidatorExited, not ValidatorJailed, so the replay had no
// bound: a peer holding a single real proof -- or the accused's one genuine
// equivocation, which is public by definition once reported -- could grind that
// validator and its delegators toward zero, 64 submissions per minute per peer
// identity, using nothing but arithmetic on the envelope fields.
//
// Callers must apply this wherever a proof is verified. Verify alone is not
// sufficient.
func (p *EquivocationProof) VerifyBinding(ev ConsensusEvidence) error {
	if p == nil {
		return ErrEvidenceProofMissing
	}
	if ev.Height != p.VoteA.Height || ev.Round != p.VoteA.Round {
		return fmt.Errorf("%w: envelope claims height %d round %d, proof shows height %d round %d",
			ErrEvidenceProofInvalid, ev.Height, ev.Round, p.VoteA.Height, p.VoteA.Round)
	}
	// Order does not matter -- evidenceID sorts the hashes -- but the set must
	// be exactly the two values the accused signed. Anything else lets the
	// envelope assert a conflict the proof does not show.
	want := []string{p.VoteA.BlockHash, p.VoteB.BlockHash}
	got := append([]string(nil), ev.BlockHashes...)
	sort.Strings(want)
	sort.Strings(got)
	if len(got) != len(want) {
		return fmt.Errorf("%w: envelope carries %d block hashes, proof shows 2",
			ErrEvidenceProofInvalid, len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			return fmt.Errorf("%w: envelope block hashes do not match the signed votes",
				ErrEvidenceProofInvalid)
		}
	}
	return nil
}

// fingerprint returns a stable digest of the OFFENCE the proof establishes.
//
// It digests each exhibit's semantic content -- kind, height, round, validator
// and block hash -- and combines the two in sorted order. It deliberately does
// NOT digest the signature bytes.
//
// Signature bytes are not canonical. This build signs with the randomized
// variant of FIPS 204 6.1 (pkg/crypto/dilithium_circl.go:129, randomized=true),
// so signing one vote twice with one key yields two different, both-valid
// signatures. An earlier version of this function digested Auth.PublicKey and
// Auth.Signature, which meant a semantically identical pair of exhibits, freshly
// signed, produced a different fingerprint, a different evidenceID, and a second
// slash of the same offence: 1000 -> 950 -> 902.5. Keying on what the votes SAY
// rather than on the bytes that authenticate them makes one offence one
// identity, however many times it is signed.
//
// Sorting the two exhibit digests keeps exhibit order out of the identity:
// Verify and VerifyBinding are both symmetric in A/B, and two honest reporters
// who observed one equivocation in opposite orders must agree on its identity.
// Same reasoning as pkg/mining/slashing/doublemining, whose encoder
// canonicalises (ProofA, ProofB) lexicographically.
//
// Collision safety does not rest on this digest alone: evidence only reaches
// dedupe after Verify has checked real signatures from the accused, so an
// attacker cannot manufacture a colliding proof without that validator's key.
func (p *EquivocationProof) fingerprint() string {
	if p == nil {
		return ""
	}
	digests := make([]string, 0, 2)
	for _, x := range []SignedVoteExhibit{p.VoteA, p.VoteB} {
		h := sha256.New()
		// Length-prefixed so no field boundary can be shifted to collide,
		// e.g. a validator name absorbing the start of a block hash.
		writeLenPrefixed(h, []byte(x.Kind))
		writeLenPrefixed(h, []byte(strings.ToLower(x.Validator)))
		writeUint64Prefixed(h, x.Height)
		writeUint64Prefixed(h, uint64(x.Round))
		writeLenPrefixed(h, []byte(x.BlockHash))
		digests = append(digests, hex.EncodeToString(h.Sum(nil)))
	}
	sort.Strings(digests)

	outer := sha256.New()
	for _, d := range digests {
		writeLenPrefixed(outer, []byte(d))
	}
	return hex.EncodeToString(outer.Sum(nil))
}

func writeUint64Prefixed(h io.Writer, v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	writeLenPrefixed(h, b[:])
}

func writeLenPrefixed(h io.Writer, b []byte) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(b)))
	_, _ = h.Write(n[:])
	_, _ = h.Write(b)
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
