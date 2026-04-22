package contracts

import (
	"context"
	"testing"
	"time"
)

func TestRentManager_Register(t *testing.T) {
	engine := NewContractEngine(nil)
	ctx := context.Background()
	abi := &ABI{Functions: []Function{{Name: "f1"}}}
	engine.DeployContract(ctx, "c1", []byte{0x01, 0x02, 0x03}, abi, "alice")

	rm := NewRentManager(engine, DefaultRentConfig())
	if err := rm.RegisterContract("c1", 1.0); err != nil {
		t.Fatalf("RegisterContract: %v", err)
	}

	acc, ok := rm.GetAccount("c1")
	if !ok {
		t.Fatal("expected account")
	}
	if acc.Deposit != 1.0 {
		t.Fatalf("expected deposit 1.0, got %f", acc.Deposit)
	}
	if acc.StorageBytes <= 0 {
		t.Fatal("expected positive storage bytes")
	}
}

func TestRentManager_MinDeposit(t *testing.T) {
	engine := NewContractEngine(nil)
	ctx := context.Background()
	engine.DeployContract(ctx, "c1", []byte{0x01}, &ABI{}, "alice")

	rm := NewRentManager(engine, DefaultRentConfig())
	err := rm.RegisterContract("c1", 0.000001)
	if err == nil {
		t.Fatal("expected error for deposit below minimum")
	}
}

func TestRentManager_ChargeAll(t *testing.T) {
	engine := NewContractEngine(nil)
	ctx := context.Background()
	engine.DeployContract(ctx, "c1", make([]byte, 1000), &ABI{}, "alice")

	cfg := DefaultRentConfig()
	cfg.CostPerBytePerDay = 0.001
	rm := NewRentManager(engine, cfg)
	rm.RegisterContract("c1", 10.0)

	// Backdate to force charge
	rm.mu.Lock()
	rm.accounts["c1"].LastChargedAt = time.Now().Add(-24 * time.Hour)
	rm.mu.Unlock()

	charged, evicted := rm.ChargeAll()
	if charged != 1 {
		t.Fatalf("expected 1 charged, got %d", charged)
	}
	if evicted != 0 {
		t.Fatalf("expected 0 evicted, got %d", evicted)
	}

	acc, _ := rm.GetAccount("c1")
	if acc.Deposit >= 10.0 {
		t.Fatal("deposit should have decreased after charge")
	}
	if acc.TotalCharged <= 0 {
		t.Fatal("total charged should be positive")
	}
}

func TestRentManager_GracePeriod(t *testing.T) {
	engine := NewContractEngine(nil)
	ctx := context.Background()
	engine.DeployContract(ctx, "c1", make([]byte, 1000), &ABI{}, "alice")

	cfg := DefaultRentConfig()
	cfg.CostPerBytePerDay = 100.0 // very expensive
	cfg.GracePeriod = 1 * time.Millisecond
	rm := NewRentManager(engine, cfg)
	rm.RegisterContract("c1", 0.01) // tiny deposit

	rm.mu.Lock()
	rm.accounts["c1"].LastChargedAt = time.Now().Add(-48 * time.Hour)
	rm.mu.Unlock()

	// First charge: enters grace (deposit exhausted)
	rm.ChargeAll()
	acc, _ := rm.GetAccount("c1")
	if acc.GraceStart.IsZero() {
		t.Fatal("expected grace period to start")
	}

	time.Sleep(5 * time.Millisecond)

	// Backdate grace start so it's well past grace period
	rm.mu.Lock()
	rm.accounts["c1"].GraceStart = time.Now().Add(-time.Hour)
	rm.accounts["c1"].LastChargedAt = time.Now().Add(-time.Hour)
	rm.mu.Unlock()

	// Second charge after grace expires: eviction
	_, evicted := rm.ChargeAll()
	if evicted != 1 {
		t.Fatalf("expected 1 eviction, got %d", evicted)
	}
	acc, _ = rm.GetAccount("c1")
	if !acc.Evicted {
		t.Fatal("contract should be evicted")
	}
}

