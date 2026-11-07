package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// runCLIWithIO pipes stdin into fn and captures its stdout and stderr.
func runCLIWithIO(t *testing.T, stdin string, fn func() error) (string, string, error) {
	t.Helper()
	stdinR, stdinW, _ := os.Pipe()
	stdoutR, stdoutW, _ := os.Pipe()
	stderrR, stderrW, _ := os.Pipe()
	originalStdin, originalStdout, originalStderr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = stdinR, stdoutW, stderrW
	defer func() { os.Stdin, os.Stdout, os.Stderr = originalStdin, originalStdout, originalStderr }()
	go func() {
		_, _ = stdinW.WriteString(stdin)
		_ = stdinW.Close()
	}()
	stdoutChannel := make(chan []byte, 1)
	stderrChannel := make(chan []byte, 1)
	go func() { output, _ := io.ReadAll(stdoutR); stdoutChannel <- output }()
	go func() { output, _ := io.ReadAll(stderrR); stderrChannel <- output }()
	err := fn()
	_ = stdoutW.Close()
	_ = stderrW.Close()
	return strings.TrimSpace(string(<-stdoutChannel)), strings.TrimSpace(string(<-stderrChannel)), err
}

func qsdm20TestKeystore(t *testing.T) (keystorePath, passphrasePath, address string) {
	t.Helper()
	keystorePath, address, _ = makeKeystoreFile(t)
	passphrasePath = filepath.Join(t.TempDir(), "pass.txt")
	if err := os.WriteFile(passphrasePath, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	return keystorePath, passphrasePath, address
}

// nodeTxFromSubmitBody builds the chain transaction the way the node's
// submit route does (pkg/api.QSDM20TxFromRequest): canonical payload, fee
// in CELL and the id bound to the signing digest.
func nodeTxFromSubmitBody(t *testing.T, stdout string) (*mempool.Tx, qsdm20SubmitBody) {
	t.Helper()
	var body qsdm20SubmitBody
	if err := decodeStrict([]byte(stdout), &body); err != nil {
		t.Fatalf("output is not a submit body: %v\n%s", err, stdout)
	}
	payload, err := json.Marshal(body.Payload)
	if err != nil {
		t.Fatal(err)
	}
	fee, err := chain.QSDM20FeeCell(body.FeeDust)
	if err != nil {
		t.Fatal(err)
	}
	digest := chain.QSDM20SigningDigest(body.Sender, body.Nonce, body.FeeDust, body.Payload)
	return &mempool.Tx{
		ID:         chain.QSDM20TxID(digest),
		Sender:     body.Sender,
		Fee:        fee,
		Nonce:      body.Nonce,
		Payload:    payload,
		ContractID: chain.QSDM20ContractID,
		Signature:  body.Signature,
		PublicKey:  body.PublicKey,
	}, body
}

func TestWalletSignTokenTxIsWhatANodeVerifies(t *testing.T) {
	keystorePath, passphrasePath, address := qsdm20TestKeystore(t)
	c := &CLI{}
	input := fmt.Sprintf(`{"sender":%q,"nonce":0,"fee_dust":1500,"payload":{"action":"create","symbol":"GROOT","name":"Groot <Coin> & co","decimals":8,"max_supply":"1000000","initial_supply":"250","mint":"issuer"}}`, address)
	stdout, stderr, err := runCLIWithIO(t, input, func() error {
		return c.walletSignTokenTx([]string{"--in", keystorePath, "--passphrase-file", passphrasePath})
	})
	if err != nil {
		t.Fatalf("sign-token-tx: %v", err)
	}
	tx, body := nodeTxFromSubmitBody(t, stdout)
	action, err := chain.VerifyQSDM20Tx(tx)
	if err != nil {
		t.Fatalf("the node refuses what sign-token-tx signed: %v", err)
	}
	if action.Name != "Groot <Coin> & co" || body.Nonce != 0 || body.FeeDust != 1500 || body.Sender != address {
		t.Fatalf("signed %+v", body)
	}
	if !strings.Contains(stderr, "tx_id="+tx.ID) || !strings.Contains(stderr, "token="+chain.QSDM20TokenID(address, 0)) {
		t.Fatalf("stderr = %q", stderr)
	}

	// A bare action with the nonce and fee as flags.
	transfer := `{"action":"transfer","token":"` + strings.Repeat("cd", 32) + `","to":"` + strings.Repeat("ef", 32) + `","amount":"1000"}`
	stdout, _, err = runCLIWithIO(t, transfer, func() error {
		return c.walletSignTokenTx([]string{"--in", keystorePath, "--passphrase-file", passphrasePath, "--nonce", "7", "--fee-dust", "0"})
	})
	if err != nil {
		t.Fatalf("sign-token-tx transfer: %v", err)
	}
	tx, body = nodeTxFromSubmitBody(t, stdout)
	if _, err := chain.VerifyQSDM20Tx(tx); err != nil {
		t.Fatalf("the node refuses the signed transfer: %v", err)
	}
	if body.Nonce != 7 || body.Payload.Amount != "1000" {
		t.Fatalf("signed %+v", body)
	}

	// Tampering after signing is caught by the node's check.
	body.Payload.Amount = "1001"
	tampered, _ := json.Marshal(body)
	tx, _ = nodeTxFromSubmitBody(t, string(tampered))
	if _, err := chain.VerifyQSDM20Tx(tx); !errors.Is(err, chain.ErrQSDM20Signature) {
		t.Fatalf("tampered transfer: %v", err)
	}
}

func TestWalletSignTokenTxAutoNonce(t *testing.T) {
	keystorePath, passphrasePath, address := qsdm20TestKeystore(t)
	answer := fmt.Sprintf(`{"address":%q,"balance":3,"nonce":12,"present":true}`, address)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/mining/account" || r.URL.Query().Get("address") != address {
			t.Errorf("unexpected request %s", r.URL)
		}
		_, _ = w.Write([]byte(answer))
	}))
	defer srv.Close()
	c := &CLI{}
	burn := `{"action":"burn","token":"` + strings.Repeat("cd", 32) + `","amount":"5"}`
	sign := func() (string, error) {
		stdout, _, err := runCLIWithIO(t, burn, func() error {
			return c.walletSignTokenTx([]string{"--in", keystorePath, "--passphrase-file", passphrasePath, "--auto-nonce", "--api-url", srv.URL})
		})
		return stdout, err
	}
	stdout, err := sign()
	if err != nil {
		t.Fatal(err)
	}
	tx, body := nodeTxFromSubmitBody(t, stdout)
	if _, err := chain.VerifyQSDM20Tx(tx); err != nil || body.Nonce != 12 {
		t.Fatalf("auto nonce: %v %+v", err, body)
	}
	answer = fmt.Sprintf(`{"address":%q,"balance":0,"nonce":0,"present":false}`, address)
	if _, err := sign(); err == nil || !strings.Contains(err.Error(), "no CELL account") {
		t.Fatalf("accountless sender: %v", err)
	}
	answer = `{"address":"` + strings.Repeat("ab", 32) + `","balance":1,"nonce":1,"present":true}`
	if _, err := sign(); err == nil || !strings.Contains(err.Error(), "refusing to trust") {
		t.Fatalf("foreign answer: %v", err)
	}
}

