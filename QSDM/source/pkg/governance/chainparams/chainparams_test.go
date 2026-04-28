package chainparams

// chainparams_test.go: covers Registry / ParamSpec validation,
// the wire codec (ParseParamSet / EncodeParamSet /
// ValidateParamSetFields), the admission gate, and
// InMemoryParamStore.
//
// Test posture:
//
//   - Registry tests assert every shipped ParamSpec satisfies
//     Validate(), is unique, and exposes usable bounds.
//   - Codec tests cover happy path + every rejection branch
//     (decode failure, unknown field, missing field, kind tag,
//     unknown param, out-of-bounds, oversized memo, zero
//     EffectiveHeight).
//   - Store tests cover Stage / Promote / supersede /
//     concurrent-read safety.
//   - Admission tests assert the layered checker delegates
//     non-gov ContractIDs to `prev` correctly.

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// -----------------------------------------------------------------------------
// Registry
// -----------------------------------------------------------------------------

func TestRegistry_AllSpecsValid(t *testing.T) {
	for _, spec := range Registry() {
		if err := spec.Validate(); err != nil {
			t.Errorf("ParamSpec %q invalid: %v", spec.Name, err)
		}
	}
}

func TestRegistry_NamesUniqueAndKnown(t *testing.T) {
	want := map[string]bool{
		string(ParamRewardBPS):              true,
		string(ParamAutoRevokeMinStakeDust): true,
	}
	got := make(map[string]bool, len(Registry()))
	for _, spec := range Registry() {
		if got[string(spec.Name)] {
			t.Errorf("duplicate ParamSpec.Name %q", spec.Name)
		}
		got[string(spec.Name)] = true
	}
	for k := range want {
		if !got[k] {
			t.Errorf("missing ParamSpec.Name %q", k)
		}
	}
	for k := range got {
		if !want[k] {
			t.Logf("registry has additional param %q (not yet covered by this test)", k)
		}
	}
}

func TestRegistry_LookupAndCheckBounds(t *testing.T) {
	spec, ok := Lookup(string(ParamRewardBPS))
	if !ok {
		t.Fatalf("Lookup(reward_bps) returned false")
	}
	if err := spec.CheckBounds(0); err != nil {
		t.Errorf("min=0 should be in bounds; got %v", err)
	}
	if err := spec.CheckBounds(5000); err != nil {
		t.Errorf("max=5000 should be in bounds; got %v", err)
	}
	if err := spec.CheckBounds(5001); err == nil {
		t.Error("5001 should be out of bounds")
	}

	if _, ok := Lookup("does-not-exist"); ok {
		t.Error("Lookup of unknown param returned ok=true")
	}
}

func TestParamSpec_Validate_RejectsBadShape(t *testing.T) {
	cases := map[string]ParamSpec{
		"empty name":  {Name: "", MinValue: 0, MaxValue: 10, DefaultValue: 0},
		"bad name":    {Name: "Bad-Name", MinValue: 0, MaxValue: 10, DefaultValue: 0},
		"min>max":     {Name: "test_param", MinValue: 10, MaxValue: 5, DefaultValue: 7},
		"default<min": {Name: "test_param", MinValue: 5, MaxValue: 10, DefaultValue: 1},
		"default>max": {Name: "test_param", MinValue: 5, MaxValue: 10, DefaultValue: 99},
	}
	for label, spec := range cases {
		if err := spec.Validate(); err == nil {
			t.Errorf("%s: expected error, got nil", label)
		}
	}
}

// -----------------------------------------------------------------------------
// Codec
// -----------------------------------------------------------------------------

func TestEncodeParamSet_RoundTrip(t *testing.T) {
	in := ParamSetPayload{
		Kind:            PayloadKindParamSet,
		Param:           string(ParamRewardBPS),
		Value:           2500,
		EffectiveHeight: 12345,
		Memo:            "lower reward share",
	}
	raw, err := EncodeParamSet(in)
	if err != nil {
		t.Fatalf("EncodeParamSet: %v", err)
	}
	out, err := ParseParamSet(raw)
	if err != nil {
		t.Fatalf("ParseParamSet: %v", err)
	}
	if *out != in {
		t.Errorf("round-trip mismatch:\n  in  = %+v\n  out = %+v", in, *out)
	}
}

