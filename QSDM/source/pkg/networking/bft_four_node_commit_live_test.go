package networking

import (
	"context"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

const liveBFTGossipHeight uint64 = 700

type liveBFTGossipNode struct {
	host      host.Host
	pubsub    *pubsub.PubSub
	signer    *chain.PersistentBFTSigner
	address   string
	executor  *chain.BFTExecutor
	consensus *chain.BFTConsensus
	ingress   *BFTGossipIngress
	relay     *BFTP2PRelay
}

// TestLiveBFTGossipFourValidatorSignedCommit exercises the actual libp2p
// GossipSub path used by validators. It remains a single-process staging test,
// not a claim of multi-host or durable production consensus.
func TestLiveBFTGossipFourValidatorSignedCommit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live P2P BFT commit test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	nodes := newLiveBFTGossipNodes(t, ctx)
	defer closeLiveBFTGossipNodes(nodes)

	membership := liveBFTGossipMembership(nodes)
	if err := membership.Validate(); err != nil {
		t.Fatalf("validate membership: %v", err)
	}
	schedule, err := chain.NewConsensusMembershipSchedule([]chain.ConsensusMembership{membership})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := chain.NewBFTMembershipPolicy(schedule, liveBFTGossipHeight)
	if err != nil {
		t.Fatal(err)
	}
	peerPolicy, err := chain.NewBFTPeerOriginPolicy(policy)
	if err != nil {
		t.Fatal(err)
	}
	root, err := policy.MembershipRootForHeight(liveBFTGossipHeight)
	if err != nil {
		t.Fatal(err)
	}
	voting, err := chain.NewConsensusVotingSet(membership)
	if err != nil {
		t.Fatal(err)
	}
	configureLiveBFTGossipNodes(t, ctx, nodes, membership, policy, peerPolicy)
	connectLiveBFTGossipMesh(t, ctx, nodes)
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		for _, node := range nodes {
			round, active := node.consensus.GetRound(liveBFTGossipHeight)
			committed, done := node.consensus.GetCommitted(liveBFTGossipHeight)
			t.Logf("node=%s peer=%s topic_peers=%d ingress=%+v active=%t round=%+v committed=%t value=%+v",
				node.address, node.host.ID(), len(node.relay.topic.ListPeers()), node.ingress.Stats(), active, round, done, committed)
		}
	})
	time.Sleep(1500 * time.Millisecond)

	byAddress := make(map[string]*liveBFTGossipNode, len(nodes))
	for _, node := range nodes {
		byAddress[node.address] = node
	}
	proposer, err := voting.MemberForRound(0)
	if err != nil {
		t.Fatal(err)
	}
	proposerNode := byAddress[proposer.Address]
	if proposerNode == nil {
		t.Fatalf("missing proposer node %s", proposer.Address)
	}
	const blockHash = "live-gossip-commit-root"
	publishLiveBFTPropose(t, proposerNode, root, proposer.Address, blockHash)
	waitForLiveBFTGossip(t, ctx, "proposal propagation", func() bool {
		for _, node := range nodes {
			round, ok := node.consensus.GetRound(liveBFTGossipHeight)
			if !ok || round.Round != 0 || round.Proposer != proposer.Address || round.BlockHash != blockHash {
				return false
			}
		}
		return true
	})

	for _, node := range nodes {
		publishLiveBFTPrevote(t, node, root, blockHash)
	}
	waitForLiveBFTGossip(t, ctx, "prevote quorum", func() bool {
		for _, node := range nodes {
			round, ok := node.consensus.GetRound(liveBFTGossipHeight)
			if !ok || round.LockedBlockHash != blockHash {
				return false
			}
		}
		return true
	})

	for _, node := range nodes {
		publishLiveBFTPrecommit(t, node, root, blockHash)
	}
	waitForLiveBFTGossip(t, ctx, "commit propagation", func() bool {
		for _, node := range nodes {
			committed, ok := node.consensus.GetCommitted(liveBFTGossipHeight)
			if !ok || committed.Round != 0 || committed.Proposer != proposer.Address || committed.BlockHash != blockHash {
				return false
			}
		}
		return true
	})

	for _, node := range nodes {
		stats := node.ingress.Stats()
		if stats.PublisherRejected != 0 || stats.ApplyErrors != 0 {
			t.Fatalf("node %s ingress stats = %+v", node.address, stats)
		}
	}
}

func newLiveBFTGossipNodes(t *testing.T, ctx context.Context) []*liveBFTGossipNode {
	t.Helper()
	nodes := make([]*liveBFTGossipNode, 0, 4)
	for index := 0; index < 4; index++ {
		h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
		if err != nil {
			t.Fatalf("host %d: %v", index, err)
		}
		signer, _, err := chain.LoadOrCreateBFTSigner(filepath.Join(t.TempDir(), "consensus.json"))
		if err != nil {
			h.Close()
			t.Fatalf("signer %d: %v", index, err)
		}
		nodes = append(nodes, &liveBFTGossipNode{host: h, signer: signer, address: signer.Address()})
	}
	return nodes
}

func closeLiveBFTGossipNodes(nodes []*liveBFTGossipNode) {
	for _, node := range nodes {
		if node.relay != nil {
			node.relay.Close()
		}
		if node.host != nil {
			_ = node.host.Close()
		}
	}
}

