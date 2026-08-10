package account

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	qcrypto "github.com/blackbeardONE/QSDM/pkg/crypto"
)

const (
	sessionCookieName   = "__Host-qsdm_account"
	maxRateWindows      = 8192
	maxWalletChallenges = 4096
)

var walletAddressPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type walletChallenge struct {
	AccountID string
	Address   string
	Message   string
	ExpiresAt time.Time
}

type rateWindow struct {
	Count int
	Until time.Time
}

type Service struct {
	cfg      Config
	store    *Store
	mailer   Mailer
	telegram *telegramOIDC
	logger   *log.Logger
	verifier *qcrypto.Dilithium

	challengeMu sync.Mutex
	challenges  map[string]walletChallenge
	rateMu      sync.Mutex
	rates       map[string]rateWindow
}

func NewService(cfg Config, mailer Mailer, logger *log.Logger) (*Service, error) {
	store, err := OpenStore(cfg.StorePath, cfg.DataKey)
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = log.Default()
	}
	service := &Service{
		cfg:        cfg,
		store:      store,
		mailer:     mailer,
		logger:     logger,
		verifier:   qcrypto.NewDilithiumVerifyOnly(),
		challenges: make(map[string]walletChallenge),
		rates:      make(map[string]rateWindow),
	}
	if cfg.EmailEnabled() && service.mailer == nil {
		service.mailer = NewSMTPMailer(cfg)
	}
	if cfg.TelegramEnabled() {
		service.telegram = newTelegramOIDC(cfg)
	}
	if service.verifier == nil {
		return nil, errors.New("ML-DSA wallet verifier is unavailable")
	}
	return service, nil
}

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/account/health", s.health)
	mux.HandleFunc("/api/account/config", s.publicConfig)
	mux.HandleFunc("/api/account/email/start", s.startEmail)
	mux.HandleFunc("/api/account/email/verify", s.verifyEmail)
	mux.HandleFunc("/api/account/telegram/start", s.startTelegram)
	mux.HandleFunc("/api/account/telegram/callback", s.telegramCallback)
	mux.HandleFunc("/api/account/identities/email/start", s.startIdentityEmail)
	mux.HandleFunc("/api/account/identities/telegram/start", s.startIdentityTelegram)
	mux.HandleFunc("/api/account/me", s.me)
	mux.HandleFunc("/api/account/logout", s.logout)
	mux.HandleFunc("/api/account/sessions", s.sessions)
	mux.HandleFunc("/api/account/sessions/revoke-others", s.revokeOtherSessions)
	mux.HandleFunc("/api/account/profile", s.deleteProfile)
	mux.HandleFunc("/api/account/wallets/challenge", s.createWalletChallenge)
	mux.HandleFunc("/api/account/wallets/confirm", s.confirmWalletLink)
	mux.HandleFunc("/api/account/wallets/unlink", s.unlinkWallet)
	return s.securityHeaders(mux)
}

func (s *Service) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]interface{}{
		"ok":    false,
		"error": map[string]string{"code": code, "message": message},
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "The request body is invalid.")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "The request body must contain one JSON object.")
		return false
	}
	return true
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		w.Header().Set("Allow", method)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed.")
		return false
	}
	return true
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || host == "" {
		host = r.RemoteAddr
	}
	remote := net.ParseIP(host)
	if remote != nil && remote.IsLoopback() {
		if forwarded := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); forwarded != nil {
			return forwarded.String()
		}
	}
	return host
}

func (s *Service) allow(r *http.Request, action string, limit int, window time.Duration) bool {
	key := action + "|" + clientIP(r)
	now := time.Now()
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	if len(s.rates) >= maxRateWindows {
		for candidateKey, candidate := range s.rates {
			if !candidate.Until.After(now) {
				delete(s.rates, candidateKey)
			}
		}
	}
	entry, exists := s.rates[key]
	if !exists && len(s.rates) >= maxRateWindows {
		return false
	}
	if !entry.Until.After(now) {
		entry = rateWindow{Until: now.Add(window)}
	}
	if entry.Count >= limit {
		s.rates[key] = entry
		return false
	}
	entry.Count++
	s.rates[key] = entry
	return true
}

func (s *Service) health(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "service": "qsdm-account"})
}

func (s *Service) publicConfig(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true,
		"login": map[string]bool{
			"email":    s.cfg.EmailEnabled(),
			"telegram": s.cfg.TelegramEnabled(),
		},
		"custody": "local_wallet_only",
	})
}

