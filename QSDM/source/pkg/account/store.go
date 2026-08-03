package account

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const storeVersion = 1

var (
	ErrIdentityInUse      = errors.New("identity is already linked to another account")
	ErrIdentityAlreadySet = errors.New("account already has this sign-in method")
)

type WalletLink struct {
	Address  string    `json:"address"`
	LinkedAt time.Time `json:"linked_at"`
}

type Account struct {
	ID                    string       `json:"id"`
	EmailHash             string       `json:"email_hash,omitempty"`
	EmailEncrypted        string       `json:"email_encrypted,omitempty"`
	TelegramSubjectHash   string       `json:"telegram_subject_hash,omitempty"`
	TelegramNameEncrypted string       `json:"telegram_name_encrypted,omitempty"`
	Wallets               []WalletLink `json:"wallets,omitempty"`
	CreatedAt             time.Time    `json:"created_at"`
	LastLoginAt           time.Time    `json:"last_login_at"`
}

type sessionRecord struct {
	TokenHash string    `json:"token_hash"`
	AccountID string    `json:"account_id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type SessionView struct {
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Current   bool      `json:"current"`
}

type magicLinkRecord struct {
	TokenHash      string    `json:"token_hash"`
	EmailHash      string    `json:"email_hash"`
	EmailEncrypted string    `json:"email_encrypted"`
	AccountID      string    `json:"account_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type storeDocument struct {
	Version    int               `json:"version"`
	Accounts   []Account         `json:"accounts"`
	Sessions   []sessionRecord   `json:"sessions"`
	MagicLinks []magicLinkRecord `json:"magic_links"`
}

type Store struct {
	mu         sync.RWMutex
	path       string
	key        []byte
	accounts   map[string]*Account
	sessions   map[string]sessionRecord
	magicLinks map[string]magicLinkRecord
}

func OpenStore(path string, key []byte) (*Store, error) {
	if path == "" {
		return nil, errors.New("account store path is required")
	}
	if len(key) != 32 {
		return nil, errors.New("account store key must be 32 bytes")
	}
	s := &Store{
		path:       path,
		key:        append([]byte(nil), key...),
		accounts:   make(map[string]*Account),
		sessions:   make(map[string]sessionRecord),
		magicLinks: make(map[string]magicLinkRecord),
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("create account store directory: %w", err)
		}
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- path is trusted service configuration.
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read account store: %w", err)
	}
	if len(raw) == 0 {
		return s, nil
	}
	var doc storeDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode account store: %w", err)
	}
	if doc.Version != storeVersion {
		return nil, fmt.Errorf("unsupported account store version %d", doc.Version)
	}
	for i := range doc.Accounts {
		account := doc.Accounts[i]
		if account.ID != "" {
			copyOfAccount := account
			s.accounts[account.ID] = &copyOfAccount
		}
	}
	for _, session := range doc.Sessions {
		if session.TokenHash != "" {
			s.sessions[session.TokenHash] = session
		}
	}
	for _, link := range doc.MagicLinks {
		if link.TokenHash != "" {
			s.magicLinks[link.TokenHash] = link
		}
	}
	s.cleanupLocked(time.Now())
	return s, nil
}

func (s *Store) saveLocked() error {
	doc := storeDocument{Version: storeVersion}
	for _, account := range s.accounts {
		doc.Accounts = append(doc.Accounts, *account)
	}
	for _, session := range s.sessions {
		doc.Sessions = append(doc.Sessions, session)
	}
	for _, link := range s.magicLinks {
		doc.MagicLinks = append(doc.MagicLinks, link)
	}
	sort.Slice(doc.Accounts, func(i, j int) bool { return doc.Accounts[i].ID < doc.Accounts[j].ID })
	sort.Slice(doc.Sessions, func(i, j int) bool { return doc.Sessions[i].TokenHash < doc.Sessions[j].TokenHash })
	sort.Slice(doc.MagicLinks, func(i, j int) bool { return doc.MagicLinks[i].TokenHash < doc.MagicLinks[j].TokenHash })
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode account store: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("write account store: %w", err)
	}
	_ = os.Chmod(tmp, 0o600)
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(s.path)
		if retryErr := os.Rename(tmp, s.path); retryErr != nil {
			return fmt.Errorf("replace account store: %w", retryErr)
		}
	}
	return nil
}

