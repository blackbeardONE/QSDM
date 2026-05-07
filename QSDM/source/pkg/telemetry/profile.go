// Package telemetry implements the QSDM Reference Telemetry
// Oracle: an attester-side service that observes the GPUs
// physically attached to its host, accumulates a
// long-running profile of what those GPUs "look like"
// (static specs + driver/CUDA/VBIOS versions seen + boot
// counts), and publishes the result as a signed JSON blob
// any validator (or curious miner) can pull and reason about.
//
// Why does the network want this?
//
//   QSDM today has no way to verify a miner's GPU claims at
//   submission time. A miner can write `gpu_name = "NVIDIA
//   GeForce RTX 3050"` in their miner.toml and the validator
//   has nothing to check it against — the HMAC v1 attestation
//   only proves "the operator possesses the enrollment key",
//   not "the operator's hardware actually matches the spec
//   sheet for that SKU."
//
//   The Reference Telemetry Oracle gives the validator a
//   ground-truth catalog: "operators we trust have personally
//   observed these GPUs; here is what the real hardware
//   reports for memory_total_mb / power_max_w / pcie_gen /
//   driver_version etc." Future enforcement code can compare
//   a miner's claimed specs against the catalog and downgrade
//   or reject obvious spoofs (memory_total_mb=24576 on a SKU
//   the catalog says is always 8192 = obvious lie).
//
//   For today, the oracle is publish-only. It's the data
//   substrate that the network needs before any spoofing-
//   detection logic can land — and it ships first because
//   accumulating real-world observations takes time. A 3050
//   that's been running for a month has a far more useful
//   driver_versions_seen list than one that just booted.
//
// Threat model:
//
//   Profiles are signed with the attester's HMAC signer key
//   (the same key it uses to sign challenges). A peer that
//   trusts that key for challenges trusts it for telemetry
//   too — same operator, same hardware, same liability.
//   A peer that does NOT trust the key ignores the profile.
//
//   The signature does NOT prove the OBSERVATIONS are
//   honest — an attester operator could lie about their
//   hardware. The defense against that is the same one we
//   use for challenges: peer review (operators cross-check
//   each other's catalogs) plus eventual on-chain
//   reputation. Profiles are deliberately conservative
//   about what they include so an honest attester can never
//   accidentally publish PII or chain-state secrets.
package telemetry

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// SchemaVersion is bumped only when the on-the-wire shape of
// ReferenceProfile changes in a non-additive way (i.e. when
// an old verifier would mis-parse a new producer's output).
// Adding fields is additive — a v1 verifier seeing v1 fields
// + a v2 producer's new fields just ignores the extras —
// so most evolution does NOT bump the version. Today's only
// version is 1.
const SchemaVersion = 1

// MaxObservationsCap bounds how many version strings each
// per-GPU set can grow to. A 3050 in production will
// probably accumulate fewer than 10 driver versions in its
// lifetime, but a misbehaving collector that floods strings
// (e.g. timestamp-stamped values) would otherwise grow the
// profile without bound. 1024 is generous for the use case.
const MaxObservationsCap = 1024

