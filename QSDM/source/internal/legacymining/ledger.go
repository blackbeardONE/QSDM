package legacymining

// Ledger implementation (WP5): the pending, in-flight and paid sets, the
// pending-plus-in-flight count, per-ID missedSeals, the §6.3 pre-seal checks,
// the §6.4 post-persist invariants I1-I6 and the I7 family audit. Section
// references are to the HL1 design rev 4.
//
// HL2 WP-D (version 2 configs only; a v1 config runs exactly the HL1 paths):
//   - per-owner outstanding counts (OutstandingFor), reported to the
//     OwnerGuard at Init (ResetOwnerOutstanding) and after every change
//     (SetOwnerOutstanding), and enforced again at Enqueue like MaxPending;
//   - I6 is "each paid ID's row miner_addr is the recipient" (the row's
//     miner_addr is the enrollment owner Precheck attributed at admission),
//     not the allowlist. The current enrollment is never consulted, so an
//     unenroll between accept and seal cannot FREEZE;
//   - the per-owner emitted amount of the current owner epoch
//     (OwnerEpochBlocks) and the owner_epoch_cap_cell admission check
//     (CheckOwnerEpoch).

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// LedgerConfig holds the dependencies of NewLedger.
type LedgerConfig struct {
	// Store receives MarkPaid at H8.
	Store Store
	// Guard is tripped by the Ledger, and supplies the state (stall pause),
	// the config (allowlist, MaxPending, BudgetCell) and the config hash.
	// With a version 2 config it must also be an OwnerGuard (HL2 WP-D): the
	// Ledger reports per-owner outstanding counts to it.
	Guard Guard
	// Accounts is the live account store. At H8 the Ledger reads the
	// funder's nonce and balance from it (I4, I5).
	Accounts AccountReader
	// RewardCell returns the float-CELL block reward at a height: the d7
	// driver's rewardCellForHeight. PreSeal refuses a larger rewardCell, and
	// I3 uses it as the schedule. Nil means DefaultRewardCell.
	RewardCell func(height uint64) float64
}

// DefaultRewardCell is the d7 driver's float reward at height under
// chain.DefaultEmissionSchedule with no flat override (d7 blockdriver.go
// rewardCellForHeight): BlockRewardDust(height) / DustPerCell.
func DefaultRewardCell(height uint64) float64 {
	return float64(chain.DefaultEmissionSchedule().BlockRewardDust(height)) / float64(chain.DustPerCell)
}

// AuditTxFamilies is I7 (§6.4): it returns every tx of blk that is not a
// funder heartbeat (ContractID "", Sender = Recipient = funder, Amount 0), a
// funder reward (chain.MiningRewardContractID from the funder) or a signed
// wallet transfer (chain.WalletTransferContractID). A nil tx is reported with
// empty fields. The caller runs it for local seals in every mode (H8 (a));
// OnDurableBlock also runs it in canary mode.
func AuditTxFamilies(blk *chain.Block) []FamilyViolation {
	if blk == nil {
		return nil
	}
	funder := chain.MiningRewardFunderAddress
	var out []FamilyViolation
	for _, tx := range blk.Transactions {
		switch {
		case tx == nil:
			out = append(out, FamilyViolation{})
		case tx.ContractID == "" && tx.Sender == funder && tx.Recipient == funder && tx.Amount == 0:
		case tx.ContractID == chain.MiningRewardContractID && tx.Sender == funder:
		case tx.ContractID == chain.WalletTransferContractID:
		default:
			out = append(out, FamilyViolation{TxID: tx.ID, ContractID: tx.ContractID})
		}
	}
	return out
}

// PayoutLedger is the Ledger. All state is guarded by mu (ledger.mu in §4.5).
// The Ledger calls the Guard and the Store with mu held, which the lock order
// permits; neither calls back.
//
// Once the Ledger has tripped FREEZE itself, Take returns nil for the rest of
// the process, even if the Guard failed to latch.
type PayoutLedger struct {
	store      Store
	guard      Guard
	accounts   AccountReader
	rewardCell func(uint64) float64

	mu          sync.Mutex
	initialized bool
	tripped     bool
	seq         uint64                    // Enqueue order; Take takes the oldest first
	entries     map[ProofID]*ledgerEntry  // pending and in flight
	inFlight    int                       // entries with inFlight set
	paid        map[ProofID]uint64        // paid in this process: ID -> height
	bound       map[string]*ledgerBinding // PreSeal bindings by tx ID (I1)
	boundHeight uint64
	totals      Totals
	funderBal   float64 // expected funder balance (I5)
	funderNonce uint64  // expected funder nonce (I4, PreSeal)
	nextCell    float64 // rewardCell(tip+1) after the last durable block
	haveNext    bool

	// HL2 WP-D, version 2 configs only (owners is nil with v1).
	v2         bool
	owners     OwnerGuard
	ownerCount map[string]int     // owner -> pending plus in-flight
	epoch      uint64             // the owner epoch of the next block
	epochCell  map[string]float64 // owner -> emitted in epoch
}

