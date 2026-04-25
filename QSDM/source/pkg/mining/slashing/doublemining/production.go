package doublemining

// production.go offers convenience factories that construct a
// production-shaped slashing.Dispatcher with both the
// forgedattest and the doublemining verifiers wired in. The two
// kinds share the same hmac.Registry / hmac.DenyList, so it's
// useful for a binary to wire them together with a single call.
//
// Direct use of slashing.NewProductionDispatcher remains
// supported and is preferred when callers need fine-grained
// control (e.g. a fake double-mining verifier in an integration
// test).
//
// Typical use from cmd/qsdm/<binary>:
//
//	d, err := doublemining.NewProductionSlashingDispatcher(
//	    enrollment.NewStateBackedRegistry(state),
//	    governanceDenyList,
//	    0, // forgedattest cap: use DefaultMaxSlashDust
//	    0, // doublemining cap: use DefaultMaxSlashDust
//	)
//	if err != nil { … }
//	slashApplier := chain.NewSlashApplier(accounts, state, d, rewardBPS)

import (
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing/forgedattest"
)

// NewProductionSlashingDispatcher builds a forgedattest.Verifier
// AND a doublemining.Verifier against the supplied registry +
// deny-list, then wraps them in a production
// slashing.Dispatcher. The freshness-cheat kind remains stubbed
// pending its own verifier.
//
// Pass denyList=nil for the empty deny-list default. Pass either
// MaxSlashDust=0 to fall back to the per-package default.
func NewProductionSlashingDispatcher(
	registry hmac.Registry,
	denyList hmac.DenyList,
	forgedAttestMaxSlashDust uint64,
	doubleMiningMaxSlashDust uint64,
) (*slashing.Dispatcher, error) {
	fa := &forgedattest.Verifier{
		Registry:     registry,
		DenyList:     denyList,
		MaxSlashDust: forgedAttestMaxSlashDust,
	}
	dm := &Verifier{
		Registry:     registry,
		DenyList:     denyList,
		MaxSlashDust: doubleMiningMaxSlashDust,
	}
	return slashing.NewProductionDispatcher(slashing.ProductionConfig{
		ForgedAttestation: fa,
		DoubleMining:      dm,
	})
}
