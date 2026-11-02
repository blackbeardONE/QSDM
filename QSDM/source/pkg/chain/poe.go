package chain

// poe.go: Proof-of-Entanglement (PoE) parent rules for signed wallet
// transfers, enforced identically on every consensus path.
//
// A wallet transfer's signed envelope (tx.Payload) carries parent_cells. From
// the configured activation height on, a transfer committed at position
// (h, i) -- block height h, index i -- is valid only if (package poe holds
// the constants and error values):
//
//   1. it names MinParents..MaxParents parents, each a well-formed ID, with
//      no repeats and none equal to its own ID (poe.CheckShape);
//   2. its own ID is not already used by a transaction committed in the
//      reference window or earlier in block h (so every reference is
//      unambiguous);
//   3. every parent names a transaction committed at a height in
//      [h-ParentWindowBlocks, h-1], or a transaction at an index j < i in
//      block h.
//
// Rule 3 makes every reference point to a strictly earlier position in the
// chain's total order, so the parent graph cannot contain a cycle; a parent
// that sits later in the same block is reported as ErrParentNotEarlier, one
// that is nowhere in the window (never existed, only pending in a mempool,
// from another fork, or too old) as ErrParentUnknown.
//
// Where it runs (all in this package, all through checkWalletTransferParents):
//
//   - mempool admission: walletTransferSubmitter.Add and the mempool
//     admission layer WalletTransferPoEAdmission, against the committed tip;
//   - block production: ProduceBlock (both the pre-seal and the direct path)
//     and ReceiptProducer.ProduceBlockWithReceipts drop a failing transfer
//     with a TxFailed receipt, exactly like any other apply failure;
//   - block validation and replay: TryAppendExternalBlock rejects the whole
//     block. Followers syncing over HTTPS or P2P, BFT commits, the HL1 tail
//     replay and the S4r receipt repair all go through it.
//
// Below the activation height none of this runs and blocks replay exactly as
// before, so historical blocks (whose transfers carry no parents) stay valid
// on every follower. Zero, the default, never activates.
//
// The rules need to know which transaction IDs are committed. Every
// BlockProducer keeps a small index of the transaction IDs in the last
// ParentWindowBlocks blocks of its own canonical chain (poeHistory). It is
// derived only from blocks, never from receipts or the mempool, so every
// validator holding the same chain computes the same answers. It is built
// only when an activation height is configured.

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/poe"
)

// poeActivationHeight is the first height at which the PoE parent rules are
// consensus rules. Zero disables them.
var poeActivationHeight atomic.Uint64

// SetPoEActivationHeight sets the first block height at which signed wallet
// transfers must satisfy the PoE parent rules. Zero (the default) never
// activates. Every validator must use the same value: a validator enforcing
// a rule the producer does not would reject the producer's blocks.
func SetPoEActivationHeight(h uint64) { poeActivationHeight.Store(h) }

// PoEActivationHeight reports the configured activation height (0 = never).
func PoEActivationHeight() uint64 { return poeActivationHeight.Load() }

// PoEActiveAt reports whether the PoE parent rules govern a block at height.
func PoEActiveAt(height uint64) bool {
	h := poeActivationHeight.Load()
	return h != 0 && height >= h
}

func poeConfigured() bool { return poeActivationHeight.Load() != 0 }

// ---------------------------------------------------------------------------
// The rule itself
// ---------------------------------------------------------------------------

// poeLookup tells checkWalletTransferParents where an ID sits relative to the
// transfer being checked. committed covers the reference window of blocks
// before the current one; earlier and later cover the current block (either
// may be nil). later is used only to report a forward reference precisely.
type poeLookup struct {
	committed func(id string) bool
	earlier   func(id string) bool
	later     func(id string) bool
}

