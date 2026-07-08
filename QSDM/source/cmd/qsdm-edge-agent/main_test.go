package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentRelayURLPrefersNewFieldAndSupportsLegacyCoordinator(t *testing.T) {
	if got := agentRelayURL(agentFileConfig{Relay: "http://relay:7740", Coordinator: "http://old:7740"}); got != "http://relay:7740" {
		t.Fatalf("relay URL = %q", got)
	}
	if got := agentRelayURL(agentFileConfig{Coordinator: "http://legacy:7740"}); got != "http://legacy:7740" {
		t.Fatalf("legacy coordinator URL = %q", got)
	}
}

func TestResolveRelayTokenPathsSeparatesMotherHive(t *testing.T) {
	agent, mother, err := resolveRelayTokenPaths("", "agent.token", "mother.token")
	if err != nil {
		t.Fatal(err)
	}
	if agent != "agent.token" || mother != "mother.token" {
		t.Fatalf("unexpected token paths agent=%q mother=%q", agent, mother)
	}

	agent, mother, err = resolveRelayTokenPaths("legacy.token", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if agent != "legacy.token" || mother != "legacy.token" {
		t.Fatalf("legacy token path was not applied to both roles")
	}
}

func TestResolveRelayTokenPathsRequiresAgentCredential(t *testing.T) {
	if _, _, err := resolveRelayTokenPaths("", "", "mother.token"); err == nil {
		t.Fatal("Mother Hive token without an agent token was accepted")
	}
}

func TestComputeGatewayClientUsesBearerTokenOnLoopback(t *testing.T) {
	token := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	tokenFile := filepath.Join(t.TempDir(), "compute.token")
	if err := os.WriteFile(tokenFile, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"qsdm-compute-gateway/v1","jobs":[]}`))
	}))
	defer server.Close()

	if err := computeGatewayJSON(http.MethodGet, server.URL, "/v1/jobs", tokenFile, nil); err != nil {
		t.Fatal(err)
	}
}

func TestComputeGatewayClientRejectsNonLoopbackAddress(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "compute.token")
	if err := os.WriteFile(tokenFile, []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := computeGatewayJSON(http.MethodGet, "https://example.com", "/v1/jobs", tokenFile, nil); err == nil {
		t.Fatal("compute client accepted a non-loopback gateway")
	}
}

func TestDefaultComputeGatewayTokenFileUsesHiveApplicationData(t *testing.T) {
	t.Setenv("QSDM_COMPUTE_GATEWAY_TOKEN_FILE", "")
	want := filepath.Join("QSDM-Hive", "namespace", "qsdm-mother-hive", "compute-gateway.token")
	if got := defaultComputeGatewayTokenFile(); !strings.HasSuffix(got, want) {
		t.Fatalf("default Compute Gateway token path = %q, want suffix %q", got, want)
	}
}
