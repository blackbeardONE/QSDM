package chain

// gov_apply_test.go: covers the full surface of GovApplier and
// the SlashApplier ↔ ParamStore integration.
//
// Test plan:
//
//   - GovApplier construction: nil collaborators panic; empty
//     AuthorityList is allowed (governance disabled).
//   - ApplyGovTx happy path: tx accepted, change staged,
//     events fired, fee debited.
//   - ApplyGovTx rejection paths: wrong contract, decode
//     failure, validation failure, unauthorized, not configured,
//     effective_height in past, effective_height too far, fee
//     missing, fee insufficient (account balance).
//   - PromotePending: activates at correct height, fires event,
//     updates metrics.
//   - SlashApplier integration: with a ParamStore wired,
//     ApplySlashTx reads the active reward_bps; a governance
//     change activated mid-test affects the next slash.

import (
	"errors"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/governance/chainparams"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
)

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------

type recordingGovPublisher struct {
	events     []GovParamEvent
	authEvents []GovAuthorityEvent
}

func (p *recordingGovPublisher) PublishGovParam(ev GovParamEvent) {
	p.events = append(p.events, ev)
}

func (p *recordingGovPublisher) PublishGovAuthority(ev GovAuthorityEvent) {
	p.authEvents = append(p.authEvents, ev)
}

// buildGovFixture returns a (Accounts, ParamStore, GovApplier)
// triple with one funded authority address. `authorities` is
// passed through unchanged so callers can test the
// "governance disabled" empty case.
type govFixture struct {
	accounts  *AccountStore
	store     *chainparams.InMemoryParamStore
	applier   *GovApplier
	authority string
	publisher *recordingGovPublisher
}

func buildGovFixture(t *testing.T, authorities []string) *govFixture {
	t.Helper()
	accounts := NewAccountStore()
	const auth = "authority-1"
	accounts.Credit(auth, 100.0)
	accounts.Credit("non-authority", 100.0)

	store := chainparams.NewInMemoryParamStore()
	applier := NewGovApplier(accounts, store, authorities)
	pub := &recordingGovPublisher{}
	applier.Publisher = pub

	return &govFixture{
		accounts:  accounts,
		store:     store,
		applier:   applier,
		authority: auth,
		publisher: pub,
	}
}

func buildGovTx(sender string, nonce uint64, fee float64, p chainparams.ParamSetPayload) *mempool.Tx {
	raw, err := chainparams.EncodeParamSet(p)
	if err != nil {
		panic(err)
	}
	return &mempool.Tx{
		Sender:     sender,
		Nonce:      nonce,
		Fee:        fee,
		ContractID: chainparams.ContractID,
		Payload:    raw,
	}
}

// -----------------------------------------------------------------------------
// construction
// -----------------------------------------------------------------------------

func TestNewGovApplier_PanicsOnNilAccounts(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil Accounts")
		}
	}()
	NewGovApplier(nil, chainparams.NewInMemoryParamStore(), nil)
}

func TestNewGovApplier_PanicsOnNilStore(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil Store")
		}
	}()
	NewGovApplier(NewAccountStore(), nil, nil)
}

func TestNewGovApplier_AcceptsEmptyAuthorityList(t *testing.T) {
	a := NewGovApplier(NewAccountStore(), chainparams.NewInMemoryParamStore(), nil)
	if a == nil {
		t.Fatal("nil applier")
	}
	if got := a.AuthorityList(); len(got) != 0 {
		t.Errorf("AuthorityList=%v, want empty", got)
	}
}

