// Package blockdriver provides a single-validator block
// production loop for solo testnet bring-up. It is the
// counterweight to the standard validator's BFT-driven block
// path: when there are no peer validators to drive
// TryAppendExternalBlock via the BFT executor, the chain
// stays at tip=0 forever and accepted mining proofs accrue
// no on-chain effect. With this driver enabled, the validator
// itself periodically seals blocks, paying out pending mining
// rewards to the miner addresses recorded by the HL1 ledger.
//
// Scope:
//
//   - Behind QSDM_SOLO_VALIDATOR_MODE env gate. When the
//     gate is off, this package is dormant — the binary
//     compiles it in but never instantiates a Driver.
//
//   - HL1 (hardened legacy mining, design rev 4 §4.2-§4.3):
//     accepted proofs live in a legacymining.Ledger, not in
//     this package. Without a Ledger (Stage A) the driver
//     seals heartbeats only.
//
//   - Each tick (default every 10s) takes every pending proof
//     ID, issues one transfer-tx per unique miner address
//     from a long-lived "system funder" account carrying all
//     of that address's IDs in an LMP1 payload, and calls
//     producer.ProduceBlock(). The driver bypasses BFT/POL
//     gates entirely (see cmd/qsdm/main.go for the conditional
//     SetBFTSealGate / SetPreSealBFTRound skip in solo mode).
//     The outcome is classified as SUCCESS, SAFE or
//     POST-APPLY (§4.3); POST-APPLY fail-stops the process.
//
//   - Reward distribution is proportional: a fixed
//     per-block reward (default 1.0 CELL) is split across
//     unique miner addresses by their pending proof count.
//     A no-mining window still seals an empty heartbeat
//     block so the chain advances; metrics still track
//     block-time.
//
// Out of scope:
//
//   - Long-term tokenomics. Production QSDM rewards come
//     from §8 emission curve + halving epochs; this driver
//     uses a flat-rate testnet model to make the bring-up
//     loop visible (miner balance grows in /api/v1/wallet/
//     balance/{addr}). Crossing over to the real curve is
//     a follow-on once a peer-validator is online and BFT
//     drives blocks naturally.
//
//   - Rollback / reorg. The driver assumes a single
//     monotonic tip with no forks (true on a solo network).
//     Once a peer joins, the driver should be disabled to
//     hand block production back to BFT.
package blockdriver

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// FunderAddress is the well-known account that funds reward
// payouts in solo mode. Exported so the genesis-seal hook in
// cmd/qsdm/main.go can use the same address — the driver
// expects to inherit that account's nonce when it boots.
const FunderAddress = chain.MiningRewardFunderAddress

// Defaults for the operator-tunable Config fields. Picked so
// a fresh QSDM_SOLO_VALIDATOR_MODE=1 boot has visible
// behaviour without further configuration.
const (
	// DefaultPeriod is the gap between block-seal attempts.
	// Matches chain.DefaultTargetBlockTimeSeconds (10 s) so
	// the solo-mode chain produces blocks at the same cadence
	// the §8 emission schedule assumes. Drifting away from
	// 10 s would silently mis-tune the apparent annual
	// inflation reported by /api/v1/status.
	DefaultPeriod = 10 * time.Second

	// DefaultFunderBalance seeds FunderAddress at startup.
	// 1e15 CELL (1 quadrillion) is far above the 90 M CELL
	// supply cap; intentional, because in solo mode the
	// supply cap is enforced inside the driver via the
	// EmissionSchedule (rewards taper to 0 once the cap is
	// hit). The funder simply has to outlive the longest
	// emission run; oversizing it costs nothing because no
	// txs ever debit it past what the schedule emits.
	DefaultFunderBalance = 1e15
)

