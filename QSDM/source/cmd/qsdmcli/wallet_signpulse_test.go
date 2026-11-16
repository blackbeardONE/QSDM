package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/pulseclient"
)

func runCLIWithStdin(t *testing.T, stdinJSON string, fn func() error) (string, string, error) {
	t.Helper()
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
	outCh, errCh := make(chan []byte, 1), make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(stdoutR); outCh <- b }()
	go func() { b, _ := io.ReadAll(stderrR); errCh <- b }()
	err := fn()
	_ = stdoutW.Close()
	_ = stderrW.Close()
	return strings.TrimSpace(string(<-outCh)), strings.TrimSpace(string(<-errCh)), err
}

func pulsePassFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pass.txt")
	if err := os.WriteFile(p, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWalletSignPulseAction_JoinVerifies(t *testing.T) {
	path, address, pubHex := makeKeystoreFile(t)
	pool := strings.Repeat("cd", 32)
	req := fmt.Sprintf(`{"sender":%q,"nonce":4,"fee":0.001,"action":{"op":"join","pool":%q}}`, address, pool)
	c := &CLI{}
	out, _, err := runCLIWithStdin(t, req, func() error {
		return c.walletSignPulseAction([]string{"--in", path, "--passphrase-file", pulsePassFile(t)})
	})
	if err != nil {
		t.Fatal(err)
	}
	var env pulseclient.Envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("output %q: %v", out, err)
	}
	if env.Sender != address || env.Nonce != 4 || env.Fee != 0.001 || env.PublicKey != pubHex {
		t.Fatalf("envelope %+v", env)
	}
	if string(env.Payload) != fmt.Sprintf(`{"op":"join","pool":%q}`, pool) {
		t.Fatalf("payload %s", env.Payload)
	}
	if err := pulseclient.VerifyEnvelope(&env); err != nil {
		t.Fatal(err)
	}
}

func TestWalletSignPulseAction_Refusals(t *testing.T) {
	path, address, _ := makeKeystoreFile(t)
	pass := pulsePassFile(t)
	other := strings.Repeat("00", 32)
	cases := map[string]string{
		"another wallet":  fmt.Sprintf(`{"sender":%q,"nonce":1,"fee":0,"action":{"op":"leave"}}`, other),
		"settle":          fmt.Sprintf(`{"sender":%q,"nonce":1,"fee":0,"action":{"op":"settle"}}`, address),
		"join no pool":    fmt.Sprintf(`{"sender":%q,"nonce":1,"fee":0,"action":{"op":"join"}}`, address),
		"unknown field":   fmt.Sprintf(`{"sender":%q,"nonce":1,"fee":0,"amount":5,"action":{"op":"leave"}}`, address),
		"empty":           ``,
		"reclaim nothing": fmt.Sprintf(`{"sender":%q,"nonce":1,"fee":0,"action":{"op":"reclaim"}}`, address),
	}
	for name, req := range cases {
		c := &CLI{}
		out, _, err := runCLIWithStdin(t, req, func() error {
			return c.walletSignPulseAction([]string{"--in", path, "--passphrase-file", pass})
		})
		if err == nil {
			t.Fatalf("%s: signed %q", name, out)
		}
	}
}

func TestWalletSignPulseRegister(t *testing.T) {
	path, address, pubHex := makeKeystoreFile(t)
	c := &CLI{}
	out, _, err := runCLIWithStdin(t, "", func() error {
		return c.walletSignPulseRegister([]string{"--in", path, "--passphrase-file", pulsePassFile(t), "--worker", "rig1"})
	})
	if err != nil {
		t.Fatal(err)
	}
	var r struct{ Login, PublicKey, Signature string }
	if err := json.Unmarshal([]byte(out), &struct {
		Login     *string `json:"login"`
		PublicKey *string `json:"public_key"`
		Signature *string `json:"signature"`
	}{&r.Login, &r.PublicKey, &r.Signature}); err != nil {
		t.Fatal(err)
	}
	if r.Login != address+".rig1" || r.PublicKey != pubHex || len(r.Signature) == 0 {
		t.Fatalf("register %+v", r)
	}
	c2 := &CLI{}
	if _, _, err := runCLIWithStdin(t, "", func() error {
		return c2.walletSignPulseRegister([]string{"--in", path, "--passphrase-file", pulsePassFile(t), "--worker", "bad name"})
	}); err == nil {
		t.Fatal("a worker name with a space was signed")
	}
}
