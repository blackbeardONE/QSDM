package networking

import (
	"testing"
	"time"
)

func TestReputationConfigForEvidence_StricterWeights(t *testing.T) {
	def := DefaultReputationConfig()
	ev := ReputationConfigForEvidence()
	if ev.InvalidTxWeight >= def.InvalidTxWeight {
		t.Fatalf("evidence config should penalize invalid tx more than default")
	}
	if ev.ProtocolViolWeight >= def.ProtocolViolWeight {
		t.Fatalf("evidence config should penalize protocol violations more than default")
	}
	if ev.ValidTxWeight >= def.ValidTxWeight {
		t.Fatalf("evidence config should reward valid tx gossip less than default")
	}
}

func TestReputationTracker_NewPeerInitialScore(t *testing.T) {
	cfg := DefaultReputationConfig()
	rt := NewReputationTracker(cfg)

	score := rt.GetScore("peer-1")
	if score != cfg.InitialScore {
		t.Fatalf("expected initial score %f, got %f", cfg.InitialScore, score)
	}
}

func TestReputationTracker_ValidBlockIncreasesScore(t *testing.T) {
	cfg := DefaultReputationConfig()
	rt := NewReputationTracker(cfg)

	rt.RecordEvent("peer-1", EventValidBlock, 0)
	score := rt.GetScore("peer-1")
	expected := cfg.InitialScore + cfg.ValidBlockWeight
	if score != expected {
		t.Fatalf("expected %f, got %f", expected, score)
	}
}

func TestReputationTracker_InvalidBlockDecreasesScore(t *testing.T) {
	cfg := DefaultReputationConfig()
	rt := NewReputationTracker(cfg)

	rt.RecordEvent("peer-1", EventInvalidBlock, 0)
	score := rt.GetScore("peer-1")
	if score >= cfg.InitialScore {
		t.Fatalf("expected score below initial after invalid block, got %f", score)
	}
}

func TestReputationTracker_BanOnLowScore(t *testing.T) {
	cfg := DefaultReputationConfig()
	cfg.BanThreshold = -100
	cfg.InitialScore = 0
	rt := NewReputationTracker(cfg)

	for i := 0; i < 3; i++ {
		rt.RecordEvent("bad-peer", EventInvalidBlock, 0) // -50 each
	}

	if !rt.IsBanned("bad-peer") {
		t.Fatalf("expected peer to be banned at score %f", rt.GetScore("bad-peer"))
	}
}

func TestReputationTracker_UnbanResetScore(t *testing.T) {
	cfg := DefaultReputationConfig()
	cfg.BanThreshold = -100
	cfg.InitialScore = 0
	rt := NewReputationTracker(cfg)

	for i := 0; i < 3; i++ {
		rt.RecordEvent("bad-peer", EventInvalidBlock, 0)
	}
	if !rt.IsBanned("bad-peer") {
		t.Fatal("expected banned")
	}

	ok := rt.Unban("bad-peer")
	if !ok {
		t.Fatal("Unban should return true")
	}
	if rt.IsBanned("bad-peer") {
		t.Fatal("peer should be unbanned")
	}
	if rt.GetScore("bad-peer") != cfg.InitialScore {
		t.Fatal("score should be reset to initial")
	}
}

func TestReputationTracker_LatencyPenalty(t *testing.T) {
	cfg := DefaultReputationConfig()
	cfg.LatencyBaselineMs = 100
	cfg.LatencyPenaltyPerMs = -0.1
	rt := NewReputationTracker(cfg)

	rt.RecordEvent("peer-slow", EventLatencyReport, 300)
	score := rt.GetScore("peer-slow")
	// 200ms excess * -0.1 = -20
	expected := cfg.InitialScore - 20
	if score != expected {
		t.Fatalf("expected %f, got %f", expected, score)
	}

	rec, _ := rt.GetPeer("peer-slow")
	if rec.AvgLatencyMs != 300 {
		t.Fatalf("expected avg latency 300, got %f", rec.AvgLatencyMs)
	}
}

func TestReputationTracker_NoLatencyPenaltyBelowBaseline(t *testing.T) {
	cfg := DefaultReputationConfig()
	cfg.LatencyBaselineMs = 200
	rt := NewReputationTracker(cfg)

	rt.RecordEvent("peer-fast", EventLatencyReport, 100)
	if rt.GetScore("peer-fast") != cfg.InitialScore {
		t.Fatal("no penalty expected for fast latency")
	}
}

func TestReputationTracker_ScoreCapped(t *testing.T) {
	cfg := DefaultReputationConfig()
	cfg.MaxScore = 200
	cfg.InitialScore = 190
	rt := NewReputationTracker(cfg)

	rt.RecordEvent("peer-1", EventValidBlock, 0) // +10
	rt.RecordEvent("peer-1", EventValidBlock, 0) // +10, would be 210

	score := rt.GetScore("peer-1")
	if score != cfg.MaxScore {
		t.Fatalf("expected capped at %f, got %f", cfg.MaxScore, score)
	}
}

