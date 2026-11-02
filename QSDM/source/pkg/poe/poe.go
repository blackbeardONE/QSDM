// Package poe holds the Proof-of-Entanglement (PoE) parent rules that every
// path handling a signed wallet transfer applies: mempool admission, block
// production, block validation and replay (pkg/chain), the peer-to-peer
// ingress handlers (cmd/qsdm/transaction), the consensus signature helper
// (pkg/consensus) and the mesh wire validator (pkg/mesh3d).
//
// A wallet transfer's signed envelope carries parent_cells: the IDs of
// transactions the signer saw committed. Under PoE every parent must name a
// transaction that is already part of canonical history when the transfer
// is committed. This package holds the context-free half of those rules
// (count, format, no duplicates, no self-reference) and the error values;
// the history-dependent half (existence, ordering, the reference window)
// lives in pkg/chain, which owns the committed chain.
//
// It is a leaf package on purpose: it imports only the standard library, so
// consensus code, network ingress and API validation can share one
// definition without import cycles.
package poe

import (
	"errors"
	"fmt"
)

const (
	// MinParents is the minimum number of parent cells a transfer must name
	// once PoE is enforced (the entanglement degree).
	MinParents = 2
	// MaxParents bounds the parent list. It equals the API's long-standing
	// parent_cells limit, so no envelope the API accepts can exceed it.
	MaxParents = 10
	// MinParentIDLen and MaxParentIDLen bound one parent reference. They
	// equal the API's transaction-ID limits.
	MinParentIDLen = 16
	MaxParentIDLen = 128
	// ParentWindowBlocks is how far back a parent may be: a transfer committed
	// at height h may name a transaction committed at heights h-W .. h-1, or
	// one that appears earlier in the same block. At the 10-second target
	// block time W is about 24 hours. It bounds the history index every
	// validator keeps and gives signed transfers a natural expiry. It is a
	// consensus constant: changing it needs a new activation height.
	ParentWindowBlocks = 8640
)

// ErrViolation is the root of every PoE rule failure. errors.Is(err,
// ErrViolation) identifies a transfer that broke a PoE rule, as opposed to
// one that failed for an unrelated reason (bad signature, balance, nonce).
var ErrViolation = errors.New("proof-of-entanglement violation")

// Specific rule failures. Each wraps ErrViolation.
var (
	// ErrParentCount: fewer than MinParents or more than MaxParents.
	ErrParentCount = fmt.Errorf("%w: parent count out of range", ErrViolation)
	// ErrParentFormat: a parent ID is empty, too short, too long or has a
	// character outside [0-9A-Za-z_-].
	ErrParentFormat = fmt.Errorf("%w: malformed parent id", ErrViolation)
	// ErrDuplicateParent: the same parent ID appears twice in one transfer.
	ErrDuplicateParent = fmt.Errorf("%w: duplicate parent", ErrViolation)
	// ErrSelfParent: a transfer names its own ID as a parent.
	ErrSelfParent = fmt.Errorf("%w: transaction names itself as a parent", ErrViolation)
	// ErrParentNotEarlier: the parent is in the same block at the same or a
	// later position, so it is not an ancestor (this is how a cycle or a
	// forward reference shows up).
	ErrParentNotEarlier = fmt.Errorf("%w: parent is not an earlier transaction", ErrViolation)
	// ErrParentUnknown: the parent is not a committed transaction inside the
	// reference window. This covers a parent that never existed, one that is
	// only pending in a mempool, one from another chain or fork, and one
	// older than ParentWindowBlocks.
	ErrParentUnknown = fmt.Errorf("%w: parent is not a committed transaction in the reference window", ErrViolation)
	// ErrDuplicateTxID: the transfer's own ID is already used by a committed
	// transaction inside the reference window (or earlier in the block), so
	// references to that ID would be ambiguous.
	ErrDuplicateTxID = fmt.Errorf("%w: transaction id already committed in the reference window", ErrViolation)
	// ErrHistoryUnavailable: the validator's committed-history index is not
	// in step with its chain tip. Fails closed.
	ErrHistoryUnavailable = fmt.Errorf("%w: committed history index unavailable", ErrViolation)
)

// ValidParentID reports whether id is a well-formed parent reference:
// MinParentIDLen..MaxParentIDLen characters from [0-9A-Za-z_-]. Every
// transaction ID the producer and current clients generate has this shape.
func ValidParentID(id string) bool {
	if len(id) < MinParentIDLen || len(id) > MaxParentIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// CheckShape applies the context-free PoE rules to one transfer: the parent
// count is within [MinParents, MaxParents], every parent ID is well formed,
// no parent repeats, and no parent equals txID. It says nothing about
// whether the parents exist; that needs the committed chain (pkg/chain).
func CheckShape(txID string, parents []string) error {
	if len(parents) < MinParents || len(parents) > MaxParents {
		return fmt.Errorf("%w: got %d, want %d..%d", ErrParentCount, len(parents), MinParents, MaxParents)
	}
	seen := make(map[string]struct{}, len(parents))
	for i, p := range parents {
		if !ValidParentID(p) {
			return fmt.Errorf("%w: parent %d (%d characters)", ErrParentFormat, i, len(p))
		}
		if p == txID {
			return fmt.Errorf("%w: parent %d", ErrSelfParent, i)
		}
		if _, dup := seen[p]; dup {
			return fmt.Errorf("%w: parent %d repeats %q", ErrDuplicateParent, i, p)
		}
		seen[p] = struct{}{}
	}
	return nil
}

// IsViolation reports whether err is (or wraps) a PoE rule failure.
func IsViolation(err error) bool { return errors.Is(err, ErrViolation) }

// Reason returns a short, fixed label for a PoE error, for metrics and
// receipts. Errors that are not PoE violations return "".
func Reason(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrParentCount):
		return "parent_count"
	case errors.Is(err, ErrParentFormat):
		return "parent_format"
	case errors.Is(err, ErrDuplicateParent):
		return "duplicate_parent"
	case errors.Is(err, ErrSelfParent):
		return "self_parent"
	case errors.Is(err, ErrParentNotEarlier):
		return "parent_not_earlier"
	case errors.Is(err, ErrParentUnknown):
		return "parent_unknown"
	case errors.Is(err, ErrDuplicateTxID):
		return "duplicate_tx_id"
	case errors.Is(err, ErrHistoryUnavailable):
		return "history_unavailable"
	case errors.Is(err, ErrViolation):
		return "other"
	default:
		return ""
	}
}

// Reasons lists every label Reason can return for a violation, in a fixed
// order, so metric exporters can emit zero-valued series up front.
var Reasons = []string{
	"parent_count", "parent_format", "duplicate_parent", "self_parent",
	"parent_not_earlier", "parent_unknown", "duplicate_tx_id", "history_unavailable", "other",
}
