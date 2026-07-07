package enrollment

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

var (
	ErrSignedEnvelopeRequired = errors.New("enrollment: signed v2 envelope required")
	ErrSenderMismatch         = errors.New("enrollment: sender does not match public key")
	ErrSignatureInvalid       = errors.New("enrollment: ML-DSA signature is invalid")
	ErrLegacyContractDisabled = errors.New("enrollment: unsigned v1 submissions are disabled")
)

// SignedEnvelope is the canonical qsdm/enroll/v2 wire and signing shape.
// Field order is part of the signing contract: clients and validators clear
// Signature/PublicKey and json.Marshal this exact struct before signing.
type SignedEnvelope struct {
	ID         string  `json:"id"`
	Sender     string  `json:"sender"`
	Nonce      uint64  `json:"nonce"`
	Fee        float64 `json:"fee"`
	GasLimit   int64   `json:"gas_limit,omitempty"`
	ContractID string  `json:"contract_id"`
	PayloadB64 string  `json:"payload_b64"`
	Signature  string  `json:"signature"`
	PublicKey  string  `json:"public_key,omitempty"`
}

func (e SignedEnvelope) CanonicalBytes() ([]byte, error) {
	e.Signature = ""
	e.PublicKey = ""
	return json.Marshal(e)
}

func (e SignedEnvelope) ToTransaction() (*mempool.Tx, error) {
	payload, err := base64.StdEncoding.DecodeString(e.PayloadB64)
	if err != nil {
		return nil, fmt.Errorf("payload_b64 not valid base64: %w", err)
	}
	return &mempool.Tx{
		ID:         e.ID,
		Sender:     e.Sender,
		Nonce:      e.Nonce,
		Fee:        e.Fee,
		GasLimit:   e.GasLimit,
		ContractID: e.ContractID,
		Payload:    payload,
		Signature:  e.Signature,
		PublicKey:  e.PublicKey,
	}, nil
}

func EnvelopeFromTransaction(tx *mempool.Tx) (SignedEnvelope, error) {
	if tx == nil {
		return SignedEnvelope{}, errors.New("enrollment: nil transaction")
	}
	return SignedEnvelope{
		ID:         tx.ID,
		Sender:     tx.Sender,
		Nonce:      tx.Nonce,
		Fee:        tx.Fee,
		GasLimit:   tx.GasLimit,
		ContractID: tx.ContractID,
		PayloadB64: base64.StdEncoding.EncodeToString(tx.Payload),
		Signature:  tx.Signature,
		PublicKey:  tx.PublicKey,
	}, nil
}

func VerifySignedTransaction(tx *mempool.Tx) error {
	env, err := EnvelopeFromTransaction(tx)
	if err != nil {
		return err
	}
	return VerifySignedEnvelope(env)
}

func VerifySignedEnvelope(env SignedEnvelope) error {
	if env.ContractID != SignedContractID {
		return fmt.Errorf("%w: contract_id must be %q", ErrSignedEnvelopeRequired, SignedContractID)
	}
	if strings.TrimSpace(env.ID) == "" || strings.TrimSpace(env.PayloadB64) == "" {
		return fmt.Errorf("%w: id and payload_b64 are required", ErrSignedEnvelopeRequired)
	}
	if env.Sender == "" || env.Sender != strings.ToLower(strings.TrimSpace(env.Sender)) {
		return fmt.Errorf("%w: sender must be canonical lowercase hex", ErrSenderMismatch)
	}
	pub, err := hex.DecodeString(env.PublicKey)
	if err != nil || len(pub) != mldsa87.PublicKeySize {
		return fmt.Errorf("%w: public_key must be %d-byte hex", ErrSignedEnvelopeRequired, mldsa87.PublicKeySize)
	}
	sum := sha256.Sum256(pub)
	if env.Sender != hex.EncodeToString(sum[:]) {
		return ErrSenderMismatch
	}
	sig, err := hex.DecodeString(env.Signature)
	if err != nil || len(sig) != mldsa87.SignatureSize {
		return fmt.Errorf("%w: signature must be %d-byte hex", ErrSignatureInvalid, mldsa87.SignatureSize)
	}
	canonical, err := env.CanonicalBytes()
	if err != nil {
		return fmt.Errorf("enrollment: canonicalize envelope: %w", err)
	}
	var pk mldsa87.PublicKey
	if err := pk.UnmarshalBinary(pub); err != nil {
		return fmt.Errorf("%w: malformed public_key", ErrSignedEnvelopeRequired)
	}
	if !mldsa87.Verify(&pk, canonical, nil, sig) {
		return ErrSignatureInvalid
	}
	return nil
}
