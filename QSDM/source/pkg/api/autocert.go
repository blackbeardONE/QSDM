package api

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/acme/autocert"
)

// ACMEConfig holds configuration for automatic TLS certificate provisioning.
type ACMEConfig struct {
	Domains  []string // e.g. ["api.qsdm.io"]
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
	domains := make([]string, 0, len(cfg.Domains))
	for _, rawDomain := range cfg.Domains {
		domain := strings.TrimSpace(rawDomain)
		parsed, err := url.Parse("https://" + domain)
		if err != nil || domain == "" || parsed.Host != domain || parsed.Hostname() == "" ||
			parsed.User != nil || parsed.Port() != "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, nil, fmt.Errorf("invalid ACME domain %q", rawDomain)
		}
		domains = append(domains, domain)
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
		HostPolicy: autocert.HostWhitelist(domains...),
		Cache:      autocert.DirCache(filepath.Clean(cacheDir)),
		Email:      cfg.Email,
	}

	tlsConfig := m.TLSConfig()
	tlsConfig.MinVersion = tls.VersionTLS12

	canonicalHost := domains[0]
	challengeHandler := m.HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := url.URL{
			Scheme:   "https",
			Host:     canonicalHost,
			Path:     r.URL.Path,
			RawPath:  r.URL.RawPath,
			RawQuery: r.URL.RawQuery,
		}
		http.Redirect(w, r, target.String(), http.StatusMovedPermanently)
	}))

	return tlsConfig, challengeHandler, nil
}
