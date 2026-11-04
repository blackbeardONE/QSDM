package qsdm

// Signed CELL transfers.
//
// Public QSDM nodes accept a CELL transfer only as a self-custody envelope
// signed with ML-DSA-87 and posted to POST /api/v1/wallet/submit-signed
// (pkg/api.SubmitSignedTransaction in the node). Since block 816,600 the
// envelope must also name Proof-of-Entanglement (PoE) parents: two to ten
// transactions committed in the last 8,640 blocks. A transfer without valid
// parents is refused with HTTP 422.
//
// This SDK never holds a private key. A transfer is built in three steps:
//
//  1. PrepareTransfer fills the nonce (GET /api/v1/wallet/nonce) and the PoE
//     parents (GET /api/v1/chain/parents) into an unsigned TransferEnvelope.
//  2. A signer signs TransferEnvelope.SigningBytes: SignTransfer with a
//     caller-supplied TransferSigner, or an external tool such as
//     `qsdmcli wallet sign-tx`.
//  3. SubmitSignedTransfer posts the signed envelope.
//
// SendTransfer runs all three steps with a TransferSigner.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Proof-of-Entanglement limits. They mirror pkg/poe in the node.
const (
	// PoEMinParents is the minimum number of parents a transfer names.
	PoEMinParents = 2
	// PoEMaxParents is the maximum number of parents a transfer names.
	PoEMaxParents = 10
	// PoEParentWindowBlocks is how far back a parent may be: it must be
	// committed in the last 8,640 blocks (about 24 hours at the 10-second
	// block target). Sign shortly before submitting.
	PoEParentWindowBlocks = 8640
)

// ML-DSA-87 sizes (FIPS 204). The node refuses other sizes.
const (
	MLDSA87PublicKeySize = 2592
	MLDSA87SignatureSize = 4627
)

// Receipt statuses reported by GET /api/v1/receipts.
const (
	ReceiptStatusFailed  = 0
	ReceiptStatusSuccess = 1
)

var (
	// ErrUnsignedSendRemoved is returned by the deprecated SendTransaction.
	// It is returned without contacting the node.
	ErrUnsignedSendRemoved = errors.New("qsdm: SendTransaction is no longer supported: " +
		"QSDM nodes accept CELL transfers from clients only as ML-DSA-87 signed envelopes " +
		"with Proof-of-Entanglement parents (POST /api/v1/wallet/submit-signed); " +
		"use SendTransfer with a TransferSigner, or PrepareTransfer, sign, then SubmitSignedTransfer. " +
		"POST /api/v1/wallet/send is an operator-only route that spends from the node's own wallet")

	// ErrNoPoEParents is returned by GetPoEParents when the node offered no
	// usable parents.
	ErrNoPoEParents = errors.New("qsdm: node offered no usable Proof-of-Entanglement parents")
)

var (
	walletAddressPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	parentIDPattern      = regexp.MustCompile(`^[0-9A-Za-z_-]{16,128}$`)
)

// TransferEnvelope is a CELL transfer as POST /api/v1/wallet/submit-signed
// accepts it.
//
// The field order and JSON tags mirror pkg/wallet.TransactionData in the
// node. They are part of the signature contract: the node decodes the
// envelope, clears Signature and PublicKey, re-encodes it with Go's
// encoding/json and verifies the signature over those bytes. Do not reorder
// the fields.
type TransferEnvelope struct {
	// ID is chosen by the client. PrepareTransfer generates one in the
	// shape every PoE parent must have: 16-128 characters of [0-9A-Za-z_-].
	ID string `json:"id"`
	// Sender must equal WalletAddress(PublicKey).
	Sender string `json:"sender"`
	// Recipient is a 64-character lowercase hex wallet address.
	Recipient string `json:"recipient"`
	// Amount and Fee are CELL (8 decimals). Amount must be positive and
	// Fee zero or more.
	Amount float64 `json:"amount"`
	Fee    float64 `json:"fee"`
	// GeoTag is optional.
	GeoTag string `json:"geotag"`
	// ParentCells are the Proof-of-Entanglement parents. They are signed.
	ParentCells []string `json:"parent_cells"`
	// Nonce must be the `next` value of GET /api/v1/wallet/nonce. A
	// validator refuses nonce 0.
	Nonce uint64 `json:"nonce,omitempty"`
	// Signature is the hex ML-DSA-87 signature over SigningBytes.
	Signature string `json:"signature"`
	// PublicKey is the hex ML-DSA-87 public key. It is not signed.
	PublicKey string `json:"public_key,omitempty"`
	// Timestamp is RFC 3339.
	Timestamp string `json:"timestamp"`
}

