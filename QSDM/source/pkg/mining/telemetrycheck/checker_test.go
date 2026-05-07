package telemetrycheck

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/telemetry"
)

func newRealistic3050Catalog(t *testing.T) *Catalog {
	t.Helper()
	c := NewCatalog()
	c.Apply(&telemetry.ReferenceProfile{
		SignerID: "attester-12a0d1aa",
		IssuedAt: 1_700_000_000,
		GPUs: []telemetry.GPUObservation{
			{
				Name:                  "NVIDIA GeForce RTX 3050",
				Architecture:          "ampere",
				ComputeCapability:     "8.6",
				MemoryTotalMB:         8192,
				PCIeGen:               4,
				PCIeWidth:             16,
				PowerMaxW:             143,
				ECCSupported:          false,
				ClockGraphicsBoostMHz: 2145,
				ClockMemoryMHz:        7001,
				DriverVersionsSeen:    []string{"576.28"},
				VBIOSVersionsSeen:     []string{"94.06.37.00.c6"},
			},
		},
	})
	return c
}

func TestNewChecker_PanicsOnNilCatalog(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on nil catalog")
		}
	}()
	_ = NewChecker(nil)
}

func TestChecker_MatchPath(t *testing.T) {
	c := newRealistic3050Catalog(t)
	chk := NewChecker(c)
	chk.Now = func() time.Time { return time.Unix(1_700_001_000, 0) }

	v := chk.Check(Claim{
		AttestationType: "nvidia-hmac-v1",
		NodeID:          "rtx3050-real-001",
		GPUUUID:         "GPU-39925fa6",
		GPUName:         "NVIDIA GeForce RTX 3050",
		GPUArch:         "ampere",
		ComputeCap:      "8.6",
		DriverVer:       "576.28",
		MinerAddr:       "qsdm1minerx",
		Height:          42,
	})
	if v.Kind != VerdictMatch {
		t.Fatalf("Kind = %q, want match. mismatches=%+v", v.Kind, v.Mismatches)
	}
	if len(v.Mismatches) != 0 {
		t.Fatalf("mismatches = %+v", v.Mismatches)
	}
	if !reflect.DeepEqual(v.MatchedReferences, []string{"attester-12a0d1aa"}) {
		t.Fatalf("MatchedReferences = %v", v.MatchedReferences)
	}
	checked, matched, _, _, _ := chk.Counters()
	if checked != 1 || matched != 1 {
		t.Fatalf("counters checked=%d matched=%d", checked, matched)
	}
}

func TestChecker_MismatchOnImpossibleArchCC(t *testing.T) {
	c := newRealistic3050Catalog(t)
	chk := NewChecker(c)

	// Claim "ampere" but compute_cap 9.0 (Hopper). Impossible
	// without ANY catalog data.
	v := chk.Check(Claim{
		GPUName:    "NVIDIA GeForce RTX 3050",
		GPUArch:    "ampere",
		ComputeCap: "9.0", // <-- impossible for ampere
		DriverVer:  "576.28",
	})
	if v.Kind != VerdictMismatch {
		t.Fatalf("Kind = %q, want mismatch", v.Kind)
	}
	fields := v.MismatchedFields()
	if !containsString(fields, "arch") {
		t.Errorf("expected arch mismatch, got %v", fields)
	}
	if !containsString(fields, "compute_cap") {
		t.Errorf("expected compute_cap mismatch (catalog says 8.6), got %v", fields)
	}
	if !v.HasMajor() {
		t.Errorf("expected at least one major mismatch")
	}
}

func TestChecker_UnknownSKU(t *testing.T) {
	c := newRealistic3050Catalog(t)
	chk := NewChecker(c)

	v := chk.Check(Claim{
		GPUName:    "NVIDIA RTX 9999 (FROM_THE_FUTURE)",
		GPUArch:    "ampere",
		ComputeCap: "8.6",
		DriverVer:  "576.28",
	})
	if v.Kind != VerdictUnknownSKU {
		t.Fatalf("Kind = %q, want unknown_sku", v.Kind)
	}
	_, _, _, unk, _ := chk.Counters()
	if unk != 1 {
		t.Fatalf("unknown counter = %d", unk)
	}
}

