package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/keystore"
	"github.com/blackbeardONE/QSDM/pkg/pulseclient"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// pulseActionRequest is what Hive asks qsdmcli to sign: a member's PULSE
// action (join, leave or reclaim) with the sender's next nonce and the fee.
type pulseActionRequest struct {
	Sender string             `json:"sender"`
	Nonce  uint64             `json:"nonce"`
	Fee    float64            `json:"fee"`
	Action pulseclient.Action `json:"action"`
}

// openPulseSigner decrypts the keystore and checks it is the expected wallet.
func openPulseSigner(in, passphraseFile, wantAddress string) (*mldsa87.PrivateKey, []byte, func(), error) {
	path, err := defaultWalletPath(in)
	if err != nil {
		return nil, nil, nil, err
	}
	ks, err := loadKeystore(path)
	if err != nil {
		return nil, nil, nil, err
	}
	pub, err := hex.DecodeString(ks.PublicKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("keystore public_key not hex: %w", err)
	}
	if wantAddress != "" && pulseclient.AddressOf(pub) != wantAddress {
		return nil, nil, nil, fmt.Errorf("the request is for %s but this keystore is %s: refusing to sign",
			wantAddress, pulseclient.AddressOf(pub))
	}
	passphrase, err := readPassphrase(passphraseFile, false /*confirm*/)
	if err != nil {
		return nil, nil, nil, err
	}
	defer zero(passphrase)
	priv, err := keystore.Decrypt(ks, passphrase)
	if err != nil {
		return nil, nil, nil, err
	}
	var sk mldsa87.PrivateKey
	if err := sk.UnmarshalBinary(priv); err != nil {
		zero(priv)
		return nil, nil, nil, fmt.Errorf("private key parse: %w", err)
	}
	return &sk, pub, func() { zero(priv) }, nil
}

// qsdmcli wallet sign-pulse-action: signs a member's PULSE join, leave or
// reclaim transaction for POST /api/v1/pulse/submit-signed.
func (c *CLI) walletSignPulseAction(args []string) error {
	fs := flag.NewFlagSet("wallet sign-pulse-action", flag.ContinueOnError)
	in := fs.String("in", "", "keystore path (default: ~/.qsdm/wallet.json)")
	passphraseFile := fs.String("passphrase-file", "", "read passphrase from file ('-' for stdin); empty = prompt")
	requestFile := fs.String("request-file", "-", "JSON {sender, nonce, fee, action} to sign ('-' for stdin)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	raw, err := readAllFromPathOrStdin(*requestFile)
	if err != nil {
		return fmt.Errorf("--request-file: %w", err)
	}
	if len(raw) == 0 {
		return errors.New("PULSE request is empty (refusing to sign nothing)")
	}
	var req pulseActionRequest
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return fmt.Errorf("parse PULSE request JSON: %w", err)
	}
	if !pulseclient.IsAddress(req.Sender) {
		return errors.New("PULSE request sender must be 64 lowercase hex")
	}
	if err := pulseclient.ValidateAction(req.Action); err != nil {
		return err
	}
	sk, pub, done, err := openPulseSigner(*in, *passphraseFile, req.Sender)
	if err != nil {
		return err
	}
	defer done()
	env, err := pulseclient.SignAction(sk, pub, req.Nonce, req.Fee, req.Action)
	if err != nil {
		return err
	}
	out, err := json.Marshal(env)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "signed PULSE %s sender=%s nonce=%d (ML-DSA-87)\n", req.Action.Op, env.Sender, env.Nonce)
	_, err = fmt.Println(string(out))
	return err
}

// qsdmcli wallet sign-pulse-register: signs a PULSE pool login, proving the
// wallet holds the login's payout address.
func (c *CLI) walletSignPulseRegister(args []string) error {
	fs := flag.NewFlagSet("wallet sign-pulse-register", flag.ContinueOnError)
	in := fs.String("in", "", "keystore path (default: ~/.qsdm/wallet.json)")
	passphraseFile := fs.String("passphrase-file", "", "read passphrase from file ('-' for stdin); empty = prompt")
	worker := fs.String("worker", "", "optional worker name (1-32 letters, digits, _ or -)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	sk, pub, done, err := openPulseSigner(*in, *passphraseFile, "")
	if err != nil {
		return err
	}
	defer done()
	login, err := pulseclient.Login(pulseclient.AddressOf(pub), *worker)
	if err != nil {
		return err
	}
	sig, err := pulseclient.SignRegister(sk, pub, login)
	if err != nil {
		return err
	}
	out, _ := json.Marshal(map[string]string{"login": login, "public_key": hex.EncodeToString(pub), "signature": sig})
	_, err = fmt.Println(string(out))
	return err
}
