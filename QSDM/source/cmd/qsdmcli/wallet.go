// qsdmcli wallet — self-custody keystore operations.
//
// This subcommand creates and inspects QSDM wallet keystores
// (pkg/keystore format v1). Unlike `POST /api/v1/wallet/create` — which
// generates a server-side keypair and discards the private key, leaving
// you with a permanently-unrecoverable address — the keystore generated
// here is fully self-custody: the ML-DSA-87 keypair is created locally,
// the private key never leaves this process, and the only artefact that
// touches disk is an encrypted JSON file you protect with a passphrase.
//
// The same keystore format is produced by the browser wallet at
// qsdm.tech/wallet/ (deploy/landing/wallet.{html,js,wasm}); a keystore
// generated in the browser opens here byte-for-byte and vice versa.
//
// Subcommands:
//
//	qsdmcli wallet new      [--out PATH] [--passphrase-file FILE] [--force]
//	qsdmcli wallet show     [--in PATH]
//	qsdmcli wallet inspect  [--in PATH] [--passphrase-file FILE]
//	qsdmcli wallet sign     [--in PATH] [--passphrase-file FILE] [--message HEX | --message-file PATH]
//	qsdmcli wallet verify   --public-key HEX [--message HEX | --message-file PATH]
//	                        [--signature HEX | --signature-file PATH]
//	qsdmcli wallet sign-tx  [--in PATH] [--passphrase-file FILE]
//	                        [--envelope-file PATH | '-'] [--nonce N | --auto-nonce]
//	                        [--api-url URL] [--api-timeout DUR]
//	qsdmcli wallet sign-task-action [--in PATH] [--passphrase-file FILE]
//	                                [--envelope-file PATH | '-'] [--nonce N]
//	qsdmcli wallet sign-stream-action [--in PATH] [--passphrase-file FILE]
//	                                  [--action-file PATH | '-'] [--nonce N]
//
// `new` produces an encrypted keystore and prints only the address to
// stdout — friendly for piping straight into a miner:
//
//	./qsdmminer --validator=https://api.qsdm.tech \
//	            --address="$(qsdmcli wallet new --passphrase-file passphrase.txt)" \
//	            --batch-count=1
//
// `show` is a pure metadata read — it does NOT prompt for a passphrase
// because the address and public key live in plaintext in the keystore
// (a useful "which keystore did I open?" affordance).
//
// `inspect` and `sign` both prompt for the passphrase and decrypt; the
// difference is that `inspect` prints the decrypted public key in hex
// (and verifies it matches the stored one — a round-trip integrity check)
// while `sign` produces a FIPS 204 ML-DSA-87 signature over the supplied
// message.

package main

import (
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/keystore"
	"github.com/blackbeardONE/QSDM/pkg/walletrecovery"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
	"golang.org/x/term"
)

// walletCommand dispatches `qsdmcli wallet …`. Mirrors the multiplexer
// shape of `watch` and `slash-helper` so adding a new wallet sub-action
// later is a one-case-arm change.
func (c *CLI) walletCommand(args []string) error {
	if len(args) < 1 {
		return walletUsageError()
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "new":
		return c.walletNew(rest)
	case "restore":
		return c.walletRestore(rest)
	case "export-recovery":
		return c.walletExportRecovery(rest)
	case "enable-recovery":
		return c.walletEnableLegacyRecovery(rest)
	case "restore-legacy":
		return c.walletRestoreLegacy(rest)
	case "show":
		return c.walletShow(rest)
	case "inspect":
		return c.walletInspect(rest)
	case "sign":
		return c.walletSign(rest)
	case "verify":
		return c.walletVerify(rest)
	case "sign-tx":
		return c.walletSignTx(rest)
	case "sign-task-action":
		return c.walletSignTaskAction(rest)
	case "sign-stream-action":
		return c.walletSignStreamAction(rest)
	case "help", "-h", "--help":
		fmt.Fprint(os.Stdout, walletHelp)
		return nil
	default:
		return fmt.Errorf("unknown wallet subcommand %q\n\n%s", sub, walletHelp)
	}
}

func walletUsageError() error {
	return fmt.Errorf("usage: qsdmcli wallet <new|restore|enable-recovery|restore-legacy|export-recovery|show|inspect|sign|verify|sign-tx|sign-task-action|sign-stream-action> [flags]\n\n%s", walletHelp)
}

