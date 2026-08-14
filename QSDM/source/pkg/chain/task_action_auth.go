package chain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/blackbeardONE/QSDM/pkg/crypto"
)

// Task actions are verified at the HTTP boundary and then the proof is thrown
// away.
//
// /api/v1/tasks/actions/submit-signed derives the sender as
// hex(sha256(public_key)), checks it matches, and verifies the ML-DSA signature
// over the canonicalised envelope. That is a real check. But
// qsdmTaskActionMempoolTx then builds the mempool.Tx without Signature or
// PublicKey, and DecodeTaskActionTx cross-checks only id/sender/nonce against
// the envelope. So by the time EnrollmentAwareApplier.ApplyTx charges an
// account, nothing has authenticated anything: admission is not a consensus
// rule, and a proposer can inject task actions against any account that every
// replaying validator will accept.
//
// This file provides the consensus-side half. Two deliberate properties:
//
//   - A signature that IS present is always verified, and a bad one is
//     rejected. This carries no replay risk, because a historical unsigned tx
//     has nothing to verify and is unaffected.
//   - A signature may be REQUIRED from a configured activation height onward.
//     That is the repo's existing rollout shape (SignedConsensusActivationHeight
//     and friends) and it means an operator can turn enforcement on once they
//     have confirmed their own chain carries no unsigned task actions above
//     that height -- a fact I could not establish from this environment, and
//     which is the whole reason enforcement is height-gated rather than
//     unconditional.

// ErrTaskActionUnsigned is returned when a task action carries no signature at
// or above the height where signatures are required.
var ErrTaskActionUnsigned = errors.New("chain: task action carries no signature")

// ErrTaskActionBadSignature is returned when a task action's signature does not
// verify, or does not bind to the sender.
var ErrTaskActionBadSignature = errors.New("chain: task action signature does not verify")

// taskActionSignatureHeight is the first height at which a task action must
// carry a signature. Zero disables the requirement; signatures that are present
// are still verified.
var taskActionSignatureHeight atomic.Uint64

// SetTaskActionSignatureActivationHeight sets the first height at which task
// actions must be signed. Zero leaves the requirement off.
func SetTaskActionSignatureActivationHeight(h uint64) { taskActionSignatureHeight.Store(h) }

// TaskActionSignatureActivationHeight reports the configured activation height.
func TaskActionSignatureActivationHeight() uint64 { return taskActionSignatureHeight.Load() }

// taskActionSigned mirrors the envelope the API canonicalises before signing:
// the TaskAction fields in order, followed by "signature", which the signer
// blanks rather than omits. Reproduced here rather than imported because
// pkg/api depends on pkg/chain and not the reverse; TestCanonicalTaskAction*
// pins the two byte-for-byte so they cannot drift apart silently.
type taskActionSigned struct {
	ID        string  `json:"id"`
	Sender    string  `json:"sender"`
	TaskID    string  `json:"task_id"`
	Action    string  `json:"action"`
	Amount    float64 `json:"amount,omitempty"`
	Payload   string  `json:"payload,omitempty"`
	Nonce     uint64  `json:"nonce,omitempty"`
	Timestamp string  `json:"timestamp"`
	Signature string  `json:"signature"`
}

// CanonicalTaskActionSigningBytes returns the exact bytes a client signs for a
// task action: the envelope with an empty signature and no public key.
func CanonicalTaskActionSigningBytes(a TaskAction) ([]byte, error) {
	return json.Marshal(taskActionSigned{
		ID: a.ID, Sender: a.Sender, TaskID: a.TaskID, Action: a.Action,
		Amount: a.Amount, Payload: a.Payload, Nonce: a.Nonce,
		Timestamp: a.Timestamp, Signature: "",
	})
}

// VerifyTaskActionSignature authenticates a task action at consensus apply
// time. sigHex and pubHex come from the carrying transaction.
//
// height selects the policy: below the activation height an absent signature is
// permitted (historical transactions replay), at or above it an absent
// signature is refused. A signature that is present is verified at any height.
func VerifyTaskActionSignature(a TaskAction, sigHex, pubHex string, height uint64) error {
	if sigHex == "" || pubHex == "" {
		if h := TaskActionSignatureActivationHeight(); h != 0 && height >= h {
			return fmt.Errorf("%w: required at or above height %d", ErrTaskActionUnsigned, h)
		}
		return nil
	}

	pubBytes, err := hex.DecodeString(pubHex)
	if err != nil {
		return fmt.Errorf("%w: public key is not valid hex", ErrTaskActionBadSignature)
	}
	// The sender is the address form of the key, so a valid signature by an
	// unrelated key cannot authorise someone else's account.
	sum := sha256.Sum256(pubBytes)
	if derived := hex.EncodeToString(sum[:]); derived != a.Sender {
		return fmt.Errorf("%w: sender %q is not hex(sha256(public_key))", ErrTaskActionBadSignature, a.Sender)
	}
	sigBytes, err := hex.DecodeString(sigHex)
	if err != nil {
		return fmt.Errorf("%w: signature is not valid hex", ErrTaskActionBadSignature)
	}
	canonical, err := CanonicalTaskActionSigningBytes(a)
	if err != nil {
		return fmt.Errorf("%w: canonicalise: %v", ErrTaskActionBadSignature, err)
	}

	// Verify-only, matching the sibling pattern at txsig.go:281.
	//
	// The first version routed through wallet.NewWalletService(), which
	// GENERATES a fresh ML-DSA-87 keypair (crypto.NewDilithium ->
	// mldsa87.GenerateKey) purely to satisfy a non-nil guard on a stateless
	// verify. The key was never used. On the block-apply path that is a full
	// keygen per task action -- but the real problem is determinism: a keygen
	// that fails on entropy would make this node reject a validly-signed action
	// that its peers accept, which is a fork, not a slow path.
	d := crypto.NewDilithiumVerifyOnly()
	if d == nil {
		return fmt.Errorf("%w: ML-DSA verifier unavailable", ErrTaskActionBadSignature)
	}
	defer d.Free()

	ok, verr := d.VerifyWithPublicKey(canonical, sigBytes, pubBytes)
	if verr != nil || !ok {
		return ErrTaskActionBadSignature
	}
	return nil
}
