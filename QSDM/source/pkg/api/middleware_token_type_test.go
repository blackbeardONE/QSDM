package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
)

// Login issues a 15-minute access token and a 7-day refresh token. Only the
// access token may authenticate a request; the refresh token used to pass
// AuthMiddleware too, which made it a 7-day bearer token.
func TestAuthMiddlewareAcceptsOnlyAccessTokens(t *testing.T) {
	am, err := NewAuthManager()
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	handler := AuthMiddleware(am, logging.NewLogger("", false))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := ClaimsFromContext(r.Context()); !ok {
			t.Error("handler ran without claims in the context")
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, tc := range []struct {
		name      string
		tokenType TokenType
		want      int
	}{
		{name: "access token", tokenType: TokenTypeAccess, want: http.StatusNoContent},
		{name: "refresh token", tokenType: TokenTypeRefresh, want: http.StatusUnauthorized},
		{name: "token without a type", tokenType: "", want: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token, err := am.CreateToken("alice", "alice", "user", tc.tokenType, time.Hour)
			if err != nil {
				t.Fatalf("CreateToken: %v", err)
			}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/config", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("%s: status %d, want %d (%s)", tc.name, rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
