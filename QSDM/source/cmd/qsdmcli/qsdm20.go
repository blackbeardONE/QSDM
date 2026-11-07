package main

// QSDM-20 native tokens (QSDM/docs/docs/QSDM_20.md) from the command line.
//
//	qsdmcli wallet sign-token-tx [--in PATH] [--passphrase-file FILE]
//	                             [--tx-file PATH | '-'] [--nonce N | --auto-nonce]
//	                             [--fee-dust N] [--api-url URL] [--api-timeout DUR]
//	qsdmcli qsdm20 submit [--file PATH | '-']
//	qsdmcli qsdm20 tokens [--limit N] [--cursor C]
//	qsdmcli qsdm20 token <id>
//	qsdmcli qsdm20 holders <id> [--limit N] [--cursor C]
//	qsdmcli qsdm20 balances <address>
//	qsdmcli qsdm20 token-id <creator> <nonce>
//
// sign-token-tx signs offline with the keystore and prints the body of
// POST /api/v1/qsdm20/submit; `qsdm20 submit` posts it. Signing covers the
// node's own signing digest (chain.QSDM20SigningDigest), so what this prints
// is exactly what a node verifies.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/keystore"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// qsdm20SubmitBody mirrors pkg/api.QSDM20SubmitRequest, the body of
// POST /api/v1/qsdm20/submit.
type qsdm20SubmitBody struct {
	Sender    string             `json:"sender"`
	Nonce     uint64             `json:"nonce"`
	FeeDust   uint64             `json:"fee_dust"`
	Payload   chain.QSDM20Action `json:"payload"`
	PublicKey string             `json:"public_key"`
	Signature string             `json:"signature"`
}

// qsdm20UnsignedInput is what sign-token-tx reads: a bare action, or the
// action under "payload" with an optional sender, nonce and fee.
type qsdm20UnsignedInput struct {
	Sender    string          `json:"sender,omitempty"`
	Nonce     *uint64         `json:"nonce,omitempty"`
	FeeDust   *uint64         `json:"fee_dust,omitempty"`
	Payload   json.RawMessage `json:"payload"`
	PublicKey string          `json:"public_key,omitempty"`
	Signature string          `json:"signature,omitempty"`
}

func decodeStrict(raw []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("trailing data after the JSON object")
	}
	return nil
}

// parseQSDM20Input accepts {"action": ...} or {"payload": {...}, "nonce":
// ..., "fee_dust": ..., "sender": ...}.
func parseQSDM20Input(raw []byte) (qsdm20UnsignedInput, chain.QSDM20Action, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return qsdm20UnsignedInput{}, chain.QSDM20Action{}, fmt.Errorf("parse QSDM-20 transaction JSON: %w", err)
	}
	var in qsdm20UnsignedInput
	if _, wrapped := keys["payload"]; wrapped {
		if err := decodeStrict(raw, &in); err != nil {
			return qsdm20UnsignedInput{}, chain.QSDM20Action{}, fmt.Errorf("parse QSDM-20 transaction JSON: %w", err)
		}
	} else {
		in.Payload = raw
	}
	var action chain.QSDM20Action
	if err := decodeStrict(in.Payload, &action); err != nil {
		return qsdm20UnsignedInput{}, chain.QSDM20Action{}, fmt.Errorf("parse QSDM-20 action: %w", err)
	}
	if err := chain.ValidateQSDM20Action(action); err != nil {
		return qsdm20UnsignedInput{}, chain.QSDM20Action{}, err
	}
	return in, action, nil
}

