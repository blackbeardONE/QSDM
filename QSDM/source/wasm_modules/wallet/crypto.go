//go:build ignore
// +build ignore

// Legacy liboqs-go experiment — excluded from normal builds (use wasm_modules/wallet/walletcrypto).
package wallet

import (
	"errors"
	"fmt"
	oqs "github.com/open-quantum-safe/liboqs-go/oqs"
)

type KeyPair struct {
	PrivateKey []byte
	PublicKey  []byte
	signer     *oqs.Signer
}

// GenerateKeyPair generates a new ML-DSA (FIPS 204) key pair using liboqs
// NOTE: Dilithium was removed in liboqs 0.15.0+, replaced by ML-DSA
// Using ML-DSA-87 (256-bit security, equivalent to Dilithium5)
// Highest security level - maximum quantum resistance
func GenerateKeyPair() (*KeyPair, error) {
	signer, err := oqs.NewSigner("ML-DSA-87")
	if err != nil {
		return nil, fmt.Errorf("failed to create signer: %w", err)
	}
	pubKey := signer.PublicKey()
	privKey := signer.PrivateKey()
	return &KeyPair{
		PrivateKey: privKey,
		PublicKey:  pubKey,
		signer:     signer,
	}, nil
}

// Sign signs the given message with the private key
func (kp *KeyPair) Sign(message []byte) ([]byte, error) {
	if kp == nil || kp.signer == nil {
		return nil, errors.New("invalid key pair or signer")
	}
	signature, err := kp.signer.Sign(message)
	if err != nil {
		return nil, fmt.Errorf("signing failed: %w", err)
	}
	return signature, nil
}

// Verify verifies the signature of the message using the public key
func (kp *KeyPair) Verify(message []byte, signature []byte) (bool, error) {
	verifier, err := oqs.NewVerifier("ML-DSA-87")
	if err != nil {
		return false, fmt.Errorf("failed to create verifier: %w", err)
	}
	defer verifier.Clean()
	valid, err := verifier.Verify(message, signature)
	if err != nil {
		return false, fmt.Errorf("verification failed: %w", err)
	}
	return valid, nil
}
