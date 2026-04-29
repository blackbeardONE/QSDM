// Package recentrejects implements a bounded in-memory ring
// buffer of recent §4.6 attestation rejections.
//
// Why this package exists:
//
//	The §4.6 arch-spoof gate, the hashrate-band gate, and (after
//	commit 0638717) the CC-path leaf-cert subject check all
//	already increment Prometheus counters via the existing
//	pkg/mining/metrics.go path. The counters answer "how many
//	rejections happened in the last 5 minutes, by reason?" but
//	NOT "which miner was it, what arch did they claim, what did
//	the leaf-cert subject look like?" — incident response needs
//	per-event detail the metrics layer is structurally unable to
//	carry.
//
//	This store fills exactly that gap: a small (default 1024-slot)
//	FIFO ring of structured rejection records, queryable through
//	GET /api/v1/attest/recent-rejections. It is operator-facing
//	telemetry, not consensus state — nothing on-chain depends on
//	it, and the producer side feeds the same data into the
//	Prometheus counters in parallel so the two views never drift.
//
// Design constraints (carried over from chain.SlashReceiptStore):
//
//   - In-memory + bounded. Per-rejection footprint is small (~256
//     bytes including the labels) and the cap is conservative;
//     1024 records × 256 B ≈ 256 KiB even saturated. A malicious
//     miner spamming forged proofs cannot OOM the validator.
//
//   - Append-only with monotonic Seq. Rejections have no natural
//     primary key (multiple miners can produce identical-looking
//     forged attestations within the same second), so we assign a
//     uint64 sequence on insert and use it for cursor pagination.
//     Wraparound at 2^64 - 1 is theoretical only — at 1M
//     rejections/sec it would take ~585k years.
//
//   - O(1) append, O(eviction-cap) on overflow (one slice shift).
//     Looking up by Seq for cursor pagination is O(log n) via a
//     binary search on the sorted slice.
//
// What is NOT in scope:
//
//   - Persistence. The ring is volatile; restart wipes it. A
//     future on-disk implementation can plug behind the same
//     RejectionRecorder interface in pkg/mining without changing
//     the handler.
//
//   - Per-rejection PII. The store records public-by-design
//     fields the proof envelope already carried (gpu_arch,
//     gpu_name, leaf cert subject CN, miner_addr, height). It
//     does NOT capture HMAC keys, cert chains, or raw bundle
//     bytes; those would expand the footprint without operator
//     value.
package recentrejects

import (
	"sort"
	"sync"
	"time"
)

// DefaultMaxRejections caps the in-memory ring at a value that
// covers a realistic operator triage window without exposing a
// memory pressure surface. 1024 records × ~256 bytes/record ≈
// 256 KiB.
//
// Tunable via NewStore for tests and high-volume validators.
const DefaultMaxRejections = 1024

// RejectionKind enumerates the §4.6 rejection sites this ring
// observes. Stable wire format — JSON-serialised verbatim by
// pkg/api's view shape, parsed by qsdmcli, keyed-on by
// dashboards. Adding a new kind is non-breaking; renaming or
// removing one is.
type RejectionKind string

const (
	// KindArchSpoofUnknown — Attestation.GPUArch was outside
	// the closed-enum allowlist. Caught by archcheck.ValidateOuterArch
	// before the per-type verifier dispatch (cheap, syntactic).
	KindArchSpoofUnknown RejectionKind = "archspoof_unknown_arch"

	// KindArchSpoofGPUNameMismatch — HMAC verifier step 8
	// rejection: the bundle's reported GPU name does not match
	// the patterns for the claimed GPUArch. Wraps
	// archcheck.ErrArchGPUNameMismatch.
	KindArchSpoofGPUNameMismatch RejectionKind = "archspoof_gpu_name_mismatch"

	// KindArchSpoofCCSubjectMismatch — CC verifier step 9:
	// leaf cert Subject contains positive NVIDIA product
	// evidence that contradicts the claimed GPUArch. Wraps
	// archcheck.ErrArchCertSubjectMismatch. Critical severity
	// — the proof has already passed cert-chain pin + AIK
	// signature, so reaching this branch means a cryptographic
	// anomaly.
	KindArchSpoofCCSubjectMismatch RejectionKind = "archspoof_cc_subject_mismatch"

	// KindHashrateOutOfBand — Attestation.ClaimedHashrateHPS
	// was outside the per-arch hashrate band (§4.6.3). Recorded
	// against the canonical arch the validator resolved to.
	KindHashrateOutOfBand RejectionKind = "hashrate_out_of_band"
)

