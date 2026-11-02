package account

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"time"
)

const walletLoginCookieName = "__Host-qsdm_wallet_login"
const walletLoginTTL = 5 * time.Minute

type walletLoginChallenge struct {
	Address     string
	Message     string
	BrowserHash string
	ExpiresAt   time.Time
}

// Wallet sign-in is a separate authority from an existing public wallet link.
// Only an explicitly created wallet identity can open a profile this way.
func (s *Store) FindOrCreateWalletIdentity(address string) (*Account, error) {
	if !walletAddressPattern.MatchString(address) {
		return nil, errors.New("invalid wallet identity")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for _, candidate := range s.accounts {
		if candidate.WalletIdentity == address {
			previous := cloneAccount(candidate)
			candidate.LastLoginAt = now
			if err := s.saveLocked(); err != nil {
				*candidate = *previous
				return nil, err
			}
			return cloneAccount(candidate), nil
		}
		for _, linked := range candidate.Wallets {
			if linked.Address == address {
				return nil, ErrIdentityInUse
			}
		}
	}
	id, err := randomToken(18)
	if err != nil {
		return nil, err
	}
	account := &Account{ID: "acct_" + id, WalletIdentity: address, Wallets: []WalletLink{{Address: address, LinkedAt: now}}, CreatedAt: now, LastLoginAt: now}
	s.accounts[account.ID] = account
	if err := s.saveLocked(); err != nil {
		delete(s.accounts, account.ID)
		return nil, err
	}
	return cloneAccount(account), nil
}

func (s *Service) requireWalletLoginRequest(w http.ResponseWriter, r *http.Request) bool {
	if !requireMethod(w, r, http.MethodPost) {
		return false
	}
	if !s.cfg.WalletLoginEnabled {
		writeAPIError(w, http.StatusServiceUnavailable, "wallet_login_unavailable", "Wallet sign-in is not enabled.")
		return false
	}
	// Exact origin and JSON prevent cross-site login/session substitution. The
	// challenge also binds to a host-only HttpOnly cookie unavailable to signers.
	if r.Header.Get("Origin") != s.cfg.PublicBaseURL {
		writeAPIError(w, http.StatusForbidden, "origin_invalid", "Start wallet sign-in on the QSDM Account website.")
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(w, http.StatusUnsupportedMediaType, "content_type_invalid", "Use a JSON request.")
		return false
	}
	if !s.allow(r, "wallet-login", 30, 15*time.Minute) {
		writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "Too many wallet sign-in requests. Try again later.")
		return false
	}
	return true
}

func (s *Service) createWalletLoginChallenge(w http.ResponseWriter, r *http.Request) {
	if !s.requireWalletLoginRequest(w, r) {
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
		writeAPIError(w, http.StatusServiceUnavailable, "challenge_unavailable", "Could not start wallet sign-in.")
		return
	}
	browserToken, err := randomToken(32)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "challenge_unavailable", "Could not start wallet sign-in.")
		return
	}
	now := time.Now().UTC()
	expires := now.Add(walletLoginTTL)
	message := fmt.Sprintf("QSDM Account wallet sign-in\nVersion: 1\nPurpose: Open or create a QSDM Account profile\nOrigin: %s\nAddress: %s\nChallenge: %s\nExpires: %s\nThis signature does not authorize a transfer or restore previous account identities.", s.cfg.PublicBaseURL, address, id, expires.Format(time.RFC3339))
	oldBrowserHash := ""
	if cookie, err := r.Cookie(walletLoginCookieName); err == nil {
		oldBrowserHash = keyedHash(s.cfg.DataKey, "wallet-login-browser", cookie.Value)
	}
	s.challengeMu.Lock()
	for key, challenge := range s.loginChallenges {
		if !challenge.ExpiresAt.After(now) || (oldBrowserHash != "" && challenge.BrowserHash == oldBrowserHash) {
			delete(s.loginChallenges, key)
		}
	}
	if len(s.loginChallenges) >= maxWalletChallenges {
		s.challengeMu.Unlock()
		writeAPIError(w, http.StatusServiceUnavailable, "challenge_unavailable", "Wallet sign-in is temporarily busy.")
		return
	}
	s.loginChallenges[id] = walletLoginChallenge{Address: address, Message: message, BrowserHash: keyedHash(s.cfg.DataKey, "wallet-login-browser", browserToken), ExpiresAt: expires}
	s.challengeMu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: walletLoginCookieName, Value: browserToken, Path: "/", MaxAge: int(walletLoginTTL.Seconds()), Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusCreated, map[string]interface{}{"ok": true, "challenge": map[string]interface{}{"id": id, "message": message, "expires_at": expires}})
}

func (s *Service) confirmWalletLogin(w http.ResponseWriter, r *http.Request) {
	if !s.requireWalletLoginRequest(w, r) {
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
	cookie, err := r.Cookie(walletLoginCookieName)
	if err != nil || cookie.Value == "" {
		writeAPIError(w, http.StatusUnauthorized, "challenge_invalid", "Return to the browser where you started wallet sign-in.")
		return
	}
	browserHash := keyedHash(s.cfg.DataKey, "wallet-login-browser", cookie.Value)
	address := strings.ToLower(strings.TrimSpace(request.Address))
	s.challengeMu.Lock()
	challenge, exists := s.loginChallenges[request.ChallengeID]
	browserMatches := subtle.ConstantTimeCompare([]byte(challenge.BrowserHash), []byte(browserHash)) == 1
	// Only the matching browser may consume a challenge; another browser cannot
	// invalidate a legitimate attempt. A matching attempt is consumed on failure.
	if exists && browserMatches {
		delete(s.loginChallenges, request.ChallengeID)
	}
	s.challengeMu.Unlock()
	if !exists || !browserMatches || !challenge.ExpiresAt.After(time.Now()) || challenge.Address != address {
		writeAPIError(w, http.StatusUnauthorized, "challenge_invalid", "The wallet sign-in challenge is invalid or expired.")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: walletLoginCookieName, Value: "", Path: "/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	publicKey, keyErr := hex.DecodeString(request.PublicKey)
	signature, sigErr := hex.DecodeString(strings.TrimSpace(request.Signature))
	if keyErr != nil || sigErr != nil {
		writeAPIError(w, http.StatusUnauthorized, "signature_invalid", "The wallet signature could not be verified.")
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
	account, err := s.store.FindOrCreateWalletIdentity(address)
	if errors.Is(err, ErrIdentityInUse) {
		writeAPIError(w, http.StatusConflict, "identity_in_use", "This wallet is linked to a profile that uses another sign-in method. No profiles were merged.")
		return
	}
	if err != nil {
		s.logger.Printf("wallet identity creation failed: %v", err)
		writeAPIError(w, http.StatusServiceUnavailable, "account_unavailable", "Could not open the account profile.")
		return
	}
	// issueSession always generates a fresh random token; caller-supplied session
	// IDs and the browser challenge cookie can never become an account session.
	if err := s.issueSession(w, account.ID); err != nil {
		s.logger.Printf("wallet session creation failed: %v", err)
		writeAPIError(w, http.StatusServiceUnavailable, "session_unavailable", "Could not create the account session.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}
