package main

// watch_archspoof_test.go — pure-function and HTTP integration
// coverage for the archspoof / hashrate counter-diff watcher.
//
// The tests deliberately mirror watch_params_test.go in shape:
//
//   - flag normalisation (option struct only)
//   - URL derivation
//   - exposition parser
//   - diff core
//   - --once + --include-existing end-to-end via httptest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// -----------------------------------------------------------------------------
// flag normalisation
// -----------------------------------------------------------------------------

func TestArchSpoofOptions_Normalize_DefaultInterval(t *testing.T) {
	o := watchArchSpoofOptions{}
	if err := o.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if o.Interval != DefaultWatchInterval {
		t.Errorf("interval: got %v, want %v", o.Interval, DefaultWatchInterval)
	}
}

func TestArchSpoofOptions_Normalize_ClampsBelowMin(t *testing.T) {
	o := watchArchSpoofOptions{Interval: 1 * time.Second}
	if err := o.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if o.Interval != MinWatchInterval {
		t.Errorf("interval: got %v, want %v (clamped)", o.Interval, MinWatchInterval)
	}
}

func TestArchSpoofOptions_Normalize_RejectsBadReason(t *testing.T) {
	o := watchArchSpoofOptions{Reasons: map[string]bool{"bogus": true}}
	if err := o.normalize(); err == nil {
		t.Fatal("normalize accepted bogus reason filter")
	}
}

func TestArchSpoofOptions_Normalize_RejectsBadArch(t *testing.T) {
	o := watchArchSpoofOptions{Arches: map[string]bool{"volta": true}}
	if err := o.normalize(); err == nil {
		t.Fatal("normalize accepted bogus arch filter")
	}
}

func TestArchSpoofOptions_Normalize_AcceptsAllValidReasons(t *testing.T) {
	o := watchArchSpoofOptions{Reasons: map[string]bool{
		"unknown_arch":        true,
		"gpu_name_mismatch":   true,
		"cc_subject_mismatch": true,
	}}
	if err := o.normalize(); err != nil {
		t.Fatalf("normalize valid reasons: %v", err)
	}
}

func TestArchSpoofOptions_Normalize_AcceptsAllValidArches(t *testing.T) {
	o := watchArchSpoofOptions{Arches: map[string]bool{
		"ada":             true,
		"hopper":          true,
		"blackwell":       true,
		"blackwell_ultra": true,
		"rubin":           true,
		"rubin_ultra":     true,
		"unknown":         true,
	}}
	if err := o.normalize(); err != nil {
		t.Fatalf("normalize valid arches: %v", err)
	}
}

// -----------------------------------------------------------------------------
// CSV-set parser (used by --reason / --arch flags)
// -----------------------------------------------------------------------------

func TestParseCSVSet_Empty(t *testing.T) {
	if got := parseCSVSet(""); got != nil {
		t.Errorf("empty input: got %v, want nil", got)
	}
	if got := parseCSVSet("   "); got != nil {
		t.Errorf("whitespace input: got %v, want nil", got)
	}
}

func TestParseCSVSet_HappyPath(t *testing.T) {
	got := parseCSVSet("a,b,c")
	want := map[string]bool{"a": true, "b": true, "c": true}
	if len(got) != len(want) {
		t.Fatalf("len: got %d, want %d", len(got), len(want))
	}
	for k := range want {
		if !got[k] {
			t.Errorf("missing key %q", k)
		}
	}
}

func TestParseCSVSet_TrimsWhitespace(t *testing.T) {
	got := parseCSVSet("  a , b  , c  ")
	for _, want := range []string{"a", "b", "c"} {
		if !got[want] {
			t.Errorf("missing key %q in %v", want, got)
		}
	}
}

func TestParseCSVSet_DropsEmptyEntries(t *testing.T) {
	got := parseCSVSet("a,,b,")
	if len(got) != 2 || !got["a"] || !got["b"] {
		t.Errorf("got %v, want {a,b}", got)
	}
}

