package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --auto-parents replaces parent_cells with the node's newest committed
// transactions before signing, so the signature covers them.
func TestWalletSignTx_AutoParents(t *testing.T) {
	path, address, pubHex := makeKeystoreFile(t)
	committed := []string{"solo-heartbeat-805400-1791611907090994535", "solo-heartbeat-805399-1791611897090994535"}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/chain/parents" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"tip": 805400, "parents": committed})
	}))
	defer server.Close()

	envIn := fmt.Sprintf(`{
		"id":"deadbeef00000009",
		"sender":%q,
		"recipient":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"amount":1.0,
		"fee":0.01,
		"geotag":"",
		"parent_cells":[],
		"nonce":3,
		"timestamp":"2026-10-10T07:00:00Z"
	}`, address)
	passFile := filepath.Join(t.TempDir(), "pass.txt")
	if err := os.WriteFile(passFile, []byte("test"), 0o600); err != nil {
		t.Fatalf("write passfile: %v", err)
	}

	out, stderr, err := runSignTx(t, envIn, []string{
		"--in", path,
		"--passphrase-file", passFile,
		"--envelope-file", "-",
		"--auto-parents",
		"--api-url", server.URL,
	})
	if err != nil {
		t.Fatalf("walletSignTx: %v", err)
	}
	if strings.Contains(stderr, "warning: parent_cells") {
		t.Fatalf("unexpected parent warning: %s", stderr)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode signed envelope: %v body=%s", err, out)
	}
	parents, _ := got["parent_cells"].([]interface{})
	if len(parents) != 2 || parents[0] != committed[0] || parents[1] != committed[1] {
		t.Fatalf("parent_cells = %v, want %v", got["parent_cells"], committed)
	}
	verifySignature(t, got, pubHex)
}

// Without --auto-parents the given parents are signed unchanged, with a
// warning when they could not pass the PoE rules once active.
func TestWalletSignTx_WarnsOnParentsPoEWouldRefuse(t *testing.T) {
	path, address, _ := makeKeystoreFile(t)
	envIn := fmt.Sprintf(`{"id":"deadbeef0000000a","sender":%q,
		"recipient":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"amount":1.0,"fee":0.01,"geotag":"","parent_cells":[],"timestamp":"2026-10-10T07:00:00Z"}`, address)
	passFile := filepath.Join(t.TempDir(), "pass.txt")
	if err := os.WriteFile(passFile, []byte("test"), 0o600); err != nil {
		t.Fatalf("write passfile: %v", err)
	}
	_, stderr, err := runSignTx(t, envIn, []string{"--in", path, "--passphrase-file", passFile, "--envelope-file", "-"})
	if err != nil {
		t.Fatalf("walletSignTx: %v", err)
	}
	if !strings.Contains(stderr, "--auto-parents") {
		t.Fatalf("expected a PoE warning, stderr=%s", stderr)
	}
}

// --auto-parents fails closed when the node cannot supply parents.
func TestWalletSignTx_AutoParentsFailsClosed(t *testing.T) {
	path, address, _ := makeKeystoreFile(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "warming up", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	envIn := fmt.Sprintf(`{"id":"deadbeef0000000b","sender":%q,
		"recipient":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"amount":1.0,"fee":0.01,"geotag":"","parent_cells":[],"timestamp":"2026-10-10T07:00:00Z"}`, address)
	passFile := filepath.Join(t.TempDir(), "pass.txt")
	if err := os.WriteFile(passFile, []byte("test"), 0o600); err != nil {
		t.Fatalf("write passfile: %v", err)
	}
	out, _, err := runSignTx(t, envIn, []string{"--in", path, "--passphrase-file", passFile, "--envelope-file", "-",
		"--auto-parents", "--api-url", server.URL})
	if err == nil || out != "" {
		t.Fatalf("expected failure without output, got err=%v out=%q", err, out)
	}
}