// Config bundles collaborators the Driver needs. The zero
// value is INVALID; New checks every required field.
type Config struct {
	// Producer is the live block producer. REQUIRED.
	// In solo mode, the cmd/qsdm wiring deliberately leaves
	// SetBFTSealGate and SetPreSealBFTRound unset so
	// ProduceBlock proceeds without consulting BFT.
	Producer *chain.BlockProducer

	// Pool is the validator's admission-gated mempool.
	// REQUIRED. In solo mode the gate is configured
	// permissively (no BFT/POL extension predicate); see
	// the v2wiring.ReinstallAdmissionGate(adminPool, nil)
	// branch in cmd/qsdm/main.go.
	Pool *mempool.Mempool

	// Accounts is the live account store the producer's
	// applier mutates. REQUIRED. The driver seeds the funder
	// here at New time (idempotent — Credit on a re-Init
	// adds, but FunderInitialBalance is only added once via
	// the new-account branch).
	Accounts *chain.AccountStore

	// Logger is the structured logger to write block-seal /
	// payout / failure events to. REQUIRED so operators have
	// a paper-trail of the solo-mode behaviour.
	Logger *logging.Logger

	// FailStop is called with legacymining.ExitFailStop when
	// ProduceBlock ends POST-APPLY (§4.3). REQUIRED in every
	// mode. In production it is cmd/qsdm's failStop and never
	// returns; if it does return (tests), the driver halts and
	// seals nothing more.
	FailStop legacymining.FailStopFunc

	// Ledger holds the pending, in-flight and paid proof IDs
	// (canary mode). Nil means Stage A: the driver seals
	// heartbeats only. Ledger and Guard are both set or both
	// nil.
	Ledger legacymining.Ledger

	// Guard is the canary guard. The driver reads State on
	// every tick and pays only while State().PayoutsEnabled();
	// it trips Freeze on a pre-seal failure (§6.3) or an
	// unknown SAFE error (§4.3).
	Guard legacymining.Guard

	// LocalSeal is the local-seal flag shared with the
	// cmd/qsdm persistence hook, which reads it at H8. The
	// driver sets it immediately before ProduceBlock and
	// clears it immediately after. Nil allocates a private
	// flag.
	LocalSeal *atomic.Bool

	// Period is the tick interval. Zero uses DefaultPeriod.
	Period time.Duration

	// EmissionSchedule, when non-nil, is the canonical
	// §8 emission curve used to compute the per-block reward
	// at the height being sealed. Nil falls back to
	// chain.DefaultEmissionSchedule(), the mainnet schedule
	// (90 M CELL cap, 4-year halvings, 10 s blocks).
	//
	// Zero values for EmissionSchedule are not allowed —
	// New rejects a Config whose EmissionSchedule is set but
	// has BlocksPerEpoch == 0 (the canonical sentinel for an
	// uninitialised schedule). Use chain.NewEmissionSchedule
	// to build custom schedules in tests.
	EmissionSchedule *chain.EmissionSchedule

	// FlatRewardPerBlock, when > 0, OVERRIDES EmissionSchedule
	// and pays exactly this many CELL per block, split among
	// miners. Provided for tests and dust-truncation-free
	// scenarios; production should leave it 0 and let the
	// schedule drive emissions.
	FlatRewardPerBlock float64

	// FunderInitialBalance is the balance credited to
	// FunderAddress at New time IF the account doesn't yet
	// exist. Zero uses DefaultFunderBalance.
	FunderInitialBalance float64

	// Producer ID stamp for "heartbeat" blocks (no miners
	// in the window). Zero/empty uses "qsdm-solo-blockdriver".
	ProducerID string

	// RewardPenalty, when non-nil, is consulted at tick time
	// to scale each miner's per-block share by a multiplier
	// in [0.0, 1.0]. Wired by the validator binary when
	// QSDM_SPEC_PENALTY_ENABLED is set; nil leaves rewards
	// at their full per-proof share (the pre-Tier-3 posture).
	//
	// The penalty layer is OFF the consensus path — the
	// proofs that earn rewards have already passed every
	// consensus check. Tier-3 is purely an emission
	// modulation: penalised miners earn less, the rest of
	// the pie stays the same (i.e. the unused share is
	// NOT redistributed to honest miners, it is simply
	// unminted). See pkg/mining/telemetrycheck/penalty.go
	// for the full design rationale.
	RewardPenalty RewardPenalty
}

// RewardPenalty is the narrow contract Driver consumes.
// Identical in shape (intentionally) to
// pkg/mining/telemetrycheck.MismatchPenalty's hot path
// but redeclared here so blockdriver does not import the
// telemetrycheck package — keeps the dependency direction
// flowing exclusively from cmd/qsdm.
//
// Implementations MUST be concurrency-safe AND MUST NOT
// block on I/O — Driver.tick calls MultiplierFor inside
// the lock-free section on the single block-production
// goroutine.
type RewardPenalty interface {
	// MultiplierFor returns a value in [0.0, 1.0] that
	// scales the miner's share of the next block's
	// reward. 1.0 = no penalty, 0.0 = full forfeit.
	MultiplierFor(minerAddr string) float64
}

// noopRewardPenalty is the default. Always returns 1.0
// so the buildTxs hot path can call MultiplierFor
// unconditionally regardless of whether Tier-3 was
// wired at boot.
type noopRewardPenalty struct{}

func (noopRewardPenalty) MultiplierFor(string) float64 { return 1.0 }

