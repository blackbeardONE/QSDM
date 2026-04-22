package tests

import (
	"bufio"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

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

	// The webviewer now refuses to boot with default admin/password creds
	// unless an explicit opt-in env var is set. Use the opt-in here so we
	// don't need to plumb real credentials through every test case.
	t.Setenv("QSDM_WEBVIEWER_ALLOW_DEFAULT_CREDS", "1")

	// Start the web log viewer server
	go func() {
		if err := webviewer.StartWebLogViewer(tmpfile.Name(), "8082"); err != nil {
			t.Errorf("StartWebLogViewer returned unexpected error: %v", err)
		}
	}()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Create HTTP client with basic auth (opt-in default: admin/password)
	client := &http.Client{}
	req, err := http.NewRequest("GET", "http://localhost:8082/?level=ERROR", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.SetBasicAuth("admin", "password")

	// Test filtering by level
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to get log with level filter: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	lines := []string{}
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "ERROR") {
		t.Errorf("Expected 1 ERROR line, got %v", lines)
	}

	// Test filtering by keyword
	req2, err := http.NewRequest("GET", "http://localhost:8082/?keyword=warning", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req2.SetBasicAuth("admin", "password")
	
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("Failed to get log with keyword filter: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", resp2.StatusCode)
	}

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