// checkWalletTransferParents is the single implementation of the PoE parent
// rules (see the file comment). txID is the transfer's own ID and parents its
// signed parent_cells.
func checkWalletTransferParents(txID string, parents []string, lk poeLookup) error {
	if err := poe.CheckShape(txID, parents); err != nil {
		return err
	}
	in := func(f func(string) bool, id string) bool { return f != nil && f(id) }
	if in(lk.earlier, txID) || in(lk.committed, txID) {
		return fmt.Errorf("%w: %q", poe.ErrDuplicateTxID, txID)
	}
	for i, p := range parents {
		if in(lk.earlier, p) || in(lk.committed, p) {
			continue
		}
		if in(lk.later, p) {
			return fmt.Errorf("%w: parent %d %q appears at the same or a later position in the block", poe.ErrParentNotEarlier, i, p)
		}
		return fmt.Errorf("%w: parent %d %q", poe.ErrParentUnknown, i, p)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Committed-history index
// ---------------------------------------------------------------------------

// poeHistory indexes the transaction IDs of the last poe.ParentWindowBlocks
// blocks of one producer's chain. After folding the block at height t it
// holds heights t-W+1 .. t, which are exactly the committed parents a
// transfer in block t+1 may name. Its own lock is never held while acquiring
// bp.mu or a mempool lock, so the admission path may read it from inside
// mempool.Add.
type poeHistory struct {
	mu       sync.RWMutex
	ready    bool
	hasTip   bool
	tip      uint64
	byID     map[string]uint64   // id -> latest height it was committed at
	byHeight map[uint64][]string // height -> ids committed there
}

// rebuild indexes the window ending at the last block of blocks, which must
// be contiguous by height (BlockProducer.chain always is).
func (h *poeHistory) rebuild(blocks []*Block) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.byID = make(map[string]uint64)
	h.byHeight = make(map[uint64][]string)
	h.ready, h.hasTip, h.tip = true, false, 0
	start := 0
	if n := len(blocks); n > poe.ParentWindowBlocks {
		start = n - poe.ParentWindowBlocks
	}
	for _, b := range blocks[start:] {
		h.foldLocked(b)
	}
	poeStats.rebuilds.Add(1)
}

// fold adds the block that was just appended to the chain. A block that
// does not extend the indexed tip marks the index stale; the next
// ensurePoEHistoryLocked rebuilds it from the chain.
func (h *poeHistory) fold(b *Block) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.ready {
		return
	}
	h.foldLocked(b)
}

func (h *poeHistory) foldLocked(b *Block) {
	if b == nil || (h.hasTip && b.Height != h.tip+1) {
		h.ready = false
		return
	}
	ids := make([]string, 0, len(b.Transactions))
	for _, tx := range b.Transactions {
		if tx == nil || tx.ID == "" {
			continue
		}
		ids = append(ids, tx.ID)
		h.byID[tx.ID] = b.Height
	}
	h.byHeight[b.Height] = ids
	h.tip, h.hasTip = b.Height, true
	if b.Height >= poe.ParentWindowBlocks {
		old := b.Height - poe.ParentWindowBlocks
		for _, id := range h.byHeight[old] {
			if at, ok := h.byID[id]; ok && at == old {
				delete(h.byID, id)
			}
		}
		delete(h.byHeight, old)
	}
}

// drop releases the index (activation unset).
func (h *poeHistory) drop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ready, h.hasTip, h.tip = false, false, 0
	h.byID, h.byHeight = nil, nil
}

// inStep reports whether the index describes a chain whose tip is (hasTip,
// tip).
func (h *poeHistory) inStep(hasTip bool, tip uint64) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.inStepLocked(hasTip, tip)
}

func (h *poeHistory) inStepLocked(hasTip bool, tip uint64) bool {
	if !h.ready || h.hasTip != hasTip {
		return false
	}
	return !hasTip || h.tip == tip
}

// readyForHeight reports whether the index holds exactly the committed
// window for a block at height.
func (h *poeHistory) readyForHeight(height uint64) bool {
	if height == 0 {
		return h.inStep(false, 0)
	}
	return h.inStep(true, height-1)
}

// committed reports whether id is committed inside the indexed window.
func (h *poeHistory) committed(id string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.byID[id]
	return ok
}

// nextHeight returns the height of the block after the indexed tip.
func (h *poeHistory) nextHeight() (uint64, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if !h.ready {
		return 0, false
	}
	if !h.hasTip {
		return 0, true
	}
	return h.tip + 1, true
}

// size returns the number of indexed IDs (metrics).
func (h *poeHistory) size() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.byID)
}

// ensurePoEHistoryLocked brings the producer's index in step with its chain.
// Caller holds bp.mu. With no activation height configured the index is
// released and nothing is kept.
func (bp *BlockProducer) ensurePoEHistoryLocked() {
	if !poeConfigured() {
		if bp.poeHist.isBuilt() {
			bp.poeHist.drop()
		}
		return
	}
	hasTip := len(bp.chain) > 0
	var tip uint64
	if hasTip {
		tip = bp.chain[len(bp.chain)-1].Height
	}
	if bp.poeHist.inStep(hasTip, tip) {
		return
	}
	bp.poeHist.rebuild(bp.chain)
}

