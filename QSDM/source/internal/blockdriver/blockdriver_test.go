package blockdriver

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// quietLogger is a Logger that writes to a temp file and is
// closed at test cleanup. The driver logs every block at
// Info level which would otherwise spam test output.
func quietLogger(t *testing.T) *logging.Logger {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "blockdriver-test.log")
	l := logging.NewLogger(path, true)
	t.Cleanup(func() {
		_ = l.Close()
		_ = os.Remove(path)
	})
	return l
}

// build returns a fresh BlockProducer + Mempool + Accounts
// triple suitable for driving a Driver. The producer has no
// BFT/POL gates set (mirrors the solo-mode boot path in
// cmd/qsdm/main.go) so ProduceBlock proceeds whenever the
// mempool has at least one tx.
func build(t *testing.T) (*chain.BlockProducer, *mempool.Mempool, *chain.AccountStore) {
	t.Helper()
	pool := mempool.New(mempool.DefaultConfig())
	accounts := chain.NewAccountStore()
	bp := chain.NewBlockProducer(pool, accounts, chain.DefaultProducerConfig())
	return bp, pool, accounts
}

// validCfg returns the minimum-valid Config — every field
// populated, defaults filled in by New. Tests use
// FlatRewardPerBlock to keep arithmetic simple; schedule-
// driven behaviour is covered separately.
func validCfg(t *testing.T) Config {
	t.Helper()
	bp, pool, accounts := build(t)
	return Config{
		Producer:             bp,
		Pool:                 pool,
		Accounts:             accounts,
		Logger:               quietLogger(t),
		Period:               5 * time.Millisecond,
		FlatRewardPerBlock:   1.0,
		FunderInitialBalance: 1000.0,
	}
}

// ---- New: validation -----------------------------------------------------

func TestNew_RejectsMissingProducer(t *testing.T) {
	cfg := validCfg(t)
	cfg.Producer = nil
	if _, err := New(cfg); err == nil {
		t.Fatal("expected error when Producer is nil")
	}
}

func TestNew_RejectsMissingPool(t *testing.T) {
	cfg := validCfg(t)
	cfg.Pool = nil
	if _, err := New(cfg); err == nil {
		t.Fatal("expected error when Pool is nil")
	}
}

func TestNew_RejectsMissingAccounts(t *testing.T) {
	cfg := validCfg(t)
	cfg.Accounts = nil
	if _, err := New(cfg); err == nil {
		t.Fatal("expected error when Accounts is nil")
	}
}

func TestNew_RejectsMissingLogger(t *testing.T) {
	cfg := validCfg(t)
	cfg.Logger = nil
	if _, err := New(cfg); err == nil {
		t.Fatal("expected error when Logger is nil")
	}
}

func TestNew_FillsDefaults(t *testing.T) {
	cfg := Config{
		Producer: nilSafeBuild(t),
		Pool:     mempool.New(mempool.DefaultConfig()),
		Accounts: chain.NewAccountStore(),
		Logger:   quietLogger(t),
	}
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if d.cfg.Period != DefaultPeriod {
		t.Errorf("Period: got %v want %v", d.cfg.Period, DefaultPeriod)
	}
	if d.cfg.FunderInitialBalance != DefaultFunderBalance {
		t.Errorf("FunderInitialBalance: got %v want %v", d.cfg.FunderInitialBalance, DefaultFunderBalance)
	}
	// Default schedule should match chain.DefaultEmissionSchedule().
	want := chain.DefaultEmissionSchedule()
	if d.schedule.MiningCapDust != want.MiningCapDust {
		t.Errorf("schedule.MiningCapDust: got %d want %d", d.schedule.MiningCapDust, want.MiningCapDust)
	}
	if d.schedule.BlocksPerEpoch != want.BlocksPerEpoch {
		t.Errorf("schedule.BlocksPerEpoch: got %d want %d", d.schedule.BlocksPerEpoch, want.BlocksPerEpoch)
	}
}

