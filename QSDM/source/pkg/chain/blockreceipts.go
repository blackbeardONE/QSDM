package chain

import (
	"fmt"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// ReceiptProducer wraps BlockProducer and automatically generates receipts
// for each transaction during block production.
type ReceiptProducer struct {
	mu       sync.Mutex
	producer *BlockProducer
	receipts *ReceiptStore
}

// NewReceiptProducer creates a block producer that also emits receipts.
func NewReceiptProducer(pool *mempool.Mempool, applier StateApplier, cfg ProducerConfig, receipts *ReceiptStore) *ReceiptProducer {
	return &ReceiptProducer{
		producer: NewBlockProducer(pool, applier, cfg),
		receipts: receipts,
	}
}

// ProduceBlockWithReceipts drains the mempool, applies transactions, and generates
// a receipt for each transaction (successful or failed).
func (rp *ReceiptProducer) ProduceBlockWithReceipts() (*Block, []*TxReceipt, error) {
	rp.mu.Lock()
	defer rp.mu.Unlock()

	rp.producer.mu.Lock()
	defer rp.producer.mu.Unlock()

	txs := rp.producer.pool.Drain(rp.producer.maxTxBlock)
	if len(txs) == 0 {
		return nil, nil, fmt.Errorf("no transactions to include")
	}

	var included []*mempool.Tx
	var totalFees float64
	var totalGas int64
	var blockReceipts []*TxReceipt
	now := time.Now()

	var prevHash string
	var height uint64
	if len(rp.producer.chain) > 0 {
		last := rp.producer.chain[len(rp.producer.chain)-1]
		prevHash = last.Hash
		height = last.Height + 1
	}

	for i, tx := range txs {
		receipt := &TxReceipt{
			TxID:         tx.ID,
			BlockHeight:  height,
			Fee:          tx.Fee,
			GasUsed:      tx.GasLimit,
			Timestamp:    now,
			IndexInBlock: i,
		}

		if err := rp.producer.applier.ApplyTx(tx); err != nil {
			receipt.Status = ReceiptFailed
			receipt.Error = err.Error()
			failData := map[string]interface{}{"error": err.Error()}
			applyReceiptContractFromTx(receipt, tx, failData)
			receipt.Logs = []LogEntry{{Topic: "TxFailed", Data: failData, Index: 0}}
		} else {
			receipt.Status = ReceiptSuccess
			okData := map[string]interface{}{
				"sender": tx.Sender, "recipient": tx.Recipient, "amount": tx.Amount,
			}
			applyReceiptContractFromTx(receipt, tx, okData)
			receipt.Logs = []LogEntry{{Topic: "TxApplied", Data: okData, Index: 0}}
			included = append(included, tx)
			totalFees += tx.Fee
			totalGas += tx.GasLimit
		}

		blockReceipts = append(blockReceipts, receipt)
	}

	if len(included) == 0 {
		// Still store failed receipts
		for _, r := range blockReceipts {
			rp.receipts.Store(r)
		}
		return nil, blockReceipts, fmt.Errorf("all transactions failed state application")
	}

	stateRoot := rp.producer.applier.StateRoot()

	block := &Block{
		Height:       height,
		PrevHash:     prevHash,
		Timestamp:    now,
		Transactions: included,
		StateRoot:    stateRoot,
		TotalFees:    totalFees,
		GasUsed:      totalGas,
		ProducerID:   rp.producer.producerID,
	}
	block.Hash = computeBlockHash(block)
	rp.producer.chain = append(rp.producer.chain, block)

	// Update receipts with final block hash
	for _, r := range blockReceipts {
		r.BlockHash = block.Hash
		rp.receipts.Store(r)
	}

	return block, blockReceipts, nil
}

// GetBlock delegates to the underlying BlockProducer.
func (rp *ReceiptProducer) GetBlock(height uint64) (*Block, bool) {
	return rp.producer.GetBlock(height)
}

// LatestBlock delegates to the underlying BlockProducer.
func (rp *ReceiptProducer) LatestBlock() (*Block, bool) {
	return rp.producer.LatestBlock()
}

// Receipts returns the receipt store.
func (rp *ReceiptProducer) Receipts() *ReceiptStore {
	return rp.receipts
}

// TryAppendExternalBlock delegates to the wrapped BlockProducer (requires *AccountStore applier for replay/rollback).
func (rp *ReceiptProducer) TryAppendExternalBlock(blk *Block) error {
	if rp == nil || rp.producer == nil {
		return fmt.Errorf("chain: nil receipt producer")
	}
	return rp.producer.TryAppendExternalBlock(blk)
}

// UnderlyingProducer exposes the inner BlockProducer for gates, hooks, and metrics (same pool/applier/chain).
func (rp *ReceiptProducer) UnderlyingProducer() *BlockProducer {
	if rp == nil {
		return nil
	}
	return rp.producer
}
