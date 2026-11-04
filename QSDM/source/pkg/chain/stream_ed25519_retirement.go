package chain

// stream_ed25519_retirement.go: the height-gated retirement of Ed25519 stream
// session keys (qsdm/streams/v1).
//
// A stream open is ML-DSA-signed by the payer wallet, but it registers an
// ephemeral Ed25519 session key (StreamAction.SessionPublicKey) that later
// signs the usage receipts. Ed25519 is not post-quantum, and streams have no
// other session-key scheme, so from the configured height on a stream open is
// refused: every open registers an Ed25519 session key.
//
// Only opens are refused. Receipts, pause/resume, settle and close for a
// stream opened below the height keep working, so an existing stream can be
// settled and closed. No stream can be opened at or above the height, so no
// receipt for such a stream can exist.
//
// The rule is consensus: it is applied by the block applier at the height of
// the block being applied (EnrollmentAwareApplier) and by the boot replay of
// the stream projection at each block's height (ApplyHistoricalTx). Below the
// height nothing changes, so history replays byte-identically. Zero (the
// default) never activates. Every validator must use the same value.

import (
	"errors"
	"sync/atomic"
)

// ErrStreamEd25519SessionKeyRetired refuses a stream open at or above the
// stream Ed25519 retirement height.
var ErrStreamEd25519SessionKeyRetired = errors.New("chain: CELL stream open refused: Ed25519 stream session keys are retired at this height")

var streamEd25519RetirementHeight atomic.Uint64

// SetStreamEd25519RetirementHeight sets the first block height at which a
// stream open (which registers an Ed25519 session key) is refused. Zero (the
// default) never activates. Every validator must use the same value.
func SetStreamEd25519RetirementHeight(h uint64) { streamEd25519RetirementHeight.Store(h) }

// StreamEd25519RetirementHeight reports the configured height (0 = never).
func StreamEd25519RetirementHeight() uint64 { return streamEd25519RetirementHeight.Load() }

// StreamEd25519SessionKeysRetiredAt reports whether the rule governs a block
// at height.
func StreamEd25519SessionKeysRetiredAt(height uint64) bool {
	h := streamEd25519RetirementHeight.Load()
	return h != 0 && height >= h
}

// checkStreamEd25519Retirement is the rule for one decoded stream action in a
// block at height: nil for every action below the height and for every
// non-open action.
func checkStreamEd25519Retirement(action StreamAction, height uint64) error {
	if action.Action != StreamActionOpen || !StreamEd25519SessionKeysRetiredAt(height) {
		return nil
	}
	return ErrStreamEd25519SessionKeyRetired
}