func TestWalletSignTokenTxRefusals(t *testing.T) {
	keystorePath, passphrasePath, _ := qsdm20TestKeystore(t)
	c := &CLI{}
	transfer := `{"action":"transfer","token":"` + strings.Repeat("cd", 32) + `","to":"` + strings.Repeat("ef", 32) + `","amount":"1"}`
	cases := []struct {
		name, input string
		args        []string
		want        string
	}{
		{"no nonce", transfer, nil, "exactly one way"},
		{"two nonces", `{"nonce":1,"payload":` + transfer + `}`, []string{"--nonce", "2"}, "exactly one way"},
		{"foreign sender", `{"sender":"` + strings.Repeat("ab", 32) + `","nonce":1,"payload":` + transfer + `}`, nil, "does not match"},
		{"invalid action", strings.Replace(transfer, `"amount":"1"`, `"amount":"01"`, 1), []string{"--nonce", "1"}, "leading zero"},
		{"unknown field", strings.Replace(transfer, `"amount":"1"`, `"amount":"1","memo":"x"`, 1), []string{"--nonce", "1"}, "unknown field"},
		{"unknown wrapper field", `{"nonce":1,"memo":"x","payload":` + transfer + `}`, nil, "unknown field"},
		{"empty", "  ", []string{"--nonce", "1"}, "refusing to sign nothing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--in", keystorePath, "--passphrase-file", passphrasePath}, tc.args...)
			_, _, err := runCLIWithIO(t, tc.input, func() error { return c.walletSignTokenTx(args) })
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestQSDM20CommandSubmitAndReads(t *testing.T) {
	var posted []byte
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		if r.Method == http.MethodPost {
			posted, _ = io.ReadAll(r.Body)
		}
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	}))
	defer srv.Close()
	c := &CLI{baseURL: srv.URL + "/api/v1", client: srv.Client()}
	body := qsdm20SubmitBody{Sender: strings.Repeat("ab", 32), Nonce: 3, FeeDust: 9,
		Payload:   chain.QSDM20Action{Action: chain.QSDM20ActionRenounce, Token: strings.Repeat("cd", 32)},
		PublicKey: "aa", Signature: "bb"}
	raw, _ := json.Marshal(body)
	if _, _, err := runCLIWithIO(t, string(raw), func() error { return c.qsdm20Command([]string{"submit"}) }); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(posted, raw) {
		t.Fatalf("posted %s, want %s", posted, raw)
	}
	unsigned, _ := json.Marshal(qsdm20SubmitBody{Sender: body.Sender, Payload: body.Payload})
	if _, _, err := runCLIWithIO(t, string(unsigned), func() error { return c.qsdm20Command([]string{"submit"}) }); err == nil || !strings.Contains(err.Error(), "not signed") {
		t.Fatalf("unsigned submit: %v", err)
	}
	token := strings.Repeat("cd", 32)
	for _, args := range [][]string{
		{"tokens", "--limit", "2", "--cursor", "5:" + token},
		{"token", token},
		{"holders", token, "--limit", "50"},
		{"balances", body.Sender},
	} {
		if _, _, err := runCLIWithIO(t, "", func() error { return c.qsdm20Command(args) }); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	want := []string{
		"/api/v1/qsdm20/submit",
		"/api/v1/qsdm20/tokens?cursor=5%3A" + token + "&limit=2",
		"/api/v1/qsdm20/tokens/" + token,
		"/api/v1/qsdm20/tokens/" + token + "/holders?limit=50",
		"/api/v1/qsdm20/balances/" + body.Sender,
	}
	if strings.Join(paths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("paths:\n%s\nwant:\n%s", strings.Join(paths, "\n"), strings.Join(want, "\n"))
	}
	stdout, _, err := runCLIWithIO(t, "", func() error {
		return c.qsdm20Command([]string{"token-id", strings.Repeat("ab", 32), "7"})
	})
	if err != nil || stdout != "78d65edcdf5af01c85401a31e3ffd6a4e19e90c16a62f3a222650ef8497b768a" {
		t.Fatalf("token-id = %q %v", stdout, err)
	}
	if _, _, err := runCLIWithIO(t, "", func() error { return c.qsdm20Command([]string{"token-id", "AB", "7"}) }); err == nil {
		t.Fatal("token-id accepted a bad creator")
	}
}
