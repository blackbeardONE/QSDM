package api

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

func testStreamEnvelope(t *testing.T) QSDMStreamActionEnvelope {
	t.Helper()
	payerPublic, payerPrivate, err := mldsa87.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	providerPublic, _, err := mldsa87.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payerBytes, err := payerPublic.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	providerBytes, err := providerPublic.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	payerHash := sha256.Sum256(payerBytes)
	providerHash := sha256.Sum256(providerBytes)
	sessionPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	deviceHash := sha256.Sum256([]byte("api-test-device"))
	action := chain.StreamAction{
		ID:                 "api-stream-open",
		Sender:             hex.EncodeToString(payerHash[:]),
		StreamID:           "api-stream-1",
		Action:             chain.StreamActionOpen,
		Provider:           hex.EncodeToString(providerHash[:]),
		ServiceID:          "qsdm-vpn",
		DeviceIDHash:       hex.EncodeToString(deviceHash[:]),
		SessionPublicKey:   hex.EncodeToString(sessionPublic),
		PriceDust:          2 * chain.DustPerCell,
		PricePeriodSeconds: 2_592_000,
		BudgetDust:         2 * chain.DustPerCell,
		MaxActiveSeconds:   2_592_000,
		ExpiresAt:          "2026-09-01T00:00:00Z",
		Timestamp:          "2026-08-01T00:00:00Z",
	}
	message, err := chain.StreamActionSigningBytes(action)
	if err != nil {
		t.Fatal(err)
	}
	signature := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(payerPrivate, message, nil, true, signature); err != nil {
		t.Fatal(err)
	}
	return QSDMStreamActionEnvelope{
		Action:    action,
		Signature: hex.EncodeToString(signature),
		PublicKey: hex.EncodeToString(payerBytes),
	}
}

func postStreamEnvelope(t *testing.T, h *Handlers, env QSDMStreamActionEnvelope) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/streams/actions/submit-signed", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.QSDMStreamActionSubmitSignedHandler(rec, req)
	return rec
}

func TestQSDMStreamActionSubmitAndRead(t *testing.T) {
	pool := mempool.New(mempool.DefaultConfig())
	SetStreamActionMempool(pool)
	t.Cleanup(func() { SetStreamActionMempool(nil) })
	h := &Handlers{}
	env := testStreamEnvelope(t)

	rec := postStreamEnvelope(t, h, env)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit status = %d, body=%s", rec.Code, rec.Body.String())
	}
	tx, ok := pool.Get(env.Action.ID)
	if !ok {
		t.Fatal("accepted stream transaction was not added to mempool")
	}
	store := chain.NewStreamStateStore()
	if err := store.ApplyHistoricalTx(tx, 1); err != nil {
		t.Fatalf("project accepted stream: %v", err)
	}
	SetStreamStateProvider(store)
	t.Cleanup(func() { SetStreamStateProvider(nil) })

	listRec := httptest.NewRecorder()
	h.QSDMStreamsListHandler(listRec, httptest.NewRequest(http.MethodGet, "/api/v1/streams?payer="+env.Action.Sender, nil))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", listRec.Code, listRec.Body.String())
	}
	var list QSDMStreamsResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Streams) != 1 || list.Streams[0].StreamID != env.Action.StreamID {
		t.Fatalf("unexpected stream list: %+v", list)
	}
	if list.Streams[0].RemainingBudgetDust != env.Action.BudgetDust {
		t.Fatalf("remaining budget = %d, want %d", list.Streams[0].RemainingBudgetDust, env.Action.BudgetDust)
	}

	getRec := httptest.NewRecorder()
	h.QSDMStreamRouteHandler(getRec, httptest.NewRequest(http.MethodGet, "/api/v1/streams/"+env.Action.StreamID, nil))
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body=%s", getRec.Code, getRec.Body.String())
	}
}

func TestQSDMStreamActionRejectsTamperedSignedAction(t *testing.T) {
	pool := mempool.New(mempool.DefaultConfig())
	SetStreamActionMempool(pool)
	t.Cleanup(func() { SetStreamActionMempool(nil) })
	env := testStreamEnvelope(t)
	env.Action.BudgetDust++

	rec := postStreamEnvelope(t, &Handlers{}, env)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("tampered status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if pool.Size() != 0 {
		t.Fatalf("tampered action reached mempool: size=%d", pool.Size())
	}
}

func TestQSDMStreamEndpointsArePublic(t *testing.T) {
	for _, path := range []string{
		"/api/v1/streams",
		"/api/v1/streams/stream-1",
		"/api/v1/streams/actions/submit-signed",
	} {
		if !isPublicEndpoint(path) {
			t.Fatalf("%s should be public", path)
		}
	}
}
