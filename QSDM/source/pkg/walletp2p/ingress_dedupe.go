// Package walletp2p shares P2P wallet transaction ingress dedupe across transports:
// legacy pubsub JSON / mesh companion (cmd/qsdm/transaction) and signed gossip (pkg/networking).
package walletp2p

import (
	"encoding/json"
	"strings"
	"sync"
	"time"
)

const ingressDedupeTTL = 24 * time.Hour
const ingressDedupeMaxKeys = 50000

var (
	mu   sync.Mutex
	seen = make(map[string]time.Time, 1024)
)

// ResetForTest clears dedupe state.
func ResetForTest() {
	mu.Lock()
	seen = make(map[string]time.Time, 1024)
	mu.Unlock()
}

// WalletJSONIDFromBytes returns JSON "id" from a wallet-style payload, or "".
func WalletJSONIDFromBytes(b []byte) string {
	var m struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return ""
	}
	return strings.TrimSpace(m.ID)
}

func pruneLocked(now time.Time) {
	for k, exp := range seen {
		if !now.Before(exp) {
			delete(seen, k)
		}
	}
	if len(seen) <= ingressDedupeMaxKeys {
		return
	}
	for k := range seen {
		delete(seen, k)
		if len(seen) <= ingressDedupeMaxKeys/2 {
			break
		}
	}
}

// Reserve returns false if this wallet tx id is already ingested or reserved (duplicate ingress).
func Reserve(id string) bool {
	if id == "" {
		return true
	}
	now := time.Now()
	mu.Lock()
	defer mu.Unlock()
	pruneLocked(now)
	if exp, ok := seen[id]; ok && now.Before(exp) {
		return false
	}
	seen[id] = now.Add(ingressDedupeTTL)
	return true
}

// Release removes a reservation (call when processing fails after Reserve returned true).
func Release(id string) {
	if id == "" {
		return
	}
	mu.Lock()
	delete(seen, id)
	mu.Unlock()
}

// NoteIngested marks id as ingested for the TTL window without a prior Reserve (e.g. signed gossip accepted).
// Idempotent: refreshes expiry if already present.
func NoteIngested(id string) {
	if id == "" {
		return
	}
	now := time.Now()
	mu.Lock()
	defer mu.Unlock()
	pruneLocked(now)
	seen[id] = now.Add(ingressDedupeTTL)
}
