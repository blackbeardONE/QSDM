package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/keystore"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// taskActionEnvelope mirrors pkg/api.QSDMTaskActionEnvelope.
// Field order is the signing contract: the API verifies by clearing
// signature + public_key and json.Marshal-ing this struct shape.
type taskActionEnvelope struct {
	ID        string  `json:"id"`
	Sender    string  `json:"sender"`
	TaskID    string  `json:"task_id"`
	Action    string  `json:"action"`
	Amount    float64 `json:"amount,omitempty"`
	Payload   string  `json:"payload,omitempty"`
	Nonce     uint64  `json:"nonce,omitempty"`
	Timestamp string  `json:"timestamp"`
	Signature string  `json:"signature"`
	PublicKey string  `json:"public_key,omitempty"`
}

func (c *CLI) walletSignTaskAction(args []string) error {
	fs := flag.NewFlagSet("wallet sign-task-action", flag.ContinueOnError)
	in := fs.String("in", "", "keystore path (default: ~/.qsdm/wallet.json)")
	passphraseFile := fs.String("passphrase-file", "", "read passphrase from file ('-' for stdin); empty = prompt")
	envelopeFile := fs.String("envelope-file", "-", "JSON task-action envelope to sign ('-' for stdin)")
	nonceFlag := fs.Uint64("nonce", 0, "optional task-action nonce to stamp on the envelope")
	if err := fs.Parse(args); err != nil {
		return err
	}

	rawIn, err := readAllFromPathOrStdin(*envelopeFile)
	if err != nil {
		return fmt.Errorf("--envelope-file: %w", err)
	}
	if len(rawIn) == 0 {
		return errors.New("task action envelope is empty (refusing to sign nothing)")
	}

	var env taskActionEnvelope
	if err := json.Unmarshal(rawIn, &env); err != nil {
		return fmt.Errorf("parse task action envelope JSON: %w", err)
	}
	if env.ID == "" || env.Sender == "" || env.TaskID == "" || env.Action == "" {
		return errors.New("task action envelope is missing one of: id, sender, task_id, action")
	}
	if env.Timestamp == "" {
		env.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}

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

	pubBytes, err := hex.DecodeString(ks.PublicKey)
	if err != nil {
		return fmt.Errorf("keystore public_key not hex: %w", err)
	}
	sum := sha256.Sum256(pubBytes)
	derived := hex.EncodeToString(sum[:])
	if env.Sender != derived {
		return fmt.Errorf(
			"envelope.sender (%s) does not match this keystore's address (%s) - "+
				"either the envelope was built for a different wallet or the wrong keystore was opened",
			env.Sender, derived,
		)
	}

	if *nonceFlag != 0 {
		env.Nonce = *nonceFlag
	}
	env.Signature = ""
	env.PublicKey = ""
	canonical, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal canonical task action envelope: %w", err)
	}

	var sk mldsa87.PrivateKey
	if err := sk.UnmarshalBinary(priv); err != nil {
		return fmt.Errorf("private key parse: %w", err)
	}
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(&sk, canonical, nil, true /*randomized*/, sig); err != nil {
		return fmt.Errorf("sign: %w", err)
	}

	env.Signature = hex.EncodeToString(sig)
	env.PublicKey = ks.PublicKey
	final, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal final task action envelope: %w", err)
	}
	fmt.Fprintf(os.Stderr, "signed task action id=%s task_id=%s action=%s sender=%s nonce=%d (%d-byte ML-DSA-87 signature)\n",
		env.ID, env.TaskID, env.Action, env.Sender, env.Nonce, len(sig))
	if _, err := fmt.Println(string(final)); err != nil {
		return fmt.Errorf("write signed task action envelope to stdout: %w", err)
	}
	return nil
}
