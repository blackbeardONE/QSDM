package v2wiring

// Package v2wiring centralises the boot-time assembly of the v2
// mining surface (on-chain enrollment + slashing + observability)
// so cmd/qsdm/main.go does not have to repeat ~50 lines of
// collaborator construction. The package exists for two reasons
// the inline form does not satisfy:
//
//  1. Dependency-inversion. pkg/chain MUST NOT import
//     pkg/monitoring (the import cycle was closed via
//     chain.MetricsRecorder + chain.SetChainMetricsRecorder
//     in pkg/chain/events.go and pkg/monitoring/chain_recorder.go).
//     Wiring code that crosses BOTH packages cannot live in
//     either of them. Putting it here keeps the boundary clean.
//
//  2. Testability. The same Wire(...) call shape used by
//     production cmd/qsdm/main.go is also what the integration
//     test in v2wiring_test.go exercises. A drift between
//     production and test would be caught by the test failing,
//     not by a silent regression in mainnet.
//
// Scope:
//
//   - Constructs *enrollment.InMemoryState, *EnrollmentApplier,
//     *EnrollmentAwareApplier, optional *SlashApplier.
//   - Registers the monitoring state-provider so the four
//     `qsdm_enrollment_*` gauges populate.
//   - Composes a stacked mempool admission gate
//     (slashing > enrollment > base predicate) so each ContractID
//     family hits its own stateless validators before the
//     operator's POL/BFT gate.
//   - Wires the producer via SetHeightFn and assigns the
//     SealedBlockHook for matured-stake auto-sweep.
//   - Exposes the live mempool to the api/v1/mining/{enroll,
//     unenroll, slash} HTTP handlers via the matching
//     api.Set*Mempool() helpers; the live registry to
//     api/v1/mining/enrollment/{node_id} via
//     api.SetEnrollmentRegistry; the live lister to
//     api/v1/mining/enrollments (paginated) via
//     api.SetEnrollmentLister; and the live receipt store
//     to api/v1/mining/slash/{tx_id} via
//     api.SetSlashReceiptStore. One Wire() call lights up
//     the entire v2 mining HTTP surface (read + write).
//
// Out of scope:
//
//   - mining.VerifierConfig.Attestation wiring (that lives in
//     the mining proof submission path and is wired separately
//     in cmd/qsdmminer-* binaries).
//   - Block production lifecycle, gossip, BFT.

