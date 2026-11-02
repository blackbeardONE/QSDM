package mesh3d

// structure.go: the entanglement-structure rules of a mesh wire transaction,
// shared by the cgo and !cgo validators. Every rule here is fatal.
//
// A mesh transaction wraps one signed wallet envelope (Data) with 3..5
// parent cells (BuildMeshCompanionFromWalletJSON is the only producer). The
// wrapper is valid only if it is exactly the structure the envelope
// determines:
//
//   - parent cell IDs are distinct and non-empty, and each cell's Data is
//     sha256(ID), so a cell cannot carry content its label does not commit
//     to;
//   - Data is a wallet envelope whose ML-DSA-87 signature verifies under its
//     own public_key, with sender = hex(sha256(public_key))
//     (wallet.VerifyTransactionData, the check every validator repeats when
//     it replays a block);
//   - the envelope's signed parent_cells pass the context-free PoE rules
//     (pkg/poe.CheckShape);
//   - the first n-1 mesh parent IDs are the envelope's first n-1 signed
//     parent_cells, in order, and the last is hex(sha256(Data)), binding
//     the wrapper to the exact payload;
//   - the mesh ID is a prefix (at least 16 characters) of the envelope ID.
//
// Whether the envelope's parents are committed transactions needs the chain;
// the peer-to-peer ingress handler checks that next, against its node's
// committed history (chain.(*BlockProducer).CheckWalletTransferParents).

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/poe"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

const (
	// MinMeshParents and MaxMeshParents bound a mesh wire transaction's
	// parent cells.
	MinMeshParents = 3
	MaxMeshParents = 5
	// MinMeshTxIDLen is the shortest mesh transaction ID accepted.
	MinMeshTxIDLen = 16
)

// ErrStructure is the root of every entanglement-structure failure.
var ErrStructure = errors.New("mesh3d: invalid entanglement structure")

// ParentDataFor returns the Data a mesh parent cell labelled id must carry.
func ParentDataFor(id string) []byte {
	sum := sha256.Sum256([]byte(id))
	return sum[:]
}

// PayloadDigest returns the ID of the last mesh parent for payload data.
func PayloadDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// DecodeWalletPayload strictly decodes the signed wallet envelope a mesh
// transaction carries: unknown fields and trailing data are refused.
func DecodeWalletPayload(data []byte) (wallet.TransactionData, error) {
	var env wallet.TransactionData
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		return wallet.TransactionData{}, fmt.Errorf("%w: payload is not a wallet envelope: %v", ErrStructure, err)
	}
	if dec.More() {
		return wallet.TransactionData{}, fmt.Errorf("%w: payload has trailing data", ErrStructure)
	}
	return env, nil
}

// ValidateStructure applies every rule in the file comment and returns the
// verified envelope.
func ValidateStructure(tx *Transaction) (wallet.TransactionData, error) {
	if tx == nil {
		return wallet.TransactionData{}, fmt.Errorf("%w: nil transaction", ErrStructure)
	}
	n := len(tx.ParentCells)
	if n < MinMeshParents || n > MaxMeshParents {
		return wallet.TransactionData{}, fmt.Errorf("%w: %d parent cells (expected %d-%d)", ErrStructure, n, MinMeshParents, MaxMeshParents)
	}
	if len(tx.ID) < MinMeshTxIDLen {
		return wallet.TransactionData{}, fmt.Errorf("%w: transaction ID shorter than %d characters", ErrStructure, MinMeshTxIDLen)
	}
	if len(tx.Data) == 0 {
		return wallet.TransactionData{}, fmt.Errorf("%w: transaction data is empty", ErrStructure)
	}
	seen := make(map[string]struct{}, n)
	for i, p := range tx.ParentCells {
		if p.ID == "" {
			return wallet.TransactionData{}, fmt.Errorf("%w: parent cell %d has no ID", ErrStructure, i)
		}
		if _, dup := seen[p.ID]; dup {
			return wallet.TransactionData{}, fmt.Errorf("%w: duplicate parent cell ID %q", ErrStructure, p.ID)
		}
		seen[p.ID] = struct{}{}
		if !bytes.Equal(p.Data, ParentDataFor(p.ID)) {
			return wallet.TransactionData{}, fmt.Errorf("%w: parent cell %d data is not sha256 of its ID", ErrStructure, i)
		}
	}
	env, err := DecodeWalletPayload(tx.Data)
	if err != nil {
		return wallet.TransactionData{}, err
	}
	if err := wallet.VerifyTransactionData(env); err != nil {
		return wallet.TransactionData{}, fmt.Errorf("%w: payload signature: %v", ErrStructure, err)
	}
	if err := poe.CheckShape(env.ID, env.ParentCells); err != nil {
		return wallet.TransactionData{}, fmt.Errorf("%w: payload parents: %w", ErrStructure, err)
	}
	if !strings.HasPrefix(env.ID, tx.ID) {
		return wallet.TransactionData{}, fmt.Errorf("%w: mesh ID is not a prefix of the envelope ID", ErrStructure)
	}
	if got, want := tx.ParentCells[n-1].ID, PayloadDigest(tx.Data); got != want {
		return wallet.TransactionData{}, fmt.Errorf("%w: last parent cell is not the payload digest", ErrStructure)
	}
	if len(env.ParentCells) < n-1 {
		return wallet.TransactionData{}, fmt.Errorf("%w: %d mesh parents need %d signed envelope parents, have %d", ErrStructure, n, n-1, len(env.ParentCells))
	}
	for i := 0; i < n-1; i++ {
		if tx.ParentCells[i].ID != env.ParentCells[i] {
			return wallet.TransactionData{}, fmt.Errorf("%w: mesh parent %d is not signed envelope parent %d", ErrStructure, i, i)
		}
	}
	return env, nil
}
