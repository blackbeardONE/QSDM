package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/keystore"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// makeKeystoreFile builds a v1 keystore on disk with passphrase
// "test" and returns (path, addressHex, publicKeyHex). The
// caller is responsible for nothing — t.TempDir cleans up.
func makeKeystoreFile(t *testing.T) (path, address, pubHex string) {
	t.Helper()
	pk, sk, err := mldsa87.GenerateKey(nil)
	if err != nil {
		t.Fatalf("mldsa87.GenerateKey: %v", err)
	}
	pubBytes, err := pk.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary pk: %v", err)
	}
	privBytes, err := sk.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary sk: %v", err)
	}
	ks, err := keystore.Encrypt(pubBytes, privBytes, []byte("test"))
	if err != nil {
		t.Fatalf("keystore.Encrypt: %v", err)
	}
	data, err := keystore.Marshal(ks)
	if err != nil {
		t.Fatalf("keystore.Marshal: %v", err)
	}
	dir := t.TempDir()
	path = filepath.Join(dir, "wallet.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write keystore: %v", err)
	}
	sum := sha256.Sum256(pubBytes)
	return path, hex.EncodeToString(sum[:]), hex.EncodeToString(pubBytes)
}

// runSignTx invokes c.walletSignTx with the given flags after
// piping `stdinJSON` to the process's stdin. Captures stdout (the
// signed envelope, ~14 KiB for ML-DSA-87) and stderr (the
// informational "signed envelope ..." line). Returns
// (signedEnvelopeJSON, stderr, error).
//
// stdout + stderr are drained CONCURRENTLY with the CLI's
// execution: an ML-DSA-87 signature serialised as hex is ~9.3 KiB,
// which is larger than the OS pipe buffer (64 KiB on Windows but
// only 4 KiB on Linux/macOS by default). Without a concurrent
// drain, fmt.Println(signed) blocks on its write to stdout and
// the test deadlocks.
func runSignTx(t *testing.T, stdinJSON string, args []string) (string, string, error) {
	t.Helper()
	c := &CLI{}

	stdinR, stdinW, _ := os.Pipe()
	stdoutR, stdoutW, _ := os.Pipe()
	stderrR, stderrW, _ := os.Pipe()
	origStdin, origStdout, origStderr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = stdinR, stdoutW, stderrW
	defer func() { os.Stdin, os.Stdout, os.Stderr = origStdin, origStdout, origStderr }()

	go func() {
		_, _ = stdinW.WriteString(stdinJSON)
		_ = stdinW.Close()
	}()

	// Drain stdout + stderr concurrently to prevent pipe-buffer
	// deadlock on the ~14 KiB signed-envelope write.
	stdoutCh := make(chan []byte, 1)
	stderrCh := make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(stdoutR); stdoutCh <- b }()
	go func() { b, _ := io.ReadAll(stderrR); stderrCh <- b }()

	cmdErr := c.walletSignTx(args)
	_ = stdoutW.Close()
	_ = stderrW.Close()

	stdoutB := <-stdoutCh
	stderrB := <-stderrCh
	return strings.TrimSpace(string(stdoutB)), strings.TrimSpace(string(stderrB)), cmdErr
}

