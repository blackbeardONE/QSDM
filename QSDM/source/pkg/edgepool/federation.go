package edgepool

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	FederationContextVersion = 1
	FederationTokenDomain    = "QSDM-EDGE-FEDERATION-TOKEN-v1"
	// Federation invitations are generated for 24 hours. The extra hour
	// tolerates ordinary clock skew without permitting durable internet keys.
	MaximumFederationInvitationLifetime = 25 * time.Hour

	WorkloadCPUHashChain  = "qsdm.cpu.hash-chain.v1"
	WorkloadGPUCUDAMix    = "qsdm.gpu.cuda-mix.v1"
	WorkloadRAMMemoryScan = "qsdm.ram.memory-scan.v1"
)

var federationOfferIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{2,95}$`)
var federationWalletPattern = regexp.MustCompile(`^[A-Za-z0-9]{32,128}$`)

// FederationContext is the immutable, server-validated scope of an internet
// Mother Hive invitation. The encoded context travels with every request so
// the Relay can derive the short-lived credential from its private Mother key.
type FederationContext struct {
	Version        int      `json:"version"`
	RelayURL       string   `json:"relay_url"`
	OfferID        string   `json:"offer_id"`
	ProviderName   string   `json:"provider_name"`
	ProviderWallet string   `json:"provider_wallet,omitempty"`
	ConsumerWallet string   `json:"consumer_wallet,omitempty"`
	ExpiresAt      string   `json:"expires_at"`
	WorkloadIDs    []string `json:"workload_ids"`
}

func DefaultFederationWorkloadIDs() []string {
	return []string{WorkloadCPUHashChain, WorkloadGPUCUDAMix, WorkloadRAMMemoryScan}
}

func normalizeFederationContext(value FederationContext, now time.Time) (FederationContext, error) {
	if value.Version != FederationContextVersion {
		return FederationContext{}, fmt.Errorf("federation context version must be %d", FederationContextVersion)
	}
	parsed, err := url.Parse(strings.TrimSpace(value.RelayURL))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return FederationContext{}, errors.New("federation Relay address must be an https:// URL without credentials, query, or fragment")
	}
	parsed.Path = "/"
	parsed.RawPath = ""
	value.RelayURL = parsed.String()
	value.OfferID = strings.TrimSpace(value.OfferID)
	if !federationOfferIDPattern.MatchString(value.OfferID) {
		return FederationContext{}, errors.New("federation offer id is invalid")
	}
	value.ProviderName = strings.TrimSpace(value.ProviderName)
	if value.ProviderName == "" || len(value.ProviderName) > 80 {
		return FederationContext{}, errors.New("federation provider name is invalid")
	}
	value.ProviderWallet = strings.TrimSpace(value.ProviderWallet)
	if value.ProviderWallet != "" && !federationWalletPattern.MatchString(value.ProviderWallet) {
		return FederationContext{}, errors.New("federation provider wallet is invalid")
	}
	value.ConsumerWallet = strings.TrimSpace(value.ConsumerWallet)
	if value.ConsumerWallet != "" && !federationWalletPattern.MatchString(value.ConsumerWallet) {
		return FederationContext{}, errors.New("federation consumer wallet is invalid")
	}
	expires, err := time.Parse(time.RFC3339, strings.TrimSpace(value.ExpiresAt))
	if err != nil {
		return FederationContext{}, errors.New("federation expiry is invalid")
	}
	if !expires.After(now.UTC()) {
		return FederationContext{}, errors.New("federation invitation has expired")
	}
	if expires.After(now.UTC().Add(MaximumFederationInvitationLifetime)) {
		return FederationContext{}, errors.New("federation invitation expiry exceeds the maximum lifetime")
	}
	value.ExpiresAt = expires.UTC().Format(time.RFC3339)

	seen := make(map[string]struct{}, len(value.WorkloadIDs))
	workloads := make([]string, 0, len(value.WorkloadIDs))
	for _, workloadID := range value.WorkloadIDs {
		workloadID = strings.TrimSpace(workloadID)
		switch workloadID {
		case WorkloadCPUHashChain, WorkloadGPUCUDAMix, WorkloadRAMMemoryScan:
		default:
			return FederationContext{}, fmt.Errorf("unsupported federation workload %q", workloadID)
		}
		if _, duplicate := seen[workloadID]; duplicate {
			continue
		}
		seen[workloadID] = struct{}{}
		workloads = append(workloads, workloadID)
	}
	if len(workloads) == 0 {
		return FederationContext{}, errors.New("federation invitation has no allowed workloads")
	}
	sort.Strings(workloads)
	value.WorkloadIDs = workloads
	return value, nil
}

func EncodeFederationContext(value FederationContext, now time.Time) (string, FederationContext, error) {
	normalized, err := normalizeFederationContext(value, now)
	if err != nil {
		return "", FederationContext{}, err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", FederationContext{}, err
	}
	return base64.RawURLEncoding.EncodeToString(raw), normalized, nil
}

func DecodeFederationContext(encoded string, now time.Time) (FederationContext, error) {
	if len(encoded) == 0 || len(encoded) > 4096 {
		return FederationContext{}, errors.New("federation context is missing or too large")
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return FederationContext{}, errors.New("federation context is damaged")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value FederationContext
	if err := decoder.Decode(&value); err != nil {
		return FederationContext{}, errors.New("federation context is damaged")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return FederationContext{}, errors.New("federation context contains trailing data")
	}
	normalized, err := normalizeFederationContext(value, now)
	if err != nil {
		return FederationContext{}, err
	}
	canonical, _, err := EncodeFederationContext(normalized, now)
	if err != nil {
		return FederationContext{}, err
	}
	if !hmac.Equal([]byte(canonical), []byte(encoded)) {
		return FederationContext{}, errors.New("federation context is not canonical")
	}
	return normalized, nil
}

func DeriveFederationToken(motherToken []byte, encodedContext string, now time.Time) ([]byte, FederationContext, error) {
	if len(motherToken) < 32 {
		return nil, FederationContext{}, errors.New("Relay Mother Hive token must contain at least 32 bytes")
	}
	context, err := DecodeFederationContext(encodedContext, now)
	if err != nil {
		return nil, FederationContext{}, err
	}
	mac := hmac.New(sha256.New, motherToken)
	_, _ = mac.Write([]byte(FederationTokenDomain))
	_, _ = mac.Write([]byte{'\n'})
	_, _ = mac.Write([]byte(encodedContext))
	return mac.Sum(nil), context, nil
}

func (value FederationContext) AllowsResource(resource ResourceKind) bool {
	wanted := ""
	switch resource {
	case ResourceCPU:
		wanted = WorkloadCPUHashChain
	case ResourceGPU:
		wanted = WorkloadGPUCUDAMix
	case ResourceRAM:
		wanted = WorkloadRAMMemoryScan
	default:
		return false
	}
	for _, workloadID := range value.WorkloadIDs {
		if workloadID == wanted {
			return true
		}
	}
	return false
}
