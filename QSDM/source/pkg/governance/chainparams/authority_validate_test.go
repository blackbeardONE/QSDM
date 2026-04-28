package chainparams

// authority_validate_test.go: wire-codec + admission gate
// coverage for the authority-set payload kind. Companion to
// chainparams_test.go's parameter-side coverage.

import (
	"errors"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// -----------------------------------------------------------------------------
// PeekKind
// -----------------------------------------------------------------------------

func TestPeekKind_BothShapes(t *testing.T) {
	psBlob, err := EncodeParamSet(ParamSetPayload{
		Kind: PayloadKindParamSet, Param: string(ParamRewardBPS),
		Value: 1000, EffectiveHeight: 1,
	})
	if err != nil {
		t.Fatalf("EncodeParamSet: %v", err)
	}
	if k, err := PeekKind(psBlob); err != nil || k != PayloadKindParamSet {
		t.Errorf("PeekKind(param-set) = (%q, %v), want (%q, nil)",
			k, err, PayloadKindParamSet)
	}
	asBlob, err := EncodeAuthoritySet(AuthoritySetPayload{
		Kind: PayloadKindAuthoritySet, Op: AuthorityOpAdd,
		Address: "qsdm1new", EffectiveHeight: 1,
	})
	if err != nil {
		t.Fatalf("EncodeAuthoritySet: %v", err)
	}
	if k, err := PeekKind(asBlob); err != nil || k != PayloadKindAuthoritySet {
		t.Errorf("PeekKind(authority-set) = (%q, %v), want (%q, nil)",
			k, err, PayloadKindAuthoritySet)
	}
}

func TestPeekKind_RejectsUnknownAndEmpty(t *testing.T) {
	_, err := PeekKind([]byte(`{"kind":"banana"}`))
	if !errors.Is(err, ErrPayloadInvalid) {
		t.Errorf("err = %v, want ErrPayloadInvalid", err)
	}
	_, err = PeekKind([]byte(`{}`))
	if !errors.Is(err, ErrPayloadInvalid) {
		t.Errorf("missing-kind err = %v, want ErrPayloadInvalid", err)
	}
	_, err = PeekKind(nil)
	if !errors.Is(err, ErrPayloadDecode) {
		t.Errorf("empty err = %v, want ErrPayloadDecode", err)
	}
	_, err = PeekKind([]byte("not-json"))
	if !errors.Is(err, ErrPayloadDecode) {
		t.Errorf("non-json err = %v, want ErrPayloadDecode", err)
	}
}

// -----------------------------------------------------------------------------
// ValidateAuthoritySetFields
// -----------------------------------------------------------------------------

func TestValidateAuthoritySetFields_HappyPath(t *testing.T) {
	p := &AuthoritySetPayload{
		Kind: PayloadKindAuthoritySet, Op: AuthorityOpAdd,
		Address: "qsdm1new", EffectiveHeight: 100,
	}
	if err := ValidateAuthoritySetFields(p); err != nil {
		t.Errorf("happy-path validation rejected: %v", err)
	}
}

func TestValidateAuthoritySetFields_EveryRejectionBranch(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*AuthoritySetPayload)
	}{
		{"wrong_kind", func(p *AuthoritySetPayload) { p.Kind = PayloadKindParamSet }},
		{"empty_op", func(p *AuthoritySetPayload) { p.Op = "" }},
		{"unknown_op", func(p *AuthoritySetPayload) { p.Op = "rotate" }},
		{"empty_address", func(p *AuthoritySetPayload) { p.Address = "" }},
		{"oversize_address", func(p *AuthoritySetPayload) {
			p.Address = strings.Repeat("a", MaxAuthorityAddressLen+1)
		}},
		{"address_with_space", func(p *AuthoritySetPayload) { p.Address = "qsdm1 alice" }},
		{"address_with_tab", func(p *AuthoritySetPayload) { p.Address = "qsdm1\talice" }},
		{"address_with_newline", func(p *AuthoritySetPayload) { p.Address = "qsdm1alice\n" }},
		{"address_with_del", func(p *AuthoritySetPayload) { p.Address = "qsdm1\x7falice" }},
		{"oversized_memo", func(p *AuthoritySetPayload) {
			p.Memo = strings.Repeat("m", MaxMemoLen+1)
		}},
		{"zero_effective_height", func(p *AuthoritySetPayload) { p.EffectiveHeight = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &AuthoritySetPayload{
				Kind: PayloadKindAuthoritySet, Op: AuthorityOpAdd,
				Address: "qsdm1new", EffectiveHeight: 100,
			}
			tc.mutate(p)
			if err := ValidateAuthoritySetFields(p); err == nil {
				t.Errorf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func TestValidateAuthoritySetFields_NilPayload(t *testing.T) {
	if err := ValidateAuthoritySetFields(nil); err == nil {
		t.Error("nil payload accepted")
	}
}

// -----------------------------------------------------------------------------
// Encode / Parse round-trip
// -----------------------------------------------------------------------------

func TestEncodeParseAuthoritySet_RoundTrip(t *testing.T) {
	in := AuthoritySetPayload{
		Kind:            PayloadKindAuthoritySet,
		Op:              AuthorityOpRemove,
		Address:         "qsdm1retired-authority",
		EffectiveHeight: 12345,
		Memo:            "carol stepping down per board resolution 2026-04",
	}
	blob, err := EncodeAuthoritySet(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := ParseAuthoritySet(blob)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out == nil || *out != in {
		t.Errorf("round-trip mismatch:\n in=%+v\nout=%+v", in, out)
	}
}

func TestParseAuthoritySet_RejectsUnknownFields(t *testing.T) {
	blob := []byte(`{"kind":"authority-set","op":"add","address":"x","effective_height":1,"surprise":42}`)
	if _, err := ParseAuthoritySet(blob); !errors.Is(err, ErrPayloadDecode) {
		t.Errorf("err = %v, want ErrPayloadDecode (unknown field)", err)
	}
}

func TestParseAuthoritySet_RejectsTrailingBytes(t *testing.T) {
	blob := []byte(`{"kind":"authority-set","op":"add","address":"x","effective_height":1}{"trailing":"yes"}`)
	if _, err := ParseAuthoritySet(blob); !errors.Is(err, ErrPayloadDecode) {
		t.Errorf("err = %v, want ErrPayloadDecode (trailing)", err)
	}
}

func TestParseAuthoritySet_RejectsWrongKind(t *testing.T) {
	blob := []byte(`{"kind":"param-set","op":"add","address":"x","effective_height":1}`)
	_, err := ParseAuthoritySet(blob)
	if !errors.Is(err, ErrPayloadDecode) && !errors.Is(err, ErrPayloadInvalid) {
		t.Errorf("err = %v, want ErrPayloadDecode or ErrPayloadInvalid", err)
	}
}

// -----------------------------------------------------------------------------
// AdmissionChecker dispatch
// -----------------------------------------------------------------------------

func TestAdmissionChecker_AcceptsValidAuthorityTx(t *testing.T) {
	blob, err := EncodeAuthoritySet(AuthoritySetPayload{
		Kind: PayloadKindAuthoritySet, Op: AuthorityOpAdd,
		Address: "qsdm1new", EffectiveHeight: 100,
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	tx := &mempool.Tx{
		ContractID: ContractID,
		Payload:    blob,
		Fee:        0.001,
	}
	check := AdmissionChecker(nil)
	if err := check(tx); err != nil {
		t.Errorf("admit rejected valid authority-set tx: %v", err)
	}
}

func TestAdmissionChecker_RejectsBadAuthorityTx(t *testing.T) {
	cases := []struct {
		name string
		blob []byte
	}{
		{
			"empty_address",
			[]byte(`{"kind":"authority-set","op":"add","address":"","effective_height":1}`),
		},
		{
			"unknown_op",
			[]byte(`{"kind":"authority-set","op":"rotate","address":"x","effective_height":1}`),
		},
		{
			"zero_height",
			[]byte(`{"kind":"authority-set","op":"add","address":"x","effective_height":0}`),
		},
	}
	check := AdmissionChecker(nil)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := &mempool.Tx{ContractID: ContractID, Payload: tc.blob, Fee: 0.001}
			if err := check(tx); err == nil {
				t.Errorf("admit accepted invalid authority-set tx (%s)", tc.name)
			}
		})
	}
}

func TestAdmissionChecker_DispatchByKind(t *testing.T) {
	// A param-set decoder must NOT be applied to an
	// authority-set payload (unknown-fields would reject it
	// for the wrong reason). Hand-build an authority-set
	// payload that LOOKS like it could be a param-set if the
	// decoder didn't dispatch correctly.
	blob := []byte(`{"kind":"authority-set","op":"add","address":"x","effective_height":1}`)
	tx := &mempool.Tx{ContractID: ContractID, Payload: blob, Fee: 0.001}
	check := AdmissionChecker(nil)
	if err := check(tx); err != nil {
		t.Errorf("admit rejected valid authority-set via wrong dispatch: %v", err)
	}
}
