package tests

import (
	"bufio"
	"io/ioutil"
	"net"
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

	// Bind an ephemeral port rather than a fixed one. A hardcoded :8082 made
	// this test order- and parallelism-dependent: it passed 3/3 in isolation
	// while failing inside a full `go test ./...` run, because anything else
	// holding that port fails the listen.
	port := freeLocalPort(t)

	// Report a startup failure through a channel. Calling t.Errorf from a
	// goroutine that outlives the test is a data race, and it reported the
	// error against whichever test happened to be running.
	startErr := make(chan error, 1)
	go func() {
		startErr <- webviewer.StartWebLogViewer(tmpfile.Name(), port)
	}()

	// Poll for readiness instead of sleeping a fixed 100ms. Under a loaded
	// machine -- exactly the case in a full parallel suite -- the server was
	// not always listening within that window.
	base := "http://127.0.0.1:" + port
	waitForServer(t, base, startErr)

	// Create HTTP client with basic auth (opt-in default: admin/password)
	client := &http.Client{}
	req, err := http.NewRequest("GET", base+"/?level=ERROR", nil)
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
	req2, err := http.NewRequest("GET", base+"/?keyword=warning", nil)
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

// freeLocalPort reserves an ephemeral port and releases it for the caller to
// bind. A small reuse window remains, but it is bounded and far safer than a
// fixed port shared with every other test and with anything already running on
// the developer's machine.
func freeLocalPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a local port: %v", err)
	}
	defer ln.Close()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("parse reserved port: %v", err)
	}
	return port
}

// waitForServer blocks until the viewer answers or the deadline passes, and
// fails immediately if startup already returned an error.
func waitForServer(t *testing.T, base string, startErr <-chan error) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		// StartWebLogViewer launches the listener in its own goroutine and
		// returns nil straight away, so a nil here means "started", not
		// "finished". Only a non-nil error is a startup failure; keep polling
		// either way until the port actually answers.
		select {
		case err := <-startErr:
			if err != nil {
				t.Fatalf("StartWebLogViewer failed: %v", err)
			}
		default:
		}
		req, err := http.NewRequest("GET", base+"/", nil)
		if err != nil {
			t.Fatalf("build readiness request: %v", err)
		}
		req.SetBasicAuth("admin", "password")
		resp, err := (&http.Client{Timeout: time.Second}).Do(req)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("web log viewer did not start listening on %s within 10s", base)
}
