package chain

// slash_receipts.go — in-memory receipt store for v2-mining
// slash transactions.
//
// Why a receipt is the right model:
//
//   The slash applier emits a MiningSlashEvent on every
//   "applied" or "rejected" outcome. Operators who submitted
//   the evidence (and ops dashboards reconciling indexers
//   against on-chain state) want to look up "what happened to
//   tx X?" without subscribing to the event stream from boot.
//   A keyed-by-tx_id store gives them that read path.
//
//   The store is the natural counterpart to the
//   /api/v1/mining/enrollment/{node_id} endpoint: every write
//   endpoint should have a query counterpart, and the slash
//   write path (POST /api/v1/mining/slash) lacked one until
//   this commit.
//
// Why in-memory + bounded (rather than on-chain):
//
//   - Receipts are operator-facing telemetry, not consensus
//     state. Nothing on-chain depends on them; an indexer that
//     wants permanent records walks the block stream itself.
//   - Bounding at MaxReceipts caps memory exposure to a known
//     ceiling (FIFO eviction), so a malicious slasher cannot
//     OOM the validator by submitting receipt churn.
//   - On-disk persistence is a follow-up. The interface here
//     is small enough to swap a file-backed implementation in
//     without changing the api/v1/mining/slash/{tx_id}
//     handler.
//
// Concurrency model:
//
//   The store implements ChainEventPublisher so it can be
//   composed into the SlashApplier.Publisher chain via
//   NewCompositePublisher alongside the monitoring publisher.
//   PublishMiningSlash is the only writer; lookups are
//   guarded by the same RWMutex so a concurrent scan from a
//   handler cannot tear a half-inserted receipt.
//
//   The store keeps insertion order in a doubly-linked list
//   (via slice index) so eviction is O(1) once the cap is hit
//   — see evictOldestLocked.

import (
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
)

// DefaultMaxSlashReceipts bounds the in-memory store at a
// value that comfortably covers the operator's recent ops
// window without exposing a memory pressure surface. 10000
// receipts × ~512 bytes/receipt ≈ 5 MiB, which is negligible
// against a validator's normal heap.
//
// Tunable via NewSlashReceiptStore for tests probing
// boundary behaviour.
const DefaultMaxSlashReceipts = 10000

// SlashReceipt is the operator-facing record of a single
// slash transaction's outcome. Captures every field a
// MiningSlashEvent carries minus the non-stable Err pointer
// (we materialise it as a string so the receipt is stable
// once stored — Err interfaces are not retention-safe per
// the ChainEventPublisher contract).
//
// Field order is API-stable; new fields are additive at the
// end with zero values that are safe defaults.
type SlashReceipt struct {
	// TxID is the primary key — the mempool tx id the
	// submitter posted. Always populated.
	TxID string

	// Outcome is "applied" or "rejected".
	Outcome string

	// RecordedAt is the wall-clock time the store first saw
	// this tx_id. Useful for ordering receipts independent of
	// chain height (e.g. "show me receipts from the last
	// hour"). Stored on first PublishMiningSlash; subsequent
	// duplicate-id publishes (which shouldn't normally happen)
	// overwrite the receipt body but preserve RecordedAt.
	RecordedAt time.Time

	// Height is the chain height at which the applier
	// processed the slash.
	Height uint64

	// Slasher is the address that submitted the slash tx.
	Slasher string

	// NodeID is the offending miner's node_id. Empty on the
	// decode-failed and wrong-contract reject paths.
	NodeID string

	// EvidenceKind names the slash flavour. Empty on
	// payload-decode-failed.
	EvidenceKind slashing.EvidenceKind

	// SlashedDust is the actually-forfeited stake on
	// "applied". Zero on "rejected".
	SlashedDust uint64

	// RewardedDust is the share paid to the slasher on
	// "applied".
	RewardedDust uint64

	// BurnedDust = SlashedDust - RewardedDust.
	BurnedDust uint64

	// AutoRevoked is true when the slash drained the offender
	// below the auto-revoke threshold and the record was
	// transitioned into the unbond window in the same tx.
	AutoRevoked bool

	// AutoRevokeRemainingDust is the stake still locked in the
	// auto-revoked record.
	AutoRevokeRemainingDust uint64

	// RejectReason carries the monitoring reason tag on
	// "rejected" outcomes (matches one of the
	// SlashRejectReason* constants in events.go). Empty on
	// "applied".
	RejectReason string

	// Err is the rejection error materialised as a string.
	// Empty on "applied". Stored as a string so the receipt
	// outlives the underlying error — pkg/chain documents the
	// MiningSlashEvent.Err field as not retention-safe.
	Err string
}

