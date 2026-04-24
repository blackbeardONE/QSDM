// Package challenge implements the validator-side challenge
// issuer and verifier described in
// MINING_PROTOCOL_V2_NVIDIA_LOCKED.md §6. Before a miner computes
// a v2 proof, it fetches a fresh challenge from some validator's
// GET /api/v1/mining/challenge endpoint, commits to that
// challenge inside its Attestation.Nonce / IssuedAt fields, and
// echoes the signature in the inline attestation bundle (for the
// nvidia-hmac-v1 path, this arrives via an added bundle field in
// Phase 2c-iii — callers integrating earlier should carry the
// signature out-of-band).
//
// Why this is a separate subpackage:
//
//   - Nonce issuance is consensus-adjacent but NOT consensus-
//     critical: a misbehaving issuer can hurt its own miners by
//     serving stale nonces, but cannot forge proofs on behalf of
//     others. Keeping it out of pkg/mining lets the verifier stay
//     small and audit-focused.
//   - The crypto primitive is pluggable via the Signer interface.
//     The reference implementation uses HMAC-SHA256 so Phase 2c
//     can ship a self-contained, testable system. Production will
//     wire in the validator's consensus signing key (ML-DSA via
//     pkg/crypto) behind the same interface — the endpoint
//     contract does not change.
//   - The IssuedTracker (seen-recently cache) lives here so every
//     validator has ONE authoritative place to track "did I issue
//     this?" independent of whether the challenge was minted by
//     this process or a peer.
package challenge

import (
	"encoding/hex"
	"errors"
	"fmt"
)

// Challenge is the wire representation of a server-issued nonce.
// Miners GET this blob and commit to its fields in their proofs.
// The JSON shape is part of the v2 protocol surface — changing
// field names requires a fork bump.
type Challenge struct {
	// Nonce is 32 random bytes. Serialized as lowercase hex to
	// match the convention elsewhere in the v2 protocol (Proof
	// canonical JSON emits all byte fields as lowercase hex).
	Nonce [32]byte

	// IssuedAt is unix seconds at the time of minting. Callers
	// verifying this challenge MUST reject if
	// now - IssuedAt > FreshnessWindow (60s per
	// mining.FreshnessWindow) or if IssuedAt is farther in the
	// future than an agreed small skew tolerance.
	IssuedAt int64

	// SignerID identifies which validator minted this challenge.
	// Format is opaque to this package — HMAC signers may use a
	// short operator-chosen name; ML-DSA signers may use a
	// truncated pubkey hash. Verifiers use SignerID to look up
	// the matching verification key in a registry.
	SignerID string

	// Signature is the Signer's output over the bytes returned by
	// Challenge.SigningBytes. Format is signer-specific; for the
	// reference HMAC signer this is 32 raw bytes, rendered as
	// lowercase hex on the wire.
	Signature []byte
}

// SigningBytes returns the canonical byte sequence a signer
// covers. This is a length-prefixed concatenation of the three
// fields that identify a challenge: signer_id, issued_at, nonce.
// The prefix scheme is uvarint lengths so a malicious miner
// cannot craft two different (signer_id, issued_at) tuples whose
// concatenated bytes collide.
//
// The exact format (little-endian uvarints + raw bytes) is a
// consensus-critical serialization just like Proof.CanonicalJSON;
// a mismatch between miner and validator yields silent signature
// failures. Tests lock the byte output in a golden-string
// assertion.
func (c Challenge) SigningBytes() []byte {
	sid := []byte(c.SignerID)
	// Layout: "qsdm.v2.challenge\0" tag || uvarint(len(sid)) || sid
	//       || uvarint(issued_at) || nonce(32)
	out := make([]byte, 0, 64+len(sid))
	out = append(out, "qsdm.v2.challenge\x00"...)
	out = appendUvarint(out, uint64(len(sid)))
	out = append(out, sid...)
	// Encode issued_at via a 9-byte signed-int encoding (uvarint
	// over zig-zag) so the signature is well-defined for times
	// before the unix epoch. In practice mining nodes are always
	// post-epoch, but defining the serialization for negative
	// inputs means the function is total.
	out = appendUvarint(out, uint64(zigzagEncode(c.IssuedAt)))
	out = append(out, c.Nonce[:]...)
	return out
}

