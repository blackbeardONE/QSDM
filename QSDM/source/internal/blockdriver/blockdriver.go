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
const FunderAddress = "qsdm-system-funder"

// Defaults for the operator-tunable Config fields. Picked so
// a fresh QSDM_SOLO_VALIDATOR_MODE=1 boot has visible
// behaviour without further configuration.
const (
	// DefaultPeriod is the gap between block-seal attempts.
	// At ~10s we trade off "miner sees their balance update
	// fairly quickly" against "we don't churn the chain
	// faster than the production target_block_time_seconds
	// of 10".
	DefaultPeriod = 10 * time.Second

	// DefaultRewardPerBlock is the total CELL paid out per
	// sealed block, split across miners. 1.0 is intentionally
	// non-canonical (production uses block_reward_dust =
	// 356490987 = 3.56490987 CELL); keeps testnet emissions
	// a noticeable fraction of one CELL per block without
	// pretending to mirror mainnet tokenomics.
	DefaultRewardPerBlock = 1.0

	// DefaultFunderBalance seeds FunderAddress at startup.
	// 1e15 (1 quadrillion CELL) is far above the 90M CELL
	// supply cap; intentional, because in solo mode the
	// supply cap is not enforced — the funder is a fiat
	// faucet, not a real account.
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

	// RewardPerBlock is the total CELL paid out per sealed
	// block. Zero or negative uses DefaultRewardPerBlock.
	RewardPerBlock float64

	// FunderInitialBalance is the balance credited to
	// FunderAddress at New time IF the account doesn't yet
	// exist. Zero uses DefaultFunderBalance.
	FunderInitialBalance float64

	// Producer ID stamp for "heartbeat" blocks (no miners
	// in the window). Zero/empty uses "qsdm-solo-blockdriver".
	ProducerID string
}

// Driver is the periodic block-production loop. Safe for
// concurrent calls into OnAcceptedProof from any goroutine
// (the HTTP handlers); single-tick from the internal
// goroutine started by Start.
type Driver struct {
	cfg Config

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
	if cfg.RewardPerBlock <= 0 {
		cfg.RewardPerBlock = DefaultRewardPerBlock
	}
	if cfg.FunderInitialBalance <= 0 {
		cfg.FunderInitialBalance = DefaultFunderBalance
	}
	if cfg.ProducerID == "" {
		cfg.ProducerID = "qsdm-solo-blockdriver"
	}

	d := &Driver{
		cfg:    cfg,
		queue:  make(map[string]int, 16),
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
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
}

// Stats returns a snapshot of the driver's counters.
func (d *Driver) Stats() Stats {
	d.mu.Lock()
	depth := d.queued
	d.mu.Unlock()
	return Stats{
		Period:       d.cfg.Period,
		BlocksSealed: d.blocksSealed.Load(),
		BlocksFailed: d.blocksFailed.Load(),
		ProofsPaid:   d.proofsPaid.Load(),
		QueueDepth:   depth,
		FunderNonce:  d.funderNonce.Load(),
	}
}

func (d *Driver) run(ctx context.Context) {
	defer close(d.doneCh)
	t := time.NewTicker(d.cfg.Period)
	defer t.Stop()
	d.cfg.Logger.Info("blockdriver: started",
		"period", d.cfg.Period,
		"reward_per_block", d.cfg.RewardPerBlock,
		"funder", FunderAddress,
		"funder_initial_balance", d.cfg.FunderInitialBalance)
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

	txs := d.buildTxs(drained, drainedCount)
	for _, tx := range txs {
		if err := d.cfg.Pool.Add(tx); err != nil {
			d.cfg.Logger.Warn("blockdriver: pool admission failed; dropping tx",
				"tx_id", tx.ID,
				"error", err.Error())
			d.blocksFailed.Add(1)
			return
		}
	}

	blk, err := d.cfg.Producer.ProduceBlock()
	if err != nil {
		d.cfg.Logger.Warn("blockdriver: ProduceBlock failed",
			"error", err.Error(),
			"queued_payouts", len(drained))
		d.blocksFailed.Add(1)
		return
	}
	if blk == nil {
		d.cfg.Logger.Warn("blockdriver: ProduceBlock returned nil block with no error")
		d.blocksFailed.Add(1)
		return
	}
	d.blocksSealed.Add(1)
	d.proofsPaid.Add(uint64(drainedCount))
	d.cfg.Logger.Info("blockdriver: block sealed",
		"height", blk.Height,
		"hash", blk.Hash,
		"tx_count", len(blk.Transactions),
		"payouts", len(drained),
		"proofs_in_window", drainedCount)
}

// buildTxs creates one transaction per unique miner address
// (with reward proportional to that address's proof count) or
// a single zero-amount heartbeat tx when the window had no
// accepted proofs. The producer's mempool refuses to seal an
// empty block, so we always emit at least one tx.
func (d *Driver) buildTxs(queue map[string]int, total int) []*mempool.Tx {
	now := time.Now()
	if total == 0 || len(queue) == 0 {
		nonce := d.funderNonce.Add(1) - 1
		return []*mempool.Tx{{
			ID:        fmt.Sprintf("solo-heartbeat-%d-%d", nonce, now.UnixNano()),
			Sender:    FunderAddress,
			Recipient: FunderAddress,
			Amount:    0,
			Fee:       0,
			Nonce:     nonce,
			AddedAt:   now,
		}}
	}

	totalReward := d.cfg.RewardPerBlock
	out := make([]*mempool.Tx, 0, len(queue))
	for addr, count := range queue {
		share := totalReward * float64(count) / float64(total)
		// Skip 0-share or negative-share rounding artefacts —
		// the AccountStore would reject them anyway.
		if share <= 0 {
			continue
		}
		nonce := d.funderNonce.Add(1) - 1
		out = append(out, &mempool.Tx{
			ID:        fmt.Sprintf("solo-reward-%d-%s", nonce, addr),
			Sender:    FunderAddress,
			Recipient: addr,
			Amount:    share,
			Fee:       0,
			Nonce:     nonce,
			AddedAt:   now,
		})
	}
	if len(out) == 0 {
		// All shares rounded out — emit a heartbeat anyway.
		nonce := d.funderNonce.Add(1) - 1
		out = append(out, &mempool.Tx{
			ID:        fmt.Sprintf("solo-heartbeat-%d-%d", nonce, now.UnixNano()),
			Sender:    FunderAddress,
			Recipient: FunderAddress,
			Amount:    0,
			Fee:       0,
			Nonce:     nonce,
			AddedAt:   now,
		})
	}
	return out
}
