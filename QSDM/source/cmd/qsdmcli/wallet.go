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
//	qsdmcli wallet sign-tx  [--in PATH] [--passphrase-file FILE]
//	                        [--envelope-file PATH | '-'] [--nonce N | --auto-nonce]
//	                        [--api-url URL] [--api-timeout DUR]
//	qsdmcli wallet sign-task-action [--in PATH] [--passphrase-file FILE]
//	                                [--envelope-file PATH | '-'] [--nonce N]
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
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/keystore"
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
	case "show":
		return c.walletShow(rest)
	case "inspect":
		return c.walletInspect(rest)
	case "sign":
		return c.walletSign(rest)
	case "sign-tx":
		return c.walletSignTx(rest)
	case "sign-task-action":
		return c.walletSignTaskAction(rest)
	case "help", "-h", "--help":
		fmt.Fprint(os.Stdout, walletHelp)
		return nil
	default:
		return fmt.Errorf("unknown wallet subcommand %q\n\n%s", sub, walletHelp)
	}
}

func walletUsageError() error {
	return fmt.Errorf("usage: qsdmcli wallet <new|show|inspect|sign|sign-tx|sign-task-action> [flags]\n\n%s", walletHelp)
}

const walletHelp = `qsdmcli wallet — self-custody keystore (ML-DSA-87)

Subcommands:
  new      Generate a fresh keypair, write an encrypted keystore, print the
           address. The private key never touches disk in plaintext.
  show     Print address and public key from an existing keystore. No
           passphrase required (these fields are plaintext in the file).
  inspect  Decrypt the keystore and verify the on-disk public key matches
           the encrypted private key. Prompts for passphrase.
  sign     Decrypt the keystore and sign a message with the wallet's
           private key. Prompts for passphrase. Outputs hex signature.
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

Common flags:
  --in   PATH           Keystore file to read (default: ~/.qsdm/wallet.json)
  --out  PATH           Keystore file to write (new only; default: ~/.qsdm/wallet.json)
  --passphrase-file FILE
                        Read passphrase from FILE (use '-' for stdin).
                        Omit to prompt interactively without echo.
  --force               Overwrite an existing keystore (new only). Off by default.
  --message      HEX    Hex-encoded message bytes to sign (sign only).
  --message-file PATH   Read message bytes to sign from a file (sign only;
                        use '-' for stdin). Mutually exclusive with --message.
  --envelope-file PATH  JSON envelope to sign (sign-tx or sign-task-action;
                        default: stdin).
  --nonce N             Nonce to stamp (sign-tx or sign-task-action;
                        mutually exclusive with --auto-nonce for sign-tx).
  --auto-nonce          Resolve nonce from --api-url before signing (sign-tx only).
  --api-url URL         Validator base URL for --auto-nonce (default: https://api.qsdm.tech).
  --api-timeout DUR     HTTP timeout for --auto-nonce (default: 10s).

Examples:
  qsdmcli wallet new
  qsdmcli wallet new --out ~/.qsdm/miner.json --passphrase-file pass.txt
  qsdmcli wallet show
  qsdmcli wallet sign --message-file tx.json > tx.sig.hex
  # Build envelope.json (no signature/public_key/nonce fields), then:
  qsdmcli wallet sign-tx --auto-nonce < envelope.json \
    | curl -fsS -H 'Content-Type: application/json' --data-binary @- \
           https://api.qsdm.tech/api/v1/wallet/submit-signed
  qsdmcli wallet sign-task-action < task-action.json \
    | curl -fsS -H 'Content-Type: application/json' --data-binary @- \
           https://api.qsdm.tech/api/v1/tasks/actions/submit-signed
`

func (c *CLI) walletNew(args []string) error {
	fs := flag.NewFlagSet("wallet new", flag.ContinueOnError)
	out := fs.String("out", "", "keystore output path (default: ~/.qsdm/wallet.json)")
	passphraseFile := fs.String("passphrase-file", "", "read passphrase from file ('-' for stdin); empty = prompt")
	force := fs.Bool("force", false, "overwrite existing keystore at --out")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := defaultWalletPath(*out)
	if err != nil {
		return err
	}
	if !*force {
		if _, statErr := os.Stat(path); statErr == nil {
			return fmt.Errorf("refusing to overwrite existing keystore at %s (pass --force to override)", path)
		}
	}
	passphrase, err := readPassphrase(*passphraseFile, true /*confirm*/)
	if err != nil {
		return fmt.Errorf("read passphrase: %w", err)
	}
	defer zero(passphrase)

	pk, sk, err := mldsa87.GenerateKey(nil)
	if err != nil {
		return fmt.Errorf("mldsa87.GenerateKey: %w", err)
	}
	pubBytes, err := pk.MarshalBinary()
	if err != nil {
		return fmt.Errorf("public key marshal: %w", err)
	}
	privBytes, err := sk.MarshalBinary()
	if err != nil {
		return fmt.Errorf("private key marshal: %w", err)
	}
	defer zero(privBytes)

	ks, err := keystore.Encrypt(pubBytes, privBytes, passphrase)
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
	// Stdout: only the address, so the line can be piped straight into a
	// miner / mining-enroll command. Everything else goes to stderr.
	fmt.Fprintf(os.Stderr, "wrote keystore to %s (mode 0600)\n", path)
	fmt.Fprintf(os.Stderr, "store the keystore + remember the passphrase. Losing either is unrecoverable.\n")
	fmt.Println(ks.Address)
	return nil
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
		fmt.Printf("{\"path\":%q,\"address\":%q,\"public_key\":%q,\"algorithm\":%q,\"created_at\":%q}\n",
			path, ks.Address, ks.PublicKey, ks.Algorithm, ks.CreatedAt)
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

	priv, err := keystore.Decrypt(ks, passphrase)
	if err != nil {
		return err
	}
	defer zero(priv)

	// Round-trip integrity: reconstruct the public key from the
	// decrypted private and verify it matches the stored public_key.
	// If they disagree, the keystore is mutated even though the
	// AES-GCM tag verified — possible only if the metadata public_key
	// was edited after the file was encrypted.
	var sk mldsa87.PrivateKey
	if err := sk.UnmarshalBinary(priv); err != nil {
		return fmt.Errorf("private key parse: %w", err)
	}
	recovered, err := sk.Public().(*mldsa87.PublicKey).MarshalBinary()
	if err != nil {
		return fmt.Errorf("public-from-private marshal: %w", err)
	}
	stored, err := hex.DecodeString(ks.PublicKey)
	if err != nil {
		return fmt.Errorf("stored public_key hex: %w", err)
	}
	if !bytesEqual(recovered, stored) {
		return fmt.Errorf("integrity check failed: public_key recovered from decrypted private key does not match the public_key field in the keystore (file was edited after encryption)")
	}

	fmt.Printf("path        %s\n", path)
	fmt.Printf("address     %s\n", ks.Address)
	fmt.Printf("algorithm   %s\n", ks.Algorithm)
	fmt.Printf("public_key  %s  (%d bytes, integrity-verified)\n", ks.PublicKey, len(stored))
	fmt.Printf("OK: keystore decrypts cleanly and the decrypted private key produces the stored public key.\n")
	return nil
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
