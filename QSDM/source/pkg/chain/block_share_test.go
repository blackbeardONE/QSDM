package chain

// Tests for block_share.go: values repeated across blocks share one copy in
// memory, both when a journal is loaded (LoadChainNDJSON) and when a block is
// appended (ProduceBlock, TryAppendExternalBlock), and nothing that a block
// hashes, signs or encodes changes. Each test first requires that the values
// really are shared, so it fails if the sharing is removed, then checks that
// the blocks are unchanged.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

var shareTestRecipients = []string{"bob", "carol", "dave"}

// shareTestPaddedKey returns its signer's public key in a slice with spare
// capacity, as PersistentBFTSigner.GetPublicKey can.
type shareTestPaddedKey struct{ BFTSigner }

func (s shareTestPaddedKey) GetPublicKey() []byte {
	pk := s.BFTSigner.GetPublicKey()
	return append(make([]byte, 0, len(pk)+64), pk...)
}

type shareTestChain struct {
	producer   *BlockProducer
	blocks     []*Block
	keyA, keyB BFTSigner
}

// shareTestProduce seals n blocks of one transfer each. The first n/2 are
// signed with one key and the others with a second key, as across a producer
// hand-over.
func shareTestProduce(t *testing.T, n int) shareTestChain {
	t.Helper()
	a, _ := newBFTKey(t)
	b, _ := newBFTKey(t)
	c := shareTestChain{keyA: shareTestPaddedKey{a}, keyB: b}
	pool := mempool.New(mempool.DefaultConfig())
	c.producer = NewBlockProducer(pool, newTestApplier(), DefaultProducerConfig())
	for i := 0; i < n; i++ {
		signer := c.keyA
		if i >= n/2 {
			signer = c.keyB
		}
		c.producer.SetBlockSigner(signer)
		tx := &mempool.Tx{
			ID: fmt.Sprintf("share-%d", i), Sender: "alice", Recipient: shareTestRecipients[i%len(shareTestRecipients)],
			Amount: 1, Nonce: uint64(i), ContractID: "qsdm/share-test",
		}
		if err := pool.Add(tx); err != nil {
			t.Fatal(err)
		}
		blk, err := c.producer.ProduceBlock()
		if err != nil {
			t.Fatalf("block %d: %v", i, err)
		}
		if !bytes.Equal(blk.ProducerAuth.PublicKey, signer.GetPublicKey()) || VerifyBlockSignature(blk) != nil {
			t.Fatalf("sealed block %d does not carry its signer's key and signature", i)
		}
		c.blocks = append(c.blocks, blk)
	}
	return c
}

// shareTestFork is a second, signed block at the height of the last block of
// c, on that block's parent: the forked duplicate a journal can hold.
func shareTestFork(t *testing.T, c shareTestChain) *Block {
	t.Helper()
	last := c.blocks[len(c.blocks)-1]
	parent := c.blocks[len(c.blocks)-2]
	fork := &Block{
		Height: last.Height, PrevHash: parent.Hash, Timestamp: time.Unix(1700000000, 0).UTC(), StateRoot: "fork-root",
		Transactions: []*mempool.Tx{{ID: "fork", Sender: "alice", Recipient: "bob", Amount: 1, Nonce: 99}},
	}
	fork.Hash = computeBlockHash(fork)
	if err := SignBlock(fork, c.keyB); err != nil {
		t.Fatal(err)
	}
	return fork
}

// shareTestJournal writes blocks one line each, as a node's journal holds
// them, and returns the path and the lines.
func shareTestJournal(t *testing.T, blocks []*Block) (string, [][]byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "qsdm_chain.ndjson")
	for _, b := range blocks {
		if err := AppendBlockToFile(path, b); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
}

func shareTestLoad(t *testing.T, path string, want int) []*Block {
	t.Helper()
	blocks, err := LoadChainNDJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != want {
		t.Fatalf("loaded %d blocks, want %d", len(blocks), want)
	}
	return blocks
}

func keyArray(b *Block) *byte {
	if len(b.ProducerAuth.PublicKey) == 0 {
		return nil
	}
	return &b.ProducerAuth.PublicKey[0]
}