// Verify runs the freshness + signer lookup + signature check
// against the supplied Verifier. Returns nil on acceptance or
// one of the ErrChallenge* sentinels on rejection.
//
//	now             - validator's current wall-clock
//	window          - maximum permitted age (use mining.FreshnessWindow)
//	maxFutureSkew   - maximum permitted clock drift for
//	                  freshly-issued challenges from a peer
//	                  validator ahead of us; 5s is a sane default
//	sv              - collaborator that resolves SignerID → key
//	                  and performs the actual crypto check
func (c Challenge) Verify(now, issuedAt int64, window, maxFutureSkew int64, sv SignerVerifier) error {
	age := now - c.IssuedAt
	if age > window {
		return fmt.Errorf("challenge: age %ds > window %ds: %w", age, window, ErrChallengeStale)
	}
	if age < -maxFutureSkew {
		return fmt.Errorf("challenge: issued %ds in future (max skew %ds): %w",
			-age, maxFutureSkew, ErrChallengeStale)
	}
	if sv == nil {
		return errors.New("challenge: Verify requires a non-nil SignerVerifier")
	}
	if err := sv.VerifySignature(c.SignerID, c.SigningBytes(), c.Signature); err != nil {
		return fmt.Errorf("challenge: signature: %w", err)
	}
	return nil
}

// Sentinel errors. Wrapped by callers; downstream metrics group
// by these.
var (
	ErrChallengeStale          = errors.New("challenge: outside freshness window")
	ErrChallengeUnknownSigner  = errors.New("challenge: signer_id not recognised")
	ErrChallengeSignatureBad   = errors.New("challenge: signature verification failed")
	ErrChallengeMalformed      = errors.New("challenge: malformed payload")
)

// Signer produces signatures over challenge bytes. Implementations
// MUST be deterministic with respect to (input, key) — two calls
// with the same inputs produce identical output — so the wire
// signature is content-addressable.
type Signer interface {
	// Sign returns the signature over payload. payload is
	// Challenge.SigningBytes() output.
	Sign(payload []byte) ([]byte, error)

	// SignerID returns the stable identifier a verifier will use
	// to look up this signer's key. Empty string is not allowed.
	SignerID() string
}

// SignerVerifier is the verifier-side counterpart. One
// implementation typically backs many SignerIDs (it holds a
// registry of peer validators' keys) — hence SignerID is an
// explicit argument rather than tied to the verifier instance.
type SignerVerifier interface {
	// VerifySignature checks that signature is a valid Signer
	// output over payload for signerID's key. Returns
	// ErrChallengeUnknownSigner if signerID is not registered,
	// ErrChallengeSignatureBad on cryptographic failure, nil on
	// success.
	VerifySignature(signerID string, payload, signature []byte) error
}

// ----- helpers: uvarint + zigzag -----------------------------------

// appendUvarint appends an unsigned little-endian varint to buf
// and returns the extended slice. Duplicated from encoding/binary
// to keep this package dependency-free on encoding/binary's
// buffer allocation patterns.
func appendUvarint(buf []byte, x uint64) []byte {
	for x >= 0x80 {
		buf = append(buf, byte(x)|0x80)
		x >>= 7
	}
	return append(buf, byte(x))
}

// zigzagEncode maps a signed int64 to an unsigned uint64 such
// that small negative numbers produce small unsigned numbers
// (rather than huge two's-complement values). Used for
// issued_at so the uvarint encoding stays compact for plausible
// timestamps.
func zigzagEncode(n int64) uint64 {
	return uint64((n << 1) ^ (n >> 63))
}

// NonceHex is a small helper for HTTP wire rendering; the JSON
// encoder emits [32]byte arrays as number arrays by default, so
// the handler uses this instead.
func NonceHex(n [32]byte) string { return hex.EncodeToString(n[:]) }

// SignatureHex renders the signer's output as lowercase hex.
func SignatureHex(sig []byte) string { return hex.EncodeToString(sig) }
