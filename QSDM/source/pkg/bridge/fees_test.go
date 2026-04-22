package bridge

import (
	"math"
	"testing"
)

func TestFeeConfig_Default(t *testing.T) {
	cfg := DefaultFeeConfig()
	if cfg.BaseFee <= 0 || cfg.PercentageFee <= 0 {
		t.Fatal("default fee config should have positive base and percentage fees")
	}
}

func TestFeeCollector_CalculateFee(t *testing.T) {
	fc := NewFeeCollector(FeeConfig{
		BaseFee:       1.0,
		PercentageFee: 0.01,
		MinFee:        1.0,
		MaxFee:        50.0,
	})

	tests := []struct {
		amount   float64
		expected float64
	}{
		{0, 1.0},       // base fee only, equals min
		{100, 2.0},     // 1.0 + 100*0.01 = 2.0
		{1000, 11.0},   // 1.0 + 1000*0.01 = 11.0
		{10000, 50.0},  // capped at max (1 + 100 = 101 -> 50)
	}
	for _, tt := range tests {
		got := fc.CalculateFee(tt.amount)
		if math.Abs(got-tt.expected) > 0.001 {
			t.Errorf("CalculateFee(%f): expected %f, got %f", tt.amount, tt.expected, got)
		}
	}
}

func TestFeeCollector_MinFee(t *testing.T) {
	fc := NewFeeCollector(FeeConfig{
		BaseFee:       0.001,
		PercentageFee: 0.0001,
		MinFee:        0.5,
		MaxFee:        0,
	})
	fee := fc.CalculateFee(1.0)
	if fee < 0.5 {
		t.Fatalf("expected min fee 0.5, got %f", fee)
	}
}

func TestFeeCollector_Collect(t *testing.T) {
	fc := NewFeeCollector(FeeConfig{
		BaseFee:       0.5,
		PercentageFee: 0.01,
		MinFee:        0.5,
		MaxFee:        0,
	})

	rec := fc.Collect("lock_1", 100.0)
	if rec.FeeCharged != 1.5 {
		t.Fatalf("expected fee 1.5, got %f", rec.FeeCharged)
	}
	if rec.NetAmount != 98.5 {
		t.Fatalf("expected net 98.5, got %f", rec.NetAmount)
	}
	if fc.TotalCollected() != 1.5 {
		t.Fatalf("expected total 1.5, got %f", fc.TotalCollected())
	}
}

func TestFeeCollector_Distribution(t *testing.T) {
	fc := NewFeeCollector(FeeConfig{
		BaseFee:       1.0,
		PercentageFee: 0.0,
		MinFee:        0,
		MaxFee:        0,
	})
	err := fc.SetDistribution(map[string]float64{
		"treasury":  0.7,
		"validator": 0.3,
	})
	if err != nil {
		t.Fatalf("SetDistribution failed: %v", err)
	}

	fc.Collect("lock_1", 100.0)
	fc.Collect("lock_2", 200.0)

	treasuryBal := fc.PendingBalance("treasury")
	validatorBal := fc.PendingBalance("validator")

	if math.Abs(treasuryBal-1.4) > 0.001 {
		t.Fatalf("expected treasury 1.4, got %f", treasuryBal)
	}
	if math.Abs(validatorBal-0.6) > 0.001 {
		t.Fatalf("expected validator 0.6, got %f", validatorBal)
	}

	withdrawn := fc.Withdraw("treasury")
	if math.Abs(withdrawn-1.4) > 0.001 {
		t.Fatalf("expected withdraw 1.4, got %f", withdrawn)
	}
	if fc.PendingBalance("treasury") != 0 {
		t.Fatal("expected 0 pending after withdraw")
	}
}

func TestFeeCollector_InvalidDistribution(t *testing.T) {
	fc := NewFeeCollector(DefaultFeeConfig())
	err := fc.SetDistribution(map[string]float64{
		"a": 0.5,
		"b": 0.3,
	})
	if err == nil {
		t.Fatal("expected error for shares not summing to 1.0")
	}
}

func TestFeeCollector_History(t *testing.T) {
	fc := NewFeeCollector(FeeConfig{BaseFee: 1.0, PercentageFee: 0, MinFee: 0, MaxFee: 0})
	for i := 0; i < 5; i++ {
		fc.Collect("lock_"+string(rune('a'+i)), 100.0)
	}
	hist := fc.History(3)
	if len(hist) != 3 {
		t.Fatalf("expected 3 records, got %d", len(hist))
	}
	if hist[0].LockID != "lock_e" {
		t.Fatalf("expected most recent first, got %s", hist[0].LockID)
	}
}

func TestFeeCollector_Stats(t *testing.T) {
	fc := NewFeeCollector(FeeConfig{BaseFee: 2.0, PercentageFee: 0, MinFee: 0, MaxFee: 0})
	fc.SetDistribution(map[string]float64{"x": 1.0})
	fc.Collect("l1", 100.0)
	fc.Collect("l2", 200.0)
	fc.Withdraw("x")

	stats := fc.Stats()
	if stats["total_collected"].(float64) != 4.0 {
		t.Fatalf("unexpected total: %v", stats["total_collected"])
	}
	if stats["total_distributed"].(float64) != 4.0 {
		t.Fatalf("unexpected distributed: %v", stats["total_distributed"])
	}
	if stats["record_count"].(int) != 2 {
		t.Fatalf("unexpected record count: %v", stats["record_count"])
	}
}
