package walletcore

import (
	"testing"
)

func requireWallet(t *testing.T) {
	t.Helper()
	if GetAddress() == "" {
		t.Skip("wallet not initialized (walletcrypto has no backend in this build)")
	}
}

func TestGetBalance(t *testing.T) {
	requireWallet(t)
	if GetBalance() != 1000 {
		t.Errorf("expected balance 1000, got %d", GetBalance())
	}
}

func TestSendTransaction(t *testing.T) {
	requireWallet(t)
	_, err := SendTransaction("recipient", 100, 0, "", nil)
	if err != nil {
		t.Fatalf("SendTransaction: %v", err)
	}
}

func TestSignTransaction(t *testing.T) {
	requireWallet(t)
	signature, err := SignTransaction([]byte("data"))
	if err != nil {
		t.Fatalf("SignTransaction: %v", err)
	}
	if len(signature) == 0 {
		t.Fatal("expected non-empty signature")
	}
}
