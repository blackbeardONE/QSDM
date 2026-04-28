package main

// gov_helper.go — offline assembly + inspection of `qsdm/gov/v1`
// parameter-tuning transactions. Mirrors slash_helper.go in
// shape and split for the same reason: governance authorities
// often run from air-gapped hosts, so the CLI MUST be able to
// produce ready-to-sign payloads without ever touching the
// network.
//
// Subcommands:
//
//   - `qsdmcli gov-helper propose-param` builds a canonical
//     ParamSetPayload and writes the JSON to stdout/file.
//     Performs every client-side check the chain runs at
//     admission so an authority sees rejection causes locally.
//
//   - `qsdmcli gov-helper params` lists the currently-known
//     governance-tunable parameters with bounds and defaults,
//     sourced from chainparams.Registry. Useful to confirm
//     "what can I propose changes to?" without consulting
//     external docs.
//
//   - `qsdmcli gov-helper inspect` decodes a previously-built
//     payload and pretty-prints it. Symmetric to
//     slash-helper inspect.
//
// Out of scope:
//
//   - On-chain submission. The produced payload is consumed by
//     whatever signing pipeline the authority has (multisig
//     orchestrator, hardware wallet, etc.); the existing
//     `qsdmcli tx` path with --contract-id=qsdm/gov/v1 will
//     submit a signed envelope. Runtime listings of pending /
//     active values via HTTP belong in a follow-on commit
//     once `/api/v1/governance/params` lands.

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/blackbeardONE/QSDM/pkg/governance/chainparams"
)

// govHelper dispatches `qsdmcli gov-helper <sub> [flags]`.
func (c *CLI) govHelper(args []string) error {
	if len(args) < 1 {
		return errors.New(
			"usage: qsdmcli gov-helper <sub> [flags]\n  sub ∈ {propose-param, params, inspect}")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "propose-param":
		return c.govHelperProposeParam(rest)
	case "params":
		return c.govHelperListParams(rest)
	case "inspect":
		return c.govHelperInspect(rest)
	default:
		return fmt.Errorf(
			"unknown gov-helper subcommand %q (want propose-param | params | inspect)", sub)
	}
}

// -----------------------------------------------------------------------------
// gov-helper propose-param
// -----------------------------------------------------------------------------

// govHelperProposeParam constructs a ParamSetPayload from
// command-line flags, validates it against the chainparams
// registry, and writes the encoded JSON to --out (default
// stdout).
//
// Sanity checks performed BEFORE encoding:
//
//   - --param is a registered parameter (rejects unknown).
//   - --value is within the registry's (Min, Max) bounds.
//   - --effective-height is positive (chain admission also
//     rejects 0; we mirror locally so the authority sees the
//     error before submission).
//   - --memo length cap.
//
// The chain-side `effective_height >= currentHeight` and
// `effective_height <= currentHeight + MaxActivationDelay`
// rules cannot be checked here (we don't know currentHeight
// offline); they fire at applier time and are documented in
// the printed output so the operator picks a sensible value.
func (c *CLI) govHelperProposeParam(args []string) error {
	fs := flag.NewFlagSet("gov-helper propose-param", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		paramName       = fs.String("param", "", "registered governance parameter name (required; see `qsdmcli gov-helper params`)")
		value           = fs.Uint64("value", 0, "proposed new value (required; bounds depend on --param)")
		effectiveHeight = fs.Uint64("effective-height", 0, "chain block height at which the change becomes active (required; must be ≥ currentHeight at submission)")
		memo            = fs.String("memo", "", "optional human-readable memo (≤256 bytes)")
		out             = fs.String("out", "-", "output path for the encoded payload ('-' for stdout)")
		printCmd        = fs.Bool("print-cmd", false, "after writing, print a placeholder `qsdmcli tx` invocation to stderr")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *paramName == "" {
		fs.Usage()
		return errors.New("--param is required (registered names: " +
			strings.Join(chainparams.Names(), ", ") + ")")
	}
	if *effectiveHeight == 0 {
		fs.Usage()
		return errors.New("--effective-height is required and must be positive")
	}

	spec, ok := chainparams.Lookup(*paramName)
	if !ok {
		return fmt.Errorf(
			"--param=%q is not a registered governance parameter (known: %s)",
			*paramName, strings.Join(chainparams.Names(), ", "))
	}
	if err := spec.CheckBounds(*value); err != nil {
		return fmt.Errorf("--value rejected by registry: %w", err)
	}
	if len(*memo) > chainparams.MaxMemoLen {
		return fmt.Errorf("--memo exceeds %d bytes (got %d)",
			chainparams.MaxMemoLen, len(*memo))
	}

	payload := chainparams.ParamSetPayload{
		Kind:            chainparams.PayloadKindParamSet,
		Param:           *paramName,
		Value:           *value,
		EffectiveHeight: *effectiveHeight,
		Memo:            *memo,
	}
	blob, err := chainparams.EncodeParamSet(payload)
	if err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}
	// Round-trip guard.
	if _, err := chainparams.ParseParamSet(blob); err != nil {
		return fmt.Errorf("encoder produced bytes that fail Parse round-trip: %w", err)
	}

	if err := writeBytes(*out, blob); err != nil {
		return fmt.Errorf("write payload: %w", err)
	}

	fmt.Fprintf(os.Stderr,
		"gov param-set payload: %d bytes, param=%q value=%d effective_height=%d memo=%dB\n",
		len(blob), *paramName, *value, *effectiveHeight, len(*memo))
	fmt.Fprintf(os.Stderr,
		"note: chain-side acceptance still requires (a) sender on AuthorityList, "+
			"(b) effective_height ≥ currentHeight at submission, "+
			"(c) effective_height ≤ currentHeight + %d blocks (~%dh).\n",
		chainparams.MaxActivationDelay,
		chainparams.MaxActivationDelay*3/3600)

	if *printCmd {
		fmt.Fprintln(os.Stderr,
			"submit via your signed-tx pipeline with ContractID=qsdm/gov/v1 and Payload=<bytes-above>")
		fmt.Fprintf(os.Stderr,
			"# example: qsdmcli tx <authority-addr> <validator> 0 --contract-id=%s --payload-file=%s\n",
			chainparams.ContractID, resolveOutPath(*out))
	}
	return nil
}

