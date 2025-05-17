package webviewer

import (
	"bufio"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

func basicAuth(username, password string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != username || pass != password {
			w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func StartWebLogViewer(logFile string, port string) {
	username := os.Getenv("WEBVIEWER_USERNAME")
	if username == "" {
		username = "admin"
	}
	password := os.Getenv("WEBVIEWER_PASSWORD")
	if password == "" {
		password = "password"
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

	log.Printf("Starting web log viewer on http://localhost:%s\n", port)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Web log viewer failed: %v", err)
		}
	}()
}