func (h *poeHistory) isBuilt() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.byID != nil
}

// foldPoEHistoryLocked records a block the producer just appended. Caller
// holds bp.mu.
func (bp *BlockProducer) foldPoEHistoryLocked(b *Block) {
	if !poeConfigured() {
		return
	}
	bp.poeHist.fold(b)
}

// ---------------------------------------------------------------------------
// Per-block scope: production and validation
// ---------------------------------------------------------------------------

// poeBlockScope checks the transfers of one block in order. earlier holds the
// IDs of the transactions already in the block; remaining counts the IDs at
// the current and later positions (for precise forward-reference errors).
type poeBlockScope struct {
	active    bool
	height    uint64
	hist      *poeHistory
	histErr   error
	earlier   map[string]struct{}
	remaining map[string]int
}

// newPoEScopeLocked prepares the scope for a block at height whose candidate
// transactions, in block order, are txs. Caller holds bp.mu.
func (bp *BlockProducer) newPoEScopeLocked(height uint64, txs []*mempool.Tx) *poeBlockScope {
	// Keep the index in step while an activation height is configured, even
	// below it, so the window is complete when the rules start to apply.
	bp.ensurePoEHistoryLocked()
	s := &poeBlockScope{height: height, hist: &bp.poeHist}
	if !PoEActiveAt(height) {
		return s
	}
	s.active = true
	if !bp.poeHist.readyForHeight(height) {
		s.histErr = fmt.Errorf("%w: index not at height %d", poe.ErrHistoryUnavailable, height)
	}
	s.earlier = make(map[string]struct{}, len(txs))
	s.remaining = make(map[string]int, len(txs))
	for _, tx := range txs {
		if tx != nil && tx.ID != "" {
			s.remaining[tx.ID]++
		}
	}
	return s
}

// check applies the PoE rules to tx at the current position. Transactions
// other than wallet transfers are never checked (heartbeats, mining rewards
// and other system or contract transactions are unaffected).
func (s *poeBlockScope) check(tx *mempool.Tx) error {
	if s == nil || !s.active || tx == nil {
		return nil
	}
	if tx.ID != "" && s.remaining[tx.ID] > 0 {
		s.remaining[tx.ID]--
	}
	if tx.ContractID != WalletTransferContractID {
		return nil
	}
	if s.histErr != nil {
		return s.histErr
	}
	env, err := decodeWalletTransferEnvelope(tx.Payload)
	if err != nil {
		return err
	}
	return checkWalletTransferParents(tx.ID, env.ParentCells, poeLookup{
		committed: s.hist.committed,
		earlier:   func(id string) bool { _, ok := s.earlier[id]; return ok },
		later:     func(id string) bool { return s.remaining[id] > 0 },
	})
}

// include records that tx is now part of the block.
func (s *poeBlockScope) include(tx *mempool.Tx) {
	if s == nil || !s.active || tx == nil || tx.ID == "" {
		return
	}
	s.earlier[tx.ID] = struct{}{}
}

