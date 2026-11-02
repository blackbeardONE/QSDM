package legacymining

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// Synthetic-chain tests for Reconcile (§5 S7-S14, WP8). Each test drives a
// real SQLiteStore, CanaryGuard and PayoutLedger through simulated boots over
// one legacy-mining directory and one restored chain.

// rcCell is the live rewardCell (§6.1).
const rcCell = 3.56490987

var (
	rcHash1 = ConfigHash(sha256.Sum256([]byte("mining-canary.json phase 1")))
	rcHash2 = ConfigHash(sha256.Sum256([]byte("mining-canary.json phase 2")))
	rcNow   = time.Unix(1_790_000_000, 0)
	rcOther = strings.Repeat("cd", 32)
	errRC   = errors.New("injected store failure")
)

func rcCellAt(uint64) float64 { return rcCell }

func rcConfig() Config {
	return Config{
		Version:         ConfigVersion,
		Allowed:         []AllowEntry{{MinerAddr: stMiner, NodeID: stNode}},
		MaxProofsPerMin: 60,
		MaxProofsTotal:  3000,
		MaxPending:      600,
		BudgetCell:      1291,
		ExpiresUnix:     rcNow.Unix() + 3600,
	}
}

func rcBlock(height uint64, prev string, txs []*mempool.Tx) *chain.Block {
	h := sha256.New()
	fmt.Fprintf(h, "%d/%s", height, prev)
	for _, tx := range txs {
		fmt.Fprintf(h, "/%s", tx.ID)
	}
	return &chain.Block{Height: height, PrevHash: prev, Hash: hex.EncodeToString(h.Sum(nil)), Transactions: txs}
}

func rcHeartbeat(nonce, height uint64) *mempool.Tx {
	return &mempool.Tx{ID: fmt.Sprintf("solo-heartbeat-%d-%d", nonce, height), Sender: ltFunder, Recipient: ltFunder, Nonce: nonce}
}

// rcD7Reward is the d7 reward tx (d7:717-726): no payload, no hash suffix.
func rcD7Reward(nonce uint64, addr string, amount float64) *mempool.Tx {
	return &mempool.Tx{ID: fmt.Sprintf("solo-reward-%d-%s", nonce, addr), Sender: ltFunder, Recipient: addr,
		Amount: amount, Nonce: nonce, ContractID: chain.MiningRewardContractID}
}

// rcBuild is the d7 float formula (d7:693-701), one reward tx per claim.
func rcBuild(claims []Claim, nonce uint64) []*mempool.Tx {
	total := 0
	for _, c := range claims {
		total += len(c.IDs)
	}
	txs := make([]*mempool.Tx, 0, len(claims))
	for i, c := range claims {
		txs = append(txs, ltRewardTx(nonce+uint64(i), c.MinerAddr, rcCell*float64(len(c.IDs))/float64(total), c.IDs...))
	}
	return txs
}

// rcWorld is the durable state shared by simulated boots: the legacy-mining
// directory, the restored chain and the account store it produced.
type rcWorld struct {
	t      *testing.T
	dir    string
	blocks []*chain.Block
	accts  *chain.AccountStore
	seq    uint64
	cur    *rcBoot
}

// newRCWorld returns a pre-HL1 chain 0..d7Tip: an empty genesis, then a
// funder heartbeat in every block and an untagged d7 reward every third block.
func newRCWorld(t *testing.T, d7Tip uint64) *rcWorld {
	t.Helper()
	w := &rcWorld{t: t, dir: stDir(t), accts: chain.NewAccountStore()}
	w.accts.Credit(ltFunder, ltStartBalance)
	w.blocks = []*chain.Block{rcBlock(0, "", nil)}
	for h := uint64(1); h <= d7Tip; h++ {
		n := w.funderNonce()
		txs := []*mempool.Tx{rcHeartbeat(n, h)}
		if h%3 == 0 {
			txs = append(txs, rcD7Reward(n+1, rcOther, rcCell))
		}
		w.seal(txs...)
	}
	t.Cleanup(w.shutdown)
	return w
}

func (w *rcWorld) tip() uint64 { return w.blocks[len(w.blocks)-1].Height }

func (w *rcWorld) funderNonce() uint64 {
	acc, ok := w.accts.Get(ltFunder)
	if !ok {
		w.t.Fatal("funder account missing")
	}
	return acc.Nonce
}

// add appends a block at tip+1 without applying its txs.
func (w *rcWorld) add(txs ...*mempool.Tx) *chain.Block {
	prev := w.blocks[len(w.blocks)-1]
	b := rcBlock(prev.Height+1, prev.Hash, txs)
	w.blocks = append(w.blocks, b)
	return b
}

// seal applies txs to the account store, as ProduceBlock does, then adds the
// block.
func (w *rcWorld) seal(txs ...*mempool.Tx) *chain.Block {
	w.t.Helper()
	for _, tx := range txs {
		var err error
		if tx.ContractID == chain.MiningRewardContractID {
			err = w.accts.ApplyProtocolReward(tx, tx.Amount)
		} else {
			err = w.accts.ApplyTx(tx)
		}
		if err != nil {
			w.t.Fatalf("apply %s: %v", tx.ID, err)
		}
	}
	return w.add(txs...)
}

// trim drops the blocks above height (a truncated journal tail).
func (w *rcWorld) trim(height uint64) { w.blocks = w.blocks[:height+1] }

// shutdown ends the running boot, if any.
func (w *rcWorld) shutdown() {
	if w.cur != nil {
		_ = w.cur.store.Close()
		w.cur = nil
	}
}

// removeDB deletes the DB and its sidecars.
func (w *rcWorld) removeDB() {
	w.t.Helper()
	w.shutdown()
	for _, s := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(stPath(w.dir) + s); err != nil && !errors.Is(err, os.ErrNotExist) {
			w.t.Fatal(err)
		}
	}
}

func (w *rcWorld) exists(name string) bool { return gtExists(w.t, filepath.Join(w.dir, name)) }

// rcOpts varies one boot. Zero fields take the defaults.
type rcOpts struct {
	hash  ConfigHash               // default rcHash1
	cfg   *Config                  // default rcConfig()
	now   time.Time                // default rcNow
	guard func(*CanaryGuard) Guard // wraps the Guard given to the Ledger and Reconcile
	store func(*SQLiteStore) Store // wraps the Store given to Reconcile
	edit  func(c *ReconcileConfig) // edits the Reconcile input
}

