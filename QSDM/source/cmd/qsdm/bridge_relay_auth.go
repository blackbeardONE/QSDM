package main

// Bridge relay authentication wiring (docs/docs/BRIDGE_RELAY_AUTH.md).
// Maps the [network] bridge_relay_* keys onto bridge.RelayAuthConfig and
// exports the relay's remote-event counters. With nothing configured the
// relay applies no remote bridge event and publishes local events unsigned.

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/bridge"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/config"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// maxBridgeRelayerKeysFileSize bounds the trusted-keys file (a hex key is
// 5184 characters, so this allows well over a hundred keys).
const maxBridgeRelayerKeysFileSize = 1 << 20

// bridgeRelayAuthFromConfig builds the relay authentication settings.
// consensusPub is the validator consensus public key; the bridge signer must
// be a different key. Any configured but unusable key is an error, so a
// typo never silently leaves the node with a different trust set.
func bridgeRelayAuthFromConfig(c config.BridgeRelayConfig, consensusPub []byte) (bridge.RelayAuthConfig, error) {
	out := bridge.RelayAuthConfig{MaxEventAge: c.MaxEventAge}
	seen := map[string]bool{}
	add := func(src, hexKey string) error {
		raw, err := bridge.ParseBridgeRelayerPublicKey(hexKey)
		if err != nil {
			return fmt.Errorf("%s: %w", src, err)
		}
		if id := bridge.BridgeKeyID(raw); !seen[id] {
			seen[id] = true
			out.TrustedKeys = append(out.TrustedKeys, raw)
		}
		return nil
	}
	for i, k := range c.TrustedKeys {
		if err := add(fmt.Sprintf("bridge_relay_trusted_keys[%d]", i), k); err != nil {
			return out, err
		}
	}
	if c.TrustedKeysFile != "" {
		keys, err := readBridgeRelayerKeysFile(c.TrustedKeysFile)
		if err != nil {
			return out, err
		}
		for i, k := range keys {
			if err := add(fmt.Sprintf("%s entry %d", c.TrustedKeysFile, i+1), k); err != nil {
				return out, err
			}
		}
	}
	if c.SignerKeyPath != "" {
		signer, err := loadBridgeRelaySigner(c.SignerKeyPath)
		if err != nil {
			return out, fmt.Errorf("bridge_relay_signer_key_path: %w", err)
		}
		if len(consensusPub) > 0 && bytes.Equal(signer.GetPublicKey(), consensusPub) {
			return out, errors.New("bridge_relay_signer_key_path holds the consensus signer key; use a separate bridge relayer key")
		}
		out.Signer = signer
	}
	return out, nil
}

// loadBridgeRelaySigner loads an existing ML-DSA-87 key file in the
// consensus signer format. Unlike the consensus key, the bridge relayer key
// is never created by the node: a missing file is an error.
func loadBridgeRelaySigner(path string) (*chain.PersistentBFTSigner, error) {
	if _, err := os.Lstat(path); err != nil {
		return nil, err
	}
	// The file exists, so this only loads it, with the checks the node
	// applies to its consensus key (regular file, size, matching key pair).
	signer, created, err := chain.LoadOrCreateBFTSigner(path)
	if err != nil {
		return nil, err
	}
	if created {
		return nil, fmt.Errorf("%q disappeared while it was being loaded; a new key was written there and is not used", path)
	}
	return signer, nil
}

// readBridgeRelayerKeysFile returns the hex keys in path: one per line,
// blank lines and text after '#' ignored.
func readBridgeRelayerKeysFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("bridge_relay_trusted_keys_file: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBridgeRelayerKeysFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("bridge_relay_trusted_keys_file %q: %w", path, err)
	}
	if len(data) > maxBridgeRelayerKeysFileSize {
		return nil, fmt.Errorf("bridge_relay_trusted_keys_file %q exceeds %d bytes", path, maxBridgeRelayerKeysFileSize)
	}
	var keys []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 16<<10), maxBridgeRelayerKeysFileSize)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		if line = strings.TrimSpace(line); line != "" {
			keys = append(keys, line)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("bridge_relay_trusted_keys_file %q: %w", path, err)
	}
	return keys, nil
}

// bridgeRelayMetricsCollector exports the relay's remote-event counters.
func bridgeRelayMetricsCollector(r *bridge.P2PRelay) monitoring.MetricCollector {
	return func() []monitoring.Metric {
		if r == nil {
			return nil
		}
		return bridgeRelayMetrics(r.Stats(), r.TrustedKeyCount(), r.Signing())
	}
}

func bridgeRelayMetrics(st bridge.RelayStats, trusted int, signing bool) []monitoring.Metric {
	const name = "qsdm_bridge_relay_remote_events_total"
	const help = "Bridge gossip events received from peers, by outcome (only 'applied' changed local bridge state)"
	var out []monitoring.Metric
	for _, r := range []struct {
		result string
		v      uint64
	}{
		{"applied", st.Applied},
		{"noop", st.NoOp},
		{"malformed", st.Malformed},
		{"no_trusted_keys", st.NoTrustedKeys},
		{"unsigned", st.Unsigned},
		{"untrusted_signer", st.UntrustedSigner},
		{"bad_signature", st.BadSignature},
		{"stale", st.Stale},
		{"bad_transition", st.BadTransition},
	} {
		out = append(out, monitoring.Metric{Name: name, Help: help, Type: monitoring.MetricCounter,
			Value: float64(r.v), Labels: map[string]string{"result": r.result}})
	}
	sign := 0.0
	if signing {
		sign = 1
	}
	return append(out,
		monitoring.Metric{Name: "qsdm_bridge_relay_trusted_keys", Help: "Bridge relayer public keys this node accepts", Type: monitoring.MetricGauge, Value: float64(trusted)},
		monitoring.Metric{Name: "qsdm_bridge_relay_signing", Help: "1 when locally published bridge events are signed", Type: monitoring.MetricGauge, Value: sign},
	)
}
