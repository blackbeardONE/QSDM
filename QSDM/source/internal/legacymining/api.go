package legacymining

// FROZEN CONTRACT (WP1). See doc.go. Section references (§, S, H, I, W, L)
// are to the HL1 design rev 4.

import (
	"errors"
	"net/http"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
)

// -----------------------------------------------------------------------------
// Identities
// -----------------------------------------------------------------------------

// ProofID is a proof's 32-byte ID, as returned by mining.Verifier.Verify. It
// is the primary key of the proofs table and the unit of payment in an LMP1
// payload.
type ProofID [32]byte

// ConfigHash is the SHA-256 of the canary config file bytes (§6.1), pinned by
// EnvCanaryConfigSHA256. It keys proofs.config_sha256 and the counter window
// (config_windows). Only a new ConfigHash resets the counters.
type ConfigHash [32]byte

// -----------------------------------------------------------------------------
// Mode, environment and config (§6.1, §8 step 5, S2)
// -----------------------------------------------------------------------------

// Mode is the value of EnvMode.
type Mode string

const (
	// ModeOff means all four Env* variables are unset (Stage A).
	ModeOff Mode = ""
	// ModeCanary admits proofs from the single allowlisted miner (Stage B).
	ModeCanary Mode = "canary"
)

// Environment variables. Either all four are set or none is. Each of these
// exits ExitFatalRestore (S2): a partial set (ErrEnvPartial), a mode other
// than ModeCanary, a config SHA-256 mismatch (ErrConfigHash) or an invalid
// config (ErrConfig).
const (
	EnvMode               = "QSDM_LEGACY_MINING_MODE"
	EnvDB                 = "QSDM_LEGACY_MINING_DB"
	EnvCanaryConfig       = "QSDM_LEGACY_MINING_CANARY_CONFIG"
	EnvCanaryConfigSHA256 = "QSDM_LEGACY_MINING_CANARY_CONFIG_SHA256"
)

// Config is the canary config file (§6.1). It is decoded strictly: unknown
// fields and trailing data are rejected. The validation rules below live in
// guard.go, and a failure wraps ErrConfig:
//   - Version == ConfigVersion;
//   - exactly one Allowed entry, whose MinerAddr is 64 lowercase hex and
//     whose NodeID is non-empty;
//   - MaxProofsPerMin > 0, MaxProofsTotal > 0 and BudgetCell > 0;
//   - 0 < MaxPending <= MaxPendingLimit;
//   - ExpiresUnix > 0.
//
// Expiry is a §6.6 trigger (graceful ADMISSION_STOP), not a load failure, so
// a restart after expiry still boots the chain.
type Config struct {
	Version         int          `json:"version"`
	Allowed         []AllowEntry `json:"allowed"`
	MaxProofsPerMin int          `json:"max_proofs_per_min"`
	MaxProofsTotal  uint64       `json:"max_proofs_total"` // T
	MaxPending      int          `json:"max_pending"`      // pending plus in-flight cap
	BudgetCell      uint64       `json:"budget_cell"`      // B, in whole CELL
	ExpiresUnix     int64        `json:"expires_unix"`
}

// AllowEntry is one allowlisted (miner address, node) pair.
type AllowEntry struct {
	MinerAddr string `json:"miner_addr"`
	NodeID    string `json:"node_id"`
}

const (
	// ConfigVersion is the only accepted Config.Version.
	ConfigVersion = 1
	// MaxPendingLimit bounds Config.MaxPending. The cap counts pending plus
	// in-flight IDs, so the whole backlog always fits in one payload (§3.4).
	MaxPendingLimit = MaxPayloadIDs
)

// -----------------------------------------------------------------------------
// Reward transactions and the LMP1 payload (§2 blockdriver, §3.4)
// -----------------------------------------------------------------------------

const (
	// PayloadTag starts every LMP1 payload. The layout is PayloadTag, then a
	// u16 big-endian count (1..MaxPayloadIDs), then count 32-byte proof IDs in
	// strictly ascending order.
	PayloadTag = "QSDM-LMP1"
	// MaxPayloadIDs is the maximum number of IDs in one payload.
	MaxPayloadIDs = 1024
	// MaxPayloadBytes is the largest valid payload, 32,779 bytes.
	MaxPayloadBytes = len(PayloadTag) + 2 + MaxPayloadIDs*32

	// RewardIDPrefix and HeartbeatIDPrefix are reserved tx ID prefixes.
	// pkg/chain/wallet_transfer_admission.go rejects them with its own
	// literals, because pkg/chain cannot import this package.
	RewardIDPrefix    = "solo-reward-"
	HeartbeatIDPrefix = "solo-heartbeat-"

	// RewardIDFormat is the reward tx ID. The arguments are the funder nonce
	// (uint64), the miner address, and the lowercase hex of the first 8 bytes
	// of sha256(payload), which is 16 hex characters. Heartbeat IDs are
	// unchanged from d7.
	RewardIDFormat = "solo-reward-%d-%s-%s"
)

