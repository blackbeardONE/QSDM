package cc

// verifier.go — production nvidia-cc-v1 verifier.
//
// This file implements the §3.2 acceptance flow from
// MINING_PROTOCOL_V2.md as a concrete
// mining.AttestationVerifier. It is the consensus-side half
// of the datacenter / Confidential Computing path, in
// contrast to pkg/mining/attest/hmac (consumer-GPU HMAC).
//
// Step ordering mirrors the spec one-to-one:
//
//   1. Parse bundle → ErrAttestationBundleMalformed
//   2. Recompute canonicalPreimage(bundle, proof); cross-check
//      bundle.Quote fields against Proof.Attestation
//      → ErrAttestationNonceMismatch / ErrAttestationBundleMalformed
//   3. Walk cert chain rooted in genesis-pinned NVIDIA CA
//      → ErrAttestationSignatureInvalid
//   4. ECDSA-verify quote signature against leaf cert pubkey
//      → ErrAttestationSignatureInvalid
//   5. Optional: cross-validate challenge_sig with
//      ChallengeVerifier (mirrors HMAC path 6a-ii)
//   6. Freshness window
//      → ErrAttestationStale
//   7. Replay cache (NonceStore keyed on device_uuid + nonce)
//      → ErrAttestationNonceMismatch
//   8. PCR floor (firmware/driver minimums)
//      → ErrAttestationSignatureInvalid
//   9. Arch ↔ leaf cert subject consistency (§4.6.5)
//      → ErrAttestationSignatureInvalid (wraps
//        archcheck.ErrArchCertSubjectMismatch)
//
// Step 8 is the only step that doesn't have a perfectly
// natural error sentinel — a downgraded firmware isn't
// strictly a "signature invalid", but ErrAttestationSignatureInvalid
// is the closest fit semantically (the bundle's claim of
// running on attested firmware is what's being rejected).
// We surface a wrapping message so log readers see exactly
// what failed.

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/archcheck"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
)

// PinnedRoot is a single genesis-trusted NVIDIA CA. The
// verifier loads zero or more of these and rejects any cert
// chain that does NOT terminate in one of them. Genesis state
// pins these by DER bytes; governance can add new ones via
// chain transactions in later phases.
type PinnedRoot struct {
	// Subject is a human-readable label used for log lines
	// and metrics. Not consensus-critical (the DER bytes are).
	Subject string

	// DER is the DER-encoded x509 root certificate. The
	// verifier parses this into *x509.Certificate at config
	// time; a malformed root fails NewVerifier rather than
	// silently being skipped.
	DER []byte
}

// MinFirmware is the lower bound the verifier enforces on
// the (firmware_ver, driver_ver) pair reported in the bundle's
// PCR section. Zero-valued strings disable the check for that
// component, which is appropriate for pre-production / test
// validators but never for mainnet.
type MinFirmware struct {
	Firmware string
	Driver   string
}

// VerifierConfig assembles the collaborators a production
// nvidia-cc-v1 verifier needs. The zero value is INVALID —
// NewVerifier returns an error rather than silently defaulting
// any consensus-critical field.
type VerifierConfig struct {
	// PinnedRoots is the trust anchor set. At least one is
	// REQUIRED; without it every chain will fail to verify.
	// Operators populate this from the genesis block in
	// production.
	PinnedRoots []PinnedRoot

	// MinFirmware enforces the firmware/driver floor. Zero-
	// valued strings disable the corresponding component.
	MinFirmware MinFirmware

	// NonceStore detects replays. Optional in tests; REQUIRED
	// in production wiring (see attest.ProductionConfig).
	// Keyed by device_uuid (which is what the spec keys
	// on for CC; the spec keys HMAC by node_id).
	NonceStore hmac.NonceStore

	// FreshnessWindow overrides mining.FreshnessWindow.
	// Zero = use the spec value.
	FreshnessWindow time.Duration

	// AllowedFutureSkew is the maximum delta for issued_at
	// in the future relative to the verifier's clock. Zero =
	// 5 seconds (matches hmac.NewVerifier default).
	AllowedFutureSkew time.Duration

	// ChallengeVerifier optionally cross-verifies the
	// (nonce, issued_at) challenge signature carried in the
	// bundle. Mirrors hmac.Verifier.ChallengeVerifier.
	// Production wiring REQUIRES this; bring-up tests can
	// leave it nil.
	ChallengeVerifier challenge.SignerVerifier
}

