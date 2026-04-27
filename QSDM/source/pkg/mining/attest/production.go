package attest

// production.go ships NewProductionDispatcher: the canonical
// factory every validator binary should call exactly once at
// startup to obtain a fully-wired mining.AttestationVerifier
// ready to drop into mining.VerifierConfig.Attestation.
//
// Why this lives here and not in the individual cmd/ binaries:
//
//   - Wiring the dispatcher + hmac verifier + cc verifier
//     involves half a dozen collaborators (Registry, NonceStore,
//     DenyList, ChallengeVerifier, FreshnessWindow,
//     AllowedFutureSkew, …). If each validator binary repeats
//     that assembly, small drift — e.g. one validator defaults
//     DenyList to nil while another uses EmptyDenyList, or one
//     forgets to wire NonceStore — produces consensus-divergent
//     behaviour. Centralising the factory makes "what does a
//     correctly-wired validator look like?" a single-file
//     answer.
//
//   - Future phases (CC verifier, new attestation types) plug
//     into this factory. Call sites don't have to track new
//     required verifiers; AssertAllRegistered catches missing
//     ones at boot with a clear error, not at accept-time with
//     a silent dispatch miss.
//
// NewProductionDispatcher is stateless; each call returns a
// fresh *Dispatcher. Share the returned dispatcher across all
// goroutines — it's safe for concurrent VerifyAttestation.

import (
	"errors"
	"fmt"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/cc"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
)

// ProductionConfig is the superset of collaborators a
// production validator needs to accept v2 proofs. The zero value
// is INVALID — NewProductionDispatcher returns an error rather
// than silently defaulting anything consensus-critical.
//
// Field-by-field rationale:
type ProductionConfig struct {
	// Registry maps node_id -> (gpu_uuid, hmac_key). REQUIRED.
	// Without it there is no way to look up what an enrolled
	// operator's expected GPU UUID is, so every bundle would
	// fail at hmac verifier step 5.
	Registry hmac.Registry

	// ChallengeVerifier authenticates (nonce, issued_at) back to
	// a known validator via the challenge_sig /
	// challenge_signer_id fields on the bundle. REQUIRED for
	// production — leaving it nil degrades anti-replay to
	// freshness-window + nonce-cache only, which is the bring-up
	// posture, not the production one. NewProductionDispatcher
	// refuses to build without it.
	ChallengeVerifier challenge.SignerVerifier

	// NonceStore provides replay detection keyed on
	// (node_id, nonce). REQUIRED for production — without it, a
	// valid bundle can be replayed any number of times within
	// its freshness window.
	NonceStore hmac.NonceStore

	// DenyList is governance-controlled gpu_name blocklist.
	// Optional: defaults to hmac.EmptyDenyList (the genesis
	// posture). Pass a non-empty list when governance has
	// appended bans.
	DenyList hmac.DenyList

	// FreshnessWindow overrides mining.FreshnessWindow. Zero =
	// use mining.FreshnessWindow. Do NOT set this in production
	// without cross-validator coordination: all validators MUST
	// agree or they will accept/reject the same bundle
	// differently.
	FreshnessWindow time.Duration

	// AllowedFutureSkew is how far ahead of our clock a bundle's
	// issued_at may be before we reject it as "from the future."
	// Zero = default (5 seconds, matching hmac.NewVerifier).
	AllowedFutureSkew time.Duration

	// CCVerifier is the nvidia-cc-v1 verifier. Optional: if nil
	// AND CCConfig is also nil, cc.NewStubVerifier() is
	// registered, which rejects every nvidia-cc-v1 proof with
	// ErrNotYetAvailable. Set this to a fully-built verifier
	// (e.g. one returned by cc.NewVerifier) when you've
	// constructed it externally.
	CCVerifier mining.AttestationVerifier

	// CCConfig is the convenience knob for the canonical CC
	// production wiring: pass a populated cc.VerifierConfig
	// (genesis-pinned roots + min firmware + replay store)
	// and NewProductionDispatcher will build the
	// *cc.Verifier for you. Mutually exclusive with CCVerifier:
	// setting both returns an error.
	//
	// Pass nil to keep the stub (fail-closed) — the right
	// posture for validators that haven't yet ratified an
	// NVIDIA CC trust anchor on chain. The day the trust
	// anchor lands, flipping CCConfig from nil to a populated
	// struct is the single-line change to start accepting
	// nvidia-cc-v1 proofs.
	CCConfig *cc.VerifierConfig
}

