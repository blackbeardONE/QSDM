package chain

// Tests for journal_check.go: from one read, on any number of workers,
// CheckJournal returns exactly what LoadChainNDJSON, a hash check of every
// block and ValidateProducerTransitionChain return for the same journal,
// whatever is wrong with it and however many things are, and keeps every
// signature but the last block's only in the journal. The fixture is in
// verified_chain_test.go.

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

// journalChecksBefore is what qsdm --snapshot-import and --hl1-tail-replay
// ran on a journal before CheckJournal: LoadChainNDJSON, the first block
// whose hash is not its contents' hash, and, for a journal that loads,
// ValidateProducerTransitionChain under policy.
func journalChecksBefore(t *testing.T, path string, policy *producerpolicy.Transition) (blocks []*Block, loadErr error, badHash int, verifyErr error) {
	t.Helper()
	badHash = -1
	if blocks, loadErr = LoadChainNDJSON(path); loadErr != nil {
		return blocks, loadErr, badHash, nil
	}
	for i, b := range blocks {
		if ComputeBlockHash(b) != b.Hash {
			badHash = i
			break
		}
	}
	if policy != nil {
		bp := NewBlockProducer(mempool.New(mempool.DefaultConfig()), newTestApplier(), DefaultProducerConfig())
		if err := bp.SetProducerTransition(policy); err != nil {
			t.Fatal(err)
		}
		verifyErr = bp.ValidateProducerTransitionChain(blocks)
	}
	return blocks, nil, badHash, verifyErr
}

func errText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// requireCheckMatches runs CheckJournal on path with each of workers and
// requires the result journalChecksBefore finds: the same blocks, the same
// errors (text and kind), the same first bad hash, a VerifiedChain exactly
// when nothing failed, and every signature but the last block's stripped
// under a producer transition (none without one). A VerifiedChain must
// restore and relay the journal's blocks. lines are the journal's lines when
// it has no blank or CRLF line (nil skips that comparison). It returns what
// the checks found.
func requireCheckMatches(t *testing.T, path string, policy *producerpolicy.Transition, lines [][]byte, workers ...int) (loadErr error, badHash int, verifyErr error) {
	t.Helper()
	blocks, loadErr, badHash, verifyErr := journalChecksBefore(t, path, policy)
	for _, w := range workers {
		c, err := checkJournal(path, policy, w)
		if err != nil {
			t.Fatalf("%d workers: %v", w, err)
		}
		if errText(c.LoadErr) != errText(loadErr) {
			t.Fatalf("%d workers: LoadErr\n  %s\nLoadChainNDJSON\n  %s", w, errText(c.LoadErr), errText(loadErr))
		}
		if len(c.Blocks) != len(blocks) {
			t.Fatalf("%d workers: %d blocks, LoadChainNDJSON %d", w, len(c.Blocks), len(blocks))
		}
		for i, b := range c.Blocks {
			want := blocks[i]
			if b.Height != want.Height || b.Hash != want.Hash || b.PrevHash != want.PrevHash || b.StateRoot != want.StateRoot ||
				b.ProducerID != want.ProducerID || !bytes.Equal(b.ProducerAuth.PublicKey, want.ProducerAuth.PublicKey) ||
				len(b.Transactions) != len(want.Transactions) || ComputeBlockHash(b) != ComputeBlockHash(want) {
				t.Fatalf("%d workers: block %d differs from LoadChainNDJSON's", w, i)
			}
			signed := len(want.ProducerAuth.PublicKey) > 0 && len(want.ProducerAuth.Signature) > 0
			if policy != nil && signed && i < len(c.Blocks)-1 {
				if !b.ProducerAuth.Stripped() || b.ProducerAuth.Signature != nil || !b.ProducerAuth.Signed() {
					t.Fatalf("%d workers: block %d still holds its signature in memory", w, i)
				}
			} else if b.ProducerAuth.Stripped() || !bytes.Equal(b.ProducerAuth.Signature, want.ProducerAuth.Signature) {
				t.Fatalf("%d workers: block %d lost its signature", w, i)
			}
		}
		if c.BadHash != badHash {
			t.Fatalf("%d workers: BadHash %d, want %d", w, c.BadHash, badHash)
		}
		if errText(c.VerifyErr) != errText(verifyErr) {
			t.Fatalf("%d workers: VerifyErr\n  %s\nValidateProducerTransitionChain\n  %s", w, errText(c.VerifyErr), errText(verifyErr))
		}
		for _, kind := range []error{ErrBlockBadSignature, ErrBlockUnsigned, ErrBlockSignatureStripped, ErrExternalProducerNotAuthorized, errTransitionNoGenesis, errTransitionIncomplete} {
			if errors.Is(c.VerifyErr, kind) != errors.Is(verifyErr, kind) {
				t.Fatalf("%d workers: VerifyErr %v: errors.Is(%v) differs", w, c.VerifyErr, kind)
			}
		}
		if wantVerified := policy != nil && loadErr == nil && verifyErr == nil; (c.Verified != nil) != wantVerified {
			t.Fatalf("%d workers: Verified %v, want one: %v", w, c.Verified != nil, wantVerified)
		}
		if c.Verified == nil {
			continue
		}
		bp := NewBlockProducer(mempool.New(mempool.DefaultConfig()), newTestApplier(), DefaultProducerConfig())
		if err := bp.SetProducerTransition(policy); err != nil {
			t.Fatal(err)
		}
		if err := bp.RestoreVerifiedChain(c.Blocks, c.Verified); err != nil {
			t.Fatalf("%d workers: RestoreVerifiedChain: %v", w, err)
		}
		requireRelaysJournal(t, bp, blocks, lines)
	}
	return loadErr, badHash, verifyErr
}

