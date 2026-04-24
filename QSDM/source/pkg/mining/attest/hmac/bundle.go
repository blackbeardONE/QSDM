// Package hmac implements the nvidia-hmac-v1 attestation path for
// consumer-class NVIDIA GPUs (Turing through Ada). It is one of the
// two pluggable pkg/mining.AttestationVerifier implementations
// specified in QSDM/docs/docs/MINING_PROTOCOL_V2_NVIDIA_LOCKED.md
// §3.2.2; the other is pkg/mining/attest/cc (Hopper/Blackwell CC
// GPUs, shipping later in Phase 2c).
//
// The package is deliberately light on external dependencies — the
// only crypto primitives used are crypto/hmac, crypto/sha256, and
// encoding/hex. No cgo, no external libraries. That keeps this
// verifier easy to audit, easy to port, and free of supply-chain
// surface area, which matters because it sits on the consensus
// critical path once FORK_V2 activates.
//
// Bundle wire format (spec §3.2.2):
//
//	{
//	  "challenge_bind": "<64-hex>",  // sha256(miner_addr || batch_root || mix_digest)
//	  "compute_cap":    "8.9",
//	  "cuda_version":   "12.8",
//	  "driver_ver":     "572.16",
//	  "gpu_name":       "NVIDIA GeForce RTX 4090",
//	  "gpu_uuid":       "<nvidia-smi hex UUID>",
//	  "hmac":           "<64-hex>",   // HMAC-SHA256 over canonical JSON without this field
//	  "issued_at":      1700000000,
//	  "node_id":        "alice-rtx4090-01",
//	  "nonce":          "<64-hex, same 32 bytes as Attestation.Nonce>"
//	}
//
// The canonical form used for the HMAC is a JSON object with the
// first 9 fields in strict alphabetical order (the `hmac` field is
// excluded). This is emitted by Bundle.CanonicalForMAC. Miners who
// emit a different byte ordering will still be accepted as long as
// their HMAC was computed over THIS canonical form — the verifier
// recomputes it from the unmarshalled struct before checking.
package hmac

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// Bundle is the decoded contents of a nvidia-hmac-v1 attestation
// bundle. Its fields are laid out in alphabetical order on purpose:
// json.Marshal emits fields in struct-declaration order, and
// CanonicalForMAC relies on that order matching the spec's
// canonical form byte-for-byte. Do NOT reorder these fields without
// updating every cross-implementation test vector.
type Bundle struct {
	ChallengeBind string `json:"challenge_bind"`
	ComputeCap    string `json:"compute_cap"`
	CUDAVersion   string `json:"cuda_version"`
	DriverVer     string `json:"driver_ver"`
	GPUName       string `json:"gpu_name"`
	GPUUUID       string `json:"gpu_uuid"`
	HMAC          string `json:"hmac"`
	IssuedAt      int64  `json:"issued_at"`
	NodeID        string `json:"node_id"`
	Nonce         string `json:"nonce"`
}

// ParseBundle decodes a base64-encoded bundle into a Bundle
// struct. It does NOT validate semantic invariants (hex lengths,
// HMAC match, etc.) — those live in the Verifier. Returns an error
// whose chain wraps a sentinel from pkg/mining (the caller is
// expected to wrap further with that sentinel).
func ParseBundle(bundleBase64 string) (Bundle, error) {
	raw, err := base64.StdEncoding.DecodeString(bundleBase64)
	if err != nil {
		return Bundle{}, fmt.Errorf("base64 decode: %w", err)
	}
	var b Bundle
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return Bundle{}, fmt.Errorf("json decode: %w", err)
	}
	// Ensure there is no trailing garbage after the JSON object.
	// This is a cheap defence against attackers stuffing replay
	// material into a bundle that still parses as "valid JSON" to
	// lax parsers.
	if dec.More() {
		return Bundle{}, errors.New("trailing bytes after bundle JSON")
	}
	return b, nil
}