// Rejection is the operator-facing record of a single §4.6
// rejection. Each field is either populated by the verifier
// (Kind, Reason, Arch, Height, MinerAddr) or defensively
// truncated for safety (Detail, GPUName, CertSubject).
//
// Field order is API-stable; new fields are additive at the
// end with zero values that are safe defaults.
type Rejection struct {
	// Seq is the store-assigned monotonic sequence. First
	// inserted record has Seq=1 (so 0 is a sentinel "none").
	Seq uint64

	// RecordedAt is the wall-clock time the verifier observed
	// the rejection.
	RecordedAt time.Time

	// Kind names the §4.6 site (closed enum — see RejectionKind*).
	Kind RejectionKind

	// Reason mirrors the Prometheus counter label so dashboards
	// can join: "unknown_arch" / "gpu_name_mismatch" /
	// "cc_subject_mismatch" for archspoof_*; "" for hashrate
	// (the arch label is on Arch instead).
	Reason string

	// Arch is the canonical GPU architecture string the
	// rejection was bucketed against. For ArchSpoofUnknown this
	// is the (rejected) raw operator-supplied value; for
	// HashrateOutOfBand it is the canonicalised arch.
	Arch string

	// Height is the chain height the proof claimed. 0 if
	// unavailable (rejection happened before height parsing,
	// which shouldn't occur post-fork).
	Height uint64

	// MinerAddr is the proof's miner address. Empty if the
	// envelope did not parse far enough to populate it (rare).
	MinerAddr string

	// GPUName is the bundle-reported GPU name (e.g.
	// "NVIDIA H100 80GB HBM3"). Populated on HMAC paths only;
	// CC paths produce CertSubject instead.
	GPUName string

	// CertSubject is the leaf certificate's Subject.CommonName
	// for CC-path rejections. Empty on HMAC paths.
	CertSubject string

	// Detail carries the verifier's RejectError detail string,
	// truncated to 200 runes. Useful for operators correlating
	// against validator logs without round-tripping every byte.
	Detail string
}

// Store is the bounded in-memory ring. Construct via NewStore;
// install on the verifier via mining.SetRejectionRecorder
// (composite-friendly — multiple stores can layer through the
// same interface if needed).
//
// Zero value is NOT usable; the unexported fields require
// initialisation through the constructor.
type Store struct {
	mu     sync.RWMutex
	max    int
	seq    uint64
	buf    []Rejection // append-order; index 0 is oldest
	nowFn  func() time.Time
}

// NewStore constructs an empty store with a FIFO-eviction cap
// of `max` records. Pass 0 or a negative value to use
// DefaultMaxRejections.
//
// Tests can inject a deterministic `nowFn` to control
// RecordedAt; production callers pass nil and get time.Now.
func NewStore(max int, nowFn func() time.Time) *Store {
	if max <= 0 {
		max = DefaultMaxRejections
	}
	if nowFn == nil {
		nowFn = time.Now
	}
	return &Store{
		max:   max,
		buf:   make([]Rejection, 0, max),
		nowFn: nowFn,
	}
}

// Per-field rune caps. Defined as named constants so the
// metrics adapter and tests can reference the exact same
// numbers the store enforces — a future bump to e.g. 400
// runes for Detail must update only this one location.
const (
	maxDetailRunes      = 200
	maxGPUNameRunes     = 256
	maxCertSubjectRunes = 256
)

// Record appends a new rejection to the ring, evicting the
// oldest if the cap is reached. Returns the assigned Seq.
//
// Thread-safe. Defensive: Detail is truncated to 200 runes,
// GPUName / CertSubject to 256 runes (defending against a
// malicious miner stuffing the store with megabyte attestation
// fields).
//
// The pre-truncation rune count of every non-empty observed
// field is reported to the package-level MetricsRecorder
// (see metrics.go). Operators use this telemetry to size the
// caps; production wiring lives in pkg/monitoring.
func (s *Store) Record(rec Rejection) uint64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.seq++
	rec.Seq = s.seq
	if rec.RecordedAt.IsZero() {
		rec.RecordedAt = s.nowFn()
	}

	// Observe pre-truncation lengths BEFORE we mutate the
	// fields, so the metrics layer sees the true cap pressure.
	// observeAndTruncate is a tiny helper that does the rune
	// count + cap comparison once, calls the recorder iff the
	// field is non-empty, and returns the (possibly truncated)
	// string.
	rec.Detail = observeAndTruncate(FieldDetail, rec.Detail, maxDetailRunes)
	rec.GPUName = observeAndTruncate(FieldGPUName, rec.GPUName, maxGPUNameRunes)
	rec.CertSubject = observeAndTruncate(FieldCertSubject, rec.CertSubject, maxCertSubjectRunes)

	if len(s.buf) >= s.max {
		// FIFO eviction: drop the oldest record. Single slice
		// shift; with max bounded at 1024 the cost is amortised
		// to nothing against allocator throughput.
		copy(s.buf, s.buf[1:])
		s.buf = s.buf[:len(s.buf)-1]
	}
	s.buf = append(s.buf, rec)
	return rec.Seq
}

