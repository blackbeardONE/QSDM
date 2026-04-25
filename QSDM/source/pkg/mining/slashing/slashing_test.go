package slashing

import (
	"bytes"
	"errors"
	"testing"
)

func samplePayload() SlashPayload {
	return SlashPayload{
		NodeID:          "rig-77",
		EvidenceKind:    EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte(`{"proof":"deadbeef"}`),
		SlashAmountDust: 100_000_000, // 1 CELL
		Memo:            "smoke test",
	}
}

func TestEncodeDecodeSlashPayload_RoundTrip(t *testing.T) {
	src := samplePayload()
	raw, err := EncodeSlashPayload(src)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := DecodeSlashPayload(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.NodeID != src.NodeID || got.EvidenceKind != src.EvidenceKind ||
		got.SlashAmountDust != src.SlashAmountDust || got.Memo != src.Memo {
		t.Errorf("round-trip drift: got=%+v want=%+v", got, src)
	}
	if !bytes.Equal(got.EvidenceBlob, src.EvidenceBlob) {
		t.Errorf("blob drift: got=%q want=%q", got.EvidenceBlob, src.EvidenceBlob)
	}
}

func TestDecodeSlashPayload_RejectsUnknownFields(t *testing.T) {
	raw := []byte(`{"node_id":"rig-77","evidence_kind":"forged-attestation","evidence_blob":"YQ==","slash_amount_dust":1,"unknown":42}`)
	_, err := DecodeSlashPayload(raw)
	if err == nil || !errors.Is(err, ErrPayloadDecode) {
		t.Errorf("unknown field should be rejected, got %v", err)
	}
}

func TestDecodeSlashPayload_RejectsTrailingData(t *testing.T) {
	raw, err := EncodeSlashPayload(samplePayload())
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	raw = append(raw, ' ', '{', '}')
	if _, err := DecodeSlashPayload(raw); err == nil {
		t.Error("trailing data should be rejected")
	}
}

func TestValidateSlashFields_AcceptsValid(t *testing.T) {
	if err := ValidateSlashFields(samplePayload(), "alice"); err != nil {
		t.Errorf("valid payload rejected: %v", err)
	}
}

func TestValidateSlashFields_RejectsEmptySender(t *testing.T) {
	if err := ValidateSlashFields(samplePayload(), ""); err == nil ||
		!errors.Is(err, ErrPayloadInvalid) {
		t.Errorf("empty sender should be rejected, got %v", err)
	}
}

func TestValidateSlashFields_RejectsEmptyNodeID(t *testing.T) {
	p := samplePayload()
	p.NodeID = ""
	if err := ValidateSlashFields(p, "x"); err == nil ||
		!errors.Is(err, ErrPayloadInvalid) {
		t.Errorf("empty node_id should be rejected, got %v", err)
	}
}

func TestValidateSlashFields_RejectsUnknownKind(t *testing.T) {
	p := samplePayload()
	p.EvidenceKind = "made-up-kind"
	if err := ValidateSlashFields(p, "x"); err == nil ||
		!errors.Is(err, ErrUnknownEvidenceKind) {
		t.Errorf("unknown kind should be rejected, got %v", err)
	}
}

func TestValidateSlashFields_RejectsZeroAmount(t *testing.T) {
	p := samplePayload()
	p.SlashAmountDust = 0
	if err := ValidateSlashFields(p, "x"); err == nil ||
		!errors.Is(err, ErrPayloadInvalid) {
		t.Errorf("zero amount should be rejected, got %v", err)
	}
}

func TestValidateSlashFields_RejectsOversizedBlob(t *testing.T) {
	p := samplePayload()
	p.EvidenceBlob = make([]byte, MaxEvidenceLen+1)
	if err := ValidateSlashFields(p, "x"); err == nil ||
		!errors.Is(err, ErrPayloadInvalid) {
		t.Errorf("oversized blob should be rejected, got %v", err)
	}
}

// ----- Dispatcher --------------------------------------------------

func TestDispatcher_Register_Verify_HappyPath(t *testing.T) {
	d := NewDispatcher()
	d.Register(StubVerifier{K: EvidenceKindForgedAttestation})

	// StubVerifier always returns ErrEvidenceVerification —
	// success path is "got the right verifier wired in", which
	// proves dispatch works.
	_, err := d.Verify(samplePayload(), 100)
	if err == nil || !errors.Is(err, ErrEvidenceVerification) {
		t.Errorf("expected stub-verification error, got %v", err)
	}
}

func TestDispatcher_Verify_UnknownKind(t *testing.T) {
	d := NewDispatcher()
	_, err := d.Verify(samplePayload(), 100)
	if err == nil || !errors.Is(err, ErrUnknownEvidenceKind) {
		t.Errorf("unknown kind should error, got %v", err)
	}
}

func TestDispatcher_Register_Duplicate_Panics(t *testing.T) {
	d := NewDispatcher()
	d.Register(StubVerifier{K: EvidenceKindForgedAttestation})
	defer func() {
		if r := recover(); r == nil {
			t.Error("duplicate registration should panic")
		}
	}()
	d.Register(StubVerifier{K: EvidenceKindForgedAttestation})
}

func TestDispatcher_Register_NilVerifier_Panics(t *testing.T) {
	d := NewDispatcher()
	defer func() {
		if r := recover(); r == nil {
			t.Error("nil verifier should panic")
		}
	}()
	d.Register(nil)
}

func TestDispatcher_Register_EmptyKind_Panics(t *testing.T) {
	d := NewDispatcher()
	defer func() {
		if r := recover(); r == nil {
			t.Error("empty-kind verifier should panic")
		}
	}()
	d.Register(StubVerifier{K: ""})
}

func TestDispatcher_Kinds_ReturnsSortedKinds(t *testing.T) {
	d := NewDispatcher()
	d.Register(StubVerifier{K: EvidenceKindFreshnessCheat})
	d.Register(StubVerifier{K: EvidenceKindForgedAttestation})
	got := d.Kinds()

	// Order MUST match AllEvidenceKinds (forged-attestation,
	// double-mining, freshness-cheat). double-mining is
	// missing from the registry, so the expected output skips it.
	want := []EvidenceKind{EvidenceKindForgedAttestation, EvidenceKindFreshnessCheat}
	if len(got) != len(want) {
		t.Fatalf("Kinds(): got %v, want %v", got, want)
	}
	for i, k := range want {
		if got[i] != k {
			t.Errorf("Kinds()[%d] = %q, want %q", i, got[i], k)
		}
	}
}
