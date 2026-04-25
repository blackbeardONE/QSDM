package forgedattest

// production.go is the convenience factory that combines a
// forgedattest.Verifier with a slashing.NewProductionDispatcher
// call so callers focused on the forged-attestation flow can
// wire the slashing path with a single function. Direct use of
// slashing.NewProductionDispatcher remains supported for tests
// and binaries that need fine-grained control (e.g. to inject a
// fake forged-attestation verifier for an integration test).
//
// Note: this factory leaves the double-mining slot at its
// StubVerifier default. Binaries that want both kinds wired
// should call doublemining.NewProductionSlashingDispatcher
// instead, which constructs both verifiers against the same
// registry / deny-list.
//
// Typical use from cmd/qsdm/<binary>:
//
//	d, err := forgedattest.NewProductionSlashingDispatcher(
//	    enrollment.NewStateBackedRegistry(state),
//	    governanceDenyList,
//	    0, // use DefaultMaxSlashDust
//	)
//	if err != nil { … }
//	slashApplier := chain.NewSlashApplier(accounts, state, d, rewardBPS)

import (
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
)

// NewProductionSlashingDispatcher builds a forgedattest.Verifier
// against the given Registry + DenyList, then wraps it in a
// production slashing.Dispatcher with stubs registered for the
// other (deferred) EvidenceKinds.
//
// Pass denyList=nil for the empty-deny-list default (the genesis
// state). Pass maxSlashDust=0 for DefaultMaxSlashDust.
func NewProductionSlashingDispatcher(
	registry hmac.Registry,
	denyList hmac.DenyList,
	maxSlashDust uint64,
) (*slashing.Dispatcher, error) {
	v := &Verifier{
		Registry:     registry,
		DenyList:     denyList,
		MaxSlashDust: maxSlashDust,
	}
	return slashing.NewProductionDispatcher(slashing.ProductionConfig{
		ForgedAttestation: v,
	})
}
