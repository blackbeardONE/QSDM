package monitoring

// Slashing-pipeline counters. These instrument the chain-side
// path defined in pkg/chain/slash_apply.go: the slash applier
// calls into Record* on every accepted slash, every rejection,
// and every post-slash auto-revoke. The corresponding Prometheus
// exposition lives in prometheus_scrape.go.
//
// Why per-kind labels for "applied" / "drained" but a single
// "rewarded" / "burned" pair: kind labels matter for incident
// triage ("are we seeing a spike of double-mining?") whereas
// reward/burn totals are economic gauges that don't benefit
// from cardinality. Reject reasons are explicit so an operator
// can distinguish "verifier rejected forged proof" (security
// signal) from "fee invalid" (operator error) at a glance.
//
// Cardinality bound: kind labels are drawn from a fixed set of
// three EvidenceKinds plus "unknown" for forward-compat, and
// reason labels are drawn from a fixed enum. Total label
// combinations stay below 32 — well under Prometheus best-
// practice ceilings.

import "sync/atomic"

// ---------- per-EvidenceKind applied / drained ----------

var (
	slashAppliedForged       atomic.Uint64
	slashAppliedDoubleMining atomic.Uint64
	slashAppliedFreshness    atomic.Uint64
	slashAppliedUnknownKind  atomic.Uint64

	slashDrainedDustForged       atomic.Uint64
	slashDrainedDustDoubleMining atomic.Uint64
	slashDrainedDustFreshness    atomic.Uint64
	slashDrainedDustUnknownKind  atomic.Uint64
)

// RecordSlashApplied bumps the slash-applied counter for the
// given EvidenceKind and adds dust to the per-kind drained
// total. Called exactly once per successful slash from the
// applier (after the verifier passes and the stake mutation
// commits).
func RecordSlashApplied(kind string, drainedDust uint64) {
	switch kind {
	case "forged-attestation":
		slashAppliedForged.Add(1)
		slashDrainedDustForged.Add(drainedDust)
	case "double-mining":
		slashAppliedDoubleMining.Add(1)
		slashDrainedDustDoubleMining.Add(drainedDust)
	case "freshness-cheat":
		slashAppliedFreshness.Add(1)
		slashDrainedDustFreshness.Add(drainedDust)
	default:
		slashAppliedUnknownKind.Add(1)
		slashDrainedDustUnknownKind.Add(drainedDust)
	}
}

// SlashAppliedLabeled returns the (kind, count) pairs in stable
// order for Prometheus exposition.
func SlashAppliedLabeled() []struct {
	Kind string
	Val  uint64
} {
	return []struct {
		Kind string
		Val  uint64
	}{
		{"forged-attestation", slashAppliedForged.Load()},
		{"double-mining", slashAppliedDoubleMining.Load()},
		{"freshness-cheat", slashAppliedFreshness.Load()},
		{"unknown", slashAppliedUnknownKind.Load()},
	}
}

// SlashDrainedDustLabeled returns the (kind, dust) pairs in
// stable order for Prometheus exposition.
func SlashDrainedDustLabeled() []struct {
	Kind string
	Val  uint64
} {
	return []struct {
		Kind string
		Val  uint64
	}{
		{"forged-attestation", slashDrainedDustForged.Load()},
		{"double-mining", slashDrainedDustDoubleMining.Load()},
		{"freshness-cheat", slashDrainedDustFreshness.Load()},
		{"unknown", slashDrainedDustUnknownKind.Load()},
	}
}

// ---------- reward / burn economics ----------

var (
	slashRewardedDust atomic.Uint64
	slashBurnedDust   atomic.Uint64
)

// RecordSlashReward records the slasher reward + burn split
// for a single applied slash. rewardedDust + burnedDust must
// equal the total drained dust for that slash; the applier is
// responsible for the arithmetic.
func RecordSlashReward(rewardedDust, burnedDust uint64) {
	slashRewardedDust.Add(rewardedDust)
	slashBurnedDust.Add(burnedDust)
}

// SlashRewardedDustTotal returns total dust paid to slashers
// since process start.
func SlashRewardedDustTotal() uint64 { return slashRewardedDust.Load() }

// SlashBurnedDustTotal returns total dust burned (drained but
// not credited to a slasher) since process start.
func SlashBurnedDustTotal() uint64 { return slashBurnedDust.Load() }

// ---------- rejected slashes (per reason) ----------