import (
	"errors"
	"fmt"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/governance/chainparams"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	mining "github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/recentrejects"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing/doublemining"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// Config is the input bundle Wire consumes. Every field is
// REQUIRED unless explicitly marked optional. The zero value is
// invalid and Wire returns an error on it rather than papering
// over a missing collaborator.
type Config struct {
	// Accounts is the live AccountStore the producer mutates.
	// REQUIRED. The same instance must be passed to all v2
	// appliers so balance debits land in one ledger.
	Accounts *chain.AccountStore

	// Pool is the mempool whose admission gate we compose
	// against. REQUIRED. Wire calls SetAdmissionChecker on it
	// exactly once.
	Pool *mempool.Mempool

	// BaseAdmit is the operator's pre-existing admission gate
	// (e.g. POL extension predicate, BFT-committed predicate).
	// May be nil; in that case the gate accepts every tx that
	// the enrollment validators allow.
	BaseAdmit func(*mempool.Tx) error

	// SlashRewardBPS is the basis-points reward the slasher
	// receives from each successful drain. The protocol cap is
	// chain.SlashRewardCap (50%); higher values cause
	// NewSlashApplier to panic. Use 0 for "burn everything"
	// or chain.SlashRewardCap for "max reward".
	//
	// Once governance ships (GovernanceAuthorities non-empty),
	// this is the GENESIS value seeded into the ParamStore;
	// runtime tuning happens via `qsdm/gov/v1` txs and the
	// actual SlashApplier reads come from the store. With
	// governance disabled (empty AuthorityList) this remains
	// the static value the applier uses forever.
	SlashRewardBPS uint16

	// GovernanceAuthorities is the set of addresses authorised
	// to submit `qsdm/gov/v1` parameter-set transactions. An
	// empty / nil slice DISABLES on-chain governance: gov txs
	// reject with chainparams.ErrGovernanceNotConfigured and
	// the SlashApplier reads from the static
	// SlashRewardBPS / mining.MinEnrollStakeDust defaults
	// forever (the previous, non-governance posture).
	//
	// Production wiring populates this from a chain-config
	// file or a multisig address that orchestrates the off-
	// chain proposal lifecycle. The list is dedup'd at
	// applier construction; empty strings are silently
	// dropped.
	//
	// Deliberately NOT itself governance-tunable in this
	// revision — see pkg/governance/chainparams package doc
	// for why a circular "governance can change the list of
	// governors" surface is dangerous.
	GovernanceAuthorities []string

	// GovParamStorePath is the optional filesystem path the
	// governance parameter store snapshots itself to after every
	// sealed block. When set, Wire() loads the snapshot at boot
	// (creating a fresh store with registry defaults if the path
	// is missing) and the SealedBlockHook saves a fresh
	// snapshot post-Promote so a subsequent restart resumes
	// exactly where the prior process left off. When empty
	// (default), governance state is in-memory only and lost on
	// restart — fine for ephemeral testnets, NOT acceptable for
	// production.
	//
	// Atomic write: the snapshot is written to <path>.tmp and
	// atomically renamed to <path>. A crash between Remove and
	// Rename leaves <path> missing; LoadOrNew handles that as
	// "first boot" (registry defaults), and an operator can
	// recover the prior snapshot by hand-renaming <path>.tmp.
	//
	// LogSnapshotError (below) wraps any save failure so the
	// node operator gets visibility without aborting the chain.
	GovParamStorePath string

	// LogSnapshotError is invoked when SaveSnapshot in the
	// post-seal hook returns an error (disk full, permission
	// denied, etc.). The chain continues; persistence drift
	// on a single block is recoverable on the next save.
	// Nil = drop silently. Mirrors LogSweepError's posture.
	LogSnapshotError func(height uint64, err error)

	// ForkV2TCHeight, when non-nil, seeds the genesis value of
	// the fork_v2_tc_height governance parameter
	// (MINING_PROTOCOL_V2 §4) at chain init. Pass nil to use
	// the registry default (math.MaxUint64 = TC disabled).
	// Pass a pointer to 0 to activate the Tensor-Core mixin
	// from genesis (the integration-test mode). Pass a pointer
	// to N for activation at block N.
	//
	// The seed is one-shot, equivalent to SlashRewardBPS: it
	// is applied ONLY when the loaded ParamStore is at the
	// registry default for fork_v2_tc_height. Once a governance
	// tx has activated a different value, restarts ignore this
	// field so a node operator does not silently re-overwrite
	// the chain's committed history on every reboot.
	//
	// Production mainnets bake the activation height into
	// genesis via this field once. Subsequent re-tuning (push
	// the height earlier or later) happens via `qsdm/gov/v1`
	// param-set txs and the post-Promote hook in this package
	// re-pins the runtime mining.SetForkV2TCHeight knob.
	ForkV2TCHeight *uint64

	// LogSweepError is invoked when the post-seal hook's call
	// to SweepMaturedEnrollments returns an error. Nil = drop
	// silently (matches the legacy OnSealed contract). Used
	// for operational visibility, not for retry — the next
	// sealed block re-runs the sweep.
	LogSweepError func(height uint64, err error)

	// RecentRejectionsPath is the optional filesystem path the
	// recent-rejections ring (§4.6 forensic record) persists
	// itself to. When set, Wire() opens or creates a JSONL
	// file at the path, installs it as the
	// recentrejects.Store's Persister, and replays any
	// previously-persisted records into the in-memory ring at
	// boot. When empty (default), the ring is in-memory only
	// — fine for ephemeral testnets, NOT acceptable for
	// production validators where every restart wipes
	// forensic record continuity.
	//
	// The file is bounded by a soft cap equal to the in-memory
	// ring's DefaultMaxRejections (1024 records) — when the
	// file grows past that, the next Append triggers an
	// in-place compaction (read, keep last 1024, atomic
	// rename rewrite). Worst-case on-disk footprint is ≈ 512
	// KiB before compaction fires; recovered footprint is ≈
	// 256 KiB. See pkg/mining/attest/recentrejects/persistence.go
	// for the full design rationale.
	//
	// Atomic-write posture matches GovParamStorePath: the
	// compaction step writes to <path>.tmp then renames over
	// <path>; a crash between write and rename leaves <path>
	// unchanged (operator can recover by hand).
	//
	// LogRecentRejectionsError (below) wraps any restore /
	// persist failure so the operator gets visibility without
	// us crashing the rejection hot path. Persistence errors
	// also increment qsdm_attest_rejection_persist_errors_total
	// independently for dashboard / alert use.
	RecentRejectionsPath string

	// RecentRejectionsMaxBytes is the OPTIONAL hard ceiling
	// on the JSONL log's on-disk size in bytes. Zero (the
	// default) disables the check entirely — the
	// pre-2026-04-30 posture, where the soft cap is the only
	// bound on file growth. When > 0, the FilePersister
	// refuses to write a record that would push the file
	// past the cap AFTER attempting one in-band salvage
	// compaction; the refusal increments
	// qsdm_attest_rejection_persist_hardcap_drops_total and
	// returns recentrejects.ErrHardCapExceeded to the
	// Store.Record path (which never propagates the error
	// further — the in-memory ring continues to receive
	// records, only on-disk durability is dropped).
	//
	// Production tuning: at the default softCap
	// (DefaultMaxRejections=1024) and the realistic
	// per-record size (~512 bytes), the soft-cap rewrite
	// loop keeps the file at ~512 KiB. An operator setting
	// MaxBytes=8*1024*1024 (8 MiB) gives the soft-cap
	// roughly 16x headroom — comfortable for transient
	// traffic spikes, tight enough to cap a sustained flood
	// at minute-resolution. Lower values trade durability
	// for tighter disk-quota guarantees.
	RecentRejectionsMaxBytes int64

	// RecentRejectionsRateLimitPerSec is the OPTIONAL per-miner
	// token-bucket refill rate (records/second/miner) the
	// rejection ring enforces at Store.Record() entry. Zero
	// (the default) leaves the limiter detached — the
	// pre-2026-05-01 posture where every well-formed
	// rejection enters the ring regardless of source.
	//
	// When set, records for a miner whose bucket is exhausted
	// are dropped at Record() entry: they never enter the
	// ring, never invoke the persister, and never update the
	// per-field truncation counters. Only the dedicated
	// qsdm_attest_rejection_per_miner_rate_limited_total
	// counter increments, plus the per-Store
	// rejectionStore.RateLimitedCount() mirror.
	//
	// Reasonable values for a §4.6-active validator:
	//   - 5-15 records/sec/miner sustained: covers a miner
	//     that's misconfigured-then-retrying without
	//     suppressing legitimate operator visibility.
	//   - Below 1: tightens against a quiet validator's
	//     baseline, but risks suppressing the first
	//     real-incident burst from a freshly-misconfigured
	//     fleet.
	//
	// Records with empty MinerAddr (rare envelope-parse-
	// failure case) bypass the limiter so operator
	// visibility into the parse-failure path is preserved.
	RecentRejectionsRateLimitPerSec float64

	// RecentRejectionsRateLimitBurst is the per-miner burst
	// allowance — the maximum number of tokens any single
	// miner can accumulate. Zero derives a sensible default
	// (5 × RecentRejectionsRateLimitPerSec, clamped to >=1).
	//
	// Set explicitly to override; the derivation is intended
	// for the common case where operators tune only the
	// sustained rate and accept a 5-second burst window.
	RecentRejectionsRateLimitBurst float64

	// RecentRejectionsRateLimitIdleTTL controls how long an
	// idle per-miner bucket stays in the limiter map before
	// amortized eviction. Zero uses the package default
	// (1 hour). Negative values are clamped to 0 (eviction
	// disabled — bucket map grows unboundedly; only
	// appropriate for short-lived test validators).
	//
	// Operators rotating through a fleet of one-shot miners
	// (e.g. CI test runs) can shorten this to e.g. 5 min to
	// keep the map tight; long-running production
	// validators with stable miner populations should leave
	// it at the default.
	RecentRejectionsRateLimitIdleTTL time.Duration

	// LogRecentRejectionsError is invoked on
	// RestoreFromPersister failure (boot-time replay errored)
	// and on FilePersister construction failure. Per-record
	// Append failures are NOT routed here — they fire too
	// frequently under filesystem flap to log per-event;
	// instead they bump
	// qsdm_attest_rejection_persist_errors_total which
	// dashboards / alerts consume.
	//
	// Nil = drop silently. Mirrors LogSnapshotError's posture.
	LogRecentRejectionsError func(error)
}

// Wired is the output bundle. cmd/qsdm/main.go consumes:
//
//   - .StateApplier as the chain.StateApplier passed to
//     NewBlockProducer (drop-in for the bare AccountStore).
//   - .Aware to call SetHeightFn(...) AFTER the producer is
//     constructed (canonical Phase 2c-vii pattern).
//   - .SealedBlockHook to assign to producer.OnSealedBlock.
//
// .EnrollmentState, .Slasher, and .SlashReceipts are exposed
// for tests and for future call sites that need direct
// registry access (e.g. a /api/v1/mining/enrollment/{node_id}
// GET, or an indexer that wants to drain receipts via the
// chain store directly).
type Wired struct {
	StateApplier    chain.StateApplier
	Aware           *chain.EnrollmentAwareApplier
	EnrollmentState *enrollment.InMemoryState
	Enrollment      *chain.EnrollmentApplier
	Slasher         *chain.SlashApplier
	SlashReceipts   *chain.SlashReceiptStore
	Gov             *chain.GovApplier
	GovParams       *chainparams.InMemoryParamStore
	GovAuthVotes    *chainparams.InMemoryAuthorityVoteStore

	// RecentRejections is the bounded ring of §4.6 attestation
	// rejections (arch-spoof / hashrate-band / cc-cert-subject).
	// Operator-facing telemetry only — not part of consensus
	// state. Powers GET /api/v1/attest/recent-rejections and
	// the dependency-inverted mining.RejectionRecorder hook the
	// verifier feeds on every rejection.
	RecentRejections *recentrejects.Store

	SealedBlockHook func(*chain.Block)
}

// Wire assembles the v2 mining surface against the supplied
// collaborators. Returns an error rather than panicking on
// invalid input so cmd/qsdm/main.go can degrade to v1-only mode
// (i.e. continue booting without v2 enrollment) if a collaborator
// is missing — though Validate() rejects that case for safety.
//
// SlashApplier construction is best-effort: if the production
// dispatcher cannot be built (e.g. the doublemining factory
// returns an error), Wired.Slasher is left nil and the operator
// gets a clear error from this function. The aware applier is
// still returned with slashing OFF, so v2 enrollment can run
// even if slashing wiring is broken — slash txs just bounce
// with chain.ErrSlashingNotWired until fixed.
func Wire(cfg Config) (*Wired, error) {
	if cfg.Accounts == nil {
		return nil, errors.New("v2wiring: Config.Accounts is required")
	}
	if cfg.Pool == nil {
		return nil, errors.New("v2wiring: Config.Pool is required")
	}
	if cfg.SlashRewardBPS > chain.SlashRewardCap {
		return nil, fmt.Errorf(
			"v2wiring: SlashRewardBPS=%d exceeds chain.SlashRewardCap=%d",
			cfg.SlashRewardBPS, chain.SlashRewardCap)
	}

	state := enrollment.NewInMemoryState()
	enrollAp := chain.NewEnrollmentApplier(cfg.Accounts, state)
	aware := chain.NewEnrollmentAwareApplier(cfg.Accounts, enrollAp)

	// Slasher arm. Build the production dispatcher with the
	// real registry; on error, return a clear wiring failure
	// — slashing wiring drift is exactly the kind of silent
	// regression this package exists to prevent.
	disp, err := doublemining.NewProductionSlashingDispatcher(
		enrollment.NewStateBackedRegistry(state),
		nil, // empty deny-list at boot; governance can append later.
		0,   // forgedattest cap = forgedattest.DefaultMaxSlashDust
		0,   // doublemining cap = doublemining.DefaultMaxSlashDust
	)
	if err != nil {
		return nil, fmt.Errorf("v2wiring: slash dispatcher: %w", err)
	}
	slasher := chain.NewSlashApplier(
		cfg.Accounts, state, disp, cfg.SlashRewardBPS,
	)
	aware.SetSlashApplier(slasher)

	// v2 governance arm. Construct an InMemoryParamStore and
	// seed it with the genesis defaults derived from cfg —
	// SlashRewardBPS becomes the active reward_bps, and
	// auto_revoke_min_stake_dust starts at the registry
	// default (= MIN_ENROLL_STAKE).
	//
	// The store is wired into the slash applier UNCONDITIONALLY,
	// even when GovernanceAuthorities is empty; that way the
	// reads route through ParamStore consistently and a future
	// activation of governance just needs the AuthorityList
	// populated. With governance disabled the active values
	// never change, so SlashApplier behaviour is byte-identical
	// to the pre-governance posture.
	// Load from snapshot if a persistence path is configured.
	// Empty path → fresh in-memory store with registry defaults;
	// missing file at the path → same; present file → replay
	// active+pending state. A version mismatch or corrupted JSON
	// returns a hard error here rather than silently wiping
	// state; the operator must explicitly intervene.
	govStore, govVotes, err := chainparams.LoadOrNewWith(cfg.GovParamStorePath)
	if err != nil {
		return nil, fmt.Errorf("v2wiring: gov param store: %w", err)
	}
	if cfg.SlashRewardBPS > 0 {
		// Seed reward_bps from the operator-supplied genesis
		// value ONLY when no snapshot already provided one —
		// otherwise we'd silently overwrite a previously-
		// activated governance change every time the binary
		// restarts. The "did the snapshot supply this?" check
		// is "is the loaded value still equal to the registry
		// default?". When governance has never run, that's
		// always true; once a tx promotes, it diverges.
		spec, _ := chainparams.Lookup(string(chainparams.ParamRewardBPS))
		current, _ := govStore.ActiveValue(string(chainparams.ParamRewardBPS))
		if current == spec.DefaultValue {
			govStore.SetForTesting(
				string(chainparams.ParamRewardBPS),
				uint64(cfg.SlashRewardBPS))
		}
	}

	// Same one-shot genesis-seed pattern for fork_v2_tc_height:
	// apply the operator-supplied value only if the loaded
	// store is still at the registry default. Once a governance
	// tx has promoted a different value the snapshot replays
	// it on every boot and Config.ForkV2TCHeight is ignored —
	// preserving the chain's committed activation history.
	if cfg.ForkV2TCHeight != nil {
		spec, _ := chainparams.Lookup(string(chainparams.ParamForkV2TCHeight))
		current, _ := govStore.ActiveValue(string(chainparams.ParamForkV2TCHeight))
		if current == spec.DefaultValue {
			govStore.SetForTesting(
				string(chainparams.ParamForkV2TCHeight),
				*cfg.ForkV2TCHeight)
		}
	}

	// Pin the runtime mining knob from whatever the ParamStore
	// considers active right now (genesis seed, or a previously-
	// activated governance change replayed from snapshot, or
	// the registry default = MaxUint64 = TC disabled). The
	// SealedBlockHook below re-pins after every Promote so
	// pkg/mining.IsV2TC stays consistent with on-chain state.
	if v, ok := govStore.ActiveValue(string(chainparams.ParamForkV2TCHeight)); ok {
		mining.SetForkV2TCHeight(v)
	}

	slasher.SetParamStore(govStore)

	// Seed monitoring gauges so a /metrics scrape before any
	// gov tx fires shows the genesis-time value (not zero).
	// SetGovParamValue is keyed-by-name and overwrite-safe;
	// the per-name atomic.Uint64 is created lazily.
	for _, spec := range chainparams.Registry() {
		v, ok := govStore.ActiveValue(string(spec.Name))
		if !ok {
			continue
		}
		monitoring.SetGovParamValue(string(spec.Name), v)
	}

	govApplier := chain.NewGovApplier(
		cfg.Accounts, govStore, cfg.GovernanceAuthorities,
	)
	govApplier.SetAuthorityVoteStore(govVotes)
	aware.SetGovApplier(govApplier)

	// Seed the authority-count gauge so a /metrics scrape
	// before any rotation activates shows the genesis size,
	// not zero.
	monitoring.SetAuthorityCountGauge(uint64(len(govApplier.AuthorityList())))

	// Slash receipt store. Bounded in-memory keyed by tx_id;
	// installed as a ChainEventPublisher on the slash applier
	// so every "applied" / "rejected" outcome lands here. The
	// existing applier publisher (NoopEventPublisher by
	// default — pkg/monitoring's structured-event consumer is
	// the only other current subscriber) is preserved via
	// composition. Attach BEFORE any tx flows through so the
	// receipt for the very first slash is captured.
	slashReceipts := chain.NewSlashReceiptStore(0, nil)
	slasher.Publisher = chain.NewCompositePublisher(slasher.Publisher, slashReceipts)

	// Bounded ring buffer of recent §4.6 attestation rejections.
	// Powers the per-event detail companion to the
	// qsdm_attest_archspoof_rejected_total / hashrate counters.
	// Construct BEFORE the api.Set* registrations below so the
	// adapter hand-off is atomic from the boot perspective.
	rejectionStore := recentrejects.NewStore(0, nil)

	// Optional on-disk persistence for the rejection ring.
	// When configured, every Record() additionally appends to
	// a JSONL log under cfg.RecentRejectionsPath, and the
	// ring is replayed from disk at boot so a restart does
	// not wipe forensic continuity. When unset, the ring is
	// in-memory only (the pre-2026-04-29 posture).
	//
	// FilePersister construction failure is non-fatal: we
	// surface via LogRecentRejectionsError and continue with
	// the in-memory-only ring. A persistence-broken
	// validator is a degraded operator-experience problem,
	// not a chain-correctness problem.
	if cfg.RecentRejectionsPath != "" {
		fp, err := recentrejects.NewFilePersister(cfg.RecentRejectionsPath, 0)
		if err != nil {
			if cfg.LogRecentRejectionsError != nil {
				cfg.LogRecentRejectionsError(fmt.Errorf("v2wiring: recent-rejections persister: %w", err))
			}
		} else {
			// Apply hard byte cap when configured. Setter
			// is a no-op for n <= 0, so the bare unset
			// path (cfg.RecentRejectionsMaxBytes==0) keeps
			// pre-2026-04-30 behaviour intact.
			if cfg.RecentRejectionsMaxBytes > 0 {
				fp.SetMaxBytes(cfg.RecentRejectionsMaxBytes)
			}
			rejectionStore.SetPersister(fp)
			if _, err := rejectionStore.RestoreFromPersister(); err != nil {
				if cfg.LogRecentRejectionsError != nil {
					cfg.LogRecentRejectionsError(fmt.Errorf("v2wiring: recent-rejections restore: %w", err))
				}
			}
		}
	}

	// Apply per-miner rate-limit when configured. Setter is a
	// no-op for rate <= 0 so the bare unset path
	// (cfg.RecentRejectionsRateLimitPerSec == 0) keeps the
	// pre-2026-05-01 behaviour intact: every well-formed
	// rejection enters the ring regardless of source. Burst
	// and IdleTTL pass through to the limiter's own derivation
	// logic — see recentrejects.SetRateLimit for the defaults.
	if cfg.RecentRejectionsRateLimitPerSec > 0 {
		rejectionStore.SetRateLimit(
			cfg.RecentRejectionsRateLimitPerSec,
			cfg.RecentRejectionsRateLimitBurst,
			cfg.RecentRejectionsRateLimitIdleTTL,
		)
	}

	mining.SetRejectionRecorder(miningRejectionRecorderAdapter{store: rejectionStore})

	// Monitoring gauge provider. Replaces any prior provider
	// installed by an earlier boot — the underlying atomic.Value
	// is overwrite-on-set, so multiple Wire() calls in the same
	// process (e.g. an embedded validator restart) leave the
	// gauges consistent with the most recent state.
	monitoring.SetEnrollmentStateProvider(
		monitoring.NewEnrollmentInMemoryStateProvider(state),
	)

	// Mempool admission. Two stateless layers stacked on top of
	// the operator-supplied base predicate:
	//
	//   - slashing.AdmissionChecker  (slash txs)
	//   - enrollment.AdmissionChecker (enroll/unenroll txs)
	//   - cfg.BaseAdmit               (everything else: POL/BFT)
	//
	// Each layer only intercepts its own ContractID and
	// delegates other contracts down the chain, so layer order
	// is structurally safe but kept stable for readability:
	// slash > enroll > base mirrors the conceptual blast radius
	// (a slash tx is the most consequential so its validators
	// run first).
	cfg.Pool.SetAdmissionChecker(
		chainparams.AdmissionChecker(
			slashing.AdmissionChecker(
				enrollment.AdmissionChecker(cfg.BaseAdmit))))

	// HTTP handler hookup. All four mining endpoints
	//
	//   POST /api/v1/mining/enroll
	//   POST /api/v1/mining/unenroll
	//   POST /api/v1/mining/slash
	//   GET  /api/v1/mining/enrollment/{node_id}
	//
	// are no-ops without their respective Set*() install —
	// each returns 503 Service Unavailable until set. Wired
	// together so a validator that brings up v2 enrollment
	// brings up the full read+write surface in one call.
	//
	// SetEnrollmentRegistry exposes the same *InMemoryState
	// the appliers mutate — one source of truth for chain
	// state, no separate read replica or cache.
	api.SetEnrollmentMempool(cfg.Pool)
	api.SetSlashMempool(cfg.Pool)
	api.SetEnrollmentRegistry(state)
	api.SetEnrollmentLister(state)
	api.SetSlashReceiptStore(slashReceiptAdapter{store: slashReceipts})
	api.SetRecentRejectionLister(recentRejectionListerAdapter{store: rejectionStore})

	// Governance read API. Provider snapshots the live
	// ParamStore + GovApplier authority list; both live
	// behind their own locks so the snapshot is point-in-time
	// consistent. Same indirection-via-adapter pattern as
	// slashReceiptAdapter so pkg/api stays free of
	// pkg/governance/chainparams + pkg/chain imports.
	api.SetGovernanceProvider(governanceProviderAdapter{
		store:    govStore,
		applier:  govApplier,
	})

	enrollHook := aware.SealedBlockHook(cfg.LogSweepError)
	// Compose: run the enrollment sweep first (releases
	// matured stake for the upcoming Promote arithmetic) and
	// then promote any governance changes whose
	// EffectiveHeight has been reached. Promote firing AFTER
	// the sweep matters when a `auto_revoke_min_stake_dust`
	// change activates at the same height as a record
	// matures — the next slash on the next block sees the
	// activated value, not the stale default.
	hook := func(blk *chain.Block) {
		if enrollHook != nil {
			enrollHook(blk)
		}
		if blk == nil || govApplier == nil {
			return
		}
		govApplier.PromotePending(blk.Height)

		// Re-pin the runtime mining knob from the (possibly
		// just-promoted) ParamStore. Cheap (one atomic Store)
		// and idempotent — when no fork_v2_tc_height change
		// promoted on this block, the value is unchanged and
		// the Store call is a no-op-equivalent. Doing this
		// every block (rather than inspecting the promote
		// list) keeps the runtime knob authoritatively
		// in sync with the consensus-layer ParamStore even
		// across restarts that replay a snapshot.
		if v, ok := govStore.ActiveValue(string(chainparams.ParamForkV2TCHeight)); ok {
			mining.SetForkV2TCHeight(v)
		}

		// Persist the post-promote store snapshot if a
		// persistence path is configured. Save AFTER Promote
		// so a crash between two blocks always replays a
		// snapshot that reflects every promotion the chain
		// committed up to and including the just-sealed
		// block. Save errors are surfaced via
		// LogSnapshotError but do NOT block the chain — the
		// next sealed block re-saves and recovers the state.
		if cfg.GovParamStorePath == "" {
			return
		}
		if err := chainparams.SaveSnapshotWith(
			govStore, govVotes, cfg.GovParamStorePath,
		); err != nil {
			if cfg.LogSnapshotError != nil {
				cfg.LogSnapshotError(blk.Height, err)
			}
		}
	}

	return &Wired{
		StateApplier:     aware,
		Aware:            aware,
		EnrollmentState:  state,
		Enrollment:       enrollAp,
		Slasher:          slasher,
		SlashReceipts:    slashReceipts,
		Gov:              govApplier,
		GovParams:        govStore,
		GovAuthVotes:     govVotes,
		RecentRejections: rejectionStore,
		SealedBlockHook:  hook,
	}, nil
}

// slashReceiptAdapter bridges the chain-side
// *SlashReceiptStore (which knows about chain.SlashReceipt) to
// the api-side SlashReceiptStore interface (which knows about
// api.SlashReceiptView). The adapter is the right size to
// keep pkg/api decoupled from pkg/chain — without it, pkg/api
// would have to import pkg/chain for the receipt struct, which
// would re-introduce the import-cycle pressure that
// dependency-inverted MetricsRecorder + EnrollmentRegistry
// were designed to avoid.
//
// One-method interface; lookup is the only call the handler
// makes.
type slashReceiptAdapter struct {
	store *chain.SlashReceiptStore
}

// Lookup implements api.SlashReceiptStore by translating the
// chain receipt into the api wire view. Returns ok=false on
// nil store, missing tx, or empty tx_id.
func (a slashReceiptAdapter) Lookup(txID string) (api.SlashReceiptView, bool) {
	if a.store == nil {
		return api.SlashReceiptView{}, false
	}
	rec, ok := a.store.Lookup(txID)
	if !ok {
		return api.SlashReceiptView{}, false
	}
	return api.SlashReceiptView{
		TxID:                    rec.TxID,
		Outcome:                 rec.Outcome,
		RecordedAt:              rec.RecordedAt,
		Height:                  rec.Height,
		Slasher:                 rec.Slasher,
		NodeID:                  rec.NodeID,
		EvidenceKind:            string(rec.EvidenceKind),
		SlashedDust:             rec.SlashedDust,
		RewardedDust:            rec.RewardedDust,
		BurnedDust:              rec.BurnedDust,
		AutoRevoked:             rec.AutoRevoked,
		AutoRevokeRemainingDust: rec.AutoRevokeRemainingDust,
		RejectReason:            rec.RejectReason,
		Err:                     rec.Err,
	}, true
}

// miningRejectionRecorderAdapter wraps a recentrejects.Store so
// the verifier can call mining.RejectionRecorder.Record without
// pkg/mining importing the concrete store package (which would
// close an import cycle through archcheck.Architecture).
//
// Translation is one-to-one: every wire field of
// mining.RejectionEvent maps to the same-named field on
// recentrejects.Rejection. The truncation that the store applies
// (Detail, GPUName, CertSubject) is the authoritative defence
// against malicious-miner storage stuffing — the verifier may
// pass arbitrary-length strings and the store clamps.
type miningRejectionRecorderAdapter struct {
	store *recentrejects.Store
}

// Record implements mining.RejectionRecorder. Drops silently if
// the store is nil (defensive — Wire constructs the store before
// SetRejectionRecorder, but a future re-wire path could set the
// recorder to nil mid-flight).
func (a miningRejectionRecorderAdapter) Record(ev mining.RejectionEvent) {
	if a.store == nil {
		return
	}
	a.store.Record(recentrejects.Rejection{
		Kind:        recentrejects.RejectionKind(string(ev.Kind)),
		Reason:      ev.Reason,
		Arch:        ev.Arch,
		RecordedAt:  ev.RecordedAt,
		Height:      ev.Height,
		MinerAddr:   ev.MinerAddr,
		GPUName:     ev.GPUName,
		CertSubject: ev.CertSubject,
		Detail:      ev.Detail,
	})
}

// recentRejectionListerAdapter wraps a recentrejects.Store so
// the GET /api/v1/attest/recent-rejections handler can list
// without pkg/api importing the concrete store package.
//
// The adapter mirrors slashReceiptAdapter's posture: small,
// purely translational, no business logic. Filter validation
// happens upstream in the handler; the store applies the
// AND'd filters and returns a page.
type recentRejectionListerAdapter struct {
	store *recentrejects.Store
}

// List implements api.RecentRejectionLister. Translates the
// pkg/api options shape into the store options shape, calls
// the store, and translates the page back. nil-store guard
// returns an empty page (handler treats this as "200 with
// records=[]" — same posture as a saturated store with no
// matches).
func (a recentRejectionListerAdapter) List(opts api.RecentRejectionListOptions) api.RecentRejectionListPage {
	if a.store == nil {
		return api.RecentRejectionListPage{Records: []api.RecentRejectionView{}}
	}
	page := a.store.List(recentrejects.ListOptions{
		Cursor:       opts.Cursor,
		Limit:        opts.Limit,
		Kind:         recentrejects.RejectionKind(opts.Kind),
		Reason:       opts.Reason,
		Arch:         opts.Arch,
		SinceUnixSec: opts.SinceUnixSec,
	})
	out := api.RecentRejectionListPage{
		Records:      make([]api.RecentRejectionView, 0, len(page.Records)),
		NextCursor:   page.NextCursor,
		HasMore:      page.HasMore,
		TotalMatches: page.TotalMatches,
	}
	for _, r := range page.Records {
		out.Records = append(out.Records, api.RecentRejectionView{
			Seq:         r.Seq,
			RecordedAt:  r.RecordedAt,
			Kind:        string(r.Kind),
			Reason:      r.Reason,
			Arch:        r.Arch,
			Height:      r.Height,
			MinerAddr:   r.MinerAddr,
			GPUName:     r.GPUName,
			CertSubject: r.CertSubject,
			Detail:      r.Detail,
		})
	}
	return out
}

// governanceProviderAdapter bridges the on-chain governance
// runtime (chainparams.ParamStore + chain.GovApplier) to the
// pkg/api governance read endpoints. Same boundary discipline
// as slashReceiptAdapter — keeps pkg/api free of imports on
// pkg/chain and pkg/governance/chainparams while still
// surfacing a self-consistent point-in-time snapshot.
//
// Both store and applier may be nil if v2wiring was called
// with empty cfg.GovernanceAuthorities (governance disabled
// posture). The adapter handles that gracefully — every field
// of the returned view is empty and GovernanceEnabled is false
// — so dashboards can render "governance not configured"
// without a separate null check.
type governanceProviderAdapter struct {
	store   chainparams.ParamStore
	applier *chain.GovApplier
}

// SnapshotGovernanceParams implements api.GovernanceParamsProvider.
//
// Each access against the store and the applier takes its
// own RLock; in the worst case (a Stage between the two
// reads) the snapshot will surface the post-stage Pending
// view without the post-stage Active view, which is harmless
// — Active for a staged param is unchanged until Promote
// fires. We do NOT need a global snapshot lock.
func (a governanceProviderAdapter) SnapshotGovernanceParams() api.GovernanceParamsView {
	view := api.GovernanceParamsView{
		Active:      map[string]uint64{},
		Pending:     []api.GovernancePendingView{},
		Registry:    []api.GovernanceRegistryView{},
		Authorities: []string{},
	}
	registry := chainparams.Registry()
	for _, spec := range registry {
		view.Registry = append(view.Registry, api.GovernanceRegistryView{
			Name:         string(spec.Name),
			Description:  spec.Description,
			MinValue:     spec.MinValue,
			MaxValue:     spec.MaxValue,
			DefaultValue: spec.DefaultValue,
			Unit:         spec.Unit,
		})
	}
	if a.store != nil {
		for name, value := range a.store.AllActive() {
			view.Active[string(name)] = value
		}
		for _, pending := range a.store.AllPending() {
			view.Pending = append(view.Pending, api.GovernancePendingView{
				Param:             string(pending.Param),
				Value:             pending.Value,
				EffectiveHeight:   pending.EffectiveHeight,
				SubmittedAtHeight: pending.SubmittedAtHeight,
				Authority:         pending.Authority,
				Memo:              pending.Memo,
			})
		}
	}
	if a.applier != nil {
		view.Authorities = append(view.Authorities, a.applier.AuthorityList()...)
	}
	view.GovernanceEnabled = len(view.Authorities) > 0
	return view
}

// ReinstallAdmissionGate replaces the pool's admission checker
// with enrollment.AdmissionChecker(prev), preserving the same
// shape Wire installed but with a new BaseAdmit predicate. Use
// when the operator's BaseAdmit closes over collaborators that
// only exist after Wire is called (typical example:
// cmd/qsdm/main.go's POL/BFT predicate, which closes over
// polFollower + liveBFT, both built later in the same boot
// path).
func ReinstallAdmissionGate(pool *mempool.Mempool, prev func(*mempool.Tx) error) {
	if pool == nil {
		return
	}
	// Mirror Wire()'s stack: gov > slashing > enrollment > prev.
	// Each layer only intercepts its own ContractID, so the
	// order is structurally safe but kept stable for
	// readability and ease of comparison with Wire().
	pool.SetAdmissionChecker(
		chainparams.AdmissionChecker(
			slashing.AdmissionChecker(
				enrollment.AdmissionChecker(prev))))
}

// AttachToProducer wires the post-construction half of the
// EnrollmentAwareApplier contract into a freshly-built
// BlockProducer:
//
//   - SetHeightFn(bp.TipHeight + 1)
//   - bp.OnSealedBlock = w.SealedBlockHook
//
// Split out from Wire because BlockProducer is constructed AFTER
// the StateApplier (Wire's output) is available — the producer
// closes over the applier, the applier closes back over the
// producer's TipHeight. AttachToProducer is the second half of
// that knot.
//
// Idempotent: calling twice on the same producer is a no-op
// because SetHeightFn replaces the prior fn and OnSealedBlock
// replaces the prior assignment. Useful for tests that rebuild
// the producer.
func (w *Wired) AttachToProducer(bp *chain.BlockProducer) {
	if w == nil || bp == nil {
		return
	}
	w.Aware.SetHeightFn(func() uint64 { return bp.TipHeight() + 1 })
	bp.OnSealedBlock = w.SealedBlockHook
}