const walletHelp = `qsdmcli wallet — self-custody keystore (ML-DSA-87)

Subcommands:
  new      Generate a fresh keypair, write an encrypted keystore, print the
           address. Add --recovery-out to create 24 QSDM Recovery Words.
  restore  Restore a recovery-enabled wallet from 24 QSDM Recovery Words and
           protect the new local keystore with a new passphrase.
  export-recovery
           Export the 24 recovery words from a recovery-enabled keystore to a
           private file.
  enable-recovery
           Add 24 recovery words to an older JSON + passphrase wallet without
           changing its address. Registers only encrypted key material with
           QSDM Core, waits for chain confirmation, and keeps a JSON backup.
  restore-legacy
           Restore an older wallet after enable-recovery using its 24 words
           and the encrypted capsule replicated by QSDM Core.
  show     Print address and public key from an existing keystore. No
           passphrase required (these fields are plaintext in the file).
  inspect  Decrypt the keystore and verify the on-disk public key matches
           the encrypted private key. Prompts for passphrase.
  sign     Decrypt the keystore and sign a message with the wallet's
           private key. Prompts for passphrase. Outputs hex signature.
  verify   Verify an ML-DSA-87 signature with a public key. This command
           never opens a keystore and is suitable for release verification.
  sign-tx  v0.4.1: produce a fully-signed self-custody envelope ready
           for POST /api/v1/wallet/submit-signed. Reads an unsigned
           envelope (JSON on stdin by default), stamps the v0.4.1
           nonce (literal via --nonce, fetched via --auto-nonce, or
           left as the v0.4.0 backward-compat 0), signs the canonical
           bytes with the keystore key, and writes the signed
           envelope to stdout.
  sign-task-action
           Produce a fully-signed QSDM task action envelope ready for
           POST /api/v1/tasks/actions/submit-signed. Reads an unsigned
           envelope (JSON on stdin by default), optionally stamps
           --nonce, signs the canonical bytes with the keystore key,
           and writes the signed envelope to stdout.
  sign-stream-action
           Produce a fully-signed CELL stream action ready for
           POST /api/v1/streams/actions/submit-signed. Reads an action
           or {"action": ...} JSON object, optionally stamps --nonce,
           and writes the signed envelope to stdout.

Common flags:
  --in   PATH           Keystore file to read (default: ~/.qsdm/wallet.json)
  --out  PATH           Keystore file to write (new only; default: ~/.qsdm/wallet.json)
  --passphrase-file FILE
                        Read passphrase from FILE (use '-' for stdin).
                        Omit to prompt interactively without echo.
  --force               Overwrite an existing keystore (new only). Off by default.
  --recovery-out PATH   Write new/exported recovery words to PATH with mode 0600.
  --recovery-file PATH  Read 24 recovery words from PATH (restore only).

Legacy recovery flags:
  --api-url URL         QSDM API root ending in /api/v1. Defaults to
                        QSDM_API_URL or the CLI's configured API.
  --expected-address ADDRESS
                        Refuse enable-recovery unless the opened keystore has
                        this QSDM address. Checked before any output or submit.
  --confirm-timeout DUR Wait for encrypted capsule chain confirmation
                        (default: 90s).
  --message      HEX    Hex-encoded message bytes to sign (sign only).
  --message-file PATH   Read message bytes to sign or verify from a file;
                        use '-' for stdin). Mutually exclusive with --message.
  --public-key HEX      ML-DSA-87 public key to use for verification.
  --public-key-file PATH
                        Read the public-key hex from a file (verify only).
  --signature HEX       ML-DSA-87 signature to verify.
  --signature-file PATH Read signature hex from a file (verify only).
  --envelope-file PATH  JSON envelope to sign (sign-tx or sign-task-action;
                        default: stdin).
  --action-file PATH    JSON CELL stream action to sign (sign-stream-action;
                        default: stdin).
  --nonce N             Nonce to stamp (sign-tx, sign-task-action, or
                        sign-stream-action; mutually exclusive with
                        --auto-nonce for sign-tx).
  --auto-nonce          Resolve nonce from --api-url before signing (sign-tx only).
  --api-url URL         Validator base URL for --auto-nonce (default: https://api.qsdm.tech).
  --api-timeout DUR     HTTP timeout for --auto-nonce (default: 10s).

Examples:
  qsdmcli wallet new
  qsdmcli wallet new --out ~/.qsdm/miner.json --passphrase-file pass.txt \
      --recovery-out /media/offline/qsdm-recovery.txt
  qsdmcli wallet restore --recovery-file /media/offline/qsdm-recovery.txt \
      --out ~/.qsdm/wallet.json
  qsdmcli wallet export-recovery --out /media/offline/qsdm-recovery.txt
  qsdmcli wallet enable-recovery --in ~/.qsdm/wallet.json \
      --recovery-out /media/offline/qsdm-recovery.txt
  qsdmcli wallet restore-legacy \
      --recovery-file /media/offline/qsdm-recovery.txt \
      --out ~/.qsdm/wallet.json
  qsdmcli wallet show
  qsdmcli wallet sign --message-file tx.json > tx.sig.hex
  qsdmcli wallet verify --public-key "$PUBLIC_KEY" \
      --message-file release-manifest.json \
      --signature-file release-manifest.sig
  # Build envelope.json (no signature/public_key/nonce fields), then:
  qsdmcli wallet sign-tx --auto-nonce < envelope.json \
    | curl -fsS -H 'Content-Type: application/json' --data-binary @- \
           https://api.qsdm.tech/api/v1/wallet/submit-signed
  qsdmcli wallet sign-task-action < task-action.json \
    | curl -fsS -H 'Content-Type: application/json' --data-binary @- \
           https://api.qsdm.tech/api/v1/tasks/actions/submit-signed
  qsdmcli wallet sign-stream-action < stream-action.json \
    | curl -fsS -H 'Content-Type: application/json' --data-binary @- \
           https://api.qsdm.tech/api/v1/streams/actions/submit-signed
`

