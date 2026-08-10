package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudflare/circl/sign/mldsa/mldsa87"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/keystore"
)

// newTestKeystore writes an encrypted ML-DSA-87 keystore and returns its
// path, passphrase file, and derived address.
func newTestKeystore(t *testing.T) (walletPath, passFile, address string) {
	t.Helper()
	pub, priv, err := mldsa87.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubBytes, err := pub.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	privBytes, err := priv.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	pass := []byte("test-passphrase-for-staking-0123")
	ks, err := keystore.Encrypt(pubBytes, privBytes, pass)
	if err != nil {
		t.Fatalf("keystore.Encrypt: %v", err)
	}
	raw, err := json.Marshal(ks)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	walletPath = filepath.Join(dir, "wallet.json")
	if err := os.WriteFile(walletPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	passFile = filepath.Join(dir, "pass.txt")
	if err := os.WriteFile(passFile, pass, 0o600); err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256(pubBytes)
	return walletPath, passFile, hex.EncodeToString(sum[:])
}

// TestSignStakingEnvelope_verifiesUnderTheServersCanonicalForm is the test
// that matters most for this command.
//
// The client signs json.Marshal(envelope) with Signature and PublicKey
// blanked; the server reconstructs precisely that shape to verify. If the
// two structs ever drift — a renamed field, a changed tag, a different
// omitempty — the signature verifies nowhere and bonding fails at the
// network boundary with a misleading "signature does not verify".
//
// Unit tests on either side alone cannot catch that: each is
// self-consistent. This one signs with the CLI and verifies through the
// SERVER's struct, so a divergence fails here instead of in the field.
func TestSignStakingEnvelope_verifiesUnderTheServersCanonicalForm(t *testing.T) {
	walletPath, passFile, address := newTestKeystore(t)

	signed, err := signStakingEnvelope(stakingEnvelope{
		ID:        "stake-test-1",
		Action:    "delegate",
		Validator: "home-pc",
		Amount:    250,
		Nonce:     3,
		Timestamp: "2026-08-10T00:00:00Z",
	}, walletPath, passFile)
	if err != nil {
		t.Fatalf("signStakingEnvelope: %v", err)
	}

	if signed.Sender != address {
		t.Fatalf("sender must be derived from the keystore key: got %s want %s",
			signed.Sender, address)
	}

	// Re-express through the SERVER's struct and verify the signature the
	// way the handler does.
	server := api.StakingEnvelope{
		ID:           signed.ID,
		Sender:       signed.Sender,
		Action:       signed.Action,
		Validator:    signed.Validator,
		Amount:       signed.Amount,
		UnbondBlocks: signed.UnbondBlocks,
		Nonce:        signed.Nonce,
		Timestamp:    signed.Timestamp,
	}
	canonical, err := json.Marshal(server)
	if err != nil {
		t.Fatal(err)
	}

	pubBytes, err := hex.DecodeString(signed.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	var pub mldsa87.PublicKey
	if err := pub.UnmarshalBinary(pubBytes); err != nil {
		t.Fatal(err)
	}
	sigBytes, err := hex.DecodeString(signed.Signature)
	if err != nil {
		t.Fatal(err)
	}

	if !mldsa87.Verify(&pub, canonical, nil, sigBytes) {
		t.Fatal("signature does not verify under the server's canonical form — " +
			"the client and server envelope shapes have diverged")
	}

	// And the sender binding the handler enforces must hold.
	sum := sha256.Sum256(pubBytes)
	if hex.EncodeToString(sum[:]) != server.Sender {
		t.Fatal("sender must equal hex(sha256(public_key)) or the handler returns 401")
	}
}

// A mismatched --sender must be refused rather than silently re-signed as
// the keystore's own address.
func TestSignStakingEnvelope_rejectsSenderMismatch(t *testing.T) {
	walletPath, passFile, _ := newTestKeystore(t)

	_, err := signStakingEnvelope(stakingEnvelope{
		ID:        "stake-test-2",
		Sender:    "deadbeef",
		Action:    "delegate",
		Validator: "home-pc",
		Amount:    100,
		Timestamp: "2026-08-10T00:00:00Z",
	}, walletPath, passFile)
	if err == nil {
		t.Fatal("a sender that does not match the keystore must be refused")
	}
}

// The unbond variant must carry its lock-up through to the signed envelope.
func TestSignStakingEnvelope_carriesUnbondBlocks(t *testing.T) {
	walletPath, passFile, _ := newTestKeystore(t)

	signed, err := signStakingEnvelope(stakingEnvelope{
		ID:           "stake-test-3",
		Action:       "begin_unbond",
		Validator:    "home-pc",
		Amount:       50,
		UnbondBlocks: 250,
		Timestamp:    "2026-08-10T00:00:00Z",
	}, walletPath, passFile)
	if err != nil {
		t.Fatal(err)
	}
	if signed.UnbondBlocks != 250 {
		t.Fatalf("unbond_blocks lost in signing: %d", signed.UnbondBlocks)
	}
	if signed.Signature == "" || signed.PublicKey == "" {
		t.Fatal("signed envelope must carry signature and public key")
	}
}
