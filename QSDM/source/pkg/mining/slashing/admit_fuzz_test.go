package slashing

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// FuzzSlashAdmission runs the stateless mempool admission gate for slashing
// transactions (AdmissionChecker, one layer of the gate every mempool.Add
// passes: a follower's qsdm-transactions ingress and the API alike) on a
// transaction whose payload and fields come from the input.
func FuzzSlashAdmission(f *testing.F) {
	encode := func(p SlashPayload) []byte {
		raw, err := EncodeSlashPayload(p)
		if err != nil {
			f.Fatal(err)
		}
		return raw
	}
	valid := encode(SlashPayload{NodeID: "rig-77", EvidenceKind: EvidenceKindDoubleMining,
		EvidenceBlob: []byte(`{"proof_a":"...","proof_b":"..."}`), SlashAmountDust: 5_000_000, Memo: "double mined at 42"})
	if err := AdmissionChecker(nil)(&mempool.Tx{ID: "slash-1", Sender: "slasher", Fee: 0.01, ContractID: ContractID,
		Payload: valid}); err != nil {
		f.Fatalf("genuine seed rejected: %v", err)
	}
	for _, s := range []struct {
		payload  []byte
		sender   string
		fee      float64
		contract string
	}{
		{valid, "slasher", 0.01, ContractID},
		{valid, "slasher", 0, ContractID},
		{valid, "", 0.01, ContractID},
		{valid, "slasher", 0.01, "qsdm/enroll/v2"},
		{encode(SlashPayload{NodeID: "n", EvidenceKind: EvidenceKindForgedAttestation, EvidenceBlob: []byte{0},
			SlashAmountDust: 1}), "s", 1, ContractID},
		{encode(SlashPayload{NodeID: "n", EvidenceKind: EvidenceKindFreshnessCheat, EvidenceBlob: []byte{0},
			SlashAmountDust: 1}), "s", 1, ContractID},
		{bytes.Replace(valid, []byte(`"double-mining"`), []byte(`"double-spend"`), 1), "s", 1, ContractID},
		{append(bytes.Clone(valid), ' ', '{', '}'), "s", 1, ContractID},
		{[]byte(`{"node_id":"n","evidence_kind":"double-mining","evidence_blob":"AA==","slash_amount_dust":1,"x":1}`), "s", 1, ContractID},
		{nil, "s", 1, ContractID},
	} {
		f.Add(s.payload, s.sender, s.fee, s.contract)
	}

	errPrev := errors.New("delegated")
	f.Fuzz(func(t *testing.T, payload []byte, sender string, fee float64, contractID string) {
		tx := &mempool.Tx{ID: "slash-fuzz", Sender: sender, Fee: fee, ContractID: contractID, Payload: payload}
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
			t.Fatal("a slashing transaction was handed to the next gate")
		}
		if err != nil {
			if !errors.Is(err, ErrPayloadInvalid) && !errors.Is(err, ErrPayloadDecode) && !errors.Is(err, ErrUnknownEvidenceKind) {
				t.Fatalf("refusal wraps no slashing sentinel: %v", err)
			}
			return
		}
		p, derr := DecodeSlashPayload(payload)
		if derr != nil || sender == "" || !(fee > 0) {
			t.Fatalf("admitted payload %q from %q with fee %v (%v)", payload, sender, fee, derr)
		}
		kindOK := false
		for _, k := range AllEvidenceKinds {
			kindOK = kindOK || p.EvidenceKind == k
		}
		if p.NodeID == "" || len(p.NodeID) > MaxNodeIDLen || !kindOK || len(p.EvidenceBlob) == 0 ||
			len(p.EvidenceBlob) > MaxEvidenceLen || p.SlashAmountDust == 0 || len(p.Memo) > MaxMemoLen {
			t.Fatalf("admitted slash payload %+v", p)
		}
		again, err := EncodeSlashPayload(p)
		if err != nil {
			t.Fatalf("an admitted payload does not re-encode: %v", err)
		}
		if back, err := DecodeSlashPayload(again); err != nil || !reflect.DeepEqual(back, p) {
			t.Fatalf("decode(encode(p)) differs from p: %v", err)
		}
	})
}
