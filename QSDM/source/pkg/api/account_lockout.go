package api

import (
	"fmt"
	"sync"
	"time"
)

// AccountLockoutManager manages account lockout after failed login attempts
type AccountLockoutManager struct {
	failedAttempts map[string]*lockoutInfo
	mu             sync.RWMutex
	maxAttempts    int           // Maximum failed attempts before lockout
	lockoutDuration time.Duration // How long to lock account
	windowDuration  time.Duration // Time window for counting attempts
}

type lockoutInfo struct {
	attempts    int
	lastAttempt time.Time
	lockedUntil *time.Time
}

// NewAccountLockoutManager creates a new account lockout manager
func NewAccountLockoutManager() *AccountLockoutManager {
	return &AccountLockoutManager{
		failedAttempts: make(map[string]*lockoutInfo),
		maxAttempts:     5,                    // Lock after 5 failed attempts
		lockoutDuration: 15 * time.Minute,    // Lock for 15 minutes
		windowDuration:  15 * time.Minute,    // Count attempts within 15 minutes
	}
}

// RecordFailedAttempt records a failed login attempt
func (alm *AccountLockoutManager) RecordFailedAttempt(identifier string) {
	alm.mu.Lock()
	defer alm.mu.Unlock()

	now := time.Now()
	info, exists := alm.failedAttempts[identifier]

	if !exists {
		// First failed attempt
		alm.failedAttempts[identifier] = &lockoutInfo{
			attempts:    1,
			lastAttempt: now,
		}
		return
	}

	// Check if lockout period has expired
	if info.lockedUntil != nil && now.After(*info.lockedUntil) {
		// Reset after lockout expires
		info.attempts = 1
		info.lastAttempt = now
		info.lockedUntil = nil
		return
	}

	// Check if attempt window has expired
	if now.Sub(info.lastAttempt) > alm.windowDuration {
		// Reset counter
		info.attempts = 1
		info.lastAttempt = now
		return
	}

	// Increment attempts
	info.attempts++
	info.lastAttempt = now

	// Lock account if max attempts reached
	if info.attempts >= alm.maxAttempts {
		lockedUntil := now.Add(alm.lockoutDuration)
		info.lockedUntil = &lockedUntil
	}
}

// RecordSuccessfulAttempt clears failed attempts after successful login
func (alm *AccountLockoutManager) RecordSuccessfulAttempt(identifier string) {
	alm.mu.Lock()
	defer alm.mu.Unlock()

	// Remove from failed attempts
	delete(alm.failedAttempts, identifier)
}

// IsLocked checks if an account is currently locked
func (alm *AccountLockoutManager) IsLocked(identifier string) (bool, error) {
	alm.mu.RLock()
	defer alm.mu.RUnlock()

	info, exists := alm.failedAttempts[identifier]
	if !exists {
		return false, nil
	}

	// Check if locked
	if info.lockedUntil == nil {
		return false, nil
	}

	// Check if lockout has expired
	if time.Now().After(*info.lockedUntil) {
		// Lockout expired, but don't clear here (will be cleared on next attempt)
		return false, nil
	}

	remaining := info.lockedUntil.Sub(time.Now())
	return true, fmt.Errorf("account locked due to too many failed login attempts. Try again in %v", remaining.Round(time.Second))
}

// GetRemainingAttempts returns the number of remaining attempts before lockout
func (alm *AccountLockoutManager) GetRemainingAttempts(identifier string) int {
	alm.mu.RLock()
	defer alm.mu.RUnlock()

	info, exists := alm.failedAttempts[identifier]
	if !exists {
		return alm.maxAttempts
	}

	// Check if locked
	if info.lockedUntil != nil && time.Now().Before(*info.lockedUntil) {
		return 0
	}

	// Check if window expired
	if time.Now().Sub(info.lastAttempt) > alm.windowDuration {
		return alm.maxAttempts
	}

	remaining := alm.maxAttempts - info.attempts
	if remaining < 0 {
		return 0
	}
	return remaining
}

// GetLockoutInfo returns lockout information for an identifier
func (alm *AccountLockoutManager) GetLockoutInfo(identifier string) (attempts int, lockedUntil *time.Time, err error) {
	alm.mu.RLock()
	defer alm.mu.RUnlock()

	info, exists := alm.failedAttempts[identifier]
	if !exists {
		return 0, nil, nil
	}

	attempts = info.attempts
	lockedUntil = info.lockedUntil

	// Check if lockout expired
	if lockedUntil != nil && time.Now().After(*lockedUntil) {
		lockedUntil = nil
		attempts = 0
	}

	return attempts, lockedUntil, nil
}

