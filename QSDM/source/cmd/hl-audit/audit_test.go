package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

const (
	minerA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	minerB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	nodeID = "canary-node-1"
)

func pid(b byte) legacymining.ProofID {
	var id legacymining.ProofID
	for i := range id {
		id[i] = b
	}
	return id
}

func blockHash(salt string, height uint64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s/%d", salt, height)))
	return hex.EncodeToString(sum[:])
}

func heartbeat(height uint64) *mempool.Tx {
	f := chain.MiningRewardFunderAddress
	return &mempool.Tx{ID: fmt.Sprintf("solo-heartbeat-%d", height), Sender: f, Recipient: f, Nonce: height}
}

func rewardTx(nonce uint64, addr string, amount float64, ids ...legacymining.ProofID) *mempool.Tx {
	ids = append([]legacymining.ProofID(nil), ids...)
	sort.Slice(ids, func(i, j int) bool { return bytes.Compare(ids[i][:], ids[j][:]) < 0 })
	payload, err := legacymining.EncodePayload(ids)
	if err != nil {
		panic(err)
	}
	return &mempool.Tx{
		ID: rewardID(nonce, addr, payload), Sender: chain.MiningRewardFunderAddress, Recipient: addr,
		Amount: amount, Nonce: nonce, Payload: payload, ContractID: chain.MiningRewardContractID,
	}
}

// fixture is a copy of a producer state directory.
type fixture struct {
	t       *testing.T
	dir     string
	origin  uint64 // the height of blocks[0]
	blocks  []*chain.Block
	h0      uint64
	window  uint64
	cfg     []byte
	cfgHash legacymining.ConfigHash
	// boots are the S11 activations applied to the DB, in order; nil means
	// one activation of cfgHash at window.
	boots    []legacymining.ConfigWindow
	events   []legacymining.Event // extra events rows, written after the boots
	rows     []legacymining.Record
	payments []legacymining.Payment
	noDB     bool
}

func canaryConfig(budget, maxTotal uint64) []byte { return canaryConfigFor(minerA, budget, maxTotal) }

func canaryConfigFor(addr string, budget, maxTotal uint64) []byte {
	return []byte(fmt.Sprintf(`{"version":1,"allowed":[{"miner_addr":%q,"node_id":%q}],"max_proofs_per_min":60,"max_proofs_total":%d,"max_pending":600,"budget_cell":%d,"expires_unix":1790000000}`+"\n",
		addr, nodeID, maxTotal, budget))
}

// newFixture builds the clean scenario: blocks 0..12 with a heartbeat each,
// h0 = 5, proofs P1 and P2 paid at 6, P3 paid at 8, P4 pending, W = 12.
func newFixture(t *testing.T) *fixture {
	f := &fixture{t: t, dir: t.TempDir(), h0: 5, window: 5, cfg: canaryConfig(1291, 3000)}
	f.cfgHash = sha256.Sum256(f.cfg)
	f.chain(12, "main")
	f.pay(6, 100, minerA, pid(1), pid(2))
	f.pay(8, 101, minerA, pid(3))
	for i, b := range []byte{1, 2, 3, 4} {
		f.rows = append(f.rows, legacymining.Record{
			ProofID: pid(b), MinerAddr: minerA, NodeID: nodeID, AttNonce: pid(b + 100),
			WorkHeight: 5, AcceptTip: 5, AcceptedNS: int64(1000 + i), ConfigSHA256: f.cfgHash, ProofJSON: []byte(`{}`),
		})
	}
	f.markPaid(6, pid(1), pid(2))
	f.markPaid(8, pid(3))
	return f
}

// chain replaces the blocks with heights origin..tip, keeping the
// transactions already placed at heights that remain.
func (f *fixture) chain(tip uint64, salt string) {
	old := f.blocks
	f.blocks = nil
	prev := ""
	if f.origin > 0 {
		prev = blockHash(salt, f.origin-1)
	}
	for hgt := f.origin; hgt <= tip; hgt++ {
		b := &chain.Block{Height: hgt, PrevHash: prev, Hash: blockHash(salt, hgt), Transactions: []*mempool.Tx{heartbeat(hgt)}}
		if i := hgt - f.origin; i < uint64(len(old)) {
			b.Transactions = old[i].Transactions
		}
		f.blocks = append(f.blocks, b)
		prev = b.Hash
	}
}

// at returns the block at height.
func (f *fixture) at(height uint64) *chain.Block { return f.blocks[height-f.origin] }

// rehash gives every block from height `from` a new hash (a replaced branch).
func (f *fixture) rehash(from uint64, salt string) {
	for i := from; i < f.origin+uint64(len(f.blocks)); i++ {
		f.at(i).Hash = blockHash(salt, i)
		if i > f.origin {
			f.at(i).PrevHash = f.at(i - 1).Hash
		}
	}
}

func (f *fixture) pay(height, nonce uint64, addr string, ids ...legacymining.ProofID) *mempool.Tx {
	tx := rewardTx(nonce, addr, legacymining.DefaultRewardCell(height), ids...)
	f.at(height).Transactions = append(f.at(height).Transactions, tx)
	return tx
}

// markPaid records the DB side of the payment in block height.
func (f *fixture) markPaid(height uint64, ids ...legacymining.ProofID) {
	var tx *mempool.Tx
	for _, c := range f.at(height).Transactions {
		if c.ContractID == chain.MiningRewardContractID {
			tx = c
		}
	}
	if tx == nil {
		f.t.Fatalf("no reward tx at height %d", height)
	}
	for _, id := range ids {
		f.payments = append(f.payments, legacymining.Payment{ProofID: id, MinerAddr: tx.Recipient, Height: height, TxID: tx.ID})
	}
}

func (f *fixture) journalPath() string { return filepath.Join(f.dir, JournalFile) }
func (f *fixture) dbPath() string {
	return filepath.Join(f.dir, legacymining.LegacyDirName, legacymining.DBFile)
}

