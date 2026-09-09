package chain

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	// ConsensusMembershipSchemaVersion identifies the stable, deterministic
	// membership artifact format. New formats must use a new version rather
	// than changing the byte representation of this version.
	ConsensusMembershipSchemaVersion uint32 = 1

	consensusMembershipDomain       = "qsdm/consensus-membership/v1"
	maxConsensusNetworkIDBytes      = 128
	maxConsensusP2PPeerIDBytes      = 256
	consensusMembershipAddressBytes = sha256.Size
)

// ConsensusMember is a validator's public identity in a future consensus
// membership snapshot. It contains no private signing material.
//
// VotingPower is an integer by design. Consensus membership must not use
// floating-point stake values, because every validator must derive the same
// quorum threshold from the same committed artifact.
type ConsensusMember struct {
	Address               string `json:"address"`
	ConsensusPublicKeyHex string `json:"consensus_public_key_hex"`
	P2PPeerID             string `json:"p2p_peer_id"`
	VotingPower           uint64 `json:"voting_power"`
}

// ConsensusMembership is a deterministic, public description of a validator
// set at a particular chain height.
//
// This type is a foundation only. It is not consulted by the existing runtime
// validator set or block producer path until a later, chain-committed
// membership transition is implemented.
type ConsensusMembership struct {
	SchemaVersion   uint32            `json:"schema_version"`
	NetworkID       string            `json:"network_id"`
	EffectiveHeight uint64            `json:"effective_height"`
	Members         []ConsensusMember `json:"members"`
}

// Validate checks that a membership can be represented unambiguously and that
// each member's public identity is self-consistent.
func (m ConsensusMembership) Validate() error {
	_, err := m.validatedTotalVotingPower()
	return err
}

// CanonicalMembers returns a sorted, defensive copy of the members. Sorting
// by the self-certifying address gives every implementation the same order.
// Call Validate before treating the result as an authorized membership.
func (m ConsensusMembership) CanonicalMembers() []ConsensusMember {
	members := append([]ConsensusMember(nil), m.Members...)
	sort.Slice(members, func(i, j int) bool {
		return members[i].Address < members[j].Address
	})
	return members
}

// TotalVotingPower returns the validated total integer voting power.
func (m ConsensusMembership) TotalVotingPower() (uint64, error) {
	return m.validatedTotalVotingPower()
}

// RequiredQuorumVotingPower returns the smallest integer strictly greater
// than two thirds of the total voting power. A three-validator set with equal
// power therefore needs all three votes; a four-validator set needs three.
func (m ConsensusMembership) RequiredQuorumVotingPower() (uint64, error) {
	total, err := m.validatedTotalVotingPower()
	if err != nil {
		return 0, err
	}
	third := total / 3
	if total%3 != 0 {
		third++
	}
	return total - third + 1, nil
}

// CanonicalBytes returns the domain-separated binary representation used for
// a membership fingerprint. It validates before encoding so no ambiguous or
// malformed membership can acquire a root.
func (m ConsensusMembership) CanonicalBytes() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}

	var out bytes.Buffer
	out.WriteString(consensusMembershipDomain)
	out.WriteByte(0)
	writeMembershipUint32(&out, m.SchemaVersion)
	writeMembershipString(&out, m.NetworkID)
	writeMembershipUint64(&out, m.EffectiveHeight)
	members := m.CanonicalMembers()
	writeMembershipUint32(&out, uint32(len(members)))
	for _, member := range members {
		publicKey, _ := hex.DecodeString(member.ConsensusPublicKeyHex)
		writeMembershipString(&out, member.Address)
		writeMembershipBytes(&out, publicKey)
		writeMembershipString(&out, member.P2PPeerID)
		writeMembershipUint64(&out, member.VotingPower)
	}
	return out.Bytes(), nil
}

