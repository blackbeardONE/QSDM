package v2wiring_test

// v2wiring_test.go: integration smoke tests that exercise the
// same Wire(...) call shape cmd/qsdm/main.go uses in production.
// Confirms that a node configured with:
//
//   - InMemoryState + EnrollmentApplier + EnrollmentAwareApplier
//   - SlashApplier built off doublemining.NewProductionSlashingDispatcher
//   - mempool admission gate composed via enrollment.AdmissionChecker
//   - monitoring.SetEnrollmentStateProvider populated
//   - api.SetEnrollmentMempool populated
//   - producer.OnSealedBlock = aware.SealedBlockHook(...)
//   - aware.SetHeightFn(producer.TipHeight + 1)
//
// produces an end-to-end behaviour where:
//
//  1. An enroll tx flows admission → pool → producer → applier
//     and lands as an active record visible via direct registry
//     lookup AND via the monitoring gauge provider (one source
//     of truth).
//  2. Malformed enrollment txs bounce at admission, before they
//     ever reach ApplyTx — the gauge stays at zero.
//  3. SealedBlockHook auto-runs SweepMaturedEnrollments at the
//     unbond maturity height, releasing locked stake.
//  4. A second Wire() call replaces the prior monitoring state
//     provider rather than aliasing it, so a process restart
//     never reports stale gauges.
//
// Failure modes the test catches:
//
//   - Forgetting to call SetHeightFn → ErrEnrollmentHeightUnset.
//   - Forgetting to install OnSealedBlock → matured stake never
//     released, gauge stays elevated forever.
//   - Forgetting to compose AdmissionChecker → bare account-store
//     admission accepts malformed enroll txs into the pool.
//   - Forgetting SetEnrollmentStateProvider → gauges read 0
//     forever even with active records.
//   - Provider replacement bug → gauges show stale data after
//     a re-wire.