// Driver is the periodic block-production loop. It holds no
// mutex: the tick runs on the single goroutine started by
// Start, and every field read elsewhere (Stats) is atomic. So
// no driver, HL1 or chain lock is held across Pool.Add,
// ProduceBlock or Pool.Remove (§4.5 L1).
type Driver struct {
	cfg Config

	// schedule is the resolved emission curve used by tick().
	// Always non-nil after New (defaulted from
	// chain.DefaultEmissionSchedule when the caller didn't
	// pass one). Held by value because EmissionSchedule has
	// no mutable state and is cheap to copy.
	schedule chain.EmissionSchedule

	// totalEmittedCell is the running total of CELL paid out
	// across every block this driver has sealed. Used for
	// the cap check and exposed via Stats so tests can
	// confirm the emission curve was actually followed
	// instead of silently flat-lining at 0.
	totalEmittedCell atomic.Uint64 // dust units; load/.add via uint64

	// funderNonce tracks the next nonce to use on a tx whose
	// sender is FunderAddress. Initialised from the account
	// store at New (so a restart picks up where we left off
	// if/when persistence lands) and incremented atomically
	// from Tick because the tick goroutine is single-writer.
	funderNonce atomic.Uint64

	// localSeal is Config.LocalSeal, or a private flag.
	localSeal *atomic.Bool

	// halted is set on POST-APPLY. A halted driver never
	// ticks again (§4.3).
	halted atomic.Bool

	// now is the tx timestamp clock (time.Now; tests pin it).
	now func() time.Time

	// blocksSealed and blocksFailed are exposed via Stats
	// for tests and operator probes. Atomic so HTTP/metrics
	// readers need no lock.
	blocksSealed atomic.Uint64
	blocksFailed atomic.Uint64
	proofsPaid   atomic.Uint64

	// rewardPenalty is the resolved penalty source. Always
	// non-nil after New (defaulted to noopRewardPenalty
	// when the operator did not opt into Tier-3) so the
	// buildTxs hot path is branch-free.
	rewardPenalty RewardPenalty

	// penalisedPayouts counts the number of miner shares
	// that have been multiplied by a value < 1.0 across
	// the driver's lifetime. Surfaces in Stats() and
	// Prometheus so an operator can confirm the Tier-3
	// layer is firing as expected.
	penalisedPayouts atomic.Uint64

	// withheldDust tracks the cumulative dust NOT minted
	// because miners were over-threshold. Useful as a
	// sanity check that the penalty is shaping emissions
	// the way the operator intended.
	withheldDust atomic.Uint64

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{} // closed by run() on exit; Stop waits on it
}

// New validates cfg, seeds the funder account if needed, and
// returns a ready-to-Start Driver.
func New(cfg Config) (*Driver, error) {
	if cfg.Producer == nil {
		return nil, errors.New("blockdriver: Config.Producer is required")
	}
	if cfg.Pool == nil {
		return nil, errors.New("blockdriver: Config.Pool is required")
	}
	if cfg.Accounts == nil {
		return nil, errors.New("blockdriver: Config.Accounts is required")
	}
	if cfg.Logger == nil {
		return nil, errors.New("blockdriver: Config.Logger is required")
	}
	if cfg.FailStop == nil {
		return nil, errors.New("blockdriver: Config.FailStop is required")
	}
	if (cfg.Ledger == nil) != (cfg.Guard == nil) {
		return nil, errors.New("blockdriver: Config.Ledger and Config.Guard must be set together")
	}
	if cfg.Period <= 0 {
		cfg.Period = DefaultPeriod
	}
	if cfg.FunderInitialBalance <= 0 {
		cfg.FunderInitialBalance = DefaultFunderBalance
	}
	if cfg.ProducerID == "" {
		cfg.ProducerID = "qsdm-solo-blockdriver"
	}

	// Resolve the emission schedule. A caller-supplied
	// EmissionSchedule with BlocksPerEpoch == 0 is the
	// hallmark of `var s chain.EmissionSchedule` (zero value)
	// being passed by mistake — reject it loudly rather than
	// silently producing 0-reward forever.
	var schedule chain.EmissionSchedule
	switch {
	case cfg.EmissionSchedule != nil:
		if cfg.EmissionSchedule.BlocksPerEpoch == 0 {
			return nil, errors.New("blockdriver: Config.EmissionSchedule has BlocksPerEpoch == 0; use chain.NewEmissionSchedule to construct a valid schedule")
		}
		schedule = *cfg.EmissionSchedule
	default:
		schedule = chain.DefaultEmissionSchedule()
	}

	rp := cfg.RewardPenalty
	if rp == nil {
		rp = noopRewardPenalty{}
	}
	localSeal := cfg.LocalSeal
	if localSeal == nil {
		localSeal = new(atomic.Bool)
	}

	d := &Driver{
		cfg:           cfg,
		schedule:      schedule,
		localSeal:     localSeal,
		now:           time.Now,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
		rewardPenalty: rp,
	}

	// Seed the funder balance only if the account is brand new
	// or has been reset to zero. Repeat boots (e.g. systemctl
	// restart with persistent state) MUST NOT keep adding to
	// the funder's balance because that would break replay
	// determinism the moment a peer validator joins. Today
	// BLR1 has no persistence so this branch always fires;
	// the test-time check is what makes it future-safe.
	acc, exists := cfg.Accounts.Get(FunderAddress)
	if !exists || acc.Balance == 0 {
		cfg.Accounts.Credit(FunderAddress, cfg.FunderInitialBalance)
		acc, _ = cfg.Accounts.Get(FunderAddress)
	}
	if acc != nil {
		d.funderNonce.Store(acc.Nonce)
	}
	return d, nil
}