func (s *Service) startEmail(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if !s.cfg.EmailEnabled() || s.mailer == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "email_unavailable", "Email sign-in is not configured.")
		return
	}
	if !s.allow(r, "email-start", 5, 15*time.Minute) {
		writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "Too many sign-in requests. Try again later.")
		return
	}
	var request struct {
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	email, err := normalizeEmail(request.Email)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_email", err.Error())
		return
	}
	if err := s.sendMagicLink(r, email, ""); err != nil {
		s.logger.Printf("account magic-link delivery failed: %v", err)
		writeAPIError(w, http.StatusServiceUnavailable, "email_unavailable", "Email sign-in is temporarily unavailable.")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"ok":      true,
		"message": "Check your email for a one-time QSDM sign-in link.",
	})
}

func (s *Service) sendMagicLink(r *http.Request, email, accountID string) error {
	var (
		token string
		err   error
	)
	if accountID == "" {
		token, err = s.store.CreateMagicLink(email, s.cfg.MagicLinkTTL)
	} else {
		token, err = s.store.CreateIdentityMagicLink(accountID, email, s.cfg.MagicLinkTTL)
	}
	if err != nil {
		return err
	}
	link := s.cfg.PublicBaseURL + "/account/#email_token=" + url.QueryEscape(token)
	ctx, cancel := contextWithTimeout(r, 20*time.Second)
	defer cancel()
	if err := s.mailer.SendMagicLink(ctx, email, link); err != nil {
		if revokeErr := s.store.DeleteMagicLink(token); revokeErr != nil {
			s.logger.Printf("account undelivered magic-link revocation failed: %v", revokeErr)
		}
		return err
	}
	return nil
}

func (s *Service) startIdentityEmail(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	account, _, ok := s.requireSession(w, r, true)
	if !ok {
		return
	}
	if !s.cfg.EmailEnabled() || s.mailer == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "email_unavailable", "Email sign-in is not configured.")
		return
	}
	if account.EmailHash != "" {
		writeAPIError(w, http.StatusConflict, "identity_already_set", "This QSDM Account already has an email sign-in method.")
		return
	}
	if !s.allow(r, "email-link-start", 5, 15*time.Minute) {
		writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "Too many sign-in method requests. Try again later.")
		return
	}
	var request struct {
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	email, err := normalizeEmail(request.Email)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_email", err.Error())
		return
	}
	if err := s.sendMagicLink(r, email, account.ID); err != nil {
		if errors.Is(err, ErrIdentityAlreadySet) {
			writeAPIError(w, http.StatusConflict, "identity_already_set", "This QSDM Account already has an email sign-in method.")
			return
		}
		s.logger.Printf("account email identity-link delivery failed: %v", err)
		writeAPIError(w, http.StatusServiceUnavailable, "email_unavailable", "The email sign-in method is temporarily unavailable.")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"ok":      true,
		"message": "Check your email to add it to this QSDM Account.",
	})
}

func contextWithTimeout(r *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), timeout)
}

func (s *Service) verifyEmail(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if !s.allow(r, "email-verify", 10, 15*time.Minute) {
		writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "Too many verification attempts. Try again later.")
		return
	}
	var request struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if len(request.Token) < 32 || len(request.Token) > 256 {
		writeAPIError(w, http.StatusBadRequest, "invalid_token", "The email sign-in link is invalid.")
		return
	}
	account, err := s.store.ConsumeMagicLink(request.Token)
	if err != nil {
		if errors.Is(err, ErrIdentityInUse) {
			writeAPIError(w, http.StatusConflict, "identity_in_use", "That email already signs in to another QSDM Account. No accounts were merged.")
			return
		}
		if errors.Is(err, ErrIdentityAlreadySet) {
			writeAPIError(w, http.StatusConflict, "identity_already_set", "This QSDM Account already has a different email sign-in method.")
			return
		}
		writeAPIError(w, http.StatusUnauthorized, "invalid_token", "The email sign-in link is invalid or expired.")
		return
	}
	if err := s.issueSession(w, account.ID); err != nil {
		s.logger.Printf("account session creation failed: %v", err)
		writeAPIError(w, http.StatusServiceUnavailable, "session_unavailable", "Could not start an account session.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *Service) startTelegram(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	if s.telegram == nil {
		http.Redirect(w, r, s.cfg.PublicBaseURL+"/account/?error=telegram_unavailable", http.StatusFound)
		return
	}
	if !s.allow(r, "telegram-start", 10, 15*time.Minute) {
		http.Redirect(w, r, s.cfg.PublicBaseURL+"/account/?error=rate_limited", http.StatusFound)
		return
	}
	destination, err := s.telegram.startURL(time.Now())
	if err != nil {
		s.logger.Printf("Telegram OIDC start failed: %v", err)
		http.Redirect(w, r, s.cfg.PublicBaseURL+"/account/?error=telegram_unavailable", http.StatusFound)
		return
	}
	http.Redirect(w, r, destination, http.StatusFound)
}

