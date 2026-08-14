package chain

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"math"
	"sync/atomic"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// The block hash does not commit transaction CONTENTS.
//
// computeTxRoot merkleizes tx.ID only, and computeBlockHash signs over that
// root plus the state root and producer. So anything a transaction carries that
// the state root does not independently distinguish can be rewritten in flight
// with the block hash, the producer signature and the state root all still
// verifying. The audit has carried this since it was written and nothing in
// this session touched it.
//
// Fixing it changes every block hash, so it is gated on a height in the same
// spirit as forkDustHeight (dustfork.go), though not identically: that one
// defaults its threshold to MaxUint64 in init(), this uses the atomic's zero
// value with an explicit h != 0 check. Both are inert until set. Below the
// activation the legacy ID-only root is produced, at or above it the content
// root is. Zero, the default, never activates -- so this changes nothing until
// an operator coordinates a fork height across the network.

// txContentRootHeight is the first height whose tx root commits transaction
// contents rather than IDs. Zero disables it.
var txContentRootHeight atomic.Uint64

// SetTxContentRootActivationHeight sets the first height at which the block's
// transaction root commits contents. Zero leaves the legacy ID-only root in
// place. Every node must agree on the value: it changes block hashes, so a node
// with a different setting computes different hashes and forks.
func SetTxContentRootActivationHeight(h uint64) { txContentRootHeight.Store(h) }

// TxContentRootActivationHeight reports the configured activation height.
func TxContentRootActivationHeight() uint64 { return txContentRootHeight.Load() }

// txContentRootActiveAt reports whether the content root governs this height.
func txContentRootActiveAt(height uint64) bool {
	h := txContentRootHeight.Load()
	return h != 0 && height >= h
}

// TxContentDigest returns a digest binding every consensus-relevant field of a
// transaction.
//
// The fields are listed explicitly rather than marshalling the struct, for one
// reason that would otherwise be a fork: mempool.Tx carries AddedAt, the local
// time this node admitted the transaction. That differs on every node. A digest
// over the whole struct would make the tx root -- and therefore the block hash
// -- disagree between nodes that saw the same block, which is worse than the
// defect being fixed.
//
// Everything else is included, length-prefixed so no field boundary can be
// shifted to produce a collision (a sender ending in digits absorbing the start
// of an amount, say). Floats are digested by their IEEE-754 bits rather than a
// formatted string, so there is no rounding or formatting ambiguity.
//
// If a field is added to mempool.Tx and not added here, it will not be bound.
// TestTxContentDigest_BindsEveryConsensusField exists to make that omission
// fail rather than pass silently.
func TxContentDigest(tx *mempool.Tx) string {
	h := sha256.New()
	if tx == nil {
		return hex.EncodeToString(h.Sum(nil))
	}
	writeTxField(h, []byte(tx.ID))
	writeTxField(h, []byte(tx.Sender))
	writeTxField(h, []byte(tx.Recipient))
	writeTxUint(h, math.Float64bits(tx.Amount))
	writeTxUint(h, math.Float64bits(tx.Fee))
	writeTxUint(h, uint64(tx.GasLimit))
	writeTxUint(h, tx.Nonce)
	writeTxField(h, tx.Payload)
	writeTxField(h, []byte(tx.ContractID))
	writeTxField(h, []byte(tx.Signature))
	writeTxField(h, []byte(tx.PublicKey))
	// AddedAt is deliberately excluded: it is node-local admission time.
	return hex.EncodeToString(h.Sum(nil))
}

func writeTxField(w io.Writer, b []byte) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(b)))
	_, _ = w.Write(n[:])
	_, _ = w.Write(b)
}

func writeTxUint(w io.Writer, v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	writeTxField(w, b[:])
}
