package chain

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/fileutil"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

const (
	persistentBFTSignerKeyVersion = 1
	maxBFTSignerKeyFileSize      = 64 << 10
)

type persistentBFTSignerKeyFile struct {
	Version    int    `json:"version"`
	Algorithm  string `json:"algorithm"`
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

// PersistentBFTSigner is the validator's stable ML-DSA-87 hot key. It signs
// consensus traffic only and must not hold user or treasury funds.
type PersistentBFTSigner struct {
	privateKey *mldsa87.PrivateKey
	publicKey  []byte
}

// LoadOrCreateBFTSigner loads a validator consensus key or creates it once.
// The parent directory must already exist. The file is private signing
// material and is written with mode 0600.
func LoadOrCreateBFTSigner(path string) (*PersistentBFTSigner, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false, errors.New("chain: consensus signer key path is empty")
	}

	info, err := os.Lstat(path)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, false, fmt.Errorf("chain: consensus signer key %q must not be a symlink", path)
		}
		if !info.Mode().IsRegular() {
			return nil, false, fmt.Errorf("chain: consensus signer key %q is not a regular file", path)
		}
		if info.Size() > maxBFTSignerKeyFileSize {
			return nil, false, fmt.Errorf("chain: consensus signer key %q exceeds %d bytes", path, maxBFTSignerKeyFileSize)
		}
		signer, loadErr := loadBFTSigner(path)
		return signer, false, loadErr
	case !errors.Is(err, os.ErrNotExist):
		return nil, false, fmt.Errorf("chain: stat consensus signer key %q: %w", path, err)
	}

	parent := filepath.Dir(path)
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return nil, false, fmt.Errorf("chain: consensus signer key parent %q is unavailable: %w", parent, err)
	}
	if !parentInfo.IsDir() {
		return nil, false, fmt.Errorf("chain: consensus signer key parent %q is not a directory", parent)
	}

	publicKey, privateKey, err := mldsa87.GenerateKey(rand.Reader)
	if err != nil {
		return nil, false, fmt.Errorf("chain: generate validator ML-DSA-87 key: %w", err)
	}
	publicBytes, err := publicKey.MarshalBinary()
	if err != nil {
		return nil, false, fmt.Errorf("chain: marshal validator public key: %w", err)
	}
	privateBytes, err := privateKey.MarshalBinary()
	if err != nil {
		return nil, false, fmt.Errorf("chain: marshal validator private key: %w", err)
	}
	defer zeroSignerBytes(privateBytes)

	encoded, err := json.MarshalIndent(persistentBFTSignerKeyFile{
		Version:    persistentBFTSignerKeyVersion,
		Algorithm:  "ml-dsa-87",
		PublicKey:  hex.EncodeToString(publicBytes),
		PrivateKey: hex.EncodeToString(privateBytes),
	}, "", "  ")
	if err != nil {
		return nil, false, fmt.Errorf("chain: encode validator signing key: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := fileutil.WriteFileAtomic(path, encoded, 0o600); err != nil {
		return nil, false, fmt.Errorf("chain: persist validator signing key %q: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, false, fmt.Errorf("chain: protect validator signing key %q: %w", path, err)
	}
	return newPersistentBFTSigner(privateKey, publicBytes), true, nil
}

func loadBFTSigner(path string) (*PersistentBFTSigner, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("chain: read consensus signer key %q: %w", path, err)
	}
	var stored persistentBFTSignerKeyFile
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("chain: parse consensus signer key %q: %w", path, err)
	}
	if stored.Version != persistentBFTSignerKeyVersion || stored.Algorithm != "ml-dsa-87" {
		return nil, fmt.Errorf("chain: consensus signer key %q has unsupported version or algorithm", path)
	}
	publicBytes, err := hex.DecodeString(stored.PublicKey)
	if err != nil || len(publicBytes) != mldsa87.PublicKeySize {
		return nil, fmt.Errorf("chain: consensus signer key %q has an invalid public key", path)
	}
	privateBytes, err := hex.DecodeString(stored.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("chain: consensus signer key %q has an invalid private key", path)
	}
	defer zeroSignerBytes(privateBytes)

	var privateKey mldsa87.PrivateKey
	if err := privateKey.UnmarshalBinary(privateBytes); err != nil {
		return nil, fmt.Errorf("chain: decode consensus signer private key %q: %w", path, err)
	}
	recoveredPublic, err := privateKey.Public().(*mldsa87.PublicKey).MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("chain: derive consensus signer public key %q: %w", path, err)
	}
	if !bytes.Equal(recoveredPublic, publicBytes) {
		return nil, fmt.Errorf("chain: consensus signer key %q contains a mismatched key pair", path)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("chain: protect consensus signer key %q: %w", path, err)
	}
	return newPersistentBFTSigner(&privateKey, publicBytes), nil
}

func newPersistentBFTSigner(privateKey *mldsa87.PrivateKey, publicKey []byte) *PersistentBFTSigner {
	return &PersistentBFTSigner{
		privateKey: privateKey,
		publicKey:  append([]byte(nil), publicKey...),
	}
}

// Sign implements BFTSigner.
func (s *PersistentBFTSigner) Sign(message []byte) ([]byte, error) {
	if s == nil || s.privateKey == nil {
		return nil, errors.New("chain: validator consensus signer is unavailable")
	}
	signature := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(s.privateKey, message, nil, true, signature); err != nil {
		return nil, fmt.Errorf("chain: sign consensus message: %w", err)
	}
	return signature, nil
}

// GetPublicKey implements BFTSigner.
func (s *PersistentBFTSigner) GetPublicKey() []byte {
	if s == nil {
		return nil
	}
	return append([]byte(nil), s.publicKey...)
}

// Address returns the self-certifying validator address for this key.
func (s *PersistentBFTSigner) Address() string {
	return BFTValidatorAddress(s.GetPublicKey())
}

func zeroSignerBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
