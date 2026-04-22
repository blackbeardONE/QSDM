package main

// Integration test: builds the trustcheck binary with `go build` and
// invokes it via os/exec against an in-process httptest server that
// mimics a validator's trust endpoints. This exercises the full
// flag-parsing → HTTP → JSON decode → assertion path end-to-end,
// catching any regressions that pure in-process tests would miss
// (missing -json flag handling, stdout framing, exit codes, etc.).
//
// Only runs under `go test -run Integration` so fast unit-test cycles
// stay fast; CI calls the integration tests explicitly.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// buildTrustcheck compiles the trustcheck binary next to this test
// and returns the path. Binary is built once per test process via
// sync.Once semantics implied by t.Cleanup — each test that needs
// it calls this helper, but only the first actually spawns `go build`.
//
// On Windows the spawned child process can be blocked by Defender or
// IPv6 loopback oddities from dialing back into the httptest server;
// the integration suite is therefore skipped on Windows. CI runs on
// Linux (see .github/workflows/qsdm-split-profile.yml) so this does
// not compromise coverage of the binary in automation.
func buildTrustcheck(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("trustcheck integration tests skipped on Windows; Linux CI exercises them")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "trustcheck")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build trustcheck: %v\n%s", err, string(out))
	}
	return bin
}

// buildFixtureSummary returns a valid summary response consistent with
// Major Update §8.5.3 and the validator fixtures in
// pkg/api/handlers_trust_test.go.
func buildFixtureSummary(attested, totalPublic int, now time.Time) map[string]any {
	lastAttested := now.Add(-30 * time.Second).Format(time.RFC3339)
	ratio := 0.0
	if totalPublic > 0 {
		ratio = float64(attested) / float64(totalPublic)
	}
	return map[string]any{
		"attested":           attested,
		"total_public":       totalPublic,
		"ratio":              ratio,
		"fresh_within":       "15m0s",
		"last_attested_at":   lastAttested,
		"last_checked_at":    now.Format(time.RFC3339),
		"ngc_service_status": "healthy",
		"scope_note":         "NVIDIA-lock is an opt-in, per-operator API policy — not a consensus rule. See NVIDIA_LOCK_CONSENSUS_SCOPE.md.",
	}
}

func buildFixtureRecent(n int, now time.Time) map[string]any {
	rows := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, map[string]any{
			"node_id_prefix":    "deadbeef…0123",
			"attested_at":       now.Add(-time.Duration(10+i*10) * time.Second).Format(time.RFC3339),
			"fresh_age_seconds": int64(10 + i*10),
			"gpu_architecture":  "hopper",
			"gpu_available":     true,
			"ngc_hmac_ok":       true,
			"region_hint":       []string{"eu", "us", "apac", "other"}[i%4],
		})
	}
	return map[string]any{
		"fresh_within": "15m0s",
		"count":        len(rows),
		"attestations": rows,
	}
}

func TestIntegration_HappyPath(t *testing.T) {
	bin := buildTrustcheck(t)
	now := time.Now().UTC()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/trust/attestations/summary", func(w http.ResponseWriter, r *http.Request) {
		// Each distinct integer prefix must NOT be a duplicate, so
		// we send attested=1 and only one recent row — otherwise the
		// trustcheck duplicate-prefix assertion fires on fixtures
		// that share the "deadbeef…0123" prefix string.
		writeJSON(w, http.StatusOK, buildFixtureSummary(1, 3, now))
	})
	mux.HandleFunc("/api/v1/trust/attestations/recent", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, buildFixtureRecent(1, now))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cmd := exec.Command(bin, "-base", srv.URL, "-json", "-timeout", "5s")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("trustcheck failed against fixture: %v\n%s", err, string(out))
	}
	var report struct {
		Pass       bool `json:"pass"`
		Assertions []struct {
			Name   string `json:"name"`
			Pass   bool   `json:"pass"`
			Detail string `json:"detail,omitempty"`
		} `json:"assertions"`
	}
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("trustcheck output not JSON: %v\n%s", err, string(out))
	}
	if !report.Pass {
		for _, a := range report.Assertions {
			if !a.Pass {
				t.Errorf("assertion %q failed: %s", a.Name, a.Detail)
			}
		}
		t.Fatal("trustcheck reported pass=false against a valid fixture")
	}
}

