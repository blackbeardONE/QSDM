package chain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	qcrypto "github.com/blackbeardONE/QSDM/pkg/crypto"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/walletrecovery"
)

const (
	// RecoveryCapsuleContractID identifies wallet-authorized registrations of
	// encrypted legacy-wallet recovery capsules.
	RecoveryCapsuleContractID      = "qsdm/wallet-recovery/v1"
	RecoveryCapsuleActionRegister  = "register"
	RecoveryCapsuleMaxPayloadBytes = 32 * 1024
)

var (
	ErrNotRecoveryCapsuleTx     = errors.New("chain: tx is not a wallet recovery capsule action")
	ErrRecoveryCapsuleSignature = errors.New("chain: invalid wallet recovery capsule signature")
	ErrDuplicateRecoveryAction  = errors.New("chain: duplicate wallet recovery capsule action")
	ErrRecoveryLocatorConflict  = errors.New("chain: recovery capsule locator is already owned by another wallet")
)

// RecoveryCapsuleAction is signed by the wallet whose legacy key is protected
// by Capsule. A wallet owns exactly one current locator; registering a new one
// replaces its prior current-state record.
type RecoveryCapsuleAction struct {
	ID        string                       `json:"id"`
	Sender    string                       `json:"sender"`
	Action    string                       `json:"action"`
	Locator   string                       `json:"locator"`
	Capsule   walletrecovery.LegacyCapsule `json:"capsule"`
	Nonce     uint64                       `json:"nonce"`
	Timestamp string                       `json:"timestamp"`
}

// RecoveryCapsuleState is the public, encrypted consensus projection returned
// by the recovery API. It contains no phrase or plaintext key material.
type RecoveryCapsuleState struct {
	Owner        string                       `json:"owner"`
	Locator      string                       `json:"locator"`
	Capsule      walletrecovery.LegacyCapsule `json:"capsule"`
	ActionID     string                       `json:"action_id"`
	RegisteredAt string                       `json:"registered_at"`
}

type RecoveryCapsuleStateStore struct {
	mu         sync.RWMutex
	byLocator  map[string]*RecoveryCapsuleState
	ownerIndex map[string]string
	actionIDs  map[string]struct{}
}

func NewRecoveryCapsuleStateStore() *RecoveryCapsuleStateStore {
	return &RecoveryCapsuleStateStore{
		byLocator:  map[string]*RecoveryCapsuleState{},
		ownerIndex: map[string]string{},
		actionIDs:  map[string]struct{}{},
	}
}

func RecoveryCapsuleActionSigningBytes(action RecoveryCapsuleAction) ([]byte, error) {
	return json.Marshal(action)
}

func DecodeRecoveryCapsuleTx(tx *mempool.Tx) (RecoveryCapsuleAction, error) {
	if tx == nil {
		return RecoveryCapsuleAction{}, errors.New("chain: nil wallet recovery capsule tx")
	}
	if tx.ContractID != RecoveryCapsuleContractID {
		return RecoveryCapsuleAction{}, fmt.Errorf("%w: got %q, want %q", ErrNotRecoveryCapsuleTx, tx.ContractID, RecoveryCapsuleContractID)
	}
	if len(tx.Payload) == 0 || len(tx.Payload) > RecoveryCapsuleMaxPayloadBytes {
		return RecoveryCapsuleAction{}, errors.New("chain: wallet recovery capsule payload is empty or oversized")
	}
	var action RecoveryCapsuleAction
	if err := json.Unmarshal(tx.Payload, &action); err != nil {
		return RecoveryCapsuleAction{}, fmt.Errorf("chain: decode wallet recovery capsule action: %w", err)
	}
	canonical, err := json.Marshal(action)
	if err != nil {
		return RecoveryCapsuleAction{}, fmt.Errorf("chain: canonicalize wallet recovery capsule action: %w", err)
	}
	if !bytes.Equal(canonical, tx.Payload) {
		return RecoveryCapsuleAction{}, errors.New("chain: wallet recovery capsule payload is not canonical JSON")
	}
	if action.ID == "" || action.ID != tx.ID {
		return RecoveryCapsuleAction{}, errors.New("chain: wallet recovery capsule action id does not match tx id")
	}
	if action.Sender == "" || action.Sender != tx.Sender {
		return RecoveryCapsuleAction{}, errors.New("chain: wallet recovery capsule sender does not match tx sender")
	}
	if action.Nonce != tx.Nonce {
		return RecoveryCapsuleAction{}, errors.New("chain: wallet recovery capsule nonce does not match tx nonce")
	}
	return action, nil
}

