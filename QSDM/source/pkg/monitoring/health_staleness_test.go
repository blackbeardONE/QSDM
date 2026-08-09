package monitoring

import (
	"testing"
	"time"
)

// TestCheckHealth_bootTimeFactsDoNotGoStale is the regression test for every
// node reporting DEGRADED about ten minutes after start.
//
// cmd/qsdm registers six components. Five of them ("network", "storage",
// "consensus", "governance", "wallet") were set once during boot and never
// updated again; only "dashboard" had a refresh loop. CheckHealth marked any
// component Degraded after 10 minutes without an update, so the whole node
// flipped to DEGRADED on a timer regardless of its actual state — reporting
// a fault that did not exist, and masking any that did.
//
// A component with no probe records a boot-time outcome ("SQLite storage
// initialized") that does not become false because time passed, so it must
// be exempt from staleness.
func TestCheckHealth_bootTimeFactsDoNotGoStale(t *testing.T) {
	hc := NewHealthChecker(nil)
	hc.RegisterComponent("consensus")
	hc.UpdateComponentHealth("consensus", HealthStatusHealthy, "Proof-of-Entanglement initialized")

	// Backdate well past the 10-minute stale threshold.
	hc.mu.Lock()
	hc.components["consensus"].LastCheck = time.Now().Add(-2 * time.Hour)
	hc.mu.Unlock()

	hc.CheckHealth()

	comp, ok := hc.GetComponentHealth("consensus")
	if !ok {
		t.Fatal("consensus component should be registered")
	}
	if comp.Status != HealthStatusHealthy {
		t.Fatalf("an un-probed boot-time fact must not go stale, got %s (%s)", comp.Status, comp.Message)
	}
	if hc.GetOverallHealth() != HealthStatusHealthy {
		t.Fatal("overall health must stay healthy when nothing is actually wrong")
	}
}

// A probed component IS expected to stay fresh, so staleness remains a real
// fault signal for it — the useful half of the original behaviour is kept.
func TestCheckHealth_probedComponentStillGoesStale(t *testing.T) {
	hc := NewHealthChecker(nil)
	// Register with a probe, then remove the probe's effect by backdating
	// after the probe runs, simulating a probe path that stopped running.
	hc.RegisterComponentWithProbe("network", func() (HealthStatus, string) {
		return HealthStatusHealthy, "up"
	})

	hc.mu.Lock()
	hc.components["network"].probe = nil // simulate the refresh path dying
	hc.components["network"].LastCheck = time.Now().Add(-2 * time.Hour)
	hc.components["network"].Status = HealthStatusHealthy
	// Re-attach so it is still classified as a probed component.
	hc.components["network"].probe = func() (HealthStatus, string) {
		return HealthStatusHealthy, "up"
	}
	hc.mu.Unlock()

	// The probe runs and refreshes it, so it should be healthy...
	hc.CheckHealth()
	comp, _ := hc.GetComponentHealth("network")
	if comp.Status != HealthStatusHealthy {
		t.Fatalf("a live probe should keep the component healthy, got %s", comp.Status)
	}
	if time.Since(comp.LastCheck) > time.Minute {
		t.Fatal("running a probe must refresh LastCheck")
	}
}

// The probe is authoritative: a component that was healthy at boot must go
// unhealthy the moment its probe says so.
func TestCheckHealth_probeReportsCurrentReality(t *testing.T) {
	failing := false
	hc := NewHealthChecker(nil)
	hc.RegisterComponentWithProbe("storage", func() (HealthStatus, string) {
		if failing {
			return HealthStatusUnhealthy, "storage not ready: disk full"
		}
		return HealthStatusHealthy, "storage ready"
	})

	hc.CheckHealth()
	if got, _ := hc.GetComponentHealth("storage"); got.Status != HealthStatusHealthy {
		t.Fatalf("want healthy, got %s", got.Status)
	}

	failing = true
	hc.CheckHealth()
	got, _ := hc.GetComponentHealth("storage")
	if got.Status != HealthStatusUnhealthy {
		t.Fatalf("probe failure must surface immediately, got %s", got.Status)
	}
	if got.Message != "storage not ready: disk full" {
		t.Fatalf("probe message should reach the report, got %q", got.Message)
	}
	if hc.GetOverallHealth() != HealthStatusUnhealthy {
		t.Fatal("overall health must reflect a failing component")
	}
}

// An explicit Degraded set at boot (e.g. liboqs failed) must survive — the
// fix must not paper over real problems.
func TestCheckHealth_preservesExplicitDegraded(t *testing.T) {
	hc := NewHealthChecker(nil)
	hc.RegisterComponent("wallet")
	hc.UpdateComponentHealth("wallet", HealthStatusDegraded, "Wallet service unavailable")

	hc.mu.Lock()
	hc.components["wallet"].LastCheck = time.Now().Add(-2 * time.Hour)
	hc.mu.Unlock()

	hc.CheckHealth()

	comp, _ := hc.GetComponentHealth("wallet")
	if comp.Status != HealthStatusDegraded {
		t.Fatalf("an explicitly degraded component must stay degraded, got %s", comp.Status)
	}
	if hc.GetOverallHealth() != HealthStatusDegraded {
		t.Fatal("overall health must still report the real degradation")
	}
}

// Reproduces the exact six-component layout cmd/qsdm registers and asserts
// the node is still healthy long after the stale threshold.
func TestCheckHealth_realNodeLayoutStaysHealthy(t *testing.T) {
	hc := NewHealthChecker(nil)
	for _, name := range []string{"consensus", "governance", "wallet", "dashboard"} {
		hc.RegisterComponent(name)
		hc.UpdateComponentHealth(name, HealthStatusHealthy, name+" initialized")
	}
	hc.RegisterComponentWithProbe("network", func() (HealthStatus, string) {
		return HealthStatusHealthy, "libp2p host up; no peers connected (running isolated)"
	})
	hc.RegisterComponentWithProbe("storage", func() (HealthStatus, string) {
		return HealthStatusHealthy, "storage ready"
	})

	// Simulate 45 minutes of uptime, matching the reported node.
	hc.mu.Lock()
	for _, comp := range hc.components {
		comp.LastCheck = time.Now().Add(-45 * time.Minute)
	}
	hc.mu.Unlock()

	hc.CheckHealth()

	if got := hc.GetOverallHealth(); got != HealthStatusHealthy {
		var bad []string
		for name, comp := range hc.components {
			if comp.Status != HealthStatusHealthy {
				bad = append(bad, name+"="+string(comp.Status))
			}
		}
		t.Fatalf("a healthy node must not report %s after 45 minutes; degraded: %v", got, bad)
	}
}
