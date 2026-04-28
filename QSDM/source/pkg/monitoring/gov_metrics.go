package monitoring

// gov_metrics.go: governance-pipeline counters + per-param
// gauges. Mirrors slashing_metrics.go in shape; instruments
// the chain-side path defined in pkg/chain/gov_apply.go.
//
// Cardinality bound: param labels are drawn from the static
// chainparams.Registry (currently 2 entries) plus a small
// reason enum (≤8 values). Total label combinations stay well
// under Prometheus best-practice ceilings.

import (
	"sync"
	"sync/atomic"
)

// ---------- per-param staged / activated ----------
//
// Counters are stored in maps keyed by param name. The map is
// initialised lazily on first Record* call (so a binary that
// never touches gov metrics pays no startup cost) and is
// guarded by a single sync.RWMutex; the hot path (RecordGov*)
// takes the read lock to look up the atomic.Uint64 pointer
// then bumps that without the lock, so the lock is released
// during the fast-path Add.

var (
	govStagedMu        sync.RWMutex
	govStagedTotal     = make(map[string]*atomic.Uint64)
	govActivatedMu     sync.RWMutex
	govActivatedTotal  = make(map[string]*atomic.Uint64)
	govParamGaugeMu    sync.RWMutex
	govParamGaugeValue = make(map[string]*atomic.Uint64)
)

// ensureCounter returns the *atomic.Uint64 for `key` in `m`,
// creating it under `mu` if missing.
func ensureCounter(m map[string]*atomic.Uint64, mu *sync.RWMutex, key string) *atomic.Uint64 {
	mu.RLock()
	c, ok := m[key]
	mu.RUnlock()
	if ok {
		return c
	}
	mu.Lock()
	defer mu.Unlock()
	if c, ok := m[key]; ok {
		return c
	}
	c = new(atomic.Uint64)
	m[key] = c
	return c
}

// RecordGovParamStaged increments the per-param stage counter.
// Fires once per accepted `qsdm/gov/v1` param-set tx.
func RecordGovParamStaged(param string) {
	c := ensureCounter(govStagedTotal, &govStagedMu, param)
	c.Add(1)
}

// RecordGovParamActivated increments the per-param activation
// counter AND updates the per-param value gauge. Fires once
// per Promote-driven activation; the gauge is the operator-
// visible "current value" for the named parameter.
func RecordGovParamActivated(param string, value uint64) {
	c := ensureCounter(govActivatedTotal, &govActivatedMu, param)
	c.Add(1)
	g := ensureCounter(govParamGaugeValue, &govParamGaugeMu, param)
	g.Store(value)
}

// SetGovParamValue sets the gauge for `param` directly,
// bypassing the activated counter. Used by the bootstrap path
// (v2wiring.Wire) to seed gauges from the ParamStore's
// Genesis defaults BEFORE any governance tx has fired, so
// Prometheus shows the current values rather than zero.
func SetGovParamValue(param string, value uint64) {
	g := ensureCounter(govParamGaugeValue, &govParamGaugeMu, param)
	g.Store(value)
}

// GovStagedLabeled returns (param, count) pairs in stable
// order (lexicographic on param name) for Prometheus
// exposition. Used by prometheus_scrape.go.
func GovStagedLabeled() []struct {
	Param string
	Val   uint64
} {
	return labeledSnapshot(govStagedTotal, &govStagedMu)
}

// GovActivatedLabeled returns (param, count) pairs in stable
// order for Prometheus exposition.
func GovActivatedLabeled() []struct {
	Param string
	Val   uint64
} {
	return labeledSnapshot(govActivatedTotal, &govActivatedMu)
}

// GovParamValueLabeled returns (param, value) pairs in stable
// order for Prometheus exposition. Used by the gauge family
// `qsdm_gov_param_value{param}`.
func GovParamValueLabeled() []struct {
	Param string
	Val   uint64
} {
	return labeledSnapshot(govParamGaugeValue, &govParamGaugeMu)
}

// labeledSnapshot is a tiny helper that snapshots a counter
// map into a deterministic ordering. Inline to keep the
// per-metric API symmetric with the slashing metric helpers.
func labeledSnapshot(m map[string]*atomic.Uint64, mu *sync.RWMutex) []struct {
	Param string
	Val   uint64
} {
	mu.RLock()
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	mu.RUnlock()

	// Sort for deterministic exposition. The slice is small
	// (≤ registry size, currently 2), so insertion-sort would
	// be fine but sort.Strings is the canonical choice.
	sortStrings(keys)

	out := make([]struct {
		Param string
		Val   uint64
	}, len(keys))
	mu.RLock()
	defer mu.RUnlock()
	for i, k := range keys {
		c, ok := m[k]
		if !ok {
			continue
		}
		out[i] = struct {
			Param string
			Val   uint64
		}{Param: k, Val: c.Load()}
	}
	return out
}

// sortStrings is a tiny re-export of sort.Strings to keep this
// file's import list narrow (matches the slashing_metrics.go
// import-discipline convention).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// ---------- rejected gov txs (per reason) ----------

