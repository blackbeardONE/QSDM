package chain

// Tests for block_sig_journal.go: the startup load that verifies every block
// once and strips the verified signatures from memory, and RelayBlocks, which
// reads the stripped signatures back from the journal and refuses anything
// but the exact block. The fixture is in verified_chain_test.go.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// loadAndRestore is the startup path of main(): LoadVerifiedJournal, then
// RestoreVerifiedChain of the loaded blocks.
func (f journalFixture) loadAndRestore(t *testing.T) (*BlockProducer, *VerifiedChain, []*Block) {
	t.Helper()
	bp := f.producer(t, f.blocks)
	v, err := bp.LoadVerifiedJournal(f.path)
	if err != nil {
		t.Fatalf("LoadVerifiedJournal: %v", err)
	}
	blocks := v.Blocks()
	if len(blocks) != len(f.blocks) {
		t.Fatalf("loaded %d blocks, want %d", len(blocks), len(f.blocks))
	}
	if err := bp.RestoreVerifiedChain(blocks, v); err != nil {
		t.Fatalf("RestoreVerifiedChain: %v", err)
	}
	return bp, v, blocks
}

// requireRelaysJournal requires every block of bp to be relayed as exactly
// its journal line and as the sealed block encodes.
func requireRelaysJournal(t *testing.T, bp *BlockProducer, sealed []*Block, lines [][]byte) {
	t.Helper()
	relayed, err := bp.RelayBlocks(0, ^uint64(0), 0)
	if err != nil {
		t.Fatalf("RelayBlocks: %v", err)
	}
	if len(relayed) != len(sealed) {
		t.Fatalf("relayed %d blocks, want %d", len(relayed), len(sealed))
	}
	for i, b := range relayed {
		got := mustMarshal(t, b)
		if want := mustMarshal(t, sealed[i]); !bytes.Equal(got, want) {
			t.Fatalf("block %d: relayed %s\nwant %s", i, got, want)
		}
		if i < len(lines) && !bytes.Equal(got, lines[i]) {
			t.Fatalf("block %d: relayed block is not its journal line", i)
		}
		if VerifyBlockSignature(b) != nil || ComputeBlockHash(b) != b.Hash {
			t.Fatalf("block %d: the relayed block does not verify", i)
		}
	}
}

// TestLoadVerifiedJournalStripsVerifiedSignatures: every verified signature
// but the last block's is gone from memory, the blocks stay signed and keep
// their shared public key, and nothing else changes.
func TestLoadVerifiedJournalStripsVerifiedSignatures(t *testing.T) {
	f := newJournalFixture(t, 12)
	bp := f.producer(t, f.blocks)
	v, err := bp.LoadVerifiedJournal(f.path)
	if err != nil {
		t.Fatal(err)
	}
	loaded := v.Blocks()
	last := len(loaded) - 1
	for i, b := range loaded {
		sealed := f.blocks[i]
		switch {
		case !sealed.ProducerAuth.Signed():
			if b.ProducerAuth.Stripped() || b.ProducerAuth.Signed() || len(b.ProducerAuth.Signature) != 0 {
				t.Fatalf("unsigned block %d: %+v", i, b.ProducerAuth)
			}
		case i == last:
			if b.ProducerAuth.Stripped() || !bytes.Equal(b.ProducerAuth.Signature, sealed.ProducerAuth.Signature) {
				t.Fatal("the last block lost its signature")
			}
		default:
			if !b.ProducerAuth.Stripped() || len(b.ProducerAuth.Signature) != 0 || b.ProducerAuth.Signature != nil {
				t.Fatalf("block %d still holds %d signature bytes", i, len(b.ProducerAuth.Signature))
			}
			if !b.ProducerAuth.Signed() {
				t.Fatalf("stripped block %d reads as unsigned", i)
			}
			if !bytes.Equal(b.ProducerAuth.PublicKey, sealed.ProducerAuth.PublicKey) {
				t.Fatalf("block %d lost its public key", i)
			}
		}
		if b.Hash != sealed.Hash || b.PrevHash != sealed.PrevHash || b.ProducerID != sealed.ProducerID || b.StateRoot != sealed.StateRoot ||
			!b.Timestamp.Equal(sealed.Timestamp) || len(b.Transactions) != len(sealed.Transactions) {
			t.Fatalf("block %d changed", i)
		}
		if ComputeBlockHash(b) != b.Hash {
			t.Fatalf("block %d: hash does not recompute", i)
		}
	}
	// The stripped blocks still share one copy of each producer key.
	requireKeysShared(t, loaded[jfSigFrom:])
}

