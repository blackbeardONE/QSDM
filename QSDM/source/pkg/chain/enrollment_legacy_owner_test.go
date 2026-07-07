package chain

import (
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

func TestEnrollmentSweepAppliesLegacyOwnerSunset(t *testing.T) {
	accounts := NewAccountStore()
	state := enrollment.NewInMemoryState()
	if err := state.ApplyEnroll(enrollment.EnrollmentRecord{
		NodeID:           "legacy-rig",
		Owner:            "qsdm1legacy-miner",
		GPUUUID:          "GPU-legacy",
		HMACKey:          make([]byte, enrollment.MinHMACKeyLen),
		StakeDust:        1_000_000_000,
		EnrolledAtHeight: 10,
	}); err != nil {
		t.Fatalf("apply legacy: %v", err)
	}

	applier := NewEnrollmentApplier(accounts, state)
	if _, err := applier.SweepMaturedEnrollments(enrollment.LegacyOwnerSunsetHeight); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	record, err := state.Lookup("legacy-rig")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if record == nil || record.Active() {
		t.Fatalf("legacy record still active: %+v", record)
	}
	if bound, _ := state.GPUUUIDBound("GPU-legacy"); bound != "" {
		t.Fatalf("legacy GPU still bound to %q", bound)
	}
}
