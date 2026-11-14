package chain

// edge_relay_only.go: the height-gated "relay-only" rule for the three
// built-in resource tasks (HL2 edge, paid edge resource work).
//
// Before the rule, a resource-task `submit` with a non-Relay ("local") proof
// needed only a non-empty algorithm, units and a digest; nothing verified the
// work, the round was client-chosen, and each such submit moved up to
// reward_amount from the task pool to pending, which `claim` then paid out.
// Any staked wallet could therefore drain a funded pool.
//
// At or above the configured activation height, a `submit` to
// qsdm-edge-worker, qsdm-edge-worker-gpu or qsdm-edge-worker-ram is valid only
// as a pooled Relay settlement (source edgepool.SettlementProofSource, with
// every existing settlement check: verified ML-DSA Relay proof, manifest
// authorized_relay_ids, Mother Hive sender, production ecosystem reserve,
// slot/round binding, time window and the global proof/receipt replay maps).
// Every other submit to these tasks is refused, whatever its reward (a zero
// reward local proof is refused too: these tasks have no unverified submit
// any more). Other task IDs are unaffected.
//
// Other paths that touch a resource-task pool, at or above the height:
//   - claim pays only reward that is already pending (moved there by a
//     submit that was valid when it was committed, below the height); it
//     cannot move pool to pending. Pooled settlements are paid at submit
//     and are never pending, so they cannot be claimed twice.
//   - the legacy proof shape is accepted only below
//     ResourceWorkerProofActivationHeight (155103), far below any H.
//   - qsdm-edge-pool / qsdm-edge-relay (v1) proofs already fail closed
//     (ErrPooledResourceProofNotEnforceable); they are now refused earlier.
//   - the resource policy is keyed by the exact task ID, not by the manifest
//     capability, so no catalog or generic-proof manifest can route these IDs
//     around it. A differently spelled ID is a different task with its own
//     pool.
//   - fund, stake, unstake/withdraw, start, stop, migrate and the catalog
//     actions never move pool to pending.
//
// Below the height nothing changes, so history replays byte-identically. Zero
// (the default) never activates.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/blackbeardONE/QSDM/pkg/edgepool"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// ErrEdgeRelayOnly refuses a non-Relay submit to a built-in resource task at
// or above the edge relay-only activation height.
var ErrEdgeRelayOnly = errors.New("chain: resource task accepts only pooled Relay settlements at this height")

// EdgeRelayOnlyAdmissionLeadBlocks is how many blocks before the activation
// height mempool admission starts refusing non-Relay resource submits, as
// PoEAdmissionLeadBlocks does for PoE: a submit admitted just below the height
// could otherwise wait in the mempool (maximum age 30 minutes, about 180
// blocks) and fail in production once the rule is a consensus rule.
// Admission only; production, validation and replay start exactly at the
// activation height.
const EdgeRelayOnlyAdmissionLeadBlocks = 360

var edgeRelayOnlyActivationHeight atomic.Uint64

// edgeRelayOnlyAdmissionRejected counts submits refused at mempool admission.
var edgeRelayOnlyAdmissionRejected atomic.Uint64

// reservedTaskCatalogAdmissionRejected counts catalog actions for a reserved
// built-in task ID refused at mempool admission (task_catalog.go).
var reservedTaskCatalogAdmissionRejected atomic.Uint64

// ReservedTaskCatalogAdmissionRejected reports that counter.
func ReservedTaskCatalogAdmissionRejected() uint64 {
	return reservedTaskCatalogAdmissionRejected.Load()
}

// SetEdgeRelayOnlyActivationHeight sets the first block height at which the
// built-in resource tasks accept only pooled Relay settlements. Zero (the
// default) never activates. Every validator must use the same value.
func SetEdgeRelayOnlyActivationHeight(h uint64) { edgeRelayOnlyActivationHeight.Store(h) }

// EdgeRelayOnlyActivationHeight reports the configured height (0 = never).
func EdgeRelayOnlyActivationHeight() uint64 { return edgeRelayOnlyActivationHeight.Load() }

