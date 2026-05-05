package netmetrics

import "testing"

type fakeProvider struct{ peers int }

func (f *fakeProvider) PeerCount() int { return f.peers }

func TestRegisterAndCurrentProvider(t *testing.T) {
	defer RegisterNetworkProvider(nil) // reset for downstream tests

	if got := CurrentProvider(); got != nil {
		t.Fatalf("CurrentProvider before register = %v; want nil", got)
	}

	p := &fakeProvider{peers: 7}
	RegisterNetworkProvider(p)

	got := CurrentProvider()
	if got == nil {
		t.Fatalf("CurrentProvider after register = nil")
	}
	if got.PeerCount() != 7 {
		t.Errorf("got.PeerCount() = %d; want 7", got.PeerCount())
	}

	// Idempotent overwrite.
	q := &fakeProvider{peers: 3}
	RegisterNetworkProvider(q)
	if got := CurrentProvider().PeerCount(); got != 3 {
		t.Errorf("after re-register PeerCount() = %d; want 3", got)
	}

	// Detach.
	RegisterNetworkProvider(nil)
	if got := CurrentProvider(); got != nil {
		t.Errorf("CurrentProvider after detach = %v; want nil", got)
	}
}

func TestRecordGossipMessage(t *testing.T) {
	startIn, startOut := GossipCounts()

	RecordGossipMessage(DirectionIn)
	RecordGossipMessage(DirectionIn)
	RecordGossipMessage(DirectionOut)
	RecordGossipMessage("not_a_real_direction") // dropped

	gotIn, gotOut := GossipCounts()
	if gotIn != startIn+2 {
		t.Errorf("in counter = %d; want %d", gotIn, startIn+2)
	}
	if gotOut != startOut+1 {
		t.Errorf("out counter = %d; want %d", gotOut, startOut+1)
	}
}
