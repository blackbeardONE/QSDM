package chain

// Tests for the parallel receipts load (LoadNDJSON on several workers): the
// store ends exactly as the line-by-line load left it, and a failure is the
// first one in file order, with the same error and every receipt before it
// stored, however the workers are scheduled.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// loadNDJSONLineByLine is LoadNDJSON as it was before it decoded on several
// workers: the reference the parallel load must match.
func loadNDJSONLineByLine(rs *ReceiptStore, path string) (int, error) {
	if path == "" {
		return 0, errors.New("chain.ReceiptStore.LoadNDJSON: empty path")
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("chain.ReceiptStore.LoadNDJSON: open %s: %w", path, err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	loaded := 0
	lineno := 0
	for scanner.Scan() {
		lineno++
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		r := &TxReceipt{}
		if err := json.Unmarshal(raw, r); err != nil {
			return loaded, fmt.Errorf("chain.ReceiptStore.LoadNDJSON: parse line %d of %s: %w (loaded %d before failure; trim the bad line to recover)",
				lineno, path, err, loaded)
		}
		if r.TxID == "" {
			continue
		}
		rs.Store(r)
		loaded++
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return loaded, fmt.Errorf("chain.ReceiptStore.LoadNDJSON: scan %s: %w", path, err)
	}
	return loaded, nil
}

// receiptStoreState renders everything rs holds: the insertion order with
// each receipt, the receipts of each block and of each contract in their
// slice order, and whether each indexed receipt is the one byTxID holds.
func receiptStoreState(t *testing.T, rs *ReceiptStore) string {
	t.Helper()
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	var b strings.Builder
	fmt.Fprintf(&b, "byTxID %d, order %d\n", len(rs.byTxID), len(rs.order))
	for _, id := range rs.order {
		fmt.Fprintf(&b, "order %q %s\n", id, mustMarshal(t, rs.byTxID[id]))
	}
	heights := make([]uint64, 0, len(rs.byBlock))
	for h := range rs.byBlock {
		heights = append(heights, h)
	}
	sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })
	for _, h := range heights {
		for _, r := range rs.byBlock[h] {
			fmt.Fprintf(&b, "block %d %q indexed=%v\n", h, r.TxID, rs.byTxID[r.TxID] == r)
		}
	}
	contracts := make([]string, 0, len(rs.byContract))
	for c := range rs.byContract {
		contracts = append(contracts, c)
	}
	sort.Strings(contracts)
	for _, c := range contracts {
		for _, r := range rs.byContract[c] {
			fmt.Fprintf(&b, "contract %q %q indexed=%v\n", c, r.TxID, rs.byTxID[r.TxID] == r)
		}
	}
	return b.String()
}

// receiptLineJSON is one receipt as AppendBlockNDJSON writes it.
func receiptLineJSON(t *testing.T, id string, height uint64, contract string, index int) string {
	t.Helper()
	r := &TxReceipt{TxID: id, BlockHeight: height, BlockHash: fmt.Sprintf("%064x", height), Status: ReceiptSuccess,
		GasUsed: int64(index), Fee: 0.25, Timestamp: time.Unix(1700000000+int64(height), 0).UTC(), ContractID: contract, IndexInBlock: index}
	data := map[string]interface{}{"sender": "alice", "recipient": "bob", "amount": 1.5}
	if contract != "" {
		data["contract_id"] = contract
	}
	r.Logs = []LogEntry{{Topic: "TxApplied", Data: data, Index: 0}}
	return string(mustMarshal(t, r))
}

// receiptsFile writes lines, each followed by "\n", and returns its path.
func receiptsFile(t *testing.T, lines []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "receipts.ndjson")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// goodReceiptLines are 60 receipts over 20 blocks, a third of them with a
// contract, plus re-stored transactions, blank lines, CRLF line ends and
// rows without a transaction ID.
func goodReceiptLines(t *testing.T) []string {
	t.Helper()
	var lines []string
	for i := 0; i < 60; i++ {
		contract := ""
		if i%3 == 0 {
			contract = fmt.Sprintf("qsdm/contract-%d", i%2)
		}
		lines = append(lines, receiptLineJSON(t, fmt.Sprintf("tx-%02d", i), uint64(i/3), contract, i%3))
		switch i {
		case 10:
			lines = append(lines, "")
		case 20:
			lines = append(lines, receiptLineJSON(t, "tx-05", 30, "qsdm/contract-9", 0)) // re-stored at another height
		case 30:
			lines = append(lines, "null", "{}", `{"block_height":7}`)
		case 40:
			lines = append(lines, receiptLineJSON(t, "tx-41", 13, "", 2)+"\r") // a CRLF line end
		case 50:
			lines = append(lines, receiptLineJSON(t, "tx-02", 0, "", 2)) // re-stored unchanged
		}
	}
	return lines
}

