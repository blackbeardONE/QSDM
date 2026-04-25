// Package doublemining implements the concrete EvidenceVerifier
// for slashing.EvidenceKindDoubleMining.
//
// The offence: a single enrolled operator (NodeID) signed two
// distinct mining proofs at the same (Epoch, Height). In a
// well-behaved network only one proof per (NodeID, Height) ever
// reaches consensus; equivocation is how a malicious operator
// fans out across validators to try to land more than one
// reward, or to support a fork. Either way the protocol treats
// it as an unambiguous slashable offence.
//
// Why this verifier ships now:
//
//   1. No external blocker. Like forgedattest, every check this
//      verifier performs is a pure replay of the existing
//      pkg/mining/attest/hmac.Verifier flow against the registry
//      state at slashing time — no BFT-finality observability
//      needed (that gates freshness-cheat).
//   2. The attack surface is real: any HMAC key reuse / leak
//      lets the holder equivocate. Without a slasher this remains
//      an undetected subsidy on top of any ordinary mining reward.
//   3. The wire and chain-side plumbing already exist; what was
//      missing is exactly this verifier and its production wiring.
//
// Wire format of the EvidenceBlob is JSON; see Evidence below.
package doublemining

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

// Evidence is the high-level Go shape callers build before
// encoding to the wire. The wire form lives in evidenceWire;
// see EncodeEvidence / DecodeEvidence.
//
// The two proofs MUST share the same operator (NodeID, Epoch,
// Height) and MUST differ in their canonical encoding. The
// encoder canonicalises the (ProofA, ProofB) order
// (lexicographic on canonical bytes) so two slashers who
// independently observe the same equivocation pair produce
// byte-identical evidence — preserving the chain's
// per-fingerprint replay protection in slash_apply.go.
type Evidence struct {
	// ProofA and ProofB are the two equivocating proofs,
	// byte-identical to what the attacker signed. The
	// verifier does NOT trust caller framing; it parses both
	// bundles and re-runs the full HMAC acceptance flow.
	ProofA mining.Proof
	ProofB mining.Proof

	// Memo is optional human-readable context. Bounded so a
	// slash tx cannot stuff a megabyte of operator commentary
	// into the chain's evidence-fingerprint pre-image.
	Memo string
}

// evidenceWire is the on-the-wire form. ProofAJSON / ProofBJSON
// carry the proofs' canonical-JSON serialisations — that's the
// same byte sequence the chain hashed for the proof_id, so
// verifying against them is what consensus saw.
type evidenceWire struct {
	ProofAJSON json.RawMessage `json:"proof_a"`
	ProofBJSON json.RawMessage `json:"proof_b"`
	Memo       string          `json:"memo,omitempty"`
}

// MaxMemoLen mirrors slashing.MaxMemoLen.
const MaxMemoLen = 256

// DefaultMaxSlashDust is the default per-offence slash cap in
// dust. Picked to drain the full MIN_ENROLL_STAKE bond
// (10 CELL = 10 * 1e8 dust) in one slash, matching
// forgedattest.DefaultMaxSlashDust. Equivocation is at least as
// severe as a forged attestation — there is no good-faith
// excuse for it.
const DefaultMaxSlashDust uint64 = 10 * 100_000_000 // 10 CELL

// Verifier implements slashing.EvidenceVerifier for
// EvidenceKindDoubleMining.
//
// The verifier is stateless aside from its injected
// collaborators. Multiple chain-apply goroutines may call
// Verify concurrently.
type Verifier struct {
	// Registry resolves node_id -> (gpu_uuid, hmac_key) at
	// slashing time. Required. Same contract as
	// forgedattest.Verifier.Registry.
	Registry hmac.Registry

	// DenyList participates in the deny-listed-GPU rejection
	// path. Optional; defaults to hmac.EmptyDenyList. A proof
	// whose GPU is on the deny list at slash time would have
	// failed acceptance anyway — but for double-mining the
	// offence is "two valid proofs" so a deny-list rejection
	// makes one of the proofs invalid and the equivocation
	// claim collapses into a forged-attestation case (which is
	// out of scope for this verifier).
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
	return slashing.EvidenceKindDoubleMining
}

