package api

// TrustPeerProvider adapter wiring the live ValidatorSet (pkg/chain) into
// the trust aggregator defined in handlers_trust.go.
//
// Why this file exists:
//
//   - handlers_trust.go only speaks in terms of the TrustPeerProvider
//     interface. For unit tests we use an in-memory fake; for production
//     we need a concrete adapter that enumerates the validators this node
//     currently knows about and reports them as public peers.
//
//   - Per Major Update §8.5 and NVIDIA_LOCK_CONSENSUS_SCOPE.md the trust
//     surface is a *transparency signal*, not a consensus rule. Therefore
//     this adapter must never fabricate attestation data for a peer —
//     when we have no cross-peer attestation gossip path (the initial
//     deployment state), each validator other than the local node is
//     reported with AttestedAt=zero. The aggregator then shows "1 / N" if
//     the local node has a fresh proof, or "0 / N" if not. That is the
//     honest answer, and it is the whole point of the widget.
//
//   - When a cross-peer NGC gossip protocol ships, this adapter is the
//     single place that needs to learn how to merge remote attestation
//     snapshots into the PeerAttestation list. The interface boundary
//     above (ValidatorEnumerator) is already sized for that future.
//
// This adapter lives in pkg/api — not pkg/chain — because pkg/api is
// where the trust aggregator, the MonitoringLocalSource, and every other
// piece of transparency plumbing already live. Keeping them in one
// package means `go vet ./pkg/api/...` can reason about the whole
// transparency surface as a unit.

// ValidatorEnumerator is the minimum surface of pkg/chain.ValidatorSet
// that the peer provider needs. Defined as an interface so tests can
// inject a fake and so pkg/api does not force a pkg/chain import when a
// future downstream embeds pkg/api in a validator-free context (e.g. the
// miner binary, which has no validator set).
type ValidatorEnumerator interface {
	// ActiveValidatorAddresses returns the on-chain addresses of the
	// validators currently in the active set. An empty slice means this
	// node has not yet discovered a validator set, which is treated by
	// the aggregator as "warming up".
	//
	// The adapter below is responsible for translating *chain.Validator
	// into the minimal address view this interface exposes; we avoid
	// leaking stake or slash counts through the trust surface because
	// those fields are not part of §8.5's public schema.
	ActiveValidatorAddresses() []string
}

// ValidatorSetPeerProvider is the concrete TrustPeerProvider used by the
// validator daemon in cmd/qsdmplus/main.go. It enumerates the currently
// active validator set and returns one PeerAttestation per validator
// address, with AttestedAt=zero (see file header for why that is the
// correct behaviour in the pre-gossip world).
//
// The local node's row, if any, is *not* produced here — the aggregator
// pairs this provider's rows with the LocalAttestationSource in its
// Refresh() loop (handlers_trust.go). This keeps the two concerns
// orthogonal: this adapter answers "which public peers exist", the
// local source answers "what is our own fresh proof".
type ValidatorSetPeerProvider struct {
	// Enumerator is the live validator set. Must be non-nil; the
	// constructor panics otherwise so misconfiguration surfaces at boot.
	Enumerator ValidatorEnumerator
}

// NewValidatorSetPeerProvider constructs a ValidatorSetPeerProvider.
// Panics on nil Enumerator to fail fast at node startup — a silent nil
// would manifest as a perpetually empty trust surface in production.
func NewValidatorSetPeerProvider(e ValidatorEnumerator) *ValidatorSetPeerProvider {
	if e == nil {
		panic("api: ValidatorSetPeerProvider requires a non-nil ValidatorEnumerator")
	}
	return &ValidatorSetPeerProvider{Enumerator: e}
}

// sentinelValidatorAddresses lists validator "addresses" that exist in
// the in-memory ValidatorSet purely to satisfy BFT quorum bootstrap on
// a single-node network. They are not real cryptographic identities and
// must never appear in the trust transparency denominator: counting
// them would inflate total_public and make a 0/2 ratio look like a
// meaningful minority when it is actually "one real validator, one
// placeholder". See cmd/qsdmplus/main.go where "bootstrap" is
// registered against the set right after construction.
var sentinelValidatorAddresses = map[string]struct{}{
	"bootstrap": {},
}

// PeerAttestations implements TrustPeerProvider.
//
// For each active validator address we return a PeerAttestation with:
//   - NodeID        = the validator's on-chain address (redacted later by
//                     redactNodeID before it ever reaches a client)
//   - AttestedAt    = zero (we have no cross-peer attestation yet)
//   - GPUAvailable  = false (same reason)
//   - NGCHMACOK     = false (same reason)
//
// Sentinel entries (sentinelValidatorAddresses) are filtered out so the
// widget's denominator only counts validators that could, in principle,
// present a cryptographic attestation. Empty strings are also skipped.
//
// The aggregator merges the local node's own row on top of this list via
// its LocalAttestationSource path, so when the local node has a fresh
// proof the Ratio becomes 1/N rather than 0/N.
func (p *ValidatorSetPeerProvider) PeerAttestations() []PeerAttestation {
	addrs := p.Enumerator.ActiveValidatorAddresses()
	out := make([]PeerAttestation, 0, len(addrs))
	for _, a := range addrs {
		if a == "" {
			continue
		}
		if _, sentinel := sentinelValidatorAddresses[a]; sentinel {
			continue
		}
		out = append(out, PeerAttestation{NodeID: a})
	}
	return out
}

// ValidatorEnumeratorFunc adapts an ordinary closure into a
// ValidatorEnumerator. Useful at the call site in cmd/qsdmplus/main.go
// where the "enumerator" is really just
//
//	func() []string { return vs.RegisteredAddresses() }
//
// and a dedicated adapter type would be 3 lines of boilerplate per
// binary.
type ValidatorEnumeratorFunc func() []string

// ActiveValidatorAddresses implements ValidatorEnumerator by delegating
// to the wrapped closure. A nil closure is tolerated and treated as the
// empty set.
func (f ValidatorEnumeratorFunc) ActiveValidatorAddresses() []string {
	if f == nil {
		return nil
	}
	return f()
}

// compile-time assertions: keep the interface contracts honest.
var (
	_ TrustPeerProvider   = (*ValidatorSetPeerProvider)(nil)
	_ ValidatorEnumerator = ValidatorEnumeratorFunc(nil)
)
