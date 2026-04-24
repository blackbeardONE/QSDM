package enrollment

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mining"
)

// TestEnrollPayload_RoundTrip: any EnrollPayload that round-
// trips through Encode+Decode produces the same struct.
// Important because tx signatures cover the canonical bytes;
// any asymmetry here would cause self-submitted enrollments to
// fail signature verification.
func TestEnrollPayload_RoundTrip(t *testing.T) {
	p := EnrollPayload{
		Kind:      PayloadKindEnroll,
		NodeID:    "alice-rtx4090-01",
		GPUUUID:   "GPU-deadbeef-0000-0000-0000-000000000001",
		HMACKey:   bytes.Repeat([]byte{0x42}, 32),
		StakeDust: mining.MinEnrollStakeDust,
		Memo:      "rig #3, basement",
	}
	raw, err := EncodeEnrollPayload(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := DecodeEnrollPayload(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.NodeID != p.NodeID ||
		got.GPUUUID != p.GPUUUID ||
		!bytes.Equal(got.HMACKey, p.HMACKey) ||
		got.StakeDust != p.StakeDust ||
		got.Memo != p.Memo ||
		got.Kind != p.Kind {
		t.Fatalf("round-trip diff:\n  got: %+v\n  want: %+v", got, p)
	}
}

// TestEnrollPayload_Canonical_NoTrailingNewline: encoder output
// is a single JSON object with no trailing newline or extra
// whitespace. Matters because a tx hash that includes a
// trailing \n on one platform and not on another diverges.
func TestEnrollPayload_Canonical_NoTrailingNewline(t *testing.T) {
	p := EnrollPayload{
		Kind: PayloadKindEnroll, NodeID: "a", GPUUUID: "GPU-x",
		HMACKey: bytes.Repeat([]byte{1}, 32), StakeDust: mining.MinEnrollStakeDust,
	}
	raw, err := EncodeEnrollPayload(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("empty output")
	}
	if raw[len(raw)-1] == '\n' {
		t.Fatal("trailing newline present")
	}
	// Must be parseable as a single JSON object.
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("output not valid JSON: %v\n  %s", err, raw)
	}
}

// TestEnrollPayload_Deterministic: encoding the same payload
// twice produces identical bytes.
func TestEnrollPayload_Deterministic(t *testing.T) {
	p := EnrollPayload{
		Kind: PayloadKindEnroll, NodeID: "a", GPUUUID: "GPU-x",
		HMACKey: bytes.Repeat([]byte{1}, 32), StakeDust: mining.MinEnrollStakeDust,
	}
	a, _ := EncodeEnrollPayload(p)
	b, _ := EncodeEnrollPayload(p)
	if !bytes.Equal(a, b) {
		t.Fatalf("encode not deterministic:\n  a=%s\n  b=%s", a, b)
	}
}

// TestDecodeEnrollPayload_RejectsUnknownFields: canonical JSON
// is strict. A malicious miner cannot sneak in an extra field
// that's ignored on one node and respected on another.
func TestDecodeEnrollPayload_RejectsUnknownFields(t *testing.T) {
	raw := []byte(`{"kind":"enroll","node_id":"a","gpu_uuid":"GPU-x",` +
		`"hmac_key":"` + strings.Repeat("00", 32) + `",` +
		`"stake_dust":1000000000,"extra_field":42}`)
	_, err := DecodeEnrollPayload(raw)
	if err == nil {
		t.Fatal("expected rejection of unknown field")
	}
	if !errors.Is(err, ErrPayloadDecode) {
		t.Fatalf("want ErrPayloadDecode, got %v", err)
	}
}

// TestDecodeEnrollPayload_RejectsTrailingData guards against
// JSON-smuggling where two objects are concatenated.
func TestDecodeEnrollPayload_RejectsTrailingData(t *testing.T) {
	good := EnrollPayload{
		Kind: PayloadKindEnroll, NodeID: "a", GPUUUID: "GPU-x",
		HMACKey: bytes.Repeat([]byte{1}, 32), StakeDust: mining.MinEnrollStakeDust,
	}
	raw, _ := EncodeEnrollPayload(good)
	raw = append(raw, []byte(`{"junk":true}`)...)
	_, err := DecodeEnrollPayload(raw)
	if err == nil {
		t.Fatal("expected rejection of trailing data")
	}
}

// TestDecodeEnrollPayload_RejectsWrongKind: an unenroll payload
// fed to DecodeEnrollPayload is rejected. Defence against
// callers dispatching wrong.
func TestDecodeEnrollPayload_RejectsWrongKind(t *testing.T) {
	u := UnenrollPayload{Kind: PayloadKindUnenroll, NodeID: "a"}
	raw, _ := EncodeUnenrollPayload(u)
	_, err := DecodeEnrollPayload(raw)
	if err == nil {
		t.Fatal("expected rejection of unenroll payload via enroll decoder")
	}
	if !errors.Is(err, ErrPayloadInvalid) {
		t.Fatalf("want ErrPayloadInvalid, got %v", err)
	}
}

// TestUnenrollPayload_RoundTrip
func TestUnenrollPayload_RoundTrip(t *testing.T) {
	p := UnenrollPayload{
		Kind:   PayloadKindUnenroll,
		NodeID: "alice-rtx4090-01",
		Reason: "retiring GPU, upgrading",
	}
	raw, err := EncodeUnenrollPayload(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := DecodeUnenrollPayload(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != p {
		t.Fatalf("round-trip diff:\n  got: %+v\n  want: %+v", got, p)
	}
}

// TestPeekKind_OnBothVariants.
func TestPeekKind_OnBothVariants(t *testing.T) {
	e := EnrollPayload{
		Kind: PayloadKindEnroll, NodeID: "a", GPUUUID: "GPU-x",
		HMACKey: bytes.Repeat([]byte{1}, 32), StakeDust: mining.MinEnrollStakeDust,
	}
	raw, _ := EncodeEnrollPayload(e)
	k, err := PeekKind(raw)
	if err != nil {
		t.Fatalf("PeekKind: %v", err)
	}
	if k != PayloadKindEnroll {
		t.Fatalf("got kind %q", k)
	}

	u := UnenrollPayload{Kind: PayloadKindUnenroll, NodeID: "a"}
	raw2, _ := EncodeUnenrollPayload(u)
	k2, err := PeekKind(raw2)
	if err != nil {
		t.Fatalf("PeekKind: %v", err)
	}
	if k2 != PayloadKindUnenroll {
		t.Fatalf("got kind %q", k2)
	}
}

func TestPeekKind_RejectsEmptyKind(t *testing.T) {
	_, err := PeekKind([]byte(`{"node_id":"a"}`))
	if err == nil {
		t.Fatal("expected error on missing kind")
	}
	if !errors.Is(err, ErrPayloadDecode) {
		t.Fatalf("want ErrPayloadDecode, got %v", err)
	}
}

func TestPeekKind_RejectsNonJSON(t *testing.T) {
	_, err := PeekKind([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error on non-JSON")
	}
}

// TestEncodeEnrollPayload_DefaultsKind: passing a zero-Kind
// payload implicitly sets it to PayloadKindEnroll. Convenience
// for callers that construct payloads by field name.
func TestEncodeEnrollPayload_DefaultsKind(t *testing.T) {
	p := EnrollPayload{NodeID: "a", GPUUUID: "GPU-x",
		HMACKey: bytes.Repeat([]byte{1}, 32), StakeDust: mining.MinEnrollStakeDust}
	raw, err := EncodeEnrollPayload(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := DecodeEnrollPayload(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.Kind != PayloadKindEnroll {
		t.Fatalf("default Kind: got %q", decoded.Kind)
	}
}

// TestEncodeEnrollPayload_RejectsWrongExplicitKind: an encoder
// caller who passes PayloadKindUnenroll to EncodeEnrollPayload
// gets a clean error. Prevents structurally-valid-but-typed-
// wrong payloads from reaching the wire.
func TestEncodeEnrollPayload_RejectsWrongExplicitKind(t *testing.T) {
	p := EnrollPayload{Kind: PayloadKindUnenroll, NodeID: "a"}
	_, err := EncodeEnrollPayload(p)
	if err == nil {
		t.Fatal("expected error on wrong explicit Kind")
	}
	if !errors.Is(err, ErrPayloadInvalid) {
		t.Fatalf("want ErrPayloadInvalid, got %v", err)
	}
}
