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
