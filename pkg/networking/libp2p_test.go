package networking

import (
    "context"
    "testing"
    "time"
    "github.com/blackbeardONE/QSDM/internal/logging"
)

func TestSetupLibP2P(t *testing.T) {
    logging.SetupLogger("test_libp2p.log")

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    net, err := SetupLibP2P(ctx)
    if err != nil {
        t.Fatalf("SetupLibP2P failed: %v", err)
    }
    defer net.Close()

    if net.Host == nil {
        t.Fatalf("Host is nil after SetupLibP2P")
    }

    if net.PubSub == nil {
        t.Fatalf("PubSub is nil after SetupLibP2P")
    }

    if net.Topic == nil {
        t.Fatalf("Topic is nil after SetupLibP2P")
    }

    if net.Sub == nil {
        t.Fatalf("Subscription is nil after SetupLibP2P")
    }

    // Test message handler invocation directly
    received := make(chan []byte, 1)
    net.SetMessageHandler(func(msg []byte) {
        received <- msg
    })

    testMsg := []byte("test message")
    // Directly invoke message handler to test
    net.msgHandler(testMsg)

    select {
    case msg := <-received:
        t.Logf("Received message: %s", string(msg))
        if string(msg) != string(testMsg) {
            t.Fatalf("Received message mismatch: got %s, want %s", msg, testMsg)
        }
    case <-time.After(1 * time.Second):
        t.Fatalf("Timeout waiting for message")
    }
}