func (f *fixture) writeJournal(tail string) {
	var buf bytes.Buffer
	for _, b := range f.blocks {
		js, err := json.Marshal(b)
		if err != nil {
			f.t.Fatal(err)
		}
		buf.Write(js)
		buf.WriteByte('\n')
	}
	buf.WriteString(tail)
	if err := os.WriteFile(f.journalPath(), buf.Bytes(), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) writeWatermark(name string, height uint64, hash, source string) {
	js, err := json.Marshal(legacymining.Watermark{Version: 1, Height: height, Hash: hash, Source: source, WrittenNS: 1})
	if err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, name), append(js, '\n'), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) writeDB() {
	if f.noDB {
		return
	}
	dir := filepath.Dir(f.dbPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		f.t.Fatal(err)
	}
	st := legacymining.NewSQLiteStore()
	if err := st.Open(f.dbPath(), &legacymining.Meta{H0: f.h0, GenesisHash: f.blocks[0].Hash, Funder: chain.MiningRewardFunderAddress, Release: "test", CreatedNS: 1}); err != nil {
		f.t.Fatal(err)
	}
	defer st.Close()
	boots := f.boots
	if boots == nil {
		boots = []legacymining.ConfigWindow{{ConfigSHA256: f.cfgHash, FirstHeight: f.window, ActivatedNS: 1}}
	}
	for _, w := range boots {
		if _, err := st.ApplyReconcile(legacymining.Reconciliation{Window: w}); err != nil {
			f.t.Fatal(err)
		}
	}
	for _, ev := range f.events {
		if err := st.Event(ev); err != nil {
			f.t.Fatal(err)
		}
	}
	for _, r := range f.rows {
		if err := st.Accept(r); err != nil {
			f.t.Fatal(err)
		}
	}
	if len(f.payments) > 0 {
		if err := st.MarkPaid(f.payments); err != nil {
			f.t.Fatal(err)
		}
	}
}

// write materialises the state directory with W at the tip.
func (f *fixture) write() {
	f.writeJournal("")
	tip := f.blocks[len(f.blocks)-1]
	f.writeWatermark(legacymining.WatermarkFile, tip.Height, tip.Hash, legacymining.WatermarkSourceSeal)
	f.writeDB()
}

func (f *fixture) options() Options {
	return Options{
		JournalPath:   f.journalPath(),
		WatermarkPath: filepath.Join(f.dir, legacymining.WatermarkFile),
		RetiredDir:    f.dir,
		DBPath:        f.dbPath(),
		TempDir:       f.t.TempDir(),
	}
}

func (f *fixture) audit(mut func(*Options)) *Report {
	f.t.Helper()
	o := f.options()
	if mut != nil {
		mut(&o)
	}
	rep, err := Audit(o)
	if err != nil {
		f.t.Fatalf("Audit: %v", err)
	}
	return rep
}

func codes(r *Report) []string {
	var out []string
	for _, f := range r.Findings {
		out = append(out, f.Severity+":"+f.Code)
	}
	return out
}

func wantPass(t *testing.T, r *Report) {
	t.Helper()
	if r.Result != resultPass || r.Failed() {
		t.Fatalf("result %s, findings %v", r.Result, r.Findings)
	}
}

func wantFail(t *testing.T, r *Report, code string) {
	t.Helper()
	if r.Result != resultFail || !r.Failed() {
		t.Fatalf("result %s, want FAIL with %s; findings %v", r.Result, code, r.Findings)
	}
	for _, f := range r.Findings {
		if f.Code == code && f.Severity == sevFail {
			return
		}
	}
	t.Fatalf("no fail finding %s; findings %v", code, codes(r))
}

func TestCleanAudit(t *testing.T) {
	f := newFixture(t)
	f.write()
	r := f.audit(nil)
	wantPass(t, r)
	if len(r.Findings) != 0 {
		t.Fatalf("findings %v", r.Findings)
	}
	if r.Chain.Blocks != 13 || r.Chain.Tip != 12 || r.Chain.TipHash != f.blocks[12].Hash || r.Chain.GenesisHash != f.blocks[0].Hash {
		t.Fatalf("chain summary %+v", r.Chain)
	}
	if r.DB == nil || r.DB.H0 != 5 || r.DB.Rows != 4 || r.DB.Paid != 3 || r.DB.Unpaid != 1 || r.DB.Windows != 1 {
		t.Fatalf("db summary %+v", r.DB)
	}
	p := r.Payments
	if p.RewardTxs != 2 || p.PayloadIDs != 3 || p.DistinctIDs != 3 || p.FirstPaidHeight != 6 || p.LastPaidHeight != 8 {
		t.Fatalf("payments %+v", p)
	}
	if hw := r.ServedHighWater; hw == nil || hw.Height != 12 || hw.Hash != f.blocks[12].Hash {
		t.Fatalf("served high-water %+v", r.ServedHighWater)
	}
}

// TestDoublePayDetected is the WP11 acceptance "the audit detects injected
// double pay" (oracle 1).
func TestDoublePayDetected(t *testing.T) {
	t.Run("later block", func(t *testing.T) {
		f := newFixture(t)
		f.pay(10, 102, minerA, pid(3)) // P3 was paid at 8
		f.write()
		r := f.audit(nil)
		wantFail(t, r, CodeDoublePay)
		if r.Payments.PayloadIDs != 4 || r.Payments.DistinctIDs != 3 {
			t.Fatalf("payments %+v", r.Payments)
		}
	})
	t.Run("same block", func(t *testing.T) {
		f := newFixture(t)
		f.pay(6, 102, minerA, pid(2))
		f.write()
		r := f.audit(nil)
		wantFail(t, r, CodeDoublePay)
		wantFail(t, r, CodeRewardOverSchedule) // two full rewards in one block
	})
	t.Run("payload outside the reward contract", func(t *testing.T) {
		f := newFixture(t)
		tx := rewardTx(103, minerA, 0, pid(1))
		tx.ContractID = chain.WalletTransferContractID
		f.blocks[9].Transactions = append(f.blocks[9].Transactions, tx)
		f.write()
		r := f.audit(nil)
		wantFail(t, r, CodeDoublePay)
		wantFail(t, r, CodeTagOutsideReward)
	})
}