// -----------------------------------------------------------------------------
// metrics URL derivation
// -----------------------------------------------------------------------------

func TestDeriveMetricsURL_HappyPath(t *testing.T) {
	got, err := deriveMetricsURL("http://localhost:8080/api/v1")
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	want := "http://localhost:8080/api/metrics/prometheus"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDeriveMetricsURL_HappyPath_TrailingSlash(t *testing.T) {
	got, err := deriveMetricsURL("http://localhost:8080/api/v1/")
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	want := "http://localhost:8080/api/metrics/prometheus"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDeriveMetricsURL_RejectsMissingV1Suffix(t *testing.T) {
	_, err := deriveMetricsURL("http://localhost:8080/api/v2")
	if err == nil {
		t.Fatal("derive accepted non-v1 baseURL")
	}
	if !strings.Contains(err.Error(), "/api/v1") {
		t.Errorf("error msg should mention /api/v1: %v", err)
	}
}

// -----------------------------------------------------------------------------
// exposition parser
// -----------------------------------------------------------------------------

func TestParseArchSpoofExposition_HappyPath(t *testing.T) {
	body := []byte(`# HELP qsdm_attest_archspoof_rejected_total foo
# TYPE qsdm_attest_archspoof_rejected_total counter
qsdm_attest_archspoof_rejected_total{reason="unknown_arch"} 5
qsdm_attest_archspoof_rejected_total{reason="gpu_name_mismatch"} 2
qsdm_attest_archspoof_rejected_total{reason="cc_subject_mismatch"} 1
# HELP qsdm_attest_hashrate_rejected_total bar
# TYPE qsdm_attest_hashrate_rejected_total counter
qsdm_attest_hashrate_rejected_total{arch="hopper"} 3
qsdm_attest_hashrate_rejected_total{arch="blackwell"} 1
# unrelated metric — must be ignored
qsdm_some_other_metric{foo="bar"} 999
`)
	snap, err := parseArchSpoofExposition(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wantArch := map[string]uint64{
		"unknown_arch":        5,
		"gpu_name_mismatch":   2,
		"cc_subject_mismatch": 1,
	}
	for k, want := range wantArch {
		if got := snap.ArchSpoof[k]; got != want {
			t.Errorf("ArchSpoof[%q]: got %d, want %d", k, got, want)
		}
	}
	wantHr := map[string]uint64{
		"hopper":    3,
		"blackwell": 1,
	}
	for k, want := range wantHr {
		if got := snap.Hashrate[k]; got != want {
			t.Errorf("Hashrate[%q]: got %d, want %d", k, got, want)
		}
	}
}

func TestParseArchSpoofExposition_HandlesFloatValues(t *testing.T) {
	// Some Prometheus exporters emit "5" as "5.0"; we truncate
	// to floor since these are counters.
	body := []byte(`qsdm_attest_archspoof_rejected_total{reason="unknown_arch"} 5.0
qsdm_attest_hashrate_rejected_total{arch="hopper"} 3.7
`)
	snap, err := parseArchSpoofExposition(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if snap.ArchSpoof["unknown_arch"] != 5 {
		t.Errorf("ArchSpoof[unknown_arch]: got %d, want 5", snap.ArchSpoof["unknown_arch"])
	}
	if snap.Hashrate["hopper"] != 3 {
		t.Errorf("Hashrate[hopper]: got %d, want 3 (floor of 3.7)", snap.Hashrate["hopper"])
	}
}

func TestParseArchSpoofExposition_SkipsMalformedLines(t *testing.T) {
	body := []byte(`qsdm_attest_archspoof_rejected_total{reason="unknown_arch"} 5
this is not a metric line at all
qsdm_attest_archspoof_rejected_total{reason= 7
qsdm_attest_archspoof_rejected_total{reason="gpu_name_mismatch"} 2
qsdm_attest_archspoof_rejected_total{reason="cc_subject_mismatch"} -3
qsdm_attest_archspoof_rejected_total{} 4
`)
	snap, err := parseArchSpoofExposition(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if snap.ArchSpoof["unknown_arch"] != 5 {
		t.Errorf("ArchSpoof[unknown_arch]: got %d, want 5", snap.ArchSpoof["unknown_arch"])
	}
	if snap.ArchSpoof["gpu_name_mismatch"] != 2 {
		t.Errorf("ArchSpoof[gpu_name_mismatch]: got %d, want 2", snap.ArchSpoof["gpu_name_mismatch"])
	}
	if _, ok := snap.ArchSpoof["cc_subject_mismatch"]; ok {
		t.Error("negative-value line should have been dropped, not parsed")
	}
}

func TestParseArchSpoofExposition_EmptyInput(t *testing.T) {
	snap, err := parseArchSpoofExposition([]byte(""))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(snap.ArchSpoof) != 0 || len(snap.Hashrate) != 0 {
		t.Errorf("empty body: got %+v, want empty maps", snap)
	}
}

func TestParseArchSpoofExposition_NormalizesEmptyArchToUnknown(t *testing.T) {
	// Defensive: if the exporter ever emits arch="" we map it
	// to "unknown" so dashboards aren't surprised by a blank
	// label.
	body := []byte(`qsdm_attest_hashrate_rejected_total{arch=""} 7`)
	snap, err := parseArchSpoofExposition(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if snap.Hashrate["unknown"] != 7 {
		t.Errorf("Hashrate[unknown]: got %d, want 7", snap.Hashrate["unknown"])
	}
	if _, ok := snap.Hashrate[""]; ok {
		t.Error("empty arch should be remapped to unknown, not stored as empty key")
	}
}

// splitExpositionLine direct coverage

func TestSplitExpositionLine_NoLabels(t *testing.T) {
	name, labels, value, ok := splitExpositionLine("foo 42")
	if !ok || name != "foo" || value != 42 || len(labels) != 0 {
		t.Errorf("got name=%q labels=%v value=%d ok=%v", name, labels, value, ok)
	}
}

func TestSplitExpositionLine_WithLabels(t *testing.T) {
	name, labels, value, ok := splitExpositionLine(`foo{a="b",c="d"} 99`)
	if !ok || name != "foo" || value != 99 {
		t.Errorf("got name=%q value=%d ok=%v", name, value, ok)
	}
	if labels["a"] != "b" || labels["c"] != "d" {
		t.Errorf("labels: got %v", labels)
	}
}

func TestSplitExpositionLine_RejectsUnclosedBrace(t *testing.T) {
	_, _, _, ok := splitExpositionLine(`foo{a="b" 99`)
	if ok {
		t.Error("accepted unclosed brace")
	}
}

// -----------------------------------------------------------------------------
// diff core
// -----------------------------------------------------------------------------

func TestDiffArchSpoofSnapshots_NoChange_NoEvents(t *testing.T) {
	prev := archspoofSnapshot{
		ArchSpoof: map[string]uint64{"unknown_arch": 5},
		Hashrate:  map[string]uint64{"hopper": 3},
	}
	next := archspoofSnapshot{
		ArchSpoof: map[string]uint64{"unknown_arch": 5},
		Hashrate:  map[string]uint64{"hopper": 3},
	}
	got := diffArchSpoofSnapshots(prev, next, time.Now(), nil, nil)
	if len(got) != 0 {
		t.Errorf("no-change diff: got %d events, want 0: %+v", len(got), got)
	}
}

func TestDiffArchSpoofSnapshots_BurstOnReason(t *testing.T) {
	prev := archspoofSnapshot{
		ArchSpoof: map[string]uint64{"unknown_arch": 5},
		Hashrate:  map[string]uint64{},
	}
	next := archspoofSnapshot{
		ArchSpoof: map[string]uint64{"unknown_arch": 8},
		Hashrate:  map[string]uint64{},
	}
	ts := time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC)
	got := diffArchSpoofSnapshots(prev, next, ts, nil, nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d: %+v", len(got), got)
	}
	ev := got[0]
	if ev.Kind != WatchKindArchSpoofBurst {
		t.Errorf("kind: got %q", ev.Kind)
	}
	if ev.Reason != "unknown_arch" {
		t.Errorf("reason: got %q", ev.Reason)
	}
	if ev.DeltaCount != 3 {
		t.Errorf("delta: got %d, want 3", ev.DeltaCount)
	}
	if ev.TotalCount != 8 {
		t.Errorf("total: got %d, want 8", ev.TotalCount)
	}
	if !ev.Timestamp.Equal(ts) {
		t.Errorf("ts: got %v, want %v", ev.Timestamp, ts)
	}
}

func TestDiffArchSpoofSnapshots_BurstOnArch(t *testing.T) {
	prev := archspoofSnapshot{
		ArchSpoof: map[string]uint64{},
		Hashrate:  map[string]uint64{"hopper": 1},
	}
	next := archspoofSnapshot{
		ArchSpoof: map[string]uint64{},
		Hashrate:  map[string]uint64{"hopper": 4, "blackwell": 2},
	}
	got := diffArchSpoofSnapshots(prev, next, time.Now(), nil, nil)
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d: %+v", len(got), got)
	}
	// Sorted ASC: blackwell before hopper.
	if got[0].Arch != "blackwell" || got[0].DeltaCount != 2 || got[0].TotalCount != 2 {
		t.Errorf("event 0: %+v", got[0])
	}
	if got[1].Arch != "hopper" || got[1].DeltaCount != 3 || got[1].TotalCount != 4 {
		t.Errorf("event 1: %+v", got[1])
	}
}

func TestDiffArchSpoofSnapshots_CounterRollback_Silent(t *testing.T) {
	// Process restart drops the counter to zero; we must NOT
	// emit a synthetic burst on the way back up.
	prev := archspoofSnapshot{
		ArchSpoof: map[string]uint64{"unknown_arch": 100},
		Hashrate:  map[string]uint64{"hopper": 50},
	}
	next := archspoofSnapshot{
		ArchSpoof: map[string]uint64{"unknown_arch": 0},
		Hashrate:  map[string]uint64{"hopper": 0},
	}
	got := diffArchSpoofSnapshots(prev, next, time.Now(), nil, nil)
	if len(got) != 0 {
		t.Errorf("rollback: got %d events, want 0: %+v", len(got), got)
	}
}

func TestDiffArchSpoofSnapshots_RespectsReasonFilter(t *testing.T) {
	prev := archspoofSnapshot{
		ArchSpoof: map[string]uint64{"unknown_arch": 5, "gpu_name_mismatch": 0},
	}
	next := archspoofSnapshot{
		ArchSpoof: map[string]uint64{"unknown_arch": 8, "gpu_name_mismatch": 3},
	}
	filter := map[string]bool{"unknown_arch": true}
	got := diffArchSpoofSnapshots(prev, next, time.Now(), filter, nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 filtered event, got %d", len(got))
	}
	if got[0].Reason != "unknown_arch" {
		t.Errorf("filter mismatch: got %q", got[0].Reason)
	}
}

func TestDiffArchSpoofSnapshots_RespectsArchFilter(t *testing.T) {
	prev := archspoofSnapshot{
		Hashrate: map[string]uint64{"hopper": 1, "blackwell": 0},
	}
	next := archspoofSnapshot{
		Hashrate: map[string]uint64{"hopper": 5, "blackwell": 7},
	}
	filter := map[string]bool{"blackwell": true}
	got := diffArchSpoofSnapshots(prev, next, time.Now(), nil, filter)
	if len(got) != 1 {
		t.Fatalf("expected 1 filtered event, got %d: %+v", len(got), got)
	}
	if got[0].Arch != "blackwell" {
		t.Errorf("filter mismatch: got %q", got[0].Arch)
	}
}

// -----------------------------------------------------------------------------
// snapshot-as-initial-events (--include-existing path)
// -----------------------------------------------------------------------------

func TestArchSpoofSnapshotAsInitialEvents_OnlyNonZero(t *testing.T) {
	snap := archspoofSnapshot{
		ArchSpoof: map[string]uint64{
			"unknown_arch":        5,
			"gpu_name_mismatch":   0, // dropped
			"cc_subject_mismatch": 1,
		},
		Hashrate: map[string]uint64{
			"hopper": 3,
			"ada":    0, // dropped
		},
	}
	got := archspoofSnapshotAsInitialEvents(snap, time.Now(), nil, nil)
	// Want: 2 archspoof events + 1 hashrate event = 3 total.
	if len(got) != 3 {
		t.Fatalf("expected 3 events, got %d: %+v", len(got), got)
	}
	// Order: archspoof events sorted ASC, then hashrate.
	if got[0].Kind != WatchKindArchSpoofBurst || got[0].Reason != "cc_subject_mismatch" {
		t.Errorf("event 0: %+v", got[0])
	}
	if got[1].Kind != WatchKindArchSpoofBurst || got[1].Reason != "unknown_arch" {
		t.Errorf("event 1: %+v", got[1])
	}
	if got[2].Kind != WatchKindHashrateBurst || got[2].Arch != "hopper" {
		t.Errorf("event 2: %+v", got[2])
	}
}

// -----------------------------------------------------------------------------
// HTTP integration
// -----------------------------------------------------------------------------

// fakeMetricsServer hands out sequential exposition stages so a
// runWatchArchSpoof invocation can step through a deterministic
// counter timeline.
type fakeMetricsServer struct {
	srv    *httptest.Server
	stages []string
	idx    int
}

func newFakeMetricsServer(stages []string) *fakeMetricsServer {
	f := &fakeMetricsServer{stages: stages}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *fakeMetricsServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/metrics/prometheus" {
		http.NotFound(w, r)
		return
	}
	stage := f.stages[len(f.stages)-1]
	if f.idx < len(f.stages) {
		stage = f.stages[f.idx]
		f.idx++
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprint(w, stage)
}

func (f *fakeMetricsServer) Close()           { f.srv.Close() }
func (f *fakeMetricsServer) MetricsURL() string { return f.srv.URL + "/api/metrics/prometheus" }

func TestRunWatchArchSpoof_OnceMode_NoEvents(t *testing.T) {
	stages := []string{
		`qsdm_attest_archspoof_rejected_total{reason="unknown_arch"} 5
qsdm_attest_hashrate_rejected_total{arch="hopper"} 3
`,
	}
	srv := newFakeMetricsServer(stages)
	defer srv.Close()

	cli := &CLI{baseURL: srv.srv.URL + "/api/v1", client: &http.Client{Timeout: 5 * time.Second}}
	var stdout, stderr bytes.Buffer
	opts := watchArchSpoofOptions{
		Once:       true,
		MetricsURL: srv.MetricsURL(),
	}
	_ = opts.normalize()

	if err := cli.runWatchArchSpoof(context.Background(), opts, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout (no --include-existing): %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr: %q", stderr.String())
	}
}

func TestRunWatchArchSpoof_OnceMode_IncludeExisting(t *testing.T) {
	stages := []string{
		`qsdm_attest_archspoof_rejected_total{reason="unknown_arch"} 5
qsdm_attest_hashrate_rejected_total{arch="hopper"} 3
`,
	}
	srv := newFakeMetricsServer(stages)
	defer srv.Close()

	cli := &CLI{baseURL: srv.srv.URL + "/api/v1", client: &http.Client{Timeout: 5 * time.Second}}
	var stdout, stderr bytes.Buffer
	opts := watchArchSpoofOptions{
		Once:            true,
		IncludeExisting: true,
		JSON:            true,
		MetricsURL:      srv.MetricsURL(),
	}
	_ = opts.normalize()

	if err := cli.runWatchArchSpoof(context.Background(), opts, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSON lines, got %d:\n%s", len(lines), stdout.String())
	}
	// Ordering is archspoof first, then hashrate; assert wire shape.
	var ev0, ev1 WatchEvent
	if err := json.Unmarshal([]byte(lines[0]), &ev0); err != nil {
		t.Fatalf("unmarshal ev0: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &ev1); err != nil {
		t.Fatalf("unmarshal ev1: %v", err)
	}
	if ev0.Kind != WatchKindArchSpoofBurst || ev0.Reason != "unknown_arch" || ev0.TotalCount != 5 {
		t.Errorf("ev0: %+v", ev0)
	}
	if ev1.Kind != WatchKindHashrateBurst || ev1.Arch != "hopper" || ev1.TotalCount != 3 {
		t.Errorf("ev1: %+v", ev1)
	}
}

func TestRunWatchArchSpoof_InitialFailureIsFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "scrape secret required", http.StatusUnauthorized)
	}))
	defer srv.Close()

	cli := &CLI{baseURL: srv.URL + "/api/v1", client: &http.Client{Timeout: 5 * time.Second}}
	var stdout, stderr bytes.Buffer
	opts := watchArchSpoofOptions{
		Once:       true,
		MetricsURL: srv.URL + "/api/metrics/prometheus",
	}
	_ = opts.normalize()
	err := cli.runWatchArchSpoof(context.Background(), opts, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected initial-snapshot error")
	}
	if !strings.Contains(err.Error(), "initial snapshot") {
		t.Errorf("error msg: %v", err)
	}
}