// SyncFunderNonce re-reads the funder account from the live
// AccountStore and replaces the driver's in-memory nonce
// counter with whatever's there. Call this after any
// out-of-band tx that mutates the funder (e.g. the genesis-
// seal heartbeat in cmd/qsdm/main.go) before Start, otherwise
// the very first tick will issue a tx with a stale nonce and
// the producer's ApplyTx will reject it.
//
// Idempotent and safe to call multiple times. No-op if the
// funder account does not exist (which would be a bug —
// New seeds it — but we tolerate the no-op rather than
// panic in a hot path).
func (d *Driver) SyncFunderNonce() {
	acc, ok := d.cfg.Accounts.Get(FunderAddress)
	if !ok || acc == nil {
		return
	}
	d.funderNonce.Store(acc.Nonce)
	d.cfg.Logger.Info("blockdriver: funder nonce resynced",
		"funder", FunderAddress,
		"new_nonce", acc.Nonce)
}

// Start kicks off the tick loop in a fresh goroutine. The
// loop runs until the supplied context is cancelled, Stop
// is called, or a POST-APPLY outcome halts the driver. Call
// it once per Driver.
func (d *Driver) Start(ctx context.Context) {
	go d.run(ctx)
}

// Stop signals the run goroutine to exit and blocks until
// it has actually returned, so callers can be sure no more
// writes will hit the logger / mempool / producer after Stop
// returns. Safe to call multiple times; only the first close
// signals exit, subsequent calls just re-wait on doneCh
// (which is already closed and so returns immediately).
func (d *Driver) Stop() {
	d.stopOnce.Do(func() { close(d.stopCh) })
	if d.doneCh != nil {
		<-d.doneCh
	}
}

// Stats returns a snapshot of operational counters. Used by
// tests and (eventually) /metrics endpoints.
type Stats struct {
	Period       time.Duration
	BlocksSealed uint64
	BlocksFailed uint64
	// ProofsPaid counts proof IDs carried by reward txs that
	// were included in a sealed block.
	ProofsPaid uint64
	// QueueDepth is the Ledger's pending plus in-flight
	// count (0 without a Ledger).
	QueueDepth  int
	FunderNonce uint64
	// EmittedDust is the running total of dust paid out to
	// miner addresses (heartbeat-only blocks don't count).
	// Useful in tests to confirm the schedule's cumulative
	// emission was actually followed.
	EmittedDust uint64
	// Schedule is a copy of the resolved emission schedule
	// the driver is using. Tests assert it matches the
	// caller's expectation; operators can introspect it via
	// /api/v1/mining/account or future /api/v1/mining/emission.
	Schedule chain.EmissionSchedule
	// FlatReward is true when the driver was configured with
	// FlatRewardPerBlock > 0, i.e. the schedule is being
	// overridden. Useful for tests + a future operator log.
	FlatReward bool
	// PenalisedPayouts is the running count of miner shares
	// that were multiplied by a value < 1.0 by the Tier-3
	// reward-penalty layer. Zero pre-Tier-3.
	PenalisedPayouts uint64
	// WithheldDust is the cumulative dust NOT minted because
	// miners were over the spec-mismatch threshold. Lifetime
	// counter. Zero pre-Tier-3.
	WithheldDust uint64
	// PenaltyActive is true when a non-noop RewardPenalty is
	// wired (i.e. Tier-3 is enabled).
	PenaltyActive bool
	// Halted is true after a POST-APPLY outcome.
	Halted bool
}

// Stats returns a snapshot of the driver's counters.
func (d *Driver) Stats() Stats {
	depth := 0
	if d.cfg.Ledger != nil {
		depth = d.cfg.Ledger.Outstanding()
	}
	_, isNoop := d.rewardPenalty.(noopRewardPenalty)
	return Stats{
		Period:           d.cfg.Period,
		BlocksSealed:     d.blocksSealed.Load(),
		BlocksFailed:     d.blocksFailed.Load(),
		ProofsPaid:       d.proofsPaid.Load(),
		QueueDepth:       depth,
		FunderNonce:      d.funderNonce.Load(),
		EmittedDust:      d.totalEmittedCell.Load(),
		Schedule:         d.schedule,
		FlatReward:       d.cfg.FlatRewardPerBlock > 0,
		PenalisedPayouts: d.penalisedPayouts.Load(),
		WithheldDust:     d.withheldDust.Load(),
		PenaltyActive:    !isNoop,
		Halted:           d.halted.Load(),
	}
}

