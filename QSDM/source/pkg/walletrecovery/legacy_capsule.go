package walletrecovery

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
	"github.com/tyler-smith/go-bip39"
	"golang.org/x/crypto/hkdf"
)

const (
	// LegacyCapsuleScheme identifies recovery words that unlock an encrypted
	// copy of an existing random-key wallet. Unlike Scheme, these words do not
	// deterministically generate the wallet key.
	LegacyCapsuleScheme  = "qsdm-legacy-wallet-recovery-v1"
	LegacyCapsuleVersion = 1

	legacyCapsuleMaxCiphertextBytes = 16 * 1024
	legacyCapsuleAddressBytes       = sha256.Size * 2
	legacyCapsulePlaintextBytes     = legacyCapsuleAddressBytes + mldsa87.PublicKeySize + mldsa87.PrivateKeySize
)

var (
	legacyCapsuleSalt        = []byte("QSDM Legacy Wallet Recovery v1")
	legacyCapsuleKeyInfo     = []byte("legacy wallet capsule / AES-256-GCM key")
	legacyCapsuleLocatorInfo = []byte("legacy wallet capsule / lookup locator")
)

// LegacyCapsule is safe to replicate publicly. It contains only authenticated
// ciphertext plus the minimum metadata needed to locate and validate it.
// Possession of the matching 24 words is required to decrypt the private key.
type LegacyCapsule struct {
	Version    int    `json:"version"`
	Scheme     string `json:"scheme"`
	Locator    string `json:"locator"`
	Address    string `json:"address"`
	Cipher     string `json:"cipher"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
	CreatedAt  string `json:"created_at"`
}

// LegacyCapsuleMaterial is produced while enabling recovery on an existing
// wallet. Call ZeroSecrets after the words and entropy have been handed to the
// caller's protected output path.
type LegacyCapsuleMaterial struct {
	Words   string
	Entropy []byte
	Capsule LegacyCapsule
}

// LegacyRecoveredKey is the exact key material recovered from a capsule.
type LegacyRecoveredKey struct {
	PublicKey  []byte
	PrivateKey []byte
	Address    string
}

// GenerateLegacyCapsule assigns fresh 24 recovery words to an existing ML-DSA
// wallet without changing its address. The words encrypt the existing private
// key; they do not generate a replacement wallet.
func GenerateLegacyCapsule(address string, publicKey, privateKey []byte) (LegacyCapsuleMaterial, error) {
	if err := verifyLegacyKeyMaterial(address, publicKey, privateKey); err != nil {
		return LegacyCapsuleMaterial{}, err
	}
	entropy, err := bip39.NewEntropy(EntropyBits)
	if err != nil {
		return LegacyCapsuleMaterial{}, fmt.Errorf("qsdm legacy recovery: generate entropy: %w", err)
	}
	words, err := bip39.NewMnemonic(entropy)
	if err != nil {
		zero(entropy)
		return LegacyCapsuleMaterial{}, fmt.Errorf("qsdm legacy recovery: encode words: %w", err)
	}
	capsule, err := legacyCapsuleFromEntropy(entropy, address, publicKey, privateKey, time.Now().UTC())
	if err != nil {
		zero(entropy)
		return LegacyCapsuleMaterial{}, err
	}
	return LegacyCapsuleMaterial{
		Words:   words,
		Entropy: entropy,
		Capsule: capsule,
	}, nil
}

func legacyCapsuleFromEntropy(entropy []byte, address string, publicKey, privateKey []byte, createdAt time.Time) (LegacyCapsule, error) {
	if len(entropy) != EntropySize {
		return LegacyCapsule{}, fmt.Errorf("qsdm legacy recovery: entropy must be %d bytes, got %d", EntropySize, len(entropy))
	}
	if err := verifyLegacyKeyMaterial(address, publicKey, privateKey); err != nil {
		return LegacyCapsule{}, err
	}

	encryptionKey, locatorKey, err := deriveLegacyCapsuleKeys(entropy)
	if err != nil {
		return LegacyCapsule{}, err
	}
	defer zero(encryptionKey)
	defer zero(locatorKey)
	locator := legacyLocator(locatorKey)

	plaintext := make([]byte, legacyCapsulePlaintextBytes)
	offset := copy(plaintext, strings.ToLower(address))
	offset += copy(plaintext[offset:], publicKey)
	copy(plaintext[offset:], privateKey)
	defer zero(plaintext)

	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return LegacyCapsule{}, fmt.Errorf("qsdm legacy recovery: create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return LegacyCapsule{}, fmt.Errorf("qsdm legacy recovery: create GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return LegacyCapsule{}, fmt.Errorf("qsdm legacy recovery: generate nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, legacyCapsuleAAD(address, locator))
	if len(ciphertext) > legacyCapsuleMaxCiphertextBytes {
		return LegacyCapsule{}, errors.New("qsdm legacy recovery: encrypted capsule is unexpectedly large")
	}

	return LegacyCapsule{
		Version:    LegacyCapsuleVersion,
		Scheme:     LegacyCapsuleScheme,
		Locator:    locator,
		Address:    strings.ToLower(address),
		Cipher:     "aes-256-gcm",
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
		CreatedAt:  createdAt.UTC().Format(time.RFC3339Nano),
	}, nil
}

// RestoreLegacyCapsule decrypts and integrity-checks an existing wallet from
// its 24 recovery words and replicated capsule.
func RestoreLegacyCapsule(words string, capsule LegacyCapsule) (LegacyRecoveredKey, error) {
	if err := ValidateLegacyCapsule(capsule); err != nil {
		return LegacyRecoveredKey{}, err
	}
	entropy, err := legacyEntropyFromWords(words)
	if err != nil {
		return LegacyRecoveredKey{}, err
	}
	defer zero(entropy)
	encryptionKey, locatorKey, err := deriveLegacyCapsuleKeys(entropy)
	if err != nil {
		return LegacyRecoveredKey{}, err
	}
	defer zero(encryptionKey)
	defer zero(locatorKey)
	if !hmac.Equal([]byte(legacyLocator(locatorKey)), []byte(capsule.Locator)) {
		return LegacyRecoveredKey{}, errors.New("qsdm legacy recovery: words do not match this recovery capsule")
	}

	nonce, _ := base64.StdEncoding.DecodeString(capsule.Nonce)
	ciphertext, _ := base64.StdEncoding.DecodeString(capsule.Ciphertext)
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return LegacyRecoveredKey{}, fmt.Errorf("qsdm legacy recovery: create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return LegacyRecoveredKey{}, fmt.Errorf("qsdm legacy recovery: create GCM: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, legacyCapsuleAAD(capsule.Address, capsule.Locator))
	if err != nil {
		return LegacyRecoveredKey{}, errors.New("qsdm legacy recovery: words do not match or capsule authentication failed")
	}
	defer zero(plaintext)
	if len(plaintext) != legacyCapsulePlaintextBytes {
		return LegacyRecoveredKey{}, errors.New("qsdm legacy recovery: decrypted capsule is malformed")
	}
	address := string(plaintext[:legacyCapsuleAddressBytes])
	publicKeyEnd := legacyCapsuleAddressBytes + mldsa87.PublicKeySize
	publicKey := append([]byte(nil), plaintext[legacyCapsuleAddressBytes:publicKeyEnd]...)
	privateKey := append([]byte(nil), plaintext[publicKeyEnd:]...)
	if address != capsule.Address {
		zero(privateKey)
		return LegacyRecoveredKey{}, errors.New("qsdm legacy recovery: capsule address integrity check failed")
	}
	if err := verifyLegacyKeyMaterial(address, publicKey, privateKey); err != nil {
		zero(privateKey)
		return LegacyRecoveredKey{}, err
	}
	return LegacyRecoveredKey{
		PublicKey:  publicKey,
		PrivateKey: privateKey,
		Address:    address,
	}, nil
}

// LegacyLocatorFromWords returns the public lookup locator used to retrieve a
// capsule. It reveals neither the words nor the wallet address.
func LegacyLocatorFromWords(words string) (string, error) {
	entropy, err := legacyEntropyFromWords(words)
	if err != nil {
		return "", err
	}
	defer zero(entropy)
	_, locatorKey, err := deriveLegacyCapsuleKeys(entropy)
	if err != nil {
		return "", err
	}
	defer zero(locatorKey)
	return legacyLocator(locatorKey), nil
}

// LegacyRecoveryEntropyFromWords decodes the secret entropy carried by a
// legacy-wallet recovery phrase. Callers must erase the returned bytes after
// attaching recovery metadata to a local keystore.
func LegacyRecoveryEntropyFromWords(words string) ([]byte, error) {
	return legacyEntropyFromWords(words)
}

func legacyEntropyFromWords(words string) ([]byte, error) {
	normalized := normalize(words)
	if count := len(strings.Fields(normalized)); count != WordCount {
		return nil, fmt.Errorf("qsdm legacy recovery: expected %d words, got %d", WordCount, count)
	}
	entropy, err := bip39.EntropyFromMnemonic(normalized)
	if err != nil {
		return nil, fmt.Errorf("qsdm legacy recovery: invalid words or checksum: %w", err)
	}
	if len(entropy) != EntropySize {
		zero(entropy)
		return nil, fmt.Errorf("qsdm legacy recovery: decoded entropy is %d bytes, want %d", len(entropy), EntropySize)
	}
	return entropy, nil
}

func deriveLegacyCapsuleKeys(entropy []byte) ([]byte, []byte, error) {
	if len(entropy) != EntropySize {
		return nil, nil, fmt.Errorf("qsdm legacy recovery: entropy must be %d bytes", EntropySize)
	}
	encryptionKey := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, entropy, legacyCapsuleSalt, legacyCapsuleKeyInfo), encryptionKey); err != nil {
		return nil, nil, fmt.Errorf("qsdm legacy recovery: derive encryption key: %w", err)
	}
	locatorKey := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, entropy, legacyCapsuleSalt, legacyCapsuleLocatorInfo), locatorKey); err != nil {
		zero(encryptionKey)
		return nil, nil, fmt.Errorf("qsdm legacy recovery: derive locator key: %w", err)
	}
	return encryptionKey, locatorKey, nil
}

func legacyLocator(locatorKey []byte) string {
	mac := hmac.New(sha256.New, locatorKey)
	_, _ = mac.Write(legacyCapsuleLocatorInfo)
	return hex.EncodeToString(mac.Sum(nil))
}

func legacyCapsuleAAD(address, locator string) []byte {
	return []byte(LegacyCapsuleScheme + ":" + strings.ToLower(address) + ":" + strings.ToLower(locator))
}

// ValidateLegacyCapsule performs public shape and size checks without the
// recovery words. Consensus and API admission use this before storing data.
func ValidateLegacyCapsule(capsule LegacyCapsule) error {
	if capsule.Version != LegacyCapsuleVersion || capsule.Scheme != LegacyCapsuleScheme {
		return errors.New("qsdm legacy recovery: unsupported capsule version or scheme")
	}
	if capsule.Address != strings.ToLower(strings.TrimSpace(capsule.Address)) {
		return errors.New("qsdm legacy recovery: capsule address must use canonical lower-case hex")
	}
	address, err := hex.DecodeString(capsule.Address)
	if err != nil || len(address) != sha256.Size {
		return errors.New("qsdm legacy recovery: capsule address must be a 32-byte hex digest")
	}
	if capsule.Locator != strings.ToLower(strings.TrimSpace(capsule.Locator)) {
		return errors.New("qsdm legacy recovery: capsule locator must use canonical lower-case hex")
	}
	locator, err := hex.DecodeString(capsule.Locator)
	if err != nil || len(locator) != sha256.Size {
		return errors.New("qsdm legacy recovery: capsule locator must be a 32-byte hex digest")
	}
	if capsule.Cipher != "aes-256-gcm" {
		return errors.New("qsdm legacy recovery: unsupported capsule cipher")
	}
	nonce, err := base64.StdEncoding.DecodeString(capsule.Nonce)
	if err != nil || len(nonce) != 12 {
		return errors.New("qsdm legacy recovery: malformed capsule nonce")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(capsule.Ciphertext)
	if err != nil || len(ciphertext) < 32 || len(ciphertext) > legacyCapsuleMaxCiphertextBytes {
		return errors.New("qsdm legacy recovery: malformed or oversized capsule ciphertext")
	}
	if _, err := time.Parse(time.RFC3339Nano, capsule.CreatedAt); err != nil {
		return errors.New("qsdm legacy recovery: invalid capsule creation timestamp")
	}
	return nil
}

func verifyLegacyKeyMaterial(address string, publicKey, privateKey []byte) error {
	if len(publicKey) != mldsa87.PublicKeySize {
		return fmt.Errorf("qsdm legacy recovery: public key must be %d bytes", mldsa87.PublicKeySize)
	}
	var private mldsa87.PrivateKey
	if err := private.UnmarshalBinary(privateKey); err != nil {
		return fmt.Errorf("qsdm legacy recovery: parse private key: %w", err)
	}
	recoveredPublic, err := private.Public().(*mldsa87.PublicKey).MarshalBinary()
	if err != nil {
		return fmt.Errorf("qsdm legacy recovery: recover public key: %w", err)
	}
	if !bytes.Equal(recoveredPublic, publicKey) {
		return errors.New("qsdm legacy recovery: private key does not match public key")
	}
	sum := sha256.Sum256(publicKey)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), strings.TrimSpace(address)) {
		return errors.New("qsdm legacy recovery: public key does not match wallet address")
	}
	return nil
}

func (m *LegacyCapsuleMaterial) ZeroSecrets() {
	if m == nil {
		return
	}
	zero(m.Entropy)
	m.Words = ""
}

func (k *LegacyRecoveredKey) ZeroSecrets() {
	if k == nil {
		return
	}
	zero(k.PrivateKey)
}
