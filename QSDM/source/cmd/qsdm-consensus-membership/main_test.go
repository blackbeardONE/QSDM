package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestRunInspectsValidatedMembership(t *testing.T) {
	membership := testMembership()
	raw, err := json.Marshal(membership)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "membership.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"--in", path, "--json"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var result membershipResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.MemberCount != len(membership.Members) || result.TotalVotingPower != 3 || result.RequiredQuorumVotingPower != 3 {
		t.Fatalf("unexpected membership result: %+v", result)
	}
	if len(result.MembershipFingerprint) != 64 {
		t.Fatalf("membership fingerprint = %q", result.MembershipFingerprint)
	}
}

func TestRunInspectsValidatedMembershipSchedule(t *testing.T) {
	first := testMembership()
	first.EffectiveHeight = 77
	second := testMembership()
	second.EffectiveHeight = 100
	second.Members[0] = testMember(8, 8, 3)
	document := chain.ConsensusMembershipScheduleFile{
		SchemaVersion: chain.ConsensusMembershipScheduleFileSchemaVersion,
		NetworkID:     first.NetworkID,
		Snapshots:     []chain.ConsensusMembership{second, first},
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "schedule.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"--schedule", path, "--json"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var result scheduleResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.NetworkID != first.NetworkID || result.SnapshotCount != 2 {
		t.Fatalf("unexpected schedule result: %+v", result)
	}
	if result.Snapshots[0].EffectiveHeight != 77 || result.Snapshots[1].EffectiveHeight != 100 {
		t.Fatalf("schedule order = %+v, want heights [77 100]", result.Snapshots)
	}
}
func TestReadMembershipRejectsUnknownFields(t *testing.T) {
	membership := testMembership()
	raw, err := json.Marshal(membership)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["unexpected"] = true
	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "membership.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readMembership(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("readMembership() error = %v, want unknown-field error", err)
	}
}

func TestRunExportsOnlyPublicConsensusIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "consensus-key.json")
	signer, _, err := chain.LoadOrCreateBFTSigner(path)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--identity-key", path, "--p2p-peer-id", testPeerID(45), "--json"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var result identityResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Address != signer.Address() {
		t.Fatalf("identity address = %s, want %s", result.Address, signer.Address())
	}
	if len(result.ConsensusPublicKeyHex) != 2592*2 {
		t.Fatalf("public key hex length = %d, want %d", len(result.ConsensusPublicKeyHex), 2592*2)
	}
	if strings.Contains(string(stdout.Bytes()), "private_key") {
		t.Fatal("identity output leaked a private key field")
	}
}

func TestRunRequiresExactlyOneMode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run(nil, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("run(nil) error = %v, want exactly-one error", err)
	}
	if err := run([]string{"--in", "membership.json", "--identity-key", "signer.json"}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "cannot be used together") {
		t.Fatalf("run(two modes) error = %v, want conflict error", err)
	}
}

func testMembership() chain.ConsensusMembership {
	first := testMember(1, 1, 1)
	second := testMember(2, 2, 2)
	return chain.ConsensusMembership{
		SchemaVersion:   chain.ConsensusMembershipSchemaVersion,
		NetworkID:       "qsdm-mainnet",
		EffectiveHeight: 77,
		Members:         []chain.ConsensusMember{first, second},
	}
}

func testMember(publicSeed, peerSeed byte, power uint64) chain.ConsensusMember {
	publicKey := make([]byte, 2592)
	for index := range publicKey {
		publicKey[index] = publicSeed + byte(index)
	}
	return chain.ConsensusMember{
		Address:               chain.BFTValidatorAddress(publicKey),
		ConsensusPublicKeyHex: hex.EncodeToString(publicKey),
		P2PPeerID:             testPeerID(peerSeed),
		VotingPower:           power,
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
