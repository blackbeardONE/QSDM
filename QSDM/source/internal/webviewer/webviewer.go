package webviewer

import (
	"bufio"
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"os"
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

// StartWebLogViewer boots the log-viewer HTTP listener on the given port.
// It returns an error and does NOT start the listener when credentials
// are unsafe (see ErrInsecureDefaultCreds); callers are expected to log
// the error and continue running the node without the viewer.
func StartWebLogViewer(logFile string, port string) error {
	username, password, err := resolveCreds()
	if err != nil {
		return err
	}

	// Setup log rotation for the log file
	logger := &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    100, // megabytes
		MaxBackups: 7,
		MaxAge:     30,   // days
		Compress:   true, // compress rotated files
	}
	log.SetOutput(logger)

	http.HandleFunc("/", basicAuth(username, password, func(w http.ResponseWriter, r *http.Request) {
		levelFilter := r.URL.Query().Get("level")
		keywordFilter := r.URL.Query().Get("keyword")

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

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()
			if levelFilter != "" && !strings.Contains(line, levelFilter) {
				continue
			}
			if keywordFilter != "" && !strings.Contains(line, keywordFilter) {
				continue
			}
			w.Write([]byte(line + "\n"))
		}
		if err := scanner.Err(); err != nil {
			log.Printf("Error reading log file: %v", err)
		}
	}))

	http.HandleFunc("/stream", basicAuth(username, password, func(w http.ResponseWriter, r *http.Request) {
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

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()
			_, err := w.Write([]byte("data: " + line + "\n\n"))
			if err != nil {
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
				// In a real implementation, read new lines appended to the log file
				// For simplicity, just send a heartbeat
				_, err := w.Write([]byte("data: \n\n"))
				if err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}))

	srv := &http.Server{
		Addr:         ":" + port,
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
