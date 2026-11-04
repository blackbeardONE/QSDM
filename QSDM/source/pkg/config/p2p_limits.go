package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/envcompat"
)

// P2PLimitsTOML is the file form of the libp2p connection / resource /
// gossip limits (QSDM/docs/docs/P2P_LIMITS.md). It is embedded in
// [network], so the keys sit directly in that table:
//
//	[network]
//	conn_low_water = 64
//	conn_high_water = 96
//	conn_grace_period = "1m"
//	max_conns = 192
//	max_inbound_conns = 128
//	max_streams = 2048
//	max_inbound_streams = 1024
//	max_conns_per_peer = 8
//	max_streams_per_peer = 256
//	max_inbound_streams_per_peer = 128
//	max_conns_per_ip = 8
//	gossip_max_message_bytes = 1048576
//	gossip_peer_rate = 100.0          # msgs/s per peer per topic; < 0 disables
//	gossip_peer_burst = 1000
//	gossip_peer_scoring_disabled = false
//	gossip_validate_queue = 256
//	gossip_validate_throttle = 2048
//	p2p_limits_disabled = false       # escape hatch: libp2p library defaults
//
//	[network.gossip_topic_max_bytes]
//	"qsdm-transactions" = 262144
//
// Every key is optional; 0 / unset means "use the built-in default" (see
// pkg/networking/p2p_limits.go DefaultP2PLimits).
type P2PLimitsTOML struct {
	P2PLimitsDisabled         bool           `toml:"p2p_limits_disabled" yaml:"p2p_limits_disabled"`
	ConnLowWater              int            `toml:"conn_low_water" yaml:"conn_low_water"`
	ConnHighWater             int            `toml:"conn_high_water" yaml:"conn_high_water"`
	ConnGracePeriod           string         `toml:"conn_grace_period" yaml:"conn_grace_period"`
	MaxConns                  int            `toml:"max_conns" yaml:"max_conns"`
	MaxInboundConns           int            `toml:"max_inbound_conns" yaml:"max_inbound_conns"`
	MaxStreams                int            `toml:"max_streams" yaml:"max_streams"`
	MaxInboundStreams         int            `toml:"max_inbound_streams" yaml:"max_inbound_streams"`
	MaxConnsPerPeer           int            `toml:"max_conns_per_peer" yaml:"max_conns_per_peer"`
	MaxStreamsPerPeer         int            `toml:"max_streams_per_peer" yaml:"max_streams_per_peer"`
	MaxInboundStreamsPerPeer  int            `toml:"max_inbound_streams_per_peer" yaml:"max_inbound_streams_per_peer"`
	MaxConnsPerIP             int            `toml:"max_conns_per_ip" yaml:"max_conns_per_ip"`
	GossipMaxMessageBytes     int            `toml:"gossip_max_message_bytes" yaml:"gossip_max_message_bytes"`
	GossipTopicMaxBytes       map[string]int `toml:"gossip_topic_max_bytes" yaml:"gossip_topic_max_bytes"`
	GossipPeerRate            float64        `toml:"gossip_peer_rate" yaml:"gossip_peer_rate"`
	GossipPeerBurst           int            `toml:"gossip_peer_burst" yaml:"gossip_peer_burst"`
	GossipPeerScoringDisabled bool           `toml:"gossip_peer_scoring_disabled" yaml:"gossip_peer_scoring_disabled"`
	GossipValidateQueue       int            `toml:"gossip_validate_queue" yaml:"gossip_validate_queue"`
	GossipValidateThrottle    int            `toml:"gossip_validate_throttle" yaml:"gossip_validate_throttle"`
}

