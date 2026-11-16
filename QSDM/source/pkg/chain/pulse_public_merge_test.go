package chain

// Tests for the join points where PULSE meets the public tree's QSDM-20 token state: the state-root fold order and
// the admission nesting. PULSE folds after QSDM-20, so (1) with no PULSE state every public root is unchanged and
// (2) a chain without tokens folds exactly as a build without the token store does.

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/pulse"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// The golden roots of TestQSDM20RootUnchangedUntilFirstToken, with an EMPTY PULSE store wired as well: the same
// roots step by step, then the first token folds exactly as before (no PULSE segment).
func TestPulseEmptyStoreKeepsThePublicRoots(t *testing.T) {
	setQSDM20ActivationForTest(t, 1)
	alice := strings.Repeat("a1", 32)
	bob := strings.Repeat("b2", 32)
	carol := strings.Repeat("c3", 32)
	payer := newFuzzKey(41)
	accounts := NewAccountStore()
	accounts.Credit(alice, 100)
	accounts.Credit(payer.address, 20)
	aware := NewEnrollmentAwareApplier(accounts, nil)
	aware.SetTaskStateStore(NewTaskStateStore())
	aware.SetStreamStateStore(NewStreamStateStore())
	aware.SetRecoveryCapsuleStateStore(NewRecoveryCapsuleStateStore())
	tokens := NewQSDM20StateStore()
	aware.SetQSDM20StateStore(tokens)
	aware.SetPulseStateStore(NewPulseStateStore())
	aware.SetHeightFn(func() uint64 { return 7 })
	golden := []string{
		"449454402baaa564685de7e12b3b09360c58be962301eeb8acb02be76537b467",
		"2882561d60f2d92219606768c9f7ec7f7387b1f4c2629028ee17d5ddaf503898",
		"cbc155ad9efb161981441d8718bc894be26c70c7f6cf7a70b512f85aa55352e4",
		"64abd5e60f20c99950b970d5bfecafec37f037a23927953318a6e62354426ee7",
		"7c5c8d3daf68ff11abfb32e49f72bef42c5607c1d8237e4a0c28015d510a9cd4",
	}
	if got := aware.StateRoot(); got != golden[0] {
		t.Fatalf("root 0 = %s, want %s", got, golden[0])
	}
	txs := []*mempool.Tx{
		{ID: "golden-1", Sender: alice, Recipient: bob, Amount: 10, Fee: 0.5, Nonce: 0},
		{ID: "golden-2", Sender: bob, Recipient: carol, Amount: 3, Fee: 0.25, Nonce: 0},
		{ID: "golden-3", Sender: alice, Recipient: carol, Amount: 1.5, Fee: 0, Nonce: 1},
	}
	session := ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey)
	open := StreamAction{
		ID: "golden-stream-open", Sender: payer.address, StreamID: "golden-stream", Action: StreamActionOpen,
		Provider: carol, ServiceID: "qsdm-vpn", DeviceIDHash: strings.Repeat("d4", 32),
		SessionPublicKey: hex.EncodeToString(session), PriceDust: 2 * DustPerCell, PricePeriodSeconds: 2_592_000,
		BudgetDust: 2 * DustPerCell, MaxActiveSeconds: 2_592_000, ExpiresAt: "2026-09-01T00:00:00Z",
		Nonce: 0, Timestamp: "2026-08-01T00:00:00Z",
	}
	payload, _ := json.Marshal(open)
	txs = append(txs, &mempool.Tx{ID: open.ID, Sender: payer.address, Nonce: 0, Payload: payload,
		ContractID: StreamContractID, Signature: hex.EncodeToString(payer.mustSign(t, payload)),
		PublicKey: hex.EncodeToString(payer.pub)})
	for i, tx := range txs {
		if err := aware.ApplyTx(tx); err != nil {
			t.Fatal(err)
		}
		if got := aware.StateRoot(); got != golden[i+1] {
			t.Fatalf("root %d = %s, want the public root %s", i+1, got, golden[i+1])
		}
	}
	issuer := newQSDM20Wallet(t)
	accounts.Credit(issuer.address, 1)
	if err := aware.ApplyTx(issuer.signedTx(t, 0, 0, qsdm20Create("FIRST", "First token", 0, "1", "1", QSDM20MintNone, ""))); err != nil {
		t.Fatal(err)
	}
	accountsOnly := NewEnrollmentAwareApplier(accounts, nil)
	accountsOnly.SetStreamStateStore(aware.StreamStateStore())
	sum := sha256.Sum256([]byte("state:" + accountsOnly.StateRoot() + "\nqsdm-20:" + tokens.StateRoot()))
	if got := aware.StateRoot(); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("an empty PULSE store changed the root with a token: %s, want %s", got, hex.EncodeToString(sum[:]))
	}
}

// pulseRoot is the documented fold: SHA-256("state:" + root + "\npulse:" + PULSE root).
func pulseRoot(base, ps string) string {
	s := sha256.Sum256([]byte("state:" + base + "\npulse:" + ps))
	return hex.EncodeToString(s[:])
}

