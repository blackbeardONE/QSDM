package api

import (
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// TokenRevocationStore maintains the set of tokens that have been
// explicitly invalidated before their natural expiry. The store is keyed
// by the token's per-issue nonce (Claims.Nonce) because the nonce is
// the only field guaranteed unique across the lifetime of a single user
// (the user_id + issued_at pair could collide on clocks with poor
// resolution; the 256-bit random nonce cannot).
//
// Each entry carries the original token's ExpiresAt; once the wall clock
// passes that timestamp the entry is removed by the background sweeper.
// This bounds the store to (revocations per token-TTL) regardless of how
// many logouts have happened total — a year-long server can still answer
// "is this nonce revoked?" in O(1) without unbounded memory growth.
type TokenRevocationStore struct {
	mu      sync.RWMutex
	entries map[string]time.Time // nonce -> expiresAt

	cleanupInterval time.Duration
	stopCh          chan struct{}
	stopOnce        sync.Once
}

// NewTokenRevocationStore creates a new store and starts the background
// cleanup goroutine. Call Stop() when the store is no longer needed
// (tests or graceful shutdown).
func NewTokenRevocationStore() *TokenRevocationStore {
	s := &TokenRevocationStore{
		entries:         make(map[string]time.Time),
		cleanupInterval: 1 * time.Minute,
		stopCh:          make(chan struct{}),
	}
	go s.runCleanup()
	return s
}

// Stop terminates the background cleanup goroutine. Safe to call
// multiple times.
func (s *TokenRevocationStore) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}

func (s *TokenRevocationStore) runCleanup() {
	t := time.NewTicker(s.cleanupInterval)
	defer t.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-t.C:
			s.sweep()
		}
	}
}

func (s *TokenRevocationStore) sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for nonce, exp := range s.entries {
		if now.After(exp) {
			delete(s.entries, nonce)
		}
	}
}

// Revoke records the token claims as revoked. expiresAt is the natural
// expiry of the JWT — we keep the revocation entry until then, after
// which the token would be rejected on expiry anyway and the entry can
// be safely dropped.
func (s *TokenRevocationStore) Revoke(nonce string, expiresAt time.Time) {
	if nonce == "" {
		return
	}
	s.mu.Lock()
	s.entries[nonce] = expiresAt
	s.mu.Unlock()
	monitoring.RecordTokenRevocation()
}

// IsRevoked reports whether the given nonce has been revoked AND has not
// yet expired. Callers (ValidateToken) treat a true result as a hard
// rejection.
func (s *TokenRevocationStore) IsRevoked(nonce string) bool {
	if nonce == "" {
		return false
	}
	s.mu.RLock()
	exp, present := s.entries[nonce]
	s.mu.RUnlock()
	if !present {
		return false
	}
	if time.Now().After(exp) {
		// Stale entry — race with the sweeper. Treat as not revoked
		// because the token has already expired naturally and a fresh
		// login would mint a different nonce.
		return false
	}
	monitoring.RecordTokenRevokedHit()
	return true
}

// Size returns the current number of live revocation entries. Used by
// the dashboard and tests; not part of the hot path.
func (s *TokenRevocationStore) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}
