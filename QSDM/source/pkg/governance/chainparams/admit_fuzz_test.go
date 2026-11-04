package chainparams

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// FuzzGovAdmission runs the stateless mempool admission gate for governance
// transactions (AdmissionChecker, the outermost layer of the gate every
// mempool.Add passes: a follower's qsdm-transactions ingress and the API
// alike) on a transaction whose payload and fee come from the input.
func FuzzGovAdmission(f *testing.F) {
	paramSet, err := EncodeParamSet(ParamSetPayload{Kind: PayloadKindParamSet, Param: string(ParamRewardBPS),
		Value: 2500, EffectiveHeight: 1000, Memo: "raise slasher reward"})
	if err != nil {
		f.Fatal(err)
	}
	authority, err := EncodeAuthoritySet(AuthoritySetPayload{Kind: PayloadKindAuthoritySet, Op: AuthorityOpAdd,
		Address: "qsdm1authority", EffectiveHeight: 2000})
	if err != nil {
		f.Fatal(err)
	}
	for _, payload := range [][]byte{paramSet, authority} {
		if err := AdmissionChecker(nil)(&mempool.Tx{ID: "gov-1", Fee: 0.01, ContractID: ContractID, Payload: payload}); err != nil {
			f.Fatalf("genuine seed rejected: %v", err)
		}
	}
	for _, s := range []struct {
		payload  []byte
		fee      float64
		contract string
	}{
		{paramSet, 0.01, ContractID},
		{authority, 0.01, ContractID},
		{paramSet, 0, ContractID},
		{authority, 0.01, "qsdm/slash/v1"},
		{bytes.Replace(paramSet, []byte(`"value":2500`), []byte(`"value":5001`), 1), 1, ContractID},
		{bytes.Replace(paramSet, []byte(string(ParamRewardBPS)), []byte("reward_bps_unknown"), 1), 1, ContractID},
		{bytes.Replace(authority, []byte(`"add"`), []byte(`"rotate"`), 1), 1, ContractID},
		{bytes.Replace(authority, []byte("qsdm1authority"), []byte("qsdm1 authority"), 1), 1, ContractID},
		{[]byte(`{"kind":"param-set","param":"reward_bps","value":1,"effective_height":0}`), 1, ContractID},
		{[]byte(`{"kind":"authority-set"} {}`), 1, ContractID},
		{[]byte(`{"kind":"param-set","kind":"authority-set"}`), 1, ContractID},
	} {
		f.Add(s.payload, s.fee, s.contract)
	}

	errPrev := errors.New("delegated")
	f.Fuzz(func(t *testing.T, payload []byte, fee float64, contractID string) {
		tx := &mempool.Tx{ID: "gov-fuzz", Sender: "authority", Fee: fee, ContractID: contractID, Payload: payload}
		orig := *tx
		orig.Payload = bytes.Clone(payload)
		delegated := 0
		err := AdmissionChecker(func(*mempool.Tx) error { delegated++; return errPrev })(tx)
		if !reflect.DeepEqual(*tx, orig) {
			t.Fatal("the admission gate modified the transaction")
		}
		if contractID != ContractID {
			if delegated != 1 || !errors.Is(err, errPrev) {
				t.Fatalf("a %q transaction was not handed to the next gate (%d calls, %v)", contractID, delegated, err)
			}
			return
		}
		if delegated != 0 {
			t.Fatal("a governance transaction was handed to the next gate")
		}
		if err != nil {
			return
		}
		if !(fee > 0) {
			t.Fatalf("admitted fee %v", fee)
		}
		kind, err := PeekKind(payload)
		if err != nil {
			t.Fatalf("admitted a payload without a known kind: %v", err)
		}
		switch kind {
		case PayloadKindParamSet:
			p, err := ParseParamSet(payload)
			if err != nil {
				t.Fatalf("admitted an undecodable param-set: %v", err)
			}
			spec, ok := Lookup(p.Param)
			if !ok || p.Value < spec.MinValue || p.Value > spec.MaxValue || p.EffectiveHeight == 0 || len(p.Memo) > MaxMemoLen {
				t.Fatalf("admitted param-set %+v", p)
			}
			again, err := EncodeParamSet(*p)
			if back, perr := ParseParamSet(again); err != nil || perr != nil || !reflect.DeepEqual(back, p) {
				t.Fatalf("an admitted param-set does not round-trip: %v %v", err, perr)
			}
		case PayloadKindAuthoritySet:
			p, err := ParseAuthoritySet(payload)
			if err != nil {
				t.Fatalf("admitted an undecodable authority-set: %v", err)
			}
			if p.Op != AuthorityOpAdd && p.Op != AuthorityOpRemove || p.Address == "" ||
				len(p.Address) > MaxAuthorityAddressLen || p.EffectiveHeight == 0 || len(p.Memo) > MaxMemoLen {
				t.Fatalf("admitted authority-set %+v", p)
			}
			for i := 0; i < len(p.Address); i++ {
				if c := p.Address[i]; c <= 0x20 || c == 0x7f {
					t.Fatalf("admitted authority address %q", p.Address)
				}
			}
			again, err := EncodeAuthoritySet(*p)
			if back, perr := ParseAuthoritySet(again); err != nil || perr != nil || !reflect.DeepEqual(back, p) {
				t.Fatalf("an admitted authority-set does not round-trip: %v %v", err, perr)
			}
		default:
			t.Fatalf("admitted kind %q", kind)
		}
	})
}