// requireLoadsAsLineByLine loads path line by line, and in runs of several
// sizes (one line per run, a few lines, the default) on each number of
// workers, into stores that already hold pre, and requires the same count,
// the same error and the same store.
func requireLoadsAsLineByLine(t *testing.T, name, path string, pre []*TxReceipt) {
	t.Helper()
	newStore := func() *ReceiptStore {
		rs := NewReceiptStore()
		for _, r := range pre {
			cp := *r
			rs.Store(&cp)
		}
		return rs
	}
	ref := newStore()
	wantN, wantErr := loadNDJSONLineByLine(ref, path)
	want := receiptStoreState(t, ref)
	for _, runBytes := range []int{1, 1500, receiptRunBytes} {
		for _, workers := range []int{1, 2, 3, 16} {
			for round := 0; round < 3; round++ {
				rs := newStore()
				n, err := rs.loadNDJSON(path, workers, runBytes)
				if n != wantN || (err == nil) != (wantErr == nil) || (err != nil && err.Error() != wantErr.Error()) {
					t.Fatalf("%s, runs of %d bytes, %d workers: (%d, %v), line by line (%d, %v)", name, runBytes, workers, n, err, wantN, wantErr)
				}
				if wantErr != nil && errors.Unwrap(err) == nil {
					t.Fatalf("%s, runs of %d bytes, %d workers: the error does not wrap its cause: %v", name, runBytes, workers, err)
				}
				if got := receiptStoreState(t, rs); got != want {
					t.Fatalf("%s, runs of %d bytes, %d workers: the store differs from the line-by-line load\n got: %s\nwant: %s", name, runBytes, workers, got, want)
				}
			}
		}
	}
	rs := newStore()
	n, err := rs.LoadNDJSON(path)
	if n != wantN || (err == nil) != (wantErr == nil) || receiptStoreState(t, rs) != want {
		t.Fatalf("%s: LoadNDJSON (%d, %v), line by line (%d, %v)", name, n, err, wantN, wantErr)
	}
}

// TestLoadNDJSONWorkerCountsAgree: on any number of workers the load stores
// the same receipts in the same order as the line-by-line load, re-stored
// transactions, blank lines, CRLF line ends and rows without a transaction
// ID included, also into a store that already holds receipts.
func TestLoadNDJSONWorkerCountsAgree(t *testing.T) {
	lines := goodReceiptLines(t)
	path := receiptsFile(t, lines)
	requireLoadsAsLineByLine(t, "good file", path, nil)
	requireLoadsAsLineByLine(t, "into a store that holds receipts", path, []*TxReceipt{
		makeReceipt("tx-07", 99, ReceiptFailed), makeReceipt("other", 3, ReceiptSuccess),
	})
	requireLoadsAsLineByLine(t, "empty file", receiptsFile(t, nil), nil)
	// More than one default run: about 2,000 receipts, 0.9 MB.
	var many []string
	for i := 0; i < 2000; i++ {
		many = append(many, receiptLineJSON(t, fmt.Sprintf("tx-many-%04d", i), uint64(i/4), "", i%4))
		if i%700 == 3 {
			many = append(many, receiptLineJSON(t, fmt.Sprintf("tx-many-%04d", i/2), uint64(i), "qsdm/re-stored", 0))
		}
	}
	requireLoadsAsLineByLine(t, "several default runs", receiptsFile(t, many), nil)
	requireLoadsAsLineByLine(t, "several default runs, a bad line in the third",
		receiptsFile(t, append(append(append([]string(nil), many[:1500]...), "{"), many[1500:]...)), nil)
	if n, err := NewReceiptStore().loadNDJSON(filepath.Join(t.TempDir(), "absent.ndjson"), 4, receiptRunBytes); n != 0 || err != nil {
		t.Fatalf("a missing file: (%d, %v), want (0, nil)", n, err)
	}
	if _, err := NewReceiptStore().loadNDJSON("", 4, receiptRunBytes); err == nil {
		t.Fatal("an empty path loaded")
	}
}

// TestLoadNDJSONReportsTheFirstFailure: lines are decoded out of order on
// several workers, and the result is still the line-by-line load's: the
// first failure in file order, whichever kind it is, with the receipts
// before it stored and none after it.
func TestLoadNDJSONReportsTheFirstFailure(t *testing.T) {
	good := goodReceiptLines(t)
	insert := func(at int, extra ...string) []string {
		out := append([]string(nil), good[:at]...)
		out = append(out, extra...)
		return append(out, good[at:]...)
	}
	long := `{"tx_id":"too-long","error":"` + strings.Repeat("x", 4*1024*1024) + `"}`
	for _, tc := range []struct {
		name  string
		lines []string
	}{
		{"a bad first line", insert(0, "{not json")},
		{"a bad line in the middle", insert(31, `{"tx_id":`)},
		{"a bad last line", append(append([]string(nil), good...), `{"tx_id":"x"`)},
		{"two bad lines", insert(12, "garbage", "")},
		{"a type error", insert(25, `{"tx_id":5}`)},
		{"a type error before a syntax error", insert(40, `{"tx_id":"t","block_height":"high"}`, "{")},
		{"a blank-padded line", insert(5, "   ")},
		{"a line too long", insert(33, long)},
		{"a bad line before a line too long", insert(20, "{", long)},
		{"a line too long before a bad line", insert(20, long, "{")},
	} {
		requireLoadsAsLineByLine(t, tc.name, receiptsFile(t, tc.lines), nil)
	}
}
