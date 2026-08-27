package chain

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// applyReceiptContractFromTx sets receipt.ContractID and adds contract_id to logData when tx carries a contract hint.
func applyReceiptContractFromTx(receipt *TxReceipt, tx *mempool.Tx, logData map[string]interface{}) {
	if receipt == nil || tx == nil || tx.ContractID == "" {
		return
	}
	receipt.ContractID = tx.ContractID
	if logData != nil {
		logData["contract_id"] = tx.ContractID
	}
}

// ReceiptStatus indicates whether a transaction succeeded.
type ReceiptStatus uint8

const (
	ReceiptSuccess ReceiptStatus = 1
	ReceiptFailed  ReceiptStatus = 0
)

// LogEntry is a structured log emitted during transaction execution.
type LogEntry struct {
	Topic string                 `json:"topic"`
	Data  map[string]interface{} `json:"data,omitempty"`
	Index int                    `json:"index"`
}

// TxReceipt records the outcome of an executed transaction.
type TxReceipt struct {
	TxID            string        `json:"tx_id"`
	BlockHeight     uint64        `json:"block_height"`
	BlockHash       string        `json:"block_hash"`
	Status          ReceiptStatus `json:"status"`
	GasUsed         int64         `json:"gas_used"`
	Fee             float64       `json:"fee"`
	Logs            []LogEntry    `json:"logs,omitempty"`
	Error           string        `json:"error,omitempty"`
	Timestamp       time.Time     `json:"timestamp"`
	ContractID      string        `json:"contract_id,omitempty"`
	IndexInBlock    int           `json:"index_in_block"`
}

// ReceiptStore persists and indexes transaction receipts.
type ReceiptStore struct {
	mu         sync.RWMutex
	byTxID     map[string]*TxReceipt
	byBlock    map[uint64][]*TxReceipt
	byContract map[string][]*TxReceipt
	order      []string // insertion order of tx IDs
}

// NewReceiptStore creates an empty receipt store.
func NewReceiptStore() *ReceiptStore {
	return &ReceiptStore{
		byTxID:     make(map[string]*TxReceipt),
		byBlock:    make(map[uint64][]*TxReceipt),
		byContract: make(map[string][]*TxReceipt),
	}
}

// Store adds a receipt to the store.
func (rs *ReceiptStore) Store(receipt *TxReceipt) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	// Re-storing a TxID REPLACES it everywhere rather than appending again.
	//
	// byTxID is a map and was always last-write-wins, but byBlock, byContract
	// and order appended unconditionally -- so storing one receipt twice left
	// GetByBlock returning the same transaction twice, and Save writes from
	// `order`, so the duplication round-tripped and survived every subsequent
	// restart. Measured before this fix: two Stores of one receipt gave
	// order=2, byBlock=2, and a save/load cycle preserved both.
	//
	// LoadNDJSON's own doc comment asserted "the Store call dedupes by TxID, so
	// re-loading the same NDJSON twice has the same final state as loading it
	// once", and invited operators to call it twice to merge log segments. That
	// claim was false: measured, a second load of the same file grew order 1->2
	// and byBlock 1->2. Making it true here is cheaper than correcting the
	// comment, and fixes the invited operation rather than documenting it as
	// unsafe.
	if _, exists := rs.byTxID[receipt.TxID]; exists {
		rs.replaceLocked(receipt)
		return
	}
	rs.byTxID[receipt.TxID] = receipt
	rs.byBlock[receipt.BlockHeight] = append(rs.byBlock[receipt.BlockHeight], receipt)
	if receipt.ContractID != "" {
		rs.byContract[receipt.ContractID] = append(rs.byContract[receipt.ContractID], receipt)
	}
	rs.order = append(rs.order, receipt.TxID)
}

// Get retrieves a receipt by transaction ID.
func (rs *ReceiptStore) Get(txID string) (*TxReceipt, bool) {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	r, ok := rs.byTxID[txID]
	return r, ok
}

// GetByBlock returns all receipts for a given block height.
func (rs *ReceiptStore) GetByBlock(height uint64) []*TxReceipt {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return rs.byBlock[height]
}

// GetByContract returns all receipts for a given contract.
func (rs *ReceiptStore) GetByContract(contractID string) []*TxReceipt {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return rs.byContract[contractID]
}