// rcBoot is one canary process start: a fresh Store, Guard and Ledger, then
// Reconcile.
type rcBoot struct {
	w      *rcWorld
	hash   ConfigHash
	store  *SQLiteStore
	guard  *CanaryGuard
	ledger *PayoutLedger
	rep    ReconcileReport
	err    error
	stops  []string
}

func (w *rcWorld) boot(o rcOpts) *rcBoot {
	w.t.Helper()
	w.shutdown()
	if o.hash == (ConfigHash{}) {
		o.hash = rcHash1
	}
	cfg := rcConfig()
	if o.cfg != nil {
		cfg = *o.cfg
	}
	if o.now.IsZero() {
		o.now = rcNow
	}
	now := func() time.Time { return o.now }
	b := &rcBoot{w: w, hash: o.hash, store: NewSQLiteStore()}
	b.store.now = now
	w.cur = b
	g, err := NewGuard(GuardOptions{
		Dir: w.dir, Config: cfg, ConfigHash: o.hash, Release: "hl1-test", Store: b.store,
		FailStop:         func(code int, cause string) { b.stops = append(b.stops, fmt.Sprintf("%d:%s", code, cause)) },
		EnrollmentActive: func(string, string) bool { return true },
		Now:              now,
		Logf:             w.t.Logf,
	})
	if err != nil {
		w.t.Fatalf("NewGuard: %v", err)
	}
	b.guard = g
	var guard Guard = g
	if o.guard != nil {
		guard = o.guard(g)
	}
	if b.ledger, err = NewLedger(LedgerConfig{Store: b.store, Guard: guard, Accounts: w.accts, RewardCell: rcCellAt}); err != nil {
		w.t.Fatalf("NewLedger: %v", err)
	}
	var store Store = b.store
	if o.store != nil {
		store = o.store(b.store)
	}
	in := ReconcileConfig{
		Store: store, DBPath: stPath(w.dir), Guard: guard, Ledger: b.ledger, Accounts: w.accts,
		Blocks: w.blocks, Release: "hl1-test", RewardCell: rcCellAt, Now: now,
	}
	if o.edit != nil {
		o.edit(&in)
	}
	b.rep, b.err = Reconcile(in)
	return b
}

func (w *rcWorld) bootHash(h ConfigHash) *rcBoot { return w.boot(rcOpts{hash: h}) }

// newRecord is §4.1 step 8 only: a new proof of miner, committed.
func (b *rcBoot) newRecord(miner string) Record {
	w := b.w
	w.t.Helper()
	w.seq++
	var nonce [32]byte
	nonce[0] = 0x52
	binary.BigEndian.PutUint64(nonce[24:], w.seq)
	rec := stRecord(w.t, w.seq, nonce)
	rec.MinerAddr = miner
	rec.AcceptTip = w.tip()
	rec.ConfigSHA256 = b.hash
	if err := b.store.Accept(rec); err != nil {
		w.t.Fatalf("Accept: %v", err)
	}
	return rec
}

// accept is §4.1 steps 8-9.
func (b *rcBoot) accept() Record {
	b.w.t.Helper()
	rec := b.newRecord(stMiner)
	if err := b.ledger.Enqueue(rec); err != nil {
		b.w.t.Fatalf("Enqueue: %v", err)
	}
	return rec
}

// tick is one §4.2 SUCCESS tick and its H8(b): Take, the d7 float formula,
// PreSeal, apply and seal, OnDurableBlock (MarkPaid), Requeue.
func (b *rcBoot) tick() *chain.Block {
	w := b.w
	w.t.Helper()
	height, nonce := w.tip()+1, w.funderNonce()
	txs := []*mempool.Tx{rcHeartbeat(nonce, height)}
	if claims := b.ledger.Take(); len(claims) > 0 {
		txs = rcBuild(claims, nonce)
		if err := b.ledger.PreSeal(height, rcCell, txs); err != nil {
			w.t.Fatalf("PreSeal: %v", err)
		}
	}
	blk := w.seal(txs...)
	if err := b.ledger.OnDurableBlock(blk, true); err != nil {
		w.t.Fatalf("OnDurableBlock: %v", err)
	}
	b.ledger.Requeue()
	return blk
}

func (b *rcBoot) ledgerInitialized() bool {
	b.ledger.mu.Lock()
	defer b.ledger.mu.Unlock()
	return b.ledger.initialized
}

func (b *rcBoot) mustClean() {
	t := b.w.t
	t.Helper()
	if b.err != nil || !b.rep.Clean || b.rep.Step != "" || b.rep.AnomalyCount != 0 {
		t.Fatalf("Reconcile = %+v, %v; want clean", b.rep, b.err)
	}
	if b.w.exists(TrippedFile) {
		t.Fatalf("TRIPPED.json after a clean reconcile: %s", gtReadCause(t, b.w.dir, MarkerTripped))
	}
	if !b.ledgerInitialized() {
		t.Fatal("ledger not initialized after a clean reconcile")
	}
	b.mustBeArmed()
	if len(b.stops) != 0 {
		t.Fatalf("fail-stop: %q", b.stops)
	}
}

// mustBeArmed requires S14 to have left each guard marker armed or tripped.
func (b *rcBoot) mustBeArmed() {
	b.w.t.Helper()
	for _, m := range []string{MarkerTripped, MarkerAdmissionStopped} {
		if !b.w.exists(m+ArmedSuffix) && !b.w.exists(m+TrippedSuffix) {
			b.w.t.Fatalf("S14 did not arm %s", m)
		}
	}
}

// mustTrip requires a FREEZE at step whose cause starts with prefix, S14 to
// have run, and the Ledger to be uninitialised unless the failure came after
// S13 began.
func (b *rcBoot) mustTrip(step, prefix string) {
	t := b.w.t
	t.Helper()
	if b.rep.Clean || b.rep.Step != step || !errors.Is(b.err, ErrReconcile) {
		t.Fatalf("Reconcile = %+v, %v; want a failure at %s", b.rep, b.err, step)
	}
	if !strings.HasPrefix(b.err.Error(), ErrReconcile.Error()+": "+prefix) {
		t.Fatalf("error %q, want cause prefix %q", b.err, prefix)
	}
	if got := gtReadCause(t, b.w.dir, MarkerTripped); !strings.HasPrefix(got, prefix) {
		t.Fatalf("TRIPPED cause %q, want prefix %q", got, prefix)
	}
	if !b.w.exists(TrippedFile) || b.guard.State() != StateFrozen {
		t.Fatalf("not FROZEN: state %v", b.guard.State())
	}
	b.mustBeArmed()
	if b.ledgerInitialized() {
		t.Fatal("ledger initialized after a failed reconcile")
	}
	if len(b.stops) != 0 {
		t.Fatalf("fail-stop: %q", b.stops)
	}
}

