package slashing

import (
	"bytes"
	"errors"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

const (
	tSubmitter = "qsdm1submitter"
	tNodeID    = "alice-rtx4090-01"
)

// validSlashPayload returns a well-formed encoded SlashPayload
// suitable for the happy-path tests. Centralised so the field
// shape stays in one place.
func validSlashPayload(t *testing.T) []byte {
	t.Helper()
	raw, err := EncodeSlashPayload(SlashPayload{
		NodeID:          tNodeID,
		EvidenceKind:    EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte("opaque-evidence-bytes"),
		SlashAmountDust: 5 * 100_000_000, // 5 CELL
		Memo:            "test",
	})
	if err != nil {
		t.Fatalf("EncodeSlashPayload: %v", err)
	}
	return raw
}

func TestAdmissionChecker_AcceptsValidSlash(t *testing.T) {
	check := AdmissionChecker(nil)
	tx := &mempool.Tx{
		Sender:     tSubmitter,
		ContractID: ContractID,
		Payload:    validSlashPayload(t),
		Fee:        0.001,
	}
	if err := check(tx); err != nil {
		t.Fatalf("valid slash rejected: %v", err)
	}
}

func TestAdmissionChecker_RejectsNilTx(t *testing.T) {
	if err := AdmissionChecker(nil)(nil); err == nil {
		t.Fatal("nil tx must be rejected loudly")
	}
}

func TestAdmissionChecker_RejectsEmptyPayload(t *testing.T) {
	check := AdmissionChecker(nil)
	tx := &mempool.Tx{Sender: tSubmitter, ContractID: ContractID, Fee: 0.001}
	if err := check(tx); err == nil || !errors.Is(err, ErrPayloadInvalid) {
		t.Fatalf("empty payload not rejected as invalid: err=%v", err)
	}
}

func TestAdmissionChecker_RejectsBadDecode(t *testing.T) {
	check := AdmissionChecker(nil)
	tx := &mempool.Tx{
		Sender:     tSubmitter,
		ContractID: ContractID,
		Payload:    []byte(`{"node_id":}`), // malformed JSON
		Fee:        0.001,
	}
	err := check(tx)
	if err == nil || !errors.Is(err, ErrPayloadDecode) {
		t.Fatalf("bad decode not surfaced as ErrPayloadDecode: err=%v", err)
	}
}

func TestAdmissionChecker_RejectsUnknownKind(t *testing.T) {
	check := AdmissionChecker(nil)
	raw, err := EncodeSlashPayload(SlashPayload{
		NodeID:          tNodeID,
		EvidenceKind:    EvidenceKind("not-a-real-kind"),
		EvidenceBlob:    []byte("x"),
		SlashAmountDust: 1,
	})
	if err != nil {
		t.Fatalf("EncodeSlashPayload: %v", err)
	}
	tx := &mempool.Tx{Sender: tSubmitter, ContractID: ContractID, Payload: raw, Fee: 0.001}
	if err := check(tx); err == nil || !errors.Is(err, ErrUnknownEvidenceKind) {
		t.Fatalf("unknown kind not rejected: err=%v", err)
	}
}

func TestAdmissionChecker_RejectsZeroSlashAmount(t *testing.T) {
	check := AdmissionChecker(nil)
	raw, err := EncodeSlashPayload(SlashPayload{
		NodeID:       tNodeID,
		EvidenceKind: EvidenceKindForgedAttestation,
		EvidenceBlob: []byte("x"),
	})
	if err != nil {
		t.Fatalf("EncodeSlashPayload: %v", err)
	}
	tx := &mempool.Tx{Sender: tSubmitter, ContractID: ContractID, Payload: raw, Fee: 0.001}
	if err := check(tx); err == nil || !errors.Is(err, ErrPayloadInvalid) {
		t.Fatalf("zero slash amount not rejected as invalid: err=%v", err)
	}
}

func TestAdmissionChecker_RejectsOversizedEvidence(t *testing.T) {
	check := AdmissionChecker(nil)
	raw, err := EncodeSlashPayload(SlashPayload{
		NodeID:          tNodeID,
		EvidenceKind:    EvidenceKindForgedAttestation,
		EvidenceBlob:    bytes.Repeat([]byte{0x42}, MaxEvidenceLen+1),
		SlashAmountDust: 1,
	})
	if err != nil {
		t.Fatalf("EncodeSlashPayload: %v", err)
	}
	tx := &mempool.Tx{Sender: tSubmitter, ContractID: ContractID, Payload: raw, Fee: 0.001}
	if err := check(tx); err == nil || !errors.Is(err, ErrPayloadInvalid) {
		t.Fatalf("oversized evidence not rejected: err=%v", err)
	}
}

func TestAdmissionChecker_RejectsZeroFee(t *testing.T) {
	check := AdmissionChecker(nil)
	tx := &mempool.Tx{
		Sender:     tSubmitter,
		ContractID: ContractID,
		Payload:    validSlashPayload(t),
		Fee:        0, // missing fee
	}
	if err := check(tx); err == nil || !errors.Is(err, ErrPayloadInvalid) {
		t.Fatalf("zero fee not rejected: err=%v", err)
	}
}

func TestAdmissionChecker_RejectsNegativeFee(t *testing.T) {
	check := AdmissionChecker(nil)
	tx := &mempool.Tx{
		Sender:     tSubmitter,
		ContractID: ContractID,
		Payload:    validSlashPayload(t),
		Fee:        -0.01,
	}
	if err := check(tx); err == nil || !errors.Is(err, ErrPayloadInvalid) {
		t.Fatalf("negative fee not rejected: err=%v", err)
	}
}

func TestAdmissionChecker_DelegatesNonSlashing(t *testing.T) {
	called := false
	prev := func(*mempool.Tx) error { called = true; return nil }
	check := AdmissionChecker(prev)
	tx := &mempool.Tx{ContractID: "qsdm/transfer/v1", Sender: "x"}
	if err := check(tx); err != nil {
		t.Fatalf("transfer rejected by slash gate: %v", err)
	}
	if !called {
		t.Error("prev gate not consulted for non-slashing tx")
	}
}

func TestAdmissionChecker_NilPrevAllowsTransfer(t *testing.T) {
	check := AdmissionChecker(nil)
	tx := &mempool.Tx{ContractID: "qsdm/transfer/v1", Sender: "x"}
	if err := check(tx); err != nil {
		t.Fatalf("transfer rejected when prev=nil: %v", err)
	}
}

func TestAdmissionChecker_PrevErrorPropagates(t *testing.T) {
	want := errors.New("prev rejected")
	check := AdmissionChecker(func(*mempool.Tx) error { return want })
	tx := &mempool.Tx{ContractID: "qsdm/transfer/v1", Sender: "x"}
	got := check(tx)
	if !errors.Is(got, want) {
		t.Errorf("prev error not propagated: got %v, want %v", got, want)
	}
}

func TestAdmissionChecker_RejectsEmptySender(t *testing.T) {
	check := AdmissionChecker(nil)
	tx := &mempool.Tx{
		ContractID: ContractID,
		Payload:    validSlashPayload(t),
		Fee:        0.001,
	}
	if err := check(tx); err == nil || !errors.Is(err, ErrPayloadInvalid) {
		t.Fatalf("empty sender not rejected: err=%v", err)
	}
}
