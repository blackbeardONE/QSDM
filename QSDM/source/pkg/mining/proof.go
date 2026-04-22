package mining

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// Attestation carries optional NGC hardware-attestation evidence. Per
// MINING_PROTOCOL.md §6 this is a transparency signal, not a consensus
// rule — an absent or stale attestation MUST NOT by itself cause a
// validator to reject an otherwise-valid proof.
type Attestation struct {
	Type                 string `json:"type"`                // e.g. "ngc-v1"
	BundleBase64         string `json:"bundle"`              // base64 NGC proof bundle
	GPUArch              string `json:"gpu_arch"`            // e.g. "ada-lovelace"
	ClaimedHashrateHPS   uint64 `json:"claimed_hashrate_hps"`
}

// Empty reports whether the attestation carries any content. Used by the
// verifier to decide whether to even attempt NGC bundle verification.
func (a Attestation) Empty() bool {
	return a.Type == "" && a.BundleBase64 == "" && a.GPUArch == "" && a.ClaimedHashrateHPS == 0
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
		attBytes, err := json.Marshal(struct {
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
	Version     uint32      `json:"version"`
	Epoch       json.Number `json:"epoch"`
	Height      json.Number `json:"height"`
	HeaderHash  string      `json:"header_hash"`
	MinerAddr   string      `json:"miner_addr"`
	BatchRoot   string      `json:"batch_root"`
	BatchCount  uint32      `json:"batch_count"`
	Nonce       string      `json:"nonce"`
	MixDigest   string      `json:"mix_digest"`
	Attestation Attestation `json:"attestation"`
}

func (w proofWire) toProof() (*Proof, error) {
	p := &Proof{
		Version:     w.Version,
		MinerAddr:   w.MinerAddr,
		BatchCount:  w.BatchCount,
		Attestation: w.Attestation,
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
