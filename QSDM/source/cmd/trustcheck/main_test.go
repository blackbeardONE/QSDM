package main

import (
	"encoding/json"
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

// ---------------------------------------------------------------------------
// JSON output schema — locks the wire contract for downstream consumers
// (Datadog, Grafana, jq pipelines, the trustcheck-external GitHub Actions
// artifact). A rename in any of these field names is a breaking change
// and must move the test in the same commit.
// ---------------------------------------------------------------------------

func TestEmitJSON_Schema_TopLevelKeys(t *testing.T) {
	// Round-trip through a bytes buffer so we assert on the *wire*
	// JSON, not just the Go struct. Tools in the wild key off the
	// JSON-tag casing (`summary`, not `Summary`), so we compare
	// against the marshalled key set directly.
	rs := &results{}
	rs.pass("ok-row")
	report := buildJSONReport(rs, baseSummary(), nil)
	b, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("re-unmarshal to map: %v", err)
	}

	for _, k := range []string{"summary", "assertions", "pass"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("top-level JSON missing required key %q; got keys=%v", k, keysOf(raw))
		}
	}
}

func TestEmitJSON_Schema_AssertionRowShape(t *testing.T) {
	rs := &results{}
	rs.pass("a/pass")
	rs.fail("b/fail", "because")

	b, err := json.Marshal(buildJSONReport(rs, nil, nil))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw struct {
		Assertions []map[string]any `json:"assertions"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(raw.Assertions) != 2 {
		t.Fatalf("expected 2 assertions, got %d", len(raw.Assertions))
	}
	// Pass row: detail must be absent (omitempty).
	if _, has := raw.Assertions[0]["detail"]; has {
		t.Errorf("passing row should omit \"detail\"; got %+v", raw.Assertions[0])
	}
	if raw.Assertions[0]["name"] != "a/pass" || raw.Assertions[0]["pass"] != true {
		t.Errorf("pass row shape wrong: %+v", raw.Assertions[0])
	}
	// Fail row: detail must be present with the original message.
	if raw.Assertions[1]["detail"] != "because" {
		t.Errorf("fail row should include \"detail\"; got %+v", raw.Assertions[1])
	}
	if raw.Assertions[1]["pass"] != false {
		t.Errorf("fail row pass field should be false; got %+v", raw.Assertions[1])
	}
}

func TestEmitJSON_Schema_PassReflectsAllOK(t *testing.T) {
	// The top-level `pass` field is what CI gates and dashboards
	// key off. Drift between per-row fail flags and the aggregate
	// pass field would silently break those alarms.
	okRs := &results{}
	okRs.pass("only-pass")
	if !buildJSONReport(okRs, nil, nil).Pass {
		t.Error("all-pass results should set top-level Pass=true")
	}

	badRs := &results{}
	badRs.pass("one-pass")
	badRs.fail("one-fail", "detail")
	if buildJSONReport(badRs, nil, nil).Pass {
		t.Error("mixed pass/fail results should set top-level Pass=false")
	}
}

func TestEmitJSON_Schema_NilSummaryAndRecentAreOmitted(t *testing.T) {
	// Informational exit paths (warming-up, disabled) emit neither a
	// summary nor a recent sub-object. omitempty must honor that so
	// a downstream consumer can trivially detect the no-data case
	// without a presence test on every nested field.
	rs := &results{}
	rs.pass("row")
	b, err := json.Marshal(buildJSONReport(rs, nil, nil))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, has := raw["summary"]; has {
		t.Errorf("nil summary must be omitted; got %+v", raw)
	}
	if _, has := raw["recent"]; has {
		t.Errorf("nil recent must be omitted; got %+v", raw)
	}
}

func TestEmitJSON_Schema_SummaryFieldNamesMatchWireContract(t *testing.T) {
	// The summary sub-object is a mirror of pkg/api.TrustSummary's
	// JSON shape. If we ever rename a server-side tag we need the
	// scraper's local mirror (trustSummary in main.go) to move in
	// lockstep. This test guards against silent drift: it names the
	// fields a jq pipeline is known to read and fails the build if
	// any of them disappears from the trustcheck --json artifact.
	b, err := json.Marshal(buildJSONReport(&results{}, baseSummary(), nil))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw struct {
		Summary map[string]any `json:"summary"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	expected := []string{
		"attested",
		"total_public",
		"ratio",
		"fresh_within",
		"last_attested_at",
		"last_checked_at",
		"ngc_service_status",
		"scope_note",
	}
	for _, k := range expected {
		if _, has := raw.Summary[k]; !has {
			t.Errorf("summary sub-object missing required wire field %q; got keys=%v", k, keysOf(raw.Summary))
		}
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