// Fingerprint returns the SHA-256 hash of CanonicalBytes as lower-case hex.
func (m ConsensusMembership) Fingerprint() (string, error) {
	canonical, err := m.CanonicalBytes()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func (m ConsensusMembership) validatedTotalVotingPower() (uint64, error) {
	if m.SchemaVersion != ConsensusMembershipSchemaVersion {
		return 0, fmt.Errorf("chain: unsupported consensus membership schema version %d", m.SchemaVersion)
	}
	if m.NetworkID == "" || strings.TrimSpace(m.NetworkID) != m.NetworkID {
		return 0, fmt.Errorf("chain: consensus membership network ID is empty or contains surrounding whitespace")
	}
	if len(m.NetworkID) > maxConsensusNetworkIDBytes {
		return 0, fmt.Errorf("chain: consensus membership network ID exceeds %d bytes", maxConsensusNetworkIDBytes)
	}
	if len(m.Members) == 0 {
		return 0, fmt.Errorf("chain: consensus membership has no members")
	}

	seenAddresses := make(map[string]struct{}, len(m.Members))
	seenPublicKeys := make(map[string]struct{}, len(m.Members))
	seenPeers := make(map[string]struct{}, len(m.Members))
	var total uint64
	for index, member := range m.Members {
		address, _, peerID, err := validateConsensusMember(member)
		if err != nil {
			return 0, fmt.Errorf("chain: consensus membership member %d: %w", index, err)
		}
		if _, exists := seenAddresses[address]; exists {
			return 0, fmt.Errorf("chain: consensus membership member %d duplicates address %q", index, address)
		}
		if _, exists := seenPublicKeys[member.ConsensusPublicKeyHex]; exists {
			return 0, fmt.Errorf("chain: consensus membership member %d duplicates consensus public key", index)
		}
		if _, exists := seenPeers[peerID]; exists {
			return 0, fmt.Errorf("chain: consensus membership member %d duplicates p2p peer ID %q", index, peerID)
		}
		if total > ^uint64(0)-member.VotingPower {
			return 0, fmt.Errorf("chain: consensus membership voting power overflows uint64")
		}
		seenAddresses[address] = struct{}{}
		seenPublicKeys[member.ConsensusPublicKeyHex] = struct{}{}
		seenPeers[peerID] = struct{}{}
		total += member.VotingPower
	}
	return total, nil
}

func validateConsensusMember(member ConsensusMember) (string, []byte, string, error) {
	address, err := decodeCanonicalMembershipAddress(member.Address)
	if err != nil {
		return "", nil, "", err
	}
	publicKey, err := decodeCanonicalMembershipPublicKey(member.ConsensusPublicKeyHex)
	if err != nil {
		return "", nil, "", err
	}
	if derived := BFTValidatorAddress(publicKey); derived != address {
		return "", nil, "", fmt.Errorf("address %q does not match the consensus public key", member.Address)
	}
	if member.VotingPower == 0 {
		return "", nil, "", fmt.Errorf("voting power must be greater than zero")
	}
	if member.P2PPeerID == "" || strings.TrimSpace(member.P2PPeerID) != member.P2PPeerID {
		return "", nil, "", fmt.Errorf("p2p peer ID is empty or contains surrounding whitespace")
	}
	if len(member.P2PPeerID) > maxConsensusP2PPeerIDBytes {
		return "", nil, "", fmt.Errorf("p2p peer ID exceeds %d bytes", maxConsensusP2PPeerIDBytes)
	}
	decodedPeer, err := peer.Decode(member.P2PPeerID)
	if err != nil {
		return "", nil, "", fmt.Errorf("invalid p2p peer ID: %w", err)
	}
	if decodedPeer.String() != member.P2PPeerID {
		return "", nil, "", fmt.Errorf("p2p peer ID is not canonical")
	}
	return address, publicKey, decodedPeer.String(), nil
}

func decodeCanonicalMembershipAddress(value string) (string, error) {
	if value == "" || strings.TrimSpace(value) != value || value != strings.ToLower(value) {
		return "", fmt.Errorf("address must be lower-case hexadecimal without surrounding whitespace")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != consensusMembershipAddressBytes || hex.EncodeToString(decoded) != value {
		return "", fmt.Errorf("address must be a %d-byte lower-case hexadecimal value", consensusMembershipAddressBytes)
	}
	return value, nil
}

func decodeCanonicalMembershipPublicKey(value string) ([]byte, error) {
	if value == "" || strings.TrimSpace(value) != value || value != strings.ToLower(value) {
		return nil, fmt.Errorf("consensus public key must be lower-case hexadecimal without surrounding whitespace")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != mldsa87PublicKeyLen || hex.EncodeToString(decoded) != value {
		return nil, fmt.Errorf("consensus public key must decode to %d bytes", mldsa87PublicKeyLen)
	}
	return decoded, nil
}

func writeMembershipUint32(out *bytes.Buffer, value uint32) {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	out.Write(encoded[:])
}

func writeMembershipUint64(out *bytes.Buffer, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	out.Write(encoded[:])
}

func writeMembershipString(out *bytes.Buffer, value string) {
	writeMembershipBytes(out, []byte(value))
}

func writeMembershipBytes(out *bytes.Buffer, value []byte) {
	writeMembershipUint32(out, uint32(len(value)))
	out.Write(value)
}