func rcIDs(recs ...Record) []ProofID {
	ids := make([]ProofID, len(recs))
	for i, r := range recs {
		ids[i] = r.ProofID
	}
	return ids
}

func rcSameFloat(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Float64bits(got) != math.Float64bits(want) {
		t.Fatalf("%s = %v (bits %#x), want %v (bits %#x)", what, got, math.Float64bits(got), want, math.Float64bits(want))
	}
}

// -----------------------------------------------------------------------------
// S8: creation and the absent-DB refusal
// -----------------------------------------------------------------------------

func TestReconcileCreatesDBOnUntaggedChain(t *testing.T) {
	requireHLSQLite(t)
	w := newRCWorld(t, 12)
	b := w.bootHash(rcHash1)
	b.mustClean()
	r := b.rep
	if !r.Created || !r.StoreOpen || r.Tip != 12 {
		t.Fatalf("report %+v: want created and open at tip 12", r)
	}
	wantMeta := Meta{H0: 13, GenesisHash: w.blocks[0].Hash, Funder: ltFunder, Release: "hl1-test", CreatedNS: rcNow.UnixNano()}
	if r.Meta != wantMeta {
		t.Fatalf("meta %+v, want %+v", r.Meta, wantMeta)
	}
	if got, _ := b.store.Meta(); got != wantMeta {
		t.Fatalf("stored meta %+v, want %+v", got, wantMeta)
	}
	// The untagged d7 rewards are below h0: not payments and not anomalies.
	if r.Paid != 0 || r.Pending != 0 || r.Counts != (ReconcileCounts{WindowInserted: true}) {
		t.Fatalf("report %+v", r)
	}
	if want := (ConfigWindow{ConfigSHA256: rcHash1, FirstHeight: 13, ActivatedNS: rcNow.UnixNano()}); r.Window != want {
		t.Fatalf("window %+v, want %+v", r.Window, want)
	}
	if r.Totals != (Totals{ConfigSHA256: rcHash1}) || b.ledger.Totals() != r.Totals {
		t.Fatalf("totals %+v, ledger %+v", r.Totals, b.ledger.Totals())
	}
	// S13: the funder expectations come from the restored account store.
	acc, _ := w.accts.Get(ltFunder)
	if b.ledger.funderNonce != acc.Nonce || math.Float64bits(b.ledger.funderBal) != math.Float64bits(acc.Balance) {
		t.Fatalf("ledger funder %d/%v, chain %d/%v", b.ledger.funderNonce, b.ledger.funderBal, acc.Nonce, acc.Balance)
	}
	if n := stEventCount(t, b.store, storeEventReconcile); n != 1 {
		t.Fatalf("%d reconcile events, want 1", n)
	}
	if b.guard.State() != StateOpen {
		t.Fatalf("state %v", b.guard.State())
	}

	// A restart finds the DB: nothing is created or changed.
	w.add(rcHeartbeat(w.funderNonce(), 13))
	b2 := w.bootHash(rcHash1)
	b2.mustClean()
	if b2.rep.Created || b2.rep.Meta != wantMeta || b2.rep.Counts != (ReconcileCounts{}) || b2.rep.Window != r.Window {
		t.Fatalf("restart report %+v", b2.rep)
	}
}

func TestReconcileAbsentDBWithTaggedChain(t *testing.T) {
	requireHLSQLite(t)
	t.Run("reward", func(t *testing.T) {
		w := newRCWorld(t, 9)
		b1 := w.bootHash(rcHash1)
		b1.mustClean()
		b1.accept()
		b1.tick() // an LMP1 reward at height 10
		w.removeDB()

		b2 := w.bootHash(rcHash1)
		b2.mustTrip("S8", CauseDBMissing+":S8: the DB is missing but block 10 tx ")
		if b2.rep.StoreOpen || b2.rep.Created {
			t.Fatalf("report %+v", b2.rep)
		}
		if w.exists(DBFile) {
			t.Fatal("S8 created a DB over a tagged chain")
		}
		// Every later boot refuses the same way.
		w.boot(rcOpts{}).mustTrip("S8", CauseDBMissing+":S8:")
		if w.exists(DBFile) {
			t.Fatal("S8 created a DB over a tagged chain")
		}
	})
	t.Run("tag outside the reward contract", func(t *testing.T) {
		w := newRCWorld(t, 9)
		hb := rcHeartbeat(w.funderNonce(), 10)
		hb.Payload = ltPayload(ltID(1))
		w.seal(hb)
		w.bootHash(rcHash1).mustTrip("S8", CauseDBMissing+":S8:")
		if w.exists(DBFile) {
			t.Fatal("S8 created a DB over a tagged chain")
		}
	})
}

// -----------------------------------------------------------------------------
// S10-S13 on a consistent chain
// -----------------------------------------------------------------------------

func TestReconcileRestartDerivesPaymentsAndCounters(t *testing.T) {
	requireHLSQLite(t)
	w := newRCWorld(t, 9)
	b1 := w.bootHash(rcHash1)
	b1.mustClean()
	b1.accept()
	b1.accept()
	b1.tick() // height 10 pays 2
	b1.accept()
	b1.tick() // 11 pays 1
	b1.tick() // 12 heartbeat
	r4 := b1.accept()
	want := b1.ledger.Totals()

	b2 := w.bootHash(rcHash1)
	b2.mustClean()
	if b2.rep.Paid != 3 || b2.rep.Counts != (ReconcileCounts{}) || b2.rep.Pending != 1 || b2.ledger.Outstanding() != 1 {
		t.Fatalf("report %+v, outstanding %d", b2.rep, b2.ledger.Outstanding())
	}
	if b2.rep.Totals.ConfigSHA256 != rcHash1 || b2.rep.Totals.Proofs != 4 || want.Proofs != 4 {
		t.Fatalf("totals %+v, ledger before restart %+v", b2.rep.Totals, want)
	}
	rcSameFloat(t, "emitted after restart", b2.rep.Totals.Emitted, want.Emitted)
	rcSameFloat(t, "emitted", want.Emitted, rcCell+rcCell)

	// The reloaded ledger pays the pending proof once.
	blk := b2.tick()
	if len(blk.Transactions) != 1 || !reflect.DeepEqual(blk.Transactions[0].Payload, ltPayload(r4.ProofID)) {
		t.Fatalf("block %d does not pay the reloaded proof: %+v", blk.Height, blk.Transactions)
	}
	b3 := w.bootHash(rcHash1)
	b3.mustClean()
	if b3.rep.Paid != 4 || b3.rep.Pending != 0 || b3.rep.Counts != (ReconcileCounts{}) {
		t.Fatalf("report %+v", b3.rep)
	}
	rcSameFloat(t, "emitted after the second restart", b3.rep.Totals.Emitted, b2.ledger.Totals().Emitted)
}

