package v2wiring_test

// v2wiring_tcfork_test.go: integration tests for the
// fork_v2_tc_height governance parameter wiring
// (MINING_PROTOCOL_V2 §4 + §12.2). Companion to
// v2wiring_gov_test.go.
//
// Four lifecycle paths exercised here:
//
//   1. Default config -> mining.ForkV2TCHeight() = math.MaxUint64
//      (TC disabled, the safe genesis posture).
//   2. Genesis seed via v2wiring.Config.ForkV2TCHeight ->
//      ParamStore active value matches the seed AND the runtime
//      mining knob is pinned to it at boot.
//   3. Governance param-set tx promoted at effective_height ->
//      runtime knob updated on the very next sealed block (so
//      pkg/mining/verifier.go and pkg/mining/solver.go see the
//      new value without a binary restart).
//   4. Snapshot replay across simulated restart -> fresh boot
//      restores the runtime knob to the previously activated
//      value, AND the genesis-seed config field is ignored
//      (snapshot wins; the chain's committed history is the
//      authoritative source).

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/v2wiring"
	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/governance/chainparams"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	mining "github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// resetMiningForkV2TC restores pkg/mining's package-level atomic
// to the safe disabled default after each test. Required because
// SetForkV2TCHeight mutates global state and t.Parallel() tests
// would otherwise see each other's leftover values.
func resetMiningForkV2TC(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { mining.SetForkV2TCHeight(math.MaxUint64) })
}

// tcRig is a minimal rig that builds a fully-wired chain with
// governance enabled and an optional Config.ForkV2TCHeight seed.
// Returns the wired bundle + producer for tests that drive blocks.
type tcRig struct {
	w        *v2wiring.Wired
	pool     *mempool.Mempool
	producer *chain.BlockProducer
}

func buildTCRig(t *testing.T, seed *uint64, storePath string) *tcRig {
	t.Helper()
	t.Cleanup(func() {
		monitoring.SetEnrollmentStateProvider(nil)
		api.SetEnrollmentRegistry(nil)
		api.SetEnrollmentLister(nil)
		api.SetEnrollmentMempool(nil)
		api.SetSlashMempool(nil)
		api.SetSlashReceiptStore(nil)
		api.SetGovernanceProvider(nil)
		api.SetRecentRejectionLister(nil)
		mining.SetRejectionRecorder(nil)
	})

	accounts := chain.NewAccountStore()
	accounts.Credit(tAlice, 1000)
	accounts.Credit(tCarol, 1000)
	accounts.Credit(tBob, 1000)
	pool := mempool.New(mempool.DefaultConfig())

	wired, err := v2wiring.Wire(v2wiring.Config{
		Accounts:              accounts,
		Pool:                  pool,
		BaseAdmit:             nil,
		SlashRewardBPS:        chain.SlashRewardCap,
		GovernanceAuthorities: []string{tAlice, tCarol},
		GovParamStorePath:     storePath,
		ForkV2TCHeight:        seed,
		LogSweepError:         func(uint64, error) {},
		LogSnapshotError:      func(uint64, error) {},
	})
	if err != nil {
		t.Fatalf("v2wiring.Wire: %v", err)
	}

	cfg := chain.DefaultProducerConfig()
	cfg.ProducerID = "test-tcfork-producer"
	bp := chain.NewBlockProducer(pool, wired.StateApplier, cfg)
	wired.AttachToProducer(bp)
	return &tcRig{w: wired, pool: pool, producer: bp}
}

// uint64Ptr is a tiny helper because Go's type system insists
// on a named variable to take the address of an int literal.
// Used by every test below that wants to pass a non-nil seed.
func uint64Ptr(v uint64) *uint64 { return &v }

// -----------------------------------------------------------------------------
// 1. Default: TC disabled
// -----------------------------------------------------------------------------

// TestTCWire_DefaultDisabled is the safety-default lock: a Wire()
// call with no ForkV2TCHeight seed leaves the mining runtime knob
// at math.MaxUint64 (TC disabled). Existing callers that do not
// know about this field continue to behave identically to the
// pre-change posture.
func TestTCWire_DefaultDisabled(t *testing.T) {
	resetMiningForkV2TC(t)
	r := buildTCRig(t, nil, "")

	if got := mining.ForkV2TCHeight(); got != math.MaxUint64 {
		t.Errorf("mining.ForkV2TCHeight() = %d after Wire(default); want MaxUint64",
			got)
	}
	v, _ := r.w.GovParams.ActiveValue(string(chainparams.ParamForkV2TCHeight))
	if v != math.MaxUint64 {
		t.Errorf("ParamStore active fork_v2_tc_height = %d; want MaxUint64", v)
	}
}