// Verifier is a fully-configured nvidia-cc-v1 verifier.
// Stateless aside from collaborators; safe for concurrent
// VerifyAttestation calls.
type Verifier struct {
	roots             *x509.CertPool
	rootSubjects      []string // for log/error messages
	minFirmware       MinFirmware
	nonceStore        hmac.NonceStore
	freshness         time.Duration
	allowedFutureSkew time.Duration
	challengeVerifier challenge.SignerVerifier
}

// NewVerifier validates cfg and returns a ready-to-use
// Verifier. Returns an error if PinnedRoots is empty or any
// root fails DER parsing.
func NewVerifier(cfg VerifierConfig) (*Verifier, error) {
	if len(cfg.PinnedRoots) == 0 {
		return nil, errors.New("cc: VerifierConfig.PinnedRoots must contain at least one root")
	}
	pool := x509.NewCertPool()
	subjects := make([]string, 0, len(cfg.PinnedRoots))
	for i, r := range cfg.PinnedRoots {
		if len(r.DER) == 0 {
			return nil, fmt.Errorf("cc: PinnedRoots[%d].DER empty", i)
		}
		cert, err := x509.ParseCertificate(r.DER)
		if err != nil {
			return nil, fmt.Errorf("cc: PinnedRoots[%d] (subject=%q) parse: %w",
				i, r.Subject, err)
		}
		pool.AddCert(cert)
		label := r.Subject
		if label == "" {
			label = cert.Subject.CommonName
		}
		subjects = append(subjects, label)
	}
	freshness := cfg.FreshnessWindow
	if freshness <= 0 {
		freshness = mining.FreshnessWindow
	}
	skew := cfg.AllowedFutureSkew
	if skew <= 0 {
		skew = 5 * time.Second
	}
	return &Verifier{
		roots:             pool,
		rootSubjects:      subjects,
		minFirmware:       cfg.MinFirmware,
		nonceStore:        cfg.NonceStore,
		freshness:         freshness,
		allowedFutureSkew: skew,
		challengeVerifier: cfg.ChallengeVerifier,
	}, nil
}

// PinnedRootSubjects returns a defensive copy of the human-
// readable trust-anchor labels the verifier was configured
// with. Useful for startup banners.
func (v *Verifier) PinnedRootSubjects() []string {
	out := make([]string, len(v.rootSubjects))
	copy(out, v.rootSubjects)
	return out
}