type ledgerEntry struct {
	minerAddr string
	seq       uint64
	inFlight  bool
	boundTx   string // the PreSeal tx that carries the ID; "" if none
	missed    int    // consecutive local durable seals missed while payouts were enabled
}

// ledgerBinding is the in-flight record of one reward tx (I1).
type ledgerBinding struct {
	sender, recipient, contractID string
	nonce                         uint64
	amountBits, feeBits           uint64
	payload                       []byte
	ids                           []ProofID
}

func (b *ledgerBinding) matches(tx *mempool.Tx) bool {
	return tx.Sender == b.sender && tx.Recipient == b.recipient && tx.ContractID == b.contractID &&
		tx.Nonce == b.nonce && math.Float64bits(tx.Amount) == b.amountBits &&
		math.Float64bits(tx.Fee) == b.feeBits && bytes.Equal(tx.Payload, b.payload)
}

var _ Ledger = (*PayoutLedger)(nil)

// NewLedger returns an uninitialised Ledger. Store, Guard and Accounts are
// required.
func NewLedger(cfg LedgerConfig) (*PayoutLedger, error) {
	if cfg.Store == nil || cfg.Guard == nil || cfg.Accounts == nil {
		return nil, errors.New("legacymining: NewLedger requires Store, Guard and Accounts")
	}
	rc := cfg.RewardCell
	if rc == nil {
		rc = DefaultRewardCell
	}
	l := &PayoutLedger{
		store:      cfg.Store,
		guard:      cfg.Guard,
		accounts:   cfg.Accounts,
		rewardCell: rc,
		entries:    make(map[ProofID]*ledgerEntry),
		paid:       make(map[ProofID]uint64),
		ownerCount: make(map[string]int),
		epochCell:  make(map[string]float64),
	}
	if cfg.Guard.Config().Version == ConfigVersion2 {
		og, ok := cfg.Guard.(OwnerGuard)
		if !ok {
			return nil, errors.New("legacymining: NewLedger: a version 2 config requires a Guard that implements OwnerGuard")
		}
		l.v2, l.owners = true, og
	}
	return l, nil
}

// tripLocked trips FREEZE with "<cause>:<detail>".
func (l *PayoutLedger) tripLocked(cause, detail string) {
	l.tripped = true
	if detail != "" {
		cause += ":" + detail
	}
	l.guard.Freeze(cause)
}

// Init implements Ledger. A malformed s trips FREEZE (CauseReconcile), leaves
// the Ledger uninitialised and returns an error wrapping ErrReconcile.
func (l *PayoutLedger) Init(s LedgerInit) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.initialized {
		return fmt.Errorf("%w: ledger already initialized", ErrInvariant)
	}
	fail := func(format string, a ...any) error {
		detail := fmt.Sprintf(format, a...)
		l.tripLocked(CauseReconcile, "ledger-init: "+detail)
		return fmt.Errorf("%w: ledger init: %s", ErrReconcile, detail)
	}
	if s.Totals.ConfigSHA256 != l.guard.ConfigHash() {
		return fail("totals are for config %x, guard has %x", s.Totals.ConfigSHA256, l.guard.ConfigHash())
	}
	if !ledgerFinite(s.FunderBalance) || !ledgerFinite(s.Totals.Emitted) || s.Totals.Emitted < 0 {
		return fail("funder balance %v or emitted %v is not a finite amount", s.FunderBalance, s.Totals.Emitted)
	}
	entries := make(map[ProofID]*ledgerEntry, len(s.Pending))
	counts := make(map[string]int)
	for i, rec := range s.Pending {
		switch {
		case rec.PaidTxID != "":
			return fail("pending record %x is paid by %s", rec.ProofID, rec.PaidTxID)
		case rec.MinerAddr == "":
			return fail("pending record %x has no miner address", rec.ProofID)
		case entries[rec.ProofID] != nil:
			return fail("pending record %x is repeated", rec.ProofID)
		}
		entries[rec.ProofID] = &ledgerEntry{minerAddr: rec.MinerAddr, seq: uint64(i) + 1}
		counts[rec.MinerAddr]++
	}
	epochCell := make(map[string]float64)
	if l.v2 {
		for owner, cell := range s.OwnerEmitted {
			if !ledgerFinite(cell) || cell < 0 {
				return fail("owner %s emitted %v in the owner epoch is not a finite amount", owner, cell)
			}
			if cell > 0 {
				epochCell[owner] = cell
			}
		}
	}
	l.entries = entries
	l.ownerCount = counts
	l.epoch = OwnerEpochOf(s.Tip + 1)
	l.epochCell = epochCell
	l.seq = uint64(len(s.Pending))
	l.inFlight = 0
	l.bound = nil
	l.totals = s.Totals
	l.funderBal = s.FunderBalance
	l.funderNonce = s.FunderNonce
	l.initialized = true
	if l.v2 {
		snapshot := make(map[string]int, len(counts))
		for owner, n := range counts {
			snapshot[owner] = n
		}
		l.owners.ResetOwnerOutstanding(snapshot)
	}
	return nil
}

