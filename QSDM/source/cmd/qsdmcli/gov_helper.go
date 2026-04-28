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
			"usage: qsdmcli gov-helper <sub> [flags]\n" +
				"  sub ∈ {propose-param, propose-authority, params, inspect}")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "propose-param":
		return c.govHelperProposeParam(rest)
	case "propose-authority":
		return c.govHelperProposeAuthority(rest)
	case "params":
		return c.govHelperListParams(rest)
	case "inspect":
		return c.govHelperInspect(rest)
	default:
		return fmt.Errorf(
			"unknown gov-helper subcommand %q (want propose-param | propose-authority | params | inspect)", sub)
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

// govHelperListParams renders the chainparams registry as a
// table or JSON. When --remote is passed, the offline registry
// is merged with the running validator's
// `/api/v1/governance/params` snapshot, surfacing live `active`
// values and any pending changes alongside the static bounds /
// defaults.
//
// --remote is best-effort: a 503 (v1-only node) or transport
// error degrades gracefully to an offline-only view with a
// stderr warning, so an authority running this against an
// unreachable validator still gets the registry table they need
// to author a proposal.
func (c *CLI) govHelperListParams(args []string) error {
	fs := flag.NewFlagSet("gov-helper params", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit registry as JSON (one object per param)")
	remote := fs.Bool("remote", false,
		"query the validator at $QSDM_API_URL and merge live active/pending values into the table")
	if err := fs.Parse(args); err != nil {
		return err
	}

	specs := chainparams.Registry()

	// Best-effort live lookup. live==nil means we render the
	// registry-only view; live!=nil means we merge values.
	var live *govParamsRemoteView
	if *remote {
		v, err := c.fetchGovernanceParams()
		if err != nil {
			fmt.Fprintf(os.Stderr,
				"warning: --remote query failed: %v (falling back to offline registry view)\n",
				err)
		} else {
			live = v
		}
	}

	if *asJSON {
		// JSON path: with --remote we emit the validator's
		// snapshot verbatim if available (that's what dashboards
		// consume); without --remote we emit the static
		// registry, identical to the prior behaviour.
		if live != nil {
			out, err := json.MarshalIndent(live, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal remote view: %w", err)
			}
			fmt.Println(string(out))
			return nil
		}
		out, err := json.MarshalIndent(specs, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal registry: %w", err)
		}
		fmt.Println(string(out))
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if live != nil {
		// Live merge: ACTIVE column shows the on-chain value,
		// PENDING column collapses to a one-line summary
		// "value@height" when a change is staged. Empty PENDING
		// cell means no pending change.
		fmt.Fprintln(tw, "PARAM\tACTIVE\tPENDING\tDEFAULT\tMIN\tMAX\tUNIT\tDESCRIPTION")
		for _, s := range specs {
			active := "—"
			if v, ok := live.Active[string(s.Name)]; ok {
				active = strconv.FormatUint(v, 10)
			}
			pending := ""
			for _, p := range live.Pending {
				if p.Param == string(s.Name) {
					pending = fmt.Sprintf("%d@H+%d", p.Value, p.EffectiveHeight)
					break
				}
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%s\t%s\n",
				s.Name, active, pending, s.DefaultValue,
				s.MinValue, s.MaxValue, s.Unit, s.Description)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		// Footer with authority list + governance-enabled flag.
		// Helps the operator confirm "yes, I'm one of the
		// authorities, my proposal will admit" before they
		// build a payload.
		fmt.Fprintf(os.Stderr,
			"governance_enabled=%t  authorities=%s\n",
			live.GovernanceEnabled,
			strings.Join(live.Authorities, ","))
		return nil
	}

	fmt.Fprintln(tw, "PARAM\tDEFAULT\tMIN\tMAX\tUNIT\tDESCRIPTION")
	for _, s := range specs {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%s\t%s\n",
			s.Name, s.DefaultValue, s.MinValue, s.MaxValue, s.Unit, s.Description)
	}
	return tw.Flush()
}

// govParamsRemoteView mirrors api.GovernanceParamsView. Kept
// local to avoid pulling pkg/api into the CLI binary (same
// posture as watch.go's watchRecord and watch_slashes.go's
// slashReceiptWire). JSON tags MUST stay byte-identical to the
// canonical view; tests pin this.
type govParamsRemoteView struct {
	Active            map[string]uint64           `json:"active"`
	Pending           []govParamsPendingWire      `json:"pending"`
	Registry          []govParamsRegistryWire     `json:"registry"`
	Authorities       []string                    `json:"authorities"`
	GovernanceEnabled bool                        `json:"governance_enabled"`
}

type govParamsPendingWire struct {
	Param             string `json:"param"`
	Value             uint64 `json:"value"`
	EffectiveHeight   uint64 `json:"effective_height"`
	SubmittedAtHeight uint64 `json:"submitted_at_height"`
	Authority         string `json:"authority"`
	Memo              string `json:"memo,omitempty"`
}

type govParamsRegistryWire struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	MinValue     uint64 `json:"min_value"`
	MaxValue     uint64 `json:"max_value"`
	DefaultValue uint64 `json:"default_value"`
	Unit         string `json:"unit"`
}

// fetchGovernanceParams hits GET /governance/params and decodes
// the snapshot. Returns a typed wrapper around HTTP-level
// failures so the caller can degrade gracefully on 503 / 5xx.
func (c *CLI) fetchGovernanceParams() (*govParamsRemoteView, error) {
	body, err := c.get("/governance/params")
	if err != nil {
		return nil, err
	}
	var view govParamsRemoteView
	if err := json.Unmarshal(body, &view); err != nil {
		return nil, fmt.Errorf("decode governance/params: %w", err)
	}
	return &view, nil
}

// -----------------------------------------------------------------------------
// gov-helper inspect
// -----------------------------------------------------------------------------

// govHelperInspectView is the human-readable wire shape printed
// by the inspect subcommand for a param-set payload. Kept for
// the param-set path; authority-set payloads use
// govHelperInspectAuthorityView so the JSON is shape-clean.
type govHelperInspectView struct {
	Kind            string                 `json:"kind"`
	Param           string                 `json:"param"`
	Value           uint64                 `json:"value"`
	EffectiveHeight uint64                 `json:"effective_height"`
	Memo            string                 `json:"memo,omitempty"`
	SizeBytes       int                    `json:"size_bytes"`
	RegistryEntry   *chainparams.ParamSpec `json:"registry_entry,omitempty"`
}

// govHelperInspectAuthorityView is the human-readable wire
// shape printed by the inspect subcommand for an authority-set
// payload.
type govHelperInspectAuthorityView struct {
	Kind            string `json:"kind"`
	Op              string `json:"op"`
	Address         string `json:"address"`
	EffectiveHeight uint64 `json:"effective_height"`
	Memo            string `json:"memo,omitempty"`
	SizeBytes       int    `json:"size_bytes"`
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

	// Dispatch on payload kind so the right view shape is
	// emitted. Same kind-peek the chain admit gate uses.
	kind, err := chainparams.PeekKind(blob)
	if err != nil {
		return fmt.Errorf("peek payload kind: %w", err)
	}
	switch kind {
	case chainparams.PayloadKindParamSet:
		parsed, err := chainparams.ParseParamSet(blob)
		if err != nil {
			return fmt.Errorf("parse param-set payload: %w", err)
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
	case chainparams.PayloadKindAuthoritySet:
		parsed, err := chainparams.ParseAuthoritySet(blob)
		if err != nil {
			return fmt.Errorf("parse authority-set payload: %w", err)
		}
		view := govHelperInspectAuthorityView{
			Kind:            string(parsed.Kind),
			Op:              string(parsed.Op),
			Address:         parsed.Address,
			EffectiveHeight: parsed.EffectiveHeight,
			Memo:            parsed.Memo,
			SizeBytes:       len(blob),
		}
		out, err := json.MarshalIndent(view, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal inspect view: %w", err)
		}
		fmt.Println(string(out))
		return nil
	default:
		return fmt.Errorf("unsupported payload kind %q", kind)
	}
}

// -----------------------------------------------------------------------------
// gov-helper propose-authority
// -----------------------------------------------------------------------------

// govHelperProposeAuthority constructs an AuthoritySetPayload
// from command-line flags, validates it locally, and writes
// the encoded JSON to --out (default stdout).
//
// A rotation requires M-of-N approval — each authority runs
// this command independently to produce their VOTE, then signs
// + submits via the same `qsdmcli tx --contract-id=qsdm/gov/v1`
// path used for param-set proposals. The chain accumulates
// votes by (op, address, effective-height) tuple and stages
// the rotation when the threshold is crossed.
//
// Sanity checks performed BEFORE encoding:
//
//   - --op is "add" or "remove".
//   - --address is non-empty, ≤MaxAuthorityAddressLen, ASCII
//     printable (no whitespace, no control bytes).
//   - --effective-height is positive.
//   - --memo length cap.
//
// The chain-side height window and AuthorityList membership
// rules cannot be checked offline; they fire at applier time.
func (c *CLI) govHelperProposeAuthority(args []string) error {
	fs := flag.NewFlagSet("gov-helper propose-authority", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		op              = fs.String("op", "", `rotation kind: "add" or "remove" (required)`)
		address         = fs.String("address", "", "target address being added/removed (required)")
		effectiveHeight = fs.Uint64("effective-height", 0, "chain block height at which the rotation must be visible (required; ≥ currentHeight at submission)")
		memo            = fs.String("memo", "", "optional human-readable memo (≤256 bytes)")
		out             = fs.String("out", "-", "output path for the encoded payload ('-' for stdout)")
		printCmd        = fs.Bool("print-cmd", false, "after writing, print a placeholder `qsdmcli tx` invocation to stderr")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *op == "" {
		fs.Usage()
		return errors.New(`--op is required ("add" or "remove")`)
	}
	if *address == "" {
		fs.Usage()
		return errors.New("--address is required")
	}
	if *effectiveHeight == 0 {
		fs.Usage()
		return errors.New("--effective-height is required and must be positive")
	}

	payload := chainparams.AuthoritySetPayload{
		Kind:            chainparams.PayloadKindAuthoritySet,
		Op:              chainparams.AuthorityOp(*op),
		Address:         *address,
		EffectiveHeight: *effectiveHeight,
		Memo:            *memo,
	}
	if err := chainparams.ValidateAuthoritySetFields(&payload); err != nil {
		return fmt.Errorf("payload rejected: %w", err)
	}
	blob, err := chainparams.EncodeAuthoritySet(payload)
	if err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}
	if _, err := chainparams.ParseAuthoritySet(blob); err != nil {
		return fmt.Errorf("encoder produced bytes that fail Parse round-trip: %w", err)
	}

	if err := writeBytes(*out, blob); err != nil {
		return fmt.Errorf("write payload: %w", err)
	}

	fmt.Fprintf(os.Stderr,
		"gov authority-set payload: %d bytes, op=%q address=%q effective_height=%d memo=%dB\n",
		len(blob), *op, *address, *effectiveHeight, len(*memo))
	fmt.Fprintf(os.Stderr,
		"note: chain-side acceptance still requires (a) sender on AuthorityList, "+
			"(b) effective_height ≥ currentHeight at submission, "+
			"(c) effective_height ≤ currentHeight + %d blocks (~%dh), "+
			"and (d) M-of-N votes on the same (op, address, effective_height) tuple.\n",
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