// VerifyAttestation implements mining.AttestationVerifier for
// Attestation.Type == "nvidia-cc-v1". Returns nil on
// acceptance, or an error wrapping the appropriate
// mining.ErrAttestation* sentinel on rejection.
func (v *Verifier) VerifyAttestation(p mining.Proof, now time.Time) error {
	// Defensive: dispatcher should have routed by type before
	// reaching us, but we re-check so this verifier is safe to
	// invoke standalone.
	if p.Attestation.Type != mining.AttestationTypeCC {
		return fmt.Errorf("cc: got attestation type %q want %q: %w",
			p.Attestation.Type, mining.AttestationTypeCC,
			mining.ErrAttestationTypeUnknown)
	}

	// Step 1: parse bundle.
	b, err := ParseBundle(p.Attestation.BundleBase64)
	if err != nil {
		return fmt.Errorf("cc: %v: %w", err, mining.ErrAttestationBundleMalformed)
	}

	// Step 2: nonce / issued_at consistency between bundle
	// and outer Attestation. A bundle whose inner challenge
	// nonce ≠ outer Attestation.Nonce is either a swap attempt
	// or operator misuse — reject early so the more
	// expensive crypto checks below don't run.
	innerNonce, err := hex.DecodeString(b.Quote.ChallengeNonce)
	if err != nil || len(innerNonce) != 32 {
		return fmt.Errorf("cc: quote.challenge_nonce malformed: %w",
			mining.ErrAttestationBundleMalformed)
	}
	if !bytesEqual32(innerNonce, p.Attestation.Nonce[:]) {
		return fmt.Errorf("cc: bundle.challenge_nonce != Attestation.Nonce: %w",
			mining.ErrAttestationNonceMismatch)
	}
	if b.Quote.IssuedAt != p.Attestation.IssuedAt {
		return fmt.Errorf("cc: bundle.issued_at (%d) != Attestation.IssuedAt (%d): %w",
			b.Quote.IssuedAt, p.Attestation.IssuedAt,
			mining.ErrAttestationNonceMismatch)
	}

	// Step 3: cert chain validation. We verify the LEAF
	// against the genesis-pinned roots, with any intermediate
	// certs supplied as Intermediates. We pin the verification
	// time to `now` so a leaf cert that's expired at consensus
	// time is rejected even if the validator's wall clock
	// differs from the miner's.
	leafDER, err := base64.StdEncoding.DecodeString(b.CertChain[0])
	if err != nil {
		return fmt.Errorf("cc: cert_chain[0] base64: %w",
			mining.ErrAttestationBundleMalformed)
	}
	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		return fmt.Errorf("cc: cert_chain[0] parse: %v: %w",
			err, mining.ErrAttestationBundleMalformed)
	}
	intermediates := x509.NewCertPool()
	for i := 1; i < len(b.CertChain); i++ {
		der, err := base64.StdEncoding.DecodeString(b.CertChain[i])
		if err != nil {
			return fmt.Errorf("cc: cert_chain[%d] base64: %w",
				i, mining.ErrAttestationBundleMalformed)
		}
		c, err := x509.ParseCertificate(der)
		if err != nil {
			return fmt.Errorf("cc: cert_chain[%d] parse: %v: %w",
				i, err, mining.ErrAttestationBundleMalformed)
		}
		intermediates.AddCert(c)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:         v.roots,
		Intermediates: intermediates,
		CurrentTime:   now,
		// Empty KeyUsages list means the verifier accepts any
		// extKeyUsage on the leaf; the NVIDIA AIK leaf
		// usually has no extKeyUsage extension, so a strict
		// list would reject every real chain.
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		return fmt.Errorf("cc: cert chain verify: %v: %w",
			err, mining.ErrAttestationSignatureInvalid)
	}

	// Step 4: ECDSA quote signature. Reconstruct the canonical
	// preimage from the Bundle (challenge fields) plus the
	// enclosing Proof (miner_addr / batch_root / mix_digest)
	// and verify against the leaf cert's public key.
	pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("cc: leaf public key is %T, want *ecdsa.PublicKey: %w",
			leaf.PublicKey, mining.ErrAttestationSignatureInvalid)
	}
	csigBytes, err := hex.DecodeString(b.Quote.ChallengeSig)
	if err != nil {
		return fmt.Errorf("cc: quote.challenge_sig hex: %w",
			mining.ErrAttestationBundleMalformed)
	}
	preimage, err := canonicalPreimage(PreimageInputs{
		DeviceUUID:        b.DeviceUUID,
		ChallengeNonce:    p.Attestation.Nonce,
		IssuedAt:          p.Attestation.IssuedAt,
		MinerAddr:         p.MinerAddr,
		BatchRoot:         p.BatchRoot,
		MixDigest:         p.MixDigest,
		ChallengeSignerID: b.Quote.ChallengeSignerID,
		ChallengeSig:      csigBytes,
	})
	if err != nil {
		return fmt.Errorf("cc: build preimage: %v: %w",
			err, mining.ErrAttestationBundleMalformed)
	}
	digest := sha256.Sum256(preimage)
	sigBytes, err := base64.StdEncoding.DecodeString(b.Quote.Signature)
	if err != nil {
		return fmt.Errorf("cc: quote.signature base64: %w",
			mining.ErrAttestationBundleMalformed)
	}
	if !ecdsa.VerifyASN1(pub, digest[:], sigBytes) {
		return fmt.Errorf("cc: AIK signature verification failed: %w",
			mining.ErrAttestationSignatureInvalid)
	}

	// Step 5: optional challenge-sig cross-check via the
	// validator's challenge.SignerVerifier. Mirrors hmac
	// Step 6a-ii: even if a leaked AIK signs the bundle, an
	// invalid challenge_sig means the (nonce, issued_at) pair
	// wasn't actually issued by a known validator.
	if v.challengeVerifier != nil {
		var nonceArr [32]byte
		copy(nonceArr[:], innerNonce)
		chg := challenge.Challenge{
			Nonce:     nonceArr,
			IssuedAt:  b.Quote.IssuedAt,
			SignerID:  b.Quote.ChallengeSignerID,
			Signature: csigBytes,
		}
		if err := v.challengeVerifier.VerifySignature(
			chg.SignerID, chg.SigningBytes(), chg.Signature,
		); err != nil {
			return fmt.Errorf("cc: challenge signature: %w: %w",
				err, mining.ErrAttestationSignatureInvalid)
		}
	}

	// Step 6: freshness window.
	issued := time.Unix(b.Quote.IssuedAt, 0)
	age := now.Sub(issued)
	if age > v.freshness {
		return fmt.Errorf("cc: attestation age %v > window %v: %w",
			age, v.freshness, mining.ErrAttestationStale)
	}
	if age < -v.allowedFutureSkew {
		return fmt.Errorf("cc: attestation issued %v in future (max skew %v): %w",
			-age, v.allowedFutureSkew, mining.ErrAttestationStale)
	}

	// Step 7: replay cache. Keyed on (device_uuid, nonce) —
	// distinct from the HMAC path which keys on (node_id,
	// nonce). The CC path has no node_id; the GPU UUID is
	// the strongest stable identity. We Record AFTER all
	// other checks pass so half-failing proofs don't burn a
	// nonce.
	if v.nonceStore != nil {
		var nonceBuf [32]byte
		copy(nonceBuf[:], innerNonce)
		if v.nonceStore.Seen(b.DeviceUUID, nonceBuf) {
			return fmt.Errorf("cc: nonce already used by device %s: %w",
				b.DeviceUUID, mining.ErrAttestationNonceMismatch)
		}
		v.nonceStore.Record(b.DeviceUUID, nonceBuf, now)
	}

	// Step 8: PCR floor. Lex-compare with dotted-numeric
	// awareness so "535.86.10" beats "535.86.9" the way an
	// operator expects, not the way naive string compare
	// would (which would say "9" > "10").
	if v.minFirmware.Firmware != "" {
		if cmp, err := compareDottedNumeric(b.PCR.FirmwareVer, v.minFirmware.Firmware); err != nil {
			return fmt.Errorf("cc: pcr.firmware_ver %q unparseable: %v: %w",
				b.PCR.FirmwareVer, err, mining.ErrAttestationSignatureInvalid)
		} else if cmp < 0 {
			return fmt.Errorf("cc: pcr.firmware_ver %s < min %s: %w",
				b.PCR.FirmwareVer, v.minFirmware.Firmware,
				mining.ErrAttestationSignatureInvalid)
		}
	}
	if v.minFirmware.Driver != "" {
		if cmp, err := compareDottedNumeric(b.PCR.DriverVer, v.minFirmware.Driver); err != nil {
			return fmt.Errorf("cc: pcr.driver_ver %q unparseable: %v: %w",
				b.PCR.DriverVer, err, mining.ErrAttestationSignatureInvalid)
		} else if cmp < 0 {
			return fmt.Errorf("cc: pcr.driver_ver %s < min %s: %w",
				b.PCR.DriverVer, v.minFirmware.Driver,
				mining.ErrAttestationSignatureInvalid)
		}
	}

	// Step 9 (MINING_PROTOCOL_V2 §4.6.5): leaf cert subject
	// ↔ arch consistency. The outer Verifier (pkg/mining/
	// verifier.go) has already canonicalised
	// p.Attestation.GPUArch via archcheck.ValidateOuterArch
	// before dispatching here, so for any post-fork v2 proof
	// arriving via the dispatcher GPUArch is guaranteed to be
	// canonical or alias-valid. A standalone caller that
	// invokes VerifyAttestation directly with an empty GPUArch
	// (test fixtures, diagnostic tooling, pre-fork bring-up
	// vectors) skips this check — the cert-chain pin (step 3)
	// + AIK signature (step 4) remain the cryptographic
	// locks.
	//
	// We pass leaf.Subject.CommonName as the candidate string
	// today. Production NVIDIA AIK certs carry the GPU model
	// in CN; if a future revision moves the model into a
	// custom OID extension or an OU field, ValidateBundleArch
	// ConsistencyCC's evidence-based rule lets us extend the
	// candidate string set (e.g. CN + " " + OU joined) without
	// re-shaping the package boundary.
	if p.Attestation.GPUArch != "" {
		arch, ok := archcheck.Canonicalise(p.Attestation.GPUArch)
		if !ok {
			// Defensive: outer verifier should have rejected
			// this already with ErrArchUnknown. If we reach
			// here standalone, surface as a signature-invalid
			// (the bundle's claim of arch X cannot be
			// validated).
			return fmt.Errorf("cc: gpu_arch %q not in allowlist: %w",
				p.Attestation.GPUArch, mining.ErrAttestationSignatureInvalid)
		}
		if err := archcheck.ValidateBundleArchConsistencyCC(
			arch, leaf.Subject.CommonName,
		); err != nil {
			// Double-wrap so callers can errors.Is against
			// EITHER the archcheck sentinel (for the
			// dedicated archspoof_rejected{cc_subject_mismatch}
			// counter) OR the mining sentinel (for generic
			// signature-invalid grouping). Go 1.20+ %w / %w
			// multiple wrap.
			return fmt.Errorf("cc: arch consistency: %w: %w",
				err, mining.ErrAttestationSignatureInvalid)
		}
	}

	return nil
}

