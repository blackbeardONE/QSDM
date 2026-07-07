package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPTreasuryPayoutStatusAndPay(t *testing.T) {
	address := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "address": address, "role": "referral"})
	})
	mux.HandleFunc("/v1/balance", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("address") != address {
			t.Errorf("wrong balance address: %s", r.URL.Query().Get("address"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"address": address, "balance": 25.0})
	})
	mux.HandleFunc("/v1/pay", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("missing signer authorization")
		}
		var req TreasuryPayoutRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(TreasuryPayoutReceipt{
			TransactionID: "payout-transaction", Nonce: 1, Sender: address,
			Recipient: req.Recipient, Amount: req.Amount,
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	service, err := NewHTTPTreasuryPayoutService(server.URL, "secret", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Address != address || status.Balance != 25 || status.Role != "referral" {
		t.Fatalf("unexpected status: %+v", status)
	}
	receipt, err := service.Pay(t.Context(), TreasuryPayoutRequest{
		RequestID: "referral-1", Purpose: "referral", Recipient: address, Amount: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.TransactionID != "payout-transaction" || receipt.Amount != 5 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestHTTPTreasuryPayoutRejectsPlainRemoteHTTP(t *testing.T) {
	if _, err := NewHTTPTreasuryPayoutService("http://treasury.example", "secret", time.Second); err == nil {
		t.Fatal("expected non-loopback plain HTTP signer URL to be rejected")
	}
}

func TestHTTPTreasuryPayoutRejectsNonLoopbackAndAmbiguousURLs(t *testing.T) {
	tests := []string{
		"https://treasury.example",
		"http://localhost:8897",
		"http://127.0.0.1:8897/v1/pay",
		"http://user@127.0.0.1:8897",
		"http://127.0.0.1:8897?target=remote",
	}
	for _, rawURL := range tests {
		t.Run(rawURL, func(t *testing.T) {
			if _, err := NewHTTPTreasuryPayoutService(rawURL, "secret", time.Second); err == nil {
				t.Fatalf("expected treasury signer URL to be rejected: %s", rawURL)
			}
		})
	}
}

func TestHTTPTreasuryPayoutRefusesRedirects(t *testing.T) {
	destinationCalled := false
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		destinationCalled = true
	}))
	defer destination.Close()

	signer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer signer.Close()

	service, err := NewHTTPTreasuryPayoutService(signer.URL, "secret", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Status(t.Context()); err == nil {
		t.Fatal("expected redirect response to be rejected")
	}
	if destinationCalled {
		t.Fatal("treasury signer client followed a redirect")
	}
}
