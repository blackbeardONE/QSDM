package chain

import "sync/atomic"

var enrollmentStateRootHeight atomic.Uint64

// SetEnrollmentStateRootActivationHeight sets the first height at which block
// state roots commit mining-enrollment side state. Zero keeps the legacy root.
func SetEnrollmentStateRootActivationHeight(h uint64) { enrollmentStateRootHeight.Store(h) }

// EnrollmentStateRootActivationHeight reports the configured activation height.
func EnrollmentStateRootActivationHeight() uint64 { return enrollmentStateRootHeight.Load() }

func enrollmentStateRootActiveAt(height uint64) bool {
	activation := enrollmentStateRootHeight.Load()
	return activation > 0 && height >= activation
}