// compareDottedNumeric compares two version strings of the
// form "A.B.C..." numerically per dotted-component. Returns
// -1 / 0 / +1 in the usual sense, or an error if either side
// has a non-numeric component.
//
// We explicitly DON'T support pre-release tags or build
// metadata (semver "1.2.3-rc1"). NVIDIA driver/firmware
// strings in the wild are always pure dotted-numeric in the
// reports the CC subsystem emits; supporting semver here
// would only widen the attack surface.
func compareDottedNumeric(a, b string) (int, error) {
	pa := strings.Split(a, ".")
	pb := strings.Split(b, ".")
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var ai, bi uint64
		var err error
		if i < len(pa) {
			ai, err = strconv.ParseUint(pa[i], 10, 32)
			if err != nil {
				return 0, fmt.Errorf("component %d of %q: %w", i, a, err)
			}
		}
		if i < len(pb) {
			bi, err = strconv.ParseUint(pb[i], 10, 32)
			if err != nil {
				return 0, fmt.Errorf("component %d of %q: %w", i, b, err)
			}
		}
		if ai < bi {
			return -1, nil
		}
		if ai > bi {
			return 1, nil
		}
	}
	return 0, nil
}

// bytesEqual32 is a small helper that tolerates the common
// case where one side is a slice and the other a [32]byte
// without forcing the caller to slice or copy.
func bytesEqual32(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// Compile-time guard.
var _ mining.AttestationVerifier = (*Verifier)(nil)
