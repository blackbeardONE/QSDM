package chain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// A leaf is sha256(txID) and an inner node is sha256(left+right) over the
// 64-character hex of its children. VerifyTxInBlock must therefore pin the
// proof to the depth of the block's tree: otherwise the 128-character "ID"
// left+right proves an inner node with a shorter path, and a real ID that
// begins with the hex of sha256(fake) proves fake with a longer one.

// withLegacyTxRoot keeps the ID-keyed tx root, the one VerifyTxInBlock checks,
// in force for the test.
func withLegacyTxRoot(t *testing.T) {
	t.Helper()
	prev := TxContentRootActivationHeight()
	SetTxContentRootActivationHeight(0)
	t.Cleanup(func() { SetTxContentRootActivationHeight(prev) })
}

// inclusionTestIDs returns n transaction IDs in the shapes blocks carry: node
// wallet transfers (32 hex characters), solo rewards, and a client-chosen ID
// of 128 lowercase hex characters. IDs are free-form (where the API checks one,
// ValidateTransactionID allows 16 to 128 characters of [0-9A-Za-z_-]), so that
// last shape, an inner node's preimage, can be a real ID, and a fix that
// refused such IDs would refuse real transactions.
func inclusionTestIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		sum := sha256.Sum256([]byte(fmt.Sprintf("tx-%d", i)))
		switch i % 3 {
		case 0:
			ids[i] = hex.EncodeToString(sum[:16])
		case 1:
			ids[i] = fmt.Sprintf("solo-reward-%d-%x-%x", i, sum[:8], sum[8:12])
		default:
			ids[i] = hashLeaf(fmt.Sprintf("left-%d", i)) + hashLeaf(fmt.Sprintf("right-%d", i))
		}
	}
	return ids
}

// inclusionTestHeader is the header a node builds (Block.Header) for a block
// holding transactions with these IDs.
func inclusionTestHeader(t *testing.T, ids []string) BlockHeader {
	t.Helper()
	txs := make([]*mempool.Tx, len(ids))
	for i, id := range ids {
		txs[i] = &mempool.Tx{ID: id, Sender: "sender", Recipient: "recipient", Amount: 1}
	}
	header := (&Block{Height: 7, Transactions: txs}).Header()
	if header.TxCount != len(ids) {
		t.Fatalf("the header carries TxCount %d for %d transactions", header.TxCount, len(ids))
	}
	if tree := BuildMerkleTree(ids); tree.Root != header.TxRoot {
		t.Fatalf("%d transactions: tree root %s, header tx root %s", len(ids), tree.Root, header.TxRoot)
	}
	return header
}

// treeRow returns level k of the tree, an odd row padded the way
// BuildMerkleTree and GenerateProof pad it (the last node repeated).
func treeRow(tree *MerkleTree, k int) []string {
	row := append([]string(nil), tree.nodes[k]...)
	if len(row)%2 != 0 && k < len(tree.nodes)-1 {
		row = append(row, row[len(row)-1])
	}
	return row
}

// pathFrom returns the siblings from node idx of level k up to the root.
func pathFrom(tree *MerkleTree, k, idx int) []ProofNode {
	var path []ProofNode
	for level := k; level < len(tree.nodes)-1; level++ {
		row := treeRow(tree, level)
		if idx%2 == 0 {
			path = append(path, ProofNode{Hash: row[idx+1], IsRight: true})
		} else {
			path = append(path, ProofNode{Hash: row[idx-1], IsRight: false})
		}
		idx /= 2
	}
	return path
}

// Real proofs, as GenerateProof makes them, still verify against the header a
// node builds, for every index of every tree of 1 to 17 transactions.
func TestVerifyTxInBlock_RealProofsVerifyForEveryIndex(t *testing.T) {
	withLegacyTxRoot(t)
	for n := 1; n <= 17; n++ {
		ids := inclusionTestIDs(n)
		header := inclusionTestHeader(t, ids)
		tree := BuildMerkleTree(ids)
		for i, id := range ids {
			proof, err := tree.GenerateProof(i)
			if err != nil {
				t.Fatalf("GenerateProof(%d) of %d: %v", i, n, err)
			}
			if !VerifyTxInBlock(id, proof, header) {
				t.Errorf("transaction %d of %d (%q) does not verify in its own block", i, n, id)
			}
		}
	}
}

