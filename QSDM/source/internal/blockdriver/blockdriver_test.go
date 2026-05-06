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
// populated, defaults filled in by New.
func validCfg(t *testing.T) Config {
	t.Helper()
	bp, pool, accounts := build(t)
	return Config{
		Producer:             bp,
		Pool:                 pool,
		Accounts:             accounts,
		Logger:               quietLogger(t),
		Period:               5 * time.Millisecond,
		RewardPerBlock:       1.0,
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
	if d.cfg.RewardPerBlock != DefaultRewardPerBlock {
		t.Errorf("RewardPerBlock: got %v want %v", d.cfg.RewardPerBlock, DefaultRewardPerBlock)
	}
	if d.cfg.FunderInitialBalance != DefaultFunderBalance {
		t.Errorf("FunderInitialBalance: got %v want %v", d.cfg.FunderInitialBalance, DefaultFunderBalance)
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
	cfg.RewardPerBlock = 4.0 // exact split: alice=3 of 4, bob=1 of 4.
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
	want := cfg.FunderInitialBalance - cfg.RewardPerBlock
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
	cfg.RewardPerBlock = 0.5
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
	want := 5 * cfg.RewardPerBlock
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