// SlashReceiptStore is the in-memory bounded keyed-by-tx_id
// store. Construct via NewSlashReceiptStore; install on the
// SlashApplier via NewCompositePublisher composition.
//
// Zero value is NOT usable; the unexported fields require
// initialisation through the constructor.
type SlashReceiptStore struct {
	mu       sync.RWMutex
	max      int
	byID     map[string]*SlashReceipt
	order    []string // insertion order — order[0] is oldest
	nowFn    func() time.Time
}

// NewSlashReceiptStore constructs an empty store with a
// FIFO-eviction cap of `max` receipts. Pass 0 or a negative
// value to use DefaultMaxSlashReceipts.
//
// Tests can inject a deterministic `nowFn` to control the
// RecordedAt timestamp; production callers pass nil and get
// time.Now.
func NewSlashReceiptStore(max int, nowFn func() time.Time) *SlashReceiptStore {
	if max <= 0 {
		max = DefaultMaxSlashReceipts
	}
	if nowFn == nil {
		nowFn = time.Now
	}
	return &SlashReceiptStore{
		max:   max,
		byID:  make(map[string]*SlashReceipt, max),
		order: make([]string, 0, max),
		nowFn: nowFn,
	}
}

// PublishMiningSlash implements ChainEventPublisher. The
// applier calls this synchronously from inside ApplySlashTx;
// keep the work O(1) so we do not slow the apply path. Drops
// silently when ev.TxID is empty (no key to index on) — that
// branch should never fire in practice but the contract is
// defensive.
func (s *SlashReceiptStore) PublishMiningSlash(ev MiningSlashEvent) {
	if s == nil || ev.TxID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	errStr := ""
	if ev.Err != nil {
		errStr = ev.Err.Error()
	}

	rec := &SlashReceipt{
		TxID:                    ev.TxID,
		Outcome:                 ev.Outcome,
		Height:                  ev.Height,
		Slasher:                 ev.Slasher,
		NodeID:                  ev.NodeID,
		EvidenceKind:            ev.EvidenceKind,
		SlashedDust:             ev.SlashedDust,
		RewardedDust:            ev.RewardedDust,
		BurnedDust:              ev.BurnedDust,
		AutoRevoked:             ev.AutoRevoked,
		AutoRevokeRemainingDust: ev.AutoRevokeRemainingDust,
		RejectReason:            ev.RejectReason,
		Err:                     errStr,
	}
	if existing, ok := s.byID[ev.TxID]; ok {
		// Duplicate id — preserve original RecordedAt so the
		// timeline stays monotonic. Overwrite body so the
		// most recent outcome wins (this branch should not
		// fire under normal operation: the mempool dedupes
		// by tx_id, and ApplySlashTx is single-threaded per
		// applier).
		rec.RecordedAt = existing.RecordedAt
		s.byID[ev.TxID] = rec
		return
	}
	rec.RecordedAt = s.nowFn()
	if len(s.byID) >= s.max {
		s.evictOldestLocked()
	}
	s.byID[ev.TxID] = rec
	s.order = append(s.order, ev.TxID)
}

// PublishEnrollment implements ChainEventPublisher as a no-op
// — enrollment receipts are out of scope for this store. The
// composite publisher pattern means the enrollment publisher
// (if any) sees the events through its own arm.
func (s *SlashReceiptStore) PublishEnrollment(EnrollmentEvent) {}

// Lookup returns a copy of the receipt for txID, or
// (zero, false) if no receipt exists. Returning a copy (not a
// pointer) keeps the store's internal map immune to mutation
// by callers — receipts are read-only outside the publisher.
func (s *SlashReceiptStore) Lookup(txID string) (SlashReceipt, bool) {
	if s == nil || txID == "" {
		return SlashReceipt{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.byID[txID]
	if !ok {
		return SlashReceipt{}, false
	}
	return *rec, true
}

// Len returns the current number of stored receipts. Useful
// for tests and for a future /api/v1/mining/slash/receipts
// list endpoint that wants to advertise total count.
func (s *SlashReceiptStore) Len() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byID)
}

// evictOldestLocked removes the front of the FIFO. Caller
// MUST hold s.mu in write mode. O(n) on the slice shift in
// the worst case; in practice we keep the cap small enough
// that this is a no-op against allocator throughput.
func (s *SlashReceiptStore) evictOldestLocked() {
	if len(s.order) == 0 {
		return
	}
	oldest := s.order[0]
	s.order = s.order[1:]
	delete(s.byID, oldest)
}
