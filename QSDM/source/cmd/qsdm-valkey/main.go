// Command qsdm-valkey provisions a validator consensus (BFT) signer key
// offline, in exactly the on-disk format the node loads from its consensus
// signer key path. It never prints private key material: only the validator
// address (hex SHA-256 of the ML-DSA-87 public key) and a public-key
// fingerprint.
//
//	qsdm-valkey generate --out <file>   create a new key (mode 0600); refuses to overwrite
//	qsdm-valkey show --in <file>        load the key as the node would and print its address
package main

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

const usage = `usage:
  qsdm-valkey generate --out <file>   create a new validator consensus key (0600, never overwrites)
  qsdm-valkey show --in <file>        verify the key loads as the node loads it; print its address
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "generate":
		err = runGenerate(args[1:], stdout, stderr)
	case "show":
		err = runShow(args[1:], stdout, stderr)
	case "-h", "-help", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "qsdm-valkey: unknown subcommand %q\n%s", args[0], usage)
		return 2
	}
	if err != nil {
		var usageErr usageError
		if errors.As(err, &usageErr) {
			fmt.Fprintf(stderr, "qsdm-valkey: %v\n%s", err, usage)
			return 2
		}
		fmt.Fprintf(stderr, "qsdm-valkey: %v\n", err)
		return 1
	}
	return 0
}

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func parsePathFlag(name, flagName string, args []string, stderr io.Writer) (string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String(flagName, "", "path of the consensus signer key file")
	if err := fs.Parse(args); err != nil {
		return "", usageError{msg: err.Error()}
	}
	if fs.NArg() != 0 {
		return "", usageError{msg: fmt.Sprintf("%s: unexpected arguments %q", name, fs.Args())}
	}
	if *path == "" {
		return "", usageError{msg: fmt.Sprintf("%s: --%s is required", name, flagName)}
	}
	return *path, nil
}

func runGenerate(args []string, stdout, stderr io.Writer) error {
	path, err := parsePathFlag("generate", "out", args, stderr)
	if err != nil {
		return err
	}
	signer, err := chain.CreateBFTSigner(path)
	if err != nil {
		return err
	}
	// Re-load through the node's loader so a file that would be rejected at
	// startup is reported here instead.
	loaded, err := chain.LoadBFTSigner(path)
	if err != nil {
		return fmt.Errorf("generated key does not load as the node would load it: %w", err)
	}
	if loaded.Address() != signer.Address() {
		return errors.New("generated key reloaded with a different address")
	}
	fmt.Fprintf(stdout, "validator_address: %s\n", loaded.Address())
	fmt.Fprintf(stdout, "public_key_fingerprint: %s\n", publicKeyFingerprint(loaded.GetPublicKey()))
	return nil
}

func runShow(args []string, stdout, stderr io.Writer) error {
	path, err := parsePathFlag("show", "in", args, stderr)
	if err != nil {
		return err
	}
	signer, err := chain.LoadBFTSigner(path)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "validator_address: %s\n", signer.Address())
	return nil
}

// publicKeyFingerprint renders the SHA-256 of the public key in the familiar
// "SHA256:<base64>" form for visual comparison. It is the same digest as the
// validator address, in a different encoding.
func publicKeyFingerprint(publicKey []byte) string {
	sum := sha256.Sum256(publicKey)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}
