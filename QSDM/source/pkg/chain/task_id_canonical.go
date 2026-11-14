package chain

// task_id_canonical.go: identifiers that decide payment or identity have one
// spelling (release rc1112, the same activation height as
// relay_settlement_scope.go).
//
//   - A Relay settlement's proof ID and receipt IDs must be canonical: 64
//     lowercase hexadecimal characters, nothing around them, the spelling the
//     global replay maps are keyed by.
//   - A catalog action for a task ID that differs from a reserved built-in
//     task ID only in letter case or surrounding whitespace is refused for
//     every sender: such an ID could only imitate a built-in task.
//
// Both are consensus rules from RelaySettlementScopeActivationHeight; the
// producer's admission refuses at once. Below the height nothing changes.

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/edgepool"
)

// ErrSettlementIDNotCanonical refuses a Relay settlement whose proof ID or
// receipt IDs are not 64 lowercase hexadecimal characters.
var ErrSettlementIDNotCanonical = errors.New("chain: Relay settlement proof and receipt ids must be 64 lowercase hex characters")

// ErrTaskCatalogReservedLookalike refuses a catalog action for a task ID that
// differs from a reserved built-in task ID only in letter case.
var ErrTaskCatalogReservedLookalike = errors.New("chain: task id imitates a reserved built-in task id")

var canonicalHex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// checkSettlementIDsCanonical is the rule for one verified settlement proof.
func checkSettlementIDsCanonical(proof edgepool.PoolProof) error {
	if !canonicalHex64.MatchString(proof.ProofID) {
		return fmt.Errorf("%w: proof id %q", ErrSettlementIDNotCanonical, proof.ProofID)
	}
	for _, id := range proof.ReceiptIDs {
		if !canonicalHex64.MatchString(id) {
			return fmt.Errorf("%w: receipt id %q", ErrSettlementIDNotCanonical, id)
		}
	}
	return nil
}

// checkSettlementIDsCanonicalAdmission applies checkSettlementIDsCanonical at
// mempool admission to a Relay settlement envelope on a built-in resource
// task. A payload that does not decode is left to the applier.
func checkSettlementIDsCanonicalAdmission(action TaskAction) error {
	if relaySettlementScopeFrom.Load() == 0 || action.Action != "submit" || !IsSystemResourceTask(action.TaskID) {
		return nil
	}
	env, ok := decodeRelaySettlementEnvelope(action.Payload)
	if !ok || !env.isRelaySettlementSource() || len(env.Proof) == 0 {
		return nil
	}
	var proof edgepool.PoolProof
	if err := json.Unmarshal(env.Proof, &proof); err != nil {
		return nil
	}
	return checkSettlementIDsCanonical(proof)
}

// isReservedTaskCatalogLookalike reports whether taskID differs from a reserved
// built-in task ID only in letter case or surrounding whitespace.
func isReservedTaskCatalogLookalike(taskID string) bool {
	folded := strings.ToLower(strings.TrimSpace(taskID))
	return folded != taskID && IsReservedTaskCatalogID(folded)
}

// checkReservedTaskCatalogLookalike refuses a catalog action for such an ID.
func checkReservedTaskCatalogLookalike(action TaskAction) error {
	if !isTaskCatalogAction(action.Action) || !isReservedTaskCatalogLookalike(action.TaskID) {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrTaskCatalogReservedLookalike, action.TaskID)
}