// -----------------------------------------------------------------------------
// Rejections: 503 versus 400 (§4.1, §6.2)
// -----------------------------------------------------------------------------

// RejectKind classifies an admission refusal. The kind alone decides the HTTP
// class; see HTTPStatus.
type RejectKind uint8

const (
	// 503 with Retry-After, never latched by itself:

	// KindAdmissionClosed means AdmissionOpen is false. This covers a state
	// other than OPEN, KILL present, the S16 quiet period, and not yet
	// Activated.
	KindAdmissionClosed RejectKind = iota + 1
	// KindRateLimited means the MaxProofsPerMin cap was reached (step 5).
	KindRateLimited
	// KindPendingFull means pending plus in-flight >= MaxPending (step 6).
	KindPendingFull
	// KindUnavailable means an internal failure that has tripped FREEZE,
	// such as a non-UNIQUE Store.Accept error or a Sink.Enqueue failure.
	KindUnavailable

	// 400 (a *mining.RejectError reaches the handler):

	// KindMalformed means the proof or its attestation bundle did not parse
	// (step 4). Reason: non-canonical.
	KindMalformed
	// KindMinerNotAllowed means miner_addr is not allowlisted. Reason:
	// bad-addr. It counts toward the non-allowlisted trigger.
	KindMinerNotAllowed
	// KindNodeNotAllowed means the bundle node_id is not allowlisted.
	// Reason: attestation. It counts toward the non-allowlisted trigger.
	KindNodeNotAllowed
	// KindAttestationType means the attestation type is not
	// mining.AttestationTypeHMAC. Reason: attestation.
	KindAttestationType
	// KindDuplicate means the DB UNIQUE constraint on proof_id was hit
	// (step 8). Reason: duplicate. It counts toward the duplicate trigger.
	KindDuplicate
	// KindNonceConflict means the DB UNIQUE constraint on (node_id,
	// att_nonce) was hit (step 8). Reason: attestation. It counts toward the
	// duplicate trigger.
	KindNonceConflict
)

// String returns the kind's stable label, which is used in logs, metrics and
// the RejectError detail.
func (k RejectKind) String() string {
	switch k {
	case KindAdmissionClosed:
		return "admission-closed"
	case KindRateLimited:
		return "rate-limited"
	case KindPendingFull:
		return "pending-full"
	case KindUnavailable:
		return "unavailable"
	case KindMalformed:
		return "malformed"
	case KindMinerNotAllowed:
		return "miner-not-allowed"
	case KindNodeNotAllowed:
		return "node-not-allowed"
	case KindAttestationType:
		return "attestation-type"
	case KindDuplicate:
		return "duplicate"
	case KindNonceConflict:
		return "nonce-conflict"
	}
	return "invalid"
}

// rejectReason is the only 400 table: a kind is 400 exactly when it has a
// mining.RejectReason. Unknown kinds fall back to 503.
func (k RejectKind) rejectReason() (mining.RejectReason, bool) {
	switch k {
	case KindMalformed:
		return mining.ReasonNonCanonical, true
	case KindMinerNotAllowed:
		return mining.ReasonBadAddr, true
	case KindNodeNotAllowed, KindAttestationType, KindNonceConflict:
		return mining.ReasonAttestation, true
	case KindDuplicate:
		return mining.ReasonDuplicate, true
	}
	return "", false
}

// HTTPStatus returns 400 for the reject kinds and 503 for all others,
// including unknown kinds.
func (k RejectKind) HTTPStatus() int {
	if _, ok := k.rejectReason(); ok {
		return http.StatusBadRequest
	}
	return http.StatusServiceUnavailable
}

// Rejection is the typed admission refusal shared by the Guard, the Store and
// miningsvc. Its Unwrap gives the handler class:
//   - a 400 kind unwraps to a *mining.RejectError whose Detail starts with
//     Kind.String(), so the api handler returns 400;
//   - every other kind unwraps to ErrUnavailable. miningsvc must wrap such
//     errors with api.ErrMiningUnavailable, which the handler returns as 503
//     with Retry-After.
type Rejection struct {
	Kind   RejectKind
	Detail string
}

func (r *Rejection) Error() string {
	if r.Detail == "" {
		return "legacymining: " + r.Kind.String()
	}
	return "legacymining: " + r.Kind.String() + ": " + r.Detail
}

