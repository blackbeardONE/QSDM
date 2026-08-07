package walletrecovery

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

func legacyTestKey(t *testing.T) (string, []byte, []byte) {
	t.Helper()
	public, private, err := mldsa87.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicBytes, err := public.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	privateBytes, err := private.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return addressHex(publicBytes), publicBytes, privateBytes
}

func TestLegacyCapsuleRoundTripPreservesExistingWallet(t *testing.T) {
	address, publicKey, privateKey := legacyTestKey(t)
	material, err := GenerateLegacyCapsule(address, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	defer material.ZeroSecrets()
	if got := len(splitWords(material.Words)); got != WordCount {
		t.Fatalf("words = %d, want %d", got, WordCount)
	}
	locator, err := LegacyLocatorFromWords(material.Words)
	if err != nil {
		t.Fatal(err)
	}
	if locator != material.Capsule.Locator {
		t.Fatalf("locator mismatch: %s != %s", locator, material.Capsule.Locator)
	}
	recovered, err := RestoreLegacyCapsule(material.Words, material.Capsule)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.ZeroSecrets()
	if recovered.Address != address || hex.EncodeToString(recovered.PublicKey) != hex.EncodeToString(publicKey) || base64.StdEncoding.EncodeToString(recovered.PrivateKey) != base64.StdEncoding.EncodeToString(privateKey) {
		t.Fatal("recovered legacy key material changed")
	}
}

func TestLegacyCapsuleRejectsWrongWordsAndTampering(t *testing.T) {
	address, publicKey, privateKey := legacyTestKey(t)
	material, err := GenerateLegacyCapsule(address, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	defer material.ZeroSecrets()
	other, err := GenerateLegacyCapsule(address, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	defer other.ZeroSecrets()
	if _, err := RestoreLegacyCapsule(other.Words, material.Capsule); err == nil {
		t.Fatal("wrong words restored legacy capsule")
	}
	tampered := material.Capsule
	ciphertext, _ := base64.StdEncoding.DecodeString(tampered.Ciphertext)
	ciphertext[len(ciphertext)/2] ^= 0x80
	tampered.Ciphertext = base64.StdEncoding.EncodeToString(ciphertext)
	if _, err := RestoreLegacyCapsule(material.Words, tampered); err == nil {
		t.Fatal("tampered capsule restored")
	}
}

func TestLegacyCapsuleRejectsMismatchedPrivateKey(t *testing.T) {
	address, publicKey, _ := legacyTestKey(t)
	_, _, otherPrivate := legacyTestKey(t)
	if _, err := GenerateLegacyCapsule(address, publicKey, otherPrivate); err == nil {
		t.Fatal("mismatched private key accepted")
	}
}

func splitWords(words string) []string {
	return strings.Fields(words)
}

func addressHex(publicKey []byte) string {
	sum := sha256.Sum256(publicKey)
	return hex.EncodeToString(sum[:])
}