// ListByHeightRange returns receipts whose BlockHeight ∈ [from, to]
// in newest-first order: heights are walked from `to` down to `from`,
// and within each height the receipts are returned in their
// IndexInBlock-ascending natural order (the slice GetByBlock yields).
//
// The result is capped at `limit`; the function returns up to that
// many records and does NOT report a separate "matches beyond the
// page" count — list views (the chain dashboard's recent-tx tile)
// just re-page when the user scrolls. This matches the
// SlashReceiptStore.List posture: cheap O(walked-heights × per-block-receipts)
// reads, no global scan.
//
// Behaviour:
//
//	limit <= 0          → nil (caller must clamp; handler enforces a default + cap)
//	from  >  to         → nil (empty range; same posture as MiningBlocksHandler)
//	missing height      → skipped silently (height with no receipts)
//	exhausted before to → returns whatever was collected; never blocks
//
// Returns a fresh slice; the underlying *TxReceipt pointers are
// shared with the store but receipts are immutable after Store(),
// so the API layer's TxReceiptView projection is safe to read
// concurrently with new appends.
func (rs *ReceiptStore) ListByHeightRange(from, to uint64, limit int) []*TxReceipt {
	if limit <= 0 || from > to {
		return nil
	}
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	out := make([]*TxReceipt, 0, limit)
	// Walk heights from `to` (newest) down to `from` (oldest).
	// uint64 underflow guard: stop the loop when h would wrap.
	for h := to; ; h-- {
		recs := rs.byBlock[h]
		for _, r := range recs {
			out = append(out, r)
			if len(out) >= limit {
				return out
			}
		}
		if h == from {
			break
		}
		if h == 0 {
			// from > 0 but we've reached 0 — exit before
			// the next h-- underflows. Should never fire
			// given the from > to guard above, but
			// defensive against a future signed-arithmetic
			// refactor.
			break
		}
	}
	return out
}

// Recent returns the last N receipts in insertion order.
func (rs *ReceiptStore) Recent(n int) []*TxReceipt {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	if n <= 0 || len(rs.order) == 0 {
		return nil
	}
	start := len(rs.order) - n
	if start < 0 {
		start = 0
	}
	out := make([]*TxReceipt, 0, n)
	for i := len(rs.order) - 1; i >= start; i-- {
		if r, ok := rs.byTxID[rs.order[i]]; ok {
			out = append(out, r)
		}
	}
	return out
}

// SearchLogs returns receipts that contain a log entry with the given topic.
func (rs *ReceiptStore) SearchLogs(topic string) []*TxReceipt {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	var result []*TxReceipt
	for _, txID := range rs.order {
		r := rs.byTxID[txID]
		for _, log := range r.Logs {
			if log.Topic == topic {
				result = append(result, r)
				break
			}
		}
	}
	return result
}

// Count returns the total number of stored receipts.
func (rs *ReceiptStore) Count() int {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return len(rs.byTxID)
}

// Stats returns summary statistics.
func (rs *ReceiptStore) Stats() map[string]interface{} {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	var totalGas int64
	var failed int
	for _, r := range rs.byTxID {
		totalGas += r.GasUsed
		if r.Status == ReceiptFailed {
			failed++
		}
	}
	return map[string]interface{}{
		"total":     len(rs.byTxID),
		"failed":    failed,
		"total_gas": totalGas,
		"blocks":    len(rs.byBlock),
	}
}

