package enrollment

// gpu_uuid_fold.go: enrollment compares gpu_uuid without case.
//
// The attestation verifier compares GPU UUIDs without case (hmac.Verifier,
// strings.EqualFold: nvidia-smi spells them differently across driver
// versions). From GPUUUIDFoldActivationHeight enrollment uniqueness does the
// same: an enroll is refused when any ACTIVE record holds the same UUID in any
// case. A producer that checks enrollments at admission passes AtAdmission and
// applies the rule at once. Below the height nothing changes, so history
// replays as produced.
//
// This keeps one bond per GPU consistent with the verifier. It is not a
// hardware proof: a gpu_uuid is self-asserted, and the bond stays the
// anti-Sybil cost (nvidia-hmac-v1 is an economic control).

import (
	"fmt"
	"strings"
	"sync/atomic"
)

// GPUUUIDFoldActivationHeight is the first block at which enrollment compares
// gpu_uuid without case.
const GPUUUIDFoldActivationHeight uint64 = 1_190_000

// AtAdmission is the height the producer's admission passes to
// ValidateEnrollAgainstStateAt: every height-gated stateful rule applies there
// at once (admission changes no block).
const AtAdmission = ^uint64(0)

var gpuUUIDFoldFrom atomic.Uint64

func init() { gpuUUIDFoldFrom.Store(GPUUUIDFoldActivationHeight) }

// SetGPUUUIDFoldActivationHeight changes the activation height in force (tests
// and replay tools; 0 = never). Every validator must use the same value.
func SetGPUUUIDFoldActivationHeight(h uint64) { gpuUUIDFoldFrom.Store(h) }

// GPUUUIDFoldActivationHeightInForce reports the activation height in force.
func GPUUUIDFoldActivationHeightInForce() uint64 { return gpuUUIDFoldFrom.Load() }

// GPUUUIDFoldActiveAt reports whether the rule governs an enroll at height.
func GPUUUIDFoldActiveAt(height uint64) bool {
	h := gpuUUIDFoldFrom.Load()
	return h != 0 && height >= h
}

// GPUUUIDFoldBinder is implemented by states that can look up an ACTIVE
// binding of a gpu_uuid without case.
type GPUUUIDFoldBinder interface {
	GPUUUIDBoundFold(gpuUUID string) (string, error)
}

// GPUUUIDBoundFold is GPUUUIDBound without case: the node_id an ACTIVE record
// of any spelling of gpuUUID is bound to, or "". When history left more than one
// spelling active, the smallest node_id is returned, so the answer does not
// depend on map order.
func (s *InMemoryState) GPUUUIDBoundFold(gpuUUID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	best := ""
	for uuid, nodeID := range s.byGPUActive {
		if strings.EqualFold(uuid, gpuUUID) && (best == "" || nodeID < best) {
			best = nodeID
		}
	}
	return best, nil
}

// checkGPUUUIDFold is the height-gated part of ValidateEnrollAgainstStateAt.
func checkGPUUUIDFold(p EnrollPayload, state EnrollmentState, height uint64) error {
	if !GPUUUIDFoldActiveAt(height) {
		return nil
	}
	fb, ok := state.(GPUUUIDFoldBinder)
	if !ok {
		return nil
	}
	boundTo, err := fb.GPUUUIDBoundFold(p.GPUUUID)
	if err != nil {
		return fmt.Errorf("enrollment: state GPUUUIDBoundFold: %w", err)
	}
	if boundTo != "" {
		return fmt.Errorf("%w: gpu_uuid bound (in another case) to node_id %q", ErrGPUUUIDTaken, boundTo)
	}
	return nil
}
