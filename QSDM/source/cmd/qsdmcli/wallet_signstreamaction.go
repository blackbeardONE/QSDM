package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/keystore"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

type streamActionEnvelope struct {
	Action    chain.StreamAction `json:"action"`
	Signature string             `json:"signature"`
	PublicKey string             `json:"public_key"`
}

func (c *CLI) walletSignStreamAction(args []string) error {
	fs := flag.NewFlagSet("wallet sign-stream-action", flag.ContinueOnError)
	in := fs.String("in", "", "keystore path (default: ~/.qsdm/wallet.json)")
	passphraseFile := fs.String("passphrase-file", "", "read passphrase from file ('-' for stdin); empty = prompt")
	actionFile := fs.String("action-file", "-", "JSON stream action or {\"action\": ...} envelope ('-' for stdin)")
	nonceFlag := fs.Uint64("nonce", 0, "optional account nonce to stamp on the stream action")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rawIn, err := readAllFromPathOrStdin(*actionFile)
	if err != nil {
		return fmt.Errorf("--action-file: %w", err)
	}
	if len(rawIn) == 0 {
		return errors.New("CELL stream action is empty (refusing to sign nothing)")
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rawIn, &fields); err != nil {
		return fmt.Errorf("parse CELL stream action JSON: %w", err)
	}
	var action chain.StreamAction
	rawAction := bytes.TrimSpace(fields["action"])
	if len(rawAction) > 0 && rawAction[0] == '{' {
		if err := json.Unmarshal(rawAction, &action); err != nil {
			return fmt.Errorf("parse wrapped CELL stream action JSON: %w", err)
		}
	} else if err := json.Unmarshal(rawIn, &action); err != nil {
		return fmt.Errorf("parse CELL stream action JSON: %w", err)
	}
	if action.ID == "" || action.Sender == "" || action.StreamID == "" || action.Action == "" {
		return errors.New("CELL stream action is missing one of: id, sender, stream_id, action")
	}
	if action.Timestamp == "" {
		action.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	if *nonceFlag != 0 {
		action.Nonce = *nonceFlag
	}

	path, err := defaultWalletPath(*in)
	if err != nil {
		return err
	}
	ks, err := loadKeystore(path)
	if err != nil {
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

	publicBytes, err := hex.DecodeString(ks.PublicKey)
	if err != nil {
		return fmt.Errorf("keystore public_key not hex: %w", err)
	}
	sum := sha256.Sum256(publicBytes)
	derived := hex.EncodeToString(sum[:])
	if action.Sender != derived {
		return fmt.Errorf(
			"action.sender (%s) does not match this keystore's address (%s) - "+
				"the action was built for a different wallet or the wrong keystore was opened",
			action.Sender, derived,
		)
	}
	canonical, err := chain.StreamActionSigningBytes(action)
	if err != nil {
		return fmt.Errorf("marshal canonical CELL stream action: %w", err)
	}
	var privateKey mldsa87.PrivateKey
	if err := privateKey.UnmarshalBinary(privateBytes); err != nil {
		return fmt.Errorf("private key parse: %w", err)
	}
	signature := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(&privateKey, canonical, nil, true, signature); err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	envelope := streamActionEnvelope{
		Action:    action,
		Signature: hex.EncodeToString(signature),
		PublicKey: ks.PublicKey,
	}
	final, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal signed CELL stream envelope: %w", err)
	}
	fmt.Fprintf(os.Stderr, "signed CELL stream action id=%s stream_id=%s action=%s sender=%s nonce=%d\n",
		action.ID, action.StreamID, action.Action, action.Sender, action.Nonce)
	if _, err := fmt.Println(string(final)); err != nil {
		return fmt.Errorf("write signed CELL stream envelope: %w", err)
	}
	return nil
}
