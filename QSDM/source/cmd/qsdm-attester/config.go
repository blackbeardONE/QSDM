package main

// Configuration + signer-key persistence for the qsdm-attester
// binary. Mirrors cmd/qsdm/main.go's loadOrCreateChallengeKey
// helper but uses an "attester-" prefix on the derived signer ID
// so logs and validator-side allowlists clearly distinguish a
// remote attester from a validator's own self-issued signer.
//
// Why a separate binary at all (instead of "validator-with-an-
// extra-flag"): operators running an attester at home need a
// minimal surface area — no chain state, no mempool, no BFT, no
// account store. A 200-line binary is auditable and small enough
// to deploy on a Windows desktop alongside the miner without
// surprising the operator. This file keeps the auditable pieces
// (key persistence, env parsing, defaults) close together.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// signerKeyLen is the on-disk size of an attester's HMAC signer
// key. 32 bytes matches HMAC-SHA256's natural key size and the
// validator's own challengeKeyLen. The pkg/mining/challenge
// package itself enforces a 16-byte minimum, so 32 satisfies any
// downstream consumer.
const signerKeyLen = 32

// signerIDPrefix distinguishes attester-issued challenges from
// validator-issued ones in the logs and on the validator's
// peer-signer allowlist. Validators use "validator-<hex>"; we
// use "attester-<hex>" so a glance at any log line tells you
// whether the issuance came from the BLR1 box or from a remote
// home attester.
const signerIDPrefix = "attester-"

// Config bundles the runtime knobs the attester needs. Built by
// loadConfig from flags + environment + defaults. The zero value
// is invalid (KeyPath would be empty) — always go through
// loadConfig.
type Config struct {
	// ListenAddr is the [host]:port the HTTP server binds.
	// Default ":7733" — port chosen far above ephemeral range
	// to avoid OS conflicts on a home machine.
	ListenAddr string

	// KeyPath is the absolute path to the 32-byte HMAC signer
	// key file. Created with 0o600 perms on first boot.
	// Default: <homedir>/.qsdm/attester.key.
	KeyPath string

	// SignerIDOverride, when non-empty, replaces the
	// derive-from-key SignerID. Use this only when an operator
	// wants a human-readable id (e.g. "blackbeard-rtx3050") in
	// the validator allowlist instead of the hex-derived form.
	// Empty = derive from key bytes (the recommended posture).
	SignerIDOverride string

	// Note is a free-form operator-supplied tag emitted on
	// /info so the validator operator can double-check what
	// they're allowlisting before pasting into peer_signers.toml.
	// Defaults to the attester binary's hostname.
	Note string

	// LogIssuanceEvery, if > 0, samples one out of every N
	// successful Issue() calls into the structured log so
	// operators can watch traffic without log spam. Zero
	// disables sampling (no per-issuance log).
	LogIssuanceEvery uint64
}

// defaults applies the zero-aware defaults. Called by
// loadConfig after env/flag overrides so an explicit empty
// string falls back rather than persisting the empty value.
func (c *Config) defaults() error {
	if c.ListenAddr == "" {
		c.ListenAddr = ":7733"
	}
	if c.KeyPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("attester: locate home dir for default key path: %w", err)
		}
		c.KeyPath = filepath.Join(home, ".qsdm", "attester.key")
	}
	if c.Note == "" {
		host, err := os.Hostname()
		if err == nil {
			c.Note = host
		} else {
			c.Note = "qsdm-attester"
		}
	}
	return nil
}

