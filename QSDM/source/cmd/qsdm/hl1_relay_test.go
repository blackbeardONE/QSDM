package main

// The two paths that relay blocks to other nodes, GET /api/v1/chain/blocks
// (blocksStoreProbe.BlocksInRange) and the P2P block_request
// (hl1BlockProvider), on a producer restarted under a producer transition:
// its startup load verified every block once and stripped the verified
// signatures from memory, and both paths read them back from the journal
// (chain.BlockProducer.RelayBlocks) and serve every block as exactly its
// journal line, or refuse it.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

// hl1RelayRestart seals a transition chain to height tip, then restarts the
// producer as main() does (S4 with LoadVerifiedJournal, S4r, S5). It returns
// the restarted stack, its durable tip and the journal lines.
func hl1RelayRestart(t *testing.T, tip uint64) (*hl1ReplayStack, *hl1DurableTip, [][]byte, string) {
	t.Helper()
	dir := t.TempDir()
	policy, repl := hl1rTransitionPrefix(t, dir, 2)
	c := hl1rStartCore(t, dir, policy, repl)
	for c.st.producer.TipHeight() < tip {
		c.seal()
	}
	c.crash()
	st, s5, err := hl1rBoot(t, dir, policy)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	durable := &hl1DurableTip{}
	if !s5.DurableTipSet || s5.DurableTip != tip {
		t.Fatalf("durable tip %d/%v, want %d", s5.DurableTip, s5.DurableTipSet, tip)
	}
	durable.Store(s5.DurableTip)
	journal := filepath.Join(dir, hl1JournalName)
	data, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
	if uint64(len(lines)) != tip+1 {
		t.Fatalf("journal holds %d lines, want %d", len(lines), tip+1)
	}
	blocks := st.producer.AllBlocks()
	for i, b := range blocks {
		if stripped := b.ProducerAuth.Stripped(); stripped != (i < len(blocks)-1) {
			t.Fatalf("block %d: stripped %v (every verified signature but the tip's is dropped)", i, stripped)
		}
		if len(b.ProducerAuth.Signature) != 0 && i < len(blocks)-1 {
			t.Fatalf("block %d still holds its signature", i)
		}
	}
	return st, durable, lines, journal
}

func TestHL1RelayPathsServeStrippedBlocks(t *testing.T) {
	const tip = 6
	st, durable, lines, _ := hl1RelayRestart(t, tip)
	probe := blocksProbeFromProducer(st.producer, durable)
	provider := hl1BlockProvider(st.producer, durable)

	raw := probe.BlocksInRange(0, 100)
	if len(raw) != tip+1 {
		t.Fatalf("/chain/blocks served %d blocks, want %d", len(raw), tip+1)
	}
	for i, r := range raw {
		if !bytes.Equal(r, lines[i]) {
			t.Fatalf("/chain/blocks block %d is not its journal line", i)
		}
	}
	served := provider(0, 100, 64)
	if len(served) != tip+1 {
		t.Fatalf("P2P provider served %d blocks, want %d", len(served), tip+1)
	}
	for i, b := range served {
		data, err := json.Marshal(b)
		if err != nil || !bytes.Equal(data, lines[i]) {
			t.Fatalf("P2P block %d is not its journal line (%v)", i, err)
		}
		if err := chain.VerifyBlockSignature(b); err != nil {
			t.Fatalf("P2P block %d does not verify: %v", i, err)
		}
	}
	if got := provider(2, 4, 2); len(got) != 2 || got[0].Height != 2 || got[1].Height != 3 {
		t.Fatalf("P2P provider(2, 4, 2) = %d blocks", len(got))
	}
	for i, hdr := range probe.HeadersInRange(0, 100) {
		if hdr.Height != uint64(i) || hdr.Hash != served[i].Hash {
			t.Fatalf("header %d", i)
		}
	}
}

// TestHL1RelayPathsRefuseAChangedJournal: a stripped block whose journal line
// no longer holds its verified signature is not served by either path; the
// blocks before it are.
func TestHL1RelayPathsRefuseAChangedJournal(t *testing.T) {
	const tip, bad = 6, 4
	st, durable, lines, journal := hl1RelayRestart(t, tip)
	data, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	start := 0
	for _, l := range lines[:bad] {
		start += len(l) + 1
	}
	line := data[start : start+len(lines[bad])]
	auth := bytes.Index(line, []byte(`"producer_auth":`))
	sig := bytes.Index(line[auth:], []byte(`"signature":"`))
	if auth < 0 || sig < 0 {
		t.Fatal("no producer signature in the line")
	}
	at := auth + sig + len(`"signature":"`) + 20
	if line[at] == 'A' {
		line[at] = 'B'
	} else {
		line[at] = 'A'
	}
	if err := os.WriteFile(journal, data, 0o644); err != nil {
		t.Fatal(err)
	}

	probe := blocksProbeFromProducer(st.producer, durable)
	provider := hl1BlockProvider(st.producer, durable)
	raw := probe.BlocksInRange(0, 100)
	if len(raw) != bad {
		t.Fatalf("/chain/blocks served %d blocks, want the %d before the changed line", len(raw), bad)
	}
	for i, r := range raw {
		if !bytes.Equal(r, lines[i]) {
			t.Fatalf("/chain/blocks block %d is not its journal line", i)
		}
	}
	if got := provider(0, 100, 64); len(got) != bad {
		t.Fatalf("P2P provider served %d blocks, want %d", len(got), bad)
	}
	if got := provider(bad, 100, 64); len(got) != 0 {
		t.Fatalf("P2P provider served %d blocks from the changed line", len(got))
	}
	if got := probe.BlocksInRange(bad, bad); len(got) != 0 {
		t.Fatal("/chain/blocks served the block whose line changed")
	}
	// The tip keeps its signature in memory and is still served.
	if got := provider(tip, tip, 1); len(got) != 1 || !bytes.Equal(mustJSON(t, got[0]), lines[tip]) {
		t.Fatal("the tip is not served")
	}
}
