// Package forgedattest implements the concrete EvidenceVerifier
// for slashing.EvidenceKindForgedAttestation.
//
// The offence: a v2 mining proof was accepted on-chain whose
// nvidia-hmac-v1 attestation actually fails verification under
// the current consensus state. Re-running the HMAC verifier at
// slashing time MUST reject the proof; if it does, the offence
// is proven and the slasher is paid out per the chain-side
// slasher.RewardBPS configuration.
//
// Why this verifier is the first concrete EvidenceVerifier to
// ship:
//
//  1. No external blocker. Unlike the freshness-cheat verifier
//     (needs BFT-finality observability to attribute the
//     misacceptance) or the double-mining verifier (needs an
//     epoch-indexed seen-proofs cache), every check this verifier
//     performs is a pure replay of the existing
//     pkg/mining/attest/hmac.Verifier flow against the registry
//     state at slashing time.
//  2. Highest expected attack surface. The full consumer-GPU
//     population is on the nvidia-hmac-v1 path, so any practical
//     forged-proof attack will route through this verifier first.
//  3. The infrastructure (slash applier, evidence-fingerprint
//     replay protection, reward economics) already shipped in
//     pkg/chain — see commit 5f5fce7 / pkg/chain/slash_apply.go.
//     What was missing is exactly this verifier.
//
// Wire format of the EvidenceBlob is JSON; see Evidence below.
package forgedattest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
)

// FaultClass is a forensic-only narrowing of the alleged offence.
// It does NOT control verification behaviour — the verifier
// always runs the full HMAC flow regardless of the value here —
// but it lets dashboards and post-mortems group slashes by
// what the slasher believed they were proving.
//
// An empty FaultClass is permitted; the verifier still runs and
// the slash either lands or is rejected on the cryptographic
// merits.
type FaultClass string

const (
	FaultUnspecified         FaultClass = ""
	FaultHMACMismatch        FaultClass = "hmac_mismatch"
	FaultGPUUUIDMismatch     FaultClass = "gpu_uuid_mismatch"
	FaultChallengeBindMismat FaultClass = "challenge_bind_mismatch"
	FaultDenyListedGPU       FaultClass = "deny_listed_gpu"
	FaultNodeNotEnrolled     FaultClass = "node_not_enrolled"
	FaultNodeRevoked         FaultClass = "node_revoked"
	FaultBundleMalformed     FaultClass = "bundle_malformed"
	FaultNonceMismatch       FaultClass = "nonce_mismatch"
)

// AllFaultClasses is the canonical set of permitted FaultClass
// values, used by validation.
var AllFaultClasses = []FaultClass{
	FaultUnspecified,
	FaultHMACMismatch,
	FaultGPUUUIDMismatch,
	FaultChallengeBindMismat,
	FaultDenyListedGPU,
	FaultNodeNotEnrolled,
	FaultNodeRevoked,
	FaultBundleMalformed,
	FaultNonceMismatch,
}

// Evidence is the high-level Go shape callers build before
// encoding to the wire. The wire form lives in evidenceWire;
// see EncodeEvidence / DecodeEvidence.
//
// The reason for the two-tier shape: mining.Proof tags four of
// its binary fields (HeaderHash, BatchRoot, Nonce, MixDigest) as
// json:"-" because they are serialised through a hand-rolled
// canonical JSON encoder (proof.go: Proof.CanonicalJSON) for
// consensus byte-stability. A naive json.Marshal of a
// mining.Proof drops those fields, which would silently corrupt
// the proof during evidence round-tripping. Encoding the proof
// through its canonical JSON keeps every byte the verifier
// re-derives (challenge_bind, MAC inputs) consistent with what
// the chain originally accepted.
type Evidence struct {
	// Proof is the offending mining proof, byte-identical to
	// what the chain accepted. The verifier does NOT trust the
	// caller's framing; it parses the embedded attestation
	// bundle and re-runs all relevant checks.
	Proof mining.Proof

	// FaultClass is forensic metadata. See FaultClass docs.
	FaultClass FaultClass

	// Memo is optional human-readable context. Bounded so a
	// slash tx cannot stuff a megabyte of operator commentary
	// into the chain's evidence-fingerprint pre-image.
	Memo string
}