// verifyBlockPoE checks every transfer of an external block in order. Any
// failure rejects the block. Caller holds sealLifecycleMu and has run
// ensurePoEHistoryLocked; bp.mu is not held.
func (bp *BlockProducer) verifyBlockPoE(blk *Block) error {
	if blk == nil || !PoEActiveAt(blk.Height) {
		return nil
	}
	s := &poeBlockScope{active: true, height: blk.Height, hist: &bp.poeHist}
	if !bp.poeHist.readyForHeight(blk.Height) {
		s.histErr = fmt.Errorf("%w: index not at height %d", poe.ErrHistoryUnavailable, blk.Height)
	}
	s.earlier = make(map[string]struct{}, len(blk.Transactions))
	s.remaining = make(map[string]int, len(blk.Transactions))
	for _, tx := range blk.Transactions {
		if tx != nil && tx.ID != "" {
			s.remaining[tx.ID]++
		}
	}
	for i, tx := range blk.Transactions {
		if tx == nil {
			continue
		}
		if err := s.check(tx); err != nil {
			poeStats.recordReject(err)
			poeStats.blocksRejected.Add(1)
			return fmt.Errorf("block %d tx %d (%s): %w", blk.Height, i, tx.ID, err)
		}
		s.include(tx)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Admission
// ---------------------------------------------------------------------------

// PoEAdmissionLeadBlocks is how many blocks before the activation height
// mempool admission starts enforcing the PoE parent rules. A transfer
// admitted without valid parents could otherwise still be waiting in a
// mempool (maximum age 30 minutes, about 180 blocks) when the rules become
// consensus rules, and production would then drop it with a failed receipt,
// which reserves its ID for good. Admission only; production, validation and
// replay start exactly at the activation height.
const PoEAdmissionLeadBlocks = 360

// poeAdmissionActiveAt reports whether admission enforces the rules for a
// transfer checked against the block at height next.
func poeAdmissionActiveAt(next uint64) bool {
	return PoEActiveAt(next) || PoEActiveAt(next+PoEAdmissionLeadBlocks)
}

// CheckWalletTransferAdmission applies the PoE parent rules to a wallet
// transfer about to enter the mempool, against the committed tip: the
// transfer is checked as if it were the first transaction of the next block.
// A parent that is only pending in the mempool is therefore unknown;
// consensus also accepts a parent earlier in the same block, but admission
// asks clients to reference committed transactions. It returns nil until
// PoEAdmissionLeadBlocks before the activation height. It reads only the
// history index, never bp.mu, so it is safe to call from inside
// mempool.Add.
func (bp *BlockProducer) CheckWalletTransferAdmission(tx *mempool.Tx) error {
	if bp == nil || tx == nil || tx.ContractID != WalletTransferContractID {
		return nil
	}
	next, ok := bp.poeHist.nextHeight()
	if !ok {
		// The index is not built: decide activation from the published tip
		// and fail closed if the rules apply.
		next = 0
		if bp.tipHeightSet.Load() {
			next = bp.tipHeight.Load() + 1
		}
		if !poeAdmissionActiveAt(next) {
			return nil
		}
		err := fmt.Errorf("%w: index not built", poe.ErrHistoryUnavailable)
		poeStats.recordReject(err)
		return err
	}
	if !poeAdmissionActiveAt(next) {
		return nil
	}
	err := bp.checkTransferAgainstTip(tx)
	if err != nil {
		poeStats.recordReject(err)
	}
	return err
}

// checkTransferAgainstTip evaluates the rules for tx against the indexed
// committed window, ignoring activation.
func (bp *BlockProducer) checkTransferAgainstTip(tx *mempool.Tx) error {
	env, err := decodeWalletTransferEnvelope(tx.Payload)
	if err != nil {
		return err
	}
	return checkWalletTransferParents(tx.ID, env.ParentCells, poeLookup{committed: bp.poeHist.committed})
}

// shadowCheckWalletTransfer evaluates the PoE rules for an admitted transfer
// while an activation height is configured but not yet reached, and counts
// what enforcement would reject. It never rejects anything.
func (bp *BlockProducer) shadowCheckWalletTransfer(tx *mempool.Tx) {
	if bp == nil || tx == nil || tx.ContractID != WalletTransferContractID || !poeConfigured() {
		return
	}
	next, ok := bp.poeHist.nextHeight()
	if !ok || poeAdmissionActiveAt(next) {
		return
	}
	poeStats.shadowChecked.Add(1)
	if err := bp.checkTransferAgainstTip(tx); err != nil {
		poeStats.recordShadow(err)
	}
}

// CheckWalletTransferParents applies the PoE rules to a transfer that is not
// (yet) a mempool transaction -- the peer-to-peer ingress paths use it -- at
// the next height. Below the activation height it returns nil.
func (bp *BlockProducer) CheckWalletTransferParents(txID string, parents []string) error {
	if bp == nil {
		return nil
	}
	next, ok := bp.poeHist.nextHeight()
	if !ok {
		next = 0
		if bp.tipHeightSet.Load() {
			next = bp.tipHeight.Load() + 1
		}
		if !PoEActiveAt(next) {
			return nil
		}
		return fmt.Errorf("%w: index not built", poe.ErrHistoryUnavailable)
	}
	if !PoEActiveAt(next) {
		return nil
	}
	return checkWalletTransferParents(txID, parents, poeLookup{committed: bp.poeHist.committed})
}

// WalletTransferPoEAdmission wraps a mempool admission checker so that every
// wallet transfer entering the pool by any path -- not only
// walletTransferSubmitter -- passes the PoE parent rules. Other transactions
// go straight to next.
func (bp *BlockProducer) WalletTransferPoEAdmission(next func(*mempool.Tx) error) func(*mempool.Tx) error {
	return func(tx *mempool.Tx) error {
		if tx != nil && tx.ContractID == WalletTransferContractID {
			if err := bp.CheckWalletTransferAdmission(tx); err != nil {
				return err
			}
		}
		if next != nil {
			return next(tx)
		}
		return nil
	}
}

// ---------------------------------------------------------------------------
// Parent source for clients
// ---------------------------------------------------------------------------

// PoEParentRef is one committed transaction a client may name as a parent.
type PoEParentRef struct {
	ID     string `json:"id"`
	Height uint64 `json:"height"`
}

// PoEParentCandidates returns up to n distinct IDs of the most recently
// committed transactions at or below maxHeight, newest first, keeping only
// IDs that are well-formed parent references and that lie inside the window
// a transfer in the next block may reference.
func (bp *BlockProducer) PoEParentCandidates(maxHeight uint64, n int) []PoEParentRef {
	if bp == nil || n <= 0 {
		return nil
	}
	bp.mu.Lock()
	defer bp.mu.Unlock()
	if len(bp.chain) == 0 {
		return nil
	}
	tip := bp.chain[len(bp.chain)-1].Height
	var oldest uint64
	if tip+1 > poe.ParentWindowBlocks {
		oldest = tip + 1 - poe.ParentWindowBlocks
	}
	out := make([]PoEParentRef, 0, n)
	seen := make(map[string]struct{}, n)
	for i := len(bp.chain) - 1; i >= 0 && len(out) < n; i-- {
		b := bp.chain[i]
		if b == nil {
			continue
		}
		if b.Height < oldest {
			break
		}
		if b.Height > maxHeight {
			continue
		}
		for j := len(b.Transactions) - 1; j >= 0 && len(out) < n; j-- {
			tx := b.Transactions[j]
			if tx == nil || !poe.ValidParentID(tx.ID) {
				continue
			}
			if _, dup := seen[tx.ID]; dup {
				continue
			}
			seen[tx.ID] = struct{}{}
			out = append(out, PoEParentRef{ID: tx.ID, Height: b.Height})
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Counters
// ---------------------------------------------------------------------------

type poeCounters struct {
	rejected       [16]atomic.Uint64 // enforcement rejections by poe.Reasons index
	shadow         [16]atomic.Uint64 // would-be rejections before activation
	shadowChecked  atomic.Uint64
	blocksRejected atomic.Uint64
	rebuilds       atomic.Uint64
}

var poeStats poeCounters

func poeReasonIndex(err error) int {
	r := poe.Reason(err)
	if r == "" {
		r = "other"
	}
	for i, name := range poe.Reasons {
		if name == r {
			return i
		}
	}
	return len(poe.Reasons) - 1
}

func (c *poeCounters) recordReject(err error) { c.rejected[poeReasonIndex(err)].Add(1) }
func (c *poeCounters) recordShadow(err error) { c.shadow[poeReasonIndex(err)].Add(1) }

// PoEStatsSnapshot is a point-in-time copy of the PoE counters.
type PoEStatsSnapshot struct {
	ActivationHeight      uint64            `json:"activation_height"`
	Rejected              map[string]uint64 `json:"rejected"`
	ShadowWouldReject     map[string]uint64 `json:"shadow_would_reject"`
	ShadowChecked         uint64            `json:"shadow_checked"`
	ExternalBlocksRefused uint64            `json:"external_blocks_refused"`
	HistoryRebuilds       uint64            `json:"history_rebuilds"`
}

// PoEStats returns the process-wide PoE counters. Rejected counts transfers
// refused at admission or dropped during production plus transfers that made
// an external block invalid; ShadowWouldReject counts admitted transfers that
// would have failed had the rules been active (only while an activation
// height is configured and not yet reached).
func PoEStats() PoEStatsSnapshot {
	s := PoEStatsSnapshot{
		ActivationHeight:      PoEActivationHeight(),
		Rejected:              make(map[string]uint64, len(poe.Reasons)),
		ShadowWouldReject:     make(map[string]uint64, len(poe.Reasons)),
		ShadowChecked:         poeStats.shadowChecked.Load(),
		ExternalBlocksRefused: poeStats.blocksRejected.Load(),
		HistoryRebuilds:       poeStats.rebuilds.Load(),
	}
	for i, name := range poe.Reasons {
		s.Rejected[name] = poeStats.rejected[i].Load()
		s.ShadowWouldReject[name] = poeStats.shadow[i].Load()
	}
	return s
}

// PoEHistorySize reports how many committed IDs bp's index holds (0 when the
// index is not built).
func (bp *BlockProducer) PoEHistorySize() int {
	if bp == nil {
		return 0
	}
	return bp.poeHist.size()
}
