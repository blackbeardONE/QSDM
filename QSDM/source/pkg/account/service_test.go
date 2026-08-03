package account

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	qcrypto "github.com/blackbeardONE/QSDM/pkg/crypto"
)

type capturedMailer struct {
	recipient string
	link      string
}

type failingMailer struct {
	link string
}

func (m *failingMailer) SendMagicLink(_ context.Context, _, link string) error {
	m.link = link
	return errors.New("mail delivery unavailable")
}

func (m *capturedMailer) SendMagicLink(_ context.Context, recipient, link string) error {
	m.recipient = recipient
	m.link = link
	return nil
}

func testService(t *testing.T) (*Service, *capturedMailer) {
	t.Helper()
	cfg := Config{
		ListenAddress: "127.0.0.1:0",
		PublicBaseURL: "https://qsdm.tech",
		StorePath:     filepath.Join(t.TempDir(), "accounts.json"),
		DataKey:       testDataKey(),
		SessionTTL:    time.Hour,
		MagicLinkTTL:  time.Minute,
		OIDCFlowTTL:   time.Minute,
		SMTPHost:      "smtp.example.test",
		SMTPPort:      587,
		SMTPFrom:      "accounts@qsdm.tech",
		SMTPUseTLS:    true,
	}
	mailer := &capturedMailer{}
	service, err := NewService(cfg, mailer, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	return service, mailer
}

func requestJSON(t *testing.T, handler http.Handler, method, path string, body interface{}, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	request := httptest.NewRequest(method, path, reader)
	request.RemoteAddr = "192.0.2.10:12345"
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	if csrf != "" {
		request.Header.Set("X-QSDM-CSRF", csrf)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestEmailSessionAndWalletSignatureLinkFlow(t *testing.T) {
	service, mailer := testService(t)
	handler := service.Handler()

	start := requestJSON(t, handler, http.MethodPost, "/api/account/email/start", map[string]string{"email": "Person@Example.com"}, nil, "")
	if start.Code != http.StatusAccepted {
		t.Fatalf("email start status=%d body=%s", start.Code, start.Body.String())
	}
	if mailer.recipient != "person@example.com" {
		t.Fatalf("unexpected recipient %q", mailer.recipient)
	}
	link, err := url.Parse(mailer.link)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(link.Fragment, "email_token=")
	if token == "" {
		t.Fatalf("magic link did not put its token in the URL fragment: %s", mailer.link)
	}

	verify := requestJSON(t, handler, http.MethodPost, "/api/account/email/verify", map[string]string{"token": token}, nil, "")
	if verify.Code != http.StatusOK {
		t.Fatalf("email verify status=%d body=%s", verify.Code, verify.Body.String())
	}
	cookies := verify.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookieName || !cookies[0].HttpOnly || !cookies[0].Secure {
		t.Fatalf("session cookie posture is invalid: %#v", cookies)
	}
	session := cookies[0]

	me := requestJSON(t, handler, http.MethodGet, "/api/account/me", nil, session, "")
	if me.Code != http.StatusOK {
		t.Fatalf("me status=%d body=%s", me.Code, me.Body.String())
	}
	var mePayload struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &mePayload); err != nil || mePayload.CSRF == "" {
		t.Fatalf("missing CSRF token: %v body=%s", err, me.Body.String())
	}

	signer := qcrypto.NewDilithium()
	if signer == nil {
		t.Fatal("ML-DSA signer unavailable")
	}
	publicKey := signer.GetPublicKey()
	digest := sha256.Sum256(publicKey)
	address := hex.EncodeToString(digest[:])

	challengeResponse := requestJSON(t, handler, http.MethodPost, "/api/account/wallets/challenge", map[string]string{"address": address}, session, mePayload.CSRF)
	if challengeResponse.Code != http.StatusCreated {
		t.Fatalf("challenge status=%d body=%s", challengeResponse.Code, challengeResponse.Body.String())
	}
	var challengePayload struct {
		Challenge struct {
			ID      string `json:"id"`
			Message string `json:"message"`
		} `json:"challenge"`
	}
	if err := json.Unmarshal(challengeResponse.Body.Bytes(), &challengePayload); err != nil {
		t.Fatal(err)
	}
	signature, err := signer.Sign([]byte(challengePayload.Challenge.Message))
	if err != nil {
		t.Fatal(err)
	}
	confirm := requestJSON(t, handler, http.MethodPost, "/api/account/wallets/confirm", map[string]string{
		"challenge_id": challengePayload.Challenge.ID,
		"address":      address,
		"public_key":   hex.EncodeToString(publicKey),
		"signature":    hex.EncodeToString(signature),
	}, session, mePayload.CSRF)
	if confirm.Code != http.StatusOK {
		t.Fatalf("confirm status=%d body=%s", confirm.Code, confirm.Body.String())
	}

	me = requestJSON(t, handler, http.MethodGet, "/api/account/me", nil, session, "")
	if !strings.Contains(me.Body.String(), address) {
		t.Fatalf("linked wallet missing from account: %s", me.Body.String())
	}

	unlink := requestJSON(t, handler, http.MethodPost, "/api/account/wallets/unlink", map[string]string{"address": address}, session, mePayload.CSRF)
	if unlink.Code != http.StatusOK || !strings.Contains(unlink.Body.String(), `"unlinked":true`) {
		t.Fatalf("unlink status=%d body=%s", unlink.Code, unlink.Body.String())
	}
	me = requestJSON(t, handler, http.MethodGet, "/api/account/me", nil, session, "")
	if strings.Contains(me.Body.String(), address) {
		t.Fatalf("unlinked wallet remained on account: %s", me.Body.String())
	}
}

func TestWalletChallengeRequiresCSRF(t *testing.T) {
	service, mailer := testService(t)
	handler := service.Handler()
	_ = requestJSON(t, handler, http.MethodPost, "/api/account/email/start", map[string]string{"email": "person@example.com"}, nil, "")
	parsed, _ := url.Parse(mailer.link)
	verify := requestJSON(t, handler, http.MethodPost, "/api/account/email/verify", map[string]string{"token": strings.TrimPrefix(parsed.Fragment, "email_token=")}, nil, "")
	session := verify.Result().Cookies()[0]
	response := requestJSON(t, handler, http.MethodPost, "/api/account/wallets/challenge", map[string]string{"address": strings.Repeat("a", 64)}, session, "")
	if response.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF accepted: %d %s", response.Code, response.Body.String())
	}
	response = requestJSON(t, handler, http.MethodPost, "/api/account/wallets/unlink", map[string]string{"address": strings.Repeat("a", 64)}, session, "")
	if response.Code != http.StatusForbidden {
		t.Fatalf("wallet unlink without CSRF accepted: %d %s", response.Code, response.Body.String())
	}
}

func TestAuthenticatedEmailIdentityLinkKeepsOneAccount(t *testing.T) {
	service, mailer := testService(t)
	account, err := service.store.FindOrCreateTelegram("telegram-existing", "@existing")
	if err != nil {
		t.Fatal(err)
	}
	sessionToken := mustCreateSession(t, service.store, account.ID)
	_, csrf, err := service.store.AccountForSession(sessionToken)
	if err != nil {
		t.Fatal(err)
	}
	session := &http.Cookie{Name: sessionCookieName, Value: sessionToken}

	start := requestJSON(t, service.Handler(), http.MethodPost, "/api/account/identities/email/start", map[string]string{"email": "linked@example.com"}, session, csrf)
	if start.Code != http.StatusAccepted {
		t.Fatalf("identity email start status=%d body=%s", start.Code, start.Body.String())
	}
	parsed, err := url.Parse(mailer.link)
	if err != nil {
		t.Fatal(err)
	}
	verify := requestJSON(t, service.Handler(), http.MethodPost, "/api/account/email/verify", map[string]string{"token": strings.TrimPrefix(parsed.Fragment, "email_token=")}, nil, "")
	if verify.Code != http.StatusOK {
		t.Fatalf("identity email verify status=%d body=%s", verify.Code, verify.Body.String())
	}
	linkedSession := verify.Result().Cookies()[0]
	me := requestJSON(t, service.Handler(), http.MethodGet, "/api/account/me", nil, linkedSession, "")
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), account.ID) || !strings.Contains(me.Body.String(), "l****d@example.com") || !strings.Contains(me.Body.String(), "@existing") {
		t.Fatalf("alternate login did not retain the existing account: status=%d body=%s", me.Code, me.Body.String())
	}
}