var (
	slashRejectVerifier        atomic.Uint64
	slashRejectEvidenceReplay  atomic.Uint64
	slashRejectNodeNotEnrolled atomic.Uint64
	slashRejectDecode          atomic.Uint64
	slashRejectFee             atomic.Uint64
	slashRejectWrongContract   atomic.Uint64
	slashRejectStateLookup     atomic.Uint64
	slashRejectStakeMutation   atomic.Uint64
	slashRejectOther           atomic.Uint64
)

// Slash reject reason tags. Kept narrow so cardinality stays
// bounded and reasons map 1:1 to the rejection branches in
// pkg/chain/slash_apply.go.
const (
	SlashRejectReasonVerifier        = "verifier_failed"
	SlashRejectReasonEvidenceReplay  = "evidence_replayed"
	SlashRejectReasonNodeNotEnrolled = "node_not_enrolled"
	SlashRejectReasonDecode          = "decode_failed"
	SlashRejectReasonFee             = "fee_invalid"
	SlashRejectReasonWrongContract   = "wrong_contract"
	SlashRejectReasonStateLookup     = "state_lookup_failed"
	SlashRejectReasonStakeMutation   = "stake_mutation_failed"
	SlashRejectReasonOther           = "other"
)

// RecordSlashRejected increments the reject counter for the
// supplied reason. Unknown reasons fall into the "other"
// bucket so cardinality stays bounded if a future code path
// passes a typo.
func RecordSlashRejected(reason string) {
	switch reason {
	case SlashRejectReasonVerifier:
		slashRejectVerifier.Add(1)
	case SlashRejectReasonEvidenceReplay:
		slashRejectEvidenceReplay.Add(1)
	case SlashRejectReasonNodeNotEnrolled:
		slashRejectNodeNotEnrolled.Add(1)
	case SlashRejectReasonDecode:
		slashRejectDecode.Add(1)
	case SlashRejectReasonFee:
		slashRejectFee.Add(1)
	case SlashRejectReasonWrongContract:
		slashRejectWrongContract.Add(1)
	case SlashRejectReasonStateLookup:
		slashRejectStateLookup.Add(1)
	case SlashRejectReasonStakeMutation:
		slashRejectStakeMutation.Add(1)
	default:
		slashRejectOther.Add(1)
	}
}

// SlashRejectedLabeled returns (reason, count) pairs in stable
// order for Prometheus exposition.
func SlashRejectedLabeled() []struct {
	Reason string
	Val    uint64
} {
	return []struct {
		Reason string
		Val    uint64
	}{
		{SlashRejectReasonVerifier, slashRejectVerifier.Load()},
		{SlashRejectReasonEvidenceReplay, slashRejectEvidenceReplay.Load()},
		{SlashRejectReasonNodeNotEnrolled, slashRejectNodeNotEnrolled.Load()},
		{SlashRejectReasonDecode, slashRejectDecode.Load()},
		{SlashRejectReasonFee, slashRejectFee.Load()},
		{SlashRejectReasonWrongContract, slashRejectWrongContract.Load()},
		{SlashRejectReasonStateLookup, slashRejectStateLookup.Load()},
		{SlashRejectReasonStakeMutation, slashRejectStakeMutation.Load()},
		{SlashRejectReasonOther, slashRejectOther.Load()},
	}
}

// ---------- auto-revokes ----------

var (
	slashAutoRevokeFullDrain   atomic.Uint64
	slashAutoRevokeUnderBonded atomic.Uint64
)

const (
	SlashAutoRevokeReasonFullDrain   = "fully_drained"
	SlashAutoRevokeReasonUnderBonded = "under_bonded"
)

// RecordSlashAutoRevoke increments the post-slash auto-revoke
// counter under one of two reasons:
//   - "fully_drained": post-slash StakeDust == 0
//   - "under_bonded":  0 < StakeDust < AutoRevokeMinStakeDust
//
// Called from SlashApplier.ApplySlashTx after step 10 commits.
func RecordSlashAutoRevoke(reason string) {
	switch reason {
	case SlashAutoRevokeReasonFullDrain:
		slashAutoRevokeFullDrain.Add(1)
	default:
		slashAutoRevokeUnderBonded.Add(1)
	}
}

// SlashAutoRevokedLabeled returns (reason, count) pairs in
// stable order for Prometheus exposition.
func SlashAutoRevokedLabeled() []struct {
	Reason string
	Val    uint64
} {
	return []struct {
		Reason string
		Val    uint64
	}{
		{SlashAutoRevokeReasonFullDrain, slashAutoRevokeFullDrain.Load()},
		{SlashAutoRevokeReasonUnderBonded, slashAutoRevokeUnderBonded.Load()},
	}
}

// ---------- snapshot view (for the dashboard tile) ----------

