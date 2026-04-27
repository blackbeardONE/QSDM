package freshnesscheat

// freshnesscheat.go ships the concrete EvidenceVerifier for
// EvidenceKindFreshnessCheat. See witness.go for the package-
// level overview and the BFT-finality dependency analysis.
//
// Wire format (Evidence):
//
//   {
//     "proof":            <canonical-JSON of mining.Proof>,
//     "anchor_height":    <uint64-as-string>,
//     "anchor_block_time": <int64 unix seconds>,
//     "memo":             <optional ≤256-byte string>
//   }
//
// All fields are required except `memo`. The verifier rejects
// any extra JSON keys (DisallowUnknownFields).

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
)

// MaxMemoLen mirrors slashing.MaxMemoLen for consistency with
// the other two verifiers. Operators occasionally want to drop
// a forensic note in here; we cap it so a slash tx cannot stuff
// a megabyte of commentary into the chain's evidence-fingerprint
// pre-image.
const MaxMemoLen = 256

// DefaultMaxSlashDust is the per-offence cap returned to the
// dispatcher on a successful verification. Picked to drain the
// full MIN_ENROLL_STAKE bond (10 CELL = 10 * 1e8 dust),
// matching forgedattest.DefaultMaxSlashDust and
// doublemining.DefaultMaxSlashDust. Validator collusion / clock
// fraud is at least as severe as the other two offences — there
// is no good-faith excuse for accepting a stale proof.
const DefaultMaxSlashDust uint64 = 10 * 100_000_000 // 10 CELL

// DefaultGraceWindow is added to FreshnessWindow before the
// verifier flags a proof as "provably stale". The reason this
// exists: the slasher's anchor (`AnchorBlockTime`) is the
// chain's finalised wall-clock at acceptance height. In
// well-behaved networks block-time is monotonic and within a few
// seconds of physical time, but consensus-time can drift by
// several seconds across reorgs / network partitions. We don't
// want a sub-second anchor lag to produce a false-positive
// slash, so the verifier requires the offence to be staler than
// `FreshnessWindow + GraceWindow` before it accepts.
//
// 30 seconds is intentionally generous: borderline freshness
// cases are exactly the situations that arise legitimately on a
// real network (clock skew, slow propagation), and we'd rather
// fail to slash a borderline case than slash an honest operator.
// A real attacker who manages to land a stale proof has likely
// done so by minutes, not by 30 seconds.
const DefaultGraceWindow = 30 * time.Second

// MaxAnchorFutureSkew bounds how far in the *future* relative to
// `currentHeight`'s wall-clock the slasher's claimed anchor may
// sit. A future-dated anchor is suspicious — a slasher could
// claim "block H accepted at year 2050" to make any past proof
// look infinitely stale. We don't have a chain clock at slash
// time (Verifier.Verify takes a height, not a time), so we use
// the proof's own bundle.IssuedAt as a sanity floor: the anchor
// MUST be after the proof's claimed IssuedAt by SOME positive
// amount (otherwise the proof was anchored before it was
// issued, which is non-physical) and no more than
// `MaxAnchorAgeSeconds` after.
const MaxAnchorAgeSeconds = 365 * 24 * 3600 // 1 year

