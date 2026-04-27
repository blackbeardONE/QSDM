package hmac

// This file implements the 9-step acceptance flow from
// MINING_PROTOCOL_V2.md §3.3 as a concrete
// implementation of pkg/mining.AttestationVerifier. It is the
// consensus-critical half of the consumer-GPU attestation path —
// every byte that reaches this code has already been accepted as
// well-formed v2 by the pkg/mining Verifier's Step-1 gate.
//
// The code is linear by design: each step checks one invariant
// and returns on the first failure. This is NOT a place for
// cleverness — the flow maps one-to-one to the spec so a
// side-by-side audit stays legible.
//
// Step 8 (Tensor-Core mix_digest ↔ claimed gpu_arch cross-check)
// is deferred to Phase 2c-iv. That check is run in
// pkg/mining/pow.go against the PoW mixin, not here; duplicating
// it would couple two code paths that should stay independent.
// The spec permits this factoring — step 8 guards consistency
// between the attestation's self-reported arch and the actual
// Tensor-Core workload, which is a PoW property.

import (
	"bytes"
	"crypto/hmac"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
)

// Verifier implements mining.AttestationVerifier for the
// "nvidia-hmac-v1" Attestation.Type. It is stateless aside from
// the injected collaborators; multiple validator goroutines can
// call VerifyAttestation concurrently.
type Verifier struct {
	// Registry resolves node_id -> (gpu_uuid, hmac_key). Required.
	Registry Registry

	// NonceStore detects replays within 2*FRESHNESS_WINDOW.
	// Optional: if nil, replay detection is skipped and the
	// verifier relies solely on the freshness-window check. Tests
	// that only want to cover the crypto path can leave this nil.
	// Production validators MUST wire in a real NonceStore.
	NonceStore NonceStore

	// DenyList filters gpu_name against governance-appended
	// strings. Optional: defaults to EmptyDenyList (the genesis
	// state).
	DenyList DenyList

	// FreshnessWindow overrides mining.FreshnessWindow for tests.
	// Zero means use mining.FreshnessWindow.
	FreshnessWindow time.Duration

	// AllowedFutureSkew is the maximum delta the verifier tolerates
	// for a nonce whose issued_at is slightly in the future
	// relative to our clock. Clocks disagree; the spec doesn't
	// pin this value, so we default to 5 seconds — small enough
	// that a replay attacker can't use it as a time-travel
	// amplifier, large enough to absorb ordinary NTP skew.
	AllowedFutureSkew time.Duration

	// ChallengeVerifier cryptographically authenticates the
	// (nonce, issued_at) pair back to a known validator. When
	// non-nil, the verifier rejects any bundle whose
	// challenge_sig / challenge_signer_id fields don't produce a
	// valid signature over the same (nonce, issued_at) the miner
	// committed to. When nil, this check is skipped and
	// freshness-window + replay-cache are the sole anti-replay
	// defences — acceptable for bring-up and isolated testnets
	// but NEVER for production.
	ChallengeVerifier challenge.SignerVerifier
}

// NewVerifier constructs a Verifier with the required collaborator
// (Registry) and sensible defaults for the others. Callers wire in
// NonceStore and ChallengeVerifier for production — both are nil
// by default because some test paths intentionally exercise the
// skip-these-checks behaviour.
func NewVerifier(registry Registry) *Verifier {
	return &Verifier{
		Registry:          registry,
		NonceStore:        nil,
		DenyList:          EmptyDenyList{},
		FreshnessWindow:   mining.FreshnessWindow,
		AllowedFutureSkew: 5 * time.Second,
		ChallengeVerifier: nil,
	}
}