// -----------------------------------------------------------------------------
// gov-helper params (registry listing)
// -----------------------------------------------------------------------------

func (c *CLI) govHelperListParams(args []string) error {
	fs := flag.NewFlagSet("gov-helper params", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit registry as JSON (one object per param)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	specs := chainparams.Registry()
	if *asJSON {
		out, err := json.MarshalIndent(specs, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal registry: %w", err)
		}
		fmt.Println(string(out))
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PARAM\tDEFAULT\tMIN\tMAX\tUNIT\tDESCRIPTION")
	for _, s := range specs {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%s\t%s\n",
			s.Name, s.DefaultValue, s.MinValue, s.MaxValue, s.Unit, s.Description)
	}
	return tw.Flush()
}

// -----------------------------------------------------------------------------
// gov-helper inspect
// -----------------------------------------------------------------------------

// govHelperInspectView is the human-readable wire shape printed
// by the inspect subcommand.
type govHelperInspectView struct {
	Kind            string                 `json:"kind"`
	Param           string                 `json:"param"`
	Value           uint64                 `json:"value"`
	EffectiveHeight uint64                 `json:"effective_height"`
	Memo            string                 `json:"memo,omitempty"`
	SizeBytes       int                    `json:"size_bytes"`
	RegistryEntry   *chainparams.ParamSpec `json:"registry_entry,omitempty"`
}

func (c *CLI) govHelperInspect(args []string) error {
	fs := flag.NewFlagSet("gov-helper inspect", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		payloadFile = fs.String("payload-file", "", "path to the encoded payload ('-' for stdin)")
		payloadHex  = fs.String("payload-hex", "", "hex-encoded payload bytes")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *payloadFile == "" && *payloadHex == "" {
		fs.Usage()
		return errors.New("provide one of --payload-file or --payload-hex")
	}

	var blob []byte
	switch {
	case *payloadFile != "":
		var err error
		if *payloadFile == "-" {
			blob, err = io.ReadAll(os.Stdin)
		} else {
			blob, err = os.ReadFile(*payloadFile)
		}
		if err != nil {
			return fmt.Errorf("read payload: %w", err)
		}
	case *payloadHex != "":
		s := strings.TrimSpace(*payloadHex)
		decoded, err := hexDecode(s)
		if err != nil {
			return fmt.Errorf("decode payload-hex: %w", err)
		}
		blob = decoded
	}

	parsed, err := chainparams.ParseParamSet(blob)
	if err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}
	view := govHelperInspectView{
		Kind:            string(parsed.Kind),
		Param:           parsed.Param,
		Value:           parsed.Value,
		EffectiveHeight: parsed.EffectiveHeight,
		Memo:            parsed.Memo,
		SizeBytes:       len(blob),
	}
	if spec, ok := chainparams.Lookup(parsed.Param); ok {
		view.RegistryEntry = &spec
	}
	out, err := json.MarshalIndent(view, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal inspect view: %w", err)
	}
	fmt.Println(string(out))
	return nil
}

// -----------------------------------------------------------------------------
// shared utilities
// -----------------------------------------------------------------------------

// writeBytes writes `b` to `path`. Path "-" routes to stdout
// followed by a newline so the bytes are easy to pipe.
func writeBytes(path string, b []byte) error {
	if path == "-" {
		if _, err := os.Stdout.Write(b); err != nil {
			return err
		}
		_, err := os.Stdout.Write([]byte{'\n'})
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// resolveOutPath returns "stdin" for "-" and the literal path
// otherwise. Used in the printed example so a piped invocation
// reads sensibly.
func resolveOutPath(p string) string {
	if p == "-" {
		return "<paste-bytes-here>"
	}
	return p
}

// hexDecode tolerates both "0x"-prefixed and bare hex strings.
// Reused by the inspect subcommand.
func hexDecode(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	if len(s) == 0 {
		return nil, errors.New("empty hex")
	}
	if len(s)%2 != 0 {
		return nil, errors.New("odd-length hex")
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		hi, err := hexNibble(s[i*2])
		if err != nil {
			return nil, err
		}
		lo, err := hexNibble(s[i*2+1])
		if err != nil {
			return nil, err
		}
		out[i] = hi<<4 | lo
	}
	return out, nil
}

func hexNibble(b byte) (byte, error) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', nil
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, nil
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, nil
	}
	return 0, fmt.Errorf("invalid hex byte %q", b)
}

// formatUint64 mirrors strconv.FormatUint without importing
// the package twice across this file (kept minimal).
func formatUint64(v uint64) string {
	return strconv.FormatUint(v, 10)
}

// (silence unused-import warning if any)
var _ = bytes.NewReader
var _ = formatUint64
