package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPathVersion(t *testing.T) {
	cases := map[string]string{
		"/api/v1/status":         "v1",
		"/api/v2/foo/bar":        "v2",
		"/api/v1":                "v1",
		"/api/":                  "",
		"/healthz":               "",
		"":                       "",
	}
	for in, want := range cases {
		if got := pathVersion(in); got != want {
			t.Errorf("pathVersion(%q)=%q want %q", in, got, want)
		}
	}
}

func TestDeprecationMiddleware_ActiveVersionPassThrough(t *testing.T) {
	h := DeprecationMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("active version expected 200, got %d", w.Code)
	}
	if got := w.Header().Get("Deprecation"); got != "" {
		t.Errorf("active version must not set Deprecation, got %q", got)
	}
}

func TestDeprecationMiddleware_DeprecatedAddsHeaders(t *testing.T) {
	// Snapshot and restore the v1 entry so this test is hermetic.
	orig := LookupAPIVersion("v1")
	t.Cleanup(func() { RegisterAPIVersion(*orig) })

	RegisterAPIVersion(APIVersion{
		Name:              "v1",
		Prefix:            "/api/v1",
		Status:            APIVersionDeprecated,
		DeprecatedAt:      time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		SunsetAt:          time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
		SuccessorVersion:  "v2",
		MigrationGuideURL: "https://docs.example/v2-migration",
	})

	h := DeprecationMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("deprecated (not sunset) must still serve, got %d", w.Code)
	}
	if got := w.Header().Get("Deprecation"); got == "" {
		t.Error("expected Deprecation header")
	}
	if got := w.Header().Get("Sunset"); got == "" {
		t.Error("expected Sunset header")
	}
	links := w.Header().Values("Link")
	if len(links) < 2 {
		t.Errorf("expected at least 2 Link headers (successor + deprecation guide), got %v", links)
	}
}

func TestDeprecationMiddleware_SunsetReturns410(t *testing.T) {
	orig := LookupAPIVersion("v1")
	t.Cleanup(func() { RegisterAPIVersion(*orig) })

	RegisterAPIVersion(APIVersion{
		Name:   "v1",
		Prefix: "/api/v1",
		Status: APIVersionSunset,
	})

	called := false
	h := DeprecationMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/wallet/balance", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if called {
		t.Fatal("sunset version must short-circuit the chain")
	}
	if w.Code != http.StatusGone {
		t.Fatalf("expected 410, got %d", w.Code)
	}
}

func TestVersionsHandler_ListsCatalog(t *testing.T) {
	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/versions", nil)
	w := httptest.NewRecorder()
	h.Versions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body struct {
		Versions []APIVersion `json:"versions"`
		Current  string       `json:"current"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if body.Current != "v1" {
		t.Errorf("expected current=v1, got %q", body.Current)
	}
	if len(body.Versions) == 0 {
		t.Error("expected at least one version in catalogue")
	}
}
