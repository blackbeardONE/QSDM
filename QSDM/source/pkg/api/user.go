package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

// User represents a registered user
type User struct {
	Address    string
	PasswordHash string
	Role       string
	CreatedAt  time.Time
}

// UserStore manages user storage and authentication. When persistPath is
// non-empty, every mutation is flushed to disk via saveLocked (defined in
// user_persist.go). When empty, the store is in-memory only — this shape
// stays reserved for tests that want fresh state per run.
type UserStore struct {
	users       map[string]*User
	mu          sync.RWMutex
	persistPath string
}

// NewUserStore creates a new, in-memory user store. Production callers
// should prefer LoadOrNewUserStore(path) so that registered users
// survive a service restart; otherwise every redeploy wipes the auth
// surface (see the 2026-04-23 account-wipe incident documented in
// docs/docs/USER_STORE_PERSISTENCE.md).
func NewUserStore() *UserStore {
	return &UserStore{
		users: make(map[string]*User),
	}
}

// HashPassword hashes a password using Argon2id (memory-hard, secure)
func HashPassword(password string) (string, error) {
	// Generate a random salt
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("failed to generate salt: %w", err)
	}

	// Argon2id parameters (memory: 64MB, time: 3, threads: 4, key length: 32)
	hash := argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32)

	// Encode: base64(salt) + ":" + base64(hash)
	saltB64 := base64.RawStdEncoding.EncodeToString(salt)
	hashB64 := base64.RawStdEncoding.EncodeToString(hash)
	
	return fmt.Sprintf("%s:%s", saltB64, hashB64), nil
}

// VerifyPassword verifies a password against a hash
func VerifyPassword(password, hash string) (bool, error) {
	// Split salt and hash
	parts := splitString(hash, ":")
	if len(parts) != 2 {
		return false, errors.New("invalid hash format")
	}

	saltB64, hashB64 := parts[0], parts[1]

	// Decode salt
	salt, err := base64.RawStdEncoding.DecodeString(saltB64)
	if err != nil {
		return false, fmt.Errorf("failed to decode salt: %w", err)
	}

	// Decode stored hash
	storedHash, err := base64.RawStdEncoding.DecodeString(hashB64)
	if err != nil {
		return false, fmt.Errorf("failed to decode hash: %w", err)
	}

	// Compute hash with same parameters
	computedHash := argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32)

	// Constant-time comparison
	return subtle.ConstantTimeCompare(storedHash, computedHash) == 1, nil
}

// RegisterUser registers a new user. When persistence is configured
// (persistPath != ""), the resulting account is flushed to disk via an
// atomic temp-file-rename before the call returns. A disk failure rolls
// the in-memory map back so callers never observe a "registered but not
// persisted" half-state — that shape is the whole point of the
// persistence fix.
func (us *UserStore) RegisterUser(address, password, role string) error {
	us.mu.Lock()
	defer us.mu.Unlock()

	if _, exists := us.users[address]; exists {
		return errors.New("user already exists")
	}

	passwordHash, err := HashPassword(password)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	us.users[address] = &User{
		Address:      address,
		PasswordHash: passwordHash,
		Role:         role,
		CreatedAt:    time.Now(),
	}

	if err := us.saveLocked(); err != nil {
		delete(us.users, address)
		return fmt.Errorf("user store persist: %w", err)
	}

	return nil
}

// AuthenticateUser authenticates a user and returns the user if successful
func (us *UserStore) AuthenticateUser(address, password string) (*User, error) {
	us.mu.RLock()
	defer us.mu.RUnlock()

	user, exists := us.users[address]
	if !exists {
		return nil, errors.New("user not found")
	}

	// Verify password
	valid, err := VerifyPassword(password, user.PasswordHash)
	if err != nil {
		return nil, fmt.Errorf("failed to verify password: %w", err)
	}
	if !valid {
		return nil, errors.New("invalid password")
	}

	return user, nil
}

// Count returns the number of users currently loaded into the store.
// Useful for startup logs and admin diagnostics.
func (us *UserStore) Count() int {
	us.mu.RLock()
	defer us.mu.RUnlock()
	return len(us.users)
}

// GetUser retrieves a user by address
func (us *UserStore) GetUser(address string) (*User, error) {
	us.mu.RLock()
	defer us.mu.RUnlock()

	user, exists := us.users[address]
	if !exists {
		return nil, errors.New("user not found")
	}

	return user, nil
}

// Helper function to split string
func splitString(s, sep string) []string {
	parts := []string{}
	current := ""
	for _, char := range s {
		if string(char) == sep {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else {
			current += string(char)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

