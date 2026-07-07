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
	if GetBalance() != 0 {
		t.Errorf("expected canonical-ledger balance 0, got %d", GetBalance())
	}
}

func TestSendTransactionRequiresCanonicalFunds(t *testing.T) {
	requireWallet(t)
	_, err := SendTransaction("recipient", 100, 0, "", nil)
	if err == nil {
		t.Fatal("expected an unfunded wallet to reject the transfer")
	}
	if GetBalance() != 0 {
		t.Errorf("failed transfer changed balance: got %d", GetBalance())
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