func (c *CLI) walletSignTokenTx(args []string) error {
	fs := flag.NewFlagSet("wallet sign-token-tx", flag.ContinueOnError)
	in := fs.String("in", "", "keystore path (default: ~/.qsdm/wallet.json)")
	passphraseFile := fs.String("passphrase-file", "", "read passphrase from file ('-' for stdin); empty = prompt")
	txFile := fs.String("tx-file", "-", "QSDM-20 action JSON, or {\"payload\": action, \"nonce\": N, \"fee_dust\": N} ('-' for stdin)")
	nonceFlag := fs.String("nonce", "", "the sender's current account nonce (0 is a valid nonce)")
	autoNonce := fs.Bool("auto-nonce", false, "read the account nonce from --api-url (GET /api/v1/mining/account)")
	feeFlag := fs.String("fee-dust", "", "fee in dust (1 CELL = 100000000 dust), burned; default: the file's fee_dust, else 0")
	apiURL := fs.String("api-url", defaultWalletAPIURL(), "node base URL for --auto-nonce (no trailing slash)")
	timeout := fs.Duration("api-timeout", 10*time.Second, "HTTP timeout for --auto-nonce")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	raw, err := readAllFromPathOrStdin(*txFile)
	if err != nil {
		return fmt.Errorf("--tx-file: %w", err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return errors.New("QSDM-20 transaction is empty (refusing to sign nothing)")
	}
	input, action, err := parseQSDM20Input(raw)
	if err != nil {
		return err
	}

	path, err := defaultWalletPath(*in)
	if err != nil {
		return err
	}
	ks, err := loadKeystore(path)
	if err != nil {
		return err
	}
	publicBytes, err := hex.DecodeString(ks.PublicKey)
	if err != nil {
		return fmt.Errorf("keystore public_key not hex: %w", err)
	}
	sender := walletAddressOf(publicBytes)
	if input.Sender != "" && input.Sender != sender {
		return fmt.Errorf("sender (%s) does not match this keystore's address (%s) - the transaction was built for a different wallet or the wrong keystore was opened",
			input.Sender, sender)
	}

	sources := 0
	for _, set := range []bool{input.Nonce != nil, *nonceFlag != "", *autoNonce} {
		if set {
			sources++
		}
	}
	if sources != 1 {
		return errors.New("give the account nonce exactly one way: \"nonce\" in the file, --nonce N, or --auto-nonce")
	}
	var nonce uint64
	switch {
	case input.Nonce != nil:
		nonce = *input.Nonce
	case *nonceFlag != "":
		if nonce, err = strconv.ParseUint(*nonceFlag, 10, 64); err != nil {
			return fmt.Errorf("--nonce: %w", err)
		}
	default:
		if nonce, err = fetchAccountNonce(*apiURL, sender, *timeout); err != nil {
			return fmt.Errorf("--auto-nonce: %w", err)
		}
	}
	var feeDust uint64
	if input.FeeDust != nil {
		feeDust = *input.FeeDust
	}
	if *feeFlag != "" {
		if feeDust, err = strconv.ParseUint(*feeFlag, 10, 64); err != nil {
			return fmt.Errorf("--fee-dust: %w", err)
		}
	}
	if _, err := chain.QSDM20FeeCell(feeDust); err != nil {
		return err
	}

	passphrase, err := readPassphrase(*passphraseFile, false)
	if err != nil {
		return err
	}
	defer zero(passphrase)
	privateBytes, err := keystore.Decrypt(ks, passphrase)
	if err != nil {
		return err
	}
	defer zero(privateBytes)
	var privateKey mldsa87.PrivateKey
	if err := privateKey.UnmarshalBinary(privateBytes); err != nil {
		return fmt.Errorf("private key parse: %w", err)
	}
	digest := chain.QSDM20SigningDigest(sender, nonce, feeDust, action)
	signature := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(&privateKey, digest, nil, true, signature); err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	body := qsdm20SubmitBody{
		Sender:    sender,
		Nonce:     nonce,
		FeeDust:   feeDust,
		Payload:   action,
		PublicKey: ks.PublicKey,
		Signature: hex.EncodeToString(signature),
	}
	out, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal signed QSDM-20 transaction: %w", err)
	}
	token := action.Token
	if action.Action == chain.QSDM20ActionCreate {
		token = chain.QSDM20TokenID(sender, nonce)
	}
	fmt.Fprintf(os.Stderr, "signed QSDM-20 %s tx_id=%s token=%s sender=%s nonce=%d fee_dust=%d\n",
		action.Action, chain.QSDM20TxID(digest), token, sender, nonce, feeDust)
	if _, err := fmt.Println(string(out)); err != nil {
		return fmt.Errorf("write signed QSDM-20 transaction: %w", err)
	}
	return nil
}

func walletAddressOf(publicKey []byte) string {
	sum := sha256.Sum256(publicKey)
	return hex.EncodeToString(sum[:])
}

func isWalletAddress(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !(s[i] >= '0' && s[i] <= '9' || s[i] >= 'a' && s[i] <= 'f') {
			return false
		}
	}
	return true
}