// Validate checks for the required collaborators. Returns an
// error that names the specific missing field so operators
// don't have to grep the source to figure out what's wrong.
func (cfg ProductionConfig) Validate() error {
	if cfg.Registry == nil {
		return errors.New("attest: ProductionConfig.Registry is required — " +
			"without it, enrolled operators cannot be resolved")
	}
	if cfg.ChallengeVerifier == nil {
		return errors.New("attest: ProductionConfig.ChallengeVerifier is required in production — " +
			"without it, miners can mint their own nonces and replay them indefinitely")
	}
	if cfg.NonceStore == nil {
		return errors.New("attest: ProductionConfig.NonceStore is required in production — " +
			"without it, a single valid bundle can be replayed across blocks")
	}
	if cfg.CCVerifier != nil && cfg.CCConfig != nil {
		return errors.New("attest: ProductionConfig.CCVerifier and CCConfig are mutually exclusive — " +
			"pass one or the other (or neither, to use the stub)")
	}
	return nil
}

// NewProductionDispatcher builds a fully-wired Dispatcher
// registered with production verifiers for both v2 attestation
// types. It calls AssertAllRegistered before returning, so any
// caller that then swaps one out will immediately see the
// mismatch via a fresh AssertAllRegistered call.
//
// The returned *Dispatcher is the value to assign to
// mining.VerifierConfig.Attestation.
func NewProductionDispatcher(cfg ProductionConfig) (*Dispatcher, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// HMAC verifier (consumer GPU path)
	hmacV := hmac.NewVerifier(cfg.Registry)
	hmacV.NonceStore = cfg.NonceStore
	hmacV.ChallengeVerifier = cfg.ChallengeVerifier
	if cfg.DenyList != nil {
		hmacV.DenyList = cfg.DenyList
	}
	if cfg.FreshnessWindow > 0 {
		hmacV.FreshnessWindow = cfg.FreshnessWindow
	}
	if cfg.AllowedFutureSkew > 0 {
		hmacV.AllowedFutureSkew = cfg.AllowedFutureSkew
	}

	// CC verifier — three-way pick:
	//   1. caller-supplied verifier (escape hatch for custom
	//      builds, e.g. an HSM-backed AIK validator);
	//   2. caller-supplied config → build a real cc.Verifier
	//      via the canonical factory;
	//   3. neither → fall back to cc.NewStubVerifier (the fail-
	//      closed default until governance pins a CC trust
	//      anchor).
	var ccV mining.AttestationVerifier = cc.NewStubVerifier()
	switch {
	case cfg.CCVerifier != nil:
		ccV = cfg.CCVerifier
	case cfg.CCConfig != nil:
		built, err := cc.NewVerifier(*cfg.CCConfig)
		if err != nil {
			return nil, fmt.Errorf("attest: build cc verifier: %w", err)
		}
		ccV = built
	}

	d := NewDispatcher()
	if err := d.Register(mining.AttestationTypeHMAC, hmacV); err != nil {
		return nil, fmt.Errorf("attest: register hmac verifier: %w", err)
	}
	if err := d.Register(mining.AttestationTypeCC, ccV); err != nil {
		return nil, fmt.Errorf("attest: register cc verifier: %w", err)
	}

	// Fail-closed guarantee: if either required type is missing,
	// refuse to hand back the dispatcher. This should be
	// impossible given the two Register calls above, but the
	// assertion makes the contract visible and survives future
	// refactors that add new required types.
	if err := d.AssertAllRegistered(
		mining.AttestationTypeHMAC,
		mining.AttestationTypeCC,
	); err != nil {
		return nil, fmt.Errorf("attest: AssertAllRegistered: %w", err)
	}

	return d, nil
}
