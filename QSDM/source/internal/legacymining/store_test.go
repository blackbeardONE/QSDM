package legacymining

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
)

var (
	stMiner = strings.Repeat("ab", 32)
	stNode  = "canary-node-1"
	stCfg1  = ConfigHash{0xc1}
	stCfg2  = ConfigHash{0xc2}
	stMeta  = Meta{H0: 101, GenesisHash: "genesis-hash", Funder: "qsdm-system-funder", Release: "hardened-legacy-test-d7ffcd4-hl1", CreatedNS: 7}
	stClock = time.Unix(1_790_000_000, 42)
)

// stDir returns a fresh <tmp>/legacy-mining directory with mode 0700.
func stDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), LegacyDirName)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { // independent of the umask
		t.Fatal(err)
	}
	return dir
}

func stPath(dir string) string { return filepath.Join(dir, DBFile) }

func stNewStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s := NewSQLiteStore()
	s.now = func() time.Time { return stClock }
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// stCreate creates a DB in dir with stMeta and opens it.
func stCreate(t *testing.T, dir string) *SQLiteStore {
	t.Helper()
	s := stNewStore(t)
	m := stMeta
	if err := s.Open(stPath(dir), &m); err != nil {
		t.Fatalf("create: %v", err)
	}
	return s
}

// stReopen closes s (a simulated restart) and opens the same DB again.
func stReopen(t *testing.T, s *SQLiteStore, dir string) *SQLiteStore {
	t.Helper()
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	s2 := stNewStore(t)
	if err := s2.Open(stPath(dir), nil); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	return s2
}

func stWindow(t *testing.T, s *SQLiteStore, h ConfigHash, first uint64) {
	t.Helper()
	if _, err := s.ApplyReconcile(Reconciliation{Window: ConfigWindow{ConfigSHA256: h, FirstHeight: first}}); err != nil {
		t.Fatalf("window: %v", err)
	}
}

// stRecord builds a record from a real canonical v2 HMAC proof. seq selects
// the proof ID; the ID excludes the attestation, so attNonce does not change
// it.
func stRecord(t *testing.T, seq uint64, attNonce [32]byte) Record {
	t.Helper()
	p := mining.Proof{
		Version:    mining.ProtocolVersionV2,
		Height:     200 + seq%7,
		MinerAddr:  stMiner,
		BatchCount: 1,
		Attestation: mining.Attestation{
			Type:               mining.AttestationTypeHMAC,
			BundleBase64:       "YnVuZGxl",
			GPUArch:            "ada",
			ClaimedHashrateHPS: 1,
			Nonce:              attNonce,
			IssuedAt:           1_790_000_000,
		},
	}
	binary.BigEndian.PutUint64(p.Nonce[:8], seq)
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	id, err := p.ID()
	if err != nil {
		t.Fatal(err)
	}
	return Record{
		ProofID:      id,
		MinerAddr:    p.MinerAddr,
		NodeID:       stNode,
		AttNonce:     attNonce,
		WorkHeight:   p.Height,
		AcceptTip:    p.Height + 1,
		AcceptedNS:   int64(1_000 + seq),
		ConfigSHA256: stCfg1,
		ProofJSON:    raw,
	}
}

func stNonce(b byte) [32]byte { return [32]byte{0: 0x4e, 31: b} }

// stStep8 is the §4.1 step 8 caller contract: a *Rejection is its HTTP class
// (400 for UNIQUE hits, which count toward the §6.6 duplicate trigger); any
// other error trips FREEZE and returns 503.
func stStep8(err error) (status int, freeze, counted bool) {
	if err == nil {
		return 200, false, false
	}
	if k := RejectKindOf(err); k != 0 {
		return k.HTTPStatus(), false, k == KindDuplicate || k == KindNonceConflict
	}
	return 503, true, false
}

func stWantReject(t *testing.T, err error, kind RejectKind, reason mining.RejectReason) {
	t.Helper()
	if got := RejectKindOf(err); got != kind {
		t.Fatalf("err = %v: kind %v, want %v", err, got, kind)
	}
	var re *mining.RejectError
	if !errors.As(err, &re) || re.Reason != reason {
		t.Fatalf("err = %v: want a *mining.RejectError with reason %q", err, reason)
	}
	if errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v: a UNIQUE hit must not be the 503 class", err)
	}
	if status, freeze, counted := stStep8(err); status != 400 || freeze || !counted {
		t.Fatalf("err = %v: step 8 gives status %d freeze %v counted %v, want 400 false true", err, status, freeze, counted)
	}
}

func stWantPlain(t *testing.T, err error) {
	t.Helper()
	if err == nil || RejectKindOf(err) != 0 {
		t.Fatalf("err = %v: want a non-Rejection error", err)
	}
	if status, freeze, _ := stStep8(err); status != 503 || !freeze {
		t.Fatalf("err = %v: step 8 gives %d freeze %v, want 503 and FREEZE", err, status, freeze)
	}
}

