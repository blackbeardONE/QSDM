// Package v2client is the reference miner-side client for the v2
// NVIDIA-locked mining protocol. It exists so every future miner
// binary (cmd/qsdmminer, cmd/qsdmminer-console, and the eventual
// native-NVIDIA miner from Phase 2c-iv) can share one well-tested
// implementation of:
//
//  1. Fetching a server-issued challenge via
//     GET /api/v1/mining/challenge (FetchChallenge)
//  2. Assembling the on-the-wire hmac.Bundle from a proof's
//     consensus fields + the fetched challenge + the operator's
//     local HMAC key (BuildHMACAttestation)
//
// The package intentionally has no dependency on the validator-
// side subpackages beyond the concrete types those packages
// export: it does NOT import pkg/api (which would pull in the
// whole HTTP server surface) and it does NOT import
// pkg/mining/attest/* (which is the verifier side). The one
// shared contract is the hmac.Bundle schema itself — miners
// assemble what validators later parse.
//
// Packaging rationale: we deliberately do not put this in
// pkg/mining itself because pkg/mining is the consensus-critical
// core and the client path pulls in net/http. Keeping v2client a
// peer of pkg/mining/attest/* keeps the dependency fan-out of
// pkg/mining minimal.
package v2client

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
)

// challengeWire mirrors api.ChallengeWire. Duplicated here rather
// than imported from pkg/api because pulling pkg/api just for one
// tiny struct would drag the whole HTTP server tree into every
// miner binary's link graph. The JSON tags MUST match
// api.ChallengeWire byte-for-byte; the
// TestChallengeWireMatchesAPI test in v2client_test.go asserts
// this at build-with-tests time.
type challengeWire struct {
	Nonce     string `json:"nonce"`
	IssuedAt  int64  `json:"issued_at"`
	SignerID  string `json:"signer_id"`
	Signature string `json:"signature"`
}

// FetchChallenge performs GET /api/v1/mining/challenge and
// returns the decoded Challenge. baseURL should be the validator
// root (e.g. "http://validator:8080"), WITHOUT a trailing slash.
// The caller owns the http.Client — we do not construct one
// internally so mining binaries can share their configured
// transport / timeout / TLS settings.
//
// The returned challenge is NOT verified — verification is the
// validator's job, not the miner's. The miner simply echoes
// (signer_id, signature) through its bundle; a bad signature
// from a misbehaving issuer only hurts that issuer's miners,
// who will see their proofs rejected downstream.
func FetchChallenge(ctx context.Context, client *http.Client, baseURL string) (challenge.Challenge, error) {
	if client == nil {
		return challenge.Challenge{}, errors.New("v2client: nil http client")
	}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet, baseURL+"/api/v1/mining/challenge", nil,
	)
	if err != nil {
		return challenge.Challenge{}, fmt.Errorf("v2client: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return challenge.Challenge{}, fmt.Errorf("v2client: http error: %w", err)
	}
	defer resp.Body.Close()

	// Keep the response body bounded — a misbehaving validator
	// shouldn't be able to exhaust miner memory. 8 KiB is ~50×
	// the expected response size, generous but safe.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if err != nil {
		return challenge.Challenge{}, fmt.Errorf("v2client: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return challenge.Challenge{}, fmt.Errorf("v2client: challenge endpoint returned %d: %s",
			resp.StatusCode, string(body))
	}

	var wire challengeWire
	dec := json.NewDecoder(nopSnip(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return challenge.Challenge{}, fmt.Errorf("v2client: decode challenge: %w", err)
	}

	// Decode the fixed-width fields.
	rawNonce, err := hex.DecodeString(wire.Nonce)
	if err != nil || len(rawNonce) != 32 {
		return challenge.Challenge{}, fmt.Errorf("v2client: bad nonce hex: want 32 bytes, got %d", len(rawNonce))
	}
	rawSig, err := hex.DecodeString(wire.Signature)
	if err != nil || len(rawSig) == 0 {
		return challenge.Challenge{}, errors.New("v2client: bad or empty signature hex")
	}
	if wire.SignerID == "" {
		return challenge.Challenge{}, errors.New("v2client: empty signer_id")
	}
	if wire.IssuedAt <= 0 {
		return challenge.Challenge{}, fmt.Errorf("v2client: non-positive issued_at %d", wire.IssuedAt)
	}

	var nonce [32]byte
	copy(nonce[:], rawNonce)
	return challenge.Challenge{
		Nonce:     nonce,
		IssuedAt:  wire.IssuedAt,
		SignerID:  wire.SignerID,
		Signature: rawSig,
	}, nil
}

// BundleInputs is the miner-side info needed to assemble a
// nvidia-hmac-v1 attestation bundle. Separating it from the
// building logic keeps the inputs visible at call sites without
// a dozen positional arguments.
type BundleInputs struct {
	// NodeID is the operator's enrolled handle
	// ("alice-rtx4090-01"). Must match the registry entry.
	NodeID string

	// GPUUUID is the nvidia-smi UUID. Must match the UUID the
	// NodeID was enrolled with. Case is normalised by the
	// verifier (strings.EqualFold).
	GPUUUID string

	// GPUName is the human-readable GPU identifier. Used for the
	// deny-list check only; not bound to enrollment.
	GPUName string

	// ComputeCap, CUDAVersion, DriverVer are self-reported
	// metadata. Not currently consensus-critical but covered by
	// the HMAC so they can't be mutated post-sign.
	ComputeCap  string
	CUDAVersion string
	DriverVer   string

	// HMACKey is the operator key registered at enrollment. Used
	// to sign the canonical bundle bytes. MUST NOT be logged.
	HMACKey []byte

	// MinerAddr / BatchRoot / MixDigest together feed the
	// challenge_bind (spec §3.2.2 step 2). MUST match the same
	// values in the enclosing Proof or the verifier rejects.
	MinerAddr string
	BatchRoot [32]byte
	MixDigest [32]byte

	// Challenge is the fetched server-issued challenge. Its
	// Nonce / IssuedAt are echoed into the bundle; its SignerID /
	// Signature are passed through as challenge_signer_id /
	// challenge_sig.
	Challenge challenge.Challenge
}

