package contracts

import (
	"context"
	"testing"
	"time"
)

func TestDeployAndListContracts(t *testing.T) {
	engine := NewContractEngine(nil)

	tmpl, err := GetTemplate("SimpleToken")
	if err != nil {
		t.Fatalf("GetTemplate: %v", err)
	}

	ctx := context.Background()
	c, err := engine.DeployContract(ctx, "token1", tmpl.Code, tmpl.ABI, "owner_alice")
	if err != nil {
		t.Fatalf("DeployContract: %v", err)
	}
	if c.ID != "token1" {
		t.Fatalf("ID = %s, want token1", c.ID)
	}
	if c.Owner != "owner_alice" {
		t.Fatalf("Owner = %s, want owner_alice", c.Owner)
	}

	list := engine.ListContracts()
	if len(list) != 1 {
		t.Fatalf("ListContracts = %d, want 1", len(list))
	}
}

func TestDeployDuplicateContractFails(t *testing.T) {
	engine := NewContractEngine(nil)
	abi := &ABI{Functions: []Function{{Name: "test"}}}
	ctx := context.Background()

	engine.DeployContract(ctx, "dup", []byte("code"), abi, "owner")
	_, err := engine.DeployContract(ctx, "dup", []byte("code"), abi, "owner")
	if err == nil {
		t.Fatal("expected error for duplicate contract ID")
	}
}

func TestExecuteContract(t *testing.T) {
	engine := NewContractEngine(nil)
	tmpl, _ := GetTemplate("SimpleToken")
	ctx := context.Background()
	engine.DeployContract(ctx, "token2", tmpl.Code, tmpl.ABI, "owner")

	result, err := engine.ExecuteContract(ctx, "token2", "transfer", map[string]interface{}{
		"to":     "bob",
		"amount": 100,
	})
	if err != nil {
		t.Fatalf("ExecuteContract: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if result.GasUsed < 0 {
		t.Fatal("expected non-negative gas usage")
	}

	// Verify simulated state mutation
	out, ok := result.Output.(map[string]interface{})
	if !ok {
		t.Fatalf("output type = %T, want map", result.Output)
	}
	if out["success"] != true {
		t.Errorf("transfer success = %v, want true", out["success"])
	}
}

func TestSimulatedTokenBalanceTracking(t *testing.T) {
	engine := NewContractEngine(nil)
	tmpl, _ := GetTemplate("SimpleToken")
	ctx := context.Background()
	engine.DeployContract(ctx, "sim_tok", tmpl.Code, tmpl.ABI, "owner")

	engine.ExecuteContract(ctx, "sim_tok", "transfer", map[string]interface{}{"to": "alice", "amount": 50})
	engine.ExecuteContract(ctx, "sim_tok", "transfer", map[string]interface{}{"to": "alice", "amount": 30})

	result, _ := engine.ExecuteContract(ctx, "sim_tok", "balanceOf", map[string]interface{}{"address": "alice"})
	out := result.Output.(map[string]interface{})
	if out["balance"] != float64(80) {
		t.Errorf("balance = %v, want 80", out["balance"])
	}
}

func TestSimulatedVoting(t *testing.T) {
	engine := NewContractEngine(nil)
	tmpl, _ := GetTemplate("Voting")
	ctx := context.Background()
	engine.DeployContract(ctx, "vote1", tmpl.Code, tmpl.ABI, "owner")

	engine.ExecuteContract(ctx, "vote1", "vote", map[string]interface{}{"proposal": "p1", "choice": true})
	engine.ExecuteContract(ctx, "vote1", "vote", map[string]interface{}{"proposal": "p1", "choice": true})
	engine.ExecuteContract(ctx, "vote1", "vote", map[string]interface{}{"proposal": "p1", "choice": false})

	result, _ := engine.ExecuteContract(ctx, "vote1", "getResults", map[string]interface{}{"proposal": "p1"})
	out := result.Output.(map[string]interface{})
	if out["yes"] != float64(2) {
		t.Errorf("yes = %v, want 2", out["yes"])
	}
	if out["no"] != float64(1) {
		t.Errorf("no = %v, want 1", out["no"])
	}
}

func TestSimulatedEscrow(t *testing.T) {
	engine := NewContractEngine(nil)
	tmpl, _ := GetTemplate("Escrow")
	ctx := context.Background()
	engine.DeployContract(ctx, "esc1", tmpl.Code, tmpl.ABI, "owner")

	result, _ := engine.ExecuteContract(ctx, "esc1", "deposit", map[string]interface{}{"amount": 100})
	out := result.Output.(map[string]interface{})
	escrowID, ok := out["escrowId"].(string)
	if !ok || escrowID == "" {
		t.Fatalf("expected escrowId, got %v", out)
	}

	result, _ = engine.ExecuteContract(ctx, "esc1", "release", map[string]interface{}{"escrowId": escrowID})
	out = result.Output.(map[string]interface{})
	if out["success"] != true {
		t.Errorf("release success = %v, want true", out["success"])
	}
}

func TestExecuteNonExistentContract(t *testing.T) {
	engine := NewContractEngine(nil)
	ctx := context.Background()
	_, err := engine.ExecuteContract(ctx, "missing", "fn", nil)
	if err == nil {
		t.Fatal("expected error for missing contract")
	}
}

func TestExecuteNonExistentFunction(t *testing.T) {
	engine := NewContractEngine(nil)
	tmpl, _ := GetTemplate("Voting")
	ctx := context.Background()
	engine.DeployContract(ctx, "v1", tmpl.Code, tmpl.ABI, "owner")

	_, err := engine.ExecuteContract(ctx, "v1", "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for missing function")
	}
}

func TestGetTemplates(t *testing.T) {
	templates := GetTemplates()
	if len(templates) < 3 {
		t.Fatalf("expected at least 3 templates, got %d", len(templates))
	}
	names := map[string]bool{}
	for _, tmpl := range templates {
		names[tmpl.Name] = true
	}
	for _, want := range []string{"SimpleToken", "Voting", "Escrow"} {
		if !names[want] {
			t.Errorf("missing template %s", want)
		}
	}
}

func TestGetTemplateMissing(t *testing.T) {
	_, err := GetTemplate("NonExistent")
	if err == nil {
		t.Fatal("expected error for missing template")
	}
}

func TestContractTestFramework(t *testing.T) {
	fw := NewContractTestFramework(nil)
	ctx := context.Background()

	contract, err := fw.DeployTestContract(ctx, "Escrow")
	if err != nil {
		t.Fatalf("DeployTestContract: %v", err)
	}
	if contract.ID != "test_Escrow" {
		t.Fatalf("ID = %s, want test_Escrow", contract.ID)
	}

	result := fw.TestContractExecution(t, "test_Escrow", "deposit", map[string]interface{}{"amount": 50})
	fw.AssertExecutionSuccess(t, result, "deposit")
}

func TestGetExecutionResult(t *testing.T) {
	engine := NewContractEngine(nil)
	tmpl, _ := GetTemplate("SimpleToken")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	engine.DeployContract(ctx, "tok", tmpl.Code, tmpl.ABI, "o")
	engine.ExecuteContract(ctx, "tok", "transfer", map[string]interface{}{"to": "x", "amount": 1})

	_, err := engine.GetExecutionResult("nonexistent_id")
	if err == nil {
		t.Fatal("expected error for missing execution result")
	}
}
