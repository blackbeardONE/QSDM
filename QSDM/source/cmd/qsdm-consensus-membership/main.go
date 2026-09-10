// Command qsdm-consensus-membership validates deterministic consensus
// membership artifacts and exports a validator's public consensus identity.
// It does not enroll validators, modify chain state, or start block production.
package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/libp2p/go-libp2p/core/peer"
)

type membershipResult struct {
	SchemaVersion             uint32 `json:"schema_version"`
	NetworkID                 string `json:"network_id"`
	EffectiveHeight           uint64 `json:"effective_height"`
	MemberCount               int    `json:"member_count"`
	TotalVotingPower          uint64 `json:"total_voting_power"`
	RequiredQuorumVotingPower uint64 `json:"required_quorum_voting_power"`
	MembershipFingerprint     string `json:"membership_fingerprint"`
}

type scheduleResult struct {
	SchemaVersion uint32             `json:"schema_version"`
	NetworkID     string             `json:"network_id"`
	SnapshotCount int                `json:"snapshot_count"`
	Snapshots     []membershipResult `json:"snapshots"`
}
type identityResult struct {
	Address               string `json:"address"`
	ConsensusPublicKeyHex string `json:"consensus_public_key_hex"`
	P2PPeerID             string `json:"p2p_peer_id"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "qsdm-consensus-membership: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("qsdm-consensus-membership", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inputPath := flags.String("in", "", "Membership JSON file to validate.")
	schedulePath := flags.String("schedule", "", "Membership schedule JSON file to validate.")
	identityKeyPath := flags.String("identity-key", "", "Existing private consensus signer key to inspect without creating one.")
	p2pPeerID := flags.String("p2p-peer-id", "", "Canonical libp2p peer ID for --identity-key output.")
	jsonOutput := flags.Bool("json", false, "Write JSON output.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}

	inputPathValue := strings.TrimSpace(*inputPath)
	schedulePathValue := strings.TrimSpace(*schedulePath)
	identityKeyPathValue := strings.TrimSpace(*identityKeyPath)
	modeCount := 0
	for _, path := range []string{inputPathValue, schedulePathValue, identityKeyPathValue} {
		if path != "" {
			modeCount++
		}
	}
	switch {
	case modeCount == 0:
		return errors.New("provide exactly one of --in, --schedule, or --identity-key")
	case modeCount > 1:
		return errors.New("--in, --schedule, and --identity-key cannot be used together")
	case inputPathValue != "":
		return inspectMembership(inputPathValue, *jsonOutput, stdout)
	case schedulePathValue != "":
		return inspectSchedule(schedulePathValue, *jsonOutput, stdout)
	default:
		return inspectIdentity(identityKeyPathValue, strings.TrimSpace(*p2pPeerID), *jsonOutput, stdout)
	}
}

func inspectMembership(path string, jsonOutput bool, stdout io.Writer) error {
	membership, err := readMembership(path)
	if err != nil {
		return err
	}
	total, err := membership.TotalVotingPower()
	if err != nil {
		return err
	}
	quorum, err := membership.RequiredQuorumVotingPower()
	if err != nil {
		return err
	}
	fingerprint, err := membership.Fingerprint()
	if err != nil {
		return err
	}
	result := membershipResult{
		SchemaVersion:             membership.SchemaVersion,
		NetworkID:                 membership.NetworkID,
		EffectiveHeight:           membership.EffectiveHeight,
		MemberCount:               len(membership.Members),
		TotalVotingPower:          total,
		RequiredQuorumVotingPower: quorum,
		MembershipFingerprint:     fingerprint,
	}
	if jsonOutput {
		return json.NewEncoder(stdout).Encode(result)
	}
	_, err = fmt.Fprintf(stdout,
		"Membership is valid.\nNetwork: %s\nEffective height: %d\nMembers: %d\nTotal voting power: %d\nRequired quorum voting power: %d\nFingerprint: %s\n",
		result.NetworkID,
		result.EffectiveHeight,
		result.MemberCount,
		result.TotalVotingPower,
		result.RequiredQuorumVotingPower,
		result.MembershipFingerprint,
	)
	return err
}

func inspectSchedule(path string, jsonOutput bool, stdout io.Writer) error {
	schedule, err := chain.LoadConsensusMembershipScheduleFile(path)
	if err != nil {
		return err
	}
	snapshots := schedule.Snapshots()
	result := scheduleResult{
		SchemaVersion: chain.ConsensusMembershipScheduleFileSchemaVersion,
		NetworkID:     schedule.NetworkID(),
		SnapshotCount: len(snapshots),
		Snapshots:     make([]membershipResult, 0, len(snapshots)),
	}
	for _, membership := range snapshots {
		total, err := membership.TotalVotingPower()
		if err != nil {
			return err
		}
		quorum, err := membership.RequiredQuorumVotingPower()
		if err != nil {
			return err
		}
		fingerprint, err := membership.Fingerprint()
		if err != nil {
			return err
		}
		result.Snapshots = append(result.Snapshots, membershipResult{
			SchemaVersion:             membership.SchemaVersion,
			NetworkID:                 membership.NetworkID,
			EffectiveHeight:           membership.EffectiveHeight,
			MemberCount:               len(membership.Members),
			TotalVotingPower:          total,
			RequiredQuorumVotingPower: quorum,
			MembershipFingerprint:     fingerprint,
		})
	}
	if jsonOutput {
		return json.NewEncoder(stdout).Encode(result)
	}
	if _, err := fmt.Fprintf(stdout, "Membership schedule is valid.\nNetwork: %s\nSnapshots: %d\n", result.NetworkID, result.SnapshotCount); err != nil {
		return err
	}
	for _, snapshot := range result.Snapshots {
		if _, err := fmt.Fprintf(stdout, "- Height %d: %d members, quorum %d, fingerprint %s\n", snapshot.EffectiveHeight, snapshot.MemberCount, snapshot.RequiredQuorumVotingPower, snapshot.MembershipFingerprint); err != nil {
			return err
		}
	}
	return nil
}

func inspectIdentity(path, p2pPeerID string, jsonOutput bool, stdout io.Writer) error {
	if p2pPeerID == "" {
		return errors.New("--p2p-peer-id is required with --identity-key")
	}
	parsedPeerID, err := peer.Decode(p2pPeerID)
	if err != nil || parsedPeerID.String() != p2pPeerID {
		return fmt.Errorf("--p2p-peer-id must be a canonical libp2p peer ID")
	}
	signer, err := chain.LoadBFTSigner(path)
	if err != nil {
		return err
	}
	result := identityResult{
		Address:               signer.Address(),
		ConsensusPublicKeyHex: hex.EncodeToString(signer.GetPublicKey()),
		P2PPeerID:             parsedPeerID.String(),
	}
	if jsonOutput {
		return json.NewEncoder(stdout).Encode(result)
	}
	_, err = fmt.Fprintf(stdout,
		"Public consensus identity (not enrolled or activated):\nAddress: %s\nConsensus public key: %s\nP2P peer ID: %s\n",
		result.Address,
		result.ConsensusPublicKeyHex,
		result.P2PPeerID,
	)
	return err
}

func readMembership(path string) (chain.ConsensusMembership, error) {
	return chain.LoadConsensusMembershipFile(path)
}