// ReferenceProfile is one attester's authoritative snapshot
// of the GPUs it has personally observed. The Signature
// covers the canonical encoding of every other field; see
// CanonicalForSigning for the exact rules.
//
// Field ordering matches the JSON marshalling output. Stable
// across versions for v1; new fields go AFTER GPUs and
// BEFORE Signature so the signature always lives at the end
// of the rendered JSON.
type ReferenceProfile struct {
	// SchemaVersion is the wire-version. Always 1 today.
	SchemaVersion int `json:"schema_version"`

	// SignerID is the attester's signer ID (the same ID it
	// uses on /api/v1/mining/challenge). Allowed shapes
	// are the same — "attester-<hex>" or any operator
	// override on first boot.
	SignerID string `json:"signer_id"`

	// HostNote is a free-form operator-supplied tag — same
	// semantics as the Note in /info. Lets a validator
	// operator at a glance see "this catalog comes from
	// blackbeard's Manila box" before deciding what
	// to do with it.
	HostNote string `json:"host_note,omitempty"`

	// IssuedAt is the seconds-since-epoch the profile was
	// signed. Verifiers can choose to discard profiles
	// older than some threshold — though for static
	// reference data, "old" doesn't mean "wrong", just
	// "not refreshed recently".
	IssuedAt int64 `json:"issued_at"`

	// CollectorKind names the ground-truth source for the
	// observations (today: "nvidia-smi"; tomorrow:
	// "nvml-attestation", "rocm-smi", "spdm", etc.).
	// Verifiers MAY weight profiles differently by
	// collector source — an attestation-grade collector
	// is more trustworthy than a parsed-CLI-output one.
	CollectorKind string `json:"collector_kind,omitempty"`

	// GPUs holds one entry per physical GPU the attester
	// has ever observed. Profiles are accumulative; an
	// attester restart re-loads the on-disk file rather
	// than starting from a blank slate.
	GPUs []GPUObservation `json:"gpus"`

	// Signature is the lowercase-hex HMAC-SHA256 of
	// CanonicalForSigning(). Exists in the JSON so a
	// receiver can round-trip the profile through any
	// number of intermediaries without losing the
	// signature; the signing process must always re-set
	// this field to "" before computing the HMAC (handled
	// by Sign / Verify).
	Signature string `json:"signature,omitempty"`
}

// GPUObservation accumulates everything one attester has
// learned about one physical GPU across its lifetime. Static
// fields (UUID, Name, Vendor) settle on the first observation
// and never change; observation-history fields
// (DriverVersionsSeen, Observations, etc.) grow over time.
type GPUObservation struct {
	// UUID is the NVIDIA GPU UUID (or vendor-equivalent
	// for future non-NVIDIA collectors). Acts as the
	// identity key inside a profile — two GPUs with the
	// same UUID MUST be the same physical card.
	UUID string `json:"uuid"`

	// Name is the human-readable model string, exactly
	// as the collector reports it. Trimmed of leading /
	// trailing whitespace.
	Name string `json:"name"`

	// Vendor is the GPU vendor ("NVIDIA", "AMD", "Intel").
	// Inferred by the collector from the device string;
	// today always "NVIDIA" because nvidia-smi is the
	// only collector.
	Vendor string `json:"vendor,omitempty"`

	// Architecture is the GPU microarchitecture string
	// ("ampere", "hopper", "blackwell", etc.). Inferred
	// from the compute capability + product line, since
	// nvidia-smi doesn't report it directly.
	Architecture string `json:"arch,omitempty"`

	// ComputeCapability is the major.minor CUDA capability
	// string ("8.6", "9.0"). Empty for non-CUDA devices.
	ComputeCapability string `json:"compute_cap,omitempty"`

	// MemoryTotalMB is the device's total memory in
	// mebibytes (1024×1024 bytes), as reported by the
	// collector. NVIDIA marketing numbers (e.g. 8 GB) are
	// often slightly lower than the reported total —
	// store what the device says, not the marketing value.
	MemoryTotalMB uint64 `json:"memory_total_mb"`

	// PCIeGen / PCIeWidth: the maximum negotiable PCIe
	// generation + lane width the device advertises. Empty
	// (0) when the collector can't determine.
	PCIeGen   uint8 `json:"pcie_gen,omitempty"`
	PCIeWidth uint8 `json:"pcie_width,omitempty"`

	// PowerMaxW is the device's TDP cap in watts. For
	// 3050 desktop variants this is typically 130W; for
	// laptop variants 35–80W; H100 SXM 700W. A future
	// spoofing detector can fail-fast on a claim like
	// "RTX 3050 with PowerMaxW=400" — that combination
	// does not exist.
	PowerMaxW float64 `json:"power_max_w,omitempty"`

	// ECCSupported is true when the device supports ECC
	// memory. Consumer cards (3050, 3060, etc.) → false.
	// Datacenter cards (A100, H100) → true. A miner who
	// claims ECCSupported=true for a 3050 is lying.
	ECCSupported bool `json:"ecc_supported"`

	// ClockGraphicsBaseMHz / BoostMHz / MemoryMHz: the
	// stock clocks the device reports. 0 when unavailable.
	ClockGraphicsBaseMHz  uint32 `json:"clock_graphics_base_mhz,omitempty"`
	ClockGraphicsBoostMHz uint32 `json:"clock_graphics_boost_mhz,omitempty"`
	ClockMemoryMHz        uint32 `json:"clock_memory_mhz,omitempty"`

	// DriverVersionsSeen is the union of every distinct
	// driver_version this attester has reported for this
	// GPU. A new boot under a fresh driver appends to the
	// list; existing values are deduplicated.
	DriverVersionsSeen []string `json:"driver_versions_seen,omitempty"`

	// CUDAVersionsSeen is the same idea for CUDA runtime
	// versions. Often correlated with DriverVersionsSeen
	// but evolves independently when a CUDA update ships
	// without a driver update.
	CUDAVersionsSeen []string `json:"cuda_versions_seen,omitempty"`

	// VBIOSVersionsSeen is the union of every distinct
	// VBIOS string. NVIDIA OEM partners sometimes ship
	// the same physical SKU with different VBIOSes; this
	// catalog captures that diversity.
	VBIOSVersionsSeen []string `json:"vbios_versions_seen,omitempty"`

	// FirstObservedAt is the first wall-clock time this
	// attester ever saw the UUID. LastObservedAt is the
	// most recent. Both are seconds since Unix epoch.
	FirstObservedAt int64 `json:"first_observed_at"`
	LastObservedAt  int64 `json:"last_observed_at"`

	// Observations counts how many times Apply() has been
	// invoked for this UUID across the attester's
	// lifetime. Useful as a "longevity" signal — a
	// profile with Observations=12000 has been collecting
	// for far longer than one with Observations=3.
	Observations uint64 `json:"observations"`
}