// Unwrap implements the class mapping described on Rejection.
func (r *Rejection) Unwrap() error {
	reason, ok := r.Kind.rejectReason()
	if !ok {
		return ErrUnavailable
	}
	detail := r.Kind.String()
	if r.Detail != "" {
		detail += ": " + r.Detail
	}
	return &mining.RejectError{Reason: reason, Detail: detail}
}

// RejectKindOf returns the Kind of the first *Rejection in err's chain, or 0
// if there is none.
func RejectKindOf(err error) RejectKind {
	var r *Rejection
	if errors.As(err, &r) {
		return r.Kind
	}
	return 0
}

// -----------------------------------------------------------------------------
// Store (§3.3, store.go, WP3)
// -----------------------------------------------------------------------------

// Record is one row of the proofs table.
type Record struct {
	ProofID      ProofID
	MinerAddr    string   // 64 lowercase hex; the allowlisted address
	NodeID       string   // HMAC bundle node_id; allowlisted
	AttNonce     [32]byte // attestation challenge nonce
	WorkHeight   uint64   // the proof's height
	AcceptTip    uint64   // durable tip used as the accept height (step 7)
	AcceptedNS   int64
	ConfigSHA256 ConfigHash
	ProofJSON    []byte // the submitted bytes, which Verify requires to be canonical
	PaidHeight   uint64 // valid only when PaidTxID != ""
	PaidTxID     string // "" while unpaid
}

// Meta is the single meta row. S9 checks it against the chain.
type Meta struct {
	H0          uint64 // first height accounted for; tip+1 when the DB was created (S8)
	GenesisHash string
	Funder      string // chain.MiningRewardFunderAddress
	Release     string // version string of the binary that created the DB
	CreatedNS   int64
}

// Payment says that ProofID was paid by reward tx TxID in the block at Height.
type Payment struct {
	ProofID   ProofID
	MinerAddr string // the reward tx Recipient; must equal the row's miner_addr
	Height    uint64
	TxID      string
}

// ConfigWindow is one config_windows row: the counter window of a config.
type ConfigWindow struct {
	ConfigSHA256 ConfigHash
	FirstHeight  uint64 // tip+1 when the window was inserted (S11)
	ActivatedNS  int64
}

// Counters is the DB side of a window's counters. The emitted amount comes
// from the chain (see Totals).
type Counters struct {
	Window ConfigWindow
	Known  bool   // false if config_windows has no row for the hash
	Proofs uint64 // proofs_H: rows with config_sha256 == the hash
	Paid   uint64 // how many of those rows are paid
}

// Reconciliation is the input of S11.
type Reconciliation struct {
	// Paid is every LMP1 payment found on the chain in [Meta.H0, tip]. It is
	// the complete set, not a delta.
	Paid []Payment
	// Window is inserted unless its ConfigSHA256 already has a row.
	Window ConfigWindow
}

// ReconcileCounts reports what ApplyReconcile changed. The same counts are
// written to the events table.
type ReconcileCounts struct {
	Updated        int // rows whose paid_height or paid_tx_id was set from the chain
	Reset          int // paid rows the chain does not confirm, now unpaid
	WindowInserted bool
}

// Event is one row of the append-only events table.
type Event struct {
	AtNS   int64
	Kind   string
	Detail string
}

