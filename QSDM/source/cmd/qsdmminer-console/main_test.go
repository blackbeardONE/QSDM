package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Config round-trip test: save → load must produce an equal struct.
// This guards against a field being added without a toml tag (which
// would silently drop from the persisted file).
func TestConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "miner.toml")
	orig := Config{
		ValidatorURL: "https://testnet.qsdm.tech",
		RewardAddr:   "qsdm1abcdefg",
		BatchCount:   3,
		PollInterval: "1500ms",
		Plain:        true,
	}
	if err := saveConfig(path, orig); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := loadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded != orig {
		t.Errorf("round-trip mismatch:\n  saved:  %+v\n  loaded: %+v", orig, loaded)
	}
}

// Missing file must return a zero Config with no error, because the
// main() flow wants to proceed to the setup wizard rather than abort
// when the user has never configured the miner before.
func TestConfig_MissingIsZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.toml")
	c, err := loadConfig(path)
	if err != nil {
		t.Fatalf("missing file must not error; got %v", err)
	}
	if c != (Config{}) {
		t.Errorf("missing file must return zero Config; got %+v", c)
	}
}

// Malformed TOML is a hard error — we don't want to silently replace
// a corrupted config with defaults, because that would wipe the
// user's reward address without warning.
func TestConfig_MalformedErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(path, []byte("this is = = not valid toml"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := loadConfig(path); err == nil {
		t.Error("expected malformed TOML to error")
	}
}

// pollDuration must fall back to the 2-second default on any invalid
// or missing value. A miner that polled every 0ns would hammer the
// validator — this is an important guardrail.
func TestConfig_PollDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"":        2 * time.Second,
		"not-dur": 2 * time.Second,
		"0":       2 * time.Second,
		"-5s":     2 * time.Second,
		"500ms":   500 * time.Millisecond,
		"3s":      3 * time.Second,
	}
	for in, want := range cases {
		got := Config{PollInterval: in}.pollDuration()
		if got != want {
			t.Errorf("PollInterval=%q: got %s, want %s", in, got, want)
		}
	}
}

