package main

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSlotsFile_MissingReturnsEmpty(t *testing.T) {
	got, err := LoadSlotsFile(filepath.Join(t.TempDir(), "does-not-exist.toml"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d entries, want 0", len(got))
	}
}

func TestLoadSlotsFile_EmptyPathReturnsEmpty(t *testing.T) {
	got, err := LoadSlotsFile("")
	if err != nil {
		t.Fatalf("empty path should not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d entries, want 0", len(got))
	}
}

func TestLoadSlotsFile_ParsesValidEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "slots.toml")
	contents := `
[[slot]]
slot_id = "alice-3050"
key_hex = "` + strings.Repeat("ab", 32) + `"
note    = "alice's home GPU"

[[slot]]
slot_id = "bob.h100"
key_hex = "` + strings.Repeat("cd", 32) + `"
note    = "bob datacenter"
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	entries, err := LoadSlotsFile(path)
	if err != nil {
		t.Fatalf("LoadSlotsFile: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].SlotID != "alice-3050" || entries[1].SlotID != "bob.h100" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
}

func TestLoadSlotsFile_RejectsMalformedTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(path, []byte("this is = = not toml"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadSlotsFile(path); err == nil {
		t.Fatalf("expected error on malformed TOML")
	}
}

func TestBuildAuthMap_HappyPath(t *testing.T) {
	keyA := strings.Repeat("ab", 32)
	keyB := strings.Repeat("cd", 32)
	auth, errs := BuildAuthMap([]SlotEntry{
		{SlotID: "alice", KeyHex: keyA, Note: "alice"},
		{SlotID: "bob", KeyHex: keyB, Note: "bob"},
	})
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(auth) != 2 {
		t.Fatalf("auth len = %d", len(auth))
	}
	wantA, _ := hex.DecodeString(keyA)
	if string(auth["alice"].Key) != string(wantA) {
		t.Errorf("alice key mismatch")
	}
	if auth["alice"].Note != "alice" {
		t.Errorf("alice note %q", auth["alice"].Note)
	}
}

func TestBuildAuthMap_RejectsBadEntries(t *testing.T) {
	cases := []struct {
		name string
		in   SlotEntry
	}{
		{"empty slot_id", SlotEntry{SlotID: "", KeyHex: strings.Repeat("ab", 16)}},
		{"bad slot chars", SlotEntry{SlotID: "bad/slot", KeyHex: strings.Repeat("ab", 16)}},
		{"empty key", SlotEntry{SlotID: "ok", KeyHex: ""}},
		{"non-hex key", SlotEntry{SlotID: "ok", KeyHex: "not-hex-zz"}},
		{"short key", SlotEntry{SlotID: "ok", KeyHex: strings.Repeat("ab", 8)}}, // 16 hex = 8 bytes
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auth, errs := BuildAuthMap([]SlotEntry{tc.in})
			if len(errs) == 0 {
				t.Fatalf("expected error for %s", tc.name)
			}
			if len(auth) != 0 {
				t.Fatalf("expected empty auth, got %v", auth)
			}
		})
	}
}

func TestBuildAuthMap_RejectsDuplicate(t *testing.T) {
	key := strings.Repeat("ab", 32)
	_, errs := BuildAuthMap([]SlotEntry{
		{SlotID: "alice", KeyHex: key},
		{SlotID: "alice", KeyHex: key},
	})
	if len(errs) != 1 {
		t.Fatalf("got %d errs, want 1: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Error(), "duplicate") {
		t.Errorf("error %q does not mention duplicate", errs[0])
	}
}

func TestConfig_DefaultsApplied(t *testing.T) {
	c := &Config{}
	c.applyDefaults()
	if c.SlotsPath == "" {
		t.Errorf("SlotsPath empty after defaults")
	}
	if c.TunnelListenAddr == "" || c.ProxyListenAddr == "" || c.MetricsListenAddr == "" {
		t.Errorf("listen addrs empty after defaults: %+v", c)
	}
}

func TestLoadConfig_HonoursEnvOverride(t *testing.T) {
	t.Setenv("QSDM_RELAY_SLOTS_PATH", "/custom/path/slots.toml")
	t.Setenv("QSDM_RELAY_TUNNEL_LISTEN", ":17700")
	c := loadConfig()
	if c.SlotsPath != "/custom/path/slots.toml" {
		t.Errorf("SlotsPath = %q", c.SlotsPath)
	}
	if c.TunnelListenAddr != ":17700" {
		t.Errorf("TunnelListenAddr = %q", c.TunnelListenAddr)
	}
}
