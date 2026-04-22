//go:build !cgo
// +build !cgo

package wallet

import (
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestNewWalletService(t *testing.T) {
	ws, err := NewWalletService()
	if err != nil {
		t.Fatalf("NewWalletService() error: %v", err)
	}
	if ws == nil {
		t.Fatal("expected non-nil WalletService")
	}
	addr := ws.GetAddress()
	if addr == "" {
		t.Fatal("address must not be empty")
	}
	if _, err := hex.DecodeString(addr); err != nil {
		t.Fatalf("address is not valid hex: %v", err)
	}
}

func TestGetAddressIsDeterministic(t *testing.T) {
	ws, _ := NewWalletService()
	a1 := ws.GetAddress()
	a2 := ws.GetAddress()
	if a1 != a2 {
		t.Fatalf("address changed between calls: %s vs %s", a1, a2)
	}
}

func TestTwoWalletsHaveDifferentAddresses(t *testing.T) {
	ws1, _ := NewWalletService()
	ws2, _ := NewWalletService()
	if ws1.GetAddress() == ws2.GetAddress() {
		t.Fatal("two wallets should have different addresses")
	}
}

func TestCreateTransaction(t *testing.T) {
	ws, _ := NewWalletService()
	txBytes, err := ws.CreateTransaction("recipient_abc", 10, 0.01, "US", []string{"p1", "p2"})
	if err != nil {
		t.Fatalf("CreateTransaction error: %v", err)
	}

	var txData map[string]interface{}
	if err := json.Unmarshal(txBytes, &txData); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if txData["sender"] != ws.GetAddress() {
		t.Errorf("sender = %v, want %s", txData["sender"], ws.GetAddress())
	}
	if txData["recipient"] != "recipient_abc" {
		t.Errorf("recipient = %v, want recipient_abc", txData["recipient"])
	}
	if txData["signature"] == nil || txData["signature"] == "" {
		t.Error("expected non-empty signature")
	}
}

func TestSignAndVerify(t *testing.T) {
	ws, _ := NewWalletService()
	data := []byte("test payload")

	sig, err := ws.SignData(data)
	if err != nil {
		t.Fatalf("SignData error: %v", err)
	}
	if len(sig) == 0 {
		t.Fatal("signature must not be empty")
	}

	ok, err := ws.VerifySignature(data, sig, nil)
	if err != nil {
		t.Fatalf("VerifySignature error: %v", err)
	}
	if !ok {
		t.Error("expected signature to verify")
	}
}

func TestDecodeEncodeAddress(t *testing.T) {
	original := []byte{0xab, 0xcd, 0xef, 0x01}
	encoded := EncodeAddress(original)
	decoded, err := DecodeAddress(encoded)
	if err != nil {
		t.Fatalf("DecodeAddress error: %v", err)
	}
	if hex.EncodeToString(decoded) != hex.EncodeToString(original) {
		t.Fatalf("round-trip failed: got %x, want %x", decoded, original)
	}
}
