package tunnel

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-yamux/v5"
)

// -----------------------------------------------------------
// AuthInputs / SignAuth / VerifyAuth
// -----------------------------------------------------------

func mustKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return k
}

func TestSignAuth_VerifyRoundTrip(t *testing.T) {
	key := mustKey(t)
	in := AuthInputs{
		Version:   UpgradeProtocol,
		SlotID:    "blackbeard-3050",
		SignerID:  "attester-deadbeefcafebabe",
		Timestamp: 1700000000,
	}
	h, err := SignAuth(key, in)
	if err != nil {
		t.Fatalf("SignAuth: %v", err)
	}
	if !VerifyAuth(key, in, h) {
		t.Fatalf("VerifyAuth rejected its own signature")
	}
	// Wrong key MUST fail.
	other := mustKey(t)
	if VerifyAuth(other, in, h) {
		t.Fatalf("VerifyAuth accepted a wrong-key signature")
	}
	// Tampered slot MUST fail.
	bad := in
	bad.SlotID = "blackbeard-3060"
	if VerifyAuth(key, bad, h) {
		t.Fatalf("VerifyAuth accepted a tampered slot")
	}
	// Tampered timestamp MUST fail.
	bad = in
	bad.Timestamp++
	if VerifyAuth(key, bad, h) {
		t.Fatalf("VerifyAuth accepted a tampered timestamp")
	}
}

func TestSignAuth_RejectsShortKey(t *testing.T) {
	if _, err := SignAuth(make([]byte, 8), AuthInputs{
		Version: "v1", SlotID: "x", SignerID: "y", Timestamp: 1,
	}); err == nil {
		t.Fatalf("expected error for 8-byte key")
	}
}

func TestSignAuth_RejectsBadInputs(t *testing.T) {
	key := mustKey(t)
	cases := []AuthInputs{
		{Version: "", SlotID: "a", SignerID: "b", Timestamp: 1},
		{Version: "v1", SlotID: "", SignerID: "b", Timestamp: 1},
		{Version: "v1", SlotID: "bad/slot", SignerID: "b", Timestamp: 1},
		{Version: "v1", SlotID: "a", SignerID: "", Timestamp: 1},
		{Version: "v1", SlotID: "a", SignerID: "b\n", Timestamp: 1},
		{Version: "v1", SlotID: "a", SignerID: "b", Timestamp: 0},
		{Version: "v1", SlotID: "a", SignerID: "b", Timestamp: -5},
	}
	for i, tc := range cases {
		if _, err := SignAuth(key, tc); err == nil {
			t.Errorf("case %d: SignAuth accepted bad input %+v", i, tc)
		}
	}
}

func TestVerifyAuth_RejectsBadHexNoPanic(t *testing.T) {
	key := mustKey(t)
	in := AuthInputs{Version: "v1", SlotID: "a", SignerID: "b", Timestamp: 1}
	if VerifyAuth(key, in, "not-hex") {
		t.Fatalf("VerifyAuth accepted non-hex auth")
	}
	if VerifyAuth(key, in, "") {
		t.Fatalf("VerifyAuth accepted empty auth")
	}
}

// -----------------------------------------------------------
// ValidSlotID / VerifyTimestampWithin
// -----------------------------------------------------------

func TestValidSlotID(t *testing.T) {
	good := []string{
		"blackbeard-3050", "alice", "BOB.42", "x_y_z", strings.Repeat("a", 64),
	}
	for _, s := range good {
		if !ValidSlotID(s) {
			t.Errorf("ValidSlotID(%q) = false, want true", s)
		}
	}
	bad := []string{
		"", "with space", "with/slash", "emoji-🎉", strings.Repeat("a", 65),
	}
	for _, s := range bad {
		if ValidSlotID(s) {
			t.Errorf("ValidSlotID(%q) = true, want false", s)
		}
	}
}