func (c *CLI) walletNew(args []string) error {
	fs := flag.NewFlagSet("wallet new", flag.ContinueOnError)
	out := fs.String("out", "", "keystore output path (default: ~/.qsdm/wallet.json)")
	passphraseFile := fs.String("passphrase-file", "", "read passphrase from file ('-' for stdin); empty = prompt")
	recoveryOut := fs.String("recovery-out", "", "write 24 QSDM Recovery Words to this private file")
	force := fs.Bool("force", false, "overwrite existing keystore at --out")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := defaultWalletPath(*out)
	if err != nil {
		return err
	}
	var recoveryPath string
	if strings.TrimSpace(*recoveryOut) != "" {
		recoveryPath = filepath.Clean(*recoveryOut)
		if samePath(recoveryPath, path) {
			return fmt.Errorf("--recovery-out must be different from the keystore path")
		}
	}
	if !*force {
		if _, statErr := os.Stat(path); statErr == nil {
			return fmt.Errorf("refusing to overwrite existing keystore at %s (pass --force to override)", path)
		}
		if recoveryPath != "" {
			if _, statErr := os.Stat(recoveryPath); statErr == nil {
				return fmt.Errorf("refusing to overwrite existing recovery file at %s (pass --force to override)", recoveryPath)
			}
		}
	}
	passphrase, err := readPassphrase(*passphraseFile, true /*confirm*/)
	if err != nil {
		return fmt.Errorf("read passphrase: %w", err)
	}
	defer zero(passphrase)

	var (
		ks            keystore.Keystore
		recoveryWords string
	)
	if strings.TrimSpace(*recoveryOut) != "" {
		material, generationErr := walletrecovery.Generate()
		if generationErr != nil {
			return generationErr
		}
		defer material.ZeroSecrets()
		ks, err = keystore.EncryptWithRecovery(
			material.PublicKey,
			material.PrivateKey,
			material.Entropy,
			passphrase,
		)
		recoveryWords = material.Words
	} else {
		pk, sk, generationErr := mldsa87.GenerateKey(nil)
		if generationErr != nil {
			return fmt.Errorf("mldsa87.GenerateKey: %w", generationErr)
		}
		pubBytes, marshalErr := pk.MarshalBinary()
		if marshalErr != nil {
			return fmt.Errorf("public key marshal: %w", marshalErr)
		}
		privBytes, marshalErr := sk.MarshalBinary()
		if marshalErr != nil {
			return fmt.Errorf("private key marshal: %w", marshalErr)
		}
		defer zero(privBytes)
		ks, err = keystore.Encrypt(pubBytes, privBytes, passphrase)
	}
	if err != nil {
		return fmt.Errorf("keystore encrypt: %w", err)
	}
	data, err := keystore.Marshal(ks)
	if err != nil {
		return fmt.Errorf("keystore marshal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := writeFileExclusive(path, data, *force); err != nil {
		return fmt.Errorf("write keystore: %w", err)
	}
	if recoveryWords != "" {
		if err := os.MkdirAll(filepath.Dir(recoveryPath), 0o700); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(recoveryPath), err)
		}
		if err := writeFileExclusive(recoveryPath, []byte(recoveryWords+"\n"), *force); err != nil {
			return fmt.Errorf("write recovery words: %w", err)
		}
		fmt.Fprintf(os.Stderr, "wrote 24 QSDM Recovery Words to %s (mode 0600)\n", recoveryPath)
	}
	// Stdout: only the address, so the line can be piped straight into a
	// miner / mining-enroll command. Everything else goes to stderr.
	fmt.Fprintf(os.Stderr, "wrote keystore to %s (mode 0600)\n", path)
	if recoveryWords != "" {
		fmt.Fprintln(os.Stderr, "keep the recovery words offline and separate from this device; they can rebuild the wallet without this JSON file")
	} else {
		fmt.Fprintln(os.Stderr, "legacy wallet: store the keystore + remember the passphrase. Losing either is unrecoverable")
	}
	fmt.Println(ks.Address)
	return nil
}

func (c *CLI) walletRestore(args []string) error {
	fs := flag.NewFlagSet("wallet restore", flag.ContinueOnError)
	out := fs.String("out", "", "keystore output path (default: ~/.qsdm/wallet.json)")
	recoveryFile := fs.String("recovery-file", "", "private file containing 24 QSDM Recovery Words")
	passphraseFile := fs.String("passphrase-file", "", "read new keystore passphrase from file ('-' for stdin); empty = prompt")
	force := fs.Bool("force", false, "overwrite existing keystore at --out")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*recoveryFile) == "" || *recoveryFile == "-" {
		return fmt.Errorf("--recovery-file is required and must be a private file (stdin is refused to reduce accidental disclosure)")
	}
	wordsBytes, err := os.ReadFile(filepath.Clean(*recoveryFile))
	if err != nil {
		return fmt.Errorf("read recovery words: %w", err)
	}
	if len(wordsBytes) > 4096 {
		zero(wordsBytes)
		return fmt.Errorf("recovery words file is unexpectedly large")
	}
	defer zero(wordsBytes)

	material, err := walletrecovery.Restore(string(wordsBytes))
	if err != nil {
		return err
	}
	defer material.ZeroSecrets()

	path, err := defaultWalletPath(*out)
	if err != nil {
		return err
	}
	if !*force {
		if _, statErr := os.Stat(path); statErr == nil {
			return fmt.Errorf("refusing to overwrite existing keystore at %s (pass --force to override)", path)
		}
	}
	passphrase, err := readPassphrase(*passphraseFile, true)
	if err != nil {
		return fmt.Errorf("read passphrase: %w", err)
	}
	defer zero(passphrase)

	ks, err := keystore.EncryptWithRecovery(
		material.PublicKey,
		material.PrivateKey,
		material.Entropy,
		passphrase,
	)
	if err != nil {
		return fmt.Errorf("keystore encrypt: %w", err)
	}
	data, err := keystore.Marshal(ks)
	if err != nil {
		return fmt.Errorf("keystore marshal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := writeFileExclusive(path, data, *force); err != nil {
		return fmt.Errorf("write keystore: %w", err)
	}
	fmt.Fprintf(os.Stderr, "restored recovery-enabled keystore to %s (mode 0600)\n", path)
	fmt.Println(ks.Address)
	return nil
}

