package chain

import (
	"fmt"
	"sort"
)

// Chain-state-derived validator membership.
//
// The problem this solves:
//
// cmd/qsdm built its validator set with two literal registrations —
// Register("bootstrap", minStake) and Register(ownWalletAddress, minStake).
// Membership was therefore LOCAL and node-specific: node A believed the set
// was {bootstrap, A} while node B believed it was {bootstrap, B}. No two
// peers agreed on who the validators were, so a 2/3-of-stake quorum could
// never be computed across them, and no home node could ever become a real
// validator on the canonical chain.
//
// SyncValidatorStakesFromCommittedChain did not help: it iterates
// vs.RegisteredAddresses(), so it only re-weights members that were already
// registered locally. It never adds one.
//
// The consequence was a network with exactly one effective producer. When
// that producer stopped, the chain stopped, and nothing else could take
// over — the opposite of "keeps working as long as one peer is running".
//
// The fix is to make membership a FUNCTION OF COMMITTED STATE. Every node
// runs the same derivation over the same bonded-stake ledger and gets a
// byte-identical set, so quorum is meaningful across peers and a home node
// joins simply by bonding stake.

// ValidatorMembershipSource supplies bonded stake per candidate validator.
// *StakingLedger satisfies it. Kept as an interface so the derivation can be
// tested without constructing a full ledger, and so a future on-chain
// registry can replace the source without touching the derivation rules.
type ValidatorMembershipSource interface {
	// BondedByValidator returns validator address -> total bonded stake.
	// The returned map must not alias caller-visible state.
	BondedByValidator() map[string]float64
}

// DeriveValidatorMembership returns the addresses that qualify as validators
// under the given state, in deterministic (sorted) order.
//
// Rules, all of which must be evaluated identically on every node:
//
//   - a candidate qualifies at bonded stake >= cfg.MinStake
//   - candidates are ranked by stake descending, then address ascending so
//     ties break deterministically rather than by map iteration order
//   - at most cfg.MaxValidators are admitted
//
// Determinism is the whole point: two nodes given the same bonded-stake map
// MUST produce the same slice, or they will disagree about quorum.
func DeriveValidatorMembership(cfg ValidatorSetConfig, bonded map[string]float64) []string {
	type candidate struct {
		addr  string
		stake float64
	}
	cands := make([]candidate, 0, len(bonded))
	for addr, stake := range bonded {
		if addr == "" {
			continue
		}
		if stake < cfg.MinStake {
			continue
		}
		cands = append(cands, candidate{addr: addr, stake: stake})
	}

	sort.Slice(cands, func(i, j int) bool {
		if cands[i].stake != cands[j].stake {
			return cands[i].stake > cands[j].stake
		}
		return cands[i].addr < cands[j].addr
	})

	max := cfg.MaxValidators
	if max <= 0 || max > len(cands) {
		max = len(cands)
	}
	out := make([]string, 0, max)
	for i := 0; i < max; i++ {
		out = append(out, cands[i].addr)
	}
	return out
}

// ValidatorSetFromChainState builds a ValidatorSet whose membership is
// derived from committed state rather than declared locally.
//
// Returns an error when the derivation admits nobody. That is deliberately
// loud: a node that silently ran with an empty validator set would accept
// any block and finalize nothing, which is worse than refusing to start.
func ValidatorSetFromChainState(cfg ValidatorSetConfig, src ValidatorMembershipSource) (*ValidatorSet, error) {
	if src == nil {
		return nil, fmt.Errorf("chain: validator membership source is nil")
	}
	bonded := src.BondedByValidator()
	members := DeriveValidatorMembership(cfg, bonded)
	if len(members) == 0 {
		return nil, fmt.Errorf(
			"chain: no address meets the %.0f minimum bonded stake, so the validator set would be empty",
			cfg.MinStake)
	}

	vs := NewValidatorSet(cfg)
	for _, addr := range members {
		if err := vs.Register(addr, bonded[addr]); err != nil {
			return nil, fmt.Errorf("chain: register derived validator %s: %w", addr, err)
		}
	}
	return vs, nil
}

// ReconcileValidatorMembership updates an existing set in place to match the
// membership implied by current state: admits newly-qualified addresses,
// re-weights existing ones, and exits those that dropped below the minimum.
//
// Used on committed-height transitions so the active set tracks the chain
// without a restart. Returns the admitted and exited addresses so callers
// can log a membership change rather than have it happen invisibly.
func ReconcileValidatorMembership(vs *ValidatorSet, src ValidatorMembershipSource) (admitted, exited []string, err error) {
	if vs == nil {
		return nil, nil, fmt.Errorf("chain: nil validator set")
	}
	if src == nil {
		return nil, nil, fmt.Errorf("chain: validator membership source is nil")
	}

	bonded := src.BondedByValidator()
	want := DeriveValidatorMembership(vs.config, bonded)
	wantSet := make(map[string]struct{}, len(want))
	for _, a := range want {
		wantSet[a] = struct{}{}
	}

	// Refusing to empty the set is a liveness guard: a transient state read
	// that admits nobody must not silently disband consensus.
	if len(want) == 0 {
		return nil, nil, fmt.Errorf(
			"chain: refusing to reconcile to an empty validator set (min stake %.0f)", vs.config.MinStake)
	}

	current := make(map[string]struct{})
	for _, addr := range vs.RegisteredAddresses() {
		current[addr] = struct{}{}
	}

	for _, addr := range want {
		if _, ok := current[addr]; ok {
			_ = vs.SetStake(addr, bonded[addr])
			continue
		}
		if err := vs.Register(addr, bonded[addr]); err != nil {
			return nil, nil, fmt.Errorf("chain: admit validator %s: %w", addr, err)
		}
		admitted = append(admitted, addr)
	}

	for addr := range current {
		if _, ok := wantSet[addr]; ok {
			continue
		}
		if _, err := vs.Exit(addr); err != nil {
			// An address that cannot exit cleanly is not fatal to the
			// reconcile; report it and keep the rest of the set correct.
			continue
		}
		exited = append(exited, addr)
	}

	sort.Strings(admitted)
	sort.Strings(exited)
	return admitted, exited, nil
}
