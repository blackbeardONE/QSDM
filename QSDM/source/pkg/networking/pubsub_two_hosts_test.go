package networking

import (
	"context"
	"fmt"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
)

// TestTwoHostsGossipSubRoundTrip spins two local libp2p hosts, connects them, joins the same pubsub topic,
// and asserts a published payload is received (slow; skipped under -short).
func TestTwoHostsGossipSubRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("two-host libp2p pubsub smoke is slow")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h1.Close() })
	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h2.Close() })

	ps1, err := pubsub.NewGossipSub(ctx, h1)
	if err != nil {
		t.Fatal(err)
	}
	ps2, err := pubsub.NewGossipSub(ctx, h2)
	if err != nil {
		t.Fatal(err)
	}

	t1, err := ps1.Join("qsdm-twohost-smoke")
	if err != nil {
		t.Fatal(err)
	}
	t2, err := ps2.Join("qsdm-twohost-smoke")
	if err != nil {
		t.Fatal(err)
	}
	sub2, err := t2.Subscribe()
	if err != nil {
		t.Fatal(err)
	}

	if err := h1.Connect(ctx, peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()}); err != nil {
		t.Fatalf("connect: %v", err)
	}

	payload := []byte("two-host-smoke-payload")
	errCh := make(chan error, 1)
	go func() {
		for {
			msg, err := sub2.Next(ctx)
			if err != nil {
				errCh <- err
				return
			}
			if msg.ReceivedFrom == h2.ID() {
				continue
			}
			if string(msg.Data) == string(payload) {
				errCh <- nil
				return
			}
		}
	}()

	time.Sleep(800 * time.Millisecond)
	if err := t1.Publish(ctx, payload); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(fmt.Errorf("timeout waiting for pubsub delivery: %w", ctx.Err()))
	}
}
