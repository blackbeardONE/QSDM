package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
)

// reuses fakeSubmitter from handlers_enrollment_test.go (same package).

func mustSlashPayloadAPI(t *testing.T) []byte {
	t.Helper()
	raw, err := slashing.EncodeSlashPayload(slashing.SlashPayload{
		NodeID:          "rig-77",
		EvidenceKind:    slashing.EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte("opaque-evidence"),
		SlashAmountDust: 5 * 100_000_000,
		Memo:            "test",
	})
	if err != nil {
		t.Fatalf("EncodeSlashPayload: %v", err)
	}
	return raw
}

func encodeSlashReq(req SlashSubmitRequest) *bytes.Buffer {
	b, _ := json.Marshal(req)
	return bytes.NewBuffer(b)
}

func TestSlashSubmit_HappyPath(t *testing.T) {
	pool := &fakeSubmitter{}
	SetSlashMempool(pool)
	t.Cleanup(func() { SetSlashMempool(nil) })

	body := SlashSubmitRequest{
		ID:         "slash-1",
		Sender:     "watcher",
		Nonce:      0,
		Fee:        0.01,
		ContractID: slashing.ContractID,
		PayloadB64: base64.StdEncoding.EncodeToString(mustSlashPayloadAPI(t)),
	}
	h := &Handlers{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mining/slash", encodeSlashReq(body))
	rec := httptest.NewRecorder()
	h.SlashSubmitHandler(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if len(pool.added) != 1 {
		t.Fatalf("expected 1 tx admitted, got %d", len(pool.added))
	}
	got := pool.added[0]
	if got.ID != "slash-1" || got.Sender != "watcher" || got.ContractID != slashing.ContractID {
		t.Errorf("tx fields: %+v", got)
	}
	if !bytes.Equal(got.Payload, mustSlashPayloadAPI(t)) {
		t.Error("payload bytes did not round-trip exactly")
	}

	var resp SlashSubmitResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.TxID != "slash-1" || resp.Status != "accepted" {
		t.Errorf("response body: %+v", resp)
	}
}

func TestSlashSubmit_RejectsWrongMethod(t *testing.T) {
	SetSlashMempool(&fakeSubmitter{})
	t.Cleanup(func() { SetSlashMempool(nil) })
	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/slash", nil)
	rec := httptest.NewRecorder()
	h.SlashSubmitHandler(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", rec.Code)
	}
}

func TestSlashSubmit_NoMempool_Returns503(t *testing.T) {
	SetSlashMempool(nil)
	h := &Handlers{}
	body := SlashSubmitRequest{
		ID: "x", Sender: "a", ContractID: slashing.ContractID,
		PayloadB64: base64.StdEncoding.EncodeToString(mustSlashPayloadAPI(t)),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mining/slash", encodeSlashReq(body))
	rec := httptest.NewRecorder()
	h.SlashSubmitHandler(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
}

func TestSlashSubmit_BadContractID(t *testing.T) {
	SetSlashMempool(&fakeSubmitter{})
	t.Cleanup(func() { SetSlashMempool(nil) })
	h := &Handlers{}
	body := SlashSubmitRequest{
		ID: "x", Sender: "a", ContractID: "qsdm/enroll/v1",
		PayloadB64: base64.StdEncoding.EncodeToString(mustSlashPayloadAPI(t)),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mining/slash", encodeSlashReq(body))
	rec := httptest.NewRecorder()
	h.SlashSubmitHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestSlashSubmit_BadBase64(t *testing.T) {
	SetSlashMempool(&fakeSubmitter{})
	t.Cleanup(func() { SetSlashMempool(nil) })
	h := &Handlers{}
	body := SlashSubmitRequest{
		ID: "x", Sender: "a", ContractID: slashing.ContractID,
		PayloadB64: "not!!!base64!!!",
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mining/slash", encodeSlashReq(body))
	rec := httptest.NewRecorder()
	h.SlashSubmitHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestSlashSubmit_MalformedPayload_Returns400(t *testing.T) {
	SetSlashMempool(&fakeSubmitter{})
	t.Cleanup(func() { SetSlashMempool(nil) })
	h := &Handlers{}
	body := SlashSubmitRequest{
		ID: "x", Sender: "a", ContractID: slashing.ContractID, Fee: 0.001,
		PayloadB64: base64.StdEncoding.EncodeToString([]byte("{")),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mining/slash", encodeSlashReq(body))
	rec := httptest.NewRecorder()
	h.SlashSubmitHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestSlashSubmit_DuplicateMapsTo409(t *testing.T) {
	SetSlashMempool(&fakeSubmitter{err: mempool.ErrDuplicateTx})
	t.Cleanup(func() { SetSlashMempool(nil) })
	h := &Handlers{}
	body := SlashSubmitRequest{
		ID: "x", Sender: "a", ContractID: slashing.ContractID, Fee: 0.001,
		PayloadB64: base64.StdEncoding.EncodeToString(mustSlashPayloadAPI(t)),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mining/slash", encodeSlashReq(body))
	rec := httptest.NewRecorder()
	h.SlashSubmitHandler(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("status: got %d, want 409", rec.Code)
	}
}

func TestSlashSubmit_FullMapsTo503(t *testing.T) {
	SetSlashMempool(&fakeSubmitter{err: mempool.ErrMempoolFull})
	t.Cleanup(func() { SetSlashMempool(nil) })
	h := &Handlers{}
	body := SlashSubmitRequest{
		ID: "x", Sender: "a", ContractID: slashing.ContractID, Fee: 0.001,
		PayloadB64: base64.StdEncoding.EncodeToString(mustSlashPayloadAPI(t)),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mining/slash", encodeSlashReq(body))
	rec := httptest.NewRecorder()
	h.SlashSubmitHandler(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503", rec.Code)
	}
}

func TestSlashSubmit_RejectsMissingPayloadB64(t *testing.T) {
	SetSlashMempool(&fakeSubmitter{})
	t.Cleanup(func() { SetSlashMempool(nil) })
	h := &Handlers{}
	body := SlashSubmitRequest{
		ID: "x", Sender: "a", ContractID: slashing.ContractID,
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mining/slash", encodeSlashReq(body))
	rec := httptest.NewRecorder()
	h.SlashSubmitHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestSlashSubmit_RejectsMissingID(t *testing.T) {
	SetSlashMempool(&fakeSubmitter{})
	t.Cleanup(func() { SetSlashMempool(nil) })
	h := &Handlers{}
	body := SlashSubmitRequest{
		Sender: "a", ContractID: slashing.ContractID,
		PayloadB64: base64.StdEncoding.EncodeToString(mustSlashPayloadAPI(t)),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mining/slash", encodeSlashReq(body))
	rec := httptest.NewRecorder()
	h.SlashSubmitHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}
