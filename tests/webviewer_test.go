package tests

import (
    "bufio"
    "io/ioutil"
    "net/http"
    "net/http/httptest"
    "os"
    "strings"
    "testing"

    "github.com/blackbeardONE/QSDM/internal/webviewer"
)

func TestLogViewerFiltering(t *testing.T) {
    // Create a temporary log file
    tmpfile, err := ioutil.TempFile("", "testlog")
    if err != nil {
        t.Fatal(err)
    }
    defer os.Remove(tmpfile.Name())

    logContent := `INFO: This is an info message
WARN: This is a warning message
ERROR: This is an error message
INFO: Another info message
`
    if _, err := tmpfile.WriteString(logContent); err != nil {
        t.Fatal(err)
    }
    tmpfile.Close()

    // Start the web log viewer server
    go webviewer.StartWebLogViewer(tmpfile.Name(), "8081")

    // Test filtering by level
    resp, err := http.Get("http://localhost:8081/?level=ERROR")
    if err != nil {
        t.Fatalf("Failed to get log with level filter: %v", err)
    }
    defer resp.Body.Close()

    scanner := bufio.NewScanner(resp.Body)
    lines := []string{}
    for scanner.Scan() {
        lines = append(lines, scanner.Text())
    }
    if len(lines) != 1 || !strings.Contains(lines[0], "ERROR") {
        t.Errorf("Expected 1 ERROR line, got %v", lines)
    }

    // Test filtering by keyword
    resp2, err := http.Get("http://localhost:8081/?keyword=warning")
    if err != nil {
        t.Fatalf("Failed to get log with keyword filter: %v", err)
    }
    defer resp2.Body.Close()

    scanner2 := bufio.NewScanner(resp2.Body)
    found := false
    for scanner2.Scan() {
        if strings.Contains(scanner2.Text(), "warning") {
            found = true
            break
        }
    }
    if !found {
        t.Errorf("Expected to find a line with 'warning'")
    }
}