// A block whose MarkPaid never happened (a crash after H7, C7/C8) is marked
// paid from the chain (S11); a row paid by another tx at another height is
// corrected.
func TestReconcileRecordsChainPayments(t *testing.T) {
	requireHLSQLite(t)
	w := newRCWorld(t, 9)
	b1 := w.bootHash(rcHash1)
	b1.mustClean()
	r1, r2 := b1.accept(), b1.accept()
	b1.tick()                   // 10 pays r1 and r2 (MarkPaid)
	r3 := b1.newRecord(stMiner) // committed, then the process died before Enqueue
	w.trim(9)                   // 10 is lost ...
	n := w.funderNonce()
	w.add(rcHeartbeat(n, 10)) // ... and 10', 11' are sealed without HL1's MarkPaid
	w.add(ltRewardTx(n+1, stMiner, rcCell, ltSorted(r1.ProofID, r2.ProofID, r3.ProofID)...))

	b2 := w.bootHash(rcHash1)
	b2.mustClean()
	if b2.rep.Paid != 3 || b2.rep.Counts != (ReconcileCounts{Updated: 3}) || b2.rep.Pending != 0 {
		t.Fatalf("report %+v", b2.rep)
	}
	rows, err := b2.store.Lookup(rcIDs(r1, r2, r3))
	if err != nil {
		t.Fatal(err)
	}
	txID := w.blocks[11].Transactions[0].ID
	for _, r := range []Record{r1, r2, r3} {
		if got := rows[r.ProofID]; got.PaidHeight != 11 || got.PaidTxID != txID {
			t.Fatalf("proof %x paid at %d by %q, want 11 by %q", r.ProofID[:4], got.PaidHeight, got.PaidTxID, txID)
		}
	}
	rcSameFloat(t, "emitted", b2.rep.Totals.Emitted, rcCell)
}

// A truncated journal tail above the DB's knowledge: rows the chain no longer
// pays are reset to unpaid and paid again, once.
func TestReconcileTruncatedTail(t *testing.T) {
	requireHLSQLite(t)
	w := newRCWorld(t, 9)
	b1 := w.bootHash(rcHash1)
	b1.mustClean()
	r1 := b1.accept()
	b1.tick() // 10 pays r1
	r2, r3 := b1.accept(), b1.accept()
	b1.tick() // 11 pays r2, r3
	w.trim(10)

	b2 := w.bootHash(rcHash1)
	b2.mustClean()
	if b2.rep.Tip != 10 || b2.rep.Paid != 1 || b2.rep.Counts != (ReconcileCounts{Reset: 2}) || b2.rep.Pending != 2 {
		t.Fatalf("report %+v", b2.rep)
	}
	if got := stPendingIDs(t, b2.store); !reflect.DeepEqual(got, rcIDs(r2, r3)) {
		t.Fatalf("pending %x, want r2, r3", got)
	}
	rcSameFloat(t, "emitted", b2.rep.Totals.Emitted, rcCell)
	if b2.ledger.Outstanding() != 2 {
		t.Fatalf("outstanding %d", b2.ledger.Outstanding())
	}
	blk := b2.tick() // a new 11 pays r2 and r3 again
	if blk.Height != 11 || !reflect.DeepEqual(blk.Transactions[0].Payload, ltPayload(ltSorted(r2.ProofID, r3.ProofID)...)) {
		t.Fatalf("block %+v", blk)
	}
	b3 := w.bootHash(rcHash1)
	b3.mustClean()
	if b3.rep.Paid != 3 || b3.rep.Counts != (ReconcileCounts{}) || b3.rep.Pending != 0 {
		t.Fatalf("report %+v", b3.rep)
	}
	rows, err := b3.store.Lookup(rcIDs(r1, r2, r3))
	if err != nil {
		t.Fatal(err)
	}
	if rows[r1.ProofID].PaidHeight != 10 || rows[r2.ProofID].PaidHeight != 11 || rows[r3.ProofID].PaidHeight != 11 ||
		rows[r2.ProofID].PaidTxID != blk.Transactions[0].ID {
		t.Fatalf("paid rows %+v", rows)
	}
}

// -----------------------------------------------------------------------------
// The anomaly matrix (S9, S10)
// -----------------------------------------------------------------------------

