package config

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

// liveShapeEnv lists every environment switch of the P2P hardening, plus the
// bridge relay ones. A node configured like the block producer sets none of
// them; the test clears them so a developer shell cannot leak in.
var liveShapeEnv = []string{
	"QSDM_P2P_LIMITS_ENABLED", "QSDM_P2P_LIMITS_DISABLED", "QSDM_P2P_CONN_LOW_WATER", "QSDM_P2P_CONN_HIGH_WATER",
	"QSDM_P2P_CONN_GRACE_PERIOD", "QSDM_P2P_MAX_CONNS", "QSDM_P2P_MAX_INBOUND_CONNS", "QSDM_P2P_MAX_STREAMS",
	"QSDM_P2P_MAX_INBOUND_STREAMS", "QSDM_P2P_MAX_CONNS_PER_PEER", "QSDM_P2P_MAX_STREAMS_PER_PEER",
	"QSDM_P2P_MAX_INBOUND_STREAMS_PER_PEER", "QSDM_P2P_MAX_CONNS_PER_IP", "QSDM_P2P_GOSSIP_MAX_MESSAGE_BYTES",
	"QSDM_P2P_GOSSIP_TOPIC_MAX_BYTES", "QSDM_P2P_GOSSIP_PEER_RATE", "QSDM_P2P_GOSSIP_PEER_BURST",
	"QSDM_P2P_GOSSIP_PEER_SCORING_DISABLED", "QSDM_P2P_GOSSIP_VALIDATE_QUEUE", "QSDM_P2P_GOSSIP_VALIDATE_THROTTLE",
	"QSDM_BRIDGE_RELAY_TRUSTED_KEYS", "QSDM_BRIDGE_RELAY_TRUSTED_KEYS_FILE", "QSDM_BRIDGE_RELAY_SIGNER_KEY_PATH",
	"QSDM_BRIDGE_RELAY_MAX_EVENT_AGE", "QSDM_P2P_PRIVATE", "QSDM_P2P_PRIVATE_ALLOWED_PEERS",
}

// loadLiveShape loads testdata/producer_live_shape.toml through the
// same steps as LoadConfig (file, defaults, env, Validate) with every new
// switch unset.
func loadLiveShape(t *testing.T) *Config {
	t.Helper()
	for _, k := range liveShapeEnv {
		t.Setenv(k, "")
	}
	cfg := &Config{}
	if err := loadConfigFile(filepath.Join("testdata", "producer_live_shape.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	applyDefaults(cfg)
	applyEnvOverrides(cfg)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the live-shaped config must still validate: %v", err)
	}
	return cfg
}

// TestLiveShapedConfig_NewFeaturesOff: a config shaped like the block
// producer's, written before the P2P hardening, leaves every new feature at
// its zero value (off), and the version 1 transition keeps its meaning.
func TestLiveShapedConfig_NewFeaturesOff(t *testing.T) {
	cfg := loadLiveShape(t)

	if !reflect.DeepEqual(cfg.P2PLimits, P2PLimitsConfig{}) || cfg.P2PLimits.Effective() {
		t.Errorf("P2P limits must be off (the host from before the limits): %+v", cfg.P2PLimits)
	}
	if !reflect.DeepEqual(cfg.BridgeRelay, BridgeRelayConfig{}) {
		t.Errorf("bridge relay auth must have no keys and no signer: %+v", cfg.BridgeRelay)
	}
	if !reflect.DeepEqual(cfg.PrivateP2P, PrivateP2PConfig{}) {
		t.Errorf("private P2P must be off: %+v", cfg.PrivateP2P)
	}

	// Settings the config already had are unchanged.
	if cfg.NetworkBindAddress != "127.0.0.1" || cfg.NetworkPort != 14001 || len(cfg.BootstrapPeers) != 0 {
		t.Errorf("network: %s:%d %v", cfg.NetworkBindAddress, cfg.NetworkPort, cfg.BootstrapPeers)
	}
	if !cfg.RequireSignedVotes || cfg.SignedConsensusActivationHeight != 827000 || cfg.PoEActivationHeight != 816600 ||
		cfg.TaskActionSignatureActivationHeight != 625000 || cfg.TxContentRootActivationHeight != 625000 {
		t.Errorf("activation heights changed: %+v", cfg)
	}

	// The version 1 table is still version 1, names the same hand-over and
	// means the same two epochs.
	p := cfg.ProducerTransition
	if p == nil || p.Version != 1 || len(p.Epochs) != 0 {
		t.Fatalf("transition: %+v", p)
	}
	if p.CheckpointHeight != 659976 || p.EffectiveHeight != 659977 || p.HistoricalSignatureHeight != 482566 ||
		p.HistoricalProducer != "42d05e653f004d0aa71ae9cebc7b2a5b5d0dc7bb41b219a8e99d7c29229c438b" ||
		p.ReplacementProducer != "1cf60b16cf9aee5e8eaf67a8518a2b2ba7bc54f2dd607f05bc5a861b7a94c859" ||
		p.CheckpointHash != "16884d35cee8b805fab51f1ecad740022fdb991e0fece15394774e1c5b7cdd0b" {
		t.Errorf("transition table: %+v", p)
	}
	want := []producerpolicy.Epoch{
		{FromHeight: 482566, ProducerID: "42d05e653f004d0aa71ae9cebc7b2a5b5d0dc7bb41b219a8e99d7c29229c438b"},
		{FromHeight: 659977, ProducerID: "1cf60b16cf9aee5e8eaf67a8518a2b2ba7bc54f2dd607f05bc5a861b7a94c859",
			CheckpointHash: "16884d35cee8b805fab51f1ecad740022fdb991e0fece15394774e1c5b7cdd0b"},
	}
	if got := p.Schedule(); !reflect.DeepEqual(got, want) {
		t.Errorf("schedule:\n got %+v\nwant %+v", got, want)
	}
	if p.FirstHandoverHeight() != p.EffectiveHeight || p.FinalEpoch().ProducerID != p.ReplacementProducer {
		t.Errorf("v1 fields and the epoch view disagree: first=%d final=%+v", p.FirstHandoverHeight(), p.FinalEpoch())
	}
}
