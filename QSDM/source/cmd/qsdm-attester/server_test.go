package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
)

// buildTestServer wires a fully-populated *Server backed by a
// freshly-minted in-memory key. Returned alongside the matching
// HMACSignerVerifier so tests can verify a returned Challenge
// out-of-band.
func buildTestServer(t *testing.T) (*Server, *challenge.HMACSignerVerifier, []byte, string) {
	t.Helper()
	key := make([]byte, signerKeyLen)
	for i := range key {
		key[i] = byte(i + 1)
	}
	id := resolveSignerID(key, "")
	signer, err := challenge.NewHMACSigner(id, key)
	if err != nil {
		t.Fatalf("NewHMACSigner: %v", err)
	}
	issuer, err := challenge.NewIssuer(signer)
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	cfg := &Config{
		ListenAddr: ":0",
		KeyPath:    "/tmp/unused",
		Note:       "test-attester",
	}
	if defErr := cfg.defaults(); defErr != nil {
		t.Fatalf("cfg.defaults: %v", defErr)
	}
	srv, err := NewServer(cfg, issuer, signer, keyFingerprint(key))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	verifier := challenge.NewHMACSignerVerifier()
	if regErr := verifier.Register(id, key); regErr != nil {
		t.Fatalf("verifier.Register: %v", regErr)
	}
	return srv, verifier, key, id
}

// TestChallenge_MiningPathAlias asserts the wire-compatible
// /api/v1/mining/challenge path returns a body byte-identical
// to the short /api/v1/challenge form. Miners' existing
// FetchChallenge code path appends "/api/v1/mining/challenge"
// to whatever URL it is given, so a missing alias would silently
// 404 on every miner request to a peer attester.
//
// We don't need the verifier here because the
// TestChallenge_RoundTripsThroughHMACVerifier test below
// already asserts signature validity via the short URL — the
// same handler serves both URLs, so signature validity is
// inherited.
func TestChallenge_MiningPathAlias(t *testing.T) {
	srv, _, _, id := buildTestServer(t)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/api/v1/mining/challenge")
	if err != nil {
		t.Fatalf("GET /api/v1/mining/challenge: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d want 200 from /api/v1/mining/challenge", resp.StatusCode)
	}
	var wire api.ChallengeWire
	if err := json.NewDecoder(resp.Body).Decode(&wire); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if wire.SignerID != id {
		t.Fatalf("signer_id %q want %q", wire.SignerID, id)
	}
	if wire.Nonce == "" || wire.Signature == "" {
		t.Fatalf("alias returned an empty challenge: %+v", wire)
	}
}

func TestChallenge_RoundTripsThroughHMACVerifier(t *testing.T) {
	srv, verifier, _, id := buildTestServer(t)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/api/v1/challenge")
	if err != nil {
		t.Fatalf("GET /api/v1/challenge: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q want no-store", got)
	}
	var wire api.ChallengeWire
	if err := json.NewDecoder(resp.Body).Decode(&wire); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if wire.SignerID != id {
		t.Fatalf("signer_id %q want %q", wire.SignerID, id)
	}
	if len(wire.Nonce) != 64 {
		t.Fatalf("nonce hex length %d want 64", len(wire.Nonce))
	}
	if len(wire.Signature) != 64 {
		t.Fatalf("signature hex length %d want 64", len(wire.Signature))
	}
	c, err := decodeChallengeWire(wire)
	if err != nil {
		t.Fatalf("decodeChallengeWire: %v", err)
	}
	now := time.Now().Unix()
	if vErr := c.Verify(now, c.IssuedAt, 60, 5, verifier); vErr != nil {
		t.Fatalf("challenge.Verify against allowlisted signer: %v", vErr)
	}
}

func TestChallenge_DistinctNoncesAcrossCalls(t *testing.T) {
	srv, _, _, _ := buildTestServer(t)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	const n = 10
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		resp, err := http.Get(ts.URL + "/api/v1/challenge")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		var w api.ChallengeWire
		if err := json.NewDecoder(resp.Body).Decode(&w); err != nil {
			resp.Body.Close()
			t.Fatalf("decode call %d: %v", i, err)
		}
		resp.Body.Close()
		if _, exists := seen[w.Nonce]; exists {
			t.Fatalf("duplicate nonce on call %d: %s", i, w.Nonce)
		}
		seen[w.Nonce] = struct{}{}
	}
}

func TestChallenge_RejectsNonGET(t *testing.T) {
	srv, _, _, _ := buildTestServer(t)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	resp, err := http.Post(ts.URL+"/api/v1/challenge", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status %d want 405", resp.StatusCode)
	}
	if got := resp.Header.Get("Allow"); got != http.MethodGet {
		t.Fatalf("Allow = %q want GET", got)
	}
}

func TestHealthz_AlwaysReturnsOK(t *testing.T) {
	srv, _, _, _ := buildTestServer(t)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), `"status":"ok"`) {
		t.Fatalf("body = %q missing status:ok", body)
	}
}

