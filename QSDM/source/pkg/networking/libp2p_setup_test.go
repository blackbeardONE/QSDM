package networking

import (
	"context"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
)

// Exercises go-libp2p + gossipsub construction after dependency upgrades (short, no peers required).
func TestSetupLibP2P_Smoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := logging.NewSilentLogger()
	net, err := SetupLibP2P(ctx, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = net.Close() }()

	if net.Host == nil || net.Host.ID().String() == "" {
		t.Fatal("expected non-empty host ID")
	}
	if net.PubSub == nil || net.Topic == nil || net.Sub == nil {
		t.Fatal("expected pubsub stack initialized")
	}
}
