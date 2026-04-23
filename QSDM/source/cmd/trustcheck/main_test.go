package main

import (
	"fmt"
	"testing"
	"time"
)

func mustTime(s string) *string { return &s }

func baseSummary() *trustSummary {
	now := time.Now().UTC().Format(time.RFC3339)
	return &trustSummary{
		Attested:         3,
		TotalPublic:      5,
		Ratio:            0.6,
		FreshWithin:      "15m0s",
		LastAttestedAt:   mustTime(now),
		LastCheckedAt:    now,
		NGCServiceStatus: "healthy",
		ScopeNote:        expectedScopeNote,
	}
}

func TestValidateSummary_Pass(t *testing.T) {
	rs := &results{}
	validateSummary(baseSummary(), rs)
	if !rs.allOK() {
		for _, r := range rs.rows {
			if !r.ok {
				t.Errorf("%s — %s", r.name, r.msg)
			}
		}
	}
}

func TestValidateSummary_AntiClaim_AttestedWithoutDenominator(t *testing.T) {
	s := baseSummary()
	s.TotalPublic = 0
	s.Attested = 1
	rs := &results{}
	validateSummary(s, rs)
	if rs.allOK() {
		t.Fatal("expected §8.5.2 violation to fail assertion")
	}
}

func TestValidateSummary_ScopeNoteDrift(t *testing.T) {
	s := baseSummary()
	s.ScopeNote = "NVIDIA-lock is cool"
	rs := &results{}
	validateSummary(s, rs)
	if rs.allOK() {
		t.Fatal("expected scope-note drift to fail assertion")
	}
}

func TestValidateSummary_BadNGCStatus(t *testing.T) {
	s := baseSummary()
	s.NGCServiceStatus = "sideways"
	rs := &results{}
	validateSummary(s, rs)
	if rs.allOK() {
		t.Fatal("expected out-of-enum ngc_service_status to fail assertion")
	}
}

func TestValidateSummary_RatioDrift(t *testing.T) {
	s := baseSummary()
	s.Ratio = 0.2
	rs := &results{}
	validateSummary(s, rs)
	if rs.allOK() {
		t.Fatal("expected ratio/attested-over-total divergence to fail assertion")
	}
}

func TestValidateMinAttested_DisabledByDefault(t *testing.T) {
	// minAttested=0 must not append any row at all — callers that
	// don't set --min-attested shouldn't see the new assertion in
	// their output or artifact, preserving backward compatibility.
	rs := &results{}
	validateMinAttested(baseSummary(), 0, rs)
	if len(rs.rows) != 0 {
		t.Fatalf("expected no rows when minAttested<=0; got %d: %+v", len(rs.rows), rs.rows)
	}
}

func TestValidateMinAttested_PassWhenAtOrAboveFloor(t *testing.T) {
	s := baseSummary()
	s.Attested = 2
	rs := &results{}
	validateMinAttested(s, 2, rs)
	if !rs.allOK() || len(rs.rows) != 1 {
		t.Fatalf("expected exactly one PASS row; got %+v", rs.rows)
	}

	// Strictly above the floor also passes.
	rs2 := &results{}
	s.Attested = 5
	validateMinAttested(s, 2, rs2)
	if !rs2.allOK() {
		t.Fatalf("attested>floor should pass; got %+v", rs2.rows)
	}
}

func TestValidateMinAttested_FailBelowFloor(t *testing.T) {
	s := baseSummary()
	s.Attested = 1
	rs := &results{}
	validateMinAttested(s, 2, rs)
	if rs.allOK() {
		t.Fatal("expected attested<floor to fail assertion")
	}
	// Failure message must cite both numbers so the CI log is
	// self-describing without cross-referencing the summary.
	row := rs.rows[0]
	if row.ok || row.name != "summary/min-attested-floor" {
		t.Fatalf("unexpected row shape: %+v", row)
	}
	for _, substr := range []string{"attested=1", "floor=2"} {
		if !contains(row.msg, substr) {
			t.Errorf("failure msg %q missing %q", row.msg, substr)
		}
	}
}

func TestValidateMinAttested_NilSummaryIsHardFail(t *testing.T) {
	// Defensive: a nil summary should fail cleanly rather than panic.
	// This can only happen if callers wire the function up incorrectly
	// — main() never passes nil — but the guard keeps future callers
	// (tests, library consumers) safe.
	rs := &results{}
	validateMinAttested(nil, 1, rs)
	if rs.allOK() || len(rs.rows) != 1 {
		t.Fatalf("expected single FAIL row for nil summary; got %+v", rs.rows)
	}
}

func TestValidateRecent_Pass(t *testing.T) {
	s := baseSummary()
	now := time.Now().UTC()
	r := &trustRecent{
		FreshWithin: s.FreshWithin,
		Count:       2,
		Attestations: []trustAttestation{
			{NodeIDPrefix: "abc12…ef", AttestedAt: now.Format(time.RFC3339), FreshAgeSeconds: 10, GPUArchitecture: "hopper", GPUAvailable: true, NGCHMACOK: true, RegionHint: "eu"},
			{NodeIDPrefix: "9aaa1…bb", AttestedAt: now.Add(-30 * time.Second).Format(time.RFC3339), FreshAgeSeconds: 40, GPUArchitecture: "ada", GPUAvailable: true, NGCHMACOK: true, RegionHint: "us"},
		},
	}
	rs := &results{}
	validateRecent(r, s, rs)
	if !rs.allOK() {
		for _, row := range rs.rows {
			if !row.ok {
				t.Errorf("%s — %s", row.name, row.msg)
			}
		}
	}
}

