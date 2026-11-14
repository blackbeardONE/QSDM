package chain

// relay_settlement_scope.go: a Relay settlement belongs to the three built-in
// resource tasks, and a consumed settlement proof or receipt stays consumed on
// every task.
//
// A pooled Relay settlement (source edgepool.SettlementProofSource) is
// verified, paid 70/15/15 and recorded in the global proof and receipt replay
// maps (settledPooledProofs, settledPooledReceipts) on qsdm-edge-worker, -gpu
// and -ram. No other task verifies a settlement.
//
// From RelaySettlementScopeActivationHeight a submit to any task that is not a
// built-in resource task is refused when its payload is a Relay settlement
// envelope, or when its proof names a proof ID or a receipt ID that a
// settlement already consumed, whatever source it declares. Ordinary custom
// submissions are unaffected. The producer's admission refuses settlement
// envelopes on other tasks at once (admission only: it changes no block).
// Below the height nothing changes, so history replays as produced.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/blackbeardONE/QSDM/pkg/edgepool"
)

// RelaySettlementScopeActivationHeight is the first block at which the rule
// above binds every block.
const RelaySettlementScopeActivationHeight uint64 = 1_190_000

// relaySettlementScopeFrom holds the activation height in force; tests lower it.
var relaySettlementScopeFrom atomic.Uint64

func init() { relaySettlementScopeFrom.Store(RelaySettlementScopeActivationHeight) }

// RelaySettlementScopeActiveAt reports whether the rule governs a block at height.
func RelaySettlementScopeActiveAt(height uint64) bool {
	h := relaySettlementScopeFrom.Load()
	return h != 0 && height >= h
}

// RelaySettlementScopeHeight reports the activation height in force (0 = never).
func RelaySettlementScopeHeight() uint64 { return relaySettlementScopeFrom.Load() }

var (
	// ErrRelaySettlementWrongTask refuses a Relay settlement envelope on a task
	// that is not a built-in resource task.
	ErrRelaySettlementWrongTask = errors.New("chain: a Relay settlement is valid only on the built-in resource tasks")
	// ErrRelaySettlementConsumed refuses a submit whose proof names a settlement
	// proof or receipt that was already consumed.
	ErrRelaySettlementConsumed = errors.New("chain: Relay settlement proof or receipt was already consumed")
)

var relaySettlementScopeAdmissionRejected atomic.Uint64

// RelaySettlementScopeAdmissionRejected counts settlement envelopes for other
// tasks that the producer's admission refused since the process started.
func RelaySettlementScopeAdmissionRejected() uint64 {
	return relaySettlementScopeAdmissionRejected.Load()
}

// relaySettlementEnvelope is the part of a submit payload that can carry a
// Relay settlement.
type relaySettlementEnvelope struct {
	Source string          `json:"source"`
	Proof  json.RawMessage `json:"proof"`
}

// decodeRelaySettlementEnvelope decodes a submit payload leniently: a payload
// that is not a JSON object carries no settlement.
func decodeRelaySettlementEnvelope(payload string) (relaySettlementEnvelope, bool) {
	var env relaySettlementEnvelope
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		return relaySettlementEnvelope{}, false
	}
	return env, true
}

// isRelaySettlementSource reports whether env declares the Relay settlement source.
func (env relaySettlementEnvelope) isRelaySettlementSource() bool {
	return strings.ToLower(strings.TrimSpace(env.Source)) == edgepool.SettlementProofSource
}

// checkRelaySettlementScopeLocked is the rule for one submit to a task that is
// not a built-in resource task. Caller holds s.mu.
func (s *TaskStateStore) checkRelaySettlementScopeLocked(taskID, payload string) error {
	env, ok := decodeRelaySettlementEnvelope(payload)
	if !ok {
		return nil
	}
	if env.isRelaySettlementSource() {
		return fmt.Errorf("%w: task %s", ErrRelaySettlementWrongTask, taskID)
	}
	if len(env.Proof) == 0 {
		return nil
	}
	var proof struct {
		ProofID    string   `json:"proof_id"`
		ReceiptIDs []string `json:"receipt_ids"`
	}
	if err := json.Unmarshal(env.Proof, &proof); err != nil {
		return nil
	}
	if id := strings.ToLower(strings.TrimSpace(proof.ProofID)); id != "" {
		if prior, used := s.settledPooledProofs[id]; used {
			return fmt.Errorf("%w: proof %s (settled by action %s)", ErrRelaySettlementConsumed, id, prior)
		}
	}
	for _, receiptID := range proof.ReceiptIDs {
		id := strings.ToLower(strings.TrimSpace(receiptID))
		if prior, used := s.settledPooledReceipts[id]; id != "" && used {
			return fmt.Errorf("%w: receipt %s (settled by proof %s)", ErrRelaySettlementConsumed, id, prior)
		}
	}
	return nil
}

// checkRelaySettlementScopeAdmission refuses, at mempool admission, a Relay
// settlement envelope sent to a task that is not a built-in resource task. It
// reads no store state, so it is safe inside mempool.Add.
func checkRelaySettlementScopeAdmission(action TaskAction) error {
	if relaySettlementScopeFrom.Load() == 0 || action.Action != "submit" || IsSystemResourceTask(action.TaskID) {
		return nil
	}
	if env, ok := decodeRelaySettlementEnvelope(action.Payload); ok && env.isRelaySettlementSource() {
		relaySettlementScopeAdmissionRejected.Add(1)
		return fmt.Errorf("%w: task %s", ErrRelaySettlementWrongTask, action.TaskID)
	}
	return nil
}