// TestWatermarkRegressionDetected is the WP11 acceptance "the audit detects
// injected W regression" (oracle 3).
func TestWatermarkRegressionDetected(t *testing.T) {
	baseline := func(t *testing.T) (*fixture, *Report) {
		f := newFixture(t)
		f.write()
		r := f.audit(nil)
		wantPass(t, r)
		js, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		prev, err := ParseReport(js)
		if err != nil {
			t.Fatal(err)
		}
		return f, prev
	}
	withPrev := func(prev *Report) func(*Options) { return func(o *Options) { o.Prev = prev } }

	t.Run("next audit passes", func(t *testing.T) {
		f, prev := baseline(t)
		f.chain(13, "main")
		f.writeJournal("")
		f.writeWatermark(legacymining.WatermarkFile, 13, f.blocks[13].Hash, legacymining.WatermarkSourceSeal)
		r := f.audit(withPrev(prev))
		wantPass(t, r)
		if r.ServedHighWater.Height != 13 {
			t.Fatalf("high-water %+v", r.ServedHighWater)
		}
	})
	t.Run("W and journal rolled back", func(t *testing.T) {
		f, prev := baseline(t)
		f.chain(9, "main")
		f.writeJournal("")
		f.writeWatermark(legacymining.WatermarkFile, 9, f.blocks[9].Hash, legacymining.WatermarkSourceSeed)
		r := f.audit(withPrev(prev))
		wantFail(t, r, CodeWatermarkRegress)
		wantFail(t, r, CodeServedHeightLost)
		if hw := r.ServedHighWater; hw == nil || hw.Height != 12 {
			t.Fatalf("the high-water mark must not regress: %+v", hw)
		}
	})
	t.Run("served block replaced", func(t *testing.T) {
		f, prev := baseline(t)
		f.rehash(11, "fork")
		f.writeJournal("")
		f.writeWatermark(legacymining.WatermarkFile, 12, f.blocks[12].Hash, legacymining.WatermarkSourceBoot)
		r := f.audit(withPrev(prev))
		wantFail(t, r, CodeServedBlockChanged)
	})
	t.Run("retired watermark above the restored tip", func(t *testing.T) {
		f := newFixture(t)
		f.write()
		f.writeWatermark(legacymining.WatermarkRetiredPrefix+"20260926T000000Z", 12, f.blocks[12].Hash, legacymining.WatermarkSourceSeal)
		if err := os.Remove(filepath.Join(f.dir, legacymining.WatermarkFile)); err != nil {
			t.Fatal(err)
		}
		f.chain(10, "main") // a backup restored without R-B
		f.writeJournal("")
		r := f.audit(nil)
		wantFail(t, r, CodeServedHeightLost)
		if !r.Has(CodeWatermarkAbsent) || len(r.Retired) != 1 {
			t.Fatalf("findings %v retired %v", codes(r), r.Retired)
		}
	})
}

func TestWatermarkAgainstJournal(t *testing.T) {
	cases := []struct {
		name string
		mut  func(f *fixture)
		code string
	}{
		{"W above tip", func(f *fixture) {
			f.writeWatermark(legacymining.WatermarkFile, 13, blockHash("main", 13), legacymining.WatermarkSourceSeal)
		}, CodeWatermarkAboveTip},
		{"hash mismatch at W", func(f *fixture) {
			f.writeWatermark(legacymining.WatermarkFile, 11, blockHash("other", 11), legacymining.WatermarkSourceSeal)
		}, CodeWatermarkHash},
		{"tip above W+1", func(f *fixture) {
			f.writeWatermark(legacymining.WatermarkFile, 10, f.blocks[10].Hash, legacymining.WatermarkSourceSeal)
		}, CodeWatermarkBehindTip},
		{"invalid W", func(f *fixture) {
			if err := os.WriteFile(filepath.Join(f.dir, legacymining.WatermarkFile), []byte(`{"version":1,"height":12,"hash":"x","source":"seal","written_ns":1}`), 0o600); err != nil {
				f.t.Fatal(err)
			}
		}, CodeWatermarkInvalid},
		{"absent W required", func(f *fixture) {
			if err := os.Remove(filepath.Join(f.dir, legacymining.WatermarkFile)); err != nil {
				f.t.Fatal(err)
			}
		}, CodeWatermarkAbsent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.write()
			c.mut(f)
			wantFail(t, f.audit(func(o *Options) { o.RequireWatermark = true }), c.code)
		})
	}
	t.Run("tip = W+1 passes (C7)", func(t *testing.T) {
		f := newFixture(t)
		f.write()
		f.writeWatermark(legacymining.WatermarkFile, 11, f.blocks[11].Hash, legacymining.WatermarkSourceSeal)
		wantPass(t, f.audit(nil))
	})
	t.Run("absent W warns by default", func(t *testing.T) {
		f := newFixture(t)
		f.write()
		if err := os.Remove(filepath.Join(f.dir, legacymining.WatermarkFile)); err != nil {
			t.Fatal(err)
		}
		r := f.audit(nil)
		wantPass(t, r)
		if !r.Has(CodeWatermarkAbsent) {
			t.Fatalf("findings %v", codes(r))
		}
	})
}