func TestReconcileAnomalyMatrix(t *testing.T) {
	requireHLSQLite(t)
	type fixture struct {
		w       *rcWorld
		paid    Record   // paid by block 10
		pending []Record // stored and unpaid
		nonce   uint64   // the funder nonce for block 11
	}
	cases := []struct {
		name   string
		mutate func(f *fixture)
		step   string // "" for the clean control
		want   string // substring of the first anomaly
	}{
		{"control", func(f *fixture) {
			f.w.add(ltRewardTx(f.nonce, stMiner, rcCell, f.pending[0].ProofID))
		}, "", ""},
		{"double pay across blocks", func(f *fixture) {
			f.w.add(ltRewardTx(f.nonce, stMiner, rcCell, f.paid.ProofID))
		}, "S10", "is paid again (first paid at height 10"},
		{"double pay in one block", func(f *fixture) {
			id := f.pending[0].ProofID
			f.w.add(ltRewardTx(f.nonce, stMiner, rcCell/2, id), ltRewardTx(f.nonce+1, stMiner, rcCell/2, id))
		}, "S10", "is paid again (first paid at height 11"},
		{"orphan", func(f *fixture) {
			f.w.add(ltRewardTx(f.nonce, stMiner, rcCell, ltID(999)))
		}, "S10", "is not stored (orphan)"},
		{"recipient is not the stored miner", func(f *fixture) {
			f.w.add(ltRewardTx(f.nonce, rcOther, rcCell, f.pending[0].ProofID))
		}, "S10", "belongs to " + stMiner + ", paid to " + rcOther},
		{"untagged reward", func(f *fixture) {
			f.w.add(rcD7Reward(f.nonce, stMiner, rcCell))
		}, "S10", "untagged reward"},
		{"invalid LMP1 payload", func(f *fixture) {
			tx := ltRewardTx(f.nonce, stMiner, rcCell, f.pending[0].ProofID)
			tx.Payload = append(tx.Payload, 0)
			tx.ID = ledgerRewardID(tx.Nonce, tx.Recipient, tx.Payload)
			f.w.add(tx)
		}, "S10", ErrBadPayload.Error()},
		{"descending payload", func(f *fixture) {
			ids := ltSorted(f.pending[0].ProofID, f.pending[1].ProofID)
			tx := ltRewardTx(f.nonce, stMiner, rcCell, ids[1], ids[0])
			f.w.add(tx)
		}, "S10", "not strictly ascending"},
		{"not the derived ID", func(f *fixture) {
			tx := ltRewardTx(f.nonce, stMiner, rcCell, f.pending[0].ProofID)
			tx.ID = fmt.Sprintf("solo-reward-%d-%s", tx.Nonce, tx.Recipient)
			f.w.add(tx)
		}, "S10", "tx ID is not the derived ID"},
		{"sender is not the funder", func(f *fixture) {
			tx := ltRewardTx(f.nonce, stMiner, rcCell, f.pending[0].ProofID)
			tx.Sender = rcOther
			f.w.add(tx)
		}, "S10", "is not the funder"},
		{"zero amount", func(f *fixture) {
			f.w.add(ltRewardTx(f.nonce, stMiner, 0, f.pending[0].ProofID))
		}, "S10", "amount 0 is not positive"},
		{"NaN amount", func(f *fixture) {
			f.w.add(ltRewardTx(f.nonce, stMiner, math.NaN(), f.pending[0].ProofID))
		}, "S10", "amount NaN is not positive"},
		{"infinite amount", func(f *fixture) {
			f.w.add(ltRewardTx(f.nonce, stMiner, math.Inf(1), f.pending[0].ProofID))
		}, "S10", "amount +Inf is not positive"},
		{"fee", func(f *fixture) {
			tx := ltRewardTx(f.nonce, stMiner, rcCell, f.pending[0].ProofID)
			tx.Fee = 0.5
			f.w.add(tx)
		}, "S10", "fee 0.5 is not zero"},
		{"sum above the schedule", func(f *fixture) {
			f.w.add(ltRewardTx(f.nonce, stMiner, rcCell*0.75, f.pending[0].ProofID),
				ltRewardTx(f.nonce+1, stMiner, rcCell*0.75, f.pending[1].ProofID))
		}, "S10", "above the schedule"},
		{"sum just above the slack", func(f *fixture) {
			f.w.add(ltRewardTx(f.nonce, stMiner, rcCell*(1+4*RewardSumSlack), f.pending[0].ProofID))
		}, "S10", "above the schedule"},
		{"tag outside the reward contract", func(f *fixture) {
			hb := rcHeartbeat(f.nonce, 11)
			hb.Payload = ltPayload(f.pending[0].ProofID)
			f.w.add(hb)
		}, "S10", "LMP1 payload outside the reward contract"},
		{"tag below h0", func(f *fixture) {
			blk := f.w.blocks[5]
			hb := rcHeartbeat(99, 5)
			hb.Payload = ltPayload(f.pending[0].ProofID)
			blk.Transactions = append(blk.Transactions, hb)
		}, "S10", "height 5 is below h0 10"},
		{"genesis mismatch", func(f *fixture) {
			g := *f.w.blocks[0]
			g.Hash = "another-genesis"
			f.w.blocks[0] = &g
		}, "S9", `meta genesis_hash "`},
		{"h0 above tip+1", func(f *fixture) {
			f.w.trim(8)
		}, "S9", "meta h0 10 is above tip+1 9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newRCWorld(t, 9) // h0 = 10
			b1 := w.bootHash(rcHash1)
			b1.mustClean()
			f := &fixture{w: w, paid: b1.accept()}
			b1.tick() // 10 pays f.paid
			f.pending = []Record{b1.accept(), b1.accept(), b1.accept()}
			f.nonce = w.funderNonce()
			w.shutdown()
			tc.mutate(f)

			b2 := w.bootHash(rcHash1)
			if tc.step == "" {
				b2.mustClean()
				if b2.rep.Paid != 2 || b2.rep.Counts != (ReconcileCounts{Updated: 1}) || b2.rep.Pending != 2 {
					t.Fatalf("control report %+v", b2.rep)
				}
				return
			}
			b2.mustTrip(tc.step, CauseReconcile+":"+tc.step+": ")
			if len(b2.rep.Anomalies) == 0 || !strings.Contains(b2.rep.Anomalies[0], tc.want) {
				t.Fatalf("anomalies %q, want %q first", b2.rep.Anomalies, tc.want)
			}
			if b2.rep.AnomalyCount != len(b2.rep.Anomalies) {
				t.Fatalf("anomaly count %d, listed %d", b2.rep.AnomalyCount, len(b2.rep.Anomalies))
			}
			// S11 did not run: the DB keeps its evidence.
			if n := stEventCount(t, b2.store, storeEventReconcile); n != 1 {
				t.Fatalf("%d reconcile events, want the first boot's only", n)
			}
			if got := stPendingIDs(t, b2.store); !reflect.DeepEqual(got, rcIDs(f.pending...)) {
				t.Fatalf("pending rows changed: %x", got)
			}
			rows, err := b2.store.Lookup([]ProofID{f.paid.ProofID})
			if err != nil || rows[f.paid.ProofID].PaidHeight != 10 {
				t.Fatalf("paid row changed: %+v %v", rows, err)
			}
			if n := stEventCount(t, b2.store, "freeze"); n != 1 {
				t.Fatalf("%d freeze events, want 1", n)
			}
			if b2.rep.Totals != (Totals{}) || b2.rep.Pending != 0 || b2.ledger.Outstanding() != 0 {
				t.Fatalf("S12-S13 ran: %+v", b2.rep)
			}
			// The latch and the anomaly both survive a restart.
			b3 := w.bootHash(rcHash1)
			if b3.rep.Clean || b3.rep.Step != tc.step || b3.guard.State() != StateFrozen {
				t.Fatalf("restart after an anomaly: %+v, state %v", b3.rep, b3.guard.State())
			}
		})
	}
}