func TestAuthenticatedTelegramStartBindsCurrentAccountAndRequiresCSRF(t *testing.T) {
	service, mailer := testService(t)
	_ = requestJSON(t, service.Handler(), http.MethodPost, "/api/account/email/start", map[string]string{"email": "person@example.com"}, nil, "")
	parsed, _ := url.Parse(mailer.link)
	verify := requestJSON(t, service.Handler(), http.MethodPost, "/api/account/email/verify", map[string]string{"token": strings.TrimPrefix(parsed.Fragment, "email_token=")}, nil, "")
	session := verify.Result().Cookies()[0]
	account, csrf, _, err := service.currentAccount(httptest.NewRequest(http.MethodGet, "/", nil))
	if err == nil || account != nil || csrf != "" {
		t.Fatal("test request unexpectedly inherited an account session")
	}
	account, csrf, err = service.store.AccountForSession(session.Value)
	if err != nil {
		t.Fatal(err)
	}
	service.telegram = newTelegramOIDC(Config{
		PublicBaseURL:        "https://qsdm.tech",
		OIDCFlowTTL:          time.Minute,
		TelegramClientID:     "telegram-client",
		TelegramClientSecret: "telegram-secret",
	})

	withoutCSRF := requestJSON(t, service.Handler(), http.MethodPost, "/api/account/identities/telegram/start", nil, session, "")
	if withoutCSRF.Code != http.StatusForbidden {
		t.Fatalf("Telegram identity link without CSRF status=%d body=%s", withoutCSRF.Code, withoutCSRF.Body.String())
	}
	start := requestJSON(t, service.Handler(), http.MethodPost, "/api/account/identities/telegram/start", nil, session, csrf)
	if start.Code != http.StatusOK {
		t.Fatalf("Telegram identity start status=%d body=%s", start.Code, start.Body.String())
	}
	var payload struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	destination, err := url.Parse(payload.URL)
	if err != nil {
		t.Fatal(err)
	}
	state := destination.Query().Get("state")
	service.telegram.flowMu.Lock()
	flow := service.telegram.flows[state]
	service.telegram.flowMu.Unlock()
	if state == "" || flow.AccountID != account.ID {
		t.Fatalf("Telegram flow is not bound to the active account: state=%q flow=%#v", state, flow)
	}
}