// MergeWith folds the snapshot snap into o, conservatively
// preserving the longer-lived data and unioning the version
// sets. Returns true when something material changed (i.e.
// when the caller's persisted file should be rewritten).
//
// The "static" fields (Name, Vendor, Architecture,
// ComputeCapability, MemoryTotalMB, PCIe*, PowerMaxW,
// ECCSupported, Clock*) update on every merge so a corrected
// reading from a newer driver supersedes a stale one. UUID
// is the identity and MUST already match before MergeWith is
// called.
func (o *GPUObservation) MergeWith(snap GPUObservation, now int64) bool {
	if snap.UUID != "" && o.UUID != snap.UUID {
		// MergeWith pre-condition violated; refuse
		// silently — the caller has a bug but we don't
		// want to corrupt the profile.
		return false
	}

	changed := false

	// Static-but-mutable: a snapshot's value wins when
	// non-empty / non-zero. Lets a fresh driver fill in a
	// previously-missing field without overwriting good
	// data with bad zeroes from a transient read failure.
	changed = updateString(&o.Name, snap.Name) || changed
	changed = updateString(&o.Vendor, snap.Vendor) || changed
	changed = updateString(&o.Architecture, snap.Architecture) || changed
	changed = updateString(&o.ComputeCapability, snap.ComputeCapability) || changed
	if snap.MemoryTotalMB != 0 && snap.MemoryTotalMB != o.MemoryTotalMB {
		o.MemoryTotalMB = snap.MemoryTotalMB
		changed = true
	}
	if snap.PCIeGen != 0 && snap.PCIeGen != o.PCIeGen {
		o.PCIeGen = snap.PCIeGen
		changed = true
	}
	if snap.PCIeWidth != 0 && snap.PCIeWidth != o.PCIeWidth {
		o.PCIeWidth = snap.PCIeWidth
		changed = true
	}
	if snap.PowerMaxW != 0 && snap.PowerMaxW != o.PowerMaxW {
		o.PowerMaxW = snap.PowerMaxW
		changed = true
	}
	if snap.ECCSupported != o.ECCSupported {
		o.ECCSupported = snap.ECCSupported
		changed = true
	}
	if snap.ClockGraphicsBaseMHz != 0 && snap.ClockGraphicsBaseMHz != o.ClockGraphicsBaseMHz {
		o.ClockGraphicsBaseMHz = snap.ClockGraphicsBaseMHz
		changed = true
	}
	if snap.ClockGraphicsBoostMHz != 0 && snap.ClockGraphicsBoostMHz != o.ClockGraphicsBoostMHz {
		o.ClockGraphicsBoostMHz = snap.ClockGraphicsBoostMHz
		changed = true
	}
	if snap.ClockMemoryMHz != 0 && snap.ClockMemoryMHz != o.ClockMemoryMHz {
		o.ClockMemoryMHz = snap.ClockMemoryMHz
		changed = true
	}

	// Version sets: union with cap.
	if c := unionStringSet(&o.DriverVersionsSeen, snap.DriverVersionsSeen, MaxObservationsCap); c {
		changed = true
	}
	if c := unionStringSet(&o.CUDAVersionsSeen, snap.CUDAVersionsSeen, MaxObservationsCap); c {
		changed = true
	}
	if c := unionStringSet(&o.VBIOSVersionsSeen, snap.VBIOSVersionsSeen, MaxObservationsCap); c {
		changed = true
	}

	// Lifetime counters always advance.
	if o.FirstObservedAt == 0 || (snap.FirstObservedAt != 0 && snap.FirstObservedAt < o.FirstObservedAt) {
		o.FirstObservedAt = snap.FirstObservedAt
		if o.FirstObservedAt == 0 {
			o.FirstObservedAt = now
		}
		changed = true
	}
	if o.FirstObservedAt == 0 {
		o.FirstObservedAt = now
		changed = true
	}
	if now > o.LastObservedAt {
		o.LastObservedAt = now
		changed = true
	}
	o.Observations++
	changed = true

	return changed
}

