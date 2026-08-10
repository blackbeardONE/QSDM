package chain

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"

	qcrypto "github.com/blackbeardONE/QSDM/pkg/crypto"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// StreamContractID identifies consensus-enforced CELL metered-payment streams.
const StreamContractID = "qsdm/streams/v1"

const (
	StreamActionOpen    = "open"
	StreamActionReceipt = "receipt"
	StreamActionPause   = "pause"
	StreamActionResume  = "resume"
	StreamActionSettle  = "settle"
	StreamActionClose   = "close"

	StreamStatusActive = "active"
	StreamStatusPaused = "paused"
	StreamStatusClosed = "closed"
)

var (
	ErrNotStreamActionTx       = errors.New("chain: tx is not a CELL stream action")
	ErrDuplicateStreamAction   = errors.New("chain: duplicate CELL stream action")
	ErrDuplicateStream         = errors.New("chain: CELL stream already exists")
	ErrStreamNotFound          = errors.New("chain: CELL stream not found")
	ErrStreamClosed            = errors.New("chain: CELL stream is closed")
	ErrStreamNotActive         = errors.New("chain: CELL stream is not active")
	ErrStreamReceiptReplay     = errors.New("chain: CELL stream receipt replay")
	ErrStreamBudgetExceeded    = errors.New("chain: CELL stream budget exceeded")
	ErrStreamUsageCapExceeded  = errors.New("chain: CELL stream active-time cap exceeded")
	ErrStreamWallTimeExceeded  = errors.New("chain: CELL stream receipt exceeds elapsed active wall time")
	ErrNoStreamSettlement      = errors.New("chain: no unsettled CELL stream charge")
	ErrStreamSignatureInvalid  = errors.New("chain: invalid CELL stream wallet signature")
	ErrReceiptSignatureInvalid = errors.New("chain: invalid CELL stream session signature")
)

// StreamUsageReceipt is a cumulative active-use acknowledgement signed by the
// payer-authorized ephemeral Ed25519 session key. Cumulative counters avoid
// rounding loss and make a replayed or reordered receipt unambiguous.
type StreamUsageReceipt struct {
	StreamID                string `json:"stream_id"`
	Sequence                uint64 `json:"sequence"`
	CumulativeActiveSeconds uint64 `json:"cumulative_active_seconds"`
	ObservedAt              string `json:"observed_at"`
	Signature               string `json:"signature,omitempty"`
}

// StreamAction is the wallet-signed consensus command for a CELL stream.
//
// Open authorizes a bounded escrow and an ephemeral session key. Receipt
// carries a session-signed cumulative usage counter. Pause/resume/close are
// payer controls; receipt/settle are provider controls.
type StreamAction struct {
	ID        string `json:"id"`
	Sender    string `json:"sender"`
	StreamID  string `json:"stream_id"`
	Action    string `json:"action"`
	Provider  string `json:"provider,omitempty"`
	ServiceID string `json:"service_id,omitempty"`

	// DeviceIDHash identifies the billed installation without publishing a
	// raw device identifier.
	DeviceIDHash string `json:"device_id_hash,omitempty"`

	// SessionPublicKey is a 32-byte Ed25519 public key encoded as lower-case
	// hex. It can acknowledge usage only inside this stream's fixed limits.
	SessionPublicKey string `json:"session_public_key,omitempty"`

	// PriceDust / PricePeriodSeconds is the exact rational CELL-dust rate.
	// Example: 2 CELL per 30 active days is 200,000,000 / 2,592,000.
	PriceDust          uint64 `json:"price_dust,omitempty"`
	PricePeriodSeconds uint64 `json:"price_period_seconds,omitempty"`
	BudgetDust         uint64 `json:"budget_dust,omitempty"`
	MaxActiveSeconds   uint64 `json:"max_active_seconds,omitempty"`
	ExpiresAt          string `json:"expires_at,omitempty"`

	Receipt   *StreamUsageReceipt `json:"receipt,omitempty"`
	Nonce     uint64              `json:"nonce,omitempty"`
	Timestamp string              `json:"timestamp"`
}