// TestStrippedBlockIsNeitherVerifiedNorUnsigned: a stripped block fails every
// verification with ErrBlockSignatureStripped and refuses to encode.
func TestStrippedBlockIsNeitherVerifiedNorUnsigned(t *testing.T) {
	f := newJournalFixture(t, 10)
	bp, _, loaded := f.loadAndRestore(t)
	stripped := loaded[jfCheckpoint+1]
	if !stripped.ProducerAuth.Stripped() {
		t.Fatal("fixture: block is not stripped")
	}
	// Signatures are optional here (the package default), so a stripped
	// block that read as unsigned would pass VerifyBlockSignature.
	if RequireSignedBlocks() {
		t.Fatal("fixture: signed blocks are required")
	}
	if err := VerifyBlockSignature(stripped); !errors.Is(err, ErrBlockSignatureStripped) {
		t.Fatalf("VerifyBlockSignature = %v, want ErrBlockSignatureStripped", err)
	}
	if err := validateTransitionBlock(bp.producerEpochs, stripped); !errors.Is(err, ErrBlockSignatureStripped) {
		t.Fatalf("validateTransitionBlock = %v, want ErrBlockSignatureStripped", err)
	}
	if err := verifyAuth(stripped.ProducerAuth, blockSigDigest(stripped), stripped.ProducerID); !errors.Is(err, ErrBlockSignatureStripped) {
		t.Fatalf("verifyAuth = %v, want ErrBlockSignatureStripped", err)
	}
	if _, err := json.Marshal(stripped); !errors.Is(err, ErrBlockSignatureStripped) {
		t.Fatalf("json.Marshal of a stripped block = %v, want ErrBlockSignatureStripped", err)
	}
	if _, err := json.Marshal([]*Block{loaded[0], stripped}); err == nil {
		t.Fatal("a slice holding a stripped block encoded")
	}
	// A copy of the struct is still stripped.
	cp := *stripped
	if err := VerifyBlockSignature(&cp); !errors.Is(err, ErrBlockSignatureStripped) {
		t.Fatalf("copy: %v", err)
	}
	// Nothing can append it: a fresh producer verifies it and refuses.
	fresh := f.producer(t, f.blocks[:stripped.Height])
	if err := fresh.RestoreChain(f.blocks[:stripped.Height]); err != nil {
		t.Fatal(err)
	}
	if err := fresh.TryAppendExternalBlock(&cp); !errors.Is(err, ErrBlockSignatureStripped) {
		t.Fatalf("TryAppendExternalBlock = %v, want ErrBlockSignatureStripped", err)
	}
	// Without a producer transition the external path checks it too.
	plain := NewBlockProducer(mempool.New(mempool.DefaultConfig()), applierAfter(t, f.blocks[:stripped.Height]), DefaultProducerConfig())
	if err := plain.RestoreChain(f.blocks[:stripped.Height]); err != nil {
		t.Fatal(err)
	}
	if err := plain.TryAppendExternalBlock(&cp); !errors.Is(err, ErrBlockSignatureStripped) {
		t.Fatalf("TryAppendExternalBlock without a transition = %v, want ErrBlockSignatureStripped", err)
	}
	// A block decoded from JSON is never stripped.
	var decoded Block
	if err := json.Unmarshal(f.lines[stripped.Height], &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ProducerAuth.Stripped() || VerifyBlockSignature(&decoded) != nil {
		t.Fatal("a decoded block is not a plain signed block")
	}
}

// TestBFTWireAuthEncodingUnchanged pins the JSON of an authenticator, which
// now has a MarshalJSON method: the same bytes as its plain fields.
func TestBFTWireAuthEncodingUnchanged(t *testing.T) {
	for _, tc := range []struct {
		auth BFTWireAuth
		want string
	}{
		{BFTWireAuth{}, `{}`},
		{BFTWireAuth{PublicKey: []byte{1, 2, 3}}, `{"public_key":"AQID"}`},
		{BFTWireAuth{Signature: []byte{0xfb, 0xff}}, `{"signature":"+/8="}`},
		{BFTWireAuth{PublicKey: []byte("<k>&"), Signature: []byte{}}, `{"public_key":"PGs+Jg=="}`},
		{BFTWireAuth{PublicKey: []byte{9}, Signature: []byte{8, 7}}, `{"public_key":"CQ==","signature":"CAc="}`},
	} {
		if got := string(mustMarshal(t, tc.auth)); got != tc.want {
			t.Fatalf("%+v encodes as %s, want %s", tc.auth, got, tc.want)
		}
	}
	m := BFTWirePrevoteMsg{Height: 3, Validator: "v", Auth: BFTWireAuth{PublicKey: []byte{1}, Signature: []byte{2}}}
	if got, want := string(mustMarshal(t, m)), `{"height":3,"round":0,"validator":"v","block_hash":"","auth":{"public_key":"AQ==","signature":"Ag=="}}`; got != want {
		t.Fatalf("prevote encodes as %s, want %s", got, want)
	}
	// A block encodes as a struct with the same fields and a plain
	// authenticator does, indented too.
	type plainAuth struct {
		PublicKey []byte `json:"public_key,omitempty"`
		Signature []byte `json:"signature,omitempty"`
	}
	type plainBlock struct {
		Height       uint64        `json:"height"`
		PrevHash     string        `json:"prev_hash"`
		Hash         string        `json:"hash"`
		Timestamp    time.Time     `json:"timestamp"`
		Transactions []*mempool.Tx `json:"transactions"`
		StateRoot    string        `json:"state_root"`
		TotalFees    float64       `json:"total_fees"`
		GasUsed      int64         `json:"gas_used"`
		ProducerID   string        `json:"producer_id"`
		ProducerAuth plainAuth     `json:"producer_auth,omitempty"`
	}
	f := newJournalFixture(t, 9)
	for _, b := range f.blocks {
		p := plainBlock{b.Height, b.PrevHash, b.Hash, b.Timestamp, b.Transactions, b.StateRoot, b.TotalFees, b.GasUsed, b.ProducerID,
			plainAuth{b.ProducerAuth.PublicKey, b.ProducerAuth.Signature}}
		if !bytes.Equal(mustMarshal(t, b), mustMarshal(t, p)) {
			t.Fatalf("block %d encodes differently from its plain fields", b.Height)
		}
		gotIndent, err1 := json.MarshalIndent(b, "", "  ")
		wantIndent, err2 := json.MarshalIndent(p, "", "  ")
		if err1 != nil || err2 != nil || !bytes.Equal(gotIndent, wantIndent) {
			t.Fatalf("block %d indents differently from its plain fields", b.Height)
		}
	}
}

// TestRelayBlocksServesTheOriginalJSON: every block, stripped or not, is
// relayed as exactly its journal line, which is how the sealed block encodes,
// and relaying leaves the chain in memory stripped and unchanged.
func TestRelayBlocksServesTheOriginalJSON(t *testing.T) {
	f := newJournalFixture(t, 14)
	bp, _, loaded := f.loadAndRestore(t)
	requireRelaysJournal(t, bp, f.blocks, f.lines)

	// Ranges and limits, as the API and P2P paths ask.
	for _, tc := range []struct {
		from, to uint64
		limit    int
		want     []uint64
	}{
		{0, 2, 0, []uint64{0, 1, 2}},
		{5, 9, 2, []uint64{5, 6}},
		{12, 100, 0, []uint64{12, 13}},
		{14, 20, 0, nil},
		{9, 3, 0, nil},
	} {
		got, err := bp.RelayBlocks(tc.from, tc.to, tc.limit)
		if err != nil {
			t.Fatal(err)
		}
		var hs []uint64
		for _, b := range got {
			hs = append(hs, b.Height)
			if !bytes.Equal(mustMarshal(t, b), f.lines[b.Height]) {
				t.Fatalf("block %d is not its journal line", b.Height)
			}
		}
		if fmt.Sprint(hs) != fmt.Sprint(tc.want) {
			t.Fatalf("RelayBlocks(%d, %d, %d) = %v, want %v", tc.from, tc.to, tc.limit, hs, tc.want)
		}
	}
	// The chain still holds the stripped blocks themselves, not the copies.
	for i, b := range bp.AllBlocks() {
		if b != loaded[i] {
			t.Fatalf("block %d in memory was replaced", i)
		}
		if i >= jfSigFrom && i < len(loaded)-1 && (!b.ProducerAuth.Stripped() || len(b.ProducerAuth.Signature) != 0) {
			t.Fatalf("block %d in memory holds its signature again", i)
		}
	}
}

// journalEdit rewrites the journal with edit applied to its bytes.
func journalEdit(t *testing.T, path string, edit func([]byte) []byte) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, edit(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// lineStart is the offset of journal line i (0-based).
func lineStart(lines [][]byte, i int) int {
	off := 0
	for _, l := range lines[:i] {
		off += len(l) + 1
	}
	return off
}

// lineSignature is the offset in data of the base64 producer signature of
// journal line i.
func lineSignature(t *testing.T, data []byte, lines [][]byte, i int) int {
	t.Helper()
	start := lineStart(lines, i)
	line := data[start : start+len(lines[i])]
	auth := bytes.Index(line, []byte(`"producer_auth":`))
	if auth < 0 {
		t.Fatal("no producer_auth in the line")
	}
	sig := bytes.Index(line[auth:], []byte(`"signature":"`))
	if sig < 0 {
		t.Fatal("no producer signature in the line")
	}
	return start + auth + sig + len(`"signature":"`)
}

// TestRelayBlocksRefusesAChangedJournal: a corrupted line, a changed
// signature byte, swapped lines, a rewritten or a removed journal are
// refused: the blocks before the first one that cannot be read back are
// relayed, never a wrong block.
func TestRelayBlocksRefusesAChangedJournal(t *testing.T) {
	const target = 8 // a stripped block, signed by the replacement producer
	for _, tc := range []struct {
		name string
		edit func(t *testing.T, f journalFixture) []byte
	}{
		{"corrupted line", func(t *testing.T, f journalFixture) []byte {
			data := marshalTransitionBlocks(t, f.blocks)
			copy(data[lineStart(f.lines, target):], "#garbage#")
			return data
		}},
		{"corrupted signature encoding", func(t *testing.T, f journalFixture) []byte {
			data := marshalTransitionBlocks(t, f.blocks)
			copy(data[lineSignature(t, data, f.lines, target)+10:], "#")
			return data
		}},
		{"changed signature byte", func(t *testing.T, f journalFixture) []byte {
			data := marshalTransitionBlocks(t, f.blocks)
			i := lineSignature(t, data, f.lines, target) + 10
			if data[i] == 'A' {
				data[i] = 'B'
			} else {
				data[i] = 'A'
			}
			return data
		}},
		{"swapped lines", func(t *testing.T, f journalFixture) []byte {
			order := append([]*Block(nil), f.blocks...)
			order[target], order[target+1] = order[target+1], order[target]
			return marshalTransitionBlocks(t, order)
		}},
		{"line naming another height", func(t *testing.T, f journalFixture) []byte {
			data := marshalTransitionBlocks(t, f.blocks)
			at := lineStart(f.lines, target)
			copy(data[at:], fmt.Sprintf(`{"height":%d,`, target+1))
			return data
		}},
		{"line naming another hash", func(t *testing.T, f journalFixture) []byte {
			data := marshalTransitionBlocks(t, f.blocks)
			line := data[lineStart(f.lines, target):]
			i := bytes.Index(line, []byte(`"hash":"`)) + len(`"hash":"`)
			line[i] ^= 1 // a hex digit becomes another
			return data
		}},
		{"rewritten journal", func(t *testing.T, f journalFixture) []byte {
			// The same blocks, one blank line earlier at the top: every
			// offset moves.
			return append([]byte("\n"), marshalTransitionBlocks(t, f.blocks)...)
		}},
		{"truncated journal", func(t *testing.T, f journalFixture) []byte {
			data := marshalTransitionBlocks(t, f.blocks)
			return data[:lineStart(f.lines, target)+len(f.lines[target])/2]
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newJournalFixture(t, 12)
			bp, _, _ := f.loadAndRestore(t)
			edited := tc.edit(t, f)
			journalEdit(t, f.path, func([]byte) []byte { return edited })
			first := uint64(target)
			if tc.name == "rewritten journal" {
				first = jfSigFrom // the first stripped block
			}
			got, err := bp.RelayBlocks(0, 11, 0)
			if err == nil {
				t.Fatal("a changed journal was relayed without an error")
			}
			if !strings.Contains(err.Error(), fmt.Sprintf("block %d", first)) {
				t.Fatalf("error %v does not name block %d", err, first)
			}
			if uint64(len(got)) != first {
				t.Fatalf("relayed %d blocks before the refusal, want %d", len(got), first)
			}
			for i, b := range got {
				if !bytes.Equal(mustMarshal(t, b), f.lines[i]) {
					t.Fatalf("block %d relayed wrong", i)
				}
			}
			// Asking for the refused block alone relays nothing.
			if one, err := bp.RelayBlocks(first, first, 0); err == nil || len(one) != 0 {
				t.Fatalf("block %d alone: %d blocks, %v", first, len(one), err)
			}
		})
	}
	t.Run("removed journal", func(t *testing.T) {
		f := newJournalFixture(t, 10)
		bp, _, _ := f.loadAndRestore(t)
		if err := os.Remove(f.path); err != nil {
			t.Fatal(err)
		}
		got, err := bp.RelayBlocks(0, 9, 0)
		if err == nil || len(got) != jfSigFrom {
			t.Fatalf("relayed %d blocks, err %v; want %d blocks and an error", len(got), err, jfSigFrom)
		}
		// The tip keeps its signature and needs no journal.
		if tip, err := bp.RelayBlocks(9, 9, 0); err != nil || len(tip) != 1 {
			t.Fatalf("tip: %d blocks, %v", len(tip), err)
		}
	})
}

// TestRelayBlocksAfterAppends: with the journal not attached to the producer
// (KeepSignaturesInJournal, block_sig_append_test.go), blocks appended after
// the startup load keep their signatures and relay from memory, next to the
// stripped ones.
func TestRelayBlocksAfterAppends(t *testing.T) {
	f := newJournalFixture(t, 14)
	const boot = 10
	path, lines := shareTestJournal(t, f.blocks[:boot])
	bp := f.producer(t, f.blocks[:boot])
	v, err := bp.LoadVerifiedJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := bp.RestoreVerifiedChain(v.Blocks(), v); err != nil {
		t.Fatal(err)
	}
	j, err := OpenChainJournal(path, f.blocks[boot-1])
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	for _, sealed := range f.blocks[boot:] {
		var wire Block // as the gossip and HTTP sync paths decode it
		if err := json.Unmarshal(mustMarshal(t, sealed), &wire); err != nil {
			t.Fatal(err)
		}
		if err := bp.TryAppendExternalBlock(&wire); err != nil {
			t.Fatalf("append %d: %v", sealed.Height, err)
		}
		if err := j.Append(&wire); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, mustMarshal(t, sealed))
	}
	for _, b := range bp.AllBlocks()[boot:] {
		if b.ProducerAuth.Stripped() || len(b.ProducerAuth.Signature) == 0 {
			t.Fatalf("appended block %d lost its signature", b.Height)
		}
	}
	requireRelaysJournal(t, bp, f.blocks, lines)
}

// TestRelayBlocksHoldsNoJournalHandle: the journal can be renamed between
// two relays (Windows refuses to rename an open file).
func TestRelayBlocksHoldsNoJournalHandle(t *testing.T) {
	f := newJournalFixture(t, 10)
	bp, _, _ := f.loadAndRestore(t)
	if _, err := bp.RelayBlocks(0, 9, 0); err != nil {
		t.Fatal(err)
	}
	moved := f.path + ".moved"
	if err := os.Rename(f.path, moved); err != nil {
		t.Fatalf("rename the journal after a relay: %v", err)
	}
	if _, err := bp.RelayBlocks(0, 9, 0); err == nil {
		t.Fatal("relayed stripped blocks without their journal")
	}
	if err := os.Rename(moved, f.path); err != nil {
		t.Fatal(err)
	}
	requireRelaysJournal(t, bp, f.blocks, f.lines)
	if err := os.Rename(f.path, moved); err != nil {
		t.Fatalf("rename the journal after a second relay: %v", err)
	}
}

// TestLoadVerifiedJournalPositionsSurviveBlankAndCRLFLines: the recorded line
// positions are byte offsets, not line numbers.
func TestLoadVerifiedJournalPositionsSurviveBlankAndCRLFLines(t *testing.T) {
	f := newJournalFixture(t, 10)
	var data []byte
	for i, l := range f.lines {
		switch i {
		case 2:
			data = append(data, '\n') // a blank line
		case 5:
			data = append(append(data, l...), '\r', '\n')
			continue
		}
		data = append(append(data, l...), '\n')
	}
	journalEdit(t, f.path, func([]byte) []byte { return data })
	bp, _, _ := f.loadAndRestore(t)
	requireRelaysJournal(t, bp, f.blocks, f.lines)
}

// TestLoadVerifiedJournalVerifiesEveryBlock: the startup load is the one
// authentication of the journal, so it refuses everything
// ValidateProducerTransitionChain refuses.
func TestLoadVerifiedJournalVerifiesEveryBlock(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(t *testing.T, f journalFixture) []*Block
		want error
		msg  string
	}{
		{name: "tampered signature", want: ErrBlockBadSignature, edit: func(t *testing.T, f journalFixture) []*Block {
			b := *f.blocks[8]
			b.ProducerAuth.Signature = append([]byte(nil), b.ProducerAuth.Signature...)
			b.ProducerAuth.Signature[100] ^= 1
			return replaceBlock(f.blocks, &b)
		}},
		{name: "tampered first signature", want: ErrBlockBadSignature, edit: func(t *testing.T, f journalFixture) []*Block {
			b := *f.blocks[jfSigFrom]
			b.ProducerAuth.Signature = append([]byte(nil), b.ProducerAuth.Signature...)
			b.ProducerAuth.Signature[0] ^= 1
			return replaceBlock(f.blocks, &b)
		}},
		{name: "tampered tip signature", want: ErrBlockBadSignature, edit: func(t *testing.T, f journalFixture) []*Block {
			b := *f.blocks[len(f.blocks)-1]
			b.ProducerAuth.Signature = append([]byte(nil), b.ProducerAuth.Signature...)
			b.ProducerAuth.Signature[7] ^= 1
			return replaceBlock(f.blocks, &b)
		}},
		{name: "old producer after the hand-over", want: ErrExternalProducerNotAuthorized, edit: func(t *testing.T, f journalFixture) []*Block {
			b := *f.blocks[8]
			if err := SignBlock(&b, f.oldKey); err != nil {
				t.Fatal(err)
			}
			return resealFrom(t, f, replaceBlock(f.blocks, &b), 9)
		}},
		{name: "unsigned block in an epoch", want: ErrBlockUnsigned, edit: func(t *testing.T, f journalFixture) []*Block {
			b := *f.blocks[4]
			b.ProducerAuth = BFTWireAuth{}
			b.ProducerID = "legacy-peer"
			b.Hash = computeBlockHash(&b)
			return resealFrom(t, f, replaceBlock(f.blocks, &b), 5)
		}},
		{name: "changed contents", msg: "invalid block hash", edit: func(t *testing.T, f journalFixture) []*Block {
			b := *f.blocks[8]
			b.StateRoot = "forged"
			return replaceBlock(f.blocks, &b)
		}},
		{name: "forked duplicate", msg: "not contiguous", edit: func(t *testing.T, f journalFixture) []*Block {
			return append(append([]*Block(nil), f.blocks...), f.blocks[len(f.blocks)-1])
		}},
		{name: "gap", msg: "not contiguous", edit: func(t *testing.T, f journalFixture) []*Block {
			return append(append([]*Block(nil), f.blocks[:5]...), f.blocks[6:]...)
		}},
		{name: "no genesis", msg: "starting at genesis", edit: func(t *testing.T, f journalFixture) []*Block {
			return f.blocks[1:]
		}},
		{name: "genesis with a parent", msg: "starting at genesis", edit: func(t *testing.T, f journalFixture) []*Block {
			b := *f.blocks[0]
			b.PrevHash = strings.Repeat("0", 64)
			b.Hash = computeBlockHash(&b)
			return resealFrom(t, f, replaceBlock(f.blocks, &b), 1)
		}},
		{name: "before the checkpoint", msg: "complete approved checkpoint", edit: func(t *testing.T, f journalFixture) []*Block {
			return f.blocks[:jfCheckpoint]
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newJournalFixture(t, 12)
			blocks := tc.edit(t, f)
			journalEdit(t, f.path, func([]byte) []byte { return marshalTransitionBlocks(t, blocks) })
			bp := f.producer(t, nil)
			v, err := bp.LoadVerifiedJournal(f.path)
			if err == nil || v != nil {
				t.Fatalf("loaded a bad journal: %v", err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("error %v, want %v", err, tc.want)
			}
			if tc.msg != "" && !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("error %v, want %q", err, tc.msg)
			}
			// The batch check of the same blocks agrees.
			if err := f.producer(t, nil).ValidateProducerTransitionChain(blocks); err == nil {
				t.Fatal("ValidateProducerTransitionChain accepted the same blocks")
			}
		})
	}
	t.Run("parse error", func(t *testing.T) {
		f := newJournalFixture(t, 12)
		journalEdit(t, f.path, func(b []byte) []byte { return append(b, "{not json\n"...) })
		if _, err := f.producer(t, nil).LoadVerifiedJournal(f.path); err == nil || !strings.Contains(err.Error(), "parse line 13") {
			t.Fatalf("error %v, want a parse error on line 13", err)
		}
	})
	t.Run("missing journal is a fresh chain", func(t *testing.T) {
		f := newJournalFixture(t, 12)
		v, err := f.producer(t, nil).LoadVerifiedJournal(filepath.Join(t.TempDir(), "absent.ndjson"))
		if err != nil || v == nil || len(v.Blocks()) != 0 {
			t.Fatalf("missing journal: %v %v", v, err)
		}
	})
	t.Run("no producer transition", func(t *testing.T) {
		f := newJournalFixture(t, 12)
		bp := NewBlockProducer(mempool.New(mempool.DefaultConfig()), newTestApplier(), DefaultProducerConfig())
		if _, err := bp.LoadVerifiedJournal(f.path); err == nil {
			t.Fatal("LoadVerifiedJournal ran without a producer transition")
		}
	})
}

