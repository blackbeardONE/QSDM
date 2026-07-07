// Package blockdriver provides a single-validator block
// production loop for solo testnet bring-up. It is the
// counterweight to the standard validator's BFT-driven block
// path: when there are no peer validators to drive
// TryAppendExternalBlock via the BFT executor, the chain
// stays at tip=0 forever and accepted mining proofs accrue
// no on-chain effect. With this driver enabled, the validator
// itself periodically seals blocks, paying out queued mining
// rewards to the miner addresses recorded by miningsvc.
//
// Scope:
//
//   - Behind QSDM_SOLO_VALIDATOR_MODE env gate. When the
//     gate is off, this package is dormant — the binary
//     compiles it in but never instantiates a Driver.
//
//   - Implements miningsvc.RewardSink so accepted proofs
//     accrue per-address in an in-memory queue between ticks.
//
//   - Each tick (default every 10s) drains the queue,
//     issues one transfer-tx per unique miner address from a
//     long-lived "system funder" account, and calls
//     producer.ProduceBlock(). The driver bypasses BFT/POL
//     gates entirely (see cmd/qsdm/main.go for the conditional
//     SetBFTSealGate / SetPreSealBFTRound skip in solo mode).
//
//   - Reward distribution is proportional: a fixed
//     per-block reward (default 1.0 CELL) is split across
//     unique miner addresses by their accepted-proof count
//     in the window since the last block. A no-mining
//     window still seals an empty heartbeat block so the
//     chain advances; metrics still track block-time.
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
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/internal/miningsvc"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// Compile-time guard: Driver implements miningsvc.RewardSink.
// Drift here would break the cmd/qsdm wiring at boot.
var _ miningsvc.RewardSink = (*Driver)(nil)

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
// the queue-lock-free section but still on the single
// block-production goroutine.
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

// Driver is the periodic block-production loop. Safe for
// concurrent calls into OnAcceptedProof from any goroutine
// (the HTTP handlers); single-tick from the internal
// goroutine started by Start.
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

	mu     sync.Mutex
	queue  map[string]int // miner_addr -> proof count, drained per tick
	queued int            // total proofs queued across all addrs

	// blocksSealed and blocksFailed are exposed via Stats
	// for tests and operator probes. Atomic so HTTP/metrics
	// readers don't need to hold mu.
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

	d := &Driver{
		cfg:           cfg,
		schedule:      schedule,
		queue:         make(map[string]int, 16),
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

// OnAcceptedProof implements miningsvc.RewardSink. Pure
// O(1) increment under the queue mutex.
func (d *Driver) OnAcceptedProof(minerAddr string) {
	if minerAddr == "" {
		return
	}
	d.mu.Lock()
	d.queue[minerAddr]++
	d.queued++
	d.mu.Unlock()
}

// Start kicks off the tick loop in a fresh goroutine. The
// loop runs until the supplied context is cancelled OR Stop
// is called. Idempotent: a second Start on the same Driver
// is a no-op (the first goroutine still owns stopCh).
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
	ProofsPaid   uint64
	QueueDepth   int
	FunderNonce  uint64
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
}

// Stats returns a snapshot of the driver's counters.
func (d *Driver) Stats() Stats {
	d.mu.Lock()
	depth := d.queued
	d.mu.Unlock()
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
		"schedule_epoch0_reward_dust", d.schedule.BlockRewardDust(1))
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
		}
	}
}

// tick is the single-writer path. Drains the proof queue,
// builds payout transactions, and asks the producer to seal a
// block. All errors are logged but never panic — the goal is
// "keep the chain advancing through transient hiccups".
func (d *Driver) tick() {
	d.mu.Lock()
	drained := d.queue
	drainedCount := d.queued
	d.queue = make(map[string]int, 16)
	d.queued = 0
	d.mu.Unlock()

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
		d.requeueProofs(drained)
		d.cfg.Logger.Warn("blockdriver: funder account missing; retaining payouts")
		d.blocksFailed.Add(1)
		return
	}
	d.funderNonce.Store(funder.Nonce)
	txs := d.buildTxs(drained, drainedCount, rewardCell, funder.Nonce)
	added := make([]*mempool.Tx, 0, len(txs))
	for _, tx := range txs {
		if err := d.cfg.Pool.Add(tx); err != nil {
			for _, admitted := range added {
				d.cfg.Pool.Remove(admitted.ID)
			}
			d.requeueProofs(drained)
			d.SyncFunderNonce()
			d.cfg.Logger.Warn("blockdriver: pool admission failed; retaining payouts",
				"tx_id", tx.ID,
				"error", err.Error())
			d.blocksFailed.Add(1)
			return
		}
		added = append(added, tx)
	}

	blk, err := d.cfg.Producer.ProduceBlock()
	if err != nil {
		// ProduceBlock restores a failed batch to the mempool. Remove only the
		// transactions owned by this driver, retain their proof counts, and
		// rebuild them from the authoritative nonce on the next tick.
		for _, tx := range txs {
			d.cfg.Pool.Remove(tx.ID)
		}
		d.requeueProofs(drained)
		d.SyncFunderNonce()
		d.cfg.Logger.Warn("blockdriver: ProduceBlock failed",
			"error", err.Error(),
			"queued_payouts", len(drained))
		d.blocksFailed.Add(1)
		return
	}
	if blk == nil {
		for _, tx := range txs {
			d.cfg.Pool.Remove(tx.ID)
		}
		d.requeueProofs(drained)
		d.SyncFunderNonce()
		d.cfg.Logger.Warn("blockdriver: ProduceBlock returned nil block with no error")
		d.blocksFailed.Add(1)
		return
	}

	included := make(map[string]struct{}, len(blk.Transactions))
	for _, tx := range blk.Transactions {
		if tx != nil {
			included[tx.ID] = struct{}{}
		}
	}
	paidProofs := 0
	emittedDust := uint64(0)
	for _, tx := range txs {
		if _, ok := included[tx.ID]; !ok {
			if count := drained[tx.Recipient]; count > 0 {
				d.requeueProofs(map[string]int{tx.Recipient: count})
			}
			continue
		}
		if count := drained[tx.Recipient]; count > 0 {
			paidProofs += count
			emittedDust += uint64(tx.Amount * float64(chain.DustPerCell))
		}
	}
	if acc, exists := d.cfg.Accounts.Get(FunderAddress); exists && acc != nil {
		d.funderNonce.Store(acc.Nonce)
	}
	d.blocksSealed.Add(1)
	d.proofsPaid.Add(uint64(paidProofs))
	if emittedDust > 0 {
		// totalEmittedCell tracks dust we actually paid out
		// (i.e. excluding heartbeat-only blocks where
		// drainedCount==0 and the reward goes unclaimed).
		// Mirrors the "no proofs => no emission" rule
		// CELL_TOKENOMICS implies for solo testnet bring-up.
		d.totalEmittedCell.Add(emittedDust)
	}
	d.cfg.Logger.Info("blockdriver: block sealed",
		"height", blk.Height,
		"hash", blk.Hash,
		"tx_count", len(blk.Transactions),
		"payouts", len(drained),
		"proofs_in_window", drainedCount,
		"reward_dust", rewardDust,
		"reward_cell", d.schedule.BlockRewardCell(blk.Height),
		"epoch", d.schedule.EpochForHeight(blk.Height),
		"penalised_payouts_total", d.penalisedPayouts.Load(),
		"withheld_dust_total", d.withheldDust.Load())
}