func (d *Driver) run(ctx context.Context) {
	defer close(d.doneCh)
	t := time.NewTicker(d.cfg.Period)
	defer t.Stop()
	d.cfg.Logger.Info("blockdriver: started",
		"period", d.cfg.Period,
		"funder", FunderAddress,
		"funder_initial_balance", d.cfg.FunderInitialBalance,
		"flat_reward_cell", d.cfg.FlatRewardPerBlock,
		"schedule_cap_dust", d.schedule.MiningCapDust,
		"schedule_blocks_per_epoch", d.schedule.BlocksPerEpoch,
		"schedule_epoch0_reward_dust", d.schedule.BlockRewardDust(1),
		"payouts_wired", d.cfg.Ledger != nil)
	for {
		select {
		case <-ctx.Done():
			d.cfg.Logger.Info("blockdriver: stopping (context cancelled)")
			return
		case <-d.stopCh:
			d.cfg.Logger.Info("blockdriver: stopping (Stop called)")
			return
		case <-t.C:
			d.tick()
			if d.halted.Load() {
				d.cfg.Logger.Error("blockdriver: stopping (halted after POST-APPLY)")
				return
			}
		}
	}
}

// tick is the single-writer path (§4.2): take the pending
// IDs, build and pre-seal the payout transactions, and ask
// the producer to seal a block, then classify the outcome
// (§4.3). It takes no lock.
func (d *Driver) tick() {
	if d.halted.Load() {
		return
	}

	// rewardForHeight is computed from the height we are
	// ABOUT to seal — that is, current tip + 1. Reading the
	// tip here (not at buildTxs time) lets us include the
	// computed reward in the seal log.
	nextHeight := d.cfg.Producer.TipHeight() + 1
	if !d.cfg.Producer.HasTip() {
		// Pre-genesis: no block is sealed yet, so the next
		// seal would be height 0 (genesis). Genesis carries
		// no reward per CELL_TOKENOMICS §3, so we pass 0.
		// The driver should not normally reach tick() before
		// genesis because cmd/qsdm seals genesis synchronously
		// before Start, but keep the path defensive.
		nextHeight = 0
	}
	rewardCell := d.rewardCellForHeight(nextHeight)
	rewardDust := d.rewardDustForHeight(nextHeight)

	// The AccountStore is authoritative for the next funder nonce. Never
	// reserve nonce space merely by constructing a transaction: a rejected
	// block must retry the same nonce or the reward stream develops a gap that
	// no later transaction can cross.
	funder, ok := d.cfg.Accounts.Get(FunderAddress)
	if !ok || funder == nil {
		d.cfg.Logger.Warn("blockdriver: funder account missing; retaining payouts")
		d.blocksFailed.Add(1)
		return
	}
	d.funderNonce.Store(funder.Nonce)

	// claims is non-nil exactly when txs are reward txs whose
	// IDs are in flight; txs[i] carries claims[i].
	txs, claims := d.planTxs(nextHeight, rewardCell, funder.Nonce)

	added := make([]*mempool.Tx, 0, len(txs))
	for _, tx := range txs {
		if err := d.cfg.Pool.Add(tx); err != nil {
			for _, admitted := range added {
				d.cfg.Pool.Remove(admitted.ID)
			}
			d.release(claims)
			d.SyncFunderNonce()
			d.cfg.Logger.Warn("blockdriver: pool admission failed; retaining payouts",
				"tx_id", tx.ID,
				"error", err.Error())
			d.blocksFailed.Add(1)
			return
		}
		added = append(added, tx)
	}

	fp0 := d.fingerprint()
	d.localSeal.Store(true)
	blk, err := d.cfg.Producer.ProduceBlock()
	d.localSeal.Store(false)

	switch classifyProduce(blk, err, fp0, d.fingerprint) {
	case produceSuccess:
		d.onSuccess(blk, txs, claims, rewardDust)
	case produceSafe:
		d.onSafe(err, txs, claims)
	default:
		d.onPostApply(err)
	}
}

// planTxs is §4.2 steps 1-3. It returns a single heartbeat
// unless payouts are wired and enabled and the Ledger has
// pending IDs. A pre-seal failure (§6.3) trips FREEZE,
// returns the IDs to pending and falls back to a heartbeat.
func (d *Driver) planTxs(height uint64, rewardCell float64, startNonce uint64) ([]*mempool.Tx, []legacymining.Claim) {
	heartbeat := func() ([]*mempool.Tx, []legacymining.Claim) {
		return []*mempool.Tx{d.heartbeatTx(startNonce)}, nil
	}
	if d.cfg.Ledger == nil {
		return heartbeat()
	}
	// State is read on every tick: it stats KILL and
	// evaluates the guard's time-based triggers.
	if state := d.cfg.Guard.State(); !state.PayoutsEnabled() {
		return heartbeat()
	}
	claims := d.cfg.Ledger.Take()
	if len(claims) == 0 {
		return heartbeat()
	}
	txs, err := d.buildTxs(claims, rewardCell, startNonce)
	if err == nil {
		// A share <= 0 is passed through; PreSeal rejects it.
		err = d.cfg.Ledger.PreSeal(height, rewardCell, txs)
	}
	if err == nil {
		// Backstop for the share check, independent of the
		// Ledger implementation.
		err = checkShares(txs)
	}
	if err != nil {
		// Freeze is idempotent and keeps the first cause, and
		// Requeue is a no-op once PreSeal has already
		// returned the IDs, so both are safe to repeat here.
		d.cfg.Guard.Freeze(legacymining.CausePreSeal + ":" + err.Error())
		d.cfg.Ledger.Requeue()
		d.cfg.Logger.Error("blockdriver: pre-seal check failed; FREEZE, payouts retained, sealing heartbeat",
			"height", height,
			"claims", len(claims),
			"error", err.Error())
		return heartbeat()
	}
	return txs, claims
}

