package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func p2pBaseValidConfig() *Config {
	return &Config{NetworkPort: 4001, DashboardPort: 8081, LogViewerPort: 9000, APIPort: 8080, StorageType: "file"}
}

func TestP2PLimits_TOMLUnsetIsZero(t *testing.T) {
	p := filepath.Join(t.TempDir(), "node.toml")
	if err := os.WriteFile(p, []byte("[network]\nport = 4001\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{}
	if err := loadConfigFile(p, cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.P2PLimits.Enabled || cfg.P2PLimits.Effective() || cfg.P2PLimits.Disabled || cfg.P2PLimits.MaxConns != 0 || cfg.P2PLimits.GossipTopicMaxBytes != nil {
		t.Fatalf("unset keys must stay zero (networking defaults apply): %+v", cfg.P2PLimits)
	}
	if err := cfg.P2PLimits.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestP2PLimits_TOMLParsesAllKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "node.toml")
	content := `
[network]
port = 4001
p2p_limits_enabled = true
conn_low_water = 10
conn_high_water = 20
conn_grace_period = "30s"
max_conns = 40
max_inbound_conns = 30
max_streams = 500
max_inbound_streams = 250
max_conns_per_peer = 4
max_streams_per_peer = 64
max_inbound_streams_per_peer = 32
max_conns_per_ip = 3
gossip_max_message_bytes = 524288
gossip_peer_rate = 12.5
gossip_peer_burst = 50
gossip_peer_scoring_disabled = true
gossip_validate_queue = 128
gossip_validate_throttle = 512

[network.gossip_topic_max_bytes]
"qsdm-bridge" = 2048
"qsdm-transactions" = 131072
`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{}
	if err := loadConfigFile(p, cfg); err != nil {
		t.Fatal(err)
	}
	l := cfg.P2PLimits
	want := P2PLimitsConfig{
		Enabled:      true,
		ConnLowWater: 10, ConnHighWater: 20, ConnGracePeriod: 30 * time.Second,
		MaxConns: 40, MaxInboundConns: 30, MaxStreams: 500, MaxInboundStreams: 250,
		MaxConnsPerPeer: 4, MaxStreamsPerPeer: 64, MaxInboundStreamsPerPeer: 32, MaxConnsPerIP: 3,
		GossipMaxMessageBytes: 524288, GossipPeerRate: 12.5, GossipPeerBurst: 50,
		GossipPeerScoringDisabled: true, GossipValidateQueue: 128, GossipValidateThrottle: 512,
	}
	got := l
	got.GossipTopicMaxBytes = nil
	if !p2pLimitsEqualNoMap(got, want) {
		t.Fatalf("parsed limits:\n got %+v\nwant %+v", got, want)
	}
	if l.GossipTopicMaxBytes["qsdm-bridge"] != 2048 || l.GossipTopicMaxBytes["qsdm-transactions"] != 131072 {
		t.Fatalf("topic table: %v", l.GossipTopicMaxBytes)
	}
	if err := l.Validate(); err != nil {
		t.Fatal(err)
	}
}

func p2pLimitsEqualNoMap(a, b P2PLimitsConfig) bool {
	a.GossipTopicMaxBytes, b.GossipTopicMaxBytes = nil, nil
	return a.Enabled == b.Enabled && a.Disabled == b.Disabled && a.ConnLowWater == b.ConnLowWater && a.ConnHighWater == b.ConnHighWater &&
		a.ConnGracePeriod == b.ConnGracePeriod && a.MaxConns == b.MaxConns && a.MaxInboundConns == b.MaxInboundConns &&
		a.MaxStreams == b.MaxStreams && a.MaxInboundStreams == b.MaxInboundStreams && a.MaxConnsPerPeer == b.MaxConnsPerPeer &&
		a.MaxStreamsPerPeer == b.MaxStreamsPerPeer && a.MaxInboundStreamsPerPeer == b.MaxInboundStreamsPerPeer &&
		a.MaxConnsPerIP == b.MaxConnsPerIP && a.GossipMaxMessageBytes == b.GossipMaxMessageBytes &&
		a.GossipPeerRate == b.GossipPeerRate && a.GossipPeerBurst == b.GossipPeerBurst &&
		a.GossipPeerScoringDisabled == b.GossipPeerScoringDisabled && a.GossipValidateQueue == b.GossipValidateQueue &&
		a.GossipValidateThrottle == b.GossipValidateThrottle
}

func TestP2PLimits_YAMLParses(t *testing.T) {
	p := filepath.Join(t.TempDir(), "node.yaml")
	content := `
network:
  port: 4001
  max_conns: 77
  conn_grace_period: 2m
  p2p_limits_disabled: true
  gossip_topic_max_bytes:
    qsdm-bridge: 1000
`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{}
	if err := loadConfigFile(p, cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.P2PLimits.MaxConns != 77 || cfg.P2PLimits.ConnGracePeriod != 2*time.Minute || !cfg.P2PLimits.Disabled ||
		cfg.P2PLimits.GossipTopicMaxBytes["qsdm-bridge"] != 1000 {
		t.Fatalf("yaml limits: %+v", cfg.P2PLimits)
	}
}

func TestP2PLimits_TOMLBadDuration(t *testing.T) {
	p := filepath.Join(t.TempDir(), "node.toml")
	if err := os.WriteFile(p, []byte("[network]\nconn_grace_period = \"soon\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := loadConfigFile(p, &Config{}); err == nil || !strings.Contains(err.Error(), "conn_grace_period") {
		t.Fatalf("expected conn_grace_period error, got %v", err)
	}
}

func TestP2PLimits_EnvOverrides(t *testing.T) {
	t.Setenv("QSDM_P2P_CONN_LOW_WATER", "5")
	t.Setenv("QSDM_P2P_CONN_HIGH_WATER", "9")
	t.Setenv("QSDM_P2P_CONN_GRACE_PERIOD", "45s")
	t.Setenv("QSDM_P2P_MAX_CONNS", "100")
	t.Setenv("QSDM_P2P_MAX_INBOUND_CONNS", "60")
	t.Setenv("QSDM_P2P_MAX_STREAMS", "900")
	t.Setenv("QSDM_P2P_MAX_INBOUND_STREAMS", "400")
	t.Setenv("QSDM_P2P_MAX_CONNS_PER_PEER", "2")
	t.Setenv("QSDM_P2P_MAX_STREAMS_PER_PEER", "100")
	t.Setenv("QSDM_P2P_MAX_INBOUND_STREAMS_PER_PEER", "50")
	t.Setenv("QSDM_P2P_MAX_CONNS_PER_IP", "4")
	t.Setenv("QSDM_P2P_GOSSIP_MAX_MESSAGE_BYTES", "700000")
	t.Setenv("QSDM_P2P_GOSSIP_TOPIC_MAX_BYTES", "qsdm-bridge=999, qsdm-evidence=8888")
	t.Setenv("QSDM_P2P_GOSSIP_PEER_RATE", "-1")
	t.Setenv("QSDM_P2P_GOSSIP_PEER_BURST", "7")
	t.Setenv("QSDM_P2P_GOSSIP_PEER_SCORING_DISABLED", "true")
	t.Setenv("QSDM_P2P_GOSSIP_VALIDATE_QUEUE", "64")
	t.Setenv("QSDM_P2P_GOSSIP_VALIDATE_THROTTLE", "256")
	t.Setenv("QSDM_P2P_LIMITS_DISABLED", "false")
	t.Setenv("QSDM_P2P_LIMITS_ENABLED", "1")

	c := P2PLimitsConfig{MaxConns: 1, GossipTopicMaxBytes: map[string]int{"qsdm-bridge": 1, "qsdm-blocks": 5}}
	applyP2PLimitsEnv(&c)
	if c.ConnLowWater != 5 || c.ConnHighWater != 9 || c.ConnGracePeriod != 45*time.Second || c.MaxConns != 100 ||
		c.MaxInboundConns != 60 || c.MaxStreams != 900 || c.MaxInboundStreams != 400 || c.MaxConnsPerPeer != 2 ||
		c.MaxStreamsPerPeer != 100 || c.MaxInboundStreamsPerPeer != 50 || c.MaxConnsPerIP != 4 ||
		c.GossipMaxMessageBytes != 700000 || c.GossipPeerRate != -1 || c.GossipPeerBurst != 7 ||
		!c.GossipPeerScoringDisabled || c.GossipValidateQueue != 64 || c.GossipValidateThrottle != 256 || c.Disabled || !c.Enabled {
		t.Fatalf("env overrides: %+v", c)
	}
	if c.GossipTopicMaxBytes["qsdm-bridge"] != 999 || c.GossipTopicMaxBytes["qsdm-evidence"] != 8888 || c.GossipTopicMaxBytes["qsdm-blocks"] != 5 {
		t.Fatalf("env topic merge: %v", c.GossipTopicMaxBytes)
	}
}

func TestP2PLimits_EnvMalformedIgnored(t *testing.T) {
	t.Setenv("QSDM_P2P_MAX_CONNS", "lots")
	t.Setenv("QSDM_P2P_GOSSIP_TOPIC_MAX_BYTES", "qsdm-bridge")
	c := P2PLimitsConfig{MaxConns: 42}
	applyP2PLimitsEnv(&c)
	if c.MaxConns != 42 || c.GossipTopicMaxBytes != nil {
		t.Fatalf("malformed env must be ignored: %+v", c)
	}
}

func TestP2PLimits_ConfigValidate(t *testing.T) {
	bad := []P2PLimitsConfig{
		{Enabled: true, ConnLowWater: 20, ConnHighWater: 10},
		{Enabled: true, MaxConns: -1},
		{Enabled: true, ConnGracePeriod: -time.Second},
		{Enabled: true, GossipTopicMaxBytes: map[string]int{"qsdm-bridge": 0}},
		// Opt-in: a tuning key without p2p_limits_enabled is refused.
		{MaxConns: 64},
		{GossipTopicMaxBytes: map[string]int{"qsdm-transactions": 262144}},
		{GossipPeerScoringDisabled: true},
	}
	for i, l := range bad {
		c := p2pBaseValidConfig()
		c.P2PLimits = l
		if err := c.Validate(); err == nil {
			t.Errorf("case %d: expected Validate error for %+v", i, l)
		}
	}
	c := p2pBaseValidConfig()
	c.P2PLimits = P2PLimitsConfig{Disabled: true, ConnLowWater: 20, ConnHighWater: 10}
	if err := c.Validate(); err != nil {
		t.Fatalf("disabled limits are not validated: %v", err)
	}
	if err := p2pBaseValidConfig().Validate(); err != nil {
		t.Fatalf("zero limits must validate: %v", err)
	}
	c = p2pBaseValidConfig()
	c.P2PLimits = P2PLimitsConfig{Enabled: true, MaxConns: 64}
	if err := c.Validate(); err != nil || !c.P2PLimits.Effective() {
		t.Fatalf("enabled limits with a tuning key must validate: %v", err)
	}
}

func TestP2PLimits_EnvEnabled(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want bool
	}{{"1", true}, {"true", true}, {"0", false}, {"false", false}} {
		t.Setenv("QSDM_P2P_LIMITS_ENABLED", tc.env)
		c := P2PLimitsConfig{Enabled: !tc.want}
		applyP2PLimitsEnv(&c)
		if c.Enabled != tc.want || c.Effective() != tc.want {
			t.Errorf("QSDM_P2P_LIMITS_ENABLED=%s: %+v", tc.env, c)
		}
	}
}
