// qsdmcli wallet sign-tx — produce a fully-signed self-custody
// envelope for POST /api/v1/wallet/submit-signed without leaving the
// terminal. Companion to the browser wallet's Send tab and the
// `qsdm_wallet_sign_transaction` WASM helper used at qsdm.tech/wallet/.
//
// Why this exists: before v0.4.1 the CLI path required users to
// build the canonical envelope JSON by hand and pipe it through
// `qsdmcli wallet sign --message-file -`. The exact byte shape of
// the canonical payload — json.Marshal of pkg/wallet.TransactionData
// with signature + public_key cleared, with Go's field-declaration
// ordering and floating-point formatting — is implementation-defined
// in a way that hand-rolled JSON in another language almost never
// matches (e.g. JS's `JSON.stringify(1e-7)` emits "1e-7", Go's
// `json.Marshal` emits "1e-07"). This subcommand encapsulates the
// contract so a non-browser caller doesn't have to.
//
// Usage:
//
//	qsdmcli wallet sign-tx [--in PATH] [--passphrase-file FILE]
//	                       [--envelope-file PATH | '-']
//	                       [--nonce N | --auto-nonce]
//	                       [--api-url URL]
//
// Input on stdin (default) or --envelope-file: a JSON object with at
// minimum {id, sender, recipient, amount, fee, geotag, parent_cells,
// timestamp}. The `nonce`, `signature`, and `public_key` fields are
// ignored on input — sign-tx populates them.
//
// Output on stdout: the signed envelope JSON, ready to:
//
//	curl -fsS -H 'Content-Type: application/json' \
//	     --data-binary @- \
//	     https://api.qsdm.tech/api/v1/wallet/submit-signed
//
// --nonce vs --auto-nonce:
//
//   - --nonce N stamps the envelope with exactly N. Use this when
//     you've already queried /api/v1/wallet/nonce yourself or are
//     replaying an off-line ceremony.
//
//   - --auto-nonce (default OFF) issues a GET against --api-url's
//     /api/v1/wallet/nonce?sender=... and stamps the response's
//     `next` field. This is the one-shot convenience path; the cost
//     is a TLS handshake to --api-url before signing. If the lookup
//     fails (network error, 5xx, JSON shape drift) sign-tx exits
//     non-zero rather than silently stamping nonce=1 against an API
//     it can't trust.
//
//   - Neither flag set → nonce=0, the v0.4.0 backward-compat path.
//     /wallet/submit-signed accepts these for the v0.4.1 → v0.4.2
//     deprecation window; you'll see a "legacy v0.4.0 path" entry
//     in the validator's logs but no rejection.

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/keystore"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// txEnvelope mirrors pkg/wallet.TransactionData and the
// wasm_modules/wallet/cmd/qsdm-wallet/main.go::txEnvelope. Any
// drift between these three places will produce signatures the
// server cannot verify, so the comment block here is intentionally
// repeated verbatim across the WASM signer and the CLI.
//
// Field order is the wire contract: json.Marshal emits in struct
// declaration order, and the server canonicalises by parse →
// re-marshal, so any field reorder here invalidates every
// signature this command produces.
type txEnvelope struct {
	ID          string   `json:"id"`
	Sender      string   `json:"sender"`
	Recipient   string   `json:"recipient"`
	Amount      float64  `json:"amount"`
	Fee         float64  `json:"fee"`
	GeoTag      string   `json:"geotag"`
	ParentCells []string `json:"parent_cells"`
	// v0.4.1 (Session 99) per-sender replay counter. omitempty
	// is load-bearing: a Nonce of 0 (v0.4.0 backward-compat
	// envelopes) drops out of the canonical bytes entirely so
	// old signatures still verify on a v0.4.1 server.
	Nonce     uint64 `json:"nonce,omitempty"`
	Signature string `json:"signature"`
	// omitempty on PublicKey matches the server and is what
	// makes the canonical payload exclude `public_key` when we
	// clear it before signing.
	PublicKey string `json:"public_key,omitempty"`
	Timestamp string `json:"timestamp"`
}

// nonceResponse mirrors pkg/api.GetWalletNonceResponse.
type nonceResponse struct {
	Sender string `json:"sender"`
	Nonce  uint64 `json:"nonce"`
	Next   uint64 `json:"next"`
}

