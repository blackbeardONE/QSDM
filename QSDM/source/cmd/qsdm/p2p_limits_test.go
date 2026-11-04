package main

import (
	"reflect"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/config"
	"github.com/blackbeardONE/QSDM/pkg/networking"
)

// An unconfigured node gets exactly the networking defaults.
func TestP2PLimitsFromConfig_ZeroIsDefaults(t *testing.T) {
	got := p2pLimitsFromConfig(config.P2PLimitsConfig{}).WithDefaults()
	if !reflect.DeepEqual(got, networking.DefaultP2PLimits()) {
		t.Fatalf("zero config should map to defaults:\n got %+v\nwant %+v", got, networking.DefaultP2PLimits())
	}
}

func TestP2PLimitsFromConfig_CopiesEveryField(t *testing.T) {
	c := config.P2PLimitsConfig{
		Disabled: true, ConnLowWater: 1, ConnHighWater: 2, ConnGracePeriod: 3 * time.Second,
		MaxConns: 4, MaxInboundConns: 5, MaxStreams: 6, MaxInboundStreams: 7,
		MaxConnsPerPeer: 8, MaxStreamsPerPeer: 9, MaxInboundStreamsPerPeer: 10, MaxConnsPerIP: 11,
		GossipMaxMessageBytes: 12, GossipTopicMaxBytes: map[string]int{"t": 13},
		GossipPeerRate: 14, GossipPeerBurst: 15, GossipPeerScoringDisabled: true,
		GossipValidateQueue: 16, GossipValidateThrottle: 17,
	}
	want := networking.P2PLimits{
		Disabled: true, ConnLowWater: 1, ConnHighWater: 2, ConnGracePeriod: 3 * time.Second,
		MaxConns: 4, MaxInboundConns: 5, MaxStreams: 6, MaxInboundStreams: 7,
		MaxConnsPerPeer: 8, MaxStreamsPerPeer: 9, MaxInboundStreamsPerPeer: 10, MaxConnsPerIP: 11,
		GossipMaxMessageBytes: 12, GossipTopicMaxBytes: map[string]int{"t": 13},
		GossipPeerRate: 14, GossipPeerBurst: 15, GossipPeerScoringDisabled: true,
		GossipValidateQueue: 16, GossipValidateThrottle: 17,
	}
	got := p2pLimitsFromConfig(c)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mapping:\n got %+v\nwant %+v", got, want)
	}
	// The mapped topic table is a copy, not an alias of the config map.
	got.GossipTopicMaxBytes["t"] = 99
	if c.GossipTopicMaxBytes["t"] != 13 {
		t.Fatal("topic map aliased")
	}
	// Guard against a field added to one struct but not mapped.
	if n, m := reflect.TypeOf(c).NumField(), reflect.TypeOf(want).NumField(); n != m {
		t.Fatalf("config has %d limit fields, networking has %d", n, m)
	}
}