// VerifyAttestation implements mining.AttestationVerifier. Returns
// nil on acceptance, or an error wrapping one of the
// mining.ErrAttestation* sentinels on rejection. The error message
// is informative but not sensitive — no HMAC key material and no
// bundle contents that would help an attacker shape a second
// attempt appear in the output.
func (v *Verifier) VerifyAttestation(p mining.Proof, now time.Time) error {
	// Defensive: the outer Verifier should have filtered by type
	// before dispatch, but we re-check so this package is safe to
	// use standalone (tests, future replay tools).
	if p.Attestation.Type != mining.AttestationTypeHMAC {
		return fmt.Errorf("hmac: got attestation type %q want %q: %w",
			p.Attestation.Type, mining.AttestationTypeHMAC, mining.ErrAttestationTypeUnknown)
	}
	if v.Registry == nil {
		return fmt.Errorf("hmac: Verifier.Registry is nil: %w", mining.ErrAttestationSignatureInvalid)
	}

	// Step 1: parse bundle.
	bundle, err := ParseBundle(p.Attestation.BundleBase64)
	if err != nil {
		return fmt.Errorf("hmac: %v: %w", err, mining.ErrAttestationBundleMalformed)
	}

	// Step 2: challenge-bind. Recompute sha256(miner_addr ||
	// batch_root || mix_digest) and compare hex-decoded bundle
	// field. Mismatch means the bundle was signed for a different
	// proof (replay or substitution).
	wantBind := ComputeChallengeBind(p.MinerAddr, p.BatchRoot, p.MixDigest)
	gotBind, err := hex.DecodeString(bundle.ChallengeBind)
	if err != nil || len(gotBind) != 32 {
		return fmt.Errorf("hmac: challenge_bind not 32 hex bytes: %w", mining.ErrAttestationBundleMalformed)
	}
	if !bytes.Equal(gotBind, wantBind[:]) {
		return fmt.Errorf("hmac: challenge_bind mismatch: %w", mining.ErrAttestationSignatureInvalid)
	}

	// Step 3: registry lookup. Unknown or revoked node → reject.
	// Use %w twice (Go 1.20+) so callers can errors.Is against
	// BOTH the specific registry sentinel (ErrNodeRevoked,
	// ErrNodeNotRegistered) AND the consensus-level
	// mining.ErrAttestationSignatureInvalid.
	entry, err := v.Registry.Lookup(bundle.NodeID)
	if err != nil {
		return fmt.Errorf("hmac: registry lookup failed: %w: %w", err, mining.ErrAttestationSignatureInvalid)
	}
	if entry == nil {
		return fmt.Errorf("hmac: registry returned nil entry for %q: %w", bundle.NodeID, mining.ErrAttestationSignatureInvalid)
	}

	// Step 4: HMAC check. Recompute the MAC over the canonical
	// form (minus hmac field) and compare in constant time.
	canonical, err := bundle.CanonicalForMAC()
	if err != nil {
		return fmt.Errorf("hmac: canonical encode: %v: %w", err, mining.ErrAttestationBundleMalformed)
	}
	gotMAC, err := hex.DecodeString(bundle.HMAC)
	if err != nil || len(gotMAC) != 32 {
		return fmt.Errorf("hmac: hmac field not 32 hex bytes: %w", mining.ErrAttestationBundleMalformed)
	}
	wantMAC := ComputeMAC(entry.HMACKey, canonical)
	if !hmac.Equal(gotMAC, wantMAC) {
		return fmt.Errorf("hmac: hmac mismatch for node %q: %w", bundle.NodeID, mining.ErrAttestationSignatureInvalid)
	}

	// Step 5: GPU UUID binding. The registry records the GPU
	// UUID at enrollment; every subsequent proof must echo that
	// exact UUID. Without this check one key could mine forever
	// on any hardware (the spec's §3.2.2 step 5 rationale).
	// Case-insensitive compare because nvidia-smi emits mixed
	// case across driver versions.
	if !strings.EqualFold(bundle.GPUUUID, entry.GPUUUID) {
		return fmt.Errorf("hmac: gpu_uuid %q != registered %q: %w",
			bundle.GPUUUID, entry.GPUUUID, mining.ErrAttestationSignatureInvalid)
	}

	// Step 6a: nonce consistency between inner bundle and outer
	// Attestation field. A miner who signs over one nonce and
	// ships another is attempting a swap — reject.
	bundleNonce, err := hex.DecodeString(bundle.Nonce)
	if err != nil || len(bundleNonce) != 32 {
		return fmt.Errorf("hmac: nonce not 32 hex bytes: %w", mining.ErrAttestationBundleMalformed)
	}
	if !bytes.Equal(bundleNonce, p.Attestation.Nonce[:]) {
		return fmt.Errorf("hmac: bundle.nonce != Attestation.Nonce: %w", mining.ErrAttestationNonceMismatch)
	}
	if bundle.IssuedAt != p.Attestation.IssuedAt {
		return fmt.Errorf("hmac: bundle.issued_at != Attestation.IssuedAt: %w", mining.ErrAttestationNonceMismatch)
	}

	// Step 6a-ii: challenge signature. When a ChallengeVerifier is
	// wired in, prove this (nonce, issued_at) came from a
	// validator — not from the miner's own PRNG. Performed
	// BEFORE freshness so an invalid signature fails fast
	// without the freshness-window check; performed AFTER
	// nonce/issued_at consistency so we verify the SAME value
	// the inner HMAC covers.
	if v.ChallengeVerifier != nil {
		if bundle.ChallengeSignerID == "" {
			return fmt.Errorf("hmac: challenge_signer_id missing: %w", mining.ErrAttestationBundleMalformed)
		}
		sigBytes, err := hex.DecodeString(bundle.ChallengeSig)
		if err != nil || len(sigBytes) == 0 {
			return fmt.Errorf("hmac: challenge_sig not hex: %w", mining.ErrAttestationBundleMalformed)
		}
		var nonceArr [32]byte
		copy(nonceArr[:], bundleNonce)
		chg := challenge.Challenge{
			Nonce:     nonceArr,
			IssuedAt:  bundle.IssuedAt,
			SignerID:  bundle.ChallengeSignerID,
			Signature: sigBytes,
		}
		if err := v.ChallengeVerifier.VerifySignature(
			chg.SignerID, chg.SigningBytes(), chg.Signature,
		); err != nil {
			return fmt.Errorf("hmac: challenge signature: %w: %w",
				err, mining.ErrAttestationSignatureInvalid)
		}
	}

	// Step 6b: freshness. Reject proofs issued too far in the
	// past or impossibly in the future.
	window := v.FreshnessWindow
	if window == 0 {
		window = mining.FreshnessWindow
	}
	issued := time.Unix(bundle.IssuedAt, 0)
	age := now.Sub(issued)
	if age > window {
		return fmt.Errorf("hmac: attestation age %v > window %v: %w", age, window, mining.ErrAttestationStale)
	}
	if age < -v.AllowedFutureSkew {
		return fmt.Errorf("hmac: attestation issued %v in future (max skew %v): %w",
			-age, v.AllowedFutureSkew, mining.ErrAttestationStale)
	}

	// Step 6c: replay check against the nonce store. Only
	// consulted if the caller wired one in.
	if v.NonceStore != nil {
		var nonceBuf [32]byte
		copy(nonceBuf[:], bundleNonce)
		if v.NonceStore.Seen(bundle.NodeID, nonceBuf) {
			return fmt.Errorf("hmac: nonce already used by node %q: %w",
				bundle.NodeID, mining.ErrAttestationNonceMismatch)
		}
		// Record AFTER all other checks pass so a half-failing
		// proof can still be replayed once the attacker fixes the
		// other defects — but that only helps if their fix keeps
		// the HMAC valid, which requires the operator key.
		v.NonceStore.Record(bundle.NodeID, nonceBuf, now)
	}

	// Step 7: deny-list.
	if v.DenyList != nil && v.DenyList.Denied(bundle.GPUName) {
		return fmt.Errorf("hmac: gpu_name %q on deny-list: %w",
			bundle.GPUName, mining.ErrAttestationSignatureInvalid)
	}

	// Step 8: Tensor-Core mix_digest cross-check — deferred to
	// Phase 2c-iv where the PoW mixin itself lands. When that
	// lands, add a call here that asserts the mix_digest is
	// consistent with the claimed_hashrate_hps and gpu_arch.
	// For now, we trust the outer PoW check.

	return nil
}

// compile-time check that Verifier satisfies the interface.
var _ mining.AttestationVerifier = (*Verifier)(nil)
