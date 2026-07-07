package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConfigureACMERedirectUsesCanonicalDomain(t *testing.T) {
	_, handler, err := ConfigureACME(ACMEConfig{
		Domains:  []string{"api.qsdm.tech"},
		CacheDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://attacker.example/status?view=full", nil)
	req.Host = "attacker.example"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301 redirect, got %d", recorder.Code)
	}
	if location := recorder.Header().Get("Location"); location != "https://api.qsdm.tech/status?view=full" {
		t.Fatalf("redirect used untrusted request host: %q", location)
	}
}

func TestConfigureACMERejectsAmbiguousDomains(t *testing.T) {
	for _, domain := range []string{
		"api.qsdm.tech@attacker.example",
		"api.qsdm.tech/path",
		"api.qsdm.tech:8443",
	} {
		t.Run(domain, func(t *testing.T) {
			if _, _, err := ConfigureACME(ACMEConfig{Domains: []string{domain}, CacheDir: t.TempDir()}); err == nil {
				t.Fatalf("expected invalid ACME domain to be rejected: %s", domain)
			}
		})
	}
}
