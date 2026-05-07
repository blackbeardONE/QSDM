package telemetrycheck

import (
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
)

func TestHMACAdapter_NilCheckerPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic")
		}
	}()
	_ = NewHMACAdapter(nil, 16)
}

func TestHMACAdapter_DefaultRingCap(t *testing.T) {
	c := NewCatalog()
	c.LoadBaseline()
	a := NewHMACAdapter(NewChecker(c), 0)
	if a.ringCap != 256 {
		t.Fatalf("default ring cap = %d, want 256", a.ringCap)
	}
}

func TestHMACAdapter_MatchPathRecordsNothing(t *testing.T) {
	c := NewCatalog()
	c.LoadBaseline()
	a := NewHMACAdapter(NewChecker(c), 16)

	now := time.Unix(1_700_000_000, 0)
	a.OnHMACAccept(hmac.Bundle{
		ComputeCap: "8.6", GPUName: "NVIDIA GeForce RTX 3050",
		DriverVer: "576.28", NodeID: "alice-001",
	}, mining.Proof{
		Attestation: mining.Attestation{
			Type: "nvidia-hmac-v1", GPUArch: "ampere",
		},
		MinerAddr: "qsdm1alice", Height: 42,
	}, now)

	if got := a.RecentAnomalies(10); len(got) != 0 {
		t.Fatalf("want 0 anomalies, got %d: %+v", len(got), got)
	}
	if a.AnomalyCount() != 0 {
		t.Fatalf("AnomalyCount = %d, want 0", a.AnomalyCount())
	}
}

func TestHMACAdapter_MismatchPathRecorded(t *testing.T) {
	c := NewCatalog()
	c.LoadBaseline()
	a := NewHMACAdapter(NewChecker(c), 16)

	now := time.Unix(1_700_000_000, 0)
	a.OnHMACAccept(hmac.Bundle{
		ComputeCap: "9.0", // <- impossible for ampere
		GPUName:    "NVIDIA GeForce RTX 3050",
		DriverVer:  "576.28", NodeID: "alice-001",
	}, mining.Proof{
		Attestation: mining.Attestation{
			Type: "nvidia-hmac-v1", GPUArch: "ampere",
		},
		MinerAddr: "qsdm1alice", Height: 42,
	}, now)

	got := a.RecentAnomalies(10)
	if len(got) != 1 {
		t.Fatalf("want 1 anomaly, got %d", len(got))
	}
	rec := got[0]
	if rec.Verdict != "mismatch" {
		t.Errorf("verdict = %q, want mismatch", rec.Verdict)
	}
	if !rec.HasMajor {
		t.Errorf("HasMajor = false, expected true (impossible arch+cc)")
	}
	if rec.NodeID != "alice-001" || rec.GPUName != "NVIDIA GeForce RTX 3050" {
		t.Errorf("rec metadata wrong: %+v", rec)
	}
	if rec.Height != 42 {
		t.Errorf("Height = %d", rec.Height)
	}
}

func TestHMACAdapter_UnknownSKURecorded(t *testing.T) {
	c := NewCatalog()
	c.LoadBaseline()
	a := NewHMACAdapter(NewChecker(c), 16)

	now := time.Unix(1_700_000_000, 0)
	a.OnHMACAccept(hmac.Bundle{
		ComputeCap: "8.6", GPUName: "NVIDIA RTX 9999",
	}, mining.Proof{
		Attestation: mining.Attestation{Type: "nvidia-hmac-v1", GPUArch: "ampere"},
	}, now)

	got := a.RecentAnomalies(10)
	if len(got) != 1 {
		t.Fatalf("want 1 anomaly")
	}
	if got[0].Verdict != "unknown_sku" {
		t.Errorf("verdict = %q", got[0].Verdict)
	}
}

func TestHMACAdapter_RingBufferEvictsOldest(t *testing.T) {
	c := NewCatalog()
	c.LoadBaseline()
	const cap = 4
	a := NewHMACAdapter(NewChecker(c), cap)

	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < cap+3; i++ {
		a.OnHMACAccept(hmac.Bundle{
			ComputeCap: "9.0", // mismatch every time
			GPUName:    "NVIDIA GeForce RTX 3050",
			NodeID:     "alice-001",
		}, mining.Proof{
			Attestation: mining.Attestation{Type: "nvidia-hmac-v1", GPUArch: "ampere"},
			Height:      uint64(i),
		}, now.Add(time.Duration(i)*time.Second))
	}

	got := a.RecentAnomalies(100)
	if len(got) != cap {
		t.Fatalf("got %d, want %d", len(got), cap)
	}
	// Newest first, so the highest height is index 0
	if got[0].Height != cap+2 {
		t.Errorf("newest height = %d, want %d", got[0].Height, cap+2)
	}
	// Oldest in buffer is the (cap+3 - cap)-th submission = height 3
	if got[cap-1].Height != 3 {
		t.Errorf("oldest in buffer height = %d, want 3", got[cap-1].Height)
	}
	if a.AnomalyCount() != uint64(cap+3) {
		t.Errorf("AnomalyCount = %d, want %d (counter never evicts)", a.AnomalyCount(), cap+3)
	}
}

func TestHMACAdapter_RecentAnomaliesNNonPositiveOrEmpty(t *testing.T) {
	c := NewCatalog()
	c.LoadBaseline()
	a := NewHMACAdapter(NewChecker(c), 16)
	if got := a.RecentAnomalies(0); len(got) != 0 {
		t.Errorf("n=0 returned %d", len(got))
	}
	if got := a.RecentAnomalies(-5); len(got) != 0 {
		t.Errorf("n=-5 returned %d", len(got))
	}
	if got := a.RecentAnomalies(10); len(got) != 0 {
		t.Errorf("empty ring returned %d", len(got))
	}
}
