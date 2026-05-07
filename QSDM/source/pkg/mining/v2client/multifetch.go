package v2client

// MultiFetchChallenge: round-robin / failover wrapper around
// FetchChallenge. Lets a miner pull v2 challenges from any of N
// endpoints (validator + zero-or-more peer attesters) so the
// mining loop survives a transient outage at any single endpoint
// AND so operators can choose to spread their challenge-fetch
// load across geographically-closer attesters.
//
// Why this lives here (not in cmd/qsdmminer-console): every
// future miner binary needs the same multi-endpoint failover, so
// hosting the fetcher in pkg/mining/v2client lets cmd/qsdmminer
// (the daemon) and any planned native-NVIDIA miner share one
// well-tested implementation.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
)

// ChallengeFetcher is the narrow contract miner code uses for
// challenge retrieval. *MultiFetcher (multi-URL with failover)
// and *singleFetcher (one URL) both satisfy it; tests can
// inject their own without spinning up an HTTP server.
type ChallengeFetcher interface {
	Fetch(ctx context.Context) (challenge.Challenge, error)
}

// MultiFetcher rotates through a list of base URLs on each
// Fetch call, falling through to the next URL on any error
// from the previous. The rotation is round-robin: call N starts
// at URLs[N mod len(URLs)] and proceeds in order, returning the
// first successful challenge. This both load-balances normal
// traffic across endpoints AND tolerates a single endpoint
// going dark for the duration of its retention.
//
// Failover is on ANY error from FetchChallenge: network errors,
// non-200 statuses, malformed JSON, and protocol-level
// rejections (bad signer_id, etc.). The miner cannot
// distinguish "endpoint is genuinely down" from "endpoint is
// returning bad data", and in both cases the right response is
// "try the next one".
//
// Safe for concurrent use: the round-robin counter is atomic,
// and *http.Client is documented as goroutine-safe.
type MultiFetcher struct {
	client *http.Client
	urls   []string
	cursor atomic.Uint64

	// observers, if set, receive a callback after every
	// per-endpoint attempt — successful or failed. Used by
	// the miner UI to surface "challenge endpoint X failed,
	// fell back to Y" without parsing log strings. The
	// observer slice is fixed at construction time so no
	// mutex is needed during the hot path.
	observers []FetchObserver

	// mu guards observers list mutation post-construction.
	// Today no caller mutates after New, but exposing
	// AddObserver in the future is a natural extension and
	// the lock costs nothing in the normal path.
	mu sync.Mutex
}

// FetchObserver receives one event per endpoint attempt during
// a Fetch call. Implementations MUST be non-blocking — the
// miner's hot path waits on Fetch, and a slow observer would
// stall block production.
type FetchObserver interface {
	OnFetchAttempt(url string, err error)
}

// FetchObserverFunc is a convenience type letting plain
// functions satisfy FetchObserver without a wrapper struct.
type FetchObserverFunc func(url string, err error)

// OnFetchAttempt implements FetchObserver.
func (f FetchObserverFunc) OnFetchAttempt(url string, err error) { f(url, err) }

// NewMultiFetcher constructs a MultiFetcher. Returns an error
// if urls is empty or any URL is empty after trimming. Trims
// trailing slashes the same way the rest of the miner does so
// "https://api.qsdm.tech/" and "https://api.qsdm.tech" produce
// byte-identical request URLs downstream.
func NewMultiFetcher(client *http.Client, urls []string, observers ...FetchObserver) (*MultiFetcher, error) {
	if client == nil {
		return nil, errors.New("v2client: NewMultiFetcher requires non-nil http client")
	}
	cleaned := make([]string, 0, len(urls))
	for _, u := range urls {
		u = strings.TrimRight(strings.TrimSpace(u), "/")
		if u == "" {
			continue
		}
		cleaned = append(cleaned, u)
	}
	if len(cleaned) == 0 {
		return nil, errors.New("v2client: NewMultiFetcher requires at least one non-empty URL")
	}
	return &MultiFetcher{
		client:    client,
		urls:      cleaned,
		observers: append([]FetchObserver(nil), observers...),
	}, nil
}

// URLs returns a defensive copy of the configured endpoints.
// Useful for tests + the miner UI's "active endpoints" line.
func (f *MultiFetcher) URLs() []string {
	out := make([]string, len(f.urls))
	copy(out, f.urls)
	return out
}

// Fetch attempts each endpoint starting at the round-robin
// cursor and returns the first successful challenge. On
// failure across ALL endpoints, returns an error wrapping every
// per-endpoint error so the operator can diagnose multi-
// endpoint outages.
func (f *MultiFetcher) Fetch(ctx context.Context) (challenge.Challenge, error) {
	if ctx == nil {
		return challenge.Challenge{}, errors.New("v2client: Fetch requires non-nil context")
	}
	start := f.cursor.Add(1) - 1
	n := uint64(len(f.urls))
	errs := make([]error, 0, n)
	for i := uint64(0); i < n; i++ {
		// Bail out early if the caller's context already
		// expired. Important on short-deadline ctxs because
		// we'd otherwise burn through retries the caller has
		// already given up on.
		if err := ctx.Err(); err != nil {
			errs = append(errs, fmt.Errorf("v2client: ctx cancelled mid-rotation: %w", err))
			break
		}
		idx := (start + i) % n
		url := f.urls[idx]
		c, err := FetchChallenge(ctx, f.client, url)
		f.notifyObservers(url, err)
		if err == nil {
			return c, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", url, err))
	}
	return challenge.Challenge{}, fmt.Errorf("v2client: all %d challenge endpoint(s) failed: %w",
		len(f.urls), errors.Join(errs...))
}

func (f *MultiFetcher) notifyObservers(url string, err error) {
	f.mu.Lock()
	obs := append([]FetchObserver(nil), f.observers...)
	f.mu.Unlock()
	for _, o := range obs {
		if o == nil {
			continue
		}
		o.OnFetchAttempt(url, err)
	}
}

// SingleURLFetcher returns a ChallengeFetcher backed by exactly
// one URL — useful for tests and for the legacy single-validator
// miner posture. Implemented as a one-element MultiFetcher so
// there is exactly one Fetch path in production.
func SingleURLFetcher(client *http.Client, url string) (ChallengeFetcher, error) {
	return NewMultiFetcher(client, []string{url})
}

// compile-time check.
var _ ChallengeFetcher = (*MultiFetcher)(nil)
