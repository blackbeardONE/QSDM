package chain

// Tests for the signatures of appended blocks (KeepSignaturesInJournal,
// block_sig_journal.go): once the next block is durable in the journal, an
// appended block's signature leaves memory, the block in the chain becomes a
// stripped copy, every reader still gets exactly the block it got before,
// and anything unexpected keeps the signature in memory. The fixture is in
// verified_chain_test.go.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// appendRig is a producer under the fixture's transition whose persistence
// hook journals every block it appends, as main() wires it: the block joins
// the chain, then OnSealedBlock appends and fsyncs its line (step H2).
type appendRig struct {
	t      *testing.T
	f      journalFixture
	bp     *BlockProducer
	j      *ChainJournal
	path   string
	sealed []*Block // every block of the chain as sealed or received, by height
	// failed counts the hook's journal errors (the real hook fail-stops).
	failed int
}

// newAppendRig restores the first boot blocks of f as a start does
// (LoadVerifiedJournal, RestoreVerifiedChain), or starts a fresh chain when
// boot is 0, then opens the journal at the tip and, if attach, attaches it.
func newAppendRig(t *testing.T, f journalFixture, boot int, attach bool) *appendRig {
	t.Helper()
	r := &appendRig{t: t, f: f}
	var tip *Block
	if boot > 0 {
		r.path, _ = shareTestJournal(t, f.blocks[:boot])
		r.bp = f.producer(t, f.blocks[:boot])
		v, err := r.bp.LoadVerifiedJournal(r.path)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.bp.RestoreVerifiedChain(v.Blocks(), v); err != nil {
			t.Fatal(err)
		}
		tip = f.blocks[boot-1]
	} else {
		r.path = filepath.Join(t.TempDir(), "qsdm_chain.ndjson")
		r.bp = f.producer(t, nil)
	}
	r.sealed = append(r.sealed, f.blocks[:boot]...)
	j, err := OpenChainJournal(r.path, tip)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	r.j = j
	if attach {
		if err := r.bp.KeepSignaturesInJournal(j); err != nil {
			t.Fatalf("KeepSignaturesInJournal: %v", err)
		}
	}
	r.bp.OnSealedBlock = func(b *Block) {
		if err := j.Append(b); err != nil {
			r.failed++
		}
	}
	r.bp.SetBlockSigner(f.newKey)
	return r
}

// follow appends sealed as a full node does: decoded from its JSON, as the
// gossip and the HTTPS sync decode it, through TryAppendExternalBlock.
func (r *appendRig) follow(sealed *Block) *Block {
	r.t.Helper()
	var wire Block
	if err := json.Unmarshal(mustMarshal(r.t, sealed), &wire); err != nil {
		r.t.Fatal(err)
	}
	if err := r.bp.TryAppendExternalBlock(&wire); err != nil {
		r.t.Fatalf("append %d: %v", sealed.Height, err)
	}
	r.sealed = append(r.sealed, &wire)
	return &wire
}

// produce seals one block as the producer does (ProduceBlock, signed by the
// replacement producer).
func (r *appendRig) produce() *Block {
	r.t.Helper()
	h := r.bp.TipHeight() + 1
	tx := &mempool.Tx{ID: fmt.Sprintf("ap-%d", h), Sender: "alice", Recipient: shareTestRecipients[h%3], Amount: 1, Nonce: h,
		ContractID: "qsdm/journal-test"}
	if err := r.bp.pool.Add(tx); err != nil {
		r.t.Fatal(err)
	}
	b, err := r.bp.ProduceBlock()
	if err != nil {
		r.t.Fatalf("ProduceBlock at %d: %v", h, err)
	}
	r.sealed = append(r.sealed, b)
	return b
}

// lines returns the journal's lines.
func (r *appendRig) lines() [][]byte {
	r.t.Helper()
	data, err := os.ReadFile(r.path)
	if err != nil {
		r.t.Fatal(err)
	}
	return bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
}

