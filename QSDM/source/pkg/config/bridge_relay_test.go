package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testRelayerKeyHex = strings.Repeat("ab", mlDSA87PublicKeySize)

func TestBridgeRelay_UnsetIsEmpty(t *testing.T) {
	p := filepath.Join(t.TempDir(), "node.toml")
	if err := os.WriteFile(p, []byte("[network]\nport = 4001\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{}
	if err := loadConfigFile(p, cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.BridgeRelay.TrustedKeys) != 0 || cfg.BridgeRelay.TrustedKeysFile != "" ||
		cfg.BridgeRelay.SignerKeyPath != "" || cfg.BridgeRelay.MaxEventAge != 0 {
		t.Fatalf("unset bridge relay keys must stay empty: %+v", cfg.BridgeRelay)
	}
	if err := cfg.BridgeRelay.Validate(""); err != nil {
		t.Fatal(err)
	}
}

func TestBridgeRelay_TOMLParsesAllKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "node.toml")
	content := "[network]\nport = 4001\n" +
		"bridge_relay_trusted_keys = [\"" + testRelayerKeyHex + "\", \"  \"]\n" +
		"bridge_relay_trusted_keys_file = \" /etc/qsdm/relayers.txt \"\n" +
		"bridge_relay_signer_key_path = \"/opt/qsdm/bridge_relayer_key.json\"\n" +
		"bridge_relay_max_event_age = \"90s\"\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{}
	if err := loadConfigFile(p, cfg); err != nil {
		t.Fatal(err)
	}
	br := cfg.BridgeRelay
	if len(br.TrustedKeys) != 1 || br.TrustedKeys[0] != testRelayerKeyHex ||
		br.TrustedKeysFile != "/etc/qsdm/relayers.txt" ||
		br.SignerKeyPath != "/opt/qsdm/bridge_relayer_key.json" || br.MaxEventAge != 90*time.Second {
		t.Fatalf("parsed = %+v", br)
	}
	if err := br.Validate("/opt/qsdm/consensus.json"); err != nil {
		t.Fatal(err)
	}
}

func TestBridgeRelay_YAMLParses(t *testing.T) {
	p := filepath.Join(t.TempDir(), "node.yaml")
	content := "network:\n  port: 4001\n  bridge_relay_trusted_keys:\n    - " + testRelayerKeyHex +
		"\n  bridge_relay_signer_key_path: /k.json\n  bridge_relay_max_event_age: 5m\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{}
	if err := loadConfigFile(p, cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.BridgeRelay.TrustedKeys) != 1 || cfg.BridgeRelay.SignerKeyPath != "/k.json" ||
		cfg.BridgeRelay.MaxEventAge != 5*time.Minute {
		t.Fatalf("parsed = %+v", cfg.BridgeRelay)
	}
}

func TestBridgeRelay_BadDurationRejected(t *testing.T) {
	p := filepath.Join(t.TempDir(), "node.toml")
	if err := os.WriteFile(p, []byte("[network]\nport = 4001\nbridge_relay_max_event_age = \"soon\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := loadConfigFile(p, &Config{}); err == nil || !strings.Contains(err.Error(), "bridge_relay_max_event_age") {
		t.Fatalf("err = %v", err)
	}
}

func TestBridgeRelay_EnvOverrides(t *testing.T) {
	c := BridgeRelayConfig{TrustedKeys: []string{"from-file"}, MaxEventAge: time.Minute}
	t.Setenv("QSDM_BRIDGE_RELAY_TRUSTED_KEYS", " "+testRelayerKeyHex+" , ,0x"+testRelayerKeyHex)
	t.Setenv("QSDM_BRIDGE_RELAY_TRUSTED_KEYS_FILE", "/f.txt")
	t.Setenv("QSDM_BRIDGE_RELAY_SIGNER_KEY_PATH", "/s.json")
	t.Setenv("QSDM_BRIDGE_RELAY_MAX_EVENT_AGE", "bogus")
	applyBridgeRelayEnv(&c)
	if len(c.TrustedKeys) != 2 || c.TrustedKeysFile != "/f.txt" || c.SignerKeyPath != "/s.json" || c.MaxEventAge != time.Minute {
		t.Fatalf("after env = %+v", c)
	}
	if err := c.Validate(""); err != nil {
		t.Fatal(err)
	}
	t.Setenv("QSDM_BRIDGE_RELAY_MAX_EVENT_AGE", "3m")
	applyBridgeRelayEnv(&c)
	if c.MaxEventAge != 3*time.Minute {
		t.Fatalf("MaxEventAge = %s", c.MaxEventAge)
	}
}

func TestBridgeRelay_Validate(t *testing.T) {
	bad := []BridgeRelayConfig{
		{TrustedKeys: []string{"zz"}},
		{TrustedKeys: []string{"abcd"}},
		{MaxEventAge: -time.Second},
	}
	for i, c := range bad {
		if err := c.Validate(""); err == nil {
			t.Fatalf("case %d accepted: %+v", i, c)
		}
	}
	reuse := BridgeRelayConfig{SignerKeyPath: "/opt/qsdm/./consensus.json"}
	if err := reuse.Validate("/opt/qsdm/consensus.json"); err == nil {
		t.Fatal("consensus key reused as bridge signer was accepted")
	}

	cfg := baseValidConfig()
	cfg.BridgeRelay.TrustedKeys = []string{"nothex"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "bridge_relay_trusted_keys") {
		t.Fatalf("Config.Validate err = %v", err)
	}
}
