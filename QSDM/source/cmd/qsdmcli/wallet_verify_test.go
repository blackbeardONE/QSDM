package main

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

func TestVerifyMLDSA87Signature(t *testing.T) {
	publicKey, privateKey, err := mldsa87.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	publicKeyBytes, err := publicKey.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	message := []byte("qsdm release manifest fixture\n")
	signature := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(privateKey, message, nil, true, signature); err != nil {
		t.Fatal(err)
	}

	if err := verifyMLDSA87Signature(
		hex.EncodeToString(publicKeyBytes),
		hex.EncodeToString(signature),
		message,
	); err != nil {
		t.Fatalf("fresh signature was rejected: %v", err)
	}

	tampered := append([]byte(nil), message...)
	tampered[0] ^= 0xff
	if err := verifyMLDSA87Signature(
		hex.EncodeToString(publicKeyBytes),
		hex.EncodeToString(signature),
		tampered,
	); err == nil || !strings.Contains(err.Error(), "verification failed") {
		t.Fatalf("tampered message was not rejected: %v", err)
	}
}

func TestVerifyMLDSA87SignatureRejectsWrongSizes(t *testing.T) {
	if err := verifyMLDSA87Signature("00", "00", []byte("manifest")); err == nil ||
		!strings.Contains(err.Error(), "public key must be") {
		t.Fatalf("short public key was not rejected: %v", err)
	}
}
