package mining

import (
	"bytes"
	"testing"
)

func TestCanonicalJSONRoundTrip(t *testing.T) {
	p := Proof{
		Version:    ProtocolVersion,
		Epoch:      7,
		Height:     60_480*7 + 12,
		HeaderHash: [32]byte{0x01, 0x02, 0x03},
		MinerAddr:  "qsdm1abc",
		BatchRoot:  [32]byte{0xaa, 0xbb},
		BatchCount: 4,
		Nonce:      [16]byte{0xde, 0xad, 0xbe, 0xef},
		MixDigest:  [32]byte{0xff, 0xee},
		Attestation: Attestation{
			Type:               "ngc-v1",
			BundleBase64:       "",
			GPUArch:            "ada-lovelace",
			ClaimedHashrateHPS: 123456,
		},
	}

	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical encode: %v", err)
	}
	p2, err := ParseProof(raw)
	if err != nil {
		t.Fatalf("parse proof: %v", err)
	}
	raw2, err := p2.CanonicalJSON()
	if err != nil {
		t.Fatalf("re-encode: %v", err)
	}
	if !bytes.Equal(raw, raw2) {
		t.Fatalf("canonical JSON not stable:\n  first:  %s\n  second: %s", raw, raw2)
	}
}

func TestCanonicalJSONFieldOrder(t *testing.T) {
	p := Proof{
		Version:    ProtocolVersion,
		Epoch:      1,
		Height:     60480,
		MinerAddr:  "m",
		BatchCount: 1,
	}
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	s := string(raw)
	// Field names must appear strictly in the order documented in §4.1.
	expected := []string{
		`"version":`, `"epoch":`, `"height":`, `"header_hash":`,
		`"miner_addr":`, `"batch_root":`, `"batch_count":`, `"nonce":`,
		`"mix_digest":`, `"attestation":`,
	}
	pos := 0
	for _, field := range expected {
		idx := indexFrom(s, field, pos)
		if idx < 0 {
			t.Fatalf("field %q missing in canonical JSON: %s", field, s)
		}
		if idx < pos {
			t.Fatalf("field %q appears before previous expected field (out of order): %s", field, s)
		}
		pos = idx + len(field)
	}
}

func TestProofIDExcludesAttestation(t *testing.T) {
	base := Proof{
		Version:    ProtocolVersion,
		Epoch:      1,
		Height:     60481,
		MinerAddr:  "m",
		BatchCount: 1,
	}
	withA := base
	withA.Attestation = Attestation{Type: "ngc-v1", BundleBase64: "AAAA", GPUArch: "hopper"}
	id1, err := base.ID()
	if err != nil {
		t.Fatal(err)
	}
	id2, err := withA.ID()
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("proof ID changed when attestation was added; spec §4.2 requires it to be excluded")
	}
}

func TestParseProofRejectsShortHex(t *testing.T) {
	bad := []byte(`{"version":1,"epoch":"0","height":"0","header_hash":"aa","miner_addr":"x","batch_root":"","batch_count":1,"nonce":"","mix_digest":"","attestation":{"type":"","bundle":"","gpu_arch":"","claimed_hashrate_hps":0}}`)
	if _, err := ParseProof(bad); err == nil {
		t.Fatal("parser must reject short header_hash")
	}
}

// TestCanonicalJSONV2RoundTrip exercises the v2 encode/parse path
// end-to-end: a proof with Version = ProtocolVersionV2 MUST emit
// the two new attestation fields (nonce, issued_at) in the
// canonical JSON and MUST parse back byte-identical. Regression
// test against drift between the hand-rolled canonicalBytes and
// the encoding/json-driven attestationWire parse path.
func TestCanonicalJSONV2RoundTrip(t *testing.T) {
	p := Proof{
		Version:    ProtocolVersionV2,
		Epoch:      8,
		Height:     60_480*8 + 34,
		HeaderHash: [32]byte{0x11, 0x22, 0x33},
		MinerAddr:  "qsdm1v2demo",
		BatchRoot:  [32]byte{0xcc, 0xdd},
		BatchCount: 2,
		Nonce:      [16]byte{0xa5, 0xa5},
		MixDigest:  [32]byte{0xbe, 0xef},
		Attestation: Attestation{
			Type:               AttestationTypeHMAC,
			BundleBase64:       "dGVzdC1idW5kbGU=",
			GPUArch:            "ada-lovelace",
			ClaimedHashrateHPS: 5_000_000,
			Nonce: [32]byte{
				0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
				0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
				0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18,
				0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20,
			},
			IssuedAt: 1_745_000_000,
		},
	}

	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical encode: %v", err)
	}
	if !bytes.Contains(raw, []byte(`"nonce":"010203040506070809`)) {
		t.Errorf("v2 canonical JSON missing attestation.nonce hex: %s", raw)
	}
	if !bytes.Contains(raw, []byte(`"issued_at":1745000000`)) {
		t.Errorf("v2 canonical JSON missing attestation.issued_at: %s", raw)
	}

	p2, err := ParseProof(raw)
	if err != nil {
		t.Fatalf("parse v2 proof: %v", err)
	}
	raw2, err := p2.CanonicalJSON()
	if err != nil {
		t.Fatalf("re-encode v2: %v", err)
	}
	if !bytes.Equal(raw, raw2) {
		t.Fatalf("v2 canonical JSON not stable:\n  first:  %s\n  second: %s", raw, raw2)
	}
}

// TestCanonicalJSONV1StableAcrossSchemaExtension is the regression
// guard for "adding v2 wire fields must not change v1 canonical
// bytes for the same in-memory Proof." Two proofs are constructed
// that differ only in version; the v1 one must encode byte-
// identically to the pre-fork shape.
func TestCanonicalJSONV1StableAcrossSchemaExtension(t *testing.T) {
	p := Proof{
		Version:    ProtocolVersion, // = 1
		Epoch:      1,
		Height:     60480,
		MinerAddr:  "m",
		BatchCount: 1,
		Attestation: Attestation{
			// A v1 proof may carry the new fields in memory
			// (because the struct is shared) but MUST NOT emit
			// them on the wire. This is the crux of the gated
			// serialisation in canonicalBytes.
			Nonce:    [32]byte{0x99},
			IssuedAt: 42,
		},
	}
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatalf("encode v1: %v", err)
	}
	if bytes.Contains(raw, []byte(`"nonce":"99`)) {
		t.Errorf("v1 canonical JSON must not leak v2 attestation.nonce field: %s", raw)
	}
	if bytes.Contains(raw, []byte(`"issued_at"`)) {
		t.Errorf("v1 canonical JSON must not leak v2 attestation.issued_at field: %s", raw)
	}
}

// indexFrom is a tiny substring search anchored at a position.
func indexFrom(s, substr string, from int) int {
	if from > len(s) {
		return -1
	}
	idx := bytes.Index([]byte(s[from:]), []byte(substr))
	if idx < 0 {
		return -1
	}
	return idx + from
}
