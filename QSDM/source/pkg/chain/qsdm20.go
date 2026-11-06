package chain

// qsdm20.go: QSDM-20 native tokens, version 1 (contract qsdm/token/v1).
//
// The rules are in QSDM/docs/docs/QSDM_20.md. This file holds the stateless
// half: the transaction shape, the payload decoder and its field rules, the
// token id, the signing digest, the signature check, the activation height
// and the mempool admission checker. qsdm20_state.go holds the token state.
//
// Switched off by default: with no activation height (0) every token
// transaction is refused at admission and in every block, and the token
// state stays empty, so no block's state root changes.
//
// Token amounts are integers (math/big) bounded to 2^256 - 1. The only
// float in this file is the transaction's CELL fee, which every transaction
// carries as float64 CELL; it must be exactly a whole number of dust
// (qsdm20FeeDust), and the signature covers that integer.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	qcrypto "github.com/blackbeardONE/QSDM/pkg/crypto"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

const (
	// QSDM20ContractID identifies QSDM-20 token transactions.
	QSDM20ContractID = "qsdm/token/v1"

	QSDM20ActionCreate   = "create"
	QSDM20ActionTransfer = "transfer"
	QSDM20ActionMint     = "mint"
	QSDM20ActionBurn     = "burn"
	QSDM20ActionRenounce = "renounce"

	// Minting modes of a token.
	QSDM20MintNone   = "none"
	QSDM20MintIssuer = "issuer"
	QSDM20MintOpen   = "open"

	// QSDM20TxIDPrefix starts every QSDM-20 transaction id. The rest of the
	// id is the lowercase hex signing digest, so the id is fixed by what the
	// sender signed and cannot be changed by whoever relays the transaction.
	QSDM20TxIDPrefix = "qsdm20-"

	// Field rules (QSDM_20.md, "Field rules").
	qsdm20MaxSymbolLen = 12
	qsdm20MaxNameLen   = 48
	qsdm20MaxDecimals  = 18

	// Domains of the three length-prefixed encodings.
	qsdm20IDDomain    = "qsdm/token/v1/id"
	qsdm20TxDomain    = "qsdm/token/v1/tx"
	qsdm20StateDomain = "qsdm/token/v1/state"
)

// qsdm20MaxUnits is 2^256 - 1, the largest amount any field may carry.
var qsdm20MaxUnits = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))

// qsdm20MaxUnitDigits is the number of decimal digits of 2^256 - 1. A longer
// string is over the bound whatever its digits are, so it is refused before
// it is parsed.
var qsdm20MaxUnitDigits = len(qsdm20MaxUnits.String())

var (
	ErrNotQSDM20Tx = errors.New("chain: tx is not a QSDM-20 token transaction")
	// ErrQSDM20Inactive refuses every token transaction below the activation
	// height, and every one while no height is set.
	ErrQSDM20Inactive = errors.New("chain: QSDM-20 token transactions are not active")
	// ErrQSDM20Field refuses a malformed transaction: a bad field, a payload
	// that is not canonical JSON, or outer transaction fields that do not
	// fit a token transaction.
	ErrQSDM20Field = errors.New("chain: invalid QSDM-20 transaction")
	// ErrQSDM20Signature refuses a signature that does not verify, or a
	// public key that does not hash to the sender.
	ErrQSDM20Signature         = errors.New("chain: invalid QSDM-20 signature")
	ErrQSDM20UnknownToken      = errors.New("chain: unknown QSDM-20 token")
	ErrQSDM20TokenExists       = errors.New("chain: QSDM-20 token already exists")
	ErrQSDM20NotIssuer         = errors.New("chain: only the token's issuer may do this")
	ErrQSDM20MintingOff        = errors.New("chain: minting is off for this token")
	ErrQSDM20OpenMintRecipient = errors.New("chain: an open mint must go to its sender")
	ErrQSDM20OverMintLimit     = errors.New("chain: amount is over the token's mint_limit")
	ErrQSDM20OverMaxSupply     = errors.New("chain: the mint would pass the token's max_supply")
	ErrQSDM20InsufficientUnits = errors.New("chain: not enough token units")
)

var qsdm20ActivationHeight atomic.Uint64

// SetQSDM20ActivationHeight sets the first block height at which QSDM-20
// token transactions are valid. Zero (the default) keeps them off. Every
// validator must use the same value.
func SetQSDM20ActivationHeight(h uint64) { qsdm20ActivationHeight.Store(h) }

// QSDM20ActivationHeight reports the configured height (0 = off).
func QSDM20ActivationHeight() uint64 { return qsdm20ActivationHeight.Load() }