// SigningBytes returns the exact bytes the ML-DSA-87 signature covers: the
// envelope encoded with Signature set to "" and PublicKey removed. This is
// byte for byte what the node verifies (pkg/wallet.TransactionData's
// CanonicalBytes).
//
// It refuses strings that are not valid UTF-8: encoding/json would replace
// the bad bytes, the node would re-encode the replacement differently, and
// the signature would not verify.
func (e TransferEnvelope) SigningBytes() ([]byte, error) {
	e.Signature = ""
	e.PublicKey = ""
	fields := []struct{ name, value string }{
		{"id", e.ID}, {"sender", e.Sender}, {"recipient", e.Recipient},
		{"geotag", e.GeoTag}, {"timestamp", e.Timestamp},
	}
	for _, field := range fields {
		if !utf8.ValidString(field.value) {
			return nil, fmt.Errorf("qsdm: transfer %s is not valid UTF-8", field.name)
		}
	}
	for i, parent := range e.ParentCells {
		if !utf8.ValidString(parent) {
			return nil, fmt.Errorf("qsdm: transfer parent %d is not valid UTF-8", i)
		}
	}
	return json.Marshal(e)
}

// IsSigned reports whether the envelope carries a signature and a public key.
func (e TransferEnvelope) IsSigned() bool {
	return e.Signature != "" && e.PublicKey != ""
}

// TransferRequest describes a transfer for PrepareTransfer and SendTransfer.
type TransferRequest struct {
	// Sender is the paying wallet address. SendTransfer derives it from the
	// signer's public key when it is empty.
	Sender    string
	Recipient string
	// Amount and Fee are CELL.
	Amount float64
	Fee    float64
	GeoTag string

	// Optional overrides. PrepareTransfer fills each one that is empty:
	// ID with a random ID, Timestamp with the current UTC time, Nonce with
	// `next` from GET /api/v1/wallet/nonce, and ParentCells with
	// GetPoEParents. A non-nil, empty ParentCells is kept as it is (only
	// for nodes that do not enforce Proof-of-Entanglement).
	ID          string
	Timestamp   string
	Nonce       uint64
	ParentCells []string
}

// SubmitTransferResponse is the body of a successful
// POST /api/v1/wallet/submit-signed.
type SubmitTransferResponse struct {
	TransactionID string `json:"transaction_id"`
	// Status is "pending" (HTTP 202: queued for the next block, the usual
	// answer of a validator) or "accepted" (HTTP 200: applied by a node that
	// settles transfers itself).
	Status string `json:"status"`
	// Broadcast is "block-pending", "p2p" or "local-only".
	Broadcast string `json:"broadcast,omitempty"`
	// HTTPStatus is the response status code.
	HTTPStatus int `json:"-"`
}

// ChainParent is one committed transaction a transfer may name as a parent.
type ChainParent struct {
	ID     string `json:"id"`
	Height uint64 `json:"height"`
}

// ChainParentsResponse is the body of GET /api/v1/chain/parents.
type ChainParentsResponse struct {
	// Tip is the durable chain height the parents were read at.
	Tip                 uint64 `json:"tip"`
	PoEActivationHeight uint64 `json:"poe_activation_height"`
	PoEActive           bool   `json:"poe_active"`
	MinParents          int    `json:"min_parents"`
	MaxParents          int    `json:"max_parents"`
	WindowBlocks        int    `json:"window_blocks"`
	// Parents is the value to copy into TransferEnvelope.ParentCells.
	Parents []string `json:"parents"`
	// Recent lists recent committed transactions, newest first.
	Recent []ChainParent `json:"recent"`
}