// Store is the durable proof store: legacy-mining.db. Every method is one
// self-contained transaction and never calls the Ledger or the Guard (L4).
// Methods are safe for concurrent use; the store uses one connection and
// serialises access. Every method except Open returns ErrClosed when the
// store is not open.
type Store interface {
	// Open opens the DB at path (EnvDB). The checks are:
	//   - the parent directory is the legacy-mining directory: a real
	//     directory (not a symlink), mode 0700, owned by the process;
	//   - the DB, -wal and -shm files are regular 0600 files;
	//   - application_id StoreApplicationID, user_version StoreUserVersion,
	//     WAL, synchronous=FULL, foreign_keys on, BEGIN IMMEDIATE, one
	//     connection, and a quick_check that passes.
	// The owner and mode checks run on Unix only.
	//
	// If the DB file does not exist and create is nil, Open creates nothing
	// and returns ErrDBMissing (S8 then scans the chain). If create is non-nil,
	// Open creates the schema and the meta row in one transaction. Open never
	// creates the directory. Path failures wrap ErrUnsafePath, and identity or
	// schema failures wrap ErrSchema. The caller trips FREEZE with CauseDBOpen
	// (S7).
	Open(path string, create *Meta) error
	Close() error

	// Meta returns the meta row (S9).
	Meta() (Meta, error)

	// Accept inserts rec, which must be unpaid, and COMMITs before returning
	// (W1). UNIQUE violations return a *Rejection (§4.1 step 8; 400, no
	// FREEZE):
	//   - on proof_id: KindDuplicate;
	//   - on (node_id, att_nonce): KindNonceConflict.
	// Any other error is returned unchanged (not a *Rejection). The caller
	// then trips FREEZE (CauseAcceptIO) and returns KindUnavailable.
	Accept(rec Record) error

	// Pending returns the unpaid rows, ordered by (AcceptedNS, ProofID) (S13).
	Pending() ([]Record, error)

	// Lookup returns the rows for ids. IDs with no row are absent from the
	// map (S10 orphan check).
	Lookup(ids []ProofID) (map[ProofID]Record, error)

	// MarkPaid sets paid_height and paid_tx_id for every payment in one
	// transaction (H8). It is all-or-nothing: if any ProofID is absent,
	// already paid, or has a miner_addr different from MinerAddr, nothing
	// changes and the error wraps ErrPaidConflict.
	MarkPaid(ps []Payment) error

	// ApplyReconcile is S11, as one transaction:
	//   - every r.Paid row gets its paid state from the chain;
	//   - every other paid row is reset to unpaid;
	//   - r.Window is inserted if it is new;
	//   - one events row records the counts.
	// An absent ID or a miner_addr mismatch changes nothing, and the error
	// wraps ErrPaidConflict.
	ApplyReconcile(r Reconciliation) (ReconcileCounts, error)

	// Counters returns the window and DB counts for h (S12).
	Counters(h ConfigHash) (Counters, error)

	// Event appends ev to the events table.
	Event(ev Event) error
}

const (
	// StoreApplicationID is PRAGMA application_id ("QLM1").
	StoreApplicationID = 0x514C4D31
	// StoreUserVersion is PRAGMA user_version.
	StoreUserVersion = 1
)

// -----------------------------------------------------------------------------
// Guard (§6, guard.go, WP4)
// -----------------------------------------------------------------------------

// State is the guard state (§6.5). The zero value is invalid.
type State uint8

const (
	// StateOpen: admission and payouts.
	StateOpen State = iota + 1
	// StateAdmissionStopped: payouts only. The latch is ADMISSION_STOPPED.json.
	StateAdmissionStopped
	// StateFrozen: heartbeats only, pending kept. The latch is TRIPPED.json.
	StateFrozen
	// StateKilled: heartbeats only, pending kept. The latch is KILL.
	StateKilled
)

func (s State) String() string {
	switch s {
	case StateOpen:
		return "OPEN"
	case StateAdmissionStopped:
		return "ADMISSION_STOPPED"
	case StateFrozen:
		return "FROZEN"
	case StateKilled:
		return "KILLED"
	}
	return "INVALID"
}

// PayoutsEnabled reports whether the driver may pay in this state (§4.2 step 1).
func (s State) PayoutsEnabled() bool {
	return s == StateOpen || s == StateAdmissionStopped
}

// Candidate is a proof that passed Precheck (§4.1 step 4). It is not verified
// yet.
type Candidate struct {
	Proof    *mining.Proof
	NodeID   string   // from the HMAC bundle; allowlisted
	AttNonce [32]byte // the attestation challenge nonce (bundle nonce)
}