func TestVerifyTimestampWithin(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	if err := VerifyTimestampWithin(1_700_000_000, now); err != nil {
		t.Fatalf("equal ts rejected: %v", err)
	}
	if err := VerifyTimestampWithin(1_700_000_000-30, now); err != nil {
		t.Fatalf("-30s rejected: %v", err)
	}
	if err := VerifyTimestampWithin(1_700_000_000+30, now); err != nil {
		t.Fatalf("+30s rejected: %v", err)
	}
	if err := VerifyTimestampWithin(1_700_000_000-3600, now); err == nil {
		t.Fatalf("-1h accepted")
	}
	if err := VerifyTimestampWithin(1_700_000_000+3600, now); err == nil {
		t.Fatalf("+1h accepted")
	}
}

// -----------------------------------------------------------
// Registry
// -----------------------------------------------------------

func TestRegistry_RegisterLookupDeregister(t *testing.T) {
	var reg Registry
	if _, ok := reg.Lookup("unknown"); ok {
		t.Fatalf("lookup of unknown slot returned true")
	}
	sess := newFakeSession(t)
	if err := reg.Register("alice", "signer-a", "1.2.3.4", "note", sess); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := reg.Lookup("alice")
	if !ok || got != sess {
		t.Fatalf("Lookup returned (%v, %v)", got, ok)
	}
	// Duplicate register replaces the old authenticated session. This
	// keeps NAT-bound clients from getting stuck behind a stale relay-side
	// session after a reconnect.
	next := newFakeSession(t)
	if err := reg.Register("alice", "x", "y", "z", next); err != nil {
		t.Fatalf("replacement Register: %v", err)
	}
	got, ok = reg.Lookup("alice")
	if !ok || got != next {
		t.Fatalf("Lookup after replacement returned (%v, %v)", got, ok)
	}
	select {
	case <-sess.CloseChan():
	case <-time.After(time.Second):
		t.Fatalf("replaced session was not closed")
	}
	reg.Deregister("alice", next)
	if _, ok := reg.Lookup("alice"); ok {
		t.Fatalf("post-deregister lookup returned true")
	}
	// Deregister with wrong session pointer is a no-op
	// (protects against re-connect race).
	reg.Register("alice", "s", "ip", "n", sess)
	reg.Deregister("alice", newFakeSession(t))
	if _, ok := reg.Lookup("alice"); !ok {
		t.Fatalf("Deregister(wrong-pointer) removed the slot")
	}
}

func TestRegistry_Snapshot(t *testing.T) {
	var reg Registry
	reg.Register("a", "sa", "1.1.1.1", "alice", newFakeSession(t))
	reg.Register("b", "sb", "2.2.2.2", "bob", newFakeSession(t))
	snap := reg.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("snapshot len = %d want 2", len(snap))
	}
	seen := map[string]SlotEvent{}
	for _, e := range snap {
		if !e.Connected {
			t.Errorf("snapshot entry %+v has Connected=false", e)
		}
		seen[e.SlotID] = e
	}
	if seen["a"].SignerID != "sa" || seen["b"].SignerID != "sb" {
		t.Fatalf("snapshot signer mapping wrong: %+v", seen)
	}
}

func TestRegistry_ObserverFires(t *testing.T) {
	var reg Registry
	var mu sync.Mutex
	var events []SlotEvent
	reg.SetObserver(func(e SlotEvent) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})
	sess := newFakeSession(t)
	reg.Register("alice", "s", "ip", "n", sess)
	reg.Deregister("alice", sess)
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 {
		t.Fatalf("got %d events want 2: %+v", len(events), events)
	}
	if !events[0].Connected {
		t.Errorf("first event not Connected=true: %+v", events[0])
	}
	if events[1].Connected {
		t.Errorf("second event not Connected=false: %+v", events[1])
	}
}

// -----------------------------------------------------------
// AuthMap
// -----------------------------------------------------------

func TestAuthMap_Lookup(t *testing.T) {
	key := mustKey(t)
	m := AuthMap{
		"slot-a": {Key: key, Note: "alice"},
	}
	got, note, ok := m.Lookup("slot-a")
	if !ok || string(got) != string(key) || note != "alice" {
		t.Fatalf("Lookup(slot-a) = (%x, %q, %v)", got, note, ok)
	}
	if _, _, ok := m.Lookup("slot-z"); ok {
		t.Fatalf("Lookup of unknown slot returned true")
	}
}