func (s *Service) startIdentityTelegram(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	account, _, ok := s.requireSession(w, r, true)
	if !ok {
		return
	}
	if s.telegram == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "telegram_unavailable", "Telegram sign-in is not configured.")
		return
	}
	if account.TelegramSubjectHash != "" {
		writeAPIError(w, http.StatusConflict, "identity_already_set", "This QSDM Account already has a Telegram sign-in method.")
		return
	}
	if !s.allow(r, "telegram-link-start", 10, 15*time.Minute) {
		writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "Too many sign-in method requests. Try again later.")
		return
	}
	destination, err := s.telegram.startURLForAccount(time.Now(), account.ID)
	if err != nil {
		s.logger.Printf("Telegram identity-link start failed: %v", err)
		writeAPIError(w, http.StatusServiceUnavailable, "telegram_unavailable", "Telegram sign-in is temporarily unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "url": destination})
}

func (s *Service) telegramCallback(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	if s.telegram == nil || r.URL.Query().Get("code") == "" || r.URL.Query().Get("state") == "" {
		http.Redirect(w, r, s.cfg.PublicBaseURL+"/account/?error=telegram_failed", http.StatusFound)
		return
	}
	ctx, cancel := contextWithTimeout(r, 20*time.Second)
	defer cancel()
	claims, err := s.telegram.exchange(ctx, r.URL.Query().Get("code"), r.URL.Query().Get("state"))
	if err != nil {
		s.logger.Printf("Telegram OIDC callback rejected: %v", err)
		http.Redirect(w, r, s.cfg.PublicBaseURL+"/account/?error=telegram_failed", http.StatusFound)
		return
	}
	var account *Account
	if claims.FlowAccountID != "" {
		current, _, _, sessionErr := s.currentAccount(r)
		if sessionErr != nil || current.ID != claims.FlowAccountID {
			http.Redirect(w, r, s.cfg.PublicBaseURL+"/account/?error=identity_session_changed", http.StatusFound)
			return
		}
		account, err = s.store.LinkTelegramIdentity(claims.FlowAccountID, claims.Subject, telegramDisplayName(claims))
		if errors.Is(err, ErrIdentityInUse) {
			http.Redirect(w, r, s.cfg.PublicBaseURL+"/account/?error=identity_in_use", http.StatusFound)
			return
		}
		if errors.Is(err, ErrIdentityAlreadySet) {
			http.Redirect(w, r, s.cfg.PublicBaseURL+"/account/?error=identity_already_set", http.StatusFound)
			return
		}
		if err == nil {
			http.Redirect(w, r, s.cfg.PublicBaseURL+"/account/?linked=telegram", http.StatusFound)
			return
		}
	} else {
		account, err = s.store.FindOrCreateTelegram(claims.Subject, telegramDisplayName(claims))
	}
	if err != nil {
		s.logger.Printf("Telegram account persistence failed: %v", err)
		http.Redirect(w, r, s.cfg.PublicBaseURL+"/account/?error=session_unavailable", http.StatusFound)
		return
	}
	if err := s.issueSession(w, account.ID); err != nil {
		s.logger.Printf("Telegram session creation failed: %v", err)
		http.Redirect(w, r, s.cfg.PublicBaseURL+"/account/?error=session_unavailable", http.StatusFound)
		return
	}
	http.Redirect(w, r, s.cfg.PublicBaseURL+"/account/", http.StatusFound)
}

func (s *Service) issueSession(w http.ResponseWriter, accountID string) error {
	token, err := s.store.CreateSession(accountID, s.cfg.SessionTTL)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(s.cfg.SessionTTL.Seconds()),
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (s *Service) currentAccount(r *http.Request) (*Account, string, string, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil, "", "", errors.New("account session is required")
	}
	account, csrf, err := s.store.AccountForSession(cookie.Value)
	return account, csrf, cookie.Value, err
}

func (s *Service) requireSession(w http.ResponseWriter, r *http.Request, csrfRequired bool) (*Account, string, bool) {
	account, csrf, _, ok := s.requireCurrentSession(w, r, csrfRequired)
	return account, csrf, ok
}

func (s *Service) requireCurrentSession(w http.ResponseWriter, r *http.Request, csrfRequired bool) (*Account, string, string, bool) {
	account, csrf, token, err := s.currentAccount(r)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "not_authenticated", "Sign in to QSDM Account.")
		return nil, "", "", false
	}
	if csrfRequired && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-QSDM-CSRF")), []byte(csrf)) != 1 {
		writeAPIError(w, http.StatusForbidden, "csrf_failed", "The account security token is missing or invalid.")
		return nil, "", "", false
	}
	return account, csrf, token, true
}