// P2PLimitsConfig is the parsed form held on Config. Zero fields mean "use
// the networking default"; cmd/qsdm maps this onto networking.P2PLimits.
type P2PLimitsConfig struct {
	Disabled                  bool
	ConnLowWater              int
	ConnHighWater             int
	ConnGracePeriod           time.Duration
	MaxConns                  int
	MaxInboundConns           int
	MaxStreams                int
	MaxInboundStreams         int
	MaxConnsPerPeer           int
	MaxStreamsPerPeer         int
	MaxInboundStreamsPerPeer  int
	MaxConnsPerIP             int
	GossipMaxMessageBytes     int
	GossipTopicMaxBytes       map[string]int
	GossipPeerRate            float64
	GossipPeerBurst           int
	GossipPeerScoringDisabled bool
	GossipValidateQueue       int
	GossipValidateThrottle    int
}

func (t P2PLimitsTOML) toConfig() (P2PLimitsConfig, error) {
	c := P2PLimitsConfig{
		Disabled:                  t.P2PLimitsDisabled,
		ConnLowWater:              t.ConnLowWater,
		ConnHighWater:             t.ConnHighWater,
		MaxConns:                  t.MaxConns,
		MaxInboundConns:           t.MaxInboundConns,
		MaxStreams:                t.MaxStreams,
		MaxInboundStreams:         t.MaxInboundStreams,
		MaxConnsPerPeer:           t.MaxConnsPerPeer,
		MaxStreamsPerPeer:         t.MaxStreamsPerPeer,
		MaxInboundStreamsPerPeer:  t.MaxInboundStreamsPerPeer,
		MaxConnsPerIP:             t.MaxConnsPerIP,
		GossipMaxMessageBytes:     t.GossipMaxMessageBytes,
		GossipPeerRate:            t.GossipPeerRate,
		GossipPeerBurst:           t.GossipPeerBurst,
		GossipPeerScoringDisabled: t.GossipPeerScoringDisabled,
		GossipValidateQueue:       t.GossipValidateQueue,
		GossipValidateThrottle:    t.GossipValidateThrottle,
	}
	if s := strings.TrimSpace(t.ConnGracePeriod); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return c, fmt.Errorf("invalid [network] conn_grace_period %q: %w", s, err)
		}
		c.ConnGracePeriod = d
	}
	if len(t.GossipTopicMaxBytes) > 0 {
		c.GossipTopicMaxBytes = make(map[string]int, len(t.GossipTopicMaxBytes))
		for k, v := range t.GossipTopicMaxBytes {
			c.GossipTopicMaxBytes[strings.TrimSpace(k)] = v
		}
	}
	return c, nil
}

// applyP2PLimitsEnv applies QSDM_P2P_* environment overrides. Malformed
// values are ignored, matching the other numeric env overrides in this
// package; Validate catches out-of-range values.
func applyP2PLimitsEnv(c *P2PLimitsConfig) {
	envInt := func(key string, dst *int) {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				*dst = n
			}
		}
	}
	if strings.TrimSpace(os.Getenv("QSDM_P2P_LIMITS_DISABLED")) != "" {
		c.Disabled = envcompat.Truthy("QSDM_P2P_LIMITS_DISABLED", "QSDM_P2P_LIMITS_DISABLED")
	}
	envInt("QSDM_P2P_CONN_LOW_WATER", &c.ConnLowWater)
	envInt("QSDM_P2P_CONN_HIGH_WATER", &c.ConnHighWater)
	if v := strings.TrimSpace(os.Getenv("QSDM_P2P_CONN_GRACE_PERIOD")); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.ConnGracePeriod = d
		}
	}
	envInt("QSDM_P2P_MAX_CONNS", &c.MaxConns)
	envInt("QSDM_P2P_MAX_INBOUND_CONNS", &c.MaxInboundConns)
	envInt("QSDM_P2P_MAX_STREAMS", &c.MaxStreams)
	envInt("QSDM_P2P_MAX_INBOUND_STREAMS", &c.MaxInboundStreams)
	envInt("QSDM_P2P_MAX_CONNS_PER_PEER", &c.MaxConnsPerPeer)
	envInt("QSDM_P2P_MAX_STREAMS_PER_PEER", &c.MaxStreamsPerPeer)
	envInt("QSDM_P2P_MAX_INBOUND_STREAMS_PER_PEER", &c.MaxInboundStreamsPerPeer)
	envInt("QSDM_P2P_MAX_CONNS_PER_IP", &c.MaxConnsPerIP)
	envInt("QSDM_P2P_GOSSIP_MAX_MESSAGE_BYTES", &c.GossipMaxMessageBytes)
	if v := strings.TrimSpace(os.Getenv("QSDM_P2P_GOSSIP_TOPIC_MAX_BYTES")); v != "" {
		if m, err := parseTopicSizes(v); err == nil {
			if c.GossipTopicMaxBytes == nil {
				c.GossipTopicMaxBytes = map[string]int{}
			}
			for k, n := range m {
				c.GossipTopicMaxBytes[k] = n
			}
		}
	}
	if v := strings.TrimSpace(os.Getenv("QSDM_P2P_GOSSIP_PEER_RATE")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.GossipPeerRate = f
		}
	}
	envInt("QSDM_P2P_GOSSIP_PEER_BURST", &c.GossipPeerBurst)
	if strings.TrimSpace(os.Getenv("QSDM_P2P_GOSSIP_PEER_SCORING_DISABLED")) != "" {
		c.GossipPeerScoringDisabled = envcompat.Truthy("QSDM_P2P_GOSSIP_PEER_SCORING_DISABLED", "QSDM_P2P_GOSSIP_PEER_SCORING_DISABLED")
	}
	envInt("QSDM_P2P_GOSSIP_VALIDATE_QUEUE", &c.GossipValidateQueue)
	envInt("QSDM_P2P_GOSSIP_VALIDATE_THROTTLE", &c.GossipValidateThrottle)
}

