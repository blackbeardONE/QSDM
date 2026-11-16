package main

// rc1114 release defaults (2026-11-16): an unset PULSE config gives the release height 1,210,000 and the hosted pool
// as the only authorized pool, and both reach the chain through applyPulseParams (main and the hl1 replay).

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/config"
)

func TestPulseReleaseDefaultsReachTheChain(t *testing.T) {
	t.Cleanup(func() { _ = chain.SetPulseParams(chain.PulseParams{}) })
	if config.PulseDefaultActivationHeight != 1210000 {
		t.Fatalf("release height %d, want 1210000", config.PulseDefaultActivationHeight)
	}
	pk, err := hex.DecodeString(config.PulseHostedPoolPublicKey)
	if err != nil || len(pk) != 2592 {
		t.Fatalf("hosted pool key: %d bytes, %v", len(pk), err)
	}
	if s := sha256.Sum256(pk); hex.EncodeToString(s[:]) != config.PulseHostedPoolID {
		t.Fatal("the hosted pool ID is not sha256 of its key")
	}

	cfg := rc1113LoadCfg(t, "[consensus]\n")
	if cfg.PulseActivationHeight != 1210000 || len(cfg.PulsePools) != 1 || cfg.PulsePools[0] != config.PulseHostedPoolID+":"+config.PulseHostedPoolPublicKey {
		t.Fatalf("unset config: height %d, pools %d", cfg.PulseActivationHeight, len(cfg.PulsePools))
	}
	if err := applyPulseParams(cfg); err != nil {
		t.Fatalf("applyPulseParams with the release defaults: %v", err)
	}
	p := chain.PulseParamsSnapshot()
	if p.ActivationHeight != 1210000 || len(p.Pools) != 1 || string(p.Pools[0].ID) != config.PulseHostedPoolID {
		t.Fatalf("chain params: height %d, pools %+v", p.ActivationHeight, len(p.Pools))
	}
	if p.MaxFeeBPS != 300 || p.ReclaimAfterBlocks != 8640 {
		t.Fatalf("fee ceiling %d, reclaim %d; want 300 and 8640", p.MaxFeeBPS, p.ReclaimAfterBlocks)
	}

	// The replay path applies the same values.
	_ = chain.SetPulseParams(chain.PulseParams{})
	hl1ReplayConsensusSettings(rc1113LoadCfg(t, "[consensus]\n"))
	if chain.PulseActivationHeight() != 1210000 {
		t.Fatalf("replay: chain height %d, want 1210000", chain.PulseActivationHeight())
	}

	// An explicit key or env value still overrides the release default.
	t.Setenv("QSDM_PULSE_ACTIVATION_HEIGHT", "1215000")
	if cfg = rc1113LoadCfg(t, "[consensus]\n"); cfg.PulseActivationHeight != 1215000 {
		t.Fatalf("env override: %d", cfg.PulseActivationHeight)
	}
	os.Unsetenv("QSDM_PULSE_ACTIVATION_HEIGHT")
	if cfg = rc1113LoadCfg(t, "[consensus]\npulse_activation_height = 1220000\n"); cfg.PulseActivationHeight != 1220000 {
		t.Fatalf("config key override: %d", cfg.PulseActivationHeight)
	}
}