// Outstanding implements Sink: pending plus in-flight.
func (l *PayoutLedger) Outstanding() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

// OutstandingFor implements OwnerSink: owner's pending plus in-flight.
func (l *PayoutLedger) OutstandingFor(owner string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ownerCount[owner]
}

// CheckOwnerEpoch implements OwnerSink (HL2 WP-D).
func (l *PayoutLedger) CheckOwnerEpoch(owner string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.v2 {
		return nil
	}
	limit := l.guard.Config().OwnerEpochCapCell
	if got := l.epochCell[owner]; got >= float64(limit) {
		first := l.epoch * OwnerEpochBlocks
		return &Rejection{Kind: KindOwnerRateLimited, Detail: fmt.Sprintf(
			"owner epoch cap: %v of %d CELL emitted in owner epoch %d (heights %d..%d); admission resumes at height %d",
			got, limit, l.epoch, first, first+OwnerEpochBlocks-1, first+OwnerEpochBlocks)}
	}
	return nil
}

// OwnerEpochEmitted returns the current owner epoch and the amount emitted to
// owner in it (always 0 with a v1 config). For status and metrics.
func (l *PayoutLedger) OwnerEpochEmitted(owner string) (epoch uint64, cell float64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.epoch, l.epochCell[owner]
}

// OwnerEpochStats aggregates the current owner epoch over every owner (HL2
// WP-H metrics). With a v1 config it is the zero value.
func (l *PayoutLedger) OwnerEpochStats() OwnerEpochStats {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.v2 {
		return OwnerEpochStats{}
	}
	s := OwnerEpochStats{Epoch: l.epoch}
	limit := float64(l.guard.Config().OwnerEpochCapCell)
	for _, cell := range l.epochCell {
		if !(cell > 0) {
			continue
		}
		s.Owners++
		s.Total += cell
		s.Max = max(s.Max, cell)
		if cell >= limit {
			s.AtCap++
		}
	}
	return s
}

// setOwnerLocked reports owner's count to the OwnerGuard (v2 only).
func (l *PayoutLedger) setOwnerLocked(owner string) {
	if l.v2 {
		l.owners.SetOwnerOutstanding(owner, l.ownerCount[owner])
	}
}

// Enqueue implements Sink. Besides an uninitialised Ledger and a held ID, it
// refuses (and trips FREEZE with CauseEnqueue) a paid record, a record with
// no miner address or another config hash, an ID already paid in this
// process, and a count already at MaxPending (the §4.1 step 6 check must
// still hold at step 9).
func (l *PayoutLedger) Enqueue(rec Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.initialized {
		l.tripLocked(CauseEnqueue, fmt.Sprintf("%x: ledger not initialized", rec.ProofID))
		return fmt.Errorf("%w: enqueue %x", ErrNotInitialized, rec.ProofID)
	}
	var detail string
	_, isPaid := l.paid[rec.ProofID]
	switch {
	case rec.PaidTxID != "":
		detail = "record is paid"
	case rec.MinerAddr == "":
		detail = "record has no miner address"
	case l.entries[rec.ProofID] != nil:
		detail = "ID is already pending or in flight"
	case isPaid:
		detail = "ID is already paid"
	case rec.ConfigSHA256 != l.totals.ConfigSHA256:
		detail = fmt.Sprintf("record config %x is not the active window %x", rec.ConfigSHA256, l.totals.ConfigSHA256)
	case len(l.entries) >= l.guard.Config().MaxPending:
		detail = fmt.Sprintf("pending plus in-flight %d is at max_pending", len(l.entries))
	case l.v2 && l.ownerCount[rec.MinerAddr] >= l.guard.Config().MaxPendingPerOwner:
		// miningsvc checked the guard's copy of this count under submitMu
		// (CheckOwnerPending), so it still holds here unless the two
		// disagree: an invariant violation, like max_pending above.
		detail = fmt.Sprintf("owner %s pending plus in-flight %d is at max_pending_per_owner", rec.MinerAddr, l.ownerCount[rec.MinerAddr])
	}
	if detail != "" {
		l.tripLocked(CauseEnqueue, fmt.Sprintf("%x: %s", rec.ProofID, detail))
		return fmt.Errorf("%w: enqueue %x: %s", ErrInvariant, rec.ProofID, detail)
	}
	l.seq++
	l.entries[rec.ProofID] = &ledgerEntry{minerAddr: rec.MinerAddr, seq: l.seq}
	l.ownerCount[rec.MinerAddr]++
	l.setOwnerLocked(rec.MinerAddr)
	l.totals.Proofs++
	l.guard.ObserveTotals(l.totals, l.observeCellLocked())
	return nil
}