// -----------------------------------------------------------
// HandleUpgrade — auth rejection paths
// -----------------------------------------------------------

func TestHandleUpgrade_RejectsWrongMethod(t *testing.T) {
	var reg Registry
	h := HandleUpgrade(&reg, AuthMap{}, nil)
	r := httptest.NewRequest(http.MethodPost, TunnelEndpoint, nil)
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d want 405", w.Code)
	}
}

func TestHandleUpgrade_RejectsMissingUpgrade(t *testing.T) {
	var reg Registry
	h := HandleUpgrade(&reg, AuthMap{}, nil)
	r := httptest.NewRequest(http.MethodGet, TunnelEndpoint, nil)
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d want 400", w.Code)
	}
}

func TestHandleUpgrade_RejectsBadSlot(t *testing.T) {
	var reg Registry
	h := HandleUpgrade(&reg, AuthMap{}, nil)
	r := httptest.NewRequest(http.MethodGet, TunnelEndpoint, nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", UpgradeProtocol)
	r.Header.Set(HeaderSlotID, "bad/slot")
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d want 400", w.Code)
	}
}

func TestHandleUpgrade_RejectsBadAuth(t *testing.T) {
	key := mustKey(t)
	var reg Registry
	h := HandleUpgrade(&reg, AuthMap{"slot": {Key: key}}, nil)
	r := httptest.NewRequest(http.MethodGet, TunnelEndpoint, nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", UpgradeProtocol)
	r.Header.Set(HeaderSlotID, "slot")
	r.Header.Set(HeaderSignerID, "attester-x")
	r.Header.Set(HeaderTimestamp, fmt.Sprintf("%d", time.Now().Unix()))
	r.Header.Set(HeaderAuth, "deadbeef") // wrong
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d want 401", w.Code)
	}
}

func TestHandleUpgrade_RejectsUnknownSlot(t *testing.T) {
	key := mustKey(t)
	var reg Registry
	h := HandleUpgrade(&reg, AuthMap{"known": {Key: key}}, nil)
	now := time.Now().Unix()
	in := AuthInputs{Version: UpgradeProtocol, SlotID: "unknown", SignerID: "s", Timestamp: now}
	good, _ := SignAuth(key, in)
	r := httptest.NewRequest(http.MethodGet, TunnelEndpoint, nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", UpgradeProtocol)
	r.Header.Set(HeaderSlotID, "unknown")
	r.Header.Set(HeaderSignerID, "s")
	r.Header.Set(HeaderTimestamp, fmt.Sprintf("%d", now))
	r.Header.Set(HeaderAuth, good)
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d want 401", w.Code)
	}
}

// -----------------------------------------------------------
// Full end-to-end: tunnel client + relay registry +
// HandleProxy round-trip an HTTP request through the tunnel
// -----------------------------------------------------------

