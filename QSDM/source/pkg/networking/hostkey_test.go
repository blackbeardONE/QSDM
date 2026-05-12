package networking

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/logging"
	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestLoadOrCreateHostKey_EmptyPath(t *testing.T) {
	t.Parallel()
	k, err := loadOrCreateHostKey("")
	if err != nil {
		t.Fatalf("empty path should not error, got %v", err)
	}
	if k != nil {
		t.Fatalf("empty path should return nil key, got %T", k)
	}

	k, err = loadOrCreateHostKey("   \n\t  ")
	if err != nil {
		t.Fatalf("whitespace-only path should be treated as empty, got %v", err)
	}
	if k != nil {
		t.Fatalf("whitespace-only path should return nil key, got %T", k)
	}
}

func TestLoadOrCreateHostKey_CreateThenReload(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "host_key")

	first, err := loadOrCreateHostKey(path)
	if err != nil {
		t.Fatalf("first load (create) failed: %v", err)
	}
	if first == nil {
		t.Fatalf("expected newly-generated key, got nil")
	}
	if got := first.Type(); got != libp2pcrypto.Ed25519 {
		t.Fatalf("expected Ed25519 key by default, got type %v", got)
	}

	// File on disk must exist, be 0600 (mode bits — skipped on Windows
	// where filesystem permissions don't follow Unix octal semantics
	// and `os.WriteFile(..., 0o600)` does not produce 0o600 mode bits
	// in `os.Stat` results), and decode back to the same key.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after create: %v", err)
	}
	if runtime.GOOS != "windows" {
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("host key file mode = %o; want 0600", mode)
		}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if _, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw))); err != nil {
		t.Errorf("on-disk file is not valid base64: %v", err)
	}

	// Second call must return the *same* key (same marshalled bytes,
	// same peer.ID) — that is the whole point of the file.
	second, err := loadOrCreateHostKey(path)
	if err != nil {
		t.Fatalf("second load failed: %v", err)
	}
	firstID, err := peer.IDFromPrivateKey(first)
	if err != nil {
		t.Fatalf("peer.IDFromPrivateKey(first): %v", err)
	}
	secondID, err := peer.IDFromPrivateKey(second)
	if err != nil {
		t.Fatalf("peer.IDFromPrivateKey(second): %v", err)
	}
	if firstID != secondID {
		t.Fatalf("peer.ID changed across reload: %s -> %s", firstID, secondID)
	}
}

func TestLoadOrCreateHostKey_CorruptedFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "host_key")

	cases := []struct {
		name    string
		content string
		wantSub string // expected error substring (lowercased)
	}{
		{"empty file", "", "empty"},
		{"only whitespace", "   \n\t  \n", "empty"},
		{"not base64", "not-base-64-at-all !!!", "not valid base64"},
		{"valid base64 but not a key", base64.StdEncoding.EncodeToString([]byte("hello world, definitely not a libp2p private key")), "not a valid libp2p private key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatalf("seed file: %v", err)
			}
			_, err := loadOrCreateHostKey(path)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.wantSub) {
				t.Errorf("error %q does not mention %q", err.Error(), tc.wantSub)
			}
			// The error must mention the path so an operator can grep
			// systemd journals for it. Errors are rendered with %q, which
			// double-escapes Windows backslashes ("C:\\path\\..."), so
			// substring-matching against the raw OS-native path fails on
			// Windows. Comparing against the filename is robust on every
			// platform and still meets the operator-debugging intent.
			if !strings.Contains(err.Error(), filepath.Base(path)) {
				t.Errorf("error %q does not mention path basename %q (operator needs the path to debug)", err.Error(), filepath.Base(path))
			}
		})
	}
}

func TestLoadOrCreateHostKey_MissingParent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "definitely-not-a-real-subdir", "host_key")

	_, err := loadOrCreateHostKey(path)
	if err == nil {
		t.Fatalf("expected error when parent directory is missing")
	}
	if !strings.Contains(err.Error(), "parent") {
		t.Errorf("error %q should mention the missing parent directory", err.Error())
	}
}

func TestLoadOrCreateHostKey_PathIsDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := loadOrCreateHostKey(dir); err == nil {
		t.Fatalf("expected error when path is a directory, got nil")
	} else if !strings.Contains(err.Error(), "directory") {
		t.Errorf("error %q should mention 'directory'", err.Error())
	}
}

func TestSetupLibP2PWithPortAndKey_StableIdentity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "host_key")
	logger := logging.NewSilentLogger()

	first, err := SetupLibP2PWithPortAndKey(context.Background(), logger, 0, path)
	if err != nil {
		t.Fatalf("first host setup failed: %v", err)
	}
	firstID := first.Host.ID().String()
	// Close the first host before dialing a second on the same identity
	// so the new host doesn't see a duplicate-listener / port-conflict
	// from a still-running peer (only matters on a fixed port, but
	// belt-and-braces).
	_ = first.Host.Close()

	second, err := SetupLibP2PWithPortAndKey(context.Background(), logger, 0, path)
	if err != nil {
		t.Fatalf("second host setup failed: %v", err)
	}
	secondID := second.Host.ID().String()
	_ = second.Host.Close()

	if firstID != secondID {
		t.Fatalf("libp2p peer.ID changed across simulated restart with persisted key:\n  first  = %s\n  second = %s", firstID, secondID)
	}
	if !strings.HasPrefix(firstID, "12D3KooW") {
		// Ed25519-derived peer.IDs always start with 12D3KooW. If a
		// future libp2p version changes that prefix we want to
		// catch it explicitly rather than silently accept a different
		// shape.
		t.Errorf("peer.ID %q does not look like an Ed25519-derived libp2p ID (expected 12D3KooW prefix)", firstID)
	}
}

func TestSetupLibP2PWithPortAndKey_EmptyPathStaysEphemeral(t *testing.T) {
	t.Parallel()
	logger := logging.NewSilentLogger()

	first, err := SetupLibP2PWithPortAndKey(context.Background(), logger, 0, "")
	if err != nil {
		t.Fatalf("first host setup failed: %v", err)
	}
	firstID := first.Host.ID().String()
	_ = first.Host.Close()

	second, err := SetupLibP2PWithPortAndKey(context.Background(), logger, 0, "")
	if err != nil {
		t.Fatalf("second host setup failed: %v", err)
	}
	secondID := second.Host.ID().String()
	_ = second.Host.Close()

	if firstID == secondID {
		t.Fatalf("empty hostKeyPath should NOT pin identity across calls; got same peer.ID twice = %s (this means the legacy ephemeral-key behaviour is broken)", firstID)
	}
}
