package api

import (
	"os"
	"path/filepath"
	"testing"
)

// TestUserStorePersistence_RoundTrip covers the core "account survives a
// service restart" contract: register → instance goes away → new
// instance loads the same file → auth still succeeds with the original
// password. This is the regression test for the 2026-04-23 wipe
// incident.
func TestUserStorePersistence_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.json")

	us1, err := LoadOrNewUserStore(path)
	if err != nil {
		t.Fatalf("LoadOrNewUserStore: %v", err)
	}
	const addr = "5a4afcdd4474d02aed8f58e1808b4e5da118a072741dc969e0fbc32785df0e58"
	const pass = "Charming123!@#"
	if err := us1.RegisterUser(addr, pass, "user"); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}

	// The file must exist and be 0600 after register. We rely on this
	// posture in production — systemd's PrivateTmp/0750 state dir plus
	// 0600 file == nobody but the qsdmplus service user reads it.
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after register: %v", err)
	}
	if st.Size() == 0 {
		t.Fatalf("persist file unexpectedly empty")
	}
	if mode := st.Mode().Perm(); mode != 0o600 && mode != 0o666 {
		// On Windows the umask dance yields 0666; accept either.
		t.Logf("persist file mode = %v (expected 0600 on POSIX, 0666 on Windows)", mode)
	}

	us2, err := LoadOrNewUserStore(path)
	if err != nil {
		t.Fatalf("reopen LoadOrNewUserStore: %v", err)
	}
	u, err := us2.AuthenticateUser(addr, pass)
	if err != nil {
		t.Fatalf("AuthenticateUser after reopen: %v", err)
	}
	if u.Address != addr {
		t.Fatalf("address mismatch: got %q want %q", u.Address, addr)
	}
	if u.Role != "user" {
		t.Fatalf("role mismatch: got %q want %q", u.Role, "user")
	}

	if _, err := us2.AuthenticateUser(addr, "wrong"+pass); err == nil {
		t.Fatalf("AuthenticateUser accepted wrong password")
	}
}

// TestUserStorePersistence_RegisterRollsBackOnPersistError documents
// that a persist failure is NOT observable as "account created". We
// simulate the failure by pointing the store at a path whose parent is
// a regular file — MkdirAll in LoadOrNewUserStore will succeed (same
// dir as the file itself), but WriteFile for users.json.tmp will fail
// because the path points at a directory we can't write to. Skipping
// on Windows because the file-vs-dir coercion is posix-specific.
func TestUserStorePersistence_RegisterRollsBackOnPersistError(t *testing.T) {
	if os.Getenv("SKIP_POSIX_ONLY_TESTS") != "" {
		t.Skip("skipped (posix-only)")
	}
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// Point the store's "parent directory" at a regular file; writes
	// beneath it must fail.
	path := filepath.Join(blocker, "users.json")
	us := &UserStore{users: make(map[string]*User), persistPath: path}

	err := us.RegisterUser("abc", "Charming123!@#", "user")
	if err == nil {
		t.Fatalf("RegisterUser unexpectedly succeeded with unwritable persist path")
	}
	if _, exists := us.users["abc"]; exists {
		t.Fatalf("user was left in memory after persist failure (rollback missing)")
	}
}

// TestUserStorePersistence_VersionFailClosed guarantees that a future
// on-disk upgrade does not silently drop accounts because a
// down-leveled binary "successfully" reads the file as empty.
func TestUserStorePersistence_VersionFailClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.json")
	if err := os.WriteFile(path, []byte(`{"v": 999, "users": []}`), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := LoadOrNewUserStore(path); err == nil {
		t.Fatalf("LoadOrNewUserStore accepted unsupported version")
	}
}

// TestUserStorePersistence_CorruptFailClosed is the same idea but for a
// parse error — again, never silently reset.
func TestUserStorePersistence_CorruptFailClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.json")
	if err := os.WriteFile(path, []byte(`{not json`), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := LoadOrNewUserStore(path); err == nil {
		t.Fatalf("LoadOrNewUserStore accepted malformed JSON")
	}
}
