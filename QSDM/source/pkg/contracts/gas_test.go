package contracts

import (
	"context"
	"testing"
)

func TestGasMeter_BasicConsumption(t *testing.T) {
	gm := NewGasMeter(1000)
	if gm.Limit() != 1000 {
		t.Fatalf("Limit = %d, want 1000", gm.Limit())
	}
	if gm.Remaining() != 1000 {
		t.Fatalf("Remaining = %d, want 1000", gm.Remaining())
	}

	if err := gm.Consume(400); err != nil {
		t.Fatalf("Consume(400): %v", err)
	}
	if gm.Consumed() != 400 {
		t.Fatalf("Consumed = %d, want 400", gm.Consumed())
	}
	if gm.Remaining() != 600 {
		t.Fatalf("Remaining = %d, want 600", gm.Remaining())
	}
}

func TestGasMeter_ExhaustionError(t *testing.T) {
	gm := NewGasMeter(100)
	if err := gm.Consume(50); err != nil {
		t.Fatal(err)
	}
	if err := gm.Consume(60); err == nil {
		t.Fatal("expected out-of-gas error")
	}
}

func TestGasMeter_DefaultLimit(t *testing.T) {
	gm := NewGasMeter(0)
	if gm.Limit() != DefaultGasLimit {
		t.Fatalf("Limit = %d, want %d", gm.Limit(), DefaultGasLimit)
	}
}

func TestDeploymentGas(t *testing.T) {
	gas := DeploymentGas(200)
	want := GasBaseDeployment + 200*GasPerByteCode
	if gas != want {
		t.Fatalf("DeploymentGas(200) = %d, want %d", gas, want)
	}
}

func TestExecuteContract_GasTracking(t *testing.T) {
	engine := NewContractEngine(nil)
	tmpl, _ := GetTemplate("SimpleToken")
	ctx := context.Background()
	engine.DeployContract(ctx, "gas_tok", tmpl.Code, tmpl.ABI, "owner")

	result, err := engine.ExecuteContract(ctx, "gas_tok", "transfer", map[string]interface{}{
		"to": "bob", "amount": 10,
	})
	if err != nil {
		t.Fatalf("ExecuteContract: %v", err)
	}
	if result.GasUsed <= 0 {
		t.Fatalf("expected positive gas usage, got %d", result.GasUsed)
	}
}

func TestExecuteContract_GasLimitExceeded(t *testing.T) {
	engine := NewContractEngine(nil)
	tmpl, _ := GetTemplate("SimpleToken")
	ctx := context.Background()
	engine.DeployContract(ctx, "gas_lim", tmpl.Code, tmpl.ABI, "owner")

	result, err := engine.ExecuteContract(ctx, "gas_lim", "transfer", map[string]interface{}{
		"to": "bob", "amount": 10, "_gas_limit": float64(1),
	})
	if err == nil {
		t.Fatal("expected out-of-gas error")
	}
	if result.Success {
		t.Fatal("expected failure result")
	}
}

func TestContract_CumulativeGas(t *testing.T) {
	engine := NewContractEngine(nil)
	tmpl, _ := GetTemplate("SimpleToken")
	ctx := context.Background()
	c, _ := engine.DeployContract(ctx, "cum_gas", tmpl.Code, tmpl.ABI, "owner")

	if c.GasUsedDeploy <= 0 {
		t.Fatalf("expected deployment gas, got %d", c.GasUsedDeploy)
	}

	engine.ExecuteContract(ctx, "cum_gas", "transfer", map[string]interface{}{"to": "a", "amount": 1})
	engine.ExecuteContract(ctx, "cum_gas", "transfer", map[string]interface{}{"to": "b", "amount": 2})

	c2, _ := engine.GetContract("cum_gas")
	if c2.TotalGasUsed <= 0 {
		t.Fatalf("expected cumulative gas > 0, got %d", c2.TotalGasUsed)
	}
}

func TestDefaultGasConfig(t *testing.T) {
	cfg := DefaultGasConfig()
	if cfg.DefaultLimit != DefaultGasLimit {
		t.Fatalf("DefaultLimit = %d, want %d", cfg.DefaultLimit, DefaultGasLimit)
	}
	if cfg.MaxLimit < cfg.DefaultLimit {
		t.Fatal("MaxLimit should be >= DefaultLimit")
	}
}