// updateString writes src into *dst when src is non-empty and
// different from the current *dst. Returns true on change.
func updateString(dst *string, src string) bool {
	src = strings.TrimSpace(src)
	if src == "" || *dst == src {
		return false
	}
	*dst = src
	return true
}

// unionStringSet appends every element of incoming that is
// not already in *dst, capped to maxLen. Returns true on
// any append. Trims whitespace; skips empty values.
func unionStringSet(dst *[]string, incoming []string, maxLen int) bool {
	if len(incoming) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(*dst))
	for _, v := range *dst {
		seen[v] = struct{}{}
	}
	changed := false
	for _, v := range incoming {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		if len(*dst) >= maxLen {
			break
		}
		*dst = append(*dst, v)
		seen[v] = struct{}{}
		changed = true
	}
	return changed
}

// CanonicalForSigning returns the bytes that the HMAC
// signature is computed over. Three rules:
//
//  1. Signature is cleared (the signature is over a
//     not-yet-signed profile).
//  2. GPUs are sorted by UUID — slice order in the wire
//     JSON might be arbitrary but the signature must be
//     order-independent.
//  3. String slices inside each GPU are sorted — same
//     reason. unionStringSet preserves insertion order
//     for human readability; the signature does not.
//
// Signers and verifiers MUST go through this function so
// formatting drift between Go versions / json package
// updates does not invalidate signatures.
func (p *ReferenceProfile) CanonicalForSigning() ([]byte, error) {
	cp := *p
	cp.Signature = ""

	gpusCopy := make([]GPUObservation, len(p.GPUs))
	for i, g := range p.GPUs {
		gpu := g
		if len(gpu.DriverVersionsSeen) > 0 {
			gpu.DriverVersionsSeen = sortedCopy(gpu.DriverVersionsSeen)
		}
		if len(gpu.CUDAVersionsSeen) > 0 {
			gpu.CUDAVersionsSeen = sortedCopy(gpu.CUDAVersionsSeen)
		}
		if len(gpu.VBIOSVersionsSeen) > 0 {
			gpu.VBIOSVersionsSeen = sortedCopy(gpu.VBIOSVersionsSeen)
		}
		gpusCopy[i] = gpu
	}
	sort.SliceStable(gpusCopy, func(i, j int) bool {
		return gpusCopy[i].UUID < gpusCopy[j].UUID
	})
	cp.GPUs = gpusCopy

	out, err := json.Marshal(&cp)
	if err != nil {
		return nil, fmt.Errorf("telemetry: canonical encode: %w", err)
	}
	return out, nil
}

