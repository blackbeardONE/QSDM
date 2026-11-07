package qsdm

// QSDM-20 native tokens (QSDM/docs/docs/QSDM_20.md).
//
// A token action (create, transfer, mint, burn or renounce) is a transaction
// the sender signs with the same ML-DSA-87 key that holds their CELL. The
// signature covers the SHA-256 of a length-prefixed encoding of the sender,
// the nonce, the fee in dust and the action's fields (QSDM20SigningDigest),
// not JSON, so there is one thing to sign and the node checks exactly that.
// As with CELL transfers the SDK never holds a private key:
//
//  1. PrepareQSDM20 reads the sender's account nonce
//     (GET /api/v1/mining/account) and returns an unsigned QSDM20Transaction.
//  2. SignQSDM20 signs its SigningDigest with a TransferSigner.
//  3. SubmitQSDM20 posts it to POST /api/v1/qsdm20/submit.
//
// SendQSDM20 runs all three. Nodes refuse token transactions until the
// network sets an activation height: GET /api/v1/status reports it as
// consensus_auth.qsdm20_activation_height, and consensus_auth.qsdm20_active
// says whether the next block takes them.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// QSDM-20 identifiers. They mirror pkg/chain in the node.
const (
	// QSDM20ContractID is the contract_id of every token transaction.
	QSDM20ContractID = "qsdm/token/v1"
	// QSDM20TxIDPrefix starts every token transaction id; the rest is the
	// lowercase hex signing digest.
	QSDM20TxIDPrefix = "qsdm20-"

	QSDM20ActionCreate   = "create"
	QSDM20ActionTransfer = "transfer"
	QSDM20ActionMint     = "mint"
	QSDM20ActionBurn     = "burn"
	QSDM20ActionRenounce = "renounce"

	// Minting modes of a token.
	QSDM20MintNone   = "none"
	QSDM20MintIssuer = "issuer"
	QSDM20MintOpen   = "open"

	// Field rules (QSDM_20.md, "Field rules").
	QSDM20MaxSymbolLength = 12
	QSDM20MaxNameLength   = 48
	QSDM20MaxDecimals     = 18

	// QSDM20MaxUnits is 2^256 - 1, the largest amount any field may carry,
	// as the decimal string the fields use.
	QSDM20MaxUnits = "115792089237316195423570985008687907853269984665640564039457584007913129639935"

	qsdm20IDDomain = "qsdm/token/v1/id"
	qsdm20TxDomain = "qsdm/token/v1/tx"
)

// ErrQSDM20Field is wrapped by every error ValidateQSDM20Action returns.
var ErrQSDM20Field = errors.New("qsdm: invalid QSDM-20 action")

var qsdm20MaxUnits, _ = new(big.Int).SetString(QSDM20MaxUnits, 10)

// QSDM20Action is the payload of a token transaction: the action and its
// fields. Each action carries exactly the fields of its row in QSDM_20.md;
// leave the others empty. Amounts are whole base units as decimal strings
// (a token with 8 decimals counts 1.5 tokens as "150000000").
//
// The field order and JSON tags mirror pkg/chain.QSDM20Action in the node.
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

// QSDM20Decimals returns a pointer to d, for QSDM20Action.Decimals.
func QSDM20Decimals(d uint8) *uint8 { return &d }

func qsdm20FieldError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrQSDM20Field, fmt.Sprintf(format, args...))
}

