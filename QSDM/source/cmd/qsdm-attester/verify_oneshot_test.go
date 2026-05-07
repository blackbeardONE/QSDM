package main

// One-shot end-to-end signature check: pulls the live
// telemetry profile from the local attester (or the
// public tunnel via env var QSDM_TELEMETRY_URL), reads the
// signer key from ~/.qsdm/attester.key, and validates the
// signature. Skipped by default (run with
// `go test -run TestLive_VerifyTelemetrySignature -tags
// liveverify`) so CI doesn't depend on a live attester.
//
// Lives in the test files because it doubles as
// documentation for "how do I verify a profile in 30
// lines of Go" — exactly the snippet the
// TELEMETRY_ORACLE.md doc points at.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/telemetry"
)

func TestLive_VerifyTelemetrySignature(t *testing.T) {
	url := os.Getenv("QSDM_TELEMETRY_URL")
	if url == "" {
		t.Skip("QSDM_TELEMETRY_URL not set; skipping live verification")
	}
	keyPath := os.Getenv("QSDM_ATTESTER_KEY")
	if keyPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("home dir: %v", err)
		}
		keyPath = filepath.Join(home, ".qsdm", "attester.key")
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key %s: %v", keyPath, err)
	}
	if len(key) < 16 {
		t.Fatalf("key %s too short: %d bytes", keyPath, len(key))
	}

	c := &http.Client{Timeout: 15 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d from %s", resp.StatusCode, url)
	}
	var p telemetry.ReferenceProfile
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !p.Verify(key) {
		t.Fatalf("Verify FAILED for %s (signature does not match key %s)", url, keyPath)
	}
	t.Logf("verified: signer=%s gpus=%d issued=%d ago=%s",
		p.SignerID, len(p.GPUs), p.IssuedAt, p.FreshnessAge(time.Now()))
}