// StreamState is the consensus projection of one metered-payment stream.
// Every monetary field is integer dust; 1 CELL = DustPerCell dust.
type StreamState struct {
	StreamID           string `json:"stream_id"`
	Payer              string `json:"payer"`
	Provider           string `json:"provider"`
	ServiceID          string `json:"service_id"`
	DeviceIDHash       string `json:"device_id_hash"`
	SessionPublicKey   string `json:"session_public_key"`
	PriceDust          uint64 `json:"price_dust"`
	PricePeriodSeconds uint64 `json:"price_period_seconds"`
	BudgetDust         uint64 `json:"budget_dust"`
	MaxActiveSeconds   uint64 `json:"max_active_seconds"`
	ExpiresAt          string `json:"expires_at"`
	Status             string `json:"status"`

	CumulativeActiveSeconds uint64 `json:"cumulative_active_seconds"`
	PausedDurationSeconds   uint64 `json:"paused_duration_seconds"`
	LastReceiptSequence     uint64 `json:"last_receipt_sequence"`
	LastReceiptObservedAt   string `json:"last_receipt_observed_at,omitempty"`
	AccruedDust             uint64 `json:"accrued_dust"`
	SettledDust             uint64 `json:"settled_dust"`
	RefundedDust            uint64 `json:"refunded_dust"`

	OpenedAt      string `json:"opened_at"`
	LastPausedAt  string `json:"last_paused_at,omitempty"`
	LastResumedAt string `json:"last_resumed_at,omitempty"`
	ClosedAt      string `json:"closed_at,omitempty"`
	LastAction    string `json:"last_action"`
	LastActionID  string `json:"last_action_id"`
	LastActionAt  string `json:"last_action_at"`
	ActionCount   uint64 `json:"action_count"`
}

// RemainingBudgetDust returns escrow that has not yet been consumed by
// cumulative active use.
func (s StreamState) RemainingBudgetDust() uint64 {
	if s.AccruedDust >= s.BudgetDust {
		return 0
	}
	return s.BudgetDust - s.AccruedDust
}

type StreamStateStore struct {
	mu        sync.RWMutex
	streams   map[string]*StreamState
	actionIDs map[string]struct{}
}

type streamEconomicEffect struct {
	Provider        string
	ProviderDust    uint64
	Payer           string
	PayerRefundDust uint64
}

func NewStreamStateStore() *StreamStateStore {
	return &StreamStateStore{
		streams:   map[string]*StreamState{},
		actionIDs: map[string]struct{}{},
	}
}

// StreamActionSigningBytes returns the canonical bytes signed by a QSDM
// ML-DSA wallet. The struct field order is the protocol signing contract.
func StreamActionSigningBytes(action StreamAction) ([]byte, error) {
	return json.Marshal(action)
}

// StreamUsageReceiptSigningBytes returns the canonical session-signing bytes.
// Signature is deliberately cleared so it cannot sign itself.
func StreamUsageReceiptSigningBytes(receipt StreamUsageReceipt) ([]byte, error) {
	receipt.Signature = ""
	return json.Marshal(receipt)
}

