package legacymining

// LMP1 payload codec (§3.4, WP3). The encoding is canonical: every valid ID
// list has exactly one encoding, and DecodePayload accepts exactly the byte
// strings that EncodePayload produces.

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

const (
	// payloadHeaderLen is len(PayloadTag) plus the u16 count.
	payloadHeaderLen = len(PayloadTag) + 2
	// payloadIDLen is the size of one proof ID in the payload.
	payloadIDLen = len(ProofID{})
)

// EncodePayload returns the LMP1 payload for ids: PayloadTag, a big-endian
// u16 count, then the IDs. ids must hold 1..MaxPayloadIDs IDs in strictly
// ascending byte order; the codec never sorts or deduplicates, so a caller bug
// surfaces as an error instead of a silently different payload. Errors wrap
// ErrBadPayload.
func EncodePayload(ids []ProofID) ([]byte, error) {
	if len(ids) == 0 || len(ids) > MaxPayloadIDs {
		return nil, fmt.Errorf("%w: %d IDs, want 1..%d", ErrBadPayload, len(ids), MaxPayloadIDs)
	}
	out := make([]byte, 0, payloadHeaderLen+len(ids)*payloadIDLen)
	out = append(out, PayloadTag...)
	out = binary.BigEndian.AppendUint16(out, uint16(len(ids)))
	for i := range ids {
		if i > 0 && bytes.Compare(ids[i-1][:], ids[i][:]) >= 0 {
			return nil, fmt.Errorf("%w: IDs not strictly ascending at index %d", ErrBadPayload, i)
		}
		out = append(out, ids[i][:]...)
	}
	return out, nil
}

// DecodePayload parses an LMP1 payload strictly. It rejects a missing or
// different tag, a count outside 1..MaxPayloadIDs, a length other than
// exactly header+32*count (so truncated and trailing bytes both fail), and IDs
// that are not strictly ascending (which also rejects duplicates). The
// returned slice does not alias payload. Errors wrap ErrBadPayload.
func DecodePayload(payload []byte) ([]ProofID, error) {
	if len(payload) < payloadHeaderLen || string(payload[:len(PayloadTag)]) != PayloadTag {
		return nil, fmt.Errorf("%w: missing %s tag", ErrBadPayload, PayloadTag)
	}
	n := int(binary.BigEndian.Uint16(payload[len(PayloadTag):payloadHeaderLen]))
	if n == 0 || n > MaxPayloadIDs {
		return nil, fmt.Errorf("%w: count %d, want 1..%d", ErrBadPayload, n, MaxPayloadIDs)
	}
	if want := payloadHeaderLen + n*payloadIDLen; len(payload) != want {
		return nil, fmt.Errorf("%w: length %d, want %d for %d IDs", ErrBadPayload, len(payload), want, n)
	}
	ids := make([]ProofID, n)
	body := payload[payloadHeaderLen:]
	for i := range ids {
		copy(ids[i][:], body[i*payloadIDLen:])
		if i > 0 && bytes.Compare(ids[i-1][:], ids[i][:]) >= 0 {
			return nil, fmt.Errorf("%w: IDs not strictly ascending at index %d", ErrBadPayload, i)
		}
	}
	return ids, nil
}
