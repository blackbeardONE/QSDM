package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGatewayAllowlist(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend-Path", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	u, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	g := newGatewayHandler(u, false, false)

	tests := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{"status", http.MethodGet, "/api/v1/status", http.StatusNoContent},
		{"work", http.MethodGet, "/api/v1/mining/work", http.StatusNoContent},
		{"challenge", http.MethodGet, "/api/v1/mining/challenge", http.StatusNoContent},
		{"submit", http.MethodPost, "/api/v1/mining/submit", http.StatusNoContent},
		{"enrollment query", http.MethodGet, "/api/v1/mining/enrollment/rtx3050", http.StatusNoContent},
		{"dashboard blocked", http.MethodGet, "/", http.StatusForbidden},
		{"wallet blocked", http.MethodPost, "/api/v1/wallet/send", http.StatusForbidden},
		{"hive signed wallet blocked by default", http.MethodPost, "/api/v1/wallet/submit-signed", http.StatusForbidden},
		{"hive tasks blocked by default", http.MethodGet, "/api/v1/tasks", http.StatusForbidden},
		{"admin blocked", http.MethodGet, "/api/admin/accounts", http.StatusForbidden},
		{"enroll blocked by default", http.MethodPost, "/api/v1/mining/enroll", http.StatusForbidden},
		{"wrong method blocked", http.MethodDelete, "/api/v1/mining/work", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader("{}"))
			rec := httptest.NewRecorder()
			g.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestGatewayOptionalEnrollment(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer backend.Close()

	u, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	g := newGatewayHandler(u, true, false)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/mining/enroll"},
		{http.MethodPost, "/api/v1/mining/unenroll"},
		{http.MethodGet, "/api/v1/mining/enrollments"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("%s %s status = %d, want 202", tc.method, tc.path, rec.Code)
		}
	}
}

func TestGatewayOptionalHiveConsumerAPI(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer backend.Close()

	u, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	g := newGatewayHandler(u, false, true)

	for _, tc := range []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{"versions", http.MethodGet, "/api/v1/versions", http.StatusAccepted},
		{"wallet balance", http.MethodGet, "/api/v1/wallet/balance", http.StatusAccepted},
		{"wallet nonce", http.MethodGet, "/api/v1/wallet/nonce", http.StatusAccepted},
		{"mining account", http.MethodGet, "/api/v1/mining/account", http.StatusAccepted},
		{"tasks list", http.MethodGet, "/api/v1/tasks", http.StatusAccepted},
		{"task detail", http.MethodGet, "/api/v1/tasks/task-1", http.StatusAccepted},
		{"task state", http.MethodGet, "/api/v1/tasks/task-1/state", http.StatusAccepted},
		{"task submissions", http.MethodGet, "/api/v1/tasks/task-1/submissions", http.StatusAccepted},
		{"task actions", http.MethodGet, "/api/v1/tasks/actions", http.StatusAccepted},
		{"receipt list", http.MethodGet, "/api/v1/receipts", http.StatusAccepted},
		{"receipt detail", http.MethodGet, "/api/v1/receipts/tx-1", http.StatusAccepted},
		{"signed wallet submit", http.MethodPost, "/api/v1/wallet/submit-signed", http.StatusAccepted},
		{"signed task action", http.MethodPost, "/api/v1/tasks/actions/submit-signed", http.StatusAccepted},
		{"path traversal blocked", http.MethodGet, "/api/v1/tasks/../admin/accounts", http.StatusForbidden},
		{"raw wallet send blocked", http.MethodPost, "/api/v1/wallet/send", http.StatusForbidden},
		{"admin blocked", http.MethodGet, "/api/admin/accounts", http.StatusForbidden},
		{"auth blocked", http.MethodPost, "/api/v1/auth/login", http.StatusForbidden},
		{"mint blocked", http.MethodPost, "/api/v1/wallet/mint", http.StatusForbidden},
		{"enroll still blocked", http.MethodPost, "/api/v1/mining/enroll", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
			rec := httptest.NewRecorder()
			g.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