func TestRunWatchArchSpoof_HumanFormat(t *testing.T) {
	stages := []string{
		`qsdm_attest_archspoof_rejected_total{reason="cc_subject_mismatch"} 7
`,
	}
	srv := newFakeMetricsServer(stages)
	defer srv.Close()

	cli := &CLI{baseURL: srv.srv.URL + "/api/v1", client: &http.Client{Timeout: 5 * time.Second}}
	var stdout, stderr bytes.Buffer
	opts := watchArchSpoofOptions{
		Once:            true,
		IncludeExisting: true,
		MetricsURL:      srv.MetricsURL(),
	}
	_ = opts.normalize()

	if err := cli.runWatchArchSpoof(context.Background(), opts, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"ARCHSPOOF_BURST", "reason=cc_subject_mismatch", "delta=+7", "total=7"} {
		if !strings.Contains(out, want) {
			t.Errorf("human output missing %q:\n%s", want, out)
		}
	}
}

// -----------------------------------------------------------------------------
// router wiring
// -----------------------------------------------------------------------------

func TestWatchCommand_Routes_Archspoof(t *testing.T) {
	// Routing is exercised by passing an empty arg list to
	// watchArchSpoof through the router. The flag.Parse on
	// nothing is a no-op; the URL derivation will then run
	// against the default baseURL ("") and surface a clear
	// derive error. We assert we hit THAT path (not the
	// "unknown subcommand" path) — this proves the router
	// dispatches.
	cli := &CLI{baseURL: "", client: &http.Client{Timeout: 5 * time.Second}}
	err := cli.watchCommand([]string{"archspoof", "--once"})
	if err == nil {
		t.Fatal("expected derivation / connection error")
	}
	if strings.Contains(err.Error(), "unknown watch subcommand") {
		t.Errorf("router did not dispatch archspoof: %v", err)
	}
}