// requireKeysShared requires every block to hold the one array of its
// producer's key, with a capacity equal to its length.
func requireKeysShared(t *testing.T, blocks []*Block) {
	t.Helper()
	arrays := map[string]*byte{}
	for _, b := range blocks {
		pk := b.ProducerAuth.PublicKey
		if len(pk) == 0 {
			t.Fatalf("block %d is unsigned", b.Height)
		}
		if cap(pk) != len(pk) {
			t.Fatalf("block %d: key capacity %d, length %d: an append could write into a shared array", b.Height, cap(pk), len(pk))
		}
		if a, ok := arrays[string(pk)]; !ok {
			arrays[string(pk)] = keyArray(b)
		} else if a != keyArray(b) {
			t.Fatalf("block %d holds its own copy of its producer's public key", b.Height)
		}
	}
}

// sameData returns a check that equal strings share their bytes.
func sameData(t *testing.T) func(field, s string) {
	data := map[string]*byte{}
	return func(field, s string) {
		t.Helper()
		if s == "" {
			return
		}
		if p, ok := data[s]; !ok {
			data[s] = unsafe.StringData(s)
		} else if p != unsafe.StringData(s) {
			t.Fatalf("%s %q is not shared", field, s)
		}
	}
}

// requireBlockStringsShared requires equal producer IDs to share their bytes
// and each block's prev_hash to share its parent's hash.
func requireBlockStringsShared(t *testing.T, blocks []*Block) {
	t.Helper()
	same := sameData(t)
	for i, b := range blocks {
		same("producer_id", b.ProducerID)
		if i > 0 && b.PrevHash == blocks[i-1].Hash && unsafe.StringData(b.PrevHash) != unsafe.StringData(blocks[i-1].Hash) {
			t.Fatalf("block %d: prev_hash does not share its parent's hash", b.Height)
		}
	}
}

// requireTxStringsShared requires equal transaction senders, recipients and
// contract IDs to share their bytes.
func requireTxStringsShared(t *testing.T, blocks []*Block) {
	t.Helper()
	same := sameData(t)
	for _, b := range blocks {
		for _, tx := range b.Transactions {
			same("sender", tx.Sender)
			same("recipient", tx.Recipient)
			same("contract_id", tx.ContractID)
		}
	}
}

// TestLoadChainNDJSONSharesRepeatedValues: a loaded journal holds one copy of
// each producer key, producer ID, sender, recipient and contract ID, and every
// prev_hash shares its parent's hash.
func TestLoadChainNDJSONSharesRepeatedValues(t *testing.T) {
	c := shareTestProduce(t, 8)
	path, _ := shareTestJournal(t, c.blocks)
	loaded := shareTestLoad(t, path, len(c.blocks))
	requireKeysShared(t, loaded)
	requireBlockStringsShared(t, loaded)
	requireTxStringsShared(t, loaded)
	for i, b := range loaded {
		if !bytes.Equal(b.ProducerAuth.PublicKey, c.blocks[i].ProducerAuth.PublicKey) {
			t.Fatalf("block %d: the loaded key differs from the sealed one", i)
		}
	}
}

// TestLoadChainNDJSONSharedBlocksEqualDecodedBlocks: every loaded block is
// deeply equal to its line decoded on its own, including a forked duplicate
// whose prev_hash is not the previous line's hash.
func TestLoadChainNDJSONSharedBlocksEqualDecodedBlocks(t *testing.T) {
	c := shareTestProduce(t, 8)
	journal := append(append([]*Block(nil), c.blocks...), shareTestFork(t, c))
	path, lines := shareTestJournal(t, journal)
	loaded := shareTestLoad(t, path, len(journal))
	requireKeysShared(t, loaded)
	for i, line := range lines {
		var own Block
		if err := json.Unmarshal(line, &own); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(loaded[i], &own) {
			t.Fatalf("line %d: the loaded block differs from the line decoded on its own", i+1)
		}
	}
	if fork := loaded[len(loaded)-1]; fork.PrevHash != c.blocks[len(c.blocks)-2].Hash {
		t.Fatal("the forked duplicate lost its own prev_hash")
	}
}

// TestLoadChainNDJSONSharedBlocksEncodeToTheJournal: every loaded block
// encodes to exactly its journal line, which is what a node serves and
// writes.
func TestLoadChainNDJSONSharedBlocksEncodeToTheJournal(t *testing.T) {
	c := shareTestProduce(t, 8)
	path, lines := shareTestJournal(t, append(append([]*Block(nil), c.blocks...), shareTestFork(t, c)))
	loaded := shareTestLoad(t, path, len(lines))
	requireKeysShared(t, loaded)
	for i, b := range loaded {
		data, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, lines[i]) {
			t.Fatalf("line %d: the re-encoded block differs from the journal line", i+1)
		}
	}
}