// observeCellLocked is the rewardCell passed to ObserveTotals after Enqueue:
// the next height's reward once a durable block has been seen, and otherwise
// RewardCell(1), the schedule's largest (so the most conservative) value.
func (l *PayoutLedger) observeCellLocked() float64 {
	if l.haveNext {
		return l.nextCell
	}
	return l.rewardCell(1)
}

// Totals implements Ledger.
func (l *PayoutLedger) Totals() Totals {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.totals
}

// Take implements Ledger. It takes at most MaxPayloadIDs IDs per address,
// oldest first; the rest stay pending. It returns nil before Init, after the
// Ledger has tripped FREEZE, or while the guard state disables payouts. A Take
// while IDs are still in flight or bound (no Requeue since the last Take)
// trips FREEZE (CausePreSeal) and returns nil.
func (l *PayoutLedger) Take() []Claim {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.initialized || l.tripped {
		return nil
	}
	if l.inFlight > 0 || len(l.bound) > 0 {
		l.tripLocked(CausePreSeal, "take: IDs are already in flight; Requeue was not called")
		return nil
	}
	if len(l.entries) == 0 || !l.guard.State().PayoutsEnabled() {
		return nil
	}
	type item struct {
		id ProofID
		e  *ledgerEntry
	}
	byAddr := make(map[string][]item)
	for id, e := range l.entries {
		byAddr[e.minerAddr] = append(byAddr[e.minerAddr], item{id, e})
	}
	addrs := make([]string, 0, len(byAddr))
	for a := range byAddr {
		addrs = append(addrs, a)
	}
	sort.Strings(addrs)
	claims := make([]Claim, 0, len(addrs))
	for _, a := range addrs {
		items := byAddr[a]
		sort.Slice(items, func(i, j int) bool { return items[i].e.seq < items[j].e.seq })
		if len(items) > MaxPayloadIDs {
			items = items[:MaxPayloadIDs]
		}
		ids := make([]ProofID, len(items))
		for i, it := range items {
			it.e.inFlight = true
			ids[i] = it.id
		}
		sort.Slice(ids, func(i, j int) bool { return bytes.Compare(ids[i][:], ids[j][:]) < 0 })
		l.inFlight += len(items)
		claims = append(claims, Claim{MinerAddr: a, IDs: ids})
	}
	return claims
}

// PreSeal implements Ledger. Beyond the contract's list it also requires
// each tx to be a well-formed reward from the funder (contract, sender, zero
// fee, the derived RewardIDFormat ID, a strict LMP1 payload), nonces
// consecutive from the expected funder nonce, one tx per recipient, an
// allowlisted recipient (I6 before sealing; with a v2 config, I6 is that
// every ID's row miner_addr is the recipient, which PreSeal checks for every
// version), and rewardCell within RewardCell(height).
func (l *PayoutLedger) PreSeal(height uint64, rewardCell float64, txs []*mempool.Tx) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	bindings, detail := l.preSealLocked(height, rewardCell, txs)
	if detail != "" {
		l.requeueLocked()
		l.tripLocked(CausePreSeal, detail)
		return fmt.Errorf("%w: %s", ErrPreSeal, detail)
	}
	for txID, b := range bindings {
		for _, id := range b.ids {
			l.entries[id].boundTx = txID
		}
	}
	l.bound = bindings
	l.boundHeight = height
	return nil
}

