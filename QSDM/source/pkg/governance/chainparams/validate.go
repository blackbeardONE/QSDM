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

// kindHeader is the minimal first-pass shape used to peek the
// PayloadKind tag from a raw payload before committing to a
// variant decoder. Permits unknown fields on this stage so a
// `kind=authority-set` payload (with op/address/etc.) doesn't
// fail decode just because the param-set decoder would.
type kindHeader struct {
	Kind PayloadKind `json:"kind"`
}

// PeekKind decodes ONLY the `kind` field of a raw `qsdm/gov/v1`
// payload. Used by the admission gate and the chain applier to
// dispatch to the variant-specific parser without committing to
// a payload shape yet.
//
// Returns ErrPayloadDecode wrapping the underlying error on
// malformed JSON; ErrPayloadInvalid when the kind tag is empty
// or unrecognised.
func PeekKind(raw []byte) (PayloadKind, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("%w: empty payload", ErrPayloadDecode)
	}
	var h kindHeader
	if err := json.Unmarshal(raw, &h); err != nil {
		return "", fmt.Errorf("%w: %w", ErrPayloadDecode, err)
	}
	switch h.Kind {
	case PayloadKindParamSet, PayloadKindAuthoritySet:
		return h.Kind, nil
	case "":
		return "", fmt.Errorf(
			"%w: payload missing kind field (want %q or %q)",
			ErrPayloadInvalid,
			PayloadKindParamSet, PayloadKindAuthoritySet)
	default:
		return "", fmt.Errorf(
			"%w: unknown kind %q (want %q or %q)",
			ErrPayloadInvalid, h.Kind,
			PayloadKindParamSet, PayloadKindAuthoritySet)
	}
}

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

// ParseAuthoritySet decodes a canonical-JSON AuthoritySetPayload.
// Same disallow-unknown-fields posture as ParseParamSet so wire
// drift is caught at the syntactic boundary rather than silently
// accepted.
func ParseAuthoritySet(raw []byte) (*AuthoritySetPayload, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: empty payload", ErrPayloadDecode)
	}
	var p AuthoritySetPayload
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPayloadDecode, err)
	}
	if dec.More() {
		return nil, fmt.Errorf(
			"%w: trailing bytes after payload JSON", ErrPayloadDecode)
	}
	if p.Kind != PayloadKindAuthoritySet {
		return nil, fmt.Errorf(
			"%w: kind=%q want %q",
			ErrPayloadInvalid, p.Kind, PayloadKindAuthoritySet)
	}
	return &p, nil
}

// ValidateAuthoritySetFields runs every stateless check on a
// decoded AuthoritySetPayload. As with ValidateParamSetFields,
// the returned error wraps the appropriate sentinel so callers
// can errors.Is against the category.
//
// Checks performed:
//   - Kind tag matches PayloadKindAuthoritySet.
//   - Op is one of {add, remove}.
//   - Address is non-empty, ≤MaxAuthorityAddressLen bytes,
//     contains no whitespace control characters (a sloppy
//     copy-paste with a trailing newline must reject — the
//     applier compares addresses as exact strings).
//   - EffectiveHeight is positive (zero is unreachable
//     against any live chain).
//   - Memo length cap.
func ValidateAuthoritySetFields(p *AuthoritySetPayload) error {
	if p == nil {
		return errors.New("chainparams: nil AuthoritySetPayload")
	}
	if p.Kind != PayloadKindAuthoritySet {
		return fmt.Errorf(
			"%w: kind=%q want %q",
			ErrPayloadInvalid, p.Kind, PayloadKindAuthoritySet)
	}
	if p.Op != AuthorityOpAdd && p.Op != AuthorityOpRemove {
		return fmt.Errorf(
			"%w: op=%q want %q or %q",
			ErrPayloadInvalid, p.Op,
			AuthorityOpAdd, AuthorityOpRemove)
	}
	if p.Address == "" {
		return fmt.Errorf(
			"%w: address is empty", ErrPayloadInvalid)
	}
	if len(p.Address) > MaxAuthorityAddressLen {
		return fmt.Errorf(
			"%w: address exceeds %d bytes (got %d)",
			ErrPayloadInvalid, MaxAuthorityAddressLen, len(p.Address))
	}
	for i := 0; i < len(p.Address); i++ {
		c := p.Address[i]
		if c <= 0x20 || c == 0x7f {
			return fmt.Errorf(
				"%w: address contains non-printable byte 0x%02x at index %d",
				ErrPayloadInvalid, c, i)
		}
	}
	if len(p.Memo) > MaxMemoLen {
		return fmt.Errorf(
			"%w: memo exceeds %d bytes (got %d)",
			ErrPayloadInvalid, MaxMemoLen, len(p.Memo))
	}
	if p.EffectiveHeight == 0 {
		return fmt.Errorf(
			"%w: effective_height must be positive (got 0)",
			ErrPayloadInvalid)
	}
	return nil
}

// EncodeAuthoritySet emits canonical JSON for an
// AuthoritySetPayload. Mirrors EncodeParamSet — caller-friendly
// helper for the CLI / tests; not on the consensus path.
func EncodeAuthoritySet(p AuthoritySetPayload) ([]byte, error) {
	if err := ValidateAuthoritySetFields(&p); err != nil {
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
