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
