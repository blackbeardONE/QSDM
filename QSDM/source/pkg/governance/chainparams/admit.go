package chainparams

// admit.go: stateless mempool admission gate for governance-
// parameter transactions. Symmetric to
// pkg/mining/{enrollment,slashing}/admit.go. The intended
// wiring (see internal/v2wiring/v2wiring.go) is:
//
//	pool.SetAdmissionChecker(
//	    chainparams.AdmissionChecker(
//	        slashing.AdmissionChecker(
//	            enrollment.AdmissionChecker(baseAdmit))))
//
// Each layer only intercepts its own ContractID and delegates
// other contracts down the chain, so the order is structurally
// safe but kept stable for readability: governance > slash >
// enroll > base mirrors the conceptual blast radius (a gov tx
// retunes consensus-shaping parameters, the most consequential
// kind of change a single tx can land).
//
// Why stateless-only at admission time:
//
//   - The mempool has no view of chain state (no ParamStore, no
//     current height, no AuthorityList). Stateful checks
//     (EffectiveHeight window, sender-on-authority-list) belong
//     in chain.GovApplier where they can consult the live
//     collaborators under the same lock that mutates them.
//
//   - The cheap checks here (kind tag, registry name, bounds,
//     memo cap, fee floor) catch 99 % of malformed traffic and
//     protect the pool from junk. Anything that survives
//     admission is at least a syntactically valid candidate.

import (
	"errors"
	"fmt"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// AdmissionChecker returns a function suitable for
// mempool.Mempool.SetAdmissionChecker that:
//
//  1. Runs ValidateParamSetFields on any tx whose ContractID
//     matches chainparams.ContractID.
//  2. Delegates everything else to `prev` (which may be nil
//     to mean "accept").
//
// Failures from stage 1 are returned with a clear, non-leaky
// message; HTTP/RPC layers can pass them through to the
// client. Never panics on nil tx — returns a plain "nil tx"
// error so the mempool's "Add never panics on user input"
// invariant is preserved.
//
// The returned function is concurrency-safe (the validators
// take no shared state).
func AdmissionChecker(prev func(*mempool.Tx) error) func(*mempool.Tx) error {
	return func(tx *mempool.Tx) error {
		if tx == nil {
			return errors.New("chainparams admit: nil tx")
		}
		if tx.ContractID != ContractID {
			if prev != nil {
				return prev(tx)
			}
			return nil
		}
		if len(tx.Payload) == 0 {
			return fmt.Errorf("%w: gov tx missing Payload", ErrPayloadInvalid)
		}
		// Dispatch on payload kind. Both variants share
		// ContractID, so the kind tag is the discriminator
		// the admit gate uses to pick a per-shape validator.
		// Wire drift (unknown kind, missing kind) surfaces
		// here at the cheapest possible point.
		kind, err := PeekKind(tx.Payload)
		if err != nil {
			return fmt.Errorf("chainparams admit: %w", err)
		}
		switch kind {
		case PayloadKindParamSet:
			p, err := ParseParamSet(tx.Payload)
			if err != nil {
				return fmt.Errorf("chainparams admit: %w", err)
			}
			if err := ValidateParamSetFields(p); err != nil {
				return fmt.Errorf("chainparams admit: %w", err)
			}
		case PayloadKindAuthoritySet:
			p, err := ParseAuthoritySet(tx.Payload)
			if err != nil {
				return fmt.Errorf("chainparams admit: %w", err)
			}
			if err := ValidateAuthoritySetFields(p); err != nil {
				return fmt.Errorf("chainparams admit: %w", err)
			}
		default:
			return fmt.Errorf(
				"chainparams admit: %w: unsupported kind %q",
				ErrPayloadInvalid, kind)
		}
		// Gov txs MUST carry a positive Fee. Without one,
		// nonce accounting in chain.GovApplier would break
		// (the applier debits the submitter's account by Fee
		// to bump the nonce). Mirrors the slashing fee-floor
		// rule because the cost shape is identical.
		if tx.Fee <= 0 {
			return fmt.Errorf("%w: gov tx requires positive Fee, got %v",
				ErrPayloadInvalid, tx.Fee)
		}
		return nil
	}
}
