package chain

import (
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// An inclusion proof must match the format of the root it is checked against.
//
// computeTxRoot merkleizes tx.ID below the content-root activation height and
// TxContentDigest at or above it. VerifyTxInBlock builds its leaf from an ID,
// so once the content root is live it would return false for a GENUINE proof --
// a silent wrong answer on an inclusion check, and the caller has no way to
// tell "not included" from "I cannot check this".
//
// Review flagged this as latent: the function has no production caller today,
// but it is exported, and the landmine arms itself the moment the content root
// is activated.
func TestInclusionProofs_MatchTheRootFormat(t *testing.T) {
	prev := TxContentRootActivationHeight()
	t.Cleanup(func() { SetTxContentRootActivationHeight(prev) })

	txs := []*mempool.Tx{
		{ID: "a", Sender: "s1", Amount: 1},
		{ID: "b", Sender: "s2", Amount: 2},
		{ID: "c", Sender: "s3", Amount: 3},
	}

	t.Run("below activation the ID proof works and the content proof refuses", func(t *testing.T) {
		SetTxContentRootActivationHeight(100)
		const h = 50
		ids := []string{"a", "b", "c"}
		tree := BuildMerkleTree(ids)
		proof, err := tree.GenerateProof(1)
		if err != nil {
			t.Fatalf("could not generate an ID proof: %v", err)
		}
		header := BlockHeader{Height: h, TxRoot: computeTxRoot(txs, h)}
		if header.TxRoot != tree.Root {
			t.Fatalf("below activation the root must merkleize IDs: %s vs %s", header.TxRoot, tree.Root)
		}
		if !VerifyTxInBlock("b", proof, header) {
			t.Error("an ID proof must verify below the activation height")
		}
		if VerifyTxContentInBlock(txs[1], proof, header) {
			t.Error("a content proof must not verify against an ID root")
		}
	})

	t.Run("at activation the content proof works and the ID proof refuses", func(t *testing.T) {
		SetTxContentRootActivationHeight(100)
		const h = 100
		digests := make([]string, len(txs))
		for i, tx := range txs {
			digests[i] = TxContentDigest(tx)
		}
		tree := BuildMerkleTree(digests)
		proof, err := tree.GenerateProof(1)
		if err != nil {
			t.Fatalf("could not generate a content proof: %v", err)
		}
		header := BlockHeader{Height: h, TxRoot: computeTxRoot(txs, h)}
		if header.TxRoot != tree.Root {
			t.Fatalf("at activation the root must merkleize content digests: %s vs %s",
				header.TxRoot, tree.Root)
		}
		if !VerifyTxContentInBlock(txs[1], proof, header) {
			t.Error("a content proof must verify at the activation height")
		}
		if VerifyTxInBlock("b", proof, header) {
			t.Error("an ID proof must be REFUSED against a content root, not silently " +
				"answer false as though the transaction were absent")
		}
	})

	// The content proof binds contents, which is the whole point: a transaction
	// with the same ID but a rewritten amount must not verify.
	t.Run("content proof rejects a rewritten transaction", func(t *testing.T) {
		SetTxContentRootActivationHeight(100)
		const h = 100
		digests := make([]string, len(txs))
		for i, tx := range txs {
			digests[i] = TxContentDigest(tx)
		}
		tree := BuildMerkleTree(digests)
		proof, _ := tree.GenerateProof(1)
		header := BlockHeader{Height: h, TxRoot: computeTxRoot(txs, h)}

		rewritten := *txs[1]
		rewritten.Amount = 9999
		if VerifyTxContentInBlock(&rewritten, proof, header) {
			t.Error("a rewritten amount must not verify: binding contents is the point")
		}
	})
}
