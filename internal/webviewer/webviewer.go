package webviewer

import (
    "log"
    "net/http"
    "os"
    "time"
)

func StartWebLogViewer(logFile string, port string) {
    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        file, err := os.ReadFile(logFile)
        if err != nil {
            http.Error(w, "Failed to read log file", http.StatusInternalServerError)
            return
        }
        w.Header().Set("Content-Type", "text/plain; charset=utf-8")
        w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
        w.Header().Set("Pragma", "no-cache")
        w.Header().Set("Expires", "0")
        w.Write(file)
    })

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