// requireSignatures requires each block of the chain to hold its signature
// in memory, or only its line's position, as stripped says, and every
// stripped block to be the sealed block but for its signature.
func (r *appendRig) requireSignatures(stripped func(h uint64) bool) {
	r.t.Helper()
	blocks := r.bp.AllBlocks()
	if len(blocks) != len(r.sealed) {
		r.t.Fatalf("chain holds %d blocks, want %d", len(blocks), len(r.sealed))
	}
	for i, b := range blocks {
		sealed := r.sealed[i]
		a := b.ProducerAuth
		switch {
		case !sealed.ProducerAuth.Signed():
			if a.Stripped() || a.Signed() {
				r.t.Fatalf("unsigned block %d: %+v", i, a)
			}
		case stripped(b.Height):
			if !a.Stripped() || a.Signature != nil || !a.Signed() {
				r.t.Fatalf("block %d holds %d signature bytes, want none", i, len(a.Signature))
			}
			if !bytes.Equal(a.PublicKey, sealed.ProducerAuth.PublicKey) {
				r.t.Fatalf("block %d lost its public key", i)
			}
		default:
			if a.Stripped() || !bytes.Equal(a.Signature, sealed.ProducerAuth.Signature) {
				r.t.Fatalf("block %d lost its signature (stripped %v)", i, a.Stripped())
			}
		}
		if b.Hash != sealed.Hash || b.PrevHash != sealed.PrevHash || b.ProducerID != sealed.ProducerID ||
			b.StateRoot != sealed.StateRoot || !b.Timestamp.Equal(sealed.Timestamp) || len(b.Transactions) != len(sealed.Transactions) {
			r.t.Fatalf("block %d changed", i)
		}
	}
	if tip, ok := r.bp.LatestBlock(); !ok || tip.ProducerAuth.Stripped() || VerifyBlockSignature(tip) != nil {
		r.t.Fatal("the tip does not hold its verified signature")
	}
}

// requireRelayed requires RelayBlocks to relay every block of the chain as
// exactly its journal line and as the sealed block encodes, verified.
func (r *appendRig) requireRelayed() {
	r.t.Helper()
	requireRelaysJournal(r.t, r.bp, r.sealed, r.lines())
}

// TestAppendedBlocksKeepSignaturesInTheJournal: with the journal attached,
// every block appended while the node runs, whether it produced it or
// received it, holds its signature only in the journal once the next block is
// durable; the tip keeps its signature, and so does the tip the start
// restored; every block relays exactly as before.
func TestAppendedBlocksKeepSignaturesInTheJournal(t *testing.T) {
	f := newJournalFixture(t, 22)
	const boot = 10
	for _, tc := range []struct {
		name   string
		append func(r *appendRig, i int)
	}{
		{"received", func(r *appendRig, i int) { r.follow(f.blocks[i]) }},
		{"produced", func(r *appendRig, i int) { r.produce() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newAppendRig(t, f, boot, true)
			for i := boot; i < len(f.blocks); i++ {
				tc.append(r, i)
				tip := uint64(i)
				r.requireSignatures(func(h uint64) bool {
					return h >= jfSigFrom && h < tip && h != boot-1 // the restored tip keeps it
				})
			}
			if r.failed != 0 {
				t.Fatalf("%d journal appends failed", r.failed)
			}
			r.requireRelayed()
			requireKeysShared(t, r.bp.AllBlocks()[jfCheckpoint+1:])
		})
	}
	t.Run("fresh chain", func(t *testing.T) {
		r := newAppendRig(t, f, 0, true)
		for _, b := range f.blocks {
			r.follow(b)
		}
		tip := f.blocks[len(f.blocks)-1].Height
		r.requireSignatures(func(h uint64) bool { return h >= jfSigFrom && h < tip })
		r.requireRelayed()
		// The fresh chain's journal is the journal RelayBlocks reads.
		if got := r.bp.journalPath; got != r.path {
			t.Fatalf("journalPath %q, want %q", got, r.path)
		}
	})
}

