package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

type fakeSubmitter struct {
	added []*mempool.Tx
	err   error
}

func (f *fakeSubmitter) Add(tx *mempool.Tx) error {
	if f.err != nil {
		return f.err
	}
	f.added = append(f.added, tx)
	return nil
}

func mustEnrollPayloadAPI(t *testing.T) []byte {
	t.Helper()
	raw, err := enrollment.EncodeEnrollPayload(enrollment.EnrollPayload{
		Kind:      enrollment.PayloadKindEnroll,
		NodeID:    "rig-77",
		GPUUUID:   "GPU-12345678-1234-1234-1234-123456789abc",
		HMACKey:   []byte("0123456789abcdef0123456789abcdef"),
		StakeDust: mining.MinEnrollStakeDust,
	})
	if err != nil {
		t.Fatalf("EncodeEnrollPayload: %v", err)
	}
	return raw
}

func mustUnenrollPayloadAPI(t *testing.T) []byte {
	t.Helper()
	raw, err := enrollment.EncodeUnenrollPayload(enrollment.UnenrollPayload{
		Kind:   enrollment.PayloadKindUnenroll,
		NodeID: "rig-77",
	})
	if err != nil {
		t.Fatalf("EncodeUnenrollPayload: %v", err)
	}
	return raw
}

func encodeEnrollReq(req EnrollmentSubmitRequest) *bytes.Buffer {
	b, _ := json.Marshal(req)
	return bytes.NewBuffer(b)
}

