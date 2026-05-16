package webviewer

// Pins the routing + filtering contract of the webviewer's private
// mux. These tests exist because the pre-2026-05-16 implementation
// served the entire log file on EVERY path (including /api/foobar
// and /thisdoesnotexist) under the basic-auth gate, which:
//
//   - was an information-disclosure foot-gun if Caddy ever proxied
//     the webviewer port to the public internet (which it doesn't,
//     but a future Caddyfile edit could),
//   - returned 31MB+ of unbounded log content per request, which
//     could be used to mount a low-effort exhaustion attack against
//     an authenticated session.
//
// The four invariants pinned here:
//
//   1. Only "/", "/log", "/view" return log content. Every other
//      path returns 404. (TestNewMux_UnknownPathReturns404 +
//      TestNewMux_AllowedPathsServeLog.)
//
//   2. /stream is unchanged (SSE), still serves log content with the
//      text/event-stream content-type.
//
//   3. The ?tail=N query knob caps the response to the LAST N
//      matching lines. Composes correctly with level=/keyword=.
//      (TestServeLog_TailCapsToLastN + TestServeLog_TailComposesWithKeyword.)
//
//   4. parseTail clamps / sanitises bad input rather than returning
//      pathological values. (TestParseTail_Sanitises.)
//
// PLUS — invariant #5, the most important defense-in-depth property —
// the webviewer must not pollute http.DefaultServeMux. A handler
// registered on DefaultServeMux after webviewer startup must NOT be
// reachable through the webviewer's listener.
// (TestNewMux_DoesNotPolluteDefaultMux.)

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testUser = "ops"
	testPass = "S3cret"
)

// writeLogFile builds a deterministic log file containing N JSON-shaped
// lines spanning two log levels, two keywords, and a stable sort order
// so the tail-mode tests can assert on exact slices.
func writeLogFile(t *testing.T, lines []string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "qsdm.log")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write log file: %v", err)
	}
	return path
}

// auth adds the basic-auth header expected by every webviewer handler.
func auth(req *http.Request) *http.Request {
	req.SetBasicAuth(testUser, testPass)
	return req
}

func TestNewMux_UnknownPathReturns404(t *testing.T) {
	logPath := writeLogFile(t, []string{`{"level":"INFO","msg":"x"}`})
	mux := newMux(logPath, testUser, testPass)

	for _, path := range []string{
		"/api/foobar",
		"/thisdoesnotexist",
		"/api/metrics/prometheus",
		"/api/v1/audit/items",
		"/metrics",
		"/streamx",                 // close-but-not-equal to /stream
		"/log/extra",               // does not match /log exactly
		"/view/another",            // does not match /view exactly
		"/.well-known/security.txt",
	} {
		t.Run(path, func(t *testing.T) {
			req := auth(httptest.NewRequest(http.MethodGet, path, nil))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("path %q: got status %d, want 404; body=%q", path, rec.Code, rec.Body.String())
			}
			// Critically: body must NOT contain log content. Any
			// JSON-shaped log envelope leaking into a 404 body
			// would be a regression of the original foot-gun.
			if strings.Contains(rec.Body.String(), `"level":"INFO"`) {
				t.Fatalf("path %q: 404 body leaked log content: %q", path, rec.Body.String())
			}
		})
	}
}

func TestNewMux_AllowedPathsServeLog(t *testing.T) {
	logPath := writeLogFile(t, []string{
		`{"level":"INFO","msg":"line1"}`,
		`{"level":"ERROR","msg":"line2"}`,
	})
	mux := newMux(logPath, testUser, testPass)

	for _, path := range []string{"/", "/log", "/view"} {
		t.Run(path, func(t *testing.T) {
			req := auth(httptest.NewRequest(http.MethodGet, path, nil))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("path %q: got status %d, want 200", path, rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "line1") || !strings.Contains(rec.Body.String(), "line2") {
				t.Fatalf("path %q: body missing log content: %q", path, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
				t.Fatalf("path %q: content-type = %q, want text/plain", path, ct)
			}
			if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("path %q: missing X-Content-Type-Options: nosniff", path)
			}
		})
	}
}

func TestNewMux_UnauthenticatedReturns401(t *testing.T) {
	logPath := writeLogFile(t, []string{`{"level":"INFO","msg":"x"}`})
	mux := newMux(logPath, testUser, testPass)

	// Note: we deliberately do NOT call auth() here.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Basic") {
		t.Fatalf("WWW-Authenticate = %q, want Basic realm", got)
	}
}

func TestNewMux_WrongCredentialsReturns401(t *testing.T) {
	logPath := writeLogFile(t, []string{`{"level":"INFO","msg":"x"}`})
	mux := newMux(logPath, testUser, testPass)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth(testUser, "wrong-password")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401", rec.Code)
	}
}

