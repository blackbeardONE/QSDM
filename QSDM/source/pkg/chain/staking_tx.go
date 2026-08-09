package chain

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// Validator staking as a chain transaction.
//
// StakingLedger.Delegate / BeginUnbond existed but had zero production
// callers, so the bonded-stake ledger was always empty. Since validator
// membership is derived from that ledger
// (validator_registry_chainstate.go), an empty ledger meant membership
// always fell back to the node-local genesis bootstrap pair — and no home
// node could ever join the canonical validator set.
//
// Bonding MUST arrive as a transaction rather than a local API or CLI call.
// A local call mutates one node's ledger; its peers never see it, so they
// derive a different validator set and disagree about quorum. That is
// precisely the node-local-membership bug this whole line of work exists to
// remove, so exposing Delegate directly would reintroduce it with extra
// steps and a convincing success message.
//
// As a transaction it is replayed identically by every node from committed
// block data, so all nodes converge on the same bonded stake and therefore
// the same validator set.

// StakingContractID marks validator staking transactions.
const StakingContractID = "qsdm/staking/v1"

// DefaultUnbondBlocks is the bonding lock-up applied when a payload does not
// specify one. Unbonding is deliberately not instant: voting power drops
// immediately while funds return only after this many blocks, so a validator
// cannot vote, unbond, and walk away inside one block.
const DefaultUnbondBlocks uint64 = 100

// Staking actions.
const (
	StakingActionDelegate = "delegate"
	StakingActionUnbond   = "begin_unbond"
)

// Errors surfaced to the applier and, through it, to submitters.
var (
	ErrStakingNotWired      = errors.New("chain: staking ledger is not wired")
	ErrStakingBadPayload    = errors.New("chain: invalid staking payload")
	ErrStakingUnknownAction = errors.New("chain: unknown staking action")
)

// StakingPayload is the JSON body of a qsdm/staking/v1 transaction.
//
// The delegator is always tx.Sender — never a payload field — so a
// transaction can only ever bond the signer's own funds. Taking the
// delegator from the payload would let anyone bond anyone else's balance.
type StakingPayload struct {
	Action    string  `json:"action"`
	Validator string  `json:"validator"`
	Amount    float64 `json:"amount"`
	// UnbondBlocks optionally overrides DefaultUnbondBlocks for a
	// begin_unbond. Ignored for delegate.
	UnbondBlocks uint64 `json:"unbond_blocks,omitempty"`
}

// DecodeStakingPayload parses and validates a staking payload.
func DecodeStakingPayload(raw []byte) (StakingPayload, error) {
	var p StakingPayload
	if len(raw) == 0 {
		return p, fmt.Errorf("%w: empty payload", ErrStakingBadPayload)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, fmt.Errorf("%w: %v", ErrStakingBadPayload, err)
	}
	p.Action = strings.TrimSpace(p.Action)
	p.Validator = strings.TrimSpace(p.Validator)
	if p.Validator == "" {
		return p, fmt.Errorf("%w: validator is required", ErrStakingBadPayload)
	}
	if p.Amount <= 0 {
		return p, fmt.Errorf("%w: amount must be positive", ErrStakingBadPayload)
	}
	switch p.Action {
	case StakingActionDelegate, StakingActionUnbond:
	default:
		return p, fmt.Errorf("%w: %q", ErrStakingUnknownAction, p.Action)
	}
	return p, nil
}

// EncodeStakingPayload is the submitter-side helper.
func EncodeStakingPayload(p StakingPayload) ([]byte, error) {
	if _, err := DecodeStakingPayload(mustJSON(p)); err != nil {
		return nil, err
	}
	return json.Marshal(p)
}

func mustJSON(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// ApplyStakingTx applies a qsdm/staking/v1 transaction.
//
// Deterministic by construction: the outcome depends only on the
// transaction, the account store and the committed height, so every node
// replaying the same block reaches the same bonded-stake state and derives
// the same validator set.
func ApplyStakingTx(
	sl *StakingLedger,
	as *AccountStore,
	tx *mempool.Tx,
	height uint64,
) error {
	if sl == nil {
		return ErrStakingNotWired
	}
	if as == nil {
		return errors.New("chain: staking requires an account store")
	}
	if tx == nil {
		return errors.New("chain: nil staking tx")
	}
	if tx.ContractID != StakingContractID {
		return fmt.Errorf("chain: not a staking tx: %q", tx.ContractID)
	}
	if strings.TrimSpace(tx.Sender) == "" {
		return fmt.Errorf("%w: sender is required", ErrStakingBadPayload)
	}

	p, err := DecodeStakingPayload(tx.Payload)
	if err != nil {
		return err
	}

	switch p.Action {
	case StakingActionDelegate:
		// Delegate debits the sender's account itself, so the tx must not
		// also carry a transfer Amount — that would move funds twice.
		if tx.Amount != 0 {
			return fmt.Errorf(
				"%w: delegate must carry the amount in the payload, not tx.Amount", ErrStakingBadPayload)
		}
		return sl.Delegate(as, tx.Sender, p.Validator, p.Amount)

	case StakingActionUnbond:
		blocks := p.UnbondBlocks
		if blocks == 0 {
			blocks = DefaultUnbondBlocks
		}
		return sl.BeginUnbond(as, tx.Sender, p.Validator, p.Amount, height, blocks)
	}

	return fmt.Errorf("%w: %q", ErrStakingUnknownAction, p.Action)
}