// ReceiptLog is one log entry of a receipt.
type ReceiptLog struct {
	Topic string                 `json:"topic"`
	Data  map[string]interface{} `json:"data,omitempty"`
	Index int                    `json:"index"`
}

// Receipt is the outcome of one committed transaction
// (GET /api/v1/receipts/{tx_id}).
type Receipt struct {
	TxID        string  `json:"tx_id"`
	BlockHeight uint64  `json:"block_height"`
	BlockHash   string  `json:"block_hash,omitempty"`
	Status      uint8   `json:"status"`
	GasUsed     int64   `json:"gas_used"`
	Fee         float64 `json:"fee"`
	// Logs and Error explain the outcome.
	Logs         []ReceiptLog `json:"logs,omitempty"`
	Error        string       `json:"error,omitempty"`
	Timestamp    string       `json:"timestamp"`
	ContractID   string       `json:"contract_id,omitempty"`
	IndexInBlock int          `json:"index_in_block"`
}

// Succeeded reports whether the transaction was applied.
func (r *Receipt) Succeeded() bool { return r != nil && r.Status == ReceiptStatusSuccess }

// ReceiptsResponse is the body of GET /api/v1/receipts.
type ReceiptsResponse struct {
	Tip      uint64    `json:"tip"`
	From     uint64    `json:"from"`
	To       uint64    `json:"to"`
	Limit    int       `json:"limit"`
	Receipts []Receipt `json:"receipts"`
}

// ReceiptsQuery selects a height range for GetReceipts. A zero field lets
// the node choose: To defaults to the tip, Limit to 20 (the node caps it at
// 200), and From to To-Limit+1.
type ReceiptsQuery struct {
	From  uint64
	To    uint64
	Limit int
}

// TransferSigner signs transfers with an ML-DSA-87 key that the caller
// holds. The SDK never sees the private key.
//
// Sign must return an ML-DSA-87 (FIPS 204) signature over message with an
// empty context string: the node and every validator verify it with
// mldsa87.Verify(publicKey, message, nil, signature) from
// github.com/cloudflare/circl. The README shows a signer built on that
// package; a hardware module, a remote signer or `qsdmcli` can implement
// the interface too.
type TransferSigner interface {
	// PublicKey returns the raw ML-DSA-87 public key (2,592 bytes).
	PublicKey() []byte
	// Sign returns the raw ML-DSA-87 signature (4,627 bytes) over message.
	Sign(ctx context.Context, message []byte) ([]byte, error)
}

// WalletAddress returns the QSDM wallet address of an ML-DSA-87 public key:
// lowercase hex of its SHA-256 digest.
func WalletAddress(publicKey []byte) string {
	sum := sha256.Sum256(publicKey)
	return hex.EncodeToString(sum[:])
}

// SignTransfer signs env with signer and returns the signed copy. The
// envelope's Sender must be the signer's wallet address.
func SignTransfer(ctx context.Context, env TransferEnvelope, signer TransferSigner) (TransferEnvelope, error) {
	if signer == nil {
		return TransferEnvelope{}, errors.New("qsdm: a TransferSigner is required")
	}
	publicKey := signer.PublicKey()
	if len(publicKey) != MLDSA87PublicKeySize {
		return TransferEnvelope{}, fmt.Errorf("qsdm: signer public key is %d bytes, want %d (ML-DSA-87)",
			len(publicKey), MLDSA87PublicKeySize)
	}
	if address := WalletAddress(publicKey); env.Sender != address {
		return TransferEnvelope{}, fmt.Errorf("qsdm: transfer sender %q is not the signer's wallet address %s",
			env.Sender, address)
	}
	message, err := env.SigningBytes()
	if err != nil {
		return TransferEnvelope{}, err
	}
	signature, err := signer.Sign(ctx, message)
	if err != nil {
		return TransferEnvelope{}, fmt.Errorf("qsdm: sign transfer: %w", err)
	}
	if len(signature) != MLDSA87SignatureSize {
		return TransferEnvelope{}, fmt.Errorf("qsdm: signer returned a %d-byte signature, want %d (ML-DSA-87)",
			len(signature), MLDSA87SignatureSize)
	}
	env.Signature = hex.EncodeToString(signature)
	env.PublicKey = hex.EncodeToString(publicKey)
	return env, nil
}

