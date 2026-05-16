package bridge

import (
	"context"
	"testing"
	"time"
)

func TestLockAndRedeemAsset(t *testing.T) {
	bp, err := NewBridgeProtocol()
	if err != nil {
		t.Skipf("Bridge needs Dilithium (CGO): %v", err)
	}

	ctx := context.Background()
	lock, err := bp.LockAsset(ctx, "qsdm", "ethereum", "QSD", 100.0, "0xRecipient", 1*time.Hour)
	if err != nil {
		t.Fatalf("LockAsset: %v", err)
	}
	if lock.Status != LockStatusLocked {
		t.Fatalf("status = %s, want locked", lock.Status)
	}

	if err := bp.RedeemAsset(ctx, lock.ID, lock.Secret); err != nil {
		t.Fatalf("RedeemAsset: %v", err)
	}

	got, _ := bp.GetLock(lock.ID)
	if got.Status != LockStatusRedeemed {
		t.Fatalf("status = %s, want redeemed", got.Status)
	}
}

func TestRedeemWithWrongSecret(t *testing.T) {
	bp, err := NewBridgeProtocol()
	if err != nil {
		t.Skipf("Bridge needs Dilithium (CGO): %v", err)
	}
	ctx := context.Background()
	lock, _ := bp.LockAsset(ctx, "qsdm", "bitcoin", "QSD", 50.0, "bc1...", 1*time.Hour)

	if err := bp.RedeemAsset(ctx, lock.ID, "wrong_secret"); err == nil {
		t.Fatal("expected error for wrong secret")
	}
}

func TestRefundBeforeExpiry(t *testing.T) {
	bp, err := NewBridgeProtocol()
	if err != nil {
		t.Skipf("Bridge needs Dilithium (CGO): %v", err)
	}
	ctx := context.Background()
	lock, _ := bp.LockAsset(ctx, "qsdm", "ethereum", "QSD", 10.0, "0xR", 1*time.Hour)

	if err := bp.RefundAsset(ctx, lock.ID); err == nil {
		t.Fatal("expected error: lock has not expired yet")
	}
}

// TestRedeemAfterExpiry pins the OTHER half of the lock-expiry gate
// (audit row bridge-02): a redeem with the CORRECT secret must still
// fail if the lock has already expired. Without this we'd only be
// testing the "wrong secret" rejection path; the time-window invariant
// would slip unobserved if a refactor accidentally let RedeemAsset
// skip the time.Now().After(ExpiresAt) check in protocol.go:126.
//
// We give the lock a 1ns expiry and sleep 5ms before redeeming —
// avoids any monotonic-clock race while still completing in well
// under a second. The status transition to LockStatusExpired is also
// asserted so a future refactor that removed the status-update line
// but kept the error return would be caught.
func TestRedeemAfterExpiry(t *testing.T) {
	bp, err := NewBridgeProtocol()
	if err != nil {
		t.Skipf("Bridge needs Dilithium (CGO): %v", err)
	}
	ctx := context.Background()
	lock, err := bp.LockAsset(ctx, "qsdm", "ethereum", "QSD", 10.0, "0xR", 1*time.Nanosecond)
	if err != nil {
		t.Fatalf("LockAsset: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	if err := bp.RedeemAsset(ctx, lock.ID, lock.Secret); err == nil {
		t.Fatal("expected error: lock has expired (redeem-after-expiry must fail)")
	}
	got, _ := bp.GetLock(lock.ID)
	if got.Status != LockStatusExpired {
		t.Fatalf("expected status=expired after rejected redeem, got %s", got.Status)
	}
}

func TestListLocks(t *testing.T) {
	bp, err := NewBridgeProtocol()
	if err != nil {
		t.Skipf("Bridge needs Dilithium (CGO): %v", err)
	}
	ctx := context.Background()
	bp.LockAsset(ctx, "a", "b", "QSD", 1, "r", 1*time.Hour)
	bp.LockAsset(ctx, "c", "d", "QSD", 2, "s", 1*time.Hour)

	if len(bp.ListLocks()) != 2 {
		t.Fatalf("expected 2 locks, got %d", len(bp.ListLocks()))
	}
}

func TestAtomicSwapFullCycle(t *testing.T) {
	asp, err := NewAtomicSwapProtocol()
	if err != nil {
		t.Skipf("AtomicSwap needs Dilithium (CGO): %v", err)
	}
	ctx := context.Background()

	swap, err := asp.InitiateSwap(ctx, "qsdm", "ethereum", "QSD", "ETH", 100, 0.05, "alice", "bob", 1*time.Hour)
	if err != nil {
		t.Fatalf("InitiateSwap: %v", err)
	}
	if swap.Status != SwapStatusInitiated {
		t.Fatalf("status = %s, want initiated", swap.Status)
	}

	swap, err = asp.ParticipateInSwap(ctx, swap.ID)
	if err != nil {
		t.Fatalf("ParticipateInSwap: %v", err)
	}
	if swap.Status != SwapStatusParticipated {
		t.Fatalf("status = %s, want participated", swap.Status)
	}

	if err := asp.CompleteSwap(ctx, swap.ID, swap.InitiatorSecret); err != nil {
		t.Fatalf("CompleteSwap: %v", err)
	}

	got, _ := asp.GetSwap(swap.ID)
	if got.Status != SwapStatusCompleted {
		t.Fatalf("status = %s, want completed", got.Status)
	}
}

func TestAtomicSwapWrongSecret(t *testing.T) {
	asp, err := NewAtomicSwapProtocol()
	if err != nil {
		t.Skipf("AtomicSwap needs Dilithium (CGO): %v", err)
	}
	ctx := context.Background()

	swap, _ := asp.InitiateSwap(ctx, "qsdm", "ethereum", "QSD", "ETH", 10, 0.01, "a", "b", 1*time.Hour)
	asp.ParticipateInSwap(ctx, swap.ID)

	if err := asp.CompleteSwap(ctx, swap.ID, "bad_secret"); err == nil {
		t.Fatal("expected error for wrong secret")
	}
}
