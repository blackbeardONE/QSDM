package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/edgepool"
)

const (
	pairingCodePrefix            = "QSDM-EDGE-1."
	federationPairingCodePrefix  = "QSDM-EDGE-2."
	localMotherPairingCodePrefix = "QSDM-EDGE-3."
)

type pairingPayload struct {
	Version           int      `json:"version"`
	Kind              string   `json:"kind"`
	RelayURL          string   `json:"relay_url"`
	Token             string   `json:"token"`
	OfferID           string   `json:"offer_id,omitempty"`
	ProviderName      string   `json:"provider_name,omitempty"`
	ProviderWallet    string   `json:"provider_wallet,omitempty"`
	ConsumerWallet    string   `json:"consumer_wallet,omitempty"`
	ExpiresAt         string   `json:"expires_at,omitempty"`
	WorkloadIDs       []string `json:"workload_ids,omitempty"`
	FederationContext string   `json:"federation_context,omitempty"`
	MotherID          string   `json:"mother_id,omitempty"`
	MotherName        string   `json:"mother_name,omitempty"`
	MotherContext     string   `json:"mother_context,omitempty"`
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

func encodeFederationPairingCode(relayURL string, token []byte, providerName string) (string, error) {
	parsed, err := validateRelayURL(relayURL, false)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "https" {
		return "", errors.New("internet federation invitations require an https:// Relay address")
	}
	now := time.Now().UTC()
	offerBytes := make([]byte, 12)
	if _, err := rand.Read(offerBytes); err != nil {
		return "", fmt.Errorf("generate federation offer id: %w", err)
	}
	contextValue := edgepool.FederationContext{
		Version:      edgepool.FederationContextVersion,
		RelayURL:     parsed.String(),
		OfferID:      "edge-" + hex.EncodeToString(offerBytes),
		ProviderName: strings.TrimSpace(providerName),
		ExpiresAt:    now.Add(24 * time.Hour).Format(time.RFC3339),
		WorkloadIDs:  edgepool.DefaultFederationWorkloadIDs(),
	}
	if contextValue.ProviderName == "" {
		contextValue.ProviderName = "QSDM Edge Relay"
	}
	encodedContext, normalizedContext, err := edgepool.EncodeFederationContext(contextValue, now)
	if err != nil {
		return "", err
	}
	federationToken, _, err := edgepool.DeriveFederationToken(token, encodedContext, now)
	if err != nil {
		return "", err
	}
	payload := pairingPayload{
		Version:           2,
		Kind:              "mother-federation",
		RelayURL:          normalizedContext.RelayURL,
		Token:             hex.EncodeToString(federationToken),
		OfferID:           normalizedContext.OfferID,
		ProviderName:      normalizedContext.ProviderName,
		ProviderWallet:    normalizedContext.ProviderWallet,
		ConsumerWallet:    normalizedContext.ConsumerWallet,
		ExpiresAt:         normalizedContext.ExpiresAt,
		WorkloadIDs:       normalizedContext.WorkloadIDs,
		FederationContext: encodedContext,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return federationPairingCodePrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func encodeLocalMotherPairingCode(relayURL, stateDir string, token []byte, motherName string) (string, edgepool.MotherTenantStatus, error) {
	parsed, err := validateRelayURL(relayURL, false)
	if err != nil {
		return "", edgepool.MotherTenantStatus{}, err
	}
	encodedContext, scopedToken, tenant, err := edgepool.CreateMotherTenantCredential(
		stateDir,
		token,
		motherName,
		time.Now().UTC(),
	)
	if err != nil {
		return "", edgepool.MotherTenantStatus{}, err
	}
	payload := pairingPayload{
		Version:       3,
		Kind:          "mother-local",
		RelayURL:      parsed.String(),
		Token:         hex.EncodeToString(scopedToken),
		MotherID:      tenant.MotherID,
		MotherName:    tenant.MotherName,
		MotherContext: encodedContext,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", edgepool.MotherTenantStatus{}, err
	}
	return localMotherPairingCodePrefix + base64.RawURLEncoding.EncodeToString(raw), tenant, nil
}

func decodePairingCode(value, expectedKind string) (pairingPayload, []byte, error) {
	value = strings.TrimSpace(value)
	if len(value) > 4096 || (!strings.HasPrefix(value, pairingCodePrefix) && !strings.HasPrefix(value, federationPairingCodePrefix) && !strings.HasPrefix(value, localMotherPairingCodePrefix)) {
		return pairingPayload{}, nil, errors.New("this is not a valid QSDM Edge pairing code")
	}
	prefix := pairingCodePrefix
	if strings.HasPrefix(value, federationPairingCodePrefix) {
		prefix = federationPairingCodePrefix
	} else if strings.HasPrefix(value, localMotherPairingCodePrefix) {
		prefix = localMotherPairingCodePrefix
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, prefix))
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
	expectedVersion := 1
	if expectedKind == "mother-federation" {
		expectedVersion = 2
	} else if expectedKind == "mother-local" {
		expectedVersion = 3
	}
	if payload.Kind != expectedKind {
		return pairingPayload{}, nil, fmt.Errorf("this pairing code is for %s, not %s", payload.Kind, expectedKind)
	}
	if expectedKind == "mother-federation" && (payload.Version != expectedVersion || prefix != federationPairingCodePrefix) {
		return pairingPayload{}, nil, errors.New("legacy federation invitation is not server-expiring; create a new invitation")
	}
	if expectedKind == "mother-local" && (payload.Version != expectedVersion || prefix != localMotherPairingCodePrefix) {
		return pairingPayload{}, nil, errors.New("local Mother Hive pairing code is not scoped to one Hive")
	}
	if payload.Version != expectedVersion {
		return pairingPayload{}, nil, fmt.Errorf("pairing code version %d is not supported", payload.Version)
	}
	parsed, err := validateRelayURL(payload.RelayURL, false)
	if err != nil {
		return pairingPayload{}, nil, fmt.Errorf("pairing code Relay address: %w", err)
	}
	if expectedKind == "mother-federation" {
		parsed.Path = "/"
	}
	payload.RelayURL = parsed.String()
	token, err := hex.DecodeString(payload.Token)
	if err != nil || len(token) < 32 {
		return pairingPayload{}, nil, errors.New("pairing code has an invalid security key")
	}
	if expectedKind == "mother-federation" {
		contextValue, err := edgepool.DecodeFederationContext(payload.FederationContext, time.Now().UTC())
		if err != nil {
			return pairingPayload{}, nil, fmt.Errorf("federation context: %w", err)
		}
		if payload.RelayURL != contextValue.RelayURL || payload.OfferID != contextValue.OfferID ||
			payload.ProviderName != contextValue.ProviderName || payload.ProviderWallet != contextValue.ProviderWallet ||
			payload.ConsumerWallet != contextValue.ConsumerWallet || payload.ExpiresAt != contextValue.ExpiresAt ||
			strings.Join(payload.WorkloadIDs, "\n") != strings.Join(contextValue.WorkloadIDs, "\n") {
			return pairingPayload{}, nil, errors.New("federation invitation metadata does not match its credential context")
		}
	}
	if expectedKind == "mother-local" {
		contextValue, err := edgepool.DecodeMotherContext(payload.MotherContext)
		if err != nil {
			return pairingPayload{}, nil, fmt.Errorf("Mother Hive context: %w", err)
		}
		if payload.MotherID != contextValue.MotherID || payload.MotherName != contextValue.MotherName {
			return pairingPayload{}, nil, errors.New("Mother Hive invitation metadata does not match its credential context")
		}
	}
	return payload, token, nil
}