// Verify decodes the Evidence blob and re-runs the
// nvidia-hmac-v1 acceptance flow against both proofs. The
// equivocation is proven iff:
//
//  1. Both proofs are v2 (Version >= ProtocolVersionV2).
//  2. Both proofs have the same (Epoch, Height).
//  3. Both proofs have distinct canonical bytes.
//  4. Both proofs' bundles bind to the slash payload's NodeID.
//  5. Both proofs independently PASS hmac.VerifyAttestation
//     (i.e. each is a crypto-valid attestation by the same
//     enrolled operator).
//
// If any step rejects, the evidence does NOT prove
// double-mining and Verify returns ErrEvidenceVerification.
//
// The Verify path is pure (no I/O, no real-time clock): freshness
// is neutralised because freshness-violations are the
// freshness-cheat verifier's domain, not this one. A miner who
// equivocates with one stale and one fresh proof is still
// double-mining — the equivocation, not the freshness, is the
// offence.
func (v *Verifier) Verify(p slashing.SlashPayload, currentHeight uint64) (uint64, error) {
	if v == nil {
		return 0, errors.New("doublemining: nil verifier")
	}
	if v.Registry == nil {
		return 0, errors.New("doublemining: verifier.Registry is nil")
	}

	if p.EvidenceKind != slashing.EvidenceKindDoubleMining {
		return 0, fmt.Errorf("%w: got %q want %q",
			slashing.ErrUnknownEvidenceKind,
			p.EvidenceKind,
			slashing.EvidenceKindDoubleMining)
	}

	ev, err := DecodeEvidence(p.EvidenceBlob)
	if err != nil {
		return 0, fmt.Errorf("doublemining: decode evidence: %w: %w",
			err, slashing.ErrEvidenceVerification)
	}

	// Cheap structural checks first — fail fast before any
	// HMAC work.
	if ev.ProofA.Version < mining.ProtocolVersionV2 ||
		ev.ProofB.Version < mining.ProtocolVersionV2 {
		return 0, fmt.Errorf(
			"doublemining: pre-v2 proofs not slashable (a.version=%d b.version=%d): %w",
			ev.ProofA.Version, ev.ProofB.Version,
			slashing.ErrEvidenceVerification)
	}
	if ev.ProofA.Height != ev.ProofB.Height {
		return 0, fmt.Errorf(
			"doublemining: heights differ (a=%d b=%d): %w",
			ev.ProofA.Height, ev.ProofB.Height,
			slashing.ErrEvidenceVerification)
	}
	if ev.ProofA.Epoch != ev.ProofB.Epoch {
		return 0, fmt.Errorf(
			"doublemining: epochs differ (a=%d b=%d): %w",
			ev.ProofA.Epoch, ev.ProofB.Epoch,
			slashing.ErrEvidenceVerification)
	}

	// Distinctness: re-derive canonical bytes locally
	// (DecodeEvidence does NOT store them; doing so would
	// risk drift between what the wire said and what the
	// in-memory Proof shape implies). If the canonical
	// encodings tie, the "two proofs" are one proof submitted
	// twice — not equivocation, just a confused slasher.
	aCanon, errA := ev.ProofA.CanonicalJSON()
	if errA != nil {
		return 0, fmt.Errorf("doublemining: canonical proof_a: %w: %w",
			errA, slashing.ErrEvidenceVerification)
	}
	bCanon, errB := ev.ProofB.CanonicalJSON()
	if errB != nil {
		return 0, fmt.Errorf("doublemining: canonical proof_b: %w: %w",
			errB, slashing.ErrEvidenceVerification)
	}
	if bytes.Equal(aCanon, bCanon) {
		return 0, fmt.Errorf(
			"doublemining: proof_a == proof_b (no equivocation): %w",
			slashing.ErrEvidenceVerification)
	}

	// Bundle parse + NodeID binding for both proofs.
	bundleA, parseAErr := hmac.ParseBundle(ev.ProofA.Attestation.BundleBase64)
	if parseAErr != nil {
		return 0, fmt.Errorf(
			"doublemining: proof_a bundle parse: %w: %w",
			parseAErr, slashing.ErrEvidenceVerification)
	}
	bundleB, parseBErr := hmac.ParseBundle(ev.ProofB.Attestation.BundleBase64)
	if parseBErr != nil {
		return 0, fmt.Errorf(
			"doublemining: proof_b bundle parse: %w: %w",
			parseBErr, slashing.ErrEvidenceVerification)
	}
	if bundleA.NodeID != p.NodeID {
		return 0, fmt.Errorf(
			"doublemining: proof_a.bundle.node_id %q != payload.node_id %q: %w",
			bundleA.NodeID, p.NodeID, slashing.ErrEvidenceVerification)
	}
	if bundleB.NodeID != p.NodeID {
		return 0, fmt.Errorf(
			"doublemining: proof_b.bundle.node_id %q != payload.node_id %q: %w",
			bundleB.NodeID, p.NodeID, slashing.ErrEvidenceVerification)
	}

	// HMAC re-acceptance. Both proofs MUST pass independently;
	// if either rejects, this is not double-mining (it's at
	// most a forged-attestation case, slashable through the
	// other verifier).
	hvA := v.makeHMACVerifier(bundleA.IssuedAt)
	if err := hvA.VerifyAttestation(ev.ProofA, time.Unix(bundleA.IssuedAt, 0)); err != nil {
		return 0, fmt.Errorf(
			"doublemining: proof_a does not pass hmac acceptance: %w: %w",
			err, slashing.ErrEvidenceVerification)
	}
	hvB := v.makeHMACVerifier(bundleB.IssuedAt)
	if err := hvB.VerifyAttestation(ev.ProofB, time.Unix(bundleB.IssuedAt, 0)); err != nil {
		return 0, fmt.Errorf(
			"doublemining: proof_b does not pass hmac acceptance: %w: %w",
			err, slashing.ErrEvidenceVerification)
	}

	// Equivocation proven.
	return v.maxSlash(), nil
}

