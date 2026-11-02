package main

// Receipts coverage (HL1 errata E7): every chain tx at or above a lower bound
// has a receipts line at its height with its block hash. Before E7 a crash
// after H3 or H4, or an I/O error at H5, let a boot advance W over a block
// whose receipts were never written; the rehearsal compared journals only and
// did not see it. Receipts are not consensus state, but wallets and explorers
// read them.
//
// The lower bound is -receipts-from, or meta.h0 when a DB was audited. Without
// either the check is skipped with a warning. A receipts file that is absent
// is a warning unless -require-receipts.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"sort"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

// ReceiptsFile is the receipts NDJSON name in the state directory.
const ReceiptsFile = "qsdm_receipts.ndjson"

// Receipts finding codes.
const (
	CodeReceiptsAbsent    = "receipts-absent"    // warn unless -require-receipts
	CodeReceiptsUnbounded = "receipts-unbounded" // warn: no -receipts-from and no DB
	CodeReceiptsParse     = "receipts-parse"     // a complete receipts line is not a receipt
	CodeReceiptsTornTail  = "receipts-torn-tail" // bytes after the last "\n" (R-C3)
	CodeReceiptMissing    = "receipt-missing"    // a chain tx without a receipt (E7)
)

// ReceiptSummary describes the receipts coverage check.
type ReceiptSummary struct {
	FromHeight uint64 `json:"from_height"`
	Lines      int    `json:"lines"`
	ChainTxs   int    `json:"chain_txs"`
	Covered    int    `json:"covered"`
	Missing    int    `json:"missing"`
}

// receiptKey is one chain tx: its height and ID.
type receiptKey struct {
	height uint64
	txID   string
}

// receiptsFrom decides the lower bound of the coverage check once the DB has
// been read; math.MaxUint64 means the check is off.
func (a *auditor) receiptsFrom() {
	a.rcptFrom = math.MaxUint64
	if a.o.ReceiptsPath == "" {
		return
	}
	switch {
	case a.o.ReceiptsFrom != nil:
		a.rcptFrom = *a.o.ReceiptsFrom
	case a.db != nil:
		a.rcptFrom = a.h0
	default:
		a.rep.add(CodeReceiptsUnbounded, sevWarn, nil, "receipts coverage not checked: no -receipts-from and no legacy-mining.db (meta.h0)")
		return
	}
	a.chainTxs = make(map[receiptKey]string)
}

// noteTxs records the txs of a block the coverage check covers.
func (a *auditor) noteTxs(b *chain.Block) {
	if a.chainTxs == nil || b.Height < a.rcptFrom {
		return
	}
	for _, tx := range b.Transactions {
		if tx != nil {
			a.chainTxs[receiptKey{b.Height, tx.ID}] = b.Hash
		}
	}
}

// checkReceipts reads the receipts NDJSON and reports every chain tx at or
// above the bound without a line at its height and block hash.
func (a *auditor) checkReceipts() error {
	if a.chainTxs == nil {
		return nil
	}
	s := &ReceiptSummary{FromHeight: a.rcptFrom, ChainTxs: len(a.chainTxs)}
	a.rep.Receipts = s
	f, err := os.Open(a.o.ReceiptsPath)
	if errors.Is(err, fs.ErrNotExist) {
		sev := sevWarn
		if a.o.RequireReceipts {
			sev = sevFail
		}
		a.rep.add(CodeReceiptsAbsent, sev, nil, "%s is absent; %d chain txs at or above %d are not covered", a.o.ReceiptsPath, len(a.chainTxs), a.rcptFrom)
		s.Missing = len(a.chainTxs)
		return nil
	}
	if err != nil {
		return fmt.Errorf("receipts: %w", err)
	}
	defer f.Close()
	covered := make(map[receiptKey]bool, len(a.chainTxs))
	r := bufio.NewReaderSize(f, 1<<20)
	lineNo := 0
	for {
		line, err := r.ReadBytes('\n')
		if err == io.EOF {
			if len(line) > 0 {
				a.rep.add(CodeReceiptsTornTail, sevFail, nil, "%d bytes after the last newline of %s (R-C3)", len(line), a.o.ReceiptsPath)
			}
			break
		}
		if err != nil {
			return fmt.Errorf("receipts: read: %w", err)
		}
		lineNo++
		line = line[:len(line)-1]
		if len(line) == 0 {
			continue
		}
		s.Lines++
		var rc struct {
			TxID        string `json:"tx_id"`
			BlockHeight uint64 `json:"block_height"`
			BlockHash   string `json:"block_hash"`
		}
		if err := json.Unmarshal(line, &rc); err != nil {
			a.rep.add(CodeReceiptsParse, sevFail, nil, "receipts line %d: %v", lineNo, err)
			continue
		}
		k := receiptKey{rc.BlockHeight, rc.TxID}
		if hash, ok := a.chainTxs[k]; ok && hash == rc.BlockHash {
			covered[k] = true
		}
	}
	s.Covered = len(covered)
	s.Missing = len(a.chainTxs) - len(covered)
	if s.Missing == 0 {
		return nil
	}
	type gap struct {
		missing, txs int
		first        string
	}
	byHeight := map[uint64]*gap{}
	for k := range a.chainTxs {
		g := byHeight[k.height]
		if g == nil {
			g = &gap{}
			byHeight[k.height] = g
		}
		g.txs++
		if !covered[k] {
			g.missing++
			if g.first == "" || k.txID < g.first {
				g.first = k.txID
			}
		}
	}
	heights := make([]uint64, 0, len(byHeight))
	for hgt, g := range byHeight {
		if g.missing > 0 {
			heights = append(heights, hgt)
		}
	}
	sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })
	for _, hgt := range heights {
		g := byHeight[hgt]
		a.rep.add(CodeReceiptMissing, sevFail, h(hgt), "%d of the block's %d txs have no receipt (first %s)", g.missing, g.txs, g.first)
	}
	return nil
}