func (s *Store) cleanupLocked(now time.Time) {
	for hash, session := range s.sessions {
		if !session.ExpiresAt.After(now) {
			delete(s.sessions, hash)
		}
	}
	for hash, link := range s.magicLinks {
		if !link.ExpiresAt.After(now) {
			delete(s.magicLinks, hash)
		}
	}
}

func (s *Store) CreateMagicLink(email string, ttl time.Duration) (string, error) {
	return s.createMagicLink("", email, ttl)
}

func (s *Store) CreateIdentityMagicLink(accountID, email string, ttl time.Duration) (string, error) {
	if accountID == "" {
		return "", errors.New("account is required")
	}
	return s.createMagicLink(accountID, email, ttl)
}

func (s *Store) createMagicLink(accountID, email string, ttl time.Duration) (string, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	encrypted, err := encryptString(s.key, "qsdm-account-email-v1", email)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	record := magicLinkRecord{
		TokenHash:      keyedHash(s.key, "magic-link", token),
		EmailHash:      keyedHash(s.key, "email", email),
		EmailEncrypted: encrypted,
		AccountID:      accountID,
		CreatedAt:      now,
		ExpiresAt:      now.Add(ttl),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if accountID != "" {
		account := s.accounts[accountID]
		if account == nil {
			return "", errors.New("account not found")
		}
		if account.EmailHash != "" {
			return "", ErrIdentityAlreadySet
		}
	}
	s.cleanupLocked(now)
	s.magicLinks[record.TokenHash] = record
	if err := s.saveLocked(); err != nil {
		delete(s.magicLinks, record.TokenHash)
		return "", err
	}
	return token, nil
}

func (s *Store) ConsumeMagicLink(token string) (*Account, error) {
	hash := keyedHash(s.key, "magic-link", token)
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.magicLinks[hash]
	if !ok || !record.ExpiresAt.After(now) {
		delete(s.magicLinks, hash)
		return nil, errors.New("magic link is invalid or expired")
	}
	delete(s.magicLinks, hash)
	var identityOwner *Account
	for _, candidate := range s.accounts {
		if subtle.ConstantTimeCompare([]byte(candidate.EmailHash), []byte(record.EmailHash)) == 1 {
			identityOwner = candidate
			break
		}
	}
	if record.AccountID != "" {
		account := s.accounts[record.AccountID]
		if account == nil {
			if err := s.saveLocked(); err != nil {
				s.magicLinks[hash] = record
				return nil, err
			}
			return nil, errors.New("magic link account no longer exists")
		}
		if identityOwner != nil && identityOwner.ID != account.ID {
			if err := s.saveLocked(); err != nil {
				s.magicLinks[hash] = record
				return nil, err
			}
			return nil, ErrIdentityInUse
		}
		if account.EmailHash != "" && subtle.ConstantTimeCompare([]byte(account.EmailHash), []byte(record.EmailHash)) != 1 {
			if err := s.saveLocked(); err != nil {
				s.magicLinks[hash] = record
				return nil, err
			}
			return nil, ErrIdentityAlreadySet
		}
		previous := cloneAccount(account)
		account.EmailHash = record.EmailHash
		account.EmailEncrypted = record.EmailEncrypted
		account.LastLoginAt = now
		if err := s.saveLocked(); err != nil {
			*account = *previous
			s.magicLinks[hash] = record
			return nil, err
		}
		return cloneAccount(account), nil
	}

	account := identityOwner
	created := false
	var previous *Account
	if account == nil {
		id, err := randomToken(18)
		if err != nil {
			s.magicLinks[hash] = record
			return nil, err
		}
		account = &Account{
			ID:             "acct_" + id,
			EmailHash:      record.EmailHash,
			EmailEncrypted: record.EmailEncrypted,
			CreatedAt:      now,
		}
		s.accounts[account.ID] = account
		created = true
	} else {
		previous = cloneAccount(account)
	}
	account.LastLoginAt = now
	if err := s.saveLocked(); err != nil {
		if created {
			delete(s.accounts, account.ID)
		} else {
			*account = *previous
		}
		s.magicLinks[hash] = record
		return nil, err
	}
	return cloneAccount(account), nil
}

func (s *Store) DeleteMagicLink(token string) error {
	hash := keyedHash(s.key, "magic-link", token)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.magicLinks, hash)
	return s.saveLocked()
}

