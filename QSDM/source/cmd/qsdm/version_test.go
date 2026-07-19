package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/buildinfo"
)

func TestPrintVersionIfRequested(t *testing.T) {
	originalVersion, originalSHA, originalDate := buildinfo.Version, buildinfo.GitSHA, buildinfo.BuildDate
	buildinfo.Version = "v0.4.7-rc.test"
	buildinfo.GitSHA = "abc1234"
	buildinfo.BuildDate = "2026-07-20T00:00:00Z"
	t.Cleanup(func() {
		buildinfo.Version, buildinfo.GitSHA, buildinfo.BuildDate = originalVersion, originalSHA, originalDate
	})

	for _, arg := range []string{"--version", "-version", "version", " VERSION "} {
		t.Run(strings.TrimSpace(arg), func(t *testing.T) {
			var out bytes.Buffer
			if !printVersionIfRequested([]string{arg}, &out) {
				t.Fatalf("printVersionIfRequested(%q) = false, want true", arg)
			}
			for _, want := range []string{"qsdm", "v0.4.7-rc.test", "abc1234", "2026-07-20T00:00:00Z"} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("version output %q does not contain %q", out.String(), want)
				}
			}
		})
	}
}

func TestPrintVersionIfRequestedRejectsStartupArguments(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{},
		{"--config", "config.toml"},
		{"--version", "extra"},
		{"--help"},
	} {
		var out bytes.Buffer
		if printVersionIfRequested(args, &out) {
			t.Errorf("printVersionIfRequested(%q) = true, want false", args)
		}
		if out.Len() != 0 {
			t.Errorf("printVersionIfRequested(%q) wrote %q", args, out.String())
		}
	}
}
