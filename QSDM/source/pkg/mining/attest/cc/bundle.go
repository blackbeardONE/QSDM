package cc

// bundle.go — wire format for the nvidia-cc-v1 attestation
// payload, plus the canonical preimage hash that the GPU's AIK
// signs over.
//
// Why we ship our own encoding rather than re-using NVIDIA's
// nvtrust binary frame: the on-chain consensus path needs a
// deterministic, byte-identical canonicalisation across every
// validator, every Go version, and every GOARCH. NVIDIA's
// nvtrust frame is technically deterministic but the framing
// is a moving target across CC SDK releases. Pinning a Go-
// native shape gives us:
//
//   - reproducible test vectors with no external fixtures
//     (testvectors.go generates them from a seeded PRNG)
//   - a one-line audit step for "is the preimage exactly the
//     spec tuple?" (canonicalPreimage below)
//   - a clear seam to swap in real nvtrust framing later: the
//     swap is a single ParseBundle reimplementation, the
//     verifier code never changes
//
// The shape MIRRORS what an nvtrust quote actually contains —
// device cert chain, AIK signature, firmware/driver version
// strings — so the consensus checks (chain rooted in pinned
// NVIDIA CA, signature verifies, PCR floor met) are real and
// testable today, not deferred behind a stub.

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// MaxCertChainLen caps the number of certs the verifier will
// parse in a bundle. NVIDIA's CC chains are typically 3-4
// deep (leaf → intermediate → NVIDIA issuing CA → root). A
// pathologically deep chain would let an attacker burn CPU
// during chain validation; 8 is generous enough to absorb
// future schema additions but tight enough to block DoS.
const MaxCertChainLen = 8

// MaxBundleBytes caps the size of the base64-decoded bundle
// JSON. AIK signatures are ~96 bytes (P-384) or ~72 bytes
// (P-256); cert chains are typically <8 KiB; the rest of the
// fields are tiny. 64 KiB is ~10× the realistic ceiling.
const MaxBundleBytes = 64 << 10

// MinerAddrMaxLen bounds the miner_addr field inside the
// preimage. Has to match (or exceed) the cap pkg/mining
// already enforces on Proof.MinerAddr. The address strings
// today are ~50 chars; 256 leaves room for future schemes
// without blowing past uint16 length-prefix territory.
const MinerAddrMaxLen = 256

// ChallengeSignerIDMaxLen bounds the validator identifier
// embedded in the preimage. Validator identifiers in the
// challenge package are short stable strings; 128 is generous.
const ChallengeSignerIDMaxLen = 128

// ChallengeSigMaxLen bounds the challenge signature. HMAC-
// SHA256 is 32 bytes; future Dilithium signatures top out
// around 4-5 KiB. 8 KiB is a comfortable upper bound.
const ChallengeSigMaxLen = 8 << 10

// Bundle is the in-memory shape of the base64-encoded JSON
// blob carried in Proof.Attestation.BundleBase64 when
// Attestation.Type == "nvidia-cc-v1".
//
// Field ordering in the struct mirrors human-reading order;
// canonical encoding is by JSON key order (alphabetical),
// produced via json.Marshal of a sorted map. Operators must
// NOT hand-roll JSON for this struct — round-trip through
// EncodeBundle / ParseBundle to stay canonical.
type Bundle struct {
	// DeviceUUID is the GPU's hardware UUID, encoded as a
	// 16-byte hex string. MUST match the UUID baked into the
	// quote preimage.
	DeviceUUID string `json:"device_uuid"`

	// CertChain is the per-GPU device certificate chain,
	// LEAF FIRST. Each entry is a base64-encoded DER blob.
	// The root MUST be one of the genesis-pinned NVIDIA CA
	// certs in VerifierConfig.PinnedRoots; otherwise the
	// chain is rejected at step 1 of the verifier.
	CertChain []string `json:"cert_chain"`

	// Quote is the AIK signature + the fields the AIK signed
	// over. The verifier reconstructs the canonical preimage
	// from these fields plus the enclosing Proof and verifies
	// the signature against CertChain[0]'s public key.
	Quote QuoteV1 `json:"quote"`

	// PCR carries the firmware/driver version strings the
	// GPU's CC subsystem reports. The verifier rejects any
	// bundle whose versions fall below VerifierConfig.MinFirmware,
	// preventing a downgrade-to-vulnerable-firmware attack.
	PCR PCRMeasurementsV1 `json:"pcr"`
}