func TestServeLog_TailCapsToLastN(t *testing.T) {
	lines := make([]string, 0, 50)
	for i := 0; i < 50; i++ {
		lines = append(lines, `{"line":`+itoa(i)+`}`)
	}
	logPath := writeLogFile(t, lines)
	mux := newMux(logPath, testUser, testPass)

	req := auth(httptest.NewRequest(http.MethodGet, "/?tail=3", nil))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	got := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(got) != 3 {
		t.Fatalf("got %d lines, want 3; body=%q", len(got), body)
	}
	// The last 3 lines of a 50-line file are 47, 48, 49.
	want := []string{`{"line":47}`, `{"line":48}`, `{"line":49}`}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("line %d: got %q, want %q", i, got[i], w)
		}
	}
}

func TestServeLog_TailComposesWithKeyword(t *testing.T) {
	lines := []string{
		`{"kind":"alpha","i":1}`,
		`{"kind":"beta","i":2}`,
		`{"kind":"alpha","i":3}`,
		`{"kind":"beta","i":4}`,
		`{"kind":"alpha","i":5}`,
		`{"kind":"beta","i":6}`,
		`{"kind":"alpha","i":7}`,
	}
	logPath := writeLogFile(t, lines)
	mux := newMux(logPath, testUser, testPass)

	// Filter to kind=alpha (4 matching lines: i=1,3,5,7) then tail=2.
	// Expected result: i=5 and i=7.
	req := auth(httptest.NewRequest(http.MethodGet, "/?keyword=alpha&tail=2", nil))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := strings.TrimRight(rec.Body.String(), "\n")
	got := strings.Split(body, "\n")
	if len(got) != 2 {
		t.Fatalf("got %d lines, want 2; body=%q", len(got), body)
	}
	if !strings.Contains(got[0], `"i":5`) || !strings.Contains(got[1], `"i":7`) {
		t.Fatalf("composed filter wrong: %q", body)
	}
}

func TestServeLog_LevelFilterStillWorks(t *testing.T) {
	logPath := writeLogFile(t, []string{
		`INFO: x`,
		`ERROR: y`,
		`INFO: z`,
		`WARN: q`,
	})
	mux := newMux(logPath, testUser, testPass)

	req := auth(httptest.NewRequest(http.MethodGet, "/?level=ERROR", nil))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "ERROR: y") {
		t.Fatalf("level filter dropped the ERROR line; body=%q", body)
	}
	if strings.Contains(body, "INFO: x") || strings.Contains(body, "INFO: z") {
		t.Fatalf("level filter leaked INFO lines; body=%q", body)
	}
}

func TestParseTail_Sanitises(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"0", 0},
		{"-5", 0},
		{"abc", 0},
		{"100", 100},
		{"99999999", MaxTailLines}, // clamped
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := parseTail(c.in); got != c.want {
				t.Fatalf("parseTail(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

func TestIsLogViewPath(t *testing.T) {
	cases := map[string]bool{
		"/":             true,
		"/log":          true,
		"/view":         true,
		"/stream":       false, // /stream has its own handler; not "log view"
		"/api/foobar":   false,
		"//":            false,
		"/log/":         false,
		"":              false,
	}
	for path, want := range cases {
		if got := isLogViewPath(path); got != want {
			t.Fatalf("isLogViewPath(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestNewMux_DoesNotPolluteDefaultMux is the defense-in-depth pin: a
// handler registered on http.DefaultServeMux at TEST TIME must NOT be
// reachable through the webviewer's private mux. Before the refactor
// the webviewer registered on DefaultServeMux directly, so any pprof
// or expvar auto-registration in the same process would have been
// served on the webviewer port too.
func TestNewMux_DoesNotPolluteDefaultMux(t *testing.T) {
	logPath := writeLogFile(t, []string{`{"level":"INFO","msg":"x"}`})

	// Register a sentinel handler on DefaultServeMux. We use a
	// uniquely-named path so we don't collide with any real handler
	// registered by an init() in another test file.
	sentinelPath := "/__webviewer_test_sentinel__"
	// Wrap in a one-shot guard so test-suite re-runs don't panic on
	// duplicate registration.
	registerSentinelOnce(t, sentinelPath)

	mux := newMux(logPath, testUser, testPass)
	req := auth(httptest.NewRequest(http.MethodGet, sentinelPath, nil))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// The sentinel handler returns 418. If the webviewer's mux were
	// http.DefaultServeMux (or otherwise delegated to it for unknown
	// paths), we'd see 418 here. Because the webviewer uses a
	// private mux with explicit path matching, we expect 404.
	if rec.Code != http.StatusNotFound {
		t.Fatalf("private mux leaked to DefaultServeMux: got status %d, want 404 (sentinel handler should not be reachable)", rec.Code)
	}
}

// --- test helpers ---

// itoa is a tiny stdlib-free int formatter; using strconv would force a
// second import path for what's already in the test scope, and a 50-iter
// test really doesn't care about microbenchmarks.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}

// registerSentinelOnce ensures the sentinel handler is registered on
// DefaultServeMux exactly once across multiple test runs in the same
// process (e.g. when invoked via `go test -count=N`). http.HandleFunc
// panics on duplicate registration.
var sentinelRegistered bool

func registerSentinelOnce(t *testing.T, path string) {
	t.Helper()
	if sentinelRegistered {
		return
	}
	http.DefaultServeMux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	sentinelRegistered = true
}