// Guard is the canary control plane (§6): config, allowlist, caps, latched
// states, pre-armed markers and automatic triggers. Exactly one Guard exists,
// and only in ModeCanary. It is safe for concurrent use.
//
// Config, ConfigHash, State and AdmissionOpen never wait on guard.mu (L5).
// guard.mu is taken only to trip. A trip may call Store.Event and
// FailStopFunc, never the Ledger.
//
// Time-based triggers (expiry, NoSealTimeout) are evaluated lazily by State,
// AdmissionOpen and Admit. The driver calls State on every tick, so no timer
// goroutine is needed.
type Guard interface {
	// Config returns the loaded, validated config.
	Config() Config
	// ConfigHash returns the pinned SHA-256 of the config file.
	ConfigHash() ConfigHash

	// State stats <legacy-mining>/KILL on every call (§6.7). It returns the
	// most restrictive state: KILLED, then FROZEN, then ADMISSION_STOPPED,
	// then OPEN.
	State() State

	// AdmissionOpen reports whether State() == StateOpen and every S16
	// condition holds:
	//   - Activate was called with reconcileClean true;
	//   - QuietPeriod has elapsed since Activate;
	//   - QuietSeals local durable seals have been observed;
	//   - the config is unexpired;
	//   - the allowlisted node_id is an active enrollment owned by the
	//     allowlisted miner_addr.
	// It is the predicate for pkg/api SetMiningCanaryAdmission and for
	// miningsvc WorkAt.
	AdmissionOpen() bool

	// PreArm is S14. It D1-creates TRIPPED.armed and ADMISSION_STOPPED.armed
	// unless the matching .json latch exists. On error the caller trips FREEZE
	// with CausePreArm.
	PreArm() error

	// Activate is S16 and is called once, after the driver has started.
	// reconcileClean is the S10 result. Admission never opens before Activate,
	// and never opens in this process if reconcileClean is false. The
	// QuietPeriod and NoSealTimeout clocks start here.
	Activate(reconcileClean bool)

	// Admit is §4.1 step 3. It records the submission for the rate-burst
	// trigger, then returns a KindAdmissionClosed *Rejection unless
	// AdmissionOpen.
	Admit() error

	// Precheck is §4.1 step 4 and touches no nonce state. It runs ParseProof,
	// the miner_addr allowlist, the attestation type check
	// (mining.AttestationTypeHMAC), hmac.ParseBundle and the node_id
	// allowlist. It returns a 400 *Rejection (KindMalformed,
	// KindMinerNotAllowed, KindAttestationType or KindNodeNotAllowed), and it
	// counts its own non-allowlisted rejections toward the §6.6 trigger.
	Precheck(rawProofJSON []byte) (Candidate, error)

	// TakeRate is §4.1 step 5. It takes one slot of MaxProofsPerMin in the
	// current minute, or returns a KindRateLimited *Rejection.
	TakeRate() error

	// ObserveRejection feeds the §6.6 duplicate/nonce-conflict trigger. Call
	// it with every non-nil error from §4.1 step 7 (Verify) and step 8
	// (Store.Accept), but never with a Precheck error. It counts:
	//   - KindDuplicate and KindNonceConflict;
	//   - a *mining.RejectError with ReasonDuplicate;
	//   - an HMAC nonce replay.
	// It ignores everything else.
	ObserveRejection(err error)

	// ObserveSeal is called at H8 for every durable block. A local seal
	// counts toward QuietSeals and restarts the NoSealTimeout clock.
	ObserveSeal(height uint64, local bool)

	// ObserveTotals checks the graceful ADMISSION_STOP triggers:
	//   - t.Proofs >= MaxProofsTotal (CauseProofsTotal);
	//   - t.Emitted >= BudgetCell - BudgetStopMarginCells*rewardCell
	//     (CauseBudget).
	// It is called at S12 and by the Ledger after Enqueue and OnDurableBlock.
	ObserveTotals(t Totals, rewardCell float64)

	// Freeze latches FROZEN. It runs D2 (TRIPPED.armed -> TRIPPED.json),
	// falls back to D1 of TRIPPED.json, and otherwise calls
	// FailStopFunc(ExitFailStop, CauseMarkerIO). It writes TRIPPED.cause.json
	// best effort. Freeze is idempotent, keeps the first cause and never
	// exits except through marker I/O.
	Freeze(cause string)

	// StopAdmission latches ADMISSION_STOPPED in the same way, using the
	// ADMISSION_STOPPED markers.
	StopAdmission(cause string)
}

// -----------------------------------------------------------------------------
// Ledger (ledger.go, WP5)
// -----------------------------------------------------------------------------

// Sink is the submit side of the Ledger. miningsvc receives it as Config.Sink.
type Sink interface {
	// Outstanding returns pending plus in-flight (§4.1 step 6). Only Enqueue
	// increases it.
	Outstanding() int

	// Enqueue adds a committed record to pending (§4.1 step 9, under
	// submitMu). It fails only on an invariant violation, such as an
	// uninitialised ledger or an ID it already holds. In that case the Ledger
	// has tripped FREEZE and the caller returns KindUnavailable. The row stays
	// committed and is reloaded at the next S13.
	Enqueue(rec Record) error
}

// Claim is one address's part of a Take: all of its IDs that are now in
// flight.
type Claim struct {
	MinerAddr string
	IDs       []ProofID // strictly ascending; 1..MaxPayloadIDs
}

// Totals holds the counters of the active config window (§6.5, §6.6). They are
// derived from the chain and the DB at S12, and only a new ConfigHash resets
// them.
type Totals struct {
	ConfigSHA256 ConfigHash
	Proofs       uint64 // proofs_H
	// Emitted is emitted_H in CELL: the float64 sum, in block and tx order, of
	// the LMP1 reward amounts in blocks at or above the window's FirstHeight.
	Emitted float64
}

// LedgerInit is the S13 state passed to Ledger.Init.
type LedgerInit struct {
	Pending       []Record // Store.Pending order; in-flight starts empty
	Totals        Totals   // from S12
	FunderBalance float64  // expected funder balance for I5, read from the restored chain
	FunderNonce   uint64   // expected funder nonce for I4
}

