package networking_test

import (
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/bridge"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/networking"
)

// Pins the per-topic size table to the topic constants owned by other
// packages, so a renamed topic cannot silently fall back to the global limit.
func TestDefaultGossipTopicMaxBytes_CoversAllTopics(t *testing.T) {
	m := networking.DefaultGossipTopicMaxBytes()
	for _, topic := range []string{
		networking.TxTopicName,
		networking.BFTTopicName,
		networking.EvidenceTopicName,
		networking.PolTopicName,
		chain.BlockTopicName,
		bridge.BridgeTopicName,
	} {
		if m[topic] <= 0 {
			t.Errorf("no size ceiling for gossip topic %q", topic)
		}
	}
}
