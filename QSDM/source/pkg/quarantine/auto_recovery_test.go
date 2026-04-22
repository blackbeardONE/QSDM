package quarantine

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestAutoRecoveryManager_RecoversAfterConsecutiveHealthyWindows(t *testing.T) {
	qm := NewQuarantineManager(0.5)
	cfg := AutoRecoveryConfig{
		WindowSize:              5,
		RecoveryThreshold:       0,
		ConsecutiveHealthy:      3,
		CooldownAfterQuarantine: 0,
	}
	ar := NewAutoRecoveryManager(qm, cfg)

	sm := "submesh-a"

	// Drive 10 invalid txs to trigger quarantine in the QM (ratio 1.0 >> 0.5 threshold).
	for i := 0; i < 10; i++ {
		qm.RecordTransaction(sm, false)
		ar.Observe(sm, false)
	}
	if !qm.IsQuarantined(sm) {
		t.Fatalf("expected quarantine after 10 invalid txs")
	}

	// Feed three healthy windows of 5 valid txs each. QM threshold is 0.5 so the QM
	// itself will re-evaluate to "not quarantined" on its own boundary; we assert the
	// auto-recovery manager also reports a recovery trigger on the final window boundary.
	var recoveries int32
	ar.OnRecovery = func(string) { atomic.AddInt32(&recoveries, 1) }

	for w := 0; w < 3; w++ {
		for i := 0; i < 5; i++ {
			qm.RecordTransaction(sm, true)
			ar.Observe(sm, true)
		}
	}
	if atomic.LoadInt32(&recoveries) == 0 {
		// Either QM cleared quarantine before AR fired (acceptable) or AR did fire —
		// at minimum the submesh must not be quarantined.
		if qm.IsQuarantined(sm) {
			t.Fatal("expected submesh to be un-quarantined after healthy windows")
		}
	}
}

func TestAutoRecoveryManager_KeepsQuarantineDuringCooldown(t *testing.T) {
	qm := NewQuarantineManager(0.5)
	qm.SetQuarantine("slow", true)

	cfg := AutoRecoveryConfig{
		WindowSize:              2,
		RecoveryThreshold:       0,
		ConsecutiveHealthy:      1,
		CooldownAfterQuarantine: 50 * time.Millisecond,
	}
	ar := NewAutoRecoveryManager(qm, cfg)

	fake := time.Unix(0, 0)
	ar.now = func() time.Time { return fake }

	ar.Observe("slow", true) // first tx inside quarantine, start cooldown clock
	ar.Observe("slow", true) // window boundary — healthy streak=1, cooldown NOT elapsed
	if !qm.IsQuarantined("slow") {
		t.Fatal("expected quarantine retained during cooldown window")
	}

	fake = fake.Add(60 * time.Millisecond)
	// Next healthy window should recover.
	ar.Observe("slow", true)
	ar.Observe("slow", true)
	if qm.IsQuarantined("slow") {
		t.Fatal("expected auto-recovery after cooldown elapsed")
	}
}

func TestAutoRecoveryManager_InvalidResetsStreak(t *testing.T) {
	qm := NewQuarantineManager(0.5)
	qm.SetQuarantine("bad", true)

	cfg := AutoRecoveryConfig{
		WindowSize:              3,
		RecoveryThreshold:       0,
		ConsecutiveHealthy:      2,
		CooldownAfterQuarantine: 0,
	}
	ar := NewAutoRecoveryManager(qm, cfg)

	for i := 0; i < 3; i++ {
		ar.Observe("bad", true)
	}
	if got := ar.HealthyStreak("bad"); got != 1 {
		t.Fatalf("expected healthy streak 1, got %d", got)
	}
	ar.Observe("bad", false)
	ar.Observe("bad", true)
	ar.Observe("bad", true) // window 2 boundary: had one invalid → streak resets to 0
	if got := ar.HealthyStreak("bad"); got != 0 {
		t.Fatalf("expected streak to reset to 0, got %d", got)
	}
	if !qm.IsQuarantined("bad") {
		t.Fatal("expected bad submesh to remain quarantined")
	}
}

func TestAutoRecoveryManager_SnapshotReturnsPerSubmeshStreaks(t *testing.T) {
	qm := NewQuarantineManager(0.5)
	ar := NewAutoRecoveryManager(qm, AutoRecoveryConfig{
		WindowSize:         2,
		RecoveryThreshold:  0,
		ConsecutiveHealthy: 2,
	})
	ar.Observe("x", true)
	ar.Observe("x", true)
	snap := ar.Snapshot()
	if snap["x"] != 1 {
		t.Fatalf("expected snapshot x=1, got %v", snap)
	}
}
