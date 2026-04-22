package qsdm_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	qsdmplus "github.com/blackbeardONE/QSDM/sdk/go"
	"github.com/blackbeardONE/QSDM/sdk/go/qsdm"
)

// Smoke-test confirming the qsdm re-export package talks to the same wire
// surface as the legacy qsdmplus package and shares its types.
func TestQSDM_GetBalance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("address"); got != "addr-1" {
			t.Errorf("unexpected address query: %s", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]float64{"balance": 7.5})
	}))
	defer srv.Close()

	c := qsdm.NewClient(srv.URL)
	v, err := c.GetBalance("addr-1")
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if v != 7.5 {
		t.Fatalf("unexpected balance: %v", v)
	}

	// The preferred package's Client type must be identical to the legacy one.
	var _ *qsdmplus.Client = c
	var _ *qsdm.Client = c
}

func TestQSDM_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing", http.StatusNotFound)
	}))
	defer srv.Close()

	c := qsdm.NewClient(srv.URL)
	_, err := c.GetTransactionContext(context.Background(), "does-not-exist")
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
	if !qsdm.IsNotFound(err) {
		t.Fatalf("expected IsNotFound to match, got %v", err)
	}
	var apiErr *qsdm.ErrAPI
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *qsdm.ErrAPI, got %T", err)
	}
}