func TestReconcileAnomalyListIsBounded(t *testing.T) {
	requireHLSQLite(t)
	w := newRCWorld(t, 9)
	w.bootHash(rcHash1).mustClean()
	n := w.funderNonce()
	for i := 0; i < reconcileMaxAnomalies+5; i++ {
		w.add(rcD7Reward(n+uint64(i), stMiner, rcCell/100))
	}
	b := w.bootHash(rcHash1)
	b.mustTrip("S10", CauseReconcile+":S10: ")
	if len(b.rep.Anomalies) != reconcileMaxAnomalies || b.rep.AnomalyCount != reconcileMaxAnomalies+5 {
		t.Fatalf("%d listed, %d counted", len(b.rep.Anomalies), b.rep.AnomalyCount)
	}
	if !strings.Contains(b.err.Error(), fmt.Sprintf("(and %d more)", reconcileMaxAnomalies+4)) {
		t.Fatalf("error %q does not count the rest", b.err)
	}
}

// -----------------------------------------------------------------------------
// S12: counters per config hash
// -----------------------------------------------------------------------------

func TestReconcileCountersPerConfigHash(t *testing.T) {
	requireHLSQLite(t)
	w := newRCWorld(t, 9)

	// Phase 1 (H1): window [10, ...).
	b1 := w.bootHash(rcHash1)
	b1.mustClean()
	b1.accept()
	b1.accept()
	b1.accept()
	b1.tick() // 10
	b1.accept()
	b1.tick() // 11
	leftover := b1.accept()
	p1 := b1.ledger.Totals()
	if p1.Proofs != 5 {
		t.Fatalf("phase 1 ledger totals %+v", p1)
	}

	// A restart with the same hash derives the counters; nothing resets.
	b2 := w.bootHash(rcHash1)
	b2.mustClean()
	if b2.rep.Totals.Proofs != p1.Proofs || b2.rep.Window.FirstHeight != 10 || b2.rep.Counts.WindowInserted {
		t.Fatalf("restart report %+v", b2.rep)
	}
	rcSameFloat(t, "emitted_H1", b2.rep.Totals.Emitted, p1.Emitted)

	// Phase 2 (H2): a new hash starts a new window at tip+1 with zero counters.
	start2 := w.tip() + 1
	b3 := w.bootHash(rcHash2)
	b3.mustClean()
	if !b3.rep.Counts.WindowInserted || b3.rep.Window.FirstHeight != start2 || b3.rep.Totals != (Totals{ConfigSHA256: rcHash2}) {
		t.Fatalf("phase 2 report %+v", b3.rep)
	}
	if b3.rep.Pending != 1 || b3.ledger.Totals() != b3.rep.Totals {
		t.Fatalf("phase 2: pending %d, ledger %+v", b3.rep.Pending, b3.ledger.Totals())
	}
	c1, err := b3.store.Counters(rcHash1)
	if err != nil || !c1.Known || c1.Proofs != 5 || c1.Paid != 4 || c1.Window.FirstHeight != 10 {
		t.Fatalf("phase 1 counters %+v, %v", c1, err)
	}
	// Phase 2 pays the phase-1 leftover and two H2 proofs in one tx.
	b3.accept()
	b3.accept()
	blk := b3.tick()
	if len(blk.Transactions) != 1 {
		t.Fatalf("block %+v", blk)
	}
	p2 := b3.ledger.Totals()
	if p2.Proofs != 2 {
		t.Fatalf("phase 2 ledger totals %+v", p2)
	}
	rcSameFloat(t, "phase 2 emitted", p2.Emitted, blk.Transactions[0].Amount)

	// emitted_H2 counts every LMP1 reward from FirstHeight, including the
	// one that paid the H1 leftover; proofs_H2 counts only H2 rows.
	b4 := w.bootHash(rcHash2)
	b4.mustClean()
	if b4.rep.Totals.ConfigSHA256 != rcHash2 || b4.rep.Totals.Proofs != 2 || b4.rep.Paid != 7 {
		t.Fatalf("phase 2 restart report %+v", b4.rep)
	}
	rcSameFloat(t, "emitted_H2", b4.rep.Totals.Emitted, p2.Emitted)
	if rows, _ := b4.store.Lookup([]ProofID{leftover.ProofID}); rows[leftover.ProofID].PaidHeight != blk.Height {
		t.Fatalf("leftover not paid at %d: %+v", blk.Height, rows)
	}

	// Returning to H1 reuses its window: emitted_H1 is every LMP1 reward from
	// height 10 in block and tx order, and proofs_H1 is unchanged.
	b5 := w.bootHash(rcHash1)
	b5.mustClean()
	var want float64
	for _, blk := range w.blocks[10:] {
		for _, tx := range blk.Transactions {
			if tx.ContractID == chain.MiningRewardContractID {
				want += tx.Amount
			}
		}
	}
	if b5.rep.Totals.Proofs != 5 || b5.rep.Window.FirstHeight != 10 || b5.rep.Counts.WindowInserted {
		t.Fatalf("back to H1: %+v", b5.rep)
	}
	rcSameFloat(t, "emitted_H1 after phase 2", b5.rep.Totals.Emitted, want)
}