func TestEnrollmentSubmit_HappyPath(t *testing.T) {
	pool := &fakeSubmitter{}
	SetEnrollmentMempool(pool)
	t.Cleanup(func() { SetEnrollmentMempool(nil) })

	body := EnrollmentSubmitRequest{
		ID:         "tx-1",
		Sender:     "alice",
		Nonce:      0,
		Fee:        0.01,
		ContractID: enrollment.ContractID,
		PayloadB64: base64.StdEncoding.EncodeToString(mustEnrollPayloadAPI(t)),
	}

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mining/enroll", encodeEnrollReq(body))
	rec := httptest.NewRecorder()
	h.EnrollmentSubmitHandler(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if len(pool.added) != 1 {
		t.Fatalf("expected 1 tx admitted, got %d", len(pool.added))
	}
	got := pool.added[0]
	if got.ID != "tx-1" || got.Sender != "alice" || got.ContractID != enrollment.ContractID {
		t.Errorf("tx fields: %+v", got)
	}
	if !bytes.Equal(got.Payload, mustEnrollPayloadAPI(t)) {
		t.Error("payload bytes did not round-trip exactly")
	}
}

func TestEnrollmentSubmit_RejectsWrongMethod(t *testing.T) {
	SetEnrollmentMempool(&fakeSubmitter{})
	t.Cleanup(func() { SetEnrollmentMempool(nil) })
	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/enroll", nil)
	rec := httptest.NewRecorder()
	h.EnrollmentSubmitHandler(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", rec.Code)
	}
}

func TestEnrollmentSubmit_NoMempool_Returns503(t *testing.T) {
	SetEnrollmentMempool(nil)
	h := &Handlers{}
	body := EnrollmentSubmitRequest{
		ID: "x", Sender: "a", ContractID: enrollment.ContractID,
		PayloadB64: base64.StdEncoding.EncodeToString(mustEnrollPayloadAPI(t)),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mining/enroll", encodeEnrollReq(body))
	rec := httptest.NewRecorder()
	h.EnrollmentSubmitHandler(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503", rec.Code)
	}
}

func TestEnrollmentSubmit_BadContractID(t *testing.T) {
	SetEnrollmentMempool(&fakeSubmitter{})
	t.Cleanup(func() { SetEnrollmentMempool(nil) })
	h := &Handlers{}
	body := EnrollmentSubmitRequest{
		ID: "tx-1", Sender: "alice", ContractID: "wrong",
		PayloadB64: base64.StdEncoding.EncodeToString(mustEnrollPayloadAPI(t)),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mining/enroll", encodeEnrollReq(body))
	rec := httptest.NewRecorder()
	h.EnrollmentSubmitHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestEnrollmentSubmit_KindRouteMismatch(t *testing.T) {
	SetEnrollmentMempool(&fakeSubmitter{})
	t.Cleanup(func() { SetEnrollmentMempool(nil) })
	h := &Handlers{}
	// Posting an UNENROLL payload to the ENROLL endpoint must fail.
	body := EnrollmentSubmitRequest{
		ID: "tx-1", Sender: "alice", ContractID: enrollment.ContractID, Fee: 0.001,
		PayloadB64: base64.StdEncoding.EncodeToString(mustUnenrollPayloadAPI(t)),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mining/enroll", encodeEnrollReq(body))
	rec := httptest.NewRecorder()
	h.EnrollmentSubmitHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "kind") {
		t.Errorf("error should mention kind: %s", rec.Body.String())
	}
}

func TestUnenrollmentSubmit_HappyPath(t *testing.T) {
	pool := &fakeSubmitter{}
	SetEnrollmentMempool(pool)
	t.Cleanup(func() { SetEnrollmentMempool(nil) })
	h := &Handlers{}
	body := EnrollmentSubmitRequest{
		ID: "tx-2", Sender: "alice", Nonce: 1, Fee: 0.001,
		ContractID: enrollment.ContractID,
		PayloadB64: base64.StdEncoding.EncodeToString(mustUnenrollPayloadAPI(t)),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mining/unenroll", encodeEnrollReq(body))
	rec := httptest.NewRecorder()
	h.UnenrollmentSubmitHandler(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
}

func TestEnrollmentSubmit_DuplicateTx_Returns409(t *testing.T) {
	pool := &fakeSubmitter{err: mempool.ErrDuplicateTx}
	SetEnrollmentMempool(pool)
	t.Cleanup(func() { SetEnrollmentMempool(nil) })
	h := &Handlers{}
	body := EnrollmentSubmitRequest{
		ID: "tx-1", Sender: "a", ContractID: enrollment.ContractID, Fee: 0.01,
		PayloadB64: base64.StdEncoding.EncodeToString(mustEnrollPayloadAPI(t)),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mining/enroll", encodeEnrollReq(body))
	rec := httptest.NewRecorder()
	h.EnrollmentSubmitHandler(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("status: got %d, want 409", rec.Code)
	}
}

func TestEnrollmentSubmit_MempoolFull_Returns503(t *testing.T) {
	pool := &fakeSubmitter{err: mempool.ErrMempoolFull}
	SetEnrollmentMempool(pool)
	t.Cleanup(func() { SetEnrollmentMempool(nil) })
	h := &Handlers{}
	body := EnrollmentSubmitRequest{
		ID: "tx-1", Sender: "a", ContractID: enrollment.ContractID, Fee: 0.01,
		PayloadB64: base64.StdEncoding.EncodeToString(mustEnrollPayloadAPI(t)),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mining/enroll", encodeEnrollReq(body))
	rec := httptest.NewRecorder()
	h.EnrollmentSubmitHandler(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503", rec.Code)
	}
}

func TestEnrollmentSubmit_GenericPoolError_Returns400(t *testing.T) {
	pool := &fakeSubmitter{err: errors.New("admit gate rejected: bad payload")}
	SetEnrollmentMempool(pool)
	t.Cleanup(func() { SetEnrollmentMempool(nil) })
	h := &Handlers{}
	body := EnrollmentSubmitRequest{
		ID: "tx-1", Sender: "a", ContractID: enrollment.ContractID, Fee: 0.01,
		PayloadB64: base64.StdEncoding.EncodeToString(mustEnrollPayloadAPI(t)),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mining/enroll", encodeEnrollReq(body))
	rec := httptest.NewRecorder()
	h.EnrollmentSubmitHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestEnrollmentSubmit_RejectsBadBase64(t *testing.T) {
	SetEnrollmentMempool(&fakeSubmitter{})
	t.Cleanup(func() { SetEnrollmentMempool(nil) })
	h := &Handlers{}
	body := EnrollmentSubmitRequest{
		ID: "tx-1", Sender: "a", ContractID: enrollment.ContractID, Fee: 0.01,
		PayloadB64: "@@@not-base64@@@",
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mining/enroll", encodeEnrollReq(body))
	rec := httptest.NewRecorder()
	h.EnrollmentSubmitHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}
