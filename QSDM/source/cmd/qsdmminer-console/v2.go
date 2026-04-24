package main

// v2.go: opt-in NVIDIA-locked (v2) mining path.
//
// When the operator passes --protocol=v2 (or protocol="v2" in
// miner.toml), this file's helpers slot into the existing
// mining loop between mining.Solve and submitProof:
//
//  1. Before Solve, or at loop start, fetch a fresh challenge
//     from the validator via v2client.FetchChallenge.
//  2. After Solve returns a proof, build an HMAC attestation
//     bundle via v2client.BuildHMACAttestation using the
//     proof's batch_root + mix_digest + miner_addr and the
//     challenge we just fetched.
//  3. Attach the attestation to the proof (bumps Version to
//     mining.ProtocolVersionV2) before encoding + submitting.
//
// Config discipline:
//
//   - The v2 code path is OFF by default. Any operator who has
//     not set protocol="v2" (in config or via --protocol) runs
//     the unchanged v1 path. This keeps CI self-tests green
//     and keeps testnet replay working until the fork
//     activates.
//
//   - v1 → v2 is strictly opt-in. There is NO auto-upgrade
//     behaviour based on validator advertising; operators must
//     actively enable v2 once their enrollment has landed.
//
//   - The HMAC key is loaded from a file (--hmac-key-path) at
//     startup. It never leaves memory and is not logged. A
//     misconfigured path yields a clean startup error, not a
//     silent v1 fallback, to prevent operators accidentally
//     mining v1 on a validator that has already forked.
//
// See also: pkg/mining/v2client (the shared client surface
// every miner binary uses for challenge fetch + bundle build)
// and pkg/mining/enrollment (the on-chain registry model the
// operator's node_id / gpu_uuid must be registered against for
// the validator to accept the bundle).

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/v2client"
)

// V2Context bundles every v2-specific input the runLoop needs.
// Constructed once in main() from the merged (config + flags)
// state and passed immutably into the loop. Zero value = "v2
// not enabled"; callers check IsEnabled() before invoking any
// of the v2 path.
type V2Context struct {
	// Enabled mirrors "protocol == v2" after flag/config
	// resolution. Kept as an explicit field rather than a
	// method on an implicit protocol string so tests don't
	// have to synthesise a whole config just to toggle.
	Enabled bool

	// NodeID is the operator's enrolled handle. MUST match the
	// NodeID recorded in the on-chain enrollment; otherwise
	// the verifier's hmac.Registry lookup fails with
	// ErrNodeNotRegistered.
	NodeID string

	// GPUUUID is the nvidia-smi UUID, used by the verifier's
	// step-5 check to enforce one-operator-key-per-GPU. MUST
	// match the GPUUUID recorded at enrollment.
	GPUUUID string

	// GPUName is the human-readable GPU string (e.g. "NVIDIA
	// GeForce RTX 4090"). Checked against the deny-list at
	// step 7. Does NOT have to match enrollment exactly —
	// nvidia-smi may rephrase the string across driver
	// versions — but governance-appended deny strings will
	// reject known-bad substrings.
	GPUName string

	// GPUArch is the lowercase arch tag ("ada", "ampere",
	// "hopper", "blackwell"). Populates Attestation.GPUArch
	// and will feed the Tensor-Core PoW mixin cross-check in
	// Phase 2c-iv. Empty string tolerated today.
	GPUArch string

	// Metadata — all purely self-reported, all covered by the
	// HMAC so they cannot be mutated post-sign. Not consensus-
	// critical today; carried so the transparency API can
	// display them.
	ComputeCap  string
	CUDAVersion string
	DriverVer   string

	// HMACKey is the 32-to-128-byte symmetric key the operator
	// registered at enrollment. Held in memory; NEVER logged.
	// A V2Context with Enabled=true but len(HMACKey)==0 is
	// invalid and LoadV2Context refuses to build one.
	HMACKey []byte
}

// IsEnabled is a nil-safe predicate for use in the runLoop.
func (c *V2Context) IsEnabled() bool { return c != nil && c.Enabled }