// Sentinel errors. Callers MAY errors.Is against these when they
// want to differentiate the failure category; the dispatcher
// only cares about ErrEvidenceVerification (which all of these
// wrap).
var (
	// ErrProofNotV2 — the offending proof has Version < 2.
	// freshness-cheat is a v2-only offence (v1 proofs do not
	// carry a freshness-bound bundle).
	ErrProofNotV2 = errors.New("freshnesscheat: proof.version < 2")

	// ErrAnchorBeforeIssuedAt — the supplied anchor block time
	// is at or before the proof's bundle.IssuedAt. Non-
	// physical: a block cannot include a proof signed in its
	// future. Almost always means the slasher mixed up the
	// (height, time) pair.
	ErrAnchorBeforeIssuedAt = errors.New("freshnesscheat: anchor block time precedes proof.bundle.issued_at")

	// ErrAnchorTooOld — the supplied anchor is more than
	// MaxAnchorAgeSeconds after the proof's IssuedAt. Usually
	// means a copy-paste error in the slasher's evidence.
	ErrAnchorTooOld = errors.New("freshnesscheat: anchor age exceeds 1 year sanity bound")

	// ErrNotStaleEnough — the proof is within the (freshness
	// window + grace), so the anchor does NOT prove a
	// freshness-cheat. The slasher should not have submitted
	// this evidence.
	ErrNotStaleEnough = errors.New("freshnesscheat: proof is within freshness window + grace")

	// ErrBundleNodeIDMismatch — the proof's bundle.NodeID
	// does not match the slash payload's NodeID.
	ErrBundleNodeIDMismatch = errors.New("freshnesscheat: proof.bundle.node_id != payload.node_id")
)

// Evidence is the high-level Go shape callers build before
// encoding to the wire. The wire form is `evidenceWire`; see
// EncodeEvidence / DecodeEvidence.
type Evidence struct {
	// Proof is the offending v2 proof, byte-identical to what
	// the chain accepted.
	Proof mining.Proof

	// AnchorHeight is the chain block height that included
	// `Proof`. Used by the BlockInclusionWitness to identify
	// which sealed block the slasher is referring to.
	AnchorHeight uint64

	// AnchorBlockTime is the wall-clock timestamp of
	// AnchorHeight, in unix seconds. The verifier compares
	// this against the proof's bundle.IssuedAt to decide
	// whether the proof was stale at acceptance time.
	AnchorBlockTime int64

	// Memo is optional human-readable context. Bounded so a
	// slash tx cannot stuff a megabyte of operator commentary
	// into the chain's evidence-fingerprint pre-image.
	Memo string
}

// evidenceWire is the on-the-wire form. ProofJSON carries the
// proof's canonical-JSON serialisation — same byte sequence the
// chain hashed for proof_id, so verifying against it is what
// consensus saw. Anchor numerics use string form (uint64) to
// match the canonical-JSON convention of the rest of the
// protocol; AnchorBlockTime is int64 (unix seconds) and may
// fit in a JSON number, but we still encode it as a JSON number
// of integer form.
type evidenceWire struct {
	ProofJSON       json.RawMessage `json:"proof"`
	AnchorHeight    string          `json:"anchor_height"`     // uint64 stringified
	AnchorBlockTime int64           `json:"anchor_block_time"` // unix seconds
	Memo            string          `json:"memo,omitempty"`
}

// Verifier implements slashing.EvidenceVerifier for
// EvidenceKindFreshnessCheat.
//
// The verifier is stateless aside from its injected
// collaborators. Multiple chain-apply goroutines may call
// Verify concurrently.
type Verifier struct {
	// Witness adjudicates whether the slasher's claimed
	// (Height, BlockTime, proofID) tuple corresponds to a
	// real on-chain inclusion. REQUIRED. Use RejectAllWitness
	// in production today; replace with a real
	// quorum-attested witness once BFT finality lands.
	Witness BlockInclusionWitness

	// Registry resolves node_id → (gpu_uuid, hmac_key) at
	// slashing time. REQUIRED. Used to confirm the proof's
	// bundle binds to the slashed operator's enrollment
	// record (rules out "slash a different node's freshness
	// cheat" attacks).
	Registry hmac.Registry

	// FreshnessWindow overrides mining.FreshnessWindow for
	// tests. Zero means use the protocol default
	// (mining.FreshnessWindow = 60s).
	FreshnessWindow time.Duration

	// GraceWindow overrides DefaultGraceWindow for tests.
	// Zero means use DefaultGraceWindow (30s).
	GraceWindow time.Duration

	// MaxSlashDust is the per-offence cap returned to the
	// dispatcher on a successful verification. Zero means
	// "use DefaultMaxSlashDust".
	MaxSlashDust uint64
}

