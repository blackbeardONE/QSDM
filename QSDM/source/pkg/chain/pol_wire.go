package chain

import "encoding/json"

// PrevoteLockProof is a portable proof-of-lock (POL) bundle for gossip or evidence sidecars.
// Validators may attach Prevotes that justify the LockedBlockHash at Height/Round.
type PrevoteLockProof struct {
	Height          uint64      `json:"height"`
	Round           uint32      `json:"round"`
	LockedBlockHash string      `json:"locked_block_hash"`
	CarriedFromLock string      `json:"carried_from_lock,omitempty"`
	Prevotes        []BlockVote `json:"prevotes,omitempty"`
}

// EncodePrevoteLockProof returns JSON bytes for wire transport.
func EncodePrevoteLockProof(p *PrevoteLockProof) ([]byte, error) {
	if p == nil {
		return nil, nil
	}
	return json.Marshal(p)
}

// DecodePrevoteLockProof parses JSON from EncodePrevoteLockProof.
func DecodePrevoteLockProof(b []byte) (*PrevoteLockProof, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var p PrevoteLockProof
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
