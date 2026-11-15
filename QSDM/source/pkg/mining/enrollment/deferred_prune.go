package enrollment

// deferred_prune.go: a deferred-bond enrollment that never earned anything is
// removed a week after it was made (rc1112).
//
// A deferred-bond ("use mining earnings") enrollment costs no CELL: only the
// enrollment postage (DeferredBondWorkDifficulty bits of Hashcash, well under a
// second of CPU). Records that never mine would stay in the registry for ever,
// in every node's memory and in the enrollment state root that every block
// recomputes over all records, so junk enrollments could slow every node down.
//
// From DeferredPruneActivationHeight, once per block after its transactions
// (EnrollmentApplier.SweepMaturedEnrollments, which every node runs on every
// block it applies), each active deferred-bond enrollment whose bond fill is
// still exactly zero DeferredPruneAfterBlocks or more after the height it was
// enrolled at is removed, and its node_id and gpu_uuid are free again.
//   - A record holding any bond dust (it earned at least one reward) is kept.
//   - An upfront-bonded record is never touched.
//   - A revoked record is left to the unbond sweep.
//   - There is nothing to pay back: the fill is zero.
// The miner can enroll again (paying the postage again). Below the height
// nothing changes, so history replays as produced.

import (
	"sort"
	"sync/atomic"
)

// DeferredPruneActivationHeight is the first block at which the rule above
// binds every block. It is the rc1112 consensus height.
const DeferredPruneActivationHeight uint64 = 1_190_000

// DeferredPruneAfterBlocks is how long a deferred-bond enrollment may earn
// nothing before it is removed: 7 days at the 10-second block target.
const DeferredPruneAfterBlocks uint64 = 60_480

// deferredPruneFrom holds the activation height in force; tests change it.
var deferredPruneFrom atomic.Uint64

func init() { deferredPruneFrom.Store(DeferredPruneActivationHeight) }

// SetDeferredPruneActivationHeight sets the activation height in force
// (0 = never). Tests only: every validator must use the constant.
func SetDeferredPruneActivationHeight(h uint64) { deferredPruneFrom.Store(h) }

// DeferredPruneHeight reports the activation height in force (0 = never).
func DeferredPruneHeight() uint64 { return deferredPruneFrom.Load() }

// DeferredPruneActiveAt reports whether the rule governs the block at height.
func DeferredPruneActiveAt(height uint64) bool {
	h := deferredPruneFrom.Load()
	return h != 0 && height >= h
}

// DeferredPrune is one record removed by PruneUnfundedDeferred.
type DeferredPrune struct {
	NodeID           string
	Owner            string
	GPUUUID          string
	EnrolledAtHeight uint64
}

// deferredPrunable reports whether rec is removed at height (the rule's
// conditions, apart from the activation height).
func deferredPrunable(rec *EnrollmentRecord, height uint64) bool {
	return rec != nil && rec.Active() &&
		rec.NormalizedBondMode() == BondModeMiningRewards &&
		rec.StakeDust == 0 &&
		height >= rec.EnrolledAtHeight &&
		height-rec.EnrolledAtHeight >= DeferredPruneAfterBlocks
}

// PruneUnfundedDeferred removes, at currentHeight, every record the rule above
// removes and returns them in node_id order. It changes nothing below the
// activation height.
func (s *InMemoryState) PruneUnfundedDeferred(currentHeight uint64) []DeferredPrune {
	if s == nil || !DeferredPruneActiveAt(currentHeight) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for nodeID, rec := range s.byNodeID {
		if deferredPrunable(rec, currentHeight) {
			ids = append(ids, nodeID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Strings(ids)
	out := make([]DeferredPrune, 0, len(ids))
	for _, nodeID := range ids {
		rec := s.byNodeID[nodeID]
		out = append(out, DeferredPrune{NodeID: rec.NodeID, Owner: rec.Owner, GPUUUID: rec.GPUUUID,
			EnrolledAtHeight: rec.EnrolledAtHeight})
		delete(s.byNodeID, nodeID)
		if s.byGPUActive[rec.GPUUUID] == nodeID {
			delete(s.byGPUActive, rec.GPUUUID)
		}
	}
	return out
}