func TestChainAgainstDB(t *testing.T) {
	t.Run("orphan payment", func(t *testing.T) {
		f := newFixture(t)
		f.pay(9, 102, minerA, pid(9))
		f.write()
		wantFail(t, f.audit(nil), CodeOrphanPayment)
	})
	t.Run("recipient mismatch", func(t *testing.T) {
		f := newFixture(t)
		f.pay(9, 102, minerB, pid(4))
		f.write()
		wantFail(t, f.audit(nil), CodeRecipientMismatch)
	})
	t.Run("DB paid, chain not", func(t *testing.T) {
		f := newFixture(t)
		f.payments = append(f.payments, legacymining.Payment{ProofID: pid(4), MinerAddr: minerA, Height: 11, TxID: "solo-reward-9-x-y"})
		f.write()
		wantFail(t, f.audit(nil), CodeDBPaidNotOnChain)
	})
	t.Run("DB paid elsewhere", func(t *testing.T) {
		f := newFixture(t)
		f.payments[2].Height = 9
		f.write()
		wantFail(t, f.audit(nil), CodeDBPaidMismatch)
	})
	t.Run("chain paid, DB not yet marked (C7) warns", func(t *testing.T) {
		f := newFixture(t)
		f.pay(12, 102, minerA, pid(4))
		f.write()
		r := f.audit(nil)
		wantPass(t, r)
		if !r.Has(CodeDBUnmarked) {
			t.Fatalf("findings %v", codes(r))
		}
	})
	t.Run("tag below h0", func(t *testing.T) {
		f := newFixture(t)
		f.pay(3, 102, minerA, pid(4))
		f.write()
		wantFail(t, f.audit(nil), CodeTagBelowH0)
	})
	t.Run("bad rewards", func(t *testing.T) {
		f := newFixture(t)
		untagged := rewardTx(102, minerA, 1, pid(9))
		untagged.Payload = nil
		badID := rewardTx(103, minerA, 1, pid(4))
		badID.ID = "solo-reward-103-forged"
		badSender := rewardTx(104, minerA, 1, pid(5))
		badSender.Sender = minerB
		f.blocks[9].Transactions = append(f.blocks[9].Transactions, untagged)
		f.blocks[10].Transactions = append(f.blocks[10].Transactions, badID)
		f.blocks[11].Transactions = append(f.blocks[11].Transactions, badSender)
		f.write()
		r := f.audit(nil)
		for _, c := range []string{CodeRewardUntagged, CodeRewardBadID, CodeRewardBadSender} {
			wantFail(t, r, c)
		}
	})
	t.Run("legacy untagged reward below h0 is fine", func(t *testing.T) {
		f := newFixture(t)
		legacy := &mempool.Tx{ID: "legacy-reward", Sender: chain.MiningRewardFunderAddress, Recipient: minerA, Amount: 3, ContractID: chain.MiningRewardContractID}
		f.blocks[2].Transactions = append(f.blocks[2].Transactions, legacy)
		f.write()
		wantPass(t, f.audit(nil))
	})
	t.Run("payments without a DB", func(t *testing.T) {
		f := newFixture(t)
		f.noDB = true
		f.write()
		wantFail(t, f.audit(nil), CodeDBMissing)
	})
	t.Run("meta genesis mismatch", func(t *testing.T) {
		f := newFixture(t)
		f.write()
		f.rehash(0, "other-genesis")
		f.writeJournal("")
		f.writeWatermark(legacymining.WatermarkFile, 12, f.blocks[12].Hash, legacymining.WatermarkSourceSeal)
		wantFail(t, f.audit(nil), CodeDBMeta)
	})
	t.Run("not a legacy-mining DB", func(t *testing.T) {
		f := newFixture(t)
		f.noDB = true
		f.write()
		if err := os.MkdirAll(filepath.Dir(f.dbPath()), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.dbPath(), []byte("not sqlite"), 0o600); err != nil {
			t.Fatal(err)
		}
		r := f.audit(nil)
		wantFail(t, r, CodeDBOpen)
		if r.Has(CodeDBMissing) {
			t.Fatalf("findings %v", codes(r))
		}
	})
}

func TestJournalShape(t *testing.T) {
	t.Run("torn tail", func(t *testing.T) {
		f := newFixture(t)
		f.write()
		f.writeJournal(`{"height":13,"prev_hash":"`)
		r := f.audit(nil)
		wantFail(t, r, CodeJournalTornTail)
		if r.Chain.Tip != 12 || r.Chain.TornTailBytes == 0 {
			t.Fatalf("chain %+v", r.Chain)
		}
	})
	t.Run("gap", func(t *testing.T) {
		f := newFixture(t)
		f.blocks = append(f.blocks[:7], f.blocks[8:]...)
		f.write()
		wantFail(t, f.audit(nil), CodeJournalGap)
	})
	t.Run("unparseable line", func(t *testing.T) {
		f := newFixture(t)
		f.write()
		f.writeJournal("{not json}\n")
		wantFail(t, f.audit(nil), CodeJournalParse)
	})
	t.Run("missing journal is an input error", func(t *testing.T) {
		f := newFixture(t)
		if _, err := Audit(f.options()); err == nil {
			t.Fatal("want error")
		}
	})
}

func TestCanaryWindow(t *testing.T) {
	t.Run("within budget", func(t *testing.T) {
		f := newFixture(t)
		f.write()
		r := f.audit(func(o *Options) { o.CanaryConfig = f.cfg })
		wantPass(t, r)
		cs := r.Canary
		if cs == nil || cs.FirstHeight == nil || *cs.FirstHeight != 5 || cs.Proofs != 4 || cs.BudgetCell != 1291 || !(cs.Emitted > 0) {
			t.Fatalf("canary %+v", cs)
		}
		if cs.Window == nil || cs.Window.String() != "[5, tip]" || ranges(cs.Active) != "[5, tip]" || cs.RewardTxs != 2 {
			t.Fatalf("canary window %v active %v reward txs %d", cs.Window, cs.Active, cs.RewardTxs)
		}
	})
	t.Run("budget and proof totals exceeded", func(t *testing.T) {
		f := newFixture(t)
		f.cfg = canaryConfig(1, 3)
		f.cfgHash = sha256.Sum256(f.cfg)
		for i := range f.rows {
			f.rows[i].ConfigSHA256 = f.cfgHash
		}
		f.write()
		r := f.audit(func(o *Options) { o.CanaryConfig = f.cfg })
		wantFail(t, r, CodeBudgetExceeded)
		wantFail(t, r, CodeProofsTotal)
	})
	t.Run("recipient not allowlisted", func(t *testing.T) {
		f := newFixture(t)
		f.rows = append(f.rows, legacymining.Record{ProofID: pid(7), MinerAddr: minerB, NodeID: nodeID, AttNonce: pid(107),
			WorkHeight: 9, AcceptTip: 9, AcceptedNS: 2000, ConfigSHA256: f.cfgHash, ProofJSON: []byte(`{}`)})
		f.pay(10, 102, minerB, pid(7))
		f.markPaid(10, pid(7))
		f.write()
		wantFail(t, f.audit(func(o *Options) { o.CanaryConfig = f.cfg }), CodeNotAllowlisted)
	})
	t.Run("invalid config", func(t *testing.T) {
		f := newFixture(t)
		f.write()
		wantFail(t, f.audit(func(o *Options) { o.CanaryConfig = []byte(`{"version":2}`) }), CodeCanaryConfig)
	})
}

