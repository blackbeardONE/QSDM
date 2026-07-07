package main

import "testing"

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
