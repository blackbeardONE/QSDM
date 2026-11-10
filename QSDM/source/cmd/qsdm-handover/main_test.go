package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/config"
	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

// fullNodeHead and fullNodeTail surround the transition table of the public
// full-node release's fullnode-core.toml (fullnode-rc1108).
const fullNodeHead = `# Full node (HTTP sync) core.toml.
# libp2p listens on loopback with no peers.

[node]
role = "validator"
mining_enabled = false

[network]
port = 14001
bind_address = "127.0.0.1"
bootstrap_peers = []

[storage]
type = "sqlite"
sqlite_path = "/var/lib/qsdm-follower/qsdm.db"

[api]
port = 8080
bind_address = "127.0.0.1"

[consensus]
authorized_block_producers = ["1cf60b16cf9aee5e8eaf67a8518a2b2ba7bc54f2dd607f05bc5a861b7a94c859"]
edge_relay_only_activation_height = 833800
enrollment_state_root_activation_height = 625000
fork_dust_height = 0
poe_activation_height = 816600
require_signed_votes = true
signed_message_activation_height = 827000
task_action_signature_activation_height = 625000
tx_content_root_activation_height = 625000

`

const fullNodeV1 = `[consensus.producer_transition]
checkpoint_hash = "16884d35cee8b805fab51f1ecad740022fdb991e0fece15394774e1c5b7cdd0b"
checkpoint_height = 659976
effective_height = 659977
historical_prefix_bytes = 2728741562
historical_prefix_sha256 = "3c4db4879b82544ccef7e2abe0c0e2ef7741864e13307fc95978abc393f7057b"
historical_producer = "42d05e653f004d0aa71ae9cebc7b2a5b5d0dc7bb41b219a8e99d7c29229c438b"
historical_signature_height = 482566
replacement_producer = "1cf60b16cf9aee5e8eaf67a8518a2b2ba7bc54f2dd607f05bc5a861b7a94c859"
version = 1 # the public network's one hand-over so far

`

const fullNodeTail = `[snapshot_bootstrap]
enabled = true
checkpoint_height = 880376
checkpoint_block_hash = "a8a80c6b9e15fadd61cc263795b466d551f15da987b73e4da601f6fc6f50511e"
checkpoint_state_root = "5e58629946d9dd2e5643249e0a18494d29446e9ad82fc562e1e064a5efdcf0e1"
checkpoint_producer_id = "1cf60b16cf9aee5e8eaf67a8518a2b2ba7bc54f2dd607f05bc5a861b7a94c859"
`

const (
	histProducer = "42d05e653f004d0aa71ae9cebc7b2a5b5d0dc7bb41b219a8e99d7c29229c438b"
	liveProducer = "1cf60b16cf9aee5e8eaf67a8518a2b2ba7bc54f2dd607f05bc5a861b7a94c859"
	firstCP      = "16884d35cee8b805fab51f1ecad740022fdb991e0fece15394774e1c5b7cdd0b"
	prefixSHA    = "3c4db4879b82544ccef7e2abe0c0e2ef7741864e13307fc95978abc393f7057b"
)

func h(c string) string { return strings.Repeat(c, 64) }

func writeFile(t *testing.T, dir, name, text string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(text), 0o640); err != nil {
		t.Fatal(err)
	}
	return p
}

func runTool(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// The public full node's version 1 table becomes a three-epoch schedule in
// the same place; every other line stays as it was, and the input file is
// not touched.
func TestHandoverFromVersion1(t *testing.T) {
	dir := t.TempDir()
	src := fullNodeHead + fullNodeV1 + fullNodeTail
	in := writeFile(t, dir, "core.toml", src)
	out := filepath.Join(dir, "core-handover.toml")
	code, stdout, stderr := runTool("--config", in, "--out", out, "--from-height", "1062601",
		"--producer", h("e"), "--checkpoint-hash", h("f"))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	want := &producerpolicy.Transition{Version: 2, HistoricalPrefixBytes: 2728741562, HistoricalPrefixSHA256: prefixSHA,
		Epochs: []producerpolicy.Epoch{
			{FromHeight: 482566, ProducerID: histProducer},
			{FromHeight: 659977, ProducerID: liveProducer, CheckpointHash: firstCP},
			{FromHeight: 1062601, ProducerID: h("e"), CheckpointHash: h("f")},
		}}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != fullNodeHead+tableTOML(want)+"\n"+fullNodeTail {
		t.Fatalf("written file:\n%s", got)
	}
	cfg, err := config.LoadConfigFile(out)
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.ProducerTransition
	if p == nil || p.Version != 2 || len(p.Epochs) != 3 || p.FinalEpoch().ProducerID != h("e") || p.FirstHandoverHeight() != 659977 {
		t.Fatalf("loaded schedule %+v", p)
	}
	if cfg.SQLitePath != "/var/lib/qsdm-follower/qsdm.db" || cfg.PoEActivationHeight != 816600 || len(cfg.AuthorizedBlockProducers) != 1 {
		t.Fatalf("other settings changed: %+v", cfg)
	}
	if again, _ := os.ReadFile(in); string(again) != src {
		t.Fatal("the input file changed")
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(out)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", fi.Mode())
		}
	}
	for _, s := range []string{"from 482566  " + histProducer + "\n", "from 1062601  " + h("e") + "  after block 1062600 " + h("f"), "checked:"} {
		if !strings.Contains(stdout, s) {
			t.Fatalf("output lacks %q:\n%s", s, stdout)
		}
	}
}