// canaryCfg is one canary config of a multi-window fixture.
type canaryCfg struct {
	data []byte
	hash legacymining.ConfigHash
}

func newCanaryCfg(addr string, budget, maxTotal uint64) canaryCfg {
	data := canaryConfigFor(addr, budget, maxTotal)
	return canaryCfg{data: data, hash: sha256.Sum256(data)}
}

// boot records an S11 activation of c at height first (tip+1 at that boot).
func (f *fixture) boot(c canaryCfg, first uint64) {
	f.boots = append(f.boots, legacymining.ConfigWindow{ConfigSHA256: c.hash, FirstHeight: first, ActivatedNS: int64(len(f.boots) + 1)})
}

// accept adds a new unpaid proof of addr accepted under c at tip height-1.
func (f *fixture) accept(height uint64, c canaryCfg, addr string) legacymining.ProofID {
	n := len(f.rows)
	id := legacymining.ProofID(sha256.Sum256([]byte(fmt.Sprintf("proof/%d", n))))
	f.rows = append(f.rows, legacymining.Record{
		ProofID: id, MinerAddr: addr, NodeID: nodeID, AttNonce: sha256.Sum256([]byte(fmt.Sprintf("nonce/%d", n))),
		WorkHeight: height - 1, AcceptTip: height - 1, AcceptedNS: int64(1_000_000 + n), ConfigSHA256: c.hash, ProofJSON: []byte(`{}`),
	})
	return id
}

// settle accepts a new proof of addr under c and pays it, one full block
// reward, in the block at height.
func (f *fixture) settle(height uint64, c canaryCfg, addr string) {
	id := f.accept(height, c, addr)
	f.pay(height, 1000+uint64(len(f.rows)), addr, id)
	f.markPaid(height, id)
}

// rewardSum adds the reward amounts in the blocks [from, to), in block and
// tx order.
func (f *fixture) rewardSum(from, to uint64) (sum float64, n int) {
	for _, b := range f.blocks {
		if b.Height < from || b.Height >= to {
			continue
		}
		for _, tx := range b.Transactions {
			if tx.ContractID == chain.MiningRewardContractID {
				sum += tx.Amount
				n++
			}
		}
	}
	return sum, n
}

func (f *fixture) auditCfg(c canaryCfg) *Report {
	f.t.Helper()
	return f.audit(func(o *Options) { o.CanaryConfig = c.data })
}

func ranges(rs []HeightRange) string {
	s := make([]string, len(rs))
	for i, r := range rs {
		s[i] = r.String()
	}
	return strings.Join(s, ",")
}

// windowsFixture builds three consecutive config windows over blocks 0..30
// with h0 = 5, a canary whose config was changed twice:
//
//	A allowlists minerA, window [5, 12):  rewards to minerA at 6, 8, 10
//	B allowlists minerB, window [12, 20): rewards to minerB at 13, 15, 17, 19
//	C allowlists minerA, window [20, 30]: rewards to minerA at 22, 26
//
// B also restarted at 16 under the same config. Every reward is one full
// block reward (about 3.565 CELL) paying one proof accepted under the
// window's config, and max_proofs_total is the window's proof count.
// budget[i] is the budget_cell of config i.
func windowsFixture(t *testing.T, budget [3]uint64) (*fixture, [3]canaryCfg) {
	f := &fixture{t: t, dir: t.TempDir(), h0: 5}
	cs := [3]canaryCfg{
		newCanaryCfg(minerA, budget[0], 3),
		newCanaryCfg(minerB, budget[1], 4),
		newCanaryCfg(minerA, budget[2], 2),
	}
	f.chain(30, "main")
	f.boot(cs[0], 5)
	f.boot(cs[1], 12)
	f.boot(cs[1], 16)
	f.boot(cs[2], 20)
	for _, hgt := range []uint64{6, 8, 10} {
		f.settle(hgt, cs[0], minerA)
	}
	for _, hgt := range []uint64{13, 15, 17, 19} {
		f.settle(hgt, cs[1], minerB)
	}
	for _, hgt := range []uint64{22, 26} {
		f.settle(hgt, cs[2], minerA)
	}
	return f, cs
}

