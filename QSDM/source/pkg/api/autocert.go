package api

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"golang.org/x/crypto/acme/autocert"
)

// ACMEConfig holds configuration for automatic TLS certificate provisioning.
type ACMEConfig struct {
	Domains  []string // e.g. ["api.qsdmplus.io"]
	Email    string   // contact email for Let's Encrypt
	CacheDir string   // directory to cache certificates (default: "certs")
}

// ConfigureACME sets up an autocert.Manager and returns a TLS config + HTTP handler
// for the ACME HTTP-01 challenge. The caller should:
//  1. Use the returned *tls.Config on the HTTPS server.
//  2. Run the returned http.Handler on :80 to respond to ACME challenges and redirect HTTP→HTTPS.
func ConfigureACME(cfg ACMEConfig) (*tls.Config, http.Handler, error) {
	if len(cfg.Domains) == 0 {
		return nil, nil, fmt.Errorf("at least one domain is required for ACME")
	}

	cacheDir := cfg.CacheDir
	if cacheDir == "" {
		cacheDir = "certs"
	}
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return nil, nil, fmt.Errorf("create cert cache dir %s: %w", cacheDir, err)
	}

	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(cfg.Domains...),
		Cache:      autocert.DirCache(filepath.Clean(cacheDir)),
		Email:      cfg.Email,
	}

	tlsConfig := m.TLSConfig()
	tlsConfig.MinVersion = tls.VersionTLS12

	challengeHandler := m.HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := "https://" + r.Host + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	}))

	return tlsConfig, challengeHandler, nil
}
