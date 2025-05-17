package consensus

import (
    "testing"
    "github.com/blackbeardONE/QSDM/internal/logging"
)

func TestProofOfEntanglement_SignAndValidate(t *testing.T) {
    logging.SetupLogger("test_poe.log")

    poe := NewProofOfEntanglement()
    message := []byte("test message")

    signature, err := poe.Sign(message)
    if err != nil {
        t.Fatalf("Sign failed: %v", err)
    }

    // Valid signature test
    valid, err := poe.ValidateTransaction(message, [][]byte{[]byte("parent1"), []byte("parent2")}, [][]byte{signature})
    if err != nil {
        t.Fatalf("ValidateTransaction failed: %v", err)
    }
    if !valid {
        t.Fatalf("ValidateTransaction returned false for valid signature")
    }

    // Invalid parent cells count
    _, err = poe.ValidateTransaction(message, [][]byte{[]byte("parent1")}, [][]byte{signature})
    if err == nil {
        t.Fatalf("ValidateTransaction did not fail for invalid parent cells count")
    }

    // No signatures provided
    _, err = poe.ValidateTransaction(message, [][]byte{[]byte("parent1"), []byte("parent2")}, [][]byte{})
    if err == nil {
        t.Fatalf("ValidateTransaction did not fail for no signatures")
    }

    // Invalid signature
    _, err = poe.ValidateTransaction(message, [][]byte{[]byte("parent1"), []byte("parent2")}, [][]byte{[]byte("invalid")})
    if err == nil {
        t.Fatalf("ValidateTransaction did not fail for invalid signature")
    }
}
