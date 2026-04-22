package chain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// ErrPolExtensionBlocked is returned when POL anchoring blocks sealing another block until the tip is POL-clear.
var ErrPolExtensionBlocked = errors.New("chain: POL anchor blocks extending the chain until the current tip is POL-clear")

// ErrBFTExtensionBlocked is returned when the live BFT engine has not committed the current chain tip height yet.
var ErrBFTExtensionBlocked = errors.New("chain: BFT extension blocked until the current tip height is committed in BFT")

// ErrPreSealRequiresAccountStore is returned when PreSealBFTRound is set but the applier is not *AccountStore.
var ErrPreSealRequiresAccountStore = errors.New("chain: pre-seal BFT requires *AccountStore applier for speculative apply")

// ErrExternalAppendNeedsAccountStore is returned when the applier does not implement ChainReplayApplier
// (clone/replay/rollback required for safe external append).
var ErrExternalAppendNeedsAccountStore = errors.New("chain: external block append requires a replay-capable applier (ChainReplayApplier, e.g. *AccountStore)")

// ErrExternalAppendConflict is returned when the chain already has a different block at the same height (equivocation / fork).
var ErrExternalAppendConflict = errors.New("chain: external block conflicts with existing block at same height")

// ExternalAppendConflictError carries structured context for evidence and metrics (unwraps to ErrExternalAppendConflict).
type ExternalAppendConflictError struct {
	Height                uint64
	ExistingHash, NewHash string
}

func (e *ExternalAppendConflictError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s (existing %q new %q)", ErrExternalAppendConflict.Error(), e.ExistingHash, e.NewHash)
}

// Unwrap returns ErrExternalAppendConflict for errors.Is / errors.As chains.
func (e *ExternalAppendConflictError) Unwrap() error { return ErrExternalAppendConflict }

// Block represents a finalised block in the chain.
type Block struct {
	Height       uint64          `json:"height"`
	PrevHash     string          `json:"prev_hash"`
	Hash         string          `json:"hash"`
	Timestamp    time.Time       `json:"timestamp"`
	Transactions []*mempool.Tx   `json:"transactions"`
	StateRoot    string          `json:"state_root"`
	TotalFees    float64         `json:"total_fees"`
	GasUsed      int64           `json:"gas_used"`
	ProducerID   string          `json:"producer_id"`
}

// BlockHeader is the lightweight header for SPV validation.
type BlockHeader struct {
	Height    uint64    `json:"height"`
	PrevHash  string    `json:"prev_hash"`
	Hash      string    `json:"hash"`
	StateRoot string    `json:"state_root"`
	TxRoot    string    `json:"tx_root"`
	Timestamp time.Time `json:"timestamp"`
	TxCount   int       `json:"tx_count"`
}

// Header returns the block's lightweight header.
func (b *Block) Header() BlockHeader {
	return BlockHeader{
		Height:    b.Height,
		PrevHash:  b.PrevHash,
		Hash:      b.Hash,
		StateRoot: b.StateRoot,
		TxRoot:    computeTxRoot(b.Transactions),
		Timestamp: b.Timestamp,
		TxCount:   len(b.Transactions),
	}
}

// StateApplier applies transactions to node state. Implementations decide what "apply" means.
type StateApplier interface {
	ApplyTx(tx *mempool.Tx) error
	StateRoot() string
}

// ChainReplayApplier is a StateApplier that supports independent clone replay and rollback for TryAppendExternalBlock.
type ChainReplayApplier interface {
	StateApplier
	// ChainReplayClone returns a deep copy for speculative ApplyTx replay (must not alias live state).
	ChainReplayClone() ChainReplayApplier
	// RestoreFromChainReplay replaces the receiver's state from a snapshot produced by ChainReplayClone on the same concrete type family.
	RestoreFromChainReplay(from ChainReplayApplier) error
}