// release returns every in-flight ID to pending. It is a
// no-op for heartbeat ticks. Never call it after POST-APPLY.
func (d *Driver) release(claims []legacymining.Claim) {
	if claims != nil {
		d.cfg.Ledger.Requeue()
	}
}

// onSuccess is §4.2 step 7. H8 has already moved the
// included IDs to paid. Own txs that were not included are
// removed from the pool before their IDs are released, so no
// stale reward tx can be sealed later next to its
// replacement.
func (d *Driver) onSuccess(blk *chain.Block, txs []*mempool.Tx, claims []legacymining.Claim, rewardDust uint64) {
	included := make(map[string]struct{}, len(blk.Transactions))
	for _, tx := range blk.Transactions {
		if tx != nil {
			included[tx.ID] = struct{}{}
		}
	}
	paidProofs := 0
	emittedDust := uint64(0)
	var notIncluded []string
	for i, tx := range txs {
		if _, ok := included[tx.ID]; !ok {
			d.cfg.Pool.Remove(tx.ID)
			notIncluded = append(notIncluded, tx.ID)
			continue
		}
		if claims != nil {
			paidProofs += len(claims[i].IDs)
			emittedDust += uint64(tx.Amount * float64(chain.DustPerCell))
		}
	}
	d.release(claims)
	if acc, exists := d.cfg.Accounts.Get(FunderAddress); exists && acc != nil {
		d.funderNonce.Store(acc.Nonce)
	}
	if len(notIncluded) > 0 {
		d.cfg.Logger.Warn("blockdriver: own transactions not included; removed from pool, payouts retained",
			"height", blk.Height,
			"tx_ids", notIncluded)
	}
	d.blocksSealed.Add(1)
	d.proofsPaid.Add(uint64(paidProofs))
	if emittedDust > 0 {
		// totalEmittedCell tracks dust we actually paid out
		// (i.e. excluding heartbeat-only blocks where
		// no IDs were pending and the reward goes unclaimed).
		// Mirrors the "no proofs => no emission" rule
		// CELL_TOKENOMICS implies for solo testnet bring-up.
		d.totalEmittedCell.Add(emittedDust)
	}
	d.cfg.Logger.Info("blockdriver: block sealed",
		"height", blk.Height,
		"hash", blk.Hash,
		"tx_count", len(blk.Transactions),
		"payouts", len(claims),
		"proofs_paid", paidProofs,
		"reward_dust", rewardDust,
		"reward_cell", d.schedule.BlockRewardCell(blk.Height),
		"epoch", d.schedule.EpochForHeight(blk.Height),
		"penalised_payouts_total", d.penalisedPayouts.Load(),
		"withheld_dust_total", d.withheldDust.Load())
}

// onSafe handles a ProduceBlock error that left the account
// state unchanged (§4.3 SAFE). ProduceBlock may have restored
// the drained batch to the pool, so every own tx is removed
// before the IDs are released and the next tick rebuilds them
// from the authoritative nonce.
func (d *Driver) onSafe(err error, txs []*mempool.Tx, claims []legacymining.Claim) {
	for _, tx := range txs {
		d.cfg.Pool.Remove(tx.ID)
	}
	d.release(claims)
	d.SyncFunderNonce()
	d.blocksFailed.Add(1)
	if isKnownSafeError(err) {
		d.cfg.Logger.Warn("blockdriver: ProduceBlock failed (SAFE)",
			"error", err.Error(),
			"queued_payouts", len(claims))
		return
	}
	d.cfg.Logger.Error("blockdriver: ProduceBlock failed with an unknown error; state unchanged (SAFE)",
		"error", err.Error(),
		"queued_payouts", len(claims))
	if d.cfg.Guard != nil {
		d.cfg.Guard.Freeze(legacymining.CauseUnknownSafeError + ":" + err.Error())
	}
}

// onPostApply handles §4.3 POST-APPLY: live state already
// holds the block's effects but nothing of it was persisted.
// Nothing is removed, released or resynced; the driver halts
// and fail-stops, so a restart restores the last durable
// block.
func (d *Driver) onPostApply(err error) {
	d.halted.Store(true)
	d.blocksFailed.Add(1)
	msg := "nil block with nil error"
	if err != nil {
		msg = err.Error()
	}
	cause := legacymining.CausePostApplyPrefix + msg
	d.cfg.Logger.Error("blockdriver: ProduceBlock failed after live apply (POST-APPLY); fail-stop",
		"cause", cause)
	d.cfg.FailStop(legacymining.ExitFailStop, cause)
}

