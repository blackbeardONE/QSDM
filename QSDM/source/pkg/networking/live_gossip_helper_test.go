package networking

import (
	"context"
	"sync"
	"testing"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

// republishEvery is how often keepPublishing sends its message again.
const republishEvery = 500 * time.Millisecond

// keepPublishing publishes data on topic, then again every republishEvery
// until the returned stop function is called or ctx ends.
//
// GossipSub can drop a message published just after two hosts connect,
// before their mesh has formed. The live tests in this package used to
// publish once and wait, so on a busy machine they sometimes timed out
// although nothing was wrong. Every message they publish is harmless to
// repeat, and each test stops at the first copy that arrives.
//
// The first publish runs in the test goroutine, so its error still fails
// the test. Later errors are ignored: the test's own deadline reports a
// message that never arrives. stop waits for the publishing goroutine to
// exit; it is safe to call more than once and also runs at cleanup.
func keepPublishing(ctx context.Context, t *testing.T, topic *pubsub.Topic, data []byte) (stop func()) {
	t.Helper()
	if err := topic.Publish(ctx, data); err != nil {
		t.Fatalf("publish: %v", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(republishEvery)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				_ = topic.Publish(ctx, data)
			}
		}
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
	t.Cleanup(stop)
	return stop
}
