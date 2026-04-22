package chain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// MerkleTree builds a binary hash tree over a list of data items.
type MerkleTree struct {
	Root   string
	Leaves []string
	nodes  [][]string // nodes[level][index]
}

// BuildMerkleTree constructs a tree from leaf data (e.g. transaction IDs).
func BuildMerkleTree(items []string) *MerkleTree {
	if len(items) == 0 {
		return &MerkleTree{Root: emptyHash()}
	}

	leaves := make([]string, len(items))
	for i, item := range items {
		leaves[i] = hashLeaf(item)
	}

	levels := [][]string{leaves}
	current := leaves

	for len(current) > 1 {
		if len(current)%2 != 0 {
			current = append(current, current[len(current)-1]) // duplicate last node
		}
		var next []string
		for i := 0; i < len(current); i += 2 {
			next = append(next, hashPair(current[i], current[i+1]))
		}
		levels = append(levels, next)
		current = next
	}

	return &MerkleTree{
		Root:   current[0],
		Leaves: leaves,
		nodes:  levels,
	}
}

// MerkleProof is an inclusion proof for a leaf in the tree.
type MerkleProof struct {
	LeafHash string       `json:"leaf_hash"`
	Index    int          `json:"index"`
	Siblings []ProofNode  `json:"siblings"`
	Root     string       `json:"root"`
}

// ProofNode is one sibling in the proof path.
type ProofNode struct {
	Hash    string `json:"hash"`
	IsRight bool   `json:"is_right"` // true if sibling is to the right
}

// GenerateProof creates an inclusion proof for the item at `index`.
func (mt *MerkleTree) GenerateProof(index int) (*MerkleProof, error) {
	if index < 0 || index >= len(mt.Leaves) {
		return nil, fmt.Errorf("index %d out of range [0, %d)", index, len(mt.Leaves))
	}

	proof := &MerkleProof{
		LeafHash: mt.Leaves[index],
		Index:    index,
		Root:     mt.Root,
	}

	idx := index
	for level := 0; level < len(mt.nodes)-1; level++ {
		row := mt.nodes[level]
		// Ensure even length (tree build may have duplicated)
		if len(row)%2 != 0 {
			row = append(row, row[len(row)-1])
		}
		var siblingHash string
		var isRight bool
		if idx%2 == 0 {
			siblingHash = row[idx+1]
			isRight = true
		} else {
			siblingHash = row[idx-1]
			isRight = false
		}
		proof.Siblings = append(proof.Siblings, ProofNode{Hash: siblingHash, IsRight: isRight})
		idx /= 2
	}

	return proof, nil
}

// VerifyProof checks a Merkle inclusion proof against a root hash.
func VerifyProof(proof *MerkleProof, expectedRoot string) bool {
	current := proof.LeafHash
	for _, sibling := range proof.Siblings {
		if sibling.IsRight {
			current = hashPair(current, sibling.Hash)
		} else {
			current = hashPair(sibling.Hash, current)
		}
	}
	return current == expectedRoot
}

// VerifyTxInBlock verifies that a transaction ID is included in a block header.
func VerifyTxInBlock(txID string, proof *MerkleProof, header BlockHeader) bool {
	expectedLeaf := hashLeaf(txID)
	if proof.LeafHash != expectedLeaf {
		return false
	}
	return VerifyProof(proof, header.TxRoot)
}

func hashLeaf(data string) string {
	h := sha256.Sum256([]byte(data))
	return hex.EncodeToString(h[:])
}

func hashPair(left, right string) string {
	h := sha256.Sum256([]byte(left + right))
	return hex.EncodeToString(h[:])
}

func emptyHash() string {
	h := sha256.Sum256(nil)
	return hex.EncodeToString(h[:])
}
