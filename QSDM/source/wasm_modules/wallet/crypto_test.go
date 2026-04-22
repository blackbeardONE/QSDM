//go:build ignore
// +build ignore

package wallet

import (
	"testing"
)

func TestGenerateKeyPair(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}
	if kp == nil || len(kp.PrivateKey) == 0 || len(kp.PublicKey) == 0 {
		t.Fatal("Invalid key pair generated")
	}
}

func TestSignAndVerify(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}
	message := []byte("test message")
	signature, err := kp.Sign(message)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}
	valid, err := kp.Verify(message, signature)
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	if !valid {
		t.Fatal("Signature verification failed")
	}
}
