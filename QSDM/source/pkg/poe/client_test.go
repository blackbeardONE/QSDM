package poe

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIRoot(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.qsdm.tech":          "https://api.qsdm.tech",
		"https://api.qsdm.tech/":         "https://api.qsdm.tech",
		"https://api.qsdm.tech/api/v1":   "https://api.qsdm.tech",
		"https://api.qsdm.tech/api/v1/":  "https://api.qsdm.tech",
		" http://127.0.0.1:8080/api/v1 ": "http://127.0.0.1:8080",
	} {
		if got := APIRoot(in); got != want {
			t.Errorf("APIRoot(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFetchParents_UsesParentsEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/chain/parents" {
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"tip":7,"parents":["solo-heartbeat-7-1791611907090994535","solo-heartbeat-6-1791611897090994535"]}`))
	}))
	defer srv.Close()
	got, err := FetchParents(context.Background(), srv.Client(), srv.URL+"/api/v1/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "solo-heartbeat-7-1791611907090994535" {
		t.Fatalf("parents = %v", got)
	}
}

// Nodes released before /api/v1/chain/parents still serve receipts: use the
// newest successful, well-formed, distinct transaction IDs.
func TestFetchParents_FallsBackToReceiptsOnOlderNodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/chain/parents":
			http.NotFound(w, r)
		case "/api/v1/receipts":
			_, _ = w.Write([]byte(`{"receipts":[
				{"tx_id":"failed-transfer-000000001","status":0},
				{"tx_id":"short","status":1},
				{"tx_id":"solo-heartbeat-9-1791611927090994535","status":1},
				{"tx_id":"solo-heartbeat-9-1791611927090994535","status":1},
				{"tx_id":"solo-reward-9-abcdef0123456789","status":1},
				{"tx_id":"solo-heartbeat-8-1791611917090994535","status":1}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	got, err := FetchParents(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"solo-heartbeat-9-1791611927090994535", "solo-reward-9-abcdef0123456789"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("parents = %v, want %v", got, want)
	}
}

func TestFetchParents_Failures(t *testing.T) {
	t.Run("unavailable-is-not-a-fallback", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/receipts" {
				t.Error("503 from the parents endpoint must not fall back")
			}
			http.Error(w, "warming up", http.StatusServiceUnavailable)
		}))
		defer srv.Close()
		if _, err := FetchParents(context.Background(), srv.Client(), srv.URL); err == nil {
			t.Fatal("expected an error")
		}
	})
	t.Run("malformed-parents", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"parents":["parent1","parent2"]}`))
		}))
		defer srv.Close()
		if _, err := FetchParents(context.Background(), srv.Client(), srv.URL); !errors.Is(err, ErrNoParents) {
			t.Fatalf("err = %v, want ErrNoParents", err)
		}
	})
	t.Run("no-receipts", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/receipts" {
				_, _ = w.Write([]byte(`{"receipts":[]}`))
				return
			}
			http.NotFound(w, r)
		}))
		defer srv.Close()
		if _, err := FetchParents(context.Background(), srv.Client(), srv.URL); !errors.Is(err, ErrNoParents) {
			t.Fatalf("err = %v, want ErrNoParents", err)
		}
	})
}