// TestWalletSignTx_HappyPath_NoNonce asserts the legacy v0.4.0
// path: an envelope with no --nonce / --auto-nonce flag leaves
// Nonce=0 (omitempty drops it from the canonical bytes) and
// produces a verifiable signature. This is the "drop-in
// replacement for `qsdmcli wallet sign --message-file canonical.json`"
// case the design doc Section 5.3 promises.
func TestWalletSignTx_HappyPath_NoNonce(t *testing.T) {
	path, address, pubHex := makeKeystoreFile(t)

	envIn := fmt.Sprintf(`{
		"id":"deadbeef00000000",
		"sender":%q,
		"recipient":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"amount":1.0,
		"fee":0.01,
		"geotag":"US",
		"parent_cells":["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"],
		"timestamp":"2026-05-14T17:00:00Z"
	}`, address)

	passFile := filepath.Join(t.TempDir(), "pass.txt")
	if err := os.WriteFile(passFile, []byte("test"), 0o600); err != nil {
		t.Fatalf("write passfile: %v", err)
	}

	out, _, err := runSignTx(t, envIn, []string{
		"--in", path,
		"--passphrase-file", passFile,
		"--envelope-file", "-",
	})
	if err != nil {
		t.Fatalf("walletSignTx: %v", err)
	}
	if out == "" {
		t.Fatal("empty stdout (expected signed envelope JSON)")
	}

	// Parse the output and assert the wire-shape contract:
	// signature populated, public_key populated, nonce absent
	// (omitempty + 0).
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode signed envelope: %v body=%s", err, out)
	}
	if got["signature"] == nil || got["signature"] == "" {
		t.Fatal("output envelope missing signature")
	}
	if got["public_key"] != pubHex {
		t.Fatalf("public_key: want %q got %v", pubHex, got["public_key"])
	}
	if _, hasNonce := got["nonce"]; hasNonce {
		t.Fatalf("legacy envelope must NOT carry nonce field (omitempty); got %v", got["nonce"])
	}

	// Verify the signature against the canonical bytes algorithm.
	verifySignature(t, got, pubHex)
}

// TestWalletSignTx_WithExplicitNonce asserts --nonce N stamps
// the literal value and produces a verifiable signature over the
// new canonical bytes (which now include the nonce field).
func TestWalletSignTx_WithExplicitNonce(t *testing.T) {
	path, address, pubHex := makeKeystoreFile(t)

	envIn := fmt.Sprintf(`{
		"id":"deadbeef00000001",
		"sender":%q,
		"recipient":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"amount":1.0,
		"fee":0.01,
		"geotag":"US",
		"parent_cells":["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"],
		"timestamp":"2026-05-14T17:00:00Z"
	}`, address)

	passFile := filepath.Join(t.TempDir(), "pass.txt")
	if err := os.WriteFile(passFile, []byte("test"), 0o600); err != nil {
		t.Fatalf("write passfile: %v", err)
	}

	out, _, err := runSignTx(t, envIn, []string{
		"--in", path,
		"--passphrase-file", passFile,
		"--envelope-file", "-",
		"--nonce", "42",
	})
	if err != nil {
		t.Fatalf("walletSignTx: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode signed envelope: %v body=%s", err, out)
	}
	if got["nonce"] != float64(42) { // json.Unmarshal numbers as float64
		t.Fatalf("nonce: want 42 got %v", got["nonce"])
	}
	verifySignature(t, got, pubHex)
}

// TestWalletSignTx_AutoNonce asserts the --auto-nonce path:
// CLI hits the stubbed /api/v1/wallet/nonce endpoint and stamps
// the `next` field from the response onto the envelope.
func TestWalletSignTx_AutoNonce(t *testing.T) {
	path, address, pubHex := makeKeystoreFile(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/wallet/nonce" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("sender"); got != address {
			t.Errorf("server: want sender=%s got %s", address, got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"sender": address,
			"nonce":  4,
			"next":   5,
		})
	}))
	defer server.Close()

	envIn := fmt.Sprintf(`{
		"id":"deadbeef00000002",
		"sender":%q,
		"recipient":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"amount":1.0,
		"fee":0.01,
		"geotag":"US",
		"parent_cells":["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"],
		"timestamp":"2026-05-14T17:00:00Z"
	}`, address)

	passFile := filepath.Join(t.TempDir(), "pass.txt")
	if err := os.WriteFile(passFile, []byte("test"), 0o600); err != nil {
		t.Fatalf("write passfile: %v", err)
	}

	out, _, err := runSignTx(t, envIn, []string{
		"--in", path,
		"--passphrase-file", passFile,
		"--envelope-file", "-",
		"--auto-nonce",
		"--api-url", server.URL,
	})
	if err != nil {
		t.Fatalf("walletSignTx: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode signed envelope: %v body=%s", err, out)
	}
	if got["nonce"] != float64(5) {
		t.Fatalf("nonce (from --auto-nonce): want 5 got %v", got["nonce"])
	}
	verifySignature(t, got, pubHex)
}

