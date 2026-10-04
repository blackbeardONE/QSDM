package legacymining

import "testing"

// HL2 WP-H: OwnerEpochStats is the bounded metrics view of the owner epoch.
func TestLedgerV2OwnerEpochStats(t *testing.T) {
	if s := newLedgerHarness(t).l.OwnerEpochStats(); s != (OwnerEpochStats{}) {
		t.Fatalf("v1: %+v", s)
	}
	h, _ := newLedgerHarnessV2(t, true)
	h.g.set(func(g *ledgerFakeGuard) { g.cfg.OwnerEpochCapCell = 1 })
	h.cell = 1.5
	if s := h.l.OwnerEpochStats(); s != (OwnerEpochStats{}) {
		t.Fatalf("empty: %+v", s)
	}
	h.accept(ltMinerA)
	h.accept(ltMinerA)
	h.accept(ltMinerB)
	if _, err := h.tick(); err != nil { // A 1.0, B 0.5
		t.Fatal(err)
	}
	h.noFreeze()
	want := OwnerEpochStats{Epoch: 0, Owners: 2, Total: 1.5, Max: 1, AtCap: 1}
	if s := h.l.OwnerEpochStats(); s != want {
		t.Fatalf("stats %+v, want %+v", s, want)
	}
	// A new owner epoch starts from zero.
	h.height = OwnerEpochBlocks - 2
	if _, err := h.tick(); err != nil {
		t.Fatal(err)
	}
	if s := h.l.OwnerEpochStats(); s != (OwnerEpochStats{Epoch: 1}) {
		t.Fatalf("epoch 1: %+v", s)
	}
}
