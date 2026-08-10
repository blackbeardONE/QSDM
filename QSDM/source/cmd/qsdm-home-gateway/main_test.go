package main

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/buildinfo"
)

func TestGatewayVersionUsesReleaseBuildInfo(t *testing.T) {
	originalVersion, originalSHA, originalDate := buildinfo.Version, buildinfo.GitSHA, buildinfo.BuildDate
	buildinfo.Version = "v0.4.7-rc.test"
	buildinfo.GitSHA = "abcdef0"
	buildinfo.BuildDate = "2026-07-30T00:00:00Z"
	t.Cleanup(func() {
		buildinfo.Version, buildinfo.GitSHA, buildinfo.BuildDate = originalVersion, originalSHA, originalDate
	})

	got := gatewayVersion()
	for _, want := range []string{"qsdm-home-gateway", buildinfo.Version, buildinfo.GitSHA, buildinfo.BuildDate} {
		if !strings.Contains(got, want) {
			t.Fatalf("gatewayVersion() = %q, want it to contain %q", got, want)
		}
	}
}

func TestLoadGatewayKeyFromProtectedFile(t *testing.T) {
	want := bytes.Repeat([]byte{0x5a}, 32)
	path := filepath.Join(t.TempDir(), "gateway.key")
	if err := os.WriteFile(path, []byte(hex.EncodeToString(want)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := loadGatewayKey(path, "")
	if err != nil {
		t.Fatalf("loadGatewayKey() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("loadGatewayKey() = %x, want %x", got, want)
	}
}

func TestLoadGatewayKeyLegacyInlineCompatibility(t *testing.T) {
	want := bytes.Repeat([]byte{0x3c}, 32)
	got, err := loadGatewayKey("", hex.EncodeToString(want))
	if err != nil {
		t.Fatalf("loadGatewayKey() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("loadGatewayKey() = %x, want %x", got, want)
	}
}

func TestLoadGatewayKeyRejectsAmbiguousSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.key")
	if err := os.WriteFile(path, []byte(strings.Repeat("ab", 32)), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := loadGatewayKey(path, strings.Repeat("cd", 32)); err == nil {
		t.Fatal("loadGatewayKey() accepted both key sources")
	}
}

func TestLoadGatewayKeyRejectsMissingMalformedAndShortFiles(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "malformed", content: "not-hex"},
		{name: "short", content: strings.Repeat("ab", 15)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "gateway.key")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadGatewayKey(path, ""); err == nil {
				t.Fatal("loadGatewayKey() accepted an invalid key file")
			}
		})
	}

	if _, err := loadGatewayKey(filepath.Join(t.TempDir(), "missing.key"), ""); err == nil {
		t.Fatal("loadGatewayKey() accepted a missing key file")
	}
}

func TestLoadGatewayKeyDoesNotEchoSecretInErrors(t *testing.T) {
	secret := "definitely-not-valid-hex-secret"
	_, err := loadGatewayKey("", secret)
	if err == nil {
		t.Fatal("loadGatewayKey() accepted malformed inline key")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked key material: %v", err)
	}
}

func TestLoadGatewayKeyRejectsOpenUnixPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows permissions are restricted by the launcher ACL")
	}
	path := filepath.Join(t.TempDir(), "gateway.key")
	if err := os.WriteFile(path, []byte(strings.Repeat("ab", 32)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadGatewayKey(path, ""); err == nil {
		t.Fatal("loadGatewayKey() accepted group/world-readable key file")
	}
}
