package slashing

// production.go ships NewProductionDispatcher: the canonical
// factory every validator binary should call exactly once at
// startup to obtain a fully-wired *Dispatcher ready to drop
// into pkg/chain.SlashApplier.
//
// Centralising the wiring here mirrors pkg/mining/attest's
// production.go pattern: validators all assemble the same
// dispatcher, so what one node accepts, every node accepts.
//
// As of this revision, all three reserved EvidenceKinds have
// concrete verifiers in their respective sub-packages:
//   - forged-attestation → pkg/mining/slashing/forgedattest
//   - double-mining       → pkg/mining/slashing/doublemining
//   - freshness-cheat     → pkg/mining/slashing/freshnesscheat
//
// To avoid import cycles (each verifier sub-package imports
// slashing for the EvidenceKind constants and the
// EvidenceVerifier interface), this factory accepts the
// concrete verifiers as injected EvidenceVerifier values rather
// than constructing them directly. Caller-side wiring lives in
// the cmd/ binaries (see cmd/qsdm/... for the canonical example)
// or in the helper factories in each verifier sub-package
// (e.g. doublemining.NewProductionSlashingDispatcher,
// freshnesscheat.NewProductionSlashingDispatcher).
//
// Freshness-cheat posture (see freshnesscheat/witness.go for the
// full discussion): the verifier is fully implemented, but its
// `BlockInclusionWitness` collaborator is what actually proves a
// chain-side observation (which proof was on-chain at which
// block, with what timestamp). Production binaries today wire
// `freshnesscheat.RejectAllWitness{}` because there is no
// quorum-attested block-header source yet (the BFT-finality
// dependency, see MINING_PROTOCOL_V2.md §12.3). End-user
// behaviour matches the previous StubVerifier — every freshness-
// cheat slash is rejected with `ErrEvidenceVerification` — but
// with a kind-specific error message that names the missing
// dependency, plus the rest of the verifier's structural,
// staleness, and registry-binding checks fire on the way through
// for richer operator diagnostics. Callers leaving the
// FreshnessCheat slot nil get the same RejectAllWitness posture
// via a default constructed below; explicit injection is allowed
// for testnet binaries that wire `TrustingTestWitness{}` to
// exercise the slashing pipeline end-to-end.

import (
	"errors"
	"fmt"
)

// ProductionConfig collects the verifier slots the production
// dispatcher needs to fill. The zero value is INVALID;
// Validate() returns a specific error naming the missing field.
type ProductionConfig struct {
	// ForgedAttestation is the verifier that handles
	// EvidenceKindForgedAttestation. REQUIRED.
	//
	// In production this is *forgedattest.Verifier (in the
	// pkg/mining/slashing/forgedattest sub-package),
	// constructed against the on-chain enrollment registry.
	// In tests it can be any EvidenceVerifier whose Kind()
	// returns EvidenceKindForgedAttestation, including a
	// fake.
	//
	// We accept this as an injected interface rather than
	// importing forgedattest directly, because forgedattest
	// imports this package for the kind/interface
	// definitions; the inversion keeps the dependency graph
	// acyclic.
	ForgedAttestation EvidenceVerifier

	// DoubleMining is the verifier that handles
	// EvidenceKindDoubleMining. OPTIONAL — leaving it nil
	// keeps the StubVerifier in place, which is the correct
	// posture for binaries that have not yet linked the
	// concrete doublemining package (e.g. tooling that only
	// touches the forged-attestation flow).
	//
	// In production this is *doublemining.Verifier, against
	// the same registry the forged-attestation verifier uses.
	// Same import-cycle reasoning as ForgedAttestation
	// applies.
	DoubleMining EvidenceVerifier

	// FreshnessCheat is the verifier that handles
	// EvidenceKindFreshnessCheat. OPTIONAL — leaving it nil
	// keeps the StubVerifier in place. In production this is
	// *freshnesscheat.Verifier wired against
	// `freshnesscheat.RejectAllWitness{}` (the safe default
	// pending BFT finality, see MINING_PROTOCOL_V2.md §12.3);
	// on testnets it may be wired against
	// `freshnesscheat.TrustingTestWitness{}` to exercise the
	// slashing pipeline. Same import-cycle reasoning as
	// ForgedAttestation applies.
	FreshnessCheat EvidenceVerifier
}

