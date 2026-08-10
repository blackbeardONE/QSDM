package wallet

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/blackbeardONE/QSDM/pkg/crypto"
)

// Validator wallet-key persistence.
//
// NewWalletService calls crypto.NewDilithium, which mints a FRESH ML-DSA-87
// keypair on every call. Nothing ever loaded a key from disk, so a validator
// came up with a different identity every single restart:
//
//	02:11:05  Wallet service initialized  address=5aa5d9f5…  balance=0
//	03:30:24  Wallet service initialized  address=4b28fb63…  balance=0
//
// Two consequences, both of which look like "the node is broken":
//
//  1. The wallet balance is always 0, because the address is brand new and
//     the canonical ledger has never seen it.
//  2. Any CELL previously credited to the node — mining rewards, faucet,
//     transfers — is stranded at an address whose key no longer exists
//     anywhere. That is unrecoverable value loss, not a display bug.
//
// Consensus identity uses a separate persisted signer key; keeping these
// roles separate prevents a hot consensus key from holding user funds.
//
// The key is stored unencrypted with 0600 permissions, matching the existing
// convention for unattended node key material (.qsdm/attester.key,
// hmac.key, home-gateway.key). A passphrase would be more secure but would
// stop the node booting unattended, which is the deployment model here. Use
// filesystem permissions and disk encryption to protect it.

// walletKeyFile is the on-disk format. Both halves are stored because the
// CGO/liboqs backend cannot derive a public key from a secret key.
type walletKeyFile struct {
	Version    int    `json:"version"`
	Algorithm  string `json:"algorithm"`
	Address    string `json:"address"`
	PublicKey  string `json:"public_key"`  // base64
	PrivateKey string `json:"private_key"` // base64
}

const walletKeyFileVersion = 1

// ErrWalletKeyCorrupt indicates the key file exists but cannot be used.
// Deliberately NOT recoverable by silently regenerating: overwriting a key
// file we failed to parse would destroy the only copy of an identity that
// may hold funds.
var ErrWalletKeyCorrupt = errors.New("wallet: key file is present but unusable")

// LoadOrCreateWalletService returns a WalletService whose identity persists
// across restarts.
//
// If path holds a usable key it is loaded. If path does not exist, a new
// keypair is generated and written atomically. If path exists but cannot be
// parsed or does not match its own address, this fails with
// ErrWalletKeyCorrupt rather than minting a replacement — regenerating over
// a damaged key would strand whatever it held.
//
// An empty path falls back to the legacy ephemeral behaviour so tests and
// embedded callers are unaffected.
func LoadOrCreateWalletService(path string) (*WalletService, error) {
	if path == "" {
		return NewWalletService()
	}

	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		return walletServiceFromKeyFile(path, raw)
	case os.IsNotExist(err):
		return createAndPersistWalletService(path)
	default:
		return nil, fmt.Errorf("wallet: read key file %s: %w", path, err)
	}
}

func walletServiceFromKeyFile(path string, raw []byte) (*WalletService, error) {
	var kf walletKeyFile
	if err := json.Unmarshal(raw, &kf); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrWalletKeyCorrupt, path, err)
	}
	if kf.Version != walletKeyFileVersion {
		return nil, fmt.Errorf("%w: %s: unsupported version %d", ErrWalletKeyCorrupt, path, kf.Version)
	}
	pub, err := base64.StdEncoding.DecodeString(kf.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: public key: %v", ErrWalletKeyCorrupt, path, err)
	}
	priv, err := base64.StdEncoding.DecodeString(kf.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: private key: %v", ErrWalletKeyCorrupt, path, err)
	}

	d := crypto.NewDilithiumFromKeyPair(pub, priv)
	if d == nil {
		return nil, fmt.Errorf("%w: %s: key material rejected by the ML-DSA-87 backend", ErrWalletKeyCorrupt, path)
	}

	ws := newWalletServiceFromDilithium(d)
	// The stored address is a redundant copy of SHA256(public_key). If it
	// disagrees, the file has been edited or corrupted and we must not
	// silently adopt a different identity than the operator expects.
	if kf.Address != "" && kf.Address != ws.address {
		d.Free()
		return nil, fmt.Errorf("%w: %s: address %s does not match its public key (%s)",
			ErrWalletKeyCorrupt, path, kf.Address, ws.address)
	}
	return ws, nil
}

func createAndPersistWalletService(path string) (*WalletService, error) {
	ws, err := NewWalletService()
	if err != nil {
		return nil, err
	}
	if err := saveWalletKey(path, ws); err != nil {
		return nil, err
	}
	return ws, nil
}

// saveWalletKey writes the key atomically: a crash midway through must not
// leave a truncated file where a valid identity used to be.
func saveWalletKey(path string, ws *WalletService) error {
	if ws == nil || ws.dilithium == nil {
		return errors.New("wallet: cannot persist a wallet with no key")
	}
	pub := ws.dilithium.GetPublicKey()
	priv := ws.dilithium.GetPrivateKey()
	if len(pub) == 0 || len(priv) == 0 {
		return errors.New("wallet: key material unavailable for persistence")
	}

	body, err := json.MarshalIndent(walletKeyFile{
		Version:    walletKeyFileVersion,
		Algorithm:  "ML-DSA-87",
		Address:    ws.address,
		PublicKey:  base64.StdEncoding.EncodeToString(pub),
		PrivateKey: base64.StdEncoding.EncodeToString(priv),
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("wallet: encode key file: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("wallet: create key directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".wallet-key-*.tmp")
	if err != nil {
		return fmt.Errorf("wallet: create temp key file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("wallet: restrict key file permissions: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return fmt.Errorf("wallet: write key file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("wallet: sync key file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("wallet: close key file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("wallet: install key file %s: %w", path, err)
	}
	return nil
}
