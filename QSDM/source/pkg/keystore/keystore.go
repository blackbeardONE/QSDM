// Package keystore — passphrase-protected self-custody keystore format for
// QSDM wallets.
//
// One Go package owns the keystore JSON schema, so the qsdmcli wallet
// subcommand and the browser wallet (compiled to GOOS=js GOARCH=wasm)
// produce byte-identical files: a keystore generated in a browser can be
// loaded by qsdmcli, and vice-versa. The browser path additionally uses
// WebCrypto in JavaScript for the encrypt/decrypt envelope; the wire shape
// is what this file pins, not the implementation.
//
// Schema (version 1):
//
//	{
//	  "version":      1,
//	  "type":         "qsdm-keystore",
//	  "algorithm":    "ml-dsa-87",
//	  "address":      "<hex SHA-256 of public_key>",
//	  "public_key":   "<hex 2592-byte FIPS 204 ML-DSA-87 public key>",
//	  "kdf":          "pbkdf2-sha256",
//	  "kdf_params":   { "iterations": 600000, "salt": "<hex 16>", "key_len": 32 },
//	  "cipher":       "aes-256-gcm",
//	  "cipher_params":{ "nonce": "<hex 12>" },
//	  "ciphertext":   "<hex AES-256-GCM(private_key) with appended 16-byte tag>",
//	  "created_at":   "RFC 3339 UTC timestamp"
//	}
//
// Choice rationale:
//
//   - PBKDF2-HMAC-SHA-256 over scrypt/argon2: WebCrypto's `crypto.subtle`
//     supports PBKDF2 natively in every modern browser. scrypt and argon2
//     are not in the WebCrypto surface, so picking them would force a
//     pure-JS implementation in the browser wallet — exactly the kind of
//     "we wrote our own KDF" trap that the OWASP guidance is trying to
//     avoid. PBKDF2 with 600,000 iterations of SHA-256 matches OWASP 2023.
//   - AES-256-GCM: also in WebCrypto, deterministic-on-nonce, authenticated
//     so a tampered ciphertext rejects rather than decrypting to garbage
//     that the caller might mistake for a real key.
//   - 16-byte salt, 12-byte nonce: standard sizes for these primitives.
//   - The address field is sha256(public_key) hex-encoded to match
//     pkg/wallet.NewWalletService' on-the-wire address shape so a keystore
//     loaded here produces the same `qsdm1…` identifier as a wallet
//     generated inside the validator.
//
// What is NOT in the keystore:
//
//   - The private key in plaintext. Anywhere. Ever.
//   - HMAC over the metadata. AES-GCM authenticates the ciphertext; the
//     metadata is intentionally plaintext so an inspect-only flow (qsdmcli
//     wallet show) can render the address and public key without prompting
//     for a passphrase.
//   - A "hint" field. Hints are a footgun — they leak passphrase structure
//     and are commonly mis-used as a backup password. Omitted by design.
package keystore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/pbkdf2"
)

// Current keystore schema version. Bump only when the on-disk shape
// changes in a backward-incompatible way; additive fields can use the
// existing version since unmarshalling tolerates unknown keys.
const Version = 1

// Algorithm identifier embedded in keystore. The wallet API contract is
// "every QSDM keystore is ML-DSA-87" — a future hybrid scheme (e.g.
// ML-DSA-87 + Ed25519 fallback for legacy chain replay) would bump the
// algorithm string and the schema version together.
const Algorithm = "ml-dsa-87"

// Type tag in the file. Identifies a QSDM keystore unambiguously so a
// loader can refuse a JSON that happens to have similarly-named fields
// from another product (Ethereum keystore v3, Stellar SEP-0007, ...).
const Type = "qsdm-keystore"

// KDF / cipher identifiers. Stored as strings (not enums) so a future
// version can decode an old keystore without code changes; new params
// land alongside a Version bump.
const (
	KDFPBKDF2SHA256 = "pbkdf2-sha256"
	CipherAESGCM    = "aes-256-gcm"
	RecoveryScheme  = "qsdm-wallet-recovery-v1"
	// LegacyRecoveryScheme protects fresh recovery entropy in an existing
	// random-key wallet. The entropy unlocks a replicated recovery capsule;
	// it does not deterministically generate the wallet key.
	LegacyRecoveryScheme = "qsdm-legacy-wallet-recovery-v1"
	RecoveryWords        = 24
	RecoveryBytes        = 32
)

