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
}

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

// RegisterComponent registers a component for health monitoring
func (hc *HealthChecker) RegisterComponent(name string) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	hc.components[name] = &ComponentHealth{
		Name:      name,
		Status:    HealthStatusHealthy,
		LastCheck: time.Now(),
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

// CheckHealth performs health checks on all components
func (hc *HealthChecker) CheckHealth() {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	// Check if components are stale (not updated in last 10 minutes)
	// Increased threshold to avoid false positives for components that don't need frequent updates
	staleThreshold := 10 * time.Minute
	now := time.Now()

	for _, comp := range hc.components {
		// Only mark as degraded if it was previously healthy and is now stale
		// Don't override explicit degraded/unhealthy statuses
		if comp.Status == HealthStatusHealthy && now.Sub(comp.LastCheck) > staleThreshold {
			comp.Status = HealthStatusDegraded
			comp.Message = "Component health check is stale (not updated in 10+ minutes)"
		}
	}
}