// GetChainParents returns GET /api/v1/chain/parents, the node's
// Proof-of-Entanglement parent source. limit sets the length of Recent
// (0 = the node's default of 8; the node allows at most 32). The node
// answers 503 until it has two committed transactions.
func (c *Client) GetChainParents(ctx context.Context, limit int) (*ChainParentsResponse, error) {
	path := "/api/v1/chain/parents"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var response ChainParentsResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// GetPoEParents returns the parents to put in a new transfer: the node's
// two newest committed transaction IDs. It follows the rule qsdmcli
// (--auto-parents) and QSDM Hive use: ask GET /api/v1/chain/parents, and
// only if that route is missing (HTTP 404, 405 or 501 on a node released
// before it) fall back to the newest successful receipts of
// GET /api/v1/receipts. Any other error is returned as is.
func (c *Client) GetPoEParents(ctx context.Context) ([]string, error) {
	response, err := c.GetChainParents(ctx, 0)
	if err == nil {
		return pickPoEParents(response.Parents)
	}
	var apiErr *ErrAPI
	if !errors.As(err, &apiErr) ||
		(apiErr.StatusCode != http.StatusNotFound &&
			apiErr.StatusCode != http.StatusMethodNotAllowed &&
			apiErr.StatusCode != http.StatusNotImplemented) {
		return nil, err
	}
	receipts, rerr := c.GetReceipts(ctx, ReceiptsQuery{Limit: 32})
	if rerr != nil {
		return nil, fmt.Errorf("qsdm: parents route unavailable (%v) and receipts fallback failed: %w", err, rerr)
	}
	ids := make([]string, 0, len(receipts.Receipts))
	for _, receipt := range receipts.Receipts {
		if receipt.Status == ReceiptStatusSuccess {
			ids = append(ids, receipt.TxID)
		}
	}
	return pickPoEParents(ids)
}

// pickPoEParents keeps the first PoEMinParents distinct, well-formed IDs.
func pickPoEParents(ids []string) ([]string, error) {
	out := make([]string, 0, PoEMinParents)
	seen := make(map[string]struct{}, PoEMinParents)
	for _, id := range ids {
		if !parentIDPattern.MatchString(id) {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
		if len(out) == PoEMinParents {
			return out, nil
		}
	}
	return nil, ErrNoPoEParents
}

// GetReceipt returns the receipt of one committed transaction
// (GET /api/v1/receipts/{tx_id}). Use it to follow a submitted transfer:
// the node answers 404 (see IsNotFound) until the transfer is in a block.
func (c *Client) GetReceipt(ctx context.Context, txID string) (*Receipt, error) {
	var receipt Receipt
	if err := c.do(ctx, http.MethodGet, "/api/v1/receipts/"+url.PathEscape(txID), nil, &receipt); err != nil {
		return nil, err
	}
	return &receipt, nil
}

// GetReceipts returns recent receipts, newest first (GET /api/v1/receipts).
func (c *Client) GetReceipts(ctx context.Context, query ReceiptsQuery) (*ReceiptsResponse, error) {
	values := url.Values{}
	if query.From > 0 {
		values.Set("from", strconv.FormatUint(query.From, 10))
	}
	if query.To > 0 {
		values.Set("to", strconv.FormatUint(query.To, 10))
	}
	if query.Limit > 0 {
		values.Set("limit", strconv.Itoa(query.Limit))
	}
	path := "/api/v1/receipts"
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var response ReceiptsResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// PrepareTransfer returns an unsigned envelope for req, ready for
// SignTransfer or an external signer. It reads the sender's next nonce and
// the PoE parents from the node unless req supplies them.
//
// Parents expire: they must be committed in the last 8,640 blocks when the
// transfer is committed, so prepare, sign and submit together.
func (c *Client) PrepareTransfer(ctx context.Context, req TransferRequest) (TransferEnvelope, error) {
	if !walletAddressPattern.MatchString(req.Sender) {
		return TransferEnvelope{}, fmt.Errorf("qsdm: transfer sender %q must be a 64-character lowercase hex wallet address", req.Sender)
	}
	if !walletAddressPattern.MatchString(req.Recipient) {
		return TransferEnvelope{}, fmt.Errorf("qsdm: transfer recipient %q must be a 64-character lowercase hex wallet address", req.Recipient)
	}
	if math.IsNaN(req.Amount) || math.IsInf(req.Amount, 0) || req.Amount <= 0 {
		return TransferEnvelope{}, fmt.Errorf("qsdm: transfer amount must be a positive number of CELL, got %v", req.Amount)
	}
	if math.IsNaN(req.Fee) || math.IsInf(req.Fee, 0) || req.Fee < 0 {
		return TransferEnvelope{}, fmt.Errorf("qsdm: transfer fee must be zero or a positive number of CELL, got %v", req.Fee)
	}
	if !hasAtMost8Decimals(req.Amount) || !hasAtMost8Decimals(req.Fee) {
		// The chain moves whole dust (1 dust = 0.00000001 CELL) and drops
		// any further digits.
		return TransferEnvelope{}, fmt.Errorf("qsdm: transfer amount %v and fee %v must have at most 8 decimal places", req.Amount, req.Fee)
	}

	env := TransferEnvelope{
		ID:          req.ID,
		Sender:      req.Sender,
		Recipient:   req.Recipient,
		Amount:      req.Amount,
		Fee:         req.Fee,
		GeoTag:      req.GeoTag,
		ParentCells: req.ParentCells,
		Nonce:       req.Nonce,
		Timestamp:   req.Timestamp,
	}
	if env.ID == "" {
		id, err := newTransferID()
		if err != nil {
			return TransferEnvelope{}, err
		}
		env.ID = id
	}
	if env.Timestamp == "" {
		env.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if env.Nonce == 0 {
		nonce, err := c.GetWalletNonceContext(ctx, env.Sender)
		if err != nil {
			return TransferEnvelope{}, fmt.Errorf("qsdm: read wallet nonce: %w", err)
		}
		// Fail closed on a node that answers for another wallet or
		// with an inconsistent pair, as qsdmcli does.
		if nonce.Sender != env.Sender || nonce.Next != nonce.Nonce+1 {
			return TransferEnvelope{}, fmt.Errorf("qsdm: node returned an inconsistent nonce (sender %q, nonce %d, next %d)",
				nonce.Sender, nonce.Nonce, nonce.Next)
		}
		env.Nonce = nonce.Next
	}
	if env.ParentCells == nil {
		parents, err := c.GetPoEParents(ctx)
		if err != nil {
			return TransferEnvelope{}, fmt.Errorf("qsdm: read Proof-of-Entanglement parents: %w", err)
		}
		env.ParentCells = parents
	}
	return env, nil
}

// SubmitSignedTransfer posts a signed envelope to
// POST /api/v1/wallet/submit-signed. The route is public: the signature is
// the authorization. A refusal is returned as a *TransferError; see
// TransferRejection for the reasons and IsPoERejection for stale or
// missing parents.
func (c *Client) SubmitSignedTransfer(ctx context.Context, env TransferEnvelope) (*SubmitTransferResponse, error) {
	if !env.IsSigned() {
		return nil, errors.New("qsdm: the transfer envelope is not signed (signature and public_key are required)")
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/api/v1/wallet/submit-signed", env)
	if err != nil {
		return nil, err
	}
	resp, body, err := c.sendRaw(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, newTransferError(&ErrAPI{StatusCode: resp.StatusCode, Body: string(body), URL: req.URL.String()})
	}
	var out SubmitTransferResponse
	if len(body) > 0 {
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("qsdm: decode %s: %w", req.URL.String(), err)
		}
	}
	out.HTTPStatus = resp.StatusCode
	return &out, nil
}

// SendTransfer prepares, signs and submits one transfer. When req.Sender is
// empty it is derived from the signer's public key.
//
// It makes one attempt. If the node refuses with a stale-parents
// (IsPoERejection) or a nonce conflict (TransferError.Reason
// RejectNonce or RejectDuplicate), call SendTransfer again: every call
// reads a fresh nonce and fresh parents. A sender can have one transfer
// waiting per block, so after a conflict wait for the next block (about 10
// seconds).
func (c *Client) SendTransfer(ctx context.Context, req TransferRequest, signer TransferSigner) (*SubmitTransferResponse, error) {
	if signer == nil {
		return nil, errors.New("qsdm: a TransferSigner is required")
	}
	publicKey := signer.PublicKey()
	if len(publicKey) != MLDSA87PublicKeySize {
		return nil, fmt.Errorf("qsdm: signer public key is %d bytes, want %d (ML-DSA-87)",
			len(publicKey), MLDSA87PublicKeySize)
	}
	if req.Sender == "" {
		req.Sender = WalletAddress(publicKey)
	}
	env, err := c.PrepareTransfer(ctx, req)
	if err != nil {
		return nil, err
	}
	signed, err := SignTransfer(ctx, env, signer)
	if err != nil {
		return nil, err
	}
	return c.SubmitSignedTransfer(ctx, signed)
}

// TransferRejection says why a node refused a signed transfer.
type TransferRejection string

const (
	// RejectInvalid (HTTP 400): the envelope failed validation, for example
	// a bad address, nonce 0, or a sender that is not
	// hex(sha256(public_key)).
	RejectInvalid TransferRejection = "invalid"
	// RejectUnauthorized (HTTP 401/403): this node does not expose the route
	// publicly.
	RejectUnauthorized TransferRejection = "unauthorized"
	// RejectInsufficientBalance (HTTP 402): balance below amount + fee.
	RejectInsufficientBalance TransferRejection = "insufficient_balance"
	// RejectDuplicate (HTTP 409, status "duplicate"): the node already has a
	// transfer with this ID, or another transfer with this sender nonce is
	// waiting for the next block. It does not prove that this envelope was
	// accepted; follow it with GetReceipt.
	RejectDuplicate TransferRejection = "duplicate"
	// RejectNonce (HTTP 409): nonce replay, gap or race. Read the nonce
	// again and sign again.
	RejectNonce TransferRejection = "nonce"
	// RejectSignature (HTTP 422): the signature does not verify under
	// public_key over SigningBytes.
	RejectSignature TransferRejection = "signature"
	// RejectPoEParents (HTTP 422, "proof-of-entanglement: ..."): parents
	// are missing, malformed, or not committed in the reference window.
	// Fetch fresh parents and sign again at the same nonce.
	RejectPoEParents TransferRejection = "poe_parents"
	// RejectPolicy (HTTP 422, other): a node routing policy refused it.
	RejectPolicy TransferRejection = "policy"
	// RejectRateLimited (HTTP 429): too many submissions from this address.
	RejectRateLimited TransferRejection = "rate_limited"
	// RejectUnavailable (HTTP 501/503): the node cannot take transfers now
	// (queue full, read-only follower, writes paused, or no settlement).
	RejectUnavailable TransferRejection = "unavailable"
	// RejectOther covers any other status.
	RejectOther TransferRejection = "other"
)

// TransferError is a node's refusal of a signed transfer. It wraps the
// *ErrAPI, so IsNotFound and IsUnauthorized keep working.
type TransferError struct {
	Reason TransferRejection
	// Message is the node's explanation.
	Message string
	// TransactionID is set when the node reports a duplicate.
	TransactionID string
	API           *ErrAPI
}

func (e *TransferError) Error() string {
	text := "qsdm: transfer refused"
	if e.API != nil {
		text += fmt.Sprintf(" by %s (HTTP %d, %s)", e.API.URL, e.API.StatusCode, e.Reason)
	} else {
		text += fmt.Sprintf(" (%s)", e.Reason)
	}
	if e.Message != "" {
		text += ": " + truncate(e.Message, 256)
	}
	if hint := transferRejectionHint(e.Reason); hint != "" {
		text += " -- " + hint
	}
	return text
}

// Unwrap returns the underlying *ErrAPI.
func (e *TransferError) Unwrap() error {
	if e.API == nil {
		return nil
	}
	return e.API
}

// IsPoERejection reports whether err is a refusal because of missing,
// malformed or stale Proof-of-Entanglement parents (HTTP 422). Prepare the
// transfer again (fresh parents) and sign it again.
func IsPoERejection(err error) bool {
	var te *TransferError
	return errors.As(err, &te) && te.Reason == RejectPoEParents
}

func transferRejectionHint(reason TransferRejection) string {
	switch reason {
	case RejectPoEParents:
		return "fetch fresh parents (GetPoEParents, GET /api/v1/chain/parents) and sign the transfer again at the same nonce"
	case RejectSignature:
		return "sign SigningBytes() of exactly the envelope you submit, with ML-DSA-87 and an empty context"
	case RejectNonce:
		return "read GET /api/v1/wallet/nonce again and sign with its next value"
	case RejectDuplicate:
		return "wait for the next block, then follow the transfer with GetReceipt or prepare a new one"
	case RejectInsufficientBalance:
		return "the sender's CELL balance is below amount + fee"
	case RejectUnavailable:
		return "the node is not accepting transfers right now; retry later"
	case RejectRateLimited:
		return "wait before submitting again"
	case RejectUnauthorized:
		return "this node does not serve the public submit route"
	}
	return ""
}

// newTransferError classifies a non-2xx answer of
// POST /api/v1/wallet/submit-signed (pkg/api SubmitSignedTransaction).
func newTransferError(apiErr *ErrAPI) *TransferError {
	te := &TransferError{API: apiErr, Message: strings.TrimSpace(apiErr.Body)}
	// Two JSON shapes: errors are {"error","message","status":<code>}, a
	// duplicate is {"transaction_id","status":"duplicate","broadcast"}.
	var body struct {
		Message       string      `json:"message"`
		Status        interface{} `json:"status"`
		TransactionID string      `json:"transaction_id"`
	}
	if json.Unmarshal([]byte(apiErr.Body), &body) == nil {
		if body.Message != "" {
			te.Message = body.Message
		}
		te.TransactionID = body.TransactionID
	}
	duplicate := body.Status == "duplicate"
	lower := strings.ToLower(te.Message)
	switch code := apiErr.StatusCode; {
	case code == http.StatusBadRequest:
		te.Reason = RejectInvalid
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		te.Reason = RejectUnauthorized
	case code == http.StatusPaymentRequired:
		te.Reason = RejectInsufficientBalance
	case code == http.StatusConflict && duplicate:
		te.Reason = RejectDuplicate
		te.Message = "the node already has a transfer with this ID, or another transfer with this sender nonce is waiting for the next block"
	case code == http.StatusConflict:
		te.Reason = RejectNonce
	case code == http.StatusUnprocessableEntity && strings.Contains(lower, "proof-of-entanglement"):
		te.Reason = RejectPoEParents
	case code == http.StatusUnprocessableEntity && strings.Contains(lower, "signature"):
		te.Reason = RejectSignature
	case code == http.StatusUnprocessableEntity:
		te.Reason = RejectPolicy
	case code == http.StatusTooManyRequests:
		te.Reason = RejectRateLimited
	case code == http.StatusNotImplemented || code == http.StatusServiceUnavailable:
		te.Reason = RejectUnavailable
	default:
		te.Reason = RejectOther
	}
	return te
}

// hasAtMost8Decimals reports whether v, written with 8 decimal places,
// reads back as the same float64.
func hasAtMost8Decimals(v float64) bool {
	rounded, err := strconv.ParseFloat(strconv.FormatFloat(v, 'f', 8, 64), 64)
	return err == nil && rounded == v
}

func newTransferID() (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("qsdm: generate transfer id: %w", err)
	}
	return fmt.Sprintf("sdk_go_%d_%s", time.Now().UnixMilli(), hex.EncodeToString(random[:])), nil
}