func TestReputationTracker_ScoreFloored(t *testing.T) {
	cfg := DefaultReputationConfig()
	cfg.MinScore = -100
	cfg.InitialScore = 0
	cfg.ProtocolViolWeight = -200
	rt := NewReputationTracker(cfg)

	rt.RecordEvent("peer-1", EventProtocolViolation, 0)
	score := rt.GetScore("peer-1")
	if score != cfg.MinScore {
		t.Fatalf("expected floored at %f, got %f", cfg.MinScore, score)
	}
}

func TestReputationTracker_TopPeersExcludesBanned(t *testing.T) {
	cfg := DefaultReputationConfig()
	cfg.BanThreshold = -100
	cfg.InitialScore = 0
	rt := NewReputationTracker(cfg)

	rt.RecordEvent("good", EventValidBlock, 0)
	for i := 0; i < 3; i++ {
		rt.RecordEvent("bad", EventInvalidBlock, 0)
	}

	top := rt.TopPeers(10)
	for _, p := range top {
		if p.PeerID == "bad" {
			t.Fatal("banned peer should not appear in TopPeers")
		}
	}
	if len(top) != 1 {
		t.Fatalf("expected 1 top peer, got %d", len(top))
	}
}

func TestReputationTracker_BannedPeers(t *testing.T) {
	cfg := DefaultReputationConfig()
	cfg.BanThreshold = -100
	cfg.InitialScore = 0
	rt := NewReputationTracker(cfg)

	for i := 0; i < 3; i++ {
		rt.RecordEvent("bad", EventInvalidBlock, 0)
	}
	rt.RecordEvent("good", EventValidBlock, 0)

	banned := rt.BannedPeers()
	if len(banned) != 1 || banned[0].PeerID != "bad" {
		t.Fatal("expected only 'bad' in banned list")
	}
}

func TestReputationTracker_DecayAll(t *testing.T) {
	cfg := DefaultReputationConfig()
	cfg.DecayFactor = 0.5
	rt := NewReputationTracker(cfg)

	rt.RecordEvent("peer-1", EventValidBlock, 0) // score = 110
	rt.DecayAll()
	score := rt.GetScore("peer-1")
	if score != 55 {
		t.Fatalf("expected 55 after 50%% decay, got %f", score)
	}
}

func TestReputationTracker_AllPeersSorted(t *testing.T) {
	cfg := DefaultReputationConfig()
	rt := NewReputationTracker(cfg)

	rt.RecordEvent("low", EventInvalidBlock, 0)
	rt.RecordEvent("mid", EventValidTx, 0)
	rt.RecordEvent("high", EventValidBlock, 0)

	all := rt.AllPeers()
	if len(all) != 3 {
		t.Fatalf("expected 3, got %d", len(all))
	}
	if all[0].Score < all[1].Score || all[1].Score < all[2].Score {
		t.Fatal("expected descending order")
	}
}

func TestReputationTracker_PeerCount(t *testing.T) {
	rt := NewReputationTracker(DefaultReputationConfig())
	rt.RecordEvent("a", EventValidTx, 0)
	rt.RecordEvent("b", EventValidTx, 0)
	if rt.PeerCount() != 2 {
		t.Fatal("expected 2 peers")
	}
}

func TestReputationTracker_StartStop(t *testing.T) {
	cfg := DefaultReputationConfig()
	cfg.DecayInterval = 50 * time.Millisecond
	cfg.DecayFactor = 0.5
	rt := NewReputationTracker(cfg)
	rt.RecordEvent("peer-1", EventValidBlock, 0) // 110

	rt.Start()
	time.Sleep(120 * time.Millisecond)
	rt.Stop()

	score := rt.GetScore("peer-1")
	if score >= 110 {
		t.Fatalf("expected decay to have lowered score from 110, got %f", score)
	}
}

func TestReputationTracker_EventCounters(t *testing.T) {
	rt := NewReputationTracker(DefaultReputationConfig())

	rt.RecordEvent("peer-1", EventValidBlock, 0)
	rt.RecordEvent("peer-1", EventValidBlock, 0)
	rt.RecordEvent("peer-1", EventInvalidTx, 0)
	rt.RecordEvent("peer-1", EventTimeout, 0)

	rec, ok := rt.GetPeer("peer-1")
	if !ok {
		t.Fatal("peer should exist")
	}
	if rec.ValidBlocks != 2 {
		t.Fatalf("expected 2 valid blocks, got %d", rec.ValidBlocks)
	}
	if rec.InvalidTxs != 1 {
		t.Fatalf("expected 1 invalid tx, got %d", rec.InvalidTxs)
	}
	if rec.Timeouts != 1 {
		t.Fatalf("expected 1 timeout, got %d", rec.Timeouts)
	}
}
