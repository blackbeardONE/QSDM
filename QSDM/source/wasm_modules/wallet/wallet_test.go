package wallet

import (
	"testing"

	"github.com/blackbeardONE/QSDM/wasm_modules/wallet/walletcore"
)

func requireWalletcore(t *testing.T) {
	t.Helper()
	if walletcore.GetAddress() == "" {
		t.Skip("walletcore not initialized (walletcrypto has no backend in this build)")
	}
}

func TestWalletBalance(t *testing.T) {
	requireWalletcore(t)
	if walletcore.GetBalance() != 1000 {
		t.Errorf("expected initial balance 1000, got %d", walletcore.GetBalance())
	}
}

func TestSendTransaction(t *testing.T) {
	requireWalletcore(t)
	_, err := walletcore.SendTransaction("recipient-address", 100, 0, "", nil)
	if err != nil {
		t.Fatalf("SendTransaction: %v", err)
	}
	if walletcore.GetBalance() != 900 {
		t.Errorf("expected balance 900 after send, got %d", walletcore.GetBalance())
	}
}

func TestSendTransactionInsufficientFunds(t *testing.T) {
	requireWalletcore(t)
	_, err := walletcore.SendTransaction("recipient-address", 10000, 0, "", nil)
	if err == nil {
		t.Fatal("expected error for insufficient funds")
	}
}

func TestSignAndVerify(t *testing.T) {
	requireWalletcore(t)
	message := []byte("test message")
	signature, err := walletcore.SignTransaction(message)
	if err != nil {
		t.Fatalf("SignTransaction: %v", err)
	}
	keyPair := walletcore.GetKeyPair()
	if keyPair == nil {
		t.Fatal("KeyPair is nil")
	}
	valid, err := keyPair.Verify(message, signature)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !valid {
		t.Fatal("signature verification failed")
	}
}
