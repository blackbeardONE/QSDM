package quarantine

import (
    "testing"
)

func TestQuarantineManager(t *testing.T) {
    qm := NewQuarantineManager(0.5)

    submesh := "test-submesh"

    // Record valid transactions
    for i := 0; i < 5; i++ {
        qm.RecordTransaction(submesh, true)
    }

    // Record invalid transactions to exceed threshold
    for i := 0; i < 6; i++ {
        qm.RecordTransaction(submesh, false)
    }

    if !qm.IsQuarantined(submesh) {
        t.Errorf("Expected submesh to be quarantined")
    }

    err := qm.RemoveQuarantine(submesh)
    if err != nil {
        t.Errorf("Failed to remove quarantine: %v", err)
    }

    if qm.IsQuarantined(submesh) {
        t.Errorf("Expected submesh to not be quarantined after removal")
    }
}