func TestWatchCommand_UnknownSubcommandLists_Archspoof(t *testing.T) {
	cli := &CLI{}
	err := cli.watchCommand([]string{"nonsense"})
	if err == nil {
		t.Fatal("expected unknown-subcommand error")
	}
	if !strings.Contains(err.Error(), "archspoof") {
		t.Errorf("error message should advertise archspoof in known list: %v", err)
	}
}

// -----------------------------------------------------------------------------
// --detailed mode
// -----------------------------------------------------------------------------

// fakeRecentRejectionsServer hands out sequential page snapshots
// for /api/v1/attest/recent-rejections so the run loop can step
// through deterministic timelines.
type fakeRecentRejectionsServer struct {
	srv      *httptest.Server
	pages    []recentRejectionsPageWire
	served   int
	requests []string
}

func newFakeRecentRejectionsServer(pages []recentRejectionsPageWire) *fakeRecentRejectionsServer {
	f := &fakeRecentRejectionsServer{pages: pages}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *fakeRecentRejectionsServer) handle(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/api/v1/attest/recent-rejections") {
		http.NotFound(w, r)
		return
	}
	f.requests = append(f.requests, r.URL.RequestURI())

	page := recentRejectionsPageWire{Records: []recentRejectionWire{}}
	if f.served < len(f.pages) {
		page = f.pages[f.served]
		f.served++
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(page)
}