// TestAppendStripChangesNothingElse: the journal is byte for byte the journal
// a producer without it attached writes, a block taken from the chain before
// its signature left memory still encodes and verifies as before, and the
// chain then holds a stripped copy in its place, never the block itself
// changed.
func TestAppendStripChangesNothingElse(t *testing.T) {
	f := newJournalFixture(t, 16)
	const boot = 8
	plain := newAppendRig(t, f, boot, false)
	keep := newAppendRig(t, f, boot, true)
	for _, sealed := range f.blocks[boot:] {
		plain.follow(sealed)
		before, _ := keep.bp.GetBlock(sealed.Height - 1) // taken before the next append
		beforeJSON := mustMarshal(t, before)
		keep.follow(sealed)
		if sealed.Height-1 == boot-1 {
			continue // the restored tip keeps its signature
		}
		after, _ := keep.bp.GetBlock(sealed.Height - 1)
		if after == before || !after.ProducerAuth.Stripped() {
			t.Fatalf("block %d: the chain does not hold a stripped copy", sealed.Height-1)
		}
		if before.ProducerAuth.Stripped() || VerifyBlockSignature(before) != nil {
			t.Fatalf("block %d was changed after a reader took it", before.Height)
		}
		if got := mustMarshal(t, before); !bytes.Equal(got, beforeJSON) {
			t.Fatalf("block %d encodes differently after its signature left memory", before.Height)
		}
		if _, err := json.Marshal(after); !errors.Is(err, ErrBlockSignatureStripped) {
			t.Fatalf("the stripped copy of block %d encodes: %v", after.Height, err)
		}
	}
	plainData, err := os.ReadFile(plain.path)
	if err != nil {
		t.Fatal(err)
	}
	keepData, err := os.ReadFile(keep.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plainData, keepData) {
		t.Fatal("the journal differs from the journal written without the producer attached")
	}
	plain.requireSignatures(func(h uint64) bool { return h >= jfSigFrom && h < boot-1 })
	plain.requireRelayed()
	keep.requireRelayed()
	// Both relay the same bytes.
	a, errA := plain.bp.RelayBlocks(0, ^uint64(0), 0)
	b, errB := keep.bp.RelayBlocks(0, ^uint64(0), 0)
	if errA != nil || errB != nil || !bytes.Equal(mustMarshal(t, a), mustMarshal(t, b)) {
		t.Fatalf("the relays differ (%v, %v)", errA, errB)
	}
}