// parseTopicSizes parses "topic=bytes,topic=bytes".
func parseTopicSizes(s string) (map[string]int, error) {
	out := map[string]int{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, fmt.Errorf("topic size %q: want topic=bytes", part)
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("topic size %q: bytes must be a positive integer", part)
		}
		out[k] = n
	}
	return out, nil
}

// Validate rejects negative counts and an inverted connection-manager water
// mark when both are set. Cross-field checks that depend on the defaults
// (e.g. max_conns >= conn_high_water) are repeated by networking.P2PLimits.
func (c P2PLimitsConfig) Validate() error {
	if c.Disabled {
		return nil
	}
	ints := []struct {
		key string
		v   int
	}{
		{"conn_low_water", c.ConnLowWater},
		{"conn_high_water", c.ConnHighWater},
		{"max_conns", c.MaxConns},
		{"max_inbound_conns", c.MaxInboundConns},
		{"max_streams", c.MaxStreams},
		{"max_inbound_streams", c.MaxInboundStreams},
		{"max_conns_per_peer", c.MaxConnsPerPeer},
		{"max_streams_per_peer", c.MaxStreamsPerPeer},
		{"max_inbound_streams_per_peer", c.MaxInboundStreamsPerPeer},
		{"max_conns_per_ip", c.MaxConnsPerIP},
		{"gossip_max_message_bytes", c.GossipMaxMessageBytes},
		{"gossip_peer_burst", c.GossipPeerBurst},
		{"gossip_validate_queue", c.GossipValidateQueue},
		{"gossip_validate_throttle", c.GossipValidateThrottle},
	}
	for _, it := range ints {
		if it.v < 0 {
			return fmt.Errorf("[network] %s must be >= 0 (0 = default), got %d", it.key, it.v)
		}
	}
	if c.ConnGracePeriod < 0 {
		return fmt.Errorf("[network] conn_grace_period must be >= 0, got %s", c.ConnGracePeriod)
	}
	if c.ConnLowWater > 0 && c.ConnHighWater > 0 && c.ConnLowWater >= c.ConnHighWater {
		return fmt.Errorf("[network] conn_low_water (%d) must be < conn_high_water (%d)", c.ConnLowWater, c.ConnHighWater)
	}
	for topic, n := range c.GossipTopicMaxBytes {
		if topic == "" || n <= 0 {
			return fmt.Errorf("[network] gossip_topic_max_bytes entry %q=%d: topic must be named and bytes > 0", topic, n)
		}
	}
	return nil
}
