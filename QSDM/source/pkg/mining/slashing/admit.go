package slashing

// admit.go: stateless mempool admission gate for slashing
// transactions. Symmetric to pkg/mining/enrollment/admit.go.
// The intended wiring is:
//
//	pool := mempool.New(cfg)
//	pool.SetAdmissionChecker(slashing.AdmissionChecker(prev))
//
// where `prev` is whatever admission checker the operator had
// before. When a tx is tagged with our ContractID we run
// ValidateSlashFields; for any other tx we delegate to `prev`
// (or accept if prev is nil). Composing two admission gates —
// enrollment + slashing — is done by chaining:
//
//	gate := slashing.AdmissionChecker(
//	    enrollment.AdmissionChecker(baseAdmit))
//	pool.SetAdmissionChecker(gate)
//
// Order does not matter for correctness because each layer
// only intercepts its own ContractID; mining/transfer txs
// fall through both layers to baseAdmit unchanged.
//
// Why stateless-only at admission time:
//
//   - The mempool has no view of the chain state (no
//     EnrollmentState, no per-evidence-kind verifier). Stateful
//     checks belong in chain.SlashApplier.ApplySlashTx, where
//     they can consult the registry under the same lock that
//     mutates it. Performing them at admit time would race
//     against block production and produce false rejections.
//
//   - The cheap checks (length bounds, kind sanity, blob
//     bounds, fee floor) handle 99% of badly-formed traffic
//     and protect the pool from junk. Anything that survives
//     admission is at least a syntactically valid candidate.
//
//   - Cryptographic verification (the EvidenceVerifier dispatch)
//     is intentionally NOT run at admission time. It can be
//     expensive (signature checks, BFT equivocation analysis)
//     and the verifier needs the live registry to look up
//     gpu_uuid / hmac_key for the offender. Stateless admit
//     keeps the pool path O(payload size).
//
// Admission errors are visible to clients (HTTP 400) and
// describe attributable submitter-side bugs, never
// consensus-level rejections.

import (
	"errors"
	"fmt"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// AdmissionChecker returns a function suitable for
// mempool.Mempool.SetAdmissionChecker that:
//
//  1. Runs ValidateSlashFields on any tx whose ContractID
//     matches slashing.ContractID.
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
			return errors.New("slashing admit: nil tx")
		}
		if tx.ContractID != ContractID {
			if prev != nil {
				return prev(tx)
			}
			return nil
		}

		if len(tx.Payload) == 0 {
			return fmt.Errorf("%w: slashing tx missing Payload", ErrPayloadInvalid)
		}

		p, err := DecodeSlashPayload(tx.Payload)
		if err != nil {
			return fmt.Errorf("slashing admit: %w", err)
		}
		if err := ValidateSlashFields(p, tx.Sender); err != nil {
			return fmt.Errorf("slashing admit: %w", err)
		}

		// Slashing txs MUST carry a positive Fee. Without one,
		// nonce accounting in chain.SlashApplier would break
		// (the applier debits the submitter's account by Fee
		// to bump the nonce). Mirroring the unenroll path's
		// fee floor, since the cost shape is identical.
		if tx.Fee <= 0 {
			return fmt.Errorf("%w: slashing requires positive Fee, got %v",
				ErrPayloadInvalid, tx.Fee)
		}
		return nil
	}
}