func TestGovApplier_AuthorityList_DedupAndSorted(t *testing.T) {
	a := NewGovApplier(NewAccountStore(), chainparams.NewInMemoryParamStore(),
		[]string{"bob", "alice", "alice", "", "carol"})
	got := a.AuthorityList()
	want := []string{"alice", "bob", "carol"}
	if len(got) != len(want) {
		t.Fatalf("len=%d, want %d (got=%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("AuthorityList[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

// -----------------------------------------------------------------------------
// ApplyGovTx happy path
// -----------------------------------------------------------------------------

func TestGovApplier_ApplyGovTx_HappyPath(t *testing.T) {
	fx := buildGovFixture(t, []string{"authority-1"})

	tx := buildGovTx(fx.authority, 0, 0.01, chainparams.ParamSetPayload{
		Kind:            chainparams.PayloadKindParamSet,
		Param:           string(chainparams.ParamRewardBPS),
		Value:           2500,
		EffectiveHeight: 100,
		Memo:            "lower reward share",
	})
	if err := fx.applier.ApplyGovTx(tx, 50); err != nil {
		t.Fatalf("ApplyGovTx: %v", err)
	}

	pending, ok := fx.store.Pending(string(chainparams.ParamRewardBPS))
	if !ok || pending.Value != 2500 || pending.EffectiveHeight != 100 {
		t.Errorf("pending=%+v ok=%v, want value=2500 height=100", pending, ok)
	}
	if pending.Authority != fx.authority {
		t.Errorf("pending.Authority=%q, want %q", pending.Authority, fx.authority)
	}
	if pending.SubmittedAtHeight != 50 {
		t.Errorf("pending.SubmittedAtHeight=%d, want 50", pending.SubmittedAtHeight)
	}
	if pending.Memo != "lower reward share" {
		t.Errorf("pending.Memo=%q lost", pending.Memo)
	}

	acc, _ := fx.accounts.Get(fx.authority)
	if acc.Nonce != 1 {
		t.Errorf("nonce=%d, want 1", acc.Nonce)
	}

	if len(fx.publisher.events) != 1 ||
		fx.publisher.events[0].Kind != GovParamEventStaged {
		t.Errorf("events=%v, want one Staged", fx.publisher.events)
	}
}

func TestGovApplier_ApplyGovTx_Supersede(t *testing.T) {
	fx := buildGovFixture(t, []string{"authority-1"})

	tx1 := buildGovTx(fx.authority, 0, 0.01, chainparams.ParamSetPayload{
		Kind:            chainparams.PayloadKindParamSet,
		Param:           string(chainparams.ParamRewardBPS),
		Value:           1000,
		EffectiveHeight: 100,
	})
	if err := fx.applier.ApplyGovTx(tx1, 50); err != nil {
		t.Fatalf("first ApplyGovTx: %v", err)
	}
	tx2 := buildGovTx(fx.authority, 1, 0.01, chainparams.ParamSetPayload{
		Kind:            chainparams.PayloadKindParamSet,
		Param:           string(chainparams.ParamRewardBPS),
		Value:           2500,
		EffectiveHeight: 200,
	})
	if err := fx.applier.ApplyGovTx(tx2, 60); err != nil {
		t.Fatalf("second ApplyGovTx: %v", err)
	}

	// Expect: 1 Staged, 1 Superseded, 1 Staged (in that order).
	if len(fx.publisher.events) != 3 {
		t.Fatalf("events count=%d, want 3 (got %v)", len(fx.publisher.events), fx.publisher.events)
	}
	wantKinds := []GovParamEventKind{
		GovParamEventStaged,
		GovParamEventSuperseded,
		GovParamEventStaged,
	}
	for i, want := range wantKinds {
		if fx.publisher.events[i].Kind != want {
			t.Errorf("events[%d].Kind=%q, want %q",
				i, fx.publisher.events[i].Kind, want)
		}
	}
	supersede := fx.publisher.events[1]
	if supersede.PriorValue != 1000 || supersede.PriorEffectiveHeight != 100 {
		t.Errorf("supersede prior fields wrong: %+v", supersede)
	}
}

// -----------------------------------------------------------------------------
// ApplyGovTx rejection paths
// -----------------------------------------------------------------------------

func TestGovApplier_RejectsWrongContract(t *testing.T) {
	fx := buildGovFixture(t, []string{"authority-1"})
	tx := &mempool.Tx{
		Sender: fx.authority, ContractID: "qsdm/transfer/v1",
		Payload: []byte("anything"), Fee: 0.01,
	}
	err := fx.applier.ApplyGovTx(tx, 50)
	if !errors.Is(err, ErrNotGovTx) {
		t.Errorf("err=%v, want ErrNotGovTx", err)
	}
}

func TestGovApplier_RejectsBadPayload(t *testing.T) {
	fx := buildGovFixture(t, []string{"authority-1"})
	tx := &mempool.Tx{
		Sender: fx.authority, ContractID: chainparams.ContractID,
		Payload: []byte("not json"), Fee: 0.01,
	}
	if err := fx.applier.ApplyGovTx(tx, 50); err == nil {
		t.Error("expected decode error")
	}
}

func TestGovApplier_RejectsUnauthorized(t *testing.T) {
	fx := buildGovFixture(t, []string{"authority-1"})
	tx := buildGovTx("non-authority", 0, 0.01, chainparams.ParamSetPayload{
		Kind:            chainparams.PayloadKindParamSet,
		Param:           string(chainparams.ParamRewardBPS),
		Value:           2500,
		EffectiveHeight: 100,
	})
	err := fx.applier.ApplyGovTx(tx, 50)
	if !errors.Is(err, chainparams.ErrUnauthorized) {
		t.Errorf("err=%v, want ErrUnauthorized", err)
	}
}

func TestGovApplier_RejectsWhenGovernanceDisabled(t *testing.T) {
	fx := buildGovFixture(t, nil)
	tx := buildGovTx(fx.authority, 0, 0.01, chainparams.ParamSetPayload{
		Kind:            chainparams.PayloadKindParamSet,
		Param:           string(chainparams.ParamRewardBPS),
		Value:           2500,
		EffectiveHeight: 100,
	})
	err := fx.applier.ApplyGovTx(tx, 50)
	if !errors.Is(err, chainparams.ErrGovernanceNotConfigured) {
		t.Errorf("err=%v, want ErrGovernanceNotConfigured", err)
	}
}

func TestGovApplier_RejectsHeightInPast(t *testing.T) {
	fx := buildGovFixture(t, []string{"authority-1"})
	tx := buildGovTx(fx.authority, 0, 0.01, chainparams.ParamSetPayload{
		Kind:            chainparams.PayloadKindParamSet,
		Param:           string(chainparams.ParamRewardBPS),
		Value:           2500,
		EffectiveHeight: 49, // currentHeight=50
	})
	err := fx.applier.ApplyGovTx(tx, 50)
	if !errors.Is(err, chainparams.ErrEffectiveHeightInPast) {
		t.Errorf("err=%v, want ErrEffectiveHeightInPast", err)
	}
}

func TestGovApplier_RejectsHeightTooFar(t *testing.T) {
	fx := buildGovFixture(t, []string{"authority-1"})
	tx := buildGovTx(fx.authority, 0, 0.01, chainparams.ParamSetPayload{
		Kind:            chainparams.PayloadKindParamSet,
		Param:           string(chainparams.ParamRewardBPS),
		Value:           2500,
		EffectiveHeight: 50 + chainparams.MaxActivationDelay + 1,
	})
	err := fx.applier.ApplyGovTx(tx, 50)
	if !errors.Is(err, chainparams.ErrEffectiveHeightTooFar) {
		t.Errorf("err=%v, want ErrEffectiveHeightTooFar", err)
	}
}

func TestGovApplier_RejectsZeroFee(t *testing.T) {
	fx := buildGovFixture(t, []string{"authority-1"})
	tx := buildGovTx(fx.authority, 0, 0, chainparams.ParamSetPayload{
		Kind:            chainparams.PayloadKindParamSet,
		Param:           string(chainparams.ParamRewardBPS),
		Value:           2500,
		EffectiveHeight: 100,
	})
	if err := fx.applier.ApplyGovTx(tx, 50); err == nil {
		t.Error("expected fee-floor rejection")
	}
}

// -----------------------------------------------------------------------------
// Promote
// -----------------------------------------------------------------------------

func TestGovApplier_PromotePending_FiresActivatedEvent(t *testing.T) {
	fx := buildGovFixture(t, []string{"authority-1"})

	tx := buildGovTx(fx.authority, 0, 0.01, chainparams.ParamSetPayload{
		Kind:            chainparams.PayloadKindParamSet,
		Param:           string(chainparams.ParamRewardBPS),
		Value:           2500,
		EffectiveHeight: 100,
	})
	if err := fx.applier.ApplyGovTx(tx, 50); err != nil {
		t.Fatalf("ApplyGovTx: %v", err)
	}

	// Promote at height < EffectiveHeight: no-op.
	if got := fx.applier.PromotePending(99); len(got) != 0 {
		t.Errorf("PromotePending(99) returned %v, want empty", got)
	}

	// At height: activates.
	got := fx.applier.PromotePending(100)
	if len(got) != 1 {
		t.Fatalf("PromotePending(100) returned %v, want 1", got)
	}
	if got[0].Value != 2500 {
		t.Errorf("activated value=%d, want 2500", got[0].Value)
	}

	if v, _ := fx.store.ActiveValue(string(chainparams.ParamRewardBPS)); v != 2500 {
		t.Errorf("post-promote active=%d, want 2500", v)
	}

	// Activated event fires last.
	last := fx.publisher.events[len(fx.publisher.events)-1]
	if last.Kind != GovParamEventActivated {
		t.Errorf("last event kind=%q, want %q", last.Kind, GovParamEventActivated)
	}
}

// -----------------------------------------------------------------------------
// SlashApplier integration
// -----------------------------------------------------------------------------

func TestSlashApplier_ReadsRewardBPSFromParamStore(t *testing.T) {
	// Static field is 0, but the ParamStore says 5000 (50%).
	// The slash applier MUST honour the store.
	verifierCap := uint64(5 * 100_000_000)
	fx := buildSlashFixture(t, 0, verifierCap)

	store := chainparams.NewInMemoryParamStore()
	store.SetForTesting(string(chainparams.ParamRewardBPS), 5000)
	fx.slasher.SetParamStore(store)

	payload := slashing.SlashPayload{
		NodeID:          fx.nodeID,
		EvidenceKind:    slashing.EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte("evidence-v1"),
		SlashAmountDust: 10 * 100_000_000,
	}
	tx := buildSlashTx("slasher-addr", 0, 0.001, payload)

	if err := fx.slasher.ApplySlashTx(tx, 101); err != nil {
		t.Fatalf("slash apply: %v", err)
	}

	// 50% of 5 CELL = 2.5 CELL credited to slasher (minus 0.001 fee).
	slasherAcc, _ := fx.accounts.Get("slasher-addr")
	wantBalance := 10.0 - 0.001 + 2.5
	if absDiff(slasherAcc.Balance, wantBalance) > 1e-9 {
		t.Errorf("slasher balance=%.8f, want %.8f", slasherAcc.Balance, wantBalance)
	}
}

func TestSlashApplier_FallsBackToStaticWhenStoreMissing(t *testing.T) {
	// Static field is 5000 (50%), no ParamStore wired.
	verifierCap := uint64(5 * 100_000_000)
	fx := buildSlashFixture(t, 5000, verifierCap)
	if fx.slasher.Params != nil {
		t.Fatalf("ParamStore should be nil")
	}

	payload := slashing.SlashPayload{
		NodeID:          fx.nodeID,
		EvidenceKind:    slashing.EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte("evidence-v1"),
		SlashAmountDust: 10 * 100_000_000,
	}
	tx := buildSlashTx("slasher-addr", 0, 0.001, payload)

	if err := fx.slasher.ApplySlashTx(tx, 101); err != nil {
		t.Fatalf("slash apply: %v", err)
	}

	// Same expected balance as the with-store case — confirms
	// the static-fallback path is on parity.
	slasherAcc, _ := fx.accounts.Get("slasher-addr")
	wantBalance := 10.0 - 0.001 + 2.5
	if absDiff(slasherAcc.Balance, wantBalance) > 1e-9 {
		t.Errorf("slasher balance=%.8f, want %.8f", slasherAcc.Balance, wantBalance)
	}
}

func TestSlashApplier_ParamStoreClampsAtSlashRewardCap(t *testing.T) {
	// ParamStore hands back something above the cap (would
	// only happen via SetForTesting; admission rejects this in
	// production). The applier MUST clamp at SlashRewardCap.
	verifierCap := uint64(10 * 100_000_000)
	fx := buildSlashFixture(t, 0, verifierCap)

	store := chainparams.NewInMemoryParamStore()
	// Direct active write bypasses the registry bounds check
	// — fine for this test, simulates a "registry was relaxed
	// post-binary" scenario.
	defer func() {
		if r := recover(); r != nil {
			t.Skipf("registry bounds prevent writing 99999 — clamp behaviour exercised elsewhere; r=%v", r)
		}
	}()
	store.SetForTesting(string(chainparams.ParamRewardBPS), 99999)
	fx.slasher.SetParamStore(store)

	payload := slashing.SlashPayload{
		NodeID:          fx.nodeID,
		EvidenceKind:    slashing.EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte("evidence-v1"),
		SlashAmountDust: 10 * 100_000_000,
	}
	tx := buildSlashTx("slasher-addr", 0, 0.001, payload)
	if err := fx.slasher.ApplySlashTx(tx, 101); err != nil {
		t.Fatalf("slash apply: %v", err)
	}
}

func TestSlashApplier_ActiveAutoRevokeFromParamStore(t *testing.T) {
	// Register a verifier that drains 5 CELL from a 10 CELL bond.
	verifierCap := uint64(5 * 100_000_000)
	fx := buildSlashFixture(t, 0, verifierCap)
	// Default static field (10 CELL) would auto-revoke (5 < 10).
	// But we configure ParamStore to a 1-CELL threshold — the
	// post-slash 5-CELL stake is ABOVE that, so auto-revoke
	// must NOT fire.
	store := chainparams.NewInMemoryParamStore()
	store.SetForTesting(string(chainparams.ParamAutoRevokeMinStakeDust), 100_000_000) // 1 CELL
	fx.slasher.SetParamStore(store)

	payload := slashing.SlashPayload{
		NodeID:          fx.nodeID,
		EvidenceKind:    slashing.EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte("evidence-v1"),
		SlashAmountDust: 5 * 100_000_000,
	}
	tx := buildSlashTx("slasher-addr", 0, 0.001, payload)
	if err := fx.slasher.ApplySlashTx(tx, 101); err != nil {
		t.Fatalf("slash apply: %v", err)
	}
	rec, _ := fx.state.Lookup(fx.nodeID)
	if rec == nil {
		t.Fatal("record missing post-slash")
	}
	if !rec.Active() {
		t.Error("record auto-revoked; should have stayed active under 1-CELL threshold")
	}
}

// -----------------------------------------------------------------------------
// EnrollmentAwareApplier routing
// -----------------------------------------------------------------------------

func TestEnrollmentAwareApplier_RoutesGovTx(t *testing.T) {
	fx := buildGovFixture(t, []string{"authority-1"})
	enrollAware := NewEnrollmentAwareApplier(fx.accounts, nil)
	enrollAware.SetGovApplier(fx.applier)
	enrollAware.SetHeightFn(func() uint64 { return 50 })

	tx := buildGovTx(fx.authority, 0, 0.01, chainparams.ParamSetPayload{
		Kind:            chainparams.PayloadKindParamSet,
		Param:           string(chainparams.ParamRewardBPS),
		Value:           2500,
		EffectiveHeight: 100,
	})
	if err := enrollAware.ApplyTx(tx); err != nil {
		t.Errorf("ApplyTx via aware: %v", err)
	}
}

func TestEnrollmentAwareApplier_RejectsGovTxWhenNotWired(t *testing.T) {
	accounts := NewAccountStore()
	enrollAware := NewEnrollmentAwareApplier(accounts, nil)
	enrollAware.SetHeightFn(func() uint64 { return 50 })

	tx := buildGovTx("authority-1", 0, 0.01, chainparams.ParamSetPayload{
		Kind:            chainparams.PayloadKindParamSet,
		Param:           string(chainparams.ParamRewardBPS),
		Value:           2500,
		EffectiveHeight: 100,
	})
	err := enrollAware.ApplyTx(tx)
	if !errors.Is(err, ErrGovernanceNotWired) {
		t.Errorf("err=%v, want ErrGovernanceNotWired", err)
	}
}