func (c *CLI) walletExportRecovery(args []string) error {
	fs := flag.NewFlagSet("wallet export-recovery", flag.ContinueOnError)
	in := fs.String("in", "", "keystore path (default: ~/.qsdm/wallet.json)")
	out := fs.String("out", "", "private output file for 24 QSDM Recovery Words")
	passphraseFile := fs.String("passphrase-file", "", "read passphrase from file ('-' for stdin); empty = prompt")
	force := fs.Bool("force", false, "overwrite an existing recovery output file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*out) == "" || *out == "-" {
		return fmt.Errorf("--out is required and must be a private file (stdout is refused to reduce accidental disclosure)")
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
		return fmt.Errorf("read passphrase: %w", err)
	}
	defer zero(passphrase)
	if _, err := verifyKeystorePrivateKey(ks, passphrase); err != nil {
		return err
	}
	entropy, err := keystore.DecryptRecovery(ks, passphrase)
	if err != nil {
		return err
	}
	defer zero(entropy)
	words, err := walletrecovery.WordsFromEntropy(entropy)
	if err != nil {
		return err
	}
	if ks.Recovery.Scheme == keystore.LegacyRecoveryScheme {
		locator, locatorErr := walletrecovery.LegacyLocatorFromWords(words)
		if locatorErr != nil {
			return locatorErr
		}
		if locator != ks.Recovery.Locator {
			return fmt.Errorf("legacy recovery integrity check failed: words do not match the keystore locator")
		}
	} else {
		material, restoreErr := walletrecovery.Restore(words)
		if restoreErr != nil {
			return restoreErr
		}
		defer material.ZeroSecrets()
		if material.Address != ks.Address {
			return fmt.Errorf("recovery integrity check failed: words rebuild %s, keystore address is %s", material.Address, ks.Address)
		}
	}

	recoveryPath := filepath.Clean(*out)
	if samePath(recoveryPath, path) {
		return fmt.Errorf("--out must be different from the keystore path")
	}
	if err := os.MkdirAll(filepath.Dir(recoveryPath), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(recoveryPath), err)
	}
	if err := writeFileExclusive(recoveryPath, []byte(words+"\n"), *force); err != nil {
		return fmt.Errorf("write recovery words: %w", err)
	}
	fmt.Fprintf(os.Stderr, "exported 24 QSDM Recovery Words for %s to %s (mode 0600)\n", ks.Address, recoveryPath)
	return nil
}

type legacyRecoveryNonceResponse struct {
	ActionNonce uint64 `json:"action_nonce"`
	Present     bool   `json:"present"`
}

type legacyRecoveryCapsuleResponse struct {
	State chain.RecoveryCapsuleState `json:"recovery"`
}

type legacyRecoveryEnvelope struct {
	Action    chain.RecoveryCapsuleAction `json:"action"`
	Signature string                      `json:"signature"`
	PublicKey string                      `json:"public_key"`
}

