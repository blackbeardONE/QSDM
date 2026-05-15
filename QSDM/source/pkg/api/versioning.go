package api

import (
	"net/http"
	"sync"
	"time"
)

// API versioning policy (MED-4).
//
// The QSDM HTTP API is mounted under a path-versioned prefix (currently
// /api/v1). Major version bumps create a new sibling prefix (/api/v2,
// etc.) and the old prefix stays live until its sunset date. Minor
// changes are strictly additive (new fields, new endpoints, looser
// validation) and never break a v1 client.
//
// Deprecation flow:
//
//  1. An endpoint or version is marked Deprecated with a sunset date.
//  2. DeprecationMiddleware tags every response from that endpoint with
//     a Deprecation header (true | <date>) and a Sunset header (RFC 8594
//     HTTP-date). Optional Link rel="successor-version" points clients at
//     the replacement.
//  3. The /api/v1/versions endpoint returns the full catalogue so a SDK
//     can render a "deprecated" badge in tooling without scraping
//     every endpoint response.
//  4. After the sunset date, the endpoint returns 410 Gone with a
//     JSON body pointing at the migration guide.
//
// Operators flip the deprecation flag at runtime via the registry below;
// no redeploy required. Tests mutate the registry directly.

// APIVersionStatus is the lifecycle stage of an API version.
type APIVersionStatus string

const (
	APIVersionActive     APIVersionStatus = "active"
	APIVersionDeprecated APIVersionStatus = "deprecated"
	APIVersionSunset     APIVersionStatus = "sunset" // returns 410 Gone
)

// APIVersion describes one major version of the API surface.
type APIVersion struct {
	Name              string           `json:"name"`               // e.g. "v1"
	Prefix            string           `json:"prefix"`             // e.g. "/api/v1"
	Status            APIVersionStatus `json:"status"`             // active | deprecated | sunset
	DeprecatedAt      time.Time        `json:"deprecated_at,omitempty"`
	SunsetAt          time.Time        `json:"sunset_at,omitempty"`
	SuccessorVersion  string           `json:"successor_version,omitempty"`
	MigrationGuideURL string           `json:"migration_guide_url,omitempty"`
}

// apiVersionRegistry is the process-wide catalogue. The mutex is held only
// during registry mutations and snapshots; the hot path (/versions
// handler, deprecation middleware) takes the read lock.
type apiVersionRegistry struct {
	mu       sync.RWMutex
	versions map[string]*APIVersion // keyed by Name ("v1", "v2", ...)
}

var versionRegistry = &apiVersionRegistry{
	versions: map[string]*APIVersion{
		// v1 is the only major version today. Bumping to v2 means
		// registering a fresh entry here AND marking this entry as
		// Deprecated with a Sunset date.
		"v1": {
			Name:   "v1",
			Prefix: "/api/v1",
			Status: APIVersionActive,
		},
	},
}

// RegisterAPIVersion installs or replaces a version descriptor. Safe for
// concurrent use; intended for unit tests and admin handlers.
func RegisterAPIVersion(v APIVersion) {
	if v.Name == "" {
		return
	}
	cp := v
	versionRegistry.mu.Lock()
	versionRegistry.versions[v.Name] = &cp
	versionRegistry.mu.Unlock()
}

// LookupAPIVersion returns a copy of the version descriptor, or nil if
// the name is not registered.
func LookupAPIVersion(name string) *APIVersion {
	versionRegistry.mu.RLock()
	defer versionRegistry.mu.RUnlock()
	v, ok := versionRegistry.versions[name]
	if !ok {
		return nil
	}
	cp := *v
	return &cp
}

// ListAPIVersions returns a sorted snapshot of every registered version.
func ListAPIVersions() []APIVersion {
	versionRegistry.mu.RLock()
	defer versionRegistry.mu.RUnlock()
	out := make([]APIVersion, 0, len(versionRegistry.versions))
	for _, v := range versionRegistry.versions {
		out = append(out, *v)
	}
	return out
}

// Versions is the /api/v1/versions handler. Public read; returns the
// catalogue as JSON so SDKs can render deprecation banners.
func (h *Handlers) Versions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"versions": ListAPIVersions(),
		"current":  "v1",
	})
}

// DeprecationMiddleware adds Deprecation / Sunset / Link headers to every
// response from a deprecated version, and short-circuits with HTTP 410
// Gone after the sunset date.
//
// Wire AFTER routing (so the path prefix is fixed) and BEFORE the
// handler chain. The middleware reads the registry on every request,
// which lets an operator flip the flag without restarting the process.
func DeprecationMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Resolve which version this request hit. Today every route
			// is under /api/v1; future v2 routes follow the same convention.
			versionName := pathVersion(r.URL.Path)
			if versionName == "" {
				next.ServeHTTP(w, r)
				return
			}
			v := LookupAPIVersion(versionName)
			if v == nil || v.Status == APIVersionActive {
				next.ServeHTTP(w, r)
				return
			}

			// Deprecation header (RFC 8594-style boolean form).
			if !v.DeprecatedAt.IsZero() {
				w.Header().Set("Deprecation", v.DeprecatedAt.UTC().Format(time.RFC1123))
			} else {
				w.Header().Set("Deprecation", "true")
			}
			if !v.SunsetAt.IsZero() {
				w.Header().Set("Sunset", v.SunsetAt.UTC().Format(time.RFC1123))
			}
			if v.SuccessorVersion != "" {
				w.Header().Add("Link", `</api/`+v.SuccessorVersion+`/>; rel="successor-version"`)
			}
			if v.MigrationGuideURL != "" {
				w.Header().Add("Link", `<`+v.MigrationGuideURL+`>; rel="deprecation"`)
			}

			if v.Status == APIVersionSunset {
				writeErrorResponse(w, http.StatusGone, "API "+v.Name+" is sunset; see Link header for migration guide")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// pathVersion extracts the major-version segment from /api/v1/... style
// paths. Returns "" if the path does not look like an API URL — those
// requests bypass deprecation handling entirely.
func pathVersion(path string) string {
	const prefix = "/api/"
	if len(path) < len(prefix)+1 || path[:len(prefix)] != prefix {
		return ""
	}
	rest := path[len(prefix):]
	// Take everything up to the next slash.
	for i := 0; i < len(rest); i++ {
		if rest[i] == '/' {
			return rest[:i]
		}
	}
	return rest
}
