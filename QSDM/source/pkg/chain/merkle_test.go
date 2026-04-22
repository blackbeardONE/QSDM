package chain

import (
	"testing"
)

func TestMerkleTree_SingleLeaf(t *testing.T) {
	tree := BuildMerkleTree([]string{"tx1"})
	if tree.Root == "" {
		t.Fatal("expected non-empty root")
	}
	if len(tree.Leaves) != 1 {
		t.Fatalf("expected 1 leaf, got %d", len(tree.Leaves))
	}
}

func TestMerkleTree_MultipleLeaves(t *testing.T) {
	tree := BuildMerkleTree([]string{"tx1", "tx2", "tx3", "tx4"})
	if tree.Root == "" {
		t.Fatal("expected non-empty root")
	}
	if len(tree.Leaves) != 4 {
		t.Fatalf("expected 4 leaves, got %d", len(tree.Leaves))
	}
}

func TestMerkleTree_OddLeaves(t *testing.T) {
	tree := BuildMerkleTree([]string{"tx1", "tx2", "tx3"})
	if tree.Root == "" {
		t.Fatal("expected non-empty root for odd-count tree")
	}
}

func TestMerkleTree_Empty(t *testing.T) {
	tree := BuildMerkleTree(nil)
	if tree.Root == "" {
		t.Fatal("expected deterministic empty root")
	}
}

func TestMerkleTree_GenerateAndVerifyProof(t *testing.T) {
	items := []string{"tx1", "tx2", "tx3", "tx4"}
	tree := BuildMerkleTree(items)

	for i := range items {
		proof, err := tree.GenerateProof(i)
		if err != nil {
			t.Fatalf("GenerateProof(%d): %v", i, err)
		}
		if !VerifyProof(proof, tree.Root) {
			t.Fatalf("proof verification failed for index %d", i)
		}
	}
}

func TestMerkleTree_ProofRejectsWrongRoot(t *testing.T) {
	tree := BuildMerkleTree([]string{"tx1", "tx2", "tx3", "tx4"})
	proof, _ := tree.GenerateProof(0)

	if VerifyProof(proof, "deadbeef") {
		t.Fatal("proof should fail against wrong root")
	}
}

func TestMerkleTree_ProofOddCount(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}
	tree := BuildMerkleTree(items)

	for i := range items {
		proof, err := tree.GenerateProof(i)
		if err != nil {
			t.Fatalf("GenerateProof(%d): %v", i, err)
		}
		if !VerifyProof(proof, tree.Root) {
			t.Fatalf("proof verification failed for index %d (odd tree)", i)
		}
	}
}

func TestMerkleTree_DeterministicRoot(t *testing.T) {
	items := []string{"tx1", "tx2", "tx3"}
	t1 := BuildMerkleTree(items)
	t2 := BuildMerkleTree(items)
	if t1.Root != t2.Root {
		t.Fatal("same items should produce same root")
	}
}

func TestMerkleTree_DifferentItemsDifferentRoot(t *testing.T) {
	t1 := BuildMerkleTree([]string{"tx1", "tx2"})
	t2 := BuildMerkleTree([]string{"tx3", "tx4"})
	if t1.Root == t2.Root {
		t.Fatal("different items should produce different roots")
	}
}

func TestMerkleTree_OutOfRangeProof(t *testing.T) {
	tree := BuildMerkleTree([]string{"tx1"})
	_, err := tree.GenerateProof(5)
	if err == nil {
		t.Fatal("expected error for out-of-range index")
	}
}

func TestVerifyTxInBlock(t *testing.T) {
	txIDs := []string{"tx1", "tx2", "tx3", "tx4"}
	tree := BuildMerkleTree(txIDs)
	header := BlockHeader{TxRoot: tree.Root}

	proof, _ := tree.GenerateProof(2)
	if !VerifyTxInBlock("tx3", proof, header) {
		t.Fatal("tx3 should verify against block header")
	}
	if VerifyTxInBlock("tx_fake", proof, header) {
		t.Fatal("fake tx should not verify")
	}
}
