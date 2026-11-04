package main

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/bridge"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/config"
	"github.com/blackbeardONE/QSDM/pkg/crypto"
)

func testRelayerPub(t *testing.T) []byte {
	t.Helper()
	d := crypto.NewDilithium()
	if d == nil {
		t.Fatal("NewDilithium returned nil")
	}
	return d.GetPublicKey()
}

func TestBridgeRelayAuthFromConfig_DefaultIsClosed(t *testing.T) {
	a, err := bridgeRelayAuthFromConfig(config.BridgeRelayConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.TrustedKeys) != 0 || a.Signer != nil || a.MaxEventAge != 0 {
		t.Fatalf("default auth = %+v, want no keys and no signer", a)
	}
}

func TestBridgeRelayAuthFromConfig_InlineAndFileKeys(t *testing.T) {
	k1, k2, k3 := testRelayerPub(t), testRelayerPub(t), testRelayerPub(t)
	file := filepath.Join(t.TempDir(), "relayers.txt")
	body := "# bridge relayers\n\n" + hex.EncodeToString(k2) + "  # relayer two\n" +
		strings.ToUpper(hex.EncodeToString(k3)) + "\n" + hex.EncodeToString(k1) + "\n" // k1 duplicates the inline key
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := bridgeRelayAuthFromConfig(config.BridgeRelayConfig{
		TrustedKeys:     []string{"0x" + hex.EncodeToString(k1)},
		TrustedKeysFile: file,
		MaxEventAge:     time.Minute,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.TrustedKeys) != 3 || !bytes.Equal(a.TrustedKeys[0], k1) || !bytes.Equal(a.TrustedKeys[1], k2) ||
		!bytes.Equal(a.TrustedKeys[2], k3) || a.MaxEventAge != time.Minute {
		t.Fatalf("auth = %d keys, age %s", len(a.TrustedKeys), a.MaxEventAge)
	}
}

func TestBridgeRelayAuthFromConfig_Errors(t *testing.T) {
	dir := t.TempDir()
	badFile := filepath.Join(dir, "bad.txt")
	if err := os.WriteFile(badFile, []byte("abcd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bigFile := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(bigFile, bytes.Repeat([]byte("#"), maxBridgeRelayerKeysFileSize+1), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string]config.BridgeRelayConfig{
		"bad inline key": {TrustedKeys: []string{"not-a-key"}},
		"bad file entry": {TrustedKeysFile: badFile},
		"missing file":   {TrustedKeysFile: filepath.Join(dir, "absent.txt")},
		"oversized file": {TrustedKeysFile: bigFile},
		"missing signer": {SignerKeyPath: filepath.Join(dir, "absent.json")},
	}
	for name, c := range cases {
		if _, err := bridgeRelayAuthFromConfig(c, nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A missing signer key is an error, never created on the spot.
	if _, err := os.Lstat(filepath.Join(dir, "absent.json")); !os.IsNotExist(err) {
		t.Fatalf("missing bridge signer key was created (lstat err %v)", err)
	}
}

func TestBridgeRelayAuthFromConfig_Signer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bridge_relayer_key.json")
	key, created, err := chain.LoadOrCreateBFTSigner(path)
	if err != nil || !created {
		t.Fatalf("create key file: created=%v err=%v", created, err)
	}
	a, err := bridgeRelayAuthFromConfig(config.BridgeRelayConfig{SignerKeyPath: path}, testRelayerPub(t))
	if err != nil {
		t.Fatal(err)
	}
	if a.Signer == nil || !bytes.Equal(a.Signer.GetPublicKey(), key.GetPublicKey()) {
		t.Fatal("signer not loaded")
	}
	// The key ID in signed events is the key's validator address.
	if bridge.BridgeKeyID(key.GetPublicKey()) != key.Address() {
		t.Fatal("bridge key ID differs from the key's validator address")
	}
	// The relay accepts the loaded key as a signer and a peer trusting it
	// accepts what it signs (covered end to end in pkg/bridge).
	if _, err := bridgeRelayAuthFromConfig(config.BridgeRelayConfig{SignerKeyPath: path}, key.GetPublicKey()); err == nil {
		t.Fatal("consensus key accepted as bridge signer")
	}
}

func TestBridgeRelayMetrics(t *testing.T) {
	ms := bridgeRelayMetrics(bridge.RelayStats{Applied: 2, NoTrustedKeys: 7, BadSignature: 1}, 3, true)
	got := map[string]float64{}
	for _, m := range ms {
		got[m.Name+"/"+m.Labels["result"]] = m.Value
	}
	want := map[string]float64{
		"qsdm_bridge_relay_remote_events_total/applied":         2,
		"qsdm_bridge_relay_remote_events_total/no_trusted_keys": 7,
		"qsdm_bridge_relay_remote_events_total/bad_signature":   1,
		"qsdm_bridge_relay_remote_events_total/unsigned":        0,
		"qsdm_bridge_relay_trusted_keys/":                       3,
		"qsdm_bridge_relay_signing/":                            1,
	}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			t.Errorf("%s = %v (present %v), want %v", k, g, ok, v)
		}
	}
	if len(ms) != 11 {
		t.Fatalf("metric count = %d, want 11", len(ms))
	}
	if bridgeRelayMetricsCollector(nil)() != nil {
		t.Fatal("nil relay must export nothing")
	}
}