func (c *CLI) walletEnableLegacyRecovery(args []string) error {
	fs := flag.NewFlagSet("wallet enable-recovery", flag.ContinueOnError)
	in := fs.String("in", "", "legacy keystore path (default: ~/.qsdm/wallet.json)")
	passphraseFile := fs.String("passphrase-file", "", "read existing passphrase from file ('-' for stdin); empty = prompt")
	recoveryOut := fs.String("recovery-out", "", "private output file for the new 24 recovery words")
	apiURL := fs.String("api-url", "", "QSDM API root ending in /api/v1")
	expectedAddress := fs.String("expected-address", "", "require the legacy keystore to have this QSDM address")
	confirmTimeout := fs.Duration("confirm-timeout", 90*time.Second, "wait for chain confirmation")
	force := fs.Bool("force", false, "overwrite an existing recovery output file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*recoveryOut) == "" || *recoveryOut == "-" {
		return fmt.Errorf("--recovery-out is required and must be a private file")
	}
	if *confirmTimeout <= 0 || *confirmTimeout > 10*time.Minute {
		return fmt.Errorf("--confirm-timeout must be between 1ns and 10m")
	}
	path, err := defaultWalletPath(*in)
	if err != nil {
		return err
	}
	recoveryPath := filepath.Clean(*recoveryOut)
	if samePath(path, recoveryPath) {
		return fmt.Errorf("--recovery-out must be different from the keystore path")
	}
	if !*force {
		if _, statErr := os.Stat(recoveryPath); statErr == nil {
			return fmt.Errorf("refusing to overwrite existing recovery file at %s (pass --force to override)", recoveryPath)
		}
	}
	ks, err := loadKeystore(path)
	if err != nil {
		return err
	}
	if expected := strings.TrimSpace(*expectedAddress); expected != "" {
		raw, decodeErr := hex.DecodeString(expected)
		if decodeErr != nil || len(raw) != 32 || expected != strings.ToLower(expected) {
			return fmt.Errorf("--expected-address must be a lowercase 64-character QSDM address")
		}
		if ks.Address != expected {
			return fmt.Errorf("legacy recovery address mismatch: opened keystore %s, expected %s", ks.Address, expected)
		}
	}
	if ks.Recovery != nil {
		return fmt.Errorf("wallet %s already has %d-word recovery enabled; use export-recovery instead", ks.Address, ks.Recovery.Words)
	}
	passphrase, err := readPassphrase(*passphraseFile, false)
	if err != nil {
		return fmt.Errorf("read passphrase: %w", err)
	}
	defer zero(passphrase)
	privateKey, err := keystore.Decrypt(ks, passphrase)
	if err != nil {
		return err
	}
	defer zero(privateKey)
	publicKey, err := verifiedPublicKey(ks, privateKey)
	if err != nil {
		return err
	}

	material, err := walletrecovery.GenerateLegacyCapsule(ks.Address, publicKey, privateKey)
	if err != nil {
		return err
	}
	defer material.ZeroSecrets()
	if err := os.MkdirAll(filepath.Dir(recoveryPath), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(recoveryPath), err)
	}
	if err := writeFileExclusive(recoveryPath, []byte(material.Words+"\n"), *force); err != nil {
		return fmt.Errorf("write recovery words: %w", err)
	}

	recoveryCLI := c.recoveryAPI(*apiURL)
	nonceBody, err := recoveryCLI.get("/wallet/recovery/nonce?sender=" + url.QueryEscape(ks.Address))
	if err != nil {
		return fmt.Errorf("recovery words were saved but activation did not start: %w", err)
	}
	var nonce legacyRecoveryNonceResponse
	if err := json.Unmarshal(nonceBody, &nonce); err != nil {
		return fmt.Errorf("decode recovery nonce: %w", err)
	}
	if !nonce.Present {
		return fmt.Errorf("wallet %s is not present in QSDM account state; receive or mine CELL before enabling network recovery", ks.Address)
	}
	actionID, err := randomRecoveryActionID()
	if err != nil {
		return err
	}
	action := chain.RecoveryCapsuleAction{
		ID:        actionID,
		Sender:    ks.Address,
		Action:    chain.RecoveryCapsuleActionRegister,
		Locator:   material.Capsule.Locator,
		Capsule:   material.Capsule,
		Nonce:     nonce.ActionNonce,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}
	message, err := chain.RecoveryCapsuleActionSigningBytes(action)
	if err != nil {
		return err
	}
	var signingKey mldsa87.PrivateKey
	if err := signingKey.UnmarshalBinary(privateKey); err != nil {
		return fmt.Errorf("private key parse: %w", err)
	}
	signature := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(&signingKey, message, nil, true, signature); err != nil {
		return fmt.Errorf("sign recovery registration: %w", err)
	}
	if _, err := recoveryCLI.post("/wallet/recovery/capsules/submit-signed", legacyRecoveryEnvelope{
		Action: action, Signature: hex.EncodeToString(signature), PublicKey: ks.PublicKey,
	}); err != nil {
		return fmt.Errorf("recovery words were saved but capsule registration was rejected; the keystore was not changed: %w", err)
	}
	if _, err := waitForLegacyRecoveryCapsule(recoveryCLI, material.Capsule.Locator, ks.Address, *confirmTimeout); err != nil {
		return fmt.Errorf("capsule was submitted but not confirmed; the keystore was not changed and the words remain at %s: %w", recoveryPath, err)
	}

	migrated, err := keystore.AttachLegacyRecovery(ks, material.Entropy, passphrase, material.Capsule.Locator)
	if err != nil {
		return err
	}
	data, err := keystore.Marshal(migrated)
	if err != nil {
		return err
	}
	backupPath, err := replaceKeystoreWithBackup(path, data)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "enabled 24-word recovery for %s without changing its address\n", ks.Address)
	fmt.Fprintf(os.Stderr, "recovery words: %s\n", recoveryPath)
	fmt.Fprintf(os.Stderr, "original keystore backup: %s\n", backupPath)
	fmt.Println(ks.Address)
	return nil
}

func (c *CLI) walletRestoreLegacy(args []string) error {
	fs := flag.NewFlagSet("wallet restore-legacy", flag.ContinueOnError)
	out := fs.String("out", "", "keystore output path (default: ~/.qsdm/wallet.json)")
	recoveryFile := fs.String("recovery-file", "", "private file containing 24 recovery words")
	passphraseFile := fs.String("passphrase-file", "", "read new passphrase from file ('-' for stdin); empty = prompt")
	apiURL := fs.String("api-url", "", "QSDM API root ending in /api/v1")
	force := fs.Bool("force", false, "overwrite an existing keystore")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*recoveryFile) == "" || *recoveryFile == "-" {
		return fmt.Errorf("--recovery-file is required and must be a private file")
	}
	wordsBytes, err := os.ReadFile(filepath.Clean(*recoveryFile))
	if err != nil {
		return fmt.Errorf("read recovery words: %w", err)
	}
	if len(wordsBytes) > 4096 {
		zero(wordsBytes)
		return fmt.Errorf("recovery words file is unexpectedly large")
	}
	defer zero(wordsBytes)
	words := string(wordsBytes)
	locator, err := walletrecovery.LegacyLocatorFromWords(words)
	if err != nil {
		return err
	}
	recoveryCLI := c.recoveryAPI(*apiURL)
	response, err := fetchLegacyRecoveryCapsule(recoveryCLI, locator)
	if err != nil {
		return fmt.Errorf("retrieve encrypted legacy wallet recovery capsule: %w", err)
	}
	recovered, err := walletrecovery.RestoreLegacyCapsule(words, response.State.Capsule)
	if err != nil {
		return err
	}
	defer recovered.ZeroSecrets()
	entropy, err := walletrecovery.LegacyRecoveryEntropyFromWords(words)
	if err != nil {
		return err
	}
	defer zero(entropy)
	path, err := defaultWalletPath(*out)
	if err != nil {
		return err
	}
	if !*force {
		if _, statErr := os.Stat(path); statErr == nil {
			return fmt.Errorf("refusing to overwrite existing keystore at %s (pass --force to override)", path)
		}
	}
	passphrase, err := readPassphrase(*passphraseFile, true)
	if err != nil {
		return fmt.Errorf("read passphrase: %w", err)
	}
	defer zero(passphrase)
	ks, err := keystore.Encrypt(recovered.PublicKey, recovered.PrivateKey, passphrase)
	if err != nil {
		return err
	}
	ks, err = keystore.AttachLegacyRecovery(ks, entropy, passphrase, locator)
	if err != nil {
		return err
	}
	data, err := keystore.Marshal(ks)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := writeFileExclusive(path, data, *force); err != nil {
		return fmt.Errorf("write restored keystore: %w", err)
	}
	fmt.Fprintf(os.Stderr, "restored legacy wallet %s with a new local passphrase\n", ks.Address)
	fmt.Println(ks.Address)
	return nil
}

