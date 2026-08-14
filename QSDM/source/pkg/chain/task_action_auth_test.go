package chain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/crypto"
)

// Sign a task action the way a client does, then verify it the way consensus
// would. The canonical-bytes test in pkg/api proves the two FORMS match; this
// proves the two HALVES do -- a real key, a real signature, through the real
// verify path.
func signedTaskAction(t *testing.T) (TaskAction, string, string) {
	t.Helper()
	d := crypto.NewDilithium()
	if d == nil {
		t.Skip("ML-DSA signer unavailable in this build")
	}
	t.Cleanup(d.Free)

	pub := d.GetPublicKey()
	sum := sha256.Sum256(pub)
	a := TaskAction{
		ID: "act-1", Sender: hex.EncodeToString(sum[:]), TaskID: "task-1",
		Action: "stake", Amount: 5, Nonce: 2, Timestamp: "2026-08-14T00:00:00Z",
	}
	canonical, err := CanonicalTaskActionSigningBytes(a)
	if err != nil {
		t.Fatalf("canonicalise: %v", err)
	}
	sig, err := d.Sign(canonical)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return a, hex.EncodeToString(sig), hex.EncodeToString(pub)
}

func TestVerifyTaskActionSignature_AcceptsAGenuineSignature(t *testing.T) {
	a, sig, pub := signedTaskAction(t)
	if err := VerifyTaskActionSignature(a, sig, pub, 100); err != nil {
		t.Fatalf("a genuine signature must verify: %v", err)
	}
}

func TestVerifyTaskActionSignature_RejectsTampering(t *testing.T) {
	base, sig, pub := signedTaskAction(t)

	// Each mutation is a way an unauthenticated proposer could profit today:
	// redirect the action, change the amount, replay at a new nonce, or point
	// it at a different task.
	for name, mutate := range map[string]func(*TaskAction){
		"amount raised":  func(a *TaskAction) { a.Amount = 5000 },
		"different task": func(a *TaskAction) { a.TaskID = "task-2" },
		"nonce bumped":   func(a *TaskAction) { a.Nonce = 3 },
		"action changed": func(a *TaskAction) { a.Action = "unstake" },
		"id changed":     func(a *TaskAction) { a.ID = "act-2" },
	} {
		t.Run(name, func(t *testing.T) {
			a := base
			mutate(&a)
			if err := VerifyTaskActionSignature(a, sig, pub, 100); !errors.Is(err, ErrTaskActionBadSignature) {
				t.Errorf("tampered field must be refused, got: %v", err)
			}
		})
	}

	// A valid signature by the WRONG key must not authorise this sender --
	// otherwise anyone with any key could sign anyone's action.
	other := crypto.NewDilithium()
	if other == nil {
		t.Skip("ML-DSA signer unavailable")
	}
	defer other.Free()
	otherCanonical, _ := CanonicalTaskActionSigningBytes(base)
	otherSig, err := other.Sign(otherCanonical)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	err = VerifyTaskActionSignature(base, hex.EncodeToString(otherSig), hex.EncodeToString(other.GetPublicKey()), 100)
	if !errors.Is(err, ErrTaskActionBadSignature) {
		t.Errorf("a signature by an unrelated key must be refused, got: %v", err)
	}
}

// The height gate: absent signatures are tolerated below the activation
// height so historical transactions replay, and refused at or above it.
func TestVerifyTaskActionSignature_HeightGate(t *testing.T) {
	prev := TaskActionSignatureActivationHeight()
	t.Cleanup(func() { SetTaskActionSignatureActivationHeight(prev) })

	a := TaskAction{ID: "x", Sender: "s", TaskID: "t", Action: "start", Timestamp: "ts"}

	SetTaskActionSignatureActivationHeight(0)
	if err := VerifyTaskActionSignature(a, "", "", 999999); err != nil {
		t.Errorf("zero activation height must never require a signature: %v", err)
	}

	SetTaskActionSignatureActivationHeight(500)
	if err := VerifyTaskActionSignature(a, "", "", 499); err != nil {
		t.Errorf("below the activation height an unsigned action must replay: %v", err)
	}
	if err := VerifyTaskActionSignature(a, "", "", 500); !errors.Is(err, ErrTaskActionUnsigned) {
		t.Errorf("at the activation height an unsigned action must be refused, got: %v", err)
	}
	if err := VerifyTaskActionSignature(a, "", "", 501); !errors.Is(err, ErrTaskActionUnsigned) {
		t.Errorf("above the activation height an unsigned action must be refused, got: %v", err)
	}
}
