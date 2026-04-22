package chain

import (
	"encoding/json"
	"fmt"
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

	for _, r := range receipts {
		rs.Store(r)
	}
	return len(receipts), nil
}
