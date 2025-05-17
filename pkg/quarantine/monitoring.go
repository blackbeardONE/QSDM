package quarantine

import (
    "github.com/blackbeardONE/QSDM/internal/logging"
    "time"
)

// Monitor periodically logs quarantine and reputation status.
type Monitor struct {
    quarantineManager *QuarantineManager
    // Add reputationManager if needed
    interval time.Duration
    stopChan chan struct{}
}

// NewMonitor creates a new Monitor instance.
func NewMonitor(qm *QuarantineManager, interval time.Duration) *Monitor {
    return &Monitor{
        quarantineManager: qm,
        interval:          interval,
        stopChan:          make(chan struct{}),
    }
}

// Start begins the monitoring loop.
func (m *Monitor) Start() {
    go func() {
        ticker := time.NewTicker(m.interval)
        defer ticker.Stop()
        for {
            select {
            case <-ticker.C:
                m.logStatus()
            case <-m.stopChan:
                return
            }
        }
    }()
}

// Stop stops the monitoring loop.
func (m *Monitor) Stop() {
    close(m.stopChan)
}

// logStatus logs the current quarantine status.
func (m *Monitor) logStatus() {
    m.quarantineManager.mu.Lock()
    defer m.quarantineManager.mu.Unlock()

    for submesh, quarantined := range m.quarantineManager.quarantined {
        if quarantined {
            logging.Warn.Printf("Submesh %s is currently quarantined", submesh)
        }
    }
}
