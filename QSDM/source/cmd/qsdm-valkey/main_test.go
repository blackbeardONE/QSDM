package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func addressFrom(t *testing.T, output string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if value, ok := strings.CutPrefix(line, "validator_address: "); ok {
			return value
		}
	}
	t.Fatalf("no validator_address in output %q", output)
	return ""
}

func TestGenerateLoadsWithNodeLoader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "validator-consensus-key.json")
	code, stdout, stderr := runCLI(t, "generate", "--out", path)
	if code != 0 {
		t.Fatalf("generate exit %d: %s", code, stderr)
	}
	address := addressFrom(t, stdout)
	if len(address) != 64 {
		t.Fatalf("address %q is not 32-byte hex", address)
	}
	if !strings.Contains(stdout, "public_key_fingerprint: SHA256:") {
		t.Fatalf("missing fingerprint in %q", stdout)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if v, _ := stored["version"].(float64); v != 1 {
		t.Fatalf("version = %v, want 1", stored["version"])
	}
	if alg, _ := stored["algorithm"].(string); alg != "ml-dsa-87" {
		t.Fatalf("algorithm = %q, want ml-dsa-87", alg)
	}
	if priv, _ := stored["private_key"].(string); priv == "" || strings.Contains(stdout+stderr, priv) {
		t.Fatal("private key missing from file or leaked to output")
	}
	if pub, _ := stored["public_key"].(string); pub == "" || strings.Contains(stdout+stderr, pub) {
		t.Fatal("public key missing from file or printed in full")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("key file mode = %o, want 600", got)
		}
	}

	// The node's own startup path must accept the file and reuse it.
	signer, created, err := chain.LoadOrCreateBFTSigner(path)
	if err != nil {
		t.Fatalf("node loader rejected generated key: %v", err)
	}
	if created {
		t.Fatal("node loader created a new key instead of loading the generated one")
	}
	if signer.Address() != address {
		t.Fatalf("node address %s != generated address %s", signer.Address(), address)
	}
	if chain.BFTValidatorAddress(signer.GetPublicKey()) != address {
		t.Fatal("address is not SHA-256 of the public key")
	}

	code, stdout, stderr = runCLI(t, "show", "--in", path)
	if code != 0 {
		t.Fatalf("show exit %d: %s", code, stderr)
	}
	if got := addressFrom(t, stdout); got != address {
		t.Fatalf("show address %s != %s", got, address)
	}
}

func TestGenerateRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "validator.json")
	if code, _, stderr := runCLI(t, "generate", "--out", path); code != 0 {
		t.Fatalf("first generate failed: %s", stderr)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runCLI(t, "generate", "--out", path)
	if code == 0 {
		t.Fatal("second generate must refuse to overwrite")
	}
	if !strings.Contains(stderr, "refusing to overwrite") {
		t.Fatalf("unexpected error: %s", stderr)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("existing key file was modified")
	}
}

func TestShowRejectsWrongVersionOrAlgorithm(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		value any
	}{
		{name: "version", field: "version", value: 2},
		{name: "algorithm", field: "algorithm", value: "ed25519"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "validator.json")
			if code, _, stderr := runCLI(t, "generate", "--out", path); code != 0 {
				t.Fatalf("generate failed: %s", stderr)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var stored map[string]any
			if err := json.Unmarshal(raw, &stored); err != nil {
				t.Fatal(err)
			}
			stored[tc.field] = tc.value
			tampered, err := json.Marshal(stored)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, tampered, 0o600); err != nil {
				t.Fatal(err)
			}
			code, stdout, stderr := runCLI(t, "show", "--in", path)
			if code == 0 {
				t.Fatalf("show accepted tampered key: %s", stdout)
			}
			if !strings.Contains(stderr, "unsupported version or algorithm") {
				t.Fatalf("unexpected error: %s", stderr)
			}
		})
	}
}

func TestShowMissingFileDoesNotCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "validator.json")
	if code, _, _ := runCLI(t, "show", "--in", path); code == 0 {
		t.Fatal("show must fail for a missing key")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("show must not create a key")
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"bogus"},
		{"generate"},
		{"show"},
		{"generate", "--out", "x", "extra"},
	} {
		if code, _, _ := runCLI(t, args...); code != 2 {
			t.Fatalf("args %q: exit %d, want 2", args, code)
		}
	}
}