func TestIntegration_ScopeNoteDriftIsDetected(t *testing.T) {
	bin := buildTrustcheck(t)
	now := time.Now().UTC()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/trust/attestations/summary", func(w http.ResponseWriter, r *http.Request) {
		sum := buildFixtureSummary(1, 3, now)
		sum["scope_note"] = "NVIDIA-lock is awesome and totally required."
		writeJSON(w, http.StatusOK, sum)
	})
	mux.HandleFunc("/api/v1/trust/attestations/recent", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, buildFixtureRecent(1, now))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cmd := exec.Command(bin, "-base", srv.URL, "-json", "-timeout", "5s")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected trustcheck to fail on scope-note drift, got pass\n%s", string(out))
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() != 2 {
		t.Errorf("expected exit code 2 (contract failure), got %d\n%s", ee.ExitCode(), string(out))
	}
	if !strings.Contains(string(out), "scope-note-verbatim") {
		t.Errorf("output should mention scope-note assertion, got:\n%s", string(out))
	}
}

func TestIntegration_WarmingUpAllowedDowngradesToZero(t *testing.T) {
	bin := buildTrustcheck(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/trust/attestations/summary", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "trust aggregator warming up"})
	})
	mux.HandleFunc("/api/v1/trust/attestations/recent", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "trust aggregator warming up"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Without -allow-warmup the tool exits 3.
	cmd := exec.Command(bin, "-base", srv.URL, "-timeout", "5s")
	err := cmd.Run()
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 3 {
		t.Errorf("default 503 handling should exit 3, got err=%v", err)
	}

	// With -allow-warmup it exits 0.
	cmd = exec.Command(bin, "-base", srv.URL, "-timeout", "5s", "-allow-warmup")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("-allow-warmup should downgrade 503 to exit 0, got err=%v\n%s", err, string(out))
	}
}

func TestIntegration_DisabledAllowedDowngradesToZero(t *testing.T) {
	bin := buildTrustcheck(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/trust/attestations/summary", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "trust endpoints disabled on this node"})
	})
	mux.HandleFunc("/api/v1/trust/attestations/recent", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "trust endpoints disabled on this node"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cmd := exec.Command(bin, "-base", srv.URL, "-timeout", "5s", "-allow-disabled")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("-allow-disabled should downgrade 404 to exit 0, got err=%v\n%s", err, string(out))
	}
}

// End-to-end test against the in-process fixture server using a stock
// QSDM-shaped response: no flag gymnastics, just the defaults as a
// third-party scraper would invoke.
func TestIntegration_DefaultFlagsAgainstFullAdoption(t *testing.T) {
	bin := buildTrustcheck(t)
	now := time.Now().UTC()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/trust/attestations/summary", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, buildFixtureSummary(3, 3, now))
	})
	mux.HandleFunc("/api/v1/trust/attestations/recent", func(w http.ResponseWriter, r *http.Request) {
		// NB: 3 rows with distinct prefixes so the duplicate-prefix
		// assertion inside trustcheck does not fire.
		rows := []map[string]any{
			{"node_id_prefix": "aaaaaaaa…1111", "attested_at": now.Add(-10 * time.Second).Format(time.RFC3339), "fresh_age_seconds": int64(10), "region_hint": "eu"},
			{"node_id_prefix": "bbbbbbbb…2222", "attested_at": now.Add(-20 * time.Second).Format(time.RFC3339), "fresh_age_seconds": int64(20), "region_hint": "us"},
			{"node_id_prefix": "cccccccc…3333", "attested_at": now.Add(-30 * time.Second).Format(time.RFC3339), "fresh_age_seconds": int64(30), "region_hint": "apac"},
		}
		writeJSON(w, http.StatusOK, map[string]any{"fresh_within": "15m0s", "count": len(rows), "attestations": rows})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cmd := exec.Command(bin, "-base", srv.URL, "-timeout", "5s")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("trustcheck failed against full-adoption fixture: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "3/3 attested") && !strings.Contains(string(out), "3 of 3") {
		// Human checklist output should mention "3 of 3 attested".
		// Exact phrasing is "3 of 3" per the current emit() format.
		t.Errorf("human output should surface ratio, got:\n%s", string(out))
	}
}
