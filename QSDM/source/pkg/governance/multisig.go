package governance

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// MultiSigAction defines a pending multi-sig operation.
type MultiSigAction struct {
	ID          string                 `json:"id"`
	ActionType  ActionType             `json:"action_type"`
	Parameters  map[string]interface{} `json:"parameters"`
	Required    int                    `json:"required"`    // signatures needed
	Signers     []string               `json:"signers"`     // authorised signer addresses
	Signatures  map[string]time.Time   `json:"signatures"`  // address -> signed_at
	CreatedAt   time.Time              `json:"created_at"`
	ExpiresAt   time.Time              `json:"expires_at"`
	Executed    bool                   `json:"executed"`
	ExecutedAt  time.Time              `json:"executed_at,omitempty"`
}

// MultiSig manages N-of-M signature requirements for critical actions.
type MultiSig struct {
	mu       sync.RWMutex
	actions  map[string]*MultiSigAction
	handlers map[ActionType]ActionHandler
	signers  []string // globally authorised signers
	required int      // default required signatures
}

// MultiSigConfig configures the multi-sig system.
type MultiSigConfig struct {
	Signers         []string      `json:"signers"`
	RequiredSigs    int           `json:"required_sigs"`
	ActionExpiry    time.Duration `json:"action_expiry"`
}

// DefaultMultiSigConfig returns a 2-of-3 config with 24h expiry.
func DefaultMultiSigConfig() MultiSigConfig {
	return MultiSigConfig{
		RequiredSigs: 2,
		ActionExpiry: 24 * time.Hour,
	}
}

// NewMultiSig creates a new multi-sig manager.
func NewMultiSig(cfg MultiSigConfig) *MultiSig {
	req := cfg.RequiredSigs
	if req < 1 {
		req = 2
	}
	return &MultiSig{
		actions:  make(map[string]*MultiSigAction),
		handlers: make(map[ActionType]ActionHandler),
		signers:  cfg.Signers,
		required: req,
	}
}

// RegisterHandler registers an executor for a given action type.
func (ms *MultiSig) RegisterHandler(actionType ActionType, handler ActionHandler) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.handlers[actionType] = handler
}

// ProposeAction creates a new multi-sig action requiring N signatures.
func (ms *MultiSig) ProposeAction(proposerAddr string, actionType ActionType, params map[string]interface{}, expiry time.Duration) (*MultiSigAction, error) {
	if !ms.isAuthorisedSigner(proposerAddr) {
		return nil, errors.New("proposer is not an authorised signer")
	}
	if expiry == 0 {
		expiry = 24 * time.Hour
	}

	id := generateActionID(proposerAddr, actionType, params)

	ms.mu.Lock()
	defer ms.mu.Unlock()

	if _, exists := ms.actions[id]; exists {
		return nil, fmt.Errorf("action %s already exists", id)
	}

	action := &MultiSigAction{
		ID:         id,
		ActionType: actionType,
		Parameters: params,
		Required:   ms.required,
		Signers:    append([]string{}, ms.signers...),
		Signatures: map[string]time.Time{proposerAddr: time.Now()},
		CreatedAt:  time.Now(),
		ExpiresAt:  time.Now().Add(expiry),
	}
	ms.actions[id] = action
	return action, nil
}

// Sign adds a signature to a pending action. Returns true if threshold is now met.
func (ms *MultiSig) Sign(actionID, signerAddr string) (bool, error) {
	if !ms.isAuthorisedSigner(signerAddr) {
		return false, errors.New("signer is not authorised")
	}

	ms.mu.Lock()
	defer ms.mu.Unlock()

	action, exists := ms.actions[actionID]
	if !exists {
		return false, fmt.Errorf("action %s not found", actionID)
	}
	if action.Executed {
		return false, errors.New("action already executed")
	}
	if time.Now().After(action.ExpiresAt) {
		return false, errors.New("action has expired")
	}
	if _, already := action.Signatures[signerAddr]; already {
		return false, errors.New("already signed by this address")
	}
	if !ms.isSignerForAction(action, signerAddr) {
		return false, errors.New("signer not authorised for this action")
	}

	action.Signatures[signerAddr] = time.Now()
	return len(action.Signatures) >= action.Required, nil
}

// Execute runs the action if the signature threshold is met.
func (ms *MultiSig) Execute(actionID string) error {
	ms.mu.Lock()
	action, exists := ms.actions[actionID]
	if !exists {
		ms.mu.Unlock()
		return fmt.Errorf("action %s not found", actionID)
	}
	if action.Executed {
		ms.mu.Unlock()
		return errors.New("action already executed")
	}
	if time.Now().After(action.ExpiresAt) {
		ms.mu.Unlock()
		return errors.New("action has expired")
	}
	if len(action.Signatures) < action.Required {
		ms.mu.Unlock()
		return fmt.Errorf("need %d signatures, have %d", action.Required, len(action.Signatures))
	}

	handler, hasHandler := ms.handlers[action.ActionType]
	if !hasHandler {
		ms.mu.Unlock()
		return fmt.Errorf("no handler registered for action type %s", action.ActionType)
	}

	action.Executed = true
	action.ExecutedAt = time.Now()
	ms.mu.Unlock()

	return handler(actionID, action.Parameters)
}

// GetAction returns action details.
func (ms *MultiSig) GetAction(actionID string) (*MultiSigAction, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	action, exists := ms.actions[actionID]
	if !exists {
		return nil, fmt.Errorf("action %s not found", actionID)
	}
	return action, nil
}

// PendingActions returns all non-executed, non-expired actions.
func (ms *MultiSig) PendingActions() []*MultiSigAction {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	now := time.Now()
	var pending []*MultiSigAction
	for _, a := range ms.actions {
		if !a.Executed && now.Before(a.ExpiresAt) {
			pending = append(pending, a)
		}
	}
	return pending
}

func (ms *MultiSig) isAuthorisedSigner(addr string) bool {
	for _, s := range ms.signers {
		if s == addr {
			return true
		}
	}
	return false
}

func (ms *MultiSig) isSignerForAction(action *MultiSigAction, addr string) bool {
	for _, s := range action.Signers {
		if s == addr {
			return true
		}
	}
	return false
}

func generateActionID(proposer string, actionType ActionType, params map[string]interface{}) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	raw := fmt.Sprintf("%s:%s:%v:%d", proposer, actionType, keys, time.Now().UnixNano())
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:16])
}