// produceClass is the §4.3 outcome of one ProduceBlock call.
type produceClass int

const (
	produceSuccess produceClass = iota
	produceSafe
	producePostApply
)

// fingerprint is the account-state fingerprint of §4.2:
// AccountStore.StateRoot (which hashes every nonce), plus the
// funder's nonce and balance bits.
type fingerprint struct {
	stateRoot     string
	funderExists  bool
	funderNonce   uint64
	funderBalance uint64 // math.Float64bits
}

func (d *Driver) fingerprint() fingerprint {
	fp := fingerprint{stateRoot: d.cfg.Accounts.StateRoot()}
	if acc, ok := d.cfg.Accounts.Get(FunderAddress); ok && acc != nil {
		fp.funderExists = true
		fp.funderNonce = acc.Nonce
		fp.funderBalance = math.Float64bits(acc.Balance)
	}
	return fp
}

// classifyProduce implements the §4.3 table. after is
// evaluated only for an error.
func classifyProduce(blk *chain.Block, err error, before fingerprint, after func() fingerprint) produceClass {
	if err == nil {
		if blk != nil {
			return produceSuccess
		}
		return producePostApply
	}
	if after() != before {
		return producePostApply
	}
	return produceSafe
}

// knownSafeErrors and knownSafeMessages are the ProduceBlock
// errors of §4.3 that return before any state mutation. Any
// other error with an unchanged fingerprint is still SAFE but
// trips FREEZE.
var knownSafeErrors = []error{
	chain.ErrSealGuardBlocked,
	chain.ErrPolExtensionBlocked,
	chain.ErrBFTExtensionBlocked,
	chain.ErrPreSealRequiresAccountStore,
	chain.ErrBlockUnsigned,
	chain.ErrExternalProducerNotAuthorized,
}

var knownSafeMessages = map[string]struct{}{
	"chain: local production requires approved transition checkpoint": {},
	"chain: producer transition checkpoint is not established":        {},
	"no transactions to include":                                      {},
	"all transactions failed state application":                       {},
}

func isKnownSafeError(err error) bool {
	for _, known := range knownSafeErrors {
		if errors.Is(err, known) {
			return true
		}
	}
	_, ok := knownSafeMessages[err.Error()]
	return ok
}

// checkShares fails when any reward amount is not a finite
// value > 0 (§6.3; d7 silently skipped a share <= 0).
func checkShares(txs []*mempool.Tx) error {
	for _, tx := range txs {
		if !(tx.Amount > 0) || math.IsInf(tx.Amount, 0) {
			return fmt.Errorf("%w: share %v for %s is not > 0", legacymining.ErrPreSeal, tx.Amount, tx.Recipient)
		}
	}
	return nil
}

// rewardDustForHeight returns the dust reward for the given
// block height honouring the schedule unless the operator
// passed FlatRewardPerBlock > 0. FlatRewardPerBlock is in
// whole CELL and is converted to dust once via float→uint64
// truncation; values > 9e7 (90 M CELL) are clamped to the
// schedule's MiningCapDust to keep tests safe from overflow.
func (d *Driver) rewardDustForHeight(height uint64) uint64 {
	if d.cfg.FlatRewardPerBlock > 0 {
		dust := uint64(d.cfg.FlatRewardPerBlock * float64(chain.DustPerCell))
		if dust > d.schedule.MiningCapDust {
			dust = d.schedule.MiningCapDust
		}
		return dust
	}
	if height == 0 {
		return 0
	}
	return d.schedule.BlockRewardDust(height)
}

// rewardCellForHeight returns the float-CELL reward, the unit
// AccountStore.Credit speaks. Float64 precision is sufficient
// for any value ≤ 9e15 dust (the cap is 9e15 ≈ 2^53). The
// truncation residue between (float dust)/DustPerCell and the
// integer value is at most 1 ULP per block, which is
// 2^-52 ≈ 2.2e-16 — orders of magnitude below the 1-dust
// minimum the AccountStore can represent anyway.
func (d *Driver) rewardCellForHeight(height uint64) float64 {
	dust := d.rewardDustForHeight(height)
	return float64(dust) / float64(chain.DustPerCell)
}

// heartbeatTx is the zero-amount funder self-transfer that
// keeps the chain advancing. Its bytes are identical to d7.
func (d *Driver) heartbeatTx(nonce uint64) *mempool.Tx {
	now := d.now()
	return &mempool.Tx{
		ID:        fmt.Sprintf("solo-heartbeat-%d-%d", nonce, now.UnixNano()),
		Sender:    FunderAddress,
		Recipient: FunderAddress,
		Amount:    0,
		Fee:       0,
		Nonce:     nonce,
		AddedAt:   now,
	}
}