// QSDM20ActiveAt reports whether token transactions are valid in a block at
// height.
func QSDM20ActiveAt(height uint64) bool {
	h := qsdm20ActivationHeight.Load()
	return h != 0 && height >= h
}

// checkQSDM20Active is the activation rule for a block at height.
func checkQSDM20Active(height uint64) error {
	if QSDM20ActiveAt(height) {
		return nil
	}
	h := QSDM20ActivationHeight()
	if h == 0 {
		return fmt.Errorf("%w: no activation height is set", ErrQSDM20Inactive)
	}
	return fmt.Errorf("%w: block %d is below the activation height %d", ErrQSDM20Inactive, height, h)
}

// qsdm20HeightFrom resolves the height a token transaction is judged at
// from a block-height source (the next block's height). With no activation
// height it refuses without asking the source; with one, a missing source
// fails closed rather than judging at height 0.
func qsdm20HeightFrom(next func() (uint64, bool)) (uint64, error) {
	if QSDM20ActivationHeight() == 0 {
		return 0, checkQSDM20Active(0)
	}
	if next == nil {
		return 0, ErrEnrollmentHeightUnset
	}
	h, ok := next()
	if !ok {
		return 0, ErrEnrollmentHeightUnset
	}
	if err := checkQSDM20Active(h); err != nil {
		return 0, err
	}
	return h, nil
}

// QSDM20Action is the payload of a QSDM-20 transaction. Each action carries
// exactly its own fields (QSDM_20.md, "Transactions"); every other field is
// absent. The field order is the canonical JSON key order.
type QSDM20Action struct {
	Action string `json:"action"`

	// create
	Symbol        string `json:"symbol,omitempty"`
	Name          string `json:"name,omitempty"`
	Decimals      *uint8 `json:"decimals,omitempty"`
	MaxSupply     string `json:"max_supply,omitempty"`
	InitialSupply string `json:"initial_supply,omitempty"`
	Mint          string `json:"mint,omitempty"`
	MintLimit     string `json:"mint_limit,omitempty"`

	// transfer, mint, burn, renounce
	Token  string `json:"token,omitempty"`
	To     string `json:"to,omitempty"`
	Amount string `json:"amount,omitempty"`
}

// QSDM20Decimals returns a pointer to d, for building a create action.
func QSDM20Decimals(d uint8) *uint8 { return &d }

func qsdm20FieldError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrQSDM20Field, fmt.Sprintf(format, args...))
}

// DecodeQSDM20Payload decodes a payload and applies the field rules. The
// payload must be exactly the compact JSON encoding/json writes for the
// decoded action, so one action has one payload: no other key order, no
// spaces, no unknown, duplicate or empty fields.
func DecodeQSDM20Payload(payload []byte) (QSDM20Action, error) {
	var a QSDM20Action
	if err := json.Unmarshal(payload, &a); err != nil {
		return QSDM20Action{}, qsdm20FieldError("payload is not a JSON action: %v", err)
	}
	canonical, err := json.Marshal(a)
	if err != nil {
		return QSDM20Action{}, qsdm20FieldError("canonicalize payload: %v", err)
	}
	if !bytes.Equal(canonical, payload) {
		return QSDM20Action{}, qsdm20FieldError("payload is not canonical JSON")
	}
	if err := ValidateQSDM20Action(a); err != nil {
		return QSDM20Action{}, err
	}
	return a, nil
}

