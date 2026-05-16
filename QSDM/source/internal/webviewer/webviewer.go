package webviewer

import (
	"bufio"
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

// ErrInsecureDefaultCreds is returned by StartWebLogViewer when either
// WEBVIEWER_USERNAME or WEBVIEWER_PASSWORD is unset/empty and the
// operator has not explicitly opted into insecure defaults via
// QSDM_WEBVIEWER_ALLOW_DEFAULT_CREDS=1.
//
// Historically this package silently fell back to "admin" / "password"
// when the env vars were unset, which is a real foot-gun now that the
// repo is public: anyone who clones, builds, and runs the node without
// reading the docs ends up exposing their live log stream under
// trivially guessable credentials. Refusing to start (and letting the
// caller log + continue) is the conservative default.
var ErrInsecureDefaultCreds = errors.New("webviewer: WEBVIEWER_USERNAME and WEBVIEWER_PASSWORD must both be set; set QSDM_WEBVIEWER_ALLOW_DEFAULT_CREDS=1 to explicitly opt into insecure defaults (admin/password) for local development only")

// MaxTailLines caps the ?tail= query parameter to prevent an attacker
// (or a careless operator) from asking for an absurd line count that
// would dominate the per-request memory footprint. Lines past this
// cap are silently truncated; if the operator wants more, they should
// run `journalctl -u qsdm` on the box directly.
const MaxTailLines = 100_000

func basicAuth(username, password string, next http.HandlerFunc) http.HandlerFunc {
	expectedUser := []byte(username)
	expectedPass := []byte(password)
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(user), expectedUser) != 1 ||
			subtle.ConstantTimeCompare([]byte(pass), expectedPass) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// resolveCreds reads WEBVIEWER_USERNAME and WEBVIEWER_PASSWORD from the
// environment. If either is unset/empty it returns ErrInsecureDefaultCreds
// unless QSDM_WEBVIEWER_ALLOW_DEFAULT_CREDS is truthy, in which case it
// returns the historical admin/password defaults and logs a loud warning.
// Exposed in the package solely so tests can exercise the policy without
// booting an HTTP listener.
func resolveCreds() (username, password string, err error) {
	username = os.Getenv("WEBVIEWER_USERNAME")
	password = os.Getenv("WEBVIEWER_PASSWORD")
	if username != "" && password != "" {
		return username, password, nil
	}
	allow := os.Getenv("QSDM_WEBVIEWER_ALLOW_DEFAULT_CREDS")
	if allow == "1" || strings.EqualFold(allow, "true") || strings.EqualFold(allow, "yes") {
		log.Printf("[WEBVIEWER][WARN] using insecure default credentials admin/password because QSDM_WEBVIEWER_ALLOW_DEFAULT_CREDS is set; NEVER enable this in production")
		return "admin", "password", nil
	}
	return "", "", ErrInsecureDefaultCreds
}

// newMux builds the webviewer HTTP routing on a PRIVATE *http.ServeMux
// rather than the global http.DefaultServeMux. Two reasons:
//
//   - Defense-in-depth against handler bleed-over. Any package in the
//     qsdm binary that imports net/http/pprof or registers expvar.Publish
//     auto-registers debug handlers on http.DefaultServeMux. Before this
//     change the webviewer used DefaultServeMux, so a future debug-only
//     import would silently expose pprof on the webviewer's port — a
//     real production foot-gun that a binary-CVE scan would catch but a
//     pre-commit grep would not.
//
//   - Testability. A private mux can be ServeHTTP'd directly from
//     httptest.NewRecorder without binding a real socket, which is what
//     the routing / tail / unknown-path tests in webviewer_routing_test.go
//     do. The mux returned here is the exact mux the production listener
//     uses, so the tests pin the production behaviour byte-for-byte.
//
// Public-only-by-virtue-of-test-package — keep lower-case.
func newMux(logFile, username, password string) *http.ServeMux {
	mux := http.NewServeMux()

	// Root handler: serves the log file under the basic-auth gate. The
	// explicit path check is what stops /api/foobar / /thisdoesnotexist
	// etc. from also serving the log dump — Go's http.ServeMux treats
	// "/" as a catch-all prefix unless the handler itself enforces
	// equality, so we do.
	mux.HandleFunc("/", basicAuth(username, password, func(w http.ResponseWriter, r *http.Request) {
		// Hard 404 for unknown paths. The webviewer historically
		// served the log file for /every/path on the port (because
		// the "/" prefix matched everything); that meant an
		// authenticated client probing for e.g. /api/metrics
		// expecting Prometheus exposition would receive 31MB of
		// access logs instead and get very confused. Returning 404
		// here keeps the surface predictable: only "/", "/log",
		// "/view", and "/stream" return log content.
		if !isLogViewPath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		serveLog(w, r, logFile)
	}))

	// /stream: SSE log tail. Untouched behaviour-wise vs the prior
	// implementation; kept on the private mux for the same reasons.
	mux.HandleFunc("/stream", basicAuth(username, password, func(w http.ResponseWriter, r *http.Request) {
		serveLogStream(w, r, logFile)
	}))

	return mux
}

// isLogViewPath reports whether the given URL path is one of the
// well-known webviewer routes that should serve log content. Any other
// path returns 404. Kept as a separate function to make the contract
// explicit at the call sites + cheap to test in isolation.
func isLogViewPath(p string) bool {
	switch p {
	case "/", "/log", "/view":
		return true
	}
	return false
}

// serveLog writes the (filtered) log file content. Query knobs:
//   - level=<substring>    keep lines that contain the substring
//   - keyword=<substring>  AND keep lines that contain the substring
//   - tail=<N>             return only the LAST N matching lines
//     (capped at MaxTailLines; 0 / unset / invalid = no cap)
//
// The filters compose: tail is applied AFTER level+keyword so an
// operator asking for "last 200 ERROR-level lines mentioning audit-row"
// gets exactly that, not the last 200 of all lines followed by a
// filter.
func serveLog(w http.ResponseWriter, r *http.Request, logFile string) {
	levelFilter := r.URL.Query().Get("level")
	keywordFilter := r.URL.Query().Get("keyword")
	tail := parseTail(r.URL.Query().Get("tail"))

	file, err := os.Open(logFile)
	if err != nil {
		http.Error(w, "Failed to open log file", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	scanner := bufio.NewScanner(file)
	// Some QSDM log lines are dense JSON envelopes that can exceed the
	// default 64KiB bufio scan limit; bump to 1MiB so a single oversized
	// line doesn't truncate the response with bufio.ErrTooLong.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	if tail > 0 {
		// Tail mode: stream into a ring buffer so we never hold the
		// full file in memory. After scanning, write only the last N
		// matching lines.
		ring := make([]string, 0, tail)
		for scanner.Scan() {
			line := scanner.Text()
			if levelFilter != "" && !strings.Contains(line, levelFilter) {
				continue
			}
			if keywordFilter != "" && !strings.Contains(line, keywordFilter) {
				continue
			}
			if len(ring) < tail {
				ring = append(ring, line)
			} else {
				copy(ring, ring[1:])
				ring[len(ring)-1] = line
			}
		}
		for _, line := range ring {
			_, _ = w.Write([]byte(line + "\n"))
		}
		if err := scanner.Err(); err != nil {
			log.Printf("Error reading log file: %v", err)
		}
		return
	}

	for scanner.Scan() {
		line := scanner.Text()
		if levelFilter != "" && !strings.Contains(line, levelFilter) {
			continue
		}
		if keywordFilter != "" && !strings.Contains(line, keywordFilter) {
			continue
		}
		_, _ = w.Write([]byte(line + "\n"))
	}
	if err := scanner.Err(); err != nil {
		log.Printf("Error reading log file: %v", err)
	}
}

// parseTail interprets the tail= query value. Empty / non-numeric / <=0
// returns 0 (= no cap). Values above MaxTailLines clamp to MaxTailLines.
func parseTail(v string) int {
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0
	}
	if n > MaxTailLines {
		return MaxTailLines
	}
	return n
}

// serveLogStream is the SSE variant. Behaviour preserved from the
// pre-refactor implementation.
func serveLogStream(w http.ResponseWriter, r *http.Request, logFile string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	file, err := os.Open(logFile)
	if err != nil {
		http.Error(w, "Failed to open log file", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if _, err := w.Write([]byte("data: " + line + "\n\n")); err != nil {
			break
		}
		flusher.Flush()
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			// In a real implementation, read new lines appended to
			// the log file. For now, send an SSE heartbeat so the
			// client keeps the connection open.
			if _, err := w.Write([]byte("data: \n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// StartWebLogViewer boots the log-viewer HTTP listener on the given port.
// It returns an error and does NOT start the listener when credentials
// are unsafe (see ErrInsecureDefaultCreds); callers are expected to log
// the error and continue running the node without the viewer.
//
// The listener is bound to a PRIVATE *http.ServeMux (see newMux for the
// rationale); the global http.DefaultServeMux is not used.
func StartWebLogViewer(logFile string, port string) error {
	username, password, err := resolveCreds()
	if err != nil {
		return err
	}

	logger := &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    100, // megabytes
		MaxBackups: 7,
		MaxAge:     30,   // days
		Compress:   true, // compress rotated files
	}
	log.SetOutput(logger)

	mux := newMux(logFile, username, password)

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux, // private mux — NOT http.DefaultServeMux
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  15 * time.Second,
	}

	log.Printf("Starting web log viewer on http://localhost:%s (user=%q)\n", port, username)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Web log viewer failed: %v", err)
		}
	}()
	return nil
}
