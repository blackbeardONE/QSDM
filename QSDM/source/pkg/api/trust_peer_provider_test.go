package api

import (
	"testing"
	"time"
)

type stubEnum struct{ addrs []string }

func (s stubEnum) ActiveValidatorAddresses() []string { return s.addrs }

func TestValidatorSetPeerProvider_PeerAttestations_HappyPath(t *testing.T) {
	p := NewValidatorSetPeerProvider(stubEnum{addrs: []string{"alpha", "bravo", "charlie"}})
	got := p.PeerAttestations()
	if len(got) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(got))
	}
	for i, want := range []string{"alpha", "bravo", "charlie"} {
		if got[i].NodeID != want {
			t.Errorf("row %d: NodeID=%q, want %q", i, got[i].NodeID, want)
		}
		if !got[i].AttestedAt.IsZero() {
			t.Errorf("row %d: AttestedAt should be zero (no cross-peer gossip yet), got %v", i, got[i].AttestedAt)
		}
		if got[i].GPUAvailable || got[i].NGCHMACOK {
			t.Errorf("row %d: must not fabricate GPU/HMAC fields", i)
		}
	}
}

func TestValidatorSetPeerProvider_SkipsEmptyAddresses(t *testing.T) {
	p := NewValidatorSetPeerProvider(stubEnum{addrs: []string{"", "alpha", "", "bravo"}})
	got := p.PeerAttestations()
	if len(got) != 2 {
		t.Fatalf("empty addresses should be skipped; got %d rows: %+v", len(got), got)
	}
}

// TestValidatorSetPeerProvider_SkipsSentinelAddresses locks in the
// sentinel filter that stops the BFT-bootstrap placeholder from
// inflating the trust transparency denominator. See file-level comment
// on sentinelValidatorAddresses for the reasoning.
func TestValidatorSetPeerProvider_SkipsSentinelAddresses(t *testing.T) {
	p := NewValidatorSetPeerProvider(stubEnum{addrs: []string{"bootstrap", "alpha", "bravo"}})
	got := p.PeerAttestations()
	if len(got) != 2 {
		t.Fatalf("sentinel 'bootstrap' should be filtered; got %d rows: %+v", len(got), got)
	}
	for _, row := range got {
		if row.NodeID == "bootstrap" {
			t.Fatalf("sentinel leaked into trust surface: %+v", row)
		}
	}
}

func TestValidatorSetPeerProvider_NilEnumeratorPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on nil enumerator; misconfiguration must fail fast")
		}
	}()
	_ = NewValidatorSetPeerProvider(nil)
}

func TestValidatorSetPeerProvider_FeedsAggregator(t *testing.T) {
	// End-to-end sanity: stitch the peer provider into a real
	// TrustAggregator and confirm the ratio is 0/N when no local
	// source attests, which is the initial deployment state.
	fixed := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fixed }
	agg := NewTrustAggregator(TrustConfig{
		PeerProvider: NewValidatorSetPeerProvider(stubEnum{addrs: []string{"v1", "v2", "v3"}}),
		Clock:        clock,
	})
	agg.Refresh()
	s, _ := agg.Summary()
	if s.TotalPublic != 3 {
		t.Fatalf("TotalPublic=%d, want 3", s.TotalPublic)
	}
	if s.Attested != 0 {
		t.Fatalf("Attested=%d, want 0 (no cross-peer gossip yet)", s.Attested)
	}
	if s.Ratio != 0.0 {
		t.Fatalf("Ratio=%v, want 0.0", s.Ratio)
	}
	if s.NGCServiceStatus != "healthy" {
		t.Fatalf("NGCServiceStatus=%q, want healthy (zero opt-in is healthy per §8.5.4)", s.NGCServiceStatus)
	}
}