func TestRentManager_TopUp(t *testing.T) {
	engine := NewContractEngine(nil)
	ctx := context.Background()
	engine.DeployContract(ctx, "c1", []byte{0x01}, &ABI{}, "alice")

	rm := NewRentManager(engine, DefaultRentConfig())
	rm.RegisterContract("c1", 1.0)

	if err := rm.TopUp("c1", 5.0); err != nil {
		t.Fatalf("TopUp: %v", err)
	}
	acc, _ := rm.GetAccount("c1")
	if acc.Deposit != 6.0 {
		t.Fatalf("expected 6.0 after top-up, got %f", acc.Deposit)
	}
}

func TestRentManager_TopUpClearsGrace(t *testing.T) {
	engine := NewContractEngine(nil)
	ctx := context.Background()
	engine.DeployContract(ctx, "c1", make([]byte, 1000), &ABI{}, "alice")

	cfg := DefaultRentConfig()
	cfg.CostPerBytePerDay = 100.0
	rm := NewRentManager(engine, cfg)
	rm.RegisterContract("c1", 0.01)

	rm.mu.Lock()
	rm.accounts["c1"].LastChargedAt = time.Now().Add(-48 * time.Hour)
	rm.mu.Unlock()
	rm.ChargeAll()

	acc, _ := rm.GetAccount("c1")
	if acc.GraceStart.IsZero() {
		t.Fatal("should be in grace")
	}

	rm.TopUp("c1", 1000.0)
	acc, _ = rm.GetAccount("c1")
	if !acc.GraceStart.IsZero() {
		t.Fatal("top-up should clear grace")
	}
}

func TestRentManager_EvictedCannotTopUp(t *testing.T) {
	engine := NewContractEngine(nil)
	ctx := context.Background()
	engine.DeployContract(ctx, "c1", []byte{0x01}, &ABI{}, "alice")

	rm := NewRentManager(engine, DefaultRentConfig())
	rm.RegisterContract("c1", 1.0)

	rm.mu.Lock()
	rm.accounts["c1"].Evicted = true
	rm.mu.Unlock()

	if err := rm.TopUp("c1", 5.0); err == nil {
		t.Fatal("should not allow top-up on evicted contract")
	}
}

func TestRentManager_InGrace(t *testing.T) {
	engine := NewContractEngine(nil)
	ctx := context.Background()
	engine.DeployContract(ctx, "c1", make([]byte, 100), &ABI{}, "alice")
	engine.DeployContract(ctx, "c2", make([]byte, 100), &ABI{}, "bob")

	cfg := DefaultRentConfig()
	cfg.CostPerBytePerDay = 100.0
	cfg.GracePeriod = time.Hour
	rm := NewRentManager(engine, cfg)
	rm.RegisterContract("c1", 0.01)
	rm.RegisterContract("c2", 999999.0) // large enough to never exhaust

	rm.mu.Lock()
	rm.accounts["c1"].LastChargedAt = time.Now().Add(-48 * time.Hour)
	rm.accounts["c2"].LastChargedAt = time.Now().Add(-48 * time.Hour)
	rm.mu.Unlock()

	rm.ChargeAll()
	inGrace := rm.InGrace()
	if len(inGrace) != 1 {
		t.Fatalf("expected 1 in grace, got %d", len(inGrace))
	}
	if inGrace[0].ContractID != "c1" {
		t.Fatalf("expected c1 in grace, got %s", inGrace[0].ContractID)
	}
}

func TestRentManager_ListAccounts(t *testing.T) {
	engine := NewContractEngine(nil)
	ctx := context.Background()
	engine.DeployContract(ctx, "c1", []byte{0x01}, &ABI{}, "a")
	engine.DeployContract(ctx, "c2", []byte{0x02}, &ABI{}, "b")

	rm := NewRentManager(engine, DefaultRentConfig())
	rm.RegisterContract("c1", 1.0)
	rm.RegisterContract("c2", 2.0)

	list := rm.ListAccounts()
	if len(list) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(list))
	}
}