// TestAppendStripRefusals: without a producer transition, with the start's
// signatures in another journal, or without a journal, nothing is stripped
// and the node behaves as before.
func TestAppendStripRefusals(t *testing.T) {
	f := newJournalFixture(t, 14)
	const boot = 8
	t.Run("no producer transition", func(t *testing.T) {
		path, _ := shareTestJournal(t, f.blocks[:boot])
		loaded, err := LoadChainNDJSON(path)
		if err != nil {
			t.Fatal(err)
		}
		bp := NewBlockProducer(mempool.New(mempool.DefaultConfig()), applierAfter(t, f.blocks[:boot]), DefaultProducerConfig())
		if err := bp.RestoreChain(loaded); err != nil {
			t.Fatal(err)
		}
		j, err := OpenChainJournal(path, f.blocks[boot-1])
		if err != nil {
			t.Fatal(err)
		}
		defer j.Close()
		if err := bp.KeepSignaturesInJournal(j); err == nil || !strings.Contains(err.Error(), "no producer transition") {
			t.Fatalf("KeepSignaturesInJournal without a transition: %v", err)
		}
		bp.OnSealedBlock = func(b *Block) {
			if err := j.Append(b); err != nil {
				t.Error(err)
			}
		}
		chain := append([]*Block(nil), loaded...)
		for _, sealed := range f.blocks[boot:] {
			var wire Block
			if err := json.Unmarshal(mustMarshal(t, sealed), &wire); err != nil {
				t.Fatal(err)
			}
			if err := bp.TryAppendExternalBlock(&wire); err != nil {
				t.Fatal(err)
			}
			chain = append(chain, &wire)
		}
		for i, b := range bp.AllBlocks() {
			if b.ProducerAuth.Stripped() || b != chain[i] || !bytes.Equal(mustMarshal(t, b), mustMarshal(t, f.blocks[i])) {
				t.Fatalf("block %d was stripped or replaced", b.Height)
			}
		}
		if bp.journalPath != "" {
			t.Fatalf("journalPath %q", bp.journalPath)
		}
	})
	t.Run("another journal", func(t *testing.T) {
		r := newAppendRig(t, f, boot, false)
		other := filepath.Join(t.TempDir(), "qsdm_chain.ndjson")
		data, err := os.ReadFile(r.path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(other, data, 0o644); err != nil {
			t.Fatal(err)
		}
		j, err := OpenChainJournal(other, f.blocks[boot-1])
		if err != nil {
			t.Fatal(err)
		}
		defer j.Close()
		if err := r.bp.KeepSignaturesInJournal(j); err == nil || !strings.Contains(err.Error(), "not in") {
			t.Fatalf("KeepSignaturesInJournal of another journal: %v", err)
		}
		r.bp.OnSealedBlock = func(b *Block) {
			if err := j.Append(b); err != nil {
				t.Error(err)
			}
		}
		for _, sealed := range f.blocks[boot:] {
			r.follow(sealed)
		}
		r.requireSignatures(func(h uint64) bool { return h >= jfSigFrom && h < boot-1 })
		if r.bp.journalPath != r.path {
			t.Fatalf("journalPath %q, want the start's %q", r.bp.journalPath, r.path)
		}
		// The start's stripped blocks relay from the start's journal, the
		// appended ones from memory.
		relayed, err := r.bp.RelayBlocks(0, ^uint64(0), 0)
		if err != nil || len(relayed) != len(f.blocks) {
			t.Fatalf("relayed %d blocks: %v", len(relayed), err)
		}
		for i, b := range relayed {
			if !bytes.Equal(mustMarshal(t, b), mustMarshal(t, f.blocks[i])) {
				t.Fatalf("block %d relayed differently", i)
			}
		}
	})
	t.Run("journal attached but never written", func(t *testing.T) {
		// A node without a journal: the appends alone strip nothing.
		r := newAppendRig(t, f, boot, true)
		r.bp.OnSealedBlock = nil
		for _, sealed := range f.blocks[boot:] {
			r.follow(sealed)
		}
		r.requireSignatures(func(h uint64) bool { return h >= jfSigFrom && h < boot-1 })
	})
	t.Run("journal not attached", func(t *testing.T) {
		r := newAppendRig(t, f, boot, false)
		for _, sealed := range f.blocks[boot:] {
			r.follow(sealed)
		}
		r.requireSignatures(func(h uint64) bool { return h >= jfSigFrom && h < boot-1 })
		r.requireRelayed()
	})
	t.Run("nil", func(t *testing.T) {
		var bp *BlockProducer
		if bp.KeepSignaturesInJournal(nil) == nil || f.producer(t, nil).KeepSignaturesInJournal(nil) == nil {
			t.Fatal("KeepSignaturesInJournal accepted a nil producer or journal")
		}
	})
}

// TestAppendStripWaitsForDurableLines: a block's signature leaves memory only
// once the next block's line is durable and right after its own. A failed
// fsync, a line Append cannot locate exactly, or a line that does not follow
// the one before it, drops nothing.
func TestAppendStripWaitsForDurableLines(t *testing.T) {
	f := newJournalFixture(t, 14)
	const boot = 8
	t.Run("failed fsync", func(t *testing.T) {
		r := newAppendRig(t, f, boot, true)
		for _, sealed := range f.blocks[boot : boot+3] {
			r.follow(sealed)
		}
		last := uint64(boot + 2)
		r.j.sync = func(*os.File) error { return errors.New("injected fsync failure") }
		r.follow(f.blocks[last+1]) // in the chain; its line is written, not durable
		if r.failed != 1 {
			t.Fatalf("%d journal appends failed, want 1", r.failed)
		}
		// The last durable block keeps its signature: its successor's line
		// is not durable.
		r.requireSignatures(func(h uint64) bool { return h >= jfSigFrom && h < last && h != boot-1 })
		r.bp.mu.Lock()
		tail := r.bp.appendTail.blk
		r.bp.mu.Unlock()
		if tail == nil || tail.Height != last {
			t.Fatalf("the producer remembers block %v, want the last durable block %d", tail, last)
		}
	})
	t.Run("foreign write during the append", func(t *testing.T) {
		r := newAppendRig(t, f, boot, true)
		r.follow(f.blocks[boot])
		r.follow(f.blocks[boot+1])
		r.j.sync = func(fh *os.File) error {
			w, err := os.OpenFile(r.path, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				return err
			}
			defer w.Close()
			if _, err := w.Write([]byte("\n")); err != nil {
				return err
			}
			return fh.Sync()
		}
		r.follow(f.blocks[boot+2]) // not located: the file grew by more than its line
		r.j.sync = nil
		r.follow(f.blocks[boot+3]) // located, but does not follow a located line
		r.follow(f.blocks[boot+4]) // located, right after boot+3
		r.requireSignatures(func(h uint64) bool {
			return h >= jfSigFrom && (h < boot-1 || h == boot || h == boot+3)
		})
		// The extra blank line moved nothing RelayBlocks reads.
		relayed, err := r.bp.RelayBlocks(0, ^uint64(0), 0)
		if err != nil || len(relayed) != len(r.sealed) {
			t.Fatalf("relayed %d blocks: %v", len(relayed), err)
		}
		for i, b := range relayed {
			if !bytes.Equal(mustMarshal(t, b), mustMarshal(t, r.sealed[i])) {
				t.Fatalf("block %d relayed differently", i)
			}
		}
	})
	t.Run("gap between the lines", func(t *testing.T) {
		r := newAppendRig(t, f, boot, true)
		r.follow(f.blocks[boot])
		r.follow(f.blocks[boot+1])
		// Something writes to the journal between two appends.
		w, err := os.OpenFile(r.path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("\n")); err != nil {
			t.Fatal(err)
		}
		w.Close()
		r.follow(f.blocks[boot+2]) // located, but not right after boot+1's line
		r.follow(f.blocks[boot+3])
		r.requireSignatures(func(h uint64) bool {
			return h >= jfSigFrom && (h < boot-1 || h == boot || h == boot+2)
		})
	})
	t.Run("journal renamed or replaced", func(t *testing.T) {
		r := newAppendRig(t, f, boot, true)
		r.follow(f.blocks[boot])
		r.follow(f.blocks[boot+1])
		moved := r.path + ".moved"
		if err := os.Rename(r.path, moved); err != nil {
			// Windows refuses to rename the open journal: nothing to test.
			t.Skipf("rename the open journal: %v", err)
		}
		r.follow(f.blocks[boot+2]) // written to the moved file: no file at the path
		data, err := os.ReadFile(moved)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(r.path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		r.follow(f.blocks[boot+3]) // the path names another file, equal so far
		r.follow(f.blocks[boot+4])
		r.requireSignatures(func(h uint64) bool { return h >= jfSigFrom && (h < boot-1 || h == boot) })
	})
}

// TestAppendStripRefusesAChangedJournal: a journal changed under the running
// node is refused for a stripped appended block, never relayed wrong: the
// blocks before it are relayed, with the error.
func TestAppendStripRefusesAChangedJournal(t *testing.T) {
	f := newJournalFixture(t, 16)
	const boot, target = 8, 11 // target: an appended block, stripped
	for _, tc := range []struct {
		name  string
		first uint64 // the first block that cannot be read back
		edit  func(t *testing.T, data []byte, lines [][]byte) []byte
	}{
		{"changed signature byte", target, func(t *testing.T, data []byte, lines [][]byte) []byte {
			i := lineSignature(t, data, lines, target) + 10
			if data[i] == 'A' {
				data[i] = 'B'
			} else {
				data[i] = 'A'
			}
			return data
		}},
		{"line naming another hash", target, func(t *testing.T, data []byte, lines [][]byte) []byte {
			line := data[lineStart(lines, target):]
			i := bytes.Index(line, []byte(`"hash":"`)) + len(`"hash":"`)
			line[i] ^= 1
			return data
		}},
		{"swapped lines", target, func(t *testing.T, data []byte, lines [][]byte) []byte {
			order := append([][]byte(nil), lines...)
			order[target], order[target+1] = order[target+1], order[target]
			return append(bytes.Join(order, []byte("\n")), '\n')
		}},
		{"truncated journal", target, func(t *testing.T, data []byte, lines [][]byte) []byte {
			return data[:lineStart(lines, target)+len(lines[target])/2]
		}},
		{"rewritten journal", jfSigFrom, func(t *testing.T, data []byte, lines [][]byte) []byte {
			return append([]byte("\n"), data...) // every offset moves
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newAppendRig(t, f, boot, true)
			for _, sealed := range f.blocks[boot:] {
				r.follow(sealed)
			}
			lines := r.lines()
			data, err := os.ReadFile(r.path)
			if err != nil {
				t.Fatal(err)
			}
			// The journal is still open for append, as in a running node.
			if err := os.WriteFile(r.path, tc.edit(t, data, lines), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := r.bp.RelayBlocks(0, ^uint64(0), 0)
			if err == nil {
				t.Fatal("a changed journal was relayed without an error")
			}
			if !strings.Contains(err.Error(), fmt.Sprintf("block %d", tc.first)) {
				t.Fatalf("error %v does not name block %d", err, tc.first)
			}
			if uint64(len(got)) != tc.first {
				t.Fatalf("relayed %d blocks before the refusal, want %d", len(got), tc.first)
			}
			for i, b := range got {
				if !bytes.Equal(mustMarshal(t, b), lines[i]) {
					t.Fatalf("block %d relayed wrong", i)
				}
			}
			if one, err := r.bp.RelayBlocks(tc.first, tc.first, 0); err == nil || len(one) != 0 {
				t.Fatalf("block %d alone: %d blocks, %v", tc.first, len(one), err)
			}
			// The tip keeps its signature and needs no journal.
			tip := uint64(len(f.blocks) - 1)
			if one, err := r.bp.RelayBlocks(tip, tip, 0); err != nil || len(one) != 1 || !bytes.Equal(mustMarshal(t, one[0]), lines[tip]) {
				t.Fatalf("tip: %d blocks, %v", len(one), err)
			}
		})
	}
}

// TestAppendStripConcurrentReaders: readers that take blocks from the chain
// and encode them outside the producer lock, as the API and the P2P
// providers do, while blocks are appended and their predecessors stripped,
// always get complete blocks that encode exactly as sealed. Run with -race.
func TestAppendStripConcurrentReaders(t *testing.T) {
	f := newJournalFixture(t, 40)
	const boot = 8
	r := newAppendRig(t, f, boot, true)
	want := make([][]byte, len(f.blocks))
	for i, b := range f.blocks {
		want[i] = mustMarshal(t, b)
	}
	stop := make(chan struct{})
	errs := make(chan error, 16)
	report := func(err error) {
		select {
		case errs <- err:
		default:
		}
	}
	var wg sync.WaitGroup
	var rounds [4]atomic.Int64 // each reader's completed reads
	reader := func(n *atomic.Int64, read func() error) {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := read(); err != nil {
				report(err)
				return
			}
			n.Add(1)
		}
	}
	wg.Add(4)
	// RelayBlocks, as GET /api/v1/chain/blocks and the P2P provider call it.
	go reader(&rounds[0], func() error {
		blocks, err := r.bp.RelayBlocks(0, ^uint64(0), 0)
		if err != nil {
			return err
		}
		for _, b := range blocks {
			if got := mustMarshalErr(b); !bytes.Equal(got, want[b.Height]) {
				return fmt.Errorf("RelayBlocks: block %d encodes as %.60q", b.Height, got)
			}
		}
		return nil
	})
	// A block taken by height and encoded later: either the complete block,
	// which must stay complete, or the stripped copy, which refuses.
	go reader(&rounds[1], func() error {
		for h := uint64(0); h <= r.bp.TipHeight(); h++ {
			b, ok := r.bp.GetBlock(h)
			if !ok {
				return fmt.Errorf("no block %d", h)
			}
			if b.ProducerAuth.Stripped() {
				if _, err := json.Marshal(b); !errors.Is(err, ErrBlockSignatureStripped) {
					return fmt.Errorf("stripped block %d encodes: %v", h, err)
				}
				continue
			}
			if got := mustMarshalErr(b); !bytes.Equal(got, want[h]) {
				return fmt.Errorf("block %d encodes as %.60q", h, got)
			}
		}
		return nil
	})
	// The tip, as LatestBlock returns it, verifies.
	go reader(&rounds[2], func() error {
		tip, ok := r.bp.LatestBlock()
		if !ok {
			return errors.New("no tip")
		}
		if err := VerifyBlockSignature(tip); err != nil {
			return fmt.Errorf("tip %d: %w", tip.Height, err)
		}
		return nil
	})
	// Headers, the dashboard's view.
	go reader(&rounds[3], func() error {
		for _, hdr := range r.bp.Headers(0, ^uint64(0)) {
			if hdr.Hash != f.blocks[hdr.Height].Hash {
				return fmt.Errorf("header %d", hdr.Height)
			}
		}
		return nil
	})
	// Every reader is running before the first append, and keeps reading
	// until the last one.
	for i := range rounds {
		for rounds[i].Load() == 0 && len(errs) == 0 {
			time.Sleep(time.Millisecond)
		}
	}
	for _, sealed := range f.blocks[boot:] {
		r.follow(sealed)
	}
	close(stop)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	t.Logf("reads during %d appends: relay %d, by height %d, tip %d, headers %d", len(f.blocks)-boot,
		rounds[0].Load(), rounds[1].Load(), rounds[2].Load(), rounds[3].Load())
	tip := uint64(len(f.blocks) - 1)
	r.requireSignatures(func(h uint64) bool { return h >= jfSigFrom && h < tip && h != boot-1 })
	r.requireRelayed()
}

// mustMarshalErr is json.Marshal, with the error as the encoding.
func mustMarshalErr(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		return []byte("error: " + err.Error())
	}
	return data
}
