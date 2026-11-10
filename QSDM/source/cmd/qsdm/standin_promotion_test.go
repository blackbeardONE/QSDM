package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

// A stand-in producer (PRODUCER_TRANSITION.md, "A full node as a stand-in
// producer"): a full node that never produced takes over block production
// after the producer stops for good. The node is built the way a public full
// node is: a snapshot import (journal, accounts and enrollment of the
// producer's directory, nothing else), then blocks appended as HTTP chain
// sync appends them, in the follower role, which never writes the served
// watermark W.
//
// Producer A is the replacement producer of a version 1 policy, as on the
// public network. A seals 3..6; follower F starts from A's snapshot at 4 and
// appends 5..6; then A stops. Without a watermark F cannot boot in the
// producer role. The operator seeds W with hl1-tail (served tip 6, checked
// against a second follower G at 6), installs a version 2 schedule that
// names F's own consensus key from 7, and F boots in the producer role and
// seals 7..9 on top of block 6. A, restarted with the schedule, cannot seal.
// G, with the schedule, refuses a block 7 that A sealed under the old policy
// and accepts F's blocks; under the old policy G would do the opposite, which
// is why every node must install the same schedule.
func TestStandInPromotesAFollower(t *testing.T) {
	if testing.Short() {
		t.Skip("state-directory hand-over test")
	}
	dirA := t.TempDir()
	v1, signerA := hl1rTransitionPrefix(t, dirA, 2)

	// A seals 3..4; the snapshot is taken while A is stopped.
	a := hl1rStartCore(t, dirA, v1, signerA)
	for a.st.producer.TipHeight() < 4 {
		a.seal()
	}
	a.crash()
	dirF, dirG := t.TempDir(), t.TempDir()
	for _, dst := range []string{dirF, dirG} {
		standinCopySnapshot(t, dirA, dst)
	}

	// A seals 5..6 and stops for good: 6 is its last block.
	a = hl1rStartCore(t, dirA, v1, signerA)
	for a.st.producer.TipHeight() < 6 {
		a.seal()
	}
	a.crash()
	fromA := standinJournal(t, dirA)
	if len(fromA) != 7 {
		t.Fatalf("A's journal holds %d blocks, want 0..6", len(fromA))
	}
	last := fromA[6]

	// F and G follow A to 6 under the version 1 policy.
	for _, dir := range []string{dirF, dirG} {
		f := standinStartFollower(t, dir, v1)
		for _, b := range fromA[5:] {
			if err := f.st.producer.TryAppendExternalBlock(b); err != nil {
				t.Fatalf("follower append %d: %v", b.Height, err)
			}
		}
		f.stop()
		if _, err := os.Lstat(filepath.Join(dir, legacymining.WatermarkFile)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("a follower wrote the served watermark: %v", err)
		}
	}

	// The schedule: the version 1 epochs as they are, then F's own key from
	// 7, pinned to A's last block. The journal prefix pin is unchanged.
	signerF := hl1rSigner(t)
	v2 := &producerpolicy.Transition{Version: 2, HistoricalPrefixBytes: v1.HistoricalPrefixBytes,
		HistoricalPrefixSHA256: v1.HistoricalPrefixSHA256,
		Epochs:                 append(v1.Schedule(), producerpolicy.Epoch{FromHeight: last.Height + 1, ProducerID: signerF.Address(), CheckpointHash: last.Hash})}
	if err := v2.Validate(); err != nil {
		t.Fatal(err)
	}

	// A follower's directory has no watermark, so the producer role refuses
	// it (S5 rule 1) until the operator seeds one.
	if _, _, err := hl1rBoot(t, dirF, v2); !errors.Is(err, errHL1WatermarkMissing) {
		t.Fatalf("producer boot of a follower directory without W: %v", err)
	}
	// The seed refuses a served tip above F's journal tip (A served a block
	// F never received) and a follower hash that F's journal does not hold.
	if code, out := hl1rTail(t, dirF, "watermark", "seed", "--served-tip", "7",
		"--follower-height", "6", "--follower-hash", last.Hash); code != 2 {
		t.Fatalf("seed above the journal tip: exit %d\n%s", code, out)
	}
	if code, out := hl1rTail(t, dirF, "watermark", "seed", "--served-tip", "6",
		"--follower-height", "6", "--follower-hash", fromA[5].Hash); code != 2 {
		t.Fatalf("seed with a hash F does not hold: exit %d\n%s", code, out)
	}
	g := standinStartFollower(t, dirG, v1)
	gTip, _ := g.st.producer.LatestBlock()
	g.stop()
	if code, out := hl1rTail(t, dirF, "watermark", "seed", "--served-tip", "6",
		"--follower-height", "6", "--follower-hash", gTip.Hash); code != 0 {
		t.Fatalf("seed: exit %d\n%s", code, out)
	}

	// F boots in the producer role with its own key and seals on top of 6.
	f := hl1rStartCore(t, dirF, v2, signerF)
	var fromF []*chain.Block
	for f.st.producer.TipHeight() < 9 {
		fromF = append(fromF, f.seal())
	}
	f.crash()
	if b7 := fromF[0]; b7.Height != 7 || b7.PrevHash != last.Hash || b7.ProducerID != signerF.Address() {
		t.Fatalf("first stand-in block: height %d prev %s producer %s", b7.Height, b7.PrevHash, b7.ProducerID)
	}
	if w := hl1rW(t, dirF); w.Height != 9 || w.Hash != fromF[2].Hash {
		t.Fatalf("stand-in W = %+v, want 9", w)
	}
	fromF = standinJournal(t, dirF)[7:] // as served: with their signatures

	// A with the schedule boots as a follower would, but cannot seal.
	fenced := hl1rStartCore(t, dirA, v2, signerA)
	acc, _ := fenced.st.accounts.Get(hl1rSenders[0])
	if err := fenced.st.pool.Add(&mempool.Tx{ID: "fenced", Sender: hl1rSenders[0], Recipient: hl1rSenders[0], Nonce: acc.Nonce}); err != nil {
		t.Fatal(err)
	}
	if _, err := fenced.st.producer.ProduceBlock(); !errors.Is(err, chain.ErrExternalProducerNotAuthorized) {
		t.Fatalf("the old producer sealed under the schedule: %v", err)
	}
	fenced.crash()

	// A restarted on a copy with the OLD policy seals a competing block 7.
	dirZ := t.TempDir()
	standinCopyAll(t, dirA, dirZ)
	z := hl1rStartCore(t, dirZ, v1, signerA)
	zombie := z.seal()
	z.crash()
	zombie = standinJournal(t, dirZ)[zombie.Height]
	if zombie.Height != 7 || zombie.ProducerID != signerA.Address() {
		t.Fatalf("zombie block %d by %s", zombie.Height, zombie.ProducerID)
	}

	// Under the old policy a follower takes the zombie block and refuses F's.
	dirOld := t.TempDir()
	standinCopyAll(t, dirG, dirOld)
	old := standinStartFollower(t, dirOld, v1)
	if err := old.st.producer.TryAppendExternalBlock(fromF[0]); err == nil {
		t.Fatal("a follower with the old policy accepted the stand-in's block")
	}
	if err := old.st.producer.TryAppendExternalBlock(zombie); err != nil {
		t.Fatalf("a follower with the old policy refused the old producer's block: %v", err)
	}
	old.stop()

	// With the schedule G refuses the zombie block and follows F.
	g = standinStartFollower(t, dirG, v2)
	if err := g.st.producer.TryAppendExternalBlock(zombie); !errors.Is(err, chain.ErrExternalProducerNotAuthorized) {
		t.Fatalf("the schedule let the old producer's block 7 in: %v", err)
	}
	for _, b := range fromF {
		if err := g.st.producer.TryAppendExternalBlock(b); err != nil {
			t.Fatalf("G append stand-in block %d: %v", b.Height, err)
		}
	}
	g.stop()
	gotG := standinJournal(t, dirG)
	if len(gotG) != 10 {
		t.Fatalf("G holds %d blocks, want 0..9", len(gotG))
	}
	for _, b := range fromF {
		if gotG[b.Height].Hash != b.Hash {
			t.Fatalf("G block %d hash %s, stand-in %s", b.Height, gotG[b.Height].Hash, b.Hash)
		}
	}

	// G restarts with the schedule and keeps the whole chain.
	g = standinStartFollower(t, dirG, v2)
	if tip, _ := g.st.producer.LatestBlock(); tip == nil || tip.Height != 9 || tip.Hash != fromF[2].Hash {
		t.Fatalf("G after restart: tip %+v", tip)
	}
	g.stop()
}

