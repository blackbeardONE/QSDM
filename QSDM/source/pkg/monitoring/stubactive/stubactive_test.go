package stubactive

import (
	"sort"
	"testing"
)

func TestAllKinds_StableOrder(t *testing.T) {
	got := AllKinds()
	sorted := append([]string(nil), got...)
	sort.Strings(sorted)
	for i := range got {
		if got[i] != sorted[i] {
			t.Fatalf("AllKinds() not in sorted order: got %v", got)
		}
	}
	want := 7
	if len(got) != want {
		t.Fatalf("AllKinds() returned %d kinds, want %d (update if a new stub was added)", len(got), want)
	}
}

func TestMarkActive_AndSnapshot(t *testing.T) {
	Reset()
	t.Cleanup(Reset)

	if Active(KindPoE) {
		t.Fatal("KindPoE active before any MarkActive")
	}

	MarkActive(KindPoE)
	if !Active(KindPoE) {
		t.Fatal("KindPoE not active after MarkActive")
	}

	snap := Snapshot()
	if snap[KindPoE] != 1 {
		t.Fatalf("Snapshot[KindPoE]=%d; want 1", snap[KindPoE])
	}
	if snap[KindCC] != 0 {
		t.Fatalf("Snapshot[KindCC]=%d; want 0 (untouched)", snap[KindCC])
	}

	MarkInactive(KindPoE)
	if Active(KindPoE) {
		t.Fatal("KindPoE still active after MarkInactive")
	}
	snap = Snapshot()
	if snap[KindPoE] != 0 {
		t.Fatalf("Snapshot[KindPoE]=%d after MarkInactive; want 0", snap[KindPoE])
	}
}

func TestSnapshot_PrePopulatesAllCanonicalKinds(t *testing.T) {
	// Even before any MarkActive call, Snapshot should return a
	// row per canonical kind so the alerting expression
	// `qsdm_stub_active == 1` evaluates against a time series that
	// always exists.
	Reset()
	t.Cleanup(Reset)

	snap := Snapshot()
	for _, k := range AllKinds() {
		if _, ok := snap[k]; !ok {
			t.Fatalf("Snapshot missing canonical kind %q", k)
		}
		if snap[k] != 0 {
			t.Fatalf("Snapshot[%q]=%d before MarkActive; want 0", k, snap[k])
		}
	}
}

func TestMarkActive_Idempotent(t *testing.T) {
	Reset()
	t.Cleanup(Reset)

	MarkActive(KindCC)
	MarkActive(KindCC)
	MarkActive(KindCC)

	snap := Snapshot()
	if snap[KindCC] != 1 {
		t.Fatalf("repeated MarkActive yielded %d; want 1", snap[KindCC])
	}
}

func TestForwardCompatibility_CustomKindAppears(t *testing.T) {
	// A future stub registers a kind that's not in AllKinds().
	// Snapshot should still include it.
	Reset()
	t.Cleanup(Reset)

	custom := "future_stub_x"
	MarkActive(custom)
	snap := Snapshot()
	if v, ok := snap[custom]; !ok || v != 1 {
		t.Fatalf("custom kind %q absent or 0 in snapshot: %v", custom, snap)
	}
}