func TestParseParamSet_RejectsBadInput(t *testing.T) {
	cases := map[string][]byte{
		"empty":            nil,
		"not json":         []byte("not json"),
		"unknown field":    []byte(`{"kind":"param-set","param":"reward_bps","value":1000,"effective_height":10,"extra":"junk"}`),
		"trailing bytes":   []byte(`{"kind":"param-set","param":"reward_bps","value":1000,"effective_height":10}{}`),
		"wrong kind tag":   []byte(`{"kind":"some-other","param":"reward_bps","value":1000,"effective_height":10}`),
	}
	for label, raw := range cases {
		if _, err := ParseParamSet(raw); err == nil {
			t.Errorf("%s: expected error", label)
		}
	}
}

func TestValidateParamSetFields_RejectsBadFields(t *testing.T) {
	good := ParamSetPayload{
		Kind:            PayloadKindParamSet,
		Param:           string(ParamRewardBPS),
		Value:           1000,
		EffectiveHeight: 10,
	}
	cases := map[string]func(p *ParamSetPayload){
		"unknown param":  func(p *ParamSetPayload) { p.Param = "no_such_param" },
		"empty param":    func(p *ParamSetPayload) { p.Param = "" },
		"out of bounds":  func(p *ParamSetPayload) { p.Value = 99999 },
		"zero height":    func(p *ParamSetPayload) { p.EffectiveHeight = 0 },
		"oversized memo": func(p *ParamSetPayload) { p.Memo = strings.Repeat("x", MaxMemoLen+1) },
		"wrong kind":     func(p *ParamSetPayload) { p.Kind = "set-param" },
	}
	for label, mut := range cases {
		p := good
		mut(&p)
		if err := ValidateParamSetFields(&p); err == nil {
			t.Errorf("%s: expected error", label)
		}
	}
}

func TestValidateParamSetFields_HappyPath(t *testing.T) {
	for _, spec := range Registry() {
		p := ParamSetPayload{
			Kind:            PayloadKindParamSet,
			Param:           string(spec.Name),
			Value:           spec.MinValue,
			EffectiveHeight: 1,
		}
		if err := ValidateParamSetFields(&p); err != nil {
			t.Errorf("min for %q rejected: %v", spec.Name, err)
		}
		p.Value = spec.MaxValue
		if err := ValidateParamSetFields(&p); err != nil {
			t.Errorf("max for %q rejected: %v", spec.Name, err)
		}
	}
}