// EdgeRelayOnlyActiveAt reports whether the rule governs a block at height.
func EdgeRelayOnlyActiveAt(height uint64) bool {
	h := edgeRelayOnlyActivationHeight.Load()
	return h != 0 && height >= h
}

// EdgeRelayOnlyAdmissionRejected reports how many submits mempool admission
// refused under the rule since the process started.
func EdgeRelayOnlyAdmissionRejected() uint64 { return edgeRelayOnlyAdmissionRejected.Load() }

// IsSystemResourceTask reports whether taskID is one of the three built-in
// resource tasks (exact match).
func IsSystemResourceTask(taskID string) bool {
	_, ok := systemResourceTaskPolicies[taskID]
	return ok
}

// checkEdgeRelayOnlySubmit is the rule for one resource-task submit payload:
// nil only for a pooled Relay settlement source. A payload that does not
// decode is refused with its decode error.
func checkEdgeRelayOnlySubmit(taskID, payload string) error {
	var envelope resourceWorkerProofPayload
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		return fmt.Errorf("%w: %s: decode resource worker proof: %v", ErrEdgeRelayOnly, taskID, err)
	}
	if source := strings.ToLower(strings.TrimSpace(envelope.Source)); source != edgepool.SettlementProofSource {
		return fmt.Errorf("%w: %s submit source %q, want %q", ErrEdgeRelayOnly, taskID, source, edgepool.SettlementProofSource)
	}
	return nil
}

// edgeRelayOnlyAdmissionActiveAt reports whether admission enforces the rule
// for a tx checked against the block at height next.
func edgeRelayOnlyAdmissionActiveAt(next uint64) bool {
	return EdgeRelayOnlyActiveAt(next) || EdgeRelayOnlyActiveAt(next+EdgeRelayOnlyAdmissionLeadBlocks)
}

// CheckEdgeRelayOnlyAdmission refuses a catalog action for a reserved built-in
// task ID from any sender but TaskCatalogReservedManager (always), and applies the
// relay-only rule to a task tx about to enter the
// mempool, as if it were in the next block, from EdgeRelayOnlyAdmissionLeadBlocks
// before the activation height. Other txs, other actions and other task IDs
// pass. If an activation height is configured and the tip is not known yet,
// a resource-task submit that is not a Relay settlement is refused (fail
// closed). It reads only atomics, never bp.mu, so it is safe inside
// mempool.Add.
func (bp *BlockProducer) CheckEdgeRelayOnlyAdmission(tx *mempool.Tx) error {
	if tx == nil || tx.ContractID != TaskContractID {
		return nil
	}
	action, err := DecodeTaskActionTx(tx)
	if err != nil {
		// A tx that does not decode is refused by the applier anyway.
		return nil
	}
	if err := checkReservedTaskCatalogAction(action); err != nil {
		reservedTaskCatalogAdmissionRejected.Add(1)
		return err
	}
	if EdgeRelayOnlyActivationHeight() == 0 || action.Action != "submit" || !IsSystemResourceTask(action.TaskID) {
		return nil
	}
	known := bp != nil && bp.tipHeightSet.Load()
	if known && !edgeRelayOnlyAdmissionActiveAt(bp.tipHeight.Load()+1) {
		return nil
	}
	if err := checkEdgeRelayOnlySubmit(action.TaskID, action.Payload); err != nil {
		edgeRelayOnlyAdmissionRejected.Add(1)
		return err
	}
	return nil
}

// EdgeRelayOnlyAdmission wraps a mempool admission checker so that every task
// tx entering the pool by any path passes CheckEdgeRelayOnlyAdmission. Other
// transactions go straight to next.
func (bp *BlockProducer) EdgeRelayOnlyAdmission(next func(*mempool.Tx) error) func(*mempool.Tx) error {
	return func(tx *mempool.Tx) error {
		if err := bp.CheckEdgeRelayOnlyAdmission(tx); err != nil {
			return err
		}
		if next != nil {
			return next(tx)
		}
		return nil
	}
}