// S12 applies the graceful ADMISSION_STOP triggers to the derived counters.
// They latch but do not make reconciliation unclean: payouts continue.
func TestReconcileCounterStops(t *testing.T) {
	requireHLSQLite(t)
	setup := func(t *testing.T) *rcWorld {
		w := newRCWorld(t, 9)
		b := w.bootHash(rcHash1)
		b.mustClean()
		b.accept()
		b.accept()
		b.tick()
		b.accept()
		b.tick() // 3 proofs, emitted 2*rcCell
		w.shutdown()
		return w
	}
	cases := []struct {
		name  string
		cfg   func(c *Config)
		now   time.Time
		cause string
	}{
		{"proofs total", func(c *Config) { c.MaxProofsTotal = 3 }, rcNow, CauseProofsTotal + ":3>=3"},
		{"budget", func(c *Config) { c.BudgetCell = 9 }, rcNow, CauseBudget + ":emitted="},
		{"expiry", func(c *Config) {}, time.Unix(rcNow.Unix()+3600, 0), fmt.Sprintf("%s:expires_unix=%d", CauseExpired, rcNow.Unix()+3600)},
		{"below every threshold", func(c *Config) { c.MaxProofsTotal = 4; c.BudgetCell = 15 }, time.Unix(rcNow.Unix()+3599, 0), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := setup(t)
			cfg := rcConfig()
			tc.cfg(&cfg)
			b := w.boot(rcOpts{cfg: &cfg, now: tc.now})
			b.mustClean()
			// Reconcile itself tripped the latch: nothing has called State.
			if tc.cause == "" {
				if w.exists(AdmissionStoppedFile) {
					t.Fatalf("ADMISSION_STOPPED: %s", gtReadCause(t, w.dir, MarkerAdmissionStopped))
				}
				if b.guard.State() != StateOpen {
					t.Fatalf("state %v", b.guard.State())
				}
				return
			}
			if !w.exists(AdmissionStoppedFile) {
				t.Fatal("no ADMISSION_STOPPED latch after S12")
			}
			if got := gtReadCause(t, w.dir, MarkerAdmissionStopped); !strings.HasPrefix(got, tc.cause) {
				t.Fatalf("cause %q, want prefix %q", got, tc.cause)
			}
			if s := b.guard.State(); s != StateAdmissionStopped || !s.PayoutsEnabled() {
				t.Fatalf("state %v", s)
			}
			if b.rep.Totals.Proofs != 3 {
				t.Fatalf("totals %+v", b.rep.Totals)
			}

			// A new config hash starts new counters; once the operator clears
			// the latch, the new window boots open.
			for _, f := range []string{AdmissionStoppedFile, AdmissionStoppedCauseFile} {
				if err := os.Remove(filepath.Join(w.dir, f)); err != nil {
					t.Fatal(err)
				}
			}
			cfg2 := rcConfig()
			tc.cfg(&cfg2)
			cfg2.ExpiresUnix = tc.now.Unix() + 3600
			b2 := w.boot(rcOpts{hash: rcHash2, cfg: &cfg2, now: tc.now})
			b2.mustClean()
			if b2.rep.Totals != (Totals{ConfigSHA256: rcHash2}) || w.exists(AdmissionStoppedFile) || b2.guard.State() != StateOpen {
				t.Fatalf("new window: %+v, state %v", b2.rep.Totals, b2.guard.State())
			}
		})
	}
}

// The active window can never start above tip+1 (each window starts at the
// tip+1 of the boot that inserted it, and W keeps later chains at least that
// long), so a later start is an anomaly.
func TestReconcileWindowAboveTip(t *testing.T) {
	requireHLSQLite(t)
	w := newRCWorld(t, 9)
	b1 := w.bootHash(rcHash1)
	b1.mustClean()
	for i := 0; i < 4; i++ {
		b1.tick() // 10..13
	}
	w.bootHash(rcHash2).mustClean() // H2 window at 14
	w.trim(11)
	b := w.bootHash(rcHash2)
	b.mustTrip("S12", CauseReconcile+":S12: config window ")
	if !strings.Contains(b.err.Error(), "starts at height 14, above tip+1 12") {
		t.Fatalf("error %q", b.err)
	}
}

// -----------------------------------------------------------------------------
// S7, S8 and store failures
// -----------------------------------------------------------------------------

func TestReconcileOpenFailures(t *testing.T) {
	t.Run("S7 foreign file", func(t *testing.T) {
		w := newRCWorld(t, 9)
		stWriteFile(t, stPath(w.dir), []byte("not a database"))
		b := w.bootHash(rcHash1)
		b.mustTrip("S7", CauseDBOpen+":S7: open: ")
		if !errors.Is(b.err, ErrSchema) || b.rep.StoreOpen {
			t.Fatalf("err %v, report %+v", b.err, b.rep)
		}
	})
	t.Run("S8 stale sidecar", func(t *testing.T) {
		w := newRCWorld(t, 9)
		stWriteFile(t, stPath(w.dir)+"-wal", nil)
		b := w.bootHash(rcHash1)
		b.mustTrip("S8", CauseDBOpen+":S8: create: ")
		if !errors.Is(b.err, ErrUnsafePath) || b.rep.StoreOpen || b.rep.Created || w.exists(DBFile) {
			t.Fatalf("err %v, report %+v", b.err, b.rep)
		}
	})
	t.Run("S7 unsafe path", func(t *testing.T) {
		w := newRCWorld(t, 9)
		b := w.boot(rcOpts{edit: func(c *ReconcileConfig) { c.DBPath = filepath.Join(filepath.Dir(w.dir), DBFile) }})
		b.mustTrip("S7", CauseDBOpen+":S7: open: ")
		if !errors.Is(b.err, ErrUnsafePath) {
			t.Fatalf("err %v", b.err)
		}
	})
}

// rcFaultStore fails one Store method with errRC.
type rcFaultStore struct {
	*SQLiteStore
	fail string
}

func (s *rcFaultStore) Meta() (Meta, error) {
	if s.fail == "Meta" {
		return Meta{}, errRC
	}
	return s.SQLiteStore.Meta()
}

func (s *rcFaultStore) Lookup(ids []ProofID) (map[ProofID]Record, error) {
	if s.fail == "Lookup" {
		return nil, errRC
	}
	return s.SQLiteStore.Lookup(ids)
}

func (s *rcFaultStore) ApplyReconcile(r Reconciliation) (ReconcileCounts, error) {
	if s.fail == "ApplyReconcile" {
		return ReconcileCounts{}, errRC
	}
	return s.SQLiteStore.ApplyReconcile(r)
}

func (s *rcFaultStore) Counters(h ConfigHash) (Counters, error) {
	switch s.fail {
	case "Counters":
		return Counters{}, errRC
	case "CountersUnknown":
		return Counters{Window: ConfigWindow{ConfigSHA256: h}}, nil
	}
	return s.SQLiteStore.Counters(h)
}

func (s *rcFaultStore) Pending() ([]Record, error) {
	if s.fail == "Pending" {
		return nil, errRC
	}
	return s.SQLiteStore.Pending()
}