// BlockProducer assembles blocks from the mempool.
type BlockProducer struct {
	mu           sync.Mutex
	pool         *mempool.Mempool
	applier      StateApplier
	chain        []*Block
	maxTxBlock   int
	producerID   string
	polFollower  *PolFollower
	// bftSealGate, when set, requires BFTConsensus.IsCommitted(tip.Height) before sealing the next block.
	bftSealGate *BFTConsensus
	// preSealBFTRound, when set, runs after txs are applied to a cloned AccountStore and before the live
	// applier is mutated; it receives the tentative block (hash/state root) and must commit BFT for its height.
	preSealBFTRound func(tentative *Block) error
	// OnSealed runs after a block is appended and the producer lock is released (best-effort hooks).
	OnSealed func()
	// appendReceipts, when set, stores per-tx receipts after a successful TryAppendExternalBlock (same replay semantics as ProduceBlockWithReceipts).
	appendReceipts *ReceiptStore
}

// ProducerConfig configures the block producer.
type ProducerConfig struct {
	MaxTxPerBlock int
	ProducerID    string
}

// DefaultProducerConfig returns sensible defaults.
func DefaultProducerConfig() ProducerConfig {
	return ProducerConfig{MaxTxPerBlock: 500, ProducerID: "node-0"}
}

// NewBlockProducer creates a producer that drains the given mempool.
func NewBlockProducer(pool *mempool.Mempool, applier StateApplier, cfg ProducerConfig) *BlockProducer {
	if cfg.MaxTxPerBlock <= 0 {
		cfg.MaxTxPerBlock = 500
	}
	return &BlockProducer{
		pool:       pool,
		applier:    applier,
		maxTxBlock: cfg.MaxTxPerBlock,
		producerID: cfg.ProducerID,
	}
}

// SetPolFollower attaches optional POL fork-choice gating for block production (may be nil).
func (bp *BlockProducer) SetPolFollower(p *PolFollower) {
	if bp == nil {
		return
	}
	bp.mu.Lock()
	defer bp.mu.Unlock()
	bp.polFollower = p
}

// SetBFTSealGate attaches optional BFT finality gating: the current tip height must be committed
// in the given consensus instance before another block may be sealed (may be nil).
func (bp *BlockProducer) SetBFTSealGate(bc *BFTConsensus) {
	if bp == nil {
		return
	}
	bp.mu.Lock()
	defer bp.mu.Unlock()
	bp.bftSealGate = bc
}

// SetAppendReceiptStore optionally records per-tx receipts when TryAppendExternalBlock commits a block (replay-derived, same shape as ProduceBlockWithReceipts).
func (bp *BlockProducer) SetAppendReceiptStore(rs *ReceiptStore) {
	if bp == nil {
		return
	}
	bp.mu.Lock()
	defer bp.mu.Unlock()
	bp.appendReceipts = rs
}

// SetPreSealBFTRound sets a hook that runs after speculative tx application and before committing
// state to the live applier. When non-nil, the applier must be *AccountStore (cloned for simulation).
// Set to nil to disable pre-seal (legacy: BFT only in OnSealed).
func (bp *BlockProducer) SetPreSealBFTRound(fn func(tentative *Block) error) {
	if bp == nil {
		return
	}
	bp.mu.Lock()
	defer bp.mu.Unlock()
	bp.preSealBFTRound = fn
}