// standinFollower is main()'s follower role on a real state directory: the
// S4 restore, S5 without a watermark, the journal, and the persistence hook
// with ProducerRole false, so TryAppendExternalBlock makes each appended
// block durable as HTTP chain sync does.
type standinFollower struct {
	st      *hl1ReplayStack
	journal *chain.ChainJournal
}

func standinStartFollower(t *testing.T, dir string, policy *producerpolicy.Transition) *standinFollower {
	t.Helper()
	st, _, _, err := hl1rRestoreVerified(t, dir, policy)
	if err != nil {
		t.Fatalf("follower restore: %v", err)
	}
	tip, _ := st.producer.LatestBlock()
	s5, err := hl1StartupWatermark(hl1OSFS{}, dir, false, tip, st.producer.GetBlock, hl1rNow)
	if err != nil || s5.Wrote {
		t.Fatalf("follower S5: %+v %v", s5, err)
	}
	j, err := chain.OpenChainJournal(filepath.Join(dir, hl1JournalName), tip)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	if policy != nil { // as main() does
		if err := st.producer.KeepSignaturesInJournal(j); err != nil {
			t.Fatal(err)
		}
	}
	hook := &hl1PersistHook{
		Mode:           hl1HookNormal,
		ProducerRole:   false,
		StateDir:       dir,
		JournalPath:    filepath.Join(dir, hl1JournalName),
		AccountsPath:   filepath.Join(dir, hl1AccountsName),
		EnrollmentPath: filepath.Join(dir, hl1EnrollmentName),
		ReceiptsPath:   filepath.Join(dir, hl1ReceiptsName),
		FS:             hl1OSFS{},
		Prior:          st.v2.SealedBlockHook,
		AppendJournal:  j.Append,
		SaveAccounts:   st.accounts.Save,
		SaveEnrollment: st.v2.EnrollmentState.Save,
		AppendReceipts: st.receipts.AppendBlockNDJSON,
		FailStop: func(_ int, cause string) {
			t.Errorf("follower fail-stop: %s", cause)
		},
		OnFailStopReturn: func(error) {},
		Log:              logging.NewSilentLogger(),
		Metrics:          &hl1MetricSet{},
	}
	st.producer.OnSealedBlock = hook.OnSealedBlock
	return &standinFollower{st: st, journal: j}
}

func (f *standinFollower) stop() { _ = f.journal.Close() }

// standinJournal loads a stopped node's journal as served: every block with
// its producer signature.
func standinJournal(t *testing.T, dir string) []*chain.Block {
	t.Helper()
	blocks, err := chain.LoadChainNDJSON(filepath.Join(dir, hl1JournalName))
	if err != nil {
		t.Fatal(err)
	}
	for i, b := range blocks {
		if b.Height != uint64(i) {
			t.Fatalf("journal line %d holds height %d", i, b.Height)
		}
	}
	return blocks
}

// standinCopySnapshot copies what a snapshot import installs (the journal,
// accounts and enrollment of a stopped producer) into an empty directory.
func standinCopySnapshot(t *testing.T, src, dst string) {
	t.Helper()
	for _, name := range snapshotInstallOrder {
		standinCopyFile(t, filepath.Join(src, name), filepath.Join(dst, name))
	}
}

// standinCopyAll copies every regular file of a stopped node's directory.
func standinCopyAll(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Type().IsRegular() {
			standinCopyFile(t, filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()))
		}
	}
}

func standinCopyFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