var (
	govRejectDecode        atomic.Uint64
	govRejectWrongContract atomic.Uint64
	govRejectFee           atomic.Uint64
	govRejectUnauthorized  atomic.Uint64
	govRejectNotConfigured atomic.Uint64
	govRejectHeightInPast  atomic.Uint64
	govRejectHeightTooFar  atomic.Uint64
	govRejectStageRejected atomic.Uint64
	govRejectNonceFee      atomic.Uint64
	govRejectOther         atomic.Uint64
)

// Gov reject reason tags. Mirror the chain.GovRejectReason*
// enum. The two MUST be kept in sync.
const (
	GovRejectReasonDecode        = "decode_failed"
	GovRejectReasonWrongContract = "wrong_contract"
	GovRejectReasonFee           = "fee_invalid"
	GovRejectReasonUnauthorized  = "unauthorized"
	GovRejectReasonNotConfigured = "not_configured"
	GovRejectReasonHeightInPast  = "effective_height_in_past"
	GovRejectReasonHeightTooFar  = "effective_height_too_far"
	GovRejectReasonStageRejected = "stage_rejected"
	GovRejectReasonNonceFee      = "nonce_or_fee_failed"
	GovRejectReasonOther         = "other"
)

// RecordGovParamRejected increments the reject counter for the
// supplied reason. Unknown reasons fall into "other" so
// cardinality stays bounded.
func RecordGovParamRejected(reason string) {
	switch reason {
	case GovRejectReasonDecode:
		govRejectDecode.Add(1)
	case GovRejectReasonWrongContract:
		govRejectWrongContract.Add(1)
	case GovRejectReasonFee:
		govRejectFee.Add(1)
	case GovRejectReasonUnauthorized:
		govRejectUnauthorized.Add(1)
	case GovRejectReasonNotConfigured:
		govRejectNotConfigured.Add(1)
	case GovRejectReasonHeightInPast:
		govRejectHeightInPast.Add(1)
	case GovRejectReasonHeightTooFar:
		govRejectHeightTooFar.Add(1)
	case GovRejectReasonStageRejected:
		govRejectStageRejected.Add(1)
	case GovRejectReasonNonceFee:
		govRejectNonceFee.Add(1)
	default:
		govRejectOther.Add(1)
	}
}

// GovRejectedLabeled returns (reason, count) pairs in stable
// order for Prometheus exposition.
func GovRejectedLabeled() []struct {
	Reason string
	Val    uint64
} {
	return []struct {
		Reason string
		Val    uint64
	}{
		{GovRejectReasonDecode, govRejectDecode.Load()},
		{GovRejectReasonWrongContract, govRejectWrongContract.Load()},
		{GovRejectReasonFee, govRejectFee.Load()},
		{GovRejectReasonUnauthorized, govRejectUnauthorized.Load()},
		{GovRejectReasonNotConfigured, govRejectNotConfigured.Load()},
		{GovRejectReasonHeightInPast, govRejectHeightInPast.Load()},
		{GovRejectReasonHeightTooFar, govRejectHeightTooFar.Load()},
		{GovRejectReasonStageRejected, govRejectStageRejected.Load()},
		{GovRejectReasonNonceFee, govRejectNonceFee.Load()},
		{GovRejectReasonOther, govRejectOther.Load()},
	}
}

// ---------- authority-rotation counters ----------
//
// Per-op (add/remove) counters for the authority rotation
// pipeline. Cardinality is a hard {add, remove} for op-keyed
// counters and a finite reason enum for rejected — well below
// any Prometheus best-practice ceiling.

var (
	govAuthVotedMu       sync.RWMutex
	govAuthVotedTotal    = make(map[string]*atomic.Uint64)
	govAuthCrossedMu     sync.RWMutex
	govAuthCrossedTotal  = make(map[string]*atomic.Uint64)
	govAuthActivatedMu   sync.RWMutex
	govAuthActivatedTotal = make(map[string]*atomic.Uint64)

	// Single gauge for the active AuthorityList size. Updated
	// on every add/remove activation.
	govAuthorityCountGauge atomic.Uint64
)

// RecordGovAuthorityVoted bumps the per-op vote-accepted
// counter. `op` is "add" or "remove"; unknown values fall
// into "other" so cardinality stays bounded.
func RecordGovAuthorityVoted(op string) {
	c := ensureCounter(govAuthVotedTotal, &govAuthVotedMu, normaliseAuthOp(op))
	c.Add(1)
}

// RecordGovAuthorityCrossed bumps the per-op threshold-cross
// counter. Fires exactly once per proposal that reaches
// M-of-N, regardless of whether it later activates.
func RecordGovAuthorityCrossed(op string) {
	c := ensureCounter(govAuthCrossedTotal, &govAuthCrossedMu, normaliseAuthOp(op))
	c.Add(1)
}

// RecordGovAuthorityActivated bumps the per-op activation
// counter and updates the AuthorityList-size gauge to
// `postCount`.
func RecordGovAuthorityActivated(op string, postCount uint64) {
	c := ensureCounter(govAuthActivatedTotal, &govAuthActivatedMu, normaliseAuthOp(op))
	c.Add(1)
	govAuthorityCountGauge.Store(postCount)
}

