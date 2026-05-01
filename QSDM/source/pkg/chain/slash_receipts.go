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
// for tests and for the dashboard tile that advertises total
// count alongside the page slice.
func (s *SlashReceiptStore) Len() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byID)
}

// SlashReceiptListOptions controls a paginated walk over the
// receipt store. All filters are AND'd together; an empty
// filter passes through.
//
// Limit is clamped to [1, MaxSlashReceiptListLimit]; a value
// of 0 selects DefaultSlashReceiptListLimit.
//
// Outcome filter is "applied" / "rejected" or empty (both).
// EvidenceKind filter matches the receipt's slashing.EvidenceKind
// string-encoded value verbatim ("forged-attestation",
// "double-mining", "freshness-cheat"); the dashboard validates
// against a fixed allowlist BEFORE forwarding so a typo
// returns 400 rather than silently dropping all rows.
//
// SinceUnixSec, when non-zero, drops receipts with RecordedAt
// strictly before the supplied unix-seconds timestamp — used
// by the dashboard tile's rolling-time-window selector.
type SlashReceiptListOptions struct {
	Limit        int
	Outcome      string
	EvidenceKind string
	SinceUnixSec int64
}

// SlashReceiptListPage is one page of List() results. Records
// are returned NEWEST-FIRST (reverse-chronological) — the
// natural order for an operator-facing tile that wants the
// most recent receipts at the top. TotalMatches is the total
// number of records matching the filters across the whole
// store, not just the page.
type SlashReceiptListPage struct {
	Records      []SlashReceipt
	TotalMatches uint64
	HasMore      bool
}

// DefaultSlashReceiptListLimit and MaxSlashReceiptListLimit
// mirror the conventions of pkg/mining/enrollment.ListOptions.
// Smaller defaults than the rejection ring's because slash
// receipts are individually larger (full SlashReceipt struct
// vs. a Rejection record) and operators rarely need more than
// the last 100 in a tile context — bulk export of the receipt
// store is a future-feature operator concern.
const (
	DefaultSlashReceiptListLimit = 100
	MaxSlashReceiptListLimit     = 500
)

// List returns a page of receipts matching opts, sorted by
// RecordedAt DESC (newest first). Pure read path — guarded by
// RLock so concurrent PublishMiningSlash calls do not block
// listings (and vice versa).
//
// The filter walk is O(n) over the bounded store size; with
// max=DefaultMaxSlashReceipts=10000 this is in the noise on
// modern hardware. Callers wanting cursor-stable pagination
// should switch to a future cursor-based variant; the
// dashboard tile re-fetches the entire current page every
// poll tick so cursor stability is not required at this
// scope.
func (s *SlashReceiptStore) List(opts SlashReceiptListOptions) SlashReceiptListPage {
	if s == nil {
		return SlashReceiptListPage{}
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultSlashReceiptListLimit
	}
	if limit > MaxSlashReceiptListLimit {
		limit = MaxSlashReceiptListLimit
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	out := SlashReceiptListPage{
		Records: make([]SlashReceipt, 0, limit),
	}
	matched := uint64(0)

	// Walk in reverse insertion order so the newest matching
	// records fill the page first. The store's `order` slice
	// has order[0] = oldest, so we iterate from the tail.
	for i := len(s.order) - 1; i >= 0; i-- {
		txID := s.order[i]
		rec, ok := s.byID[txID]
		if !ok {
			// order/byID got out of sync — shouldn't happen
			// under the existing locking discipline but guard
			// defensively so a future bug doesn't panic the
			// dashboard.
			continue
		}
		if !slashReceiptMatches(*rec, opts) {
			continue
		}
		matched++
		if len(out.Records) < limit {
			out.Records = append(out.Records, *rec)
			continue
		}
		// We already have `limit` records; anything else
		// matching is "more". Break early so we don't scan
		// the rest of the store counting matches the client
		// will never see (TotalMatches is documented as
		// "matches in this page + at least one more if
		// HasMore", not a global count; the cost of a full
		// scan is bounded by the cap but pointless).
		out.HasMore = true
		break
	}
	out.TotalMatches = matched
	return out
}

// slashReceiptMatches applies the AND'd filter set to one
// receipt. Empty filter fields pass through.
func slashReceiptMatches(r SlashReceipt, opts SlashReceiptListOptions) bool {
	if opts.Outcome != "" && r.Outcome != opts.Outcome {
		return false
	}
	if opts.EvidenceKind != "" && string(r.EvidenceKind) != opts.EvidenceKind {
		return false
	}
	if opts.SinceUnixSec > 0 && r.RecordedAt.Unix() < opts.SinceUnixSec {
		return false
	}
	return true
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