func (l *PayoutLedger) preSealLocked(height uint64, rewardCell float64, txs []*mempool.Tx) (map[string]*ledgerBinding, string) {
	if !l.initialized {
		return nil, "ledger not initialized"
	}
	if len(l.bound) > 0 {
		return nil, "txs are already bound; Requeue was not called"
	}
	if sched := l.rewardCell(height); !(rewardCell > 0) || math.IsInf(rewardCell, 0) || rewardCell > sched {
		return nil, fmt.Sprintf("reward cell %v at height %d is not within the schedule %v", rewardCell, height, sched)
	}
	cfg := l.guard.Config()
	bindings := make(map[string]*ledgerBinding, len(txs))
	recipients := make(map[string]bool, len(txs))
	inPayload := make(map[ProofID]bool)
	var sum float64
	for i, tx := range txs {
		if tx == nil {
			return nil, fmt.Sprintf("tx %d is nil", i)
		}
		var bad string
		switch {
		case tx.ContractID != chain.MiningRewardContractID:
			bad = fmt.Sprintf("contract %q is not the reward contract", tx.ContractID)
		case tx.Sender != chain.MiningRewardFunderAddress:
			bad = fmt.Sprintf("sender %q is not the funder", tx.Sender)
		case math.Float64bits(tx.Fee) != 0:
			bad = fmt.Sprintf("fee %v is not zero", tx.Fee)
		case !(tx.Amount > 0) || math.IsInf(tx.Amount, 0):
			bad = fmt.Sprintf("share %v is not positive", tx.Amount)
		case tx.Nonce != l.funderNonce+uint64(i):
			bad = fmt.Sprintf("nonce %d, want %d", tx.Nonce, l.funderNonce+uint64(i))
		case !l.v2 && !ledgerAllowlisted(cfg, tx.Recipient):
			// I6 before sealing. With a v2 config I6 is the per-ID row
			// check below ("belongs to ..., not the recipient").
			bad = fmt.Sprintf("recipient %s is not allowlisted", tx.Recipient)
		case recipients[tx.Recipient]:
			bad = fmt.Sprintf("recipient %s has more than one tx", tx.Recipient)
		}
		if bad != "" {
			return nil, fmt.Sprintf("tx %s: %s", tx.ID, bad)
		}
		ids, err := ledgerDecodePayload(tx.Payload)
		if err != nil {
			return nil, fmt.Sprintf("tx %s: %v", tx.ID, err)
		}
		if want := ledgerRewardID(tx.Nonce, tx.Recipient, tx.Payload); tx.ID != want {
			return nil, fmt.Sprintf("tx ID %q, want %q", tx.ID, want)
		}
		for _, id := range ids {
			e := l.entries[id]
			_, isPaid := l.paid[id]
			switch {
			case isPaid:
				bad = "is already paid"
			case e == nil || !e.inFlight:
				bad = "is not in flight"
			case inPayload[id]:
				bad = "is in more than one payload"
			case e.minerAddr != tx.Recipient:
				bad = fmt.Sprintf("belongs to %s, not the recipient", e.minerAddr)
			}
			if bad != "" {
				return nil, fmt.Sprintf("tx %s: ID %x %s", tx.ID, id, bad)
			}
			inPayload[id] = true
		}
		recipients[tx.Recipient] = true
		sum += tx.Amount
		bindings[tx.ID] = &ledgerBinding{
			sender: tx.Sender, recipient: tx.Recipient, contractID: tx.ContractID,
			nonce: tx.Nonce, amountBits: math.Float64bits(tx.Amount), feeBits: math.Float64bits(tx.Fee),
			payload: bytes.Clone(tx.Payload), ids: ids,
		}
	}
	if len(inPayload) != l.inFlight {
		return nil, fmt.Sprintf("%d IDs are in flight but %d are in payloads", l.inFlight, len(inPayload))
	}
	if limit := rewardCell * (1 + RewardSumSlack); !(sum <= limit) {
		return nil, fmt.Sprintf("sum of amounts %v exceeds %v", sum, limit)
	}
	if budget := float64(cfg.BudgetCell); !(l.totals.Emitted+sum <= budget) {
		return nil, fmt.Sprintf("emitted %v plus %v exceeds the budget %v", l.totals.Emitted, sum, budget)
	}
	return bindings, ""
}

// Requeue implements Ledger.
func (l *PayoutLedger) Requeue() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.requeueLocked()
}

func (l *PayoutLedger) requeueLocked() {
	for _, e := range l.entries {
		e.inFlight = false
		e.boundTx = ""
	}
	l.inFlight = 0
	l.bound = nil
}

// ledgerViolation is one failed check of OnDurableBlock. order ranks the
// invariants (I1..I7), and the lowest-ranked violation is the FREEZE cause.
type ledgerViolation struct {
	order  int
	cause  string
	detail string
}

// ledgerReward is one reward-contract tx of a durable block.
type ledgerReward struct {
	tx  *mempool.Tx
	ids []ProofID
	err error
}

