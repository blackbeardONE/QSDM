package contracts

import (
	"context"
	"testing"
)

func TestUpgradeManager_BasicUpgrade(t *testing.T) {
	engine := NewContractEngine(nil)
	ctx := context.Background()
	abi1 := &ABI{Functions: []Function{{Name: "transfer", Inputs: []Param{{Name: "to", Type: "string"}}}}}
	engine.DeployContract(ctx, "tok1", []byte{0x01}, abi1, "alice")

	um := NewUpgradeManager(engine)

	abi2 := &ABI{Functions: []Function{
		{Name: "transfer", Inputs: []Param{{Name: "to", Type: "string"}, {Name: "amount", Type: "uint64"}}},
		{Name: "approve", Inputs: []Param{{Name: "spender", Type: "string"}}},
	}}
	newCode := []byte{0x02}

	contract, err := um.Upgrade(ctx, "tok1", newCode, abi2, "alice", "added approve function")
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if len(contract.ABI.Functions) != 2 {
		t.Fatalf("expected 2 functions after upgrade, got %d", len(contract.ABI.Functions))
	}
	if contract.Code[0] != 0x02 {
		t.Fatal("code should be updated")
	}
}

func TestUpgradeManager_PreservesState(t *testing.T) {
	engine := NewContractEngine(nil)
	ctx := context.Background()
	abi := &ABI{Functions: []Function{
		{Name: "transfer", Inputs: []Param{{Name: "to", Type: "string"}, {Name: "amount", Type: "uint64"}}, StateMutating: true},
		{Name: "balanceOf", Inputs: []Param{{Name: "address", Type: "string"}}},
	}}
	engine.DeployContract(ctx, "tok1", []byte{0x01}, abi, "alice")

	engine.ExecuteContract(ctx, "tok1", "transfer", map[string]interface{}{"to": "bob", "amount": 100})

	um := NewUpgradeManager(engine)
	abiV2 := &ABI{Functions: []Function{
		{Name: "transfer", Inputs: []Param{{Name: "to", Type: "string"}, {Name: "amount", Type: "uint64"}}, StateMutating: true},
		{Name: "balanceOf", Inputs: []Param{{Name: "address", Type: "string"}}},
	}}
	um.Upgrade(ctx, "tok1", []byte{0x02}, abiV2, "alice", "v2")

	result, err := engine.ExecuteContract(ctx, "tok1", "balanceOf", map[string]interface{}{"address": "bob"})
	if err != nil {
		t.Fatalf("balanceOf: %v", err)
	}
	out := result.Output.(map[string]interface{})
	if out["balance"].(float64) != 100 {
		t.Fatalf("expected balance 100 preserved after upgrade, got %v", out["balance"])
	}
}

func TestUpgradeManager_VersionHistory(t *testing.T) {
	engine := NewContractEngine(nil)
	ctx := context.Background()
	abi := &ABI{Functions: []Function{{Name: "f1"}}}
	engine.DeployContract(ctx, "c1", []byte{0x01}, abi, "alice")

	um := NewUpgradeManager(engine)
	um.Upgrade(ctx, "c1", []byte{0x02}, &ABI{Functions: []Function{{Name: "f2"}}}, "alice", "v2")
	um.Upgrade(ctx, "c1", []byte{0x03}, &ABI{Functions: []Function{{Name: "f3"}}}, "alice", "v3")

	history := um.VersionHistory("c1")
	if len(history) != 2 {
		t.Fatalf("expected 2 versions in history, got %d", len(history))
	}
	if history[0].Version != 1 {
		t.Fatalf("first archived version should be 1, got %d", history[0].Version)
	}
	if um.CurrentVersion("c1") != 3 {
		t.Fatalf("expected current version 3, got %d", um.CurrentVersion("c1"))
	}
}

func TestUpgradeManager_UnauthorisedUpgrade(t *testing.T) {
	engine := NewContractEngine(nil)
	ctx := context.Background()
	abi := &ABI{Functions: []Function{{Name: "f1"}}}
	engine.DeployContract(ctx, "c1", []byte{0x01}, abi, "alice")

	um := NewUpgradeManager(engine)
	_, err := um.Upgrade(ctx, "c1", []byte{0x02}, abi, "mallory", "hack")
	if err == nil {
		t.Fatal("expected error for unauthorised upgrader")
	}
}

func TestUpgradeManager_AllowedUpgraders(t *testing.T) {
	engine := NewContractEngine(nil)
	ctx := context.Background()
	abi := &ABI{Functions: []Function{{Name: "f1"}}}
	engine.DeployContract(ctx, "c1", []byte{0x01}, abi, "alice")

	um := NewUpgradeManager(engine)
	um.SetPolicy("c1", UpgradePolicy{
		AllowOwnerUpgrade: true,
		AllowedUpgraders:  []string{"bob"},
	})

	_, err := um.Upgrade(ctx, "c1", []byte{0x02}, abi, "bob", "authorised")
	if err != nil {
		t.Fatalf("bob should be allowed: %v", err)
	}
}

func TestUpgradeManager_FreezePolicy(t *testing.T) {
	engine := NewContractEngine(nil)
	ctx := context.Background()
	abi := &ABI{Functions: []Function{{Name: "f1"}}}
	engine.DeployContract(ctx, "c1", []byte{0x01}, abi, "alice")

	um := NewUpgradeManager(engine)
	um.SetPolicy("c1", UpgradePolicy{
		AllowOwnerUpgrade: true,
		FreezeAfterV:      2,
	})

	// v1 -> v2 allowed
	_, err := um.Upgrade(ctx, "c1", []byte{0x02}, abi, "alice", "v2")
	if err != nil {
		t.Fatalf("v2 should be allowed: %v", err)
	}
	// v2 -> v3 blocked by freeze
	_, err = um.Upgrade(ctx, "c1", []byte{0x03}, abi, "alice", "v3")
	if err == nil {
		t.Fatal("expected freeze error")
	}
}

func TestUpgradeManager_Rollback(t *testing.T) {
	engine := NewContractEngine(nil)
	ctx := context.Background()
	abi1 := &ABI{Functions: []Function{{Name: "f1"}}}
	abi2 := &ABI{Functions: []Function{{Name: "f1"}, {Name: "f2"}}}
	engine.DeployContract(ctx, "c1", []byte{0x01}, abi1, "alice")

	um := NewUpgradeManager(engine)
	um.Upgrade(ctx, "c1", []byte{0x02}, abi2, "alice", "v2")

	contract, err := um.Rollback(ctx, "c1", 1, "alice")
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if len(contract.ABI.Functions) != 1 {
		t.Fatalf("expected 1 function after rollback, got %d", len(contract.ABI.Functions))
	}
}

func TestUpgradeManager_RollbackNonexistentVersion(t *testing.T) {
	engine := NewContractEngine(nil)
	ctx := context.Background()
	abi := &ABI{Functions: []Function{{Name: "f1"}}}
	engine.DeployContract(ctx, "c1", []byte{0x01}, abi, "alice")

	um := NewUpgradeManager(engine)
	_, err := um.Rollback(ctx, "c1", 99, "alice")
	if err == nil {
		t.Fatal("expected error for nonexistent version")
	}
}