func verifiedPublicKey(ks keystore.Keystore, privateKey []byte) ([]byte, error) {
	var parsed mldsa87.PrivateKey
	if err := parsed.UnmarshalBinary(privateKey); err != nil {
		return nil, fmt.Errorf("private key parse: %w", err)
	}
	publicKey, err := parsed.Public().(*mldsa87.PublicKey).MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("public-from-private marshal: %w", err)
	}
	stored, err := hex.DecodeString(ks.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("stored public key: %w", err)
	}
	if !bytesEqual(publicKey, stored) {
		return nil, fmt.Errorf("keystore integrity check failed: decrypted private key does not match stored public key")
	}
	return publicKey, nil
}

func (c *CLI) recoveryAPI(override string) *CLI {
	base := strings.TrimRight(strings.TrimSpace(override), "/")
	if base == "" {
		base = strings.TrimRight(c.baseURL, "/")
	}
	if !strings.HasSuffix(base, "/api/v1") {
		base += "/api/v1"
	}
	return &CLI{baseURL: base, token: c.token, client: c.client}
}

func randomRecoveryActionID() (string, error) {
	random := make([]byte, 16)
	if _, err := cryptorand.Read(random); err != nil {
		return "", fmt.Errorf("generate recovery action id: %w", err)
	}
	return "legacy-recovery-" + hex.EncodeToString(random), nil
}

func fetchLegacyRecoveryCapsule(c *CLI, locator string) (legacyRecoveryCapsuleResponse, error) {
	body, err := c.get("/wallet/recovery/capsules/" + url.PathEscape(locator))
	if err != nil {
		return legacyRecoveryCapsuleResponse{}, err
	}
	var response legacyRecoveryCapsuleResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return legacyRecoveryCapsuleResponse{}, fmt.Errorf("decode recovery capsule: %w", err)
	}
	return response, nil
}

func waitForLegacyRecoveryCapsule(c *CLI, locator, owner string, timeout time.Duration) (chain.RecoveryCapsuleState, error) {
	deadline := time.Now().Add(timeout)
	for {
		response, err := fetchLegacyRecoveryCapsule(c, locator)
		if err == nil && response.State.Owner == owner && response.State.Locator == locator {
			return response.State, nil
		}
		if time.Now().After(deadline) {
			return chain.RecoveryCapsuleState{}, fmt.Errorf("timed out after %s", timeout)
		}
		time.Sleep(750 * time.Millisecond)
	}
}

func replaceKeystoreWithBackup(path string, data []byte) (string, error) {
	original, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read original keystore for backup: %w", err)
	}
	backupPath := fmt.Sprintf("%s.pre-recovery-%s.bak", path, time.Now().UTC().Format("20060102T150405Z"))
	if err := writeFileExclusive(backupPath, original, false); err != nil {
		return "", fmt.Errorf("write original keystore backup: %w", err)
	}
	if err := writeFileExclusive(path, data, true); err != nil {
		_ = writeFileExclusive(path, original, true)
		return "", fmt.Errorf("write migrated keystore (original restored from backup): %w", err)
	}
	return backupPath, nil
}