// TestRestoreVerifiedChainRestoresStrippedBlocks: stripped blocks cannot be
// verified again at all, so they restore only through the VerifiedChain the
// load returned (also a prefix of it, as the boot step S4r restores), and
// the restored producer relays them from the journal.
func TestRestoreVerifiedChainRestoresStrippedBlocks(t *testing.T) {
	f := newJournalFixture(t, 12)
	v, err := f.producer(t, nil).LoadVerifiedJournal(f.path)
	if err != nil {
		t.Fatal(err)
	}
	blocks := v.Blocks()
	if err := f.producer(t, nil).RestoreChain(blocks); !errors.Is(err, ErrBlockSignatureStripped) {
		t.Fatalf("RestoreChain of stripped blocks = %v, want ErrBlockSignatureStripped", err)
	}
	if err := f.producer(t, nil).RestoreVerifiedChain(blocks, nil); !errors.Is(err, ErrBlockSignatureStripped) {
		t.Fatalf("RestoreVerifiedChain without the verified chain = %v, want ErrBlockSignatureStripped", err)
	}
	// A VerifiedChain of the same journal's blocks loaded another time
	// covers other *Block values.
	if other, _ := f.verified(t); f.producer(t, nil).RestoreVerifiedChain(blocks, other) == nil {
		t.Fatal("restored stripped blocks through another verified chain")
	}
	prefix := blocks[: len(blocks)-1 : len(blocks)-1]
	if err := f.producer(t, nil).RestoreVerifiedChain(prefix, v); err != nil {
		t.Fatalf("the prefix S4r restores: %v", err)
	}
	bp := f.producer(t, f.blocks)
	if err := bp.RestoreVerifiedChain(blocks, v); err != nil {
		t.Fatal(err)
	}
	requireRelaysJournal(t, bp, f.blocks, f.lines)
}

