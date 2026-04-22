package governance

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestMultiSig_ProposeAndSign(t *testing.T) {
	ms := NewMultiSig(MultiSigConfig{
		Signers:      []string{"alice", "bob", "carol"},
		RequiredSigs: 2,
	})

	action, err := ms.ProposeAction("alice", ActionConfigChange, map[string]interface{}{"key": "val"}, time.Hour)
	if err != nil {
		t.Fatalf("ProposeAction: %v", err)
	}
	if len(action.Signatures) != 1 {
		t.Fatal("proposer should auto-sign")
	}

	ready, err := ms.Sign(action.ID, "bob")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if !ready {
		t.Fatal("expected threshold met after 2nd signature")
	}
}

func TestMultiSig_Execute(t *testing.T) {
	ms := NewMultiSig(MultiSigConfig{
		Signers:      []string{"alice", "bob"},
		RequiredSigs: 2,
	})

	var executed int32
	ms.RegisterHandler(ActionConfigChange, func(id string, params map[string]interface{}) error {
		atomic.AddInt32(&executed, 1)
		return nil
	})

	action, _ := ms.ProposeAction("alice", ActionConfigChange, map[string]interface{}{"x": 1}, time.Hour)
	ms.Sign(action.ID, "bob")

	if err := ms.Execute(action.ID); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if atomic.LoadInt32(&executed) != 1 {
		t.Fatal("handler should have been called once")
	}

	if err := ms.Execute(action.ID); err == nil {
		t.Fatal("double execution should fail")
	}
}

func TestMultiSig_InsufficientSignatures(t *testing.T) {
	ms := NewMultiSig(MultiSigConfig{
		Signers:      []string{"alice", "bob", "carol"},
		RequiredSigs: 3,
	})

	action, _ := ms.ProposeAction("alice", ActionContractUpgrade, nil, time.Hour)
	ms.Sign(action.ID, "bob")

	ms.RegisterHandler(ActionContractUpgrade, func(id string, params map[string]interface{}) error { return nil })

	if err := ms.Execute(action.ID); err == nil {
		t.Fatal("expected error with only 2 of 3 signatures")
	}
}

func TestMultiSig_UnauthorisedSigner(t *testing.T) {
	ms := NewMultiSig(MultiSigConfig{
		Signers:      []string{"alice", "bob"},
		RequiredSigs: 2,
	})

	_, err := ms.ProposeAction("mallory", ActionConfigChange, nil, time.Hour)
	if err == nil {
		t.Fatal("expected error for unauthorised proposer")
	}
}

func TestMultiSig_ExpiredAction(t *testing.T) {
	ms := NewMultiSig(MultiSigConfig{
		Signers:      []string{"alice", "bob"},
		RequiredSigs: 2,
	})

	action, _ := ms.ProposeAction("alice", ActionConfigChange, nil, 1*time.Millisecond)
	time.Sleep(5 * time.Millisecond)

	_, err := ms.Sign(action.ID, "bob")
	if err == nil {
		t.Fatal("expected error for expired action")
	}
}

func TestMultiSig_DuplicateSignature(t *testing.T) {
	ms := NewMultiSig(MultiSigConfig{
		Signers:      []string{"alice", "bob"},
		RequiredSigs: 2,
	})

	action, _ := ms.ProposeAction("alice", ActionConfigChange, nil, time.Hour)
	_, err := ms.Sign(action.ID, "alice")
	if err == nil {
		t.Fatal("expected error for duplicate signature")
	}
}

func TestMultiSig_PendingActions(t *testing.T) {
	ms := NewMultiSig(MultiSigConfig{
		Signers:      []string{"alice", "bob"},
		RequiredSigs: 2,
	})

	ms.ProposeAction("alice", ActionConfigChange, map[string]interface{}{"a": 1}, time.Hour)
	ms.ProposeAction("alice", ActionContractUpgrade, map[string]interface{}{"b": 2}, time.Hour)

	pending := ms.PendingActions()
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending, got %d", len(pending))
	}
}
