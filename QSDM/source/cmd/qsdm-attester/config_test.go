package main

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSignerID_DerivesFromKeyByDefault(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	id := resolveSignerID(key, "")
	wantPrefix := signerIDPrefix
	if !strings.HasPrefix(id, wantPrefix) {
		t.Fatalf("signer id %q missing %q prefix", id, wantPrefix)
	}
	wantHex := hex.EncodeToString(key[:8])
	if !strings.HasSuffix(id, wantHex) {
		t.Fatalf("signer id %q does not end in expected hex %q", id, wantHex)
	}
}

func TestResolveSignerID_OverrideIsReturnedVerbatim(t *testing.T) {
	key := []byte("01234567ABCDEFGH01234567ABCDEFGH")
	got := resolveSignerID(key, "attester-blackbeard")
	if got != "attester-blackbeard" {
		t.Fatalf("override not honoured: got %q want %q", got, "attester-blackbeard")
	}
}

func TestResolveSignerID_ShortKeyFallsBack(t *testing.T) {
	got := resolveSignerID([]byte("abc"), "")
	if got != signerIDPrefix+"shortkey" {
		t.Fatalf("short-key fallback wrong: got %q", got)
	}
}

func TestKeyFingerprint_IsStableAndNonEmpty(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	fp1 := keyFingerprint(key)
	fp2 := keyFingerprint(key)
	if fp1 != fp2 {
		t.Fatalf("fingerprint not stable: %q vs %q", fp1, fp2)
	}
	if fp1 == "" {
		t.Fatalf("fingerprint is empty for non-empty key")
	}
	if len(fp1) != 16 {
		t.Fatalf("fingerprint length %d != 16 hex chars", len(fp1))
	}
}

func TestKeyFingerprint_EmptyKeyReturnsEmpty(t *testing.T) {
	if got := keyFingerprint(nil); got != "" {
		t.Fatalf("empty key fingerprint = %q want empty", got)
	}
}

func TestLoadOrCreateSignerKey_FreshCreatesFileWithMode(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "sub", "attester.key")

	id, key, fresh, err := loadOrCreateSignerKey(keyPath, "")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !fresh {
		t.Fatalf("expected fresh=true on first call")
	}
	if len(key) != signerKeyLen {
		t.Fatalf("fresh key length %d != %d", len(key), signerKeyLen)
	}
	if !strings.HasPrefix(id, signerIDPrefix) {
		t.Fatalf("id %q missing prefix %q", id, signerIDPrefix)
	}
	st, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat after create: %v", err)
	}
	if st.Size() != signerKeyLen {
		t.Fatalf("on-disk size %d != %d", st.Size(), signerKeyLen)
	}
}

func TestLoadOrCreateSignerKey_SecondCallLoadsSameKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "attester.key")
	id1, key1, fresh1, err := loadOrCreateSignerKey(keyPath, "")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !fresh1 {
		t.Fatalf("first call should be fresh")
	}
	id2, key2, fresh2, err := loadOrCreateSignerKey(keyPath, "")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if fresh2 {
		t.Fatalf("second call should NOT be fresh")
	}
	if id1 != id2 {
		t.Fatalf("signer id changed across reloads: %q vs %q", id1, id2)
	}
	if string(key1) != string(key2) {
		t.Fatalf("key bytes changed across reloads")
	}
}

func TestLoadOrCreateSignerKey_RejectsWrongLengthFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "attester.key")
	if err := os.WriteFile(keyPath, []byte("too-short"), 0o600); err != nil {
		t.Fatalf("seed wrong-length file: %v", err)
	}
	_, _, _, err := loadOrCreateSignerKey(keyPath, "")
	if err == nil {
		t.Fatalf("expected error on wrong-length key file")
	}
	if !strings.Contains(err.Error(), "expected") {
		t.Fatalf("error %q missing 'expected' hint", err)
	}
}

func TestLoadOrCreateSignerKey_EmptyPathErrors(t *testing.T) {
	_, _, _, err := loadOrCreateSignerKey("", "")
	if err == nil {
		t.Fatalf("expected error on empty path")
	}
}

func TestLoadConfig_AppliesEnvOverrides(t *testing.T) {
	t.Setenv("QSDM_ATTESTER_LISTEN", ":9999")
	t.Setenv("QSDM_ATTESTER_NOTE", "test-host")
	t.Setenv("QSDM_ATTESTER_LOG_EVERY", "10")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.ListenAddr != ":9999" {
		t.Fatalf("ListenAddr = %q want :9999", cfg.ListenAddr)
	}
	if cfg.Note != "test-host" {
		t.Fatalf("Note = %q want test-host", cfg.Note)
	}
	if cfg.LogIssuanceEvery != 10 {
		t.Fatalf("LogIssuanceEvery = %d want 10", cfg.LogIssuanceEvery)
	}
	if cfg.KeyPath == "" {
		t.Fatalf("KeyPath should default to a non-empty path even when env is unset")
	}
}

func TestLoadConfig_DefaultsListenAddr(t *testing.T) {
	t.Setenv("QSDM_ATTESTER_LISTEN", "")
	t.Setenv("QSDM_ATTESTER_KEY_PATH", "")
	t.Setenv("QSDM_ATTESTER_NOTE", "")
	t.Setenv("QSDM_ATTESTER_LOG_EVERY", "")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.ListenAddr != ":7733" {
		t.Fatalf("default ListenAddr = %q want :7733", cfg.ListenAddr)
	}
}

func TestHexKey_RoundTrip(t *testing.T) {
	in := []byte{0x00, 0x01, 0xff, 0xab}
	got := hexKey(in)
	if got != "0001ffab" {
		t.Fatalf("hexKey = %q want 0001ffab", got)
	}
}
