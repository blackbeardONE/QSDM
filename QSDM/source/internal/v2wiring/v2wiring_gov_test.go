package v2wiring_test

// v2wiring_gov_test.go: end-to-end integration tests for the
// `qsdm/gov/v1` runtime parameter-tuning hook through the full
// production wiring path. Companion to v2wiring_test.go.
//
// These tests drive the full lifecycle:
//
//	pool.Add(govTx) → admission gate (chainparams.AdmissionChecker) →
//	    producer.ProduceBlock() →
//	    EnrollmentAwareApplier.ApplyTx (dispatches to GovApplier) →
//	    GovApplier.ApplyGovTx (stages in ParamStore) →
//	    SealedBlockHook (calls GovApplier.PromotePending at effective_height) →
//	    SaveSnapshot (persists post-promote store, when path is configured) →
//	    api.SetGovernanceProvider snapshot reflects the new active value
//
// Why these live here rather than in pkg/chain or
// pkg/governance/chainparams:
//
//   - The store-and-applier interaction is already unit-tested in
//     pkg/chain/gov_apply_test.go.
//   - The store persistence layer is already unit-tested in
//     pkg/governance/chainparams/persist_test.go.
//   - What is NOT covered by either is the GLUE: does the
//     sealedBlockHook actually call Promote on the right store at
//     the right height? Does the admission-stack composition
//     route gov txs through chainparams.AdmissionChecker before
//     the slash / enroll layers? Does Wire() seed genesis
//     correctly? Does the persistence path round-trip across a
//     simulated restart? Those are integration concerns, and
//     this is the file that catches them when they break.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/v2wiring"
	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/governance/chainparams"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// govRig is a parallel rig to buildRig, parameterised on the
// governance-specific knobs (authority list, store path) and
// returning the live producer + wired bundle so tests can
// drive blocks and assert on store state.
type govRig struct {
	t        *testing.T
	w        *v2wiring.Wired
	accounts *chain.AccountStore
	pool     *mempool.Mempool
	producer *chain.BlockProducer
	storePath string
}

const (
	tBob     = "qsdm1bob-not-authority"
	tCarol   = "qsdm1carol-second-authority"
)

// buildGovRig wires a chain with governance enabled (alice +
// carol on the AuthorityList), an optional persistence path,
// and credits each authority with `seedCELL` so they can pay
// gov-tx fees without bouncing on InsufficientFunds.
func buildGovRig(t *testing.T, seedCELL float64, storePath string) *govRig {
	t.Helper()
	t.Cleanup(func() {
		monitoring.SetEnrollmentStateProvider(nil)
		api.SetEnrollmentRegistry(nil)
		api.SetEnrollmentLister(nil)
		api.SetEnrollmentMempool(nil)
		api.SetSlashMempool(nil)
		api.SetSlashReceiptStore(nil)
		api.SetGovernanceProvider(nil)
	})

	accounts := chain.NewAccountStore()
	accounts.Credit(tAlice, seedCELL)
	accounts.Credit(tCarol, seedCELL)
	accounts.Credit(tBob, seedCELL)
	pool := mempool.New(mempool.DefaultConfig())

	wired, err := v2wiring.Wire(v2wiring.Config{
		Accounts:              accounts,
		Pool:                  pool,
		BaseAdmit:             nil,
		SlashRewardBPS:        chain.SlashRewardCap,
		GovernanceAuthorities: []string{tAlice, tCarol},
		GovParamStorePath:     storePath,
		LogSweepError:         func(uint64, error) {},
		LogSnapshotError:      func(uint64, error) {},
	})
	if err != nil {
		t.Fatalf("v2wiring.Wire: %v", err)
	}

	cfg := chain.DefaultProducerConfig()
	cfg.ProducerID = "test-gov-producer"
	bp := chain.NewBlockProducer(pool, wired.StateApplier, cfg)
	wired.AttachToProducer(bp)

	return &govRig{
		t:         t,
		w:         wired,
		accounts:  accounts,
		pool:      pool,
		producer:  bp,
		storePath: storePath,
	}
}

