package chain

import (
	"errors"
	"sync/atomic"
)

// rc1113RulesHeight is the ONE activation height of the rc1113 security-sweep
// consensus rules (2026-11-16). Zero means never: until a release sets it on
// every node (SetRC1113RulesHeight from the node's config), none of these rules
// apply and history replays exactly as before. Every node must use the same
// value, or nodes disagree at that height.
var rc1113RulesHeight atomic.Uint64

// SetRC1113RulesHeight sets the activation height (0 = never).
func SetRC1113RulesHeight(h uint64) { rc1113RulesHeight.Store(h) }

// RC1113RulesHeight returns the configured activation height (0 = never).
func RC1113RulesHeight() uint64 { return rc1113RulesHeight.Load() }

// RC1113RulesActiveAt reports whether the rc1113 rules bind at height h.
func RC1113RulesActiveAt(h uint64) bool {
	act := rc1113RulesHeight.Load()
	return act != 0 && h >= act
}

// ErrTaskRegisterNotFresh refuses catalog-register on a task ID that already
// holds stake, a reward pool or participants without a manifest.
var ErrTaskRegisterNotFresh = errors.New("chain: catalog-register needs a task ID with no stake, pool or participants")

// checkTaskRegisterFreshLocked is rc1113 rule 1 (sweep 2026-11-16, the C7/R1
// authority class): stake and fund create a task record for any task ID, and
// catalog-register was refused only when a MANIFEST existed. So whoever
// registered first became the manager of a task ID others had already staked
// in or funded, and from then on controlled its manifest. From the rc1113 height
// a register needs a fresh ID: no stake, no pool, no participants.
// Caller holds s.mu.
func (s *TaskStateStore) checkTaskRegisterFreshLocked(action TaskAction, currentHeight uint64) error {
	if action.Action != TaskActionCatalogRegister || !RC1113RulesActiveAt(currentHeight) {
		return nil
	}
	t := s.tasks[action.TaskID]
	if t == nil || t.Manifest != nil {
		return nil // fresh, or ErrTaskManifestExists applies
	}
	if t.TotalStakeAmount > 0 || t.RewardPoolAmount > 0 || len(t.Participants) > 0 {
		return ErrTaskRegisterNotFresh
	}
	return nil
}
