// Session 98 — v0.4.0 production /wallet/submit-signed smoke test.
//
// Runs three negative-path probes against the live BLR1 validator at
// https://api.qsdm.tech/api/v1/wallet/submit-signed. Each probe ends
// the handler at a different terminal state, with NO state mutation
// on production (i.e. no StoreTransaction call). This is the
// post-release safety check we run before declaring the v0.4.0
// signing pipeline production-ready end-to-end.
//
// Probes:
//
//	1. bad-sig         valid keypair, valid envelope, signature
//	                   tampered → HTTP 422 signature_invalid
//	                   (exercises: JSON parse, shape validate, hex
//	                    decode, sender == hex(sha256(pubkey)) check,
//	                    ML-DSA-87 verify, monitoring counter bump)
//
//	2. sender-mismatch valid keypair + valid sig over a payload
//	                   whose `sender` doesn't match
//	                   hex(sha256(pubkey)) → HTTP 400
//	                   "envelope.sender does not match…"
//	                   (exercises: sender-binding check)
//
//	3. malformed-json  truncated body → HTTP 400
//	                   "invalid envelope: …"
//	                   (exercises: JSON decoder + bounded body
//	                    reader + rate-limit + publicPaths)
//
// To run:
//
//	cd QSDM/source
//	set CGO_ENABLED=0
//	go run ./cmd/v040smoke
//
// The program is intentionally non-test (no go test framework) so
// CI does not regress on production-endpoint reachability during
// unrelated PRs.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/crypto"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

const (
	endpoint = "https://api.qsdm.tech/api/v1/wallet/submit-signed"
	timeout  = 15 * time.Second
)