// Save persists receipts to a JSON file.
func (rs *ReceiptStore) Save(path string) error {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	receipts := make([]*TxReceipt, 0, len(rs.order))
	for _, txID := range rs.order {
		receipts = append(receipts, rs.byTxID[txID])
	}

	data, err := json.MarshalIndent(receipts, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal receipts: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// Load restores receipts from a JSON file.
func (rs *ReceiptStore) Load(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read receipts: %w", err)
	}

	var receipts []*TxReceipt
	if err := json.Unmarshal(data, &receipts); err != nil {
		return 0, fmt.Errorf("unmarshal receipts: %w", err)
	}

	// Deduplicate by TxID at the file boundary, and report distinct receipts.
	//
	// Store is append-only for its secondary indexes: byTxID is a map and
	// last-write-wins, but byBlock, byContract and order all append
	// unconditionally (receipt.go:76-86). So loading a file that carries the
	// same TxID twice made GetByBlock return the SAME TRANSACTION TWICE, and
	// Save writes from `order`, so the duplication round-tripped and persisted
	// across every subsequent restart. Measured before fixing: two Stores of one
	// receipt gave order=2, byBlock=2, and a save/load cycle preserved both.
	//
	// Fixed here rather than in Store because duplicates do not arise in normal
	// operation -- the two production Store call sites (blockreceipts.go:90 and
	// :113) are mutually exclusive within a block, so each receipt is stored
	// once. The reachable source is a file: a hand-edited, merged or corrupted
	// receipts JSON. Guarding the boundary leaves runtime behaviour untouched.
	//
	// The count matches AccountStore.Load's fix in the same spirit: it feeds a
	// boot log field ("receipts_loaded"), and reporting file entries rather than
	// stored receipts overstated what was restored.
	seen := make(map[string]struct{}, len(receipts))
	for _, r := range receipts {
		if r == nil {
			continue
		}
		if _, dup := seen[r.TxID]; dup {
			continue
		}
		seen[r.TxID] = struct{}{}
		rs.Store(r)
	}
	return len(seen), nil
}

// AppendBlockNDJSON appends every receipt in the receiver whose
// BlockHeight == height as a single NDJSON line to path, creating
// the file (mode 0o644) if missing. Returns the number of receipts
// written.
//
// This is the post-seal hook companion to chain.AppendBlockToFile:
// instead of rewriting the entire receipt store on every seal
// (the O(N_total) cost the legacy ReceiptStore.Save pays), it
// writes only the receipts produced during the block being sealed.
// On a long-running testnet that crosses ~10k receipts, the
// per-seal cost drops from "marshal + write the whole store" to
// "marshal + write this block's receipts" — typically a handful
// of bytes.
//
// Format mirrors qsdm_chain.ndjson: one JSON object per line,
// trailing newline, no leading marker. A reader that sees a
// truncated tail (process crashed mid-write) skips the partial
// line; LoadNDJSON surfaces a parse error indicating which line
// failed so the operator can trim and recover.
//
// Concurrency: takes rs.mu in read mode. Callers that drive this
// from a single serialised hook (e.g. BlockProducer.OnSealedBlock)
// get a strict happens-before edge with the receipts that the
// applier just Stored; callers that fan-out must serialise
// themselves.
func (rs *ReceiptStore) AppendBlockNDJSON(path string, height uint64) (int, error) {
	if path == "" {
		return 0, errors.New("chain.ReceiptStore.AppendBlockNDJSON: empty path")
	}
	rs.mu.RLock()
	receipts := append([]*TxReceipt(nil), rs.byBlock[height]...)
	rs.mu.RUnlock()
	if len(receipts) == 0 {
		return 0, nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, fmt.Errorf("chain.ReceiptStore.AppendBlockNDJSON: open %s: %w", path, err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	written := 0
	for _, r := range receipts {
		if r == nil {
			continue
		}
		data, err := json.Marshal(r)
		if err != nil {
			// One bad receipt should not lose the rest of
			// the batch — flush what we have and surface
			// the error with the partial count so the
			// operator can investigate the offending tx.
			_ = w.Flush()
			return written, fmt.Errorf("chain.ReceiptStore.AppendBlockNDJSON: marshal receipt %s: %w", r.TxID, err)
		}
		if _, err := w.Write(append(data, '\n')); err != nil {
			_ = w.Flush()
			return written, fmt.Errorf("chain.ReceiptStore.AppendBlockNDJSON: write %s: %w", path, err)
		}
		written++
	}
	if err := w.Flush(); err != nil {
		return written, fmt.Errorf("chain.ReceiptStore.AppendBlockNDJSON: flush %s: %w", path, err)
	}
	return written, nil
}

// LoadNDJSON reads path line-by-line, decoding each as a *TxReceipt
// and Store-ing it into the receiver. Returns (loadedCount, nil) on
// success, or (0, nil) if the file does not exist (the canonical
// "fresh chain, no prior receipts" case).
//
// On a truncated trailing line (process crashed mid-write), the
// loader reports a parse error citing the offending line number
// AND returns the partial count already loaded. Operators recover
// by deleting the trailing partial line.
//
// Unlike Load (legacy JSON-array path), LoadNDJSON intentionally
// does NOT require the receiver to be empty — it can be called
// twice if an operator legitimately wants to merge two log
// segments. The Store call dedupes by TxID, so re-loading the
// same NDJSON twice has the same final state as loading it once.
func (rs *ReceiptStore) LoadNDJSON(path string) (int, error) {
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
	// 4 MiB ceiling matches LoadChainNDJSON. A receipt with
	// hundreds of LogEntry rows is well under this; runaway
	// lines surface as parse failures rather than OOM.
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
			// Skip empty-TxID rows defensively — they
			// would corrupt byTxID/order if Stored as-is.
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

// replaceLocked swaps an already-stored receipt in place across every index.
// Caller must hold rs.mu.
//
// Kept separate from Store so the common path (a receipt seen for the first
// time) stays a plain append and pays nothing for this. The scans are bounded
// by the receipts in ONE block and ONE contract, not by the whole store.
func (rs *ReceiptStore) replaceLocked(receipt *TxReceipt) {
	prev := rs.byTxID[receipt.TxID]
	rs.byTxID[receipt.TxID] = receipt

	// A re-stored receipt may carry a different BlockHeight than the one it
	// replaces. Drop it from the old bucket before adding to the new one, or
	// the stale height keeps serving a receipt that no longer claims it.
	//
	// The trigger is a transaction re-submitted and landing in a different
	// block: a tx that fails to apply is dropped from the mempool without
	// advancing the sender nonce, so an identical resubmission is admitted and
	// can later succeed at a new height.
	//
	// NOT a block producer storing the same receipt twice -- an earlier version
	// of this comment said that, and it is false on both producer paths.
	// ReceiptProducer.ProduceBlockWithReceipts stores at blockreceipts.go:90
	// and :113, which are mutually exclusive within one call (the first is
	// inside the all-transactions-failed early return), and BlockHeight is set
	// once at :59 and never reassigned. BlockProducer.ProduceBlock (block.go:259)
	// stores via storeProduceBlockReceipts (block.go:517), whose failure branch
	// continues the loop -- one Store per TxID per call.
	if prev != nil && prev.BlockHeight != receipt.BlockHeight {
		rs.byBlock[prev.BlockHeight] = removeReceipt(rs.byBlock[prev.BlockHeight], receipt.TxID)
	}
	rs.byBlock[receipt.BlockHeight] = upsertReceipt(rs.byBlock[receipt.BlockHeight], receipt)

	if prev != nil && prev.ContractID != "" && prev.ContractID != receipt.ContractID {
		rs.byContract[prev.ContractID] = removeReceipt(rs.byContract[prev.ContractID], receipt.TxID)
	}
	if receipt.ContractID != "" {
		rs.byContract[receipt.ContractID] = upsertReceipt(rs.byContract[receipt.ContractID], receipt)
	}
	// rs.order already holds this TxID exactly once; leave its position alone
	// so Save preserves original arrival ordering.
}

// upsertReceipt returns a bucket with receipt in place of any entry sharing
// its TxID, COPYING rather than writing through.
//
// The copy is the point. GetByBlock and GetByContract hand out the store's
// live slice (receipt.go:117-128), so a caller can still be reading it after
// the RLock has been released -- pkg/api/handlers_admin.go:182 marshals
// exactly that slice with no lock held. Appending never disturbed such a
// reader, because its slice header pins the old length. Writing list[i] in
// place does, and the first version of this function did: a reviewer
// reproduced a genuine DATA RACE under -race against that handler's access
// pattern. Pre-fix Store only ever appended, so this hazard was introduced
// by the dedupe fix rather than uncovered by it.
//
// Restoring the invariant, not documenting it: entries visible to an
// already-returned slice are never mutated.
func upsertReceipt(list []*TxReceipt, receipt *TxReceipt) []*TxReceipt {
	for i, r := range list {
		if r != nil && r.TxID == receipt.TxID {
			out := make([]*TxReceipt, len(list))
			copy(out, list)
			out[i] = receipt
			return out
		}
	}
	return append(list, receipt)
}

// removeReceipt returns a bucket without txID, as a fresh slice. The obvious
// `out := list[:0]` compaction reuses the caller's backing array and would
// corrupt a slice a reader already holds -- same hazard as upsertReceipt.
func removeReceipt(list []*TxReceipt, txID string) []*TxReceipt {
	out := make([]*TxReceipt, 0, len(list))
	for _, r := range list {
		if r == nil || r.TxID == txID {
			continue
		}
		out = append(out, r)
	}
	return out
}