func (f *fakeRecentRejectionsServer) Close()        { f.srv.Close() }
func (f *fakeRecentRejectionsServer) BaseURL() string { return f.srv.URL + "/api/v1" }

func TestRunWatchArchSpoofDetailed_OnceMode_NoEvents(t *testing.T) {
	// Empty page on the first poll → no events, no error.
	srv := newFakeRecentRejectionsServer([]recentRejectionsPageWire{
		{Records: []recentRejectionWire{}},
	})
	defer srv.Close()

	cli := &CLI{baseURL: srv.BaseURL(), client: &http.Client{Timeout: 5 * time.Second}}
	var stdout, stderr bytes.Buffer
	opts := watchArchSpoofOptions{
		Once:     true,
		Detailed: true,
	}
	_ = opts.normalize()
	if err := cli.runWatchArchSpoofDetailed(context.Background(), opts, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout (no --include-existing): %q", stdout.String())
	}
}

func TestRunWatchArchSpoofDetailed_IncludeExisting_DrainsRing(t *testing.T) {
	t0 := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	srv := newFakeRecentRejectionsServer([]recentRejectionsPageWire{
		{
			Records: []recentRejectionWire{
				{Seq: 1, RecordedAt: t0, Kind: "archspoof_unknown_arch", Reason: "unknown_arch", Arch: "rubin", Height: 100, MinerAddr: "qsdm1a", Detail: "future-arch"},
				{Seq: 2, RecordedAt: t0.Add(time.Second), Kind: "hashrate_out_of_band", Arch: "hopper", Height: 101, MinerAddr: "qsdm1b"},
			},
			HasMore:    false,
			NextCursor: 2,
		},
	})
	defer srv.Close()

	cli := &CLI{baseURL: srv.BaseURL(), client: &http.Client{Timeout: 5 * time.Second}}
	var stdout, stderr bytes.Buffer
	opts := watchArchSpoofOptions{
		Once:            true,
		IncludeExisting: true,
		JSON:            true,
		Detailed:        true,
	}
	_ = opts.normalize()
	if err := cli.runWatchArchSpoofDetailed(context.Background(), opts, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSON lines, got %d:\n%s", len(lines), stdout.String())
	}
	var ev0, ev1 WatchEvent
	if err := json.Unmarshal([]byte(lines[0]), &ev0); err != nil {
		t.Fatalf("ev0: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &ev1); err != nil {
		t.Fatalf("ev1: %v", err)
	}
	if ev0.Kind != WatchKindArchSpoofRejection {
		t.Errorf("ev0 kind: got %q", ev0.Kind)
	}
	if ev0.Seq != 1 || ev0.MinerAddr != "qsdm1a" || ev0.Arch != "rubin" {
		t.Errorf("ev0 payload: %+v", ev0)
	}
	if ev1.Seq != 2 || ev1.Arch != "hopper" || ev1.Kind != WatchKindArchSpoofRejection {
		t.Errorf("ev1 payload: %+v", ev1)
	}
}

func TestRunWatchArchSpoofDetailed_503FailsLoudly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "v2 recent-rejections not configured", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	cli := &CLI{baseURL: srv.URL + "/api/v1", client: &http.Client{Timeout: 5 * time.Second}}
	var stdout, stderr bytes.Buffer
	opts := watchArchSpoofOptions{
		Once:     true,
		Detailed: true,
	}
	_ = opts.normalize()
	err := cli.runWatchArchSpoofDetailed(context.Background(), opts, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected fatal error on 503")
	}
	if !strings.Contains(err.Error(), "v1-only") && !strings.Contains(err.Error(), "503") {
		t.Errorf("error msg should hint at fallback: %v", err)
	}
}