// Sign computes and stores the HMAC signature in p.Signature.
// key MUST be ≥16 bytes. Returns an error on a too-short
// key or any encoding failure.
func (p *ReferenceProfile) Sign(key []byte) error {
	if len(key) < 16 {
		return fmt.Errorf("telemetry: Sign: key length %d < 16", len(key))
	}
	canonical, err := p.CanonicalForSigning()
	if err != nil {
		return err
	}
	mac := hmac.New(sha256.New, key)
	if _, err := mac.Write(canonical); err != nil {
		return fmt.Errorf("telemetry: Sign: hmac write: %w", err)
	}
	p.Signature = hex.EncodeToString(mac.Sum(nil))
	return nil
}

// Verify checks that p.Signature is the HMAC of
// CanonicalForSigning() under key. Returns true on a
// constant-time match.
func (p *ReferenceProfile) Verify(key []byte) bool {
	if len(key) < 16 {
		return false
	}
	if p.Signature == "" {
		return false
	}
	wantBytes, err := hex.DecodeString(p.Signature)
	if err != nil {
		return false
	}
	canonical, err := p.CanonicalForSigning()
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, key)
	if _, err := mac.Write(canonical); err != nil {
		return false
	}
	return hmac.Equal(mac.Sum(nil), wantBytes)
}

// Validate enforces well-formedness invariants Sign and
// Verify alone do not check (e.g. signer ID non-empty,
// schema version sanity). Useful as a defensive check at
// the wire boundary so malformed-but-correctly-signed
// profiles surface as Validate errors rather than confusing
// downstream consumers.
func (p *ReferenceProfile) Validate() error {
	if p == nil {
		return errors.New("telemetry: nil ReferenceProfile")
	}
	if p.SchemaVersion <= 0 {
		return fmt.Errorf("telemetry: invalid schema_version %d", p.SchemaVersion)
	}
	if strings.TrimSpace(p.SignerID) == "" {
		return errors.New("telemetry: empty signer_id")
	}
	if p.IssuedAt <= 0 {
		return errors.New("telemetry: non-positive issued_at")
	}
	for i, g := range p.GPUs {
		if strings.TrimSpace(g.UUID) == "" {
			return fmt.Errorf("telemetry: gpu[%d]: empty uuid", i)
		}
		if g.LastObservedAt < g.FirstObservedAt {
			return fmt.Errorf("telemetry: gpu[%d] %s: last_observed_at %d < first_observed_at %d",
				i, g.UUID, g.LastObservedAt, g.FirstObservedAt)
		}
	}
	return nil
}

// sortedCopy returns a sorted copy of in (lexicographic
// ascending). Defined here as a package-internal helper —
// not exported because callers want the original ordering
// preserved everywhere except inside CanonicalForSigning.
func sortedCopy(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	sort.Strings(out)
	return out
}

// FreshnessAge returns time.Since(IssuedAt) as a time.Duration.
// Useful for verifiers that want to bucket profiles by
// "recently refreshed" vs "stale".
func (p *ReferenceProfile) FreshnessAge(now time.Time) time.Duration {
	return time.Duration(now.Unix()-p.IssuedAt) * time.Second
}