func (c *CLI) walletSignTx(args []string) error {
	fs := flag.NewFlagSet("wallet sign-tx", flag.ContinueOnError)
	in := fs.String("in", "", "keystore path (default: ~/.qsdm/wallet.json)")
	passphraseFile := fs.String("passphrase-file", "", "read passphrase from file ('-' for stdin); empty = prompt")
	envelopeFile := fs.String("envelope-file", "-", "JSON envelope to sign ('-' for stdin)")
	nonceFlag := fs.Uint64("nonce", 0, "v0.4.1 nonce to stamp on the envelope (mutually exclusive with --auto-nonce)")
	autoNonce := fs.Bool("auto-nonce", false, "fetch the next nonce from --api-url before signing")
	apiURL := fs.String("api-url", "https://api.qsdm.tech", "validator base URL for --auto-nonce (no trailing slash)")
	timeout := fs.Duration("api-timeout", 10*time.Second, "HTTP timeout for --auto-nonce lookup")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *nonceFlag != 0 && *autoNonce {
		return errors.New("--nonce and --auto-nonce are mutually exclusive (one stamps a literal value, the other resolves it from the validator)")
	}

	// Read + parse the input envelope.
	rawIn, err := readAllFromPathOrStdin(*envelopeFile)
	if err != nil {
		return fmt.Errorf("--envelope-file: %w", err)
	}
	if len(rawIn) == 0 {
		return errors.New("envelope is empty (refusing to sign nothing)")
	}
	var env txEnvelope
	if err := json.Unmarshal(rawIn, &env); err != nil {
		return fmt.Errorf("parse envelope JSON: %w", err)
	}
	if env.ID == "" || env.Sender == "" || env.Recipient == "" {
		return errors.New("envelope is missing one of: id, sender, recipient")
	}

	// Load + decrypt the keystore.
	path, err := defaultWalletPath(*in)
	if err != nil {
		return err
	}
	ks, err := loadKeystore(path)
	if err != nil {
		return err
	}
	passphrase, err := readPassphrase(*passphraseFile, false /*confirm*/)
	if err != nil {
		return err
	}
	defer zero(passphrase)
	priv, err := keystore.Decrypt(ks, passphrase)
	if err != nil {
		return err
	}
	defer zero(priv)

	// Sender ↔ keystore-public-key bind. The server enforces
	// this too (and 400s on mismatch) but we surface the error
	// here so the user gets a clear "wrong keystore" message
	// before paying for a round trip.
	pubBytes, err := hex.DecodeString(ks.PublicKey)
	if err != nil {
		return fmt.Errorf("keystore public_key not hex: %w", err)
	}
	sum := sha256.Sum256(pubBytes)
	derived := hex.EncodeToString(sum[:])
	if env.Sender != derived {
		return fmt.Errorf(
			"envelope.sender (%s) does not match this keystore's address (%s) — "+
				"either the envelope was built for a different wallet or the wrong keystore was opened",
			env.Sender, derived,
		)
	}

	// Resolve the nonce. Three paths:
	//   --nonce N       → stamp N verbatim
	//   --auto-nonce    → GET /api/v1/wallet/nonce?sender=... → stamp .next
	//   (neither)       → leave as input value (typically 0 = legacy)
	switch {
	case *nonceFlag != 0:
		env.Nonce = *nonceFlag
	case *autoNonce:
		next, err := fetchNonceNext(*apiURL, env.Sender, *timeout)
		if err != nil {
			return fmt.Errorf("--auto-nonce: %w", err)
		}
		env.Nonce = next
	}

	// Build canonical bytes by clearing signature + public_key,
	// then json.Marshal-ing. Same algorithm pkg/api/handlers.go
	// uses, byte-for-byte.
	env.Signature = ""
	env.PublicKey = ""
	canonical, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal canonical envelope: %w", err)
	}

	// Sign.
	var sk mldsa87.PrivateKey
	if err := sk.UnmarshalBinary(priv); err != nil {
		return fmt.Errorf("private key parse: %w", err)
	}
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(&sk, canonical, nil, true /*randomized*/, sig); err != nil {
		return fmt.Errorf("sign: %w", err)
	}

	// Re-attach signature + public_key, emit final envelope.
	env.Signature = hex.EncodeToString(sig)
	env.PublicKey = ks.PublicKey
	final, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal final envelope: %w", err)
	}
	fmt.Fprintf(os.Stderr, "signed envelope id=%s sender=%s nonce=%d (%d-byte ML-DSA-87 signature)\n",
		env.ID, env.Sender, env.Nonce, len(sig))
	if _, err := fmt.Println(string(final)); err != nil {
		return fmt.Errorf("write signed envelope to stdout: %w", err)
	}
	return nil
}

// fetchNonceNext hits GET <apiURL>/api/v1/wallet/nonce?sender=<addr>
// and returns the response's `next` field. Errors on any non-200
// status, malformed JSON, or sender-mismatch in the response body
// (the latter would indicate a misbehaving validator and should
// fail closed, not just stamp a guess).
func fetchNonceNext(apiURL, sender string, timeout time.Duration) (uint64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	url := apiURL + "/api/v1/wallet/nonce?sender=" + sender
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, fmt.Errorf("GET %s: HTTP %d: %s", url, resp.StatusCode, string(b))
	}

	var nr nonceResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&nr); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}
	if nr.Sender != sender {
		return 0, fmt.Errorf("validator echoed wrong sender: want %q got %q (refusing to trust the nonce)", sender, nr.Sender)
	}
	if nr.Next != nr.Nonce+1 {
		return 0, fmt.Errorf("validator returned inconsistent nonce/next: nonce=%d next=%d (want next = nonce+1)", nr.Nonce, nr.Next)
	}
	return nr.Next, nil
}