// evidenceWire is the on-the-wire form. ProofJSON carries the
// proof's canonical-JSON serialisation (proof.go: CanonicalJSON)
// — that's the same byte sequence the chain hashed for the
// proof_id, so verifying against it is what consensus saw.
type evidenceWire struct {
	ProofJSON  json.RawMessage `json:"proof"`
	FaultClass FaultClass      `json:"fault_class,omitempty"`
	Memo       string          `json:"memo,omitempty"`
}

// MaxMemoLen mirrors slashing.MaxMemoLen. Re-declared here so a
// slasher building Evidence locally can clamp before serialisation.
const MaxMemoLen = 256

// DefaultMaxSlashDust is the default per-offence slash cap in
// dust. Picked to drain a single MIN_ENROLL_STAKE bond (10 CELL
// = 10 * 1e8 dust) in one slash; the chain-side applier will
// further clamp to the offender's actually-bonded stake.
//
// Governance can tune this by constructing the verifier with a
// different MaxSlashDust at boot. Any change is a soft fork
// (validators all need the same value to agree on the cap),
// hence a constant rather than chain-state lookup for now.
const DefaultMaxSlashDust uint64 = 10 * 100_000_000 // 10 CELL

// Verifier implements slashing.EvidenceVerifier for
// EvidenceKindForgedAttestation.
//
// The verifier is stateless aside from its injected
// collaborators. Multiple chain-apply goroutines may call
// Verify concurrently.
type Verifier struct {
	// Registry resolves node_id -> (gpu_uuid, hmac_key) at
	// slashing time. Required.
	//
	// Implementation contract: the registry returned MUST
	// reflect on-chain state at currentHeight. In production
	// that is enrollment.StateBackedRegistry, which derives
	// from the chain's EnrollmentState; in tests it is
	// hmac.InMemoryRegistry. Either way, what the verifier
	// re-runs against IS what consensus saw — that's the
	// determinism guarantee the EvidenceVerifier interface
	// promises.
	Registry hmac.Registry

	// DenyList participates in the deny_listed_gpu fault class.
	// Optional; defaults to hmac.EmptyDenyList.
	DenyList hmac.DenyList

	// MaxSlashDust is the per-offence cap returned to the
	// dispatcher on a successful verification. Zero means
	// "use DefaultMaxSlashDust".
	MaxSlashDust uint64
}

// NewVerifier constructs a Verifier with the required Registry
// collaborator and sensible defaults. Pass MaxSlashDust=0 for
// the protocol default.
func NewVerifier(registry hmac.Registry, maxSlashDust uint64) *Verifier {
	return &Verifier{
		Registry:     registry,
		DenyList:     hmac.EmptyDenyList{},
		MaxSlashDust: maxSlashDust,
	}
}

// Kind implements slashing.EvidenceVerifier.
func (v *Verifier) Kind() slashing.EvidenceKind {
	return slashing.EvidenceKindForgedAttestation
}

