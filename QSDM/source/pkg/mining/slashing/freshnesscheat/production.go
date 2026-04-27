package freshnesscheat

// production.go offers convenience factories that construct a
// production-shaped slashing.Dispatcher with the forgedattest,
// doublemining, AND freshnesscheat verifiers wired in. The
// three kinds share the same hmac.Registry / hmac.DenyList, so
// it's useful for a binary to wire them together with a single
// call.
//
// Direct use of slashing.NewProductionDispatcher remains
// supported and is preferred when callers need fine-grained
// control (e.g. a fake freshnesscheat verifier with a
// TrustingTestWitness in an integration test).
//
// Typical use from cmd/qsdm/<binary>:
//
//	d, err := freshnesscheat.NewProductionSlashingDispatcher(
//	    enrollment.NewStateBackedRegistry(state),
//	    governanceDenyList,
//	    nil,                                 // witness=nil → RejectAllWitness
//	    0, // forgedattest cap: use DefaultMaxSlashDust
//	    0, // doublemining cap: use DefaultMaxSlashDust
//	    0, // freshnesscheat cap: use DefaultMaxSlashDust
//	)
//	if err != nil { … }
//	slashApplier := chain.NewSlashApplier(accounts, state, d, rewardBPS)
//
// To exercise the freshnesscheat path on a testnet, pass
// `freshnesscheat.TrustingTestWitness{}` for the witness arg.
// NEVER do this on mainnet.

import (
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing/doublemining"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing/forgedattest"
)

// NewProductionSlashingDispatcher builds a forgedattest.Verifier,
// a doublemining.Verifier, AND a freshnesscheat.Verifier against
// the supplied registry + deny-list, then wraps them in a
// production slashing.Dispatcher.
//
// Arguments:
//
//   - registry: shared by all three verifiers; resolves
//     node_id → (gpu_uuid, hmac_key) at slashing time.
//     REQUIRED.
//   - denyList: shared by forgedattest + doublemining. Pass
//     nil for the empty deny-list default. Freshnesscheat does
//     not consult the deny-list (the offence is freshness, not
//     hardware identity).
//   - witness: drives the freshnesscheat verifier's anchor
//     authentication. Pass nil for the RejectAllWitness
//     production default. Pass `TrustingTestWitness{}` only on
//     testnets where every operator is trusted to construct
//     legitimate slash evidence.
//   - forgedAttestMaxSlashDust / doubleMiningMaxSlashDust /
//     freshnessCheatMaxSlashDust: per-offence dust caps. Pass 0
//     to fall back to the per-package default.
func NewProductionSlashingDispatcher(
	registry hmac.Registry,
	denyList hmac.DenyList,
	witness BlockInclusionWitness,
	forgedAttestMaxSlashDust uint64,
	doubleMiningMaxSlashDust uint64,
	freshnessCheatMaxSlashDust uint64,
) (*slashing.Dispatcher, error) {
	if witness == nil {
		witness = RejectAllWitness{}
	}
	fa := &forgedattest.Verifier{
		Registry:     registry,
		DenyList:     denyList,
		MaxSlashDust: forgedAttestMaxSlashDust,
	}
	dm := &doublemining.Verifier{
		Registry:     registry,
		DenyList:     denyList,
		MaxSlashDust: doubleMiningMaxSlashDust,
	}
	fc := &Verifier{
		Witness:      witness,
		Registry:     registry,
		MaxSlashDust: freshnessCheatMaxSlashDust,
	}
	return slashing.NewProductionDispatcher(slashing.ProductionConfig{
		ForgedAttestation: fa,
		DoubleMining:      dm,
		FreshnessCheat:    fc,
	})
}
