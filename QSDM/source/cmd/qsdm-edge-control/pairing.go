package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const pairingCodePrefix = "QSDM-EDGE-1."

type pairingPayload struct {
	Version  int    `json:"version"`
	Kind     string `json:"kind"`
	RelayURL string `json:"relay_url"`
	Token    string `json:"token"`
}

func encodePairingCode(kind, relayURL string, token []byte) (string, error) {
	if kind != "agent" && kind != "mother" {
		return "", errors.New("pairing code kind must be agent or mother")
	}
	if len(token) < 32 {
		return "", errors.New("pairing token must contain at least 32 bytes")
	}
	parsed, err := validateRelayURL(relayURL, false)
	if err != nil {
		return "", err
	}
	payload := pairingPayload{
		Version:  1,
		Kind:     kind,
		RelayURL: parsed.String(),
		Token:    hex.EncodeToString(token),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return pairingCodePrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodePairingCode(value, expectedKind string) (pairingPayload, []byte, error) {
	value = strings.TrimSpace(value)
	if len(value) > 4096 || !strings.HasPrefix(value, pairingCodePrefix) {
		return pairingPayload{}, nil, errors.New("this is not a valid QSDM Edge pairing code")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, pairingCodePrefix))
	if err != nil {
		return pairingPayload{}, nil, errors.New("pairing code is damaged or incomplete")
	}
	var payload pairingPayload
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return pairingPayload{}, nil, errors.New("pairing code is damaged or incomplete")
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return pairingPayload{}, nil, errors.New("pairing code contains unexpected data")
	}
	if payload.Version != 1 || payload.Kind != expectedKind {
		return pairingPayload{}, nil, fmt.Errorf("this pairing code is for %s, not %s", payload.Kind, expectedKind)
	}
	parsed, err := validateRelayURL(payload.RelayURL, false)
	if err != nil {
		return pairingPayload{}, nil, fmt.Errorf("pairing code Relay address: %w", err)
	}
	payload.RelayURL = parsed.String()
	token, err := hex.DecodeString(payload.Token)
	if err != nil || len(token) < 32 {
		return pairingPayload{}, nil, errors.New("pairing code has an invalid security key")
	}
	return payload, token, nil
}