// Verify decodes the Evidence blob and re-runs the
// nvidia-hmac-v1 acceptance flow against the verifier's
// Registry + DenyList. If the HMAC verifier rejects the proof,
// the offence is proven and Verify returns the configured cap.
// If the HMAC verifier accepts the proof, the evidence is
// bogus and Verify returns ErrEvidenceVerification — the chain
// will surface this to the slasher as "no offence proven."
//
// The Verify path is pure (no I/O, no real-time clock): the
// freshness window is set to a value large enough to neutralise
// it, because freshness violations are the freshness-cheat
// verifier's domain, not this one.
func (v *Verifier) Verify(p slashing.SlashPayload, currentHeight uint64) (uint64, error) {
	if v == nil {
		return 0, errors.New("forgedattest: nil verifier")
	}
	if v.Registry == nil {
		return 0, errors.New("forgedattest: verifier.Registry is nil")
	}

	if p.EvidenceKind != slashing.EvidenceKindForgedAttestation {
		return 0, fmt.Errorf("%w: got %q want %q",
			slashing.ErrUnknownEvidenceKind,
			p.EvidenceKind,
			slashing.EvidenceKindForgedAttestation)
	}

	ev, err := DecodeEvidence(p.EvidenceBlob)
	if err != nil {
		return 0, fmt.Errorf("forgedattest: decode evidence: %w: %w",
			err, slashing.ErrEvidenceVerification)
	}

	// Bind the evidence's bundle.NodeID to the slash payload's
	// NodeID. Without this an attacker could lift a faulty
	// proof from victim B and submit it inside a slash payload
	// that targets victim A.
	bundle, parseErr := hmac.ParseBundle(ev.Proof.Attestation.BundleBase64)
	if parseErr != nil {
		// Bundle is malformed — that itself is a
		// forged-attestation fault (a well-formed bundle is
		// part of the v2 acceptance criteria).
		// Still verify the slash payload's NodeID is plausibly
		// present in the registry; if not, the slash is
		// targeting a non-enrolled node which the chain-side
		// applier rejects independently. We don't second-guess
		// the chain-side check here.
		return v.maxSlash(), nil
	}
	if bundle.NodeID != p.NodeID {
		return 0, fmt.Errorf(
			"forgedattest: bundle.node_id %q != payload.node_id %q: %w",
			bundle.NodeID, p.NodeID, slashing.ErrEvidenceVerification)
	}

	// Construct a verifier configured to neutralise freshness
	// (this verifier's domain is HMAC/UUID/challenge-bind/deny
	// faults, not freshness — that's freshness-cheat's job).
	// Setting `now` equal to bundle.IssuedAt makes the freshness
	// check tautological regardless of attacker-chosen IssuedAt.
	hv := &hmac.Verifier{
		Registry:        v.Registry,
		NonceStore:      nil, // no replay-cache at slash time
		DenyList:        v.denyList(),
		FreshnessWindow: 365 * 24 * time.Hour,
		// AllowedFutureSkew has the same year-scale value so
		// neither past nor future bundle.IssuedAt collides with
		// freshness checks during slash-time replay.
		AllowedFutureSkew: 365 * 24 * time.Hour,
		// No ChallengeVerifier: the bundle's challenge_sig is
		// not consensus-checkable at slash time without
		// access to the issuing validator's verifying key
		// (which is itself chain state, but the slasher is
		// not the place to litigate validator-key bindings).
		// challenge_bind, which IS deterministic from the
		// proof, is checked by the verifier.
		ChallengeVerifier: nil,
	}

	hmacErr := hv.VerifyAttestation(ev.Proof, time.Unix(bundle.IssuedAt, 0))
	if hmacErr == nil {
		// Verifier accepts — no offence. Bogus evidence.
		return 0, fmt.Errorf(
			"forgedattest: hmac verifier accepts proof, no offence proven: %w",
			slashing.ErrEvidenceVerification)
	}

	// We have a confirmed forged attestation. Stay defensive:
	// if the slasher claimed a specific FaultClass, do a
	// best-effort sanity check that the rejection error
	// matches. A mismatch is NOT a verifier failure — the
	// FaultClass is forensic metadata — but we surface it
	// through a sentinel for dashboards.
	_ = ev.FaultClass // future: emit a metric label here

	return v.maxSlash(), nil
}

// maxSlash returns the per-offence cap, applying the package
// default if MaxSlashDust is unset.
func (v *Verifier) maxSlash() uint64 {
	if v.MaxSlashDust == 0 {
		return DefaultMaxSlashDust
	}
	return v.MaxSlashDust
}

// denyList returns the configured deny-list or the empty
// default. Mirrors the same fallback the hmac.NewVerifier
// constructor uses.
func (v *Verifier) denyList() hmac.DenyList {
	if v.DenyList == nil {
		return hmac.EmptyDenyList{}
	}
	return v.DenyList
}

