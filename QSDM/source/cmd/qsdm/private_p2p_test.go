package main

import (
	"context"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/config"
	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

const testPrivPeer = "12D3KooWHT2APYkMStuaiLZvto7ZmrPmUag4TStT7MryGn69feum"

// Private mode off: the historical selection (only 0.0.0.0 / :: are public).
func TestResolveP2PStartupMode_offUnchanged(t *testing.T) {
	cases := map[string]p2pStartupMode{
		"": p2pModeDefault, "127.0.0.1": p2pModeDefault, "100.101.1.1": p2pModeDefault,
		"0.0.0.0": p2pModePublic, "::": p2pModePublic, "[::]": p2pModePublic,
	}
	for bind, want := range cases {
		got, err := resolveP2PStartupMode(bind, config.PrivateP2PConfig{}, false)
		if err != nil || got != want {
			t.Errorf("bind %q: got %q, %v; want %q", bind, got, err, want)
		}
	}
	// The public fallback env is not this function's business when off.
	if got, err := resolveP2PStartupMode("127.0.0.1", config.PrivateP2PConfig{}, true); err != nil || got != p2pModeDefault {
		t.Fatalf("off with fallback: %q %v", got, err)
	}
}

func TestResolveP2PStartupMode_private(t *testing.T) {
	on := config.PrivateP2PConfig{Enabled: true, AllowedPeers: []string{testPrivPeer}}
	if got, err := resolveP2PStartupMode("100.101.1.1", on, false); err != nil || got != p2pModePrivate {
		t.Fatalf("private: %q %v", got, err)
	}
	for _, bind := range []string{"0.0.0.0", "::", "", "203.0.113.4"} {
		if got, err := resolveP2PStartupMode(bind, on, false); err == nil {
			t.Errorf("bind %q must be refused in private mode, got %q", bind, got)
		}
	}
	if _, err := resolveP2PStartupMode("100.101.1.1", on, true); err == nil || !strings.Contains(err.Error(), "QSDM_ALLOW_PUBLIC_DHT_FALLBACK") {
		t.Fatalf("public bootstrap fallback must be refused: %v", err)
	}
	if _, err := resolveP2PStartupMode("100.101.1.1", config.PrivateP2PConfig{Enabled: true}, false); err == nil {
		t.Fatal("private mode without peers must be refused")
	}
}

func TestSetupNetworkPrivate_requiresPeers(t *testing.T) {
	if _, err := SetupNetworkPrivate(context.Background(), nil, 0, "127.0.0.1", "", p2pLimitsFromConfig(config.P2PLimitsConfig{}), nil); err == nil {
		t.Fatal("no allowed peers must be an error")
	}
}

const (
	liveOld = "42d05e653f004d0aa71ae9cebc7b2a5b5d0dc7bb41b219a8e99d7c29229c438b"
	liveNew = "1cf60b16cf9aee5e8eaf67a8518a2b2ba7bc54f2dd607f05bc5a861b7a94c859"
)

func liveV1Transition() *producerpolicy.Transition {
	return &producerpolicy.Transition{Version: 1, CheckpointHeight: 659976,
		CheckpointHash:            "16884d35cee8b805fab51f1ecad740022fdb991e0fece15394774e1c5b7cdd0b",
		HistoricalSignatureHeight: 482566, HistoricalProducer: liveOld, ReplacementProducer: liveNew,
		EffectiveHeight: 659977, HistoricalPrefixBytes: 2728741562,
		HistoricalPrefixSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
}

// A stale allowlist: the block producer's allowlist pins the old producer
// while the transition names the new one. The advice says so; it is empty
// when the list is right, empty, or when no transition is configured (then
// the list is the gate and is not second-guessed).
func TestProducerAllowlistAdvice(t *testing.T) {
	tr := liveV1Transition()
	if err := tr.Validate(); err != nil {
		t.Fatal(err)
	}
	adv := producerAllowlistAdvice([]string{liveOld}, tr)
	if !strings.Contains(adv, liveNew) || !strings.Contains(adv, liveOld) {
		t.Fatalf("stale allowlist advice: %q", adv)
	}
	for name, list := range map[string][]string{
		"pinned new":    {liveNew},
		"both":          {liveOld, " " + liveNew + " "},
		"empty":         nil,
		"blank entries": {"", " "},
	} {
		if a := producerAllowlistAdvice(list, tr); a != "" {
			t.Errorf("%s: unexpected advice %q", name, a)
		}
	}
	if a := producerAllowlistAdvice([]string{liveOld}, nil); a != "" {
		t.Fatalf("no transition: %q", a)
	}
}