// ProduceBlock drains up to MaxTxPerBlock transactions, applies them, and seals a new block.
//
// When SetPreSealBFTRound is configured with an *AccountStore applier, txs are applied to a clone first,
// the hook commits BFT for the pending height, then the same txs are applied to the live store and the
// block is appended. Otherwise BFT is driven only after append via OnSealed.
func (bp *BlockProducer) ProduceBlock() (block *Block, err error) {
	bp.mu.Lock()
	runSealedHook := false
	defer func() {
		bp.mu.Unlock()
		if runSealedHook && bp.OnSealed != nil {
			bp.OnSealed()
		}
	}()

	if bp.polFollower != nil && len(bp.chain) > 0 {
		last := bp.chain[len(bp.chain)-1]
		if !bp.polFollower.CanExtendFromTip(last.Height, last.StateRoot) {
			return nil, ErrPolExtensionBlocked
		}
	}

	if bp.bftSealGate != nil && len(bp.chain) > 0 {
		last := bp.chain[len(bp.chain)-1]
		if !bp.bftSealGate.IsCommitted(last.Height) {
			return nil, ErrBFTExtensionBlocked
		}
	}

	if bp.preSealBFTRound != nil {
		if _, ok := bp.applier.(*AccountStore); !ok {
			return nil, ErrPreSealRequiresAccountStore
		}
	}

	txs := bp.pool.Drain(bp.maxTxBlock)
	if len(txs) == 0 {
		return nil, fmt.Errorf("no transactions to include")
	}

	var prevHash string
	var height uint64
	if len(bp.chain) > 0 {
		last := bp.chain[len(bp.chain)-1]
		prevHash = last.Hash
		height = last.Height + 1
	}

	var included []*mempool.Tx
	var totalFees float64
	var totalGas int64
	var stateRoot string
	var tentative *Block

	if bp.preSealBFTRound != nil {
		as := bp.applier.(*AccountStore)
		spec := as.Clone()
		for _, tx := range txs {
			if err := spec.ApplyTx(tx); err != nil {
				continue
			}
			included = append(included, tx)
			totalFees += tx.Fee
			totalGas += tx.GasLimit
		}
		if len(included) == 0 {
			bp.pool.RestoreTransactions(txs)
			return nil, fmt.Errorf("all transactions failed state application")
		}
		stateRoot = spec.StateRoot()
		now := time.Now()
		tentative = &Block{
			Height:       height,
			PrevHash:     prevHash,
			Timestamp:    now,
			Transactions: included,
			StateRoot:    stateRoot,
			TotalFees:    totalFees,
			GasUsed:      totalGas,
			ProducerID:   bp.producerID,
		}
		tentative.Hash = computeBlockHash(tentative)
		if err := bp.preSealBFTRound(tentative); err != nil {
			bp.pool.RestoreTransactions(txs)
			return nil, err
		}
		for _, tx := range included {
			if err := bp.applier.ApplyTx(tx); err != nil {
				bp.pool.RestoreTransactions(txs)
				return nil, fmt.Errorf("chain: live apply after pre-seal failed on %s: %w", tx.ID, err)
			}
		}
		if got := bp.applier.StateRoot(); got != stateRoot {
			bp.pool.RestoreTransactions(txs)
			return nil, fmt.Errorf("chain: state root mismatch after pre-seal (live %s vs spec %s)", got, stateRoot)
		}
	} else {
		for _, tx := range txs {
			if err := bp.applier.ApplyTx(tx); err != nil {
				continue
			}
			included = append(included, tx)
			totalFees += tx.Fee
			totalGas += tx.GasLimit
		}
		if len(included) == 0 {
			bp.pool.RestoreTransactions(txs)
			return nil, fmt.Errorf("all transactions failed state application")
		}
		stateRoot = bp.applier.StateRoot()
	}

	if tentative != nil {
		block = tentative
	} else {
		block = &Block{
			Height:       height,
			PrevHash:     prevHash,
			Timestamp:    time.Now(),
			Transactions: included,
			StateRoot:    stateRoot,
			TotalFees:    totalFees,
			GasUsed:      totalGas,
			ProducerID:   bp.producerID,
		}
		block.Hash = computeBlockHash(block)
	}

	bp.chain = append(bp.chain, block)
	runSealedHook = true
	return block, nil
}

