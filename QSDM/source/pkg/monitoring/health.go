package monitoring

import (
	"sync"
	"time"
)

// HealthStatus represents the health status of a component
type HealthStatus string

const (
	HealthStatusHealthy   HealthStatus = "healthy"
	HealthStatusDegraded  HealthStatus = "degraded"
	HealthStatusUnhealthy HealthStatus = "unhealthy"
)

// ComponentHealth represents the health of a system component
type ComponentHealth struct {
	Name      string
	Status    HealthStatus
	Message   string
	LastCheck time.Time

	// probe, when set, is a real liveness check re-run on every
	// CheckHealth tick. Components WITH a probe are expected to stay
	// fresh, so staleness is a genuine fault signal for them. Components
	// WITHOUT one record a boot-time fact ("SQLite storage initialized")
	// that does not rot, and are exempt from the staleness rule.
	//
	// Before probes existed, staleness was applied to everything: five of
	// the six registered components were set once at boot and never
	// updated, so every node reported DEGRADED about ten minutes after
	// start no matter how healthy it actually was.
	probe HealthProbe
}

// HealthProbe reports a component's current status. Implementations must be
// non-blocking and cheap — they run on every health-check tick.
type HealthProbe func() (HealthStatus, string)

// HealthChecker monitors system health
type HealthChecker struct {
	mu         sync.RWMutex
	components map[string]*ComponentHealth
	metrics    *Metrics
}

// NewHealthChecker creates a new health checker
func NewHealthChecker(metrics *Metrics) *HealthChecker {
	return &HealthChecker{
		components: make(map[string]*ComponentHealth),
		metrics:    metrics,
	}
}

// RegisterComponent registers a component for health monitoring. Its status
// is whatever the last UpdateComponentHealth call set; without a probe it is
// treated as a boot-time fact and never goes stale. Prefer
// RegisterComponentWithProbe for anything with genuine runtime liveness.
func (hc *HealthChecker) RegisterComponent(name string) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	hc.components[name] = &ComponentHealth{
		Name:      name,
		Status:    HealthStatusHealthy,
		LastCheck: time.Now(),
	}
}

// RegisterComponentWithProbe registers a component whose health is
// re-evaluated on every CheckHealth tick by calling probe.
//
// This is the preferred form: it reports what is true now rather than what
// was true at boot, and it makes staleness meaningful — a component with a
// probe that stops being refreshed really is faulty.
func (hc *HealthChecker) RegisterComponentWithProbe(name string, probe HealthProbe) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	hc.components[name] = &ComponentHealth{
		Name:      name,
		Status:    HealthStatusHealthy,
		LastCheck: time.Now(),
		probe:     probe,
	}
}

// SetComponentProbe attaches (or clears, with nil) a liveness probe on an
// already-registered component. Useful when the thing being probed is only
// constructed later in boot than the registration.
func (hc *HealthChecker) SetComponentProbe(name string, probe HealthProbe) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	if comp, ok := hc.components[name]; ok {
		comp.probe = probe
	}
}

// UpdateComponentHealth updates the health status of a component
func (hc *HealthChecker) UpdateComponentHealth(name string, status HealthStatus, message string) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	if comp, exists := hc.components[name]; exists {
		comp.Status = status
		comp.Message = message
		comp.LastCheck = time.Now()
	}
}

// GetComponentHealth returns the health status of a component
func (hc *HealthChecker) GetComponentHealth(name string) (*ComponentHealth, bool) {
	hc.mu.RLock()
	defer hc.mu.RUnlock()
	comp, exists := hc.components[name]
	return comp, exists
}

// GetOverallHealth returns the overall system health
func (hc *HealthChecker) GetOverallHealth() HealthStatus {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	hasUnhealthy := false
	hasDegraded := false

	for _, comp := range hc.components {
		switch comp.Status {
		case HealthStatusUnhealthy:
			hasUnhealthy = true
		case HealthStatusDegraded:
			hasDegraded = true
		}
	}

	if hasUnhealthy {
		return HealthStatusUnhealthy
	}
	if hasDegraded {
		return HealthStatusDegraded
	}
	return HealthStatusHealthy
}

// GetHealthReport returns a comprehensive health report
func (hc *HealthChecker) GetHealthReport() map[string]interface{} {
	hc.mu.RLock()

	// Calculate overall health while holding the lock
	hasUnhealthy := false
	hasDegraded := false
	components := make(map[string]interface{})

	for name, comp := range hc.components {
		components[name] = map[string]interface{}{
			"status":     comp.Status,
			"message":    comp.Message,
			"last_check": comp.LastCheck,
		}
		switch comp.Status {
		case HealthStatusUnhealthy:
			hasUnhealthy = true
		case HealthStatusDegraded:
			hasDegraded = true
		}
	}

	var overallStatus HealthStatus
	if hasUnhealthy {
		overallStatus = HealthStatusUnhealthy
	} else if hasDegraded {
		overallStatus = HealthStatusDegraded
	} else {
		overallStatus = HealthStatusHealthy
	}

	hc.mu.RUnlock()

	report := make(map[string]interface{})
	report["overall_status"] = overallStatus
	report["timestamp"] = time.Now()
	report["components"] = components

	// Get metrics without holding the health checker lock
	if hc.metrics != nil {
		report["metrics"] = hc.metrics.GetStats()
	}

	return report
}

// CheckHealth re-evaluates every component.
//
// Components with a probe are re-checked against reality on each tick, so
// their status reflects the node's current state and their timestamp stays
// fresh by construction.
//
// Components without a probe hold a boot-time fact — "SQLite storage
// initialized", "Governance system initialized" — which does not become
// false merely because time passed. They are therefore exempt from the
// staleness rule.
//
// The previous behaviour applied staleness to everything. Five of the six
// components registered by cmd/qsdm are set once at boot and never updated,
// so every node flipped to DEGRADED roughly ten minutes after start and
// stayed there permanently, reporting a fault that did not exist while
// masking any real one.
func (hc *HealthChecker) CheckHealth() {
	// Snapshot the probes under lock, then run them WITHOUT holding it: a
	// probe that touches a subsystem which itself reports health would
	// otherwise deadlock on hc.mu.
	type probeTarget struct {
		name  string
		probe HealthProbe
	}
	hc.mu.RLock()
	targets := make([]probeTarget, 0, len(hc.components))
	for name, comp := range hc.components {
		if comp.probe != nil {
			targets = append(targets, probeTarget{name: name, probe: comp.probe})
		}
	}
	hc.mu.RUnlock()

	for _, t := range targets {
		status, msg := t.probe()
		hc.UpdateComponentHealth(t.name, status, msg)
	}

	// Staleness now applies only to probed components, where a missed
	// refresh is genuine evidence that the probe path has stopped running.
	hc.mu.Lock()
	defer hc.mu.Unlock()
	staleThreshold := 10 * time.Minute
	now := time.Now()
	for _, comp := range hc.components {
		if comp.probe == nil {
			continue
		}
		if comp.Status == HealthStatusHealthy && now.Sub(comp.LastCheck) > staleThreshold {
			comp.Status = HealthStatusDegraded
			comp.Message = "Component health check is stale (not updated in 10+ minutes)"
		}
	}
}