// -----------------------------------------------------------------------------
// 2. Genesis seed
// -----------------------------------------------------------------------------

// TestTCWire_GenesisSeed_ZeroActivatesAtGenesis covers the
// integration-test mode: cfg.ForkV2TCHeight = &(0). The store
// must reflect 0 and the runtime knob must be pinned to 0 so
// Verifier / Solver dispatch through the v2 mixin from block 0.
func TestTCWire_GenesisSeed_ZeroActivatesAtGenesis(t *testing.T) {
	resetMiningForkV2TC(t)
	r := buildTCRig(t, uint64Ptr(0), "")

	if got := mining.ForkV2TCHeight(); got != 0 {
		t.Errorf("mining.ForkV2TCHeight() = %d after seed=0; want 0", got)
	}
	if !mining.IsV2TC(0) {
		t.Error("IsV2TC(0) = false after seed=0; want true")
	}
	v, _ := r.w.GovParams.ActiveValue(string(chainparams.ParamForkV2TCHeight))
	if v != 0 {
		t.Errorf("ParamStore active = %d; want 0", v)
	}
}

// TestTCWire_GenesisSeed_FutureActivation is the production
// pattern: a network operator schedules TC activation at
// block N. The runtime knob is pinned to N at boot; IsV2TC
// returns false for any height < N and true at height >= N.
func TestTCWire_GenesisSeed_FutureActivation(t *testing.T) {
	resetMiningForkV2TC(t)
	const N = uint64(100)
	_ = buildTCRig(t, uint64Ptr(N), "")

	if got := mining.ForkV2TCHeight(); got != N {
		t.Errorf("mining.ForkV2TCHeight() = %d; want %d", got, N)
	}
	if mining.IsV2TC(N - 1) {
		t.Errorf("IsV2TC(%d) = true; want false (one before fork)", N-1)
	}
	if !mining.IsV2TC(N) {
		t.Errorf("IsV2TC(%d) = false; want true (at fork)", N)
	}
	if !mining.IsV2TC(N + 1) {
		t.Errorf("IsV2TC(%d) = false; want true (past fork)", N+1)
	}
}

// -----------------------------------------------------------------------------
// 3. Governance promotion updates the runtime knob
// -----------------------------------------------------------------------------

// tcGovTx mints a well-formed param-set tx targeting
// fork_v2_tc_height. Mirrors govTx() from v2wiring_gov_test.go
// but specialised so a typo in param name is caught at compile
// time rather than producing a confusing admission rejection.
func tcGovTx(
	t *testing.T,
	sender, txID string,
	nonce uint64,
	value uint64,
	effectiveHeight uint64,
) *mempool.Tx {
	t.Helper()
	raw, err := chainparams.EncodeParamSet(chainparams.ParamSetPayload{
		Kind:            chainparams.PayloadKindParamSet,
		Param:           string(chainparams.ParamForkV2TCHeight),
		Value:           value,
		EffectiveHeight: effectiveHeight,
	})
	if err != nil {
		t.Fatalf("EncodeParamSet: %v", err)
	}
	return &mempool.Tx{
		ID:         txID,
		Sender:     sender,
		Nonce:      nonce,
		Fee:        0.001,
		Payload:    raw,
		ContractID: chainparams.ContractID,
	}
}

