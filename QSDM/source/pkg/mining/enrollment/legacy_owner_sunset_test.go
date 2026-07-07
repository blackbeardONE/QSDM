package enrollment

import "testing"

func TestRevokeLegacyOwnersAtFixedHeight(t *testing.T) {
	state := NewInMemoryState()
	legacy := EnrollmentRecord{
		NodeID:           "legacy-rig",
		Owner:            "qsdm1legacy-miner",
		GPUUUID:          "GPU-legacy",
		HMACKey:          make([]byte, MinHMACKeyLen),
		StakeDust:        1_000_000_000,
		EnrolledAtHeight: 10,
	}
	canonical := EnrollmentRecord{
		NodeID:           "signed-rig",
		Owner:            "13d786706accfbe77c5ddf6fc6757e1cca07bd01aff0cad3dcf9411d92cf11c9",
		GPUUUID:          "GPU-signed",
		HMACKey:          make([]byte, MinHMACKeyLen),
		StakeDust:        1_000_000_000,
		EnrolledAtHeight: 20,
	}
	if err := state.ApplyEnroll(legacy); err != nil {
		t.Fatalf("apply legacy: %v", err)
	}
	if err := state.ApplyEnroll(canonical); err != nil {
		t.Fatalf("apply canonical: %v", err)
	}

	if got := state.RevokeLegacyOwners(LegacyOwnerSunsetHeight - 1); len(got) != 0 {
		t.Fatalf("revoked before activation: %+v", got)
	}
	got := state.RevokeLegacyOwners(LegacyOwnerSunsetHeight)
	if len(got) != 1 || got[0].NodeID != legacy.NodeID {
		t.Fatalf("revoked=%+v, want legacy-rig only", got)
	}
	legacyAfter, _ := state.Lookup(legacy.NodeID)
	if legacyAfter.RevokedAtHeight != LegacyOwnerSunsetHeight {
		t.Fatalf("revoked height=%d", legacyAfter.RevokedAtHeight)
	}
	if legacyAfter.UnbondMaturesAtHeight != LegacyOwnerSunsetHeight+UnbondWindow {
		t.Fatalf("unbond maturity=%d", legacyAfter.UnbondMaturesAtHeight)
	}
	if bound, _ := state.GPUUUIDBound(legacy.GPUUUID); bound != "" {
		t.Fatalf("legacy GPU still bound to %q", bound)
	}
	if signedAfter, _ := state.Lookup(canonical.NodeID); !signedAfter.Active() {
		t.Fatal("canonical wallet enrollment was revoked")
	}
	if repeated := state.RevokeLegacyOwners(LegacyOwnerSunsetHeight + 10); len(repeated) != 0 {
		t.Fatalf("migration was not idempotent: %+v", repeated)
	}
}
