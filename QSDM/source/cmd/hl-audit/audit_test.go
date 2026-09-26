package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	t        *testing.T
	dir      string
	blocks   []*chain.Block
	h0       uint64
	window   uint64
	cfg      []byte
	cfgHash  legacymining.ConfigHash
	rows     []legacymining.Record
	payments []legacymining.Payment
	noDB     bool
}

func canaryConfig(budget, maxTotal uint64) []byte {
	return []byte(fmt.Sprintf(`{"version":1,"allowed":[{"miner_addr":%q,"node_id":%q}],"max_proofs_per_min":60,"max_proofs_total":%d,"max_pending":600,"budget_cell":%d,"expires_unix":1790000000}`+"\n",
		minerA, nodeID, maxTotal, budget))
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

// chain replaces the blocks with heights 0..tip, keeping the transactions
// already placed at heights that remain.
func (f *fixture) chain(tip uint64, salt string) {
	old := f.blocks
	f.blocks = nil
	prev := ""
	for hgt := uint64(0); hgt <= tip; hgt++ {
		b := &chain.Block{Height: hgt, PrevHash: prev, Hash: blockHash(salt, hgt), Transactions: []*mempool.Tx{heartbeat(hgt)}}
		if hgt < uint64(len(old)) {
			b.Transactions = old[hgt].Transactions
		}
		f.blocks = append(f.blocks, b)
		prev = b.Hash
	}
}

// rehash gives every block from height `from` a new hash (a replaced branch).
func (f *fixture) rehash(from uint64, salt string) {
	for i := from; i < uint64(len(f.blocks)); i++ {
		f.blocks[i].Hash = blockHash(salt, i)
		if i > 0 {
			f.blocks[i].PrevHash = f.blocks[i-1].Hash
		}
	}
}

func (f *fixture) pay(height, nonce uint64, addr string, ids ...legacymining.ProofID) *mempool.Tx {
	tx := rewardTx(nonce, addr, legacymining.DefaultRewardCell(height), ids...)
	f.blocks[height].Transactions = append(f.blocks[height].Transactions, tx)
	return tx
}

// markPaid records the DB side of the payment in block height.
func (f *fixture) markPaid(height uint64, ids ...legacymining.ProofID) {
	var tx *mempool.Tx
	for _, c := range f.blocks[height].Transactions {
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
	if _, err := st.ApplyReconcile(legacymining.Reconciliation{Window: legacymining.ConfigWindow{ConfigSHA256: f.cfgHash, FirstHeight: f.window, ActivatedNS: 1}}); err != nil {
		f.t.Fatal(err)
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