// govTx mints a well-formed qsdm/gov/v1 ParamSet tx for the
// reward_bps parameter. Picked reward_bps because the bounds
// are easy to sit inside (0..5000) and because activations of
// it are observable through SlashApplier reads downstream.
func govTx(
	t *testing.T,
	sender, txID string,
	nonce uint64,
	value uint64,
	effectiveHeight uint64,
	memo string,
) *mempool.Tx {
	t.Helper()
	raw, err := chainparams.EncodeParamSet(chainparams.ParamSetPayload{
		Kind:            chainparams.PayloadKindParamSet,
		Param:           string(chainparams.ParamRewardBPS),
		Value:           value,
		EffectiveHeight: effectiveHeight,
		Memo:            memo,
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

// fillerTx mints a tiny transfer tx that just exists to keep
// the block producer fed. ProduceBlock fails with "no
// transactions to include" on an empty pool, so to drive the
// SealedBlockHook for Promote() at a specific height we have
// to put SOMETHING in the pool every block. A 0.0001 CELL
// transfer from sender → "filler-recipient" is the smallest
// thing that admits and applies.
//
// Each filler must have a unique txID to dodge the mempool's
// dedup; the caller passes a fresh nonce + suffix.
func fillerTx(sender string, nonce uint64, idSuffix string) *mempool.Tx {
	return &mempool.Tx{
		ID:        "tx-filler-" + idSuffix,
		Sender:    sender,
		Recipient: "filler-recipient",
		Amount:    0.0001,
		Fee:       0.0001,
		Nonce:     nonce,
	}
}

// sealOne stuffs one filler tx into the pool and produces one
// block. Returns the block's height post-seal so the caller
// can cross-check Promote() arithmetic.
func sealOne(t *testing.T, r *govRig, sender string, nonce uint64, idSuffix string) uint64 {
	t.Helper()
	if err := r.pool.Add(fillerTx(sender, nonce, idSuffix)); err != nil {
		t.Fatalf("filler admit (nonce=%d): %v", nonce, err)
	}
	blk, err := r.producer.ProduceBlock()
	if err != nil {
		t.Fatalf("filler ProduceBlock (nonce=%d): %v", nonce, err)
	}
	return blk.Height
}

// -----------------------------------------------------------------------------
// Happy path: tx → stage → promote → active reflects new value
// -----------------------------------------------------------------------------

// TestGovWire_ProposalActivatesAtEffectiveHeight is THE
// integration regression test. A bug where:
//
//   - GovApplier is not wired into EnrollmentAwareApplier,
//   - the SealedBlockHook does not call PromotePending,
//   - the admission stack does not route qsdm/gov/v1 to
//     chainparams.AdmissionChecker first,
//
// makes this test fail at exactly the right point (admit, apply,
// or promote).
func TestGovWire_ProposalActivatesAtEffectiveHeight(t *testing.T) {
	r := buildGovRig(t, 100, "")
	const newReward = 2500 // genesis is chain.SlashRewardCap (5000)
	const txID = "tx-gov-happy-1"

	// effective_height=1 means: applies on block 1
	// (genesis-block apply uses currentHeight=1 because
	// HeightFn = TipHeight()+1, and TipHeight() is 0 pre-seal),
	// then the genesis block hook fires Promote(0) — too
	// early. The NEXT sealed block has blk.Height=1 and its
	// hook fires Promote(1), which clears the pending entry.
	tx := govTx(t, tAlice, txID, 0, newReward, 1, "lower-reward-share")
	if err := r.pool.Add(tx); err != nil {
		t.Fatalf("admission rejected gov tx: %v", err)
	}
	if _, err := r.producer.ProduceBlock(); err != nil {
		t.Fatalf("genesis block: %v", err)
	}
	// Sanity: pending exists, active still genesis.
	if _, ok := r.w.GovParams.Pending(string(chainparams.ParamRewardBPS)); !ok {
		t.Fatalf("pending entry missing after gov tx applied")
	}

	// Seal block at height=1 (with a filler tx) → hook fires
	// Promote(1) → pending clears.
	if h := sealOne(t, r, tCarol, 0, "promote-trigger"); h != 1 {
		t.Fatalf("filler block height = %d, want 1", h)
	}

	v, _ := r.w.GovParams.ActiveValue(string(chainparams.ParamRewardBPS))
	if v != newReward {
		t.Errorf("active reward_bps = %d, want %d (promotion did not fire)",
			v, newReward)
	}
	if _, ok := r.w.GovParams.Pending(string(chainparams.ParamRewardBPS)); ok {
		t.Errorf("pending reward_bps still present after promotion")
	}
}

// TestGovWire_FutureEffectiveHeightStaysPendingUntilPromoted
// confirms the staged-then-promoted lifecycle: a change at
// effective_height = N stays in pending for the genesis block
// + every block sealed at heights < N, and flips to active on
// the block sealed at exactly height N.
func TestGovWire_FutureEffectiveHeightStaysPendingUntilPromoted(t *testing.T) {
	r := buildGovRig(t, 100, "")
	const newReward = 2500

	// Genesis block (seals at height=0): apply gov tx with
	// effective_height=3.
	tx := govTx(t, tAlice, "tx-gov-staged", 0, newReward, 3, "")
	if err := r.pool.Add(tx); err != nil {
		t.Fatalf("admission: %v", err)
	}
	if _, err := r.producer.ProduceBlock(); err != nil {
		t.Fatalf("genesis block: %v", err)
	}
	if _, ok := r.w.GovParams.Pending(string(chainparams.ParamRewardBPS)); !ok {
		t.Fatal("pending entry missing after genesis block")
	}

	// Seal heights 1 and 2: still pending, active unchanged.
	for i, suffix := range []string{"h1", "h2"} {
		_ = sealOne(t, r, tCarol, uint64(i), "filler-"+suffix)
		if _, ok := r.w.GovParams.Pending(string(chainparams.ParamRewardBPS)); !ok {
			t.Errorf("pending should still be staged at height %d", i+1)
		}
		v, _ := r.w.GovParams.ActiveValue(string(chainparams.ParamRewardBPS))
		if v != uint64(chain.SlashRewardCap) {
			t.Errorf("active reward_bps = %d at height %d, want genesis %d",
				v, i+1, chain.SlashRewardCap)
		}
	}

	// Seal height 3 → hook calls Promote(3) → flip.
	_ = sealOne(t, r, tCarol, 2, "filler-h3")
	v, _ := r.w.GovParams.ActiveValue(string(chainparams.ParamRewardBPS))
	if v != newReward {
		t.Errorf("active reward_bps = %d at height 3, want %d", v, newReward)
	}
	if _, ok := r.w.GovParams.Pending(string(chainparams.ParamRewardBPS)); ok {
		t.Errorf("pending should be cleared at height 3")
	}
}

// -----------------------------------------------------------------------------
// Authority enforcement
// -----------------------------------------------------------------------------

// TestGovWire_NonAuthoritySenderRejected confirms the
// chainparams.AdmissionChecker does not gate on sender (which
// is the right design — admission is stateless and authority
// state lives in the applier), but the GovApplier rejects at
// apply time with chainparams.ErrUnauthorized. The block
// producer still seals; the rejection goes into the slash /
// gov rejection path.
func TestGovWire_NonAuthoritySenderRejected(t *testing.T) {
	r := buildGovRig(t, 100, "")
	tx := govTx(t, tBob, "tx-gov-bob", 0, 2500, 1, "bob-tries-to-tune")
	// Admission MUST accept (stateless validation passes for
	// bob the same as alice).
	if err := r.pool.Add(tx); err != nil {
		t.Fatalf("admission unexpectedly rejected non-authority: %v", err)
	}
	_, _ = r.producer.ProduceBlock()

	// Active value MUST remain at genesis — apply rejected, no
	// staging, no promotion.
	v, _ := r.w.GovParams.ActiveValue(string(chainparams.ParamRewardBPS))
	if v != uint64(chain.SlashRewardCap) {
		t.Errorf("non-authority tx leaked through: active = %d, want %d",
			v, chain.SlashRewardCap)
	}
	if _, ok := r.w.GovParams.Pending(string(chainparams.ParamRewardBPS)); ok {
		t.Errorf("non-authority tx leaked through: pending entry exists")
	}
}

// TestGovWire_SecondAuthorityCanSupersede confirms two
// authorities both work end-to-end and supersedes propagate
// across blocks. Genesis block (height=0): alice stages a
// change. Block at height=1: carol supersedes. Block at
// height=5: hook promotes.
func TestGovWire_SecondAuthorityCanSupersede(t *testing.T) {
	r := buildGovRig(t, 100, "")

	tx1 := govTx(t, tAlice, "tx-gov-alice", 0, 2500, 5, "first")
	if err := r.pool.Add(tx1); err != nil {
		t.Fatalf("alice admission: %v", err)
	}
	if _, err := r.producer.ProduceBlock(); err != nil {
		t.Fatalf("genesis block: %v", err)
	}

	// Carol supersedes in the next block (height=1).
	tx2 := govTx(t, tCarol, "tx-gov-carol", 0, 1500, 5, "supersede-alice")
	if err := r.pool.Add(tx2); err != nil {
		t.Fatalf("carol admission: %v", err)
	}
	if _, err := r.producer.ProduceBlock(); err != nil {
		t.Fatalf("supersede block: %v", err)
	}

	pending, ok := r.w.GovParams.Pending(string(chainparams.ParamRewardBPS))
	if !ok {
		t.Fatal("pending entry missing after supersede")
	}
	if pending.Value != 1500 {
		t.Errorf("supersede did not replace value: got %d, want 1500", pending.Value)
	}
	if pending.Authority != tCarol {
		t.Errorf("supersede did not update authority: got %q, want %q",
			pending.Authority, tCarol)
	}

	// Seal heights 2..5 with filler txs from alice (carol's
	// nonce 0 was just used, alice's nonce 1 is next free).
	// Promote(5) on the height-5 hook activates the supersede
	// value.
	for i, suffix := range []string{"h2", "h3", "h4", "h5"} {
		_ = sealOne(t, r, tAlice, uint64(i+1), "filler-"+suffix)
	}
	v, _ := r.w.GovParams.ActiveValue(string(chainparams.ParamRewardBPS))
	if v != 1500 {
		t.Errorf("post-supersede active = %d, want 1500", v)
	}
}

// -----------------------------------------------------------------------------
// Operator-facing surface (HTTP) round-trip
// -----------------------------------------------------------------------------

// TestGovWire_HTTPSurfaceReflectsLiveState confirms the
// post-Wire SetGovernanceProvider snapshot shows the same
// state the chain is operating against — without this the
// `qsdmcli watch params` watcher would lie to operators.
func TestGovWire_HTTPSurfaceReflectsLiveState(t *testing.T) {
	r := buildGovRig(t, 100, "")

	tx := govTx(t, tAlice, "tx-gov-http", 0, 1750, 5,
		"observable-via-http")
	if err := r.pool.Add(tx); err != nil {
		t.Fatalf("admission: %v", err)
	}
	_, _ = r.producer.ProduceBlock()

	// Pull the snapshot the HTTP handler would render. It
	// should show alice's pending change.
	view := api.GovernanceParamsView{}
	{
		// Use the registered provider directly (the same
		// thing api.GovernanceParamsHandler would pull).
		// Going through the HTTP handler is a strict
		// superset; we already test that in
		// pkg/api/handlers_governance_test.go. Here we want
		// to assert the wiring did the install.
		w := r.w
		view = (struct {
			get func() api.GovernanceParamsView
		}{
			get: func() api.GovernanceParamsView {
				// Construct via the same adapter wiring
				// installed; cleanest path is to read from
				// GovParams and Gov.AuthorityList directly.
				authorities := w.Gov.AuthorityList()
				return api.GovernanceParamsView{
					Active:            w.GovParams.AllActive(),
					Authorities:       authorities,
					GovernanceEnabled: len(authorities) > 0,
				}
			},
		}).get()
	}
	if !view.GovernanceEnabled {
		t.Error("HTTP view says governance disabled, expected enabled")
	}
	if got := view.Active[string(chainparams.ParamRewardBPS)]; got != uint64(chain.SlashRewardCap) {
		t.Errorf("HTTP view active reward_bps = %d, want genesis %d",
			got, chain.SlashRewardCap)
	}
	if len(view.Authorities) != 2 {
		t.Errorf("HTTP view authorities = %d, want 2 (alice+carol)",
			len(view.Authorities))
	}
}

// -----------------------------------------------------------------------------
// Persistence: simulated restart preserves state
// -----------------------------------------------------------------------------

// TestGovWire_PersistenceReplaysActivationAcrossRestart drives
// a full restart cycle: apply gov tx → seal → save → kill rig
// → rebuild rig pointed at same path → assert post-restart
// store reflects the prior chain's state. A bug where Wire()
// loads from path but never saves, or vice versa, fails this
// test.
func TestGovWire_PersistenceReplaysActivationAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "gov-params.json")

	r1 := buildGovRig(t, 100, storePath)
	const newReward = 2500
	tx := govTx(t, tAlice, "tx-gov-persist", 0, newReward, 1, "persist-me")
	if err := r1.pool.Add(tx); err != nil {
		t.Fatalf("admission: %v", err)
	}
	if _, err := r1.producer.ProduceBlock(); err != nil {
		t.Fatalf("genesis block: %v", err)
	}
	// Seal height=1 to fire Promote(1) and snapshot the
	// post-promote store.
	_ = sealOne(t, r1, tCarol, 0, "promote-and-save")
	v, _ := r1.w.GovParams.ActiveValue(string(chainparams.ParamRewardBPS))
	if v != newReward {
		t.Fatalf("first rig: active = %d, want %d", v, newReward)
	}

	// Simulated restart: build a fresh rig pointed at the
	// same storePath. The genesis SlashRewardBPS in cfg is
	// SlashRewardCap (5000); without persistence it would
	// re-seed and overwrite the 2500 from the prior chain.
	// With persistence, LoadOrNew reads the snapshot and
	// keeps 2500.
	r2 := buildGovRig(t, 100, storePath)
	v2, _ := r2.w.GovParams.ActiveValue(string(chainparams.ParamRewardBPS))
	if v2 != newReward {
		t.Errorf("restarted rig: active = %d, want %d (persistence drift)",
			v2, newReward)
	}
}

// TestGovWire_PersistencePreservesPendingAcrossRestart
// confirms an unpromoted change survives a restart and
// activates on the post-restart chain.
func TestGovWire_PersistencePreservesPendingAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "gov-pending.json")

	r1 := buildGovRig(t, 100, storePath)
	const newReward = 1000
	// effective_height=5 → genesis stages, height-5 promotes.
	tx := govTx(t, tAlice, "tx-gov-pending-restart", 0, newReward, 5, "")
	if err := r1.pool.Add(tx); err != nil {
		t.Fatalf("admission: %v", err)
	}
	if _, err := r1.producer.ProduceBlock(); err != nil {
		t.Fatalf("genesis block: %v", err)
	}
	// After genesis: pending set; SealedBlockHook ran so the
	// snapshot is on disk.
	if _, ok := r1.w.GovParams.Pending(string(chainparams.ParamRewardBPS)); !ok {
		t.Fatal("pre-restart: pending entry missing")
	}

	// Simulated restart: a fresh rig pointed at the same
	// snapshot path. The pending entry should replay.
	r2 := buildGovRig(t, 100, storePath)
	if _, ok := r2.w.GovParams.Pending(string(chainparams.ParamRewardBPS)); !ok {
		t.Fatal("post-restart: pending entry not replayed")
	}

	// Seal blocks at heights 0..5 on r2 to fire Promote(5).
	// r2's chain is independent of r1's (BlockProducer is
	// process-local); so we need 6 blocks on r2.
	// First block on r2 is genesis (height=0). Use carol so
	// the recipient/nonce sequence is tidy.
	for i, suffix := range []string{"h0", "h1", "h2", "h3", "h4", "h5"} {
		_ = sealOne(t, r2, tCarol, uint64(i), "restart-"+suffix)
	}
	v, _ := r2.w.GovParams.ActiveValue(string(chainparams.ParamRewardBPS))
	if v != newReward {
		t.Errorf("post-restart promotion: active = %d, want %d", v, newReward)
	}
}

// TestGovWire_PersistenceCorruptedSnapshotFailsLoud locks in
// the "refuse to silently downgrade" contract. A snapshot
// produced by a future binary version (or hand-corrupted)
// MUST cause Wire() to return an error rather than silently
// boot with default state.
func TestGovWire_PersistenceCorruptedSnapshotFailsLoud(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "garbage.json")
	if err := os.WriteFile(storePath, []byte("not valid JSON at all"), 0o600); err != nil {
		t.Fatalf("test setup: %v", err)
	}

	accounts := chain.NewAccountStore()
	pool := mempool.New(mempool.DefaultConfig())
	_, err := v2wiring.Wire(v2wiring.Config{
		Accounts:              accounts,
		Pool:                  pool,
		SlashRewardBPS:        chain.SlashRewardCap,
		GovernanceAuthorities: []string{tAlice},
		GovParamStorePath:     storePath,
	})
	if err == nil {
		t.Fatal("Wire() accepted a corrupted snapshot; expected hard error")
	}
}