// FamilyViolation is one I7 failure (§6.4): a tx in a local block that is not
// a funder heartbeat, a funder reward or a signed wallet transfer.
type FamilyViolation struct {
	TxID       string
	ContractID string
}

// AccountReader is the read side of *chain.AccountStore. The Ledger uses it
// to read the funder's nonce and balance after a durable block (I4, I5).
type AccountReader interface {
	Get(address string) (*chain.Account, bool)
}

// Ledger holds the pending, in-flight and paid sets and the per-ID
// missedSeals counters, guarded by ledger.mu. The tick goroutine is the only
// caller of Take, PreSeal and Requeue.
type Ledger interface {
	Sink

	// Init loads the S13 state. It is called exactly once, before the driver
	// starts, and resets missedSeals to 0. Until Init:
	//   - Enqueue fails;
	//   - Take returns nil;
	//   - OnDurableBlock trips FREEZE (CauseLedgerUninit).
	Init(s LedgerInit) error

	// Totals returns the current window totals.
	Totals() Totals

	// Take is §4.2 step 2. It moves every pending ID to in-flight and returns
	// them grouped by address, sorted by MinerAddr. The driver calls it only
	// while State().PayoutsEnabled().
	Take() []Claim

	// PreSeal is §4.2 step 3, the §6.3 checks. txs are exactly the reward
	// txs built from the last Take, in nonce order, with no heartbeats. A
	// claim whose float share is <= 0 is still passed, with that amount. The
	// checks are:
	//   - every in-flight ID is in exactly one payload, and every payload
	//     ID is in flight, unpaid and bound to no other tx;
	//   - each Recipient equals the rows' miner_addr;
	//   - each Amount > 0;
	//   - the sum of amounts <= rewardCell*(1+RewardSumSlack);
	//   - Emitted + the sum <= BudgetCell.
	// On success it binds the txs for I1. On failure it trips FREEZE
	// (CausePreSeal), returns every in-flight ID to pending, and returns an
	// error that wraps ErrPreSeal. The driver then seals a heartbeat.
	PreSeal(height uint64, rewardCell float64, txs []*mempool.Tx) error

	// Requeue returns every in-flight ID to pending and drops the tx
	// bindings. The driver calls it after its own txs have left the pool:
	//   - after a Pool.Add failure;
	//   - after a SAFE error;
	//   - after SUCCESS, when OnDurableBlock has already moved the included
	//     IDs to paid.
	// It is never called after POST-APPLY.
	Requeue()

	// OnDurableBlock is H8(b), canary only, called with sealLifecycleMu held.
	// In order, it:
	//   - moves the block's LMP1 IDs to paid (the chain is the record of
	//     payment, W3);
	//   - checks I1-I6;
	//   - calls Store.MarkPaid in one DB transaction;
	//   - counts missedSeals for local seals while payouts are enabled, and
	//     trips CauseStall at StallSeals;
	//   - calls Guard.ObserveTotals.
	// Any failure trips FREEZE and never exits. A non-local block trips
	// CauseNonLocalBlock. The returned error is for logging only.
	OnDurableBlock(blk *chain.Block, local bool) error
}

// -----------------------------------------------------------------------------
// Chain view (miningsvc Config.Producer) and fail-stop
// -----------------------------------------------------------------------------

// ChainView is the read-only chain surface of miningsvc. It serves WorkAt,
// HeaderHashAt and the accept height. cmd/qsdm passes durableChainView, which
// clamps all three methods to the durable tip (W5) and reports no tip until
// S5 sets it. *chain.BlockProducer also satisfies ChainView, so the pinned
// miningsvc tests still compile.
type ChainView interface {
	HasTip() bool
	TipHeight() uint64
	GetBlock(height uint64) (*chain.Block, bool)
}

// FailStopFunc is failStop in cmd/qsdm/failstop.go (WP9). It runs D2
// (FAILSTOP.armed -> FAILSTOP.json), writes FAILSTOP.cause.json best effort,
// then exits with code, which is always ExitFailStop. It never returns, may
// be called with any lock held, and is guarded by a sync.Once (L6). cause is
// one of:
//   - CausePersistPrefix + "H<k>:<err>"
//   - CausePostApplyPrefix + "<err>"
//   - CauseMarkerIO
type FailStopFunc func(code int, cause string)

// -----------------------------------------------------------------------------
// Served watermark W (§3.1): cmd/qsdm (S5, H7, replay) and cmd/hl1-tail
// -----------------------------------------------------------------------------

