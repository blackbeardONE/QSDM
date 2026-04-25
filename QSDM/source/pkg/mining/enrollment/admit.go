package enrollment

// admit.go: stateless mempool admission gate for enrollment
// transactions. The intended wiring is:
//
//	pool := mempool.New(cfg)
//	pool.SetAdmissionChecker(enrollment.AdmissionChecker(prev))
//
// where `prev` is whatever admission checker the operator had
// before (e.g. POL fees, signature checks). When a tx is tagged
// with our ContractID we run the appropriate Validate*Fields;
// for any other tx we delegate to `prev` (or accept if prev is
// nil). This composes cleanly with existing chains of admission
// rules without forcing all callers to know about enrollment.
//
// Why stateless-only at admission time:
//
//   - The mempool has no view of the chain state (no
//     EnrollmentState, no AccountStore). Stateful checks belong
//     in EnrollmentApplier.ApplyEnrollmentTx, where they can
//     consult the live state under the same lock that mutates
//     it. Performing them at admit time would race against any
//     concurrent block production and produce false rejections.
//
//   - The cheap checks (length bounds, kind sanity, stake match)
//     handle 99% of the badly-formed traffic and protect the
//     pool from being filled with junk. Anything that survives
//     admission is at least a syntactically valid candidate.
//
//   - Admission errors are visible to clients (HTTP 400) and
//     should describe attributable miner-side bugs, never
//     consensus-level rejections.

import (
	"errors"
	"fmt"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// AdmissionChecker returns a function suitable for
// mempool.Mempool.SetAdmissionChecker that:
//
//  1. Runs the stateless enrollment validators on any tx whose
//     ContractID matches enrollment.ContractID.
//  2. Delegates everything else to `prev` (which may be nil to
//     mean "accept").
//
// Failures from stage 1 are returned with a clear, non-leaky
// message; the caller (HTTP handler, RPC) can pass them through
// to the client. The function never panics on nil tx — it
// returns a plain "nil tx" error so the mempool's existing
// invariant ("Add never panics on user input") is preserved.
//
// The returned function is concurrency-safe (the validators
// take no shared state).
func AdmissionChecker(prev func(*mempool.Tx) error) func(*mempool.Tx) error {
	return func(tx *mempool.Tx) error {
		if tx == nil {
			return errors.New("enrollment admit: nil tx")
		}
		if tx.ContractID != ContractID {
			if prev != nil {
				return prev(tx)
			}
			return nil
		}

		// Enrollment-tagged tx: must have a payload.
		if len(tx.Payload) == 0 {
			return fmt.Errorf("%w: enrollment tx missing Payload", ErrPayloadInvalid)
		}

		kind, err := PeekKind(tx.Payload)
		if err != nil {
			return fmt.Errorf("enrollment admit: %w", err)
		}

		switch kind {
		case PayloadKindEnroll:
			p, err := DecodeEnrollPayload(tx.Payload)
			if err != nil {
				return fmt.Errorf("enrollment admit (enroll decode): %w", err)
			}
			if err := ValidateEnrollFields(p, tx.Sender); err != nil {
				return fmt.Errorf("enrollment admit (enroll fields): %w", err)
			}
			// Belt-and-suspenders: an enroll tx with zero or
			// negative fee is admitted by the bare account-store
			// path because applyEnroll has no fee-floor check.
			// Catch it here so the pool never fills with
			// fee-free spam masquerading as enrollment.
			if tx.Fee < 0 {
				return fmt.Errorf("%w: enrollment fee must be >= 0, got %v", ErrPayloadInvalid, tx.Fee)
			}
			return nil

		case PayloadKindUnenroll:
			p, err := DecodeUnenrollPayload(tx.Payload)
			if err != nil {
				return fmt.Errorf("enrollment admit (unenroll decode): %w", err)
			}
			if err := ValidateUnenrollFields(p, tx.Sender); err != nil {
				return fmt.Errorf("enrollment admit (unenroll fields): %w", err)
			}
			// applyUnenroll requires positive Fee for nonce
			// accounting; mirror that here so the rejection is
			// attributable at admit time, not buried in a
			// mid-block apply error.
			if tx.Fee <= 0 {
				return fmt.Errorf("%w: unenrollment requires positive Fee, got %v", ErrPayloadInvalid, tx.Fee)
			}
			return nil

		default:
			return fmt.Errorf("%w: unknown payload kind %q", ErrPayloadInvalid, kind)
		}
	}
}