// TestLoadChainNDJSONSharedBlocksVerify: the loaded blocks' hashes recompute,
// their producer signatures verify, and a producer restores them and extends
// the chain.
func TestLoadChainNDJSONSharedBlocksVerify(t *testing.T) {
	c := shareTestProduce(t, 8)
	path, _ := shareTestJournal(t, c.blocks)
	loaded := shareTestLoad(t, path, len(c.blocks))
	requireKeysShared(t, loaded)
	for _, b := range loaded {
		if ComputeBlockHash(b) != b.Hash {
			t.Fatalf("block %d: the hash does not recompute", b.Height)
		}
		if err := VerifyBlockSignature(b); err != nil {
			t.Fatalf("block %d: %v", b.Height, err)
		}
	}
	// A key shared by many blocks still authenticates only its own blocks.
	forged := *loaded[len(loaded)-1]
	forged.ProducerAuth.Signature = loaded[len(loaded)-2].ProducerAuth.Signature
	if err := VerifyBlockSignature(&forged); err == nil {
		t.Fatal("a signature over another block verified")
	}

	ta := newTestApplier()
	for _, b := range loaded {
		for _, tx := range b.Transactions {
			if err := ta.ApplyTx(tx); err != nil {
				t.Fatal(err)
			}
		}
	}
	pool := mempool.New(mempool.DefaultConfig())
	restored := NewBlockProducer(pool, ta, DefaultProducerConfig())
	if err := restored.RestoreChain(loaded); err != nil {
		t.Fatal(err)
	}
	restored.SetBlockSigner(c.keyB)
	if err := pool.Add(&mempool.Tx{ID: "share-next", Sender: "alice", Recipient: "bob", Amount: 1, Nonce: 100}); err != nil {
		t.Fatal(err)
	}
	next, err := restored.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	if next.PrevHash != loaded[len(loaded)-1].Hash || VerifyBlockSignature(next) != nil {
		t.Fatal("the block sealed on the restored chain does not extend it and verify")
	}
	requireKeysShared(t, restored.AllBlocks())
}

// TestAppendSharesWithParent: blocks sealed by ProduceBlock and blocks a
// follower appends with TryAppendExternalBlock hold their parent's copy of the
// producer key, producer ID and parent hash, and the follower's blocks encode,
// hash, verify and replay exactly like the producer's.
func TestAppendSharesWithParent(t *testing.T) {
	c := shareTestProduce(t, 8)
	requireKeysShared(t, c.blocks)
	requireBlockStringsShared(t, c.blocks)

	follower := NewBlockProducer(mempool.New(mempool.DefaultConfig()), newTestApplier(), DefaultProducerConfig())
	for _, b := range c.blocks {
		wire, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		var blk Block // decoded on its own, as the HTTP sync and gossip paths do
		if err := json.Unmarshal(wire, &blk); err != nil {
			t.Fatal(err)
		}
		if err := follower.TryAppendExternalBlock(&blk); err != nil {
			t.Fatalf("append %d: %v", b.Height, err)
		}
	}
	appended := follower.AllBlocks()
	if len(appended) != len(c.blocks) {
		t.Fatalf("the follower holds %d blocks, want %d", len(appended), len(c.blocks))
	}
	requireKeysShared(t, appended)
	requireBlockStringsShared(t, appended)
	for i, b := range appended {
		want, _ := json.Marshal(c.blocks[i])
		got, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("block %d encodes differently on the follower", i)
		}
		if ComputeBlockHash(b) != b.Hash || VerifyBlockSignature(b) != nil {
			t.Fatalf("block %d: the hash or signature does not verify on the follower", i)
		}
	}
	if follower.applier.StateRoot() != c.producer.applier.StateRoot() {
		t.Fatal("the follower's state root differs from the producer's")
	}
	// Appending to a shared key copies it, so the shared array never changes.
	for _, blocks := range [][]*Block{c.blocks, appended} {
		before := append([]byte(nil), blocks[1].ProducerAuth.PublicKey...)
		grown := append(blocks[0].ProducerAuth.PublicKey, 0xff)
		grown[0] ^= 0xff
		if !bytes.Equal(blocks[1].ProducerAuth.PublicKey, before) {
			t.Fatal("appending to one block's key changed another block's key")
		}
	}
}