// journalCheckCase is one journal: the fixture's blocks after edit, as
// lines, then raw applied to the bytes (nil: none).
type journalCheckCase struct {
	name string
	edit func(t *testing.T, f journalFixture) []*Block
	raw  func(data []byte) []byte
}

// replaceAt returns a copy of blocks with the block at index i replaced by b.
func replaceAt(blocks []*Block, i int, b *Block) []*Block {
	out := append([]*Block(nil), blocks...)
	out[i] = b
	return out
}

// tamperSignature flips one bit of the signature of the block at index i.
func tamperSignature(blocks []*Block, i, at int) []*Block {
	b := *blocks[i]
	b.ProducerAuth.Signature = append([]byte(nil), b.ProducerAuth.Signature...)
	b.ProducerAuth.Signature[at%len(b.ProducerAuth.Signature)] ^= 1
	return replaceAt(blocks, i, &b)
}

// changeContents changes the state root of the block at index i; with rehash
// its hash is recomputed (it then no longer matches its signature or its
// child's link).
func changeContents(blocks []*Block, i int, rehash bool) []*Block {
	b := *blocks[i]
	b.StateRoot = fmt.Sprintf("%064x", i+1)
	if rehash {
		b.Hash = computeBlockHash(&b)
	}
	return replaceAt(blocks, i, &b)
}

// relink re-signs the block at index i on the block two before it: valid on
// its own, but it does not extend the block before it.
func relink(t *testing.T, f journalFixture, blocks []*Block, i int) []*Block {
	t.Helper()
	b := *blocks[i]
	b.PrevHash = blocks[i-2].Hash
	b.ProducerID = "legacy-peer"
	b.ProducerAuth = BFTWireAuth{}
	b.Hash = computeBlockHash(&b)
	key := f.oldKey
	if i > jfCheckpoint {
		key = f.newKey
	}
	if err := SignBlock(&b, key); err != nil {
		t.Fatal(err)
	}
	return replaceAt(blocks, i, &b)
}