// buildTxs creates one reward transaction per claim, i.e. per
// unique miner address, with a reward proportional to that
// address's number of IDs and an LMP1 payload carrying all of
// them, or a single heartbeat when there are no claims. The
// producer's mempool refuses to seal an empty block, so we
// always emit at least one tx.
//
// The float formula and the tx shape are d7's; the reward ID
// gains the payload hash (legacymining.RewardIDFormat) and
// the tx gains the payload. A share <= 0 is no longer skipped:
// the tx is built with that amount and fails the pre-seal
// checks (§6.3).
//
// As of Tier-3, each per-miner share is also multiplied by
// the operator-configured RewardPenalty before being credited.
// Multipliers below 1.0 mint LESS dust than the schedule
// would normally allow — the unused share is unminted, NOT
// redistributed to other miners. That keeps the supply cap
// monotonically respected and makes the tokenomic effect of
// Tier-3 strictly subtractive.
func (d *Driver) buildTxs(claims []legacymining.Claim, rewardCell float64, startNonce uint64) ([]*mempool.Tx, error) {
	if len(claims) == 0 {
		return []*mempool.Tx{d.heartbeatTx(startNonce)}, nil
	}
	total := 0
	payloads := make([][]byte, len(claims))
	for i, c := range claims {
		// Take returns claims sorted by address; strictly
		// ascending also means one tx per address.
		if c.MinerAddr == "" {
			return nil, fmt.Errorf("%w: claim %d has no miner address", legacymining.ErrPreSeal, i)
		}
		if i > 0 && c.MinerAddr <= claims[i-1].MinerAddr {
			return nil, fmt.Errorf("%w: claims not strictly ascending by address at %q", legacymining.ErrPreSeal, c.MinerAddr)
		}
		payload, err := encodePayload(c.IDs)
		if err != nil {
			return nil, fmt.Errorf("%w: claim for %s: %w", legacymining.ErrPreSeal, c.MinerAddr, err)
		}
		payloads[i] = payload
		total += len(c.IDs)
	}

	now := d.now()
	nextNonce := startNonce
	out := make([]*mempool.Tx, 0, len(claims))
	for i, c := range claims {
		addr := c.MinerAddr
		count := len(c.IDs)
		baseShare := rewardCell * float64(count) / float64(total)
		mult := d.rewardPenalty.MultiplierFor(addr)
		// Defensive clamp: if a buggy MismatchPenalty
		// returns NaN / Inf / negative / >1 we round it
		// to a safe band rather than mint anything weird.
		if !(mult >= 0 && mult <= 1) {
			mult = 1.0
		}
		share := baseShare * mult
		// A share <= 0 is a pre-seal failure; like d7, it is
		// not counted as a penalised payout.
		if share > 0 && mult < 1.0 {
			d.penalisedPayouts.Add(1)
			// withheldDust = (baseShare - share) in dust.
			// Truncate via float→uint64 to mirror the same
			// rounding the producer uses for credits.
			withheld := uint64((baseShare - share) * float64(chain.DustPerCell))
			if withheld > 0 {
				d.withheldDust.Add(withheld)
			}
		}
		sum := sha256.Sum256(payloads[i])
		out = append(out, &mempool.Tx{
			ID:         fmt.Sprintf(legacymining.RewardIDFormat, nextNonce, addr, hex.EncodeToString(sum[:8])),
			Sender:     FunderAddress,
			Recipient:  addr,
			Amount:     share,
			Fee:        0,
			Nonce:      nextNonce,
			Payload:    payloads[i],
			ContractID: chain.MiningRewardContractID,
			AddedAt:    now,
		})
		nextNonce++
	}
	return out, nil
}

// encodePayload is the LMP1 encoding of §3.4:
// legacymining.PayloadTag, a u16 big-endian count
// (1..MaxPayloadIDs), then the IDs in strictly ascending
// order. It matches legacymining.EncodePayload (WP3); the
// Ledger decodes every payload again in PreSeal, so a
// mismatch fails toward FREEZE.
func encodePayload(ids []legacymining.ProofID) ([]byte, error) {
	if len(ids) == 0 || len(ids) > legacymining.MaxPayloadIDs {
		return nil, fmt.Errorf("%w: %d IDs, want 1..%d", legacymining.ErrBadPayload, len(ids), legacymining.MaxPayloadIDs)
	}
	out := make([]byte, 0, len(legacymining.PayloadTag)+2+len(ids)*len(ids[0]))
	out = append(out, legacymining.PayloadTag...)
	out = binary.BigEndian.AppendUint16(out, uint16(len(ids)))
	for i := range ids {
		if i > 0 && string(ids[i][:]) <= string(ids[i-1][:]) {
			return nil, fmt.Errorf("%w: IDs not strictly ascending at index %d", legacymining.ErrBadPayload, i)
		}
		out = append(out, ids[i][:]...)
	}
	return out, nil
}