// Validate checks BundleInputs for obviously-wrong values before
// spending CPU on canonical-form + HMAC computation. Exposed so
// tests can exercise the validation path without actually
// building a bundle.
func (in BundleInputs) Validate() error {
	if in.NodeID == "" {
		return errors.New("v2client: NodeID empty")
	}
	if in.GPUUUID == "" {
		return errors.New("v2client: GPUUUID empty")
	}
	if len(in.HMACKey) < 16 {
		return errors.New("v2client: HMACKey < 16 bytes")
	}
	if in.MinerAddr == "" {
		return errors.New("v2client: MinerAddr empty")
	}
	if in.Challenge.SignerID == "" {
		return errors.New("v2client: Challenge.SignerID empty")
	}
	if len(in.Challenge.Signature) == 0 {
		return errors.New("v2client: Challenge.Signature empty")
	}
	if in.Challenge.IssuedAt <= 0 {
		return errors.New("v2client: Challenge.IssuedAt non-positive")
	}
	return nil
}

// BuildHMACAttestation assembles and signs a nvidia-hmac-v1
// bundle, then wraps it as a mining.Attestation suitable for the
// Proof.Attestation field. Returns the base64-JSON-encoded
// bundle via the Attestation.BundleBase64 field plus the
// plumbed-through Nonce / IssuedAt on the Attestation itself (so
// the consensus JSON carries them independently of the inner
// bundle — cross-checked at verify time).
//
// The gpuArch parameter is the Attestation.GPUArch value, which
// is NOT currently consensus-critical but is reserved for the
// Tensor-Core mixin check (Phase 2c-iv step 8). Callers should
// pass the lowercased arch tag ("ada", "ampere", "hopper",
// "blackwell", ...). Empty string is tolerated for now.
func BuildHMACAttestation(in BundleInputs, gpuArch string) (mining.Attestation, error) {
	if err := in.Validate(); err != nil {
		return mining.Attestation{}, err
	}

	// Assemble the unsigned bundle. Field names must stay in
	// alphabetical order — hmac.Bundle declares them that way
	// and json.Marshal relies on declaration order to match the
	// canonical form.
	bundle := hmac.Bundle{
		ChallengeBind:     hmac.HexChallengeBind(in.MinerAddr, in.BatchRoot, in.MixDigest),
		ChallengeSig:      hex.EncodeToString(in.Challenge.Signature),
		ChallengeSignerID: in.Challenge.SignerID,
		ComputeCap:        in.ComputeCap,
		CUDAVersion:       in.CUDAVersion,
		DriverVer:         in.DriverVer,
		GPUName:           in.GPUName,
		GPUUUID:           in.GPUUUID,
		IssuedAt:          in.Challenge.IssuedAt,
		NodeID:            in.NodeID,
		Nonce:             hex.EncodeToString(in.Challenge.Nonce[:]),
	}
	signed, err := bundle.Sign(in.HMACKey)
	if err != nil {
		return mining.Attestation{}, fmt.Errorf("v2client: sign bundle: %w", err)
	}
	b64, err := signed.MarshalBase64()
	if err != nil {
		return mining.Attestation{}, fmt.Errorf("v2client: marshal bundle: %w", err)
	}
	return mining.Attestation{
		Type:         mining.AttestationTypeHMAC,
		BundleBase64: b64,
		GPUArch:      gpuArch,
		Nonce:        in.Challenge.Nonce,
		IssuedAt:     in.Challenge.IssuedAt,
	}, nil
}

// AttachToProof copies Attestation fields + bumps Proof.Version
// onto an existing proof. Exposed because some mining flows
// build the Proof first and only attach attestation afterward
// (e.g. solver returns a Proof; miner then fetches challenge and
// attaches). The function does NOT change any consensus fields
// except Version and Attestation.
func AttachToProof(p *mining.Proof, att mining.Attestation) error {
	if p == nil {
		return errors.New("v2client: nil proof")
	}
	p.Version = mining.ProtocolVersionV2
	p.Attestation = att
	return nil
}

// SuggestFreshnessDeadline reports when the fetched challenge
// will go stale from the validator's perspective. Miners can
// call this to avoid starting a long solve with a challenge that
// will expire before submission. Returns the unix-second
// deadline = IssuedAt + mining.FreshnessWindow.
func SuggestFreshnessDeadline(c challenge.Challenge) time.Time {
	return time.Unix(c.IssuedAt, 0).Add(mining.FreshnessWindow)
}

// nopSnip wraps a []byte so json.Decoder can read it without the
// allocation cost of bytes.NewReader in the hot path. Tiny
// optimisation — the miner's hot loop calls FetchChallenge once
// per block attempt, so every heap allocation shows up in flame
// graphs.
func nopSnip(b []byte) *byteSliceReader { return &byteSliceReader{b: b} }

type byteSliceReader struct{ b []byte }

func (r *byteSliceReader) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.b)
	r.b = r.b[n:]
	return n, nil
}
