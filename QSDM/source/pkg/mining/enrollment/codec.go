package enrollment

// codec.go: canonical JSON encode/decode for EnrollPayload and
// UnenrollPayload. The canonical form MUST be
// deterministic byte-for-byte so that:
//
//   1. Tx signatures cover a stable digest (any miner or
//      validator re-encoding must get the same bytes).
//   2. CI / audit tooling can diff on-chain payloads without
//      whitespace noise.
//
// Go's encoding/json marshals struct fields in declaration
// order, so the EnrollPayload / UnenrollPayload field order in
// types.go IS the canonical order. Don't reorder fields there
// without a fork.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// EncodeEnrollPayload returns the canonical bytes of an
// EnrollPayload. Suitable for mempool.Tx.Payload.
func EncodeEnrollPayload(p EnrollPayload) ([]byte, error) {
	if p.Kind == "" {
		p.Kind = PayloadKindEnroll
	}
	if p.Kind != PayloadKindEnroll {
		return nil, fmt.Errorf("%w: Kind must be %q for enroll encode, got %q",
			ErrPayloadInvalid, PayloadKindEnroll, p.Kind)
	}
	return marshalCanonical(p)
}

// EncodeUnenrollPayload returns the canonical bytes of an
// UnenrollPayload.
func EncodeUnenrollPayload(p UnenrollPayload) ([]byte, error) {
	if p.Kind == "" {
		p.Kind = PayloadKindUnenroll
	}
	if p.Kind != PayloadKindUnenroll {
		return nil, fmt.Errorf("%w: Kind must be %q for unenroll encode, got %q",
			ErrPayloadInvalid, PayloadKindUnenroll, p.Kind)
	}
	return marshalCanonical(p)
}

// PeekKind inspects a raw payload to discover which variant it
// encodes, WITHOUT fully decoding. Lets the chain's state-
// transition hook dispatch without double-parsing.
//
// Returns ErrPayloadDecode if the bytes are not valid JSON or
// do not contain a "kind" field.
func PeekKind(raw []byte) (PayloadKind, error) {
	var envelope struct {
		Kind PayloadKind `json:"kind"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	// Unknown fields allowed here: PeekKind is intentionally
	// lenient so it can be called before full validation (which
	// IS strict). The subsequent full Decode pass catches
	// extras.
	if err := dec.Decode(&envelope); err != nil {
		return "", fmt.Errorf("%w: %v", ErrPayloadDecode, err)
	}
	if envelope.Kind == "" {
		return "", fmt.Errorf("%w: missing 'kind' field", ErrPayloadDecode)
	}
	return envelope.Kind, nil
}

// DecodeEnrollPayload parses raw bytes into an EnrollPayload,
// rejecting unknown fields and duplicate keys. Does NOT perform
// consensus validation — see ValidateEnrollPayload for that.
func DecodeEnrollPayload(raw []byte) (EnrollPayload, error) {
	var p EnrollPayload
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return EnrollPayload{}, fmt.Errorf("%w: %v", ErrPayloadDecode, err)
	}
	if dec.More() {
		return EnrollPayload{}, fmt.Errorf("%w: trailing data after JSON", ErrPayloadDecode)
	}
	if p.Kind != PayloadKindEnroll {
		return EnrollPayload{}, fmt.Errorf(
			"%w: Kind must be %q for enroll decode, got %q",
			ErrPayloadInvalid, PayloadKindEnroll, p.Kind,
		)
	}
	return p, nil
}

// DecodeUnenrollPayload parses raw bytes into an UnenrollPayload.
// Strict: rejects unknown fields, trailing data, wrong Kind.
func DecodeUnenrollPayload(raw []byte) (UnenrollPayload, error) {
	var p UnenrollPayload
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return UnenrollPayload{}, fmt.Errorf("%w: %v", ErrPayloadDecode, err)
	}
	if dec.More() {
		return UnenrollPayload{}, fmt.Errorf("%w: trailing data after JSON", ErrPayloadDecode)
	}
	if p.Kind != PayloadKindUnenroll {
		return UnenrollPayload{}, fmt.Errorf(
			"%w: Kind must be %q for unenroll decode, got %q",
			ErrPayloadInvalid, PayloadKindUnenroll, p.Kind,
		)
	}
	return p, nil
}

// marshalCanonical is the shared marshaller for both payload
// types. Keeps the encoder settings in one place so the two
// payload types cannot drift (e.g. one gaining SetEscapeHTML and
// the other not).
func marshalCanonical(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// Escape HTML is a json.Encoder default that would produce
	// "\u003c" for "<". Not security-relevant for us (these
	// bytes are wrapped in a signed tx, not embedded in HTML),
	// but it makes the bytes platform-dependent on some JSON
	// libs. Disable for cross-stack determinism.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// json.Encoder always appends a trailing newline. Strip it
	// so the canonical bytes match what DecodeEnrollPayload /
	// DecodeUnenrollPayload round-trip.
	out := buf.Bytes()
	if len(out) > 0 && out[len(out)-1] == '\n' {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return nil, errors.New("enrollment: marshalCanonical produced empty bytes")
	}
	return out, nil
}
