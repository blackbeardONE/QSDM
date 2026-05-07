package telemetry

// Collector is the narrow contract an attester uses to
// gather GPUObservations from the local system. The
// production implementation is NVIDIASMICollector
// (parses nvidia-smi --query-gpu=…); tests inject
// FixedCollector to drive deterministic snapshots; future
// collectors (rocm-smi for AMD, hostside SPDM for
// nvidia-cc-v1) will satisfy the same interface.

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Collector is the abstraction over "ask the OS what GPUs
// are attached and what they look like right now". Returned
// observations are MERGED into the registry — the registry
// owns longevity (FirstObservedAt / Observations); the
// collector owns "what does the device say in this instant".
type Collector interface {
	// Kind identifies the data source for /info and
	// signed profiles. Stable identifier across versions.
	Kind() string

	// Collect returns one snapshot per attached GPU, or
	// (nil, error) if the data source is unavailable.
	// Implementations MUST honour ctx cancellation /
	// deadline.
	Collect(ctx context.Context) ([]GPUObservation, error)
}

// FixedCollector returns a static slice on every Collect.
// Used in tests + as the no-op fallback when no real
// collector is wired (e.g. on a Linux box without GPUs).
type FixedCollector struct {
	KindStr      string
	Observations []GPUObservation
	Err          error
}

// Kind implements Collector.
func (f *FixedCollector) Kind() string {
	if f.KindStr == "" {
		return "fixed"
	}
	return f.KindStr
}

// Collect implements Collector.
func (f *FixedCollector) Collect(_ context.Context) ([]GPUObservation, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	out := make([]GPUObservation, len(f.Observations))
	copy(out, f.Observations)
	return out, nil
}

// NVIDIASMICollector reads observations from `nvidia-smi
// --query-gpu=…`. Default Path is "nvidia-smi" (resolved
// from PATH); override Path for sandboxed/relocated
// installations or for tests that point at a fake CLI.
type NVIDIASMICollector struct {
	// Path is the executable to invoke. Empty = "nvidia-smi"
	// from PATH.
	Path string

	// Timeout bounds one Collect() call. Zero = 5s, which
	// is generous for a healthy nvidia-smi (typically <1s)
	// while bounding hangs from a stuck driver.
	Timeout time.Duration
}

// Kind implements Collector.
func (c *NVIDIASMICollector) Kind() string { return "nvidia-smi" }

// nvidiaSMIQueryFields is the canonical column list this
// collector requests. Order matches the column order in the
// CSV response, so the parse loop indexes by ordinal.
//
// Each field maps to a documented nvidia-smi attribute; see
// `nvidia-smi --help-query-gpu` for the full list.
//
// IMPORTANT: do NOT reorder this slice without also
// reordering the parse switch in parseNVIDIASMICSV. The
// pairing is deliberate to keep the CSV-to-struct mapping
// declarative and easy to extend (add to both ends).
// We deliberately query the *max* clock domains
// (clocks.max.gr / clocks.max.mem) rather than the current
// clocks (clocks.gr / clocks.mem). The reference profile is
// a per-SKU fingerprint, so we want the value the device
// boosts to under load — a static spec — not the
// instantaneous idle clock, which depends on whether the
// GPU is actively busy at the moment of the snapshot.
var nvidiaSMIQueryFields = []string{
	"uuid",
	"name",
	"compute_cap",
	"memory.total",
	"driver_version",
	"pcie.link.gen.max",
	"pcie.link.width.max",
	"power.max_limit",
	"vbios_version",
	"ecc.mode.current",
	"clocks.max.gr",
	"clocks.max.mem",
}

// Collect implements Collector.
func (c *NVIDIASMICollector) Collect(ctx context.Context) ([]GPUObservation, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	path := c.Path
	if path == "" {
		path = "nvidia-smi"
	}
	args := []string{
		"--query-gpu=" + strings.Join(nvidiaSMIQueryFields, ","),
		"--format=csv,noheader,nounits",
	}
	cmd := exec.CommandContext(cctx, path, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("telemetry: %s exited: %w (stderr: %s)",
			path, err, strings.TrimSpace(stderr.String()))
	}
	return parseNVIDIASMICSV(stdout.Bytes())
}