// Validate checks for required collaborators. Returns an error
// that names the specific missing field so operators don't have
// to grep the source to figure out what's wrong.
func (cfg ProductionConfig) Validate() error {
	if cfg.ForgedAttestation == nil {
		return errors.New(
			"slashing: ProductionConfig.ForgedAttestation is required — " +
				"construct one via forgedattest.NewVerifier and pass it here")
	}
	if cfg.ForgedAttestation.Kind() != EvidenceKindForgedAttestation {
		return fmt.Errorf(
			"slashing: ProductionConfig.ForgedAttestation.Kind() = %q, want %q",
			cfg.ForgedAttestation.Kind(), EvidenceKindForgedAttestation)
	}
	// DoubleMining is optional, but if supplied its Kind()
	// must match. A wrong-kind injection would route slash
	// txs of the wrong type to the wrong verifier — silent
	// non-determinism on the consensus path.
	if cfg.DoubleMining != nil &&
		cfg.DoubleMining.Kind() != EvidenceKindDoubleMining {
		return fmt.Errorf(
			"slashing: ProductionConfig.DoubleMining.Kind() = %q, want %q",
			cfg.DoubleMining.Kind(), EvidenceKindDoubleMining)
	}
	// Same kind-mismatch guard for the freshness-cheat slot.
	if cfg.FreshnessCheat != nil &&
		cfg.FreshnessCheat.Kind() != EvidenceKindFreshnessCheat {
		return fmt.Errorf(
			"slashing: ProductionConfig.FreshnessCheat.Kind() = %q, want %q",
			cfg.FreshnessCheat.Kind(), EvidenceKindFreshnessCheat)
	}
	return nil
}

// NewProductionDispatcher builds a fully-wired *Dispatcher with
// the supplied forged-attestation verifier registered for
// EvidenceKindForgedAttestation, and StubVerifier registered for
// the two deferred kinds (so unknown-kind rejections route
// cleanly to ErrEvidenceVerification rather than
// ErrUnknownEvidenceKind).
//
// The returned dispatcher is safe for concurrent use; it is
// expected to live for the lifetime of the chain instance.
func NewProductionDispatcher(cfg ProductionConfig) (*Dispatcher, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	d := NewDispatcher()

	// Dispatcher.Register panics ONLY on programmer errors
	// (nil verifier, empty kind, duplicate registration), all
	// of which would be bugs in this wiring code itself —
	// crash-at-boot is the right failure mode.
	d.Register(cfg.ForgedAttestation)

	// Wire double-mining: real verifier if injected, stub
	// otherwise. The stub returns ErrEvidenceVerification with
	// a "not yet implemented" message — better operator
	// signalling than ErrUnknownEvidenceKind, which would
	// otherwise leak the implementation gap as a versioning
	// problem.
	if cfg.DoubleMining != nil {
		d.Register(cfg.DoubleMining)
	} else {
		d.Register(StubVerifier{K: EvidenceKindDoubleMining})
	}

	// Wire freshness-cheat: real verifier if injected, stub
	// otherwise. Note: the canonical production posture is
	// "real verifier with RejectAllWitness", which still
	// rejects every slash but with kind-specific diagnostics —
	// see the freshnesscheat package overview for why we
	// prefer this over a permanent StubVerifier. Callers that
	// don't even import the freshnesscheat package (e.g.
	// tooling that only touches forged-attestation) fall back
	// here to the StubVerifier path; the end-user behaviour is
	// identical.
	if cfg.FreshnessCheat != nil {
		d.Register(cfg.FreshnessCheat)
	} else {
		d.Register(StubVerifier{K: EvidenceKindFreshnessCheat})
	}

	// Coverage guarantee: every kind in AllEvidenceKinds must
	// be present in the dispatcher. This catches the case
	// where AllEvidenceKinds grows but production.go forgets
	// to register the new kind.
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, k := range AllEvidenceKinds {
		if _, ok := d.verifiers[k]; !ok {
			return nil, fmt.Errorf(
				"slashing: production dispatcher missing verifier for kind %q "+
					"(this is a programmer error in NewProductionDispatcher; "+
					"add a Register call when extending AllEvidenceKinds)", k)
		}
	}

	return d, nil
}
