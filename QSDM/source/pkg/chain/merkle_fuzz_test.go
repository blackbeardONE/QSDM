package chain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// FuzzMerkleProof builds a transaction tree over fuzzed IDs, checks the
// inclusion proofs it hands out, and runs proofs decoded from raw bytes (as a
// light client would receive them) through VerifyProof and VerifyTxInBlock.
func FuzzMerkleProof(f *testing.F) {
	honest := BuildMerkleTree([]string{"tx-1", "tx-2", "tx-3", "tx-4", "tx-5"})
	proof, err := honest.GenerateProof(4)
	if err != nil {
		f.Fatal(err)
	}
	rawProof, err := json.Marshal(proof)
	if err != nil {
		f.Fatal(err)
	}
	f.Add([]byte("tx-1\ntx-2\ntx-3\ntx-4\ntx-5"), uint16(4), rawProof, "tx-5")
	f.Add([]byte("tx-1\ntx-2\ntx-3\ntx-4\ntx-5"), uint16(1), rawProof, "tx-4")
	f.Add([]byte("only"), uint16(0), []byte(`{"leaf_hash":"x","siblings":[{"hash":"y","is_right":true}]}`), "only")
	f.Add([]byte("dup\ndup\ndup"), uint16(2), []byte(`{"siblings":null}`), "dup")
	f.Add([]byte(""), uint16(0), []byte(`{}`), "")
	f.Add([]byte("\n"), uint16(7), []byte(`{"index":-1}`), "\n")

	f.Fuzz(func(t *testing.T, rawItems []byte, index uint16, rawProof []byte, txID string) {
		var items []string
		if len(rawItems) > 0 {
			items = strings.Split(string(rawItems), "\n")
		}
		if len(items) > 1024 {
			items = items[:1024]
		}
		tree := BuildMerkleTree(items)
		if again := BuildMerkleTree(items); again.Root != tree.Root {
			t.Fatal("the tree root is not a function of the items")
		}
		header := BlockHeader{Height: 1, TxRoot: tree.Root, TxCount: len(items)}
		legacyRoot := !txContentRootActiveAt(header.Height)
		if legacyRoot {
			txs := make([]*mempool.Tx, len(items))
			for i, id := range items {
				txs[i] = &mempool.Tx{ID: id}
			}
			if got := computeTxRoot(txs, header.Height); got != tree.Root {
				t.Fatalf("block tx root %s differs from the proof tree root %s", got, tree.Root)
			}
		}

		// Proofs decoded from the wire never panic, and an accepted one
		// matches the claimed ID and reaches the root.
		var wire MerkleProof
		if json.Unmarshal(rawProof, &wire) == nil {
			ok := VerifyProof(&wire, tree.Root)
			if VerifyProof(&wire, tree.Root) != ok {
				t.Fatal("VerifyProof is not deterministic")
			}
			if VerifyTxInBlock(txID, &wire, header) && (!ok || wire.LeafHash != hashLeaf(txID)) {
				t.Fatal("VerifyTxInBlock accepted a proof whose leaf or path does not check out")
			}
			if VerifyTxContentInBlock(nil, &wire, header) {
				t.Fatal("VerifyTxContentInBlock accepted a nil transaction")
			}
		}

		if len(items) == 0 {
			if tree.Root != emptyHash() {
				t.Fatalf("empty tree root %s", tree.Root)
			}
			if _, err := tree.GenerateProof(0); err == nil {
				t.Fatal("generated a proof in an empty tree")
			}
			return
		}
		idx := int(index) % len(items)
		p, err := tree.GenerateProof(idx)
		if err != nil {
			t.Fatalf("GenerateProof(%d) of %d: %v", idx, len(items), err)
		}
		depth := 0
		for n := len(items); n > 1; n = (n + 1) / 2 {
			depth++
		}
		if p.Index != idx || p.LeafHash != hashLeaf(items[idx]) || p.Root != tree.Root || len(p.Siblings) != depth {
			t.Fatalf("proof %+v for item %d of %d", p, idx, len(items))
		}
		// Complete: an honest proof verifies, for exactly its own item.
		if !VerifyProof(p, tree.Root) {
			t.Fatalf("honest proof for item %d of %d does not verify", idx, len(items))
		}
		if legacyRoot {
			if !VerifyTxInBlock(items[idx], p, header) {
				t.Fatalf("item %d of %d is not shown in its own block", idx, len(items))
			}
			if txID != items[idx] && VerifyTxInBlock(txID, p, header) {
				t.Fatalf("the proof for %q also proves %q", items[idx], txID)
			}
		}
		if depth > 0 {
			tampered := *p
			tampered.Siblings = append([]ProofNode(nil), p.Siblings...)
			tampered.Siblings[int(index)%depth].Hash += "0"
			if VerifyProof(&tampered, tree.Root) {
				t.Fatal("a proof with a tampered sibling verified")
			}
		}
	})
}
