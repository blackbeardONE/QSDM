package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRevocationStore_RoundTrip(t *testing.T) {
	s := NewTokenRevocationStore()
	t.Cleanup(s.Stop)

	if s.IsRevoked("never-seen") {
		t.Fatal("unknown nonce must not be marked revoked")
	}

	s.Revoke("nonce-1", time.Now().Add(1*time.Minute))
	if !s.IsRevoked("nonce-1") {
		t.Fatal("freshly revoked nonce must be IsRevoked=true")
	}

	if s.Size() != 1 {
		t.Fatalf("expected size 1, got %d", s.Size())
	}
}

func TestRevocationStore_ExpiredEntryTreatedAsNotRevoked(t *testing.T) {
	s := NewTokenRevocationStore()
	t.Cleanup(s.Stop)

	// Revoke with a deadline already in the past.
	s.Revoke("nonce-old", time.Now().Add(-1*time.Hour))
	if s.IsRevoked("nonce-old") {
		t.Fatal("entry past its natural expiry must not block")
	}
}

func TestAuthManager_ValidateTokenRejectsRevoked(t *testing.T) {
	am, err := NewAuthManager()
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	am.SetJWTHMACFallbackSecret("test-secret")
	store := NewTokenRevocationStore()
	t.Cleanup(store.Stop)
	am.SetRevocationStore(store)

	tok, err := am.CreateToken("alice", "alice", "user", TokenTypeAccess, 5*time.Minute)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	claims, err := am.ValidateToken(tok)
	if err != nil {
		t.Fatalf("validate fresh token: %v", err)
	}

	// Revoke and validate again — must now fail.
	am.RevokeToken(claims)
	if _, err := am.ValidateToken(tok); err == nil {
		t.Fatal("expected revoked token to fail validation")
	}
}

func TestLogoutHandler_RevokesToken(t *testing.T) {
	am, err := NewAuthManager()
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	am.SetJWTHMACFallbackSecret("test-secret")
	store := NewTokenRevocationStore()
	t.Cleanup(store.Stop)
	am.SetRevocationStore(store)

	tok, err := am.CreateToken("alice", "alice", "user", TokenTypeAccess, 5*time.Minute)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	claims, err := am.ValidateToken(tok)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	h := &Handlers{authManager: am}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req = req.WithContext(context.WithValue(req.Context(), "claims", claims))
	w := httptest.NewRecorder()
	h.Logout(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("logout expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	if _, err := am.ValidateToken(tok); err == nil {
		t.Fatal("token must be revoked after logout")
	}
}

func TestLogoutHandler_RejectsUnauthenticated(t *testing.T) {
	h := &Handlers{authManager: nil}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	w := httptest.NewRecorder()
	h.Logout(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestLogoutHandler_MethodNotAllowed(t *testing.T) {
	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/logout", nil)
	w := httptest.NewRecorder()
	h.Logout(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}
