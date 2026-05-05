package repmetrics

import "testing"

type fakeProvider struct{ snap ReputationSnapshot }

func (f *fakeProvider) Snapshot() ReputationSnapshot { return f.snap }

func TestRegisterAndProviders(t *testing.T) {
	defer RegisterReputationProvider("tx", nil)
	defer RegisterReputationProvider("evidence", nil)
	defer RegisterReputationProvider("tx_v2", nil)

	if got := Providers(); len(got) != 0 {
		t.Fatalf("Providers() = %v before any register; want empty", got)
	}

	tx := &fakeProvider{snap: ReputationSnapshot{TotalPeers: 5, BannedPeers: 1, MaxScore: 100}}
	ev := &fakeProvider{snap: ReputationSnapshot{TotalPeers: 3}}
	RegisterReputationProvider("tx", tx)
	RegisterReputationProvider("evidence", ev)

	got := Providers()
	if len(got) != 2 {
		t.Fatalf("Providers() len = %d; want 2", len(got))
	}
	if got["tx"].Snapshot().TotalPeers != 5 {
		t.Errorf("tx.TotalPeers = %d; want 5", got["tx"].Snapshot().TotalPeers)
	}
	if got["evidence"].Snapshot().TotalPeers != 3 {
		t.Errorf("evidence.TotalPeers = %d; want 3", got["evidence"].Snapshot().TotalPeers)
	}

	// Idempotent overwrite under same name.
	tx2 := &fakeProvider{snap: ReputationSnapshot{TotalPeers: 99}}
	RegisterReputationProvider("tx", tx2)
	if Providers()["tx"].Snapshot().TotalPeers != 99 {
		t.Errorf("after overwrite tx.TotalPeers = %d; want 99", Providers()["tx"].Snapshot().TotalPeers)
	}

	// Detach via nil.
	RegisterReputationProvider("tx", nil)
	if _, ok := Providers()["tx"]; ok {
		t.Errorf("tx still present after nil detach")
	}
	if _, ok := Providers()["evidence"]; !ok {
		t.Errorf("evidence detached unexpectedly")
	}
}

// Providers() must return a copy so callers can't mutate the
// internal map by accident — important because the scrape
// renders by iterating the returned map under no lock.
func TestProviders_ReturnsCopy(t *testing.T) {
	defer RegisterReputationProvider("a", nil)
	RegisterReputationProvider("a", &fakeProvider{snap: ReputationSnapshot{TotalPeers: 1}})

	snap := Providers()
	delete(snap, "a")

	if _, ok := Providers()["a"]; !ok {
		t.Errorf("Providers() returned a live reference; mutating the result removed entry from the registry")
	}
}