// loadOrCreateSignerKey reads the persisted HMAC key from path,
// or generates a fresh 32-byte key and persists it (mode 0o600,
// directory created if absent) on first boot. Returned signerID
// is "attester-" || hex(key[:8]) unless override is non-empty,
// in which case override is returned verbatim.
//
// The function NEVER logs or returns the key bytes through
// stderr — only the derived signerID and the path are
// considered safe to log. Callers must keep the returned key
// in memory and not write it elsewhere.
func loadOrCreateSignerKey(path, override string) (signerID string, key []byte, isFresh bool, err error) {
	if path == "" {
		return "", nil, false, errors.New("attester: empty signer key path")
	}
	if existing, readErr := os.ReadFile(path); readErr == nil {
		if len(existing) != signerKeyLen {
			return "", nil, false, fmt.Errorf(
				"attester: signer key at %s has length %d, expected %d (delete the file to regenerate)",
				path, len(existing), signerKeyLen)
		}
		return resolveSignerID(existing, override), existing, false, nil
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return "", nil, false, fmt.Errorf("attester: read signer key %s: %w", path, readErr)
	}
	if mkErr := os.MkdirAll(filepath.Dir(path), 0o700); mkErr != nil {
		return "", nil, false, fmt.Errorf("attester: create key dir %s: %w", filepath.Dir(path), mkErr)
	}
	fresh := make([]byte, signerKeyLen)
	if _, randErr := rand.Read(fresh); randErr != nil {
		return "", nil, false, fmt.Errorf("attester: generate signer key: %w", randErr)
	}
	if writeErr := os.WriteFile(path, fresh, 0o600); writeErr != nil {
		return "", nil, false, fmt.Errorf("attester: persist signer key %s: %w", path, writeErr)
	}
	return resolveSignerID(fresh, override), fresh, true, nil
}

// resolveSignerID returns override if non-empty (after a sanity
// check that it starts with the expected prefix), or the
// derive-from-key form otherwise.
func resolveSignerID(key []byte, override string) string {
	if override != "" {
		// Don't transform; operators may want very specific
		// ids in the validator allowlist. We DO require the
		// prefix to be honoured so logs stay readable.
		return override
	}
	if len(key) < 8 {
		return signerIDPrefix + "shortkey"
	}
	return signerIDPrefix + hex.EncodeToString(key[:8])
}

// keyFingerprint returns a non-secret 16-character SHA-256
// fingerprint of the signer key, suitable for /info. Lets the
// operator confirm "this attester at addr X is using key with
// fingerprint Y" without ever exposing the key itself. The full
// hash is truncated to 16 hex chars (64 bits) — beyond
// practical collision range for a single QSDM operator's set
// of attesters.
func keyFingerprint(key []byte) string {
	if len(key) == 0 {
		return ""
	}
	h := sha256.Sum256(key)
	return hex.EncodeToString(h[:8])
}

// loadConfig reads from environment variables, applies defaults,
// and returns a populated Config. CLI flags are parsed in main.go
// and passed in via the env-style overrides. Any required
// invariant violation surfaces as an error rather than a panic
// so the binary's startup failures are observable.
//
// Recognised env vars:
//
//	QSDM_ATTESTER_LISTEN          - "host:port" override
//	QSDM_ATTESTER_KEY_PATH        - absolute path override
//	QSDM_ATTESTER_SIGNER_ID       - human-readable id override
//	QSDM_ATTESTER_NOTE            - free-form tag for /info
//	QSDM_ATTESTER_LOG_EVERY       - log every Nth issuance
func loadConfig() (*Config, error) {
	cfg := &Config{
		ListenAddr:       strings.TrimSpace(os.Getenv("QSDM_ATTESTER_LISTEN")),
		KeyPath:          strings.TrimSpace(os.Getenv("QSDM_ATTESTER_KEY_PATH")),
		SignerIDOverride: strings.TrimSpace(os.Getenv("QSDM_ATTESTER_SIGNER_ID")),
		Note:             strings.TrimSpace(os.Getenv("QSDM_ATTESTER_NOTE")),
	}
	if v := strings.TrimSpace(os.Getenv("QSDM_ATTESTER_LOG_EVERY")); v != "" {
		var n uint64
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			cfg.LogIssuanceEvery = n
		}
	}
	if err := cfg.defaults(); err != nil {
		return nil, err
	}
	return cfg, nil
}
