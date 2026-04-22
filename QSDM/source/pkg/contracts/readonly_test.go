package contracts

import (
	"context"
	"testing"
	"time"
)

func deployTestContract(ce *ContractEngine, id string) {
	abi := &ABI{
		Functions: []Function{
			{Name: "balanceOf", Inputs: []Param{{Name: "address", Type: "string"}}, Outputs: []Param{{Name: "balance", Type: "float64"}}, StateMutating: false},
			{Name: "transfer", Inputs: []Param{{Name: "to", Type: "string"}, {Name: "amount", Type: "float64"}}, StateMutating: true},
			{Name: "getResults", Inputs: []Param{{Name: "proposal", Type: "string"}}, StateMutating: false},
			{Name: "vote", Inputs: []Param{{Name: "proposal", Type: "string"}, {Name: "choice", Type: "bool"}}, StateMutating: true},
		},
	}
	ce.DeployContract(context.Background(), id, []byte("sim"), abi, "owner")
}

func TestQueryContract_ViewFunction(t *testing.T) {
	ce := NewContractEngine(nil)
	deployTestContract(ce, "token-1")

	// First set some state via mutating call
	ce.ExecuteContract(context.Background(), "token-1", "transfer",
		map[string]interface{}{"to": "alice", "amount": float64(500)})

	// Now query read-only
	result, err := ce.QueryContract(context.Background(), "token-1", "balanceOf",
		map[string]interface{}{"address": "alice"})
	if err != nil {
		t.Fatalf("QueryContract: %v", err)
	}
	if result.ContractID != "token-1" {
		t.Fatal("wrong contract ID")
	}
	out, ok := result.Output.(map[string]interface{})
	if !ok {
		t.Fatal("expected map output")
	}
	bal, _ := out["balance"].(float64)
	if bal != 500 {
		t.Fatalf("expected balance 500, got %f", bal)
	}
	if result.GasEstimate <= 0 {
		t.Fatal("expected positive gas estimate")
	}
}

func TestQueryContract_RejectsMutating(t *testing.T) {
	ce := NewContractEngine(nil)
	deployTestContract(ce, "token-1")

	_, err := ce.QueryContract(context.Background(), "token-1", "transfer",
		map[string]interface{}{"to": "bob", "amount": float64(100)})
	if err == nil {
		t.Fatal("mutating function should be rejected in view call")
	}
}

func TestQueryContract_DoesNotMutateState(t *testing.T) {
	ce := NewContractEngine(nil)
	deployTestContract(ce, "token-1")

	ce.ExecuteContract(context.Background(), "token-1", "transfer",
		map[string]interface{}{"to": "alice", "amount": float64(100)})

	// Query should not change state even if the WASM/sim modifies it
	ce.QueryContract(context.Background(), "token-1", "balanceOf",
		map[string]interface{}{"address": "alice"})

	// Balance should still be 100
	contract, _ := ce.GetContract("token-1")
	bal, _ := toFloat64(contract.State["balance:alice"])
	if bal != 100 {
		t.Fatalf("state should not be modified by view call, balance=%f", bal)
	}
}

func TestQueryContract_NotFound(t *testing.T) {
	ce := NewContractEngine(nil)
	_, err := ce.QueryContract(context.Background(), "nonexistent", "fn", nil)
	if err == nil {
		t.Fatal("expected error for non-existent contract")
	}
}

func TestQueryContract_FunctionNotFound(t *testing.T) {
	ce := NewContractEngine(nil)
	deployTestContract(ce, "token-1")

	_, err := ce.QueryContract(context.Background(), "token-1", "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for non-existent function")
	}
}

func TestIsViewFunction(t *testing.T) {
	ce := NewContractEngine(nil)
	deployTestContract(ce, "token-1")

	isView, err := ce.IsViewFunction("token-1", "balanceOf")
	if err != nil || !isView {
		t.Fatal("balanceOf should be a view function")
	}

	isView, err = ce.IsViewFunction("token-1", "transfer")
	if err != nil || isView {
		t.Fatal("transfer should NOT be a view function")
	}
}

func TestListViewFunctions(t *testing.T) {
	ce := NewContractEngine(nil)
	deployTestContract(ce, "token-1")

	views, err := ce.ListViewFunctions("token-1")
	if err != nil {
		t.Fatalf("ListViewFunctions: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("expected 2 view functions (balanceOf, getResults), got %d", len(views))
	}
	names := map[string]bool{}
	for _, fn := range views {
		names[fn.Name] = true
	}
	if !names["balanceOf"] || !names["getResults"] {
		t.Fatal("expected balanceOf and getResults")
	}
}

func TestEstimateGas(t *testing.T) {
	ce := NewContractEngine(nil)
	deployTestContract(ce, "token-1")

	gas, err := ce.EstimateGas(context.Background(), "token-1", "balanceOf",
		map[string]interface{}{"address": "x"})
	if err != nil {
		t.Fatalf("EstimateGas: %v", err)
	}
	if gas <= 0 {
		t.Fatal("expected positive gas estimate")
	}
}

func TestQueryContract_ViewResultTimestamp(t *testing.T) {
	ce := NewContractEngine(nil)
	deployTestContract(ce, "token-1")

	before := time.Now()
	result, _ := ce.QueryContract(context.Background(), "token-1", "balanceOf",
		map[string]interface{}{"address": "x"})
	if result.Timestamp.Before(before) {
		t.Fatal("timestamp should be recent")
	}
}

func TestListViewFunctions_NoABI(t *testing.T) {
	ce := NewContractEngine(nil)
	ce.DeployContract(context.Background(), "raw", []byte("sim"), nil, "owner")

	_, err := ce.ListViewFunctions("raw")
	if err == nil {
		// nil ABI means findFunction will fail, but ListViewFunctions handles nil ABI
		t.Log("nil ABI correctly returns empty list or error")
	}
}

func TestQueryContract_GetResults(t *testing.T) {
	ce := NewContractEngine(nil)
	deployTestContract(ce, "vote-1")

	// Cast some votes
	ce.ExecuteContract(context.Background(), "vote-1", "vote",
		map[string]interface{}{"proposal": "p1", "choice": true})
	ce.ExecuteContract(context.Background(), "vote-1", "vote",
		map[string]interface{}{"proposal": "p1", "choice": false})

	// Query results read-only
	result, err := ce.QueryContract(context.Background(), "vote-1", "getResults",
		map[string]interface{}{"proposal": "p1"})
	if err != nil {
		t.Fatalf("QueryContract getResults: %v", err)
	}
	out := result.Output.(map[string]interface{})
	yes, _ := out["yes"].(float64)
	no, _ := out["no"].(float64)
	if yes != 1 || no != 1 {
		t.Fatalf("expected 1 yes + 1 no, got %f/%f", yes, no)
	}
}