// TestWalletSignTx_SenderMismatch asserts the safety guard: if
// the envelope's sender field doesn't equal hex(sha256(public_key))
// of the keystore being opened, sign-tx errors out BEFORE
// touching the private key.
func TestWalletSignTx_SenderMismatch(t *testing.T) {
	path, _, _ := makeKeystoreFile(t)

	envIn := `{
		"id":"deadbeef00000003",
		"sender":"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		"recipient":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"amount":1.0,
		"fee":0.01,
		"geotag":"US",
		"parent_cells":["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"],
		"timestamp":"2026-05-14T17:00:00Z"
	}`

	passFile := filepath.Join(t.TempDir(), "pass.txt")
	if err := os.WriteFile(passFile, []byte("test"), 0o600); err != nil {
		t.Fatalf("write passfile: %v", err)
	}

	_, _, err := runSignTx(t, envIn, []string{
		"--in", path,
		"--passphrase-file", passFile,
		"--envelope-file", "-",
	})
	if err == nil {
		t.Fatal("expected sender-mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected 'does not match' error, got %v", err)
	}
}

// TestWalletSignTx_NonceAndAutoNonceMutex asserts the mutual-
// exclusion guard between --nonce and --auto-nonce.
func TestWalletSignTx_NonceAndAutoNonceMutex(t *testing.T) {
	path, _, _ := makeKeystoreFile(t)
	passFile := filepath.Join(t.TempDir(), "pass.txt")
	_ = os.WriteFile(passFile, []byte("test"), 0o600)

	_, _, err := runSignTx(t, `{"id":"x"}`, []string{
		"--in", path,
		"--passphrase-file", passFile,
		"--envelope-file", "-",
		"--nonce", "7",
		"--auto-nonce",
	})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutual-exclusion error, got %v", err)
	}
}

// verifySignature parses the signed envelope, strips signature +
// public_key, re-marshals (the EXACT canonicalisation algorithm
// pkg/api/handlers.go::SubmitSignedTransaction uses), and verifies
// the signature against the envelope's own public_key. This is the
// hard-line acceptance test for "the server will accept this
// signature." Any drift between the CLI's canonicalisation and the
// server's will surface as a failure here.
func verifySignature(t *testing.T, signed map[string]interface{}, pubHex string) {
	t.Helper()
	sigHex, _ := signed["signature"].(string)
	if sigHex == "" {
		t.Fatal("verifySignature: signed envelope has empty signature")
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		t.Fatalf("verifySignature: decode signature: %v", err)
	}

	// Strip + re-marshal the EXACT way the server does. To match
	// Go's struct-order serialisation we unmarshal into txEnvelope
	// (the local mirror) rather than rely on the map iteration
	// order — which is randomised.
	rawAll, _ := json.Marshal(signed)
	var env txEnvelope
	if err := json.Unmarshal(rawAll, &env); err != nil {
		t.Fatalf("verifySignature: re-unmarshal: %v", err)
	}
	env.Signature = ""
	env.PublicKey = ""
	canonical, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("verifySignature: marshal canonical: %v", err)
	}

	pubBytes, err := hex.DecodeString(pubHex)
	if err != nil {
		t.Fatalf("verifySignature: decode pubkey: %v", err)
	}
	var pk mldsa87.PublicKey
	if err := pk.UnmarshalBinary(pubBytes); err != nil {
		t.Fatalf("verifySignature: unmarshal pubkey: %v", err)
	}
	if !mldsa87.Verify(&pk, canonical, nil, sig) {
		t.Fatalf("verifySignature: signature does NOT verify over canonical bytes (the server would 422 this envelope)")
	}
}
