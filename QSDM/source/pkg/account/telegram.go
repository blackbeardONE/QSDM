package account

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	telegramIssuer       = "https://oauth.telegram.org"
	telegramAuthorizeURL = telegramIssuer + "/auth"
	telegramTokenURL     = telegramIssuer + "/token"
	telegramJWKSURL      = telegramIssuer + "/.well-known/jwks.json"
	maxTelegramOIDCFlows = 4096
)

var errTelegramFlowCapacity = errors.New("Telegram login is temporarily at capacity")

type telegramFlow struct {
	Verifier  string
	Nonce     string
	AccountID string
	Expires   time.Time
}

type telegramClaims struct {
	Issuer            string      `json:"iss"`
	Audience          interface{} `json:"aud"`
	Subject           string      `json:"sub"`
	ExpiresAt         int64       `json:"exp"`
	IssuedAt          int64       `json:"iat"`
	Nonce             string      `json:"nonce"`
	Name              string      `json:"name"`
	PreferredUsername string      `json:"preferred_username"`
	FlowAccountID     string      `json:"-"`
}

type telegramOIDC struct {
	clientID     string
	clientSecret string
	redirectURI  string
	flowTTL      time.Duration
	httpClient   *http.Client
	authorizeURL string
	tokenURL     string
	jwksURL      string

	flowMu sync.Mutex
	flows  map[string]telegramFlow

	keysMu      sync.Mutex
	keys        map[string]*rsa.PublicKey
	keysExpires time.Time
}

func newTelegramOIDC(cfg Config) *telegramOIDC {
	return &telegramOIDC{
		clientID:     cfg.TelegramClientID,
		clientSecret: cfg.TelegramClientSecret,
		redirectURI:  cfg.PublicBaseURL + "/api/account/telegram/callback",
		flowTTL:      cfg.OIDCFlowTTL,
		httpClient:   &http.Client{Timeout: 12 * time.Second},
		authorizeURL: telegramAuthorizeURL,
		tokenURL:     telegramTokenURL,
		jwksURL:      telegramJWKSURL,
		flows:        make(map[string]telegramFlow),
		keys:         make(map[string]*rsa.PublicKey),
	}
}

func (t *telegramOIDC) startURL(now time.Time) (string, error) {
	return t.startURLForAccount(now, "")
}

func (t *telegramOIDC) startURLForAccount(now time.Time, accountID string) (string, error) {
	state, err := randomToken(32)
	if err != nil {
		return "", err
	}
	verifier, err := randomToken(48)
	if err != nil {
		return "", err
	}
	nonce, err := randomToken(32)
	if err != nil {
		return "", err
	}
	challengeBytes := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeBytes[:])

	t.flowMu.Lock()
	for key, flow := range t.flows {
		if !flow.Expires.After(now) || (accountID != "" && flow.AccountID == accountID) {
			delete(t.flows, key)
		}
	}
	if len(t.flows) >= maxTelegramOIDCFlows {
		t.flowMu.Unlock()
		return "", errTelegramFlowCapacity
	}
	t.flows[state] = telegramFlow{Verifier: verifier, Nonce: nonce, AccountID: accountID, Expires: now.Add(t.flowTTL)}
	t.flowMu.Unlock()

	query := url.Values{
		"client_id":             {t.clientID},
		"redirect_uri":          {t.redirectURI},
		"response_type":         {"code"},
		"scope":                 {"openid profile"},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	return t.authorizeURL + "?" + query.Encode(), nil
}

func (t *telegramOIDC) consumeFlow(state string, now time.Time) (telegramFlow, error) {
	t.flowMu.Lock()
	defer t.flowMu.Unlock()
	flow, ok := t.flows[state]
	delete(t.flows, state)
	if !ok || !flow.Expires.After(now) {
		return telegramFlow{}, errors.New("Telegram login state is invalid or expired")
	}
	return flow, nil
}

func (t *telegramOIDC) discardAccountFlows(accountID string) {
	if accountID == "" {
		return
	}
	t.flowMu.Lock()
	defer t.flowMu.Unlock()
	for state, flow := range t.flows {
		if flow.AccountID == accountID {
			delete(t.flows, state)
		}
	}
}