// QuoteV1 carries the AIK signature plus the SUBSET of the
// preimage fields that the bundle commits to redundantly.
// The remaining preimage fields come from the enclosing Proof
// (so a forged bundle can't drift from the proof being
// attested). Holding both lets the verifier produce a clear
// "preimage mismatch" error when the operator tries to ship a
// signature for proof A while the bundle claims proof B.
type QuoteV1 struct {
	// ChallengeNonce mirrors Proof.Attestation.Nonce. The
	// verifier rejects mismatch with ErrAttestationNonceMismatch.
	ChallengeNonce string `json:"challenge_nonce"`

	// IssuedAt mirrors Proof.Attestation.IssuedAt.
	IssuedAt int64 `json:"issued_at"`

	// ChallengeSignerID is the validator identifier whose key
	// signed the (nonce, issued_at) challenge. The verifier
	// can optionally cross-check this against its
	// challenge.SignerVerifier (mirroring the HMAC path).
	ChallengeSignerID string `json:"challenge_signer_id"`

	// ChallengeSig is the validator signature over the
	// (nonce, issued_at) challenge — same value the miner
	// fetched from /api/v1/mining/challenge. Hex-encoded.
	ChallengeSig string `json:"challenge_sig"`

	// Signature is the AIK signature (ASN.1 ECDSA per X9.62)
	// over canonicalPreimage. Base64-encoded. The verifier
	// uses crypto/ecdsa.VerifyASN1 against CertChain[0]'s
	// SubjectPublicKeyInfo.
	Signature string `json:"signature"`
}

// PCRMeasurementsV1 mirrors the "PCR-equivalent measurements"
// the spec calls for: firmware + driver version strings. The
// verifier compares them against VerifierConfig.MinFirmware
// (lex-compared after dotted-numeric normalisation; see
// compareDottedNumeric).
type PCRMeasurementsV1 struct {
	FirmwareVer string `json:"firmware_ver"`
	DriverVer   string `json:"driver_ver"`
}