func TestReconcileStoreFailures(t *testing.T) {
	requireHLSQLite(t)
	for _, tc := range []struct {
		method, step, prefix string
		injected             bool
	}{
		{"Meta", "S9", "read meta", true},
		{"Lookup", "S10", "lookup", true},
		{"ApplyReconcile", "S11", "apply", true},
		{"Counters", "S12", "counters", true},
		{"CountersUnknown", "S12", "config window ", false},
		{"Pending", "S13", "pending", true},
	} {
		t.Run(tc.method, func(t *testing.T) {
			w := newRCWorld(t, 9)
			b1 := w.bootHash(rcHash1)
			b1.mustClean()
			b1.accept()
			b1.tick()
			b := w.boot(rcOpts{store: func(s *SQLiteStore) Store { return &rcFaultStore{SQLiteStore: s, fail: tc.method} }})
			b.mustTrip(tc.step, CauseReconcile+":"+tc.step+": "+tc.prefix)
			if errors.Is(b.err, errRC) != tc.injected {
				t.Fatalf("err %v: wraps the injected error %v, want %v", b.err, errors.Is(b.err, errRC), tc.injected)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Inputs, S13 and S14
// -----------------------------------------------------------------------------

func TestReconcileInputs(t *testing.T) {
	t.Run("nil guard", func(t *testing.T) {
		rep, err := Reconcile(ReconcileConfig{})
		if err == nil || errors.Is(err, ErrReconcile) || rep.Clean {
			t.Fatalf("Reconcile = %+v, %v", rep, err)
		}
	})
	for _, tc := range []struct {
		name string
		edit func(w *rcWorld, c *ReconcileConfig)
		want string
	}{
		{"nil store", func(_ *rcWorld, c *ReconcileConfig) { c.Store = nil }, "Store, Ledger and Accounts are required"},
		{"nil ledger", func(_ *rcWorld, c *ReconcileConfig) { c.Ledger = nil }, "Store, Ledger and Accounts are required"},
		{"nil accounts", func(_ *rcWorld, c *ReconcileConfig) { c.Accounts = nil }, "Store, Ledger and Accounts are required"},
		{"empty chain", func(_ *rcWorld, c *ReconcileConfig) { c.Blocks = nil }, "the restored chain is empty"},
		{"gap", func(w *rcWorld, c *ReconcileConfig) {
			c.Blocks = append(append([]*chain.Block(nil), w.blocks[:4]...), w.blocks[5:]...)
		}, "index 4 is not the block at height 4"},
		{"nil block", func(w *rcWorld, c *ReconcileConfig) {
			c.Blocks = append([]*chain.Block(nil), w.blocks...)
			c.Blocks[3] = nil
		}, "index 3 is not the block at height 3"},
		{"no genesis", func(w *rcWorld, c *ReconcileConfig) { c.Blocks = w.blocks[1:] }, "index 0 is not the block at height 0"},
		{"genesis without hash", func(w *rcWorld, c *ReconcileConfig) {
			g := *w.blocks[0]
			g.Hash = ""
			c.Blocks = append([]*chain.Block{&g}, w.blocks[1:]...)
		}, "the genesis block has no hash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newRCWorld(t, 9)
			b := w.boot(rcOpts{edit: func(c *ReconcileConfig) { tc.edit(w, c) }})
			b.mustTrip("S7", CauseReconcile+":S7: ")
			if !strings.Contains(b.err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", b.err, tc.want)
			}
			if b.rep.StoreOpen || w.exists(DBFile) {
				t.Fatal("the DB was opened or created")
			}
		})
	}
}

func TestReconcileLedgerInitFailures(t *testing.T) {
	requireHLSQLite(t)
	t.Run("funder missing", func(t *testing.T) {
		w := newRCWorld(t, 9)
		b := w.boot(rcOpts{edit: func(c *ReconcileConfig) { c.Accounts = chain.NewAccountStore() }})
		b.mustTrip("S13", CauseReconcile+":S13: funder account "+ltFunder+" is missing")
		// S11 ran: the DB holds the window, and only the Ledger is missing.
		if n := stEventCount(t, b.store, storeEventReconcile); n != 1 || !b.rep.StoreOpen {
			t.Fatalf("%d reconcile events, report %+v", n, b.rep)
		}
	})
	t.Run("ledger already initialized", func(t *testing.T) {
		w := newRCWorld(t, 9)
		b := w.boot(rcOpts{edit: func(c *ReconcileConfig) {
			if err := c.Ledger.Init(LedgerInit{Totals: Totals{ConfigSHA256: rcHash1}}); err != nil {
				t.Fatal(err)
			}
		}})
		if b.rep.Clean || b.rep.Step != "S13" || !errors.Is(b.err, ErrInvariant) {
			t.Fatalf("Reconcile = %+v, %v", b.rep, b.err)
		}
		if got := gtReadCause(t, w.dir, MarkerTripped); !strings.HasPrefix(got, CauseReconcile+":S13: ledger init: ") {
			t.Fatalf("cause %q", got)
		}
	})
}

// rcPreArmFails is the real guard with a failing PreArm.
type rcPreArmFails struct{ *CanaryGuard }

func (rcPreArmFails) PreArm() error { return errGTNoSpace }

func TestReconcilePreArm(t *testing.T) {
	requireHLSQLite(t)
	t.Run("failure after a clean reconcile", func(t *testing.T) {
		w := newRCWorld(t, 9)
		b := w.boot(rcOpts{guard: func(g *CanaryGuard) Guard { return rcPreArmFails{g} }})
		if b.rep.Clean || b.rep.Step != "S14" || !errors.Is(b.err, errGTNoSpace) || !errors.Is(b.err, ErrReconcile) {
			t.Fatalf("Reconcile = %+v, %v", b.rep, b.err)
		}
		if got := gtReadCause(t, w.dir, MarkerTripped); !strings.HasPrefix(got, CausePreArm+":S14: ") {
			t.Fatalf("cause %q", got)
		}
		if !b.ledgerInitialized() || b.guard.State() != StateFrozen {
			t.Fatal("S13 should have completed and FREEZE latched")
		}
	})
	t.Run("an earlier failure is reported first", func(t *testing.T) {
		w := newRCWorld(t, 9)
		b := w.boot(rcOpts{
			guard: func(g *CanaryGuard) Guard { return rcPreArmFails{g} },
			edit:  func(c *ReconcileConfig) { c.Blocks = nil },
		})
		if b.rep.Step != "S7" || !strings.Contains(b.err.Error(), "empty") {
			t.Fatalf("Reconcile = %+v, %v", b.rep, b.err)
		}
		if got := gtReadCause(t, w.dir, MarkerTripped); !strings.HasPrefix(got, CauseReconcile+":S7: ") {
			t.Fatalf("cause %q", got)
		}
	})
	t.Run("armed markers let a later trip rename", func(t *testing.T) {
		w := newRCWorld(t, 9)
		b := w.bootHash(rcHash1)
		b.mustClean()
		armed, err := os.ReadFile(filepath.Join(w.dir, TrippedArmedFile))
		if err != nil {
			t.Fatal(err)
		}
		b.guard.Freeze("test")
		tripped, err := os.ReadFile(filepath.Join(w.dir, TrippedFile))
		if err != nil || !reflect.DeepEqual(armed, tripped) || w.exists(TrippedArmedFile) {
			t.Fatalf("TRIPPED.json %q, armed %q: %v", tripped, armed, err)
		}
	})
}
