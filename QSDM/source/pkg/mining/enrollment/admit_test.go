package enrollment

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

const (
	tNodeID  = "rig-77"
	tGPUUUID = "GPU-12345678-1234-1234-1234-123456789abc"
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

func mustSignedEnrollmentTx(t *testing.T, payload []byte, fee float64) *mempool.Tx {
	t.Helper()
	pk, sk, err := mldsa87.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pub, _ := pk.MarshalBinary()
	sum := sha256.Sum256(pub)
	tx := &mempool.Tx{
		ID: "enrollment-test", Sender: hex.EncodeToString(sum[:]),
		ContractID: SignedContractID, Payload: payload, Fee: fee,
	}
	env, _ := EnvelopeFromTransaction(tx)
	canonical, _ := env.CanonicalBytes()
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(sk, canonical, nil, true, sig); err != nil {
		t.Fatalf("SignTo: %v", err)
	}
	tx.PublicKey = hex.EncodeToString(pub)
	tx.Signature = hex.EncodeToString(sig)
	return tx
}

func TestAdmissionChecker_AcceptsValidEnroll(t *testing.T) {
	check := AdmissionChecker(nil)
	tx := mustSignedEnrollmentTx(t, mustEnrollPayload(t), 0.01)
	if err := check(tx); err != nil {
		t.Fatalf("valid enroll rejected: %v", err)
	}
}

func TestAdmissionChecker_AcceptsValidUnenroll(t *testing.T) {
	check := AdmissionChecker(nil)
	tx := mustSignedEnrollmentTx(t, mustUnenrollPayload(t), 0.001)
	if err := check(tx); err != nil {
		t.Fatalf("valid unenroll rejected: %v", err)
	}
}

func TestAdmissionChecker_AcceptsZeroFeeUnenroll(t *testing.T) {
	check := AdmissionChecker(nil)
	tx := mustSignedEnrollmentTx(t, mustUnenrollPayload(t), 0)
	if err := check(tx); err != nil {
		t.Fatalf("signed zero-fee unenroll rejected: %v", err)
	}
}

func TestAdmissionChecker_RejectsNegativeFeeEnroll(t *testing.T) {
	check := AdmissionChecker(nil)
	tx := mustSignedEnrollmentTx(t, mustEnrollPayload(t), -0.01)
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
	tx := mustSignedEnrollmentTx(t, raw, 0.01)
	if err := check(tx); err == nil || !errors.Is(err, ErrStakeMismatch) {
		t.Errorf("want ErrStakeMismatch, got %v", err)
	}
}

func TestAdmissionChecker_RejectsEmptyPayload(t *testing.T) {
	check := AdmissionChecker(nil)
	tx := mustSignedEnrollmentTx(t, nil, 0.01)
	if err := check(tx); err == nil || !errors.Is(err, ErrSignedEnvelopeRequired) {
		t.Errorf("want ErrSignedEnvelopeRequired, got %v", err)
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
	tx := mustSignedEnrollmentTx(t, raw, 0.01)
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

func TestAdmissionChecker_RejectsLegacyUnsignedEnrollment(t *testing.T) {
	tx := &mempool.Tx{ID: "legacy", Sender: "alice", ContractID: ContractID, Payload: mustEnrollPayload(t)}
	if err := AdmissionChecker(nil)(tx); !errors.Is(err, ErrLegacyContractDisabled) {
		t.Fatalf("legacy unsigned enrollment: got %v, want ErrLegacyContractDisabled", err)
	}
}

func TestAdmissionChecker_RejectsTamperedSignedEnvelope(t *testing.T) {
	tx := mustSignedEnrollmentTx(t, mustEnrollPayload(t), 0.01)
	tx.Nonce++
	if err := AdmissionChecker(nil)(tx); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("tampered nonce: got %v, want ErrSignatureInvalid", err)
	}
}
