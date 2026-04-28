package chainparams

// validate.go contains the stateless wire-validation logic
// shared between the mempool admission gate (admit.go) and
// the chain-side applier (pkg/chain.GovApplier).
//
// What "stateless" means here: anything that can be checked
// without consulting the live ParamStore or the current chain
// height. So:
//
//   - JSON well-formedness
//   - Kind tag matches PayloadKindParamSet
//   - Param is on the registry whitelist
//   - Value is within the registry bounds
//   - Memo length cap
//
// What is NOT stateless (and lives in the applier):
//
//   - EffectiveHeight is in the (currentHeight,
//     currentHeight + MaxActivationDelay] window. The window
//     reference depends on chain state.
//   - tx.Sender is on the AuthorityList. The list is a runtime
//     applier collaborator.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// ParseParamSet decodes a canonical-JSON ParamSetPayload.
// `DisallowUnknownFields` is set so any wire drift surfaces as
// a clean rejection.
//
// Returns (nil, ErrPayloadDecode) wrapped with the underlying
// json error on parse failure; (nil, ErrPayloadInvalid) wrapped
// when the bytes parse but a field violates a structural rule
// already at decode time (today, only the kind tag).
func ParseParamSet(raw []byte) (*ParamSetPayload, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: empty payload", ErrPayloadDecode)
	}

	var p ParamSetPayload
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPayloadDecode, err)
	}
	if dec.More() {
		return nil, fmt.Errorf(
			"%w: trailing bytes after payload JSON", ErrPayloadDecode)
	}
	if p.Kind != PayloadKindParamSet {
		return nil, fmt.Errorf(
			"%w: kind=%q want %q",
			ErrPayloadInvalid, p.Kind, PayloadKindParamSet)
	}
	return &p, nil
}

// ValidateParamSetFields runs every stateless check on a
// decoded ParamSetPayload. The returned error wraps the
// appropriate sentinel (ErrPayloadInvalid, ErrUnknownParam,
// ErrValueOutOfBounds) so callers can errors.Is against the
// category.
func ValidateParamSetFields(p *ParamSetPayload) error {
	if p == nil {
		return errors.New("chainparams: nil ParamSetPayload")
	}
	if p.Kind != PayloadKindParamSet {
		return fmt.Errorf(
			"%w: kind=%q want %q",
			ErrPayloadInvalid, p.Kind, PayloadKindParamSet)
	}
	if len(p.Memo) > MaxMemoLen {
		return fmt.Errorf(
			"%w: memo exceeds %d bytes (got %d)",
			ErrPayloadInvalid, MaxMemoLen, len(p.Memo))
	}
	if p.Param == "" {
		return fmt.Errorf(
			"%w: param name is empty (registry: %s)",
			ErrUnknownParam, formatNames())
	}
	spec, ok := Lookup(p.Param)
	if !ok {
		return fmt.Errorf(
			"%w: param=%q (registry: %s)",
			ErrUnknownParam, p.Param, formatNames())
	}
	if err := spec.CheckBounds(p.Value); err != nil {
		return err
	}
	if p.EffectiveHeight == 0 {
		// A zero EffectiveHeight cannot be applied — every
		// chain has a positive height at apply time. Catching
		// this stateless saves a round-trip.
		return fmt.Errorf(
			"%w: effective_height must be positive (got 0)",
			ErrPayloadInvalid)
	}
	return nil
}

// EncodeParamSet emits canonical JSON for a ParamSetPayload.
// Caller-friendly helper used by the CLI and by tests; not on
// the consensus path (the chain only consumes raw bytes via
// ParseParamSet).
func EncodeParamSet(p ParamSetPayload) ([]byte, error) {
	if err := ValidateParamSetFields(&p); err != nil {
		return nil, fmt.Errorf("chainparams: encode: %w", err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(p); err != nil {
		return nil, fmt.Errorf("chainparams: encode: %w", err)
	}
	out := buf.Bytes()
	if n := len(out); n > 0 && out[n-1] == '\n' {
		out = out[:n-1]
	}
	return out, nil
}
