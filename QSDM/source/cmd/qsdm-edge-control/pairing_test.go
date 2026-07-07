package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPairingCodeRoundTrip(t *testing.T) {
	token := bytes.Repeat([]byte{0x5a}, 32)
	code, err := encodePairingCode("agent", "http://192.168.20.5:7740", token)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(code, pairingCodePrefix) {
		t.Fatalf("code %q does not use the QSDM prefix", code)
	}
	payload, decoded, err := decodePairingCode(code, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if payload.RelayURL != "http://192.168.20.5:7740" {
		t.Fatalf("unexpected Relay URL %q", payload.RelayURL)
	}
	if !bytes.Equal(decoded, token) {
		t.Fatal("decoded token does not match")
	}
}

func TestPairingCodeSeparatesAgentAndMother(t *testing.T) {
	code, err := encodePairingCode("mother", "http://127.0.0.1:7740", bytes.Repeat([]byte{0x33}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := decodePairingCode(code, "agent"); err == nil || !strings.Contains(err.Error(), "not agent") {
		t.Fatalf("expected role mismatch, got %v", err)
	}
}

func TestPairingCodeRejectsDamagedInput(t *testing.T) {
	if _, _, err := decodePairingCode("QSDM-EDGE-1.not-base64!", "agent"); err == nil {
		t.Fatal("expected damaged pairing code to fail")
	}
}
