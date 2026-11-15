package chain

// custom_task_pay.go: a task that is not a built-in resource task pays nothing
// on an unverified submit (rc1112).
//
// A submit to a custom task moves reward_amount from the task's pool to the
// submitter's pending balance, which `claim` then pays out; consensus has no
// way to verify the work behind it. The three built-in resource tasks pay only
// through a verified Relay settlement (edge_relay_only.go), and from
// RelaySettlementScopeActivationHeight a Relay settlement is valid only on
// those three (relay_settlement_scope.go). No verified way for a custom task
// to pay exists yet, so from CustomTaskPayActivationHeight a submit to any
// other task that asks for a reward above zero is refused: no pool CELL moves
// to pending. A submit with reward 0 is still recorded, `fund`, `stake`,
// `withdraw` and the catalog actions are unchanged, and reward booked to
// pending below the height stays claimable.
//
// The inactive non-resource built-in tasks follow the same rule: inactive,
// they refuse every submit anyway; reactivated, they could record work but pay
// nothing until a verified design for them ships. Their pools stay where they
// are.
//
// The producer's admission refuses such submits at once (admission only: it
// changes no block). Below the height nothing changes, so history replays as
// produced.

import (
	"errors"
	"fmt"
	"sync/atomic"
)

// CustomTaskPayActivationHeight is the first block at which the rule above
// binds every block. It is the rc1112 height of the Relay settlement scope.
const CustomTaskPayActivationHeight uint64 = 1_190_000

// customTaskPayFrom holds the activation height in force; tests lower it.
var customTaskPayFrom atomic.Uint64

func init() { customTaskPayFrom.Store(CustomTaskPayActivationHeight) }

// CustomTaskPayActiveAt reports whether the rule governs a block at height.
func CustomTaskPayActiveAt(height uint64) bool {
	h := customTaskPayFrom.Load()
	return h != 0 && height >= h
}

// CustomTaskPayHeight reports the activation height in force (0 = never).
func CustomTaskPayHeight() uint64 { return customTaskPayFrom.Load() }

// ErrCustomTaskUnverifiedPay refuses a submit that asks a task other than the
// built-in resource tasks for a reward.
var ErrCustomTaskUnverifiedPay = errors.New("chain: a submit to this task cannot ask for a reward: only verified work is paid, and custom tasks have no verified settlement yet")

var customTaskPayAdmissionRejected atomic.Uint64

// CustomTaskPayAdmissionRejected counts such submits refused at mempool
// admission since the process started.
func CustomTaskPayAdmissionRejected() uint64 { return customTaskPayAdmissionRejected.Load() }

// checkCustomTaskPaySubmit is the rule for one submit: nil for a built-in
// resource task (which has its own rules) or a reward of zero.
func checkCustomTaskPaySubmit(taskID string, rewardAmount float64) error {
	if IsSystemResourceTask(taskID) || !(rewardAmount > 0) {
		return nil
	}
	return fmt.Errorf("%w (task %s, reward_amount %.8f)", ErrCustomTaskUnverifiedPay, taskID, rewardAmount)
}

// checkCustomTaskPayAdmission refuses, at mempool admission, a submit that asks
// a task other than the built-in resource tasks for a reward. It reads no store
// state, so it is safe inside mempool.Add.
func checkCustomTaskPayAdmission(action TaskAction) error {
	if customTaskPayFrom.Load() == 0 || action.Action != "submit" {
		return nil
	}
	if err := checkCustomTaskPaySubmit(action.TaskID, parseTaskActionPayload(action.Payload).RewardAmount); err != nil {
		customTaskPayAdmissionRejected.Add(1)
		return err
	}
	return nil
}
