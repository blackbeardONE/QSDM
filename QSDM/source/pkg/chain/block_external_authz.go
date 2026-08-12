package chain

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// ErrExternalProducerNotAuthorized is returned when an external block arrives
// from a producer that is not on the configured allowlist.
var ErrExternalProducerNotAuthorized = errors.New("chain: external block producer is not authorized")

// externalProducerAllowlist gates which producers may inject blocks through
// TryAppendExternalBlock.
//
// # Why this exists
//
// Blocks arrive on the `qsdm-blocks` gossip topic and are appended by
// TryAppendExternalBlock, which checks the block hash, the producer signature,
// tip linkage, POL, and a full replay state root. None of those establish that
// the sender is *allowed* to produce. Concretely:
//
//   - VerifyBlockSignature returns nil for an UNSIGNED block whenever
//     SignedBlocksRequiredAt(height) is false, which is the deployed posture --
//     live blocks on api.qsdm.tech carry no producer_auth at all.
//   - When a block is signed the check is self-certifying: ProducerID is derived
//     from the signing key, so any freshly generated key verifies against itself.
//
// So any peer that can reach the topic may sync the chain, build a block on the
// current tip containing transactions of its choosing, and have every honest
// node replay and append it. Block replay does not verify per-transaction
// signatures, so those transactions are not independently constrained either.
//
// # Why an allowlist rather than a validator-set membership check
//
// Membership cannot be used yet. ProducerID on the live chain is the libp2p
// host ID (a 12D3Koo... peer ID), while the validator set holds ML-DSA-derived
// consensus addresses. Requiring "producer is an active validator" would reject
// every block the reference deployment currently produces and halt the chain.
// An operator-pinned allowlist closes the injection path today, using the peer
// ID operators already pin as their bootstrap peer, and does not depend on the
// signed-block activation height being coordinated first.
//
// The empty allowlist preserves today's behaviour exactly, so this is additive
// and a node that does not configure it is byte-identical to before. Callers
// are expected to log that posture loudly at startup.
type externalProducerAllowlist struct {
	mu      sync.RWMutex
	allowed map[string]struct{}
}

// set replaces the allowlist. Entries are trimmed and empty entries dropped, so
// a stray comma in configuration cannot silently authorize the empty producer
// ID that an unsigned block would carry if ProducerID were omitted.
func (a *externalProducerAllowlist) set(ids []string) {
	next := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		next[trimmed] = struct{}{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(next) == 0 {
		a.allowed = nil
		return
	}
	a.allowed = next
}

// enforced reports whether any producer has been pinned.
func (a *externalProducerAllowlist) enforced() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.allowed) > 0
}

// check authorizes one producer ID. An unconfigured allowlist authorizes
// everything, which is the pre-existing behaviour.
func (a *externalProducerAllowlist) check(producerID string) error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(a.allowed) == 0 {
		return nil
	}
	if _, ok := a.allowed[strings.TrimSpace(producerID)]; ok {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrExternalProducerNotAuthorized, producerID)
}

// snapshot returns the pinned producer IDs, for startup logging and diagnostics.
func (a *externalProducerAllowlist) snapshot() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]string, 0, len(a.allowed))
	for id := range a.allowed {
		out = append(out, id)
	}
	return out
}

// SetAuthorizedBlockProducers pins the producers whose blocks this node will
// append from gossip. Passing an empty slice clears the allowlist and restores
// the accept-any behaviour.
func (bp *BlockProducer) SetAuthorizedBlockProducers(ids []string) {
	if bp == nil {
		return
	}
	bp.externalAuthz.set(ids)
}

// AuthorizedBlockProducers returns the pinned producer IDs. Empty means the
// gate is open.
func (bp *BlockProducer) AuthorizedBlockProducers() []string {
	if bp == nil {
		return nil
	}
	return bp.externalAuthz.snapshot()
}

// ExternalProducerGateEnforced reports whether external blocks are restricted.
func (bp *BlockProducer) ExternalProducerGateEnforced() bool {
	if bp == nil {
		return false
	}
	return bp.externalAuthz.enforced()
}
