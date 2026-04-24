package mining

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// Attestation carries hardware-attestation evidence for the proof.
//
// Under v1 (Proof.Version == ProtocolVersion == 1) this field is
// optional: per MINING_PROTOCOL.md §6 it is a transparency signal,
// not a consensus rule — an absent or stale attestation MUST NOT
// by itself cause a validator to reject an otherwise-valid proof.
//
// Under v2 (Proof.Version == ProtocolVersionV2 == 2, active at or
// above ForkV2Height) this field is MANDATORY: per
// MINING_PROTOCOL_V2_NVIDIA_LOCKED.md §3 a v2 proof with an empty
// or unverifiable attestation is rejected with
// ErrAttestationRequired or one of the more specific sentinels in
// fork.go. The verifier dispatches on Attestation.Type to the
// nvidia-cc-v1 or nvidia-hmac-v1 verifier path.
//
// The Nonce and IssuedAt fields were added in v2; they are
// serialised into the canonical JSON only when Proof.Version >=
// ProtocolVersionV2, so v1 proofs retain byte-identical encoding
// to pre-fork canonical form.
type Attestation struct {
	Type               string `json:"type"`
	BundleBase64       string `json:"bundle"`
	GPUArch            string `json:"gpu_arch"`
	ClaimedHashrateHPS uint64 `json:"claimed_hashrate_hps"`

	// Nonce is the 32-byte server-issued challenge the miner
	// committed to when computing this proof. v2 only; MUST be
	// zero (and omitted from canonical JSON) for v1 proofs.
	// Serialised as lowercase hex in canonical JSON.
	Nonce [32]byte `json:"-"`

	// IssuedAt is the unix-seconds timestamp at which the
	// validator issued Nonce. v2 only; MUST be zero for v1
	// proofs. The verifier rejects the proof if IssuedAt is more
	// than FreshnessWindow before its wall clock or outside the
	// tolerated future skew.
	IssuedAt int64 `json:"issued_at,omitempty"`
}

// Empty reports whether the attestation carries any content. Used
// by the v1 verifier to decide whether to even attempt NGC bundle
// verification. v2 uses a different check (Attestation.Type == ""
// → ErrAttestationRequired) because v2's semantics distinguish
// "fully empty" from "has Type but nothing else" more sharply.
func (a Attestation) Empty() bool {
	return a.Type == "" &&
		a.BundleBase64 == "" &&
		a.GPUArch == "" &&
		a.ClaimedHashrateHPS == 0 &&
		a.Nonce == [32]byte{} &&
		a.IssuedAt == 0
}

// Proof is a miner's solution submission. Field order here is normative
// and mirrored by the canonical-JSON codec below. Do not reorder fields
// without bumping ProtocolVersion.
type Proof struct {
	Version     uint32      `json:"version"`
	Epoch       uint64      `json:"epoch"`
	Height      uint64      `json:"height"`
	HeaderHash  [32]byte    `json:"-"` // serialized as hex below
	MinerAddr   string      `json:"miner_addr"`
	BatchRoot   [32]byte    `json:"-"` // serialized as hex below
	BatchCount  uint32      `json:"batch_count"`
	Nonce       [16]byte    `json:"-"` // serialized as hex below
	MixDigest   [32]byte    `json:"-"` // serialized as hex below
	Attestation Attestation `json:"attestation"`
}

// -----------------------------------------------------------------------------
// Canonical JSON
// -----------------------------------------------------------------------------
//
// MINING_PROTOCOL.md §4.1 pins a strict canonical serialization so two
// honest implementations produce byte-identical inputs to the proof_id
// hash:
//
//   - field order: version, epoch, height, header_hash, miner_addr,
//     batch_root, batch_count, nonce, mix_digest, attestation
//   - no whitespace
//   - hex strings lowercase, no 0x prefix
//   - uint64 integers rendered as JSON strings ("123") to survive the
//     JavaScript number-precision trap; uint32 integers rendered as bare
//     JSON numbers (they always fit in a double)
//   - attestation emitted as a nested JSON object exactly as encoded by
//     encoding/json with struct-tag ordering
//
// We do the serialization by hand (no reflection) so the byte stream is
// entirely deterministic — encoding/json has historically been
// field-order-stable but we do not want to rely on that invariant.