func TestUndeliveredEmailLinkIsRevoked(t *testing.T) {
	service, _ := testService(t)
	mailer := &failingMailer{}
	service.mailer = mailer
	response := requestJSON(t, service.Handler(), http.MethodPost, "/api/account/email/start", map[string]string{"email": "person@example.com"}, nil, "")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("email failure status=%d body=%s", response.Code, response.Body.String())
	}
	parsed, err := url.Parse(mailer.link)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(parsed.Fragment, "email_token=")
	if token == "" {
		t.Fatal("mailer did not receive a tokenized link")
	}
	if _, err := service.store.ConsumeMagicLink(token); err == nil {
		t.Fatal("an undelivered email token remained usable")
	}
}

func TestStrictJSONAndTrustedProxyAddress(t *testing.T) {
	service, mailer := testService(t)
	request := httptest.NewRequest(http.MethodPost, "/api/account/email/start", strings.NewReader(`{"email":"first@example.com"}{"email":"second@example.com"}`))
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Real-IP", "198.51.100.42")
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || mailer.link != "" {
		t.Fatalf("trailing JSON was accepted: status=%d body=%s", response.Code, response.Body.String())
	}
	if got := clientIP(request); got != "198.51.100.42" {
		t.Fatalf("loopback reverse-proxy address was not used: %q", got)
	}
	request.RemoteAddr = "203.0.113.8:23456"
	if got := clientIP(request); got != "203.0.113.8" {
		t.Fatalf("untrusted direct request spoofed X-Real-IP: %q", got)
	}
}