// PBKDF2 parameters. The browser side derives its key under the exact same
// constants so an in-browser-generated keystore round-trips through the
// CLI. Bumping iterations is a backward-compatible change for new files
// (the kdf_params block carries the parameter explicitly); old files keep
// their own value.
const (
	DefaultPBKDF2Iterations = 600_000
	DefaultPBKDF2KeyLen     = 32 // AES-256 key
	DefaultPBKDF2SaltLen    = 16
	GCMNonceLen             = 12
)

// PublicKeySize matches mldsa87 / FIPS 204 §6.1 (informational; callers
// can use this to validate a public_key field length before attempting
// any cryptographic operation).
const PublicKeySize = 2592

// Keystore is the in-memory representation of a v1 keystore file. Field
// order is the JSON order on disk; `json.Marshal` preserves struct field
// order so two keystores generated with identical inputs (modulo random
// salt/nonce) have identical key positions in the output JSON.
type Keystore struct {
	Version      int          `json:"version"`
	Type         string       `json:"type"`
	Algorithm    string       `json:"algorithm"`
	Address      string       `json:"address"`
	PublicKey    string       `json:"public_key"`
	KDF          string       `json:"kdf"`
	KDFParams    KDFParams    `json:"kdf_params"`
	Cipher       string       `json:"cipher"`
	CipherParams CipherParams `json:"cipher_params"`
	Ciphertext   string       `json:"ciphertext"`
	CreatedAt    string       `json:"created_at"`
	Recovery     *Recovery    `json:"recovery,omitempty"`
}

// Recovery contains an independently encrypted copy of the 256-bit recovery
// entropy for wallets created from QSDM Recovery Words. Legacy randomly
// generated wallets omit this block and continue to use JSON + passphrase.
// A separate salt and nonce prevent key/nonce reuse with the private-key
// ciphertext even though both records are protected by the same passphrase.
type Recovery struct {
	Scheme       string       `json:"scheme"`
	Words        int          `json:"words"`
	Locator      string       `json:"locator,omitempty"`
	KDF          string       `json:"kdf"`
	KDFParams    KDFParams    `json:"kdf_params"`
	Cipher       string       `json:"cipher"`
	CipherParams CipherParams `json:"cipher_params"`
	Ciphertext   string       `json:"ciphertext"`
}

// KDFParams describes the password-derivation step. For
// PBKDF2-HMAC-SHA-256, only these three numeric / hex fields are needed;
// the hash family is encoded in the KDF string itself ("pbkdf2-sha256")
// so a future "pbkdf2-sha384" can be added without restructuring this
// struct.
type KDFParams struct {
	Iterations int    `json:"iterations"`
	Salt       string `json:"salt"`
	KeyLen     int    `json:"key_len"`
}

// CipherParams describes the symmetric-cipher step. For AES-GCM, the
// authenticated nonce is the only per-record value (the key comes from
// the KDF, the algorithm parameters are fixed by the Cipher string).
type CipherParams struct {
	Nonce string `json:"nonce"`
}