// TestRelayBlocksRangesMatchTheWalk: RelayBlocks selects what a walk of the
// chain selects, for a chain from genesis and for one above it, with and
// without a limit.
func TestRelayBlocksRangesMatchTheWalk(t *testing.T) {
	for _, base := range []uint64{0, 7} {
		t.Run(fmt.Sprintf("chain from height %d", base), func(t *testing.T) {
			bp := NewBlockProducer(mempool.New(mempool.DefaultConfig()), newTestApplier(), DefaultProducerConfig())
			if err := bp.RestoreChain(plainChain(base, 6)); err != nil {
				t.Fatal(err)
			}
			chain := bp.AllBlocks()
			start := uint64(0)
			if base > 3 {
				start = base - 3
			}
			for from := start; from <= base+8; from++ {
				for to := start; to <= base+8; to++ {
					for _, limit := range []int{0, 1, 2, 5} {
						got, err := bp.RelayBlocks(from, to, limit)
						if err != nil {
							t.Fatal(err)
						}
						if want := walkRange(chain, from, to, limit); !sameBlocks(got, want) {
							t.Fatalf("RelayBlocks(%d, %d, %d) = %d blocks, the walk finds %d", from, to, limit, len(got), len(want))
						}
					}
				}
			}
		})
	}
}