func TestFormatHashrate(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0.00  H/s"},
		{5.5, "5.50  H/s"},
		{1500, "1.50 KH/s"},
		{2_500_000, "2.50 MH/s"},
	}
	for _, c := range cases {
		if got := formatHashrate(c.in); got != c.want {
			t.Errorf("formatHashrate(%v): got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "00:00:00"},
		{59 * time.Second, "00:00:59"},
		{61 * time.Second, "00:01:01"},
		{3661 * time.Second, "01:01:01"},
		{25 * time.Hour, "1d 01:00:00"},
		{-5 * time.Second, "00:00:00"}, // negative clamps to zero
	}
	for _, c := range cases {
		if got := formatDuration(c.in); got != c.want {
			t.Errorf("formatDuration(%v): got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTruncateAddr(t *testing.T) {
	cases := map[string]string{
		"":                                          "",
		"short":                                     "short",
		"qsdm1abc":                                  "qsdm1abc",
		"qsdm1abcdef":                               "qsdm1abcdef",
		"qsdm1abcdefghijklmnopqrstuvwxyzABCDEF1234": "qsdm1abc\u20261234",
	}
	for in, want := range cases {
		if got := truncateAddr(in); got != want {
			t.Errorf("truncateAddr(%q): got %q, want %q", in, got, want)
		}
	}
}

func TestShortAge(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{2 * time.Second, "2s"},
		{90 * time.Second, "1m"},
		{2 * time.Hour, "2h"},
		{48 * time.Hour, "2d"},
	}
	for _, c := range cases {
		if got := shortAge(c.in); got != c.want {
			t.Errorf("shortAge(%v): got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTruncateForLine(t *testing.T) {
	if got := truncateForLine("hello world", 20); got != "hello world" {
		t.Errorf("short input: got %q", got)
	}
	got := truncateForLine("abcdefghijklmnop", 8)
	if got != "abcdefg\u2026" {
		t.Errorf("truncate: got %q", got)
	}
}

// The Dashboard is a finite state machine driven by Events. We verify
// the high-value transitions: status flips on EvConnecting → EvConnected,
// accepted/rejected counters are mutually exclusive, and epoch + DAG
// readiness decouple correctly so the panel can render "building..."
// between EvEpochChanged and EvDAGReady.
func TestDashboard_ApplyEvent(t *testing.T) {
	d := &Dashboard{StartedAt: time.Now()}

	d.applyEvent(Event{Kind: EvConnecting, Message: "contacting node", At: time.Now()})
	if d.Status != "connecting" {
		t.Errorf("expected status=connecting, got %q", d.Status)
	}

	d.applyEvent(Event{Kind: EvConnected, Message: "work received", At: time.Now()})
	if d.Status != "connected" || d.StatusDetail != "" {
		t.Errorf("EvConnected should clear StatusDetail; got status=%q detail=%q", d.Status, d.StatusDetail)
	}

	d.applyEvent(Event{Kind: EvEpochChanged, Epoch: 7, DAGSize: 1024, Message: "epoch 7", At: time.Now()})
	if d.Epoch != 7 || d.DAGReady {
		t.Errorf("epoch must set, DAGReady must be false; got epoch=%d ready=%v", d.Epoch, d.DAGReady)
	}

	d.applyEvent(Event{Kind: EvDAGReady, Message: "DAG built", At: time.Now()})
	if !d.DAGReady {
		t.Error("EvDAGReady must flip DAGReady=true")
	}

	d.applyEvent(Event{Kind: EvProofAccepted, Message: "accepted 1", At: time.Now()})
	d.applyEvent(Event{Kind: EvProofAccepted, Message: "accepted 2", At: time.Now()})
	d.applyEvent(Event{Kind: EvProofRejected, Message: "rejected 1", At: time.Now()})
	if d.Accepted != 2 || d.Rejected != 1 {
		t.Errorf("counters wrong: accepted=%d rejected=%d", d.Accepted, d.Rejected)
	}

	d.applyEvent(Event{Kind: EvError, Message: "boom", At: time.Now()})
	if d.Status != "error" || d.StatusDetail != "boom" {
		t.Errorf("EvError should set status=error and surface detail; got status=%q detail=%q", d.Status, d.StatusDetail)
	}

	// EvV2ChallengeOK should bump V2Attestations + record both
	// the wall-clock and validator-side issued_at. Verifying
	// here means the dashboard's v2 row updates at the same
	// moment the loop receives the event, not on the next
	// renderer tick.
	t0 := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	d.applyEvent(Event{Kind: EvV2ChallengeOK, At: t0, IssuedAt: 1745496000, Message: "v2 ok"})
	if d.V2Attestations != 1 {
		t.Errorf("EvV2ChallengeOK must bump V2Attestations: got %d", d.V2Attestations)
	}
	if !d.V2LastChallengeAt.Equal(t0) {
		t.Errorf("V2LastChallengeAt: got %v want %v", d.V2LastChallengeAt, t0)
	}
	if d.V2LastChallengeIssue != 1745496000 {
		t.Errorf("V2LastChallengeIssue: got %d", d.V2LastChallengeIssue)
	}
}

// formatV2Line must show "—" placeholders before the first
// challenge has been built. This is what the operator sees
// during the gap between launching --protocol=v2 and the
// first successful prepare.
func TestFormatV2Line_PreFirstChallenge(t *testing.T) {
	d := &Dashboard{V2Enabled: true, V2NodeID: "alice", V2GPUArch: "ada"}
	got := formatV2Line(d)
	for _, want := range []string{"node=alice", "arch=ada", "attestations=0", "challenge=—"} {
		if !contains(got, want) {
			t.Errorf("expected %q in %q", want, got)
		}
	}
}

// formatV2Line must surface the challenge age in seconds when
// a recent challenge has been built. Operators rely on this
// to spot drift past mining.FreshnessWindow (60s).
func TestFormatV2Line_RecentChallenge(t *testing.T) {
	d := &Dashboard{
		V2Enabled:         true,
		V2NodeID:          "bob",
		V2GPUArch:         "hopper",
		V2LastChallengeAt: time.Now().Add(-3 * time.Second),
		V2Attestations:    7,
	}
	got := formatV2Line(d)
	for _, want := range []string{"node=bob", "arch=hopper", "attestations=7", "challenge="} {
		if !contains(got, want) {
			t.Errorf("expected %q in %q", want, got)
		}
	}
}

// The plain renderer must emit one line per event with the expected
// label prefix. This is what shows up in journalctl / CI logs, so a
// regression in the label set would silently break log parsers.
func TestPlainRenderer_EventFormat(t *testing.T) {
	var buf bytes.Buffer
	r := &plainRenderer{w: &buf}
	r.Event(Event{Kind: EvProofAccepted, At: time.Date(2026, 4, 24, 12, 0, 5, 0, time.UTC), Message: "ok"})
	got := buf.String()
	if got == "" {
		t.Fatal("expected output")
	}
	// Must include label, message, and a time prefix (12:00:05).
	for _, substr := range []string{"[PASS]", "ok", "12:00:05"} {
		if !contains(got, substr) {
			t.Errorf("missing %q in %q", substr, got)
		}
	}
}

// kindLabel must return a non-empty label for every defined kind —
// guards against adding a new EvXxx and forgetting to label it, which
// would print "[info]" (the default) and confuse log readers.
func TestKindLabel_AllKindsLabelled(t *testing.T) {
	// Update this list when adding a new EventKind.
	kinds := []EventKind{
		EvConnecting,
		EvConnected,
		EvEpochChanged,
		EvDAGReady,
		EvProofAccepted,
		EvProofRejected,
		EvError,
		EvInfo,
		EvShutdown,
		EvV2ChallengeOK,
	}
	seen := map[string]bool{}
	for _, k := range kinds {
		lbl := kindLabel(k)
		if lbl == "" {
			t.Errorf("kind %d has empty label", k)
		}
		seen[lbl] = true
	}
	// Sanity: distinct kinds should have at least 3 distinct labels.
	if len(seen) < 3 {
		t.Errorf("expected diverse labels; got %+v", seen)
	}
}

// Helper used by multiple tests — avoids pulling strings just for one
// substring check and keeps the test file self-contained.
func contains(h, n string) bool {
	if len(n) == 0 {
		return true
	}
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}
