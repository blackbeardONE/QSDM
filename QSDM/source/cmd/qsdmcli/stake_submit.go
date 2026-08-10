package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cloudflare/circl/sign/mldsa/mldsa87"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/keystore"
)

// Signed submission for qsdm/staking/v1 bonds.
//
// stake_helper.go builds validated payloads; this file signs and submits
// them, which is what an operator actually needs to join the validator set.
// It reuses the ML-DSA-87 keystore path the enrollment commands use, so
// there is one way to unlock a wallet in this CLI rather than two.

// stakingEnvelope mirrors api.StakingEnvelope.
//
// Field names and JSON tags MUST stay identical to the server's struct: the
// signature covers json.Marshal of this shape with Signature and PublicKey
// blanked, and the handler reconstructs exactly that to verify. A divergence
// on either side yields a signature that verifies nowhere, so the shapes are
// deliberately kept symmetric rather than hand-rolling an encoding.
type stakingEnvelope struct {
	ID           string  `json:"id"`
	Sender       string  `json:"sender"`
	Action       string  `json:"action"`
	Validator    string  `json:"validator"`
	Amount       float64 `json:"amount"`
	UnbondBlocks uint64  `json:"unbond_blocks,omitempty"`
	Nonce        uint64  `json:"nonce,omitempty"`
	Timestamp    string  `json:"timestamp"`
	Signature    string  `json:"signature"`
	PublicKey    string  `json:"public_key,omitempty"`
}

func newStakingEnvelopeID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Uniqueness here only needs to satisfy mempool dedupe; replay is
		// rejected by nonce at the chain layer regardless.
		return fmt.Sprintf("stake-%d", time.Now().UnixNano())
	}
	return "stake-" + hex.EncodeToString(b[:])
}

// stakeHelperSubmit builds, signs and submits a bond or unbond.
func (c *CLI) stakeHelperSubmit(action string, args []string) error {
	fs := flag.NewFlagSet("stake-helper submit-"+action, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		validator      = fs.String("validator", "", "validator address to bond to (required)")
		amount         = fs.Float64("amount", 0, "amount of CELL (required, > 0)")
		unbondFor      = fs.Uint64("unbond-blocks", 0, "blocks before unbonded funds mature (unbond only; 0 = protocol default)")
		nonce          = fs.Uint64("nonce", 0, "per-sender replay counter; fetch the next value from /api/v1/wallet/nonce")
		walletPath     = fs.String("wallet", "", "path to the ML-DSA-87 keystore (default: standard wallet location)")
		passphraseFile = fs.String("passphrase-file", "", "file holding the keystore passphrase (keeps it out of argv)")
		dryRun         = fs.Bool("dry-run", false, "print the signed envelope instead of submitting it")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*validator) == "" {
		fs.Usage()
		return errors.New("--validator is required")
	}
	if *amount <= 0 {
		fs.Usage()
		return errors.New("--amount is required and must be positive")
	}

	env := stakingEnvelope{
		ID:        newStakingEnvelopeID(),
		Action:    action,
		Validator: strings.TrimSpace(*validator),
		Amount:    *amount,
		Nonce:     *nonce,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	if action == chain.StakingActionUnbond {
		env.UnbondBlocks = *unbondFor
	}

	// Validate through the chain decoder BEFORE prompting for a
	// passphrase, so a malformed request fails without costing the
	// operator a keystore unlock.
	if _, err := chain.EncodeStakingPayload(chain.StakingPayload{
		Action:       env.Action,
		Validator:    env.Validator,
		Amount:       env.Amount,
		UnbondBlocks: env.UnbondBlocks,
	}); err != nil {
		return fmt.Errorf("payload rejected by the chain validator: %w", err)
	}

	signed, err := signStakingEnvelope(env, *walletPath, *passphraseFile)
	if err != nil {
		return err
	}

	if *dryRun {
		out, _ := json.MarshalIndent(signed, "", "  ")
		fmt.Println(string(out))
		fmt.Fprintf(os.Stderr,
			"\nDry run: nothing submitted. POST this to %s/staking/submit-signed to bond.\n",
			c.baseURL)
		return nil
	}

	body, err := c.post("/staking/submit-signed", signed)
	if err != nil {
		return err
	}
	prettyPrint(body)
	fmt.Fprintf(os.Stderr,
		"\nSubmitted. The bond takes effect once committed; validator membership is\n"+
			"re-derived on each committed height. Check with: qsdmcli stake-helper show\n")
	return nil
}

// signStakingEnvelope unlocks the keystore and signs the canonical form.
//
// Mirrors signEnrollmentEnvelope: same keystore, same ML-DSA-87 primitive,
// and the same empty FIPS 204 context (ctx = nil) the server's verifier
// uses via crypto.Dilithium.VerifyWithPublicKey.
func signStakingEnvelope(env stakingEnvelope, walletPath, passphraseFile string) (stakingEnvelope, error) {
	path, err := defaultWalletPath(walletPath)
	if err != nil {
		return stakingEnvelope{}, err
	}
	ks, err := loadKeystore(path)
	if err != nil {
		return stakingEnvelope{}, err
	}
	passphrase, err := readPassphrase(passphraseFile, false)
	if err != nil {
		return stakingEnvelope{}, fmt.Errorf("read passphrase: %w", err)
	}
	defer zero(passphrase)
	priv, err := keystore.Decrypt(ks, passphrase)
	if err != nil {
		return stakingEnvelope{}, err
	}
	defer zero(priv)

	pub, err := hex.DecodeString(ks.PublicKey)
	if err != nil {
		return stakingEnvelope{}, fmt.Errorf("keystore public_key not hex: %w", err)
	}
	sum := sha256.Sum256(pub)
	address := hex.EncodeToString(sum[:])
	if env.Sender == "" {
		env.Sender = address
	} else if env.Sender != address {
		return stakingEnvelope{}, fmt.Errorf(
			"sender %s does not match keystore address %s", env.Sender, address)
	}

	// Canonical form: Signature and PublicKey blanked, encoding/json.
	env.Signature = ""
	env.PublicKey = ""
	canonical, err := json.Marshal(env)
	if err != nil {
		return stakingEnvelope{}, fmt.Errorf("canonicalize staking envelope: %w", err)
	}

	var sk mldsa87.PrivateKey
	if err := sk.UnmarshalBinary(priv); err != nil {
		return stakingEnvelope{}, fmt.Errorf("private key parse: %w", err)
	}
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(&sk, canonical, nil, true, sig); err != nil {
		return stakingEnvelope{}, fmt.Errorf("sign staking envelope: %w", err)
	}
	env.Signature = hex.EncodeToString(sig)
	env.PublicKey = ks.PublicKey
	return env, nil
}