// NewVerifier constructs a Verifier with required collaborators
// and protocol defaults. Pass `witness=nil` to wire
// `RejectAllWitness{}` (the safe production default — see
// witness.go for posture).
func NewVerifier(witness BlockInclusionWitness, registry hmac.Registry, maxSlashDust uint64) *Verifier {
	if witness == nil {
		witness = RejectAllWitness{}
	}
	return &Verifier{
		Witness:      witness,
		Registry:     registry,
		MaxSlashDust: maxSlashDust,
	}
}

// Kind implements slashing.EvidenceVerifier.
func (v *Verifier) Kind() slashing.EvidenceKind {
	return slashing.EvidenceKindFreshnessCheat
}

// Verify decodes the Evidence blob, runs the structural and
// staleness checks, then calls the witness to certify the
// chain-side anchor. The offence is proven iff every step
// passes; on success returns the verifier's per-offence cap.
//
// `currentHeight` is unused by this verifier (the offence is a
// pure historical observation against an anchored block) but is
// part of the EvidenceVerifier contract — kept for symmetry
// with the other two verifiers, and reserved for a future cap
// that wants to reject anchors more than N blocks old.
func (v *Verifier) Verify(p slashing.SlashPayload, _ uint64) (uint64, error) {
	if v == nil {
		return 0, errors.New("freshnesscheat: nil verifier")
	}
	if v.Witness == nil {
		return 0, errors.New("freshnesscheat: verifier.Witness is nil " +
			"(use RejectAllWitness or a real quorum-attested witness)")
	}
	if v.Registry == nil {
		return 0, errors.New("freshnesscheat: verifier.Registry is nil")
	}

	if p.EvidenceKind != slashing.EvidenceKindFreshnessCheat {
		return 0, fmt.Errorf("%w: got %q want %q",
			slashing.ErrUnknownEvidenceKind,
			p.EvidenceKind,
			slashing.EvidenceKindFreshnessCheat)
	}

	ev, err := DecodeEvidence(p.EvidenceBlob)
	if err != nil {
		return 0, fmt.Errorf("freshnesscheat: decode evidence: %w: %w",
			err, slashing.ErrEvidenceVerification)
	}

	// Step 1: structural — proof must be v2 with a non-empty
	// attestation block. v1 proofs predate the freshness-bound
	// bundle and cannot be the subject of this offence.
	if ev.Proof.Version < mining.ProtocolVersionV2 {
		return 0, fmt.Errorf("%w (version=%d): %w",
			ErrProofNotV2, ev.Proof.Version,
			slashing.ErrEvidenceVerification)
	}
	if ev.Proof.Attestation.Type == "" {
		return 0, fmt.Errorf(
			"freshnesscheat: proof has no attestation block: %w",
			slashing.ErrEvidenceVerification)
	}
	if ev.Proof.Attestation.BundleBase64 == "" {
		return 0, fmt.Errorf(
			"freshnesscheat: proof.attestation.bundle is empty: %w",
			slashing.ErrEvidenceVerification)
	}

	// Step 2: parse the bundle and bind to the slashed NodeID.
	// We do NOT re-run HMAC verification here — that is
	// forgedattest's domain. A freshness-cheat is "the proof
	// IS valid by HMAC, but it was stale". If the bundle is
	// malformed, the offence is forged-attestation, not
	// freshness-cheat, and the verifier rejects.
	bundle, err := hmac.ParseBundle(ev.Proof.Attestation.BundleBase64)
	if err != nil {
		return 0, fmt.Errorf(
			"freshnesscheat: bundle parse failed (would be a "+
				"forged-attestation case, not freshness-cheat): %w: %w",
			err, slashing.ErrEvidenceVerification)
	}
	if bundle.NodeID != p.NodeID {
		return 0, fmt.Errorf("%w (bundle=%q payload=%q): %w",
			ErrBundleNodeIDMismatch, bundle.NodeID, p.NodeID,
			slashing.ErrEvidenceVerification)
	}

	// Step 3: anchor sanity. The anchor block time MUST sit
	// strictly after the proof's bundle.IssuedAt (a block
	// cannot include a future-signed proof). It also MUST be
	// within MaxAnchorAgeSeconds of IssuedAt — otherwise the
	// evidence is more likely a copy-paste error than a real
	// freshness-cheat case.
	if ev.AnchorBlockTime <= bundle.IssuedAt {
		return 0, fmt.Errorf(
			"%w (anchor=%d issued_at=%d): %w",
			ErrAnchorBeforeIssuedAt,
			ev.AnchorBlockTime, bundle.IssuedAt,
			slashing.ErrEvidenceVerification)
	}
	if ev.AnchorBlockTime-bundle.IssuedAt > MaxAnchorAgeSeconds {
		return 0, fmt.Errorf(
			"%w (delta=%ds limit=%ds): %w",
			ErrAnchorTooOld,
			ev.AnchorBlockTime-bundle.IssuedAt,
			int64(MaxAnchorAgeSeconds),
			slashing.ErrEvidenceVerification)
	}

	// Step 4: staleness check. The proof was provably stale
	// at the anchor block iff:
	//   anchor_block_time - bundle.issued_at > freshness + grace
	window := v.freshnessWindow()
	grace := v.graceWindow()
	thresholdSecs := int64(window/time.Second) + int64(grace/time.Second)
	stalenessSecs := ev.AnchorBlockTime - bundle.IssuedAt
	if stalenessSecs <= thresholdSecs {
		return 0, fmt.Errorf(
			"%w (staleness=%ds threshold=%ds = window %v + grace %v): %w",
			ErrNotStaleEnough,
			stalenessSecs, thresholdSecs, window, grace,
			slashing.ErrEvidenceVerification)
	}

	// Step 5: registry binding. We don't run the full HMAC
	// verify (cost: would re-do work we're not asserting), but
	// we DO require that the operator is currently known to
	// the registry — slashing a node that was never enrolled
	// is meaningless (the chain-side applier would reject it
	// too, but failing here gives a clearer error).
	if _, err := v.Registry.Lookup(bundle.NodeID); err != nil {
		return 0, fmt.Errorf(
			"freshnesscheat: registry lookup for %q: %w: %w",
			bundle.NodeID, err, slashing.ErrEvidenceVerification)
	}

	// Step 6: anchor authentication. Hand off to the witness.
	// On a production binary with RejectAllWitness this
	// returns ErrAnchorUnverified; on a properly-wired
	// witness it returns nil for legitimate anchors.
	proofID, err := ev.Proof.ID()
	if err != nil {
		return 0, fmt.Errorf(
			"freshnesscheat: derive proof_id: %w: %w",
			err, slashing.ErrEvidenceVerification)
	}
	if err := v.Witness.VerifyAnchor(ev.AnchorHeight, ev.AnchorBlockTime, proofID); err != nil {
		return 0, wrapAnchorErr(err)
	}

	// Offence proven.
	return v.maxSlash(), nil
}