func (p Proof) canonicalBytes(includeAttestation bool) ([]byte, error) {
	if err := p.validateShape(); err != nil {
		return nil, err
	}
	buf := make([]byte, 0, 512)
	buf = append(buf, '{')
	buf = appendJSONField(buf, "version", strconv.FormatUint(uint64(p.Version), 10), false)
	buf = append(buf, ',')
	buf = appendJSONField(buf, "epoch", strconv.FormatUint(p.Epoch, 10), true)
	buf = append(buf, ',')
	buf = appendJSONField(buf, "height", strconv.FormatUint(p.Height, 10), true)
	buf = append(buf, ',')
	buf = appendJSONField(buf, "header_hash", hex.EncodeToString(p.HeaderHash[:]), true)
	buf = append(buf, ',')
	buf = appendJSONField(buf, "miner_addr", p.MinerAddr, true)
	buf = append(buf, ',')
	buf = appendJSONField(buf, "batch_root", hex.EncodeToString(p.BatchRoot[:]), true)
	buf = append(buf, ',')
	buf = appendJSONField(buf, "batch_count", strconv.FormatUint(uint64(p.BatchCount), 10), false)
	buf = append(buf, ',')
	buf = appendJSONField(buf, "nonce", hex.EncodeToString(p.Nonce[:]), true)
	buf = append(buf, ',')
	buf = appendJSONField(buf, "mix_digest", hex.EncodeToString(p.MixDigest[:]), true)
	if includeAttestation {
		buf = append(buf, ',')
		// v2 adds two fields (nonce + issued_at) to the
		// attestation's canonical JSON. We gate on
		// p.Version so v1 proofs retain byte-identical encoding
		// to pre-fork canonical form — round-trip equality with
		// pre-fork fixtures is a consensus invariant, not just a
		// test convenience.
		var attBytes []byte
		var err error
		if p.Version >= ProtocolVersionV2 {
			attBytes, err = json.Marshal(struct {
				Type               string `json:"type"`
				Bundle             string `json:"bundle"`
				GPUArch            string `json:"gpu_arch"`
				ClaimedHashrateHPS uint64 `json:"claimed_hashrate_hps"`
				Nonce              string `json:"nonce"`
				IssuedAt           int64  `json:"issued_at"`
			}{
				Type:               p.Attestation.Type,
				Bundle:             p.Attestation.BundleBase64,
				GPUArch:            p.Attestation.GPUArch,
				ClaimedHashrateHPS: p.Attestation.ClaimedHashrateHPS,
				Nonce:              hex.EncodeToString(p.Attestation.Nonce[:]),
				IssuedAt:           p.Attestation.IssuedAt,
			})
		} else {
			attBytes, err = json.Marshal(struct {
				Type               string `json:"type"`
				Bundle             string `json:"bundle"`
				GPUArch            string `json:"gpu_arch"`
				ClaimedHashrateHPS uint64 `json:"claimed_hashrate_hps"`
			}{
				Type:               p.Attestation.Type,
				Bundle:             p.Attestation.BundleBase64,
				GPUArch:            p.Attestation.GPUArch,
				ClaimedHashrateHPS: p.Attestation.ClaimedHashrateHPS,
			})
		}
		if err != nil {
			return nil, fmt.Errorf("mining: marshal attestation: %w", err)
		}
		buf = append(buf, '"', 'a', 't', 't', 'e', 's', 't', 'a', 't', 'i', 'o', 'n', '"', ':')
		buf = append(buf, attBytes...)
	}
	buf = append(buf, '}')
	return buf, nil
}

func appendJSONField(buf []byte, name, value string, quoteValue bool) []byte {
	buf = append(buf, '"')
	buf = append(buf, name...)
	buf = append(buf, '"', ':')
	if quoteValue {
		buf = append(buf, '"')
		buf = append(buf, value...)
		buf = append(buf, '"')
	} else {
		buf = append(buf, value...)
	}
	return buf
}

// CanonicalJSON returns the byte representation used for dedup keys and
// network gossip. This is what validators hash to compute proof_id.
func (p Proof) CanonicalJSON() ([]byte, error) {
	return p.canonicalBytes(true)
}

// ID returns the 32-byte proof identifier (MINING_PROTOCOL.md §4.2):
//
//	proof_id := SHA256( canonical_json(proof_without("attestation")) )
//
// The attestation is excluded from the ID so a single solved share can be
// re-submitted with a refreshed NGC bundle without changing its identity
// in the validator dedup set.
func (p Proof) ID() ([32]byte, error) {
	b, err := p.canonicalBytes(false)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(b), nil
}

// validateShape rejects proofs whose string / slice fields are obviously
// out of spec. This is a shallow, pre-hash check; full semantic
// verification against the chain happens in Verifier.Verify.
func (p Proof) validateShape() error {
	if p.Version == 0 {
		return errors.New("mining: proof.version must be set (>=1)")
	}
	if p.MinerAddr == "" {
		return errors.New("mining: proof.miner_addr must be non-empty")
	}
	if p.BatchCount == 0 {
		return errors.New("mining: proof.batch_count must be >= 1")
	}
	return nil
}