// A version 2 schedule gets one more epoch; the file written by one hand-over
// is the input of the next.
func TestHandoverFromVersion2(t *testing.T) {
	dir := t.TempDir()
	in := writeFile(t, dir, "core.toml", fullNodeHead+fullNodeV1+fullNodeTail)
	mid := filepath.Join(dir, "core-2.toml")
	if code, _, stderr := runTool("--config", in, "--out", mid, "--from-height", "1062601", "--producer", h("e"), "--checkpoint-hash", h("f")); code != 0 {
		t.Fatal(stderr)
	}
	out := filepath.Join(dir, "core-3.toml")
	if code, _, stderr := runTool("--config", mid, "--out", out, "--from-height", "1100000", "--producer", h("1"), "--checkpoint-hash", h("2")); code != 0 {
		t.Fatal(stderr)
	}
	cfg, err := config.LoadConfigFile(out)
	if err != nil {
		t.Fatal(err)
	}
	e := cfg.ProducerTransition.Epochs
	if len(e) != 4 || e[2].ProducerID != h("e") || e[3].FromHeight != 1100000 || e[3].ProducerID != h("1") || e[3].CheckpointHash != h("2") {
		t.Fatalf("schedule %+v", e)
	}
	data, _ := os.ReadFile(out)
	if !strings.HasPrefix(string(data), fullNodeHead) || !strings.HasSuffix(string(data), "\n"+fullNodeTail) ||
		strings.Count(string(data), "[consensus.producer_transition]") != 1 || strings.Count(string(data), "[[consensus.producer_transition.epochs]]") != 4 {
		t.Fatalf("written file:\n%s", data)
	}
}

// A file with Windows line ends stays one.
func TestHandoverKeepsCRLF(t *testing.T) {
	dir := t.TempDir()
	src := strings.ReplaceAll(fullNodeHead+fullNodeV1+fullNodeTail, "\n", "\r\n")
	in := writeFile(t, dir, "core.toml", src)
	out := filepath.Join(dir, "new.toml")
	if code, _, stderr := runTool("--config", in, "--out", out, "--from-height", "1062601", "--producer", h("e"), "--checkpoint-hash", h("f")); code != 0 {
		t.Fatal(stderr)
	}
	data, _ := os.ReadFile(out)
	if strings.Count(string(data), "\n") != strings.Count(string(data), "\r\n") {
		t.Fatal("mixed line ends")
	}
	if _, err := config.LoadConfigFile(out); err != nil {
		t.Fatal(err)
	}
}

func TestHandoverRefusals(t *testing.T) {
	good := fullNodeHead + fullNodeV1 + fullNodeTail
	inline := strings.Replace(fullNodeHead, "[consensus]\n", "[consensus]\nproducer_transition = { version = 1, checkpoint_height = 659976, checkpoint_hash = \""+firstCP+
		"\", historical_signature_height = 482566, historical_producer = \""+histProducer+"\", replacement_producer = \""+liveProducer+
		"\", effective_height = 659977, historical_prefix_bytes = 2728741562, historical_prefix_sha256 = \""+prefixSHA+"\" }\n", 1) + fullNodeTail
	for _, c := range []struct {
		name, text, file string
		args             []string
		code             int
		msg              string
	}{
		{"no table", fullNodeHead + fullNodeTail, "core.toml", nil, 1, "has no [consensus.producer_transition] table"},
		{"yaml", "consensus: {}\n", "core.yaml", nil, 1, "only .toml files"},
		{"not above the current producer", good, "core.toml", []string{"--from-height", "659977"}, 1, "is not above 659977"},
		{"a producer key again", good, "core.toml", []string{"--producer", liveProducer}, 1, "repeats epochs[1]"},
		{"zero checkpoint", good, "core.toml", []string{"--checkpoint-hash", h("0")}, 1, "must not be zero"},
		{"uppercase hex", good, "core.toml", []string{"--producer", strings.ToUpper(h("e"))}, 1, "lowercase hexadecimal"},
		{"short hash", good, "core.toml", []string{"--checkpoint-hash", "abc"}, 1, "64 lowercase hexadecimal"},
		{"inline table", inline, "core.toml", nil, 1, "inline or dotted keys"},
		{"missing flag", good, "core.toml", []string{"--producer", ""}, 2, "all required"},
		{"stray argument", good, "core.toml", []string{"extra"}, 2, "all required"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			in := writeFile(t, dir, c.file, c.text)
			out := filepath.Join(dir, "new.toml")
			args := map[string]string{"--config": in, "--out": out, "--from-height": "1062601", "--producer": h("e"), "--checkpoint-hash": h("f")}
			var extra []string
			for i := 0; i < len(c.args); i++ {
				if strings.HasPrefix(c.args[i], "--") && i+1 < len(c.args) {
					args[c.args[i]] = c.args[i+1]
					i++
				} else {
					extra = append(extra, c.args[i])
				}
			}
			var argv []string
			for _, k := range []string{"--config", "--out", "--from-height", "--producer", "--checkpoint-hash"} {
				argv = append(argv, k, args[k])
			}
			code, _, stderr := runTool(append(argv, extra...)...)
			if code != c.code || !strings.Contains(stderr, c.msg) {
				t.Fatalf("exit %d, want %d with %q:\n%s", code, c.code, c.msg, stderr)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatalf("a refused run left %s: %v", out, err)
			}
		})
	}
}

// An existing --out is never overwritten.
func TestHandoverNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	in := writeFile(t, dir, "core.toml", fullNodeHead+fullNodeV1+fullNodeTail)
	out := writeFile(t, dir, "new.toml", "keep me\n")
	code, _, stderr := runTool("--config", in, "--out", out, "--from-height", "1062601", "--producer", h("e"), "--checkpoint-hash", h("f"))
	if code != 1 || !strings.Contains(stderr, "already exists") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if data, _ := os.ReadFile(out); string(data) != "keep me\n" {
		t.Fatal("the existing file changed")
	}
}