// TestCanaryConsecutiveWindows checks that the budget and proof totals of a
// config count only its own window, [first_height, next first_height), and
// that the last window runs to the tip.
func TestCanaryConsecutiveWindows(t *testing.T) {
	t.Run("each window within its budget", func(t *testing.T) {
		f, cs := windowsFixture(t, [3]uint64{11, 15, 8})
		f.events = []legacymining.Event{{AtNS: 1, Kind: "freeze", Detail: "not a reconcile event"}}
		f.write()
		for i, want := range []struct {
			window   string
			from, to uint64
			proofs   uint64
		}{
			{"[5, 12)", 5, 12, 3},
			{"[12, 20)", 12, 20, 4},
			{"[20, tip]", 20, 31, 2},
		} {
			r := f.auditCfg(cs[i])
			wantPass(t, r)
			if len(r.Findings) != 0 {
				t.Fatalf("config %d: findings %v", i, r.Findings)
			}
			c := r.Canary
			sum, n := f.rewardSum(want.from, want.to)
			if c.Window.String() != want.window || ranges(c.Active) != want.window || c.RewardTxs != n || c.Emitted != sum || c.Proofs != want.proofs {
				t.Fatalf("config %d: window %v active %v, %d reward txs %v CELL (want %d, %v), %d proofs", i, c.Window, c.Active, c.RewardTxs, c.Emitted, n, sum, c.Proofs)
			}
			// Summed up to the tip, as before, A and B were over budget.
			if all, _ := f.rewardSum(want.from, 31); i < 2 && !(all > float64(c.BudgetCell)) {
				t.Fatalf("config %d: fixture: %v CELL from %d to the tip is within budget %d", i, all, want.from, c.BudgetCell)
			}
		}
	})
	t.Run("an over-budget window still fails", func(t *testing.T) {
		f, cs := windowsFixture(t, [3]uint64{11, 14, 8}) // B emits 4 rewards, about 14.26 CELL
		f.write()
		wantPass(t, f.auditCfg(cs[0]))
		wantPass(t, f.auditCfg(cs[2]))
		r := f.auditCfg(cs[1])
		wantFail(t, r, CodeBudgetExceeded)
		if len(r.Findings) != 1 || !strings.Contains(r.Findings[0].Detail, "[12, 20)") {
			t.Fatalf("findings %v", r.Findings)
		}
	})
	t.Run("an extra reward fails only its window", func(t *testing.T) {
		f, cs := windowsFixture(t, [3]uint64{11, 15, 8})
		f.settle(18, cs[1], minerB) // B: 5 rewards, about 17.82 CELL
		f.write()
		wantPass(t, f.auditCfg(cs[0]))
		wantPass(t, f.auditCfg(cs[2]))
		r := f.auditCfg(cs[1])
		wantFail(t, r, CodeBudgetExceeded)
		wantFail(t, r, CodeProofsTotal) // 5 proofs, max_proofs_total 4
		if r.Canary.RewardTxs != 5 {
			t.Fatalf("canary %+v", r.Canary)
		}
	})
	t.Run("a last window over budget fails", func(t *testing.T) {
		f, cs := windowsFixture(t, [3]uint64{11, 15, 7}) // C emits 2 rewards, about 7.13 CELL
		f.write()
		wantPass(t, f.auditCfg(cs[0]))
		wantPass(t, f.auditCfg(cs[1]))
		wantFail(t, f.auditCfg(cs[2]), CodeBudgetExceeded)
	})
	t.Run("pending proofs count toward their config", func(t *testing.T) {
		f, cs := windowsFixture(t, [3]uint64{11, 15, 8})
		f.accept(20, cs[1], minerB) // accepted under B, unpaid
		f.write()
		wantPass(t, f.auditCfg(cs[0]))
		wantPass(t, f.auditCfg(cs[2]))
		r := f.auditCfg(cs[1])
		wantFail(t, r, CodeProofsTotal)
		if r.Has(CodeBudgetExceeded) {
			t.Fatalf("findings %v", codes(r))
		}
	})
}

// TestCanaryWindowAllowlists checks every reward against the allowlist of
// the config it was sealed under, and only that one.
func TestCanaryWindowAllowlists(t *testing.T) {
	t.Run("each window has its own allowlist", func(t *testing.T) {
		f, cs := windowsFixture(t, [3]uint64{11, 15, 8})
		f.write()
		for i, c := range cs {
			if r := f.auditCfg(c); r.Has(CodeNotAllowlisted) || r.Failed() {
				t.Fatalf("config %d: findings %v", i, r.Findings)
			}
		}
	})
	for _, c := range []struct {
		name   string
		height uint64
		cfg    int
		addr   string
	}{
		{"minerB paid in A's window", 9, 0, minerB},
		{"minerA paid in B's window", 14, 1, minerA},
		{"minerB paid in C's window", 28, 2, minerB},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, cs := windowsFixture(t, [3]uint64{20, 20, 20})
			f.settle(c.height, cs[c.cfg], c.addr)
			f.write()
			for i := range cs {
				r := f.auditCfg(cs[i])
				if i != c.cfg {
					wantPass(t, r)
					continue
				}
				wantFail(t, r, CodeNotAllowlisted)
				var at []uint64
				for _, x := range r.Findings {
					if x.Code == CodeNotAllowlisted && x.Height != nil {
						at = append(at, *x.Height)
					}
				}
				if len(at) != 1 || at[0] != c.height || r.Has(CodeBudgetExceeded) {
					t.Fatalf("findings %v", r.Findings)
				}
			}
		})
	}
}

// TestCanaryReactivatedConfig: a config started again after another one
// keeps its config_windows row, so core's emitted_H (S12) counts from its
// first window through the configs in between. The budget does the same;
// the allowlist applies only where the config was active.
func TestCanaryReactivatedConfig(t *testing.T) {
	build := func(t *testing.T, budgetA uint64) (*fixture, canaryCfg, canaryCfg) {
		f := &fixture{t: t, dir: t.TempDir(), h0: 5}
		a, b := newCanaryCfg(minerA, budgetA, 10), newCanaryCfg(minerB, 100, 10)
		f.chain(30, "main")
		f.boot(a, 5)
		f.boot(b, 12)
		f.boot(a, 20)
		for _, hgt := range []uint64{6, 8, 10} {
			f.settle(hgt, a, minerA)
		}
		for _, hgt := range []uint64{13, 15} {
			f.settle(hgt, b, minerB)
		}
		for _, hgt := range []uint64{22, 26} {
			f.settle(hgt, a, minerA)
		}
		f.write()
		return f, a, b
	}
	t.Run("windows", func(t *testing.T) {
		f, a, b := build(t, 30)
		r := f.auditCfg(a)
		wantPass(t, r)
		sum, n := f.rewardSum(5, 31)
		if c := r.Canary; c.Window.String() != "[5, tip]" || ranges(c.Active) != "[5, 12),[20, tip]" || c.RewardTxs != n || n != 7 || c.Emitted != sum || c.Proofs != 5 {
			t.Fatalf("canary %+v window %v active %v", c, c.Window, c.Active)
		}
		r = f.auditCfg(b)
		wantPass(t, r)
		if c := r.Canary; c.Window.String() != "[12, 20)" || ranges(c.Active) != "[12, 20)" || c.RewardTxs != 2 {
			t.Fatalf("canary %+v window %v active %v", c, c.Window, c.Active)
		}
	})
	t.Run("budget counts the configs in between", func(t *testing.T) {
		f, a, _ := build(t, 20) // A's own 5 rewards are about 17.82 CELL, with B's 2 about 24.95
		wantFail(t, f.auditCfg(a), CodeBudgetExceeded)
	})
}

