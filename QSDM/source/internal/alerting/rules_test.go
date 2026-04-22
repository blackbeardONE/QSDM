package alerting

import (
	"sync"
	"testing"
	"time"
)

func metricsProvider(values map[string]float64) MetricProvider {
	var mu sync.RWMutex
	return func(key string) (float64, bool) {
		mu.RLock()
		defer mu.RUnlock()
		v, ok := values[key]
		return v, ok
	}
}

func TestRuleEngine_BasicEvaluation(t *testing.T) {
	metrics := map[string]float64{
		"peer_count": 2,
		"gas_usage":  95000,
	}
	re := NewRuleEngine(metricsProvider(metrics), NewManager(), time.Hour)

	re.AddRule(AlertRule{
		ID:         "low_peers",
		Name:       "Low Peer Count",
		Metric:     "peer_count",
		Comparator: ComparatorBelow,
		Threshold:  5,
		Severity:   SeverityWarning,
	})
	re.AddRule(AlertRule{
		ID:         "high_gas",
		Name:       "Gas Spike",
		Metric:     "gas_usage",
		Comparator: ComparatorAbove,
		Threshold:  90000,
		Severity:   SeverityCritical,
	})

	fired := re.EvaluateAll()
	if len(fired) != 2 {
		t.Fatalf("expected 2 rules to fire, got %d: %v", len(fired), fired)
	}
}

func TestRuleEngine_NotTriggered(t *testing.T) {
	metrics := map[string]float64{"peer_count": 10}
	re := NewRuleEngine(metricsProvider(metrics), NewManager(), time.Hour)

	re.AddRule(AlertRule{
		ID:         "low_peers",
		Name:       "Low Peer Count",
		Metric:     "peer_count",
		Comparator: ComparatorBelow,
		Threshold:  5,
		Severity:   SeverityWarning,
	})

	fired := re.EvaluateAll()
	if len(fired) != 0 {
		t.Fatalf("expected no rules to fire, got %d", len(fired))
	}
}

func TestRuleEngine_Cooldown(t *testing.T) {
	metrics := map[string]float64{"cpu": 99}
	re := NewRuleEngine(metricsProvider(metrics), NewManager(), time.Hour)

	re.AddRule(AlertRule{
		ID:          "hot_cpu",
		Name:        "High CPU",
		Metric:      "cpu",
		Comparator:  ComparatorAbove,
		Threshold:   80,
		Severity:    SeverityCritical,
		CooldownSec: 60,
	})

	fired1 := re.EvaluateAll()
	if len(fired1) != 1 {
		t.Fatalf("first eval: expected 1 fire, got %d", len(fired1))
	}

	fired2 := re.EvaluateAll()
	if len(fired2) != 0 {
		t.Fatalf("second eval (within cooldown): expected 0 fires, got %d", len(fired2))
	}

	if re.FireCount("hot_cpu") != 1 {
		t.Fatalf("expected fire count 1, got %d", re.FireCount("hot_cpu"))
	}
}

func TestRuleEngine_DisableEnable(t *testing.T) {
	metrics := map[string]float64{"x": 100}
	re := NewRuleEngine(metricsProvider(metrics), NewManager(), time.Hour)

	re.AddRule(AlertRule{
		ID:         "r1",
		Name:       "Test",
		Metric:     "x",
		Comparator: ComparatorAbove,
		Threshold:  50,
		Severity:   SeverityInfo,
	})

	re.DisableRule("r1")
	fired := re.EvaluateAll()
	if len(fired) != 0 {
		t.Fatal("disabled rule should not fire")
	}

	re.EnableRule("r1")
	fired = re.EvaluateAll()
	if len(fired) != 1 {
		t.Fatal("re-enabled rule should fire")
	}
}

func TestRuleEngine_RemoveRule(t *testing.T) {
	metrics := map[string]float64{"x": 100}
	re := NewRuleEngine(metricsProvider(metrics), NewManager(), time.Hour)

	re.AddRule(AlertRule{
		ID:         "r1",
		Name:       "Test",
		Metric:     "x",
		Comparator: ComparatorAbove,
		Threshold:  50,
		Severity:   SeverityInfo,
	})

	re.RemoveRule("r1")
	if len(re.ListRules()) != 0 {
		t.Fatal("expected 0 rules after removal")
	}
}

func TestRuleEngine_MissingMetric(t *testing.T) {
	metrics := map[string]float64{}
	re := NewRuleEngine(metricsProvider(metrics), NewManager(), time.Hour)

	re.AddRule(AlertRule{
		ID:         "ghost",
		Name:       "Ghost Metric",
		Metric:     "nonexistent",
		Comparator: ComparatorAbove,
		Threshold:  0,
		Severity:   SeverityInfo,
	})

	fired := re.EvaluateAll()
	if len(fired) != 0 {
		t.Fatal("rule for missing metric should not fire")
	}
}

func TestRuleEngine_EqualComparator(t *testing.T) {
	metrics := map[string]float64{"status": 0}
	re := NewRuleEngine(metricsProvider(metrics), NewManager(), time.Hour)

	re.AddRule(AlertRule{
		ID:         "down",
		Name:       "Service Down",
		Metric:     "status",
		Comparator: ComparatorEqual,
		Threshold:  0,
		Severity:   SeverityCritical,
	})

	fired := re.EvaluateAll()
	if len(fired) != 1 {
		t.Fatal("equal comparator should fire on match")
	}
}

func TestRuleEngine_StartStop(t *testing.T) {
	metrics := map[string]float64{"x": 100}
	re := NewRuleEngine(metricsProvider(metrics), NewManager(), 20*time.Millisecond)

	re.AddRule(AlertRule{
		ID:         "r1",
		Name:       "Test",
		Metric:     "x",
		Comparator: ComparatorAbove,
		Threshold:  50,
		Severity:   SeverityInfo,
	})

	re.Start()
	time.Sleep(60 * time.Millisecond)
	re.Stop()

	if re.FireCount("r1") < 1 {
		t.Fatal("expected at least one fire during background eval")
	}
}