func TestRunWatchArchSpoofDetailed_HumanFormat(t *testing.T) {
	t0 := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	srv := newFakeRecentRejectionsServer([]recentRejectionsPageWire{
		{
			Records: []recentRejectionWire{
				{
					Seq: 7, RecordedAt: t0, Kind: "archspoof_cc_subject_mismatch",
					Reason: "cc_subject_mismatch", Arch: "ada",
					Height: 9000, MinerAddr: "qsdm1critical",
					CertSubject: "NVIDIA H100 80GB",
					Detail:      "leaf cn contradicts claimed gpu_arch",
				},
			},
		},
	})
	defer srv.Close()

	cli := &CLI{baseURL: srv.BaseURL(), client: &http.Client{Timeout: 5 * time.Second}}
	var stdout, stderr bytes.Buffer
	opts := watchArchSpoofOptions{
		Once:            true,
		IncludeExisting: true,
		Detailed:        true,
	}
	_ = opts.normalize()
	if err := cli.runWatchArchSpoofDetailed(context.Background(), opts, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"ARCHSPOOF_REJECTION", "seq=7",
		"reason=cc_subject_mismatch", "arch=ada",
		"miner=qsdm1critical", "height=9000",
		"cert_cn=NVIDIA H100 80GB",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("human output missing %q:\n%s", want, out)
		}
	}
}