// Watermark is the content of WatermarkFile. ServedTip, FollowerHeight and
// FollowerHash are present only when Source is WatermarkSourceSeed.
type Watermark struct {
	Version        int     `json:"version"` // WatermarkVersion
	Height         uint64  `json:"height"`
	Hash           string  `json:"hash"`
	Source         string  `json:"source"`
	WrittenNS      int64   `json:"written_ns"`
	ServedTip      *uint64 `json:"served_tip,omitempty"`
	FollowerHeight *uint64 `json:"follower_height,omitempty"`
	FollowerHash   string  `json:"follower_hash,omitempty"`
}

const (
	WatermarkVersion      = 1
	WatermarkSourceSeal   = "seal"
	WatermarkSourceBoot   = "boot"
	WatermarkSourceReplay = "replay"
	WatermarkSourceSeed   = "seed"
)

// -----------------------------------------------------------------------------
// File names and markers (§3.1, Appendix A)
// -----------------------------------------------------------------------------

// Names in the state directory, Dir(cfg.SQLitePath).
const (
	StateLockFile          = "qsdm-validator.state.lock" // flock; not a marker
	WatermarkFile          = "hl1-served-watermark.json"
	WatermarkRetiredPrefix = WatermarkFile + ".retired-" // + <ts>
	// TempPrefix starts every D1 temp file:
	// .hl1-tmp-<final>-<pid>-<16 hex random>. S3 and the mutating hl1-tail
	// subcommands remove stale ones from both HL1 directories, under the
	// state lock only.
	TempPrefix = ".hl1-tmp-"
	// GenerationLinkFormat names a generation hard link. The arguments are
	// the snapshot file name and the height, for example
	// qsdm_accounts.json.h<N>.
	GenerationLinkFormat = "%s.h%d"
	// LegacyDirName is the legacy-mining directory under the state directory
	// (mode 0700). It holds DBFile and the legacy-mining markers.
	LegacyDirName = "legacy-mining"
	DBFile        = "legacy-mining.db"
)

// Markers. D2 trips a marker with rename(<base>+ArmedSuffix,
// <base>+TrippedSuffix) and then fsync(dir). FAILSTOP is in the state
// directory; the others are in LegacyDirName.
const (
	ArmedSuffix   = ".armed"
	TrippedSuffix = ".json"
	CauseSuffix   = ".cause.json"

	MarkerFailStop         = "FAILSTOP"
	MarkerTripped          = "TRIPPED"           // FROZEN
	MarkerAdmissionStopped = "ADMISSION_STOPPED" // ADMISSION_STOPPED

	FailStopArmedFile         = MarkerFailStop + ArmedSuffix
	FailStopFile              = MarkerFailStop + TrippedSuffix
	FailStopCauseFile         = MarkerFailStop + CauseSuffix
	TrippedArmedFile          = MarkerTripped + ArmedSuffix
	TrippedFile               = MarkerTripped + TrippedSuffix
	TrippedCauseFile          = MarkerTripped + CauseSuffix
	AdmissionStoppedArmedFile = MarkerAdmissionStopped + ArmedSuffix
	AdmissionStoppedFile      = MarkerAdmissionStopped + TrippedSuffix
	AdmissionStoppedCauseFile = MarkerAdmissionStopped + CauseSuffix

	// KillFile is created by the operator (§6.7). It is never armed or renamed.
	KillFile = "KILL"
)

// ArmedMarker is the content of an .armed marker, and so of its tripped .json
// after the D2 rename. For FAILSTOP.armed it is written at S3.
type ArmedMarker struct {
	Release string `json:"release"`
	BootNS  int64  `json:"boot_ns"`
	PID     int    `json:"pid"`
}

// MarkerCause is the content of a *.cause.json file, written best effort.
type MarkerCause struct {
	Cause string `json:"cause"`
	AtNS  int64  `json:"at_ns"`
}

// -----------------------------------------------------------------------------
// Exit codes (Appendix A)
// -----------------------------------------------------------------------------

const (
	// ExitFatalRestore (78) is a boot refusal (fatalRestore, S0-S5, a replay
	// precondition). RestartPreventExitStatus excludes it from auto-restart.
	ExitFatalRestore = 78
	// ExitFailStop (86) is the runtime fail-stop: an H1-H7 I/O error,
	// POST-APPLY or marker I/O. It is not auto-restarted, and later starts
	// exit 78 (S1) until R-FS.
	ExitFailStop = 86

	// hl1-tail exit codes.
	TailExitOK       = 0
	TailExitIO       = 1 // I/O error; rerun after fixing the cause
	TailExitRefused  = 2 // a precondition failed; follow the runbook, usually R-X
	TailExitLockBusy = 3 // the state lock is held; R-STOP first
)

// -----------------------------------------------------------------------------
// Thresholds (§5 S16, §6.3, §6.6)
// -----------------------------------------------------------------------------

