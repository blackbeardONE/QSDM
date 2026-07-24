// Package walletrecovery implements QSDM Recovery Words v1.
//
// Recovery words are a human-readable encoding of 256 bits of random entropy.
// The entropy is domain-separated with HKDF-SHA-256 before it is used as the
// deterministic ML-DSA-87 key-generation seed. The words are BIP-39 English
// words for reliable transcription and checksum validation, but the resulting
// wallet is QSDM-specific and is not a Bitcoin or BIP-32 wallet.
package walletrecovery

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
	"github.com/tyler-smith/go-bip39"
	"golang.org/x/crypto/hkdf"
)

const (
	// Scheme identifies the deterministic derivation contract. Changing any
	// derivation input requires a new scheme identifier.
	Scheme = "qsdm-wallet-recovery-v1"

	EntropyBits = 256
	EntropySize = EntropyBits / 8
	WordCount   = 24
)

var (
	hkdfSalt = []byte("QSDM Wallet Recovery v1")
	hkdfInfo = []byte("CELL wallet / ML-DSA-87 deterministic seed")
)

// Material is the wallet material reconstructed from recovery entropy. Call
// ZeroSecrets as soon as the private key and entropy are no longer needed.
type Material struct {
	Words      string
	Entropy    []byte
	PublicKey  []byte
	PrivateKey []byte
	Address    string
}

// Generate creates a new 24-word QSDM recovery phrase and its wallet.
func Generate() (Material, error) {
	entropy, err := bip39.NewEntropy(EntropyBits)
	if err != nil {
		return Material{}, fmt.Errorf("qsdm recovery: generate entropy: %w", err)
	}
	words, err := bip39.NewMnemonic(entropy)
	if err != nil {
		zero(entropy)
		return Material{}, fmt.Errorf("qsdm recovery: encode words: %w", err)
	}
	material, err := materialFromEntropy(entropy)
	if err != nil {
		zero(entropy)
		return Material{}, err
	}
	material.Words = words
	return material, nil
}

// Restore validates a 24-word QSDM recovery phrase and reconstructs the same
// wallet material on every supported platform.
func Restore(words string) (Material, error) {
	normalized := normalize(words)
	if count := len(strings.Fields(normalized)); count != WordCount {
		return Material{}, fmt.Errorf("qsdm recovery: expected %d words, got %d", WordCount, count)
	}
	entropy, err := bip39.EntropyFromMnemonic(normalized)
	if err != nil {
		return Material{}, fmt.Errorf("qsdm recovery: invalid words or checksum: %w", err)
	}
	if len(entropy) != EntropySize {
		zero(entropy)
		return Material{}, fmt.Errorf("qsdm recovery: decoded entropy is %d bytes, want %d", len(entropy), EntropySize)
	}
	material, err := materialFromEntropy(entropy)
	if err != nil {
		zero(entropy)
		return Material{}, err
	}
	material.Words = normalized
	return material, nil
}

// WordsFromEntropy converts stored QSDM recovery entropy back into its
// checksum-protected words.
func WordsFromEntropy(entropy []byte) (string, error) {
	if len(entropy) != EntropySize {
		return "", fmt.Errorf("qsdm recovery: entropy must be %d bytes, got %d", EntropySize, len(entropy))
	}
	words, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return "", fmt.Errorf("qsdm recovery: encode words: %w", err)
	}
	return words, nil
}

func materialFromEntropy(entropy []byte) (Material, error) {
	if len(entropy) != EntropySize {
		return Material{}, fmt.Errorf("qsdm recovery: entropy must be %d bytes, got %d", EntropySize, len(entropy))
	}

	var seed [mldsa87.SeedSize]byte
	reader := hkdf.New(sha256.New, entropy, hkdfSalt, hkdfInfo)
	if _, err := io.ReadFull(reader, seed[:]); err != nil {
		return Material{}, fmt.Errorf("qsdm recovery: derive ML-DSA seed: %w", err)
	}
	defer zero(seed[:])

	publicKey, privateKey := mldsa87.NewKeyFromSeed(&seed)
	publicBytes, err := publicKey.MarshalBinary()
	if err != nil {
		return Material{}, fmt.Errorf("qsdm recovery: marshal public key: %w", err)
	}
	privateBytes, err := privateKey.MarshalBinary()
	if err != nil {
		return Material{}, fmt.Errorf("qsdm recovery: marshal private key: %w", err)
	}
	addressHash := sha256.Sum256(publicBytes)

	return Material{
		Entropy:    append([]byte(nil), entropy...),
		PublicKey:  publicBytes,
		PrivateKey: privateBytes,
		Address:    hex.EncodeToString(addressHash[:]),
	}, nil
}

// ZeroSecrets clears the secret slices held by Material. Go cannot guarantee
// that compiler/runtime copies are erased, but this still shortens exposure in
// the ordinary execution path.
func (m *Material) ZeroSecrets() {
	if m == nil {
		return
	}
	zero(m.Entropy)
	zero(m.PrivateKey)
	m.Words = ""
}

func normalize(words string) string {
	words = strings.TrimPrefix(words, "\ufeff")
	return strings.ToLower(strings.Join(strings.Fields(words), " "))
}

func zero(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
