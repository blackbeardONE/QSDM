package contracts

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoadContracts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "contracts.json")

	engine := NewContractEngine(nil)
	ctx := context.Background()
	tmpl, _ := GetTemplate("SimpleToken")

	engine.DeployContract(ctx, "persist_tok", tmpl.Code, tmpl.ABI, "alice")
	engine.ExecuteContract(ctx, "persist_tok", "transfer", map[string]interface{}{"to": "bob", "amount": 50})

	if err := engine.SaveContracts(path); err != nil {
		t.Fatalf("SaveContracts: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("contracts file not created: %v", err)
	}

	engine2 := NewContractEngine(nil)
	loaded, err := engine2.LoadContracts(path)
	if err != nil {
		t.Fatalf("LoadContracts: %v", err)
	}
	if loaded != 1 {
		t.Fatalf("loaded = %d, want 1", loaded)
	}

	c, err := engine2.GetContract("persist_tok")
	if err != nil {
		t.Fatalf("GetContract: %v", err)
	}
	if c.Owner != "alice" {
		t.Errorf("Owner = %s, want alice", c.Owner)
	}
	if c.GasUsedDeploy <= 0 {
		t.Error("expected deployment gas to be persisted")
	}
}

func TestLoadContracts_FileNotExist(t *testing.T) {
	engine := NewContractEngine(nil)
	loaded, err := engine.LoadContracts("/nonexistent/path/contracts.json")
	if err != nil {
		t.Fatalf("LoadContracts should return nil for missing file: %v", err)
	}
	if loaded != 0 {
		t.Fatalf("loaded = %d, want 0", loaded)
	}
}

func TestLoadContracts_NoDuplicates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "contracts.json")

	engine := NewContractEngine(nil)
	ctx := context.Background()
	tmpl, _ := GetTemplate("Escrow")
	engine.DeployContract(ctx, "esc_dup", tmpl.Code, tmpl.ABI, "owner")
	engine.SaveContracts(path)

	loaded, _ := engine.LoadContracts(path)
	if loaded != 0 {
		t.Fatalf("should not re-load existing contracts; loaded = %d", loaded)
	}
}

func TestSaveLoadContracts_StatePreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "contracts.json")

	engine := NewContractEngine(nil)
	ctx := context.Background()
	tmpl, _ := GetTemplate("SimpleToken")
	engine.DeployContract(ctx, "state_tok", tmpl.Code, tmpl.ABI, "owner")
	engine.ExecuteContract(ctx, "state_tok", "transfer", map[string]interface{}{"to": "alice", "amount": 100})
	engine.ExecuteContract(ctx, "state_tok", "transfer", map[string]interface{}{"to": "alice", "amount": 50})
	engine.SaveContracts(path)

	engine2 := NewContractEngine(nil)
	engine2.LoadContracts(path)

	result, err := engine2.ExecuteContract(ctx, "state_tok", "balanceOf", map[string]interface{}{"address": "alice"})
	if err != nil {
		t.Fatalf("balanceOf after load: %v", err)
	}
	out := result.Output.(map[string]interface{})
	if out["balance"] != float64(150) {
		t.Errorf("balance = %v, want 150", out["balance"])
	}
}

func TestContractAutoSaver(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "contracts.json")

	engine := NewContractEngine(nil)
	ctx := context.Background()
	tmpl, _ := GetTemplate("Voting")
	engine.DeployContract(ctx, "auto_vote", tmpl.Code, tmpl.ABI, "owner")

	saver := NewContractAutoSaver(engine, path, 100*time.Millisecond)
	saver.Start()
	time.Sleep(250 * time.Millisecond)
	saver.Stop()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("auto-save file not created: %v", err)
	}

	engine2 := NewContractEngine(nil)
	loaded, _ := engine2.LoadContracts(path)
	if loaded != 1 {
		t.Fatalf("loaded = %d, want 1", loaded)
	}
}
