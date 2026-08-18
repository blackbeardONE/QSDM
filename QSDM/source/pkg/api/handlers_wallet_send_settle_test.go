package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/storage"
	"github.com/blackbeardONE/QSDM/pkg/submesh"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

// /wallet/send must debit the FEE as well as the amount.
//
// It did not. The endpoint settled through storage.StoreTransaction, which
// parses `amount` out of the transaction JSON (sqlite.go:125) and debits only
// that (sqlite.go:213). The fee was charged to nobody: the sender kept it and
// no account was credited with it.
//
// The sibling endpoint /wallet/submit-signed has settled through
// ApplyTransferAtomic since v0.4.1, which enforces balance >= amount + fee in
// one ACID step. sqlite_v041.go's header states that primitive was built to
// replace exactly the sequence /wallet/send was still running, and the comment
// at the submit-signed call site spells that sequence out. The integration
// landed on one endpoint and stopped.
//
// This pins the debit arithmetic so the endpoint cannot quietly regress to a
// primitive that under-charges.
func TestWalletSend_debitsAmountPlusFee(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Fatalf("wallet service: %v", err)
	}
	dm := submesh.NewDynamicSubmeshManager()
	dm.AddOrUpdateSubmesh(&submesh.DynamicSubmesh{
		Name: "us", FeeThreshold: 0, PriorityLevel: 1, GeoTags: []string{"US"}, MaxPayloadBytes: 1_000_000,
	})
	h := setupTestHandlersWithSubmesh(dm, ws)
	ms := h.storage.(*mockStorage)

	const start, amount, fee = 1000.0, 1.0, 0.25
	sender := ws.GetAddress()
	recipient := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	ms.SetBalance(sender, start)

	body, _ := json.Marshal(map[string]interface{}{
		"recipient": recipient, "amount": amount, "fee": fee, "geotag": "US",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/send", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ContextWithClaims(req.Context(), &Claims{Address: sender, Role: "user"}))
	w := httptest.NewRecorder()
	h.SendTransaction(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("send failed: %d %s", w.Code, w.Body.String())
	}
	gotSender, _ := ms.GetBalance(sender)
	if wantSender := start - amount - fee; gotSender != wantSender {
		t.Errorf("sender balance = %v, want %v (start %v - amount %v - fee %v). If this reads "+
			"%v the fee is not being debited, which means the endpoint is settling through a "+
			"primitive that charges only `amount` -- the defect ApplyTransferAtomic exists to "+
			"prevent.", gotSender, wantSender, start, amount, fee, start-amount)
	}
	gotRecipient, _ := ms.GetBalance(recipient)
	if gotRecipient != amount {
		t.Errorf("recipient balance = %v, want %v: the recipient receives the amount, never the fee",
			gotRecipient, amount)
	}
}

// A send the sender cannot afford once the fee is counted must be refused.
// Under the old primitive `amount` alone was compared against a
// CHECK(balance >= 0), so a balance covering the amount but not the fee settled.
func TestWalletSend_refusesWhenFeePushesOverBalance(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Fatalf("wallet service: %v", err)
	}
	dm := submesh.NewDynamicSubmeshManager()
	dm.AddOrUpdateSubmesh(&submesh.DynamicSubmesh{
		Name: "us", FeeThreshold: 0, PriorityLevel: 1, GeoTags: []string{"US"}, MaxPayloadBytes: 1_000_000,
	})
	h := setupTestHandlersWithSubmesh(dm, ws)
	ms := h.storage.(*mockStorage)

	sender := ws.GetAddress()
	ms.SetBalance(sender, 1.0) // exactly the amount, nothing for the fee

	body, _ := json.Marshal(map[string]interface{}{
		"recipient": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"amount":    1.0, "fee": 0.5, "geotag": "US",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/send", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ContextWithClaims(req.Context(), &Claims{Address: sender, Role: "user"}))
	w := httptest.NewRecorder()
	h.SendTransaction(w, req)

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("expected 402 for a balance that covers amount but not amount+fee, got %d %s",
			w.Code, w.Body.String())
	}
	if got, _ := ms.GetBalance(sender); got != 1.0 {
		t.Errorf("a refused send must not move the balance, got %v want 1", got)
	}
}

// A backend that cannot settle transfers must say so, not return a bare 500.
//
// Moving /wallet/send onto ApplyTransferAtomic newly broke it on FileStorage and
// ScyllaStorage: both implement StoreTransaction (which is why the endpoint used
// to work there, while under-charging the fee) and neither implements
// ApplyTransferAtomic. Failing closed is correct on a money path. Failing closed
// with an opaque 500 is not -- the operator cannot tell a transient error from
// an endpoint that can never work on their deployment.
//
// A reviewer caught that I shipped that regression without disclosing or testing
// it. This pins the disclosure.
func TestWalletSend_backendCannotSettle_reports501NotOpaque500(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Fatalf("wallet service: %v", err)
	}
	dm := submesh.NewDynamicSubmeshManager()
	dm.AddOrUpdateSubmesh(&submesh.DynamicSubmesh{
		Name: "us", FeeThreshold: 0, PriorityLevel: 1, GeoTags: []string{"US"}, MaxPayloadBytes: 1_000_000,
	})
	h := setupTestHandlersWithSubmesh(dm, ws)
	ms := h.storage.(*mockStorage)
	ms.SetBalance(ws.GetAddress(), 1000)
	// Exactly what FileStorage and ScyllaStorage return.
	ms.applyTransferErr = fmt.Errorf("backend stub: %w", storage.ErrAtomicTransferUnsupported)

	body, _ := json.Marshal(map[string]interface{}{
		"recipient": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"amount":    1.0, "fee": 0.25, "geotag": "US",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/send", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ContextWithClaims(req.Context(), &Claims{Address: ws.GetAddress(), Role: "user"}))
	w := httptest.NewRecorder()
	h.SendTransaction(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 when the backend cannot settle, got %d %s -- a 500 here is the "+
			"opaque failure this test exists to prevent", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "cannot settle transfers") {
		t.Errorf("the response must name the cause so an operator can act on it, got %s", w.Body.String())
	}
	if got, _ := ms.GetBalance(ws.GetAddress()); got != 1000 {
		t.Errorf("a refused send must not move the balance, got %v want 1000", got)
	}
}