func TestChecker_UnknownSKUStillFiresArchRule(t *testing.T) {
	// Even when the SKU isn't in the catalog, the
	// always-on arch+CC rule must still flag impossible
	// combinations. This is the safety net for "miner
	// claims a brand-new GPU name to dodge the catalog".
	c := newRealistic3050Catalog(t)
	chk := NewChecker(c)

	v := chk.Check(Claim{
		GPUName:    "NVIDIA RTX 9999 (FROM_THE_FUTURE)",
		GPUArch:    "ampere",
		ComputeCap: "9.0", // impossible for ampere
	})
	if v.Kind != VerdictUnknownSKU {
		t.Fatalf("Kind = %q (always-on rules don't downgrade UnknownSKU)", v.Kind)
	}
	if !containsString(v.MismatchedFields(), "arch") {
		t.Errorf("arch rule should still fire on unknown SKU; got %v", v.Mismatches)
	}
}

func TestChecker_SkippedWhenCatalogEmpty(t *testing.T) {
	c := NewCatalog()
	chk := NewChecker(c)
	v := chk.Check(Claim{GPUName: "NVIDIA RTX 3050", ComputeCap: "8.6"})
	if v.Kind != VerdictSkipped {
		t.Fatalf("Kind = %q want skipped", v.Kind)
	}
	_, _, _, _, sk := chk.Counters()
	if sk != 1 {
		t.Fatalf("skipped counter = %d", sk)
	}
}

func TestChecker_SkippedWhenClaimEmpty(t *testing.T) {
	c := newRealistic3050Catalog(t)
	chk := NewChecker(c)
	v := chk.Check(Claim{}) // nothing checkable
	if v.Kind != VerdictSkipped {
		t.Fatalf("Kind = %q want skipped", v.Kind)
	}
}

func TestChecker_BadDriverVerFires(t *testing.T) {
	c := newRealistic3050Catalog(t)
	chk := NewChecker(c)
	v := chk.Check(Claim{
		GPUName:    "NVIDIA GeForce RTX 3050",
		GPUArch:    "ampere",
		ComputeCap: "8.6",
		DriverVer:  "576.28-RC-malformed",
	})
	if v.Kind != VerdictMismatch {
		t.Fatalf("Kind = %q, want mismatch", v.Kind)
	}
	if !containsString(v.MismatchedFields(), "driver_ver_format") {
		t.Errorf("expected driver_ver_format fire, got %v", v.MismatchedFields())
	}
	// Severity should be minor — bad format isn't damning.
	if v.HasMajor() {
		t.Errorf("driver_ver_format alone shouldn't be major")
	}
}

func TestChecker_PerFieldCountersAccumulate(t *testing.T) {
	c := newRealistic3050Catalog(t)
	chk := NewChecker(c)
	for i := 0; i < 3; i++ {
		chk.Check(Claim{
			GPUName: "NVIDIA GeForce RTX 3050",
			GPUArch: "ampere", ComputeCap: "9.0", // arch + compute_cap fire
		})
	}
	m := chk.MismatchesByField()
	if m["arch"] != 3 {
		t.Errorf("arch count = %d", m["arch"])
	}
	if m["compute_cap"] != 3 {
		t.Errorf("compute_cap count = %d", m["compute_cap"])
	}
}

func TestChecker_Concurrent(t *testing.T) {
	c := newRealistic3050Catalog(t)
	chk := NewChecker(c)
	var wg sync.WaitGroup
	const N = 64
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			chk.Check(Claim{
				GPUName: "NVIDIA GeForce RTX 3050",
				GPUArch: "ampere", ComputeCap: "8.6", DriverVer: "576.28",
			})
		}()
	}
	wg.Wait()
	checked, matched, _, _, _ := chk.Counters()
	if checked != N || matched != N {
		t.Fatalf("counters drift under concurrency: checked=%d matched=%d", checked, matched)
	}
}

func TestVerdict_HasMajor(t *testing.T) {
	cases := []struct {
		name  string
		v     Verdict
		major bool
	}{
		{"empty", Verdict{}, false},
		{"only minor", Verdict{Mismatches: []FieldMismatch{{Severity: "minor"}}}, false},
		{"contains major", Verdict{Mismatches: []FieldMismatch{{Severity: "minor"}, {Severity: "major"}}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.v.HasMajor(); got != tc.major {
				t.Errorf("got %v want %v", got, tc.major)
			}
		})
	}
}

// containsString is a tiny helper to keep the assertions
// readable in the cases above. Lives in this _test.go file
// so production code carries no dead code.
func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
