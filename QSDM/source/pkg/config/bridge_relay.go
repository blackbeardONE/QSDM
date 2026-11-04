package config

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// mlDSA87PublicKeySize is the packed ML-DSA-87 public key length (FIPS 204).
const mlDSA87PublicKeySize = 2592

// BridgeRelayTOML is the file form of the qsdm-bridge gossip authentication
// settings. It is embedded in [network]:
//
//	[network]
//	bridge_relay_trusted_keys = ["<hex ML-DSA-87 public key>", ...]
//	bridge_relay_trusted_keys_file = "/opt/qsdm/bridge_relayers.txt"  # one hex key per line, # comments
//	bridge_relay_signer_key_path = "/opt/qsdm/bridge_relayer_key.json"
//	bridge_relay_max_event_age = "10m"
//
// Every key is optional. With no trusted keys (the default) the node applies
// no bridge event received from a peer; local bridge API operations still
// work. See docs/docs/BRIDGE_RELAY_AUTH.md.
type BridgeRelayTOML struct {
	BridgeRelayTrustedKeys     []string `toml:"bridge_relay_trusted_keys" yaml:"bridge_relay_trusted_keys"`
	BridgeRelayTrustedKeysFile string   `toml:"bridge_relay_trusted_keys_file" yaml:"bridge_relay_trusted_keys_file"`
	BridgeRelaySignerKeyPath   string   `toml:"bridge_relay_signer_key_path" yaml:"bridge_relay_signer_key_path"`
	BridgeRelayMaxEventAge     string   `toml:"bridge_relay_max_event_age" yaml:"bridge_relay_max_event_age"`
}

// BridgeRelayConfig is the parsed form held on Config.
type BridgeRelayConfig struct {
	// TrustedKeys are hex-encoded packed ML-DSA-87 public keys whose
	// signatures are accepted on remote bridge events.
	TrustedKeys []string
	// TrustedKeysFile, when set, adds the keys listed in that file.
	TrustedKeysFile string
	// SignerKeyPath is an existing ML-DSA-87 key file in the consensus
	// signer key format ([consensus] signer_key_path). When set, local bridge
	// events are signed with it. It must be a different key from the
	// consensus key.
	SignerKeyPath string
	// MaxEventAge bounds the age of an accepted remote event (0 = default).
	MaxEventAge time.Duration
}

func (t BridgeRelayTOML) toConfig() (BridgeRelayConfig, error) {
	c := BridgeRelayConfig{
		TrustedKeysFile: strings.TrimSpace(t.BridgeRelayTrustedKeysFile),
		SignerKeyPath:   strings.TrimSpace(t.BridgeRelaySignerKeyPath),
	}
	for _, k := range t.BridgeRelayTrustedKeys {
		if k = strings.TrimSpace(k); k != "" {
			c.TrustedKeys = append(c.TrustedKeys, k)
		}
	}
	if s := strings.TrimSpace(t.BridgeRelayMaxEventAge); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return c, fmt.Errorf("invalid [network] bridge_relay_max_event_age %q: %w", s, err)
		}
		c.MaxEventAge = d
	}
	return c, nil
}

// applyBridgeRelayEnv applies QSDM_BRIDGE_RELAY_* overrides. A set
// QSDM_BRIDGE_RELAY_TRUSTED_KEYS (comma-separated hex) replaces the file
// list. A malformed duration is ignored, like the other env overrides.
func applyBridgeRelayEnv(c *BridgeRelayConfig) {
	if v := strings.TrimSpace(os.Getenv("QSDM_BRIDGE_RELAY_TRUSTED_KEYS")); v != "" {
		c.TrustedKeys = nil
		for _, k := range strings.Split(v, ",") {
			if k = strings.TrimSpace(k); k != "" {
				c.TrustedKeys = append(c.TrustedKeys, k)
			}
		}
	}
	if v := strings.TrimSpace(os.Getenv("QSDM_BRIDGE_RELAY_TRUSTED_KEYS_FILE")); v != "" {
		c.TrustedKeysFile = v
	}
	if v := strings.TrimSpace(os.Getenv("QSDM_BRIDGE_RELAY_SIGNER_KEY_PATH")); v != "" {
		c.SignerKeyPath = v
	}
	if v := strings.TrimSpace(os.Getenv("QSDM_BRIDGE_RELAY_MAX_EVENT_AGE")); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.MaxEventAge = d
		}
	}
}

// ValidBridgeRelayerKeyHex reports whether s is a hex-encoded packed
// ML-DSA-87 public key of the right length (an optional 0x prefix is
// allowed). Full unpacking is done by pkg/bridge at startup.
func ValidBridgeRelayerKeyHex(s string) error {
	s = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(s), "0x"), "0X")
	raw, err := hex.DecodeString(s)
	if err != nil {
		return fmt.Errorf("not hex: %w", err)
	}
	if len(raw) != mlDSA87PublicKeySize {
		return fmt.Errorf("want %d bytes (ML-DSA-87 public key), got %d", mlDSA87PublicKeySize, len(raw))
	}
	return nil
}

// Validate checks the inline keys and the duration. consensusKeyPath is the
// configured consensus signer key path; the bridge signer must not reuse it.
func (c BridgeRelayConfig) Validate(consensusKeyPath string) error {
	for i, k := range c.TrustedKeys {
		if err := ValidBridgeRelayerKeyHex(k); err != nil {
			return fmt.Errorf("[network] bridge_relay_trusted_keys[%d]: %w", i, err)
		}
	}
	if c.MaxEventAge < 0 {
		return fmt.Errorf("[network] bridge_relay_max_event_age must be >= 0, got %s", c.MaxEventAge)
	}
	if c.SignerKeyPath != "" && strings.TrimSpace(consensusKeyPath) != "" &&
		filepath.Clean(c.SignerKeyPath) == filepath.Clean(strings.TrimSpace(consensusKeyPath)) {
		return fmt.Errorf("[network] bridge_relay_signer_key_path must not be the consensus signer key")
	}
	return nil
}