// ValidateQSDM20Action applies the field rules of QSDM_20.md to one action.
// It needs no chain state.
func ValidateQSDM20Action(a QSDM20Action) error {
	switch a.Action {
	case QSDM20ActionCreate:
		if a.Token != "" || a.To != "" || a.Amount != "" {
			return qsdm20FieldError("create carries only symbol, name, decimals, max_supply, initial_supply, mint and mint_limit")
		}
		if !validQSDM20Symbol(a.Symbol) {
			return qsdm20FieldError("symbol must be 1 to %d characters A to Z and 0 to 9", qsdm20MaxSymbolLen)
		}
		if !validQSDM20Name(a.Name) {
			return qsdm20FieldError("name must be 1 to %d characters of valid UTF-8 with no control characters", qsdm20MaxNameLen)
		}
		if a.Decimals == nil || *a.Decimals > qsdm20MaxDecimals {
			return qsdm20FieldError("decimals must be 0 to %d", qsdm20MaxDecimals)
		}
		maxSupply, err := parseQSDM20Units("max_supply", a.MaxSupply, false)
		if err != nil {
			return err
		}
		initial, err := parseQSDM20Units("initial_supply", a.InitialSupply, true)
		if err != nil {
			return err
		}
		if initial.Cmp(maxSupply) > 0 {
			return qsdm20FieldError("initial_supply is over max_supply")
		}
		switch a.Mint {
		case QSDM20MintNone:
			if a.MintLimit != "" {
				return qsdm20FieldError("mint_limit is present only in open mode")
			}
			if initial.Cmp(maxSupply) != 0 {
				return qsdm20FieldError("in mint none, initial_supply must equal max_supply")
			}
		case QSDM20MintIssuer:
			if a.MintLimit != "" {
				return qsdm20FieldError("mint_limit is present only in open mode")
			}
		case QSDM20MintOpen:
			if _, err := parseQSDM20Units("mint_limit", a.MintLimit, false); err != nil {
				return err
			}
		default:
			return qsdm20FieldError("mint must be none, issuer or open")
		}
		return nil
	case QSDM20ActionTransfer, QSDM20ActionMint:
		if hasQSDM20CreateFields(a) {
			return qsdm20FieldError("%s carries only token, to and amount", a.Action)
		}
		if !validQSDM20Hex64(a.Token) {
			return qsdm20FieldError("token must be a token id (64 lowercase hex characters)")
		}
		if !validQSDM20Hex64(a.To) {
			return qsdm20FieldError("to must be a QSDM address (64 lowercase hex characters)")
		}
		_, err := parseQSDM20Units("amount", a.Amount, false)
		return err
	case QSDM20ActionBurn:
		if hasQSDM20CreateFields(a) || a.To != "" {
			return qsdm20FieldError("burn carries only token and amount")
		}
		if !validQSDM20Hex64(a.Token) {
			return qsdm20FieldError("token must be a token id (64 lowercase hex characters)")
		}
		_, err := parseQSDM20Units("amount", a.Amount, false)
		return err
	case QSDM20ActionRenounce:
		if hasQSDM20CreateFields(a) || a.To != "" || a.Amount != "" {
			return qsdm20FieldError("renounce carries only token")
		}
		if !validQSDM20Hex64(a.Token) {
			return qsdm20FieldError("token must be a token id (64 lowercase hex characters)")
		}
		return nil
	default:
		return qsdm20FieldError("action must be create, transfer, mint, burn or renounce")
	}
}

func hasQSDM20CreateFields(a QSDM20Action) bool {
	return a.Symbol != "" || a.Name != "" || a.Decimals != nil || a.MaxSupply != "" ||
		a.InitialSupply != "" || a.Mint != "" || a.MintLimit != ""
}

// validQSDM20Symbol: 1 to 12 characters A-Z and 0-9.
func validQSDM20Symbol(s string) bool {
	if s == "" || len(s) > qsdm20MaxSymbolLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// validQSDM20Name: 1 to 48 Unicode characters (code points) of valid UTF-8,
// none of them a control character (category Cc).
func validQSDM20Name(s string) bool {
	if s == "" || !utf8.ValidString(s) {
		return false
	}
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
		n++
	}
	return n <= qsdm20MaxNameLen
}