// SlashLabeledCount is one row of the labeled counter view.
// Field tag uses the generic "label" name because the same
// shape is reused for both kind-labeled (applied / drained)
// and reason-labeled (rejected / auto_revoked) series.
// Frontends render the same column header regardless of
// which series they're displaying.
type SlashLabeledCount struct {
	Label string `json:"label"`
	Value uint64 `json:"value"`
}

// SlashMetricsView is the all-counters snapshot of the
// slashing-pipeline telemetry surface. Returned by
// SlashMetricsSnapshot for in-process consumers (the
// operator dashboard's slashing-receipts tile, primarily)
// that want a coherent view without scraping Prometheus.
//
// This is a snapshot — every field is captured atomically
// per-counter but not as a transaction across the whole
// struct. Two near-simultaneous slash applies can interleave
// such that AppliedByKind reflects an apply that has not yet
// been added to RewardedDustTotal; callers reading this
// snapshot alongside the receipt list together MUST treat
// the count and the list as independent samples (typical
// dashboard polling cadence is 2s, well below the operator's
// reaction time, so the eventual consistency window is not
// observable in practice).
//
// JSON tags below are the public dashboard contract. Adding
// a new field is non-breaking; renaming any of them is.
type SlashMetricsView struct {
	AppliedByKind       []SlashLabeledCount `json:"applied_by_kind"`
	DrainedDustByKind   []SlashLabeledCount `json:"drained_dust_by_kind"`
	RewardedDustTotal   uint64              `json:"rewarded_dust_total"`
	BurnedDustTotal     uint64              `json:"burned_dust_total"`
	RejectedByReason    []SlashLabeledCount `json:"rejected_by_reason"`
	AutoRevokedByReason []SlashLabeledCount `json:"auto_revoked_by_reason"`
}

// SlashMetricsSnapshot returns the current slashing-pipeline
// counters in a single coherent view. Safe for concurrent
// callers; all reads are atomic.Load.
//
// Label order in each labeled slice matches the corresponding
// *Labeled() function so the dashboard tile can render
// kind/reason rows in a stable order across polls.
func SlashMetricsSnapshot() SlashMetricsView {
	applied := SlashAppliedLabeled()
	drained := SlashDrainedDustLabeled()
	rejected := SlashRejectedLabeled()
	revoked := SlashAutoRevokedLabeled()

	out := SlashMetricsView{
		AppliedByKind:       make([]SlashLabeledCount, 0, len(applied)),
		DrainedDustByKind:   make([]SlashLabeledCount, 0, len(drained)),
		RewardedDustTotal:   slashRewardedDust.Load(),
		BurnedDustTotal:     slashBurnedDust.Load(),
		RejectedByReason:    make([]SlashLabeledCount, 0, len(rejected)),
		AutoRevokedByReason: make([]SlashLabeledCount, 0, len(revoked)),
	}
	for _, p := range applied {
		out.AppliedByKind = append(out.AppliedByKind, SlashLabeledCount{Label: p.Kind, Value: p.Val})
	}
	for _, p := range drained {
		out.DrainedDustByKind = append(out.DrainedDustByKind, SlashLabeledCount{Label: p.Kind, Value: p.Val})
	}
	for _, p := range rejected {
		out.RejectedByReason = append(out.RejectedByReason, SlashLabeledCount{Label: p.Reason, Value: p.Val})
	}
	for _, p := range revoked {
		out.AutoRevokedByReason = append(out.AutoRevokedByReason, SlashLabeledCount{Label: p.Reason, Value: p.Val})
	}
	return out
}

// ---------- test reset ----------

// ResetSlashMetricsForTest clears every counter in this file.
// Tests-only; production code MUST NOT call this.
func ResetSlashMetricsForTest() {
	slashAppliedForged.Store(0)
	slashAppliedDoubleMining.Store(0)
	slashAppliedFreshness.Store(0)
	slashAppliedUnknownKind.Store(0)
	slashDrainedDustForged.Store(0)
	slashDrainedDustDoubleMining.Store(0)
	slashDrainedDustFreshness.Store(0)
	slashDrainedDustUnknownKind.Store(0)
	slashRewardedDust.Store(0)
	slashBurnedDust.Store(0)
	slashRejectVerifier.Store(0)
	slashRejectEvidenceReplay.Store(0)
	slashRejectNodeNotEnrolled.Store(0)
	slashRejectDecode.Store(0)
	slashRejectFee.Store(0)
	slashRejectWrongContract.Store(0)
	slashRejectStateLookup.Store(0)
	slashRejectStakeMutation.Store(0)
	slashRejectOther.Store(0)
	slashAutoRevokeFullDrain.Store(0)
	slashAutoRevokeUnderBonded.Store(0)
}