func VerifyRecoveryCapsuleTx(tx *mempool.Tx) error {
	action, err := DecodeRecoveryCapsuleTx(tx)
	if err != nil {
		return err
	}
	if err := validateRecoveryCapsuleAction(action); err != nil {
		return err
	}
	if tx.Amount != 0 || tx.Fee != 0 || tx.GasLimit != 0 || tx.Recipient != "" {
		return errors.New("chain: wallet recovery capsule action has unexpected transaction fields")
	}
	publicKey, err := hex.DecodeString(strings.TrimSpace(tx.PublicKey))
	if err != nil {
		return fmt.Errorf("%w: public key is not valid hex", ErrRecoveryCapsuleSignature)
	}
	signature, err := hex.DecodeString(strings.TrimSpace(tx.Signature))
	if err != nil {
		return fmt.Errorf("%w: signature is not valid hex", ErrRecoveryCapsuleSignature)
	}
	if len(publicKey) != mldsa87PublicKeyLen || len(signature) != mldsa87SignatureLen {
		return fmt.Errorf("%w: expected ML-DSA-87 key/signature sizes", ErrRecoveryCapsuleSignature)
	}
	address := sha256.Sum256(publicKey)
	if !strings.EqualFold(hex.EncodeToString(address[:]), action.Sender) {
		return fmt.Errorf("%w: public key does not match sender", ErrRecoveryCapsuleSignature)
	}
	message, err := RecoveryCapsuleActionSigningBytes(action)
	if err != nil {
		return fmt.Errorf("chain: canonicalize wallet recovery capsule action: %w", err)
	}
	verifier := qcrypto.NewDilithiumVerifyOnly()
	if verifier == nil {
		return fmt.Errorf("%w: ML-DSA verifier unavailable", ErrRecoveryCapsuleSignature)
	}
	defer verifier.Free()
	ok, err := verifier.VerifyWithPublicKey(message, signature, publicKey)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRecoveryCapsuleSignature, err)
	}
	if !ok {
		return ErrRecoveryCapsuleSignature
	}
	return nil
}

func validateRecoveryCapsuleAction(action RecoveryCapsuleAction) error {
	if !validRecoveryIdentifier(action.ID, 128) {
		return errors.New("chain: invalid wallet recovery capsule action id")
	}
	if !validRecoveryHash(action.Sender) {
		return errors.New("chain: invalid wallet recovery capsule sender")
	}
	if action.Action != RecoveryCapsuleActionRegister {
		return errors.New("chain: unsupported wallet recovery capsule action")
	}
	if !validRecoveryHash(action.Locator) {
		return errors.New("chain: invalid wallet recovery capsule locator")
	}
	if action.Capsule.Locator != action.Locator || action.Capsule.Address != action.Sender {
		return errors.New("chain: wallet recovery capsule metadata does not match action")
	}
	if err := walletrecovery.ValidateLegacyCapsule(action.Capsule); err != nil {
		return err
	}
	actionTime, err := time.Parse(time.RFC3339Nano, action.Timestamp)
	if err != nil {
		return errors.New("chain: invalid wallet recovery capsule action timestamp")
	}
	capsuleTime, _ := time.Parse(time.RFC3339Nano, action.Capsule.CreatedAt)
	if actionTime.Before(capsuleTime) {
		return errors.New("chain: wallet recovery capsule registration predates capsule creation")
	}
	return nil
}

func validRecoveryIdentifier(value string, max int) bool {
	if value == "" || len(value) > max || value != strings.TrimSpace(value) {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' || r == ':' {
			continue
		}
		return false
	}
	return true
}

func validRecoveryHash(value string) bool {
	if value != strings.TrimSpace(value) || value != strings.ToLower(value) {
		return false
	}
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == sha256.Size
}

func RecoveryCapsuleAdmissionChecker(next func(*mempool.Tx) error) func(*mempool.Tx) error {
	return func(tx *mempool.Tx) error {
		if tx == nil {
			return errors.New("wallet recovery admit: nil tx")
		}
		if tx.ContractID != RecoveryCapsuleContractID {
			if next != nil {
				return next(tx)
			}
			return nil
		}
		if err := VerifyRecoveryCapsuleTx(tx); err != nil {
			return fmt.Errorf("wallet recovery admit: %w", err)
		}
		return nil
	}
}

func (s *RecoveryCapsuleStateStore) ApplyTx(tx *mempool.Tx) error {
	return s.ApplyHistoricalTx(tx, 0)
}

