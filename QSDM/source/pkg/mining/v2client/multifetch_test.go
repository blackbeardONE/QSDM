package v2client

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
)

// makeFakeChallenge creates a deterministic challengeWire
// payload for tests. tag varies per server so the test can
// assert which endpoint a successful Fetch came from.
func makeFakeChallenge(tag string) []byte {
	var nonce [32]byte
	for i := range nonce {
		nonce[i] = byte(i + len(tag))
	}
	w := challengeWire{
		Nonce:     hex.EncodeToString(nonce[:]),
		IssuedAt:  time.Now().Unix(),
		SignerID:  "attester-" + tag,
		Signature: hex.EncodeToString([]byte("fake-signature-bytes-must-be-non-empty")),
	}
	out, _ := json.Marshal(w)
	return out
}

func startFakeIssuer(t *testing.T, tag string) (string, *atomic.Uint64) {
	t.Helper()
	var hits atomic.Uint64
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/mining/challenge", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(makeFakeChallenge(tag))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL, &hits
}

func startFailingIssuer(t *testing.T, status int) (string, *atomic.Uint64) {
	t.Helper()
	var hits atomic.Uint64
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/mining/challenge", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "synthetic failure", status)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL, &hits
}

func TestNewMultiFetcher_RejectsEmpty(t *testing.T) {
	if _, err := NewMultiFetcher(http.DefaultClient, nil); err == nil {
		t.Fatalf("expected error for empty URLs")
	}
	if _, err := NewMultiFetcher(http.DefaultClient, []string{"  "}); err == nil {
		t.Fatalf("expected error for whitespace-only URLs")
	}
	if _, err := NewMultiFetcher(nil, []string{"http://x"}); err == nil {
		t.Fatalf("expected error for nil client")
	}
}

func TestNewMultiFetcher_TrimsTrailingSlashes(t *testing.T) {
	mf, err := NewMultiFetcher(http.DefaultClient, []string{"http://a/", "http://b//", "http://c"})
	if err != nil {
		t.Fatalf("NewMultiFetcher: %v", err)
	}
	urls := mf.URLs()
	// strings.TrimRight strips ALL trailing '/', so both
	// "http://b//" and "http://b/" collapse to "http://b".
	want := []string{"http://a", "http://b", "http://c"}
	if len(urls) != 3 {
		t.Fatalf("urls = %v want 3 items", urls)
	}
	for i := range want {
		if urls[i] != want[i] {
			t.Fatalf("urls[%d] = %q want %q", i, urls[i], want[i])
		}
	}
}

func TestMultiFetch_FirstSucceeds(t *testing.T) {
	urlA, hitsA := startFakeIssuer(t, "alpha")
	urlB, hitsB := startFakeIssuer(t, "beta")
	mf, err := NewMultiFetcher(http.DefaultClient, []string{urlA, urlB})
	if err != nil {
		t.Fatalf("NewMultiFetcher: %v", err)
	}
	ctx := context.Background()
	c, err := mf.Fetch(ctx)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if c.SignerID != "attester-alpha" {
		t.Fatalf("first Fetch SignerID = %q want attester-alpha", c.SignerID)
	}
	if hitsA.Load() != 1 || hitsB.Load() != 0 {
		t.Fatalf("hits A=%d B=%d want A=1 B=0", hitsA.Load(), hitsB.Load())
	}
}

func TestMultiFetch_FirstFailsSecondSucceeds(t *testing.T) {
	urlBad, hitsBad := startFailingIssuer(t, http.StatusServiceUnavailable)
	urlGood, hitsGood := startFakeIssuer(t, "rescue")
	mf, err := NewMultiFetcher(http.DefaultClient, []string{urlBad, urlGood})
	if err != nil {
		t.Fatalf("NewMultiFetcher: %v", err)
	}
	c, err := mf.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if c.SignerID != "attester-rescue" {
		t.Fatalf("SignerID = %q want attester-rescue", c.SignerID)
	}
	if hitsBad.Load() != 1 {
		t.Fatalf("expected 1 hit on bad endpoint, got %d", hitsBad.Load())
	}
	if hitsGood.Load() != 1 {
		t.Fatalf("expected 1 hit on good endpoint, got %d", hitsGood.Load())
	}
}

func TestMultiFetch_AllFailReturnsAggregateError(t *testing.T) {
	urlA, hitsA := startFailingIssuer(t, http.StatusServiceUnavailable)
	urlB, hitsB := startFailingIssuer(t, http.StatusInternalServerError)
	mf, err := NewMultiFetcher(http.DefaultClient, []string{urlA, urlB})
	if err != nil {
		t.Fatalf("NewMultiFetcher: %v", err)
	}
	_, fetchErr := mf.Fetch(context.Background())
	if fetchErr == nil {
		t.Fatalf("expected error when all endpoints fail")
	}
	if !strings.Contains(fetchErr.Error(), "all 2 challenge endpoint(s) failed") {
		t.Fatalf("error %q missing aggregate prefix", fetchErr)
	}
	if !strings.Contains(fetchErr.Error(), urlA) {
		t.Fatalf("error %q missing first URL", fetchErr)
	}
	if !strings.Contains(fetchErr.Error(), urlB) {
		t.Fatalf("error %q missing second URL", fetchErr)
	}
	if hitsA.Load() != 1 || hitsB.Load() != 1 {
		t.Fatalf("expected each endpoint hit once, got A=%d B=%d", hitsA.Load(), hitsB.Load())
	}
}