func (s *Service) me(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	account, csrf, ok := s.requireSession(w, r, false)
	if !ok {
		return
	}
	email, telegram, err := s.store.IdentityView(account)
	if err != nil {
		s.logger.Printf("account identity decrypt failed for %s: %v", account.ID, err)
		writeAPIError(w, http.StatusServiceUnavailable, "account_unavailable", "Account details are temporarily unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true,
		"account": map[string]interface{}{
			"id":            account.ID,
			"email":         email,
			"telegram":      telegram,
			"wallets":       account.Wallets,
			"created_at":    account.CreatedAt,
			"last_login_at": account.LastLoginAt,
		},
		"csrf_token": csrf,
	})
}

func (s *Service) logout(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	_, _, token, ok := s.requireCurrentSession(w, r, true)
	if !ok {
		return
	}
	if err := s.store.DeleteSession(token); err != nil {
		s.logger.Printf("account logout persistence failed: %v", err)
		writeAPIError(w, http.StatusServiceUnavailable, "logout_failed", "Could not securely end the account session.")
		return
	}
	clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func (s *Service) sessions(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	account, _, token, ok := s.requireCurrentSession(w, r, false)
	if !ok {
		return
	}
	views, err := s.store.SessionsForAccount(account.ID, token)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "not_authenticated", "Sign in to QSDM Account.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "sessions": views})
}

func (s *Service) revokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	account, _, token, ok := s.requireCurrentSession(w, r, true)
	if !ok {
		return
	}
	revoked, err := s.store.RevokeOtherSessions(account.ID, token)
	if err != nil {
		s.logger.Printf("account session revocation failed for %s: %v", account.ID, err)
		writeAPIError(w, http.StatusServiceUnavailable, "session_revocation_failed", "Could not sign out the other browser sessions.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "revoked": revoked})
}

func (s *Service) deleteProfile(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodDelete) {
		return
	}
	account, _, _, ok := s.requireCurrentSession(w, r, true)
	if !ok {
		return
	}
	var request struct {
		Confirmation string `json:"confirmation"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.Confirmation != "DELETE" {
		writeAPIError(w, http.StatusBadRequest, "confirmation_required", "Type DELETE to confirm account deletion.")
		return
	}
	if err := s.store.DeleteAccount(account.ID); err != nil {
		s.logger.Printf("account deletion failed for %s: %v", account.ID, err)
		writeAPIError(w, http.StatusServiceUnavailable, "account_deletion_failed", "Could not delete the QSDM Account.")
		return
	}
	s.discardAccountState(account.ID)
	clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "QSDM Account data was deleted. Hive, wallet keys, and CELL were not changed.",
	})
}

func (s *Service) discardAccountState(accountID string) {
	s.challengeMu.Lock()
	for id, challenge := range s.challenges {
		if challenge.AccountID == accountID {
			delete(s.challenges, id)
		}
	}
	s.challengeMu.Unlock()
	if s.telegram != nil {
		s.telegram.discardAccountFlows(accountID)
	}
}

func (s *Service) createWalletChallenge(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	account, _, ok := s.requireSession(w, r, true)
	if !ok {
		return
	}
	if !s.allow(r, "wallet-challenge", 20, 15*time.Minute) {
		writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "Too many wallet-link requests. Try again later.")
		return
	}
	var request struct {
		Address string `json:"address"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	address := strings.ToLower(strings.TrimSpace(request.Address))
	if !walletAddressPattern.MatchString(address) {
		writeAPIError(w, http.StatusBadRequest, "invalid_wallet", "Enter a valid QSDM wallet address.")
		return
	}
	id, err := randomToken(24)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "challenge_unavailable", "Could not create a wallet challenge.")
		return
	}
	expires := time.Now().UTC().Add(5 * time.Minute)
	message := fmt.Sprintf("QSDM Account wallet link\nVersion: 1\nAccount: %s\nAddress: %s\nChallenge: %s\nExpires: %s\nOrigin: %s", account.ID, address, id, expires.Format(time.RFC3339), s.cfg.PublicBaseURL)
	now := time.Now()
	s.challengeMu.Lock()
	for key, challenge := range s.challenges {
		if !challenge.ExpiresAt.After(now) || challenge.AccountID == account.ID {
			delete(s.challenges, key)
		}
	}
	if len(s.challenges) >= maxWalletChallenges {
		s.challengeMu.Unlock()
		writeAPIError(w, http.StatusServiceUnavailable, "challenge_unavailable", "Wallet linking is temporarily busy. Try again shortly.")
		return
	}
	s.challenges[id] = walletChallenge{AccountID: account.ID, Address: address, Message: message, ExpiresAt: expires}
	s.challengeMu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"ok": true,
		"challenge": map[string]interface{}{
			"id": id, "message": message, "expires_at": expires,
		},
	})
}