// DecodeStreamActionTx decodes the action and binds its identity fields to the
// outer transaction so there is only one possible interpretation.
func DecodeStreamActionTx(tx *mempool.Tx) (StreamAction, error) {
	if tx == nil {
		return StreamAction{}, errors.New("chain: nil CELL stream tx")
	}
	if tx.ContractID != StreamContractID {
		return StreamAction{}, fmt.Errorf("%w: got %q, want %q", ErrNotStreamActionTx, tx.ContractID, StreamContractID)
	}
	var action StreamAction
	if err := json.Unmarshal(tx.Payload, &action); err != nil {
		return StreamAction{}, fmt.Errorf("chain: decode CELL stream action: %w", err)
	}
	canonicalPayload, err := json.Marshal(action)
	if err != nil {
		return StreamAction{}, fmt.Errorf("chain: canonicalize CELL stream action: %w", err)
	}
	if !bytes.Equal(tx.Payload, canonicalPayload) {
		return StreamAction{}, errors.New("chain: CELL stream payload is not canonical JSON")
	}
	if action.ID == "" || tx.ID == "" || action.ID != tx.ID {
		return StreamAction{}, fmt.Errorf("chain: stream action id %q does not match tx id %q", action.ID, tx.ID)
	}
	if action.Sender == "" || tx.Sender == "" || action.Sender != tx.Sender {
		return StreamAction{}, fmt.Errorf("chain: stream sender %q does not match tx sender %q", action.Sender, tx.Sender)
	}
	if action.Nonce != tx.Nonce {
		return StreamAction{}, fmt.Errorf("chain: stream nonce %d does not match tx nonce %d", action.Nonce, tx.Nonce)
	}
	return action, nil
}

// VerifyStreamActionTx performs the stateless signature and shape checks used
// both at mempool admission and again during consensus application.
func VerifyStreamActionTx(tx *mempool.Tx) error {
	action, err := DecodeStreamActionTx(tx)
	if err != nil {
		return err
	}
	if err := validateStreamActionShape(action); err != nil {
		return err
	}
	if tx.Fee != 0 {
		return errors.New("chain: CELL stream actions require a zero transaction fee")
	}
	if tx.GasLimit != 0 || tx.Recipient != "" {
		return errors.New("chain: CELL stream action has unexpected transaction fields")
	}
	if tx.Amount != 0 {
		return errors.New("chain: CELL stream escrow is carried only by signed budget_dust; tx amount must be zero")
	}

	publicKey, err := hex.DecodeString(strings.TrimSpace(tx.PublicKey))
	if err != nil {
		return fmt.Errorf("%w: public key is not valid hex", ErrStreamSignatureInvalid)
	}
	signature, err := hex.DecodeString(strings.TrimSpace(tx.Signature))
	if err != nil {
		return fmt.Errorf("%w: signature is not valid hex", ErrStreamSignatureInvalid)
	}
	if len(publicKey) != mldsa87PublicKeyLen || len(signature) != mldsa87SignatureLen {
		return fmt.Errorf("%w: expected ML-DSA-87 key/signature sizes", ErrStreamSignatureInvalid)
	}
	address := sha256.Sum256(publicKey)
	if !strings.EqualFold(hex.EncodeToString(address[:]), action.Sender) {
		return fmt.Errorf("%w: public key does not match sender", ErrStreamSignatureInvalid)
	}
	message, err := StreamActionSigningBytes(action)
	if err != nil {
		return fmt.Errorf("chain: canonicalize stream action: %w", err)
	}
	verifier := qcrypto.NewDilithiumVerifyOnly()
	if verifier == nil {
		return fmt.Errorf("%w: ML-DSA verifier unavailable", ErrStreamSignatureInvalid)
	}
	defer verifier.Free()
	ok, err := verifier.VerifyWithPublicKey(message, signature, publicKey)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrStreamSignatureInvalid, err)
	}
	if !ok {
		return ErrStreamSignatureInvalid
	}
	return nil
}

// StreamAdmissionChecker rejects malformed or unsigned stream transactions
// before they consume mempool space. Stateful checks remain in ApplyEconomicTx.
func StreamAdmissionChecker(next func(*mempool.Tx) error) func(*mempool.Tx) error {
	return func(tx *mempool.Tx) error {
		if tx == nil {
			return errors.New("stream admit: nil tx")
		}
		if tx.ContractID != StreamContractID {
			if next != nil {
				return next(tx)
			}
			return nil
		}
		if err := VerifyStreamActionTx(tx); err != nil {
			return fmt.Errorf("stream admit: %w", err)
		}
		return nil
	}
}