// ParseProof decodes a canonical-JSON proof. It is strict: any extra
// whitespace, any field not in the spec, and any field out of order will
// cause the round-trip check in Verifier step 4 to fail — which is the
// intended defence against malleability. For the Phase-4 reference
// validator we use encoding/json's decoder here and rely on the round-
// trip check against CanonicalJSON to reject non-canonical inputs.
func ParseProof(raw []byte) (*Proof, error) {
	var w proofWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, fmt.Errorf("mining: parse proof: %w", err)
	}
	return w.toProof()
}

type proofWire struct {
	Version     uint32           `json:"version"`
	Epoch       json.Number      `json:"epoch"`
	Height      json.Number      `json:"height"`
	HeaderHash  string           `json:"header_hash"`
	MinerAddr   string           `json:"miner_addr"`
	BatchRoot   string           `json:"batch_root"`
	BatchCount  uint32           `json:"batch_count"`
	Nonce       string           `json:"nonce"`
	MixDigest   string           `json:"mix_digest"`
	Attestation attestationWire `json:"attestation"`
}

// attestationWire is the on-the-wire shape of Attestation. It uses
// string-typed hex for Nonce (rather than the Go struct's [32]byte)
// because encoding/json has no built-in hex codec, and we do not
// want to attach MarshalJSON / UnmarshalJSON methods directly to
// Attestation — the canonical serialisation in canonicalBytes is
// intentionally hand-rolled for byte-stability, and a struct-level
// method would interfere with that.
//
// Both the nonce and issued_at fields are optional on the wire so
// that a v1 proof (whose attestation lacks them) continues to
// parse without error. The verifier enforces that v2 proofs MUST
// carry both — see Verifier.verifyAttestation in a later phase.
type attestationWire struct {
	Type               string `json:"type"`
	Bundle             string `json:"bundle"`
	GPUArch            string `json:"gpu_arch"`
	ClaimedHashrateHPS uint64 `json:"claimed_hashrate_hps"`
	Nonce              string `json:"nonce,omitempty"`     // v2 only, lowercase hex
	IssuedAt           int64  `json:"issued_at,omitempty"` // v2 only, unix seconds
}

// toAttestation decodes an attestationWire into the strongly-typed
// Attestation struct. The Nonce field's hex string is accepted as
// either empty (v1) or exactly 64 lowercase-hex characters (v2);
// any other length is a parse error rather than a silent truncation.
func (aw attestationWire) toAttestation() (Attestation, error) {
	a := Attestation{
		Type:               aw.Type,
		BundleBase64:       aw.Bundle,
		GPUArch:            aw.GPUArch,
		ClaimedHashrateHPS: aw.ClaimedHashrateHPS,
		IssuedAt:           aw.IssuedAt,
	}
	if aw.Nonce != "" {
		if err := decodeHexInto(a.Nonce[:], aw.Nonce, "attestation.nonce"); err != nil {
			return Attestation{}, err
		}
	}
	return a, nil
}

func (w proofWire) toProof() (*Proof, error) {
	att, err := w.Attestation.toAttestation()
	if err != nil {
		return nil, err
	}
	p := &Proof{
		Version:     w.Version,
		MinerAddr:   w.MinerAddr,
		BatchCount:  w.BatchCount,
		Attestation: att,
	}
	epoch, err := strconv.ParseUint(w.Epoch.String(), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("mining: parse epoch: %w", err)
	}
	p.Epoch = epoch
	height, err := strconv.ParseUint(w.Height.String(), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("mining: parse height: %w", err)
	}
	p.Height = height
	if err := decodeHexInto(p.HeaderHash[:], w.HeaderHash, "header_hash"); err != nil {
		return nil, err
	}
	if err := decodeHexInto(p.BatchRoot[:], w.BatchRoot, "batch_root"); err != nil {
		return nil, err
	}
	if err := decodeHexInto(p.Nonce[:], w.Nonce, "nonce"); err != nil {
		return nil, err
	}
	if err := decodeHexInto(p.MixDigest[:], w.MixDigest, "mix_digest"); err != nil {
		return nil, err
	}
	return p, nil
}

func decodeHexInto(dst []byte, s, field string) error {
	b, err := hex.DecodeString(s)
	if err != nil {
		return fmt.Errorf("mining: decode %s: %w", field, err)
	}
	if len(b) != len(dst) {
		return fmt.Errorf("mining: %s wrong length: have %d want %d", field, len(b), len(dst))
	}
	copy(dst, b)
	return nil
}