// TestCanaryRollbackAcrossConfigChange: B was activated at 20, the chain was
// rolled back to 12 and B booted again at 13. The blocks from 13 on were
// sealed under B, so they are not in A's window.
func TestCanaryRollbackAcrossConfigChange(t *testing.T) {
	f := &fixture{t: t, dir: t.TempDir(), h0: 5}
	a, b := newCanaryCfg(minerA, 11, 3), newCanaryCfg(minerB, 11, 3)
	f.chain(30, "main")
	f.boot(a, 5)
	f.boot(b, 20)
	f.boot(b, 13)
	for _, hgt := range []uint64{6, 8, 10} {
		f.settle(hgt, a, minerA)
	}
	for _, hgt := range []uint64{14, 16, 22} {
		f.settle(hgt, b, minerB)
	}
	f.write()
	r := f.auditCfg(a)
	wantPass(t, r)
	if c := r.Canary; c.Window.String() != "[5, 13)" || ranges(c.Active) != "[5, 13)" || c.RewardTxs != 3 {
		t.Fatalf("canary %+v window %v active %v", c, c.Window, c.Active)
	}
	r = f.auditCfg(b)
	wantPass(t, r)
	if c := r.Canary; *c.FirstHeight != 20 || c.Window.String() != "[13, tip]" || ranges(c.Active) != "[13, tip]" || c.RewardTxs != 3 {
		t.Fatalf("canary %+v window %v active %v", c, c.Window, c.Active)
	}
}

// TestCanaryHistoryFallback: reconcile events that do not match
// config_windows are reported, and the windows then follow config_windows.
func TestCanaryHistoryFallback(t *testing.T) {
	unknown := sha256.Sum256([]byte("unknown config"))
	for _, c := range []struct{ name, detail string }{
		{"undecodable detail", `{"config_sha256":`},
		{"incomplete detail", `{"paid":0}`},
		{"config without a window", fmt.Sprintf(`{"paid":0,"updated":0,"reset":0,"window_inserted":false,"config_sha256":%q,"first_height":25}`, hex.EncodeToString(unknown[:]))},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, cs := windowsFixture(t, [3]uint64{11, 15, 8})
			f.events = []legacymining.Event{{AtNS: 1, Kind: reconcileEventKind, Detail: c.detail}}
			f.write()
			for i, want := range []string{"[5, 12)", "[12, 20)", "[20, tip]"} {
				r := f.auditCfg(cs[i])
				wantPass(t, r)
				if !r.Has(CodeCanaryHistory) || r.Canary.Window.String() != want {
					t.Fatalf("config %d: window %v findings %v", i, r.Canary.Window, codes(r))
				}
			}
		})
	}
}