import (
	"bytes"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/v2wiring"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

const (
	tAlice   = "qsdm1alice"
	tNodeID  = "alice-rtx4090-01"
	tGPUUUID = "GPU-abcd1234-5678-90ef-1234-567890abcdef"
)

// rig assembles a fresh Wired bundle around a fresh AccountStore
// and BlockProducer, exactly mirroring the cmd/qsdm/main.go boot
// sequence (minus POL/BFT predicates, which would require the
// whole consensus stack and don't add any v2-wiring coverage).
type rig struct {
	t        *testing.T
	w        *v2wiring.Wired
	accounts *chain.AccountStore
	pool     *mempool.Mempool
	producer *chain.BlockProducer
}

func buildRig(t *testing.T, aliceCELL float64) *rig {
	t.Helper()
	t.Cleanup(func() {
		monitoring.SetEnrollmentStateProvider(nil)
	})

	accounts := chain.NewAccountStore()
	accounts.Credit(tAlice, aliceCELL)
	pool := mempool.New(mempool.DefaultConfig())

	wired, err := v2wiring.Wire(v2wiring.Config{
		Accounts:       accounts,
		Pool:           pool,
		BaseAdmit:      nil,
		SlashRewardBPS: chain.SlashRewardCap,
		LogSweepError:  func(uint64, error) {},
	})
	if err != nil {
		t.Fatalf("v2wiring.Wire: %v", err)
	}

	cfg := chain.DefaultProducerConfig()
	cfg.ProducerID = "test-producer"
	bp := chain.NewBlockProducer(pool, wired.StateApplier, cfg)
	wired.AttachToProducer(bp)

	return &rig{
		t:        t,
		w:        wired,
		accounts: accounts,
		pool:     pool,
		producer: bp,
	}
}

// enrollTx mints a well-formed enroll payload using the same
// fixture material as pkg/chain/enrollment_apply_test.go.
func enrollTx(t *testing.T, sender string, nonce uint64, txID string) *mempool.Tx {
	t.Helper()
	payload := enrollment.EnrollPayload{
		Kind:      enrollment.PayloadKindEnroll,
		NodeID:    tNodeID,
		GPUUUID:   tGPUUUID,
		HMACKey:   bytes.Repeat([]byte{0xAB}, 32),
		StakeDust: mining.MinEnrollStakeDust,
		Memo:      "v2wiring-test",
	}
	raw, err := enrollment.EncodeEnrollPayload(payload)
	if err != nil {
		t.Fatalf("EncodeEnrollPayload: %v", err)
	}
	return &mempool.Tx{
		ID:         txID,
		Sender:     sender,
		Nonce:      nonce,
		Fee:        0.01,
		Payload:    raw,
		ContractID: enrollment.ContractID,
	}
}

func unenrollTx(t *testing.T, sender, nodeID string, nonce uint64, txID string) *mempool.Tx {
	t.Helper()
	payload := enrollment.UnenrollPayload{
		Kind:   enrollment.PayloadKindUnenroll,
		NodeID: nodeID,
		Reason: "v2wiring-test",
	}
	raw, err := enrollment.EncodeUnenrollPayload(payload)
	if err != nil {
		t.Fatalf("EncodeUnenrollPayload: %v", err)
	}
	return &mempool.Tx{
		ID:         txID,
		Sender:     sender,
		Nonce:      nonce,
		Fee:        0.001,
		Payload:    raw,
		ContractID: enrollment.ContractID,
	}
}

func produce(t *testing.T, r *rig) *chain.Block {
	t.Helper()
	blk, err := r.producer.ProduceBlock()
	if err != nil {
		t.Fatalf("ProduceBlock: %v", err)
	}
	if blk == nil {
		t.Fatalf("ProduceBlock returned nil block")
	}
	return blk
}

// -----------------------------------------------------------------------------
// Wire() input validation
// -----------------------------------------------------------------------------

func TestWire_RejectsMissingAccounts(t *testing.T) {
	_, err := v2wiring.Wire(v2wiring.Config{
		Pool: mempool.New(mempool.DefaultConfig()),
	})
	if err == nil {
		t.Fatal("Wire accepted missing Accounts; expected error")
	}
}

func TestWire_RejectsMissingPool(t *testing.T) {
	_, err := v2wiring.Wire(v2wiring.Config{
		Accounts: chain.NewAccountStore(),
	})
	if err == nil {
		t.Fatal("Wire accepted missing Pool; expected error")
	}
}

func TestWire_RejectsRewardOverCap(t *testing.T) {
	_, err := v2wiring.Wire(v2wiring.Config{
		Accounts:       chain.NewAccountStore(),
		Pool:           mempool.New(mempool.DefaultConfig()),
		SlashRewardBPS: chain.SlashRewardCap + 1,
	})
	if err == nil {
		t.Fatal("Wire accepted reward over SlashRewardCap; expected error")
	}
}

// -----------------------------------------------------------------------------
// End-to-end enroll flow
// -----------------------------------------------------------------------------

func TestWire_EnrollFlowsThroughEntireStack(t *testing.T) {
	r := buildRig(t, 20)

	tx := enrollTx(t, tAlice, 0, "tx-enroll-smoke-1")
	if err := r.pool.Add(tx); err != nil {
		t.Fatalf("mempool.Add: %v", err)
	}

	produce(t, r)

	// Active record visible via direct registry lookup AND via
	// the monitoring gauge provider; both paths share one mutex
	// on InMemoryState — divergence here is a wiring bug.
	rec, err := r.w.EnrollmentState.Lookup(tNodeID)
	if err != nil {
		t.Fatalf("registry lookup post-block: %v", err)
	}
	if !rec.Active() {
		t.Errorf("post-block record not Active; revoked_at=%d", rec.RevokedAtHeight)
	}
	if got := monitoring.EnrollmentStateActiveCount(); got != 1 {
		t.Errorf("active gauge after enroll: got %d, want 1", got)
	}
	if got := monitoring.EnrollmentStateBondedDust(); got != mining.MinEnrollStakeDust {
		t.Errorf("bonded gauge after enroll: got %d, want %d",
			got, mining.MinEnrollStakeDust)
	}

	// Bond debited + locked into the registry, not transferred
	// to a recipient.
	alice, _ := r.accounts.Get(tAlice)
	want := 20 - float64(mining.MinEnrollStakeDust)/1e8 - tx.Fee
	if alice.Balance != want {
		t.Errorf("alice balance: got %v, want %v", alice.Balance, want)
	}
}

// -----------------------------------------------------------------------------
// Admission gate
// -----------------------------------------------------------------------------

func TestWire_AdmissionGateRejectsMalformedEnroll(t *testing.T) {
	r := buildRig(t, 20)

	bad := &mempool.Tx{
		ID:         "tx-malformed-1",
		Sender:     tAlice,
		Nonce:      0,
		Fee:        0.01,
		ContractID: enrollment.ContractID,
		Payload:    []byte(`{"kind":"weird","node_id":"rig-x"}`),
	}

	if err := r.pool.Add(bad); err == nil {
		t.Fatalf("admission gate accepted malformed enroll tx; expected rejection")
	}
	if got := monitoring.EnrollmentStateActiveCount(); got != 0 {
		t.Errorf("active gauge after rejection: got %d, want 0", got)
	}
}

// TestWire_AdmissionGateAcceptsTransferUnchanged proves the
// enrollment admission gate doesn't accidentally reject ordinary
// transfer txs — this would be a regression that breaks v1
// traffic on a v2-aware node.
func TestWire_AdmissionGateAcceptsTransferUnchanged(t *testing.T) {
	r := buildRig(t, 20)

	transfer := &mempool.Tx{
		ID:        "tx-transfer-1",
		Sender:    tAlice,
		Recipient: "bob",
		Amount:    1.0,
		Fee:       0.001,
		Nonce:     0,
	}
	if err := r.pool.Add(transfer); err != nil {
		t.Fatalf("transfer rejected by admission gate: %v", err)
	}
	if _, err := r.producer.ProduceBlock(); err != nil {
		t.Fatalf("ProduceBlock with transfer: %v", err)
	}

	bob, _ := r.accounts.Get("bob")
	if bob.Balance != 1.0 {
		t.Errorf("transfer not applied: bob balance got %v, want 1.0", bob.Balance)
	}
}

// TestWire_ReinstallAdmissionGate proves swapping the BaseAdmit
// after Wire keeps the enrollment validators intact and adds the
// new predicate.
func TestWire_ReinstallAdmissionGate(t *testing.T) {
	r := buildRig(t, 20)

	// Reinstall with a base predicate that always rejects.
	reject := func(*mempool.Tx) error {
		return chain.ErrPolExtensionBlocked
	}
	v2wiring.ReinstallAdmissionGate(r.pool, reject)

	// Transfer must now be rejected by the new BaseAdmit.
	transfer := &mempool.Tx{
		ID: "tx-reject-1", Sender: tAlice, Recipient: "bob",
		Amount: 1.0, Fee: 0.001, Nonce: 0,
	}
	if err := r.pool.Add(transfer); err == nil {
		t.Fatalf("reinstalled BaseAdmit did not reject transfer")
	}

	// Enrollment validation still runs on enrollment-tagged
	// txs (i.e. the BaseAdmit is not consulted for these).
	if err := r.pool.Add(enrollTx(t, tAlice, 0, "tx-enroll-after-reinstall")); err != nil {
		t.Fatalf("enroll rejected after reinstall: %v", err)
	}
}

// -----------------------------------------------------------------------------
// SealedBlockHook auto-sweep
// -----------------------------------------------------------------------------

func TestWire_SealedBlockHookSweepsMatured(t *testing.T) {
	r := buildRig(t, 20)

	if err := r.pool.Add(enrollTx(t, tAlice, 0, "tx-enroll-1")); err != nil {
		t.Fatalf("enroll Add: %v", err)
	}
	produce(t, r)

	if got := monitoring.EnrollmentStateActiveCount(); got != 1 {
		t.Fatalf("post-enroll active gauge: got %d, want 1", got)
	}

	if err := r.pool.Add(unenrollTx(t, tAlice, tNodeID, 1, "tx-unenroll-1")); err != nil {
		t.Fatalf("unenroll Add: %v", err)
	}
	produce(t, r)

	if got := monitoring.EnrollmentStateActiveCount(); got != 0 {
		t.Errorf("post-unenroll active gauge: got %d, want 0", got)
	}
	if got := monitoring.EnrollmentStatePendingUnbondCount(); got != 1 {
		t.Errorf("post-unenroll pending gauge: got %d, want 1", got)
	}

	// Synthesize a matured-block hook fire. The producer
	// itself isn't going to seal `UnbondWindowBlocks` more
	// blocks under a unit test budget, so we invoke the hook
	// directly at the mature height — the same closure
	// SealedBlockHook returns.
	rec, err := r.w.EnrollmentState.Lookup(tNodeID)
	if err != nil {
		t.Fatalf("post-unenroll lookup: %v", err)
	}
	r.producer.OnSealedBlock(&chain.Block{Height: rec.UnbondMaturesAtHeight})

	if got := monitoring.EnrollmentStatePendingUnbondCount(); got != 0 {
		t.Errorf("post-sweep pending gauge: got %d, want 0", got)
	}
	if got := monitoring.EnrollmentStateBondedDust(); got != 0 {
		t.Errorf("post-sweep bonded gauge: got %d, want 0", got)
	}
}

// -----------------------------------------------------------------------------
// Provider replacement on re-wire
// -----------------------------------------------------------------------------

func TestWire_StateProviderReinstallReplacesPrior(t *testing.T) {
	r1 := buildRig(t, 20)
	if err := r1.pool.Add(enrollTx(t, tAlice, 0, "tx-r1-enroll")); err != nil {
		t.Fatalf("r1 enroll Add: %v", err)
	}
	produce(t, r1)
	if got := monitoring.EnrollmentStateActiveCount(); got != 1 {
		t.Fatalf("r1 active gauge: got %d, want 1", got)
	}

	// Second boot with a fresh InMemoryState. The monitoring
	// gauge MUST now read from the new state (zero records),
	// not the prior one — replacement, not aliasing.
	_ = buildRig(t, 20)
	if got := monitoring.EnrollmentStateActiveCount(); got != 0 {
		t.Errorf("r2 active gauge before any tx: got %d, want 0 "+
			"(SetEnrollmentStateProvider did not replace prior provider)", got)
	}
}

// -----------------------------------------------------------------------------
// Slash routing
// -----------------------------------------------------------------------------

// TestWire_SlashApplierIsRoutable confirms the SlashApplier was
// constructed and attached to the aware shim. We don't actually
// build a slash tx here (that requires real evidence + signing
// + dispatcher state); the wiring contract is "if slashing
// dispatcher build succeeded, aware.SlashApplier() != nil".
func TestWire_SlashApplierIsRoutable(t *testing.T) {
	r := buildRig(t, 20)
	if r.w.Slasher == nil {
		t.Error("Wired.Slasher is nil; production dispatcher build failed silently")
	}
	if r.w.Aware.SlashApplier() == nil {
		t.Error("aware.SlashApplier() returned nil after Wire")
	}
}
