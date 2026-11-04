package main

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/bridge"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/config"
	"github.com/blackbeardONE/QSDM/pkg/networking"
)

// liveShapeConfig loads the fixture shaped like the block producer's
// core.toml through config.LoadConfig, the loader main() uses, with every
// switch of the P2P hardening and the snapshot bootstrap unset.
func liveShapeConfig(t *testing.T) *config.Config {
	t.Helper()
	for _, k := range []string{
		"QSDM_P2P_LIMITS_ENABLED", "QSDM_P2P_LIMITS_DISABLED", "QSDM_P2P_MAX_CONNS", "QSDM_P2P_GOSSIP_TOPIC_MAX_BYTES",
		"QSDM_BRIDGE_RELAY_TRUSTED_KEYS", "QSDM_BRIDGE_RELAY_TRUSTED_KEYS_FILE", "QSDM_BRIDGE_RELAY_SIGNER_KEY_PATH",
		"QSDM_P2P_PRIVATE", "QSDM_P2P_PRIVATE_ALLOWED_PEERS", "QSDM_SNAPSHOT_BOOTSTRAP",
		"QSDM_SNAPSHOT_CHECKPOINT_HEIGHT", "QSDM_ALLOW_PUBLIC_DHT_FALLBACK",
	} {
		t.Setenv(k, "")
	}
	t.Setenv("CONFIG_FILE", filepath.Join("..", "..", "pkg", "config", "testdata", "producer_live_shape.toml"))
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("the live-shaped config must load: %v", err)
	}
	return cfg
}

// TestLiveShapedConfig_ReleaseBehaviour is the cmd/qsdm half of the "a node
// configured as before behaves as before" argument. It runs the startup
// decisions main() makes from the config for the P2P hardening, the snapshot
// bootstrap and the producer transition, and checks that each one takes the
// path the node took before.
func TestLiveShapedConfig_ReleaseBehaviour(t *testing.T) {
	cfg := liveShapeConfig(t)

	// P2P limits: none; SetupNetwork receives the construction from before
	// the limits.
	limits := p2pLimitsFromConfig(cfg.P2PLimits)
	if !reflect.DeepEqual(limits, networking.P2PLimits{Disabled: true}) {
		t.Fatalf("P2P limits must be the construction from before the limits: %+v", limits)
	}

	// Private P2P mode: off, so the historical host mode (loopback bind, no
	// NAT, no peer allowlist).
	mode, err := resolveP2PStartupMode(cfg.NetworkBindAddress, cfg.PrivateP2P, false)
	if err != nil || mode != p2pModeDefault {
		t.Fatalf("P2P startup mode: %q %v", mode, err)
	}

	// The host main() builds from these values: no gossip guard, no peer
	// allowlist and the libp2p library defaults.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	net, err := SetupNetwork(ctx, logging.NewSilentLogger(), 0, cfg.NetworkBindAddress, "", limits)
	if err != nil {
		t.Fatal(err)
	}
	defer net.Close()
	if !net.Limits().Disabled || net.GossipGuardStats() != (networking.GossipGuardStats{}) || net.PrivateMode() {
		t.Fatalf("host limits: %+v guard %+v private %v", net.Limits(), net.GossipGuardStats(), net.PrivateMode())
	}

	// Bridge relay: no trusted keys and no signer, so local events go out
	// unsigned as before and remote events are not applied.
	auth, err := bridgeRelayAuthFromConfig(cfg.BridgeRelay, nil)
	if err != nil || len(auth.TrustedKeys) != 0 || auth.Signer != nil {
		t.Fatalf("bridge relay auth: %+v %v", auth, err)
	}
	relay, err := bridge.NewP2PRelayWithAuth(net, nil, nil, net.Host.ID().String(), auth)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	if relay.TrustedKeyCount() != 0 || relay.Signing() {
		t.Fatalf("relay: keys %d signing %v", relay.TrustedKeyCount(), relay.Signing())
	}

	// Snapshot bootstrap: the node's normal command line selects no snapshot
	// mode, and the boot guard is off, so it never even reads a block.
	if snapshotModeRequested(nil) {
		t.Fatal("no arguments must not select a snapshot mode")
	}
	if err := snapshotCheckpointGuard(cfg.SnapshotBootstrap, func(uint64) (*chain.Block, bool) {
		t.Fatal("an off boot guard must not read the chain")
		return nil, false
	}); err != nil {
		t.Fatal(err)
	}

	// Producer transition: the version 1 table keeps the state-root boundary
	// and the local-producer check it had before version 2 (main.go now reads
	// FirstHandoverHeight() and FinalEpoch().ProducerID, which are
	// effective_height and replacement_producer for version 1).
	p := cfg.ProducerTransition
	if p == nil || p.Version != 1 {
		t.Fatalf("transition: %+v", p)
	}
	if p.FinalEpoch().ProducerID != p.ReplacementProducer || p.FirstHandoverHeight() != p.EffectiveHeight {
		t.Fatalf("v1 epoch view: final %+v first %d", p.FinalEpoch(), p.FirstHandoverHeight())
	}
	for _, h := range []uint64{p.EffectiveHeight - 1, p.EffectiveHeight, 900000} {
		tip := &chain.Block{Height: h}
		want := uint64(0)
		if h >= p.EffectiveHeight {
			want = h
		}
		if got := transitionStateRootHeight(p, tip); got != want {
			t.Fatalf("transitionStateRootHeight(%d) = %d, want %d (the rule before version 2: tip >= effective_height)", h, got, want)
		}
	}

	// Stale allowlist warning: the only new boot output under this config is
	// one WARN line, because the fixture, like the producer's file, pins the
	// historical producer while the transition is configured. Block
	// acceptance does not read the allowlist while a transition is
	// configured, so nothing else changes. The advice names the current
	// producer, which for version 1 is replacement_producer.
	adv := producerAllowlistAdvice(cfg.AuthorizedBlockProducers, p)
	if adv == "" || !strings.Contains(adv, "current producer "+p.ReplacementProducer) {
		t.Fatalf("expected the stale-allowlist advice naming replacement_producer, got %q", adv)
	}
}
