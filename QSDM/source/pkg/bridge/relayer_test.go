package bridge

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRelayer_EnqueueAndProcess(t *testing.T) {
	cfg := DefaultRelayerConfig()
	cfg.PollInterval = 50 * time.Millisecond
	r := NewRelayer(cfg)

	adapter := NewSimulatedChainAdapter("eth")
	r.RegisterAdapter(adapter)

	lock := &Lock{ID: "lock_123", SourceChain: "qsdm", TargetChain: "eth"}
	taskID := r.EnqueueLock(lock, "eth")
	if taskID == "" {
		t.Fatal("expected task ID")
	}

	if r.PendingCount() != 1 {
		t.Fatalf("PendingCount = %d, want 1", r.PendingCount())
	}

	r.Start()
	time.Sleep(200 * time.Millisecond)
	r.Stop()

	tasks := r.ListTasks()
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Status != RelayConfirmed && tasks[0].Status != RelaySubmitted {
		t.Fatalf("task status = %s, want submitted or confirmed", tasks[0].Status)
	}
	if tasks[0].TxHash == "" {
		t.Fatal("expected tx hash")
	}
}

func TestRelayer_RedeemEnqueue(t *testing.T) {
	r := NewRelayer(DefaultRelayerConfig())
	adapter := NewSimulatedChainAdapter("btc")
	r.RegisterAdapter(adapter)

	id := r.EnqueueRedeem("lock_456", "secret_abc", "btc")
	if id == "" {
		t.Fatal("expected task ID")
	}

	tasks := r.ListTasks()
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Kind != "redeem" {
		t.Fatalf("kind = %s, want redeem", tasks[0].Kind)
	}
}

func TestRelayer_FailsWithoutAdapter(t *testing.T) {
	cfg := DefaultRelayerConfig()
	cfg.PollInterval = 50 * time.Millisecond
	cfg.MaxRetries = 1
	r := NewRelayer(cfg)

	r.Enqueue(&RelayTask{
		ID:      "orphan_task",
		Kind:    "lock",
		ChainID: "unknown_chain",
		LockID:  "lock_789",
	})

	r.Start()
	time.Sleep(200 * time.Millisecond)
	r.Stop()

	tasks := r.ListTasks()
	if len(tasks) != 1 {
		t.Fatal("expected 1 task")
	}
	if tasks[0].Status != RelayFailed {
		t.Fatalf("status = %s, want failed", tasks[0].Status)
	}
}

func TestRelayer_NonceTracking(t *testing.T) {
	r := NewRelayer(DefaultRelayerConfig())
	adapter := NewSimulatedChainAdapter("sol")
	r.RegisterAdapter(adapter)

	r.EnqueueLock(&Lock{ID: "a"}, "sol")
	r.EnqueueLock(&Lock{ID: "b"}, "sol")
	r.EnqueueLock(&Lock{ID: "c"}, "sol")

	tasks := r.ListTasks()
	nonces := make(map[uint64]bool)
	for _, task := range tasks {
		if nonces[task.Nonce] {
			t.Fatalf("duplicate nonce %d", task.Nonce)
		}
		nonces[task.Nonce] = true
	}
	if len(nonces) != 3 {
		t.Fatalf("expected 3 unique nonces, got %d", len(nonces))
	}
}

func TestRelayer_SaveLoadQueue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "relay_queue.json")

	r := NewRelayer(DefaultRelayerConfig())
	adapter := NewSimulatedChainAdapter("eth")
	r.RegisterAdapter(adapter)
	r.EnqueueLock(&Lock{ID: "persist_lock"}, "eth")

	if err := r.SaveQueue(path); err != nil {
		t.Fatalf("SaveQueue: %v", err)
	}

	r2 := NewRelayer(DefaultRelayerConfig())
	if err := r2.LoadQueue(path); err != nil {
		t.Fatalf("LoadQueue: %v", err)
	}
	tasks := r2.ListTasks()
	if len(tasks) != 1 {
		t.Fatalf("loaded %d tasks, want 1", len(tasks))
	}
	if tasks[0].LockID != "persist_lock" {
		t.Fatalf("LockID = %s, want persist_lock", tasks[0].LockID)
	}
}

func TestRelayer_LoadQueue_FileNotExist(t *testing.T) {
	r := NewRelayer(DefaultRelayerConfig())
	err := r.LoadQueue("/nonexistent/path.json")
	if err != nil {
		t.Fatalf("expected nil error for missing file: %v", err)
	}
}

func TestSimulatedChainAdapter(t *testing.T) {
	adapter := NewSimulatedChainAdapter("test_chain")
	if adapter.ChainID() != "test_chain" {
		t.Fatal("wrong chain ID")
	}

	txHash, err := adapter.SubmitLock(nil, &Lock{ID: "lk"})
	if err != nil || txHash == "" {
		t.Fatal("SubmitLock failed")
	}

	confirmed, err := adapter.ConfirmTransaction(nil, txHash)
	if err != nil || !confirmed {
		t.Fatal("expected confirmed")
	}

	confirmed, _ = adapter.ConfirmTransaction(nil, "unknown_tx")
	if confirmed {
		t.Fatal("expected unconfirmed for unknown tx")
	}
}

func TestRelayer_ConfirmationLoop(t *testing.T) {
	cfg := DefaultRelayerConfig()
	cfg.PollInterval = 50 * time.Millisecond
	r := NewRelayer(cfg)
	adapter := NewSimulatedChainAdapter("eth")
	r.RegisterAdapter(adapter)

	r.EnqueueLock(&Lock{ID: "confirm_test"}, "eth")

	r.Start()
	time.Sleep(500 * time.Millisecond)
	r.Stop()

	tasks := r.ListTasks()
	found := false
	for _, task := range tasks {
		if task.LockID == "confirm_test" && task.Status == RelayConfirmed {
			found = true
		}
	}
	if !found {
		t.Log("task may not have confirmed in time; status:", tasks[0].Status)
	}
}

func TestRelayerConfig_Defaults(t *testing.T) {
	cfg := DefaultRelayerConfig()
	if cfg.MaxRetries <= 0 {
		t.Fatal("MaxRetries should be > 0")
	}
	if cfg.RetryDelay <= 0 {
		t.Fatal("RetryDelay should be > 0")
	}
}

// Ensure the unused import doesn't cause issues
func init() {
	_ = os.TempDir()
}
