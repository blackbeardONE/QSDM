package enrollment

import (
	"errors"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
)

const (
	tNodeID  = "rig-77"
	tGPUUUID = "GPU-12345678-1234-1234-1234-123456789abc"
	tSender  = "alice"
)

func tHMAC() []byte { return []byte("0123456789abcdef0123456789abcdef") }

func mustEnrollPayload(t *testing.T) []byte {
	t.Helper()
	raw, err := EncodeEnrollPayload(EnrollPayload{
		Kind:      PayloadKindEnroll,
		NodeID:    tNodeID,
		GPUUUID:   tGPUUUID,
		HMACKey:   tHMAC(),
		StakeDust: mining.MinEnrollStakeDust,
	})
	if err != nil {
		t.Fatalf("EncodeEnrollPayload: %v", err)
	}
	return raw
}

func mustUnenrollPayload(t *testing.T) []byte {
	t.Helper()
	raw, err := EncodeUnenrollPayload(UnenrollPayload{
		Kind:   PayloadKindUnenroll,
		NodeID: tNodeID,
	})
	if err != nil {
		t.Fatalf("EncodeUnenrollPayload: %v", err)
	}
	return raw
}

func TestAdmissionChecker_AcceptsValidEnroll(t *testing.T) {
	check := AdmissionChecker(nil)
	tx := &mempool.Tx{
		Sender:     tSender,
		ContractID: ContractID,
		Payload:    mustEnrollPayload(t),
		Fee:        0.01,
		Nonce:      0,
	}
	if err := check(tx); err != nil {
		t.Fatalf("valid enroll rejected: %v", err)
	}
}

func TestAdmissionChecker_AcceptsValidUnenroll(t *testing.T) {
	check := AdmissionChecker(nil)
	tx := &mempool.Tx{
		Sender:     tSender,
		ContractID: ContractID,
		Payload:    mustUnenrollPayload(t),
		Fee:        0.001,
	}
	if err := check(tx); err != nil {
		t.Fatalf("valid unenroll rejected: %v", err)
	}
}

func TestAdmissionChecker_RejectsZeroFeeUnenroll(t *testing.T) {
	check := AdmissionChecker(nil)
	tx := &mempool.Tx{
		Sender:     tSender,
		ContractID: ContractID,
		Payload:    mustUnenrollPayload(t),
		Fee:        0,
	}
	err := check(tx)
	if err == nil {
		t.Fatal("zero-fee unenroll should be rejected at admit time")
	}
	if !errors.Is(err, ErrPayloadInvalid) {
		t.Errorf("want ErrPayloadInvalid, got %v", err)
	}
}

func TestAdmissionChecker_RejectsNegativeFeeEnroll(t *testing.T) {
	check := AdmissionChecker(nil)
	tx := &mempool.Tx{
		Sender:     tSender,
		ContractID: ContractID,
		Payload:    mustEnrollPayload(t),
		Fee:        -0.01,
	}
	err := check(tx)
	if err == nil {
		t.Fatal("negative-fee enroll should be rejected at admit time")
	}
	if !errors.Is(err, ErrPayloadInvalid) {
		t.Errorf("want ErrPayloadInvalid, got %v", err)
	}
}

func TestAdmissionChecker_RejectsBadStake(t *testing.T) {
	check := AdmissionChecker(nil)
	raw, err := EncodeEnrollPayload(EnrollPayload{
		Kind:      PayloadKindEnroll,
		NodeID:    tNodeID,
		GPUUUID:   tGPUUUID,
		HMACKey:   tHMAC(),
		StakeDust: mining.MinEnrollStakeDust + 1, // wrong
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	tx := &mempool.Tx{
		Sender:     tSender,
		ContractID: ContractID,
		Payload:    raw,
		Fee:        0.01,
	}
	if err := check(tx); err == nil || !errors.Is(err, ErrStakeMismatch) {
		t.Errorf("want ErrStakeMismatch, got %v", err)
	}
}

func TestAdmissionChecker_RejectsEmptyPayload(t *testing.T) {
	check := AdmissionChecker(nil)
	tx := &mempool.Tx{Sender: tSender, ContractID: ContractID, Fee: 0.01}
	if err := check(tx); err == nil || !errors.Is(err, ErrPayloadInvalid) {
		t.Errorf("want ErrPayloadInvalid, got %v", err)
	}
}

func TestAdmissionChecker_RejectsNilTx(t *testing.T) {
	check := AdmissionChecker(nil)
	if err := check(nil); err == nil {
		t.Fatal("nil tx must be rejected loudly")
	}
}

func TestAdmissionChecker_DelegatesNonEnrollment(t *testing.T) {
	called := false
	prev := func(*mempool.Tx) error { called = true; return nil }
	check := AdmissionChecker(prev)
	tx := &mempool.Tx{ContractID: "qsdm/transfer/v1", Sender: "x"}
	if err := check(tx); err != nil {
		t.Fatalf("delegated check returned err: %v", err)
	}
	if !called {
		t.Error("prev should have been called for non-enrollment tx")
	}
}

func TestAdmissionChecker_NilPrevAllowsTransfer(t *testing.T) {
	check := AdmissionChecker(nil)
	tx := &mempool.Tx{ContractID: "qsdm/transfer/v1", Sender: "x"}
	if err := check(tx); err != nil {
		t.Errorf("nil prev should accept non-enrollment tx, got %v", err)
	}
}

func TestAdmissionChecker_PrevErrorPropagates(t *testing.T) {
	want := errors.New("prev rejected")
	check := AdmissionChecker(func(*mempool.Tx) error { return want })
	tx := &mempool.Tx{ContractID: "qsdm/transfer/v1", Sender: "x"}
	got := check(tx)
	if !errors.Is(got, want) {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestAdmissionChecker_RejectsBadKind(t *testing.T) {
	check := AdmissionChecker(nil)
	// Hand-craft a payload with a bogus kind.
	raw := []byte(`{"kind":"weird","node_id":"rig-77"}`)
	tx := &mempool.Tx{
		Sender:     tSender,
		ContractID: ContractID,
		Payload:    raw,
		Fee:        0.01,
	}
	err := check(tx)
	if err == nil {
		t.Fatal("bogus kind should be rejected")
	}
	if !strings.Contains(err.Error(), "enrollment") {
		t.Errorf("error should be attributed to enrollment subsystem: %v", err)
	}
	if !errors.Is(err, ErrPayloadInvalid) {
		t.Errorf("want ErrPayloadInvalid, got %v", err)
	}
}
