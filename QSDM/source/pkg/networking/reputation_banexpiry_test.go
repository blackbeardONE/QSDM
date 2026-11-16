package networking

// Owner decision 2026-11-15: a peer transport ban expires after 24 hours (before, it lasted until the process
// restarted). After expiry the peer starts again at InitialScore, so the same number of offences bans it again.

import (
	"testing"
	"time"
)

func expiryTracker(t *testing.T) (*ReputationTracker, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	rt := NewReputationTracker(DefaultReputationConfig())
	rt.clock = func() time.Time { return now }
	return rt, &now
}

func expiryBanPeer(rt *ReputationTracker, id string) {
	for i := 0; i < 3; i++ {
		rt.RecordEvent(id, EventProtocolViolation, 0)
	}
}

func TestBanExpiry_DefaultIs24Hours(t *testing.T) {
	if got := DefaultReputationConfig().BanDuration; got != 24*time.Hour {
		t.Fatalf("default ban duration %v, want 24h", got)
	}
	if got := ReputationConfigForEvidence().BanDuration; got != 24*time.Hour {
		t.Fatalf("evidence ban duration %v, want 24h", got)
	}
}

func TestBanExpiry_LiftsAfter24Hours(t *testing.T) {
	rt, now := expiryTracker(t)
	expiryBanPeer(rt, "p")
	if !rt.IsBanned("p") {
		t.Fatal("not banned after three violations")
	}
	*now = now.Add(24*time.Hour - time.Second)
	if !rt.IsBanned("p") {
		t.Fatal("the ban ended before 24 hours")
	}
	if len(rt.BannedPeers()) != 1 || rt.Snapshot().BannedPeers != 1 {
		t.Fatal("the banned list or the metrics lost the ban before 24 hours")
	}
	*now = now.Add(time.Second)
	if rt.IsBanned("p") {
		t.Fatal("the ban did not expire at 24 hours")
	}
	if len(rt.BannedPeers()) != 0 || rt.Snapshot().BannedPeers != 0 {
		t.Fatal("an expired ban is still listed or counted")
	}
	for _, p := range rt.AllPeers() {
		if p.Banned {
			t.Fatal("AllPeers shows an expired ban as active")
		}
	}
}

func TestBanExpiry_FreshStartThenBannedAgain(t *testing.T) {
	rt, now := expiryTracker(t)
	expiryBanPeer(rt, "p")
	*now = now.Add(25 * time.Hour)
	rt.RecordEvent("p", EventValidTx, 0) // any event lifts the expired ban
	rec, _ := rt.GetPeer("p")
	if rec.Banned || rec.Score < DefaultReputationConfig().InitialScore {
		t.Fatalf("after expiry the peer did not start again at the initial score: %+v", rec)
	}
	rt.RecordEvent("p", EventProtocolViolation, 0)
	if rt.IsBanned("p") {
		t.Fatal("one violation after a fresh start banned the peer")
	}
	expiryBanPeer(rt, "p")
	if !rt.IsBanned("p") {
		t.Fatal("the peer was not banned again after new violations")
	}
}

func TestBanExpiry_DecayLiftsExpiredBans(t *testing.T) {
	rt, now := expiryTracker(t)
	expiryBanPeer(rt, "p")
	*now = now.Add(24 * time.Hour)
	rt.DecayAll()
	rec, _ := rt.GetPeer("p")
	if rec.Banned {
		t.Fatal("the periodic decay left an expired ban set")
	}
}

func TestBanExpiry_ZeroDurationKeepsOldBehaviour(t *testing.T) {
	cfg := DefaultReputationConfig()
	cfg.BanDuration = 0
	rt := NewReputationTracker(cfg)
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	rt.clock = func() time.Time { return now }
	expiryBanPeer(rt, "p")
	now = now.Add(1000 * time.Hour)
	if !rt.IsBanned("p") {
		t.Fatal("BanDuration 0 must keep a ban until restart")
	}
}