func TestValidateParamSetFields_ErrorWrapping(t *testing.T) {
	cases := []struct {
		name    string
		payload ParamSetPayload
		want    error
	}{
		{
			name: "unknown param",
			payload: ParamSetPayload{
				Kind: PayloadKindParamSet, Param: "nope",
				Value: 0, EffectiveHeight: 1,
			},
			want: ErrUnknownParam,
		},
		{
			name: "out of bounds",
			payload: ParamSetPayload{
				Kind: PayloadKindParamSet, Param: string(ParamRewardBPS),
				Value: 99999, EffectiveHeight: 1,
			},
			want: ErrValueOutOfBounds,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateParamSetFields(&tc.payload)
			if !errors.Is(err, tc.want) {
				t.Errorf("got %v, want errors.Is(_, %v)", err, tc.want)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Store
// -----------------------------------------------------------------------------

func TestInMemoryParamStore_DefaultsLoaded(t *testing.T) {
	s := NewInMemoryParamStore()
	for _, spec := range Registry() {
		v, ok := s.ActiveValue(string(spec.Name))
		if !ok {
			t.Errorf("ActiveValue(%q) ok=false", spec.Name)
			continue
		}
		if v != spec.DefaultValue {
			t.Errorf("ActiveValue(%q)=%d, want default %d",
				spec.Name, v, spec.DefaultValue)
		}
	}
}

func TestInMemoryParamStore_StageThenPromote(t *testing.T) {
	s := NewInMemoryParamStore()
	change := ParamChange{
		Param:             string(ParamRewardBPS),
		Value:             2500,
		EffectiveHeight:   100,
		SubmittedAtHeight: 50,
		Authority:         "alice",
	}
	prior, hadPrior, err := s.Stage(change)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if hadPrior {
		t.Errorf("first Stage should not report a prior; got %+v", prior)
	}

	if v, _ := s.ActiveValue(string(ParamRewardBPS)); v != 0 {
		t.Errorf("active value before promote should still be default 0; got %d", v)
	}

	// Promote at height < EffectiveHeight: no-op.
	if got := s.Promote(99); len(got) != 0 {
		t.Errorf("Promote(99) returned %v, want empty", got)
	}

	// Promote at height == EffectiveHeight: activates.
	got := s.Promote(100)
	if len(got) != 1 || got[0].Param != string(ParamRewardBPS) {
		t.Fatalf("Promote(100) returned %v, want one reward_bps change", got)
	}
	if v, _ := s.ActiveValue(string(ParamRewardBPS)); v != 2500 {
		t.Errorf("post-promote active = %d, want 2500", v)
	}

	// Idempotent.
	if got := s.Promote(101); len(got) != 0 {
		t.Errorf("second Promote returned %v, want empty", got)
	}
}

func TestInMemoryParamStore_Stage_Supersede(t *testing.T) {
	s := NewInMemoryParamStore()
	first := ParamChange{
		Param:           string(ParamRewardBPS),
		Value:           1000,
		EffectiveHeight: 100,
	}
	second := ParamChange{
		Param:           string(ParamRewardBPS),
		Value:           2500,
		EffectiveHeight: 200,
	}
	if _, hadPrior, err := s.Stage(first); err != nil || hadPrior {
		t.Fatalf("first Stage: hadPrior=%v err=%v", hadPrior, err)
	}
	prior, hadPrior, err := s.Stage(second)
	if err != nil {
		t.Fatalf("second Stage: %v", err)
	}
	if !hadPrior {
		t.Error("second Stage should report hadPrior=true")
	}
	if prior.Value != 1000 || prior.EffectiveHeight != 100 {
		t.Errorf("prior=%+v, want {value=1000, height=100}", prior)
	}
	pending, ok := s.Pending(string(ParamRewardBPS))
	if !ok || pending.Value != 2500 || pending.EffectiveHeight != 200 {
		t.Errorf("pending=%+v ok=%v, want {value=2500, height=200}", pending, ok)
	}
}

func TestInMemoryParamStore_Stage_RejectsInvalid(t *testing.T) {
	s := NewInMemoryParamStore()
	cases := map[string]ParamChange{
		"unknown": {Param: "nope", Value: 0, EffectiveHeight: 1},
		"oob":     {Param: string(ParamRewardBPS), Value: 99999, EffectiveHeight: 1},
		"zero h":  {Param: string(ParamRewardBPS), Value: 0, EffectiveHeight: 0},
	}
	for label, change := range cases {
		if _, _, err := s.Stage(change); err == nil {
			t.Errorf("%s: expected error", label)
		}
	}
}

func TestInMemoryParamStore_Promote_OrderingDeterministic(t *testing.T) {
	s := NewInMemoryParamStore()
	// Two changes ready at the same height for two different
	// params — promotion order must be lexicographic on name.
	if _, _, err := s.Stage(ParamChange{
		Param:           string(ParamAutoRevokeMinStakeDust),
		Value:           dustPerCELL * 2,
		EffectiveHeight: 50,
	}); err != nil {
		t.Fatalf("Stage auto_revoke: %v", err)
	}
	if _, _, err := s.Stage(ParamChange{
		Param:           string(ParamRewardBPS),
		Value:           1000,
		EffectiveHeight: 50,
	}); err != nil {
		t.Fatalf("Stage reward_bps: %v", err)
	}
	got := s.Promote(50)
	if len(got) != 2 {
		t.Fatalf("Promote returned %d changes, want 2", len(got))
	}
	// Both at same height → name ascending.
	if got[0].Param != string(ParamAutoRevokeMinStakeDust) ||
		got[1].Param != string(ParamRewardBPS) {
		t.Errorf("promotion order = [%s, %s], want lex by name",
			got[0].Param, got[1].Param)
	}
}

func TestInMemoryParamStore_AllPending_Sorted(t *testing.T) {
	s := NewInMemoryParamStore()
	_, _, _ = s.Stage(ParamChange{
		Param: string(ParamRewardBPS), Value: 100, EffectiveHeight: 200,
	})
	_, _, _ = s.Stage(ParamChange{
		Param: string(ParamAutoRevokeMinStakeDust), Value: dustPerCELL * 2, EffectiveHeight: 100,
	})
	all := s.AllPending()
	if len(all) != 2 {
		t.Fatalf("AllPending returned %d, want 2", len(all))
	}
	if all[0].EffectiveHeight != 100 || all[1].EffectiveHeight != 200 {
		t.Errorf("AllPending not sorted by height: %v", all)
	}
}

func TestInMemoryParamStore_ConcurrentReads(t *testing.T) {
	s := NewInMemoryParamStore()
	const N = 64
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s.ActiveValue(string(ParamRewardBPS))
				s.Pending(string(ParamRewardBPS))
				s.AllActive()
				s.AllPending()
			}
		}()
	}
	wg.Wait()
}

// -----------------------------------------------------------------------------
// AdmissionChecker
// -----------------------------------------------------------------------------

func TestAdmissionChecker_DelegatesNonGov(t *testing.T) {
	delegated := false
	prev := func(*mempool.Tx) error {
		delegated = true
		return nil
	}
	checker := AdmissionChecker(prev)
	tx := &mempool.Tx{ContractID: "qsdm/transfer/v1", Payload: []byte("anything"), Fee: 1}
	if err := checker(tx); err != nil {
		t.Errorf("non-gov tx rejected: %v", err)
	}
	if !delegated {
		t.Error("prev not called for non-gov tx")
	}
}

func TestAdmissionChecker_AcceptsValidGovTx(t *testing.T) {
	checker := AdmissionChecker(nil)
	raw, err := EncodeParamSet(ParamSetPayload{
		Kind:            PayloadKindParamSet,
		Param:           string(ParamRewardBPS),
		Value:           2500,
		EffectiveHeight: 100,
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	tx := &mempool.Tx{
		ContractID: ContractID,
		Payload:    raw,
		Fee:        1,
		Sender:     "alice",
	}
	if err := checker(tx); err != nil {
		t.Errorf("valid gov tx rejected: %v", err)
	}
}

func TestAdmissionChecker_RejectsBadGovTx(t *testing.T) {
	checker := AdmissionChecker(nil)
	cases := map[string]*mempool.Tx{
		"empty payload": {
			ContractID: ContractID, Payload: nil, Fee: 1, Sender: "alice",
		},
		"junk payload": {
			ContractID: ContractID, Payload: []byte("not json"), Fee: 1, Sender: "alice",
		},
		"zero fee": func() *mempool.Tx {
			raw, _ := EncodeParamSet(ParamSetPayload{
				Kind: PayloadKindParamSet, Param: string(ParamRewardBPS),
				Value: 1000, EffectiveHeight: 10,
			})
			return &mempool.Tx{
				ContractID: ContractID, Payload: raw, Fee: 0, Sender: "alice",
			}
		}(),
	}
	for label, tx := range cases {
		if err := checker(tx); err == nil {
			t.Errorf("%s: expected rejection", label)
		}
	}
}

func TestAdmissionChecker_NilTx(t *testing.T) {
	checker := AdmissionChecker(nil)
	if err := checker(nil); err == nil {
		t.Error("nil tx should be rejected")
	}
}
