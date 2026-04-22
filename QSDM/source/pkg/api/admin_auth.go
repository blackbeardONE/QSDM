package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/config"
)

type adminActorCtxKey struct{}

// AdminActorContextKey is the request context key for the authenticated admin principal.
var AdminActorContextKey = adminActorCtxKey{}

// AdminActorFromRequest returns the admin actor id (UserID or Address) when set by AdminAccessMiddleware.
func AdminActorFromRequest(r *http.Request) string {
	v, _ := r.Context().Value(AdminActorContextKey).(string)
	return v
}

// AdminAccessMiddleware enforces optional stricter access for /api/admin/* after AuthMiddleware
// has attached JWT claims. When AdminAPIRequireRole is true, role must be "admin". When
// AdminAPIRequireMTLS is true, the request must present a verified TLS client certificate.
func AdminAccessMiddleware(cfg *config.Config, logger *logging.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if cfg == nil || !strings.HasPrefix(r.URL.Path, "/api/admin") {
				next.ServeHTTP(w, r)
				return
			}
			if cfg.AdminAPIRequireMTLS {
				if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
					if logger != nil {
						logger.Warn("admin API blocked: mTLS required", "path", r.URL.Path)
					}
					writeErrorResponse(w, http.StatusForbidden, "admin API requires verified mTLS client certificate")
					return
				}
			}
			if cfg.AdminAPIRequireRole {
				claims, ok := r.Context().Value("claims").(*Claims)
				if !ok || claims == nil {
					writeErrorResponse(w, http.StatusUnauthorized, "admin API requires authentication")
					return
				}
				if claims.Role != "admin" {
					writeErrorResponse(w, http.StatusForbidden, "admin API requires role=admin")
					return
				}
				id := claims.UserID
				if id == "" {
					id = claims.Address
				}
				if id != "" {
					r = r.WithContext(context.WithValue(r.Context(), AdminActorContextKey, id))
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
