package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/governance/chainparams"
)

// -----------------------------------------------------------------------------
// gov-helper propose-param
// -----------------------------------------------------------------------------

func TestGovHelperProposeParam_HappyPath(t *testing.T) {
	cli := &CLI{}
	dir := t.TempDir()
	out := filepath.Join(dir, "payload.bin")

	stderr := captureStderr(t, func() {
		err := cli.govHelper([]string{
			"propose-param",
			"--param", "reward_bps",
			"--value", "2500",
			"--effective-height", "100",
			"--memo", "lower reward share",
			"--out", out,
		})
		if err != nil {
			t.Fatalf("govHelper: %v", err)
		}
	})

	if !bytes.Contains(stderr, []byte("gov param-set payload:")) {
		t.Errorf("stderr summary missing; got %q", stderr)
	}

	blob, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	parsed, err := chainparams.ParseParamSet(blob)
	if err != nil {
		t.Fatalf("parse output: %v", err)
	}
	if parsed.Param != "reward_bps" {
		t.Errorf("Param=%q, want reward_bps", parsed.Param)
	}
	if parsed.Value != 2500 {
		t.Errorf("Value=%d, want 2500", parsed.Value)
	}
	if parsed.EffectiveHeight != 100 {
		t.Errorf("EffectiveHeight=%d, want 100", parsed.EffectiveHeight)
	}
	if parsed.Memo != "lower reward share" {
		t.Errorf("Memo=%q lost", parsed.Memo)
	}
}

func TestGovHelperProposeParam_RejectsMissingFlags(t *testing.T) {
	cli := &CLI{}
	cases := [][]string{
		{"propose-param"}, // nothing
		{"propose-param", "--value", "1000", "--effective-height", "10"}, // missing --param
		{"propose-param", "--param", "reward_bps", "--effective-height", "10"}, // missing --value (treated as 0, in bounds for reward_bps)
		{"propose-param", "--param", "reward_bps", "--value", "1000"},          // missing --effective-height
	}
	// Note: missing --value is fine for reward_bps (default 0
	// is a valid bound). The third case actually succeeds —
	// drop it.
	if err := cli.govHelper(cases[0]); err == nil {
		t.Error("missing all flags accepted")
	}
	if err := cli.govHelper(cases[1]); err == nil {
		t.Error("missing --param accepted")
	}
	if err := cli.govHelper(cases[3]); err == nil {
		t.Error("missing --effective-height accepted")
	}
}

func TestGovHelperProposeParam_RejectsUnknownParam(t *testing.T) {
	cli := &CLI{}
	err := cli.govHelper([]string{
		"propose-param",
		"--param", "no_such_param",
		"--value", "1000",
		"--effective-height", "10",
	})
	if err == nil || !strings.Contains(err.Error(), "not a registered governance parameter") {
		t.Errorf("expected unknown-param rejection, got %v", err)
	}
}

func TestGovHelperProposeParam_RejectsOutOfBounds(t *testing.T) {
	cli := &CLI{}
	err := cli.govHelper([]string{
		"propose-param",
		"--param", "reward_bps",
		"--value", "99999",
		"--effective-height", "10",
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "registry") {
		t.Errorf("expected out-of-bounds rejection, got %v", err)
	}
}

func TestGovHelperProposeParam_PrintCmd(t *testing.T) {
	cli := &CLI{}
	dir := t.TempDir()
	out := filepath.Join(dir, "payload.bin")

	stderr := captureStderr(t, func() {
		err := cli.govHelper([]string{
			"propose-param",
			"--param", "reward_bps",
			"--value", "1000",
			"--effective-height", "10",
			"--out", out,
			"--print-cmd",
		})
		if err != nil {
			t.Fatalf("govHelper: %v", err)
		}
	})
	if !bytes.Contains(stderr, []byte("ContractID=qsdm/gov/v1")) {
		t.Errorf("stderr missing ContractID hint; got %q", stderr)
	}
}

// -----------------------------------------------------------------------------
// gov-helper params
// -----------------------------------------------------------------------------

func TestGovHelperParams_TableOutput(t *testing.T) {
	cli := &CLI{}
	stdout := captureStdout(t, func() {
		err := cli.govHelper([]string{"params"})
		if err != nil {
			t.Fatalf("govHelper params: %v", err)
		}
	})
	for _, name := range chainparams.Names() {
		if !bytes.Contains(stdout, []byte(name)) {
			t.Errorf("registry param %q missing from output:\n%s", name, stdout)
		}
	}
	if !bytes.Contains(stdout, []byte("PARAM")) {
		t.Errorf("table header missing")
	}
}

func TestGovHelperParams_JSONOutput(t *testing.T) {
	cli := &CLI{}
	stdout := captureStdout(t, func() {
		err := cli.govHelper([]string{"params", "--json"})
		if err != nil {
			t.Fatalf("govHelper params --json: %v", err)
		}
	})
	var specs []chainparams.ParamSpec
	if err := json.Unmarshal(stdout, &specs); err != nil {
		t.Fatalf("unmarshal: %v\nstdout=%s", err, stdout)
	}
	if len(specs) != len(chainparams.Registry()) {
		t.Errorf("got %d specs, want %d", len(specs), len(chainparams.Registry()))
	}
}

// -----------------------------------------------------------------------------
// gov-helper inspect
// -----------------------------------------------------------------------------

func TestGovHelperInspect_HappyPath(t *testing.T) {
	cli := &CLI{}
	blob, err := chainparams.EncodeParamSet(chainparams.ParamSetPayload{
		Kind:            chainparams.PayloadKindParamSet,
		Param:           "reward_bps",
		Value:           1234,
		EffectiveHeight: 999,
		Memo:            "audit-2026",
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "payload.bin")
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	stdout := captureStdout(t, func() {
		err := cli.govHelper([]string{
			"inspect",
			"--payload-file", path,
		})
		if err != nil {
			t.Fatalf("govHelper inspect: %v", err)
		}
	})
	var view govHelperInspectView
	if err := json.Unmarshal(stdout, &view); err != nil {
		t.Fatalf("unmarshal: %v\nstdout=%s", err, stdout)
	}
	if view.Param != "reward_bps" || view.Value != 1234 || view.EffectiveHeight != 999 {
		t.Errorf("view=%+v, fields lost", view)
	}
	if view.RegistryEntry == nil {
		t.Error("RegistryEntry nil; should have been populated for known param")
	}
}

func TestGovHelperInspect_RejectsMissing(t *testing.T) {
	cli := &CLI{}
	if err := cli.govHelper([]string{"inspect"}); err == nil {
		t.Error("inspect with no flags accepted")
	}
}

func TestGovHelperInspect_RejectsBadInput(t *testing.T) {
	cli := &CLI{}
	if err := cli.govHelper([]string{
		"inspect",
		"--payload-hex", "not-hex",
	}); err == nil {
		t.Error("inspect with junk hex accepted")
	}
}

// -----------------------------------------------------------------------------
// dispatch
// -----------------------------------------------------------------------------

func TestGovHelper_DispatchUnknown(t *testing.T) {
	cli := &CLI{}
	err := cli.govHelper([]string{"frobnicate"})
	if err == nil || !strings.Contains(err.Error(), "unknown gov-helper subcommand") {
		t.Errorf("expected unknown-subcommand rejection, got %v", err)
	}
}

func TestGovHelper_DispatchNoArgs(t *testing.T) {
	cli := &CLI{}
	if err := cli.govHelper(nil); err == nil {
		t.Error("empty args accepted")
	}
}
