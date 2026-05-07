package telemetrycheck

// Catalog holds the reference data the Checker compares
// claims against. Two source kinds:
//
//   1. Static baseline — vendor-known specs for common
//      SKUs, compiled into the binary. Always present; gives
//      the validator something to compare against on a
//      brand-new chain with zero connected attesters.
//
//   2. Live attester profiles — pkg/telemetry.ReferenceProfile
//      documents pulled from peer attesters' /api/v1/telemetry/
//      reference endpoints. Apply()'d as they arrive. Each
//      profile carries a SignerID; the catalog tracks which
//      profiles came from which signer so a future Tier-3
//      reputation system can weight them.
//
// Lookup primitives are name-keyed (canonical lower-case),
// because all the practical rules ("does this SKU support
// CC 9.0?", "is this driver_ver an observed value for this
// SKU?") branch on the SKU first. UUID-keyed lookup is NOT
// useful: each physical card has a unique UUID, so a
// catalog of one operator's cards can never validate
// another's UUID-by-UUID.

import (
	"sort"
	"strings"
	"sync"

	"github.com/blackbeardONE/QSDM/pkg/telemetry"
)

// Catalog is safe for concurrent reads (Lookup, KnownNames,
// Counters) and serialised writes (Apply, ReplaceFromSigner).
// The expected workload is "many reads on the proof-acceptance
// hot path, one write every minute or two when an attester
// poller refreshes." A single sync.RWMutex handles that
// gracefully without specialised data structures.
type Catalog struct {
	mu sync.RWMutex

	// byName maps canonicalGPUName(name) → list of
	// (sourceID, observation). Multiple entries per name
	// are common — different attesters publishing the same
	// SKU, or the same attester observing multiple cards.
	byName map[string][]catalogEntry

	// signers tracks the latest profile-issued-at per
	// SignerID. Lets ReplaceFromSigner detect a stale
	// fetch (issued_at < last seen) and refuse to apply
	// it. Tiny defence against a downgrade-style attack
	// where a malicious relay replays an old profile.
	signers map[string]int64

	// totalEntries is the cached len of all byName slices.
	// Recomputed on every mutation; reads are O(1) so
	// /metrics can sample it cheaply.
	totalEntries int
}

// catalogEntry is one (sourceID, observation) record. The
// sourceID is either a peer-attester SignerID (e.g.
// "attester-12a0d1aa082b7e28") or the literal string
// "baseline" for hard-coded vendor spec entries. The
// distinction matters for /metrics ("how much of the
// catalog comes from which source") and for Tier-3.
type catalogEntry struct {
	SourceID string
	Obs      telemetry.GPUObservation
}

// NewCatalog returns an empty Catalog with no baseline
// loaded. Most production callers should call
// LoadBaseline immediately after construction so the
// catalog is non-empty even before any peer profiles arrive.
func NewCatalog() *Catalog {
	return &Catalog{
		byName:  make(map[string][]catalogEntry),
		signers: make(map[string]int64),
	}
}

// Apply folds one telemetry.ReferenceProfile into the
// catalog. Designed to be called once per peer-attester
// poller tick. Concurrency-safe.
//
// Behaviour:
//
//   - profile.IssuedAt earlier than the last-applied
//     IssuedAt for profile.SignerID → ignored (returns
//     0, nil).
//   - profile.SignerID == "" → ignored (returns 0, error).
//   - otherwise → all profile.GPUs entries are added under
//     this SignerID; any prior entries for the same
//     SignerID are removed first (atomic replace per
//     signer).
//
// Returns the number of entries actually added.
func (c *Catalog) Apply(profile *telemetry.ReferenceProfile) (int, error) {
	if profile == nil {
		return 0, errCatalogNilProfile
	}
	if strings.TrimSpace(profile.SignerID) == "" {
		return 0, errCatalogEmptySigner
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if last, ok := c.signers[profile.SignerID]; ok && profile.IssuedAt < last {
		return 0, nil
	}

	c.removeBySignerLocked(profile.SignerID)
	added := 0
	for _, g := range profile.GPUs {
		if strings.TrimSpace(g.Name) == "" {
			continue
		}
		key := canonicalGPUName(g.Name)
		c.byName[key] = append(c.byName[key], catalogEntry{
			SourceID: profile.SignerID,
			Obs:      g,
		})
		added++
	}
	c.signers[profile.SignerID] = profile.IssuedAt
	c.recountLocked()
	return added, nil
}

// LoadBaseline installs the built-in static catalog. Idempotent
// — calling it twice replaces the prior baseline with the
// fresh one. The "baseline" SignerID is reserved.
func (c *Catalog) LoadBaseline() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeBySignerLocked(BaselineSignerID)
	added := 0
	for _, g := range Baseline() {
		if strings.TrimSpace(g.Name) == "" {
			continue
		}
		key := canonicalGPUName(g.Name)
		c.byName[key] = append(c.byName[key], catalogEntry{
			SourceID: BaselineSignerID,
			Obs:      g,
		})
		added++
	}
	c.signers[BaselineSignerID] = 0 // baseline has no real timestamp
	c.recountLocked()
	return added
}