// ListOptions controls a paginated walk over the ring.
//
// Filters are AND'd together; an empty filter passes through.
// Cursor is exclusive — the first record returned has
// Seq > Cursor (or any Seq if Cursor==0).
//
// Limit is clamped to [1, MaxListLimit]; a value of 0 selects
// DefaultListLimit. SinceUnixSec, when non-zero, drops records
// with RecordedAt strictly before the supplied unix-seconds
// timestamp.
type ListOptions struct {
	Cursor       uint64
	Limit        int
	Kind         RejectionKind
	Reason       string
	Arch         string
	SinceUnixSec int64
}

// DefaultListLimit and MaxListLimit mirror the conventions of
// pkg/mining/enrollment.ListOptions.
const (
	DefaultListLimit = 100
	MaxListLimit     = 500
)

// ListPage is one page of List() results. NextCursor is the
// Seq of the last returned record; pass it back as Cursor on
// the next call. HasMore is true iff there is at least one
// record after NextCursor matching the same filters.
type ListPage struct {
	Records      []Rejection
	NextCursor   uint64
	HasMore      bool
	TotalMatches uint64
}

// List returns a page of rejections matching opts, sorted by
// Seq ASC. Pure read path — guarded by RLock so concurrent
// Record calls do not block listings (and vice versa).
func (s *Store) List(opts ListOptions) ListPage {
	if s == nil {
		return ListPage{}
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	startIdx := 0
	if opts.Cursor > 0 {
		// Binary search for the first Seq > Cursor. The buffer
		// is monotonically Seq-ascending, so this is exact.
		startIdx = sort.Search(len(s.buf), func(i int) bool {
			return s.buf[i].Seq > opts.Cursor
		})
	}

	out := ListPage{
		Records: make([]Rejection, 0, limit),
	}
	matched := uint64(0)

	for i := startIdx; i < len(s.buf); i++ {
		rec := s.buf[i]
		if !rejectionMatches(rec, opts) {
			continue
		}
		matched++
		if len(out.Records) < limit {
			out.Records = append(out.Records, rec)
			if rec.Seq > out.NextCursor {
				out.NextCursor = rec.Seq
			}
			continue
		}
		// We already have `limit` records — anything else that
		// matches this filter is "more". Break early so we don't
		// scan the rest of the ring counting matches that the
		// client will never see (TotalMatches is documented as
		// "matches in this page + at least one more if HasMore",
		// not a global count; the cost is bounded by the cap).
		out.HasMore = true
		break
	}
	out.TotalMatches = matched
	return out
}

// Len returns the current ring depth. Useful for tests and
// dashboards advertising the buffer's saturation level.
func (s *Store) Len() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.buf)
}

// Cap returns the configured maximum.
func (s *Store) Cap() int {
	if s == nil {
		return 0
	}
	return s.max
}

// rejectionMatches applies the AND'd filter set to one record.
// Empty filter fields pass through.
func rejectionMatches(r Rejection, opts ListOptions) bool {
	if opts.Kind != "" && r.Kind != opts.Kind {
		return false
	}
	if opts.Reason != "" && r.Reason != opts.Reason {
		return false
	}
	if opts.Arch != "" && r.Arch != opts.Arch {
		return false
	}
	if opts.SinceUnixSec > 0 && r.RecordedAt.Unix() < opts.SinceUnixSec {
		return false
	}
	return true
}

// observeAndTruncate is the metrics-aware truncation helper
// used by Store.Record. It:
//
//  1. Skips empty inputs entirely (no metric, no allocation —
//     empty fields are the common case for HMAC-only paths
//     missing a CertSubject and vice versa, and folding them
//     into the "observed" denominator would skew the
//     truncation rate).
//  2. Counts pre-truncation runes once.
//  3. Reports (fieldName, runes, truncated) to the recorder.
//  4. Delegates to truncateRunes for the actual clamp.
//
// Hot path: one rune slice allocation per non-empty field
// (matching the pre-existing truncateRunes cost) plus an
// atomic.Value load + interface dispatch.
func observeAndTruncate(fieldName, s string, cap int) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	runes := len(r)
	truncated := runes > cap
	currentMetricsRecorder().ObserveField(fieldName, runes, truncated)
	if !truncated {
		return s
	}
	return string(r[:cap]) + "…"
}

// truncateRunes clamps s to at most n runes, appending a
// horizontal ellipsis when truncation occurred. Retained for
// callers outside Store.Record; Store.Record itself now uses
// observeAndTruncate so the metrics layer sees the cap
// pressure.
func truncateRunes(s string, n int) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
