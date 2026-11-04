package bridge

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/crypto"
)

func newTestSigner(t *testing.T) *crypto.Dilithium {
	t.Helper()
	d := crypto.NewDilithium()
	if d == nil {
		t.Fatal("NewDilithium returned nil")
	}
	return d
}

// newAuthRelay builds a relay without a network, for driving processRemote.
func newAuthRelay(t *testing.T, cfg RelayAuthConfig) (*P2PRelay, *BridgeProtocol, *AtomicSwapProtocol) {
	t.Helper()
	a, err := newRelayAuth(cfg)
	if err != nil {
		t.Fatalf("newRelayAuth: %v", err)
	}
	bp := &BridgeProtocol{locks: make(map[string]*Lock)}
	asp := &AtomicSwapProtocol{swaps: make(map[string]*Swap)}
	return &P2PRelay{bridge: bp, swap: asp, auth: a}, bp, asp
}

// signedEvent builds the wire bytes a relay with the given signer publishes.
func signedEvent(t *testing.T, signer EventSigner, kind string, payload any, origin string) []byte {
	t.Helper()
	pub := &P2PRelay{}
	if signer != nil {
		a, err := newRelayAuth(RelayAuthConfig{Signer: signer})
		if err != nil {
			t.Fatalf("signer auth: %v", err)
		}
		pub.auth = a
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	evt, err := pub.buildEvent(kind, raw, origin)
	if err != nil {
		t.Fatalf("buildEvent: %v", err)
	}
	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func testLock(id string) Lock {
	return Lock{
		ID: id, SourceChain: "a", TargetChain: "b", Asset: "TOKEN", Amount: 5,
		SecretHash: "h", Secret: "pre-image", Status: LockStatusLocked,
		LockedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}
}

func lockCount(bp *BridgeProtocol) int {
	bp.mu.RLock()
	defer bp.mu.RUnlock()
	return len(bp.locks)
}

func TestRelayAuth_DefaultIgnoresAllRemoteEvents(t *testing.T) {
	relay, bp, asp := newAuthRelay(t, RelayAuthConfig{})
	signer := newTestSigner(t)

	relay.processRemote(signedEvent(t, nil, "lock_created", testLock("l1"), "peer"), "self")
	relay.processRemote(signedEvent(t, signer, "lock_created", testLock("l2"), "peer"), "self")
	relay.processRemote(signedEvent(t, signer, "swap_initiated", Swap{ID: "s1", Status: SwapStatusInitiated}, "peer"), "self")

	if n := lockCount(bp); n != 0 {
		t.Fatalf("default relay applied %d remote locks", n)
	}
	if len(asp.swaps) != 0 {
		t.Fatalf("default relay applied remote swaps")
	}
	if got := relay.Stats().NoTrustedKeys; got != 3 {
		t.Fatalf("NoTrustedKeys = %d, want 3", got)
	}
	if relay.TrustedKeyCount() != 0 || relay.Signing() {
		t.Fatal("default relay must have no keys and no signer")
	}
}

func TestRelayAuth_TrustedSignedEventApplied(t *testing.T) {
	signer := newTestSigner(t)
	relay, bp, asp := newAuthRelay(t, RelayAuthConfig{TrustedKeys: [][]byte{signer.GetPublicKey()}})

	relay.processRemote(signedEvent(t, signer, "lock_created", testLock("l1"), "peer"), "self")
	bp.mu.RLock()
	l, ok := bp.locks["l1"]
	bp.mu.RUnlock()
	if !ok {
		t.Fatalf("trusted signed lock not applied; stats %+v", relay.Stats())
	}
	if l.Secret != "" {
		t.Fatalf("secret must not be stored from gossip, got %q", l.Secret)
	}

	// Status events in the form PublishLockEvent actually sends (full object).
	relay.processRemote(signedEvent(t, signer, "lock_redeemed", testLock("l1"), "peer"), "self")
	if l.Status != LockStatusRedeemed {
		t.Fatalf("lock status = %s, want redeemed", l.Status)
	}

	relay.processRemote(signedEvent(t, signer, "swap_initiated", Swap{ID: "s1", Status: SwapStatusInitiated, InitiatorSecret: "x"}, "peer"), "self")
	relay.processRemote(signedEvent(t, signer, "swap_participated", Swap{ID: "s1"}, "peer"), "self")
	relay.processRemote(signedEvent(t, signer, "swap_completed", Swap{ID: "s1"}, "peer"), "self")
	s := asp.swaps["s1"]
	if s == nil || s.Status != SwapStatusCompleted || s.InitiatorSecret != "" {
		t.Fatalf("swap = %+v, want completed without secret", s)
	}
	if got := relay.Stats().Applied; got != 5 {
		t.Fatalf("Applied = %d, want 5 (stats %+v)", got, relay.Stats())
	}
}

func TestRelayAuth_RejectsUnsignedUntrustedAndForged(t *testing.T) {
	trusted := newTestSigner(t)
	other := newTestSigner(t)
	relay, bp, _ := newAuthRelay(t, RelayAuthConfig{TrustedKeys: [][]byte{trusted.GetPublicKey()}})

	// Unsigned.
	relay.processRemote(signedEvent(t, nil, "lock_created", testLock("u"), "peer"), "self")
	// Signed by a key that is not on the allowlist.
	relay.processRemote(signedEvent(t, other, "lock_created", testLock("o"), "peer"), "self")

	// Untrusted signature relabelled with the trusted key ID.
	var evt BridgeP2PEvent
	if err := json.Unmarshal(signedEvent(t, other, "lock_created", testLock("f"), "peer"), &evt); err != nil {
		t.Fatal(err)
	}
	evt.SignerKeyID = BridgeKeyID(trusted.GetPublicKey())
	forged, _ := json.Marshal(evt)
	relay.processRemote(forged, "self")

	// Trusted signature, payload altered after signing.
	if err := json.Unmarshal(signedEvent(t, trusted, "lock_created", testLock("p"), "peer"), &evt); err != nil {
		t.Fatal(err)
	}
	evt.Payload = json.RawMessage(strings.Replace(string(evt.Payload), `"Amount":5`, `"Amount":5000`, 1))
	tampered, _ := json.Marshal(evt)
	relay.processRemote(tampered, "self")

	// Trusted signature, kind changed after signing.
	if err := json.Unmarshal(signedEvent(t, trusted, "lock_created", testLock("k"), "peer"), &evt); err != nil {
		t.Fatal(err)
	}
	evt.Kind = "lock_refunded"
	rekinded, _ := json.Marshal(evt)
	relay.processRemote(rekinded, "self")

	// Garbage signature encoding.
	evt.Kind = "lock_created"
	evt.Signature = "!!!"
	garbage, _ := json.Marshal(evt)
	relay.processRemote(garbage, "self")

	relay.processRemote([]byte("{not json"), "self")

	if n := lockCount(bp); n != 0 {
		t.Fatalf("%d locks applied from rejected events", n)
	}
	st := relay.Stats()
	if st.Unsigned != 1 || st.UntrustedSigner != 1 || st.BadSignature != 4 || st.Malformed != 1 || st.Applied != 0 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestRelayAuth_RejectsStaleAndFutureEvents(t *testing.T) {
	signer := newTestSigner(t)
	now := time.Now()
	relay, bp, _ := newAuthRelay(t, RelayAuthConfig{
		TrustedKeys: [][]byte{signer.GetPublicKey()},
		MaxEventAge: 5 * time.Minute,
		Now:         func() time.Time { return now },
	})
	data := signedEvent(t, signer, "lock_created", testLock("l"), "peer")

	now = time.Now().Add(6 * time.Minute) // replayed too late
	relay.processRemote(data, "self")
	now = time.Now().Add(-10 * time.Minute) // receiver clock far behind sender
	relay.processRemote(data, "self")
	if lockCount(bp) != 0 || relay.Stats().Stale != 2 {
		t.Fatalf("stale events applied: locks=%d stats=%+v", lockCount(bp), relay.Stats())
	}
	now = time.Now()
	relay.processRemote(data, "self")
	if lockCount(bp) != 1 {
		t.Fatalf("fresh event not applied: %+v", relay.Stats())
	}
}

func TestRelayAuth_StateTransitionChecks(t *testing.T) {
	signer := newTestSigner(t)
	relay, bp, asp := newAuthRelay(t, RelayAuthConfig{TrustedKeys: [][]byte{signer.GetPublicKey()}})
	send := func(kind string, payload any) {
		relay.processRemote(signedEvent(t, signer, kind, payload, "peer"), "self")
	}
	ref := func(key, id string) map[string]string { return map[string]string{key: id} }

	// No terminal state for unknown objects.
	send("lock_redeemed", ref("lock_id", "ghost"))
	send("lock_refunded", ref("ID", "ghost"))
	send("swap_completed", ref("swap_id", "ghost"))
	send("swap_participated", ref("ID", "ghost"))
	// Objects may only be created in their initial state.
	redeemed := testLock("pre-redeemed")
	redeemed.Status = LockStatusRedeemed
	send("lock_created", redeemed)
	send("swap_initiated", Swap{ID: "pre-done", Status: SwapStatusCompleted})
	// Bad IDs.
	send("lock_created", testLock(""))
	send("lock_created", testLock(strings.Repeat("x", maxBridgeObjectIDLen+1)))
	if lockCount(bp) != 0 || len(asp.swaps) != 0 {
		t.Fatalf("objects created from invalid events: locks=%d swaps=%d", lockCount(bp), len(asp.swaps))
	}
	if got := relay.Stats().BadTransition; got != 8 {
		t.Fatalf("BadTransition = %d, want 8", got)
	}

	// Terminal local states are never overwritten.
	bp.locks["done"] = &Lock{ID: "done", Status: LockStatusRedeemed}
	bp.locks["expired"] = &Lock{ID: "expired", Status: LockStatusExpired}
	asp.swaps["refunded"] = &Swap{ID: "refunded", Status: SwapStatusRefunded}
	send("lock_refunded", ref("lock_id", "done"))
	send("lock_redeemed", ref("lock_id", "expired"))
	send("swap_completed", ref("swap_id", "refunded"))
	if bp.locks["done"].Status != LockStatusRedeemed || bp.locks["expired"].Status != LockStatusExpired ||
		asp.swaps["refunded"].Status != SwapStatusRefunded {
		t.Fatal("terminal state overwritten by remote event")
	}

	// Existing objects are not replaced; repeats are no-ops.
	bp.locks["live"] = &Lock{ID: "live", Amount: 1, Status: LockStatusLocked}
	send("lock_created", testLock("live"))
	if bp.locks["live"].Amount != 1 {
		t.Fatal("existing lock replaced")
	}
	send("lock_redeemed", ref("lock_id", "live"))
	send("lock_redeemed", ref("lock_id", "live"))
	// swap_refunded is never applied from gossip.
	asp.swaps["open"] = &Swap{ID: "open", Status: SwapStatusInitiated}
	send("swap_refunded", Swap{ID: "open"})
	if asp.swaps["open"].Status != SwapStatusInitiated {
		t.Fatal("swap_refunded applied from gossip")
	}
	st := relay.Stats()
	if st.BadTransition != 11 || st.Applied != 1 || st.NoOp != 3 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestRelayAuth_SelfOriginSkipped(t *testing.T) {
	signer := newTestSigner(t)
	relay, bp, _ := newAuthRelay(t, RelayAuthConfig{TrustedKeys: [][]byte{signer.GetPublicKey()}})
	relay.processRemote(signedEvent(t, signer, "lock_created", testLock("mine"), "self"), "self")
	if lockCount(bp) != 0 || relay.Stats() != (RelayStats{}) {
		t.Fatalf("self-origin event processed: %+v", relay.Stats())
	}
}

func TestRelayAuth_SignedEnvelopeRoundTrip(t *testing.T) {
	signer := newTestSigner(t)
	data := signedEvent(t, signer, "lock_created", testLock("l"), "origin")
	var evt BridgeP2PEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		t.Fatal(err)
	}
	if evt.SignerKeyID != BridgeKeyID(signer.GetPublicKey()) || evt.Signature == "" {
		t.Fatalf("event not signed: %+v", evt)
	}
	// Unsigned publishers emit no signature fields at all.
	if strings.Contains(string(signedEvent(t, nil, "lock_created", testLock("l"), "o")), "sig") {
		t.Fatal("unsigned event carries signature fields")
	}
}

func TestRelayAuth_ConfigValidation(t *testing.T) {
	if _, err := newRelayAuth(RelayAuthConfig{TrustedKeys: [][]byte{make([]byte, 10)}}); err == nil {
		t.Fatal("short trusted key accepted")
	}
	signer := newTestSigner(t)
	pub := signer.GetPublicKey()
	got, err := ParseBridgeRelayerPublicKey("  0x" + strings.ToUpper(hex.EncodeToString(pub)) + "\n")
	if err != nil || string(got) != string(pub) {
		t.Fatalf("ParseBridgeRelayerPublicKey: %v", err)
	}
	for _, bad := range []string{"", "zz", "abcd", hex.EncodeToString(pub[:len(pub)-1])} {
		if _, err := ParseBridgeRelayerPublicKey(bad); err == nil {
			t.Fatalf("ParseBridgeRelayerPublicKey(%.12q) accepted", bad)
		}
	}
	if _, err := newRelayAuth(RelayAuthConfig{Signer: crypto.NewDilithiumVerifyOnly()}); err == nil {
		t.Fatal("signer without public key accepted")
	}
}
