package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultSettingsAreValid(t *testing.T) {
	if err := validateSettings(defaultSettings()); err != nil {
		t.Fatal(err)
	}
}

func TestRelaySettingsIgnoreDormantAgentResourceSelection(t *testing.T) {
	settings := defaultSettings()
	settings.Role = "relay"
	settings.Agent.CPU = false
	settings.Agent.GPU = false
	settings.Agent.RAM = false
	if err := validateSettings(settings); err != nil {
		t.Fatalf("Relay settings should not require Agent resources: %v", err)
	}
}

func TestSettingsPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	settings := defaultSettings()
	settings.Agent.CPUShare = 37
	if err := saveSettings(path, settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Agent.CPUShare != 37 {
		t.Fatalf("CPU share = %d, want 37", loaded.Agent.CPUShare)
	}
	if runtime.GOOS != "windows" {
		if runtimeMode := fileMode(path); runtimeMode&0o077 != 0 {
			t.Fatalf("settings permissions are too broad: %o", runtimeMode)
		}
	}
}

func TestLANRelayRequiresReachableAddress(t *testing.T) {
	settings := defaultSettings()
	settings.Role = "relay"
	settings.Relay.AllowLAN = true
	settings.Relay.AdvertisedURL = "http://0.0.0.0:7740"
	if err := validateSettings(settings); err == nil {
		t.Fatal("expected wildcard advertised address to fail")
	}
}

func fileMode(path string) os.FileMode {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Mode().Perm()
}