func main() {
	fmt.Println("=== v0.4.0 /wallet/submit-signed smoke test ===")
	fmt.Println("Endpoint:", endpoint)
	fmt.Println()

	// Use the same Dilithium handle the server uses. Under !cgo
	// this is the circl/mldsa/mldsa87 pure-Go implementation;
	// the server's VerifySignature path uses the same backend
	// for !cgo builds (pkg/crypto/dilithium_circl.go).
	di := crypto.NewDilithium()
	if di == nil {
		fmt.Fprintln(os.Stderr, "ERROR: NewDilithium returned nil — !cgo build expected")
		os.Exit(1)
	}
	pubKey := di.GetPublicKey()
	if len(pubKey) != 2592 {
		fmt.Fprintf(os.Stderr, "ERROR: unexpected pubkey len %d (want 2592)\n", len(pubKey))
		os.Exit(1)
	}
	addr := sha256.Sum256(pubKey)
	sender := hex.EncodeToString(addr[:])
	fmt.Println("Generated keypair:")
	fmt.Println("  pubkey  len  =", len(pubKey), "bytes")
	fmt.Println("  sender (hex) =", sender)
	fmt.Println()

	// Build a wire-correct, valid-signature envelope. We'll mutate it
	// per-probe before posting.
	recipient := strings.Repeat("0", 64)
	now := time.Now().UTC()
	txIDSeed := sha256.Sum256([]byte(sender + recipient + now.Format(time.RFC3339Nano)))
	env := wallet.TransactionData{
		ID:          hex.EncodeToString(txIDSeed[:16]),
		Sender:      sender,
		Recipient:   recipient,
		Amount:      0.000001,
		Fee:         0.0000001,
		GeoTag:      "US",
		ParentCells: []string{strings.Repeat("a", 32), strings.Repeat("b", 32)},
		Timestamp:   now.Format(time.RFC3339),
	}
	canonical, err := json.Marshal(env)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR: canonical marshal:", err)
		os.Exit(1)
	}
	sig, err := di.Sign(canonical)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR: sign:", err)
		os.Exit(1)
	}
	if len(sig) != 4627 {
		fmt.Fprintf(os.Stderr, "ERROR: unexpected sig len %d (want 4627)\n", len(sig))
		os.Exit(1)
	}
	env.Signature = hex.EncodeToString(sig)
	env.PublicKey = hex.EncodeToString(pubKey)

	pass := 0
	fail := 0

	// ---- probe 1: bad signature ----
	{
		envBad := env
		// Flip one byte at offset 100 in the hex sig (hex is 9254
		// chars; index 100 is well within the sig body).
		sigHex := []byte(envBad.Signature)
		if sigHex[100] == 'a' {
			sigHex[100] = 'b'
		} else {
			sigHex[100] = 'a'
		}
		envBad.Signature = string(sigHex)
		status, body := postJSON(envBad)
		want := 422
		fmt.Println("[probe 1] bad-signature")
		fmt.Println("  expect HTTP", want, " got HTTP", status)
		fmt.Println("  body:", trimBody(body))
		if status == want {
			pass++
			fmt.Println("  RESULT: PASS")
		} else {
			fail++
			fmt.Println("  RESULT: FAIL")
		}
		fmt.Println()
	}

	// ---- probe 2: sender mismatch ----
	{
		envBad := env
		// Flip the first 4 hex chars of `sender`. The signature is
		// re-validated AFTER the sender-mismatch check, so the handler
		// terminates at HTTP 400 before getting to signature verification.
		mismatchedSender := []byte(envBad.Sender)
		for i := 0; i < 4; i++ {
			if mismatchedSender[i] == '0' {
				mismatchedSender[i] = '1'
			} else {
				mismatchedSender[i] = '0'
			}
		}
		envBad.Sender = string(mismatchedSender)
		// NOTE: We do NOT re-sign — the new sender field is what
		// the handler will sha256/compare-against, and the
		// public_key (still in envBad) hashes to the original
		// sender. So sender != hex(sha256(pubkey)) and the handler
		// returns 400 before signature verification.
		status, body := postJSON(envBad)
		want := 400
		fmt.Println("[probe 2] sender-mismatch")
		fmt.Println("  expect HTTP", want, " got HTTP", status)
		fmt.Println("  body:", trimBody(body))
		if status == want && strings.Contains(strings.ToLower(string(body)), "sender does not match") {
			pass++
			fmt.Println("  RESULT: PASS")
		} else {
			fail++
			fmt.Println("  RESULT: FAIL")
		}
		fmt.Println()
	}

	// ---- probe 3: malformed JSON ----
	{
		status, body := postRaw([]byte(`{"id":"abc","sender":"deadbeef","amount":1`))
		want := 400
		fmt.Println("[probe 3] malformed-json")
		fmt.Println("  expect HTTP", want, " got HTTP", status)
		fmt.Println("  body:", trimBody(body))
		if status == want {
			pass++
			fmt.Println("  RESULT: PASS")
		} else {
			fail++
			fmt.Println("  RESULT: FAIL")
		}
		fmt.Println()
	}

	fmt.Println("=== summary ===")
	fmt.Printf("PASS=%d FAIL=%d\n", pass, fail)
	if fail > 0 {
		os.Exit(1)
	}
}

// postJSON marshals env and POSTs it to the production endpoint.
// Returns (http_status, raw response body).
func postJSON(env wallet.TransactionData) (int, []byte) {
	b, err := json.Marshal(env)
	if err != nil {
		return 0, []byte(err.Error())
	}
	return postRaw(b)
}

// postRaw POSTs an arbitrary body. Used for the malformed-JSON probe.
func postRaw(body []byte) (int, []byte) {
	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, []byte(err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	cli := &http.Client{Timeout: timeout}
	resp, err := cli.Do(req)
	if err != nil {
		return 0, []byte(err.Error())
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, respBody
}

// trimBody returns body shortened to <= 240 chars so the smoke-test
// log stays readable when the server emits a long error string.
func trimBody(b []byte) string {
	s := strings.ReplaceAll(string(b), "\n", " ")
	if len(s) > 240 {
		s = s[:240] + "…"
	}
	return s
}
