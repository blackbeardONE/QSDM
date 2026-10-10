package chain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	consensusPeerBindingDomain    = "qsdm/consensus-peer-binding/v1"
	maxConsensusP2PPublicKeyBytes = 8 << 10
	maxConsensusP2PSignatureBytes = 16 << 10
)

var (
	// ErrConsensusPeerBindingRoot means a proof was made for a different
	// membership fingerprint than the supplied snapshot.
	ErrConsensusPeerBindingRoot = errors.New("chain: consensus peer binding membership root does not match")
	// ErrConsensusPeerBindingMember means the claimed address or peer identity
	// differs from the immutable membership snapshot.
	ErrConsensusPeerBindingMember = errors.New("chain: consensus peer binding member does not match membership")
	// ErrConsensusPeerBindingConsensusKey means the ML-DSA key in a proof is
	// not the exact public key committed in membership.
	ErrConsensusPeerBindingConsensusKey = errors.New("chain: consensus peer binding consensus key does not match membership")
	// ErrConsensusPeerBindingP2PKey means the supplied libp2p public key does
	// not derive the member's committed peer ID.
	ErrConsensusPeerBindingP2PKey = errors.New("chain: consensus peer binding libp2p key does not match membership")
	// ErrConsensusPeerBindingSignature means either possession signature is
	// malformed or invalid for the membership-specific challenge.
	ErrConsensusPeerBindingSignature = errors.New("chain: consensus peer binding signature is invalid")
)

// ConsensusPeerBinding proves that one operator controls both the ML-DSA
// consensus signer and the libp2p private key named by a membership member.
// It carries only public material and signatures, never either private key.
//
// The binding is intentionally a separate artifact instead of a new field in
// ConsensusMember. Membership schema version 1 has a fixed canonical byte
// representation; changing it would require a separate versioned transition.
type ConsensusPeerBinding struct {
	MembershipRoot  string      `json:"membership_root"`
	Address         string      `json:"address"`
	P2PPeerID       string      `json:"p2p_peer_id"`
	P2PPublicKeyHex string      `json:"p2p_public_key_hex"`
	ConsensusAuth   BFTWireAuth `json:"consensus_auth"`
	P2PSignatureHex string      `json:"p2p_signature_hex"`
}

// CreateConsensusPeerBinding creates a proof for a member of membership. The
// caller must supply both existing private keys. This function does not load,
// create, persist, or log either key.
func CreateConsensusPeerBinding(membership ConsensusMembership, address string, consensusSigner BFTSigner, p2pPrivateKey libp2pcrypto.PrivKey) (ConsensusPeerBinding, error) {
	member, root, err := consensusPeerBindingMember(membership, address)
	if err != nil {
		return ConsensusPeerBinding{}, err
	}
	if consensusSigner == nil {
		return ConsensusPeerBinding{}, errors.New("chain: consensus peer binding signer is nil")
	}
	memberConsensusKey, err := decodeCanonicalMembershipPublicKey(member.ConsensusPublicKeyHex)
	if err != nil {
		return ConsensusPeerBinding{}, err
	}
	if !bytes.Equal(consensusSigner.GetPublicKey(), memberConsensusKey) {
		return ConsensusPeerBinding{}, ErrConsensusPeerBindingConsensusKey
	}
	if p2pPrivateKey == nil {
		return ConsensusPeerBinding{}, errors.New("chain: consensus peer binding libp2p private key is nil")
	}
	p2pPublicKey := p2pPrivateKey.GetPublic()
	p2pPublicKeyBytes, err := libp2pcrypto.MarshalPublicKey(p2pPublicKey)
	if err != nil {
		return ConsensusPeerBinding{}, fmt.Errorf("chain: marshal consensus peer binding libp2p public key: %w", err)
	}
	if err := validateConsensusPeerBindingPublicKey(member.P2PPeerID, p2pPublicKeyBytes); err != nil {
		return ConsensusPeerBinding{}, err
	}
	digest, err := consensusPeerBindingDigest(root, member, p2pPublicKeyBytes)
	if err != nil {
		return ConsensusPeerBinding{}, err
	}
	consensusAuth, err := signAuth(consensusSigner, digest)
	if err != nil {
		return ConsensusPeerBinding{}, err
	}
	p2pSignature, err := p2pPrivateKey.Sign(digest)
	if err != nil {
		return ConsensusPeerBinding{}, fmt.Errorf("chain: sign consensus peer binding with libp2p key: %w", err)
	}
	if len(p2pSignature) == 0 || len(p2pSignature) > maxConsensusP2PSignatureBytes {
		return ConsensusPeerBinding{}, fmt.Errorf("%w: invalid libp2p signature length", ErrConsensusPeerBindingSignature)
	}
	return ConsensusPeerBinding{
		MembershipRoot:  root,
		Address:         member.Address,
		P2PPeerID:       member.P2PPeerID,
		P2PPublicKeyHex: hex.EncodeToString(p2pPublicKeyBytes),
		ConsensusAuth:   consensusAuth,
		P2PSignatureHex: hex.EncodeToString(p2pSignature),
	}, nil
}