// storeExternalAppendReceipts emits one receipt per non-nil tx by replaying blk.Transactions in order from
// preBlock (the live applier snapshot before this block was applied). Matches ProduceBlockWithReceipts:
// trial apply on a clone, then advance sequential state only on success; failed txs get TxFailed logs only.
func storeExternalAppendReceipts(rs *ReceiptStore, blk *Block, preBlock ChainReplayApplier, now time.Time) {
	if rs == nil || blk == nil || preBlock == nil {
		return
	}
	sequential := preBlock.ChainReplayClone()
	for i, tx := range blk.Transactions {
		if tx == nil {
			continue
		}
		receipt := &TxReceipt{
			TxID:         tx.ID,
			BlockHeight:  blk.Height,
			BlockHash:    blk.Hash,
			Fee:          tx.Fee,
			GasUsed:      tx.GasLimit,
			Timestamp:    now,
			IndexInBlock: i,
		}
		trial := sequential.ChainReplayClone()
		if err := trial.ApplyTx(tx); err != nil {
			receipt.Status = ReceiptFailed
			receipt.Error = err.Error()
			failData := map[string]interface{}{"error": err.Error()}
			applyReceiptContractFromTx(receipt, tx, failData)
			receipt.Logs = []LogEntry{{Topic: "TxFailed", Data: failData, Index: 0}}
			rs.Store(receipt)
			continue
		}
		if err := sequential.ApplyTx(tx); err != nil {
			receipt.Status = ReceiptFailed
			receipt.Error = err.Error()
			seqFail := map[string]interface{}{
				"error": fmt.Sprintf("sequential apply after successful trial: %v", err),
			}
			applyReceiptContractFromTx(receipt, tx, seqFail)
			receipt.Logs = []LogEntry{{Topic: "TxFailed", Data: seqFail, Index: 0}}
			rs.Store(receipt)
			return
		}
		receipt.Status = ReceiptSuccess
		okData := map[string]interface{}{
			"sender": tx.Sender, "recipient": tx.Recipient, "amount": tx.Amount,
		}
		applyReceiptContractFromTx(receipt, tx, okData)
		receipt.Logs = []LogEntry{{Topic: "TxApplied", Data: okData, Index: 0}}
		rs.Store(receipt)
	}
}

// TryAppendExternalBlock replays blk's transactions against a ChainReplayApplier clone, verifies StateRoot,
// applies to the live store, appends blk to the local chain, removes txs from the mempool, and runs OnSealed.
// Idempotent if this height is already present. Intended when BFT commits from gossip and PendingBlock has the body.
func (bp *BlockProducer) TryAppendExternalBlock(blk *Block) error {
	if bp == nil || blk == nil {
		return fmt.Errorf("chain: nil producer or block")
	}
	if want := computeBlockHash(blk); blk.Hash != want {
		return fmt.Errorf("chain: external block has invalid hash")
	}
	ra, ok := bp.applier.(ChainReplayApplier)
	if !ok {
		return ErrExternalAppendNeedsAccountStore
	}

	var runSealedHook bool
	bp.mu.Lock()
	for _, b := range bp.chain {
		if b.Height == blk.Height {
			if b.Hash == blk.Hash {
				bp.mu.Unlock()
				return nil
			}
			bp.mu.Unlock()
			return &ExternalAppendConflictError{Height: blk.Height, ExistingHash: b.Hash, NewHash: blk.Hash}
		}
	}
	if len(bp.chain) > 0 {
		last := bp.chain[len(bp.chain)-1]
		if blk.PrevHash != last.Hash || blk.Height != last.Height+1 {
			bp.mu.Unlock()
			return fmt.Errorf("chain: external block does not extend tip")
		}
		if bp.polFollower != nil && !bp.polFollower.CanExtendFromTip(last.Height, last.StateRoot) {
			bp.mu.Unlock()
			return ErrPolExtensionBlocked
		}
	} else if blk.Height != 0 || blk.PrevHash != "" {
		bp.mu.Unlock()
		return fmt.Errorf("chain: external genesis must be height 0 with empty prev_hash")
	}
	bp.mu.Unlock()

	spec := ra.ChainReplayClone()
	for _, tx := range blk.Transactions {
		if tx == nil {
			continue
		}
		if err := spec.ApplyTx(tx); err != nil {
			return fmt.Errorf("chain: external block replay (spec): %w", err)
		}
	}
	if spec.StateRoot() != blk.StateRoot {
		return fmt.Errorf("chain: external block state_root mismatch after replay")
	}

	bp.mu.Lock()
	for _, b := range bp.chain {
		if b.Height == blk.Height {
			if b.Hash == blk.Hash {
				bp.mu.Unlock()
				return nil
			}
			bp.mu.Unlock()
			return &ExternalAppendConflictError{Height: blk.Height, ExistingHash: b.Hash, NewHash: blk.Hash}
		}
	}
	if len(bp.chain) > 0 {
		last := bp.chain[len(bp.chain)-1]
		if blk.PrevHash != last.Hash || blk.Height != last.Height+1 {
			bp.mu.Unlock()
			return fmt.Errorf("chain: tip changed during external append")
		}
	}
	backup := ra.ChainReplayClone()
	live := bp.applier.(ChainReplayApplier)
	for _, tx := range blk.Transactions {
		if tx == nil {
			continue
		}
		if err := live.ApplyTx(tx); err != nil {
			_ = live.RestoreFromChainReplay(backup)
			bp.mu.Unlock()
			return fmt.Errorf("chain: external block live apply: %w", err)
		}
	}
	if live.StateRoot() != blk.StateRoot {
		_ = live.RestoreFromChainReplay(backup)
		bp.mu.Unlock()
		return fmt.Errorf("chain: live state_root mismatch after external append")
	}
	bp.chain = append(bp.chain, blk)
	runSealedHook = true
	for _, tx := range blk.Transactions {
		if tx != nil {
			_ = bp.pool.Remove(tx.ID)
		}
	}
	rs := bp.appendReceipts
	bp.mu.Unlock()

	if rs != nil {
		storeExternalAppendReceipts(rs, blk, backup, time.Now())
	}

	if runSealedHook && bp.OnSealed != nil {
		bp.OnSealed()
	}
	return nil
}