// SetAuthorityCountGauge sets the gauge directly. Used by the
// v2wiring boot path so a /metrics scrape before any rotation
// fires shows the genesis authority count.
func SetAuthorityCountGauge(n uint64) {
	govAuthorityCountGauge.Store(n)
}

// AuthorityCountGauge returns the current value of the
// `qsdm_gov_authority_count` gauge.
func AuthorityCountGauge() uint64 {
	return govAuthorityCountGauge.Load()
}

// GovAuthorityVotedLabeled returns (op, count) pairs in stable
// order for Prometheus exposition.
func GovAuthorityVotedLabeled() []struct {
	Op  string
	Val uint64
} {
	return labeledOpSnapshot(govAuthVotedTotal, &govAuthVotedMu)
}

// GovAuthorityCrossedLabeled mirrors GovAuthorityVotedLabeled
// for the `authority-staged` counter.
func GovAuthorityCrossedLabeled() []struct {
	Op  string
	Val uint64
} {
	return labeledOpSnapshot(govAuthCrossedTotal, &govAuthCrossedMu)
}

// GovAuthorityActivatedLabeled mirrors GovAuthorityVotedLabeled
// for the `authority-activated` counter.
func GovAuthorityActivatedLabeled() []struct {
	Op  string
	Val uint64
} {
	return labeledOpSnapshot(govAuthActivatedTotal, &govAuthActivatedMu)
}

// labeledOpSnapshot is the op-keyed sibling of labeledSnapshot.
// Kept distinct so the param-flavoured exposition sites don't
// have to grow an "Op" struct field.
func labeledOpSnapshot(m map[string]*atomic.Uint64, mu *sync.RWMutex) []struct {
	Op  string
	Val uint64
} {
	mu.RLock()
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	mu.RUnlock()
	sortStrings(keys)
	out := make([]struct {
		Op  string
		Val uint64
	}, len(keys))
	mu.RLock()
	defer mu.RUnlock()
	for i, k := range keys {
		c, ok := m[k]
		if !ok {
			continue
		}
		out[i] = struct {
			Op  string
			Val uint64
		}{Op: k, Val: c.Load()}
	}
	return out
}

// normaliseAuthOp coerces unrecognised op tags into "other"
// so the metric label set is bounded.
func normaliseAuthOp(op string) string {
	switch op {
	case "add", "remove":
		return op
	default:
		return "other"
	}
}

// ---------- authority-rejected counters ----------

var (
	govAuthRejectAlreadyPresent atomic.Uint64
	govAuthRejectNotPresent     atomic.Uint64
	govAuthRejectWouldEmpty     atomic.Uint64
	govAuthRejectDuplicateVote  atomic.Uint64
	govAuthRejectVoteRejected   atomic.Uint64
	govAuthRejectOther          atomic.Uint64
)

// Authority-specific reject reason tags. Mirror the
// chain.GovRejectReasonAuthority* enum.
const (
	GovRejectReasonAuthorityAlreadyPresent = "authority_already_present"
	GovRejectReasonAuthorityNotPresent     = "authority_not_present"
	GovRejectReasonAuthorityWouldEmpty     = "authority_would_empty"
	GovRejectReasonDuplicateVote           = "duplicate_vote"
	GovRejectReasonAuthorityVoteRejected   = "authority_vote_rejected"
)

// RecordGovAuthorityRejected increments the reject counter
// for the supplied authority-rotation reason. Param-side
// reject reasons (decode_failed, unauthorized, etc.) continue
// to flow through RecordGovParamRejected because the wire
// path before kind-dispatch is shared.
func RecordGovAuthorityRejected(reason string) {
	switch reason {
	case GovRejectReasonAuthorityAlreadyPresent:
		govAuthRejectAlreadyPresent.Add(1)
	case GovRejectReasonAuthorityNotPresent:
		govAuthRejectNotPresent.Add(1)
	case GovRejectReasonAuthorityWouldEmpty:
		govAuthRejectWouldEmpty.Add(1)
	case GovRejectReasonDuplicateVote:
		govAuthRejectDuplicateVote.Add(1)
	case GovRejectReasonAuthorityVoteRejected:
		govAuthRejectVoteRejected.Add(1)
	default:
		govAuthRejectOther.Add(1)
	}
}

// GovAuthorityRejectedLabeled returns (reason, count) pairs
// in stable order for Prometheus exposition.
func GovAuthorityRejectedLabeled() []struct {
	Reason string
	Val    uint64
} {
	return []struct {
		Reason string
		Val    uint64
	}{
		{GovRejectReasonAuthorityAlreadyPresent, govAuthRejectAlreadyPresent.Load()},
		{GovRejectReasonAuthorityNotPresent, govAuthRejectNotPresent.Load()},
		{GovRejectReasonAuthorityWouldEmpty, govAuthRejectWouldEmpty.Load()},
		{GovRejectReasonDuplicateVote, govAuthRejectDuplicateVote.Load()},
		{GovRejectReasonAuthorityVoteRejected, govAuthRejectVoteRejected.Load()},
		{GovRejectReasonOther, govAuthRejectOther.Load()},
	}
}
