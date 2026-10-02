// Package legacymining is the HL1 hardened legacy-mining layer (design rev 4,
// §2-§6): a durable proof store, a canary guard and a payout ledger. With
// them, the single producer pays each accepted legacy proof exactly once, and
// the chain is the only record of payment (W3).
//
// # Frozen contract (WP1)
//
// doc.go and api.go are the shared contract for work packages WP3-WP9. They
// declare interfaces, shared types, constants and sentinel errors only. The
// names, signatures and documented semantics do not change after WP1; a
// change needs a coordinated contract revision across every consumer.
//
// # Contract revision HL2 (WP-A)
//
// The first coordinated revision is HL2 public mining
// (hl1/HL2_PUBLIC_MODE_DESIGN.md §4, WP-A). It is additive; nothing existing
// is renamed or changes meaning for an HL1 deployment:
//   - ModePublic ("public") is a new EnvMode value. LoadEnv accepts it, and
//     every other mode still exits ExitFatalRestore.
//   - Config gains version 2 (ConfigVersion2) with per-owner caps,
//     difficulty_bits, require_operator_sig, require_fully_bonded and
//     bonded_slot_cap, and 0..MaxAllowedEntries allowed entries. Version 1
//     decodes and validates exactly as in HL1, against the HL1 field set,
//     and its ConfigHash is still the SHA-256 of the raw file bytes, so a
//     deployed v1 file keeps its pin and its counter window.
//   - CheckModeConfig holds the mode/version rules: canary takes v1 or v2
//     (v2 with at least one allowed entry); public takes only v2 with
//     require_operator_sig and require_fully_bonded both true.
//   - CheckSupported is the fail-closed gate: until WP-B..E land, ModePublic
//     and every v2 config are refused with ErrNotImplemented (exit 78), and
//     NewGuard refuses a v2 config. Only canary with a v1 config boots, as
//     in HL1. (WP-E opened the gate; see below.)
//   - Five reject kinds are added after KindNonceConflict, so existing kind
//     values are unchanged: KindOwnerRateLimited, KindOwnerPendingFull and
//     KindOwnerCooldown (503), KindNotEnrolled and KindBadOperatorSig (400,
//     reason attestation). WP-B produces all but KindBadOperatorSig.
//
// # Contract revision HL2 (WP-B): the N-miner guard
//
// Additive again; a v1 config runs exactly the HL1 code paths:
//   - Candidate.Owner, EnrollmentInfo, EnrollmentView, SlotPolicy (default
//     FullyBondedSlotPolicy, the plug-in point for a deferred-bond tier),
//     OwnerAuthFunc (the WP-C hook), GuardStats, and OwnerGuard, the
//     per-owner surface of the Guard (TakeOwnerRate, ObserveOwnerRejection,
//     CheckOwnerPending, and the Ledger hooks SetOwnerOutstanding and
//     ResetOwnerOutstanding). Guard itself is unchanged.
//   - GuardOptions gains Mode, Enrollments, SlotPolicy and OwnerAuth.
//     NewGuard accepts a v2 config (CheckModeConfig against Mode); with
//     require_operator_sig it needs an OwnerAuth (else ErrNotImplemented).
//   - With a v2 config: Precheck looks up each submission's enrollment
//     (no global Allowed[0] predicate) and rejects unattributable input
//     without touching any owner; per-owner token buckets scale with bonded
//     slots; the HL1 submitter triggers (not-allowlisted, duplicates,
//     rate-burst) and bad submissions become per-owner, non-latching
//     OwnerCooldowns or log-only alarms. KILL, FREEZE, expiry, the proof
//     total and the budget stay global.
//   - miningsvc calls the OwnerGuard methods only for a v2 config.
//   - Constants OwnerCooldown, OwnerBadLimit, GlobalDuplicateAlarm,
//     UnattributableAlarm and SlotUnit; cause CauseBadSubmissions.
//
// # Contract revision HL2 (WP-C): operator keys (design §2 M1)
//
// Additive; a v1 config never reaches any of it:
//   - OperatorKey, OperatorKeySize, OperatorKeyStore and ErrOperatorKey
//     (api.go); CheckOperatorKey, OperatorKeys (the in-memory owner -> key
//     index), OperatorKeysFromBlocks, HydrateOperatorKeys,
//     VerifyOperatorSig and OperatorSigAuth, the OwnerAuthFunc (opkeys.go).
//     An owner's key is any ML-DSA-87 public key with
//     owner == hex(sha256(pk)), found in chain history (normally the
//     owner's signed qsdm/enroll/v2 tx) or in operator_keys; the binding is
//     re-checked on every load. There is no registration endpoint.
//   - OperatorSigAuth rejects a missing key, a missing operator_sig or one
//     that does not verify over Bundle.CanonicalForOperatorSignature with
//     KindBadOperatorSig (400). The Guard runs it in Precheck, before any
//     per-owner accounting (WP-B ordering).
//   - legacy-mining.db schema version 2 (StoreUserVersionOperatorKeys) adds
//     the immutable operator_keys table (store_opkeys.go). NewSQLiteStoreV2
//     creates version 2 and migrates a version 1 DB at Open in one
//     transaction; NewSQLiteStore never migrates and opens either version.
//     cmd/qsdm uses the V2 store only for a version 2 config.
//   - cmd/qsdm passes Mode, an EnrollmentView over the enrollment state,
//     FullyBondedSlotPolicy and (with require_operator_sig) OperatorSigAuth,
//     and fills the key index after S14 ("S14b", hl2HydrateOperatorKeys).
//
// # Contract revision HL2 (WP-D): the N-miner Ledger
//
// Additive; a v1 config runs exactly the HL1 Ledger, Reconcile and Store
// paths (the v2 branches key on Guard.Config().Version):
//   - OwnerSink (api.go), implemented by *PayoutLedger: OutstandingFor and
//     CheckOwnerEpoch. NewLedger type-asserts a v2 Guard to OwnerGuard and
//     reports per-owner pending plus in-flight counts to it
//     (ResetOwnerOutstanding at Init, SetOwnerOutstanding after Enqueue and
//     OnDurableBlock), so CheckOwnerPending fires. Enqueue re-checks
//     max_pending_per_owner like max_pending (FREEZE CauseEnqueue: the step
//     6 check must still hold at step 9).
//   - I6 for v2, in PreSeal and at H8: every paid ID's row miner_addr is the
//     recipient. The row's miner_addr is the enrollment owner Precheck
//     attributed at admission; cfg.Allowed (empty in public mode) is not
//     used, and current enrollment is never consulted, so an unenroll
//     between accept and seal pays the proofs and does not FREEZE. v1 I6
//     (the allowlist) is unchanged.
//   - Owner epochs (OwnerEpochBlocks, OwnerEpochOf): 8640-block windows
//     aligned to height 0, so an epoch is a function of the chain alone:
//     crash-safe without new durable state, and auditable from the journal.
//     Per-owner emitted CELL per epoch comes from the chain, like
//     emitted_H: Reconcile derives it at S12 for the epoch of tip+1
//     (LedgerInit.Tip, LedgerInit.OwnerEmitted) and OnDurableBlock adds
//     every reward. The DB holds no amounts, so it cannot be the source of
//     CELL; its per-owner query is OwnerStore.OwnerCounts, served by the
//     proofs_owner index on (config_sha256, miner_addr), which S12 checks
//     against proofs_H. The index is part of schema version 2 (amended, not
//     a version 3: version 2 was never deployed).
//   - owner_epoch_cap_cell: CheckOwnerEpoch returns KindOwnerRateLimited
//     (503) with Detail "owner epoch cap: ..." while the owner's emitted
//     amount in the current epoch is >= the cap. It holds admission only;
//     accepted proofs are still paid (holding them would trip the global
//     stall FREEZE), so an epoch can end above the cap by less than
//     2*rewardCell. miningsvc calls it after CheckOwnerPending.
//   - Reward rule R4 (operator decision): the d7 pro-rata split by admitted
//     proofs per block (blockdriver), so emission is rewardCell per
//     non-empty block up to float rounding (the PreSeal and I3 bound
//     rewardCell*(1+RewardSumSlack)). A v2 config refuses the Tier-3 reward
//     penalty: cmd/qsdm exits 78 at S2 with QSDM_SPEC_PENALTY_ENABLED set,
//     and blockdriver.New refuses a RewardPenalty with a v2 Guard. So no
//     multiplier, and in particular no multiplier <= 0, can produce a zero
//     share and a global PreSeal FREEZE; the penalty's input is self-reported
//     telemetry anyway (HL2 §1 Q1 (d)). Dropping zero-share claims and
//     requeueing them was rejected: requeued IDs would trip the global stall
//     FREEZE after StallSeals.
//   - Reconcile S10 also refuses a second reward to one recipient in a block
//     (v2), and S12 reports OwnerEpoch, OwnerEmitted and Owners.
//   - Deferred-bond tier: see "Contract revision HL2 (tier A)" below. No
//     schema, Ledger or reconcile change: they key on the owner and the
//     row's miner_addr, never on bond state.
//   - cmd/qsdm builds the Guard, Store and Ledger for ModePublic too
//     (hl1BootConfig.Canary means "legacy mining is on").
//
// # Contract revision HL2 (WP-E): difficulty_bits (design §2 M4)
//
// Additive; a v1 config serves and enforces exactly the HL1 difficulty:
//   - ConfigDifficulty (difficulty.go): mining.DefaultMinDifficulty (2^16)
//     for v1, 2^difficulty_bits (16..48, so never below 2^16) for v2.
//     cmd/qsdm (hl1MiningServiceConfig) sets miningsvc Config.Difficulty
//     to it for a writable service; a read-only service (Stage A, a
//     follower, a canary whose Store did not open) keeps 2^16 and serves no
//     work. miningsvc.New refuses a writable v2 service whose Difficulty is
//     not ConfigDifficulty(Guard.Config()), so difficulty_bits can never be
//     silently ignored.
//   - /work advertises the difficulty (api.MiningWork.Difficulty, decimal),
//     and mining.Verifier step 10 rejects a proof whose PoW hash does not
//     meet the target (400, reason work, "hash does not meet target"); with
//     a v2 config that rejection feeds the owner's bad-submission cooldown.
//     The reference miners re-read the difficulty from every /work
//     (cmd/qsdmminer-console runLoop: api.WorkToMiningCore ->
//     mining.TargetFromDifficulty -> Solve), so they adapt without a
//     change; the CUDA backend takes the same target.
//   - The difficulty is producer-local (traced for WP-E, design §1 Q2
//     "replay paths not traced"). mining.Verifier is constructed only by
//     internal/miningsvc (miningsvc.go New) and the miner CLIs' self-tests;
//     the target check is pkg/mining/verifier.go:534-545, reached only from
//     miningsvc.Submit, which is read-only (503) on every node that is not
//     the canary/public producer (cmd/qsdm hl1NewCanary !producerRole,
//     hl1MiningServiceConfig ReadOnly). Reward txs carry only LMP1 proof
//     IDs (internal/blockdriver/blockdriver.go:975, 984-995). Followers
//     append blocks through BlockProducer.TryAppendExternalBlock
//     (pkg/chain/block.go:637-785): authorisation, hash, signature, then
//     every tx through the state applier and a state-root check; a reward
//     tx goes to EnrollmentApplier.ApplyMiningRewardTx
//     (pkg/chain/enrollment_aware_applier.go:175-179,
//     pkg/chain/mining_reward.go:21-56), which checks only the contract,
//     sender, fee, amount and recipient. Boot restore, hl1 replay
//     (cmd/qsdm/hl1_replay.go:418) and receipts regeneration use the same
//     appliers. No file in pkg/chain, the block header or gossip
//     validation mentions a difficulty or target, and the slashing
//     verifiers parse embedded proofs (doublemining.go:400-404,
//     forgedattest.go:426, freshnesscheat.go:462) without any target
//     check. So difficulty_bits changes need a new config (hash), never a
//     fork, and followers stay in parity whatever the producer's setting.
//   - Planning helpers (difficulty.go): BlockPeriod, GraceDuration,
//     ExpectedProofsPerMinute, ExpectedSolveTime, LandedProofsPerMinute
//     (the grace-window loss of a miner that solves each height to
//     completion), SuggestDifficultyBits; BenchmarkPoWAttemptCPU measures
//     the CPU hashrate; hl1/tools/difficulty-estimate.py is the operator
//     version.
//   - CheckSupported opens: canary v1, canary v2, and public v2 with
//     require_operator_sig boot; any other mode/version pair is still
//     refused with ErrNotImplemented (exit 78). Every other S2 gate
//     (CheckModeConfig, the Tier-3 penalty refusal, the sync-URL refusal)
//     is unchanged.
//
// # Contract revision HL2 (tier A): deferred-bond slots (operator decision A)
//
// Additive; v1 never reaches it and a v2 config without the new field
// behaves as before:
//   - Config.DeferredSlotWeightPermille (v2 "deferred_slot_weight_permille",
//     optional, 0..SlotUnit, default 0) with MaxDeferredSlotWeightPermille;
//     EnrollmentInfo.DeferredBond (bond_mode mining_rewards);
//     DeferredBondSlotPolicy and ConfigSlotPolicy. A weight above 0 needs
//     require_fully_bonded false (ValidateConfig), and public mode accepts
//     require_fully_bonded false only with a weight above 0
//     (CheckModeConfig).
//   - The tier: a deferred-bond node that is not fully bonded counts as
//     weight/1000 of a slot in its owner's bucket; a fully bonded node is a
//     full slot; any other node (including an upfront-bond node below its
//     bond after a slash) is not admitted. Weight 0 excludes deferred-bond
//     nodes. NewGuard uses ConfigSlotPolicy when GuardOptions.SlotPolicy is
//     nil, and cmd/qsdm passes it explicitly.
//   - Bond credit (verified): the LMP1 reward txs are
//     chain.MiningRewardContractID transfers from the funder, and every
//     node (producer and followers) applies them through
//     EnrollmentApplier.ApplyMiningRewardTx, which first calls
//     enrollment.InMemoryState.AccrueBondFromReward: the owner's reward is
//     locked into its active, not yet fully bonded mining_rewards
//     enrollments (lexical node order) until each reaches RequiredBondDust,
//     and only the rest is credited liquid. The Guard's EnrollmentView
//     reads that same live state, so the block that completes a bond makes
//     the node a full slot at the next submission, with no restart. The
//     accrual is per owner, not per node: any reward to the owner
//     (including one earned by its fully bonded nodes) fills its deferred
//     bonds first. owner_epoch_cap_cell and emitted_H count the gross
//     reward, including the withheld part; the Ledger's I4/I5 read only
//     the funder, so withholding changes no invariant.
//
// ModePublic is accepted at S2 with a version 2 config that sets
// require_operator_sig (CheckSupported).
//
// Implementations and the functions they must export:
//
//	store.go     WP3  Store (SQLite, §3.3).
//	payload.go   WP3  LMP1 codec (§3.4). Both functions return errors
//	                  that wrap ErrBadPayload:
//	                    func EncodePayload(ids []ProofID) ([]byte, error)
//	                    func DecodePayload(payload []byte) ([]ProofID, error)
//	guard.go     WP4  Guard, config and env loading, markers, and the
//	                  D1/D2 durable-write primitives (§3.2). cmd/qsdm and
//	                  cmd/hl1-tail reuse D1/D2 instead of re-implementing them.
//	guard_owner.go    HL2 WP-B: the v2 per-owner admission path (OwnerGuard).
//	opkeys.go         HL2 WP-C: operator keys and the operator_sig OwnerAuth.
//	difficulty.go     HL2 WP-E: ConfigDifficulty and the planning helpers.
//	store_opkeys.go   HL2 WP-C: schema version 2 (operator_keys), migration.
//	ledger.go    WP5  Ledger (and so Sink; HL2 WP-D: OwnerSink), I1-I6 and the I7 family audit,
//	                  which runs in every mode without a Ledger:
//	                    func AuditTxFamilies(blk *chain.Block) []FamilyViolation
//	reconcile.go WP8  Startup steps S8-S13 (§5), including counter derivation.
//
// Consumers:
//
//	internal/miningsvc   WP7  ChainView, Store, Guard, Sink, Rejection
//	internal/blockdriver WP6  Guard, Ledger, FailStopFunc, reward ID format
//	cmd/qsdm             WP9  wiring, hook H8, Watermark, markers, exit codes
//	cmd/hl1-tail         WP10 Watermark, file names, exit codes
//
// Imports: this package may import pkg/chain, pkg/mempool, pkg/mining and
// pkg/mining/attest/hmac. It must not import pkg/api (a heavy dependency; the
// 503 mapping is done in miningsvc, see ErrUnavailable), internal/miningsvc or
// internal/blockdriver. No pkg/... package may import it.
//
// # Modes
//
// ModeOff (Stage A): no Store, Guard or Ledger exists. miningsvc is ReadOnly
// and the driver seals heartbeats only (S6). The I7 audit still runs at H8(a).
//
// ModeCanary (Stage B): exactly one Store, Guard and Ledger per process.
// Mining is writable only when local block production is on, the Store is
// open and the Guard is loaded; otherwise miningsvc is ReadOnly.
//
// ModePublic (HL2): one Store, Guard and Ledger, wired like a canary boot,
// with a version 2 config that sets require_operator_sig (CheckModeConfig,
// CheckSupported). Admission is per enrollment and per owner (WP-B..D), at
// the config's difficulty (WP-E), for fully bonded nodes plus, with
// deferred_slot_weight_permille, deferred-bond nodes at a reduced weight.
//
// # Lock order (§4.5)
//
//	sealLifecycleMu -> ledger.mu -> guard.mu -> store
//	submitMu        -> ledger.mu -> guard.mu -> store
//
// Consequences:
//   - The Ledger may call the Guard and the Store.
//   - The Guard may call the Store (events) and FailStopFunc, but never the
//     Ledger.
//   - The Store never calls back into the Ledger or the Guard (L4).
//   - Guard state reads are atomic; guard.mu is taken only to trip (L5).
//   - No method here waits on sealLifecycleMu or submitMu, and none may be
//     called with bp.mu held.
//
// # Submit (§4.1, miningsvc)
//
// The api middleware admits the canonical POST only while
// Guard.AdmissionOpen (step 2). miningsvc then runs:
//
//	Guard.Admit                                      step 3 (503)
//	Guard.Precheck                                   step 4 (400)
//	Guard.TakeRate                                   step 5 (503)
//	lock submitMu
//	Sink.Outstanding() >= Config().MaxPending        step 6 (503, KindPendingFull)
//	Verify(raw, ChainView.TipHeight())               step 7 (durable accept height)
//	Store.Accept(Record)                             step 8
//	Sink.Enqueue(Record)                             step 9
//	unlock submitMu; 200                             step 10
//
// Every non-nil error from steps 7 and 8 goes to Guard.ObserveRejection. A
// Store.Accept error that is not a *Rejection trips Guard.Freeze(CauseAcceptIO)
// and returns KindUnavailable. Before an error leaves miningsvc, any error
// matching ErrUnavailable is wrapped with api.ErrMiningUnavailable. WorkAt
// returns 503 unless Guard.AdmissionOpen.
//
// With a v2 config (HL2 WP-B) step 5 is OwnerGuard.TakeOwnerRate, step 6
// adds OwnerGuard.CheckOwnerPending(Candidate.Owner) after the global cap,
// then (WP-D) OwnerSink.CheckOwnerEpoch(Candidate.Owner), and steps 7 and 8
// feed OwnerGuard.ObserveOwnerRejection.
//
// # Tick (§4.2, blockdriver)
//
//	Guard.State().PayoutsEnabled() false  -> heartbeat only
//	Ledger.Take                           -> claims now in flight
//	build txs (d7 float formula, RewardIDFormat, EncodePayload)
//	Ledger.PreSeal                        -> error: FREEZE already tripped,
//	                                         IDs pending again; heartbeat
//	Pool.Add; fingerprint fp0; localSeal=true; ProduceBlock; localSeal=false
//	SUCCESS:    Pool.Remove own txs not in blk, then Ledger.Requeue
//	SAFE:       Pool.Remove own txs, then Ledger.Requeue
//	POST-APPLY: FailStopFunc(ExitFailStop, CausePostApplyPrefix+err)
//
// The local-seal flag is owned by cmd/qsdm and shared with blockdriver. The
// hook reads it as the local argument at H8.
//
// # Persistence hook H8 (§4.4, cmd/qsdm)
//
//	(a) all modes, local seals: AuditTxFamilies; count
//	    hl1_unexpected_tx_family_total; canary: Guard.Freeze(CauseTxFamily)
//	(b) canary: Ledger.OnDurableBlock(blk, local), then
//	    Guard.ObserveSeal(blk.Height, local)
//
// # Startup (§5, cmd/qsdm and reconcile.go)
//
// In canary mode the Guard is constructed before Store.Open, so an S7 failure
// can trip FROZEN (CauseDBOpen). Then:
//
//	S7    Store.Open(path, nil)
//	S8    if ErrDBMissing: scan for PayloadTag, then Store.Open(path, &Meta{H0: tip+1, ...})
//	S9    Store.Meta
//	S10   Store.Lookup over the chain's LMP1 IDs
//	S11   Store.ApplyReconcile
//	S12   Store.Counters plus the chain -> Totals; Guard.ObserveTotals
//	S13   Store.Pending -> Ledger.Init
//	S14   Guard.PreArm
//	S15   SyncFunderNonce; start the driver
//	S16   Guard.Activate(S10 clean)
//
// Reconciliation must not scan the chain with ChainView.GetBlock. On
// *chain.BlockProducer that call is O(tip), because it scans the whole chain
// (pkg/chain/block.go GetBlock). Use the restored block slice instead.
package legacymining