func TestSegments(t *testing.T) {
	var a, b, c legacymining.ConfigHash
	a[0], b[0], c[0] = 1, 2, 3
	for _, tc := range []struct {
		name string
		acts []activation
		want []segment
	}{
		{"none", nil, nil},
		{"consecutive, with restarts", []activation{{a, 5}, {a, 9}, {b, 12}, {b, 15}, {c, 20}},
			[]segment{{a, 5, 12}, {b, 12, 20}, {c, 20, openEnd}}},
		{"re-activation", []activation{{a, 5}, {b, 12}, {a, 20}},
			[]segment{{a, 5, 12}, {b, 12, 20}, {a, 20, openEnd}}},
		{"rollback across a config change", []activation{{a, 5}, {b, 20}, {b, 13}},
			[]segment{{a, 5, 13}, {b, 13, openEnd}}},
		{"rollback into the previous config", []activation{{a, 5}, {b, 20}, {a, 15}},
			[]segment{{a, 5, openEnd}}},
		{"replaced before sealing", []activation{{a, 5}, {b, 5}},
			[]segment{{b, 5, openEnd}}},
		{"rollback below every window", []activation{{a, 5}, {b, 12}, {c, 3}},
			[]segment{{c, 3, openEnd}}},
	} {
		if got := segments(tc.acts); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestCanaryRehearsalFalseAlarm replays the tail of the crash-rollback
// rehearsal (evidence crash-rollback-triggers/final/window-budget.json).
// Config 16774144... (budget_cell 40) was active for [666956, 666989) and
// paid 10 rewards, 35.6490987 CELL; config 9a95b0ae... (budget_cell 1000,
// max_proofs_total 30) took over at 666989 and paid 7 more, 24.95436909
// CELL, up to the tip 667018. hl-audit summed 16774144's rewards from 666956
// to the tip, 60.60346779 CELL, and failed it with budget-exceeded.
func TestCanaryRehearsalFalseAlarm(t *testing.T) {
	const tip = 667018
	f := &fixture{t: t, dir: t.TempDir(), origin: 666770, h0: 666783}
	f.chain(tip, "rehearsal")
	prev := newCanaryCfg(minerA, 1291, 3000) // f3610b23...: [666783, 666956)
	c40 := newCanaryCfg(minerA, 40, 3000)    // 16774144...: [666956, 666989)
	last := newCanaryCfg(minerA, 1000, 30)   // 9a95b0ae...: [666989, tip]
	f.boot(prev, 666783)
	f.boot(c40, 666956)
	f.boot(last, 666989)
	for _, hgt := range []uint64{666800, 666850, 666900} {
		f.settle(hgt, prev, minerA)
	}
	for i := uint64(0); i < 10; i++ {
		f.settle(666957+3*i, c40, minerA)
	}
	for _, hgt := range []uint64{666990, 666993, 666996, 666999, 667002, 667005, 667007} {
		f.settle(hgt, last, minerA)
	}
	f.write()

	near := func(x, y float64) bool { return math.Abs(x-y) < 1e-9 }
	if all, n := f.rewardSum(666956, tip+1); n != 17 || !near(all, 60.60346779) {
		t.Fatalf("fixture: %d rewards, %v CELL from 666956 to the tip", n, all)
	}
	for _, want := range []struct {
		cfg     canaryCfg
		window  string
		txs     int
		emitted float64
	}{
		{c40, "[666956, 666989)", 10, 35.6490987},
		{last, "[666989, tip]", 7, 24.95436909},
		{prev, "[666783, 666956)", 3, 3 * legacymining.DefaultRewardCell(666800)},
	} {
		r := f.auditCfg(want.cfg)
		wantPass(t, r)
		if c := r.Canary; c.Window.String() != want.window || c.RewardTxs != want.txs || !near(c.Emitted, want.emitted) || c.Proofs != uint64(want.txs) {
			t.Fatalf("canary %+v window %v, want %s %d txs %v CELL", c, c.Window, want.window, want.txs, want.emitted)
		}
	}

	cfgPath := filepath.Join(t.TempDir(), "mining-canary.json")
	if err := os.WriteFile(cfgPath, c40.data, 0o600); err != nil {
		t.Fatal(err)
	}
	var so, se bytes.Buffer
	if code := run([]string{"-state-dir", f.dir, "-canary-config", cfgPath, "-temp-dir", t.TempDir()}, &so, &se); code != exitPass ||
		!strings.Contains(so.String(), "window [666956, 666989), emitted 35.649") {
		t.Fatalf("exit %d\n%s%s", code, so.String(), se.String())
	}
}

// TestCanaryConfigExamples parses the committed canary config examples
// (hl1/canary) with the production strict decoder and validator.
func TestCanaryConfigExamples(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "hl1", "canary")
	for _, c := range []struct {
		name          string
		budget, total uint64
	}{
		{"mining-canary.phase1.example.json", 1291, 3000},
		{"mining-canary.phase2.example.json", 30808, 72000},
	} {
		data, err := os.ReadFile(filepath.Join(dir, c.name))
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := legacymining.ParseConfig(data, sha256.Sum256(data))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if cfg.BudgetCell != c.budget || cfg.MaxProofsTotal != c.total || cfg.MaxProofsPerMin != 60 || cfg.MaxPending != 600 || len(cfg.Allowed) != 1 {
			t.Fatalf("%s: %+v", c.name, cfg)
		}
	}
}

// TestInputsUnchanged checks that the audit never writes to the copy it reads.
func TestInputsUnchanged(t *testing.T) {
	f := newFixture(t)
	f.write()
	before := snapshot(t, f.dir)
	wantPass(t, f.audit(nil))
	if after := snapshot(t, f.dir); after != before {
		t.Fatalf("state directory changed:\nbefore %s\nafter  %s", before, after)
	}
}

// TestDBWithUncheckpointedWAL audits a copy taken while the store was open:
// committed rows that live only in -wal must be seen.
func TestDBWithUncheckpointedWAL(t *testing.T) {
	f := newFixture(t)
	f.write()
	st := legacymining.NewSQLiteStore()
	if err := st.Open(f.dbPath(), nil); err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Accept(legacymining.Record{ProofID: pid(8), MinerAddr: minerA, NodeID: nodeID, AttNonce: pid(108),
		WorkHeight: 12, AcceptTip: 12, AcceptedNS: 3000, ConfigSHA256: f.cfgHash, ProofJSON: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(f.dbPath() + "-wal"); err != nil || fi.Size() == 0 {
		t.Fatalf("expected a non-empty -wal: %v", err)
	}
	r := f.audit(nil)
	wantPass(t, r)
	if r.DB.Rows != 5 || r.DB.Unpaid != 2 {
		t.Fatalf("db summary %+v", r.DB)
	}
}

func snapshot(t *testing.T, root string) string {
	var b strings.Builder
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			fmt.Fprintf(&b, "%s/;", rel)
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s=%x;", rel, sha256.Sum256(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestRunCLI(t *testing.T) {
	f := newFixture(t)
	f.write()
	out := filepath.Join(t.TempDir(), "audit-1.json")
	var so, se bytes.Buffer
	if code := run([]string{"-state-dir", f.dir, "-out", out, "-temp-dir", t.TempDir()}, &so, &se); code != exitPass {
		t.Fatalf("exit %d\n%s%s", code, so.String(), se.String())
	}
	if !strings.HasPrefix(so.String(), "hl-audit: PASS\n") {
		t.Fatalf("stdout %q", so.String())
	}
	so.Reset()
	se.Reset()
	if code := run([]string{"-state-dir", f.dir, "-out", out}, &so, &se); code != exitUsage || !strings.Contains(se.String(), "report") {
		t.Fatalf("overwrite: exit %d stderr %q", code, se.String())
	}

	// Double pay plus W regression, chained on the first report.
	f.pay(10, 102, minerA, pid(3))
	f.chain(10, "main")
	f.writeJournal("")
	f.writeWatermark(legacymining.WatermarkFile, 10, f.blocks[10].Hash, legacymining.WatermarkSourceSeed)
	so.Reset()
	se.Reset()
	code := run([]string{"-state-dir", f.dir, "-prev", out, "-json", "-temp-dir", t.TempDir()}, &so, &se)
	if code != exitFail {
		t.Fatalf("exit %d\n%s%s", code, so.String(), se.String())
	}
	var rep Report
	if err := json.Unmarshal(so.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, x := range rep.Findings {
		got[x.Code] = true
	}
	for _, c := range []string{CodeDoublePay, CodeWatermarkRegress, CodeServedHeightLost} {
		if !got[c] {
			t.Fatalf("missing %s in %v", c, rep.Findings)
		}
	}

	for _, args := range [][]string{{}, {"-state-dir", f.dir, "extra"}, {"-state-dir", f.dir, "-prev", filepath.Join(f.dir, "missing.json")}} {
		so.Reset()
		se.Reset()
		if code := run(args, &so, &se); code != exitUsage {
			t.Fatalf("%q: exit %d", args, code)
		}
	}
}