func (d *Driver) requeueProofs(queue map[string]int) {
	if len(queue) == 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for addr, count := range queue {
		if addr == "" || count <= 0 {
			continue
		}
		d.queue[addr] += count
		d.queued += count
	}
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

// buildTxs creates one transaction per unique miner address
// (with reward proportional to that address's proof count) or
// a single zero-amount heartbeat tx when the window had no
// accepted proofs OR when the per-block reward has tapered to
// 0 (cap reached). The producer's mempool refuses to seal an
// empty block, so we always emit at least one tx.
//
// As of Tier-3, each per-miner share is also multiplied by
// the operator-configured RewardPenalty before being credited.
// Multipliers below 1.0 mint LESS dust than the schedule
// would normally allow — the unused share is unminted, NOT
// redistributed to other miners. That keeps the supply cap
// monotonically respected and makes the tokenomic effect of
// Tier-3 strictly subtractive.
func (d *Driver) buildTxs(queue map[string]int, total int, rewardCell float64, startNonce uint64) []*mempool.Tx {
	now := time.Now()
	nextNonce := startNonce
	if total == 0 || len(queue) == 0 || rewardCell <= 0 {
		return []*mempool.Tx{{
			ID:        fmt.Sprintf("solo-heartbeat-%d-%d", nextNonce, now.UnixNano()),
			Sender:    FunderAddress,
			Recipient: FunderAddress,
			Amount:    0,
			Fee:       0,
			Nonce:     nextNonce,
			AddedAt:   now,
		}}
	}

	out := make([]*mempool.Tx, 0, len(queue))
	addresses := make([]string, 0, len(queue))
	for addr := range queue {
		addresses = append(addresses, addr)
	}
	sort.Strings(addresses)
	for _, addr := range addresses {
		count := queue[addr]
		baseShare := rewardCell * float64(count) / float64(total)
		mult := d.rewardPenalty.MultiplierFor(addr)
		// Defensive clamp: if a buggy MismatchPenalty
		// returns NaN / Inf / negative / >1 we round it
		// to a safe band rather than mint anything weird.
		if !(mult >= 0 && mult <= 1) {
			mult = 1.0
		}
		share := baseShare * mult
		// Skip 0-share or negative-share rounding artefacts —
		// the AccountStore would reject them anyway.
		if share <= 0 {
			continue
		}
		if mult < 1.0 {
			d.penalisedPayouts.Add(1)
			// withheldDust = (baseShare - share) in dust.
			// Truncate via float→uint64 to mirror the same
			// rounding the producer uses for credits.
			withheld := uint64((baseShare - share) * float64(chain.DustPerCell))
			if withheld > 0 {
				d.withheldDust.Add(withheld)
			}
		}
		out = append(out, &mempool.Tx{
			ID:         fmt.Sprintf("solo-reward-%d-%s", nextNonce, addr),
			Sender:     FunderAddress,
			Recipient:  addr,
			Amount:     share,
			Fee:        0,
			Nonce:      nextNonce,
			ContractID: chain.MiningRewardContractID,
			AddedAt:    now,
		})
		nextNonce++
	}
	if len(out) == 0 {
		// All shares rounded out — emit a heartbeat anyway.
		out = append(out, &mempool.Tx{
			ID:        fmt.Sprintf("solo-heartbeat-%d-%d", nextNonce, now.UnixNano()),
			Sender:    FunderAddress,
			Recipient: FunderAddress,
			Amount:    0,
			Fee:       0,
			Nonce:     nextNonce,
			AddedAt:   now,
		})
	}
	return out
}