// ValidateQSDM20Action applies the field rules of QSDM_20.md, the same rules
// a node applies before it looks at chain state. A node can still refuse an
// action that passes, for a reason that needs the chain: an unknown token,
// a sender who is not the issuer, too few units, or a mint past max_supply.
func ValidateQSDM20Action(a QSDM20Action) error {
	switch a.Action {
	case QSDM20ActionCreate:
		if a.Token != "" || a.To != "" || a.Amount != "" {
			return qsdm20FieldError("create carries only symbol, name, decimals, max_supply, initial_supply, mint and mint_limit")
		}
		if !validQSDM20Symbol(a.Symbol) {
			return qsdm20FieldError("symbol must be 1 to %d characters A to Z and 0 to 9", QSDM20MaxSymbolLength)
		}
		if !validQSDM20Name(a.Name) {
			return qsdm20FieldError("name must be 1 to %d characters of valid UTF-8 with no control characters", QSDM20MaxNameLength)
		}
		if a.Decimals == nil || *a.Decimals > QSDM20MaxDecimals {
			return qsdm20FieldError("decimals must be 0 to %d", QSDM20MaxDecimals)
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

func validQSDM20Symbol(s string) bool {
	if s == "" || len(s) > QSDM20MaxSymbolLength {
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
	return n <= QSDM20MaxNameLength
}

func validQSDM20Hex64(s string) bool {
	if len(s) != 64 {
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

func parseQSDM20Units(field, s string, allowZero bool) (*big.Int, error) {
	if s == "" {
		return nil, qsdm20FieldError("%s is required", field)
	}
	if len(s) > len(QSDM20MaxUnits) {
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

// writeQSDM20Field appends one length-prefixed field:
// "|" + tag + ":" + byte length of value + ":" + value.
func writeQSDM20Field(b *strings.Builder, tag, value string) {
	b.WriteString("|")
	b.WriteString(tag)
	b.WriteString(":")
	b.WriteString(strconv.Itoa(len(value)))
	b.WriteString(":")
	b.WriteString(value)
}

// QSDM20TokenID returns the id of the token that creator creates with the
// transaction at nonce. It is known before the transaction is sent.
func QSDM20TokenID(creator string, nonce uint64) string {
	var b strings.Builder
	writeQSDM20Field(&b, "domain", qsdm20IDDomain)
	writeQSDM20Field(&b, "creator", creator)
	writeQSDM20Field(&b, "nonce", strconv.FormatUint(nonce, 10))
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// QSDM20SigningPreimage returns the length-prefixed encoding whose SHA-256
// the signature covers (QSDM_20.md, "Signature"). It is exported for
// debugging and for other implementations; sign QSDM20SigningDigest.
func QSDM20SigningPreimage(sender string, nonce, feeDust uint64, a QSDM20Action) string {
	var b strings.Builder
	writeQSDM20Field(&b, "domain", qsdm20TxDomain)
	writeQSDM20Field(&b, "sender", sender)
	writeQSDM20Field(&b, "nonce", strconv.FormatUint(nonce, 10))
	writeQSDM20Field(&b, "fee_dust", strconv.FormatUint(feeDust, 10))
	writeQSDM20Field(&b, "action", a.Action)
	switch a.Action {
	case QSDM20ActionCreate:
		decimals := ""
		if a.Decimals != nil {
			decimals = strconv.FormatUint(uint64(*a.Decimals), 10)
		}
		writeQSDM20Field(&b, "symbol", a.Symbol)
		writeQSDM20Field(&b, "name", a.Name)
		writeQSDM20Field(&b, "decimals", decimals)
		writeQSDM20Field(&b, "max_supply", a.MaxSupply)
		writeQSDM20Field(&b, "initial_supply", a.InitialSupply)
		writeQSDM20Field(&b, "mint", a.Mint)
		writeQSDM20Field(&b, "mint_limit", a.MintLimit)
	case QSDM20ActionTransfer, QSDM20ActionMint:
		writeQSDM20Field(&b, "token", a.Token)
		writeQSDM20Field(&b, "to", a.To)
		writeQSDM20Field(&b, "amount", a.Amount)
	case QSDM20ActionBurn:
		writeQSDM20Field(&b, "token", a.Token)
		writeQSDM20Field(&b, "amount", a.Amount)
	case QSDM20ActionRenounce:
		writeQSDM20Field(&b, "token", a.Token)
	}
	return b.String()
}

// QSDM20SigningDigest returns the 32 bytes a token transaction's ML-DSA-87
// signature covers (FIPS 204, empty context).
func QSDM20SigningDigest(sender string, nonce, feeDust uint64, a QSDM20Action) []byte {
	sum := sha256.Sum256([]byte(QSDM20SigningPreimage(sender, nonce, feeDust, a)))
	return sum[:]
}

// QSDM20TxID returns the transaction id for a signing digest.
func QSDM20TxID(digest []byte) string {
	return QSDM20TxIDPrefix + hex.EncodeToString(digest)
}

// QSDM20Transaction is a token transaction as POST /api/v1/qsdm20/submit
// takes it. The node builds the chain transaction from these fields (the
// canonical JSON payload, the fee in CELL and the id) and checks it against
// its current state before its mempool takes it.
type QSDM20Transaction struct {
	// Sender must equal WalletAddress(public key).
	Sender string `json:"sender"`
	// Nonce is the sender's current account nonce: the nonce of
	// GET /api/v1/mining/account (PrepareQSDM20 reads it).
	Nonce uint64 `json:"nonce"`
	// FeeDust is the fee in dust (1 CELL = 100,000,000 dust), paid from the
	// sender's CELL and burned. It may be 0.
	FeeDust uint64       `json:"fee_dust"`
	Payload QSDM20Action `json:"payload"`
	// PublicKey and Signature are lowercase hex (ML-DSA-87).
	PublicKey string `json:"public_key"`
	Signature string `json:"signature"`
}

// SigningDigest returns the 32 bytes to sign.
func (t QSDM20Transaction) SigningDigest() []byte {
	return QSDM20SigningDigest(t.Sender, t.Nonce, t.FeeDust, t.Payload)
}

// TxID returns the id the node gives the transaction, for GetReceipt.
func (t QSDM20Transaction) TxID() string { return QSDM20TxID(t.SigningDigest()) }

// TokenID returns the token the transaction acts on; for a create, the id
// the new token gets.
func (t QSDM20Transaction) TokenID() string {
	if t.Payload.Action == QSDM20ActionCreate {
		return QSDM20TokenID(t.Sender, t.Nonce)
	}
	return t.Payload.Token
}

// IsSigned reports whether the transaction carries a signature and a public key.
func (t QSDM20Transaction) IsSigned() bool { return t.Signature != "" && t.PublicKey != "" }

// SignQSDM20 signs tx with signer and returns the signed copy. tx.Sender
// must be the signer's wallet address and tx.Payload must pass
// ValidateQSDM20Action.
func SignQSDM20(ctx context.Context, tx QSDM20Transaction, signer TransferSigner) (QSDM20Transaction, error) {
	if signer == nil {
		return QSDM20Transaction{}, errors.New("qsdm: a TransferSigner is required")
	}
	if err := ValidateQSDM20Action(tx.Payload); err != nil {
		return QSDM20Transaction{}, err
	}
	publicKey := signer.PublicKey()
	if len(publicKey) != MLDSA87PublicKeySize {
		return QSDM20Transaction{}, fmt.Errorf("qsdm: signer public key is %d bytes, want %d (ML-DSA-87)",
			len(publicKey), MLDSA87PublicKeySize)
	}
	if address := WalletAddress(publicKey); tx.Sender != address {
		return QSDM20Transaction{}, fmt.Errorf("qsdm: token transaction sender %q is not the signer's wallet address %s",
			tx.Sender, address)
	}
	signature, err := signer.Sign(ctx, tx.SigningDigest())
	if err != nil {
		return QSDM20Transaction{}, fmt.Errorf("qsdm: sign token transaction: %w", err)
	}
	if len(signature) != MLDSA87SignatureSize {
		return QSDM20Transaction{}, fmt.Errorf("qsdm: signer returned a %d-byte signature, want %d (ML-DSA-87)",
			len(signature), MLDSA87SignatureSize)
	}
	tx.PublicKey = hex.EncodeToString(publicKey)
	tx.Signature = hex.EncodeToString(signature)
	return tx, nil
}

// AccountInfo is the body of GET /api/v1/mining/account: the consensus CELL
// balance and nonce of an address.
type AccountInfo struct {
	Address string  `json:"address"`
	Balance float64 `json:"balance"`
	// Nonce is the account nonce a token transaction carries.
	Nonce uint64 `json:"nonce"`
	// Present is false for an address that never received CELL.
	Present bool `json:"present"`
}

// GetAccount returns the consensus CELL balance and nonce of an address
// (GET /api/v1/mining/account, public).
func (c *Client) GetAccount(ctx context.Context, address string) (*AccountInfo, error) {
	q := url.Values{}
	q.Set("address", address)
	var info AccountInfo
	if err := c.do(ctx, http.MethodGet, "/api/v1/mining/account?"+q.Encode(), nil, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// PrepareQSDM20 returns an unsigned token transaction from sender at the
// sender's current account nonce. The sender needs a CELL account (the fee,
// even 0, is charged to it), so an address that never received CELL is
// refused here rather than by the node.
func (c *Client) PrepareQSDM20(ctx context.Context, sender string, action QSDM20Action, feeDust uint64) (QSDM20Transaction, error) {
	if !walletAddressPattern.MatchString(sender) {
		return QSDM20Transaction{}, fmt.Errorf("qsdm: token transaction sender %q is not a 64-character lowercase hex wallet address", sender)
	}
	if err := ValidateQSDM20Action(action); err != nil {
		return QSDM20Transaction{}, err
	}
	account, err := c.GetAccount(ctx, sender)
	if err != nil {
		return QSDM20Transaction{}, err
	}
	if !account.Present {
		return QSDM20Transaction{}, fmt.Errorf("qsdm: %s has no CELL account yet; a token transaction's sender needs one", sender)
	}
	return QSDM20Transaction{Sender: sender, Nonce: account.Nonce, FeeDust: feeDust, Payload: action}, nil
}

// QSDM20SubmitResponse is the node's answer to an admitted token transaction.
type QSDM20SubmitResponse struct {
	TxID   string `json:"tx_id"`
	Action string `json:"action"`
	Sender string `json:"sender"`
	Nonce  uint64 `json:"nonce"`
	// Token is the token acted on; for a create, the id the new token gets.
	Token  string `json:"token"`
	Status string `json:"status"`
	// MempoolStatus is "submitted", or "duplicate" when the node already had it.
	MempoolStatus string `json:"mempool_status"`
}

// SubmitQSDM20 posts a signed token transaction
// (POST /api/v1/qsdm20/submit, public). A node refuses a transaction that
// breaks a rule with HTTP 422 (*ErrAPI), with nothing charged; that
// includes every token transaction before the activation height.
func (c *Client) SubmitQSDM20(ctx context.Context, tx QSDM20Transaction) (*QSDM20SubmitResponse, error) {
	if !tx.IsSigned() {
		return nil, errors.New("qsdm: the token transaction is not signed (SignQSDM20)")
	}
	var response QSDM20SubmitResponse
	if err := c.do(ctx, http.MethodPost, "/api/v1/qsdm20/submit", tx, &response); err != nil {
		return nil, err
	}
	if want := tx.TxID(); response.TxID != want {
		return &response, fmt.Errorf("qsdm: node answered tx_id %q for transaction %s", response.TxID, want)
	}
	return &response, nil
}

// SendQSDM20 prepares, signs and submits a token transaction from the
// signer's wallet.
func (c *Client) SendQSDM20(ctx context.Context, action QSDM20Action, feeDust uint64, signer TransferSigner) (*QSDM20SubmitResponse, error) {
	if signer == nil {
		return nil, errors.New("qsdm: a TransferSigner is required")
	}
	tx, err := c.PrepareQSDM20(ctx, WalletAddress(signer.PublicKey()), action, feeDust)
	if err != nil {
		return nil, err
	}
	signed, err := SignQSDM20(ctx, tx, signer)
	if err != nil {
		return nil, err
	}
	return c.SubmitQSDM20(ctx, signed)
}

// QSDM20Token is one token as the read routes return it. Amounts are base
// units as decimal strings; divide by 10^Decimals to show whole tokens.
type QSDM20Token struct {
	ID            string `json:"id"`
	Symbol        string `json:"symbol"`
	Name          string `json:"name"`
	Decimals      uint8  `json:"decimals"`
	Issuer        string `json:"issuer"`
	Mint          string `json:"mint"`
	MintLimit     string `json:"mint_limit,omitempty"`
	MaxSupply     string `json:"max_supply"`
	Minted        string `json:"minted"`
	Burned        string `json:"burned"`
	Supply        string `json:"supply"`
	Holders       int    `json:"holders"`
	CreatedHeight uint64 `json:"created_height"`
}

// QSDM20Holder is one holder of a token.
type QSDM20Holder struct {
	Address string `json:"address"`
	Balance string `json:"balance"`
}

// QSDM20Balance is one token an address holds.
type QSDM20Balance struct {
	Token    string `json:"token"`
	Symbol   string `json:"symbol"`
	Name     string `json:"name"`
	Decimals uint8  `json:"decimals"`
	Balance  string `json:"balance"`
}

// QSDM20TokensPage is the body of GET /api/v1/qsdm20/tokens.
type QSDM20TokensPage struct {
	StateRoot  string        `json:"state_root"`
	Tokens     []QSDM20Token `json:"tokens"`
	NextCursor string        `json:"next_cursor,omitempty"`
	HasMore    bool          `json:"has_more"`
}

// QSDM20TokenInfo is the body of GET /api/v1/qsdm20/tokens/{id}.
type QSDM20TokenInfo struct {
	StateRoot string      `json:"state_root"`
	Token     QSDM20Token `json:"token"`
}

// QSDM20HoldersPage is the body of GET /api/v1/qsdm20/tokens/{id}/holders.
type QSDM20HoldersPage struct {
	StateRoot  string         `json:"state_root"`
	Token      string         `json:"token"`
	Holders    []QSDM20Holder `json:"holders"`
	NextCursor string         `json:"next_cursor,omitempty"`
	HasMore    bool           `json:"has_more"`
}

// QSDM20Balances is the body of GET /api/v1/qsdm20/balances/{address}.
type QSDM20Balances struct {
	StateRoot string          `json:"state_root"`
	Address   string          `json:"address"`
	Balances  []QSDM20Balance `json:"balances"`
}

func qsdm20PageQuery(cursor string, limit int) string {
	q := url.Values{}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

// GetQSDM20Tokens returns tokens, newest first (GET /api/v1/qsdm20/tokens,
// public). limit 0 returns every remaining token; with a limit, pass the
// page's NextCursor as cursor to read the next page.
func (c *Client) GetQSDM20Tokens(ctx context.Context, cursor string, limit int) (*QSDM20TokensPage, error) {
	var page QSDM20TokensPage
	if err := c.do(ctx, http.MethodGet, "/api/v1/qsdm20/tokens"+qsdm20PageQuery(cursor, limit), nil, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// GetQSDM20Token returns one token (GET /api/v1/qsdm20/tokens/{id}, public).
// An unknown id is an *ErrAPI with IsNotFound.
func (c *Client) GetQSDM20Token(ctx context.Context, id string) (*QSDM20TokenInfo, error) {
	var info QSDM20TokenInfo
	if err := c.do(ctx, http.MethodGet, "/api/v1/qsdm20/tokens/"+url.PathEscape(id), nil, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// GetQSDM20Holders returns a token's holders, largest balance first
// (GET /api/v1/qsdm20/tokens/{id}/holders, public), paged like
// GetQSDM20Tokens.
func (c *Client) GetQSDM20Holders(ctx context.Context, id, cursor string, limit int) (*QSDM20HoldersPage, error) {
	var page QSDM20HoldersPage
	path := "/api/v1/qsdm20/tokens/" + url.PathEscape(id) + "/holders" + qsdm20PageQuery(cursor, limit)
	if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// GetQSDM20Balances returns every token an address holds
// (GET /api/v1/qsdm20/balances/{address}, public).
func (c *Client) GetQSDM20Balances(ctx context.Context, address string) (*QSDM20Balances, error) {
	var balances QSDM20Balances
	if err := c.do(ctx, http.MethodGet, "/api/v1/qsdm20/balances/"+url.PathEscape(address), nil, &balances); err != nil {
		return nil, err
	}
	return &balances, nil
}