// freshnessWindow returns the configured window or the protocol
// default. Mirrors the same fallback the hmac.Verifier uses.
func (v *Verifier) freshnessWindow() time.Duration {
	if v.FreshnessWindow > 0 {
		return v.FreshnessWindow
	}
	return mining.FreshnessWindow
}

// graceWindow returns the configured grace or the package
// default.
func (v *Verifier) graceWindow() time.Duration {
	if v.GraceWindow > 0 {
		return v.GraceWindow
	}
	return DefaultGraceWindow
}

// maxSlash returns the per-offence cap, applying the package
// default if MaxSlashDust is unset.
func (v *Verifier) maxSlash() uint64 {
	if v.MaxSlashDust == 0 {
		return DefaultMaxSlashDust
	}
	return v.MaxSlashDust
}

// EncodeEvidence emits a stable JSON encoding of an Evidence
// struct. The contained Proof is serialised via its canonical
// JSON (proof.go: CanonicalJSON), NOT via json.Marshal, because
// four of mining.Proof's fields are json:"-" and would otherwise
// be dropped silently.
//
// Used by slashers building a SlashPayload locally and by tests;
// consensus only consumes the bytes, never this helper.
func EncodeEvidence(ev Evidence) ([]byte, error) {
	if len(ev.Memo) > MaxMemoLen {
		return nil, fmt.Errorf(
			"freshnesscheat: memo exceeds %d bytes (got %d)",
			MaxMemoLen, len(ev.Memo))
	}
	canon, err := ev.Proof.CanonicalJSON()
	if err != nil {
		return nil, fmt.Errorf("freshnesscheat: canonical proof: %w", err)
	}

	wire := evidenceWire{
		ProofJSON:       canon,
		AnchorHeight:    strconv.FormatUint(ev.AnchorHeight, 10),
		AnchorBlockTime: ev.AnchorBlockTime,
		Memo:            ev.Memo,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(wire); err != nil {
		return nil, fmt.Errorf("freshnesscheat: encode: %w", err)
	}
	out := buf.Bytes()
	if n := len(out); n > 0 && out[n-1] == '\n' {
		out = out[:n-1]
	}
	if len(out) > slashing.MaxEvidenceLen {
		return nil, fmt.Errorf(
			"freshnesscheat: encoded evidence exceeds %d bytes (got %d)",
			slashing.MaxEvidenceLen, len(out))
	}
	return out, nil
}

// DecodeEvidence is the inverse of EncodeEvidence. Parses the
// embedded canonical proof JSON via mining.ParseProof so all
// binary fields are recovered exactly as the chain saw them.
func DecodeEvidence(raw []byte) (Evidence, error) {
	if len(raw) > slashing.MaxEvidenceLen {
		return Evidence{}, fmt.Errorf(
			"freshnesscheat: evidence exceeds %d bytes (got %d)",
			slashing.MaxEvidenceLen, len(raw))
	}
	var wire evidenceWire
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return Evidence{}, fmt.Errorf("freshnesscheat: json decode: %w", err)
	}
	if dec.More() {
		return Evidence{}, errors.New(
			"freshnesscheat: trailing bytes after evidence JSON")
	}
	if len(wire.Memo) > MaxMemoLen {
		return Evidence{}, fmt.Errorf(
			"freshnesscheat: memo exceeds %d bytes (got %d)",
			MaxMemoLen, len(wire.Memo))
	}
	if len(wire.ProofJSON) == 0 {
		return Evidence{}, errors.New(
			"freshnesscheat: evidence missing required proof field")
	}
	if wire.AnchorHeight == "" {
		return Evidence{}, errors.New(
			"freshnesscheat: evidence missing required anchor_height field")
	}
	height, err := strconv.ParseUint(wire.AnchorHeight, 10, 64)
	if err != nil {
		return Evidence{}, fmt.Errorf(
			"freshnesscheat: anchor_height parse: %w", err)
	}
	if wire.AnchorBlockTime <= 0 {
		return Evidence{}, fmt.Errorf(
			"freshnesscheat: anchor_block_time must be positive (got %d)",
			wire.AnchorBlockTime)
	}
	p, err := mining.ParseProof(wire.ProofJSON)
	if err != nil {
		return Evidence{}, fmt.Errorf("freshnesscheat: parse proof: %w", err)
	}
	return Evidence{
		Proof:           *p,
		AnchorHeight:    height,
		AnchorBlockTime: wire.AnchorBlockTime,
		Memo:            wire.Memo,
	}, nil
}

// hexProofID is a small helper for the slash-helper inspect
// view; lifted here so the CLI can reuse it without importing
// encoding/hex in two places.
func hexProofID(p mining.Proof) string {
	id, err := p.ID()
	if err != nil {
		return ""
	}
	return hex.EncodeToString(id[:])
}

// Compile-time check that Verifier satisfies the slashing
// EvidenceVerifier contract.
var _ slashing.EvidenceVerifier = (*Verifier)(nil)
