// Package pulse is the chain side of PULSE (Payout, Utility & Ledger Settlement
// Engine), the non-custodial mining pool: the settlement batch format and the
// per-block split are byte-for-byte the pool's (qsdm-mining-bootstrap/pulse,
// pinned by testdata/conformance.json), and rule.go is the consensus rule that
// pays a batch. Nothing here runs until a release sets the activation height.
package pulse

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// Address is a QSDM wallet address: hex(sha256(ML-DSA-87 public key)),
// 64 lowercase hex characters.
type Address string

// ParseAddress accepts exactly 64 hex characters (any case) and lowercases them.
func ParseAddress(s string) (Address, error) {
	if len(s) != 64 {
		return "", fmt.Errorf("pulse: address must be 64 hex characters, got %d", len(s))
	}
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		return "", errors.New("pulse: address is not hex")
	}
	return Address(strings.ToLower(s)), nil
}

// Bytes returns the 32 raw bytes of a valid address.
func (a Address) Bytes() ([32]byte, error) {
	var out [32]byte
	b, err := hex.DecodeString(string(a))
	if err != nil || len(b) != 32 {
		return out, errors.New("pulse: bad address")
	}
	copy(out[:], b)
	return out, nil
}

// AddressOf is the QSDM address of a public key.
func AddressOf(pk *mldsa87.PublicKey) Address {
	s := sha256.Sum256(pk.Bytes())
	return Address(hex.EncodeToString(s[:]))
}

// Login is "<address>[.<worker>]". A worker name is 1-32 of [A-Za-z0-9_-].
type Login struct {
	Address Address
	Worker  string
}

// ParseLogin splits a miner login. The address is the payout address: shares
// sent under a wrong address pay that address and cannot be recovered.
func ParseLogin(s string) (Login, error) {
	addr, worker, hasWorker := strings.Cut(s, ".")
	a, err := ParseAddress(addr)
	if err != nil {
		return Login{}, err
	}
	if hasWorker {
		if len(worker) < 1 || len(worker) > 32 {
			return Login{}, errors.New("pulse: worker name must be 1-32 characters")
		}
		for _, c := range worker {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
				return Login{}, errors.New("pulse: worker name may use letters, digits, _ and -")
			}
		}
	}
	return Login{Address: a, Worker: worker}, nil
}

// KeyPair is an ML-DSA-87 key pair kept as its 32-byte seed.
type KeyPair struct {
	Seed [mldsa87.SeedSize]byte
	Pub  *mldsa87.PublicKey
	priv *mldsa87.PrivateKey
}

// NewKeyPair makes a fresh key from the system random source.
func NewKeyPair() (*KeyPair, error) {
	var seed [mldsa87.SeedSize]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return nil, err
	}
	return KeyPairFromSeed(seed), nil
}

// KeyPairFromSeed rebuilds a key pair from its seed.
func KeyPairFromSeed(seed [mldsa87.SeedSize]byte) *KeyPair {
	pk, sk := mldsa87.NewKeyFromSeed(&seed)
	return &KeyPair{Seed: seed, Pub: pk, priv: sk}
}

// ID is the key's QSDM address; for a pool key it is the pool ID.
func (k *KeyPair) ID() Address { return AddressOf(k.Pub) }

// Sign signs domain || msg (randomized, empty context), as QSDM signs.
func (k *KeyPair) Sign(domain string, msg []byte) ([]byte, error) {
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(k.priv, append([]byte(domain), msg...), nil, true, sig); err != nil {
		return nil, err
	}
	return sig, nil
}

// ParsePublicKey decodes a packed ML-DSA-87 public key.
func ParsePublicKey(b []byte) (*mldsa87.PublicKey, error) {
	if len(b) != mldsa87.PublicKeySize {
		return nil, fmt.Errorf("pulse: public key must be %d bytes", mldsa87.PublicKeySize)
	}
	var buf [mldsa87.PublicKeySize]byte
	copy(buf[:], b)
	pk := new(mldsa87.PublicKey)
	pk.Unpack(&buf)
	return pk, nil
}

// Verify checks sig over domain || msg.
func Verify(pk *mldsa87.PublicKey, domain string, msg, sig []byte) bool {
	if pk == nil || len(sig) != mldsa87.SignatureSize {
		return false
	}
	return mldsa87.Verify(pk, append([]byte(domain), msg...), nil, sig)
}