const (
	QuietPeriod   = 120 * time.Second // S16: minimum time from Activate to admission
	QuietSeals    = 7                 // S16: local durable seals before admission
	StallSeals    = 30                // FREEZE when a pending ID has missed this many consecutive local seals
	NoSealTimeout = 60 * time.Second  // FREEZE after this long with no local durable seal

	TriggerWindow       = 10 * time.Minute // window for the two limits below
	NotAllowlistedLimit = 5                // ADMISSION_STOP at this many non-allowlisted submissions
	DuplicateLimit      = 10               // ADMISSION_STOP above this many duplicates or nonce conflicts
	RateBurstFactor     = 2                // ADMISSION_STOP above RateBurstFactor*MaxProofsPerMin submissions ...
	RateBurstMinutes    = 3                // ... in each of this many consecutive minutes

	BudgetStopMarginCells = 2               // ADMISSION_STOP at Emitted >= B - 2*rewardCell(h)
	RewardSumSlack        = 1.0 / (1 << 40) // §6.3: sum of amounts <= rewardCell(h)*(1+2^-40)
)

// -----------------------------------------------------------------------------
// Trip causes (§6.6). Freeze and StopAdmission record them in *.cause.json and
// in the events table. A detail may be appended as "<cause>:<detail>".
// -----------------------------------------------------------------------------

const (
	// FREEZE.
	CauseDBOpen           = "db-open"              // S7
	CauseDBMissing        = "db-missing"           // S8: a tagged chain with no DB
	CauseReconcile        = "reconcile"            // S9-S11 anomaly
	CausePreArm           = "pre-arm"              // S14 PreArm failure
	CauseAcceptIO         = "accept-io"            // §4.1 step 8, non-UNIQUE error
	CauseEnqueue          = "enqueue"              // Sink.Enqueue invariant
	CausePreSeal          = "pre-seal"             // §6.3, including a share <= 0
	CauseInvariant        = "invariant"            // I1-I6, as "invariant:I<n>"
	CauseTxFamily         = "tx-family"            // I7 in canary
	CauseMarkPaidIO       = "markpaid-io"          // Store.MarkPaid failure at H8
	CauseNonLocalBlock    = "non-local-block"      // a non-local block reached H8
	CauseLedgerUninit     = "ledger-uninitialized" // H8 before Ledger.Init
	CauseUnknownSafeError = "unknown-safe-error"   // §4.3 SAFE with an unknown error
	CauseStall            = "stall"                // StallSeals
	CauseNoSeal           = "no-seal"              // NoSealTimeout

	// ADMISSION_STOP.
	CauseNotAllowlisted = "not-allowlisted"
	CauseDuplicates     = "duplicates"
	CauseRateBurst      = "rate-burst"
	CauseProofsTotal    = "proofs-total" // graceful
	CauseBudget         = "budget"       // graceful
	CauseExpired        = "expired"      // graceful

	// FAILSTOP (FailStopFunc).
	CauseMarkerIO        = "marker-io"
	CausePersistPrefix   = "persist:"            // + "H<k>:<err>"
	CausePostApplyPrefix = "produce:post-apply:" // + "<err>"
)

// -----------------------------------------------------------------------------
// Sentinel errors
// -----------------------------------------------------------------------------

var (
	// ErrUnavailable is the 503 class. Every non-400 *Rejection unwraps to it.
	// miningsvc must return fmt.Errorf("%w: %w", api.ErrMiningUnavailable,
	// err) for any err that matches it.
	ErrUnavailable = errors.New("legacymining: mining unavailable")

	ErrEnvPartial = errors.New("legacymining: partial QSDM_LEGACY_MINING_* environment")
	ErrConfig     = errors.New("legacymining: invalid canary config")
	ErrConfigHash = errors.New("legacymining: canary config sha256 mismatch")

	ErrDBMissing    = errors.New("legacymining: database missing")
	ErrUnsafePath   = errors.New("legacymining: unsafe legacy-mining path")
	ErrSchema       = errors.New("legacymining: unexpected database identity or schema")
	ErrClosed       = errors.New("legacymining: store closed")
	ErrPaidConflict = errors.New("legacymining: payment conflicts with stored proofs")

	ErrBadPayload     = errors.New("legacymining: invalid LMP1 payload")
	ErrNotInitialized = errors.New("legacymining: ledger not initialized")
	ErrPreSeal        = errors.New("legacymining: pre-seal check failed")
	ErrInvariant      = errors.New("legacymining: post-persist invariant violated")
	ErrReconcile      = errors.New("legacymining: reconciliation anomaly")
)

// Compile-time checks that the live chain types satisfy the contract.
var (
	_ ChainView     = (*chain.BlockProducer)(nil)
	_ AccountReader = (*chain.AccountStore)(nil)
)