func validateStreamActionShape(action StreamAction) error {
	normalizedAction := strings.ToLower(strings.TrimSpace(action.Action))
	if action.Action != normalizedAction {
		return errors.New("chain: CELL stream action must use its canonical lower-case spelling")
	}
	if !validStreamIdentifier(action.ID, 128) {
		return errors.New("chain: invalid CELL stream action id")
	}
	if !validStreamIdentifier(action.StreamID, 128) {
		return errors.New("chain: invalid CELL stream id")
	}
	if !validStreamWalletAddress(action.Sender) {
		return errors.New("chain: invalid CELL stream sender")
	}
	actionTime, err := time.Parse(time.RFC3339Nano, action.Timestamp)
	if err != nil {
		return errors.New("chain: invalid CELL stream action timestamp")
	}
	switch action.Action {
	case StreamActionOpen:
		if !validStreamWalletAddress(action.Provider) {
			return errors.New("chain: invalid CELL stream provider")
		}
		if strings.EqualFold(action.Sender, action.Provider) {
			return errors.New("chain: CELL stream payer and provider must differ")
		}
		if !validStreamIdentifier(action.ServiceID, 128) {
			return errors.New("chain: invalid CELL stream service id")
		}
		if !validSHA256Hex(action.DeviceIDHash) {
			return errors.New("chain: device_id_hash must be a 32-byte hex digest")
		}
		if action.SessionPublicKey != strings.TrimSpace(action.SessionPublicKey) ||
			action.SessionPublicKey != strings.ToLower(action.SessionPublicKey) {
			return errors.New("chain: session_public_key must use lower-case hex")
		}
		sessionKey, err := hex.DecodeString(action.SessionPublicKey)
		if err != nil || len(sessionKey) != ed25519.PublicKeySize {
			return errors.New("chain: session_public_key must be a 32-byte Ed25519 key in hex")
		}
		if action.PriceDust == 0 || action.PricePeriodSeconds == 0 {
			return errors.New("chain: CELL stream price and period must be positive")
		}
		if action.BudgetDust == 0 || action.MaxActiveSeconds == 0 {
			return errors.New("chain: CELL stream budget and active-time cap must be positive")
		}
		expiresAt, err := time.Parse(time.RFC3339Nano, action.ExpiresAt)
		if err != nil || !expiresAt.After(actionTime) {
			return errors.New("chain: CELL stream expiry must be after its open timestamp")
		}
		if action.Receipt != nil {
			return errors.New("chain: open stream action cannot contain a usage receipt")
		}
	case StreamActionReceipt:
		if hasStreamOpenFields(action) {
			return errors.New("chain: receipt stream action contains open-only fields")
		}
		if action.Receipt == nil {
			return errors.New("chain: receipt stream action requires receipt")
		}
		if action.Receipt.StreamID != action.StreamID {
			return errors.New("chain: receipt stream_id does not match action stream_id")
		}
		if action.Receipt.Sequence == 0 || action.Receipt.CumulativeActiveSeconds == 0 {
			return errors.New("chain: receipt sequence and cumulative active seconds must be positive")
		}
		if _, err := time.Parse(time.RFC3339Nano, action.Receipt.ObservedAt); err != nil {
			return errors.New("chain: invalid stream receipt observed_at")
		}
		if strings.TrimSpace(action.Receipt.Signature) == "" {
			return errors.New("chain: stream receipt signature is required")
		}
	case StreamActionPause, StreamActionResume, StreamActionSettle, StreamActionClose:
		if hasStreamOpenFields(action) {
			return errors.New("chain: lifecycle stream action contains open-only fields")
		}
		if action.Receipt != nil {
			return errors.New("chain: non-receipt stream action contains a receipt")
		}
	default:
		return fmt.Errorf("chain: unsupported CELL stream action %q", action.Action)
	}
	return nil
}