// With PULSE state and no tokens the root is base -> pulse (the producer's build has no token store, so this is
// its order); with a token as well, the PULSE segment folds after the QSDM-20 segment.
func TestPulseFoldsAfterQSDM20(t *testing.T) {
	setQSDM20ActivationForTest(t, 1)
	f := newPulseFix(t, activeParams())
	tokens := NewQSDM20StateStore()
	f.aware.SetQSDM20StateStore(tokens)
	f.must(f.pulseTx(f.m1, PulseAction{Op: "join", Pool: string(f.pool.ID())}))

	plain := NewEnrollmentAwareApplier(f.accounts, NewEnrollmentApplier(f.accounts, f.state))
	if got, want := f.aware.StateRoot(), pulseRoot(plain.StateRoot(), f.ps.StateRoot()); got != want {
		t.Fatalf("no tokens: root %s, want base->pulse %s", got, want)
	}

	issuer := newQSDM20Wallet(t)
	f.accounts.Credit(issuer.address, 1)
	if err := f.aware.ApplyTx(issuer.signedTx(t, 0, 0, qsdm20Create("PLS", "Pulse test", 0, "1", "1", QSDM20MintNone, ""))); err != nil {
		t.Fatal(err)
	}
	if tokens.Count() == 0 {
		t.Fatal("the token was not created")
	}
	withTokens := NewEnrollmentAwareApplier(f.accounts, NewEnrollmentApplier(f.accounts, f.state))
	withTokens.SetQSDM20StateStore(tokens)
	if got, want := f.aware.StateRoot(), pulseRoot(withTokens.StateRoot(), f.ps.StateRoot()); got != want {
		t.Fatalf("with a token: root %s, want base->qsdm-20->pulse %s", got, want)
	}
	// And not the other order.
	tokenFirst := sha256.Sum256([]byte("state:" + pulseRoot(plain.StateRoot(), f.ps.StateRoot()) + "\nqsdm-20:" + tokens.StateRoot()))
	if f.aware.StateRoot() == hex.EncodeToString(tokenFirst[:]) {
		t.Fatal("PULSE folded before QSDM-20")
	}
}

// Each admission layer only intercepts its own ContractID: a PULSE tx reaches the PULSE layer through the token
// layer, and a token tx passes the PULSE layer untouched.
func TestPulseAndQSDM20AdmissionLayersAreIndependent(t *testing.T) {
	base := errors.New("base reached")
	chainFn := PulseAdmissionChecker(RecoveryCapsuleAdmissionChecker(QSDM20AdmissionChecker(func() (uint64, bool) { return 0, false },
		func(*mempool.Tx) error { return base })))
	if err := chainFn(&mempool.Tx{ID: "x", ContractID: PulseContractID, Payload: []byte("{}")}); errors.Is(err, base) || err == nil {
		t.Fatalf("a PULSE tx was not handled by the PULSE layer: %v", err)
	}
	if err := chainFn(&mempool.Tx{ID: "y", ContractID: WalletTransferContractID}); !errors.Is(err, base) {
		t.Fatalf("a transfer did not reach the base: %v", err)
	}
	if err := chainFn(&mempool.Tx{ID: "z", ContractID: QSDM20ContractID}); errors.Is(err, base) {
		t.Fatal("a token tx skipped the token layer")
	}
}

// The settlement result on the public tree, pinned with deterministic keys. The same scenario on the producer's
// build gives the same balances and PULSE state root (checked when this port was made).
func TestPulseSettlementCrossCheckVector(t *testing.T) {
	seed := func(b byte) [32]byte {
		var s [32]byte
		for i := range s {
			s[i] = b
		}
		return s
	}
	f := newPulseFix(t, nil)
	f.pool, f.m1, f.m2 = pulse.KeyPairFromSeed(seed(1)), pulse.KeyPairFromSeed(seed(2)), pulse.KeyPairFromSeed(seed(3))
	f.fee = string(pulse.KeyPairFromSeed(seed(4)).ID())
	p := activeParams()
	p.Pools = []pulse.PoolEntry{{ID: f.pool.ID(), PublicKey: hex.EncodeToString(f.pool.Pub.Bytes())}}
	if err := SetPulseParams(*p); err != nil {
		t.Fatal(err)
	}
	pool, m1, m2 := string(f.pool.ID()), string(f.m1.ID()), string(f.m2.ID())
	f.must(f.pulseTx(f.m1, PulseAction{Op: "join", Pool: pool}))
	f.must(f.pulseTx(f.m2, PulseAction{Op: "join", Pool: pool}))
	f.rewardOK(PulseEscrowAddress(pool, m1), 3, "p1", "p2")
	body, sig := f.batch(1, 100, []string{proofHex("p1"), proofHex("p2")},
		[]pulse.Weight{{Owner: f.m1.ID(), Weight: 1}, {Owner: f.m2.ID(), Weight: 3}})
	f.must(f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: body, BatchSig: sig}))
	if !near(f.bal(m1), 0.7425) || !near(f.bal(m2), 2.2275) || !near(f.bal(f.fee), 0.03) {
		t.Fatalf("m1 %.8f m2 %.8f fee %.8f", f.bal(m1), f.bal(m2), f.bal(f.fee))
	}
	const wantPulseRoot = "40f2dd74745b39ad95d16f09c20de0cf29229c7517527ad40b816fc32155c578"
	if got := f.ps.StateRoot(); got != wantPulseRoot {
		t.Fatalf("PULSE state root %s, want %s", got, wantPulseRoot)
	}
	const wantBlockRoot = "4cb7e1d3b9b348c1f284eee594c8a329fd69a81debec6d32ee4149b8ddf4fe85"
	if got := f.aware.StateRoot(); got != wantBlockRoot {
		t.Fatalf("block state root %s, want %s", got, wantBlockRoot)
	}
}
