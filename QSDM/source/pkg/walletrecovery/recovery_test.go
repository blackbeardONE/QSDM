package walletrecovery

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tyler-smith/go-bip39"
)

func TestGenerateRestoreRoundTrip(t *testing.T) {
	generated, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	defer generated.ZeroSecrets()

	if got := len(strings.Fields(generated.Words)); got != WordCount {
		t.Fatalf("word count = %d, want %d", got, WordCount)
	}

	restored, err := Restore(generated.Words)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	defer restored.ZeroSecrets()

	if generated.Address != restored.Address {
		t.Fatalf("address mismatch: %s != %s", generated.Address, restored.Address)
	}
	if !bytes.Equal(generated.PublicKey, restored.PublicKey) {
		t.Fatal("public key mismatch")
	}
	if !bytes.Equal(generated.PrivateKey, restored.PrivateKey) {
		t.Fatal("private key mismatch")
	}
	if !bytes.Equal(generated.Entropy, restored.Entropy) {
		t.Fatal("entropy mismatch")
	}
}

func TestRestoreNormalizesWhitespaceAndCase(t *testing.T) {
	generated, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	defer generated.ZeroSecrets()

	input := "  " + strings.ToUpper(strings.ReplaceAll(generated.Words, " ", "\n")) + "  "
	restored, err := Restore(input)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	defer restored.ZeroSecrets()
	if restored.Words != generated.Words {
		t.Fatalf("normalized words mismatch: %q != %q", restored.Words, generated.Words)
	}
}

func TestRestoreAcceptsUTF8BOMFromTextFile(t *testing.T) {
	material, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	defer material.ZeroSecrets()

	restored, err := Restore("\ufeff" + material.Words)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.ZeroSecrets()
	if restored.Address != material.Address {
		t.Fatalf("restored address %s, want %s", restored.Address, material.Address)
	}
}

func TestRestoreRejectsTwelveWords(t *testing.T) {
	_, err := Restore("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about")
	if err == nil || !strings.Contains(err.Error(), "expected 24 words") {
		t.Fatalf("expected 24-word error, got %v", err)
	}
}

func TestRestoreRejectsBadChecksum(t *testing.T) {
	generated, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	defer generated.ZeroSecrets()

	parts := strings.Fields(generated.Words)
	originalLastWord := parts[len(parts)-1]
	for _, replacement := range bip39.GetWordList() {
		if replacement == originalLastWord {
			continue
		}
		parts[len(parts)-1] = replacement
		candidate := strings.Join(parts, " ")
		if bip39.IsMnemonicValid(candidate) {
			continue
		}
		if _, err := Restore(candidate); err == nil {
			t.Fatal("expected checksum error")
		}
		return
	}
	t.Fatal("could not construct an invalid-checksum mnemonic")
}

func TestWordsFromEntropy(t *testing.T) {
	generated, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	defer generated.ZeroSecrets()

	words, err := WordsFromEntropy(generated.Entropy)
	if err != nil {
		t.Fatalf("WordsFromEntropy: %v", err)
	}
	if words != generated.Words {
		t.Fatalf("words mismatch: %q != %q", words, generated.Words)
	}
}