func connectLiveBFTGossipMesh(t *testing.T, ctx context.Context, nodes []*liveBFTGossipNode) {
	t.Helper()
	for left := range nodes {
		for right := left + 1; right < len(nodes); right++ {
			if err := nodes[left].host.Connect(ctx, peer.AddrInfo{ID: nodes[right].host.ID(), Addrs: nodes[right].host.Addrs()}); err != nil {
				t.Fatalf("connect %d -> %d: %v", left, right, err)
			}
		}
	}
}

func liveBFTGossipMembership(nodes []*liveBFTGossipNode) chain.ConsensusMembership {
	members := make([]chain.ConsensusMember, 0, len(nodes))
	for _, node := range nodes {
		members = append(members, chain.ConsensusMember{
			Address:               node.address,
			ConsensusPublicKeyHex: hex.EncodeToString(node.signer.GetPublicKey()),
			P2PPeerID:             node.host.ID().String(),
			VotingPower:           1,
		})
	}
	return chain.ConsensusMembership{
		SchemaVersion:   chain.ConsensusMembershipSchemaVersion,
		NetworkID:       "qsdm-live-bft-gossip-test",
		EffectiveHeight: liveBFTGossipHeight,
		Members:         members,
	}
}

func configureLiveBFTGossipNodes(t *testing.T, ctx context.Context, nodes []*liveBFTGossipNode, membership chain.ConsensusMembership, policy *chain.BFTMembershipPolicy, peerPolicy *chain.BFTPeerOriginPolicy) {
	t.Helper()
	for _, node := range nodes {
		validators := chain.NewValidatorSet(chain.DefaultValidatorSetConfig())
		for _, member := range membership.Members {
			if err := validators.Register(member.Address, 100); err != nil {
				t.Fatalf("register %s: %v", member.Address, err)
			}
		}
		node.consensus = chain.NewBFTConsensus(validators, chain.DefaultConsensusConfig())
		node.executor = chain.NewBFTExecutor(node.consensus)
		node.executor.SetMembershipPolicy(policy)
		node.executor.SetRequireSignedVotes(true)
		node.executor.SetSignedVoteActivationHeight(liveBFTGossipHeight)
		node.executor.SetVoteSigner(node.signer)
		node.ingress = NewBFTGossipIngress(DefaultBFTGossipConfig(), node.executor)
		node.ingress.SetPeerOriginPolicy(peerPolicy)
		ps, err := pubsub.NewGossipSub(ctx, node.host, pubsub.WithMessageSignaturePolicy(DefaultPubsubSignaturePolicy))
		if err != nil {
			t.Fatalf("pubsub %s: %v", node.address, err)
		}
		node.pubsub = ps
		relay, err := NewBFTP2PRelay(&psJoiner{ps: ps}, node.ingress, node.host.ID().String())
		if err != nil {
			t.Fatalf("relay %s: %v", node.address, err)
		}
		node.relay = relay
	}
}

func publishLiveBFTPropose(t *testing.T, node *liveBFTGossipNode, root, proposer, blockHash string) {
	t.Helper()
	message := chain.BFTWireProposeMsg{Height: liveBFTGossipHeight, Round: 0, Proposer: proposer, BlockHash: blockHash, MembershipRoot: root}
	if err := chain.SignPropose(&message, node.signer); err != nil {
		t.Fatal(err)
	}
	publishLiveBFTPayload(t, node, chain.BFTWirePropose, message)
}

func publishLiveBFTPrevote(t *testing.T, node *liveBFTGossipNode, root, blockHash string) {
	t.Helper()
	message := chain.BFTWirePrevoteMsg{Height: liveBFTGossipHeight, Round: 0, Validator: node.address, BlockHash: blockHash, MembershipRoot: root}
	if err := chain.SignPrevote(&message, node.signer); err != nil {
		t.Fatal(err)
	}
	publishLiveBFTPayload(t, node, chain.BFTWirePrevote, message)
}

func publishLiveBFTPrecommit(t *testing.T, node *liveBFTGossipNode, root, blockHash string) {
	t.Helper()
	message := chain.BFTWirePrecommitMsg{Height: liveBFTGossipHeight, Round: 0, Validator: node.address, BlockHash: blockHash, MembershipRoot: root}
	if err := chain.SignPrecommit(&message, node.signer); err != nil {
		t.Fatal(err)
	}
	publishLiveBFTPayload(t, node, chain.BFTWirePrecommit, message)
}

func publishLiveBFTPayload(t *testing.T, node *liveBFTGossipNode, kind string, message interface{}) {
	t.Helper()
	payload, err := chain.MarshalBFTWire(kind, message)
	if err != nil {
		t.Fatal(err)
	}
	if err := node.executor.ApplyInbound(payload); err != nil {
		t.Fatalf("apply local %s on %s: %v", kind, node.address, err)
	}
	if err := node.relay.PublishRaw(payload); err != nil {
		t.Fatalf("publish %s from %s: %v", kind, node.address, err)
	}
}

func waitForLiveBFTGossip(t *testing.T, ctx context.Context, phase string, ready func() bool) {
	t.Helper()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ready() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timeout waiting for %s: %v", phase, ctx.Err())
		case <-ticker.C:
		}
	}
}