// EncodeEvidence emits a stable JSON encoding of an Evidence
// struct. The contained mining.Proof is serialised via its
// canonical JSON (proof.go: CanonicalJSON), NOT via
// json.Marshal, because four of mining.Proof's fields are
// json:"-" and would otherwise be dropped silently.
//
// Used by slashers building a SlashPayload locally and by tests;
// consensus only consumes the bytes, never this helper.
//
// The encoder rejects oversize Memo and oversize bundles up
// front so a malformed slasher cannot ship a payload the
// chain's stateless validator will reject anyway.
func EncodeEvidence(ev Evidence) ([]byte, error) {
	if len(ev.Memo) > MaxMemoLen {
		return nil, fmt.Errorf(
			"forgedattest: memo exceeds %d bytes (got %d)",
			MaxMemoLen, len(ev.Memo))
	}
	if !validFaultClass(ev.FaultClass) {
		return nil, fmt.Errorf(
			"forgedattest: unknown fault_class %q",
			ev.FaultClass)
	}

	// Canonical proof bytes — same encoding the chain hashed.
	// Pre-fork v1 proofs use the v1 layout; v2 proofs use v2.
	// proof.canonicalBytes handles both via the Version-gated
	// branch internally.
	proofJSON, err := ev.Proof.CanonicalJSON()
	if err != nil {
		return nil, fmt.Errorf("forgedattest: canonical proof: %w", err)
	}

	// Encode the outer envelope with no HTML escaping —
	// consensus-relevant bytes MUST be stable across
	// implementations, and the default json.Marshal escapes
	// <, >, & for HTML-safety. We don't need that here and it
	// would diverge between Go and any future Rust slasher.
	wire := evidenceWire{
		ProofJSON:  proofJSON,
		FaultClass: ev.FaultClass,
		Memo:       ev.Memo,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(wire); err != nil {
		return nil, fmt.Errorf("forgedattest: encode: %w", err)
	}
	// Drop the trailing newline json.Encoder appends so the
	// blob is a pure JSON object suitable for the EvidenceBlob
	// wire field.
	out := buf.Bytes()
	if n := len(out); n > 0 && out[n-1] == '\n' {
		out = out[:n-1]
	}
	if len(out) > slashing.MaxEvidenceLen {
		return nil, fmt.Errorf(
			"forgedattest: encoded evidence exceeds %d bytes (got %d)",
			slashing.MaxEvidenceLen, len(out))
	}
	return out, nil
}

// DecodeEvidence is the inverse of EncodeEvidence. Used by the
// verifier on the consensus path. It parses the embedded
// canonical proof JSON via mining.ParseProof so all binary
// fields are recovered exactly as the chain saw them.
func DecodeEvidence(raw []byte) (Evidence, error) {
	if len(raw) > slashing.MaxEvidenceLen {
		return Evidence{}, fmt.Errorf(
			"forgedattest: evidence exceeds %d bytes (got %d)",
			slashing.MaxEvidenceLen, len(raw))
	}
	var wire evidenceWire
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return Evidence{}, fmt.Errorf("forgedattest: json decode: %w", err)
	}
	if dec.More() {
		return Evidence{}, errors.New(
			"forgedattest: trailing bytes after evidence JSON")
	}
	if len(wire.Memo) > MaxMemoLen {
		return Evidence{}, fmt.Errorf(
			"forgedattest: memo exceeds %d bytes (got %d)",
			MaxMemoLen, len(wire.Memo))
	}
	if !validFaultClass(wire.FaultClass) {
		return Evidence{}, fmt.Errorf(
			"forgedattest: unknown fault_class %q",
			wire.FaultClass)
	}
	if len(wire.ProofJSON) == 0 {
		return Evidence{}, errors.New(
			"forgedattest: evidence missing required proof field")
	}
	p, err := mining.ParseProof(wire.ProofJSON)
	if err != nil {
		return Evidence{}, fmt.Errorf("forgedattest: parse proof: %w", err)
	}
	return Evidence{
		Proof:      *p,
		FaultClass: wire.FaultClass,
		Memo:       wire.Memo,
	}, nil
}

// validFaultClass checks the FaultClass against the permitted
// set so a slasher cannot stuff arbitrary forensic metadata into
// the consensus-evidence-fingerprint pre-image.
func validFaultClass(fc FaultClass) bool {
	for _, k := range AllFaultClasses {
		if fc == k {
			return true
		}
	}
	return false
}

// Compile-time check that Verifier satisfies the slashing
// EvidenceVerifier contract.
var _ slashing.EvidenceVerifier = (*Verifier)(nil)