func hasStreamOpenFields(action StreamAction) bool {
	return action.Provider != "" ||
		action.ServiceID != "" ||
		action.DeviceIDHash != "" ||
		action.SessionPublicKey != "" ||
		action.PriceDust != 0 ||
		action.PricePeriodSeconds != 0 ||
		action.BudgetDust != 0 ||
		action.MaxActiveSeconds != 0 ||
		action.ExpiresAt != ""
}

func validStreamIdentifier(value string, max int) bool {
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

func validStreamWalletAddress(value string) bool {
	if value != strings.TrimSpace(value) || value != strings.ToLower(value) {
		return false
	}
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == sha256.Size
}

func validSHA256Hex(value string) bool {
	if value != strings.TrimSpace(value) || value != strings.ToLower(value) {
		return false
	}
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == sha256.Size
}

func streamChargeDust(activeSeconds, priceDust, periodSeconds uint64) (uint64, error) {
	if periodSeconds == 0 {
		return 0, errors.New("chain: CELL stream price period is zero")
	}
	numerator := new(big.Int).SetUint64(activeSeconds)
	numerator.Mul(numerator, new(big.Int).SetUint64(priceDust))
	charge := numerator.Quo(numerator, new(big.Int).SetUint64(periodSeconds))
	if !charge.IsUint64() {
		return 0, errors.New("chain: CELL stream charge overflows uint64 dust")
	}
	return charge.Uint64(), nil
}

func verifySessionReceipt(state *StreamState, receipt StreamUsageReceipt) error {
	publicKey, err := hex.DecodeString(state.SessionPublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return ErrReceiptSignatureInvalid
	}
	signature, err := hex.DecodeString(receipt.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return ErrReceiptSignatureInvalid
	}
	message, err := StreamUsageReceiptSigningBytes(receipt)
	if err != nil {
		return ErrReceiptSignatureInvalid
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), message, signature) {
		return ErrReceiptSignatureInvalid
	}
	return nil
}

func (s *StreamStateStore) ApplyTx(tx *mempool.Tx) error {
	return s.ApplyHistoricalTx(tx, 0)
}

// ApplyHistoricalTx rebuilds stream projection from a committed block without
// touching accounts. Account balances/nonces come from the matching persisted
// account snapshot.
func (s *StreamStateStore) ApplyHistoricalTx(tx *mempool.Tx, _ uint64) error {
	if err := VerifyStreamActionTx(tx); err != nil {
		return err
	}
	action, err := DecodeStreamActionTx(tx)
	if err != nil {
		return err
	}
	_, err = s.applyAction(action)
	return err
}

// ApplyEconomicTx atomically applies stream state and CELL ledger effects.
func (s *StreamStateStore) ApplyEconomicTx(tx *mempool.Tx, accounts *AccountStore) error {
	if accounts == nil {
		return errors.New("chain: nil AccountStore for CELL stream action")
	}
	if err := VerifyStreamActionTx(tx); err != nil {
		return err
	}
	action, err := DecodeStreamActionTx(tx)
	if err != nil {
		return err
	}

	// Stateful preflight prevents nonce churn for a command that cannot apply.
	preview := s.ChainReplayClone().(*StreamStateStore)
	if _, err := preview.applyAction(action); err != nil {
		return err
	}

	accountSnapshot := accounts.Clone()
	streamSnapshot := s.ChainReplayClone()
	restore := func(cause error) error {
		accounts.RestoreFrom(accountSnapshot)
		_ = s.RestoreFromChainReplay(streamSnapshot)
		return cause
	}

	chargeDust := uint64(0)
	if action.Action == StreamActionOpen {
		chargeDust = action.BudgetDust
	}
	charge := dustToBalance(chargeDust)
	if action.Action == StreamActionReceipt || action.Action == StreamActionSettle {
		err = accounts.ChargeAndBumpNonceAllowCreate(action.Sender, charge, tx.Nonce)
	} else {
		err = accounts.ChargeAndBumpNonce(action.Sender, charge, tx.Nonce)
	}
	if err != nil {
		return err
	}
	effect, err := s.applyAction(action)
	if err != nil {
		return restore(err)
	}
	if effect.ProviderDust > 0 {
		accounts.Credit(effect.Provider, dustToBalance(effect.ProviderDust))
	}
	if effect.PayerRefundDust > 0 {
		accounts.Credit(effect.Payer, dustToBalance(effect.PayerRefundDust))
	}
	return nil
}

