package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// AdminAction represents an audited admin operation.
type AdminAction struct {
	ID         string                 `json:"id"`
	Actor      string                 `json:"actor"`
	Action     string                 `json:"action"`
	Target     string                 `json:"target"`
	Payload    map[string]interface{} `json:"payload,omitempty"`
	Timestamp  time.Time              `json:"timestamp"`
	PrevHash   string                 `json:"prev_hash"`
	Hash       string                 `json:"hash"`
	Signature  string                 `json:"signature"`
}

// AdminAuditTrail stores admin actions in a tamper-evident hash chain.
type AdminAuditTrail struct {
	mu      sync.RWMutex
	secret  []byte
	actions []AdminAction
}

// NewAdminAuditTrail creates a hash-chained action trail.
func NewAdminAuditTrail(secret string) *AdminAuditTrail {
	return &AdminAuditTrail{secret: []byte(secret)}
}

// Record appends an admin action and returns the recorded entry.
func (at *AdminAuditTrail) Record(actor, action, target string, payload map[string]interface{}) AdminAction {
	at.mu.Lock()
	defer at.mu.Unlock()

	ts := time.Now().UTC()
	prev := ""
	if n := len(at.actions); n > 0 {
		prev = at.actions[n-1].Hash
	}
	id := fmt.Sprintf("%d-%s-%s", ts.UnixNano(), actor, action)
	hash := at.computeHash(id, actor, action, target, payload, ts, prev)
	sig := at.sign(hash)
	entry := AdminAction{
		ID:        id,
		Actor:     actor,
		Action:    action,
		Target:    target,
		Payload:   payload,
		Timestamp: ts,
		PrevHash:  prev,
		Hash:      hash,
		Signature: sig,
	}
	at.actions = append(at.actions, entry)
	return entry
}

// Recent returns the last n actions in reverse chronological order.
func (at *AdminAuditTrail) Recent(n int) []AdminAction {
	at.mu.RLock()
	defer at.mu.RUnlock()
	if n <= 0 || len(at.actions) == 0 {
		return nil
	}
	if n > len(at.actions) {
		n = len(at.actions)
	}
	out := make([]AdminAction, 0, n)
	for i := len(at.actions) - 1; i >= len(at.actions)-n; i-- {
		out = append(out, at.actions[i])
	}
	return out
}

// VerifyChain verifies all hashes and signatures in chain order.
func (at *AdminAuditTrail) VerifyChain() error {
	at.mu.RLock()
	defer at.mu.RUnlock()
	prev := ""
	for i, a := range at.actions {
		expectHash := at.computeHash(a.ID, a.Actor, a.Action, a.Target, a.Payload, a.Timestamp, prev)
		if a.Hash != expectHash {
			return fmt.Errorf("hash mismatch at index %d", i)
		}
		if a.Signature != at.sign(a.Hash) {
			return fmt.Errorf("signature mismatch at index %d", i)
		}
		if a.PrevHash != prev {
			return fmt.Errorf("prev hash mismatch at index %d", i)
		}
		prev = a.Hash
	}
	return nil
}

// Count returns total actions.
func (at *AdminAuditTrail) Count() int {
	at.mu.RLock()
	defer at.mu.RUnlock()
	return len(at.actions)
}

// List returns audit entries in reverse chronological order (newest first) with
// optional filters. limit<=0 defaults to 50; offset skips that many newest matches.
func (at *AdminAuditTrail) List(limit, offset int, actorFilter, actionFilter string) []AdminAction {
	at.mu.RLock()
	defer at.mu.RUnlock()
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var out []AdminAction
	skipped := 0
	for i := len(at.actions) - 1; i >= 0; i-- {
		a := at.actions[i]
		if actorFilter != "" && a.Actor != actorFilter {
			continue
		}
		if actionFilter != "" && a.Action != actionFilter {
			continue
		}
		if skipped < offset {
			skipped++
			continue
		}
		if len(out) >= limit {
			break
		}
		out = append(out, a)
	}
	return out
}

func (at *AdminAuditTrail) computeHash(id, actor, action, target string, payload map[string]interface{}, ts time.Time, prev string) string {
	payloadJSON, _ := json.Marshal(payload)
	data := fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s", id, actor, action, target, payloadJSON, ts.Format(time.RFC3339Nano), prev)
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

func (at *AdminAuditTrail) sign(hash string) string {
	m := hmac.New(sha256.New, at.secret)
	m.Write([]byte(hash))
	return hex.EncodeToString(m.Sum(nil))
}

