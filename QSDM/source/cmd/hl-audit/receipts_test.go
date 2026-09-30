package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

func (f *fixture) receiptsPath() string { return filepath.Join(f.dir, ReceiptsFile) }

// writeReceipts writes one receipts line per tx of every block (as H5 does),
// except those skip rejects, with hashOf's block hash, then tail.
func (f *fixture) writeReceipts(skip func(height uint64, txID string) bool, hashOf func(*chain.Block) string, tail string) {
	f.t.Helper()
	var buf bytes.Buffer
	for _, b := range f.blocks {
		for i, tx := range b.Transactions {
			if skip != nil && skip(b.Height, tx.ID) {
				continue
			}
			hash := b.Hash
			if hashOf != nil {
				hash = hashOf(b)
			}
			js, err := json.Marshal(chain.TxReceipt{TxID: tx.ID, BlockHeight: b.Height, BlockHash: hash, Status: chain.ReceiptSuccess, IndexInBlock: i})
			if err != nil {
				f.t.Fatal(err)
			}
			buf.Write(append(js, '\n'))
		}
	}
	buf.WriteString(tail)
	if err := os.WriteFile(f.receiptsPath(), buf.Bytes(), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func withReceipts(f *fixture, mut func(*Options)) func(*Options) {
	return func(o *Options) {
		o.ReceiptsPath = f.receiptsPath()
		if mut != nil {
			mut(o)
		}
	}
}

func TestReceiptsCoverage(t *testing.T) {
	const txsFromH0 = 10 // heartbeats at 5..12 and the rewards at 6 and 8
	t.Run("complete from h0", func(t *testing.T) {
		f := newFixture(t)
		f.write()
		f.writeReceipts(func(h uint64, _ string) bool { return h < 5 }, nil, "")
		r := f.audit(withReceipts(f, nil))
		wantPass(t, r)
		if len(r.Findings) != 0 {
			t.Fatalf("findings %v", r.Findings)
		}
		if s := r.Receipts; s == nil || s.FromHeight != 5 || s.ChainTxs != txsFromH0 || s.Covered != txsFromH0 || s.Missing != 0 || s.Lines != txsFromH0 {
			t.Fatalf("receipts summary %+v", r.Receipts)
		}
	})
	t.Run("a block without receipts fails at its height", func(t *testing.T) {
		f := newFixture(t)
		f.write()
		f.writeReceipts(func(h uint64, _ string) bool { return h == 8 }, nil, "")
		r := f.audit(withReceipts(f, nil))
		wantFail(t, r, CodeReceiptMissing)
		if len(r.Findings) != 1 || *r.Findings[0].Height != 8 || !strings.Contains(r.Findings[0].Detail, "2 of the block's 2 txs") {
			t.Fatalf("findings %v", r.Findings)
		}
		if r.Receipts.Missing != 2 || r.Receipts.Covered != txsFromH0-2 {
			t.Fatalf("receipts summary %+v", r.Receipts)
		}
	})
	t.Run("a receipt with another block hash does not cover", func(t *testing.T) {
		f := newFixture(t)
		f.write()
		f.writeReceipts(nil, func(b *chain.Block) string {
			if b.Height == 11 {
				return blockHash("fork", 11)
			}
			return b.Hash
		}, "")
		wantFail(t, f.audit(withReceipts(f, nil)), CodeReceiptMissing)
	})
	t.Run("below the bound is not checked; -receipts-from lowers it", func(t *testing.T) {
		f := newFixture(t)
		f.write()
		f.writeReceipts(func(h uint64, _ string) bool { return h == 3 }, nil, "")
		wantPass(t, f.audit(withReceipts(f, nil)))
		from := uint64(0)
		r := f.audit(withReceipts(f, func(o *Options) { o.ReceiptsFrom = &from }))
		wantFail(t, r, CodeReceiptMissing)
		if *r.Findings[0].Height != 3 {
			t.Fatalf("findings %v", r.Findings)
		}
	})
	t.Run("no bound without a DB: skipped with a warning", func(t *testing.T) {
		f := newFixture(t)
		f.write()
		r := f.audit(withReceipts(f, func(o *Options) { o.DBPath = "" }))
		if !r.Has(CodeReceiptsUnbounded) || r.Has(CodeReceiptMissing) || r.Receipts != nil {
			t.Fatalf("findings %v", codes(r))
		}
	})
	t.Run("absent receipts: warning, or failure when required", func(t *testing.T) {
		f := newFixture(t)
		f.write()
		r := f.audit(withReceipts(f, nil))
		wantPass(t, r)
		if !r.Has(CodeReceiptsAbsent) {
			t.Fatalf("findings %v", codes(r))
		}
		wantFail(t, f.audit(withReceipts(f, func(o *Options) { o.RequireReceipts = true })), CodeReceiptsAbsent)
	})
	t.Run("torn tail and bad lines fail", func(t *testing.T) {
		f := newFixture(t)
		f.write()
		f.writeReceipts(nil, nil, `{"tx_id":"x","block_hei`)
		wantFail(t, f.audit(withReceipts(f, nil)), CodeReceiptsTornTail)
		f.writeReceipts(nil, nil, "not json\n")
		wantFail(t, f.audit(withReceipts(f, nil)), CodeReceiptsParse)
	})
	t.Run("off without a receipts path", func(t *testing.T) {
		f := newFixture(t)
		f.write()
		r := f.audit(nil)
		wantPass(t, r)
		if r.Receipts != nil || len(r.Findings) != 0 {
			t.Fatalf("receipts %+v findings %v", r.Receipts, r.Findings)
		}
	})
}

func TestReceiptsCLI(t *testing.T) {
	f := newFixture(t)
	f.write()
	var so, se bytes.Buffer
	if code := run([]string{"-state-dir", f.dir, "-require-receipts", "-temp-dir", t.TempDir()}, &so, &se); code != exitFail || !strings.Contains(so.String(), CodeReceiptsAbsent) {
		t.Fatalf("absent receipts: exit %d\n%s%s", code, so.String(), se.String())
	}
	f.writeReceipts(func(h uint64, _ string) bool { return h == 2 }, nil, "")
	so.Reset()
	se.Reset()
	if code := run([]string{"-state-dir", f.dir, "-require-receipts", "-temp-dir", t.TempDir()}, &so, &se); code != exitPass || !strings.Contains(so.String(), "receipts: 10 of 10 chain txs from height 5 covered") {
		t.Fatalf("default bound: exit %d\n%s%s", code, so.String(), se.String())
	}
	so.Reset()
	if code := run([]string{"-state-dir", f.dir, "-receipts-from", "0", "-temp-dir", t.TempDir()}, &so, &se); code != exitFail || !strings.Contains(so.String(), CodeReceiptMissing) {
		t.Fatalf("-receipts-from 0: exit %d\n%s", code, so.String())
	}
	so.Reset()
	if code := run([]string{"-state-dir", f.dir, "-receipts", "none", "-receipts-from", "0", "-temp-dir", t.TempDir()}, &so, &se); code != exitPass || strings.Contains(so.String(), "receipts:") {
		t.Fatalf("-receipts none: exit %d\n%s", code, so.String())
	}
	if code := run([]string{"-state-dir", f.dir, "-receipts-from", "x"}, &so, &se); code != exitUsage {
		t.Fatalf("bad -receipts-from: exit %d", code)
	}
}
