// Package pulseclient is the miner and wallet side of PULSE, the
// non-custodial mining pool (protocol: qsdm-mining-bootstrap PROTOCOL.md).
//
// A member joins a pool with one signed transaction on the chain. From then on
// the producer pays the member's mining rewards into a keyless escrow, and the
// pool's signed settlement moves them to every member by share window through
// the chain itself. The pool never holds CELL. A member can leave at any time
// and can reclaim an escrowed reward the pool never settled once the chain's
// reclaim timeout has passed.
//
// This package builds and signs the member's transactions (join, leave,
// reclaim), the pool login signature, and verifies the pool's share receipts.
// The byte formats are pinned by vectors shared with the node and the pool.
package pulseclient

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// Signature domains. Each kind of PULSE signature has its own, so a signature
// for one can never be replayed as another.
const (
	TxDomain       = "QSDM/pulse/tx/v1\x00"
	RegisterDomain = "QSDM/pulse/register/v1\x00"
	ReceiptDomain  = "QSDM/pulse/receipt/v1\x00"
)

// MaxReclaimProofs bounds one reclaim transaction (the node refuses payloads
// over 64 KiB; proof IDs are short).
const MaxReclaimProofs = 256

// Action is the payload of a member's PULSE transaction. Settle is the pool's
// own transaction and is not built here.
type Action struct {
	Op     string   `json:"op"`               // join | leave | reclaim
	Pool   string   `json:"pool,omitempty"`   // join: the pool ID (64 hex)
	Proofs []string `json:"proofs,omitempty"` // reclaim: escrowed proof IDs
}

// Envelope is a signed PULSE transaction as the node's
// POST /api/v1/pulse/submit-signed takes it.
type Envelope struct {
	Sender    string          `json:"sender"`
	Nonce     uint64          `json:"nonce"`
	Fee       float64         `json:"fee"`
	Payload   json.RawMessage `json:"payload"`
	PublicKey string          `json:"public_key"`
	Signature string          `json:"signature"`
}

// TxSigningBytes is what a PULSE transaction's sender signs: the domain, then
// a canonical JSON of sender, nonce, fee and payload. It equals the node's
// chain.PulseTxSigningBytes byte for byte (TestTxSigningBytesVector).
func TxSigningBytes(sender string, nonce uint64, fee float64, payload []byte) []byte {
	body, _ := json.Marshal(struct {
		Sender  string `json:"sender"`
		Nonce   uint64 `json:"nonce"`
		Fee     string `json:"fee"`
		Payload string `json:"payload"`
	}{sender, nonce, strconv.FormatFloat(fee, 'f', -1, 64), string(payload)})
	return append([]byte(TxDomain), body...)
}

// ValidateAction checks an action before it is signed.
func ValidateAction(a Action) error {
	switch a.Op {
	case "join":
		if !IsAddress(a.Pool) {
			return errors.New("pulse: join needs the pool ID (64 lowercase hex)")
		}
		if len(a.Proofs) != 0 {
			return errors.New("pulse: join carries no proofs")
		}
	case "leave":
		if a.Pool != "" || len(a.Proofs) != 0 {
			return errors.New("pulse: leave carries no pool and no proofs")
		}
	case "reclaim":
		if a.Pool != "" {
			return errors.New("pulse: reclaim carries no pool")
		}
		if len(a.Proofs) == 0 || len(a.Proofs) > MaxReclaimProofs {
			return fmt.Errorf("pulse: reclaim needs 1-%d proof IDs", MaxReclaimProofs)
		}
		seen := map[string]bool{}
		for _, p := range a.Proofs {
			if p == "" || len(p) > 128 || seen[p] {
				return errors.New("pulse: reclaim proof IDs must be distinct and 1-128 characters")
			}
			seen[p] = true
		}
	default:
		return fmt.Errorf("pulse: unknown op %q (want join, leave or reclaim)", a.Op)
	}
	return nil
}

// IsAddress reports whether s is a QSDM address (64 lowercase hex).
func IsAddress(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// AddressOf is the QSDM address of a packed ML-DSA-87 public key.
func AddressOf(pub []byte) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])
}

