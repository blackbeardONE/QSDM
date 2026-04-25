package slashing

// codec.go: canonical JSON encode/decode for SlashPayload.
// Mirrors pkg/mining/enrollment/codec.go conventions:
//
//   - SetEscapeHTML(false) for cross-stack determinism.
//   - Trailing newline stripped after json.Encoder.Encode.
//   - DisallowUnknownFields on decode to catch typos.
//   - Trailing-data check rejects multi-document inputs.
//
// Field order in SlashPayload (types.go) IS the canonical order;
// reordering fields there is a hard fork.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// EncodeSlashPayload returns canonical bytes for a SlashPayload.
// Suitable as mempool.Tx.Payload contents.
func EncodeSlashPayload(p SlashPayload) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(p); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	if n := len(out); n > 0 && out[n-1] == '\n' {
		out = out[:n-1]
	}
	if len(out) == 0 {
		return nil, errors.New("slashing: EncodeSlashPayload produced empty bytes")
	}
	return out, nil
}

// DecodeSlashPayload parses raw bytes into a SlashPayload.
// Strict: rejects unknown fields and trailing data.
func DecodeSlashPayload(raw []byte) (SlashPayload, error) {
	var p SlashPayload
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return SlashPayload{}, fmt.Errorf("%w: %v", ErrPayloadDecode, err)
	}
	if dec.More() {
		return SlashPayload{}, fmt.Errorf("%w: trailing data after JSON", ErrPayloadDecode)
	}
	return p, nil
}
