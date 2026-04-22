package dashboard

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

func TestPostV1LoginWithoutAPIBackendIs503NotRedirect(t *testing.T) {
	m := monitoring.GetMetrics()
	hc := monitoring.NewHealthChecker(m)
	d := NewDashboard(m, hc, "0", false, DashboardNvidiaLock{}, "e2e-test-hmac-secret", "", false, "", nil)

	h, err := d.buildHandler()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	body := `{"address":"0123456789abcdef0123456789abcdef0123456789","password":"irrelevant"}`
	resp, err := client.Post(srv.URL+"/api/v1/auth/login", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusTemporaryRedirect || resp.StatusCode == http.StatusPermanentRedirect {
		loc := resp.Header.Get("Location")
		t.Fatalf("POST /api/v1/auth/login must not redirect (got %d Location=%q)", resp.StatusCode, loc)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without API backend, got %d", resp.StatusCode)
	}
	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload["status"] == nil {
		t.Fatalf("expected JSON status field: %#v", payload)
	}
}

func TestPostV1LoginProxiesToBackend(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/login" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), "0123456789abcdef0123456789abcdef0123456789") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"stub","expires_in":900}`))
	}))
	t.Cleanup(backend.Close)

	m := monitoring.GetMetrics()
	hc := monitoring.NewHealthChecker(m)
	d := NewDashboard(m, hc, "0", false, DashboardNvidiaLock{}, "e2e-test-hmac-secret", "", false, backend.URL, nil)

	h, err := d.buildHandler()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	resp, err := client.Post(srv.URL+"/api/v1/auth/login", "application/json", strings.NewReader(
		`{"address":"0123456789abcdef0123456789abcdef0123456789","password":"x"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from proxied API, got %d", resp.StatusCode)
	}
}
