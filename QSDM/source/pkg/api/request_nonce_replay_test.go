package api

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
)

// 2026-11-18 report: request signing covered the nonce but never checked it, so a captured signed request could be
// replayed byte for byte for five minutes. A verified nonce is now refused again while its window is open.

func TestRequestSigner_ReplayedNonceIsRefused(t *testing.T) {
	rs, err := NewRequestSigner("nonce-replay-test-secret")
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"symbol":"REPLAY","recipient":"bob","amount":7}`)
	ts := time.Now().Unix()
	sig, err := rs.SignRequest(body, ts, "n-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := rs.VerifyRequest(body, ts, "n-1", sig); err != nil {
		t.Fatalf("first use: %v", err)
	}
	if err := rs.VerifyRequest(body, ts, "n-1", sig); err == nil {
		t.Fatal("the same signed request was accepted twice")
	}
	sig2, _ := rs.SignRequest(body, ts, "n-2")
	if err := rs.VerifyRequest(body, ts, "n-2", sig2); err != nil {
		t.Fatalf("a fresh nonce was refused: %v", err)
	}
	// A request that fails its signature does not use up the nonce.
	if err := rs.VerifyRequest([]byte(`{"tampered":1}`), ts, "n-3", sig2); err == nil {
		t.Fatal("a tampered body verified")
	}
	sig3, _ := rs.SignRequest(body, ts, "n-3")
	if err := rs.VerifyRequest(body, ts, "n-3", sig3); err != nil {
		t.Fatalf("a nonce was burned by a failed request: %v", err)
	}
	empty, _ := rs.SignRequest(body, ts, "")
	if err := rs.VerifyRequest(body, ts, "", empty); err == nil {
		t.Fatal("a request without a nonce was accepted")
	}
}

func TestRequestSigner_NonceExpiresAndStoreIsBounded(t *testing.T) {
	rs := &RequestSigner{}
	now := int64(1_800_000_000)
	if err := rs.useNonce("a", now); err != nil {
		t.Fatal(err)
	}
	if err := rs.useNonce("a", now+2*requestSignatureWindow); err == nil {
		t.Fatal("a nonce was forgotten inside its two windows")
	}
	if err := rs.useNonce("a", now+2*requestSignatureWindow+1); err != nil {
		t.Fatalf("an expired nonce is still refused: %v", err)
	}
	for i := 0; len(rs.nonceSeen) < requestNonceCap; i++ {
		_ = rs.useNonce(fmt.Sprintf("fill-%d", i), now)
	}
	if err := rs.useNonce("one-more", now); err == nil {
		t.Fatal("the store grew past its cap")
	}
	if err := rs.useNonce("after-expiry", now+3*requestSignatureWindow); err != nil {
		t.Fatalf("expired nonces were not pruned when the store was full: %v", err)
	}
	if len(rs.nonceSeen) > requestNonceCap {
		t.Fatalf("store size %d over the cap", len(rs.nonceSeen))
	}
}

// The reporter's case through the middleware: one signed POST, sent twice; the handler runs once.
func TestRequestSigningMiddleware_ReplayRunsHandlerOnce(t *testing.T) {
	rs, err := NewRequestSigner("nonce-replay-test-secret")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusOK) })
	logger := logging.NewLogger("test.log", false)
	h := RequestSigningMiddleware(rs, logger)(next)

	body := []byte(`{"symbol":"REPLAY","recipient":"bob","amount":7}`)
	ts := time.Now().Unix()
	sig, err := rs.SignRequest(body, ts, "mint-1")
	if err != nil {
		t.Fatal(err)
	}
	send := func() int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tokens/mint", bytes.NewReader(body))
		req.Header.Set("X-Timestamp", fmt.Sprintf("%d", ts))
		req.Header.Set("X-Nonce", "mint-1")
		req.Header.Set("X-Signature", sig)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := send(); code != http.StatusOK {
		t.Fatalf("first request: %d", code)
	}
	if code := send(); code != http.StatusUnauthorized {
		t.Fatalf("replayed request: %d, want 401", code)
	}
	if calls != 1 {
		t.Fatalf("handler ran %d times, want 1", calls)
	}
}