func (s *Service) confirmWalletLink(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	account, _, ok := s.requireSession(w, r, true)
	if !ok {
		return
	}
	var request struct {
		ChallengeID string `json:"challenge_id"`
		Address     string `json:"address"`
		PublicKey   string `json:"public_key"`
		Signature   string `json:"signature"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	address := strings.ToLower(strings.TrimSpace(request.Address))
	s.challengeMu.Lock()
	challenge, exists := s.challenges[request.ChallengeID]
	delete(s.challenges, request.ChallengeID)
	s.challengeMu.Unlock()
	if !exists || !challenge.ExpiresAt.After(time.Now()) || challenge.AccountID != account.ID || challenge.Address != address {
		writeAPIError(w, http.StatusUnauthorized, "challenge_invalid", "The wallet-link challenge is invalid or expired.")
		return
	}
	publicKey, err := hex.DecodeString(request.PublicKey)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "signature_invalid", "The wallet public key is invalid.")
		return
	}
	signature, err := hex.DecodeString(strings.TrimSpace(request.Signature))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "signature_invalid", "The wallet signature is invalid.")
		return
	}
	digest := sha256.Sum256(publicKey)
	if hex.EncodeToString(digest[:]) != address {
		writeAPIError(w, http.StatusUnauthorized, "wallet_mismatch", "The public key does not belong to this wallet address.")
		return
	}
	valid, err := s.verifier.VerifyWithPublicKey([]byte(challenge.Message), signature, publicKey)
	if err != nil || !valid {
		writeAPIError(w, http.StatusUnauthorized, "signature_invalid", "The wallet signature could not be verified.")
		return
	}
	if err := s.store.LinkWallet(account.ID, address); err != nil {
		if strings.Contains(err.Error(), "another account") {
			writeAPIError(w, http.StatusConflict, "wallet_already_linked", "This wallet is already linked to another QSDM Account.")
			return
		}
		s.logger.Printf("wallet link persistence failed: %v", err)
		writeAPIError(w, http.StatusServiceUnavailable, "wallet_link_failed", "The wallet link could not be saved.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "address": address})
}

func (s *Service) unlinkWallet(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	account, _, ok := s.requireSession(w, r, true)
	if !ok {
		return
	}
	if !s.allow(r, "wallet-unlink", 20, 15*time.Minute) {
		writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "Too many wallet changes. Try again later.")
		return
	}
	var request struct {
		Address string `json:"address"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	address := strings.ToLower(strings.TrimSpace(request.Address))
	if !walletAddressPattern.MatchString(address) {
		writeAPIError(w, http.StatusBadRequest, "invalid_wallet", "Enter a valid QSDM wallet address.")
		return
	}
	unlinked, err := s.store.UnlinkWallet(account.ID, address)
	if err != nil {
		s.logger.Printf("wallet unlink persistence failed: %v", err)
		writeAPIError(w, http.StatusServiceUnavailable, "wallet_unlink_failed", "The wallet link could not be removed.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "address": address, "unlinked": unlinked})
}
