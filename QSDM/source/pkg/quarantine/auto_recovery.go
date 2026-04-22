package quarantine

import (
	"sync"
	"time"
)

// AutoRecoveryConfig controls automatic re-admission of quarantined submeshes.
//
// A submesh is auto-recovered when it sustains a low invalid-transaction ratio
// across a configurable number of consecutive evaluation windows. This keeps the
// quarantine transient for submeshes that recover from a bad deploy or transient
// peer-set issue, without manually calling RemoveQuarantine.
type AutoRecoveryConfig struct {
	// WindowSize is the number of transactions per evaluation window.
	// Must be >= 1 (defaults to 10).
	WindowSize int
	// RecoveryThreshold is the invalid ratio at/below which the window is considered healthy.
	// Typical value: 0.1 for "≤10% invalid" (0 = require perfect windows).
	RecoveryThreshold float64
	// ConsecutiveHealthy is the number of consecutive healthy windows required to recover.
	// Must be >= 1 (defaults to 3).
	ConsecutiveHealthy int
	// CooldownAfterQuarantine is the minimum time a submesh must remain quarantined before
	// recovery is considered, even if windows look healthy. Zero disables the cooldown.
	CooldownAfterQuarantine time.Duration
}

// DefaultAutoRecoveryConfig returns a conservative default policy.
func DefaultAutoRecoveryConfig() AutoRecoveryConfig {
	return AutoRecoveryConfig{
		WindowSize:              10,
		RecoveryThreshold:       0.1,
		ConsecutiveHealthy:      3,
		CooldownAfterQuarantine: 30 * time.Second,
	}
}

// AutoRecoveryManager wraps a QuarantineManager with auto re-admission logic.
//
// It is safe for concurrent use. The wrapper does not mutate QuarantineManager's
// existing counting semantics (RecordTransaction still evaluates the primary
// quarantine condition); it only adds a parallel bookkeeping path that decides
// when to call RemoveQuarantine.
type AutoRecoveryManager struct {
	mu  sync.Mutex
	cfg AutoRecoveryConfig
	qm  *QuarantineManager

	state map[string]*recoveryState

	// recordedAt tracks when a submesh entered quarantine so we can enforce the
	// cooldown period before auto-recovery fires.
	quarantinedAt map[string]time.Time

	// OnRecovery, when non-nil, is invoked (outside the internal lock) after a
	// successful auto-recovery. Useful for metrics or audit hooks.
	OnRecovery func(submesh string)

	now func() time.Time // test hook
}

type recoveryState struct {
	inWindowTx      int
	inWindowInvalid int
	healthyStreak   int
}

// NewAutoRecoveryManager creates an auto-recovery wrapper for qm using cfg.
// A nil cfg.now means time.Now.
func NewAutoRecoveryManager(qm *QuarantineManager, cfg AutoRecoveryConfig) *AutoRecoveryManager {
	if cfg.WindowSize <= 0 {
		cfg.WindowSize = 10
	}
	if cfg.ConsecutiveHealthy <= 0 {
		cfg.ConsecutiveHealthy = 3
	}
	if cfg.RecoveryThreshold < 0 {
		cfg.RecoveryThreshold = 0
	}
	return &AutoRecoveryManager{
		cfg:           cfg,
		qm:            qm,
		state:         make(map[string]*recoveryState),
		quarantinedAt: make(map[string]time.Time),
		now:           time.Now,
	}
}

// Observe updates the auto-recovery bookkeeping for submesh with the given tx validity.
// It is intended to be called alongside QuarantineManager.RecordTransaction by the caller.
// Returns true if this observation triggered an auto-recovery.
func (m *AutoRecoveryManager) Observe(submesh string, valid bool) bool {
	m.mu.Lock()
	st, ok := m.state[submesh]
	if !ok {
		st = &recoveryState{}
		m.state[submesh] = st
	}
	// Track quarantine entry for cooldown accounting.
	if m.qm.IsQuarantined(submesh) {
		if _, seen := m.quarantinedAt[submesh]; !seen {
			m.quarantinedAt[submesh] = m.now()
		}
	} else {
		delete(m.quarantinedAt, submesh)
		st.healthyStreak = 0
	}

	st.inWindowTx++
	if !valid {
		st.inWindowInvalid++
	}

	var recovered bool
	if st.inWindowTx >= m.cfg.WindowSize {
		ratio := float64(st.inWindowInvalid) / float64(st.inWindowTx)
		if ratio <= m.cfg.RecoveryThreshold {
			st.healthyStreak++
		} else {
			st.healthyStreak = 0
		}
		st.inWindowTx = 0
		st.inWindowInvalid = 0

		if st.healthyStreak >= m.cfg.ConsecutiveHealthy && m.qm.IsQuarantined(submesh) {
			if m.cooldownElapsed(submesh) {
				// Release both maps' lock ordering: remove quarantine inside manager's own lock.
				m.mu.Unlock()
				_ = m.qm.RemoveQuarantine(submesh)
				m.mu.Lock()
				st.healthyStreak = 0
				delete(m.quarantinedAt, submesh)
				recovered = true
			}
		}
	}

	hook := m.OnRecovery
	m.mu.Unlock()

	if recovered && hook != nil {
		hook(submesh)
	}
	return recovered
}

// HealthyStreak returns how many consecutive healthy windows submesh has accumulated.
func (m *AutoRecoveryManager) HealthyStreak(submesh string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.state[submesh]; ok {
		return st.healthyStreak
	}
	return 0
}

// Reset clears recovery bookkeeping for submesh (e.g. on manual un-quarantine).
func (m *AutoRecoveryManager) Reset(submesh string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.state, submesh)
	delete(m.quarantinedAt, submesh)
}

// Snapshot returns a copy of the current bookkeeping. Intended for metrics/debug only.
func (m *AutoRecoveryManager) Snapshot() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int, len(m.state))
	for k, v := range m.state {
		out[k] = v.healthyStreak
	}
	return out
}

func (m *AutoRecoveryManager) cooldownElapsed(submesh string) bool {
	if m.cfg.CooldownAfterQuarantine <= 0 {
		return true
	}
	t, ok := m.quarantinedAt[submesh]
	if !ok {
		return true
	}
	return m.now().Sub(t) >= m.cfg.CooldownAfterQuarantine
}