func (s *RecoveryCapsuleStateStore) ApplyHistoricalTx(tx *mempool.Tx, _ uint64) error {
	if err := VerifyRecoveryCapsuleTx(tx); err != nil {
		return err
	}
	action, err := DecodeRecoveryCapsuleTx(tx)
	if err != nil {
		return err
	}
	return s.applyAction(action)
}

func (s *RecoveryCapsuleStateStore) ApplyEconomicTx(tx *mempool.Tx, accounts *AccountStore) error {
	if accounts == nil {
		return errors.New("chain: nil AccountStore for wallet recovery capsule action")
	}
	if err := VerifyRecoveryCapsuleTx(tx); err != nil {
		return err
	}
	action, err := DecodeRecoveryCapsuleTx(tx)
	if err != nil {
		return err
	}
	preview := s.ChainReplayClone().(*RecoveryCapsuleStateStore)
	if err := preview.applyAction(action); err != nil {
		return err
	}
	accountSnapshot := accounts.Clone()
	recoverySnapshot := s.ChainReplayClone()
	if err := accounts.ChargeAndBumpNonce(action.Sender, 0, action.Nonce); err != nil {
		return err
	}
	if err := s.applyAction(action); err != nil {
		accounts.RestoreFrom(accountSnapshot)
		_ = s.RestoreFromChainReplay(recoverySnapshot)
		return err
	}
	return nil
}

func (s *RecoveryCapsuleStateStore) applyAction(action RecoveryCapsuleAction) error {
	if s == nil {
		return errors.New("chain: nil wallet recovery capsule state store")
	}
	if err := validateRecoveryCapsuleAction(action); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.actionIDs[action.ID]; exists {
		return ErrDuplicateRecoveryAction
	}
	if existing, exists := s.byLocator[action.Locator]; exists && existing.Owner != action.Sender {
		return ErrRecoveryLocatorConflict
	}
	if priorLocator := s.ownerIndex[action.Sender]; priorLocator != "" && priorLocator != action.Locator {
		delete(s.byLocator, priorLocator)
	}
	state := &RecoveryCapsuleState{
		Owner:        action.Sender,
		Locator:      action.Locator,
		Capsule:      action.Capsule,
		ActionID:     action.ID,
		RegisteredAt: action.Timestamp,
	}
	s.byLocator[action.Locator] = state
	s.ownerIndex[action.Sender] = action.Locator
	s.actionIDs[action.ID] = struct{}{}
	return nil
}

func (s *RecoveryCapsuleStateStore) GetByLocator(locator string) (RecoveryCapsuleState, bool) {
	if s == nil {
		return RecoveryCapsuleState{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.byLocator[strings.ToLower(strings.TrimSpace(locator))]
	if !ok {
		return RecoveryCapsuleState{}, false
	}
	return *state, true
}

func (s *RecoveryCapsuleStateStore) GetByOwner(owner string) (RecoveryCapsuleState, bool) {
	if s == nil {
		return RecoveryCapsuleState{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	locator := s.ownerIndex[strings.ToLower(strings.TrimSpace(owner))]
	state, ok := s.byLocator[locator]
	if !ok {
		return RecoveryCapsuleState{}, false
	}
	return *state, true
}

func (s *RecoveryCapsuleStateStore) Count() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byLocator)
}

func (s *RecoveryCapsuleStateStore) ChainReplayClone() ChainReplayApplier {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	clone := NewRecoveryCapsuleStateStore()
	for locator, state := range s.byLocator {
		copyState := *state
		clone.byLocator[locator] = &copyState
	}
	for owner, locator := range s.ownerIndex {
		clone.ownerIndex[owner] = locator
	}
	for id := range s.actionIDs {
		clone.actionIDs[id] = struct{}{}
	}
	return clone
}

func (s *RecoveryCapsuleStateStore) RestoreFromChainReplay(from ChainReplayApplier) error {
	if s == nil {
		return errors.New("chain: nil wallet recovery capsule state store")
	}
	other, ok := from.(*RecoveryCapsuleStateStore)
	if !ok || other == nil {
		return errors.New("chain: wallet recovery restore expects *RecoveryCapsuleStateStore")
	}
	clone := other.ChainReplayClone().(*RecoveryCapsuleStateStore)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byLocator = clone.byLocator
	s.ownerIndex = clone.ownerIndex
	s.actionIDs = clone.actionIDs
	return nil
}

func (s *RecoveryCapsuleStateStore) StateRoot() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	locators := make([]string, 0, len(s.byLocator))
	for locator := range s.byLocator {
		locators = append(locators, locator)
	}
	sort.Strings(locators)
	h := sha256.New()
	for _, locator := range locators {
		encoded, _ := json.Marshal(s.byLocator[locator])
		h.Write(encoded)
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}
