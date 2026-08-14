package chain

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// The verifier existing is not the fix; the apply path calling it is. This
// drives EnrollmentAwareApplier.ApplyTx -- the function that actually charges
// an account during block replay -- and asserts a forged task action is
// refused there.
//
// Before this wiring, the API verified the envelope and then dropped
// Signature/PublicKey when building the mempool.Tx, so replay had nothing to
// re-check: a proposer could inject task actions against any account and every
// validator would accept them.
func taskApplier(t *testing.T, height uint64) *EnrollmentAwareApplier {
	t.Helper()
	accounts := NewAccountStore()
	a := NewEnrollmentAwareApplier(accounts, nil)
	a.SetTaskStateStore(NewTaskStateStore())
	a.SetHeightFn(func() uint64 { return height })
	return a
}

func taskTx(t *testing.T, action TaskAction, sigHex, pubHex string) *mempool.Tx {
	t.Helper()
	payload, err := json.Marshal(action)
	if err != nil {
		t.Fatalf("marshal action: %v", err)
	}
	return &mempool.Tx{
		ID: action.ID, Sender: action.Sender, Nonce: action.Nonce,
		Payload: payload, ContractID: TaskContractID,
		Signature: sigHex, PublicKey: pubHex,
	}
}

func TestApplyTx_RefusesForgedTaskAction(t *testing.T) {
	genuine, sig, pub := signedTaskAction(t)

	// A proposer raises the amount on an otherwise genuine, signed action.
	forged := genuine
	forged.Amount = 5000

	a := taskApplier(t, 100)
	err := a.ApplyTx(taskTx(t, forged, sig, pub))
	if !errors.Is(err, ErrTaskActionBadSignature) {
		t.Fatalf("the apply path must refuse a forged task action, got: %v", err)
	}

	// And a signature by an unrelated key must not authorise this sender.
	other, otherSig, otherPub := signedTaskAction(t)
	_ = other
	if err := a.ApplyTx(taskTx(t, genuine, otherSig, otherPub)); !errors.Is(err, ErrTaskActionBadSignature) {
		t.Errorf("a signature by an unrelated key must be refused at apply, got: %v", err)
	}
}

// The genuine article must still apply, or the guard is satisfied by refusing
// everything -- which would look identical in the test above.
func TestApplyTx_AcceptsGenuineTaskAction(t *testing.T) {
	genuine, sig, pub := signedTaskAction(t)

	a := taskApplier(t, 100)
	err := a.ApplyTx(taskTx(t, genuine, sig, pub))
	if errors.Is(err, ErrTaskActionBadSignature) || errors.Is(err, ErrTaskActionUnsigned) {
		t.Fatalf("a genuine signed action must pass authentication, got: %v", err)
	}
	// Any remaining error is state-level (insufficient balance, unknown task)
	// and not this test's concern -- authentication is what is under test.
	t.Logf("post-authentication result: %v", err)
}

// An unwired heightFn must refuse, not authenticate at height 0.
//
// Every other test in this file calls SetHeightFn, so none of them could see
// this: the task branch took `h, _ := a.currentHeight()` and dropped the bool,
// while the enrollment, slashing and governance branches beside it all return
// ErrEnrollmentHeightUnset. Height 0 is below every non-zero activation height,
// so the unsigned-refusal gate returned nil for the exact input it exists to
// reject -- on every node whose heightFn was never installed, at every height.
//
// The assertion is the sentinel rather than "some error", because before the
// fix this path did not error at all on the auth check; it fell through to
// state application, which fails for unrelated reasons and would satisfy a
// looser assertion.
func TestApplyTx_RefusesTaskActionWhenHeightIsUnwired(t *testing.T) {
	prev := TaskActionSignatureActivationHeight()
	t.Cleanup(func() { SetTaskActionSignatureActivationHeight(prev) })
	SetTaskActionSignatureActivationHeight(500)

	// Deliberately no SetHeightFn -- this is the wiring bug being modelled.
	a := NewEnrollmentAwareApplier(NewAccountStore(), nil)
	a.SetTaskStateStore(NewTaskStateStore())

	action := TaskAction{
		ID: "unwired-1", Sender: hex.EncodeToString([]byte("some-sender")),
		TaskID: "t", Action: "start", Timestamp: "ts",
	}
	if err := a.ApplyTx(taskTx(t, action, "", "")); !errors.Is(err, ErrEnrollmentHeightUnset) {
		t.Fatalf("an unsigned task action on a node with no heightFn must be refused, "+
			"not authenticated at height 0; want ErrEnrollmentHeightUnset, got: %v", err)
	}
}

// Historical unsigned actions must still replay below the activation height,
// or turning this on would fork every chain that has one.
func TestApplyTx_UnsignedReplaysBelowActivationHeight(t *testing.T) {
	prev := TaskActionSignatureActivationHeight()
	t.Cleanup(func() { SetTaskActionSignatureActivationHeight(prev) })
	SetTaskActionSignatureActivationHeight(500)

	action := TaskAction{
		ID: "old-1", Sender: hex.EncodeToString([]byte("legacy-sender")),
		TaskID: "t", Action: "start", Timestamp: "ts",
	}

	below := taskApplier(t, 499)
	if err := below.ApplyTx(taskTx(t, action, "", "")); errors.Is(err, ErrTaskActionUnsigned) {
		t.Errorf("an unsigned action below the activation height must replay, got: %v", err)
	}

	above := taskApplier(t, 500)
	if err := above.ApplyTx(taskTx(t, action, "", "")); !errors.Is(err, ErrTaskActionUnsigned) {
		t.Errorf("an unsigned action at the activation height must be refused, got: %v", err)
	}
}