func (c *CLI) walletShow(args []string) error {
	fs := flag.NewFlagSet("wallet show", flag.ContinueOnError)
	in := fs.String("in", "", "keystore path (default: ~/.qsdm/wallet.json)")
	jsonOut := fs.Bool("json", false, "emit a JSON object with address + public_key (instead of plain text)")
	if err := fs.Parse(args); err != nil {
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
	if err := keystore.Validate(ks); err != nil {
		return fmt.Errorf("validate %s: %w", path, err)
	}
	if *jsonOut {
		recoveryScheme := ""
		if ks.Recovery != nil {
			recoveryScheme = ks.Recovery.Scheme
		}
		fmt.Printf("{\"path\":%q,\"address\":%q,\"public_key\":%q,\"algorithm\":%q,\"created_at\":%q,\"recovery_enabled\":%t,\"recovery_scheme\":%q}\n",
			path, ks.Address, ks.PublicKey, ks.Algorithm, ks.CreatedAt, ks.Recovery != nil, recoveryScheme)
		return nil
	}
	fmt.Printf("path        %s\n", path)
	fmt.Printf("address     %s\n", ks.Address)
	fmt.Printf("algorithm   %s\n", ks.Algorithm)
	fmt.Printf("public_key  %s…%s  (%d bytes)\n",
		ks.PublicKey[:24], ks.PublicKey[len(ks.PublicKey)-24:], len(ks.PublicKey)/2)
	fmt.Printf("kdf         %s (iters=%d, key_len=%d)\n", ks.KDF, ks.KDFParams.Iterations, ks.KDFParams.KeyLen)
	fmt.Printf("cipher      %s\n", ks.Cipher)
	fmt.Printf("created_at  %s\n", ks.CreatedAt)
	if ks.Recovery != nil {
		fmt.Printf("recovery    %s (%d words)\n", ks.Recovery.Scheme, ks.Recovery.Words)
	} else {
		fmt.Printf("recovery    legacy JSON + passphrase only\n")
	}
	return nil
}

func (c *CLI) walletInspect(args []string) error {
	fs := flag.NewFlagSet("wallet inspect", flag.ContinueOnError)
	in := fs.String("in", "", "keystore path (default: ~/.qsdm/wallet.json)")
	passphraseFile := fs.String("passphrase-file", "", "read passphrase from file ('-' for stdin); empty = prompt")
	if err := fs.Parse(args); err != nil {
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
	passphrase, err := readPassphrase(*passphraseFile, false /*confirm*/)
	if err != nil {
		return fmt.Errorf("read passphrase: %w", err)
	}
	defer zero(passphrase)

	stored, err := verifyKeystorePrivateKey(ks, passphrase)
	if err != nil {
		return err
	}

	fmt.Printf("path        %s\n", path)
	fmt.Printf("address     %s\n", ks.Address)
	fmt.Printf("algorithm   %s\n", ks.Algorithm)
	fmt.Printf("public_key  %s  (%d bytes, integrity-verified)\n", ks.PublicKey, len(stored))
	fmt.Printf("OK: keystore decrypts cleanly and the decrypted private key produces the stored public key.\n")
	return nil
}

func verifyKeystorePrivateKey(ks keystore.Keystore, passphrase []byte) ([]byte, error) {
	priv, err := keystore.Decrypt(ks, passphrase)
	if err != nil {
		return nil, err
	}
	defer zero(priv)

	// Round-trip integrity: reconstruct the public key from the decrypted
	// private key and verify that it matches the public metadata.
	var sk mldsa87.PrivateKey
	if err := sk.UnmarshalBinary(priv); err != nil {
		return nil, fmt.Errorf("private key parse: %w", err)
	}
	recovered, err := sk.Public().(*mldsa87.PublicKey).MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("public-from-private marshal: %w", err)
	}
	stored, err := hex.DecodeString(ks.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("stored public_key hex: %w", err)
	}
	if !bytesEqual(recovered, stored) {
		return nil, fmt.Errorf("integrity check failed: public_key recovered from decrypted private key does not match the public_key field in the keystore (file was edited after encryption)")
	}
	return stored, nil
}

func samePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return left == right
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(leftAbs, rightAbs)
	}
	return leftAbs == rightAbs
}

