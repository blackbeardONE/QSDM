package quarantine

import (
	"sync"
)

// QuarantineManager manages quarantined submeshes and their state.
type QuarantineManager struct {
	mu          sync.Mutex
	quarantined map[string]bool
	txCounts    map[string]int
	invalidTxs  map[string]int
	threshold   float64
}

// NewQuarantineManager creates a new QuarantineManager instance.
func NewQuarantineManager(threshold float64) *QuarantineManager {
	return &QuarantineManager{
		quarantined: make(map[string]bool),
		txCounts:    make(map[string]int),
		invalidTxs:  make(map[string]int),
		threshold:   threshold,
	}
}

// IsQuarantined checks if a submesh is quarantined.
func (qm *QuarantineManager) IsQuarantined(submesh string) bool {
	qm.mu.Lock()
	defer qm.mu.Unlock()
	return qm.quarantined[submesh]
}

// SetQuarantine sets the quarantine status for a submesh.
func (qm *QuarantineManager) SetQuarantine(submesh string, status bool) {
	qm.mu.Lock()
	defer qm.mu.Unlock()
	qm.quarantined[submesh] = status
}

// RecordTransaction records the validity of a transaction for a submesh.
func (qm *QuarantineManager) RecordTransaction(submesh string, valid bool) {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	qm.txCounts[submesh]++
	if !valid {
		qm.invalidTxs[submesh]++
	}

	if qm.txCounts[submesh] >= 10 {
		invalidRatio := float64(qm.invalidTxs[submesh]) / float64(qm.txCounts[submesh])
		if invalidRatio > qm.threshold {
			qm.quarantined[submesh] = true
		} else {
			qm.quarantined[submesh] = false
		}
		qm.txCounts[submesh] = 0
		qm.invalidTxs[submesh] = 0
	}

	// Debug logging
	// fmt.Printf("Submesh: %s, txCount: %d, invalidTxs: %d, quarantined: %v\n", submesh, qm.txCounts[submesh], qm.invalidTxs[submesh], qm.quarantined[submesh])
}

func (qm *QuarantineManager) RemoveQuarantine(submesh string) error {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	if _, exists := qm.quarantined[submesh]; !exists {
		return nil
	}
	delete(qm.quarantined, submesh)
	return nil
}

// Stats is a read-only snapshot of the manager's bookkeeping, intended
// for metrics / debug / dashboard consumers. The counts are consistent
// with each other (taken under a single lock) but are not atomically
// consistent with any ongoing RecordTransaction call — a scrape that
// races a write will observe one of the two consistent states, never
// an in-between.
type Stats struct {
	// Quarantined is the number of submeshes where quarantined[k] == true.
	Quarantined int
	// Tracked is the number of distinct submeshes the manager has
	// observed since process start (union of all three internal maps).
	// This is the denominator operators usually want when reasoning
	// about "what fraction of the mesh is isolated right now".
	Tracked int
	// Threshold is the configured invalid-transaction ratio above
	// which a submesh is quarantined at window boundary. Exposed so
	// dashboards can render the policy alongside the observed state.
	Threshold float64
}

// Stats returns a consistent snapshot of the manager's bookkeeping.
func (qm *QuarantineManager) Stats() Stats {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	s := Stats{Threshold: qm.threshold}
	// Union of keys across all three maps. We can't just use
	// len(quarantined): RecordTransaction only writes there at the
	// 10-tx window boundary, so a freshly-seen submesh with 1..9 tx
	// is present in txCounts but not yet in quarantined. Counting the
	// union gives a stable "submeshes ever observed" denominator.
	seen := make(map[string]struct{}, len(qm.quarantined)+len(qm.txCounts))
	for k, v := range qm.quarantined {
		if v {
			s.Quarantined++
		}
		seen[k] = struct{}{}
	}
	for k := range qm.txCounts {
		seen[k] = struct{}{}
	}
	for k := range qm.invalidTxs {
		seen[k] = struct{}{}
	}
	s.Tracked = len(seen)
	return s
}
