package chain

import (
	"crypto/ed25519"
	"encoding/hex"
	"math"
	"strings"
	"testing"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestConsensusMembershipFingerprintIsDeterministic(t *testing.T) {
	membership := testConsensusMembership(2, 3, 5, 7)
	first, err := membership.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	const wantFingerprint = "8492d9d25eca3578b9a7bda90b1807bf6c9918780eb8e5b068a5d7873b4993aa"
	if first != wantFingerprint {
		t.Fatalf("fingerprint = %s, want %s", first, wantFingerprint)
	}

	membership.Members[0], membership.Members[3] = membership.Members[3], membership.Members[0]
	second, err := membership.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("membership order changed fingerprint: %s != %s", first, second)
	}

	changes := []struct {
		name string
		edit func(*ConsensusMembership)
	}{
		{"network", func(m *ConsensusMembership) { m.NetworkID = "qsdm-other" }},
		{"effective-height", func(m *ConsensusMembership) { m.EffectiveHeight++ }},
		{"voting-power", func(m *ConsensusMembership) { m.Members[0].VotingPower++ }},
		{"peer", func(m *ConsensusMembership) { m.Members[0].P2PPeerID = testPeerID(99) }},
		{"member", func(m *ConsensusMembership) { m.Members[0] = testConsensusMember(88, 88, 2) }},
	}
	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			changed := membership
			changed.Members = append([]ConsensusMember(nil), membership.Members...)
			change.edit(&changed)
			got, err := changed.Fingerprint()
			if err != nil {
				t.Fatal(err)
			}
			if got == first {
				t.Fatalf("changing %s did not change fingerprint", change.name)
			}
		})
	}
}

func TestConsensusMembershipRejectsAmbiguousOrInvalidMembers(t *testing.T) {
	valid := testConsensusMembership(1, 1)
	tests := []struct {
		name string
		edit func(*ConsensusMembership)
		want string
	}{
		{
			name: "duplicate-address",
			edit: func(m *ConsensusMembership) { m.Members = append(m.Members, m.Members[0]) },
			want: "duplicates address",
		},
		{
			name: "duplicate-peer",
			edit: func(m *ConsensusMembership) {
				m.Members[1].P2PPeerID = m.Members[0].P2PPeerID
			},
			want: "duplicates p2p peer ID",
		},
		{
			name: "mismatched-address",
			edit: func(m *ConsensusMembership) { m.Members[0].Address = m.Members[1].Address },
			want: "does not match",
		},
		{
			name: "uppercase-address",
			edit: func(m *ConsensusMembership) { m.Members[0].Address = strings.ToUpper(m.Members[0].Address) },
			want: "lower-case hexadecimal",
		},
		{
			name: "short-public-key",
			edit: func(m *ConsensusMembership) { m.Members[0].ConsensusPublicKeyHex = "abcd" },
			want: "must decode to",
		},
		{
			name: "zero-voting-power",
			edit: func(m *ConsensusMembership) { m.Members[0].VotingPower = 0 },
			want: "greater than zero",
		},
		{
			name: "noncanonical-peer",
			edit: func(m *ConsensusMembership) { m.Members[0].P2PPeerID = " " + m.Members[0].P2PPeerID },
			want: "surrounding whitespace",
		},
		{
			name: "bad-network",
			edit: func(m *ConsensusMembership) { m.NetworkID = " qsdm-mainnet" },
			want: "network ID",
		},
		{
			name: "unsupported-schema",
			edit: func(m *ConsensusMembership) { m.SchemaVersion++ },
			want: "unsupported consensus membership schema",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			membership := cloneTestMembership(valid)
			test.edit(&membership)
			err := membership.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestConsensusMembershipRejectsVotingPowerOverflow(t *testing.T) {
	membership := testConsensusMembership(math.MaxUint64, 1)
	err := membership.Validate()
	if err == nil || !strings.Contains(err.Error(), "overflows") {
		t.Fatalf("Validate() error = %v, want overflow", err)
	}
}

func TestConsensusMembershipCanonicalMembersAreDefensive(t *testing.T) {
	membership := testConsensusMembership(1, 1)
	first := membership.CanonicalMembers()
	first[0].Address = "changed"
	second := membership.CanonicalMembers()
	if second[0].Address == "changed" {
		t.Fatal("CanonicalMembers returned aliases to membership members")
	}
}

func TestConsensusMembershipRequiredQuorumVotingPower(t *testing.T) {
	tests := []struct {
		powers []uint64
		want   uint64
	}{
		{powers: []uint64{1}, want: 1},
		{powers: []uint64{2}, want: 2},
		{powers: []uint64{1, 2}, want: 3},
		{powers: []uint64{1, 1, 1, 1}, want: 3},
		{powers: []uint64{2, 3, 5, 7}, want: 12},
	}
	for _, test := range tests {
		membership := testConsensusMembership(test.powers...)
		got, err := membership.RequiredQuorumVotingPower()
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("RequiredQuorumVotingPower(%v) = %d, want %d", test.powers, got, test.want)
		}
	}
}

func testConsensusMembership(powers ...uint64) ConsensusMembership {
	members := make([]ConsensusMember, len(powers))
	for index, power := range powers {
		members[index] = testConsensusMember(byte(index+1), byte(index+1), power)
	}
	return ConsensusMembership{
		SchemaVersion:   ConsensusMembershipSchemaVersion,
		NetworkID:       "qsdm-mainnet",
		EffectiveHeight: 42,
		Members:         members,
	}
}

func cloneTestMembership(value ConsensusMembership) ConsensusMembership {
	copy := value
	copy.Members = append([]ConsensusMember(nil), value.Members...)
	return copy
}

func testConsensusMember(publicSeed, peerSeed byte, votingPower uint64) ConsensusMember {
	publicKey := make([]byte, mldsa87PublicKeyLen)
	for index := range publicKey {
		publicKey[index] = publicSeed + byte(index)
	}
	return ConsensusMember{
		Address:               BFTValidatorAddress(publicKey),
		ConsensusPublicKeyHex: hex.EncodeToString(publicKey),
		P2PPeerID:             testPeerID(peerSeed),
		VotingPower:           votingPower,
	}
}

func testPeerID(seed byte) string {
	seedBytes := make([]byte, ed25519.SeedSize)
	for index := range seedBytes {
		seedBytes[index] = seed + byte(index)
	}
	privateKey := ed25519.NewKeyFromSeed(seedBytes)
	publicKey, err := libp2pcrypto.UnmarshalEd25519PublicKey(privateKey.Public().(ed25519.PublicKey))
	if err != nil {
		panic(err)
	}
	id, err := peer.IDFromPublicKey(publicKey)
	if err != nil {
		panic(err)
	}
	return id.String()
}