// LookupByName returns every catalog entry whose
// canonical name matches name. Empty slice when no entries
// match. Returned slice is a fresh copy — safe to mutate.
func (c *Catalog) LookupByName(name string) []telemetry.GPUObservation {
	key := canonicalGPUName(name)
	if key == "" {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	entries := c.byName[key]
	if len(entries) == 0 {
		return nil
	}
	out := make([]telemetry.GPUObservation, len(entries))
	for i, e := range entries {
		out[i] = e.Obs
	}
	return out
}

// LookupSourcesByName returns the SignerIDs of every
// catalog entry whose name matches. Used by
// Verdict.MatchedReferences so an operator can see which
// signers vouched for the claim.
func (c *Catalog) LookupSourcesByName(name string) []string {
	key := canonicalGPUName(name)
	if key == "" {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	entries := c.byName[key]
	if len(entries) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(entries))
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if _, ok := seen[e.SourceID]; ok {
			continue
		}
		seen[e.SourceID] = struct{}{}
		out = append(out, e.SourceID)
	}
	sort.Strings(out)
	return out
}

// KnownNames returns the canonical name of every SKU the
// catalog has a profile for. Sorted ascending. Useful for
// /metrics ("what does the catalog know about?") and for
// debugging "why is this proof flagged unknown_sku" —
// usually because the operator's GPU name format drifted
// from the catalog's by capitalisation or extra whitespace.
func (c *Catalog) KnownNames() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.byName))
	for k := range c.byName {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Counters returns (totalEntries, signerCount, skuCount) for
// /metrics gauges and the /info-style summary endpoint.
func (c *Catalog) Counters() (int, int, int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.totalEntries, len(c.signers), len(c.byName)
}

// Empty reports whether the catalog has zero entries. Cheap
// helper for the Checker hot path: when the catalog is
// empty, every Check returns VerdictSkipped without doing
// any work.
func (c *Catalog) Empty() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.totalEntries == 0
}

// ----- internal -----

// removeBySignerLocked drops every entry whose SourceID
// matches signer. Caller must hold c.mu (write).
func (c *Catalog) removeBySignerLocked(signer string) {
	for k, entries := range c.byName {
		filtered := entries[:0]
		for _, e := range entries {
			if e.SourceID == signer {
				continue
			}
			filtered = append(filtered, e)
		}
		if len(filtered) == 0 {
			delete(c.byName, k)
		} else {
			c.byName[k] = filtered
		}
	}
}

// recountLocked refreshes c.totalEntries. Caller must hold
// c.mu (write).
func (c *Catalog) recountLocked() {
	n := 0
	for _, entries := range c.byName {
		n += len(entries)
	}
	c.totalEntries = n
}

// BaselineSignerID is the reserved SignerID for entries
// loaded by LoadBaseline. Used in Verdict.MatchedReferences
// to distinguish "the network's hard-coded floor said this
// claim is plausible" from "a real attester observed this
// hardware and its signed profile says so."
const BaselineSignerID = "baseline"

// Sentinel errors so callers can errors.Is() distinct
// failure modes. Kept private (lowercase wrappers) because
// the only intended caller is the Catalog itself; consumers
// typically don't branch on Apply errors today.

type catalogError struct{ msg string }

func (e *catalogError) Error() string { return "telemetrycheck: " + e.msg }

var (
	errCatalogNilProfile  = &catalogError{msg: "Apply: nil profile"}
	errCatalogEmptySigner = &catalogError{msg: "Apply: profile has empty signer_id"}
)