// OnDurableBlock implements Ledger (H8 (b)). For every block it moves the
// LMP1 IDs of the reward-contract txs to paid (W3), adds their amounts to
// Emitted and calls Guard.ObserveTotals. A non-local block then trips
// CauseNonLocalBlock; its checks and MarkPaid are skipped, and S10-S11 at the
// next boot record its payments. A local block is checked against I1-I7, and
// MarkPaid records the payments of the IDs the Ledger held. The funder
// expectations of I4 and I5 are re-anchored to the account store after every
// block.
func (l *PayoutLedger) OnDurableBlock(blk *chain.Block, local bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.initialized {
		l.tripLocked(CauseLedgerUninit, "durable block before Init")
		return fmt.Errorf("%w: durable block before Init", ErrNotInitialized)
	}
	if blk == nil {
		l.tripLocked(CauseInvariant+":I1", "nil block")
		return fmt.Errorf("%w: I1: nil block", ErrInvariant)
	}

	var rewards []ledgerReward
	var viol []ledgerViolation
	for _, tx := range blk.Transactions {
		if tx == nil {
			continue
		}
		if tx.ContractID != chain.MiningRewardContractID {
			if local && bytes.HasPrefix(tx.Payload, []byte(PayloadTag)) {
				viol = append(viol, ledgerViolation{1, CauseInvariant + ":I1",
					fmt.Sprintf("tx %s carries an LMP1 payload outside the reward contract", tx.ID)})
			}
			continue
		}
		ids, err := ledgerDecodePayload(tx.Payload)
		rewards = append(rewards, ledgerReward{tx, ids, err})
	}
	if local {
		viol = append(viol, l.checkRewardsLocked(blk, rewards)...)
	}

	// W3: the chain is the record of payment.
	l.rollEpochLocked(blk.Height)
	var pays []Payment
	touched := make(map[string]bool)
	for _, r := range rewards {
		if r.err != nil {
			continue
		}
		for _, id := range r.ids {
			if _, done := l.paid[id]; done {
				continue
			}
			l.paid[id] = blk.Height
			if e := l.entries[id]; e != nil {
				if e.inFlight {
					l.inFlight--
				}
				delete(l.entries, id)
				l.ownerCount[e.minerAddr]--
				if l.ownerCount[e.minerAddr] <= 0 {
					delete(l.ownerCount, e.minerAddr)
				}
				touched[e.minerAddr] = true
				pays = append(pays, Payment{ProofID: id, MinerAddr: r.tx.Recipient, Height: blk.Height, TxID: r.tx.ID})
			}
		}
		l.totals.Emitted += r.tx.Amount
		if l.v2 {
			l.epochCell[r.tx.Recipient] += r.tx.Amount
		}
	}
	for owner := range touched {
		l.setOwnerLocked(owner)
	}
	l.rollEpochLocked(blk.Height + 1)

	if !local {
		l.tripLocked(CauseNonLocalBlock, fmt.Sprintf("height %d", blk.Height))
		l.reanchorLocked()
		l.observeLocked(blk.Height)
		return fmt.Errorf("%w: non-local block %d reached H8", ErrInvariant, blk.Height)
	}

	viol = append(viol, l.checkFunderLocked(blk)...)
	for _, v := range AuditTxFamilies(blk) {
		viol = append(viol, ledgerViolation{7, CauseTxFamily, fmt.Sprintf("tx %s contract %q", v.TxID, v.ContractID)})
	}

	var errs []error
	if len(viol) > 0 {
		sort.SliceStable(viol, func(i, j int) bool { return viol[i].order < viol[j].order })
		l.tripLocked(viol[0].cause, viol[0].detail)
		for _, v := range viol {
			errs = append(errs, fmt.Errorf("%w: %s: %s", ErrInvariant, v.cause, v.detail))
		}
	}
	if len(pays) > 0 {
		if err := l.store.MarkPaid(pays); err != nil {
			l.tripLocked(CauseMarkPaidIO, err.Error())
			errs = append(errs, fmt.Errorf("legacymining: mark paid at height %d: %w", blk.Height, err))
		}
	}
	l.countMissedLocked()
	l.observeLocked(blk.Height)
	return errors.Join(errs...)
}