// makeHMACVerifier returns an hmac.Verifier configured to
// neutralise the freshness-window check (this verifier's domain
// is equivocation, not freshness). The `now` parameter passed at
// the call site is set to bundle.IssuedAt for the same reason —
// see forgedattest.Verifier.Verify for the original derivation.
func (v *Verifier) makeHMACVerifier(_ int64) *hmac.Verifier {
	return &hmac.Verifier{
		Registry:          v.Registry,
		NonceStore:        nil, // no replay cache at slash time
		DenyList:          v.denyList(),
		FreshnessWindow:   365 * 24 * time.Hour,
		AllowedFutureSkew: 365 * 24 * time.Hour,
		ChallengeVerifier: nil,
	}
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
// struct. The two contained mining.Proofs are serialised via
// their canonical JSON (proof.go: CanonicalJSON), NOT via
// json.Marshal, because four of mining.Proof's fields are
// json:"-" and would otherwise be dropped silently.
//
// The encoder sorts (proof_a, proof_b) lexicographically by
// canonical bytes so two independent slashers who observe the
// same equivocation pair produce byte-identical evidence —
// preserving chain-side per-fingerprint replay protection.
//
// Used by slashers building a SlashPayload locally and by tests;
// consensus only consumes the bytes, never this helper.
func EncodeEvidence(ev Evidence) ([]byte, error) {
	if len(ev.Memo) > MaxMemoLen {
		return nil, fmt.Errorf(
			"doublemining: memo exceeds %d bytes (got %d)",
			MaxMemoLen, len(ev.Memo))
	}

	aCanon, err := ev.ProofA.CanonicalJSON()
	if err != nil {
		return nil, fmt.Errorf("doublemining: canonical proof_a: %w", err)
	}
	bCanon, err := ev.ProofB.CanonicalJSON()
	if err != nil {
		return nil, fmt.Errorf("doublemining: canonical proof_b: %w", err)
	}
	if bytes.Equal(aCanon, bCanon) {
		return nil, errors.New(
			"doublemining: proof_a == proof_b (no equivocation to encode)")
	}

	// Canonicalise order: smaller canonical bytes go first.
	if bytes.Compare(aCanon, bCanon) > 0 {
		aCanon, bCanon = bCanon, aCanon
	}

	wire := evidenceWire{
		ProofAJSON: aCanon,
		ProofBJSON: bCanon,
		Memo:       ev.Memo,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(wire); err != nil {
		return nil, fmt.Errorf("doublemining: encode: %w", err)
	}
	out := buf.Bytes()
	if n := len(out); n > 0 && out[n-1] == '\n' {
		out = out[:n-1]
	}
	if len(out) > slashing.MaxEvidenceLen {
		return nil, fmt.Errorf(
			"doublemining: encoded evidence exceeds %d bytes (got %d)",
			slashing.MaxEvidenceLen, len(out))
	}
	return out, nil
}

// DecodeEvidence is the inverse of EncodeEvidence. It parses
// both embedded canonical proof JSONs via mining.ParseProof so
// all binary fields are recovered exactly as the chain saw them.
func DecodeEvidence(raw []byte) (Evidence, error) {
	if len(raw) > slashing.MaxEvidenceLen {
		return Evidence{}, fmt.Errorf(
			"doublemining: evidence exceeds %d bytes (got %d)",
			slashing.MaxEvidenceLen, len(raw))
	}
	var wire evidenceWire
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return Evidence{}, fmt.Errorf("doublemining: json decode: %w", err)
	}
	if dec.More() {
		return Evidence{}, errors.New(
			"doublemining: trailing bytes after evidence JSON")
	}
	if len(wire.Memo) > MaxMemoLen {
		return Evidence{}, fmt.Errorf(
			"doublemining: memo exceeds %d bytes (got %d)",
			MaxMemoLen, len(wire.Memo))
	}
	if len(wire.ProofAJSON) == 0 {
		return Evidence{}, errors.New(
			"doublemining: evidence missing required proof_a field")
	}
	if len(wire.ProofBJSON) == 0 {
		return Evidence{}, errors.New(
			"doublemining: evidence missing required proof_b field")
	}
	pa, err := mining.ParseProof(wire.ProofAJSON)
	if err != nil {
		return Evidence{}, fmt.Errorf("doublemining: parse proof_a: %w", err)
	}
	pb, err := mining.ParseProof(wire.ProofBJSON)
	if err != nil {
		return Evidence{}, fmt.Errorf("doublemining: parse proof_b: %w", err)
	}
	return Evidence{
		ProofA: *pa,
		ProofB: *pb,
		Memo:   wire.Memo,
	}, nil
}

// Compile-time check that Verifier satisfies the slashing
// EvidenceVerifier contract.
var _ slashing.EvidenceVerifier = (*Verifier)(nil)
