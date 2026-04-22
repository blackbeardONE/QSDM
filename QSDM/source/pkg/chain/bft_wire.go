package chain

import (
	"encoding/json"
	"fmt"
)

// BFT gossip topic payloads (JSON envelope + kind).
const (
	BFTWirePropose   = "bft_propose"
	BFTWirePrevote   = "bft_prevote"
	BFTWirePrecommit = "bft_precommit"
)

// BFTWireEnvelope is the on-wire wrapper for vote-driven BFT messages.
type BFTWireEnvelope struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

// BFTWireProposeMsg is a proposal for a height/round.
// BlockHash is the BFT vote id (this codebase uses the sealed StateRoot for that role).
// Block, when set, carries the full block body so followers can execute fork-choice without local execution.
type BFTWireProposeMsg struct {
	Height    uint64 `json:"height"`
	Round     uint32 `json:"round"`
	Proposer  string `json:"proposer"`
	BlockHash string `json:"block_hash"`
	Block     *Block `json:"block,omitempty"`
}

// BFTWirePrevoteMsg is a prevote from a validator.
type BFTWirePrevoteMsg struct {
	Height    uint64 `json:"height"`
	Round     uint32 `json:"round"`
	Validator string `json:"validator"`
	BlockHash string `json:"block_hash"`
}

// BFTWirePrecommitMsg is a precommit from a validator.
type BFTWirePrecommitMsg struct {
	Height    uint64 `json:"height"`
	Round     uint32 `json:"round"`
	Validator string `json:"validator"`
	BlockHash string `json:"block_hash"`
}

// MarshalBFTWire builds a gossip payload for the given message.
func MarshalBFTWire(kind string, payload interface{}) ([]byte, error) {
	inner, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(BFTWireEnvelope{Kind: kind, Payload: inner})
}

// UnmarshalBFTWire parses the envelope and returns kind + raw payload.
func UnmarshalBFTWire(b []byte) (kind string, payload json.RawMessage, err error) {
	var env BFTWireEnvelope
	if err = json.Unmarshal(b, &env); err != nil {
		return "", nil, err
	}
	if env.Kind == "" || len(env.Payload) == 0 {
		return "", nil, fmt.Errorf("bft wire: empty kind or payload")
	}
	return env.Kind, env.Payload, nil
}