// VerifyConsensusPeerBinding checks both possession signatures against the
// supplied immutable membership snapshot. It does not assert the libp2p
// transport peer that relayed a vote; relays are allowed to differ from the
// validator that originated a signed message.
func VerifyConsensusPeerBinding(membership ConsensusMembership, binding ConsensusPeerBinding) error {
	member, root, err := consensusPeerBindingMember(membership, binding.Address)
	if err != nil {
		return err
	}
	if binding.MembershipRoot != root {
		return ErrConsensusPeerBindingRoot
	}
	if binding.P2PPeerID != member.P2PPeerID {
		return ErrConsensusPeerBindingMember
	}
	p2pPublicKeyBytes, err := decodeCanonicalConsensusPeerBindingHex(binding.P2PPublicKeyHex, maxConsensusP2PPublicKeyBytes, "libp2p public key", ErrConsensusPeerBindingP2PKey)
	if err != nil {
		return err
	}
	if err := validateConsensusPeerBindingPublicKey(member.P2PPeerID, p2pPublicKeyBytes); err != nil {
		return err
	}
	digest, err := consensusPeerBindingDigest(root, member, p2pPublicKeyBytes)
	if err != nil {
		return err
	}
	memberConsensusKey, err := decodeCanonicalMembershipPublicKey(member.ConsensusPublicKeyHex)
	if err != nil {
		return err
	}
	if !bytes.Equal(binding.ConsensusAuth.PublicKey, memberConsensusKey) {
		return ErrConsensusPeerBindingConsensusKey
	}
	if err := verifyAuth(binding.ConsensusAuth, digest, member.Address); err != nil {
		return fmt.Errorf("%w: consensus signer: %v", ErrConsensusPeerBindingSignature, err)
	}
	p2pSignature, err := decodeCanonicalConsensusPeerBindingHex(binding.P2PSignatureHex, maxConsensusP2PSignatureBytes, "libp2p signature", ErrConsensusPeerBindingSignature)
	if err != nil {
		return err
	}
	p2pPublicKey, err := libp2pcrypto.UnmarshalPublicKey(p2pPublicKeyBytes)
	if err != nil {
		return fmt.Errorf("%w: decode libp2p public key: %v", ErrConsensusPeerBindingP2PKey, err)
	}
	valid, err := p2pPublicKey.Verify(digest, p2pSignature)
	if err != nil || !valid {
		return fmt.Errorf("%w: libp2p signature", ErrConsensusPeerBindingSignature)
	}
	return nil
}

func consensusPeerBindingMember(membership ConsensusMembership, address string) (ConsensusMember, string, error) {
	if err := membership.Validate(); err != nil {
		return ConsensusMember{}, "", fmt.Errorf("chain: validate consensus peer binding membership: %w", err)
	}
	root, err := membership.Fingerprint()
	if err != nil {
		return ConsensusMember{}, "", fmt.Errorf("chain: fingerprint consensus peer binding membership: %w", err)
	}
	for _, member := range membership.Members {
		if member.Address == address {
			return member, root, nil
		}
	}
	return ConsensusMember{}, "", fmt.Errorf("%w: address %q", ErrConsensusPeerBindingMember, address)
}

func consensusPeerBindingDigest(root string, member ConsensusMember, p2pPublicKey []byte) ([]byte, error) {
	consensusPublicKey, err := decodeCanonicalMembershipPublicKey(member.ConsensusPublicKeyHex)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteString(consensusPeerBindingDomain)
	out.WriteByte(0)
	writeMembershipString(&out, root)
	writeMembershipString(&out, member.Address)
	writeMembershipBytes(&out, consensusPublicKey)
	writeMembershipString(&out, member.P2PPeerID)
	writeMembershipBytes(&out, p2pPublicKey)
	sum := sha256.Sum256(out.Bytes())
	return sum[:], nil
}

func validateConsensusPeerBindingPublicKey(expectedPeerID string, encodedPublicKey []byte) error {
	if len(encodedPublicKey) == 0 || len(encodedPublicKey) > maxConsensusP2PPublicKeyBytes {
		return fmt.Errorf("%w: invalid libp2p public key length", ErrConsensusPeerBindingP2PKey)
	}
	publicKey, err := libp2pcrypto.UnmarshalPublicKey(encodedPublicKey)
	if err != nil {
		return fmt.Errorf("%w: decode libp2p public key: %v", ErrConsensusPeerBindingP2PKey, err)
	}
	derivedPeerID, err := peer.IDFromPublicKey(publicKey)
	if err != nil {
		return fmt.Errorf("%w: derive libp2p peer ID: %v", ErrConsensusPeerBindingP2PKey, err)
	}
	if derivedPeerID.String() != expectedPeerID {
		return ErrConsensusPeerBindingP2PKey
	}
	return nil
}

func decodeCanonicalConsensusPeerBindingHex(value string, maxBytes int, label string, kind error) ([]byte, error) {
	if value == "" || strings.TrimSpace(value) != value || value != strings.ToLower(value) {
		return nil, fmt.Errorf("%w: %s must be lower-case hexadecimal without surrounding whitespace", kind, label)
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) == 0 || len(decoded) > maxBytes || hex.EncodeToString(decoded) != value {
		return nil, fmt.Errorf("%w: %s is malformed", kind, label)
	}
	return decoded, nil
}