func (s *Store) FindOrCreateTelegram(subject, displayName string) (*Account, error) {
	hash := keyedHash(s.key, "telegram-subject", subject)
	nameEncrypted, err := encryptString(s.key, "qsdm-account-telegram-name-v1", displayName)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	var account *Account
	for _, candidate := range s.accounts {
		if subtle.ConstantTimeCompare([]byte(candidate.TelegramSubjectHash), []byte(hash)) == 1 {
			account = candidate
			break
		}
	}
	created := false
	var previous *Account
	if account == nil {
		id, err := randomToken(18)
		if err != nil {
			return nil, err
		}
		account = &Account{
			ID:                    "acct_" + id,
			TelegramSubjectHash:   hash,
			TelegramNameEncrypted: nameEncrypted,
			CreatedAt:             now,
		}
		s.accounts[account.ID] = account
		created = true
	} else {
		previous = cloneAccount(account)
		account.TelegramNameEncrypted = nameEncrypted
	}
	account.LastLoginAt = now
	if err := s.saveLocked(); err != nil {
		if created {
			delete(s.accounts, account.ID)
		} else {
			*account = *previous
		}
		return nil, err
	}
	return cloneAccount(account), nil
}

func (s *Store) LinkTelegramIdentity(accountID, subject, displayName string) (*Account, error) {
	hash := keyedHash(s.key, "telegram-subject", subject)
	nameEncrypted, err := encryptString(s.key, "qsdm-account-telegram-name-v1", displayName)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	account := s.accounts[accountID]
	if account == nil {
		return nil, errors.New("account not found")
	}
	for _, candidate := range s.accounts {
		if candidate.TelegramSubjectHash == "" {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(candidate.TelegramSubjectHash), []byte(hash)) == 1 && candidate.ID != accountID {
			return nil, ErrIdentityInUse
		}
	}
	if account.TelegramSubjectHash != "" && subtle.ConstantTimeCompare([]byte(account.TelegramSubjectHash), []byte(hash)) != 1 {
		return nil, ErrIdentityAlreadySet
	}
	previous := cloneAccount(account)
	account.TelegramSubjectHash = hash
	account.TelegramNameEncrypted = nameEncrypted
	account.LastLoginAt = now
	if err := s.saveLocked(); err != nil {
		*account = *previous
		return nil, err
	}
	return cloneAccount(account), nil
}

