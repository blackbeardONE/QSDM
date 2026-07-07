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

	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

func runSignTaskAction(t *testing.T, stdinJSON string, args []string) (string, string, error) {
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

	stdoutCh := make(chan []byte, 1)
	stderrCh := make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(stdoutR); stdoutCh <- b }()
	go func() { b, _ := io.ReadAll(stderrR); stderrCh <- b }()

	cmdErr := c.walletSignTaskAction(args)
	_ = stdoutW.Close()
	_ = stderrW.Close()

	stdoutB := <-stdoutCh
	stderrB := <-stderrCh
	return strings.TrimSpace(string(stdoutB)), strings.TrimSpace(string(stderrB)), cmdErr
}

func TestWalletSignTaskAction_HappyPath(t *testing.T) {
	path, address, pubHex := makeKeystoreFile(t)

	envIn := fmt.Sprintf(`{
		"id":"action0000000001",
		"sender":%q,
		"task_id":"qsdm-task-1",
		"action":"start",
		"payload":"{\"mode\":\"service\"}",
		"timestamp":"2026-05-28T00:00:00Z"
	}`, address)

	passFile := filepath.Join(t.TempDir(), "pass.txt")
	if err := os.WriteFile(passFile, []byte("test"), 0o600); err != nil {
		t.Fatalf("write passfile: %v", err)
	}

	out, _, err := runSignTaskAction(t, envIn, []string{
		"--in", path,
		"--passphrase-file", passFile,
		"--envelope-file", "-",
		"--nonce", "9",
	})
	if err != nil {
		t.Fatalf("walletSignTaskAction: %v", err)
	}
	if out == "" {
		t.Fatal("empty stdout (expected signed task action envelope JSON)")
	}

	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode signed task action envelope: %v body=%s", err, out)
	}
	if got["signature"] == nil || got["signature"] == "" {
		t.Fatal("output envelope missing signature")
	}
	if got["public_key"] != pubHex {
		t.Fatalf("public_key: want %q got %v", pubHex, got["public_key"])
	}
	if got["nonce"] != float64(9) {
		t.Fatalf("nonce: want 9 got %v", got["nonce"])
	}

	verifyTaskActionSignature(t, got, pubHex)
}

func TestWalletSignTaskAction_SenderMismatch(t *testing.T) {
	path, _, _ := makeKeystoreFile(t)

	envIn := `{
		"id":"action0000000002",
		"sender":"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		"task_id":"qsdm-task-1",
		"action":"start",
		"timestamp":"2026-05-28T00:00:00Z"
	}`

	passFile := filepath.Join(t.TempDir(), "pass.txt")
	if err := os.WriteFile(passFile, []byte("test"), 0o600); err != nil {
		t.Fatalf("write passfile: %v", err)
	}

	_, _, err := runSignTaskAction(t, envIn, []string{
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

func TestWalletSignTaskAction_MissingRequiredFields(t *testing.T) {
	path, _, _ := makeKeystoreFile(t)
	passFile := filepath.Join(t.TempDir(), "pass.txt")
	if err := os.WriteFile(passFile, []byte("test"), 0o600); err != nil {
		t.Fatalf("write passfile: %v", err)
	}

	_, _, err := runSignTaskAction(t, `{"id":"action0000000003"}`, []string{
		"--in", path,
		"--passphrase-file", passFile,
		"--envelope-file", "-",
	})
	if err == nil {
		t.Fatal("expected missing-required-fields error, got nil")
	}
	if !strings.Contains(err.Error(), "missing one of") {
		t.Fatalf("expected missing-fields error, got %v", err)
	}
}

func verifyTaskActionSignature(t *testing.T, signed map[string]interface{}, pubHex string) {
	t.Helper()
	sigHex, _ := signed["signature"].(string)
	if sigHex == "" {
		t.Fatal("verifyTaskActionSignature: signed envelope has empty signature")
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		t.Fatalf("verifyTaskActionSignature: decode signature: %v", err)
	}

	rawAll, _ := json.Marshal(signed)
	var env taskActionEnvelope
	if err := json.Unmarshal(rawAll, &env); err != nil {
		t.Fatalf("verifyTaskActionSignature: re-unmarshal: %v", err)
	}
	env.Signature = ""
	env.PublicKey = ""
	canonical, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("verifyTaskActionSignature: marshal canonical: %v", err)
	}

	pubBytes, err := hex.DecodeString(pubHex)
	if err != nil {
		t.Fatalf("verifyTaskActionSignature: decode pubkey: %v", err)
	}
	var pk mldsa87.PublicKey
	if err := pk.UnmarshalBinary(pubBytes); err != nil {
		t.Fatalf("verifyTaskActionSignature: unmarshal pubkey: %v", err)
	}
	if !mldsa87.Verify(&pk, canonical, nil, sig) {
		t.Fatal("verifyTaskActionSignature: signature does not verify over canonical bytes")
	}
}
