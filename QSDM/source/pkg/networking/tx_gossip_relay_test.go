package networking

import (
	"fmt"
	"testing"
)

func TestTxGossipRelay_MaybePublishOpaqueDedupes(t *testing.T) {
	var n int
	r := NewTxGossipRelay(func(b []byte) error { n++; return nil }, DefaultTxGossipRelayConfig())
	p := []byte("wallet-payload")
	_ = r.MaybePublishOpaque(p)
	_ = r.MaybePublishOpaque(p)
	if n != 1 {
		t.Fatalf("opaque dedupe: want 1 publish got %d", n)
	}
}

func TestTxGossipRelay_DedupeByTxID(t *testing.T) {
	var calls int
	r := NewTxGossipRelay(func(b []byte) error {
		calls++
		return nil
	}, DefaultTxGossipRelayConfig())
	if r == nil {
		t.Fatal("expected relay")
	}
	_ = r.MaybePublish("tx-1", []byte(`{"sig":"x"}`))
	_ = r.MaybePublish("tx-1", []byte(`{"sig":"y"}`))
	if calls != 1 {
		t.Fatalf("expected 1 publish, got %d", calls)
	}
}

func TestTxGossipRelay_RetriesAfterPublishError(t *testing.T) {
	var n int
	failFirst := true
	r := NewTxGossipRelay(func(b []byte) error {
		n++
		if failFirst {
			failFirst = false
			return fmt.Errorf("boom")
		}
		return nil
	}, DefaultTxGossipRelayConfig())
	if err := r.MaybePublish("a", []byte("x")); err == nil {
		t.Fatal("expected first publish error")
	}
	if err := r.MaybePublish("a", []byte("x")); err != nil {
		t.Fatalf("second publish: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 publish attempts, got %d", n)
	}
}
