package chain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/crypto"
)

// BFT consensus message authentication.
//
// The vote-driven BFT wire format originally carried no signature at all:
// BFTWirePrevoteMsg / BFTWirePrecommitMsg named a validator in a plain
// string field and BFTExecutor.ApplyInbound fed that name straight into
// BFTConsensus.PreVote / PreCommit. Any peer subscribed to the BFT gossip
// topic could therefore forge prevotes and precommits for every validator in
// the set — i.e. manufacture a quorum — and equivocation evidence was
// likewise unsigned, so it could be fabricated against any node.
//
// Identity model: self-certifying, matching the transaction path. A
// validator's identity IS the hex SHA-256 of its ML-DSA-87 public key
// (see Ed25519WalletAddress / verifyMLDSA in txsig.go). That means vote
// verification needs no separate key registry and no key-distribution
// ceremony: the message carries the public key, the signature proves
// possession of the matching secret key, and the derived address must equal
// the validator name being claimed.

// ErrBFTUnsigned is returned when a consensus message carries no signature
// but signature verification is required.
var ErrBFTUnsigned = errors.New("chain: bft message is unsigned")

// ErrBFTBadSignature is returned when a consensus message's signature does
// not verify, or its public key does not derive the claimed validator.
var ErrBFTBadSignature = errors.New("chain: bft message signature invalid")

// BFTWireAuth carries the signer's ML-DSA-87 public key and signature over
// the message's canonical digest. Embedded in each vote message.
type BFTWireAuth struct {
	PublicKey []byte `json:"public_key,omitempty"`
	Signature []byte `json:"signature,omitempty"`
}

// Signed reports whether both halves of the authenticator are present.
func (a BFTWireAuth) Signed() bool {
	return len(a.PublicKey) > 0 && len(a.Signature) > 0
}

// BFTValidatorAddress derives the self-certifying validator identity for an
// ML-DSA-87 public key: SHA256(public_key) as lower-case hex. Identical to
// the wallet-address derivation, so one keypair serves both roles.
func BFTValidatorAddress(publicKey []byte) string {
	sum := sha256.Sum256(publicKey)
	return hex.EncodeToString(sum[:])
}

// bftVoteDigest builds the canonical pre-image signed for a vote message.
//
// Every field that changes the message's consensus meaning is covered:
// the kind (so a prevote cannot be replayed as a precommit), the height and
// round (so a vote cannot be replayed at another height), the signer, and
// the vote value. Fields are length-prefixed via the separator-free
// strconv encoding plus explicit '|' delimiters between typed segments, so
// no two distinct field tuples can produce the same pre-image.
func bftVoteDigest(kind string, height uint64, round uint32, signer, blockHash, bodyHash string) []byte {
	var b strings.Builder
	b.WriteString("qsdm/bft/v1|")
	b.WriteString(kind)
	b.WriteString("|h:")
	b.WriteString(strconv.FormatUint(height, 10))
	b.WriteString("|r:")
	b.WriteString(strconv.FormatUint(uint64(round), 10))
	b.WriteString("|s:")
	b.WriteString(strconv.Itoa(len(signer)))
	b.WriteString(":")
	b.WriteString(signer)
	b.WriteString("|v:")
	b.WriteString(strconv.Itoa(len(blockHash)))
	b.WriteString(":")
	b.WriteString(blockHash)
	b.WriteString("|b:")
	b.WriteString(strconv.Itoa(len(bodyHash)))
	b.WriteString(":")
	b.WriteString(bodyHash)
	sum := sha256.Sum256([]byte(b.String()))
	return sum[:]
}

// BFTSigner signs consensus messages. *crypto.Dilithium satisfies it.
type BFTSigner interface {
	Sign(message []byte) ([]byte, error)
	GetPublicKey() []byte
}

// signAuth produces the authenticator for a digest.
func signAuth(signer BFTSigner, digest []byte) (BFTWireAuth, error) {
	if signer == nil {
		return BFTWireAuth{}, errors.New("chain: nil BFT signer")
	}
	pub := signer.GetPublicKey()
	if len(pub) == 0 {
		return BFTWireAuth{}, errors.New("chain: BFT signer has no public key")
	}
	sig, err := signer.Sign(digest)
	if err != nil {
		return BFTWireAuth{}, fmt.Errorf("chain: sign bft message: %w", err)
	}
	return BFTWireAuth{PublicKey: pub, Signature: sig}, nil
}