// canonicalBundleForMAC is the struct whose JSON serialization is
// the input to HMAC-SHA256. It intentionally omits the HMAC field
// so the MAC is computed over "everything but the MAC itself."
// The field order matches Bundle minus HMAC — alphabetical — so
// json.Marshal emits bytes identical to the spec's canonical form.
type canonicalBundleForMAC struct {
	ChallengeBind string `json:"challenge_bind"`
	ComputeCap    string `json:"compute_cap"`
	CUDAVersion   string `json:"cuda_version"`
	DriverVer     string `json:"driver_ver"`
	GPUName       string `json:"gpu_name"`
	GPUUUID       string `json:"gpu_uuid"`
	IssuedAt      int64  `json:"issued_at"`
	NodeID        string `json:"node_id"`
	Nonce         string `json:"nonce"`
}

// CanonicalForMAC returns the exact byte sequence the HMAC must be
// computed over. It is a stable function of the semantic bundle
// contents — reordering wire bytes does NOT change the output.
// This is what lets a miner in Rust and a validator in Go agree on
// the MAC without agreeing on their JSON encoders.
func (b Bundle) CanonicalForMAC() ([]byte, error) {
	return json.Marshal(canonicalBundleForMAC{
		ChallengeBind: b.ChallengeBind,
		ComputeCap:    b.ComputeCap,
		CUDAVersion:   b.CUDAVersion,
		DriverVer:     b.DriverVer,
		GPUName:       b.GPUName,
		GPUUUID:       b.GPUUUID,
		IssuedAt:      b.IssuedAt,
		NodeID:        b.NodeID,
		Nonce:         b.Nonce,
	})
}

// ComputeMAC returns HMAC-SHA256(key, canonical) as a 32-byte
// slice. Thin wrapper around crypto/hmac kept local so the whole
// MAC path lives in one file for audit.
func ComputeMAC(key, canonical []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(canonical)
	return mac.Sum(nil)
}

// ComputeChallengeBind returns the 32-byte SHA-256 hash that the
// bundle's challenge_bind field MUST match: sha256(miner_addr ||
// batch_root || mix_digest). Separator-free concatenation is
// deliberate — every field is a fixed-length byte range (the miner
// address is encoded as its UTF-8 bytes with no terminator, which
// is safe because validateShape has already accepted the address
// length and we never concatenate two variable-length fields
// without a deterministic length prefix).
//
// Per spec §3.2.2 step 2, this value binds the bundle to the
// specific proof it rides in. A miner cannot reuse one bundle
// across two different proofs because swapping proofs changes at
// least mix_digest.
func ComputeChallengeBind(minerAddr string, batchRoot, mixDigest [32]byte) [32]byte {
	h := sha256.New()
	h.Write([]byte(minerAddr))
	h.Write(batchRoot[:])
	h.Write(mixDigest[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// HexChallengeBind is a convenience wrapper for miners building a
// bundle. Tests and reference miner code use it so the hex encoding
// is applied once and once only.
func HexChallengeBind(minerAddr string, batchRoot, mixDigest [32]byte) string {
	b := ComputeChallengeBind(minerAddr, batchRoot, mixDigest)
	return hex.EncodeToString(b[:])
}

// Sign is a test/reference helper that builds a fully-signed
// Bundle from its unsigned fields + an HMAC key. Not used on the
// verifier hot path — the verifier NEVER signs, only verifies —
// but consolidating the sign logic here keeps the round-trip test
// honest and gives reference miner implementations a single
// canonical signing function to crib from.
func (b Bundle) Sign(key []byte) (Bundle, error) {
	unsigned := b
	unsigned.HMAC = ""
	canonical, err := unsigned.CanonicalForMAC()
	if err != nil {
		return Bundle{}, err
	}
	mac := ComputeMAC(key, canonical)
	out := b
	out.HMAC = hex.EncodeToString(mac)
	return out, nil
}

// MarshalBase64 renders a Bundle as the base64-JSON blob the
// Attestation.BundleBase64 field carries. The JSON is emitted in
// Bundle's field order (alphabetical) so what ships on the wire is
// byte-identical to the canonical form plus the hmac field
// appended in its alphabetical slot. Tests and reference miners
// use this; the verifier does not — it only reads.
func (b Bundle) MarshalBase64() (string, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}