// validQSDM20Hex64 accepts exactly 64 lowercase hex characters: a token id
// or a QSDM address.
func validQSDM20Hex64(s string) bool {
	if len(s) != 2*sha256.Size {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// parseQSDM20Units parses a base-unit amount: decimal digits with no sign and
// no leading zeros, from 1 (or 0 when allowZero) to 2^256 - 1.
func parseQSDM20Units(field, s string, allowZero bool) (*big.Int, error) {
	if s == "" {
		return nil, qsdm20FieldError("%s is required", field)
	}
	if len(s) > qsdm20MaxUnitDigits {
		return nil, qsdm20FieldError("%s is over 2^256 - 1", field)
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return nil, qsdm20FieldError("%s must be a decimal string of base units", field)
		}
	}
	if len(s) > 1 && s[0] == '0' {
		return nil, qsdm20FieldError("%s has a leading zero", field)
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, qsdm20FieldError("%s must be a decimal string of base units", field)
	}
	if v.Sign() == 0 && !allowZero {
		return nil, qsdm20FieldError("%s must be at least 1", field)
	}
	if v.Cmp(qsdm20MaxUnits) > 0 {
		return nil, qsdm20FieldError("%s is over 2^256 - 1", field)
	}
	return v, nil
}

// QSDM20TokenID derives the id of the token that creator creates with the
// transaction at nonce: the lowercase hex SHA-256 of the length-prefixed
// fields domain qsdm/token/v1/id, creator and nonce.
func QSDM20TokenID(creator string, nonce uint64) string {
	var b strings.Builder
	writeTagged(&b, "domain", qsdm20IDDomain)
	writeTagged(&b, "creator", creator)
	writeTagged(&b, "nonce", strconv.FormatUint(nonce, 10))
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// qsdm20SigningPreimage is the length-prefixed encoding the signature covers
// (QSDM_20.md, "Signature"): the domain, the sender, the nonce, the fee in
// dust, the action and then the action's fields in the order of the table.
// A create's absent mint_limit is written as an empty value.
func qsdm20SigningPreimage(sender string, nonce, feeDust uint64, a QSDM20Action) string {
	var b strings.Builder
	writeTagged(&b, "domain", qsdm20TxDomain)
	writeTagged(&b, "sender", sender)
	writeTagged(&b, "nonce", strconv.FormatUint(nonce, 10))
	writeTagged(&b, "fee_dust", strconv.FormatUint(feeDust, 10))
	writeTagged(&b, "action", a.Action)
	switch a.Action {
	case QSDM20ActionCreate:
		decimals := ""
		if a.Decimals != nil {
			decimals = strconv.FormatUint(uint64(*a.Decimals), 10)
		}
		writeTagged(&b, "symbol", a.Symbol)
		writeTagged(&b, "name", a.Name)
		writeTagged(&b, "decimals", decimals)
		writeTagged(&b, "max_supply", a.MaxSupply)
		writeTagged(&b, "initial_supply", a.InitialSupply)
		writeTagged(&b, "mint", a.Mint)
		writeTagged(&b, "mint_limit", a.MintLimit)
	case QSDM20ActionTransfer, QSDM20ActionMint:
		writeTagged(&b, "token", a.Token)
		writeTagged(&b, "to", a.To)
		writeTagged(&b, "amount", a.Amount)
	case QSDM20ActionBurn:
		writeTagged(&b, "token", a.Token)
		writeTagged(&b, "amount", a.Amount)
	case QSDM20ActionRenounce:
		writeTagged(&b, "token", a.Token)
	}
	return b.String()
}

// QSDM20SigningDigest is the 32-byte message a QSDM-20 transaction's
// ML-DSA-87 signature covers.
func QSDM20SigningDigest(sender string, nonce, feeDust uint64, a QSDM20Action) []byte {
	sum := sha256.Sum256([]byte(qsdm20SigningPreimage(sender, nonce, feeDust, a)))
	return sum[:]
}

// QSDM20TxID is the transaction id for a signing digest.
func QSDM20TxID(digest []byte) string {
	return QSDM20TxIDPrefix + hex.EncodeToString(digest)
}

// QSDM20FeeCell is the tx.Fee value (CELL) of a fee of feeDust dust. It
// fails when the float does not convert back to exactly feeDust, which
// happens only for fees far above the CELL supply.
func QSDM20FeeCell(feeDust uint64) (float64, error) {
	fee := DustToCell(feeDust)
	if back, err := qsdm20FeeDust(fee); err != nil || back != feeDust {
		return 0, qsdm20FieldError("a fee of %d dust has no exact CELL value", feeDust)
	}
	return fee, nil
}

// qsdm20FeeDust returns the transaction fee in dust. The fee must be exactly
// DustToCell of a whole number of dust (bit for bit, so -0 is refused): one
// fee has one encoding, and the signature covers the integer.
func qsdm20FeeDust(fee float64) (uint64, error) {
	if math.IsNaN(fee) || math.IsInf(fee, 0) || fee < 0 {
		return 0, qsdm20FieldError("fee must be a finite amount of CELL, not below 0")
	}
	d, err := CellToDust(fee)
	if err != nil {
		return 0, qsdm20FieldError("fee is not a whole number of dust: %v", err)
	}
	if math.Float64bits(DustToCell(d)) != math.Float64bits(fee) {
		return 0, qsdm20FieldError("fee %v is not the exact CELL value of %d dust", fee, d)
	}
	return d, nil
}

// decodeQSDM20Tx checks everything but the signature and returns the action
// and the signing digest.
func decodeQSDM20Tx(tx *mempool.Tx) (QSDM20Action, []byte, error) {
	if tx == nil {
		return QSDM20Action{}, nil, errors.New("chain: nil QSDM-20 tx")
	}
	if tx.ContractID != QSDM20ContractID {
		return QSDM20Action{}, nil, fmt.Errorf("%w: got %q, want %q", ErrNotQSDM20Tx, tx.ContractID, QSDM20ContractID)
	}
	if math.Float64bits(tx.Amount) != 0 || tx.Recipient != "" || tx.GasLimit != 0 {
		return QSDM20Action{}, nil, qsdm20FieldError("a token transaction has CELL amount 0, an empty recipient and gas limit 0")
	}
	if !validQSDM20Hex64(tx.Sender) {
		return QSDM20Action{}, nil, qsdm20FieldError("sender must be a QSDM address (64 lowercase hex characters)")
	}
	feeDust, err := qsdm20FeeDust(tx.Fee)
	if err != nil {
		return QSDM20Action{}, nil, err
	}
	a, err := DecodeQSDM20Payload(tx.Payload)
	if err != nil {
		return QSDM20Action{}, nil, err
	}
	digest := QSDM20SigningDigest(tx.Sender, tx.Nonce, feeDust, a)
	if want := QSDM20TxID(digest); tx.ID != want {
		return QSDM20Action{}, nil, qsdm20FieldError("tx id must be %s", want)
	}
	return a, digest, nil
}

// DecodeQSDM20Tx checks a token transaction's shape, payload, fee and id
// without its signature.
func DecodeQSDM20Tx(tx *mempool.Tx) (QSDM20Action, error) {
	a, _, err := decodeQSDM20Tx(tx)
	return a, err
}

// VerifyQSDM20Tx performs every stateless check of a token transaction:
// DecodeQSDM20Tx and the ML-DSA-87 signature by the sender's key. It is run
// at admission and again whenever a transaction is applied.
func VerifyQSDM20Tx(tx *mempool.Tx) (QSDM20Action, error) {
	a, digest, err := decodeQSDM20Tx(tx)
	if err != nil {
		return QSDM20Action{}, err
	}
	if err := verifyQSDM20Signature(tx.Sender, tx.PublicKey, tx.Signature, digest); err != nil {
		return QSDM20Action{}, err
	}
	return a, nil
}

// verifyQSDM20Signature is the self-certifying ML-DSA-87 check every signed
// QSDM transaction uses (recovery capsules, streams, signed transfers): the
// public key must hash (SHA-256) to the sender's address and the signature
// must verify under it. The key and signature are lowercase hex, so a signed
// transaction has one encoding.
func verifyQSDM20Signature(sender, publicKeyHex, signatureHex string, digest []byte) error {
	if !validQSDM20LowerHex(publicKeyHex) || !validQSDM20LowerHex(signatureHex) {
		return fmt.Errorf("%w: public key and signature must be lowercase hex", ErrQSDM20Signature)
	}
	publicKey, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		return fmt.Errorf("%w: public key is not valid hex", ErrQSDM20Signature)
	}
	signature, err := hex.DecodeString(signatureHex)
	if err != nil {
		return fmt.Errorf("%w: signature is not valid hex", ErrQSDM20Signature)
	}
	if len(publicKey) != mldsa87PublicKeyLen || len(signature) != mldsa87SignatureLen {
		return fmt.Errorf("%w: expected ML-DSA-87 key/signature sizes", ErrQSDM20Signature)
	}
	if BFTValidatorAddress(publicKey) != sender {
		return fmt.Errorf("%w: public key does not hash to the sender", ErrQSDM20Signature)
	}
	verifier := qcrypto.NewDilithiumVerifyOnly()
	if verifier == nil {
		return fmt.Errorf("%w: ML-DSA verifier unavailable", ErrQSDM20Signature)
	}
	defer verifier.Free()
	ok, err := verifier.VerifyWithPublicKey(digest, signature, publicKey)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrQSDM20Signature, err)
	}
	if !ok {
		return ErrQSDM20Signature
	}
	return nil
}

func validQSDM20LowerHex(s string) bool {
	if s == "" || len(s)%2 != 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// QSDM20AdmissionChecker refuses token transactions that cannot be valid in
// the next block before they take mempool space: below the activation height
// (judged at nextHeight, the next block's height; always while no height is
// set) and on any stateless check. Stateful rules (units, issuer, supply)
// are checked when the transaction is applied. Other contracts go to next.
func QSDM20AdmissionChecker(nextHeight func() (uint64, bool), next func(*mempool.Tx) error) func(*mempool.Tx) error {
	return func(tx *mempool.Tx) error {
		if tx == nil {
			return errors.New("qsdm-20 admit: nil tx")
		}
		if tx.ContractID != QSDM20ContractID {
			if next != nil {
				return next(tx)
			}
			return nil
		}
		if _, err := qsdm20HeightFrom(nextHeight); err != nil {
			return fmt.Errorf("qsdm-20 admit: %w", err)
		}
		if _, err := VerifyQSDM20Tx(tx); err != nil {
			return fmt.Errorf("qsdm-20 admit: %w", err)
		}
		return nil
	}
}