// LoadV2Context resolves the v2-mode config into a V2Context
// suitable for the runLoop. Returns a disabled context (no
// error) when protocol != "v2"; returns an actionable error if
// protocol == "v2" but any required field is missing or
// invalid.
//
// Validation happens eagerly at startup rather than inside the
// loop so operators get a clear error BEFORE the first mining
// attempt rather than after a solve cycle is wasted.
func LoadV2Context(cfg V2Config) (*V2Context, error) {
	if !strings.EqualFold(cfg.Protocol, "v2") {
		return &V2Context{Enabled: false}, nil
	}

	if cfg.NodeID == "" {
		return nil, errors.New("v2: protocol=v2 requires --node-id (or node_id in config)")
	}
	if cfg.GPUUUID == "" {
		return nil, errors.New("v2: protocol=v2 requires --gpu-uuid (or gpu_uuid in config)")
	}
	if cfg.HMACKeyPath == "" {
		return nil, errors.New("v2: protocol=v2 requires --hmac-key-path (or hmac_key_path in config) " +
			"pointing to a file containing the operator HMAC key as hex")
	}

	key, err := loadHMACKeyFromFile(cfg.HMACKeyPath)
	if err != nil {
		return nil, fmt.Errorf("v2: load hmac key: %w", err)
	}
	if len(key) < 32 {
		return nil, fmt.Errorf(
			"v2: hmac key from %s is %d bytes, minimum is 32",
			cfg.HMACKeyPath, len(key),
		)
	}
	if len(key) > 128 {
		return nil, fmt.Errorf(
			"v2: hmac key from %s is %d bytes, maximum is 128 "+
				"(enrollment rule — enforced by pkg/mining/enrollment)",
			cfg.HMACKeyPath, len(key),
		)
	}

	return &V2Context{
		Enabled:     true,
		NodeID:      cfg.NodeID,
		GPUUUID:     cfg.GPUUUID,
		GPUName:     cfg.GPUName,
		GPUArch:     strings.ToLower(cfg.GPUArch),
		ComputeCap:  cfg.ComputeCap,
		CUDAVersion: cfg.CUDAVersion,
		DriverVer:   cfg.DriverVer,
		HMACKey:     key,
	}, nil
}

// V2Config is the slice of Config fields the v2 path
// depends on. Separated from the main Config struct so
// LoadV2Context can be unit-tested without a full
// miner.toml fixture.
type V2Config struct {
	Protocol    string // "v1" (default) or "v2"
	NodeID      string
	GPUUUID     string
	GPUName     string
	GPUArch     string
	ComputeCap  string
	CUDAVersion string
	DriverVer   string
	HMACKeyPath string
}

// loadHMACKeyFromFile reads a hex-encoded HMAC key from disk.
// The file MUST contain exactly one non-empty line of hex (any
// trailing newline + surrounding whitespace is stripped).
// Binary-format keys are NOT supported — hex is more auditable
// ("here is my key, this is the file, diff this") and avoids
// the "did you accidentally commit this to a git repo and now
// it's full of UTF-8 BOMs" class of bug.
//
// The file permissions are NOT checked here. POSIX operators
// are expected to chmod 0600 it themselves; Windows operators
// rely on NTFS ACLs inherited from %USERPROFILE%.
func loadHMACKeyFromFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, fmt.Errorf("file %s is empty", path)
	}
	// Accept a single-line file. Multiple non-empty lines are
	// rejected rather than silently picking the first, because
	// that's exactly the pattern that causes "I added a backup
	// key to the file and now nothing works" support tickets.
	if strings.ContainsAny(trimmed, "\r\n") {
		return nil, fmt.Errorf(
			"file %s contains multiple lines; expected a single hex-encoded key",
			path,
		)
	}
	key, err := hex.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("decode hex in %s: %w", path, err)
	}
	return key, nil
}

// V2PrepareAttestation is the glue called by runLoop between
// mining.Solve and submitProof when v2 is enabled. It:
//
//  1. Fetches a fresh challenge from the validator.
//  2. Builds an HMAC attestation bundle from the proof +
//     challenge + v2 context.
//  3. Attaches the attestation to the proof (bumps version).
//
// Returns the mutated proof on success. On error, the caller
// should treat this as "can't submit this proof" and move on
// to the next work fetch — a transient issuer 503 or a stale
// challenge race shouldn't crash the miner.
//
// Side effects on proof: sets proof.Version = v2, sets
// proof.Attestation. No other fields are touched.
func V2PrepareAttestation(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	v2 *V2Context,
	proof *mining.Proof,
) error {
	if !v2.IsEnabled() {
		return errors.New("v2: PrepareAttestation called with disabled context")
	}
	if proof == nil {
		return errors.New("v2: PrepareAttestation called with nil proof")
	}

	chg, err := v2client.FetchChallenge(ctx, client, baseURL)
	if err != nil {
		return fmt.Errorf("v2: fetch challenge: %w", err)
	}

	att, err := v2client.BuildHMACAttestation(
		v2client.BundleInputs{
			NodeID:      v2.NodeID,
			GPUUUID:     v2.GPUUUID,
			GPUName:     v2.GPUName,
			ComputeCap:  v2.ComputeCap,
			CUDAVersion: v2.CUDAVersion,
			DriverVer:   v2.DriverVer,
			HMACKey:     v2.HMACKey,
			MinerAddr:   proof.MinerAddr,
			BatchRoot:   proof.BatchRoot,
			MixDigest:   proof.MixDigest,
			Challenge:   chg,
		},
		v2.GPUArch,
	)
	if err != nil {
		return fmt.Errorf("v2: build attestation: %w", err)
	}

	if err := v2client.AttachToProof(proof, att); err != nil {
		return fmt.Errorf("v2: attach attestation: %w", err)
	}

	return nil
}
