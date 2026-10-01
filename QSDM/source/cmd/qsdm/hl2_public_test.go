package main

// HL2 WP-D wiring: ModePublic through the canary plumbing (hl1BootConfig.Canary
// is "mode is not off"), the v2 Ledger as the mining service's OwnerSink, and
// the S2 refusal of the Tier-3 reward penalty with a version 2 config. The S2
// gate (CheckSupported) still refuses ModePublic until WP-E.

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/internal/miningsvc"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

func TestHL2PublicModeWiring(t *testing.T) {
	a, b := newHL2Wallet(t), newHL2Wallet(t)
	st := enrollment.NewInMemoryState()
	hl2Enroll(t, st, "node-a", a.owner, "GPU-a", mining.MinEnrollStakeDust)
	hl2Enroll(t, st, "node-b", b.owner, "GPU-b", mining.MinEnrollStakeDust)
	legacy := hl2LegacyDir(t)
	env := legacymining.Env{Mode: legacymining.ModePublic, DBPath: filepath.Join(legacy, legacymining.DBFile)}
	cfg := legacymining.Config{
		Version: 2, MaxProofsPerMin: 60, MaxProofsPerMinPerOwner: 6, MaxProofsTotal: 1000, MaxPending: 100,
		MaxPendingPerOwner: 10, OwnerEpochCapCell: 10, DifficultyBits: 16, RequireOperatorSig: true,
		RequireFullyBonded: true, BondedSlotCap: 1, BudgetCell: 100, ExpiresUnix: time.Now().Add(time.Hour).Unix(),
	}
	if err := legacymining.CheckModeConfig(legacymining.ModePublic, cfg); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	boot := hl1BootConfig{Env: env, Config: cfg}
	if !boot.Canary() || (hl1BootConfig{}).Canary() {
		t.Fatal("Canary() must mean legacy mining is on (canary or public)")
	}
	parts := hl1NewCanary(boot, true, chain.NewAccountStore(), st)
	if !parts.enabled() || parts.view == nil || parts.keys == nil || parts.status() != "public" {
		t.Fatalf("public parts: %+v (%s)", parts, parts.status())
	}
	if parts.store.SchemaVersion() != 0 { // not opened before S7
		t.Fatal("store opened early")
	}
	var sink legacymining.Sink = parts.ledger
	if _, ok := sink.(legacymining.OwnerSink); !ok {
		t.Fatal("the v2 Ledger is not an OwnerSink")
	}
	if err := parts.ledger.CheckOwnerEpoch(a.owner); err != nil {
		t.Fatalf("CheckOwnerEpoch before any payment: %v", err)
	}
	// Followers and Stage A keep their HL1 behaviour.
	if p := hl1NewCanary(boot, false, chain.NewAccountStore(), st); p.enabled() {
		t.Fatal("public mode without local block production built the machinery")
	}

	// The writable mining service accepts the v2 Guard and Ledger.
	bp, _ := hl1ProducerWithChain(t, 2)
	tip := &hl1DurableTip{}
	tip.Store(bp.TipHeight())
	mcfg := hl1MiningServiceConfig(hl1BaseMiningConfig(), &durableChainView{tip: tip, producer: bp}, parts, true)
	if mcfg.ReadOnly || mcfg.Sink != legacymining.Sink(parts.ledger) {
		t.Fatalf("mining config %+v", mcfg)
	}
	if _, err := miningsvc.New(mcfg); err != nil {
		t.Fatalf("miningsvc.New(public): %v", err)
	}
}

func TestHL2RewardPenaltyRefusedAtS2(t *testing.T) {
	dir := t.TempDir()
	v2Path, v2Pin := hl1V2ConfigFile(t, dir, "v2.json", nil)
	v1Path, v1Pin := hl1CanaryConfigFile(t, dir)
	db := filepath.Join(dir, legacymining.LegacyDirName, legacymining.DBFile)
	env := func(mode, path, pin, penalty string) func(string) string {
		m := map[string]string{
			legacymining.EnvMode: mode, legacymining.EnvDB: db,
			legacymining.EnvCanaryConfig: path, legacymining.EnvCanaryConfigSHA256: pin,
			"QSDM_SPEC_PENALTY_ENABLED": penalty,
		}
		return func(k string) string { return m[k] }
	}
	for _, mode := range []string{"public", "canary"} {
		for _, on := range []string{"1", "true", "yes"} {
			_, err := hl1LoadBootConfig(env(mode, v2Path, v2Pin, on), nil)
			if !errors.Is(err, legacymining.ErrConfig) || !strings.Contains(err.Error(), "Tier-3") {
				t.Fatalf("%s v2 with QSDM_SPEC_PENALTY_ENABLED=%s: %v", mode, on, err)
			}
		}
		// Without the penalty the v2 config still meets the WP-E gate.
		if _, err := hl1LoadBootConfig(env(mode, v2Path, v2Pin, "0"), nil); !errors.Is(err, legacymining.ErrNotImplemented) {
			t.Fatalf("%s v2 without the penalty: %v", mode, err)
		}
	}
	// A v1 canary keeps Tier-3 (HL1 behaviour).
	if b, err := hl1LoadBootConfig(env("canary", v1Path, v1Pin, "1"), nil); err != nil || !b.Canary() {
		t.Fatalf("v1 canary with the penalty: %+v, %v", b, err)
	}
	if err := hl2CheckRewardPenalty(legacymining.Config{Version: 2}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
}
