package chain

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
)

// Authentication for POL round certificates and prevote-lock proofs.
//
// BuildRoundCertificate emitted a CommitDigest — a plain SHA-256 over the
// commit payload — and nothing else. A digest is an integrity check against
// accidental corruption, not an authenticity check: anyone can recompute it
// over fabricated commits, so a certificate proved nothing about who
// produced it or whether the round it describes ever happened. The same held
// for PrevoteLockProof, whose verdict gates block production on followers.
//
// Certificates are authenticated with the same self-certifying identity as
// BFT votes (bft_sig.go): the signer's ML-DSA-87 public key travels with the
// signature, and the claimed signer must equal SHA256(public_key) in hex.

// ErrCertUnsigned is returned when a certificate carries no signature but
// signatures are required.
var ErrCertUnsigned = errors.New("chain: consensus certificate is unsigned")

// ErrCertBadSignature is returned when a certificate's signature does not
// verify against its declared signer.
var ErrCertBadSignature = errors.New("chain: consensus certificate signature invalid")

// requireSignedCertificates gates inbound enforcement, mirroring the vote
// rollout switch. Invalid signatures are always fatal.
var requireSignedCertificates atomic.Bool
var signedCertificateActivationHeight atomic.Uint64

// SetRequireSignedCertificates controls whether unsigned POL certificates
// and lock proofs are accepted. Enable once every validator emits signed
// certificates.
func SetRequireSignedCertificates(require bool) { requireSignedCertificates.Store(require) }

// RequireSignedCertificates reports the current policy.
func RequireSignedCertificates() bool { return requireSignedCertificates.Load() }

// SetSignedCertificateActivationHeight sets the first height where unsigned
// POL certificates and lock proofs are rejected. Zero means immediate.
func SetSignedCertificateActivationHeight(height uint64) {
	signedCertificateActivationHeight.Store(height)
}

// SignedCertificateActivationHeight reports the configured activation height.
func SignedCertificateActivationHeight() uint64 {
	return signedCertificateActivationHeight.Load()
}

// SignedCertificatesRequiredAt reports whether unsigned POL artifacts are
// forbidden at height.
func SignedCertificatesRequiredAt(height uint64) bool {
	if !RequireSignedCertificates() {
		return false
	}
	activation := SignedCertificateActivationHeight()
	return activation == 0 || height >= activation
}

// writeTagged appends a length-prefixed, tagged field so no two distinct
// field tuples can collide into one pre-image.
func writeTagged(b *strings.Builder, tag, val string) {
	b.WriteString("|")
	b.WriteString(tag)
	b.WriteString(":")
	b.WriteString(strconv.Itoa(len(val)))
	b.WriteString(":")
	b.WriteString(val)
}

// roundCertDigest is the canonical pre-image signed for a round certificate.
// Every field a verifier relies on is covered, including the validator set
// the certificate claims was active — otherwise a signature could be
// replayed against a different set.
func roundCertDigest(c *RoundCertificate) []byte {
	var b strings.Builder
	b.WriteString("qsdm/pol/cert/v1")
	writeTagged(&b, "h", strconv.FormatUint(c.Height, 10))
	writeTagged(&b, "r", strconv.FormatUint(uint64(c.Round), 10))
	writeTagged(&b, "p", c.Proposer)
	writeTagged(&b, "bh", c.BlockHash)
	writeTagged(&b, "cd", c.CommitDigest)
	writeTagged(&b, "cc", strconv.Itoa(c.CommitCount))
	writeTagged(&b, "nc", strconv.Itoa(c.NilCommitCount))
	vals := append([]string(nil), c.ValidatorSet...)
	sort.Strings(vals)
	writeTagged(&b, "vn", strconv.Itoa(len(vals)))
	for _, v := range vals {
		writeTagged(&b, "v", v)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return sum[:]
}

// polProofDigest is the canonical pre-image signed for a prevote-lock proof.
func polProofDigest(p *PrevoteLockProof) []byte {
	var b strings.Builder
	b.WriteString("qsdm/pol/proof/v1")
	writeTagged(&b, "h", strconv.FormatUint(p.Height, 10))
	writeTagged(&b, "r", strconv.FormatUint(uint64(p.Round), 10))
	writeTagged(&b, "lb", p.LockedBlockHash)
	writeTagged(&b, "cf", p.CarriedFromLock)
	// The prevotes ARE the proof. A signature that did not cover them would
	// let an attacker keep a valid signature while swapping in a fabricated
	// polka.
	votes := append([]BlockVote(nil), p.Prevotes...)
	sort.Slice(votes, func(i, j int) bool {
		if votes[i].Validator != votes[j].Validator {
			return votes[i].Validator < votes[j].Validator
		}
		return votes[i].BlockHash < votes[j].BlockHash
	})
	writeTagged(&b, "pn", strconv.Itoa(len(votes)))
	for _, v := range votes {
		writeTagged(&b, "pv", v.Validator)
		writeTagged(&b, "ph", v.BlockHash)
		writeTagged(&b, "pr", strconv.FormatUint(uint64(v.Round), 10))
	}
	sum := sha256.Sum256([]byte(b.String()))
	return sum[:]
}

// SignRoundCertificate authenticates a certificate as coming from signer.
func SignRoundCertificate(c *RoundCertificate, signer BFTSigner) error {
	if c == nil {
		return errors.New("chain: nil round certificate")
	}
	auth, err := signAuth(signer, roundCertDigest(c))
	if err != nil {
		return err
	}
	c.Signer = BFTValidatorAddress(auth.PublicKey)
	c.Auth = auth
	return nil
}

// VerifyRoundCertificate checks a certificate's authenticity. An attached
// signature is always verified; the policy flag governs unsigned ones.
func VerifyRoundCertificate(c *RoundCertificate) error {
	if c == nil {
		return errors.New("chain: nil round certificate")
	}
	if !c.Auth.Signed() {
		if SignedCertificatesRequiredAt(c.Height) {
			return ErrCertUnsigned
		}
		return nil
	}
	if err := verifyAuth(c.Auth, roundCertDigest(c), c.Signer); err != nil {
		return fmt.Errorf("%w: %v", ErrCertBadSignature, err)
	}
	return nil
}

// SignPrevoteLockProof authenticates a POL bundle as coming from signer.
func SignPrevoteLockProof(p *PrevoteLockProof, signer BFTSigner) error {
	if p == nil {
		return errors.New("chain: nil prevote lock proof")
	}
	auth, err := signAuth(signer, polProofDigest(p))
	if err != nil {
		return err
	}
	p.Signer = BFTValidatorAddress(auth.PublicKey)
	p.Auth = auth
	return nil
}

// VerifyPrevoteLockProof checks a POL bundle's authenticity.
func VerifyPrevoteLockProof(p *PrevoteLockProof) error {
	if p == nil {
		return errors.New("chain: nil prevote lock proof")
	}
	if !p.Auth.Signed() {
		if SignedCertificatesRequiredAt(p.Height) {
			return ErrCertUnsigned
		}
		return nil
	}
	if err := verifyAuth(p.Auth, polProofDigest(p), p.Signer); err != nil {
		return fmt.Errorf("%w: %v", ErrCertBadSignature, err)
	}
	return nil
}
