package telemetrycheck

// Checker is the Tier-2 advisory engine. Constructed once
// per validator boot, fed (Catalog, optional Logger), and
// then invoked once per accepted v2 proof from
// internal/miningsvc.
//
// The Checker NEVER returns an error — Check always yields
// a Verdict, even if the catalog is empty or the claim is
// degenerate. This is deliberate: the caller is on the
// proof-acceptance hot path and an "error from Check" is
// not actionable. Any genuinely degenerate input yields
// VerdictSkipped instead, with the reason captured in
// Mismatches[0] so the operator can still see what went
// wrong on /spec-anomalies.

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Checker is safe for concurrent use. Its only mutable
// state is atomic counters for /metrics; the catalog
// itself owns its own concurrency.
type Checker struct {
	catalog *Catalog

	// Now is overridable for tests; in production this is
	// always time.Now. Default in NewChecker.
	Now func() time.Time

	// Counters surfaced via Counters() for /metrics.
	checked          atomic.Uint64
	matched          atomic.Uint64
	mismatched       atomic.Uint64
	unknownSKU       atomic.Uint64
	skipped          atomic.Uint64
	mismatchByField  perFieldCounters
}

// NewChecker constructs a checker bound to catalog. catalog
// MUST NOT be nil — pass an empty *Catalog (NewCatalog())
// rather than nil if no reference data is available yet, so
// the hot path doesn't need a nil check.
func NewChecker(catalog *Catalog) *Checker {
	if catalog == nil {
		// Fail loudly: passing nil here is always a wiring
		// bug, and a panic is recoverable upstream by the
		// validator's start-of-day boot script.
		panic("telemetrycheck.NewChecker: nil catalog")
	}
	return &Checker{
		catalog: catalog,
		Now:     time.Now,
	}
}

// Check produces a Verdict for one Claim. Side-effects:
// updates internal counters (atomic, lock-free). Does not
// mutate claim or catalog.
//
// Algorithm:
//
//   1. If catalog is empty OR claim has no checkable fields
//      → VerdictSkipped.
//   2. If catalog has no entry matching claim.GPUName →
//      VerdictUnknownSKU. (Architecture / compute_cap rules
//      still run — a catalog without "RTX 9999" can still
//      flag "ampere with CC 9.0" via the architecture rule
//      because that one needs no SKU lookup.)
//   3. Otherwise run all rules; collect mismatches.
//   4. If no mismatches → VerdictMatch. Else VerdictMismatch.
//
// The order matters: VerdictUnknownSKU is more specific
// than VerdictMismatch when both could apply, because the
// operator response differs ("publish a profile for this
// SKU" vs "investigate the miner"). The arch+CC rule
// graduates to a "major" mismatch even on unknown SKU
// because no catalog is needed to know that "ampere with
// CC 9.0" is impossible.
func (c *Checker) Check(claim Claim) Verdict {
	c.checked.Add(1)
	now := c.Now().Unix()
	if claim.SubmittedAt == 0 {
		claim.SubmittedAt = now
	}

	totalEntries, _, _ := c.catalog.Counters()
	verdict := Verdict{
		CatalogSize: totalEntries,
	}

	if claim.Empty() || c.catalog.Empty() {
		verdict.Kind = VerdictSkipped
		c.skipped.Add(1)
		return verdict
	}

	candidates := c.catalog.LookupByName(claim.GPUName)
	skuKnown := len(candidates) > 0

	// Always-on rules (don't need a catalog match):
	if mm := ruleArchVsComputeCap(claim); mm != nil {
		verdict.Mismatches = append(verdict.Mismatches, *mm)
		c.mismatchByField.inc(mm.Field)
	}
	if mm := ruleDriverVerFormat(claim); mm != nil {
		verdict.Mismatches = append(verdict.Mismatches, *mm)
		c.mismatchByField.inc(mm.Field)
	}

	if !skuKnown {
		// Even if the always-on rules fired, we keep the
		// kind as UnknownSKU because the SKU absence is
		// the dominant fact for the operator. The
		// always-on mismatches still surface in the body.
		verdict.Kind = VerdictUnknownSKU
		c.unknownSKU.Add(1)
		return verdict
	}

	// Catalog-driven rules:
	if mm := ruleComputeCapAgainstSKU(claim, candidates); mm != nil {
		verdict.Mismatches = append(verdict.Mismatches, *mm)
		c.mismatchByField.inc(mm.Field)
	}

	verdict.MatchedReferences = c.catalog.LookupSourcesByName(claim.GPUName)

	if len(verdict.Mismatches) == 0 {
		verdict.Kind = VerdictMatch
		c.matched.Add(1)
	} else {
		verdict.Kind = VerdictMismatch
		c.mismatched.Add(1)
	}
	return verdict
}

// Counters returns a snapshot of /metrics counters. Atomic
// load only — no lock acquisition. Order matches the
// VerdictKind constants for readability.
func (c *Checker) Counters() (checked, matched, mismatched, unknownSKU, skipped uint64) {
	return c.checked.Load(),
		c.matched.Load(),
		c.mismatched.Load(),
		c.unknownSKU.Load(),
		c.skipped.Load()
}

// MismatchesByField returns a sorted-by-field map of every
// rule's firing count. Used by /metrics to expose
// per-field labelled counters. Returned map is a defensive
// copy — caller may mutate freely.
func (c *Checker) MismatchesByField() map[string]uint64 {
	return c.mismatchByField.snapshot()
}

// CatalogSize is a passthrough to the catalog's count.
// Surfaced here because /metrics only knows about the
// checker.
func (c *Checker) CatalogSize() (total, signers, skus int) {
	return c.catalog.Counters()
}

// ----- per-field counter helper -----

// perFieldCounters wraps a map of (field name → atomic
// uint64) behind a small synchronisation surface. We don't
// know the set of field names at construction time (a
// future rule could add a new one) so we take a Mutex on
// inc to allow lazy creation, but Snapshot does an atomic
// per-counter load with the lock held only briefly.
type perFieldCounters struct {
	mu       sync.Mutex
	counters map[string]*atomic.Uint64
}

func (p *perFieldCounters) inc(field string) {
	p.mu.Lock()
	if p.counters == nil {
		p.counters = make(map[string]*atomic.Uint64)
	}
	v, ok := p.counters[field]
	if !ok {
		v = new(atomic.Uint64)
		p.counters[field] = v
	}
	p.mu.Unlock()
	v.Add(1)
}

func (p *perFieldCounters) snapshot() map[string]uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.counters == nil {
		return nil
	}
	out := make(map[string]uint64, len(p.counters))
	for k, v := range p.counters {
		out[k] = v.Load()
	}
	return out
}

// helper to keep the imports tidy (sort + strings used
// inside catalog.go); declared here so this file
// compiles standalone if catalog.go ever moves.
var _ = sort.Strings
var _ = strings.TrimSpace