func (s *StreamStateStore) applyAction(action StreamAction) (streamEconomicEffect, error) {
	if s == nil {
		return streamEconomicEffect{}, errors.New("chain: nil StreamStateStore")
	}
	if err := validateStreamActionShape(action); err != nil {
		return streamEconomicEffect{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.actionIDs[action.ID]; exists {
		return streamEconomicEffect{}, ErrDuplicateStreamAction
	}

	if action.Action == StreamActionOpen {
		if _, exists := s.streams[action.StreamID]; exists {
			return streamEconomicEffect{}, ErrDuplicateStream
		}
		state := &StreamState{
			StreamID:           action.StreamID,
			Payer:              action.Sender,
			Provider:           action.Provider,
			ServiceID:          action.ServiceID,
			DeviceIDHash:       action.DeviceIDHash,
			SessionPublicKey:   action.SessionPublicKey,
			PriceDust:          action.PriceDust,
			PricePeriodSeconds: action.PricePeriodSeconds,
			BudgetDust:         action.BudgetDust,
			MaxActiveSeconds:   action.MaxActiveSeconds,
			ExpiresAt:          action.ExpiresAt,
			Status:             StreamStatusActive,
			OpenedAt:           action.Timestamp,
			LastAction:         action.Action,
			LastActionID:       action.ID,
			LastActionAt:       action.Timestamp,
			ActionCount:        1,
		}
		s.streams[action.StreamID] = state
		s.actionIDs[action.ID] = struct{}{}
		return streamEconomicEffect{}, nil
	}

	state, exists := s.streams[action.StreamID]
	if !exists {
		return streamEconomicEffect{}, ErrStreamNotFound
	}
	if state.Status == StreamStatusClosed {
		return streamEconomicEffect{}, ErrStreamClosed
	}
	actionAt, _ := time.Parse(time.RFC3339Nano, action.Timestamp)
	lastActionAt, _ := time.Parse(time.RFC3339Nano, state.LastActionAt)
	if actionAt.Before(lastActionAt) {
		return streamEconomicEffect{}, errors.New("chain: CELL stream action timestamp moved backwards")
	}
	effect := streamEconomicEffect{}

	switch action.Action {
	case StreamActionReceipt:
		if state.Status != StreamStatusActive {
			return streamEconomicEffect{}, ErrStreamNotActive
		}
		if !strings.EqualFold(action.Sender, state.Provider) {
			return streamEconomicEffect{}, errors.New("chain: only the CELL stream provider may submit usage receipts")
		}
		receipt := *action.Receipt
		if receipt.Sequence != state.LastReceiptSequence+1 {
			return streamEconomicEffect{}, fmt.Errorf("%w: got sequence %d, want %d",
				ErrStreamReceiptReplay, receipt.Sequence, state.LastReceiptSequence+1)
		}
		if receipt.CumulativeActiveSeconds <= state.CumulativeActiveSeconds {
			return streamEconomicEffect{}, fmt.Errorf("%w: cumulative active seconds did not increase", ErrStreamReceiptReplay)
		}
		if receipt.CumulativeActiveSeconds > state.MaxActiveSeconds {
			return streamEconomicEffect{}, ErrStreamUsageCapExceeded
		}
		observedAt, _ := time.Parse(time.RFC3339Nano, receipt.ObservedAt)
		openedAt, _ := time.Parse(time.RFC3339Nano, state.OpenedAt)
		expiresAt, _ := time.Parse(time.RFC3339Nano, state.ExpiresAt)
		if observedAt.Before(openedAt) || observedAt.After(expiresAt) || observedAt.After(actionAt) {
			return streamEconomicEffect{}, errors.New("chain: CELL stream receipt timestamp is outside the authorized window")
		}
		if state.LastReceiptObservedAt != "" {
			lastObservedAt, _ := time.Parse(time.RFC3339Nano, state.LastReceiptObservedAt)
			if !observedAt.After(lastObservedAt) {
				return streamEconomicEffect{}, fmt.Errorf("%w: receipt observed_at did not increase", ErrStreamReceiptReplay)
			}
		}
		wallSeconds := uint64(observedAt.Sub(openedAt) / time.Second)
		maxActiveSeconds := uint64(0)
		if wallSeconds > state.PausedDurationSeconds {
			maxActiveSeconds = wallSeconds - state.PausedDurationSeconds
		}
		if receipt.CumulativeActiveSeconds > maxActiveSeconds {
			return streamEconomicEffect{}, ErrStreamWallTimeExceeded
		}
		if err := verifySessionReceipt(state, receipt); err != nil {
			return streamEconomicEffect{}, err
		}
		charge, err := streamChargeDust(receipt.CumulativeActiveSeconds, state.PriceDust, state.PricePeriodSeconds)
		if err != nil {
			return streamEconomicEffect{}, err
		}
		if charge > state.BudgetDust {
			return streamEconomicEffect{}, ErrStreamBudgetExceeded
		}
		state.CumulativeActiveSeconds = receipt.CumulativeActiveSeconds
		state.LastReceiptSequence = receipt.Sequence
		state.LastReceiptObservedAt = receipt.ObservedAt
		state.AccruedDust = charge

	case StreamActionPause:
		if !strings.EqualFold(action.Sender, state.Payer) {
			return streamEconomicEffect{}, errors.New("chain: only the CELL stream payer may pause")
		}
		if state.Status != StreamStatusActive {
			return streamEconomicEffect{}, ErrStreamNotActive
		}
		expiresAt, _ := time.Parse(time.RFC3339Nano, state.ExpiresAt)
		if actionAt.After(expiresAt) {
			return streamEconomicEffect{}, errors.New("chain: cannot pause an expired CELL stream")
		}
		state.Status = StreamStatusPaused
		state.LastPausedAt = action.Timestamp

	case StreamActionResume:
		if !strings.EqualFold(action.Sender, state.Payer) {
			return streamEconomicEffect{}, errors.New("chain: only the CELL stream payer may resume")
		}
		if state.Status != StreamStatusPaused {
			return streamEconomicEffect{}, errors.New("chain: only a paused CELL stream may resume")
		}
		expiresAt, _ := time.Parse(time.RFC3339Nano, state.ExpiresAt)
		if actionAt.After(expiresAt) {
			return streamEconomicEffect{}, errors.New("chain: cannot resume an expired CELL stream")
		}
		pausedAt, _ := time.Parse(time.RFC3339Nano, state.LastPausedAt)
		pausedFor := actionAt.Sub(pausedAt)
		pausedSeconds := uint64(pausedFor / time.Second)
		if pausedFor%time.Second != 0 {
			pausedSeconds++
		}
		if math.MaxUint64-state.PausedDurationSeconds < pausedSeconds {
			return streamEconomicEffect{}, errors.New("chain: CELL stream paused duration overflow")
		}
		state.PausedDurationSeconds += pausedSeconds
		state.Status = StreamStatusActive
		state.LastResumedAt = action.Timestamp

	case StreamActionSettle:
		if !strings.EqualFold(action.Sender, state.Provider) {
			return streamEconomicEffect{}, errors.New("chain: only the CELL stream provider may settle")
		}
		if state.AccruedDust <= state.SettledDust {
			return streamEconomicEffect{}, ErrNoStreamSettlement
		}
		effect.Provider = state.Provider
		effect.ProviderDust = state.AccruedDust - state.SettledDust
		state.SettledDust = state.AccruedDust

	case StreamActionClose:
		if !strings.EqualFold(action.Sender, state.Payer) {
			return streamEconomicEffect{}, errors.New("chain: only the CELL stream payer may close")
		}
		effect.Provider = state.Provider
		effect.ProviderDust = state.AccruedDust - state.SettledDust
		effect.Payer = state.Payer
		effect.PayerRefundDust = state.BudgetDust - state.AccruedDust
		state.SettledDust = state.AccruedDust
		state.RefundedDust = effect.PayerRefundDust
		state.Status = StreamStatusClosed
		state.ClosedAt = action.Timestamp
	}

	state.LastAction = action.Action
	state.LastActionID = action.ID
	state.LastActionAt = action.Timestamp
	state.ActionCount++
	s.actionIDs[action.ID] = struct{}{}
	return effect, nil
}

func (s *StreamStateStore) GetStream(streamID string) (StreamState, bool) {
	if s == nil {
		return StreamState{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.streams[streamID]
	if !ok {
		return StreamState{}, false
	}
	return *state, true
}

func (s *StreamStateStore) AllStreams() []StreamState {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.streams))
	for id := range s.streams {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]StreamState, 0, len(ids))
	for _, id := range ids {
		out = append(out, *s.streams[id])
	}
	return out
}

func (s *StreamStateStore) Count() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.streams)
}

