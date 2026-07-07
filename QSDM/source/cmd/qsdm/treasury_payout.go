package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/envcompat"
)

const (
	referralTreasurySignerURLEnv       = "QSDM_REFERRAL_TREASURY_SIGNER_URL"
	referralTreasurySignerTokenEnv     = "QSDM_REFERRAL_TREASURY_SIGNER_TOKEN"
	referralTreasurySignerTokenFileEnv = "QSDM_REFERRAL_TREASURY_SIGNER_TOKEN_FILE"
	faucetTreasurySignerURLEnv         = "QSDM_FAUCET_TREASURY_SIGNER_URL"
	faucetTreasurySignerTokenEnv       = "QSDM_FAUCET_TREASURY_SIGNER_TOKEN"
	faucetTreasurySignerTokenFileEnv   = "QSDM_FAUCET_TREASURY_SIGNER_TOKEN_FILE"
)

func wireTreasuryPayoutServices(logger *logging.Logger) error {
	if err := rejectLegacyReferralRewardPoolSeed(); err != nil {
		return err
	}
	referralEnabled := envcompat.Truthy("QSDM_REFERRAL_REWARD_POOL_ENABLED", "QSDM_REFERRAL_REWARD_POOL_ENABLED")
	faucetEnabled := envcompat.Truthy("QSDM_LOCAL_CELL_FAUCET", "QSDM_LOCAL_CELL_FAUCET")
	referralAddress, err := requireTreasuryAddress("QSDM_REFERRAL_REWARD_POOL_ADDRESS", referralEnabled)
	if err != nil {
		return err
	}
	faucetAddress, err := requireTreasuryAddress("QSDM_FAUCET_TREASURY_ADDRESS", faucetEnabled)
	if err != nil {
		return err
	}
	if referralEnabled && faucetEnabled && strings.EqualFold(referralAddress, faucetAddress) {
		return fmt.Errorf("referral and onboarding programs must use different treasury wallets")
	}

	referral, err := treasuryPayoutServiceFromEnv(referralTreasurySignerURLEnv, referralTreasurySignerTokenEnv, referralTreasurySignerTokenFileEnv, referralEnabled)
	if err != nil {
		return fmt.Errorf("referral treasury: %w", err)
	}
	api.SetReferralTreasuryPayoutService(referral)
	if referral != nil && logger != nil {
		logger.Info("Referral treasury signer configured", "url", strings.TrimSpace(os.Getenv(referralTreasurySignerURLEnv)))
	}

	faucet, err := treasuryPayoutServiceFromEnv(faucetTreasurySignerURLEnv, faucetTreasurySignerTokenEnv, faucetTreasurySignerTokenFileEnv, faucetEnabled)
	if err != nil {
		return fmt.Errorf("onboarding treasury: %w", err)
	}
	api.SetFaucetTreasuryPayoutService(faucet)
	if faucet != nil && logger != nil {
		logger.Info("Onboarding treasury signer configured", "url", strings.TrimSpace(os.Getenv(faucetTreasurySignerURLEnv)))
	}
	return nil
}

func requireTreasuryAddress(key string, required bool) (string, error) {
	address := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if address == "" {
		if required {
			return "", fmt.Errorf("feature is enabled but %s is missing", key)
		}
		return "", nil
	}
	if err := api.ValidateAddress(address); err != nil {
		return "", fmt.Errorf("%s is invalid: %w", key, err)
	}
	return address, nil
}

func treasuryPayoutServiceFromEnv(urlKey, tokenKey, tokenFileKey string, required bool) (api.TreasuryPayoutService, error) {
	baseURL := strings.TrimSpace(os.Getenv(urlKey))
	token := strings.TrimSpace(os.Getenv(tokenKey))
	tokenFile := strings.TrimSpace(os.Getenv(tokenFileKey))
	if tokenFile != "" {
		data, err := os.ReadFile(tokenFile) // #nosec G304,G703 -- local token path is supplied by the trusted service operator.
		if err != nil {
			return nil, fmt.Errorf("%s: %w", tokenFileKey, err)
		}
		token = strings.TrimSpace(string(data))
	}
	if baseURL == "" && token == "" && tokenFile == "" {
		if required {
			return nil, fmt.Errorf("feature is enabled but %s and %s are missing", urlKey, tokenFileKey)
		}
		return nil, nil
	}
	if baseURL == "" || token == "" {
		return nil, fmt.Errorf("%s and %s (or %s) must be configured together", urlKey, tokenFileKey, tokenKey)
	}
	service, err := api.NewHTTPTreasuryPayoutService(baseURL, token, 10*time.Second)
	if err != nil {
		return nil, err
	}
	return service, nil
}
