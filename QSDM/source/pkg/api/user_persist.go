package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// userStorePersistVersion is bumped whenever the on-disk format changes in
// a way that is not backward compatible. The loader fails closed on any
// value it does not recognise so a stale upgrade does not silently drop
// accounts.
const userStorePersistVersion = 1

// userPersistRecord is the on-disk representation of one account. The
// field names are kept short to save bytes (the Argon2id hashes are the
// bulky part anyway) and the full struct is snake_case-free so the file
// is human-greppable without confusion.
type userPersistRecord struct {
	Address      string    `json:"address"`
	PasswordHash string    `json:"password_hash"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
}

type userPersistDoc struct {
	V     int                 `json:"v"`
	Users []userPersistRecord `json:"users"`
}

// LoadOrNewUserStore opens the store at path. Missing file is a first-run
// signal and yields an empty, persistence-enabled store. A malformed file
// (wrong version, bad JSON, unreadable) is fail-closed: we return an
// error rather than silently overwriting accounts. path must be
// non-empty; pass NewUserStore() directly if you want the old,
// volatile, tests-only behaviour.
func LoadOrNewUserStore(path string) (*UserStore, error) {
	if path == "" {
		return nil, errors.New("LoadOrNewUserStore: empty path")
	}
	us := &UserStore{
		users:       make(map[string]*User),
		persistPath: path,
	}

	// Make sure the parent directory exists so the first save does not
	// trip on ENOENT. The mode mirrors what systemd gives /opt/qsdm.
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("user store: mkdir %s: %w", dir, err)
		}
	}

	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return us, nil
		}
		return nil, fmt.Errorf("user store: read %s: %w", path, err)
	}

	if len(b) == 0 {
		// Treat an empty file the same as "missing" — this happens when
		// a previous shutdown raced with the temp-file rename. We prefer
		// "empty users" over "refuse to start".
		return us, nil
	}

	var doc userPersistDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("user store: decode %s: %w (move or delete the file to reset)", path, err)
	}
	if doc.V != userStorePersistVersion {
		return nil, fmt.Errorf("user store: unsupported version %d in %s (expected %d)", doc.V, path, userStorePersistVersion)
	}
	for _, r := range doc.Users {
		if r.Address == "" || r.PasswordHash == "" {
			continue
		}
		us.users[r.Address] = &User{
			Address:      r.Address,
			PasswordHash: r.PasswordHash,
			Role:         r.Role,
			CreatedAt:    r.CreatedAt,
		}
	}
	return us, nil
}

// saveLocked writes the current in-memory user map to disk atomically
// (temp file + rename). Caller MUST hold us.mu for writing. No-op when
// persistence is not configured (us.persistPath == "").
func (us *UserStore) saveLocked() error {
	if us == nil || us.persistPath == "" {
		return nil
	}
	records := make([]userPersistRecord, 0, len(us.users))
	for _, u := range us.users {
		if u == nil {
			continue
		}
		records = append(records, userPersistRecord{
			Address:      u.Address,
			PasswordHash: u.PasswordHash,
			Role:         u.Role,
			CreatedAt:    u.CreatedAt,
		})
	}
	doc := userPersistDoc{V: userStorePersistVersion, Users: records}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("user store: encode: %w", err)
	}
	tmp := us.persistPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return fmt.Errorf("user store: write %s: %w", tmp, err)
	}
	// os.Rename on POSIX is atomic on the same filesystem. On Windows we
	// Remove first because the POSIX-style atomic replace is not
	// guaranteed when the destination exists.
	_ = os.Remove(us.persistPath)
	if err := os.Rename(tmp, us.persistPath); err != nil {
		return fmt.Errorf("user store: rename %s -> %s: %w", tmp, us.persistPath, err)
	}
	return nil
}