func TestInfo_ContainsSignerIDAndFingerprint(t *testing.T) {
	srv, _, key, id := buildTestServer(t)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/info")
	if err != nil {
		t.Fatalf("GET /info: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d want 200", resp.StatusCode)
	}
	var info InfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.SignerID != id {
		t.Fatalf("info.SignerID = %q want %q", info.SignerID, id)
	}
	if info.KeyFingerprint != keyFingerprint(key) {
		t.Fatalf("KeyFingerprint mismatch: %q vs %q", info.KeyFingerprint, keyFingerprint(key))
	}
	if info.Note == "" {
		t.Fatalf("Note is empty; should default to test-attester")
	}
}

func TestInfo_DoesNotLeakKeyBytes(t *testing.T) {
	srv, _, key, _ := buildTestServer(t)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/info")
	if err != nil {
		t.Fatalf("GET /info: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), hexKey(key)) {
		t.Fatalf("/info response leaks raw key bytes")
	}
}

func TestMetrics_IncrementsAfterIssue(t *testing.T) {
	srv, _, _, _ := buildTestServer(t)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	for i := 0; i < 3; i++ {
		resp, err := http.Get(ts.URL + "/api/v1/challenge")
		if err != nil {
			t.Fatalf("issue %d: %v", i, err)
		}
		resp.Body.Close()
	}

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "qsdm_attester_issued_total{") {
		t.Fatalf("metrics missing issued_total counter")
	}
	if !strings.Contains(string(body), `} 3`) {
		t.Fatalf("metrics did not record 3 issuances; body:\n%s", body)
	}
	if !strings.Contains(string(body), "qsdm_attester_uptime_seconds{") {
		t.Fatalf("metrics missing uptime gauge")
	}
}

func TestNotFound_OnUnknownRoute(t *testing.T) {
	srv, _, _, _ := buildTestServer(t)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/nope")
	if err != nil {
		t.Fatalf("GET /nope: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d want 404", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"error":"not_found"`) {
		t.Fatalf("body %q missing not_found", body)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("expected JSON content type")
	}
}

func TestRoot_ReturnsServiceDescriptor(t *testing.T) {
	srv, _, _, _ := buildTestServer(t)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("root status %d want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"service":"qsdm-attester"`) {
		t.Fatalf("root body missing service tag: %s", body)
	}
}

func TestIssuanceLogger_FiresAtConfiguredCadence(t *testing.T) {
	srv, _, _, _ := buildTestServer(t)
	srv.logEvery = 2
	var fired atomic.Uint64
	var lastSnap LogIssuance
	srv.SetIssuanceLogger(func(snap LogIssuance) {
		fired.Add(1)
		lastSnap = snap
	})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	for i := 0; i < 5; i++ {
		resp, err := http.Get(ts.URL + "/api/v1/challenge")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		resp.Body.Close()
	}
	// 5 issuances with logEvery=2 → fires on 2 and 4 → 2 events.
	if got := fired.Load(); got != 2 {
		t.Fatalf("logger fired %d times want 2", got)
	}
	if lastSnap.SignerID == "" {
		t.Fatalf("last logged snapshot has empty signer_id")
	}
	if lastSnap.NonceHex == "" {
		t.Fatalf("last logged snapshot has empty NonceHex")
	}
}

func TestRun_ShutsDownOnContextCancel(t *testing.T) {
	srv, _, _, _ := buildTestServer(t)
	// Pin to a free local port.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	srv.cfg.ListenAddr = addr

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- srv.Run(ctx, func(string, ...any) {})
	}()
	// Give the server a beat to start.
	time.Sleep(100 * time.Millisecond)
	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("healthz: %v", err)
	}
	resp.Body.Close()
	cancel()
	select {
	case err := <-doneCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(7 * time.Second):
		t.Fatalf("Run did not return after ctx cancel")
	}
}

// decodeChallengeWire is a test-only helper that lifts an
// api.ChallengeWire back into the strongly-typed
// challenge.Challenge so the test can verify the signature
// against an HMACSignerVerifier. We do NOT export this from
// the binary because the wire-to-core direction is the miner's
// job, not the issuer's.
func decodeChallengeWire(w api.ChallengeWire) (challenge.Challenge, error) {
	c := challenge.Challenge{
		IssuedAt: w.IssuedAt,
		SignerID: w.SignerID,
	}
	if len(w.Nonce) != 64 {
		return c, errors.New("nonce hex wrong length")
	}
	if _, err := hexDecodeInto(c.Nonce[:], w.Nonce); err != nil {
		return c, err
	}
	sig, err := hexDecode(w.Signature)
	if err != nil {
		return c, err
	}
	c.Signature = sig
	return c, nil
}

func hexDecode(s string) ([]byte, error) {
	out := make([]byte, len(s)/2)
	if _, err := hexDecodeInto(out, s); err != nil {
		return nil, err
	}
	return out, nil
}

func hexDecodeInto(dst []byte, s string) (int, error) {
	if len(s)%2 != 0 {
		return 0, errors.New("hex length odd")
	}
	for i := 0; i < len(s); i += 2 {
		hi, err := hexDigit(s[i])
		if err != nil {
			return i / 2, err
		}
		lo, err := hexDigit(s[i+1])
		if err != nil {
			return i / 2, err
		}
		dst[i/2] = hi<<4 | lo
	}
	return len(s) / 2, nil
}

func hexDigit(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, nil
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, nil
	default:
		return 0, errors.New("invalid hex digit")
	}
}