func (s *StreamStateStore) ChainReplayClone() ChainReplayApplier {
	if s == nil {
		return nil
	}
	clone := NewStreamStateStore()
	s.mu.RLock()
	defer s.mu.RUnlock()
	for id, state := range s.streams {
		cp := *state
		clone.streams[id] = &cp
	}
	for id := range s.actionIDs {
		clone.actionIDs[id] = struct{}{}
	}
	return clone
}

func (s *StreamStateStore) RestoreFromChainReplay(from ChainReplayApplier) error {
	if s == nil {
		return errors.New("chain: nil StreamStateStore")
	}
	other, ok := from.(*StreamStateStore)
	if !ok || other == nil {
		return errors.New("chain: stream replay restore expects *StreamStateStore snapshot")
	}
	other.mu.RLock()
	streams := make(map[string]*StreamState, len(other.streams))
	for id, state := range other.streams {
		cp := *state
		streams[id] = &cp
	}
	actionIDs := make(map[string]struct{}, len(other.actionIDs))
	for id := range other.actionIDs {
		actionIDs[id] = struct{}{}
	}
	other.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.streams = streams
	s.actionIDs = actionIDs
	return nil
}

func (s *StreamStateStore) StateRoot() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	streamIDs := make([]string, 0, len(s.streams))
	for id := range s.streams {
		streamIDs = append(streamIDs, id)
	}
	sort.Strings(streamIDs)
	states := make([]StreamState, 0, len(streamIDs))
	for _, id := range streamIDs {
		states = append(states, *s.streams[id])
	}
	actionIDs := make([]string, 0, len(s.actionIDs))
	for id := range s.actionIDs {
		actionIDs = append(actionIDs, id)
	}
	sort.Strings(actionIDs)
	s.mu.RUnlock()

	h := sha256.New()
	for _, state := range states {
		raw, _ := json.Marshal(state)
		h.Write(raw)
		h.Write([]byte{'\n'})
	}
	for _, id := range actionIDs {
		fmt.Fprintf(h, "action:%s\n", id)
	}
	return hex.EncodeToString(h.Sum(nil))
}

var (
	_ StateApplier       = (*StreamStateStore)(nil)
	_ ChainReplayApplier = (*StreamStateStore)(nil)
)