// TestTCWire_GovTxRePinsRuntimeKnob is the load-bearing test
// for this whole feature: a `qsdm/gov/v1` param-set tx that
// activates a fork_v2_tc_height change at block N must result in
// pkg/mining.ForkV2TCHeight() == N on the very next sealed
// block, without a binary restart.
//
// Sequence:
//
//	height 0: alice submits param-set(fork_v2_tc_height=42, eff=1)
//	height 0: producer seals, store stages it (pending).
//	height 1: producer seals (with a filler), hook calls
//	          PromotePending(1) → store activates 42 →
//	          re-pin pushes 42 into mining.SetForkV2TCHeight.
//	post:    mining.ForkV2TCHeight() == 42.
func TestTCWire_GovTxRePinsRuntimeKnob(t *testing.T) {
	resetMiningForkV2TC(t)
	r := buildTCRig(t, nil, "")

	if got := mining.ForkV2TCHeight(); got != math.MaxUint64 {
		t.Fatalf("precondition: mining knob = %d, want MaxUint64", got)
	}

	const newFork = uint64(42)
	tx := tcGovTx(t, tAlice, "tx-tcfork-1", 0, newFork, 1)
	if err := r.pool.Add(tx); err != nil {
		t.Fatalf("admission rejected fork_v2_tc_height tx: %v", err)
	}
	// Genesis block: applies the tx, stages pending, hook fires
	// Promote(0) which is a no-op for an effective_height=1
	// change.
	if _, err := r.producer.ProduceBlock(); err != nil {
		t.Fatalf("genesis block: %v", err)
	}
	if _, ok := r.w.GovParams.Pending(string(chainparams.ParamForkV2TCHeight)); !ok {
		t.Fatal("pending fork_v2_tc_height missing after genesis block")
	}
	if got := mining.ForkV2TCHeight(); got != math.MaxUint64 {
		t.Errorf("mining knob prematurely updated to %d at height 0; want MaxUint64",
			got)
	}

	// Seal block at height=1 with a filler tx → hook fires
	// Promote(1) → pending clears, active becomes newFork →
	// re-pin pushes newFork into the runtime knob.
	if err := r.pool.Add(fillerTx(tCarol, 0, "tcfork-promote")); err != nil {
		t.Fatalf("filler admit: %v", err)
	}
	blk, err := r.producer.ProduceBlock()
	if err != nil {
		t.Fatalf("filler block: %v", err)
	}
	if blk.Height != 1 {
		t.Fatalf("filler block height = %d, want 1", blk.Height)
	}

	if got := mining.ForkV2TCHeight(); got != newFork {
		t.Errorf("mining.ForkV2TCHeight() = %d after promote; want %d",
			got, newFork)
	}
	v, _ := r.w.GovParams.ActiveValue(string(chainparams.ParamForkV2TCHeight))
	if v != newFork {
		t.Errorf("ParamStore active = %d; want %d", v, newFork)
	}
	if _, ok := r.w.GovParams.Pending(string(chainparams.ParamForkV2TCHeight)); ok {
		t.Error("pending fork_v2_tc_height still present after promote")
	}
}

// -----------------------------------------------------------------------------
// 4. Snapshot replay across simulated restart
// -----------------------------------------------------------------------------

// TestTCWire_SnapshotReplayPinsKnob is the persistence
// regression test: an activated fork_v2_tc_height value must
// survive a simulated restart (rebuild the rig from the same
// store path) AND override any genesis-seed config field, so a
// node operator can't silently re-enable a fork that governance
// already decided to defer.
func TestTCWire_SnapshotReplayPinsKnob(t *testing.T) {
	resetMiningForkV2TC(t)
	storePath := filepath.Join(t.TempDir(), "params.json")

	// Stage 1: boot with seed=200 (future activation), submit a
	// gov tx that REPLACES that with 50 at effective_height=1,
	// then drive blocks to height=1 so the change activates.
	{
		r := buildTCRig(t, uint64Ptr(200), storePath)

		if got := mining.ForkV2TCHeight(); got != 200 {
			t.Fatalf("stage1 boot: mining knob = %d, want 200", got)
		}

		const replacedFork = uint64(50)
		tx := tcGovTx(t, tAlice, "tx-tcfork-replace", 0, replacedFork, 1)
		if err := r.pool.Add(tx); err != nil {
			t.Fatalf("stage1 admission: %v", err)
		}
		if _, err := r.producer.ProduceBlock(); err != nil {
			t.Fatalf("stage1 genesis: %v", err)
		}
		// Drive height=1 so promote fires.
		if err := r.pool.Add(fillerTx(tCarol, 0, "stage1-promote")); err != nil {
			t.Fatalf("stage1 filler admit: %v", err)
		}
		if _, err := r.producer.ProduceBlock(); err != nil {
			t.Fatalf("stage1 filler block: %v", err)
		}
		if got := mining.ForkV2TCHeight(); got != replacedFork {
			t.Errorf("stage1 post-promote mining knob = %d, want %d",
				got, replacedFork)
		}
	}

	// Reset the runtime knob to simulate a fresh process with
	// no in-memory state. This is the moment the persistence
	// path matters.
	mining.SetForkV2TCHeight(math.MaxUint64)

	// Stage 2: rebuild the rig with the SAME store path BUT
	// pass a different seed (999). The snapshot must win:
	// the runtime knob must come up at 50 (the activated
	// value), NOT 999 (the seed) and NOT MaxUint64 (the
	// pre-stage-2 default).
	{
		_ = buildTCRig(t, uint64Ptr(999), storePath)

		const expected = uint64(50)
		if got := mining.ForkV2TCHeight(); got != expected {
			t.Errorf("stage2 boot mining knob = %d, want %d (snapshot replay should win over seed)",
				got, expected)
		}
	}
}