// EncodeBundle returns the base64-encoded canonical JSON
// suitable for placement into Proof.Attestation.BundleBase64.
// CallSites that build bundles (test vectors, future on-host
// quote producers) MUST round-trip through this function to
// stay canonical.
func EncodeBundle(b Bundle) (string, error) {
	if err := b.validate(); err != nil {
		return "", fmt.Errorf("cc: encode: %w", err)
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return "", fmt.Errorf("cc: encode: marshal: %w", err)
	}
	if len(raw) > MaxBundleBytes {
		return "", fmt.Errorf("cc: encode: bundle %d bytes exceeds %d cap",
			len(raw), MaxBundleBytes)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// ParseBundle decodes Proof.Attestation.BundleBase64 into a
// Bundle. Strict-mode JSON decoding is used so an attacker
// can't smuggle extra fields the verifier ignores: any
// unknown field is a parse error wrapping
// ErrAttestationBundleMalformed at the call site.
func ParseBundle(b64 string) (Bundle, error) {
	if b64 == "" {
		return Bundle{}, errors.New("cc: bundle base64 empty")
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return Bundle{}, fmt.Errorf("cc: bundle base64 decode: %w", err)
	}
	if len(raw) > MaxBundleBytes {
		return Bundle{}, fmt.Errorf("cc: bundle %d bytes exceeds %d cap",
			len(raw), MaxBundleBytes)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var out Bundle
	if err := dec.Decode(&out); err != nil {
		return Bundle{}, fmt.Errorf("cc: bundle json: %w", err)
	}
	if err := out.validate(); err != nil {
		return Bundle{}, fmt.Errorf("cc: bundle: %w", err)
	}
	return out, nil
}

// validate enforces shallow shape invariants. Caller (the
// Verifier) is responsible for the cryptographic and
// consensus-level checks.
func (b Bundle) validate() error {
	if b.DeviceUUID == "" {
		return errors.New("device_uuid empty")
	}
	if _, err := hex.DecodeString(b.DeviceUUID); err != nil {
		return fmt.Errorf("device_uuid not hex: %w", err)
	}
	if len(b.CertChain) == 0 {
		return errors.New("cert_chain empty")
	}
	if len(b.CertChain) > MaxCertChainLen {
		return fmt.Errorf("cert_chain %d entries exceeds %d cap",
			len(b.CertChain), MaxCertChainLen)
	}
	if b.Quote.ChallengeNonce == "" {
		return errors.New("quote.challenge_nonce empty")
	}
	if n, err := hex.DecodeString(b.Quote.ChallengeNonce); err != nil || len(n) != 32 {
		return fmt.Errorf("quote.challenge_nonce must be 32 hex bytes")
	}
	if b.Quote.IssuedAt <= 0 {
		return fmt.Errorf("quote.issued_at must be positive, got %d", b.Quote.IssuedAt)
	}
	if b.Quote.ChallengeSignerID == "" {
		return errors.New("quote.challenge_signer_id empty")
	}
	if len(b.Quote.ChallengeSignerID) > ChallengeSignerIDMaxLen {
		return fmt.Errorf("quote.challenge_signer_id %d bytes exceeds %d cap",
			len(b.Quote.ChallengeSignerID), ChallengeSignerIDMaxLen)
	}
	if b.Quote.ChallengeSig == "" {
		return errors.New("quote.challenge_sig empty")
	}
	if sig, err := hex.DecodeString(b.Quote.ChallengeSig); err != nil ||
		len(sig) == 0 || len(sig) > ChallengeSigMaxLen {
		return fmt.Errorf("quote.challenge_sig must be non-empty hex up to %d bytes",
			ChallengeSigMaxLen)
	}
	if b.Quote.Signature == "" {
		return errors.New("quote.signature empty")
	}
	if _, err := base64.StdEncoding.DecodeString(b.Quote.Signature); err != nil {
		return fmt.Errorf("quote.signature not base64: %w", err)
	}
	if b.PCR.FirmwareVer == "" || b.PCR.DriverVer == "" {
		return errors.New("pcr.firmware_ver and pcr.driver_ver are required")
	}
	return nil
}

// PreimageInputs gathers the fields the AIK quote signs over.
// The verifier builds this from (Bundle, Proof) at verify
// time; the test-vector helper builds it from explicit args.
//
// CRITICAL: every consensus-relevant field a forged bundle
// could swap MUST appear here. If a field is in the proof but
// NOT in this struct, an attacker who steals one valid AIK
// signature can re-attach it to a different proof.
type PreimageInputs struct {
	DeviceUUID        string  // hex
	ChallengeNonce    [32]byte // outer Attestation.Nonce
	IssuedAt          int64
	MinerAddr         string
	BatchRoot         [32]byte
	MixDigest         [32]byte
	ChallengeSignerID string
	ChallengeSig      []byte
}

// canonicalPreimage produces the exact byte string the AIK
// signs over (and the verifier re-constructs to check the
// signature).
//
// Layout (length-prefixed where variable, big-endian where
// integer):
//
//	device_uuid_bytes        (16 bytes; the hex-decoded UUID)
//	challenge_nonce          (32 bytes)
//	issued_at                (int64 BE, 8 bytes)
//	uint16(len(miner_addr))  || miner_addr bytes
//	batch_root               (32 bytes)
//	mix_digest               (32 bytes)
//	uint16(len(signer_id))   || signer_id bytes
//	uint16(len(challenge_sig)) || challenge_sig bytes
//
// The format is deliberately NOT JSON: the AIK doesn't speak
// JSON, and a length-prefixed binary frame makes
// "what bytes did the AIK sign?" a one-line audit answer.
//
// Returns the preimage bytes, or an error if any field is
// over its declared length cap.
func canonicalPreimage(in PreimageInputs) ([]byte, error) {
	uuidBytes, err := hex.DecodeString(in.DeviceUUID)
	if err != nil {
		return nil, fmt.Errorf("device_uuid not hex: %w", err)
	}
	if len(uuidBytes) != 16 {
		return nil, fmt.Errorf("device_uuid %d bytes, want 16", len(uuidBytes))
	}
	if len(in.MinerAddr) > MinerAddrMaxLen {
		return nil, fmt.Errorf("miner_addr %d bytes exceeds %d cap",
			len(in.MinerAddr), MinerAddrMaxLen)
	}
	if len(in.ChallengeSignerID) > ChallengeSignerIDMaxLen {
		return nil, fmt.Errorf("challenge_signer_id %d bytes exceeds %d cap",
			len(in.ChallengeSignerID), ChallengeSignerIDMaxLen)
	}
	if len(in.ChallengeSig) > ChallengeSigMaxLen {
		return nil, fmt.Errorf("challenge_sig %d bytes exceeds %d cap",
			len(in.ChallengeSig), ChallengeSigMaxLen)
	}
	// Pre-size the buffer so we don't grow during the build.
	// 16 + 32 + 8 + 2 + N + 32 + 32 + 2 + M + 2 + S
	buf := make([]byte, 0,
		16+32+8+2+len(in.MinerAddr)+32+32+2+len(in.ChallengeSignerID)+
			2+len(in.ChallengeSig))
	buf = append(buf, uuidBytes...)
	buf = append(buf, in.ChallengeNonce[:]...)
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(in.IssuedAt))
	buf = append(buf, ts[:]...)
	var addrLen [2]byte
	binary.BigEndian.PutUint16(addrLen[:], uint16(len(in.MinerAddr)))
	buf = append(buf, addrLen[:]...)
	buf = append(buf, in.MinerAddr...)
	buf = append(buf, in.BatchRoot[:]...)
	buf = append(buf, in.MixDigest[:]...)
	var sidLen [2]byte
	binary.BigEndian.PutUint16(sidLen[:], uint16(len(in.ChallengeSignerID)))
	buf = append(buf, sidLen[:]...)
	buf = append(buf, in.ChallengeSignerID...)
	var csigLen [2]byte
	binary.BigEndian.PutUint16(csigLen[:], uint16(len(in.ChallengeSig)))
	buf = append(buf, csigLen[:]...)
	buf = append(buf, in.ChallengeSig...)
	return buf, nil
}
