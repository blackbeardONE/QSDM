package main

// rc1113 release wiring (2026-11-16): the one rc1113 rules height reaches the chain from the node's config on both
// paths that apply consensus settings (the live boot in main() and the hl1 replay), and the C10 retry runs every
// catch-up round, before the round can end for lack of peers.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/config"
)

func rc1113LoadCfg(t *testing.T, toml string) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "qsdm.toml")
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_FILE", path)
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return cfg
}

// The replay applies the configured height to the chain; an unset key gives the release default 1,230,000.
func TestRC1113RulesHeightReachesChainThroughReplaySettings(t *testing.T) {
	base := rc1113LoadCfg(t, "[consensus]\n")
	t.Cleanup(func() { hl1ReplayConsensusSettings(base); chain.SetRC1113RulesHeight(0) })

	cfg := rc1113LoadCfg(t, "[consensus]\nrc1113_rules_activation_height = 1235000\n")
	hl1ReplayConsensusSettings(cfg)
	if got := chain.RC1113RulesHeight(); got != 1235000 {
		t.Fatalf("config key: chain height %d, want 1235000", got)
	}

	t.Setenv("QSDM_RC1113_RULES_ACTIVATION_HEIGHT", "1240000")
	cfg = rc1113LoadCfg(t, "[consensus]\n")
	chain.SetRC1113RulesHeight(0)
	hl1ReplayConsensusSettings(cfg)
	if got := chain.RC1113RulesHeight(); got != 1240000 {
		t.Fatalf("env: chain height %d, want 1240000", got)
	}

	os.Unsetenv("QSDM_RC1113_RULES_ACTIVATION_HEIGHT")
	chain.SetRC1113RulesHeight(77)
	hl1ReplayConsensusSettings(rc1113LoadCfg(t, "[consensus]\n"))
	if got := chain.RC1113RulesHeight(); got != 1230000 {
		t.Fatalf("unset: chain height %d, want the release default 1230000", got)
	}
}

func rc1113MainSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// main() passes the config value itself to the chain (not a constant, not another field), exactly once.
func TestRC1113RulesHeightSetInMainFromConfig(t *testing.T) {
	src := rc1113MainSource(t)
	calls := regexp.MustCompile(`chain\.SetRC1113RulesHeight\(([^)]*)\)`).FindAllStringSubmatch(src, -1)
	if len(calls) != 1 {
		t.Fatalf("main.go calls chain.SetRC1113RulesHeight %d times, want 1", len(calls))
	}
	if arg := strings.TrimSpace(calls[0][1]); arg != "cfg.RC1113RulesActivationHeight" {
		t.Fatalf("main.go passes %q, want cfg.RC1113RulesActivationHeight", arg)
	}
	// It runs in main() itself, before the chain starts appending (the propagator and the producer come later).
	m := strings.Index(src, "\nfunc main() {")
	set := strings.Index(src, "chain.SetRC1113RulesHeight(")
	prop := strings.Index(src, "blockPropagator = bp")
	if m < 0 || set < m || prop < 0 || set > prop {
		t.Fatalf("SetRC1113RulesHeight is not in main() before the block propagator starts (main %d, set %d, propagator %d)", m, set, prop)
	}
}

// C10: every catch-up round calls RetryPending first, so a queued or held block is retried even with no peers.
func TestC10RetryPendingRunsEveryCatchUpRound(t *testing.T) {
	src := rc1113MainSource(t)
	i := strings.Index(src, "requestNextWindow := func() {")
	if i < 0 {
		t.Fatal("main.go has no requestNextWindow round")
	}
	body := src[i:]
	end := strings.Index(body, "\n\t\t\t}\n")
	if end < 0 {
		t.Fatal("requestNextWindow: end not found")
	}
	body = body[:end]
	r := strings.Index(body, "blockPropagator.RetryPending()")
	p := strings.Index(body, "net.PeerCount() == 0")
	if r < 0 {
		t.Fatal("requestNextWindow does not call blockPropagator.RetryPending()")
	}
	if p >= 0 && r > p {
		t.Fatal("RetryPending runs after the no-peers return, so a node without peers never retries")
	}
	if c := strings.Count(src, "requestNextWindow()"); c < 2 {
		t.Fatalf("requestNextWindow is called %d times, want the first round and the ticker", c)
	}
}