// GetBlock returns a block by height.
func (bp *BlockProducer) GetBlock(height uint64) (*Block, bool) {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	for _, b := range bp.chain {
		if b.Height == height {
			return b, true
		}
	}
	return nil, false
}

// LatestBlock returns the chain tip.
func (bp *BlockProducer) LatestBlock() (*Block, bool) {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	if len(bp.chain) == 0 {
		return nil, false
	}
	return bp.chain[len(bp.chain)-1], true
}

// ChainHeight returns the current height.
func (bp *BlockProducer) ChainHeight() uint64 {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	if len(bp.chain) == 0 {
		return 0
	}
	return bp.chain[len(bp.chain)-1].Height
}

// Headers returns block headers for a range of heights.
func (bp *BlockProducer) Headers(from, to uint64) []BlockHeader {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	var out []BlockHeader
	for _, b := range bp.chain {
		if b.Height >= from && b.Height <= to {
			out = append(out, b.Header())
		}
	}
	return out
}

// ComputeBlockHash returns the canonical block hash for b (used by BFT propose-body validation and tests).
func ComputeBlockHash(b *Block) string {
	return computeBlockHash(b)
}

func computeBlockHash(b *Block) string {
	data, _ := json.Marshal(struct {
		Height    uint64    `json:"h"`
		PrevHash  string    `json:"p"`
		StateRoot string    `json:"s"`
		TxRoot    string    `json:"t"`
		Time      time.Time `json:"ts"`
		Producer  string    `json:"pr"`
	}{b.Height, b.PrevHash, b.StateRoot, computeTxRoot(b.Transactions), b.Timestamp, b.ProducerID})
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func computeTxRoot(txs []*mempool.Tx) string {
	if len(txs) == 0 {
		return emptyHash()
	}
	ids := make([]string, len(txs))
	for i, tx := range txs {
		ids[i] = tx.ID
	}
	tree := BuildMerkleTree(ids)
	return tree.Root
}
