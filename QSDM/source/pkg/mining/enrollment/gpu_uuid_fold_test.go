package enrollment

import (
	"errors"
	"strings"
	"testing"
)

func withGPUUUIDFoldHeight(t *testing.T, h uint64) {
	t.Helper()
	prev := gpuUUIDFoldFrom.Load()
	gpuUUIDFoldFrom.Store(h)
	t.Cleanup(func() { gpuUUIDFoldFrom.Store(prev) })
}

const foldGPU = "GPU-0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0"

func foldVariant(node string, gpu string) EnrollPayload {
	p := validEnrollPayload()
	p.NodeID = node
	p.GPUUUID = gpu
	return p
}

// Below the height a case variant of an active GPU enrolls as before; from
// the height it is refused (in consensus and at admission), the exact
// spelling stays refused, and the GPU is free again once its record is no
// longer active.
func TestGPUUUIDFold(t *testing.T) {
	const h = 500
	withGPUUUIDFoldHeight(t, h)
	s := NewInMemoryState()
	if err := s.ApplyEnroll(mkRecord("rig-a", "owner-a", foldGPU, 10, 100)); err != nil {
		t.Fatal(err)
	}
	upper := "GPU-" + strings.ToUpper(foldGPU[4:])
	mixed := "GPU-0F1e2D3c-4B5a-6978-8796-A5b4C3d2E1f0"
	const bal = 1 << 62

	if err := ValidateEnrollAgainstStateAt(foldVariant("rig-b", upper), bal, s, h-1); err != nil {
		t.Fatalf("below the height a case variant must pass as before: %v", err)
	}
	for _, gpu := range []string{upper, mixed} {
		for _, at := range []uint64{h, h + 1, AtAdmission} {
			err := ValidateEnrollAgainstStateAt(foldVariant("rig-b", gpu), bal, s, at)
			if !errors.Is(err, ErrGPUUUIDTaken) || !strings.Contains(err.Error(), `"rig-a"`) {
				t.Fatalf("%s at %d: got %v, want ErrGPUUUIDTaken naming rig-a", gpu, at, err)
			}
		}
	}
	if err := ValidateEnrollAgainstStateAt(foldVariant("rig-b", foldGPU), bal, s, h-1); !errors.Is(err, ErrGPUUUIDTaken) {
		t.Fatalf("the exact spelling must stay refused below the height: %v", err)
	}
	if err := ValidateEnrollAgainstState(foldVariant("rig-b", upper), bal, s); err != nil {
		t.Fatalf("the height-less form keeps the old rule: %v", err)
	}
	if err := ValidateEnrollAgainstStateAt(foldVariant("rig-b", "GPU-11111111-2222-3333-4444-555555555555"), bal, s, h); err != nil {
		t.Fatalf("another GPU must enroll: %v", err)
	}

	if err := s.ApplyUnenroll("rig-a", 600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateEnrollAgainstStateAt(foldVariant("rig-b", upper), bal, s, h+200); err != nil {
		t.Fatalf("after unenroll the GPU is free in any case: %v", err)
	}
}

// When history (below the height) left two spellings active, the lookup is
// deterministic: the smallest node_id.
func TestGPUUUIDBoundFoldDeterministic(t *testing.T) {
	s := NewInMemoryState()
	upper := "GPU-" + strings.ToUpper(foldGPU[4:])
	if err := s.ApplyEnroll(mkRecord("rig-z", "owner-z", foldGPU, 10, 100)); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyEnroll(mkRecord("rig-m", "owner-m", upper, 10, 101)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		got, err := s.GPUUUIDBoundFold(strings.ToLower(foldGPU))
		if err != nil || got != "rig-m" {
			t.Fatalf("got %q %v, want rig-m", got, err)
		}
	}
	if got, _ := s.GPUUUIDBoundFold("GPU-ffffffff-0000-0000-0000-000000000000"); got != "" {
		t.Fatalf("unbound GPU: %q", got)
	}
}

// With no activation height (0) the rule never applies, even at admission.
func TestGPUUUIDFoldInertAtZero(t *testing.T) {
	withGPUUUIDFoldHeight(t, 0)
	s := NewInMemoryState()
	if err := s.ApplyEnroll(mkRecord("rig-a", "owner-a", foldGPU, 10, 100)); err != nil {
		t.Fatal(err)
	}
	upper := "GPU-" + strings.ToUpper(foldGPU[4:])
	if err := ValidateEnrollAgainstStateAt(foldVariant("rig-b", upper), 1<<62, s, AtAdmission); err != nil {
		t.Fatalf("at height 0 nothing changes: %v", err)
	}
	if GPUUUIDFoldActiveAt(AtAdmission) {
		t.Fatal("active with no height")
	}
}