func TestEmptyEmailTokenIsRejected(t *testing.T) {
	service, _ := testService(t)
	response := requestJSON(t, service.Handler(), http.MethodPost, "/api/account/email/verify", map[string]string{"token": ""}, nil, "")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_token") {
		t.Fatalf("empty token response=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRateWindowTableIsBounded(t *testing.T) {
	service, _ := testService(t)
	until := time.Now().Add(time.Hour)
	for i := 0; i < maxRateWindows; i++ {
		service.rates[fmt.Sprintf("existing-%d", i)] = rateWindow{Count: 1, Until: until}
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "198.51.100.22:12345"
	if service.allow(request, "new-action", 1, time.Minute) {
		t.Fatal("rate limiter accepted a new key after reaching its memory bound")
	}
}

func TestSessionManagementEndpointsKeepTheCurrentBrowser(t *testing.T) {
	service, _ := testService(t)
	account := mustCreateEmailAccount(t, service.store, "sessions@example.com")
	currentToken := mustCreateSession(t, service.store, account.ID)
	otherToken := mustCreateSession(t, service.store, account.ID)
	_, csrf, err := service.store.AccountForSession(currentToken)
	if err != nil {
		t.Fatal(err)
	}
	current := &http.Cookie{Name: sessionCookieName, Value: currentToken}

	list := requestJSON(t, service.Handler(), http.MethodGet, "/api/account/sessions", nil, current, "")
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), currentToken) || strings.Contains(list.Body.String(), "token_hash") {
		t.Fatalf("session list leaked a secret or failed: status=%d body=%s", list.Code, list.Body.String())
	}
	var listed struct {
		Sessions []SessionView `json:"sessions"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil || len(listed.Sessions) != 2 || !listed.Sessions[0].Current {
		t.Fatalf("unexpected session list: err=%v body=%s", err, list.Body.String())
	}

	withoutCSRF := requestJSON(t, service.Handler(), http.MethodPost, "/api/account/sessions/revoke-others", nil, current, "")
	if withoutCSRF.Code != http.StatusForbidden {
		t.Fatalf("session revocation without CSRF status=%d body=%s", withoutCSRF.Code, withoutCSRF.Body.String())
	}
	revoke := requestJSON(t, service.Handler(), http.MethodPost, "/api/account/sessions/revoke-others", nil, current, csrf)
	if revoke.Code != http.StatusOK || !strings.Contains(revoke.Body.String(), `"revoked":1`) {
		t.Fatalf("session revocation status=%d body=%s", revoke.Code, revoke.Body.String())
	}
	if _, _, err := service.store.AccountForSession(otherToken); err == nil {
		t.Fatal("revoked browser session remained active")
	}
	if me := requestJSON(t, service.Handler(), http.MethodGet, "/api/account/me", nil, current, ""); me.Code != http.StatusOK {
		t.Fatalf("current browser was signed out: status=%d body=%s", me.Code, me.Body.String())
	}
}

func TestDeleteProfileRemovesAccountStateButNotByAccident(t *testing.T) {
	service, _ := testService(t)
	account := mustCreateEmailAccount(t, service.store, "delete@example.com")
	sessionToken := mustCreateSession(t, service.store, account.ID)
	_, csrf, err := service.store.AccountForSession(sessionToken)
	if err != nil {
		t.Fatal(err)
	}
	session := &http.Cookie{Name: sessionCookieName, Value: sessionToken}
	pendingLogin, err := service.store.CreateMagicLink("delete@example.com", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	service.telegram = newTelegramOIDC(Config{
		PublicBaseURL:        "https://qsdm.tech",
		OIDCFlowTTL:          time.Minute,
		TelegramClientID:     "telegram-client",
		TelegramClientSecret: "telegram-secret",
	})
	if _, err := service.telegram.startURLForAccount(time.Now(), account.ID); err != nil {
		t.Fatal(err)
	}
	service.challenges["delete-challenge"] = walletChallenge{AccountID: account.ID, ExpiresAt: time.Now().Add(time.Minute)}

	missingCSRF := requestJSON(t, service.Handler(), http.MethodDelete, "/api/account/profile", map[string]string{"confirmation": "DELETE"}, session, "")
	if missingCSRF.Code != http.StatusForbidden {
		t.Fatalf("account deletion without CSRF status=%d body=%s", missingCSRF.Code, missingCSRF.Body.String())
	}
	wrongConfirmation := requestJSON(t, service.Handler(), http.MethodDelete, "/api/account/profile", map[string]string{"confirmation": "delete"}, session, csrf)
	if wrongConfirmation.Code != http.StatusBadRequest {
		t.Fatalf("account deletion accepted a weak confirmation: status=%d body=%s", wrongConfirmation.Code, wrongConfirmation.Body.String())
	}
	deleted := requestJSON(t, service.Handler(), http.MethodDelete, "/api/account/profile", map[string]string{"confirmation": "DELETE"}, session, csrf)
	if deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), "wallet keys") {
		t.Fatalf("account deletion status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	if _, _, err := service.store.AccountForSession(sessionToken); err == nil {
		t.Fatal("deleted account session remained active")
	}
	if _, err := service.store.ConsumeMagicLink(pendingLogin); err == nil {
		t.Fatal("deleted account login link remained active")
	}
	service.challengeMu.Lock()
	challengeCount := len(service.challenges)
	service.challengeMu.Unlock()
	service.telegram.flowMu.Lock()
	flowCount := len(service.telegram.flows)
	service.telegram.flowMu.Unlock()
	if challengeCount != 0 || flowCount != 0 {
		t.Fatalf("deleted account retained transient state: challenges=%d flows=%d", challengeCount, flowCount)
	}
	if cookies := deleted.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != sessionCookieName || cookies[0].MaxAge >= 0 {
		t.Fatalf("account deletion did not clear the browser cookie: %#v", cookies)
	}
}
