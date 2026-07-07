package main

// Peer-signer registration: lets a validator accept v2
// challenge signatures issued by REMOTE attesters (cmd/qsdm-attester
// instances) in addition to its own self-issued signer key.
//
// The on-disk format is a single TOML file. Operators add /
// remove peers by editing the file and restarting the validator
// (a future hot-reload is feasible but not necessary while the
// peer set changes rarely).
//
// File shape (peer_signers.toml):
//
//	# Each [[peer]] block whitelists one remote attester.
//	[[peer]]
//	signer_id = "attester-abc123def4567890"
//	key_hex   = "deadbeef..."          # 64 hex chars = 32 bytes
//	note      = "blackbeard's home 3050 (Manila, Ampere)"
//
//	[[peer]]
//	signer_id = "attester-otheroperator"
//	key_hex   = "..."
//
// The validator's existing pkg/mining/challenge.HMACSignerVerifier
// already supports a registry of signer-IDs (it is the same
// machinery that registers the validator's own key). This file
// is the operator-facing edge that converts the on-disk
// allowlist into Register() calls — no consensus surface is
// touched.

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
)

// PeerSignersFile is the TOML root object. Held in its own
// type (not anonymous) so a future migration to NDJSON or
// per-file-per-peer can be done without rippling through call
// sites.
type PeerSignersFile struct {
	Peer []PeerSigner `toml:"peer"`
}

// PeerSigner is one allowlist entry. Note is operator-facing
// only — never consumed by the verifier.
type PeerSigner struct {
	SignerID string `toml:"signer_id"`
	KeyHex   string `toml:"key_hex"`
	Note     string `toml:"note"`
}

// LoadPeerSignersFile reads and parses path. Returns the
// (possibly empty) slice of peers. A non-existent path is NOT
// an error — it returns an empty slice so a validator with no
// remote attesters configured boots cleanly.
func LoadPeerSignersFile(path string) ([]PeerSigner, error) {
	if path == "" {
		return nil, nil
	}
	if _, err := os.Stat(path); err != nil { // #nosec G703 -- operator-supplied startup configuration path, never request input.
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("peer-signers: stat %s: %w", path, err)
	}
	raw, err := os.ReadFile(path) // #nosec G304,G703 -- operator-supplied startup configuration path, never request input.
	if err != nil {
		return nil, fmt.Errorf("peer-signers: read %s: %w", path, err)
	}
	var f PeerSignersFile
	if _, err := toml.Decode(string(raw), &f); err != nil {
		return nil, fmt.Errorf("peer-signers: decode %s: %w", path, err)
	}
	return f.Peer, nil
}

// RegisterPeerSigners installs each peer into the supplied
// HMACSignerVerifier. Returns the count of peers successfully
// registered and a slice of (signer_id, error) pairs for any
// that failed (e.g. malformed key_hex, duplicate signer_id, or
// signer_id collision with the validator's own key). The
// caller decides whether a partial failure is fatal — the
// canonical wiring in cmd/qsdm/main.go fatals on any error
// because a typo in peer_signers.toml that causes a silent
// allowlist drop is worse than a loud boot failure.
//
// Skipped duplicates are returned in registered=0 territory
// because pkg/mining/challenge.HMACSignerVerifier.Register
// returns an error on duplicate registration. We surface that
// as a per-peer error rather than swallowing.
func RegisterPeerSigners(
	verifier *challenge.HMACSignerVerifier,
	peers []PeerSigner,
) (registered int, errs []PeerSignerError) {
	if verifier == nil {
		return 0, []PeerSignerError{{Err: errors.New("nil verifier")}}
	}
	for _, p := range peers {
		id := strings.TrimSpace(p.SignerID)
		if id == "" {
			errs = append(errs, PeerSignerError{
				PeerSigner: p,
				Err:        errors.New("empty signer_id"),
			})
			continue
		}
		keyHex := strings.TrimSpace(p.KeyHex)
		if keyHex == "" {
			errs = append(errs, PeerSignerError{
				PeerSigner: p,
				Err:        fmt.Errorf("peer %q: empty key_hex", id),
			})
			continue
		}
		key, err := hex.DecodeString(keyHex)
		if err != nil {
			errs = append(errs, PeerSignerError{
				PeerSigner: p,
				Err:        fmt.Errorf("peer %q: decode key_hex: %w", id, err),
			})
			continue
		}
		if regErr := verifier.Register(id, key); regErr != nil {
			errs = append(errs, PeerSignerError{
				PeerSigner: p,
				Err:        fmt.Errorf("peer %q: %w", id, regErr),
			})
			continue
		}
		registered++
	}
	return registered, errs
}

// PeerSignerError pairs a failed peer entry with its reason.
// The struct is exported so a future operator dashboard can
// render the full "what was rejected" report; today only
// cmd/qsdm/main.go consumes it.
type PeerSignerError struct {
	PeerSigner PeerSigner
	Err        error
}

func (e PeerSignerError) Error() string {
	if e.PeerSigner.SignerID == "" {
		return fmt.Sprintf("peer-signer: %v", e.Err)
	}
	return fmt.Sprintf("peer-signer %q: %v", e.PeerSigner.SignerID, e.Err)
}

// Unwrap exposes the inner error for errors.Is / errors.As.
func (e PeerSignerError) Unwrap() error { return e.Err }