// SignAction signs a member action with the wallet's private key. The sender
// is the key's own address; fee is the transaction fee in CELL.
func SignAction(sk *mldsa87.PrivateKey, pub []byte, nonce uint64, fee float64, a Action) (*Envelope, error) {
	if err := ValidateAction(a); err != nil {
		return nil, err
	}
	if fee < 0 {
		return nil, errors.New("pulse: negative fee")
	}
	payload, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	sender := AddressOf(pub)
	sig, err := sign(sk, TxSigningBytes(sender, nonce, fee, payload))
	if err != nil {
		return nil, err
	}
	return &Envelope{Sender: sender, Nonce: nonce, Fee: fee, Payload: payload,
		PublicKey: hex.EncodeToString(pub), Signature: hex.EncodeToString(sig)}, nil
}

// VerifyEnvelope checks an envelope's key, sender and signature (what the node
// checks first).
func VerifyEnvelope(env *Envelope) error {
	if env == nil {
		return errors.New("pulse: no envelope")
	}
	pub, err := hex.DecodeString(env.PublicKey)
	if err != nil || len(pub) != mldsa87.PublicKeySize {
		return errors.New("pulse: bad public key")
	}
	if AddressOf(pub) != env.Sender {
		return errors.New("pulse: sender is not the key's address")
	}
	sig, err := hex.DecodeString(env.Signature)
	if err != nil {
		return errors.New("pulse: signature is not hex")
	}
	if !verify(pub, TxSigningBytes(env.Sender, env.Nonce, env.Fee, env.Payload), sig) {
		return errors.New("pulse: signature does not verify")
	}
	return nil
}

// Login is the pool login: the payout address and an optional worker name.
func Login(address, worker string) (string, error) {
	if !IsAddress(address) {
		return "", errors.New("pulse: the login address must be 64 lowercase hex")
	}
	if worker == "" {
		return address, nil
	}
	if len(worker) > 32 {
		return "", errors.New("pulse: worker name must be 1-32 characters")
	}
	for _, c := range worker {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return "", errors.New("pulse: worker name may use letters, digits, _ and -")
		}
	}
	return address + "." + worker, nil
}

// SignRegister signs a pool login: ML-DSA-87 over RegisterDomain || login. The
// pool checks that the login's address is the key's, so rewards cannot be
// sent to a mistyped address.
func SignRegister(sk *mldsa87.PrivateKey, pub []byte, login string) (string, error) {
	addr, _, _ := strings.Cut(login, ".")
	if addr != AddressOf(pub) {
		return "", errors.New("pulse: the login's address is not this wallet's")
	}
	sig, err := sign(sk, append([]byte(RegisterDomain), login...))
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sig), nil
}

// Receipt is the pool's signed statement that one share was counted. Its JSON
// field order is the signed form (the pool's pulse.ReceiptBytes).
type Receipt struct {
	Pool       string `json:"pool"`
	Index      uint64 `json:"index"`
	Owner      string `json:"owner"`
	Worker     string `json:"worker"`
	JobID      string `json:"job_id"`
	Nonce      string `json:"nonce"`
	BatchCount uint32 `json:"batch_count"`
	Difficulty uint64 `json:"difficulty"`
	AtUnixMs   int64  `json:"at_unix_ms"`
}

// VerifyReceipt checks a receipt against the pool's public key (hex), as
// published by the pool's /info.
func VerifyReceipt(poolPublicKeyHex string, r Receipt, sigHex string) bool {
	pub, err := hex.DecodeString(poolPublicKeyHex)
	if err != nil || len(pub) != mldsa87.PublicKeySize || AddressOf(pub) != r.Pool {
		return false
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return false
	}
	b, _ := json.Marshal(r)
	return verify(pub, append([]byte(ReceiptDomain), b...), sig)
}

func sign(sk *mldsa87.PrivateKey, msg []byte) ([]byte, error) {
	if sk == nil {
		return nil, errors.New("pulse: no private key")
	}
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(sk, msg, nil, true, sig); err != nil {
		return nil, err
	}
	return sig, nil
}

func verify(pub, msg, sig []byte) bool {
	if len(pub) != mldsa87.PublicKeySize || len(sig) != mldsa87.SignatureSize {
		return false
	}
	var buf [mldsa87.PublicKeySize]byte
	copy(buf[:], pub)
	pk := new(mldsa87.PublicKey)
	pk.Unpack(&buf)
	return mldsa87.Verify(pk, msg, nil, sig)
}