func (t *telegramOIDC) exchange(ctx context.Context, code, state string) (telegramClaims, error) {
	flow, err := t.consumeFlow(state, time.Now())
	if err != nil {
		return telegramClaims{}, err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {t.redirectURI},
		"client_id":     {t.clientID},
		"code_verifier": {flow.Verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return telegramClaims{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(t.clientID, t.clientSecret)
	response, err := t.httpClient.Do(req)
	if err != nil {
		return telegramClaims{}, fmt.Errorf("exchange Telegram authorization code: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return telegramClaims{}, fmt.Errorf("Telegram token endpoint returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		IDToken string `json:"id_token"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return telegramClaims{}, fmt.Errorf("decode Telegram token response: %w", err)
	}
	if payload.IDToken == "" {
		return telegramClaims{}, errors.New("Telegram token response did not include an ID token")
	}
	claims, err := t.verifyIDToken(ctx, payload.IDToken)
	if err != nil {
		return telegramClaims{}, err
	}
	if claims.Nonce != flow.Nonce {
		return telegramClaims{}, errors.New("Telegram ID token nonce mismatch")
	}
	claims.FlowAccountID = flow.AccountID
	return claims, nil
}

func decodeJWTPart(value string, target interface{}) error {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

func (t *telegramOIDC) verifyIDToken(ctx context.Context, token string) (telegramClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return telegramClaims{}, errors.New("Telegram ID token is malformed")
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
		Type      string `json:"typ"`
	}
	if err := decodeJWTPart(parts[0], &header); err != nil {
		return telegramClaims{}, errors.New("decode Telegram ID token header")
	}
	if header.Algorithm != "RS256" || header.KeyID == "" {
		return telegramClaims{}, errors.New("Telegram ID token uses an unsupported signing key")
	}
	key, err := t.signingKey(ctx, header.KeyID)
	if err != nil {
		return telegramClaims{}, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return telegramClaims{}, errors.New("decode Telegram ID token signature")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return telegramClaims{}, errors.New("Telegram ID token signature is invalid")
	}
	var claims telegramClaims
	if err := decodeJWTPart(parts[1], &claims); err != nil {
		return telegramClaims{}, errors.New("decode Telegram ID token claims")
	}
	now := time.Now().Unix()
	if claims.Issuer != telegramIssuer || !audienceContains(claims.Audience, t.clientID) || claims.ExpiresAt <= now || claims.IssuedAt > now+60 || claims.Subject == "" {
		return telegramClaims{}, errors.New("Telegram ID token claims are invalid")
	}
	return claims, nil
}

func audienceContains(audience interface{}, expected string) bool {
	switch value := audience.(type) {
	case string:
		return value == expected
	case []interface{}:
		for _, entry := range value {
			if text, ok := entry.(string); ok && text == expected {
				return true
			}
		}
	}
	return false
}

func (t *telegramOIDC) signingKey(ctx context.Context, keyID string) (*rsa.PublicKey, error) {
	t.keysMu.Lock()
	if key := t.keys[keyID]; key != nil && t.keysExpires.After(time.Now()) {
		t.keysMu.Unlock()
		return key, nil
	}
	t.keysMu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.jwksURL, nil)
	if err != nil {
		return nil, err
	}
	response, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch Telegram signing keys: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Telegram key endpoint returned HTTP %d", response.StatusCode)
	}
	var document struct {
		Keys []struct {
			KeyID     string `json:"kid"`
			KeyType   string `json:"kty"`
			Algorithm string `json:"alg"`
			Modulus   string `json:"n"`
			Exponent  string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&document); err != nil {
		return nil, fmt.Errorf("decode Telegram signing keys: %w", err)
	}
	parsed := make(map[string]*rsa.PublicKey)
	for _, item := range document.Keys {
		if item.KeyID == "" || item.KeyType != "RSA" || item.Algorithm != "RS256" {
			continue
		}
		nBytes, nErr := base64.RawURLEncoding.DecodeString(item.Modulus)
		eBytes, eErr := base64.RawURLEncoding.DecodeString(item.Exponent)
		if nErr != nil || eErr != nil || len(nBytes) == 0 || len(eBytes) == 0 || len(eBytes) > 4 {
			continue
		}
		e := 0
		for _, b := range eBytes {
			e = e<<8 | int(b)
		}
		if e < 3 {
			continue
		}
		parsed[item.KeyID] = &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}
	}
	if len(parsed) == 0 {
		return nil, errors.New("Telegram signing key set contains no usable RS256 keys")
	}
	t.keysMu.Lock()
	t.keys = parsed
	t.keysExpires = time.Now().Add(time.Hour)
	key := t.keys[keyID]
	t.keysMu.Unlock()
	if key == nil {
		return nil, fmt.Errorf("Telegram signing key %q was not found", keyID)
	}
	return key, nil
}

func telegramDisplayName(claims telegramClaims) string {
	if claims.PreferredUsername != "" {
		return "@" + strings.TrimPrefix(claims.PreferredUsername, "@")
	}
	if claims.Name != "" {
		return claims.Name
	}
	return "Telegram " + strconv.FormatUint(shortSubject(claims.Subject), 10)
}

func shortSubject(subject string) uint64 {
	digest := sha256.Sum256([]byte(subject))
	var value uint64
	for _, b := range digest[:4] {
		value = value<<8 | uint64(b)
	}
	return value
}