func (s *Store) CreateSession(accountID string, ttl time.Duration) (string, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	record := sessionRecord{
		TokenHash: keyedHash(s.key, "session", token),
		AccountID: accountID,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[accountID]; !ok {
		return "", errors.New("account not found")
	}
	s.cleanupLocked(now)
	s.sessions[record.TokenHash] = record
	if err := s.saveLocked(); err != nil {
		delete(s.sessions, record.TokenHash)
		return "", err
	}
	return token, nil
}

func (s *Store) AccountForSession(token string) (*Account, string, error) {
	hash := keyedHash(s.key, "session", token)
	now := time.Now().UTC()
	s.mu.RLock()
	record, ok := s.sessions[hash]
	if !ok || !record.ExpiresAt.After(now) {
		s.mu.RUnlock()
		return nil, "", errors.New("session is invalid or expired")
	}
	account := cloneAccount(s.accounts[record.AccountID])
	s.mu.RUnlock()
	if account == nil {
		return nil, "", errors.New("session account not found")
	}
	csrf := keyedHash(s.key, "csrf", hash)
	return account, csrf, nil
}

func (s *Store) DeleteSession(token string) error {
	hash := keyedHash(s.key, "session", token)
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.sessions[hash]
	if !ok {
		return nil
	}
	delete(s.sessions, hash)
	if err := s.saveLocked(); err != nil {
		s.sessions[hash] = record
		return err
	}
	return nil
}

func (s *Store) SessionsForAccount(accountID, currentToken string) ([]SessionView, error) {
	currentHash := keyedHash(s.key, "session", currentToken)
	now := time.Now().UTC()
	s.mu.RLock()
	current, ok := s.sessions[currentHash]
	if !ok || current.AccountID != accountID || !current.ExpiresAt.After(now) {
		s.mu.RUnlock()
		return nil, errors.New("session is invalid or expired")
	}
	views := make([]SessionView, 0)
	for hash, record := range s.sessions {
		if record.AccountID != accountID || !record.ExpiresAt.After(now) {
			continue
		}
		views = append(views, SessionView{
			CreatedAt: record.CreatedAt,
			ExpiresAt: record.ExpiresAt,
			Current:   subtle.ConstantTimeCompare([]byte(hash), []byte(currentHash)) == 1,
		})
	}
	s.mu.RUnlock()
	sort.SliceStable(views, func(i, j int) bool {
		if views[i].Current != views[j].Current {
			return views[i].Current
		}
		return views[i].CreatedAt.After(views[j].CreatedAt)
	})
	return views, nil
}

func (s *Store) RevokeOtherSessions(accountID, currentToken string) (int, error) {
	currentHash := keyedHash(s.key, "session", currentToken)
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.sessions[currentHash]
	if !ok || current.AccountID != accountID || !current.ExpiresAt.After(now) {
		return 0, errors.New("session is invalid or expired")
	}
	removed := make(map[string]sessionRecord)
	activeRevoked := 0
	for hash, record := range s.sessions {
		if record.AccountID != accountID || subtle.ConstantTimeCompare([]byte(hash), []byte(currentHash)) == 1 {
			continue
		}
		if record.ExpiresAt.After(now) {
			activeRevoked++
		}
		removed[hash] = record
		delete(s.sessions, hash)
	}
	if len(removed) == 0 {
		return 0, nil
	}
	if err := s.saveLocked(); err != nil {
		for hash, record := range removed {
			s.sessions[hash] = record
		}
		return 0, err
	}
	return activeRevoked, nil
}

func (s *Store) DeleteAccount(accountID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	account := s.accounts[accountID]
	if account == nil {
		return errors.New("account not found")
	}
	accountBackup := cloneAccount(account)
	removedSessions := make(map[string]sessionRecord)
	removedLinks := make(map[string]magicLinkRecord)
	delete(s.accounts, accountID)
	for hash, record := range s.sessions {
		if record.AccountID == accountID {
			removedSessions[hash] = record
			delete(s.sessions, hash)
		}
	}
	for hash, record := range s.magicLinks {
		sameEmail := account.EmailHash != "" && subtle.ConstantTimeCompare([]byte(record.EmailHash), []byte(account.EmailHash)) == 1
		if record.AccountID == accountID || sameEmail {
			removedLinks[hash] = record
			delete(s.magicLinks, hash)
		}
	}
	if err := s.saveLocked(); err != nil {
		s.accounts[accountID] = accountBackup
		for hash, record := range removedSessions {
			s.sessions[hash] = record
		}
		for hash, record := range removedLinks {
			s.magicLinks[hash] = record
		}
		return err
	}
	return nil
}

func (s *Store) LinkWallet(accountID, address string) error {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	account := s.accounts[accountID]
	if account == nil {
		return errors.New("account not found")
	}
	for _, candidate := range s.accounts {
		for _, wallet := range candidate.Wallets {
			if subtle.ConstantTimeCompare([]byte(wallet.Address), []byte(address)) == 1 {
				if candidate.ID == accountID {
					return nil
				}
				return errors.New("wallet is already linked to another account")
			}
		}
	}
	previous := append([]WalletLink(nil), account.Wallets...)
	account.Wallets = append(account.Wallets, WalletLink{Address: address, LinkedAt: now})
	if err := s.saveLocked(); err != nil {
		account.Wallets = previous
		return err
	}
	return nil
}

func (s *Store) UnlinkWallet(accountID, address string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	account := s.accounts[accountID]
	if account == nil {
		return false, errors.New("account not found")
	}
	index := -1
	for i, wallet := range account.Wallets {
		if subtle.ConstantTimeCompare([]byte(wallet.Address), []byte(address)) == 1 {
			index = i
			break
		}
	}
	if index < 0 {
		return false, nil
	}
	previous := append([]WalletLink(nil), account.Wallets...)
	account.Wallets = append(account.Wallets[:index], account.Wallets[index+1:]...)
	if err := s.saveLocked(); err != nil {
		account.Wallets = previous
		return false, err
	}
	return true, nil
}

func (s *Store) IdentityView(account *Account) (maskedEmail, telegramName string, err error) {
	if account == nil {
		return "", "", errors.New("account is required")
	}
	if account.EmailEncrypted != "" {
		email, decryptErr := decryptString(s.key, "qsdm-account-email-v1", account.EmailEncrypted)
		if decryptErr != nil {
			return "", "", decryptErr
		}
		maskedEmail = maskEmail(email)
	}
	if account.TelegramNameEncrypted != "" {
		telegramName, err = decryptString(s.key, "qsdm-account-telegram-name-v1", account.TelegramNameEncrypted)
	}
	return maskedEmail, telegramName, err
}

func cloneAccount(account *Account) *Account {
	if account == nil {
		return nil
	}
	copyOfAccount := *account
	copyOfAccount.Wallets = append([]WalletLink(nil), account.Wallets...)
	return &copyOfAccount
}