// parseNVIDIASMICSV converts nvidia-smi CSV output into
// GPUObservations. Robust to whitespace / "[N/A]" /
// "[Not Supported]" — those map to zero values.
//
// Exposed (lowercase, but accessed via tests in the same
// package) so the test suite can pin the parse against
// captured real-world fixtures without spinning up an
// actual nvidia-smi.
func parseNVIDIASMICSV(raw []byte) ([]GPUObservation, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("telemetry: nvidia-smi returned empty output")
	}
	r := csv.NewReader(bytes.NewReader(raw))
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = -1 // tolerant; we'll validate per-row

	out := make([]GPUObservation, 0, 4)
	rowNum := 0
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("telemetry: csv row %d: %w", rowNum, err)
		}
		rowNum++
		if len(rec) < len(nvidiaSMIQueryFields) {
			return nil, fmt.Errorf(
				"telemetry: csv row %d has %d columns, need %d (fields: %v)",
				rowNum, len(rec), len(nvidiaSMIQueryFields), nvidiaSMIQueryFields)
		}
		obs := GPUObservation{
			UUID:               cleanField(rec[0]),
			Name:               cleanField(rec[1]),
			Vendor:             "NVIDIA",
			ComputeCapability:  cleanField(rec[2]),
			MemoryTotalMB:      parseUint64(rec[3]),
			DriverVersionsSeen: nonEmptySingleton(cleanField(rec[4])),
			PCIeGen:            parseUint8(rec[5]),
			PCIeWidth:          parseUint8(rec[6]),
			PowerMaxW:          parseFloat(rec[7]),
			VBIOSVersionsSeen:  nonEmptySingleton(cleanField(rec[8])),
			ClockGraphicsBoostMHz: parseUint32(rec[10]),
			ClockMemoryMHz:        parseUint32(rec[11]),
		}
		// ecc.mode.current is "Enabled" / "Disabled" / "[N/A]".
		eccStr := strings.ToLower(cleanField(rec[9]))
		obs.ECCSupported = eccStr == "enabled" || eccStr == "disabled"
		// Architecture is inferred from compute capability;
		// nvidia-smi doesn't report it directly. Pure
		// rule-based mapping that needs maintenance when
		// new generations land — but the alternative
		// (parsing GPU name strings) is fragile too.
		obs.Architecture = inferArchitecture(obs.ComputeCapability)
		// CUDA runtime version is NOT a per-GPU field —
		// nvidia-smi reports it once at the top of its
		// default output. We collect it separately if at
		// all; for now leave empty so a future
		// `nvidia-smi -q | grep CUDA` enrichment can fill
		// it in without breaking the per-GPU schema.
		out = append(out, obs)
	}
	if len(out) == 0 {
		return nil, errors.New("telemetry: nvidia-smi returned no GPU rows")
	}
	return out, nil
}

// cleanField trims whitespace and converts the well-known
// "Not Available" / "N/A" tokens to the empty string.
// nvidia-smi sometimes emits these per-field for unsupported
// queries (e.g. ECC on a consumer card); they should NOT
// pollute the persisted observation set.
func cleanField(s string) string {
	v := strings.TrimSpace(s)
	switch v {
	case "[N/A]", "[Not Available]", "[Not Supported]", "N/A":
		return ""
	}
	return v
}

// parseUint64 / parseUint8 / parseUint32 / parseFloat:
// nvidia-smi nounits outputs are pure decimal integers
// (memory.total in MiB, power.max_limit in watts as a
// float, clocks.gr in MHz, etc.). Empty / non-numeric
// inputs yield zero so the registry's MergeWith treats them
// as "no signal".
func parseUint64(s string) uint64 {
	s = cleanField(s)
	if s == "" {
		return 0
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func parseUint32(s string) uint32 {
	s = cleanField(s)
	if s == "" {
		return 0
	}
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0
	}
	return uint32(v)
}

func parseUint8(s string) uint8 {
	s = cleanField(s)
	if s == "" {
		return 0
	}
	v, err := strconv.ParseUint(s, 10, 8)
	if err != nil {
		return 0
	}
	return uint8(v)
}

func parseFloat(s string) float64 {
	s = cleanField(s)
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// nonEmptySingleton returns []string{s} when s != "" and
// nil otherwise. Used to populate the *VersionsSeen sets
// from a single-row collector observation.
func nonEmptySingleton(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

// inferArchitecture maps a CUDA compute capability to a
// generation name. Conservative — returns "" for unknown
// capabilities so a future generation doesn't get
// mis-classified as the closest old one.
//
// Sources:
//   - 5.x = Maxwell
//   - 6.x = Pascal
//   - 7.0 / 7.2 = Volta
//   - 7.5 = Turing
//   - 8.0 = Ampere (A100 datacenter)
//   - 8.6 / 8.7 = Ampere (RTX 30 series + Jetson)
//   - 8.9 = Ada Lovelace (RTX 40 series)
//   - 9.0 = Hopper (H100)
//   - 10.0 / 12.0 = Blackwell (B100/B200/RTX 50 series)
func inferArchitecture(cc string) string {
	cc = strings.TrimSpace(cc)
	if cc == "" {
		return ""
	}
	major, minor := splitComputeCap(cc)
	switch major {
	case 5:
		return "maxwell"
	case 6:
		return "pascal"
	case 7:
		switch minor {
		case 0, 2:
			return "volta"
		case 5:
			return "turing"
		}
	case 8:
		switch minor {
		case 0, 6, 7:
			return "ampere"
		case 9:
			return "ada-lovelace"
		}
	case 9:
		return "hopper"
	case 10, 12:
		return "blackwell"
	}
	return ""
}

func splitComputeCap(cc string) (int, int) {
	parts := strings.SplitN(cc, ".", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0
	}
	return major, minor
}