// accountResponse mirrors pkg/api.MiningAccountResponse.
type accountResponse struct {
	Address string  `json:"address"`
	Balance float64 `json:"balance"`
	Nonce   uint64  `json:"nonce"`
	Present bool    `json:"present"`
}

// fetchAccountNonce reads the account nonce a token transaction carries
// (GET <apiURL>/api/v1/mining/account?address=). It fails closed on an
// answer for another address, and on an address with no CELL account,
// which a token transaction's sender needs.
func fetchAccountNonce(apiURL, address string, timeout time.Duration) (uint64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	target := strings.TrimRight(apiURL, "/") + "/api/v1/mining/account?address=" + url.QueryEscape(address)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("GET %s: %w", target, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, fmt.Errorf("GET %s: HTTP %d: %s", target, resp.StatusCode, string(b))
	}
	var account accountResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&account); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}
	if account.Address != address {
		return 0, fmt.Errorf("node answered for %q, not %q (refusing to trust the nonce)", account.Address, address)
	}
	if !account.Present {
		return 0, fmt.Errorf("%s has no CELL account yet; a token transaction's sender needs one", address)
	}
	return account.Nonce, nil
}

func (c *CLI) qsdm20Command(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: qsdmcli qsdm20 <submit|tokens|token|holders|balances|token-id> [args]")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "submit":
		fs := flag.NewFlagSet("qsdm20 submit", flag.ContinueOnError)
		file := fs.String("file", "-", "signed transaction from wallet sign-token-tx ('-' for stdin)")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		raw, err := readAllFromPathOrStdin(*file)
		if err != nil {
			return fmt.Errorf("--file: %w", err)
		}
		var body qsdm20SubmitBody
		if err := decodeStrict(bytes.TrimSpace(raw), &body); err != nil {
			return fmt.Errorf("parse signed QSDM-20 transaction: %w", err)
		}
		if body.Signature == "" || body.PublicKey == "" {
			return errors.New("the QSDM-20 transaction is not signed (run qsdmcli wallet sign-token-tx first)")
		}
		data, err := c.post("/qsdm20/submit", body)
		if err != nil {
			return err
		}
		prettyPrint(data)
		return nil
	case "tokens", "holders":
		fs := flag.NewFlagSet("qsdm20 "+sub, flag.ContinueOnError)
		limit := fs.Int("limit", 0, "page size (0 = every remaining item)")
		cursor := fs.String("cursor", "", "next_cursor of the previous page")
		var id string
		if sub == "holders" {
			if len(rest) < 1 || strings.HasPrefix(rest[0], "-") {
				return errors.New("usage: qsdmcli qsdm20 holders <token id> [--limit N] [--cursor C]")
			}
			id, rest = rest[0], rest[1:]
		}
		if err := fs.Parse(rest); err != nil {
			return err
		}
		q := url.Values{}
		if *cursor != "" {
			q.Set("cursor", *cursor)
		}
		if *limit > 0 {
			q.Set("limit", strconv.Itoa(*limit))
		}
		path := "/qsdm20/tokens"
		if sub == "holders" {
			path += "/" + url.PathEscape(id) + "/holders"
		}
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
		data, err := c.get(path)
		if err != nil {
			return err
		}
		prettyPrint(data)
		return nil
	case "token", "balances":
		if len(rest) != 1 {
			return fmt.Errorf("usage: qsdmcli qsdm20 %s <%s>", sub, map[string]string{"token": "token id", "balances": "address"}[sub])
		}
		path := "/qsdm20/tokens/" + url.PathEscape(rest[0])
		if sub == "balances" {
			path = "/qsdm20/balances/" + url.PathEscape(rest[0])
		}
		data, err := c.get(path)
		if err != nil {
			return err
		}
		prettyPrint(data)
		return nil
	case "token-id":
		if len(rest) != 2 {
			return errors.New("usage: qsdmcli qsdm20 token-id <creator address> <nonce>")
		}
		if !isWalletAddress(rest[0]) {
			return errors.New("creator must be a QSDM address (64 lowercase hex characters)")
		}
		nonce, err := strconv.ParseUint(rest[1], 10, 64)
		if err != nil {
			return fmt.Errorf("nonce: %w", err)
		}
		fmt.Println(chain.QSDM20TokenID(rest[0], nonce))
		return nil
	default:
		return fmt.Errorf("unknown qsdm20 subcommand %q (submit, tokens, token, holders, balances, token-id)", sub)
	}
}
