package keystore

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// genKeypair produces a real ML-DSA-87 keypair for the round-trip test.
// Uses circl directly so the test stays independent of pkg/crypto (whose
// CGO-vs-Circl build tags don't bite us here — pkg/keystore is byte-only).
func genKeypair(t *testing.T) (pub, priv []byte) {
	t.Helper()
	pk, sk, err := mldsa87.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("mldsa87.GenerateKey: %v", err)
	}
	pubB, err := pk.MarshalBinary()
	if err != nil {
		t.Fatalf("pk.MarshalBinary: %v", err)
	}
	privB, err := sk.MarshalBinary()
	if err != nil {
		t.Fatalf("sk.MarshalBinary: %v", err)
	}
	return pubB, privB
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	pub, priv := genKeypair(t)
	pass := []byte("a strong passphrase you have to remember")

	ks, err := Encrypt(pub, priv, pass)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if err := Validate(ks); err != nil {
		t.Fatalf("Validate on freshly-Encrypted keystore: %v", err)
	}
	got, err := Decrypt(ks, pass)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, priv) {
		t.Fatalf("decrypted private key differs (got %d bytes, want %d)", len(got), len(priv))
	}
}

func TestDecryptWrongPassphrase(t *testing.T) {
	pub, priv := genKeypair(t)
	ks, err := Encrypt(pub, priv, []byte("right"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := Decrypt(ks, []byte("wrong")); err != ErrInvalidPassphrase {
		t.Fatalf("expected ErrInvalidPassphrase for wrong passphrase, got %v", err)
	}
}

func TestEmptyPassphraseRefused(t *testing.T) {
	pub, priv := genKeypair(t)
	if _, err := Encrypt(pub, priv, nil); err == nil {
		t.Fatalf("Encrypt with nil passphrase should fail")
	}
	if _, err := Encrypt(pub, priv, []byte{}); err == nil {
		t.Fatalf("Encrypt with empty passphrase should fail")
	}
}

func TestPublicKeyLengthEnforced(t *testing.T) {
	_, priv := genKeypair(t)
	short := make([]byte, PublicKeySize-1)
	if _, err := Encrypt(short, priv, []byte("p")); err == nil {
		t.Fatalf("Encrypt with short public key should fail")
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	pub, priv := genKeypair(t)
	ks, err := Encrypt(pub, priv, []byte("passphrase"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	data, err := Marshal(ks)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Confirm the JSON is indented (humans will see it on disk).
	if !bytes.Contains(data, []byte("\n  ")) {
		t.Fatalf("expected indented JSON; got %s", string(data))
	}
	ks2, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if err := Validate(ks2); err != nil {
		t.Fatalf("Validate after round-trip: %v", err)
	}
	got, err := Decrypt(ks2, []byte("passphrase"))
	if err != nil {
		t.Fatalf("Decrypt after round-trip: %v", err)
	}
	if !bytes.Equal(got, priv) {
		t.Fatalf("decrypted bytes differ after round-trip")
	}
}

func TestAddressIsDerivedFromPublicKey(t *testing.T) {
	pub, priv := genKeypair(t)
	ks, err := Encrypt(pub, priv, []byte("p"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	wantAddr := AddressFromPublicKey(pub)
	if ks.Address != wantAddr {
		t.Fatalf("address mismatch: ks=%s want=%s", ks.Address, wantAddr)
	}
}

func TestValidateRejectsTamperedAddress(t *testing.T) {
	pub, priv := genKeypair(t)
	ks, err := Encrypt(pub, priv, []byte("p"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// Mutate one byte of the address — Validate must reject because it
	// recomputes sha256(public_key) and compares.
	addrBytes, _ := hex.DecodeString(ks.Address)
	addrBytes[0] ^= 0xff
	ks.Address = hex.EncodeToString(addrBytes)
	if err := Validate(ks); err == nil {
		t.Fatalf("Validate should reject mutated address")
	}
}

func TestValidateRejectsTamperedPublicKey(t *testing.T) {
	pub, priv := genKeypair(t)
	ks, err := Encrypt(pub, priv, []byte("p"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	pubBytes, _ := hex.DecodeString(ks.PublicKey)
	pubBytes[100] ^= 0xff
	ks.PublicKey = hex.EncodeToString(pubBytes)
	if err := Validate(ks); err == nil {
		t.Fatalf("Validate should reject mutated public key (no longer matches address)")
	}
}

func TestValidateRejectsTamperedCiphertext(t *testing.T) {
	pub, priv := genKeypair(t)
	ks, err := Encrypt(pub, priv, []byte("p"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// Flip a byte of the ciphertext. Validate doesn't catch this (the
	// ciphertext is intentionally not authenticated by the metadata
	// cross-check), but Decrypt must — that's exactly what AES-GCM is
	// for.
	ctBytes, _ := hex.DecodeString(ks.Ciphertext)
	ctBytes[10] ^= 0x01
	ks.Ciphertext = hex.EncodeToString(ctBytes)
	if _, err := Decrypt(ks, []byte("p")); err != ErrInvalidPassphrase {
		t.Fatalf("expected ErrInvalidPassphrase for tampered ciphertext, got %v", err)
	}
}

func TestValidateRejectsWeakKDF(t *testing.T) {
	pub, priv := genKeypair(t)
	ks, err := Encrypt(pub, priv, []byte("p"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	ks.KDFParams.Iterations = 1000 // way below 100k floor
	if err := Validate(ks); err == nil {
		t.Fatalf("Validate should reject pbkdf2 iterations below 100k")
	}
}

func TestValidateRejectsWrongType(t *testing.T) {
	pub, priv := genKeypair(t)
	ks, _ := Encrypt(pub, priv, []byte("p"))
	ks.Type = "ethereum-keystore"
	if err := Validate(ks); err == nil || !strings.Contains(err.Error(), "type") {
		t.Fatalf("Validate should reject foreign type, got %v", err)
	}
}

func TestKeystoreSchemaShapeStable(t *testing.T) {
	// Locks in the on-disk JSON schema for v1. If a future commit adds
	// a field that breaks this test, the developer is forced to either
	// (a) make the new field nullable / omitempty so old keystores keep
	// parsing, or (b) bump Version and update this test.
	pub, priv := genKeypair(t)
	ks, _ := Encrypt(pub, priv, []byte("p"))
	data, err := Marshal(ks)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}
	wantKeys := []string{
		"version", "type", "algorithm", "address", "public_key",
		"kdf", "kdf_params", "cipher", "cipher_params", "ciphertext",
		"created_at",
	}
	for _, k := range wantKeys {
		if _, ok := raw[k]; !ok {
			t.Errorf("v1 keystore is missing required field %q", k)
		}
	}
	for k := range raw {
		found := false
		for _, w := range wantKeys {
			if k == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("v1 keystore contains unexpected field %q — bump Version when adding new fields", k)
		}
	}
}