// checkRewardsLocked is I1, I2, I3 and I6 over the reward txs of a local
// block. It runs before the IDs move to paid.
func (l *PayoutLedger) checkRewardsLocked(blk *chain.Block, rewards []ledgerReward) []ledgerViolation {
	var viol []ledgerViolation
	add := func(order int, format string, a ...any) {
		viol = append(viol, ledgerViolation{order, fmt.Sprintf("%s:I%d", CauseInvariant, order), fmt.Sprintf(format, a...)})
	}
	cfg := l.guard.Config()
	seenTx := make(map[string]bool, len(rewards))
	seenID := make(map[ProofID]bool)
	var sum float64
	for _, r := range rewards {
		tx := r.tx
		switch b := l.bound[tx.ID]; {
		case b == nil:
			add(1, "reward tx %s is not bound to an in-flight record", tx.ID)
		case seenTx[tx.ID]:
			add(1, "reward tx %s appears twice", tx.ID)
		case blk.Height != l.boundHeight:
			add(1, "reward tx %s sealed at height %d, bound for %d", tx.ID, blk.Height, l.boundHeight)
		case !b.matches(tx):
			add(1, "reward tx %s differs from its in-flight record", tx.ID)
		}
		seenTx[tx.ID] = true
		if r.err != nil {
			add(1, "reward tx %s: %v", tx.ID, r.err)
		}
		for _, id := range r.ids {
			_, isPaid := l.paid[id]
			if e := l.entries[id]; seenID[id] || isPaid {
				add(2, "ID %x in tx %s is repeated or already paid", id, tx.ID)
			} else if e == nil || !e.inFlight || e.boundTx != tx.ID {
				add(1, "ID %x in tx %s is not in flight for that tx", id, tx.ID)
			}
			seenID[id] = true
		}
		if l.v2 {
			// HL2 I6: the recipient is the row miner_addr of every ID it is
			// paid for, the owner Precheck attributed at admission. Current
			// enrollment is not consulted, so an unenroll between accept and
			// seal cannot trip this.
			for _, id := range r.ids {
				if e := l.entries[id]; e != nil && e.minerAddr != tx.Recipient {
					add(6, "ID %x in tx %s belongs to %s, not the recipient %s", id, tx.ID, e.minerAddr, tx.Recipient)
				}
			}
		} else if !ledgerAllowlisted(cfg, tx.Recipient) {
			add(6, "recipient %s of tx %s is not allowlisted", tx.Recipient, tx.ID)
		}
		sum += tx.Amount
	}
	if limit := l.rewardCell(blk.Height) * (1 + RewardSumSlack); !(sum <= limit) {
		add(3, "sum of reward amounts %v at height %d exceeds the schedule %v", sum, blk.Height, limit)
	}
	return viol
}

// checkFunderLocked is I4 and I5 for a local block, then re-anchors the
// expectations to the account store.
func (l *PayoutLedger) checkFunderLocked(blk *chain.Block) []ledgerViolation {
	funder := chain.MiningRewardFunderAddress
	var k uint64
	for _, tx := range blk.Transactions {
		if tx != nil && tx.Sender == funder {
			k++
		}
	}
	exp, replayErr := ledgerReplayFunder(l.funderBal, blk.Height, blk.Transactions)
	acc, ok := l.accounts.Get(funder)
	if !ok || acc == nil {
		return []ledgerViolation{
			{4, CauseInvariant + ":I4", "funder account missing"},
			{5, CauseInvariant + ":I5", "funder account missing"},
		}
	}
	var viol []ledgerViolation
	if want := l.funderNonce + k; acc.Nonce != want {
		viol = append(viol, ledgerViolation{4, CauseInvariant + ":I4",
			fmt.Sprintf("funder nonce %d, want %d + %d funder txs", acc.Nonce, l.funderNonce, k)})
	}
	if replayErr != nil {
		viol = append(viol, ledgerViolation{5, CauseInvariant + ":I5", replayErr.Error()})
	} else if math.Float64bits(exp) != math.Float64bits(acc.Balance) {
		viol = append(viol, ledgerViolation{5, CauseInvariant + ":I5",
			fmt.Sprintf("funder balance %v (bits %#016x), replay %v (bits %#016x)",
				acc.Balance, math.Float64bits(acc.Balance), exp, math.Float64bits(exp))})
	}
	l.funderBal, l.funderNonce = acc.Balance, acc.Nonce
	return viol
}

// reanchorLocked sets the funder expectations from the account store.
func (l *PayoutLedger) reanchorLocked() {
	if acc, ok := l.accounts.Get(chain.MiningRewardFunderAddress); ok && acc != nil {
		l.funderBal, l.funderNonce = acc.Balance, acc.Nonce
	}
}

