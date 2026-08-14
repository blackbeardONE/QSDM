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

// The height check in VerifyTxInBlock prevents a FALSE POSITIVE, not merely a
// redundant false.
//
// mempool.Tx.ID is an unconstrained string. A caller can pass an ID whose value
// is the content digest of a real transaction in the block -- at which point
// hashLeaf(txID) equals the tree's leaf, the proof verifies, and without the
// height check the function answers "yes, included" for an ID that is not the
// transaction's ID.
//
// This case is why the check exists. It is also why neutering it passed every
// other test in the package: nothing else constructs an ID of that shape, so
// the comment describing the check went through two wrong versions -- first
// overstating it, then dismissing it -- before a reviewer built this input.
func TestVerifyTxInBlock_RefusesADigestShapedID(t *testing.T) {
	prev := TxContentRootActivationHeight()
	t.Cleanup(func() { SetTxContentRootActivationHeight(prev) })
	SetTxContentRootActivationHeight(100)
	const h = 100

	txs := []*mempool.Tx{
		{ID: "a", Sender: "s1", Amount: 1},
		{ID: "b", Sender: "s2", Amount: 2},
	}
	digests := make([]string, len(txs))
	for i, tx := range txs {
		digests[i] = TxContentDigest(tx)
	}
	tree := BuildMerkleTree(digests)
	proof, err := tree.GenerateProof(1)
	if err != nil {
		t.Fatalf("generate proof: %v", err)
	}
	header := BlockHeader{Height: h, TxRoot: computeTxRoot(txs, h)}

	// The adversarial input: an "ID" that is really the content digest, so the
	// leaf comparison alone would succeed.
	if VerifyTxInBlock(digests[1], proof, header) {
		t.Error("an ID-keyed check must refuse against a content root even when the " +
			"ID happens to equal a content digest -- otherwise it answers 'included' " +
			"for a transaction ID that does not exist")
	}

	// And the honest path still works: the same proof, checked the right way.
	if !VerifyTxContentInBlock(txs[1], proof, header) {
		t.Error("the content-keyed check must still verify the genuine proof")
	}
}

// The symmetric case, which the previous commit missed.
//
// VerifyTxInBlock refuses at or above the activation height because an ID could
// equal a content digest. The mirror hazard exists below it: the tree is
// ID-keyed there, so if a transaction's ID equals TxContentDigest(other) for
// some transaction the attacker composed, then VerifyTxContentInBlock(other,
// proof, header) would verify against the ID tree and answer "included" for a
// transaction that never was.
//
// An attacker controls both sides: tx.ID is unconstrained (pkg/mempool performs
// no format validation, and the API handlers check only that it is non-empty),
// and the fake transaction is theirs to compose.
//
// I fixed one direction and claimed review had confirmed the other. It had not
// -- it had only checked leaf derivation. The reviewer then built this input
// and showed the guard was real and untested, which is the same mistake one
// function over.
func TestVerifyTxContentInBlock_RefusesADigestKeyedID(t *testing.T) {
	prev := TxContentRootActivationHeight()
	t.Cleanup(func() { SetTxContentRootActivationHeight(prev) })
	SetTxContentRootActivationHeight(100)
	const belowActivation = 50

	// The transaction the attacker wants to claim was included.
	fake := &mempool.Tx{ID: "never-in-any-block", Sender: "attacker", Amount: 9999}

	// A real block whose tree is ID-keyed, where one ID is the fake's digest.
	txs := []*mempool.Tx{
		{ID: "honest-a", Sender: "s1", Amount: 1},
		{ID: TxContentDigest(fake), Sender: "s2", Amount: 2},
	}
	ids := []string{txs[0].ID, txs[1].ID}
	tree := BuildMerkleTree(ids)
	proof, err := tree.GenerateProof(1)
	if err != nil {
		t.Fatalf("generate proof: %v", err)
	}
	header := BlockHeader{Height: belowActivation, TxRoot: computeTxRoot(txs, belowActivation)}
	if header.TxRoot != tree.Root {
		t.Fatalf("below activation the root must be ID-keyed: %s vs %s", header.TxRoot, tree.Root)
	}

	if VerifyTxContentInBlock(fake, proof, header) {
		t.Error("a content-keyed check must refuse against an ID-keyed root; otherwise a " +
			"transaction that was never in the block verifies as included, because its " +
			"digest happens to be some other transaction's ID")
	}

	// The honest path below activation is still the ID-keyed one, and it works.
	if !VerifyTxInBlock(txs[1].ID, proof, header) {
		t.Error("the ID-keyed check must still verify a genuine proof below activation")
	}
}