// TestNew_RejectsZeroValueSchedule guards against the
// classic "passed a zero EmissionSchedule by mistake" bug
// which would silently produce 0-reward forever.
func TestNew_RejectsZeroValueSchedule(t *testing.T) {
	cfg := validCfg(t)
	zero := chain.EmissionSchedule{}
	cfg.EmissionSchedule = &zero
	if _, err := New(cfg); err == nil {
		t.Fatal("expected error when EmissionSchedule has BlocksPerEpoch == 0")
	}
}

// nilSafeBuild builds a producer wired to a fresh mempool +
// account store, used by tests that only care about the
// producer reference.
func nilSafeBuild(t *testing.T) *chain.BlockProducer {
	t.Helper()
	pool := mempool.New(mempool.DefaultConfig())
	accounts := chain.NewAccountStore()
	return chain.NewBlockProducer(pool, accounts, chain.DefaultProducerConfig())
}

// ---- OnAcceptedProof / queue ---------------------------------------------

func TestOnAcceptedProof_AccumulatesByAddress(t *testing.T) {
	d, err := New(validCfg(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.OnAcceptedProof("qsdm1alice")
	d.OnAcceptedProof("qsdm1alice")
	d.OnAcceptedProof("qsdm1bob")
	if got := d.Stats().QueueDepth; got != 3 {
		t.Fatalf("queue depth: got %d want 3", got)
	}
}

func TestOnAcceptedProof_IgnoresEmpty(t *testing.T) {
	d, err := New(validCfg(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.OnAcceptedProof("")
	if got := d.Stats().QueueDepth; got != 0 {
		t.Fatalf("queue depth: got %d want 0 (empty addr should be ignored)", got)
	}
}

// ---- tick: heartbeat path ------------------------------------------------

// TestTick_HeartbeatSealsEmptyBlock confirms that an idle
// driver (no proofs queued) still seals a block per tick so
// the chain advances and metrics keep flowing. A heartbeat
// tx (funder→funder, amount=0) is the minimum payload.
func TestTick_HeartbeatSealsEmptyBlock(t *testing.T) {
	cfg := validCfg(t)
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.tick()
	if got := d.Stats().BlocksSealed; got != 1 {
		t.Fatalf("BlocksSealed: got %d want 1 (heartbeat path)", got)
	}
	if !cfg.Producer.HasTip() {
		t.Fatal("producer should have a tip after heartbeat seal")
	}
	tip, _ := cfg.Producer.LatestBlock()
	if tip == nil {
		t.Fatal("LatestBlock returned nil")
	}
	if len(tip.Transactions) == 0 {
		t.Fatal("heartbeat block has no txs")
	}
	hb := tip.Transactions[0]
	if hb.Sender != FunderAddress || hb.Recipient != FunderAddress {
		t.Errorf("heartbeat tx sender/recipient: got %s/%s want %s/%s",
			hb.Sender, hb.Recipient, FunderAddress, FunderAddress)
	}
	if hb.Amount != 0 {
		t.Errorf("heartbeat amount: got %v want 0", hb.Amount)
	}
}

// ---- tick: payout path ---------------------------------------------------

// TestTick_PayoutCreditsMiners is the headline test: an
// accepted-proof queue with two miners results in a sealed
// block whose state credits each miner proportional to their
// proof count.
func TestTick_PayoutCreditsMiners(t *testing.T) {
	cfg := validCfg(t)
	cfg.FlatRewardPerBlock = 4.0 // exact split: alice=3 of 4, bob=1 of 4.
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := 0; i < 3; i++ {
		d.OnAcceptedProof("qsdm1alice")
	}
	d.OnAcceptedProof("qsdm1bob")
	d.tick()

	if got := d.Stats().BlocksSealed; got != 1 {
		t.Fatalf("BlocksSealed: got %d want 1", got)
	}
	if got := d.Stats().ProofsPaid; got != 4 {
		t.Fatalf("ProofsPaid: got %d want 4", got)
	}
	alice, _ := cfg.Accounts.Get("qsdm1alice")
	bob, _ := cfg.Accounts.Get("qsdm1bob")
	if alice == nil || bob == nil {
		t.Fatalf("miner accounts not credited: alice=%v bob=%v", alice, bob)
	}
	// Allow tiny float drift in case the proportional split
	// rounded.
	if alice.Balance < 2.999 || alice.Balance > 3.001 {
		t.Errorf("alice balance: got %.6f want ~3.0", alice.Balance)
	}
	if bob.Balance < 0.999 || bob.Balance > 1.001 {
		t.Errorf("bob balance: got %.6f want ~1.0", bob.Balance)
	}
	// Funder balance dropped by exactly the reward total.
	funder, _ := cfg.Accounts.Get(FunderAddress)
	want := cfg.FunderInitialBalance - cfg.FlatRewardPerBlock
	if funder.Balance < want-0.001 || funder.Balance > want+0.001 {
		t.Errorf("funder balance: got %.6f want ~%.6f", funder.Balance, want)
	}
}

// TestTick_QueueDrainedAfterTick ensures the queue is drained
// to zero after a successful tick (so the next window starts
// fresh).
func TestTick_QueueDrainedAfterTick(t *testing.T) {
	d, err := New(validCfg(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.OnAcceptedProof("qsdm1alice")
	d.tick()
	if got := d.Stats().QueueDepth; got != 0 {
		t.Fatalf("queue should be drained, got depth %d", got)
	}
}

// ---- tick: nonce monotonicity --------------------------------------------

// TestTick_FunderNonceMonotonic ensures the funder's nonce
// advances strictly across ticks even when individual blocks
// have multiple reward txs. A double-use of the same nonce
// trips ApplyTx and produces a stuck chain.
func TestTick_FunderNonceMonotonic(t *testing.T) {
	cfg := validCfg(t)
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.OnAcceptedProof("qsdm1alice")
	d.OnAcceptedProof("qsdm1bob")
	d.tick()
	// Two reward txs were issued; funder nonce should have
	// moved by 2 from its starting value (which is 0 for a
	// fresh AccountStore + the +1 for the seed).
	funder, _ := cfg.Accounts.Get(FunderAddress)
	if funder == nil {
		t.Fatal("funder account missing")
	}
	if funder.Nonce != 2 {
		t.Errorf("funder nonce after 2 reward txs: got %d want 2", funder.Nonce)
	}
	// One more tick — heartbeat path — should still bump
	// the nonce by exactly 1.
	d.tick()
	funder2, _ := cfg.Accounts.Get(FunderAddress)
	if funder2.Nonce != 3 {
		t.Errorf("funder nonce after heartbeat: got %d want 3", funder2.Nonce)
	}
}

// ---- multi-block end-to-end ---------------------------------------------

// TestE2E_SealsManyBlocksAccumulatesBalance confirms the
// driver can advance the chain across multiple ticks with
// payouts each time, and balances accumulate. This is the
// "what BLR1 will look like in solo mode" exercise.
func TestE2E_SealsManyBlocksAccumulatesBalance(t *testing.T) {
	cfg := validCfg(t)
	cfg.FlatRewardPerBlock = 0.5
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := 0; i < 5; i++ {
		d.OnAcceptedProof("qsdm1charlie")
		d.tick()
	}
	if got := d.Stats().BlocksSealed; got != 5 {
		t.Fatalf("BlocksSealed: got %d want 5", got)
	}
	if got := cfg.Producer.TipHeight(); got != 4 {
		// 5 blocks at heights 0..4 → tip = 4.
		t.Fatalf("TipHeight: got %d want 4", got)
	}
	charlie, _ := cfg.Accounts.Get("qsdm1charlie")
	if charlie == nil {
		t.Fatal("charlie account missing")
	}
	want := 5 * cfg.FlatRewardPerBlock
	if charlie.Balance < want-0.001 || charlie.Balance > want+0.001 {
		t.Errorf("charlie balance after 5 blocks: got %.6f want %.6f",
			charlie.Balance, want)
	}
}

// ---- Start / Stop --------------------------------------------------------

// TestStart_TicksUntilStop spins up the goroutine, lets it
// run a few ticks, then Stops it. Confirms the goroutine
// exits cleanly and we observed at least one block sealed.
func TestStart_TicksUntilStop(t *testing.T) {
	cfg := validCfg(t)
	cfg.Period = 2 * time.Millisecond
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.Start(ctx)

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if d.Stats().BlocksSealed >= 2 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := d.Stats().BlocksSealed; got < 2 {
		t.Fatalf("BlocksSealed: got %d want >= 2 in 500ms", got)
	}
	d.Stop()
	// Stop should be idempotent.
	d.Stop()
}

// ---- emission schedule ---------------------------------------------------

// TestTick_UsesEmissionScheduleWhenFlatRewardZero verifies
// that the production code path (FlatRewardPerBlock = 0)
// pulls the per-block reward from the schedule. We use a
// small custom schedule (cap = 8 dust, BlocksPerEpoch = 1)
// to make the arithmetic obvious: epoch 0 allocates cap/2 = 4
// dust per block, so the miner gets 4 dust = 4e-8 CELL.
func TestTick_UsesEmissionScheduleWhenFlatRewardZero(t *testing.T) {
	bp, pool, accounts := build(t)
	// Tiny schedule: cap=8 dust, 10 s blocks, 10 s epochs ⇒
	// BlocksPerEpoch=1 ⇒ epoch 0 alloc = 4 dust = 4 dust per
	// block (only block in the epoch).
	sched, err := chain.NewEmissionSchedule(8, 10, 10)
	if err != nil {
		t.Fatalf("NewEmissionSchedule: %v", err)
	}
	d, err := New(Config{
		Producer:             bp,
		Pool:                 pool,
		Accounts:             accounts,
		Logger:               quietLogger(t),
		Period:               5 * time.Millisecond,
		EmissionSchedule:     &sched,
		FunderInitialBalance: 1000.0,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Seal genesis (height 0; 0 reward by spec) so the
	// next tick targets height 1 and the schedule pays out.
	d.tick()
	if !bp.HasTip() {
		t.Fatal("genesis tick did not seal a block")
	}
	d.OnAcceptedProof("qsdm1solo")
	d.tick()
	want := 4.0 / float64(chain.DustPerCell) // 4 dust as CELL
	got, _ := accounts.Get("qsdm1solo")
	if got == nil {
		t.Fatal("solo balance not credited")
	}
	if got.Balance < want-1e-12 || got.Balance > want+1e-12 {
		t.Errorf("schedule reward: got %.12f want %.12f", got.Balance, want)
	}
	if e := d.Stats().EmittedDust; e != 4 {
		t.Errorf("EmittedDust: got %d want 4", e)
	}
	if d.Stats().FlatReward {
		t.Error("FlatReward should be false when schedule is in use")
	}
}

// TestTick_HeartbeatWhenScheduleAtZero confirms that once the
// emission curve has emitted its full allocation, subsequent
// blocks are heartbeats (no payout) — the supply cap is
// enforced. We set cap = 1 dust + BlocksPerEpoch = 1 so
// epoch 0 allocates 0 dust per block (1/2 = 0 in integer
// math), and every block lands here.
func TestTick_HeartbeatWhenScheduleAtZero(t *testing.T) {
	bp, pool, accounts := build(t)
	sched, err := chain.NewEmissionSchedule(1, 10, 10) // alloc = 0
	if err != nil {
		t.Fatalf("NewEmissionSchedule: %v", err)
	}
	d, err := New(Config{
		Producer:             bp,
		Pool:                 pool,
		Accounts:             accounts,
		Logger:               quietLogger(t),
		Period:               5 * time.Millisecond,
		EmissionSchedule:     &sched,
		FunderInitialBalance: 1000.0,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Seal genesis first so the next tick is at height 1.
	d.tick()
	d.OnAcceptedProof("qsdm1cap")
	d.tick()
	got, _ := accounts.Get("qsdm1cap")
	if got != nil && got.Balance != 0 {
		t.Errorf("expected no credit when schedule emits 0, got %.12f", got.Balance)
	}
	if e := d.Stats().EmittedDust; e != 0 {
		t.Errorf("EmittedDust: got %d want 0", e)
	}
	// Block was still sealed (heartbeat), so chain advances.
	if !bp.HasTip() {
		t.Fatal("expected heartbeat block to seal")
	}
}

// ---- SyncFunderNonce -----------------------------------------------------

// TestSyncFunderNonce_AbsorbsOutOfBandTx mirrors the
// production boot sequence: an out-of-band tx (the genesis-
// seal heartbeat in cmd/qsdm/main.go) consumes nonce=0 from
// the funder before the driver gets to issue its first tx.
// Without SyncFunderNonce, the driver would re-issue at
// nonce=0 and ApplyTx would reject. With SyncFunderNonce
// called between the out-of-band tx and Start, the next
// tick uses nonce=1 and succeeds.
func TestSyncFunderNonce_AbsorbsOutOfBandTx(t *testing.T) {
	cfg := validCfg(t)
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Simulate an out-of-band tx that consumed funder.Nonce=0.
	// We mutate the AccountStore directly (the genesis-seal
	// path goes through ApplyTx, which has the same effect).
	cfg.Accounts.Credit("genesis-anchor", 1.0)
	if err := cfg.Accounts.ApplyTx(&mempool.Tx{
		ID: "oob", Sender: FunderAddress, Recipient: "genesis-anchor",
		Amount: 1.0, Nonce: 0,
	}); err != nil {
		t.Fatalf("oob tx setup: %v", err)
	}
	// Without sync, the driver still thinks funderNonce=0 →
	// next tick would be rejected. Confirm tick is in fact
	// rejected before sync.
	d.OnAcceptedProof("qsdm1early")
	d.tick()
	if d.Stats().BlocksFailed == 0 {
		t.Fatal("expected first tick to fail with stale nonce, but it succeeded")
	}

	// Now sync — driver's nonce should match AccountStore's.
	d.SyncFunderNonce()
	if got, want := d.Stats().FunderNonce, uint64(1); got != want {
		t.Fatalf("after sync: nonce got %d want %d", got, want)
	}
	// And the next tick should succeed.
	d.OnAcceptedProof("qsdm1latee")
	d.tick()
	if got := d.Stats().BlocksSealed; got != 1 {
		t.Fatalf("post-sync tick: blocks sealed got %d want 1", got)
	}
}

// ---- compile-time guard --------------------------------------------------

func TestDriverImplementsRewardSink(t *testing.T) {
	var d *Driver
	_ = d
	// _ used to silence unused-variable; the var-as-interface
	// assertion lives in the package source.
}

// ---- Tier-3 reward downgrade --------------------------------------------

// fakeRewardPenalty is a deterministic RewardPenalty
// implementation used by the tests below. Returns the
// configured multiplier for matching addresses; 1.0 for
// every other address.
type fakeRewardPenalty struct {
	multipliers map[string]float64
}

func (f *fakeRewardPenalty) MultiplierFor(addr string) float64 {
	if m, ok := f.multipliers[addr]; ok {
		return m
	}
	return 1.0
}

// TestTier3_PenaltyAppliesToFlaggedMiner verifies the
// happy path: a miner over-threshold gets a fraction of
// their full reward, while honest miners get their full
// share. Uses FlatRewardPerBlock to keep arithmetic
// trivially auditable.
func TestTier3_PenaltyAppliesToFlaggedMiner(t *testing.T) {
	cfg := validCfg(t)
	cfg.FlatRewardPerBlock = 4.0
	cfg.RewardPenalty = &fakeRewardPenalty{
		multipliers: map[string]float64{
			"qsdm1bad": 0.5,
		},
	}
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := 0; i < 2; i++ {
		d.OnAcceptedProof("qsdm1bad")
		d.OnAcceptedProof("qsdm1good")
	}
	d.tick()

	bad, _ := cfg.Accounts.Get("qsdm1bad")
	good, _ := cfg.Accounts.Get("qsdm1good")
	if bad == nil || good == nil {
		t.Fatalf("miner accounts not credited: bad=%v good=%v", bad, good)
	}
	// 4 proofs, 2 each → base share = 2.0 each.
	// Bad gets 2.0 * 0.5 = 1.0, Good gets 2.0.
	if bad.Balance < 0.999 || bad.Balance > 1.001 {
		t.Errorf("bad balance: got %.6f want ~1.0 (2.0 * 0.5 multiplier)", bad.Balance)
	}
	if good.Balance < 1.999 || good.Balance > 2.001 {
		t.Errorf("good balance: got %.6f want ~2.0 (no penalty)", good.Balance)
	}
	stats := d.Stats()
	if stats.PenalisedPayouts != 1 {
		t.Errorf("PenalisedPayouts: got %d want 1", stats.PenalisedPayouts)
	}
	if stats.WithheldDust == 0 {
		t.Errorf("WithheldDust: got 0 want > 0 (1.0 CELL withheld)")
	}
	if !stats.PenaltyActive {
		t.Errorf("PenaltyActive should be true")
	}
}

// TestTier3_NilPenaltyKeepsLegacyBehaviour confirms the
// pre-Tier-3 posture (no RewardPenalty wired) is byte-
// identical to before: full per-proof share, no withheld
// dust, PenaltyActive=false.
func TestTier3_NilPenaltyKeepsLegacyBehaviour(t *testing.T) {
	cfg := validCfg(t)
	cfg.FlatRewardPerBlock = 2.0
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.OnAcceptedProof("qsdm1solo")
	d.OnAcceptedProof("qsdm1solo")
	d.tick()

	solo, _ := cfg.Accounts.Get("qsdm1solo")
	if solo == nil || solo.Balance < 1.999 || solo.Balance > 2.001 {
		t.Errorf("solo balance: got %v want ~2.0 (full reward)", solo)
	}
	stats := d.Stats()
	if stats.PenalisedPayouts != 0 {
		t.Errorf("PenalisedPayouts: got %d want 0", stats.PenalisedPayouts)
	}
	if stats.WithheldDust != 0 {
		t.Errorf("WithheldDust: got %d want 0", stats.WithheldDust)
	}
	if stats.PenaltyActive {
		t.Errorf("PenaltyActive should be false when no penalty wired")
	}
}

// TestTier3_NaNAndOutOfRangeMultiplier_ClampsToFullReward
// exercises the defensive clamp inside buildTxs: a buggy
// MismatchPenalty that returns NaN, +Inf, negative, or >1
// must NOT cause a phantom mint or a negative tx amount.
// Falls back to 1.0 (no penalty) so the system is fail-safe.
type degenerateMultiplier struct{ value float64 }

func (d *degenerateMultiplier) MultiplierFor(string) float64 { return d.value }

func TestTier3_DefensiveClamp_OnDegenerateMultipliers(t *testing.T) {
	cases := []struct {
		name  string
		value float64
	}{
		{"NaN", nanFloat()},
		{"PositiveInfinity", infFloat()},
		{"Negative", -0.25},
		{"GreaterThanOne", 1.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validCfg(t)
			cfg.FlatRewardPerBlock = 1.0
			cfg.RewardPenalty = &degenerateMultiplier{value: tc.value}
			d, err := New(cfg)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			d.OnAcceptedProof("qsdm1clamp")
			d.tick()
			acc, _ := cfg.Accounts.Get("qsdm1clamp")
			if acc == nil || acc.Balance < 0.999 || acc.Balance > 1.001 {
				t.Errorf("balance under degenerate multiplier %v: got %v want ~1.0",
					tc.value, acc)
			}
			if d.Stats().PenalisedPayouts != 0 {
				t.Errorf("degenerate multiplier should not register as penalty firing")
			}
		})
	}
}

func nanFloat() float64 { var z float64; return z / z }
func infFloat() float64 { var z float64; z = 1; return z / 0 }

// Ensure concurrent OnAcceptedProof calls don't race the
// queue's internal state. Run with `go test -race`.
func TestConcurrentOnAcceptedProof_NoRace(t *testing.T) {
	d, err := New(validCfg(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				d.OnAcceptedProof("qsdm1raceaddr")
			}
		}()
	}
	wg.Wait()
	if got := d.Stats().QueueDepth; got != 8*200 {
		t.Fatalf("queue depth: got %d want %d", got, 8*200)
	}
}
