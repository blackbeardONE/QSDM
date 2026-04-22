package alerting

import (
	"fmt"
	"sync"
	"time"
)

// Comparator defines how a metric value is compared against the threshold.
type Comparator string

const (
	ComparatorAbove Comparator = "above"
	ComparatorBelow Comparator = "below"
	ComparatorEqual Comparator = "equal"
)

// AlertRule defines a threshold-based alerting rule.
type AlertRule struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Metric      string     `json:"metric"`      // metric key to watch (e.g. "gas_usage", "peer_count")
	Comparator  Comparator `json:"comparator"`
	Threshold   float64    `json:"threshold"`
	Severity    Severity   `json:"severity"`
	CooldownSec int        `json:"cooldown_sec"` // minimum seconds between repeat fires
	Enabled     bool       `json:"enabled"`
}

// AlertRuleState tracks runtime state for a rule.
type alertRuleState struct {
	lastFired time.Time
	fireCount int64
}

// MetricProvider supplies current metric values to the rule engine.
type MetricProvider func(metricKey string) (float64, bool)

// RuleEngine evaluates alerting rules against live metric values.
type RuleEngine struct {
	mu        sync.RWMutex
	rules     map[string]*AlertRule
	state     map[string]*alertRuleState
	provider  MetricProvider
	manager   *Manager
	stopCh    chan struct{}
	wg        sync.WaitGroup
	interval  time.Duration
}

// NewRuleEngine creates a rule engine that evaluates rules at the given interval.
func NewRuleEngine(provider MetricProvider, mgr *Manager, evalInterval time.Duration) *RuleEngine {
	if evalInterval <= 0 {
		evalInterval = 10 * time.Second
	}
	if mgr == nil {
		mgr = getManager()
	}
	return &RuleEngine{
		rules:    make(map[string]*AlertRule),
		state:    make(map[string]*alertRuleState),
		provider: provider,
		manager:  mgr,
		interval: evalInterval,
		stopCh:   make(chan struct{}),
	}
}

// AddRule registers or updates an alerting rule.
func (re *RuleEngine) AddRule(rule AlertRule) {
	re.mu.Lock()
	defer re.mu.Unlock()
	rule.Enabled = true
	re.rules[rule.ID] = &rule
	if _, exists := re.state[rule.ID]; !exists {
		re.state[rule.ID] = &alertRuleState{}
	}
}

// RemoveRule removes an alerting rule.
func (re *RuleEngine) RemoveRule(id string) {
	re.mu.Lock()
	defer re.mu.Unlock()
	delete(re.rules, id)
	delete(re.state, id)
}

// DisableRule disables a rule without removing it.
func (re *RuleEngine) DisableRule(id string) {
	re.mu.Lock()
	defer re.mu.Unlock()
	if r, ok := re.rules[id]; ok {
		r.Enabled = false
	}
}

// EnableRule re-enables a disabled rule.
func (re *RuleEngine) EnableRule(id string) {
	re.mu.Lock()
	defer re.mu.Unlock()
	if r, ok := re.rules[id]; ok {
		r.Enabled = true
	}
}

// ListRules returns all configured rules.
func (re *RuleEngine) ListRules() []AlertRule {
	re.mu.RLock()
	defer re.mu.RUnlock()
	out := make([]AlertRule, 0, len(re.rules))
	for _, r := range re.rules {
		out = append(out, *r)
	}
	return out
}

// Start begins the evaluation loop in a background goroutine.
func (re *RuleEngine) Start() {
	re.wg.Add(1)
	go func() {
		defer re.wg.Done()
		ticker := time.NewTicker(re.interval)
		defer ticker.Stop()
		for {
			select {
			case <-re.stopCh:
				return
			case <-ticker.C:
				re.EvaluateAll()
			}
		}
	}()
}

// Stop halts the evaluation loop.
func (re *RuleEngine) Stop() {
	close(re.stopCh)
	re.wg.Wait()
}

// EvaluateAll checks all enabled rules against current metric values.
// Returns the list of rule IDs that fired.
func (re *RuleEngine) EvaluateAll() []string {
	re.mu.RLock()
	rules := make([]*AlertRule, 0, len(re.rules))
	for _, r := range re.rules {
		if r.Enabled {
			rules = append(rules, r)
		}
	}
	re.mu.RUnlock()

	var fired []string
	for _, rule := range rules {
		if re.evaluate(rule) {
			fired = append(fired, rule.ID)
		}
	}
	return fired
}

func (re *RuleEngine) evaluate(rule *AlertRule) bool {
	val, ok := re.provider(rule.Metric)
	if !ok {
		return false
	}

	triggered := false
	switch rule.Comparator {
	case ComparatorAbove:
		triggered = val > rule.Threshold
	case ComparatorBelow:
		triggered = val < rule.Threshold
	case ComparatorEqual:
		triggered = val == rule.Threshold
	}

	if !triggered {
		return false
	}

	re.mu.Lock()
	st := re.state[rule.ID]
	if st == nil {
		st = &alertRuleState{}
		re.state[rule.ID] = st
	}
	cooldown := time.Duration(rule.CooldownSec) * time.Second
	if cooldown > 0 && time.Since(st.lastFired) < cooldown {
		re.mu.Unlock()
		return false
	}
	st.lastFired = time.Now()
	st.fireCount++
	re.mu.Unlock()

	msg := fmt.Sprintf("Rule %q fired: %s %s %.2f (current: %.2f)",
		rule.Name, rule.Metric, rule.Comparator, rule.Threshold, val)
	re.manager.Send(AlertEvent{Severity: rule.Severity, Message: msg, Source: "rule_engine"})

	return true
}

// FireCount returns how many times a rule has fired.
func (re *RuleEngine) FireCount(ruleID string) int64 {
	re.mu.RLock()
	defer re.mu.RUnlock()
	if st, ok := re.state[ruleID]; ok {
		return st.fireCount
	}
	return 0
}