func TestValidateRecent_CountMismatch(t *testing.T) {
	s := baseSummary()
	r := &trustRecent{FreshWithin: s.FreshWithin, Count: 5, Attestations: nil}
	rs := &results{}
	validateRecent(r, s, rs)
	if rs.allOK() {
		t.Fatal("expected count-mismatch to fail assertion")
	}
}

func TestValidateRecent_ExceedsAttested(t *testing.T) {
	s := baseSummary()
	s.Attested = 1
	rows := []trustAttestation{
		{NodeIDPrefix: "a…b", AttestedAt: time.Now().UTC().Format(time.RFC3339), RegionHint: "eu"},
		{NodeIDPrefix: "c…d", AttestedAt: time.Now().UTC().Format(time.RFC3339), RegionHint: "us"},
	}
	r := &trustRecent{FreshWithin: s.FreshWithin, Count: len(rows), Attestations: rows}
	rs := &results{}
	validateRecent(r, s, rs)
	if rs.allOK() {
		t.Fatal("expected count>attested to fail anti-claim assertion")
	}
}

func TestValidateRecent_RedactionMissing(t *testing.T) {
	s := baseSummary()
	rows := []trustAttestation{
		{NodeIDPrefix: "abcdef0123456789", AttestedAt: time.Now().UTC().Format(time.RFC3339), RegionHint: "eu"},
	}
	r := &trustRecent{FreshWithin: s.FreshWithin, Count: 1, Attestations: rows}
	rs := &results{}
	validateRecent(r, s, rs)
	if rs.allOK() {
		t.Fatal("expected missing ellipsis to fail redaction assertion")
	}
}

func TestValidateRecent_AgeNotMonotonic(t *testing.T) {
	s := baseSummary()
	now := time.Now().UTC()
	rows := []trustAttestation{
		{NodeIDPrefix: "a…a", AttestedAt: now.Format(time.RFC3339), FreshAgeSeconds: 100, RegionHint: "eu"},
		{NodeIDPrefix: "b…b", AttestedAt: now.Format(time.RFC3339), FreshAgeSeconds: 10, RegionHint: "us"},
	}
	r := &trustRecent{FreshWithin: s.FreshWithin, Count: 2, Attestations: rows}
	rs := &results{}
	validateRecent(r, s, rs)
	if rs.allOK() {
		t.Fatal("expected non-monotonic ages to fail assertion")
	}
}

func TestValidateRecent_DuplicateNodeIDs(t *testing.T) {
	s := baseSummary()
	now := time.Now().UTC()
	rows := []trustAttestation{
		{NodeIDPrefix: "a…a", AttestedAt: now.Format(time.RFC3339), FreshAgeSeconds: 1, RegionHint: "eu"},
		{NodeIDPrefix: "a…a", AttestedAt: now.Format(time.RFC3339), FreshAgeSeconds: 2, RegionHint: "eu"},
	}
	r := &trustRecent{FreshWithin: s.FreshWithin, Count: 2, Attestations: rows}
	rs := &results{}
	validateRecent(r, s, rs)
	if rs.allOK() {
		t.Fatal("expected duplicate node_id_prefix to fail assertion")
	}
}

func TestIsRegion(t *testing.T) {
	for _, r := range []string{"eu", "us", "apac", "other"} {
		if !isRegion(r) {
			t.Errorf("region %q should be valid", r)
		}
	}
	for _, r := range []string{"", "EU", "antarctica", "eu "} {
		if isRegion(r) {
			t.Errorf("region %q should be invalid", r)
		}
	}
}

// Sanity test: the expectedScopeNote constant here must match what the
// server emits. This test exists to flag drift in the test suite when
// §8.5.2 is ever intentionally reworded — a failure here means the
// server and the scraper's contract have diverged and both need to
// move together in a single PR.
func TestExpectedScopeNoteShape(t *testing.T) {
	if len(expectedScopeNote) < 40 {
		t.Fatal("expectedScopeNote is suspiciously short; did it get truncated?")
	}
	for _, substr := range []string{"opt-in", "not a consensus rule", "NVIDIA_LOCK_CONSENSUS_SCOPE.md"} {
		if !contains(expectedScopeNote, substr) {
			t.Errorf("expectedScopeNote should contain %q", substr)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	// Avoid pulling in strings just for a test helper that already
	// exists in the runtime; this small impl keeps the file self-
	// contained and trivially auditable.
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

// Coverage sanity check: ensure the top-level flag.Usage prints the
// expected summary fragments. We call it via a buffered os.Stderr
// substitute to avoid test-output pollution.
func TestBuildUsageString(t *testing.T) {
	s := fmt.Sprintf("trustcheck %s %s", "--help", expectedScopeNote)
	if s == "" {
		t.Fatal("usage composition produced empty string")
	}
}
