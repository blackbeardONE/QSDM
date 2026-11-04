package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	privPeerA = "12D3KooWHT2APYkMStuaiLZvto7ZmrPmUag4TStT7MryGn69feum"
	privPeerB = "12D3KooWAbCdEfGhJkLmNoPqRsTuVwXyZ123456789abcdefghij"
)

func TestPrivateP2P_TOMLUnsetIsOff(t *testing.T) {
	p := filepath.Join(t.TempDir(), "node.toml")
	if err := os.WriteFile(p, []byte("[network]\nport = 4001\nbind_address = \"127.0.0.1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{}
	if err := loadConfigFile(p, cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.PrivateP2P.Enabled || cfg.PrivateP2P.AllowedPeers != nil {
		t.Fatalf("unset keys must leave private mode off: %+v", cfg.PrivateP2P)
	}
}

func TestPrivateP2P_TOMLParses(t *testing.T) {
	p := filepath.Join(t.TempDir(), "node.toml")
	body := `[network]
port = 14001
bind_address = "100.101.102.103"
bootstrap_peers = ["/ip4/100.64.0.7/tcp/14001/p2p/` + privPeerA + `"]
private_p2p = true
private_p2p_allowed_peers = [" ` + privPeerA + ` ", "", "` + privPeerB + `"]
max_conns = 50
`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{}
	if err := loadConfigFile(p, cfg); err != nil {
		t.Fatal(err)
	}
	want := PrivateP2PConfig{Enabled: true, AllowedPeers: []string{privPeerA, privPeerB}}
	if !reflect.DeepEqual(cfg.PrivateP2P, want) {
		t.Fatalf("got %+v want %+v", cfg.PrivateP2P, want)
	}
	if cfg.P2PLimits.MaxConns != 50 {
		t.Fatal("embedding the private keys must not disturb the other [network] keys")
	}
	if err := cfg.PrivateP2P.Validate(cfg.NetworkBindAddress, cfg.BootstrapPeers); err != nil {
		t.Fatal(err)
	}
}

func TestPrivateP2P_YAMLParses(t *testing.T) {
	p := filepath.Join(t.TempDir(), "node.yaml")
	body := "network:\n  port: 14001\n  bind_address: 127.0.0.1\n  private_p2p: true\n  private_p2p_allowed_peers:\n    - " + privPeerA + "\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{}
	if err := loadConfigFile(p, cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.PrivateP2P.Enabled || !reflect.DeepEqual(cfg.PrivateP2P.AllowedPeers, []string{privPeerA}) {
		t.Fatalf("yaml: %+v", cfg.PrivateP2P)
	}
}

func TestPrivateP2P_EnvOverrides(t *testing.T) {
	t.Setenv("QSDM_P2P_PRIVATE", "true")
	t.Setenv("QSDM_P2P_PRIVATE_ALLOWED_PEERS", privPeerB+" , "+privPeerA)
	c := PrivateP2PConfig{AllowedPeers: []string{"old"}}
	applyPrivateP2PEnv(&c)
	if !c.Enabled || !reflect.DeepEqual(c.AllowedPeers, []string{privPeerB, privPeerA}) {
		t.Fatalf("env: %+v", c)
	}
	t.Setenv("QSDM_P2P_PRIVATE", "0")
	applyPrivateP2PEnv(&c)
	if c.Enabled {
		t.Fatal("QSDM_P2P_PRIVATE=0 must switch private mode off")
	}
}

func TestPrivateP2P_EnvUnsetKeepsFile(t *testing.T) {
	t.Setenv("QSDM_P2P_PRIVATE", "")
	t.Setenv("QSDM_P2P_PRIVATE_ALLOWED_PEERS", "")
	c := PrivateP2PConfig{Enabled: true, AllowedPeers: []string{privPeerA}}
	applyPrivateP2PEnv(&c)
	if !c.Enabled || len(c.AllowedPeers) != 1 {
		t.Fatalf("unset env must not change the file values: %+v", c)
	}
}

// The default (off, no list) accepts every bind, including the public one the
// public-P2P launcher uses: behaviour is unchanged.
func TestPrivateP2P_OffIsUnchanged(t *testing.T) {
	for _, b := range []string{"", "0.0.0.0", "::", "127.0.0.1", "8.8.8.8"} {
		if err := (PrivateP2PConfig{}).Validate(b, []string{"/dns4/x.example/tcp/4001/p2p/" + privPeerA}); err != nil {
			t.Errorf("off, bind %q: %v", b, err)
		}
	}
}

func TestPrivateP2P_ValidateRefusals(t *testing.T) {
	on := PrivateP2PConfig{Enabled: true, AllowedPeers: []string{privPeerA}}
	boot := "/ip4/100.64.0.7/tcp/14001/p2p/" + privPeerA
	cases := []struct {
		name  string
		c     PrivateP2PConfig
		bind  string
		boots []string
		want  string
	}{
		{"list without mode", PrivateP2PConfig{AllowedPeers: []string{privPeerA}}, "127.0.0.1", nil, "private_p2p is false"},
		{"mode without list", PrivateP2PConfig{Enabled: true}, "127.0.0.1", nil, "at least one"},
		{"bad peer id", PrivateP2PConfig{Enabled: true, AllowedPeers: []string{"peer-a"}}, "127.0.0.1", nil, "not a libp2p peer ID"},
		{"empty bind", on, "", nil, "empty binds every interface"},
		{"ipv4 any", on, "0.0.0.0", nil, "refuses public bind_address"},
		{"ipv6 any", on, "::", nil, "refuses public bind_address"},
		{"ipv6 any bracketed", on, "[::]", nil, "refuses public bind_address"},
		{"public ip", on, "203.0.113.9", nil, "not loopback"},
		{"host name", on, "vps.example", nil, "literal IP"},
		{"dns bootstrap", on, "100.101.1.1", []string{"/dns4/vps.example/tcp/14001/p2p/" + privPeerA}, "literal /ip4 or /ip6"},
		{"public bootstrap", on, "100.101.1.1", []string{"/ip4/203.0.113.9/tcp/14001/p2p/" + privPeerA}, "is not private"},
		{"bootstrap no id", on, "100.101.1.1", []string{"/ip4/100.64.0.7/tcp/14001"}, "missing /p2p/"},
		{"bootstrap unlisted", on, "100.101.1.1", []string{"/ip4/100.64.0.7/tcp/14001/p2p/" + privPeerB}, "not in private_p2p_allowed_peers"},
		{"bootstrap relayed", on, "100.101.1.1", []string{boot + "/p2p-circuit/p2p/" + privPeerA}, "relayed"},
	}
	for _, c := range cases {
		err := c.c.Validate(c.bind, c.boots)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error containing %q", c.name, err, c.want)
		}
	}
	for _, b := range []string{"127.0.0.1", "localhost", "100.101.102.103", "10.0.0.2", "192.168.1.5", "fd7a:115c:a1e0::1"} {
		if err := on.Validate(b, []string{boot, "/ip6/fd7a:115c:a1e0::7/tcp/14001/p2p/" + privPeerA, ""}); err != nil {
			t.Errorf("private bind %q: %v", b, err)
		}
	}
}

// config.Validate wires the check in: a private-mode config with a public
// bind fails the whole load.
func TestPrivateP2P_ConfigValidateWired(t *testing.T) {
	c := p2pBaseValidConfig()
	c.NetworkBindAddress = "0.0.0.0"
	c.PrivateP2P = PrivateP2PConfig{Enabled: true, AllowedPeers: []string{privPeerA}}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "private_p2p") {
		t.Fatalf("Validate: %v", err)
	}
	c.NetworkBindAddress = "100.101.102.103"
	if err := c.Validate(); err != nil {
		t.Fatalf("private bind: %v", err)
	}
	c.PrivateP2P = PrivateP2PConfig{}
	c.NetworkBindAddress = "0.0.0.0"
	if err := c.Validate(); err != nil {
		t.Fatalf("off: %v", err)
	}
}

// testdata/follower_core.toml is a follower config in private mode, as
// QSDM/docs/docs/PRIVATE_P2P.md describes it: the block producer's version 1
// transition table and consensus settings, the allowlist pinned to the
// current producer, a private bind and one private bootstrap peer, and the
// P2P limits switched on. The node must load it as written: private mode on
// and valid, the transition table valid, the allowlist pinned to the current
// producer, and the limits in effect.
func TestPrivateP2P_GeneratedFollowerConfigLoads(t *testing.T) {
	cfg := &Config{}
	if err := loadConfigFile(filepath.Join("testdata", "follower_core.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.PrivateP2P.Enabled || len(cfg.PrivateP2P.AllowedPeers) != 1 {
		t.Fatalf("private mode: %+v", cfg.PrivateP2P)
	}
	if err := cfg.PrivateP2P.Validate(cfg.NetworkBindAddress, cfg.BootstrapPeers); err != nil {
		t.Fatal(err)
	}
	if cfg.ProducerTransition == nil {
		t.Fatal("transition table missing")
	}
	if err := cfg.ProducerTransition.Validate(); err != nil {
		t.Fatal(err)
	}
	const current = "1cf60b16cf9aee5e8eaf67a8518a2b2ba7bc54f2dd607f05bc5a861b7a94c859"
	if got := cfg.ProducerTransition.ReplacementProducer; got != current {
		t.Fatalf("final producer %s", got)
	}
	if !reflect.DeepEqual(cfg.AuthorizedBlockProducers, []string{current}) {
		t.Fatalf("allowlist %v", cfg.AuthorizedBlockProducers)
	}
	if cfg.NetworkBindAddress != "100.101.102.103" || cfg.NetworkPort != 24001 || cfg.P2PLimits.MaxConns != 64 ||
		!cfg.P2PLimits.Effective() {
		t.Fatalf("network: %s %d %+v", cfg.NetworkBindAddress, cfg.NetworkPort, cfg.P2PLimits)
	}
	if err := cfg.P2PLimits.Validate(); err != nil {
		t.Fatal(err)
	}
	if !cfg.RequireSignedVotes || cfg.SignedConsensusActivationHeight != 827000 {
		t.Fatal("consensus settings must be the producer's")
	}
}
