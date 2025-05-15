package quarantine

import (
    "sync"
    "errors"
    "fmt"
)

// QuarantineManager manages quarantines for submeshes with high invalid transaction rates.
type QuarantineManager struct {
    mu          sync.Mutex
    quarantined map[string]bool
    invalidTxs  map[string]int
    totalTxs    map[string]int
    threshold   float64
}

// NewQuarantineManager creates a new QuarantineManager with a threshold (e.g., 0.5 for 50%).
func NewQuarantineManager(threshold float64) *QuarantineManager {
    return &QuarantineManager{
        quarantined: make(map[string]bool),
        invalidTxs:  make(map[string]int),
        totalTxs:    make(map[string]int),
        threshold:   threshold,
    }
}

// RecordTransaction records a transaction result for a submesh.
func (qm *QuarantineManager) RecordTransaction(submesh string, valid bool) {
    qm.mu.Lock()
    defer qm.mu.Unlock()

    qm.totalTxs[submesh]++
    if !valid {
        qm.invalidTxs[submesh]++
    }

    invalidRate := float64(qm.invalidTxs[submesh]) / float64(qm.totalTxs[submesh])
    if invalidRate > qm.threshold {
        qm.quarantined[submesh] = true
        fmt.Printf("Submesh %s quarantined due to invalid rate %.2f\n", submesh, invalidRate)
    }
}

// IsQuarantined checks if a submesh is quarantined.
func (qm *QuarantineManager) IsQuarantined(submesh string) bool {
    qm.mu.Lock()
    defer qm.mu.Unlock()
    return qm.quarantined[submesh]
}

// RemoveQuarantine removes quarantine status from a submesh.
func (qm *QuarantineManager) RemoveQuarantine(submesh string) error {
    qm.mu.Lock()
    defer qm.mu.Unlock()
    if !qm.quarantined[submesh] {
        return errors.New("submesh not quarantined")
    }
    delete(qm.quarantined, submesh)
    qm.invalidTxs[submesh] = 0
    qm.totalTxs[submesh] = 0
    fmt.Printf("Submesh %s removed from quarantine\n", submesh)
    return nil
}
