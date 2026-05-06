package cc

// loader.go — one-shot operator helper that bridges the on-disk
// pinned-root files (roots.go) to a fully-assembled
// VerifierConfig.
//
// The verifier itself (verifier.go) consumes a VerifierConfig
// directly and is happy regardless of where its collaborators
// came from. In bring-up tests the config is hand-crafted; in
// production the operator's TOML/env config feeds a thin glue
// layer that calls LoadVerifierConfig and hands the result to
// attest.NewProductionDispatcher via ProductionConfig.CCConfig.
//
// Keeping this file separate from verifier.go matters because
// the loader is OPERATIONAL tooling (it does file I/O, returns
// nil for "not configured", reports paths in errors). The
// verifier itself is consensus-critical. Mixing the two in one
// file would tempt a future refactor to have NewVerifier read
// files implicitly — which would close a deterministic
// consensus boundary around environment-dependent state.

import (
	"fmt"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
)

// VerifierConfigOptions controls how LoadVerifierConfig
// assembles a VerifierConfig from operator-supplied paths and
// tunables. The zero value (no RootPaths, no collaborators)
// causes LoadVerifierConfig to return (nil, nil) — the
// "CC verifier disabled, fall back to stub" convention that
// attest.NewProductionDispatcher already implements when
// ProductionConfig.CCConfig is nil.
type VerifierConfigOptions struct {
	// RootPaths is the list of file/dir paths to scan for
	// pinned NVIDIA CA roots. Each entry may be a single
	// PEM/DER cert file or a directory containing such files
	// (see LoadPinnedRootsFromPaths for the recognised
	// extensions and the dedup rule).
	//
	// An empty / nil slice means "no operator-supplied trust
	// anchor": LoadVerifierConfig returns (nil, nil) so the
	// caller can pass nil to ProductionConfig.CCConfig and
	// keep the stub. We deliberately do NOT error on an empty
	// slice — it's the genuine genesis posture for any
	// validator that hasn't yet ratified an NVIDIA CC trust
	// anchor on chain.
	RootPaths []string

	// MinFirmware / MinDriver are the lower bounds the verifier
	// enforces in step 8 (PCR floor). Empty disables the
	// corresponding component, which is appropriate for
	// pre-production / test validators only — production
	// operators MUST set both.
	MinFirmware string
	MinDriver   string

	// FreshnessWindow / AllowedFutureSkew override the spec
	// defaults. Zero on either field means "use the spec
	// default" (mining.FreshnessWindow / 5 seconds). Setting a
	// non-zero override here is a CONSENSUS-DIVERGENT change:
	// validators MUST agree or they will accept/reject the same
	// bundle at different ages. Operators tune these only via
	// coordinated genesis re-pin or governance.
	FreshnessWindow   time.Duration
	AllowedFutureSkew time.Duration

	// NonceStore is the replay cache the verifier consults at
	// step 7. REQUIRED when RootPaths is non-empty: without
	// it, a valid bundle replays indefinitely within the
	// freshness window. LoadVerifierConfig refuses to assemble
	// a config that would degrade to no-replay-protection
	// silently.
	NonceStore hmac.NonceStore

	// ChallengeVerifier optionally cross-validates the bundle's
	// challenge_sig. Mirrors hmac.Verifier.ChallengeVerifier.
	// Optional even in production: the AIK signature alone is
	// the cryptographic anchor; the challenge cross-check is
	// an additional defence-in-depth layer that requires the
	// validator's active challenge layer to be wired. Tests and
	// bring-up validators may leave this nil.
	ChallengeVerifier challenge.SignerVerifier
}

// LoadVerifierConfig reads pinned roots from disk per opts and
// returns a fully-assembled *VerifierConfig ready to hand to
// NewVerifier (directly) or to attest.NewProductionDispatcher
// via ProductionConfig.CCConfig.
//
// Three return shapes:
//
//   1. (nil, nil)     — opts.RootPaths is empty; caller is
//                       expected to leave CCConfig nil so the
//                       stub stays wired.
//   2. (cfg, nil)     — happy path; cfg is ready to use.
//   3. (nil, err)     — file I/O, decode, or required-collab
//                       missing. Operator must intervene before
//                       the validator can boot in production.
//
// LoadVerifierConfig does NOT validate the resulting config
// against NewVerifier's rules (e.g. PinnedRoots non-empty); it
// just hands the result back. The downstream NewVerifier call
// — invoked by NewProductionDispatcher when ProductionConfig
// .CCConfig is non-nil — re-validates and reports any residual
// problems with full operator context.
func LoadVerifierConfig(opts VerifierConfigOptions) (*VerifierConfig, error) {
	if len(opts.RootPaths) == 0 {
		return nil, nil
	}
	roots, err := LoadPinnedRootsFromPaths(opts.RootPaths)
	if err != nil {
		return nil, fmt.Errorf("cc.LoadVerifierConfig: load roots: %w", err)
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf(
			"cc.LoadVerifierConfig: no certs found in %d configured path(s) — "+
				"check that the .pem/.der files exist and are readable",
			len(opts.RootPaths))
	}
	if opts.NonceStore == nil {
		// Production posture: a populated CCConfig with a nil
		// NonceStore would degrade replay protection to
		// freshness-window-only. NewVerifier itself ALLOWS this
		// (NonceStore is documented as optional in tests), but
		// the operator-facing LoadVerifierConfig is opinionated
		// and rejects the combination so a typo in the wiring
		// can't silently disable replay protection.
		return nil, fmt.Errorf(
			"cc.LoadVerifierConfig: NonceStore is required when RootPaths is set " +
				"(production deployments must wire replay protection)")
	}
	return &VerifierConfig{
		PinnedRoots: roots,
		MinFirmware: MinFirmware{
			Firmware: opts.MinFirmware,
			Driver:   opts.MinDriver,
		},
		NonceStore:        opts.NonceStore,
		FreshnessWindow:   opts.FreshnessWindow,
		AllowedFutureSkew: opts.AllowedFutureSkew,
		ChallengeVerifier: opts.ChallengeVerifier,
	}, nil
}
