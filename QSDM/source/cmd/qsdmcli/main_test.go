package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCLI_Get(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer server.Close()

	cli := &CLI{baseURL: server.URL, client: http.DefaultClient}
	body, err := cli.get("/status")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	if result["status"] != "ok" {
		t.Fatalf("expected ok, got %s", result["status"])
	}
}

func TestCLI_Post(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatal("expected JSON content type")
		}
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		json.NewEncoder(w).Encode(map[string]string{"deployed": body["contract_id"].(string)})
	}))
	defer server.Close()

	cli := &CLI{baseURL: server.URL, client: http.DefaultClient}
	body, err := cli.post("/contracts/deploy", map[string]interface{}{"contract_id": "test1"})
	if err != nil {
		t.Fatalf("post: %v", err)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	if result["deployed"] != "test1" {
		t.Fatalf("expected test1, got %s", result["deployed"])
	}
}

func TestCLI_GetError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte("not found"))
	}))
	defer server.Close()

	cli := &CLI{baseURL: server.URL, client: http.DefaultClient}
	_, err := cli.get("/missing")
	if err == nil {
		t.Fatal("expected error for 404")
	}
}

func TestCLI_AuthHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer mytoken" {
			t.Fatalf("expected Bearer mytoken, got %s", auth)
		}
		json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer server.Close()

	cli := &CLI{baseURL: server.URL, client: http.DefaultClient, token: "mytoken"}
	_, err := cli.get("/test")
	if err != nil {
		t.Fatalf("get with auth: %v", err)
	}
}

func TestPrettyPrint(t *testing.T) {
	data := []byte(`{"a":1,"b":"hello"}`)
	prettyPrint(data) // should not panic

	prettyPrint([]byte("not json")) // should fall back to raw output
}

func TestPrintUsage(t *testing.T) {
	printUsage() // should not panic
}