func stPendingIDs(t *testing.T, s *SQLiteStore) []ProofID {
	t.Helper()
	rs, err := s.Pending()
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]ProofID, len(rs))
	for i, r := range rs {
		ids[i] = r.ProofID
	}
	return ids
}

func stEventCount(t *testing.T, s *SQLiteStore, kind string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM events WHERE kind=?`, kind).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func stListDir(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

func stFileSum(t *testing.T, path string) [32]byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(b)
}

// stRawExec runs statements on path outside the Store, to build fixtures.
func stRawExec(t *testing.T, path string, stmts ...string) {
	t.Helper()
	db, err := storeOpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			_ = db.Close()
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func stWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStoreOpenCreateReopen(t *testing.T) {
	requireHLSQLite(t)
	dir := stDir(t)
	path := stPath(dir)
	s := stNewStore(t)

	// S7: absent DB, create nil: ErrDBMissing and nothing is created.
	if err := s.Open(path, nil); !errors.Is(err, ErrDBMissing) {
		t.Fatalf("Open(nil) on a missing DB = %v, want ErrDBMissing", err)
	}
	if names := stListDir(t, dir); len(names) != 0 {
		t.Fatalf("Open(nil) created %v", names)
	}

	// S8: create with meta. A stale temp file from an earlier crash does not
	// block creation, and creation leaves no temp file of its own.
	stale := filepath.Join(dir, TempPrefix+DBFile+"-1-0000000000000000")
	stWriteFile(t, stale, []byte("stale"))
	m := stMeta
	m.CreatedNS = 0
	if err := s.Open(path, &m); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := os.Remove(stale); err != nil {
		t.Fatal(err)
	}
	for _, name := range stListDir(t, dir) {
		if strings.HasPrefix(name, TempPrefix) || !strings.HasPrefix(name, DBFile) {
			t.Errorf("unexpected file %q after create", name)
		}
	}
	want := stMeta
	want.CreatedNS = stClock.UnixNano()
	if got, err := s.Meta(); err != nil || got != want {
		t.Fatalf("Meta = %+v, %v; want %+v", got, err, want)
	}
	if err := s.Open(path, nil); err == nil {
		t.Fatal("Open on an open store succeeded")
	}
	if legacyStorePOSIX {
		for name, perm := range map[string]os.FileMode{dir: 0o700, path: 0o600, path + "-wal": 0o600, path + "-shm": 0o600} {
			fi, err := os.Lstat(name)
			if err != nil || fi.Mode().Perm() != perm {
				t.Errorf("%s: %v %v, want perm %04o", name, fi.Mode(), err, perm)
			}
		}
	}

	// Close, then every method returns ErrClosed.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	var zero SQLiteStore
	for _, st := range []*SQLiteStore{s, &zero} {
		_, errMeta := st.Meta()
		_, errPending := st.Pending()
		_, errLookup := st.Lookup([]ProofID{{1}})
		_, errReconcile := st.ApplyReconcile(Reconciliation{Window: ConfigWindow{ConfigSHA256: stCfg1, FirstHeight: 1}})
		_, errCounters := st.Counters(stCfg1)
		for i, err := range []error{st.Close(), errMeta, st.Accept(Record{}), errPending, errLookup, st.MarkPaid(nil),
			errReconcile, errCounters, st.Event(Event{Kind: "k"})} {
			if !errors.Is(err, ErrClosed) {
				t.Errorf("closed store: call %d = %v, want ErrClosed", i, err)
			}
		}
	}

	// Reopen: same meta. Creating over an existing DB is refused and leaves it
	// unchanged.
	s2 := stNewStore(t)
	if err := s2.Open(path, nil); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got, err := s2.Meta(); err != nil || got != want {
		t.Fatalf("Meta after reopen = %+v, %v", got, err)
	}
	if err := s2.Close(); err != nil {
		t.Fatal(err)
	}
	before := stFileSum(t, path)
	m = stMeta
	if err := stNewStore(t).Open(path, &m); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("create over an existing DB = %v, want ErrUnsafePath", err)
	}
	if stFileSum(t, path) != before {
		t.Fatal("refused create changed the DB")
	}
}

func TestStoreCreateRefusals(t *testing.T) {
	// Invalid meta: nothing is created.
	dir := stDir(t)
	for _, m := range []Meta{
		{H0: 1, GenesisHash: "", Funder: "f"},
		{H0: 1, GenesisHash: "g", Funder: ""},
		{H0: 1 << 63, GenesisHash: "g", Funder: "f"},
	} {
		m := m
		if err := stNewStore(t).Open(stPath(dir), &m); err == nil {
			t.Errorf("create with meta %+v succeeded", m)
		}
		if names := stListDir(t, dir); len(names) != 0 {
			t.Fatalf("refused create left %v", names)
		}
	}

	// A sidecar without its DB (for example a WAL of a deleted DB) could be
	// replayed into a new DB, so creation refuses.
	for _, suffix := range storeSidecars {
		dir := stDir(t)
		stWriteFile(t, stPath(dir)+suffix, []byte("old"))
		m := stMeta
		if err := stNewStore(t).Open(stPath(dir), &m); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("create with a stale %s = %v, want ErrUnsafePath", suffix, err)
		}
		if _, err := os.Lstat(stPath(dir)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("create with a stale %s created the DB", suffix)
		}
		if err := stNewStore(t).Open(stPath(dir), nil); !errors.Is(err, ErrDBMissing) {
			t.Errorf("Open(nil) with a stale %s = %v, want ErrDBMissing", suffix, err)
		}
	}
}

func TestStorePathRefusals(t *testing.T) {
	requireHLSQLite(t)
	dir := stDir(t)
	s := stCreate(t, dir)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(filepath.Dir(dir), "not-legacy")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	asDir := filepath.Join(t.TempDir(), LegacyDirName)
	if err := os.Mkdir(asDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(asDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(stPath(asDir), 0o700); err != nil { // the DB path is a directory
		t.Fatal(err)
	}
	fileDir := filepath.Join(t.TempDir(), LegacyDirName)
	stWriteFile(t, fileDir, nil) // the legacy-mining "directory" is a file
	sep := string(os.PathSeparator)
	for _, p := range []string{
		filepath.Join(LegacyDirName, DBFile),              // relative
		dir + sep + "." + sep + DBFile,                    // not clean
		filepath.Join(dir, "other.db"),                    // wrong file name
		filepath.Join(other, DBFile),                      // wrong directory name
		filepath.Join(t.TempDir(), LegacyDirName, DBFile), // directory missing
		filepath.Join(fileDir, DBFile),                    // directory is a file
		stPath(asDir),                                     // DB is a directory
	} {
		for _, create := range []*Meta{nil, {H0: 1, GenesisHash: "g", Funder: "f"}} {
			if err := stNewStore(t).Open(p, create); !errors.Is(err, ErrUnsafePath) {
				t.Errorf("Open(%q, create=%v) = %v, want ErrUnsafePath", p, create != nil, err)
			}
		}
	}
	if _, err := os.Lstat(filepath.Join(other, DBFile)); !errors.Is(err, os.ErrNotExist) {
		t.Error("a refused path was created")
	}
}

func TestStorePermissionRefusals(t *testing.T) {
	if !legacyStorePOSIX {
		t.Skip("mode and owner checks run on Unix only")
	}
	dir := stDir(t)
	path := stPath(dir)
	s := stCreate(t, dir)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	stWriteFile(t, path+"-wal", nil)
	stWriteFile(t, path+"-shm", nil)
	cases := []struct {
		name string
		mode os.FileMode
		ok   os.FileMode
	}{
		{dir, 0o750, 0o700},
		{dir, 0o701, 0o700},
		{dir, 0o500, 0o700},
		{path, 0o640, 0o600},
		{path, 0o604, 0o600},
		{path, 0o400, 0o600},
		{path + "-wal", 0o644, 0o600},
		{path + "-shm", 0o660, 0o600},
	}
	for _, c := range cases {
		if err := os.Chmod(c.name, c.mode); err != nil {
			t.Fatal(err)
		}
		if err := stNewStore(t).Open(path, nil); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("%s mode %04o: Open = %v, want ErrUnsafePath", filepath.Base(c.name), c.mode, err)
		}
		if err := os.Chmod(c.name, c.ok); err != nil {
			t.Fatal(err)
		}
	}
	if err := stNewStore(t).Open(path, nil); err != nil {
		t.Fatalf("Open after restoring modes: %v", err)
	}

	// Owner: only testable as root, which can chown to another uid.
	if os.Geteuid() != 0 {
		t.Log("not root: owner refusal not exercised")
		return
	}
	for _, name := range []string{dir, path} {
		if err := os.Lchown(name, 4242, 4242); err != nil {
			t.Fatal(err)
		}
		if err := stNewStore(t).Open(path, nil); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("%s owned by another uid: Open = %v, want ErrUnsafePath", filepath.Base(name), err)
		}
		if err := os.Lchown(name, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStoreSymlinkRefusals(t *testing.T) {
	requireHLSQLite(t)
	realDir := stDir(t)
	realPath := stPath(realDir)
	s := stCreate(t, realDir)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	symlink := func(target, link string) {
		t.Helper()
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}

	// The legacy-mining directory is a symlink to a valid one.
	linkRoot := t.TempDir()
	symlink(realDir, filepath.Join(linkRoot, LegacyDirName))
	if err := stNewStore(t).Open(filepath.Join(linkRoot, LegacyDirName, DBFile), nil); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("symlinked directory: Open = %v, want ErrUnsafePath", err)
	}

	// The DB file is a symlink to a valid DB.
	dir2 := stDir(t)
	symlink(realPath, stPath(dir2))
	for _, create := range []*Meta{nil, {H0: 1, GenesisHash: "g", Funder: "f"}} {
		if err := stNewStore(t).Open(stPath(dir2), create); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("symlinked DB (create=%v): Open = %v, want ErrUnsafePath", create != nil, err)
		}
	}

	// A sidecar is a symlink.
	decoy := filepath.Join(t.TempDir(), "decoy")
	stWriteFile(t, decoy, nil)
	for _, suffix := range storeSidecars {
		symlink(decoy, realPath+suffix)
		if err := stNewStore(t).Open(realPath, nil); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("symlinked %s: Open = %v, want ErrUnsafePath", suffix, err)
		}
		if err := os.Remove(realPath + suffix); err != nil {
			t.Fatal(err)
		}
	}
	if err := stNewStore(t).Open(realPath, nil); err != nil {
		t.Fatalf("Open after removing the symlinks: %v", err)
	}
}

func TestStoreIdentityRefusals(t *testing.T) {
	requireHLSQLite(t)
	cases := []struct {
		name  string
		build func(t *testing.T, path string)
	}{
		{"empty file", func(t *testing.T, path string) { stWriteFile(t, path, nil) }},
		{"not sqlite", func(t *testing.T, path string) { stWriteFile(t, path, bytes.Repeat([]byte("x"), 4096)) }},
		{"application_id mismatch", func(t *testing.T, path string) {
			stWriteFile(t, path, nil)
			stRawExec(t, path, `PRAGMA application_id = 305419896`, `PRAGMA user_version = 1`, `CREATE TABLE t (x)`)
		}},
		{"user_version mismatch", func(t *testing.T, path string) {
			stCreateAt(t, path)
			stRawExec(t, path, `PRAGMA user_version = 2`)
		}},
		{"extra table", func(t *testing.T, path string) {
			stCreateAt(t, path)
			stRawExec(t, path, `CREATE TABLE extra (x)`)
		}},
		{"missing trigger", func(t *testing.T, path string) {
			stCreateAt(t, path)
			stRawExec(t, path, `DROP TRIGGER events_no_delete`)
		}},
		{"changed index", func(t *testing.T, path string) {
			stCreateAt(t, path)
			stRawExec(t, path, `DROP INDEX proofs_unpaid`, `CREATE INDEX proofs_unpaid ON proofs(accepted_ns, proof_id)`)
		}},
		{"missing meta row", func(t *testing.T, path string) {
			stCreateAt(t, path)
			// Same schema text, but the meta row is gone.
			stRawExec(t, path, `DROP TRIGGER meta_no_delete`, `DELETE FROM meta`, storeSchemaSQL(t, "meta_no_delete"))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := stDir(t)
			path := stPath(dir)
			c.build(t, path)
			before := stFileSum(t, path)
			if err := stNewStore(t).Open(path, nil); !errors.Is(err, ErrSchema) {
				t.Fatalf("Open = %v, want ErrSchema", err)
			}
			if c.name == "application_id mismatch" && stFileSum(t, path) != before {
				t.Error("a foreign DB was written")
			}
		})
	}
}

func stCreateAt(t *testing.T, path string) {
	t.Helper()
	s := stNewStore(t)
	m := stMeta
	if err := s.Open(path, &m); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func storeSchemaSQL(t *testing.T, name string) string {
	for _, o := range storeSchema {
		if o.name == name {
			return o.sql
		}
	}
	t.Fatalf("no schema object %s", name)
	return ""
}

// WP3 acceptance (§4.1 step 8, §7 Store): the in-memory ProofIDSet and HMAC
// NonceStore reset on restart, the DB UNIQUE constraints do not. After a
// simulated restart a resubmitted proof is a 400 duplicate and a reused
// (node_id, att_nonce) is a 400 nonce conflict; neither trips FREEZE, and
// both count toward the §6.6 trigger.
func TestStoreUniqueAfterRestartIs400(t *testing.T) {
	requireHLSQLite(t)
	dir := stDir(t)
	s := stCreate(t, dir)
	stWindow(t, s, stCfg1, 101)
	at := time.Unix(1_790_000_000, 0)
	nonce := stNonce(1)
	rec := stRecord(t, 1, nonce)

	// boot runs §4.1 step 7's atomic claims on fresh in-memory sets, as
	// after a restart, and requires them to succeed.
	boot := func(r Record) {
		t.Helper()
		ids := mining.NewProofIDSet(0)
		nonces := hmac.NewInMemoryNonceStore(2 * time.Minute)
		if !ids.TryRecord(r.ProofID, r.WorkHeight) || !nonces.TryRecord(r.NodeID, r.AttNonce, at) {
			t.Fatal("fresh in-memory claims must succeed")
		}
	}

	boot(rec)
	if err := s.Accept(rec); err != nil {
		t.Fatalf("first Accept: %v", err)
	}

	cases := []struct {
		name   string
		rec    Record
		kind   RejectKind
		reason mining.RejectReason
	}{
		{"resubmitted proof", rec, KindDuplicate, mining.ReasonDuplicate},
		{"same proof ID, refreshed attestation", stRecord(t, 1, stNonce(2)), KindDuplicate, mining.ReasonDuplicate},
		{"other proof, reused node nonce", stRecord(t, 2, nonce), KindNonceConflict, mining.ReasonAttestation},
	}
	if cases[1].rec.ProofID != rec.ProofID || bytes.Equal(cases[1].rec.ProofJSON, rec.ProofJSON) || cases[2].rec.ProofID == rec.ProofID {
		t.Fatal("fixture: proof IDs are not as intended")
	}
	for _, c := range cases {
		s = stReopen(t, s, dir) // simulated restart: same DB
		boot(c.rec)
		err := s.Accept(c.rec)
		stWantReject(t, err, c.kind, c.reason)
		if !strings.HasPrefix(err.Error(), "legacymining: "+c.kind.String()) {
			t.Errorf("%s: error text %q", c.name, err)
		}
	}

	// The rejections changed nothing.
	rs, err := s.Pending()
	if err != nil || len(rs) != 1 || !reflect.DeepEqual(rs[0], rec) {
		t.Fatalf("Pending = %+v, %v; want only the first record", rs, err)
	}
	if c, err := s.Counters(stCfg1); err != nil || c.Proofs != 1 {
		t.Fatalf("Counters = %+v, %v", c, err)
	}
}

// Errors that are not UNIQUE hits are plain errors, which the caller turns
// into FREEZE (§4.1 step 8).
func TestStoreAcceptNonUniqueErrors(t *testing.T) {
	requireHLSQLite(t)
	dir := stDir(t)
	s := stCreate(t, dir)
	stWindow(t, s, stCfg1, 101)
	good := stRecord(t, 1, stNonce(1))

	noWindow := good
	noWindow.ConfigSHA256 = stCfg2 // no config_windows row: foreign key
	stWantPlain(t, s.Accept(noWindow))

	mutate := []func(*Record){
		func(r *Record) { r.MinerAddr = strings.ToUpper(r.MinerAddr) },
		func(r *Record) { r.MinerAddr = r.MinerAddr[:63] },
		func(r *Record) { r.NodeID = "" },
		func(r *Record) { r.NodeID = strings.Repeat("n", storeMaxNodeID+1) },
		func(r *Record) { r.NodeID = "\xff" },
		func(r *Record) { r.WorkHeight = 1 << 63 },
		func(r *Record) { r.AcceptTip = 1 << 63 },
		func(r *Record) { r.AcceptedNS = 0 },
		func(r *Record) { r.ProofJSON = nil },
		func(r *Record) { r.ProofJSON = make([]byte, storeMaxProofJSON+1) },
		func(r *Record) { r.PaidTxID = "tx" },
		func(r *Record) { r.PaidHeight = 5 },
	}
	for i, m := range mutate {
		r := good
		m(&r)
		if err := s.Accept(r); err == nil || RejectKindOf(err) != 0 {
			t.Errorf("mutation %d: Accept = %v, want a plain error", i, err)
		}
	}
	if ids := stPendingIDs(t, s); len(ids) != 0 {
		t.Fatalf("invalid records were stored: %x", ids)
	}
	if err := s.Accept(good); err != nil {
		t.Fatal(err)
	}
}

// §7 Race: 200 goroutines on the same proof, and separately on one shared
// nonce, give exactly one Accept.
func TestStoreAcceptRace(t *testing.T) {
	requireHLSQLite(t)
	const n = 200
	s := stCreate(t, stDir(t))
	stWindow(t, s, stCfg1, 101)

	run := func(recs []Record, loser RejectKind) {
		t.Helper()
		errs := make([]error, n)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				errs[i] = s.Accept(recs[i])
			}(i)
		}
		close(start)
		wg.Wait()
		wins := 0
		for _, err := range errs {
			if err == nil {
				wins++
			} else if RejectKindOf(err) != loser {
				t.Fatalf("loser error %v, want %v", err, loser)
			}
		}
		if wins != 1 {
			t.Fatalf("%d Accepts succeeded, want 1", wins)
		}
	}

	same := make([]Record, n)
	for i := range same {
		same[i] = stRecord(t, 1, stNonce(1))
	}
	run(same, KindDuplicate)

	shared := make([]Record, n)
	for i := range shared {
		shared[i] = stRecord(t, uint64(1000+i), stNonce(2))
	}
	run(shared, KindNonceConflict)

	if ids := stPendingIDs(t, s); len(ids) != 2 {
		t.Fatalf("%d rows stored, want 2", len(ids))
	}
}

func TestStorePendingLookupMarkPaid(t *testing.T) {
	requireHLSQLite(t)
	dir := stDir(t)
	s := stCreate(t, dir)
	stWindow(t, s, stCfg1, 101)

	var recs []Record
	for i, ns := range []int64{30, 10, 20, 10, 30, 5} {
		r := stRecord(t, uint64(i+1), stNonce(byte(i+1)))
		r.AcceptedNS = ns
		if err := s.Accept(r); err != nil {
			t.Fatal(err)
		}
		recs = append(recs, r)
	}
	want := append([]Record(nil), recs...)
	sort.Slice(want, func(i, j int) bool {
		if want[i].AcceptedNS != want[j].AcceptedNS {
			return want[i].AcceptedNS < want[j].AcceptedNS
		}
		return bytes.Compare(want[i].ProofID[:], want[j].ProofID[:]) < 0
	})
	got, err := s.Pending()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Pending order:\n got %+v\nwant %+v (%v)", got, want, err)
	}

	absent := ProofID{0xee}
	m, err := s.Lookup([]ProofID{recs[0].ProofID, absent, recs[2].ProofID, recs[0].ProofID})
	if err != nil || len(m) != 2 || !reflect.DeepEqual(m[recs[0].ProofID], recs[0]) || !reflect.DeepEqual(m[recs[2].ProofID], recs[2]) {
		t.Fatalf("Lookup = %+v, %v", m, err)
	}
	if _, ok := m[absent]; ok {
		t.Fatal("Lookup returned an absent ID")
	}
	if m, err := s.Lookup(nil); err != nil || len(m) != 0 {
		t.Fatalf("Lookup(nil) = %v, %v", m, err)
	}

	pay := func(r Record, h uint64, tx string) Payment {
		return Payment{ProofID: r.ProofID, MinerAddr: r.MinerAddr, Height: h, TxID: tx}
	}
	if err := s.MarkPaid(nil); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkPaid([]Payment{pay(recs[0], 300, "tx-a"), pay(recs[1], 300, "tx-a")}); err != nil {
		t.Fatal(err)
	}

	// All-or-nothing: each batch holds a valid payment for recs[2] plus one
	// conflict, and recs[2] stays unpaid.
	wrongMiner := pay(recs[3], 301, "tx-b")
	wrongMiner.MinerAddr = strings.Repeat("cd", 32)
	for i, bad := range []Payment{
		pay(Record{ProofID: absent, MinerAddr: stMiner}, 301, "tx-b"), // absent
		pay(recs[0], 301, "tx-b"),                                     // already paid
		pay(recs[2], 301, "tx-b"),                                     // repeated in the batch
		wrongMiner,                                                    // miner mismatch
	} {
		if err := s.MarkPaid([]Payment{pay(recs[2], 301, "tx-b"), bad}); !errors.Is(err, ErrPaidConflict) {
			t.Errorf("conflict %d: MarkPaid = %v, want ErrPaidConflict", i, err)
		}
	}
	for i, bad := range []Payment{pay(recs[3], 301, ""), pay(recs[3], 1<<63, "tx-b")} {
		if err := s.MarkPaid([]Payment{pay(recs[2], 301, "tx-b"), bad}); err == nil || errors.Is(err, ErrPaidConflict) {
			t.Errorf("invalid %d: MarkPaid = %v, want a plain error", i, err)
		}
	}

	s = stReopen(t, s, dir)
	var unpaid []Record
	for _, r := range want {
		if r.ProofID != recs[0].ProofID && r.ProofID != recs[1].ProofID {
			unpaid = append(unpaid, r)
		}
	}
	if got, err := s.Pending(); err != nil || !reflect.DeepEqual(got, unpaid) {
		t.Fatalf("Pending after MarkPaid = %+v, %v", got, err)
	}
	m, err = s.Lookup([]ProofID{recs[0].ProofID, recs[2].ProofID})
	if err != nil || m[recs[0].ProofID].PaidHeight != 300 || m[recs[0].ProofID].PaidTxID != "tx-a" || m[recs[2].ProofID].PaidTxID != "" {
		t.Fatalf("paid state = %+v, %v", m, err)
	}
	if c, err := s.Counters(stCfg1); err != nil || c.Proofs != 6 || c.Paid != 2 {
		t.Fatalf("Counters = %+v, %v", c, err)
	}
}

// Config windows and S11 (§5): the counter window of a config hash is fixed
// by its first reconciliation, survives restarts, and only a new hash starts
// a new window.
func TestStoreReconcileAndConfigWindows(t *testing.T) {
	requireHLSQLite(t)
	dir := stDir(t)
	s := stCreate(t, dir)

	c, err := s.ApplyReconcile(Reconciliation{Window: ConfigWindow{ConfigSHA256: stCfg1, FirstHeight: 101}})
	if err != nil || c != (ReconcileCounts{WindowInserted: true}) {
		t.Fatalf("first reconcile = %+v, %v", c, err)
	}
	w1 := ConfigWindow{ConfigSHA256: stCfg1, FirstHeight: 101, ActivatedNS: stClock.UnixNano()}
	if got, err := s.Counters(stCfg1); err != nil || got != (Counters{Window: w1, Known: true}) {
		t.Fatalf("Counters(cfg1) = %+v, %v", got, err)
	}

	r1, r2, r3 := stRecord(t, 1, stNonce(1)), stRecord(t, 2, stNonce(2)), stRecord(t, 3, stNonce(3))
	for _, r := range []Record{r1, r2, r3} {
		if err := s.Accept(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.MarkPaid([]Payment{{ProofID: r1.ProofID, MinerAddr: stMiner, Height: 105, TxID: "tx-1"}}); err != nil {
		t.Fatal(err)
	}

	// The chain (W3) says r2 was paid at 106 and does not confirm r1 (for
	// example a trimmed tail). A changed FirstHeight does not move the window.
	chain := Reconciliation{
		Paid:   []Payment{{ProofID: r2.ProofID, MinerAddr: stMiner, Height: 106, TxID: "tx-2"}},
		Window: ConfigWindow{ConfigSHA256: stCfg1, FirstHeight: 999, ActivatedNS: 1},
	}
	if c, err := s.ApplyReconcile(chain); err != nil || c != (ReconcileCounts{Updated: 1, Reset: 1}) {
		t.Fatalf("reconcile = %+v, %v", c, err)
	}
	if got := stPendingIDs(t, s); !reflect.DeepEqual(got, []ProofID{r1.ProofID, r3.ProofID}) {
		t.Fatalf("Pending after reconcile = %x", got)
	}
	if c, err := s.ApplyReconcile(chain); err != nil || c != (ReconcileCounts{}) {
		t.Fatalf("repeated reconcile = %+v, %v", c, err)
	}
	chain.Paid[0].Height, chain.Paid[0].TxID = 107, "tx-2b"
	if c, err := s.ApplyReconcile(chain); err != nil || c != (ReconcileCounts{Updated: 1}) {
		t.Fatalf("reconcile with changed paid state = %+v, %v", c, err)
	}
	if m, err := s.Lookup([]ProofID{r2.ProofID}); err != nil || m[r2.ProofID].PaidHeight != 107 || m[r2.ProofID].PaidTxID != "tx-2b" {
		t.Fatalf("r2 = %+v, %v", m, err)
	}

	// Anomalies change nothing, including the window of a new hash.
	cfg3 := ConfigHash{0xc3}
	orphan := Payment{ProofID: ProofID{0xee}, MinerAddr: stMiner, Height: 108, TxID: "tx-x"}
	wrongMiner := Payment{ProofID: r3.ProofID, MinerAddr: strings.Repeat("cd", 32), Height: 108, TxID: "tx-3"}
	pay3a := Payment{ProofID: r3.ProofID, MinerAddr: stMiner, Height: 108, TxID: "tx-3"}
	pay3b := Payment{ProofID: r3.ProofID, MinerAddr: stMiner, Height: 109, TxID: "tx-4"}
	events := stEventCount(t, s, storeEventReconcile)
	for i, paid := range [][]Payment{{orphan}, {wrongMiner}, {pay3a, pay3b}} { // orphan, miner mismatch, double pay
		_, err := s.ApplyReconcile(Reconciliation{Paid: paid, Window: ConfigWindow{ConfigSHA256: cfg3, FirstHeight: 200}})
		if !errors.Is(err, ErrPaidConflict) {
			t.Errorf("anomaly %d: ApplyReconcile = %v, want ErrPaidConflict", i, err)
		}
	}
	for i, w := range []ConfigWindow{{FirstHeight: 200}, {ConfigSHA256: cfg3, FirstHeight: 1 << 63}, {ConfigSHA256: cfg3, ActivatedNS: -1}} {
		if _, err := s.ApplyReconcile(Reconciliation{Window: w}); err == nil {
			t.Errorf("invalid window %d accepted", i)
		}
	}
	if got, err := s.Counters(cfg3); err != nil || got.Known || got.Window.ConfigSHA256 != cfg3 || got.Proofs != 0 {
		t.Fatalf("Counters(cfg3) = %+v, %v", got, err)
	}
	if got := stPendingIDs(t, s); !reflect.DeepEqual(got, []ProofID{r1.ProofID, r3.ProofID}) {
		t.Fatalf("Pending after anomalies = %x", got)
	}
	if n := stEventCount(t, s, storeEventReconcile); n != events {
		t.Fatalf("anomalies wrote %d events", n-events)
	}

	// A restart keeps the window; a new config hash opens its own window with
	// its own counters.
	s = stReopen(t, s, dir)
	if c, err := s.ApplyReconcile(Reconciliation{Paid: chain.Paid, Window: ConfigWindow{ConfigSHA256: stCfg2, FirstHeight: 300}}); err != nil || c != (ReconcileCounts{WindowInserted: true}) {
		t.Fatalf("new config reconcile = %+v, %v", c, err)
	}
	r4 := stRecord(t, 4, stNonce(4))
	r4.ConfigSHA256 = stCfg2
	if err := s.Accept(r4); err != nil {
		t.Fatal(err)
	}
	w2 := ConfigWindow{ConfigSHA256: stCfg2, FirstHeight: 300, ActivatedNS: stClock.UnixNano()}
	for h, want := range map[ConfigHash]Counters{
		stCfg1: {Window: w1, Known: true, Proofs: 3, Paid: 1},
		stCfg2: {Window: w2, Known: true, Proofs: 1, Paid: 0},
	} {
		if got, err := s.Counters(h); err != nil || got != want {
			t.Errorf("Counters(%x) = %+v, %v; want %+v", h[:1], got, err, want)
		}
	}

	// One events row per successful reconciliation, with the counts.
	rows, err := s.db.Query(`SELECT at_ns, detail FROM events WHERE kind=? ORDER BY id`, storeEventReconcile)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []map[string]any
	for rows.Next() {
		var at int64
		var detail string
		if err := rows.Scan(&at, &detail); err != nil {
			t.Fatal(err)
		}
		var d map[string]any
		if err := json.Unmarshal([]byte(detail), &d); err != nil || at != stClock.UnixNano() {
			t.Fatalf("event %q at %d: %v", detail, at, err)
		}
		details = append(details, d)
	}
	if len(details) != 5 || details[1]["updated"] != 1.0 || details[1]["reset"] != 1.0 || details[4]["window_inserted"] != true {
		t.Fatalf("reconcile events = %v", details)
	}
}

func TestStoreEventsAndImmutability(t *testing.T) {
	requireHLSQLite(t)
	s := stCreate(t, stDir(t))
	stWindow(t, s, stCfg1, 101)
	r := stRecord(t, 1, stNonce(1))
	if err := s.Accept(r); err != nil {
		t.Fatal(err)
	}
	if err := s.Event(Event{Kind: CauseDBOpen, Detail: "d1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Event(Event{AtNS: 9, Kind: CauseStall}); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []Event{{}, {AtNS: -1, Kind: "k"}} {
		if err := s.Event(ev); err == nil {
			t.Errorf("Event(%+v) succeeded", ev)
		}
	}
	var got []Event
	rows, err := s.db.Query(`SELECT at_ns, kind, detail FROM events WHERE kind<>? ORDER BY id`, storeEventReconcile)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var ev Event
		if err := rows.Scan(&ev.AtNS, &ev.Kind, &ev.Detail); err != nil {
			t.Fatal(err)
		}
		got = append(got, ev)
	}
	rows.Close()
	if want := []Event{{stClock.UnixNano(), CauseDBOpen, "d1"}, {9, CauseStall, ""}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %+v, want %+v", got, want)
	}

	for _, q := range []string{
		`UPDATE events SET detail='x'`,
		`DELETE FROM events`,
		`UPDATE meta SET h0=0`,
		`DELETE FROM meta`,
		`UPDATE config_windows SET first_height=0`,
		`DELETE FROM config_windows`,
		`DELETE FROM proofs`,
		`UPDATE proofs SET miner_addr='` + strings.Repeat("cd", 32) + `'`,
		`UPDATE proofs SET accepted_ns=1`,
		`UPDATE proofs SET paid_height=1`, // paid_height without paid_tx_id
	} {
		if _, err := s.db.Exec(q); err == nil {
			t.Errorf("%s succeeded", q)
		}
	}
	if rs, err := s.Pending(); err != nil || len(rs) != 1 || !reflect.DeepEqual(rs[0], r) {
		t.Fatalf("Pending = %+v, %v", rs, err)
	}
}

// Every store method runs in BEGIN IMMEDIATE (§3.3): the write lock is held
// from the start of the transaction, before any statement has run.
func TestStoreBeginImmediate(t *testing.T) {
	requireHLSQLite(t)
	dir := stDir(t)
	s := stCreate(t, dir)
	other, err := sql.Open("sqlite3", storeDSN(stPath(dir), 0))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	write := func() error {
		_, err := other.Exec(`INSERT INTO events (at_ns, kind, detail) VALUES (1, 'probe', '')`)
		return err
	}
	var inside error
	if err := s.inTx(func(*sql.Tx) error { inside = write(); return nil }); err != nil {
		t.Fatal(err)
	}
	if inside == nil {
		t.Fatal("another connection wrote inside a store transaction")
	}
	if err := write(); err != nil {
		t.Fatalf("write outside a store transaction: %v", err)
	}
	if n := stEventCount(t, s, "probe"); n != 1 {
		t.Fatalf("%d probe events, want 1", n)
	}
}

// The DB path becomes a file: URI; characters with a URI meaning must not
// change which file SQLite opens.
func TestStoreURIEscaping(t *testing.T) {
	requireHLSQLite(t)
	root := filepath.Join(t.TempDir(), "a b#c%20d&e=f")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, LegacyDirName)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	s := stCreate(t, dir)
	s = stReopen(t, s, dir)
	if m, err := s.Meta(); err != nil || m != stMeta {
		t.Fatalf("Meta = %+v, %v", m, err)
	}
	if got := stListDir(t, root); !reflect.DeepEqual(got, []string{LegacyDirName}) {
		t.Fatalf("files outside the legacy-mining directory: %v", got)
	}
}