func TestBuildRecentRejectionsPath_NoFiltersNoCursor(t *testing.T) {
	got := buildRecentRejectionsPath(0, watchArchSpoofOptions{})
	if got != "/attest/recent-rejections" {
		t.Errorf("got %q", got)
	}
}

func TestBuildRecentRejectionsPath_WithCursor(t *testing.T) {
	got := buildRecentRejectionsPath(42, watchArchSpoofOptions{})
	if !strings.Contains(got, "cursor=42") {
		t.Errorf("missing cursor=42: %q", got)
	}
}

func TestBuildRecentRejectionsPath_SingleReasonForwarded(t *testing.T) {
	opts := watchArchSpoofOptions{Reasons: map[string]bool{"cc_subject_mismatch": true}}
	got := buildRecentRejectionsPath(0, opts)
	if !strings.Contains(got, "reason=cc_subject_mismatch") {
		t.Errorf("missing reason: %q", got)
	}
}

func TestBuildRecentRejectionsPath_SingleArchForwarded(t *testing.T) {
	opts := watchArchSpoofOptions{Arches: map[string]bool{"hopper": true}}
	got := buildRecentRejectionsPath(0, opts)
	if !strings.Contains(got, "arch=hopper") {
		t.Errorf("missing arch: %q", got)
	}
}

func TestBuildRecentRejectionsPath_MultipleReasonsNotForwarded(t *testing.T) {
	// Server only accepts one reason at a time. With a multi-
	// element set we deliberately drop the filter rather than
	// pick one — the watcher does client-side filtering on the
	// emitted events instead.
	opts := watchArchSpoofOptions{Reasons: map[string]bool{
		"unknown_arch": true, "gpu_name_mismatch": true,
	}}
	got := buildRecentRejectionsPath(0, opts)
	if strings.Contains(got, "reason=") {
		t.Errorf("multi-reason should not forward: %q", got)
	}
}