// TestRoundTrip_RealTunnel spins up a real net.Listen-backed
// relay (HandleUpgrade + HandleProxy) AND a real tunnel
// client (Client.Run), then makes an HTTP request through the
// proxy and asserts the response comes back via the tunnel.
//
// This covers: handshake, hijack, yamux multiplexing, proxy
// path stripping, and concurrent stream handling.
func TestRoundTrip_RealTunnel(t *testing.T) {
	key := mustKey(t)
	slot := "alice-3050"

	var reg Registry
	auth := AuthMap{slot: {Key: key, Note: "alice"}}

	// --- Relay-side wiring ---
	upgradeMux := http.NewServeMux()
	upgradeMux.HandleFunc(TunnelEndpoint, HandleUpgrade(&reg, auth, t.Logf))
	relayUpgrade := httptest.NewServer(upgradeMux)
	t.Cleanup(relayUpgrade.Close)

	proxyMux := http.NewServeMux()
	proxyMux.HandleFunc("/", HandleProxy(&reg, t.Logf))
	relayProxy := httptest.NewServer(proxyMux)
	t.Cleanup(relayProxy.Close)

	// --- Attester-side: a tiny app handler we expect the
	// miner to reach via the tunnel. ---
	attesterMux := http.NewServeMux()
	attesterMux.HandleFunc("/api/v1/mining/challenge", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"signer_id":"attester-via-tunnel"}`)
	})
	attesterMux.HandleFunc("/echo/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "echo:"+r.URL.Path)
	})

	cli := &Client{
		RelayURL: relayUpgrade.URL,
		SlotID:   slot,
		SignerID: "attester-end2end",
		Key:      key,
		Handler:  attesterMux,
		Logf:     t.Logf,
	}
	if err := cli.validate(); err != nil {
		t.Fatalf("Client.validate: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	clientDone := make(chan error, 1)
	go func() { clientDone <- cli.Run(ctx) }()

	// Wait for the tunnel to register on the relay before
	// we issue the proxy request. The registry is the
	// authoritative readiness signal.
	deadline := time.After(5 * time.Second)
	for {
		if _, ok := reg.Lookup(slot); ok {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("tunnel did not register slot %q within 5s", slot)
		case <-time.After(20 * time.Millisecond):
		}
	}

	// --- Make a miner request through the proxy ---
	miner := &http.Client{Timeout: 5 * time.Second}
	resp, err := miner.Get(relayProxy.URL + "/" + slot + "/api/v1/mining/challenge")
	if err != nil {
		t.Fatalf("miner GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("miner GET status %d: %s", resp.StatusCode, body)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != `{"signer_id":"attester-via-tunnel"}` {
		t.Fatalf("body mismatch: %s", body)
	}

	// --- Concurrent requests prove yamux multiplexing ---
	const N = 8
	var wg sync.WaitGroup
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r2, err := miner.Get(relayProxy.URL + "/" + slot + "/echo/" + fmt.Sprintf("path%d", i))
			if err != nil {
				errs <- fmt.Errorf("req %d: %w", i, err)
				return
			}
			defer r2.Body.Close()
			b, _ := io.ReadAll(r2.Body)
			want := fmt.Sprintf("echo:/echo/path%d", i)
			if string(b) != want {
				errs <- fmt.Errorf("req %d body=%q want %q", i, b, want)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("concurrent: %v", e)
	}

	// --- Cancel cleanly; client.Run should return nil ---
	cancel()
	select {
	case err := <-clientDone:
		if err != nil {
			t.Fatalf("client.Run(cancelled) returned %v want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("client.Run did not return after ctx cancel")
	}

	if _, ok := reg.Lookup(slot); ok {
		// Allow brief debounce; deregister fires after
		// session close which is async with cancel.
		for i := 0; i < 50; i++ {
			if _, ok := reg.Lookup(slot); !ok {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("slot %q still registered after client shutdown", slot)
	}
}

func TestRoundTrip_ProxyReturns502WhenSlotMissing(t *testing.T) {
	var reg Registry
	mux := http.NewServeMux()
	mux.HandleFunc("/", HandleProxy(&reg, nil))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/missing-slot/api/v1/mining/challenge")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d want 502", resp.StatusCode)
	}
}

func TestRoundTrip_ProxyRejectsBadPath(t *testing.T) {
	var reg Registry
	mux := http.NewServeMux()
	mux.HandleFunc("/", HandleProxy(&reg, nil))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/onlyslotnoslash")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d want 400", resp.StatusCode)
	}
}

// -----------------------------------------------------------
// Helpers
// -----------------------------------------------------------

// newFakeSession constructs a real *yamux.Session over a
// net.Pipe pair. The pair stays alive (and the goroutine
// pumping the other side does too) until t.Cleanup runs;
// pointer-identity tests on Registry don't need actual
// stream traffic, just a unique pointer per call.
func newFakeSession(t *testing.T) *yamux.Session {
	t.Helper()
	a, b := net.Pipe()
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = io.Discard
	sess, err := yamux.Server(a, cfg, nil)
	if err != nil {
		t.Fatalf("yamux.Server: %v", err)
	}
	// Drain the other half so neither side blocks. We close
	// in cleanup; if the test deregisters, we still want
	// goroutines to exit cleanly.
	go io.Copy(io.Discard, b)
	t.Cleanup(func() {
		_ = sess.Close()
		_ = a.Close()
		_ = b.Close()
	})
	return sess
}

// silence unused-warning for errors imported above (used
// indirectly through some sub-tests).
var _ = errors.New
