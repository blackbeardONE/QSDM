package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/config"
)

func TestAdminAccessMiddleware_MTLSRequired(t *testing.T) {
	cfg := &config.Config{AdminAPIRequireMTLS: true}
	mw := AdminAccessMiddleware(cfg, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/admin/x", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := mw(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/x", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without mTLS, got %d", w.Code)
	}
}

func TestAdminAccessMiddleware_AdminRole(t *testing.T) {
	cfg := &config.Config{AdminAPIRequireRole: true}
	mw := AdminAccessMiddleware(cfg, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/admin/x", func(w http.ResponseWriter, r *http.Request) {
		if AdminActorFromRequest(r) == "" {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	h := mw(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/x", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without claims, got %d", w.Code)
	}

	ctx := context.WithValue(req.Context(), "claims", &Claims{Role: "user", UserID: "u1"})
	req2 := httptest.NewRequest(http.MethodGet, "/api/admin/x", nil).WithContext(ctx)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin role, got %d", w2.Code)
	}

	ctx3 := context.WithValue(req.Context(), "claims", &Claims{Role: "admin", UserID: "alice"})
	req3 := httptest.NewRequest(http.MethodGet, "/api/admin/x", nil).WithContext(ctx3)
	w3 := httptest.NewRecorder()
	h.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Fatalf("expected 200 for admin, got %d", w3.Code)
	}
}