// Encrypt produces a v1 keystore from a raw ML-DSA-87 (publicKey,
// privateKey) pair and a passphrase. Returns the keystore by value so
// callers can either WriteFile it directly or serialize it themselves.
//
// publicKey MUST be 2592 bytes (FIPS 204 ML-DSA-87 packed public key).
// privateKey is opaque to this layer — every byte gets encrypted as-is
// and emerges identical on Decrypt. The address field is derived from
// publicKey, NOT from the encrypted private key, so the same caller-side
// address computation as the validator (sha256(public_key) hex) is used.
//
// The passphrase is consumed only by PBKDF2; it never appears in the
// returned struct. A nil or empty passphrase is rejected (we refuse to
// encrypt with the empty string — a "fast unlock" trick that is
// invariably the source of "my key was stolen because the passphrase was
// blank" incident reports).
func Encrypt(publicKey, privateKey []byte, passphrase []byte) (Keystore, error) {
	if len(publicKey) != PublicKeySize {
		return Keystore{}, fmt.Errorf("keystore: public key must be %d bytes (FIPS 204 ML-DSA-87), got %d", PublicKeySize, len(publicKey))
	}
	if len(privateKey) == 0 {
		return Keystore{}, errors.New("keystore: private key is empty")
	}
	if len(passphrase) == 0 {
		return Keystore{}, errors.New("keystore: empty passphrase refused (use a real one — a zero-byte passphrase is functionally no encryption)")
	}

	kdfParams, cipherParams, ciphertext, err := encryptSecret(privateKey, passphrase, nil)
	if err != nil {
		return Keystore{}, err
	}
	// Seal appends the 16-byte tag to the ciphertext; total length is
	// len(privateKey)+16. The address bytes are not used as AAD —
	// keeping the metadata unauthenticated lets a wallet UI render the
	// public address from the file without prompting for a passphrase
	// (a useful UX affordance for "which keystore am I about to open?"
	// dialogs). Tampering with the metadata is detected separately by
	// the post-decrypt public-key cross-check in Decrypt.
	addrSum := sha256.Sum256(publicKey)

	return Keystore{
		Version:      Version,
		Type:         Type,
		Algorithm:    Algorithm,
		Address:      hex.EncodeToString(addrSum[:]),
		PublicKey:    hex.EncodeToString(publicKey),
		KDF:          KDFPBKDF2SHA256,
		KDFParams:    kdfParams,
		Cipher:       CipherAESGCM,
		CipherParams: cipherParams,
		Ciphertext:   ciphertext,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// EncryptWithRecovery creates a normal encrypted keystore and attaches an
// independently encrypted QSDM Recovery Words entropy record. recoveryEntropy
// is exactly 32 random bytes; callers are responsible for deriving the supplied
// keypair from that entropy according to RecoveryScheme.
func EncryptWithRecovery(publicKey, privateKey, recoveryEntropy, passphrase []byte) (Keystore, error) {
	if len(recoveryEntropy) != RecoveryBytes {
		return Keystore{}, fmt.Errorf("keystore: recovery entropy must be %d bytes, got %d", RecoveryBytes, len(recoveryEntropy))
	}
	ks, err := Encrypt(publicKey, privateKey, passphrase)
	if err != nil {
		return Keystore{}, err
	}
	kdfParams, cipherParams, ciphertext, err := encryptSecret(
		recoveryEntropy,
		passphrase,
		recoveryAAD(ks.Address),
	)
	if err != nil {
		return Keystore{}, fmt.Errorf("keystore: encrypt recovery entropy: %w", err)
	}
	ks.Recovery = &Recovery{
		Scheme:       RecoveryScheme,
		Words:        RecoveryWords,
		KDF:          KDFPBKDF2SHA256,
		KDFParams:    kdfParams,
		Cipher:       CipherAESGCM,
		CipherParams: cipherParams,
		Ciphertext:   ciphertext,
	}
	return ks, nil
}

// AttachLegacyRecovery records encrypted recovery entropy on an existing
// random-key keystore after its recovery capsule has been accepted by QSDM
// Core. It never changes the private-key ciphertext, public key, or address.
func AttachLegacyRecovery(ks Keystore, recoveryEntropy, passphrase []byte, locator string) (Keystore, error) {
	if err := Validate(ks); err != nil {
		return Keystore{}, err
	}
	if len(recoveryEntropy) != RecoveryBytes {
		return Keystore{}, fmt.Errorf("keystore: recovery entropy must be %d bytes, got %d", RecoveryBytes, len(recoveryEntropy))
	}
	if len(passphrase) == 0 {
		return Keystore{}, ErrInvalidPassphrase
	}
	locatorBytes, err := hex.DecodeString(locator)
	if err != nil || len(locatorBytes) != sha256.Size || locator != strings.ToLower(strings.TrimSpace(locator)) {
		return Keystore{}, errors.New("keystore: legacy recovery locator must be a canonical 32-byte lower-case hex digest")
	}
	// Refuse to attach recovery metadata unless the supplied passphrase really
	// unlocks this keystore. The decrypted key is intentionally discarded.
	privateKey, err := Decrypt(ks, passphrase)
	if err != nil {
		return Keystore{}, err
	}
	zero(privateKey)

	kdfParams, cipherParams, ciphertext, err := encryptSecret(
		recoveryEntropy,
		passphrase,
		legacyRecoveryAAD(ks.Address, locator),
	)
	if err != nil {
		return Keystore{}, fmt.Errorf("keystore: encrypt legacy recovery entropy: %w", err)
	}
	ks.Recovery = &Recovery{
		Scheme:       LegacyRecoveryScheme,
		Words:        RecoveryWords,
		Locator:      locator,
		KDF:          KDFPBKDF2SHA256,
		KDFParams:    kdfParams,
		Cipher:       CipherAESGCM,
		CipherParams: cipherParams,
		Ciphertext:   ciphertext,
	}
	return ks, nil
}

func encryptSecret(secret, passphrase, additionalData []byte) (KDFParams, CipherParams, string, error) {
	salt := make([]byte, DefaultPBKDF2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return KDFParams{}, CipherParams{}, "", fmt.Errorf("keystore: salt entropy: %w", err)
	}
	nonce := make([]byte, GCMNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return KDFParams{}, CipherParams{}, "", fmt.Errorf("keystore: nonce entropy: %w", err)
	}

	dk := pbkdf2.Key(passphrase, salt, DefaultPBKDF2Iterations, DefaultPBKDF2KeyLen, sha256.New)
	defer zero(dk)
	block, err := aes.NewCipher(dk)
	if err != nil {
		return KDFParams{}, CipherParams{}, "", fmt.Errorf("keystore: aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return KDFParams{}, CipherParams{}, "", fmt.Errorf("keystore: cipher.NewGCM: %w", err)
	}
	ct := gcm.Seal(nil, nonce, secret, additionalData)
	return KDFParams{
		Iterations: DefaultPBKDF2Iterations,
		Salt:       hex.EncodeToString(salt),
		KeyLen:     DefaultPBKDF2KeyLen,
	}, CipherParams{Nonce: hex.EncodeToString(nonce)}, hex.EncodeToString(ct), nil
}

// Decrypt recovers the raw ML-DSA-87 private key bytes from a keystore
// using the supplied passphrase. Returns (privateKey, error). On the
// happy path the returned slice is the exact byte sequence that was
// passed into Encrypt — callers reconstruct an *mldsa87.PrivateKey via
// `priv.UnmarshalBinary(out)` if they need the parsed key.
//
// Decryption errors (wrong passphrase, tampered ciphertext, corrupted
// salt/nonce) are surfaced as `ErrInvalidPassphrase` so the calling UI
// can render a friendly "passphrase doesn't match this keystore"
// message without disclosing whether the failure mode was the KDF, the
// auth-tag, or the metadata.
func Decrypt(ks Keystore, passphrase []byte) ([]byte, error) {
	if err := Validate(ks); err != nil {
		return nil, err
	}
	if len(passphrase) == 0 {
		return nil, ErrInvalidPassphrase
	}
	return decryptSecret(ks.KDFParams, ks.CipherParams, ks.Ciphertext, passphrase, nil)
}

// DecryptRecovery returns the 256-bit recovery entropy embedded in a
// recovery-enabled keystore. Legacy keystores return ErrNoRecovery.
func DecryptRecovery(ks Keystore, passphrase []byte) ([]byte, error) {
	if err := Validate(ks); err != nil {
		return nil, err
	}
	if ks.Recovery == nil {
		return nil, ErrNoRecovery
	}
	if len(passphrase) == 0 {
		return nil, ErrInvalidPassphrase
	}
	aad := recoveryAAD(ks.Address)
	if ks.Recovery.Scheme == LegacyRecoveryScheme {
		aad = legacyRecoveryAAD(ks.Address, ks.Recovery.Locator)
	}
	plaintext, err := decryptSecret(
		ks.Recovery.KDFParams,
		ks.Recovery.CipherParams,
		ks.Recovery.Ciphertext,
		passphrase,
		aad,
	)
	if err != nil {
		return nil, err
	}
	if len(plaintext) != RecoveryBytes {
		zero(plaintext)
		return nil, fmt.Errorf("keystore: decrypted recovery entropy is %d bytes, want %d", len(plaintext), RecoveryBytes)
	}
	return plaintext, nil
}

func decryptSecret(kdfParams KDFParams, cipherParams CipherParams, ciphertext string, passphrase, additionalData []byte) ([]byte, error) {

	salt, err := hex.DecodeString(kdfParams.Salt)
	if err != nil || len(salt) == 0 {
		return nil, fmt.Errorf("keystore: malformed salt: %w", err)
	}
	nonce, err := hex.DecodeString(cipherParams.Nonce)
	if err != nil || len(nonce) != GCMNonceLen {
		return nil, fmt.Errorf("keystore: malformed nonce")
	}
	ct, err := hex.DecodeString(ciphertext)
	if err != nil || len(ct) == 0 {
		return nil, fmt.Errorf("keystore: malformed ciphertext")
	}

	dk := pbkdf2.Key(passphrase, salt, kdfParams.Iterations, kdfParams.KeyLen, sha256.New)
	defer zero(dk)
	block, err := aes.NewCipher(dk)
	if err != nil {
		return nil, fmt.Errorf("keystore: aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("keystore: cipher.NewGCM: %w", err)
	}
	pt, err := gcm.Open(nil, nonce, ct, additionalData)
	if err != nil {
		// All Open failures collapse to ErrInvalidPassphrase to avoid
		// leaking "passphrase ok but ciphertext was tampered" vs
		// "passphrase wrong" via timing or message text.
		return nil, ErrInvalidPassphrase
	}
	return pt, nil
}

func recoveryAAD(address string) []byte {
	return []byte(RecoveryScheme + ":" + address)
}

func legacyRecoveryAAD(address, locator string) []byte {
	return []byte(LegacyRecoveryScheme + ":" + strings.ToLower(address) + ":" + strings.ToLower(locator))
}

// ErrInvalidPassphrase is returned by Decrypt for every recoverable
// failure mode (wrong passphrase, tampered ciphertext, mutated nonce).
// Callers must NOT log the underlying cause for these cases — that
// information aids an attacker trying to distinguish "this passphrase is
// close" from "this passphrase is way off".
var ErrInvalidPassphrase = errors.New("keystore: passphrase does not match (or the keystore is corrupted)")

// ErrNoRecovery distinguishes a wallet that has not enabled either native or
// legacy-capsule recovery from a malformed recovery-enabled keystore.
var ErrNoRecovery = errors.New("keystore: wallet was not created with QSDM Recovery Words")

// Validate checks that a keystore is well-formed enough to attempt a
// decrypt against. It does NOT verify the passphrase or the ciphertext;
// it only enforces the schema invariants (correct version, recognised
// algorithm strings, expected field lengths).
//
// Use this to short-circuit obviously-invalid input before prompting the
// user for a passphrase — a clear "this file is not a QSDM keystore"
// error is friendlier than "passphrase wrong" when the file is actually
// an Ethereum keystore or a renamed binary.
func Validate(ks Keystore) error {
	if ks.Version != Version {
		return fmt.Errorf("keystore: unsupported version %d (this build accepts v%d)", ks.Version, Version)
	}
	if ks.Type != Type {
		return fmt.Errorf("keystore: bad type %q (want %q)", ks.Type, Type)
	}
	if ks.Algorithm != Algorithm {
		return fmt.Errorf("keystore: bad algorithm %q (want %q)", ks.Algorithm, Algorithm)
	}
	if ks.KDF != KDFPBKDF2SHA256 {
		return fmt.Errorf("keystore: bad kdf %q (want %q)", ks.KDF, KDFPBKDF2SHA256)
	}
	if ks.Cipher != CipherAESGCM {
		return fmt.Errorf("keystore: bad cipher %q (want %q)", ks.Cipher, CipherAESGCM)
	}
	if ks.KDFParams.Iterations < 100_000 {
		return fmt.Errorf("keystore: pbkdf2 iterations=%d is below the 100k floor (refuse to decrypt a weak-KDF keystore)", ks.KDFParams.Iterations)
	}
	if ks.KDFParams.KeyLen != 32 {
		return fmt.Errorf("keystore: pbkdf2 key_len=%d (want 32 for AES-256)", ks.KDFParams.KeyLen)
	}
	pk, err := hex.DecodeString(ks.PublicKey)
	if err != nil {
		return fmt.Errorf("keystore: public_key not hex: %w", err)
	}
	if len(pk) != PublicKeySize {
		return fmt.Errorf("keystore: public_key is %d bytes (want %d)", len(pk), PublicKeySize)
	}
	addr, err := hex.DecodeString(ks.Address)
	if err != nil || len(addr) != sha256.Size {
		return fmt.Errorf("keystore: address must be %d hex chars (sha256 of public_key)", sha256.Size*2)
	}
	want := sha256.Sum256(pk)
	for i := range want {
		if want[i] != addr[i] {
			return fmt.Errorf("keystore: address does not match sha256(public_key) — file is mutated or corrupted")
		}
	}
	if ks.Recovery != nil {
		if err := validateRecovery(*ks.Recovery); err != nil {
			return err
		}
	}
	return nil
}

func validateRecovery(recovery Recovery) error {
	if recovery.Scheme != RecoveryScheme && recovery.Scheme != LegacyRecoveryScheme {
		return fmt.Errorf("keystore: unsupported recovery scheme %q", recovery.Scheme)
	}
	if recovery.Words != RecoveryWords {
		return fmt.Errorf("keystore: recovery words=%d (want %d)", recovery.Words, RecoveryWords)
	}
	if recovery.Scheme == RecoveryScheme && recovery.Locator != "" {
		return fmt.Errorf("keystore: native recovery record must not contain a capsule locator")
	}
	if recovery.Scheme == LegacyRecoveryScheme {
		locator, err := hex.DecodeString(recovery.Locator)
		if err != nil || len(locator) != sha256.Size || recovery.Locator != strings.ToLower(strings.TrimSpace(recovery.Locator)) {
			return fmt.Errorf("keystore: malformed legacy recovery locator")
		}
	}
	if recovery.KDF != KDFPBKDF2SHA256 {
		return fmt.Errorf("keystore: bad recovery kdf %q", recovery.KDF)
	}
	if recovery.KDFParams.Iterations < 100_000 || recovery.KDFParams.KeyLen != DefaultPBKDF2KeyLen {
		return fmt.Errorf("keystore: invalid recovery kdf parameters")
	}
	salt, err := hex.DecodeString(recovery.KDFParams.Salt)
	if err != nil || len(salt) == 0 {
		return fmt.Errorf("keystore: malformed recovery salt")
	}
	if recovery.Cipher != CipherAESGCM {
		return fmt.Errorf("keystore: bad recovery cipher %q", recovery.Cipher)
	}
	nonce, err := hex.DecodeString(recovery.CipherParams.Nonce)
	if err != nil || len(nonce) != GCMNonceLen {
		return fmt.Errorf("keystore: malformed recovery nonce")
	}
	ciphertext, err := hex.DecodeString(recovery.Ciphertext)
	if err != nil || len(ciphertext) != RecoveryBytes+16 {
		return fmt.Errorf("keystore: malformed recovery ciphertext")
	}
	return nil
}

func zero(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

// Marshal renders the keystore to indented JSON. Indent is 2 spaces so a
// human-edited keystore is diff-friendly; the wallet file isn't a hot
// path so the size overhead is irrelevant.
func Marshal(ks Keystore) ([]byte, error) {
	return json.MarshalIndent(ks, "", "  ")
}

// Unmarshal parses a keystore JSON blob. Does not Validate — callers
// that want the schema check call Validate explicitly after unmarshal.
func Unmarshal(data []byte) (Keystore, error) {
	var ks Keystore
	if err := json.Unmarshal(data, &ks); err != nil {
		return Keystore{}, fmt.Errorf("keystore: parse: %w", err)
	}
	return ks, nil
}

// AddressFromPublicKey returns the canonical QSDM address (hex
// sha256 of the packed public key) for a given ML-DSA-87 public key.
// Identical to the validator-side pkg/wallet derivation so a keystore
// generated here produces the same address as a wallet generated inside
// a running node.
func AddressFromPublicKey(publicKey []byte) string {
	sum := sha256.Sum256(publicKey)
	return hex.EncodeToString(sum[:])
}
