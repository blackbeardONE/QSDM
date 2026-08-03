package account

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func jwtPart(t *testing.T, value interface{}) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func signedTelegramToken(t *testing.T, key *rsa.PrivateKey, keyID, clientID, nonce string) string {
	t.Helper()
	header := jwtPart(t, map[string]string{"alg": "RS256", "kid": keyID, "typ": "JWT"})
	now := time.Now().Unix()
	payload := jwtPart(t, map[string]interface{}{
		"iss":                telegramIssuer,
		"aud":                clientID,
		"sub":                "123456789",
		"iat":                now - 1,
		"exp":                now + 300,
		"nonce":              nonce,
		"name":               "QSDM Test",
		"preferred_username": "qsdm_test",
	})
	input := header + "." + payload
	digest := sha256.Sum256([]byte(input))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func TestTelegramAuthorizationCodePKCEFlow(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const keyID = "telegram-test-key"
	const clientID = "123456"
	const clientSecret = "test-secret"
	var tokenNonce string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			username, password, ok := r.BasicAuth()
			if !ok || username != clientID || password != clientSecret {
				t.Errorf("unexpected token endpoint authentication")
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "test-code" || r.Form.Get("code_verifier") == "" {
				t.Errorf("unexpected token form: %v", r.Form)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id_token": signedTelegramToken(t, key, keyID, clientID, tokenNonce),
			})
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"keys": []map[string]string{{
					"kid": keyID,
					"kty": "RSA",
					"alg": "RS256",
					"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
					"e":   base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}),
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	oidc := newTelegramOIDC(Config{
		PublicBaseURL:        "https://qsdm.tech",
		OIDCFlowTTL:          time.Minute,
		TelegramClientID:     clientID,
		TelegramClientSecret: clientSecret,
	})
	oidc.authorizeURL = server.URL + "/auth"
	oidc.tokenURL = server.URL + "/token"
	oidc.jwksURL = server.URL + "/jwks"
	oidc.httpClient = server.Client()

	authorize, err := oidc.startURLForAccount(time.Now(), "acct_existing")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorize)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	state := query.Get("state")
	tokenNonce = query.Get("nonce")
	if parsed.Path != "/auth" || state == "" || tokenNonce == "" || query.Get("code_challenge") == "" || query.Get("code_challenge_method") != "S256" {
		t.Fatalf("authorization URL is missing OIDC/PKCE controls: %s", authorize)
	}
	if !strings.Contains(query.Get("scope"), "openid") {
		t.Fatalf("authorization scope does not include openid: %q", query.Get("scope"))
	}

	claims, err := oidc.exchange(context.Background(), "test-code", state)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "123456789" || telegramDisplayName(claims) != "@qsdm_test" {
		t.Fatalf("unexpected Telegram identity: %#v", claims)
	}
	if claims.FlowAccountID != "acct_existing" {
		t.Fatalf("Telegram identity-link flow lost its account binding: %#v", claims)
	}
	if _, err := oidc.exchange(context.Background(), "test-code", state); err == nil {
		t.Fatal("reused Telegram OIDC state was accepted")
	}
}

func TestTelegramFlowCapacityCleanupAndAccountReplacement(t *testing.T) {
	oidc := newTelegramOIDC(Config{
		PublicBaseURL:        "https://qsdm.tech",
		OIDCFlowTTL:          time.Minute,
		TelegramClientID:     "123456",
		TelegramClientSecret: "test-secret",
	})
	now := time.Now()
	for i := 0; i < maxTelegramOIDCFlows; i++ {
		oidc.flows[fmt.Sprintf("state-%d", i)] = telegramFlow{Expires: now.Add(time.Minute)}
	}
	if _, err := oidc.startURL(now); !errors.Is(err, errTelegramFlowCapacity) {
		t.Fatalf("anonymous flow was accepted at capacity: %v", err)
	}

	oidc.flows["state-0"] = telegramFlow{Expires: now.Add(-time.Second)}
	if _, err := oidc.startURL(now); err != nil {
		t.Fatalf("expired flow did not free capacity: %v", err)
	}
	if len(oidc.flows) != maxTelegramOIDCFlows {
		t.Fatalf("flow count after cleanup = %d, want %d", len(oidc.flows), maxTelegramOIDCFlows)
	}

	oidc.flows["state-1"] = telegramFlow{AccountID: "acct_existing", Expires: now.Add(time.Minute)}
	if _, err := oidc.startURLForAccount(now, "acct_existing"); err != nil {
		t.Fatalf("account-bound flow could not replace its older state at capacity: %v", err)
	}
	accountFlows := 0
	for _, flow := range oidc.flows {
		if flow.AccountID == "acct_existing" {
			accountFlows++
		}
	}
	if len(oidc.flows) != maxTelegramOIDCFlows || accountFlows != 1 {
		t.Fatalf("account flow replacement left flows=%d accountFlows=%d", len(oidc.flows), accountFlows)
	}
}
