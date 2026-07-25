package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

func runSignStreamAction(t *testing.T, stdinJSON string, args []string) (string, string, error) {
	t.Helper()
	c := &CLI{}
	stdinR, stdinW, _ := os.Pipe()
	stdoutR, stdoutW, _ := os.Pipe()
	stderrR, stderrW, _ := os.Pipe()
	originalStdin, originalStdout, originalStderr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = stdinR, stdoutW, stderrW
	defer func() { os.Stdin, os.Stdout, os.Stderr = originalStdin, originalStdout, originalStderr }()

	go func() {
		_, _ = stdinW.WriteString(stdinJSON)
		_ = stdinW.Close()
	}()
	stdoutChannel := make(chan []byte, 1)
	stderrChannel := make(chan []byte, 1)
	go func() { output, _ := io.ReadAll(stdoutR); stdoutChannel <- output }()
	go func() { output, _ := io.ReadAll(stderrR); stderrChannel <- output }()

	commandErr := c.walletSignStreamAction(args)
	_ = stdoutW.Close()
	_ = stderrW.Close()
	return strings.TrimSpace(string(<-stdoutChannel)), strings.TrimSpace(string(<-stderrChannel)), commandErr
}

func TestWalletSignStreamActionProducesAPIEnvelope(t *testing.T) {
	keystorePath, address, publicKeyHex := makeKeystoreFile(t)
	passphrasePath := filepath.Join(t.TempDir(), "pass.txt")
	if err := os.WriteFile(passphrasePath, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	input := fmt.Sprintf(`{
		"id":"stream-open-cli",
		"sender":%q,
		"stream_id":"vpn-device-cli",
		"action":"open",
		"provider":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"service_id":"qsdm-vpn",
		"device_id_hash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"session_public_key":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		"price_dust":200000000,
		"price_period_seconds":2592000,
		"budget_dust":200000000,
		"max_active_seconds":2592000,
		"expires_at":"2026-09-01T00:00:00Z",
		"timestamp":"2026-08-01T00:00:00Z"
	}`, address)

	stdout, _, err := runSignStreamAction(t, input, []string{
		"--in", keystorePath,
		"--passphrase-file", passphrasePath,
		"--action-file", "-",
		"--nonce", "9",
	})
	if err != nil {
		t.Fatalf("walletSignStreamAction: %v", err)
	}
	var envelope streamActionEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("decode output: %v\n%s", err, stdout)
	}
	if envelope.PublicKey != publicKeyHex || envelope.Signature == "" {
		t.Fatalf("incomplete envelope: %+v", envelope)
	}
	if envelope.Action.Nonce != 9 || envelope.Action.StreamID != "vpn-device-cli" {
		t.Fatalf("unexpected action: %+v", envelope.Action)
	}
	message, err := chain.StreamActionSigningBytes(envelope.Action)
	if err != nil {
		t.Fatal(err)
	}
	publicKeyBytes, _ := hex.DecodeString(envelope.PublicKey)
	signatureBytes, _ := hex.DecodeString(envelope.Signature)
	var publicKey mldsa87.PublicKey
	if err := publicKey.UnmarshalBinary(publicKeyBytes); err != nil {
		t.Fatal(err)
	}
	if !mldsa87.Verify(&publicKey, message, nil, signatureBytes) {
		t.Fatal("stream action signature does not verify")
	}
}

func TestWalletSignStreamActionRejectsWrongWallet(t *testing.T) {
	keystorePath, _, _ := makeKeystoreFile(t)
	passphrasePath := filepath.Join(t.TempDir(), "pass.txt")
	if err := os.WriteFile(passphrasePath, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	input := `{
		"id":"stream-close-cli",
		"sender":"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		"stream_id":"vpn-device-cli",
		"action":"close",
		"timestamp":"2026-08-01T00:00:00Z"
	}`
	_, _, err := runSignStreamAction(t, input, []string{
		"--in", keystorePath,
		"--passphrase-file", passphrasePath,
		"--action-file", "-",
	})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("wrong wallet error = %v", err)
	}
}