func writeJournal(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func insertLine(data []byte, line int, s string) []byte {
	parts := bytes.SplitAfter(data, []byte("\n"))
	if line > len(parts) {
		line = len(parts)
	}
	out := append([]byte(nil), bytes.Join(parts[:line], nil)...)
	out = append(out, s...)
	return append(out, bytes.Join(parts[line:], nil)...)
}

func journalCheckCases() []journalCheckCase {
	same := func(t *testing.T, f journalFixture) []*Block { return f.blocks }
	return []journalCheckCase{
		{name: "intact", edit: same},
		{name: "tampered signature", edit: func(t *testing.T, f journalFixture) []*Block { return tamperSignature(f.blocks, 9, 100) }},
		{name: "tampered first signature", edit: func(t *testing.T, f journalFixture) []*Block { return tamperSignature(f.blocks, jfSigFrom, 0) }},
		{name: "tampered tip signature", edit: func(t *testing.T, f journalFixture) []*Block {
			return tamperSignature(f.blocks, len(f.blocks)-1, 7)
		}},
		{name: "two tampered signatures", edit: func(t *testing.T, f journalFixture) []*Block {
			return tamperSignature(tamperSignature(f.blocks, 9, 1), 11, 2)
		}},
		{name: "old producer after the hand-over", edit: func(t *testing.T, f journalFixture) []*Block {
			b := *f.blocks[8]
			if err := SignBlock(&b, f.oldKey); err != nil {
				t.Fatal(err)
			}
			return resealFrom(t, f, replaceBlock(f.blocks, &b), 9)
		}},
		{name: "unsigned block in an epoch", edit: func(t *testing.T, f journalFixture) []*Block {
			b := *f.blocks[4]
			b.ProducerAuth = BFTWireAuth{}
			b.ProducerID = "legacy-peer"
			b.Hash = computeBlockHash(&b)
			return resealFrom(t, f, replaceBlock(f.blocks, &b), 5)
		}},
		{name: "changed contents", edit: func(t *testing.T, f journalFixture) []*Block { return changeContents(f.blocks, 8, false) }},
		{name: "changed contents, rehashed", edit: func(t *testing.T, f journalFixture) []*Block { return changeContents(f.blocks, 8, true) }},
		{name: "forked duplicate", edit: func(t *testing.T, f journalFixture) []*Block {
			return append(append([]*Block(nil), f.blocks...), f.blocks[len(f.blocks)-1])
		}},
		{name: "duplicate in the middle", edit: func(t *testing.T, f journalFixture) []*Block {
			return append(append(append([]*Block(nil), f.blocks[:6]...), f.blocks[5]), f.blocks[6:]...)
		}},
		{name: "gap", edit: func(t *testing.T, f journalFixture) []*Block {
			return append(append([]*Block(nil), f.blocks[:5]...), f.blocks[6:]...)
		}},
		{name: "swapped blocks", edit: func(t *testing.T, f journalFixture) []*Block {
			out := append([]*Block(nil), f.blocks...)
			out[9], out[10] = out[10], out[9]
			return out
		}},
		{name: "no genesis", edit: func(t *testing.T, f journalFixture) []*Block { return f.blocks[1:] }},
		{name: "genesis with a parent", edit: func(t *testing.T, f journalFixture) []*Block {
			b := *f.blocks[0]
			b.PrevHash = strings.Repeat("0", 64)
			b.Hash = computeBlockHash(&b)
			return resealFrom(t, f, replaceBlock(f.blocks, &b), 1)
		}},
		{name: "before the checkpoint", edit: func(t *testing.T, f journalFixture) []*Block { return f.blocks[:jfCheckpoint] }},
		{name: "wrong height, rehashed and re-signed", edit: func(t *testing.T, f journalFixture) []*Block {
			b := *f.blocks[10]
			b.Height = 50
			b.ProducerID = "legacy-peer"
			b.ProducerAuth = BFTWireAuth{}
			b.Hash = computeBlockHash(&b)
			if err := SignBlock(&b, f.newKey); err != nil {
				t.Fatal(err)
			}
			return replaceAt(f.blocks, 10, &b)
		}},
		{name: "broken link before a bad signature", edit: func(t *testing.T, f journalFixture) []*Block {
			return tamperSignature(relink(t, f, f.blocks, 10), 12, 3)
		}},
		{name: "bad signature before a broken link", edit: func(t *testing.T, f journalFixture) []*Block {
			return relink(t, f, tamperSignature(f.blocks, 8, 3), 11)
		}},
		{name: "bad signature before a bad hash", edit: func(t *testing.T, f journalFixture) []*Block {
			return changeContents(tamperSignature(f.blocks, 8, 3), 11, false)
		}},
		{name: "bad hash before a bad signature", edit: func(t *testing.T, f journalFixture) []*Block {
			return tamperSignature(changeContents(f.blocks, 8, false), 11, 3)
		}},
		{name: "bad signature and a broken link on one block", edit: func(t *testing.T, f journalFixture) []*Block {
			return tamperSignature(relink(t, f, f.blocks, 10), 10, 3)
		}},
		{name: "parse error after a bad signature", edit: func(t *testing.T, f journalFixture) []*Block { return tamperSignature(f.blocks, 9, 5) },
			raw: func(d []byte) []byte { return append(d, "{not json\n"...) }},
		{name: "parse error in the middle", edit: same, raw: func(d []byte) []byte { return insertLine(d, 6, "{\"height\":6,\n") }},
		{name: "parse error before a bad hash", edit: func(t *testing.T, f journalFixture) []*Block { return changeContents(f.blocks, 11, false) },
			raw: func(d []byte) []byte { return insertLine(d, 4, "]\n") }},
		{name: "null line", edit: same, raw: func(d []byte) []byte { return insertLine(d, 6, "null\n") }},
		{name: "unterminated last line", edit: same, raw: func(d []byte) []byte { return bytes.TrimSuffix(d, []byte("\n")) }},
		{name: "torn last line", edit: same, raw: func(d []byte) []byte { return append(d, `{"height":14,"prev`...) }},
		{name: "line over 4 MiB", edit: same, raw: func(d []byte) []byte {
			return insertLine(d, 9, `{"height":9,"x":"`+strings.Repeat("a", 4<<20)+"\"}\n")
		}},
		{name: "blank and CRLF lines", edit: same, raw: func(d []byte) []byte {
			d = insertLine(d, 3, "\n")
			return bytes.Replace(d, []byte("}\n"), []byte("}\r\n"), 4)
		}},
		{name: "empty journal", edit: same, raw: func([]byte) []byte { return nil }},
		{name: "blank lines only", edit: same, raw: func([]byte) []byte { return []byte("\n\n\r\n") }},
	}
}

// TestCheckJournalMatchesTheChecksItReplaces: for every journal, on 1, 2, 4
// and 16 workers, with and without the producer transition, CheckJournal
// returns what the three checks it replaces return.
func TestCheckJournalMatchesTheChecksItReplaces(t *testing.T) {
	f := newJournalFixture(t, 14)
	for _, tc := range journalCheckCases() {
		t.Run(tc.name, func(t *testing.T) {
			data := marshalTransitionBlocks(t, tc.edit(t, f))
			var lines [][]byte
			if tc.raw != nil {
				data = tc.raw(data)
			} else {
				lines = bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
			}
			path := filepath.Join(t.TempDir(), "qsdm_chain.ndjson")
			writeJournal(t, path, data)
			for _, policy := range []*producerpolicy.Transition{f.policy, nil} {
				requireCheckMatches(t, path, policy, lines, 1, 2, 4, 16)
			}
		})
	}
	t.Run("missing journal", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "absent.ndjson")
		for _, policy := range []*producerpolicy.Transition{f.policy, nil} {
			requireCheckMatches(t, path, policy, nil, 1, 4)
		}
	})
}

