package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

// The legacy state-root boundary is the first hand-over in both spellings of
// a version 1 policy, and a later hand-over does not move it. The heights are
// those of the public network's transition; the hashes are synthetic.
func TestTransitionStateRootHeightV1V2(t *testing.T) {
	h := func(c string) string { return strings.Repeat(c, 64) }
	v1 := &producerpolicy.Transition{Version: 1, CheckpointHeight: 659976, CheckpointHash: h("a"),
		HistoricalSignatureHeight: 482566, HistoricalProducer: h("b"), ReplacementProducer: h("c"),
		EffectiveHeight: 659977, HistoricalPrefixBytes: 1, HistoricalPrefixSHA256: h("d")}
	v2 := &producerpolicy.Transition{Version: 2, HistoricalPrefixBytes: 1, HistoricalPrefixSHA256: h("d"), Epochs: v1.Schedule()}
	v3 := &producerpolicy.Transition{Version: 2, HistoricalPrefixBytes: 1, HistoricalPrefixSHA256: h("d"),
		Epochs: append(v1.Schedule(), producerpolicy.Epoch{FromHeight: 900001, ProducerID: h("e"), CheckpointHash: h("f")})}
	for _, p := range []*producerpolicy.Transition{v1, v2, v3} {
		if err := p.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, tip := range []uint64{0, 482566, 659976, 659977, 659978, 900000, 900001, 900002} {
		blk := &chain.Block{Height: tip}
		// The rule before version 2: tip >= effective_height.
		want := uint64(0)
		if tip >= v1.EffectiveHeight {
			want = tip
		}
		if got := transitionStateRootHeight(v1, blk); got != want {
			t.Fatalf("tip %d: v1 %d, want %d", tip, got, want)
		}
		if got := transitionStateRootHeight(v2, blk); got != want {
			t.Fatalf("tip %d: v2 %d, want %d", tip, got, want)
		}
		if got := transitionStateRootHeight(v3, blk); got != want {
			t.Fatalf("tip %d: three epochs %d, want %d", tip, got, want)
		}
	}
	if producerScheduleSummary(v3) != h("b")+"@482566,"+h("c")+"@659977,"+h("e")+"@900001" {
		t.Fatalf("summary %s", producerScheduleSummary(v3))
	}
}

// A second hand-over on a real state directory: A seals 0..2, B (version 1
// replacement) seals 3..5, then the operator installs a version 2 schedule
// A@1, B@3, C@6. The fenced producer B can no longer seal, C seals across the
// boundary, a crash at block 8 is recovered by replay mode under the
// schedule, and the version 1 policy can no longer boot the advanced chain.
func TestTailReplayAcrossSecondHandover(t *testing.T) {
	if testing.Short() {
		t.Skip("state-directory recovery test")
	}
	dir := t.TempDir()
	v1, signerB := hl1rTransitionPrefix(t, dir, 2)
	c := hl1rStartCore(t, dir, v1, signerB)
	for c.st.producer.TipHeight() < 5 {
		c.seal()
	}
	c.crash()

	data, err := os.ReadFile(filepath.Join(dir, hl1JournalName))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	cp, ok := c.st.producer.GetBlock(5)
	if !ok {
		t.Fatal("checkpoint block missing")
	}
	signerC := hl1rSigner(t)
	v2 := &producerpolicy.Transition{Version: 2, HistoricalPrefixBytes: int64(len(data)), HistoricalPrefixSHA256: hex.EncodeToString(sum[:]),
		Epochs: append(v1.Schedule(), producerpolicy.Epoch{FromHeight: 6, ProducerID: signerC.Address(), CheckpointHash: cp.Hash})}
	if err := v2.Validate(); err != nil {
		t.Fatal(err)
	}

	// The fenced producer boots as a follower would, but cannot seal, and the
	// refusal comes before the mempool is drained.
	fenced := hl1rStartCore(t, dir, v2, signerB)
	acc, _ := fenced.st.accounts.Get(hl1rSenders[0])
	if err := fenced.st.pool.Add(&mempool.Tx{ID: "fenced", Sender: hl1rSenders[0], Recipient: hl1rSenders[0], Nonce: acc.Nonce}); err != nil {
		t.Fatal(err)
	}
	if _, err := fenced.st.producer.ProduceBlock(); !errors.Is(err, chain.ErrExternalProducerNotAuthorized) {
		t.Fatalf("fenced producer sealed under the schedule: %v", err)
	}
	if fenced.st.pool.Size() != 1 || fenced.st.producer.TipHeight() != 5 {
		t.Fatal("fenced seal attempt changed the producer")
	}
	fenced.crash()

	// C seals across the boundary; block 8 crashes at H3 (C4).
	const j = 8
	core := hl1rStartCore(t, dir, v2, signerC)
	for core.st.producer.TipHeight() < j-1 {
		core.seal()
	}
	core.failAccounts = true
	blkJ := core.seal()
	core.crash()
	if blkJ.Height != j || blkJ.ProducerID != signerC.Address() {
		t.Fatalf("faulted block %d by %s", blkJ.Height, blkJ.ProducerID)
	}
	if len(core.causes) != 1 || !strings.HasPrefix(core.causes[0], legacymining.CausePersistPrefix+"H3:") {
		t.Fatalf("fail-stop causes = %q", core.causes)
	}
	if b6, _ := core.st.producer.GetBlock(6); b6 == nil || b6.PrevHash != cp.Hash || b6.ProducerID != signerC.Address() {
		t.Fatal("first block of the new epoch does not extend the checkpoint")
	}
	if _, _, err := hl1rBoot(t, dir, v2); err == nil {
		t.Fatal("the C4 crash state boots")
	}
	journalBefore := hl1rRead(t, filepath.Join(dir, hl1JournalName))

	// Replay mode under the schedule recovers block 8 without touching the
	// journal.
	_, err, ex := hl1rReplay(t, dir, v2)
	if err != nil || ex.code != -1 {
		t.Fatalf("replay: %v exit %d", err, ex.code)
	}
	if w := hl1rW(t, dir); w.Height != j || w.Hash != blkJ.Hash || w.Source != legacymining.WatermarkSourceReplay {
		t.Fatalf("W after replay = %+v", w)
	}
	if string(hl1rRead(t, filepath.Join(dir, hl1JournalName))) != string(journalBefore) {
		t.Fatal("replay changed the journal")
	}

	// The version 1 policy cannot represent the advanced chain: boot refuses
	// it.
	if _, _, err := hl1rBoot(t, dir, v1); err == nil || !errors.Is(err, chain.ErrExternalProducerNotAuthorized) {
		t.Fatalf("version 1 policy booted the second hand-over: %v", err)
	}

	// The schedule boots and C continues.
	if _, s5, err := hl1rBoot(t, dir, v2); err != nil || !s5.DurableTipSet || s5.DurableTip != j {
		t.Fatalf("boot after replay: %v %+v", err, s5)
	}
	next := hl1rStartCore(t, dir, v2, signerC).seal()
	if next.Height != j+1 || next.ProducerID != signerC.Address() {
		t.Fatalf("next seal %d by %s", next.Height, next.ProducerID)
	}
}
