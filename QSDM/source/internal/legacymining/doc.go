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
//     in HL1.
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
// Still refused at S2 (CheckSupported, exit 78): ModePublic and every v2
// config, until WP-D (the Ledger calls the outstanding hooks; I6, the owner
// epoch cap, the zero-multiplier rule) and WP-E (difficulty_bits) land.
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
//	store_opkeys.go   HL2 WP-C: schema version 2 (operator_keys), migration.
//	ledger.go    WP5  Ledger (and so Sink), I1-I6 and the I7 family audit,
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
// ModePublic (HL2): defined by the contract, refused at S2 with
// ErrNotImplemented until HL2 WP-D and WP-E land (the WP-B Guard and the
// WP-C operator keys are in place).
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
// and steps 7 and 8 feed OwnerGuard.ObserveOwnerRejection.
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
