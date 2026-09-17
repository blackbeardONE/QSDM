package account

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	qcrypto "github.com/blackbeardONE/QSDM/pkg/crypto"
)

func walletLoginRequest(t *testing.T, s *Service, path string, body interface{}, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/account/wallet-login/"+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	req.RemoteAddr = "192.0.2.42:12345"
	if cookie != nil {
		req.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, req)
	return response
}

type loginTestAttempt struct {
	cookie  *http.Cookie
	body    map[string]string
	message string
}

func prepareWalletLogin(t *testing.T, s *Service, signer *qcrypto.Dilithium) loginTestAttempt {
	t.Helper()
	key := signer.GetPublicKey()
	digest := sha256.Sum256(key)
	address := hex.EncodeToString(digest[:])
	response := walletLoginRequest(t, s, "challenge", map[string]string{"address": address}, nil, s.cfg.PublicBaseURL)
	if response.Code != http.StatusCreated {
		t.Fatalf("challenge status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Challenge struct {
			ID      string `json:"id"`
			Message string `json:"message"`
		} `json:"challenge"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != walletLoginCookieName || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/" || cookies[0].Domain != "" || cookies[0].MaxAge != 300 {
		t.Fatalf("unsafe login cookie: %+v", cookies)
	}
	signature, err := signer.Sign([]byte(payload.Challenge.Message))
	if err != nil {
		t.Fatal(err)
	}
	return loginTestAttempt{cookie: cookies[0], message: payload.Challenge.Message, body: map[string]string{"challenge_id": payload.Challenge.ID, "address": address, "public_key": hex.EncodeToString(key), "signature": hex.EncodeToString(signature)}}
}

func TestWalletLoginCreatesExplicitIdentityAndFreshSession(t *testing.T) {
	service, _ := testService(t)
	service.cfg.WalletLoginEnabled = true
	signer := qcrypto.NewDilithium()
	attempt := prepareWalletLogin(t, service, signer)
	for _, part := range []string{"QSDM Account wallet sign-in\n", "Purpose: Open or create", "Origin: https://qsdm.tech", "Address: " + attempt.body["address"], "Challenge: " + attempt.body["challenge_id"], "Expires:", "does not authorize a transfer"} {
		if !strings.Contains(attempt.message, part) {
			t.Fatalf("missing challenge scope %q", part)
		}
	}
	response := walletLoginRequest(t, service, "confirm", attempt.body, attempt.cookie, service.cfg.PublicBaseURL)
	if response.Code != http.StatusOK {
		t.Fatalf("confirm status=%d body=%s", response.Code, response.Body.String())
	}
	var session *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			session = cookie
		}
	}
	if session == nil || session.Value == attempt.cookie.Value || !session.Secure || !session.HttpOnly {
		t.Fatalf("session not independently issued: %+v", session)
	}
	account, csrf, err := service.store.AccountForSession(session.Value)
	if err != nil || csrf == "" || account.WalletIdentity != attempt.body["address"] || len(account.Wallets) != 1 || account.EmailHash != "" || account.TelegramSubjectHash != "" {
		t.Fatalf("invalid new identity: %+v err=%v", account, err)
	}
	if response := requestJSON(t, service.Handler(), http.MethodPost, "/api/account/logout", nil, session, ""); response.Code != http.StatusForbidden {
		t.Fatalf("new session bypassed CSRF: %d", response.Code)
	}
	if response := requestJSON(t, service.Handler(), http.MethodPost, "/api/account/wallets/unlink", map[string]string{"address": account.WalletIdentity}, session, csrf); response.Code != http.StatusConflict {
		t.Fatalf("sign-in identity unlinked: %d", response.Code)
	}
	reopened, err := OpenStore(service.cfg.StorePath, service.cfg.DataKey)
	if err != nil {
		t.Fatal(err)
	}
	persisted, _, err := reopened.AccountForSession(session.Value)
	if err != nil || persisted.WalletIdentity != account.WalletIdentity {
		t.Fatalf("persisted identity lost: %v", err)
	}
	again := prepareWalletLogin(t, service, signer)
	response = walletLoginRequest(t, service, "confirm", again.body, again.cookie, service.cfg.PublicBaseURL)
	if response.Code != http.StatusOK || len(service.store.accounts) != 1 {
		t.Fatalf("repeat sign-in created duplicate account: %d", response.Code)
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == sessionCookieName && cookie.Value == session.Value {
			t.Fatal("session token reused")
		}
	}
	response = walletLoginRequest(t, service, "confirm", attempt.body, attempt.cookie, service.cfg.PublicBaseURL)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("signature replay accepted: %d", response.Code)
	}
}

func TestWalletLoginRejectsCrossSiteAndNonJSONRequests(t *testing.T) {
	service, _ := testService(t)
	service.cfg.WalletLoginEnabled = true
	for _, origin := range []string{"", "null", "http://qsdm.tech", "https://evil.test", "https://qsdm.tech.evil.test", "https://qsdm.tech/"} {
		response := walletLoginRequest(t, service, "challenge", map[string]string{"address": strings.Repeat("a", 64)}, nil, origin)
		if response.Code != http.StatusForbidden {
			t.Fatalf("origin %q accepted: %d", origin, response.Code)
		}
	}
	for _, mediaType := range []string{"text/plain", "application/x-www-form-urlencoded", ""} {
		req := httptest.NewRequest(http.MethodPost, "/api/account/wallet-login/challenge", strings.NewReader(`{"address":"`+strings.Repeat("a", 64)+`"}`))
		req.Header.Set("Origin", service.cfg.PublicBaseURL)
		req.Header.Set("Content-Type", mediaType)
		response := httptest.NewRecorder()
		service.Handler().ServeHTTP(response, req)
		if response.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("media type %q accepted: %d", mediaType, response.Code)
		}
	}
	if len(service.loginChallenges) != 0 || len(service.store.accounts) != 0 {
		t.Fatal("rejected origins changed auth state")
	}
}

func TestWalletLoginSignatureFailuresConsumeOnlyBoundAttempt(t *testing.T) {
	for _, mode := range []string{"expired", "wrong-address", "wrong-key", "bad-signature", "wrong-purpose", "link-challenge"} {
		t.Run(mode, func(t *testing.T) {
			service, _ := testService(t)
			service.cfg.WalletLoginEnabled = true
			signer := qcrypto.NewDilithium()
			attempt := prepareWalletLogin(t, service, signer)
			switch mode {
			case "expired":
				challenge := service.loginChallenges[attempt.body["challenge_id"]]
				challenge.ExpiresAt = time.Now().Add(-time.Second)
				service.loginChallenges[attempt.body["challenge_id"]] = challenge
			case "wrong-address":
				attempt.body["address"] = strings.Repeat("b", 64)
			case "wrong-key":
				attempt.body["public_key"] = "00"
			case "bad-signature":
				attempt.body["signature"] = "00"
			case "wrong-purpose":
				signature, err := signer.Sign([]byte(strings.Replace(attempt.message, "wallet sign-in", "wallet link", 1)))
				if err != nil {
					t.Fatal(err)
				}
				attempt.body["signature"] = hex.EncodeToString(signature)
			case "link-challenge":
				service.challenges[attempt.body["challenge_id"]] = walletChallenge{Address: attempt.body["address"], Message: attempt.message, ExpiresAt: time.Now().Add(time.Minute)}
				delete(service.loginChallenges, attempt.body["challenge_id"])
			}
			response := walletLoginRequest(t, service, "confirm", attempt.body, attempt.cookie, service.cfg.PublicBaseURL)
			if response.Code != http.StatusUnauthorized || len(service.store.accounts) != 0 || len(service.store.sessions) != 0 {
				t.Fatalf("bad attempt accepted: %d %s", response.Code, response.Body.String())
			}
			if len(service.loginChallenges) != 0 {
				t.Fatal("bound failed attempt not consumed")
			}
		})
	}
}

func TestWalletLoginRequiresBoundBrowserAndSurvivesUnrelatedAttempt(t *testing.T) {
	service, _ := testService(t)
	service.cfg.WalletLoginEnabled = true
	attempt := prepareWalletLogin(t, service, qcrypto.NewDilithium())
	for _, cookie := range []*http.Cookie{nil, {Name: walletLoginCookieName, Value: "another-browser"}} {
		response := walletLoginRequest(t, service, "confirm", attempt.body, cookie, service.cfg.PublicBaseURL)
		if response.Code != http.StatusUnauthorized || len(service.loginChallenges) != 1 {
			t.Fatalf("unbound attempt modified login: %d", response.Code)
		}
	}
	response := walletLoginRequest(t, service, "confirm", attempt.body, attempt.cookie, "https://evil.test")
	if response.Code != http.StatusForbidden || len(service.loginChallenges) != 1 {
		t.Fatal("cross-site confirm modified login")
	}
	response = walletLoginRequest(t, service, "confirm", attempt.body, attempt.cookie, service.cfg.PublicBaseURL)
	if response.Code != http.StatusOK {
		t.Fatalf("original browser rejected: %d", response.Code)
	}
}

func TestWalletLoginNeverConvertsAnExistingPublicLink(t *testing.T) {
	service, _ := testService(t)
	service.cfg.WalletLoginEnabled = true
	attempt := prepareWalletLogin(t, service, qcrypto.NewDilithium())
	account, err := service.store.FindOrCreateTelegram("existing-subject", "existing")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.store.LinkWallet(account.ID, attempt.body["address"]); err != nil {
		t.Fatal(err)
	}
	response := walletLoginRequest(t, service, "confirm", attempt.body, attempt.cookie, service.cfg.PublicBaseURL)
	if response.Code != http.StatusConflict || len(service.store.accounts) != 1 || len(service.store.sessions) != 0 || service.store.accounts[account.ID].WalletIdentity != "" {
		t.Fatalf("linked wallet escalated to identity: %d", response.Code)
	}
}

func TestWalletLoginDisabledRestartAndChallengeCapacity(t *testing.T) {
	service, _ := testService(t)
	response := walletLoginRequest(t, service, "challenge", map[string]string{"address": strings.Repeat("a", 64)}, nil, service.cfg.PublicBaseURL)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("wallet login enabled by default: %d", response.Code)
	}
	service.cfg.WalletLoginEnabled = true
	attempt := prepareWalletLogin(t, service, qcrypto.NewDilithium())
	restarted, err := NewService(service.cfg, nil, service.logger)
	if err != nil {
		t.Fatal(err)
	}
	response = walletLoginRequest(t, restarted, "confirm", attempt.body, attempt.cookie, service.cfg.PublicBaseURL)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("pre-restart challenge accepted: %d", response.Code)
	}
	for i := 0; i < maxWalletChallenges; i++ {
		service.loginChallenges[string(rune(i))] = walletLoginChallenge{ExpiresAt: time.Now().Add(time.Minute)}
	}
	response = walletLoginRequest(t, service, "challenge", map[string]string{"address": strings.Repeat("a", 64)}, nil, service.cfg.PublicBaseURL)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unbounded challenge allocation: %d", response.Code)
	}
}

func TestWalletIdentityPersistenceFailureRollsBack(t *testing.T) {
	service, _ := testService(t)
	service.store.path = t.TempDir()
	if _, err := service.store.FindOrCreateWalletIdentity(strings.Repeat("a", 64)); err == nil || len(service.store.accounts) != 0 {
		t.Fatal("failed persistence left an account active")
	}
}

func TestWalletIdentityStoreRejectsAmbiguousOrLegacyIdentity(t *testing.T) {
	address := strings.Repeat("a", 64)
	for _, mode := range []string{"legacy", "unlinked", "malformed", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			doc := storeDocument{Version: storeVersion, Accounts: []Account{{ID: "acct_test", WalletIdentity: address, Wallets: []WalletLink{{Address: address}}}}}
			switch mode {
			case "legacy":
				doc.Version = previousStoreVersion
			case "unlinked":
				doc.Accounts[0].Wallets = nil
			case "malformed":
				doc.Accounts[0].WalletIdentity = "invalid"
			case "duplicate":
				doc.Accounts = append(doc.Accounts, Account{ID: "acct_other", WalletIdentity: address, Wallets: []WalletLink{{Address: address}}})
			}
			if err := validateStoreDocumentStructure(doc); err == nil {
				t.Fatal("invalid wallet identity document accepted")
			}
		})
	}
}

func TestWalletLoginRateLimitAndStrictFields(t *testing.T) {
	service, _ := testService(t)
	service.cfg.WalletLoginEnabled = true
	response := walletLoginRequest(t, service, "challenge", map[string]string{"address": strings.Repeat("a", 64), "private_key": "never-accepted"}, nil, service.cfg.PublicBaseURL)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unexpected fields accepted: %d", response.Code)
	}
	for i := 0; i < 30; i++ {
		response = walletLoginRequest(t, service, "challenge", map[string]string{"address": strings.Repeat("a", 64)}, nil, service.cfg.PublicBaseURL)
	}
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("login not rate limited: %d", response.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/account/wallet-login/confirm", strings.NewReader(strings.Repeat("x", 65537)))
	req.Header.Set("Origin", service.cfg.PublicBaseURL)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.43:1000"
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("oversized request accepted: %d", recorder.Code)
	}
}

func TestWalletLoginConcurrentReplayAllowsExactlyOneSession(t *testing.T) {
	service, _ := testService(t)
	service.cfg.WalletLoginEnabled = true
	attempt := prepareWalletLogin(t, service, qcrypto.NewDilithium())
	results := make(chan int, 12)
	for i := 0; i < 12; i++ {
		go func() {
			results <- walletLoginRequest(t, service, "confirm", attempt.body, attempt.cookie, service.cfg.PublicBaseURL).Code
		}()
	}
	accepted := 0
	for i := 0; i < 12; i++ {
		status := <-results
		if status == http.StatusOK {
			accepted++
		} else if status != http.StatusUnauthorized {
			t.Fatalf("unexpected replay status %d", status)
		}
	}
	if accepted != 1 || len(service.store.accounts) != 1 || len(service.store.sessions) != 1 {
		t.Fatalf("replayed login created authority: accepted=%d accounts=%d sessions=%d", accepted, len(service.store.accounts), len(service.store.sessions))
	}
}
