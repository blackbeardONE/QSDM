package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/keystore"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// operatorProofSigner owns the ML-DSA key used for the optional
// operator_sig rail. It is loaded once at startup from the same QSDM
// keystore JSON format used by qsdmcli and Hive.
type operatorProofSigner struct {
	Address      string
	PublicKeyHex string
	privateKey   *mldsa87.PrivateKey
}

func (s *operatorProofSigner) Sign(proof mining.Proof, bundle hmac.Bundle) (string, error) {
	if s == nil || s.privateKey == nil {
		return "", errors.New("operator signer is not loaded")
	}
	canonical, err := bundle.CanonicalForOperatorSignature(proof)
	if err != nil {
		return "", fmt.Errorf("canonicalize operator proof: %w", err)
	}
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(s.privateKey, canonical, nil, true, sig); err != nil {
		return "", fmt.Errorf("sign operator proof: %w", err)
	}
	return hex.EncodeToString(sig), nil
}

func loadOperatorProofSigner(walletPath, passphraseFile string) (*operatorProofSigner, error) {
	if strings.TrimSpace(passphraseFile) == "" {
		return nil, errors.New("operator passphrase file is required for unattended proof signing")
	}

	path, err := defaultOperatorKeystorePath(walletPath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read operator keystore %s: %w", path, err)
	}
	ks, err := keystore.Unmarshal(data)
	if err != nil {
		return nil, fmt.Errorf("parse operator keystore %s: %w", path, err)
	}
	passphrase, err := readOperatorPassphraseFile(passphraseFile)
	if err != nil {
		return nil, fmt.Errorf("read operator passphrase: %w", err)
	}
	defer zeroSensitive(passphrase)

	privateBytes, err := keystore.Decrypt(ks, passphrase)
	if err != nil {
		return nil, fmt.Errorf("decrypt operator keystore: %w", err)
	}
	defer zeroSensitive(privateBytes)

	var privateKey mldsa87.PrivateKey
	if err := privateKey.UnmarshalBinary(privateBytes); err != nil {
		return nil, fmt.Errorf("parse operator private key: %w", err)
	}
	recoveredPublic, err := privateKey.Public().(*mldsa87.PublicKey).MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("recover operator public key: %w", err)
	}
	storedPublic, err := hex.DecodeString(ks.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("operator keystore public_key is not hex: %w", err)
	}
	if !bytes.Equal(recoveredPublic, storedPublic) {
		return nil, errors.New("operator keystore integrity check failed: decrypted private key does not match public_key")
	}
	address := keystore.AddressFromPublicKey(storedPublic)
	if !strings.EqualFold(address, ks.Address) {
		return nil, fmt.Errorf("operator keystore address %s does not match public key address %s", ks.Address, address)
	}

	return &operatorProofSigner{
		Address:      strings.ToLower(address),
		PublicKeyHex: strings.ToLower(ks.PublicKey),
		privateKey:   &privateKey,
	}, nil
}

func defaultOperatorKeystorePath(explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return filepath.Clean(explicit), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot resolve user home directory for default operator keystore path: %w", err)
	}
	return filepath.Join(home, ".qsdm", "wallet.json"), nil
}

func readOperatorPassphraseFile(path string) ([]byte, error) {
	if path == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, err
		}
		return trimTrailingLineEndings(b), nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return trimTrailingLineEndings(b), nil
}

func trimTrailingLineEndings(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

func zeroSensitive(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
