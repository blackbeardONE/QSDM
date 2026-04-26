package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeReceiptStore implements SlashReceiptStore for the
// handler tests. Decoupled from chain.SlashReceiptStore so we
// can express edge cases (e.g. evicted-then-readded) without
// reproducing the FIFO bookkeeping in every test.
type fakeReceiptStore struct {
	receipts map[string]SlashReceiptView
}

func (f *fakeReceiptStore) Lookup(txID string) (SlashReceiptView, bool) {
	v, ok := f.receipts[txID]
	return v, ok
}

func newFakeReceiptStoreApplied(t *testing.T) *fakeReceiptStore {
	t.Helper()
	return &fakeReceiptStore{receipts: map[string]SlashReceiptView{
		"tx-applied": {
			TxID:                    "tx-applied",
			Outcome:                 "applied",
			RecordedAt:              time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
			Height:                  42,
			Slasher:                 "alice",
			NodeID:                  "rig-77",
			EvidenceKind:            "forged-attestation",
			SlashedDust:             500_000_000,
			RewardedDust:            10_000_000,
			BurnedDust:              490_000_000,
			AutoRevoked:             true,
			AutoRevokeRemainingDust: 100_000_000,
		},
	}}
}

func newFakeReceiptStoreRejected(t *testing.T) *fakeReceiptStore {
	t.Helper()
	return &fakeReceiptStore{receipts: map[string]SlashReceiptView{
		"tx-rejected": {
			TxID:         "tx-rejected",
			Outcome:      "rejected",
			RecordedAt:   time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
			Height:       7,
			Slasher:      "bob",
			NodeID:       "rig-99",
			EvidenceKind: "double-mining",
			RejectReason: "verifier_failed",
			Err:          "verifier said no",
		},
	}}
}

func TestSlashReceipt_HappyPath_Applied(t *testing.T) {
	SetSlashReceiptStore(newFakeReceiptStoreApplied(t))
	t.Cleanup(func() { SetSlashReceiptStore(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/slash/tx-applied", nil)
	rec := httptest.NewRecorder()
	h.SlashReceiptHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var view SlashReceiptView
	if err := json.NewDecoder(rec.Body).Decode(&view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.TxID != "tx-applied" || view.Outcome != "applied" ||
		view.Height != 42 || view.Slasher != "alice" ||
		view.NodeID != "rig-77" ||
		view.EvidenceKind != "forged-attestation" ||
		view.SlashedDust != 500_000_000 ||
		view.RewardedDust != 10_000_000 ||
		view.BurnedDust != 490_000_000 ||
		!view.AutoRevoked ||
		view.AutoRevokeRemainingDust != 100_000_000 {
		t.Errorf("view fields: %+v", view)
	}
}

func TestSlashReceipt_HappyPath_Rejected(t *testing.T) {
	SetSlashReceiptStore(newFakeReceiptStoreRejected(t))
	t.Cleanup(func() { SetSlashReceiptStore(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/slash/tx-rejected", nil)
	rec := httptest.NewRecorder()
	h.SlashReceiptHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	var view SlashReceiptView
	if err := json.NewDecoder(rec.Body).Decode(&view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.Outcome != "rejected" {
		t.Errorf("outcome: got %q, want rejected", view.Outcome)
	}
	if view.RejectReason != "verifier_failed" {
		t.Errorf("reject_reason: %q", view.RejectReason)
	}
	if view.Err != "verifier said no" {
		t.Errorf("err: %q", view.Err)
	}
}

func TestSlashReceipt_NotFound(t *testing.T) {
	SetSlashReceiptStore(&fakeReceiptStore{receipts: nil})
	t.Cleanup(func() { SetSlashReceiptStore(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/slash/missing-tx", nil)
	rec := httptest.NewRecorder()
	h.SlashReceiptHandler(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
}

func TestSlashReceipt_NoStoreReturns503(t *testing.T) {
	SetSlashReceiptStore(nil)
	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/slash/whatever", nil)
	rec := httptest.NewRecorder()
	h.SlashReceiptHandler(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503", rec.Code)
	}
}

func TestSlashReceipt_RejectsWrongMethod(t *testing.T) {
	SetSlashReceiptStore(newFakeReceiptStoreApplied(t))
	t.Cleanup(func() { SetSlashReceiptStore(nil) })

	h := &Handlers{}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/api/v1/mining/slash/tx-applied", nil)
		rec := httptest.NewRecorder()
		h.SlashReceiptHandler(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status got %d, want 405", method, rec.Code)
		}
	}
}

func TestSlashReceipt_RejectsEmptyTxID(t *testing.T) {
	SetSlashReceiptStore(newFakeReceiptStoreApplied(t))
	t.Cleanup(func() { SetSlashReceiptStore(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/slash/", nil)
	rec := httptest.NewRecorder()
	h.SlashReceiptHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestSlashReceipt_TolerantToTrailingSlash(t *testing.T) {
	SetSlashReceiptStore(newFakeReceiptStoreApplied(t))
	t.Cleanup(func() { SetSlashReceiptStore(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/slash/tx-applied/", nil)
	rec := httptest.NewRecorder()
	h.SlashReceiptHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("trailing-slash status: got %d, want 200", rec.Code)
	}
}

func TestSlashReceipt_RejectsTooLongTxID(t *testing.T) {
	SetSlashReceiptStore(newFakeReceiptStoreApplied(t))
	t.Cleanup(func() { SetSlashReceiptStore(nil) })

	h := &Handlers{}
	huge := strings.Repeat("a", 257)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/slash/"+huge, nil)
	rec := httptest.NewRecorder()
	h.SlashReceiptHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestSlashReceipt_ContentTypeJSON(t *testing.T) {
	SetSlashReceiptStore(newFakeReceiptStoreApplied(t))
	t.Cleanup(func() { SetSlashReceiptStore(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/slash/tx-applied", nil)
	rec := httptest.NewRecorder()
	h.SlashReceiptHandler(rec, req)
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type: got %q, want application/json", ct)
	}
}

func TestSlashReceipt_OmitemptyZerosOnRejected(t *testing.T) {
	// A rejected receipt with zero slashed/rewarded/burned dust
	// should NOT spit those zero fields into the JSON. This
	// keeps the wire surface tight for the common case where
	// dust amounts are meaningless on a rejection.
	SetSlashReceiptStore(newFakeReceiptStoreRejected(t))
	t.Cleanup(func() { SetSlashReceiptStore(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/slash/tx-rejected", nil)
	rec := httptest.NewRecorder()
	h.SlashReceiptHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, zeroField := range []string{
		`"slashed_dust"`,
		`"rewarded_dust"`,
		`"burned_dust"`,
		`"auto_revoked"`,
		`"auto_revoke_remaining_dust"`,
	} {
		if strings.Contains(body, zeroField) {
			t.Errorf("rejected receipt should omit %s on omitempty: body=%s", zeroField, body)
		}
	}
}