func TestMultiFetch_RoundRobinAdvances(t *testing.T) {
	urlA, hitsA := startFakeIssuer(t, "a")
	urlB, hitsB := startFakeIssuer(t, "b")
	urlC, hitsC := startFakeIssuer(t, "c")
	mf, err := NewMultiFetcher(http.DefaultClient, []string{urlA, urlB, urlC})
	if err != nil {
		t.Fatalf("NewMultiFetcher: %v", err)
	}
	ctx := context.Background()
	signers := make([]string, 0, 6)
	for i := 0; i < 6; i++ {
		c, err := mf.Fetch(ctx)
		if err != nil {
			t.Fatalf("Fetch %d: %v", i, err)
		}
		signers = append(signers, c.SignerID)
	}
	// 6 calls, 3 endpoints, round-robin → each endpoint hit
	// exactly twice.
	if hitsA.Load() != 2 || hitsB.Load() != 2 || hitsC.Load() != 2 {
		t.Fatalf("rotation imbalance: A=%d B=%d C=%d (want 2/2/2)",
			hitsA.Load(), hitsB.Load(), hitsC.Load())
	}
	// And the signers we got back must match: a,b,c,a,b,c.
	want := []string{"attester-a", "attester-b", "attester-c", "attester-a", "attester-b", "attester-c"}
	for i, s := range signers {
		if s != want[i] {
			t.Fatalf("signers[%d] = %q want %q (full sequence %v)", i, s, want[i], signers)
		}
	}
}

func TestMultiFetch_ContextCancelledMidRotation(t *testing.T) {
	urlBad, _ := startFailingIssuer(t, http.StatusServiceUnavailable)
	urlGood, _ := startFakeIssuer(t, "never-reached")
	mf, err := NewMultiFetcher(http.DefaultClient, []string{urlBad, urlGood})
	if err != nil {
		t.Fatalf("NewMultiFetcher: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, fetchErr := mf.Fetch(ctx)
	if fetchErr == nil {
		t.Fatalf("expected error on cancelled context")
	}
	// The aggregate error should mention the cancellation.
	if !errors.Is(fetchErr, context.Canceled) && !strings.Contains(fetchErr.Error(), "ctx cancelled") {
		t.Fatalf("error %q does not surface ctx cancellation", fetchErr)
	}
}

func TestMultiFetch_ObserverFiresPerAttempt(t *testing.T) {
	urlBad, _ := startFailingIssuer(t, http.StatusServiceUnavailable)
	urlGood, _ := startFakeIssuer(t, "good")

	type evt struct {
		url string
		ok  bool
	}
	var mu sync.Mutex
	var events []evt
	obs := FetchObserverFunc(func(url string, err error) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, evt{url: url, ok: err == nil})
	})
	mf, err := NewMultiFetcher(http.DefaultClient, []string{urlBad, urlGood}, obs)
	if err != nil {
		t.Fatalf("NewMultiFetcher: %v", err)
	}
	if _, err := mf.Fetch(context.Background()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 {
		t.Fatalf("expected 2 observer events, got %d: %+v", len(events), events)
	}
	if events[0].url != urlBad || events[0].ok {
		t.Fatalf("first event = %+v want url=%s ok=false", events[0], urlBad)
	}
	if events[1].url != urlGood || !events[1].ok {
		t.Fatalf("second event = %+v want url=%s ok=true", events[1], urlGood)
	}
}

func TestSingleURLFetcher_BackwardsCompat(t *testing.T) {
	url, hits := startFakeIssuer(t, "single")
	f, err := SingleURLFetcher(http.DefaultClient, url)
	if err != nil {
		t.Fatalf("SingleURLFetcher: %v", err)
	}
	c, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if c.SignerID != "attester-single" {
		t.Fatalf("SignerID = %q", c.SignerID)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d want 1", hits.Load())
	}
}

func TestMultiFetch_FetchReturnsTypedChallenge(t *testing.T) {
	url, _ := startFakeIssuer(t, "typed")
	mf, err := NewMultiFetcher(http.DefaultClient, []string{url})
	if err != nil {
		t.Fatalf("NewMultiFetcher: %v", err)
	}
	c, err := mf.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	var zero challenge.Challenge
	if c.Nonce == zero.Nonce {
		t.Fatalf("nonce was zero-valued")
	}
	if c.IssuedAt == 0 {
		t.Fatalf("issued_at zero")
	}
}