// The "ID" made of an inner node's two children joined hashes to that node,
// and the path from the node to the root reaches the tx root: a proof one or
// more levels shorter than a real one. Every inner node of every tree of 2 to
// 17 transactions is tried, the root included (an empty path).
func TestVerifyTxInBlock_RejectsAnInnerNodeClaimedAsATransaction(t *testing.T) {
	withLegacyTxRoot(t)
	for n := 2; n <= 17; n++ {
		ids := inclusionTestIDs(n)
		header := inclusionTestHeader(t, ids)
		tree := BuildMerkleTree(ids)
		depth := len(tree.nodes) - 1
		for k := 1; k <= depth; k++ {
			children := treeRow(tree, k-1)
			for j := range tree.nodes[k] {
				fake := children[2*j] + children[2*j+1]
				proof := &MerkleProof{LeafHash: hashLeaf(fake), Index: j, Siblings: pathFrom(tree, k, j), Root: tree.Root}
				if proof.LeafHash != tree.nodes[k][j] || !VerifyProof(proof, header.TxRoot) {
					t.Fatalf("%d transactions, level %d node %d: the forged path does not reach the tx root", n, k, j)
				}
				if VerifyTxInBlock(fake, proof, header) {
					t.Errorf("%d transactions: the children of level-%d node %d, joined, verified as a transaction "+
						"with a %d-sibling proof; a real proof has %d", n, k, j, len(proof.Siblings), depth)
				}
			}
		}
	}
}

// The mirror image: a real transaction whose ID begins with the hex of
// sha256(fake) lets a path one level longer than a real proof show fake as
// included, the rest of the real ID being the extra first sibling. A check on
// the shape of the claimed ID cannot see this one.
func TestVerifyTxInBlock_RejectsAProofOneLevelTooLong(t *testing.T) {
	withLegacyTxRoot(t)
	const fake = "never-in-this-block"
	for n := 1; n <= 17; n++ {
		ids := inclusionTestIDs(n)
		host := n / 2
		ids[host] = hashLeaf(fake) + "x" // 65 characters, an admissible ID
		header := inclusionTestHeader(t, ids)
		tree := BuildMerkleTree(ids)
		genuine, err := tree.GenerateProof(host)
		if err != nil {
			t.Fatalf("GenerateProof(%d) of %d: %v", host, n, err)
		}
		proof := &MerkleProof{
			LeafHash: hashLeaf(fake),
			Index:    host,
			Siblings: append([]ProofNode{{Hash: "x", IsRight: true}}, genuine.Siblings...),
			Root:     tree.Root,
		}
		if !VerifyProof(proof, header.TxRoot) {
			t.Fatalf("%d transactions: the forged path does not reach the tx root", n)
		}
		if VerifyTxInBlock(fake, proof, header) {
			t.Errorf("%d transactions: %q verified through transaction %d with a %d-sibling proof; a real proof has %d",
				n, fake, host, len(proof.Siblings), len(genuine.Siblings))
		}
		if !VerifyTxInBlock(ids[host], genuine, header) {
			t.Errorf("%d transactions: the host transaction no longer verifies", n)
		}
	}
}

// Without a transaction count the depth is unknown, so nothing is proven.
// Block.Header always sets the count. An empty block holds nothing, not even
// the empty ID, whose leaf equals the empty tree's root.
func TestVerifyTxInBlock_NeedsTheTransactionCount(t *testing.T) {
	withLegacyTxRoot(t)
	ids := inclusionTestIDs(5)
	header := inclusionTestHeader(t, ids)
	proof, err := BuildMerkleTree(ids).GenerateProof(2)
	if err != nil {
		t.Fatalf("GenerateProof: %v", err)
	}
	header.TxCount = 0
	if VerifyTxInBlock(ids[2], proof, header) {
		t.Error("a proof verified against a header without a transaction count")
	}

	empty := (&Block{Height: 7}).Header()
	if VerifyTxInBlock("", &MerkleProof{LeafHash: hashLeaf(""), Root: empty.TxRoot}, empty) {
		t.Error("the empty ID verified as included in an empty block")
	}
}