// verifyAuth checks that the authenticator proves `claimedSigner` signed
// `digest`. The public key must derive the claimed identity, closing the
// gap where a valid signature by ANY key would authorise a vote attributed
// to someone else.
func verifyAuth(auth BFTWireAuth, digest []byte, claimedSigner string) error {
	if !auth.Signed() {
		return ErrBFTUnsigned
	}
	if got := BFTValidatorAddress(auth.PublicKey); !strings.EqualFold(got, claimedSigner) {
		return fmt.Errorf("%w: public key derives %s not %s", ErrBFTBadSignature, got, claimedSigner)
	}
	d := crypto.NewDilithiumVerifyOnly()
	if d == nil {
		return errors.New("chain: ML-DSA verifier unavailable for BFT signature check")
	}
	defer d.Free()
	ok, err := d.VerifyWithPublicKey(digest, auth.Signature, auth.PublicKey)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBFTBadSignature, err)
	}
	if !ok {
		return ErrBFTBadSignature
	}
	return nil
}

// proposeBodyHash returns a stable hash of the proposed block body, or the
// empty string when no body is attached. Including it in the digest stops an
// attacker swapping the block body under a validly-signed proposal header.
func proposeBodyHash(b *Block) string {
	if b == nil {
		return ""
	}
	return b.Hash
}

// SignPropose attaches an authenticator to a proposal.
func SignPropose(m *BFTWireProposeMsg, signer BFTSigner) error {
	if m == nil {
		return errors.New("chain: nil propose message")
	}
	auth, err := signAuth(signer, bftVoteDigest(
		BFTWirePropose, m.Height, m.Round, m.Proposer, m.BlockHash, proposeBodyHash(m.Block)))
	if err != nil {
		return err
	}
	m.Auth = auth
	return nil
}

// VerifyPropose checks a proposal's authenticator.
func VerifyPropose(m BFTWireProposeMsg) error {
	return verifyAuth(m.Auth, bftVoteDigest(
		BFTWirePropose, m.Height, m.Round, m.Proposer, m.BlockHash, proposeBodyHash(m.Block)), m.Proposer)
}

// SignPrevote attaches an authenticator to a prevote.
func SignPrevote(m *BFTWirePrevoteMsg, signer BFTSigner) error {
	if m == nil {
		return errors.New("chain: nil prevote message")
	}
	auth, err := signAuth(signer, bftVoteDigest(
		BFTWirePrevote, m.Height, m.Round, m.Validator, m.BlockHash, ""))
	if err != nil {
		return err
	}
	m.Auth = auth
	return nil
}

// VerifyPrevote checks a prevote's authenticator.
func VerifyPrevote(m BFTWirePrevoteMsg) error {
	return verifyAuth(m.Auth, bftVoteDigest(
		BFTWirePrevote, m.Height, m.Round, m.Validator, m.BlockHash, ""), m.Validator)
}

// SignPrecommit attaches an authenticator to a precommit.
func SignPrecommit(m *BFTWirePrecommitMsg, signer BFTSigner) error {
	if m == nil {
		return errors.New("chain: nil precommit message")
	}
	auth, err := signAuth(signer, bftVoteDigest(
		BFTWirePrecommit, m.Height, m.Round, m.Validator, m.BlockHash, ""))
	if err != nil {
		return err
	}
	m.Auth = auth
	return nil
}

// VerifyPrecommit checks a precommit's authenticator.
func VerifyPrecommit(m BFTWirePrecommitMsg) error {
	return verifyAuth(m.Auth, bftVoteDigest(
		BFTWirePrecommit, m.Height, m.Round, m.Validator, m.BlockHash, ""), m.Validator)
}

// equalAuth is a helper for tests and dedupe paths.
func equalAuth(a, b BFTWireAuth) bool {
	return bytes.Equal(a.PublicKey, b.PublicKey) && bytes.Equal(a.Signature, b.Signature)
}
