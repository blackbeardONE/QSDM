package monitoring

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

const maxNGCNonceEntries = 512

type ngcNonceEntry struct {
	expires time.Time
	used    bool
}

var (
	ngcNonceMu sync.Mutex
	ngcNonces  map[string]ngcNonceEntry // nonce hex -> entry
)

func init() {
	ngcNonces = make(map[string]ngcNonceEntry)
}

func cleanupExpiredNGCNoncesLocked() {
	now := time.Now().UTC()
	for k, e := range ngcNonces {
		if now.After(e.expires) {
			delete(ngcNonces, k)
		}
	}
	if len(ngcNonces) < maxNGCNonceEntries {
		return
	}
	for k, e := range ngcNonces {
		if e.used {
			delete(ngcNonces, k)
		}
	}
}

// IssueNGCIngestNonce creates a single-use nonce for proof ingest (replay resistance).
func IssueNGCIngestNonce(ttl time.Duration) (nonce string, expiresUnix int64, err error) {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", 0, fmt.Errorf("nonce generation: %w", err)
	}
	n := hex.EncodeToString(b)
	exp := time.Now().UTC().Add(ttl)

	ngcNonceMu.Lock()
	defer ngcNonceMu.Unlock()
	cleanupExpiredNGCNoncesLocked()
	for k, e := range ngcNonces {
		if e.used {
			delete(ngcNonces, k)
		}
	}
	if len(ngcNonces) >= maxNGCNonceEntries {
		return "", 0, fmt.Errorf("ingest nonce pool full; wait for TTL expiry or increase client interval")
	}
	ngcNonces[n] = ngcNonceEntry{expires: exp, used: false}
	return n, exp.Unix(), nil
}

// ValidateAndConsumeNGCIngestNonce returns true if the nonce was issued, not expired, and not yet used; then marks it used.
func ValidateAndConsumeNGCIngestNonce(nonce string) bool {
	if nonce == "" {
		return false
	}
	now := time.Now().UTC()

	ngcNonceMu.Lock()
	defer ngcNonceMu.Unlock()
	cleanupExpiredNGCNoncesLocked()
	e, ok := ngcNonces[nonce]
	if !ok || e.used || now.After(e.expires) {
		if ok {
			delete(ngcNonces, nonce)
		}
		return false
	}
	e.used = true
	ngcNonces[nonce] = e
	return true
}

// ResetNGCIngestNoncesForTest clears the nonce map (tests only).
func ResetNGCIngestNoncesForTest() {
	ngcNonceMu.Lock()
	defer ngcNonceMu.Unlock()
	ngcNonces = make(map[string]ngcNonceEntry)
}

// NGCIngestNoncePoolSize returns the number of tracked ingest nonces (after dropping expired entries).
func NGCIngestNoncePoolSize() int {
	ngcNonceMu.Lock()
	defer ngcNonceMu.Unlock()
	cleanupExpiredNGCNoncesLocked()
	return len(ngcNonces)
}