func (c *CLI) walletSign(args []string) error {
	fs := flag.NewFlagSet("wallet sign", flag.ContinueOnError)
	in := fs.String("in", "", "keystore path (default: ~/.qsdm/wallet.json)")
	passphraseFile := fs.String("passphrase-file", "", "read passphrase from file ('-' for stdin); empty = prompt")
	msgHex := fs.String("message", "", "hex-encoded message to sign (mutually exclusive with --message-file)")
	msgFile := fs.String("message-file", "", "file to read raw bytes from ('-' for stdin)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if (*msgHex == "" && *msgFile == "") || (*msgHex != "" && *msgFile != "") {
		return fmt.Errorf("--message OR --message-file is required (and they are mutually exclusive)")
	}
	var message []byte
	if *msgHex != "" {
		b, err := hex.DecodeString(*msgHex)
		if err != nil {
			return fmt.Errorf("--message hex: %w", err)
		}
		message = b
	} else {
		b, err := readAllFromPathOrStdin(*msgFile)
		if err != nil {
			return fmt.Errorf("--message-file: %w", err)
		}
		message = b
	}
	if len(message) == 0 {
		return fmt.Errorf("message is empty (refusing to sign nothing)")
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

	var sk mldsa87.PrivateKey
	if err := sk.UnmarshalBinary(priv); err != nil {
		return fmt.Errorf("private key parse: %w", err)
	}
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(&sk, message, nil, true /*randomized*/, sig); err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	fmt.Fprintf(os.Stderr, "signed %d bytes with %s (%d-byte ML-DSA-87 signature)\n",
		len(message), ks.Address, len(sig))
	fmt.Println(hex.EncodeToString(sig))
	return nil
}

func (c *CLI) walletVerify(args []string) error {
	fs := flag.NewFlagSet("wallet verify", flag.ContinueOnError)
	publicKeyHex := fs.String("public-key", "", "hex-encoded ML-DSA-87 public key")
	publicKeyFile := fs.String("public-key-file", "", "file containing a hex-encoded ML-DSA-87 public key")
	signatureHex := fs.String("signature", "", "hex-encoded ML-DSA-87 signature")
	signatureFile := fs.String("signature-file", "", "file containing a hex-encoded ML-DSA-87 signature")
	msgHex := fs.String("message", "", "hex-encoded message bytes")
	msgFile := fs.String("message-file", "", "file containing raw message bytes ('-' for stdin)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	publicKey, err := readHexValue("public-key", *publicKeyHex, *publicKeyFile)
	if err != nil {
		return err
	}
	signature, err := readHexValue("signature", *signatureHex, *signatureFile)
	if err != nil {
		return err
	}

	if (*msgHex == "" && *msgFile == "") || (*msgHex != "" && *msgFile != "") {
		return fmt.Errorf("--message OR --message-file is required (and they are mutually exclusive)")
	}
	var message []byte
	if *msgHex != "" {
		message, err = hex.DecodeString(strings.TrimSpace(*msgHex))
		if err != nil {
			return fmt.Errorf("--message hex: %w", err)
		}
	} else {
		message, err = readAllFromPathOrStdin(*msgFile)
		if err != nil {
			return fmt.Errorf("--message-file: %w", err)
		}
	}
	if len(message) == 0 {
		return fmt.Errorf("message is empty")
	}

	if err := verifyMLDSA87Signature(publicKey, signature, message); err != nil {
		return err
	}
	fmt.Println("OK: ML-DSA-87 signature verified")
	return nil
}

func readHexValue(flagName, inline, sourceFile string) (string, error) {
	if (strings.TrimSpace(inline) == "") == (strings.TrimSpace(sourceFile) == "") {
		return "", fmt.Errorf("exactly one of --%s or --%s-file is required", flagName, flagName)
	}
	if strings.TrimSpace(inline) != "" {
		return strings.TrimSpace(inline), nil
	}
	b, err := readAllFromPathOrStdin(sourceFile)
	if err != nil {
		return "", fmt.Errorf("read %s file: %w", flagName, err)
	}
	return strings.TrimSpace(string(b)), nil
}

func verifyMLDSA87Signature(publicKeyHex, signatureHex string, message []byte) error {
	publicKeyBytes, err := hex.DecodeString(strings.TrimSpace(publicKeyHex))
	if err != nil {
		return fmt.Errorf("public key hex: %w", err)
	}
	if len(publicKeyBytes) != mldsa87.PublicKeySize {
		return fmt.Errorf("public key must be %d bytes, got %d", mldsa87.PublicKeySize, len(publicKeyBytes))
	}
	signatureBytes, err := hex.DecodeString(strings.TrimSpace(signatureHex))
	if err != nil {
		return fmt.Errorf("signature hex: %w", err)
	}
	if len(signatureBytes) != mldsa87.SignatureSize {
		return fmt.Errorf("signature must be %d bytes, got %d", mldsa87.SignatureSize, len(signatureBytes))
	}

	var publicKey mldsa87.PublicKey
	if err := publicKey.UnmarshalBinary(publicKeyBytes); err != nil {
		return fmt.Errorf("public key parse: %w", err)
	}
	if !mldsa87.Verify(&publicKey, message, nil, signatureBytes) {
		return fmt.Errorf("ML-DSA-87 signature verification failed")
	}
	return nil
}

// ---- helpers ----

// defaultWalletPath resolves the keystore file path:
//   - explicit non-empty input wins (passed through filepath.Clean)
//   - empty input → $HOME/.qsdm/wallet.json (XDG-style default; per-OS
//     home detected via os.UserHomeDir which respects %USERPROFILE% on
//     Windows and $HOME on Unix).
func defaultWalletPath(explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return filepath.Clean(explicit), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot resolve user home directory for default keystore path: %w", err)
	}
	return filepath.Join(home, ".qsdm", "wallet.json"), nil
}

func loadKeystore(path string) (keystore.Keystore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return keystore.Keystore{}, fmt.Errorf("read %s: %w", path, err)
	}
	return keystore.Unmarshal(data)
}

// readPassphrase obtains the passphrase by one of three paths:
//   - sourceFile == "-": read from stdin (no echo control; the operator
//     is expected to pipe a file). One trailing newline trimmed.
//   - sourceFile != "" && != "-": read from that file. One trailing
//     newline trimmed.
//   - sourceFile == "": prompt the user interactively with golang.org/x/term
//     so the passphrase is never echoed. If confirm is true (wallet
//     new), prompt twice and ensure both entries match.
//
// The returned slice is owned by the caller; the caller is responsible
// for zeroing it (the helper `zero` exists for that purpose).
func readPassphrase(sourceFile string, confirm bool) ([]byte, error) {
	if sourceFile == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, err
		}
		return trimTrailingNewline(b), nil
	}
	if sourceFile != "" {
		b, err := os.ReadFile(sourceFile)
		if err != nil {
			return nil, err
		}
		return trimTrailingNewline(b), nil
	}
	// Interactive prompt.
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return nil, fmt.Errorf("no --passphrase-file supplied and stdin is not a terminal — supply a passphrase file or run interactively")
	}
	fmt.Fprint(os.Stderr, "passphrase: ")
	pass, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return nil, err
	}
	if len(pass) == 0 {
		return nil, fmt.Errorf("empty passphrase refused")
	}
	if confirm {
		fmt.Fprint(os.Stderr, "confirm:    ")
		again, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return nil, err
		}
		if !bytesEqual(pass, again) {
			zero(again)
			return nil, fmt.Errorf("passphrases do not match")
		}
		zero(again)
	}
	return pass, nil
}

func trimTrailingNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func readAllFromPathOrStdin(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

// writeFileExclusive writes data with mode 0600. If force is false and
// the target exists, returns an error. The mode-0600 part matters on
// Unix — the keystore file contains an attacker-controlled ciphertext
// that survives offline passphrase-cracking attempts, so we should at
// least keep it out of `other`/`group`.
func writeFileExclusive(path string, data []byte, force bool) error {
	flag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if !force {
		flag |= os.O_EXCL
	}
	f, err := os.OpenFile(path, flag, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