// TestCheckJournalRandomDefects: journals with one to four defects at random
// places, the kinds above mixed, give CheckJournal exactly the results of
// the checks it replaces: the first failure of each check is the one each
// check reports, wherever the others are.
func TestCheckJournalRandomDefects(t *testing.T) {
	f := newJournalFixture(t, 14)
	n := len(f.blocks)
	rounds := 300
	if testing.Short() {
		rounds = 60
	}
	rng := rand.New(rand.NewSource(20261107))
	// Every edit takes a slice of at least three blocks.
	edits := []func(t *testing.T, blocks []*Block) []*Block{
		func(t *testing.T, b []*Block) []*Block {
			h := rng.Intn(len(b))
			if len(b[h].ProducerAuth.Signature) == 0 {
				return b
			}
			return tamperSignature(b, h, rng.Int())
		},
		func(t *testing.T, b []*Block) []*Block { return changeContents(b, 1+rng.Intn(len(b)-1), false) },
		func(t *testing.T, b []*Block) []*Block { return changeContents(b, 1+rng.Intn(len(b)-1), true) },
		func(t *testing.T, b []*Block) []*Block { return relink(t, f, b, 2+rng.Intn(len(b)-2)) },
		func(t *testing.T, b []*Block) []*Block { // signed by the producer of the other epoch
			i := rng.Intn(len(b))
			blk := *b[i]
			key := f.oldKey
			if blk.Height <= jfCheckpoint {
				key = f.newKey
			}
			blk.ProducerID = "legacy-peer"
			blk.ProducerAuth = BFTWireAuth{}
			blk.Hash = computeBlockHash(&blk)
			if err := SignBlock(&blk, key); err != nil {
				t.Fatal(err)
			}
			return replaceAt(b, i, &blk)
		},
		func(t *testing.T, b []*Block) []*Block { // drop a block
			h := rng.Intn(len(b))
			return append(append([]*Block(nil), b[:h]...), b[h+1:]...)
		},
		func(t *testing.T, b []*Block) []*Block { // repeat a block
			h := rng.Intn(len(b))
			return append(append(append([]*Block(nil), b[:h+1]...), b[h]), b[h+1:]...)
		},
		func(t *testing.T, b []*Block) []*Block { // swap two neighbours
			out := append([]*Block(nil), b...)
			h := rng.Intn(len(b) - 1)
			out[h], out[h+1] = out[h+1], out[h]
			return out
		},
		func(t *testing.T, b []*Block) []*Block { return b[:1+rng.Intn(len(b))] }, // truncate
		func(t *testing.T, b []*Block) []*Block { // a block of the fixture from elsewhere in the chain
			out := append([]*Block(nil), b...)
			out[rng.Intn(len(out))] = f.blocks[rng.Intn(n)]
			return out
		},
	}
	raws := []func(d []byte) []byte{
		func(d []byte) []byte { return insertLine(d, rng.Intn(n+1), "{not json\n") },
		func(d []byte) []byte { return insertLine(d, rng.Intn(n+1), "null\n") },
		func(d []byte) []byte { return insertLine(d, rng.Intn(n+1), "\n") },
		func(d []byte) []byte { return insertLine(d, rng.Intn(n+1), "{}\n") },
	}
	dir := t.TempDir()
	seen := map[string]int{} // what the checks found, by kind: the rounds must cover every kind
	for r := 0; r < rounds; r++ {
		blocks := append([]*Block(nil), f.blocks...)
		var applied []int
		for k := 1 + rng.Intn(4); k > 0 && len(blocks) > 2; k-- {
			e := rng.Intn(len(edits))
			blocks = edits[e](t, blocks)
			applied = append(applied, e)
		}
		data := marshalTransitionBlocks(t, blocks)
		if rng.Intn(4) == 0 {
			e := rng.Intn(len(raws))
			data = raws[e](data)
			applied = append(applied, 100+e)
		}
		path := filepath.Join(dir, fmt.Sprintf("round-%03d.ndjson", r))
		writeJournal(t, path, data)
		t.Run(fmt.Sprintf("round %d edits %v", r, applied), func(t *testing.T) {
			loadErr, badHash, verifyErr := requireCheckMatches(t, path, f.policy, nil, 1+r%4, 16)
			switch {
			case loadErr != nil:
				seen["line that does not decode"]++
			case verifyErr == nil:
				seen["verified"]++
			case errors.Is(verifyErr, ErrBlockBadSignature):
				seen["bad signature"]++
			case errors.Is(verifyErr, ErrExternalProducerNotAuthorized) || errors.Is(verifyErr, ErrBlockUnsigned):
				seen["producer schedule"]++
			case errors.Is(verifyErr, errTransitionNoGenesis) || errors.Is(verifyErr, errTransitionIncomplete):
				seen["no genesis or incomplete"]++
			default:
				seen["height, hash, link or checkpoint"]++
			}
			if badHash >= 0 {
				seen["bad hash"]++
			}
			if r%5 == 0 {
				requireCheckMatches(t, path, nil, nil, 3)
			}
		})
	}
	t.Logf("%d rounds: %v", rounds, seen)
	for _, kind := range []string{"line that does not decode", "verified", "bad signature", "producer schedule", "no genesis or incomplete", "height, hash, link or checkpoint", "bad hash"} {
		if seen[kind] == 0 {
			t.Errorf("no round found %q: the rounds do not cover it", kind)
		}
	}
}

// TestCheckJournalRefusesAnInvalidTransition: the transition is validated
// before anything is read, and an empty path is an error.
func TestCheckJournalRefusesAnInvalidTransition(t *testing.T) {
	f := newJournalFixture(t, 10)
	bad := *f.policy
	bad.Version = 3
	if c, err := CheckJournal(f.path, &bad); err == nil || c != nil {
		t.Fatalf("CheckJournal under an invalid transition: %v %v", c, err)
	}
	if c, err := CheckJournal("", f.policy); err == nil || c != nil {
		t.Fatalf("CheckJournal of an empty path: %v %v", c, err)
	}
}
