package main

import "testing"

// The capability decision is pinned here because it was never tested and I got
// it wrong once per copy-paste: two Scylla-side branches fixed in 3dd199d, and
// a THIRD -- the non-Scylla SQLite-init-failure fallback, which is the default
// path on any CGO_ENABLED=0 build -- left logging nothing, gating nothing, and
// reporting the component HEALTHY for a backend that cannot settle a transfer.
//
// A reviewer found that branch. The commit that missed it claimed the flag was
// "verified read on the exact branches that print it", which was eyeball
// verification with no test behind it. This is that test.
func TestSettlementCapability_nonSettleableBackends(t *testing.T) {
	for _, tc := range []struct {
		name string
		cap  SettlementCapability
	}{
		{"scylla is a stub", settlementScylla},
		{"file storage refuses by construction", settlementFile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.cap.CanSettle {
				t.Fatal("this backend cannot settle; if that changed, update the table not the test")
			}
			if !tc.cap.HealthDegraded() {
				t.Error("a backend that cannot settle must report Degraded, never Healthy -- " +
					"reporting Healthy beside an endpoint that returns 501 is the exact lie " +
					"the third branch was telling")
			}
			if tc.cap.OperatorWarning() == "" {
				t.Error("an operator must be told at startup, not per failed request")
			}
			if got := tc.cap.FatalMessage(true); got == "" {
				t.Error("QSDM_REQUIRE_SETTLEABLE_STORAGE=1 must abort on this backend; an " +
					"instruction that does nothing is worse than none, which is what the " +
					"QSDM_REQUIRE_SQLITE_STORAGE advice was")
			}
			if got := tc.cap.FatalMessage(false); got != "" {
				t.Errorf("the gate is opt-in and defaults OFF -- refusing to boot unasked would "+
					"break every deployment that never touches wallet writes; got %q", got)
			}
			if tc.cap.Reason == "" {
				t.Error("the reason must name the mechanism so the log points at code")
			}
		})
	}
}

// SQLite is the only backend that can settle, and must not be warned about or
// gated. If a second backend implements ApplyTransferAtomic this test is what
// tells the implementer to add it here.
func TestSettlementCapability_sqliteIsTheOnlySettleableBackend(t *testing.T) {
	if !settlementSQLite.CanSettle {
		t.Fatal("SQLite implements ApplyTransferAtomic (sqlite_v041.go)")
	}
	if settlementSQLite.HealthDegraded() {
		t.Error("a settleable backend must report Healthy")
	}
	if got := settlementSQLite.OperatorWarning(); got != "" {
		t.Errorf("no warning for a backend that works, got %q", got)
	}
	if got := settlementSQLite.FatalMessage(true); got != "" {
		t.Errorf("QSDM_REQUIRE_SETTLEABLE_STORAGE=1 must NOT abort on SQLite, got %q", got)
	}
}