// countMissedLocked counts a local durable seal against every ID still
// pending or in flight, while payouts are enabled, and trips CauseStall at
// StallSeals. The count pauses while FROZEN or KILLED and restarts at 0 on
// each boot (a new Ledger).
func (l *PayoutLedger) countMissedLocked() {
	if l.tripped || len(l.entries) == 0 || !l.guard.State().PayoutsEnabled() {
		return
	}
	var worstID ProofID
	var worst *ledgerEntry
	for id, e := range l.entries {
		e.missed++
		if worst == nil || e.missed > worst.missed || (e.missed == worst.missed && e.seq < worst.seq) {
			worstID, worst = id, e
		}
	}
	if worst.missed >= StallSeals {
		l.tripLocked(CauseStall, fmt.Sprintf("%x missed %d consecutive local durable seals", worstID, worst.missed))
	}
}

// rollEpochLocked starts a new owner epoch when height is in a later one
// than the current (v2 only). Heights only grow at H8, so the epoch never
// moves back.
func (l *PayoutLedger) rollEpochLocked(height uint64) {
	if !l.v2 {
		return
	}
	if e := OwnerEpochOf(height); e > l.epoch {
		l.epoch = e
		clear(l.epochCell)
	}
}

// observeLocked reports the totals with the next height's reward.
func (l *PayoutLedger) observeLocked(height uint64) {
	l.nextCell, l.haveNext = l.rewardCell(height+1), true
	l.guard.ObserveTotals(l.totals, l.nextCell)
}

// ledgerReplayFunder is the I5 replay. Starting from exp, it applies every tx
// that touches the funder, in block order, with the float64 operations
// pkg/chain performs:
//   - a funder reward (AccountStore.ApplyProtocolReward): exp -= Amount+Fee;
//   - ContractID "" and signed wallet transfers (AccountStore.ApplyTx):
//     exp -= Amount+Fee if the funder sends, then exp += Amount if it
//     receives.
//
// Any other tx that touches the funder, a reward paid to the funder (its
// credit depends on enrollment state), and integer-dust accounting at height
// are errors: no tolerance.
func ledgerReplayFunder(exp float64, height uint64, txs []*mempool.Tx) (float64, error) {
	funder := chain.MiningRewardFunderAddress
	for _, tx := range txs {
		if tx == nil || (tx.Sender != funder && tx.Recipient != funder) {
			continue
		}
		if chain.IsDustAccounting(height) {
			return exp, fmt.Errorf("integer-dust accounting is active at height %d", height)
		}
		switch {
		case tx.ContractID == chain.MiningRewardContractID && tx.Sender == funder && tx.Recipient != funder:
			exp -= tx.Amount + tx.Fee
		case tx.ContractID == "" || tx.ContractID == chain.WalletTransferContractID:
			if tx.Sender == funder {
				exp -= tx.Amount + tx.Fee
			}
			if tx.Recipient == funder {
				exp += tx.Amount
			}
		default:
			return exp, fmt.Errorf("tx %s (contract %q) touches the funder with unknown accounting", tx.ID, tx.ContractID)
		}
	}
	return exp, nil
}

// ledgerDecodePayload is a strict LMP1 decoder (§3.4): PayloadTag, a u16
// big-endian count in 1..MaxPayloadIDs, then exactly count strictly
// ascending 32-byte IDs. Errors wrap ErrBadPayload.
func ledgerDecodePayload(p []byte) ([]ProofID, error) {
	head := len(PayloadTag) + 2
	if len(p) < head || string(p[:len(PayloadTag)]) != PayloadTag {
		return nil, fmt.Errorf("%w: missing %s tag", ErrBadPayload, PayloadTag)
	}
	n := int(binary.BigEndian.Uint16(p[len(PayloadTag):head]))
	if n < 1 || n > MaxPayloadIDs {
		return nil, fmt.Errorf("%w: count %d", ErrBadPayload, n)
	}
	if len(p) != head+32*n {
		return nil, fmt.Errorf("%w: %d bytes for %d IDs", ErrBadPayload, len(p), n)
	}
	ids := make([]ProofID, n)
	for i := range ids {
		copy(ids[i][:], p[head+32*i:])
		if i > 0 && bytes.Compare(ids[i-1][:], ids[i][:]) >= 0 {
			return nil, fmt.Errorf("%w: IDs not strictly ascending at %d", ErrBadPayload, i)
		}
	}
	return ids, nil
}

// ledgerRewardID derives the reward tx ID (RewardIDFormat).
func ledgerRewardID(nonce uint64, addr string, payload []byte) string {
	sum := sha256.Sum256(payload)
	return fmt.Sprintf(RewardIDFormat, nonce, addr, hex.EncodeToString(sum[:8]))
}

func ledgerAllowlisted(cfg Config, addr string) bool {
	for _, a := range cfg.Allowed {
		if a.MinerAddr == addr {
			return true
		}
	}
	return false
}

func ledgerFinite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }
