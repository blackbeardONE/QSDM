package legacymining

// SQLite proof store (§3.3, WP3): <state>/legacy-mining/legacy-mining.db.
//
// Durability: WAL, synchronous=FULL, one connection, and every method is one
// BEGIN IMMEDIATE transaction (L4). A committed Accept is durable before it
// returns (W1).
//
// Creation is atomic: the schema and the meta row are written to a D1-style
// temp file (TempPrefix + DBFile + "-<pid>-<16 hex>") in the legacy-mining
// directory, checkpointed, closed, fsynced, and only then hard-linked to
// DBFile. A crash leaves either no DBFile or a complete one; a leftover temp
// file is removed by the S3 cleanup.
//
// Integrity beyond §3.3: CHECK constraints pin every column's type and shape,
// proofs.config_sha256 references config_windows (S11 inserts the active
// window before admission opens, so a missing window is a wiring bug and
// Accept fails with a non-Rejection error), and triggers make events
// append-only, meta and config_windows immutable, and proof evidence
// permanent (only paid_height and paid_tx_id change).

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	_ "github.com/mattn/go-sqlite3" // registers the "sqlite3" driver (cgo)
)

const (
	storeMaxProofJSON = 1 << 20 // proofs.proof_json upper bound
	storeMaxNodeID    = 256     // proofs.node_id upper bound, in bytes

	// storeEventReconcile is the events.kind written by ApplyReconcile.
	storeEventReconcile = "reconcile"
)

// storeSidecars are the SQLite files next to the DB. -journal never exists
// in WAL mode; if one appears it must still be a safe regular file.
var storeSidecars = []string{"-wal", "-shm", "-journal"}

// storeSchema is the exact version 1 schema. Open compares sqlite_master
// against it (or against storeSchemaFor(2), store_opkeys.go), so any edit
// here needs a new user_version.
var storeSchema = []struct{ typ, name, sql string }{
	{"table", "meta", `CREATE TABLE meta (
 id INTEGER PRIMARY KEY NOT NULL CHECK(id=1),
 h0 INTEGER NOT NULL CHECK(typeof(h0)='integer' AND h0>=0),
 genesis_hash TEXT NOT NULL CHECK(typeof(genesis_hash)='text' AND length(genesis_hash)>0),
 funder TEXT NOT NULL CHECK(typeof(funder)='text' AND length(funder)>0),
 release TEXT NOT NULL CHECK(typeof(release)='text'),
 created_ns INTEGER NOT NULL CHECK(typeof(created_ns)='integer' AND created_ns>0)
)`},
	{"table", "config_windows", `CREATE TABLE config_windows (
 config_sha256 BLOB PRIMARY KEY NOT NULL CHECK(typeof(config_sha256)='blob' AND length(config_sha256)=32),
 first_height INTEGER NOT NULL CHECK(typeof(first_height)='integer' AND first_height>=0),
 activated_ns INTEGER NOT NULL CHECK(typeof(activated_ns)='integer' AND activated_ns>0)
)`},
	{"table", "proofs", `CREATE TABLE proofs (
 proof_id BLOB PRIMARY KEY NOT NULL CHECK(typeof(proof_id)='blob' AND length(proof_id)=32),
 miner_addr TEXT NOT NULL CHECK(typeof(miner_addr)='text' AND length(miner_addr)=64 AND miner_addr NOT GLOB '*[^0-9a-f]*'),
 node_id TEXT NOT NULL CHECK(typeof(node_id)='text' AND length(node_id) BETWEEN 1 AND 256),
 att_nonce BLOB NOT NULL CHECK(typeof(att_nonce)='blob' AND length(att_nonce)=32),
 work_height INTEGER NOT NULL CHECK(typeof(work_height)='integer' AND work_height>=0),
 accept_tip INTEGER NOT NULL CHECK(typeof(accept_tip)='integer' AND accept_tip>=0),
 accepted_ns INTEGER NOT NULL CHECK(typeof(accepted_ns)='integer' AND accepted_ns>0),
 config_sha256 BLOB NOT NULL REFERENCES config_windows(config_sha256) CHECK(typeof(config_sha256)='blob' AND length(config_sha256)=32),
 proof_json BLOB NOT NULL CHECK(typeof(proof_json)='blob' AND length(proof_json) BETWEEN 1 AND 1048576),
 paid_height INTEGER CHECK(paid_height IS NULL OR (typeof(paid_height)='integer' AND paid_height>=0)),
 paid_tx_id TEXT CHECK(paid_tx_id IS NULL OR (typeof(paid_tx_id)='text' AND length(paid_tx_id)>0)),
 CHECK((paid_height IS NULL) = (paid_tx_id IS NULL)),
 UNIQUE(node_id, att_nonce)
)`},
	{"index", "proofs_unpaid", `CREATE INDEX proofs_unpaid ON proofs(accepted_ns, proof_id) WHERE paid_height IS NULL`},
	{"index", "proofs_config", `CREATE INDEX proofs_config ON proofs(config_sha256)`},
	{"table", "events", `CREATE TABLE events (
 id INTEGER PRIMARY KEY NOT NULL,
 at_ns INTEGER NOT NULL CHECK(typeof(at_ns)='integer' AND at_ns>0),
 kind TEXT NOT NULL CHECK(typeof(kind)='text' AND length(kind)>0),
 detail TEXT NOT NULL CHECK(typeof(detail)='text')
)`},
	{"trigger", "meta_no_update", `CREATE TRIGGER meta_no_update BEFORE UPDATE ON meta BEGIN SELECT RAISE(ABORT, 'meta is immutable'); END`},
	{"trigger", "meta_no_delete", `CREATE TRIGGER meta_no_delete BEFORE DELETE ON meta BEGIN SELECT RAISE(ABORT, 'meta is immutable'); END`},
	{"trigger", "config_windows_no_update", `CREATE TRIGGER config_windows_no_update BEFORE UPDATE ON config_windows BEGIN SELECT RAISE(ABORT, 'config_windows is immutable'); END`},
	{"trigger", "config_windows_no_delete", `CREATE TRIGGER config_windows_no_delete BEFORE DELETE ON config_windows BEGIN SELECT RAISE(ABORT, 'config_windows is immutable'); END`},
	{"trigger", "proofs_no_delete", `CREATE TRIGGER proofs_no_delete BEFORE DELETE ON proofs BEGIN SELECT RAISE(ABORT, 'proofs rows are permanent'); END`},
	{"trigger", "proofs_evidence_immutable", `CREATE TRIGGER proofs_evidence_immutable BEFORE UPDATE OF proof_id, miner_addr, node_id, att_nonce, work_height, accept_tip, accepted_ns, config_sha256, proof_json ON proofs BEGIN SELECT RAISE(ABORT, 'proof evidence is immutable'); END`},
	{"trigger", "events_no_update", `CREATE TRIGGER events_no_update BEFORE UPDATE ON events BEGIN SELECT RAISE(ABORT, 'events is append-only'); END`},
	{"trigger", "events_no_delete", `CREATE TRIGGER events_no_delete BEFORE DELETE ON events BEGIN SELECT RAISE(ABORT, 'events is append-only'); END`},
}

const storeRecordColumns = `proof_id, miner_addr, node_id, att_nonce, work_height, accept_tip, accepted_ns, config_sha256, proof_json, paid_height, paid_tx_id`

// SQLiteStore is the Store (§3.3). The zero value is a closed store; Open it
// before use. It is safe for concurrent use: a mutex serialises every method
// on the single connection.
//
// Schema versions (HL2 WP-C): user_version 1 is the HL1 schema (storeSchema);
// user_version 2 (StoreUserVersionOperatorKeys) adds the operator_keys table
// and the proofs_owner index (storeSchemaOperatorKeys). A store from NewSQLiteStore never writes the
// schema of an existing DB: it opens a version 1 DB exactly as HL1 does, and
// also a version 2 DB (read paths such as hl-audit, and a v2-to-v1 config
// rollback on an HL2 binary). A store from NewSQLiteStoreV2 creates version 2
// and migrates a version 1 DB to version 2 at Open (storeMigrateV2).
type SQLiteStore struct {
	mu      sync.Mutex
	db      *sql.DB
	now     func() time.Time // nil means time.Now; replaced in tests
	v2      bool             // create version 2 and migrate version 1 at Open
	version int64            // user_version of the open DB

	// migrateHook, if set (tests), runs inside the migration transaction
	// after the schema statements and before the commit; an error aborts it.
	migrateHook func() error
}

var _ Store = (*SQLiteStore)(nil)
var _ OperatorKeyStore = (*SQLiteStore)(nil)

// NewSQLiteStore returns a closed store that never changes the schema version
// of an existing DB and creates version 1 (HL1).
func NewSQLiteStore() *SQLiteStore { return &SQLiteStore{} }

// NewSQLiteStoreV2 returns a closed store for a version 2 legacy-mining config
// (HL2 WP-C). It creates a version 2 DB, and Open migrates a version 1 DB to
// version 2 in one transaction (see storeMigrateV2). cmd/qsdm uses it only
// when the loaded config is version 2, so an HL1 deployment's DB is never
// migrated.
func NewSQLiteStoreV2() *SQLiteStore { return &SQLiteStore{v2: true} }

// SchemaVersion returns the user_version of the open DB (0 when closed).
func (s *SQLiteStore) SchemaVersion() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return 0
	}
	return int(s.version)
}

func (s *SQLiteStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// Open implements Store.Open. Besides the checks listed on Store.Open, path
// must be absolute and clean, name DBFile, and sit directly in a directory
// named LegacyDirName; the DB header must carry the SQLite magic and
// StoreApplicationID before SQLite is allowed to open (and possibly write) the
// file; the schema must equal storeSchema exactly; and the meta row must
// exist. Creating refuses when the DB already exists or when a stale sidecar
// (-wal, -shm, -journal) exists without it. A zero Meta.CreatedNS is set from
// the clock. The DB is opened with mode=rw, so SQLite itself never creates it.
func (s *SQLiteStore) Open(path string, create *Meta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		return errors.New("legacymining: store already open")
	}
	dir, err := storeCheckPath(path)
	if err != nil {
		return err
	}
	switch _, err := os.Lstat(path); {
	case errors.Is(err, fs.ErrNotExist):
		if create == nil {
			return ErrDBMissing
		}
		if err := s.create(dir, path, *create); err != nil {
			return err
		}
	case err != nil:
		return fmt.Errorf("%w: %w", ErrUnsafePath, err)
	case create != nil:
		return fmt.Errorf("%w: %s already exists", ErrUnsafePath, path)
	}
	if err := storeCheckFiles(path); err != nil {
		return err
	}
	if err := storeCheckHeader(path); err != nil {
		return err
	}
	db, err := storeOpenDB(path)
	if err != nil {
		return err
	}
	version, err := storeVerify(db)
	if err != nil {
		_ = db.Close()
		return err
	}
	if s.v2 && version == StoreUserVersion {
		if err := storeMigrateV2(db, s.clock().UnixNano(), s.migrateHook); err != nil {
			_ = db.Close()
			return err
		}
		version = StoreUserVersionOperatorKeys
	}
	// SQLite has now created -wal and -shm; they inherit the DB file's mode.
	if err := storeCheckFiles(path); err != nil {
		_ = db.Close()
		return err
	}
	if err := storeSyncDir(dir); err != nil {
		_ = db.Close()
		return err
	}
	s.db = db
	s.version = version
	return nil
}

// Close implements Store.Close. Closing a closed store returns ErrClosed.
func (s *SQLiteStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return ErrClosed
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// Meta implements Store.Meta.
func (s *SQLiteStore) Meta() (Meta, error) {
	var m Meta
	err := s.inTx(func(tx *sql.Tx) error {
		var h0 int64
		if err := tx.QueryRow(`SELECT h0, genesis_hash, funder, release, created_ns FROM meta WHERE id=1`).
			Scan(&h0, &m.GenesisHash, &m.Funder, &m.Release, &m.CreatedNS); err != nil {
			return err
		}
		m.H0 = uint64(h0) // CHECK(h0>=0)
		return nil
	})
	if err != nil {
		return Meta{}, err
	}
	return m, nil
}

// Accept implements Store.Accept. rec must be unpaid, MinerAddr must be 64
// lowercase hex, NodeID 1..256 bytes of UTF-8, ProofJSON 1..1 MiB, AcceptedNS
// positive and the heights at most MaxInt64; otherwise Accept returns a plain
// error. The UNIQUE constraints decide conflicts (INSERT ... ON CONFLICT DO
// NOTHING); a conflicting row is then classified inside the same transaction,
// with proof_id first, because resubmitting a stored proof usually repeats its
// attestation nonce as well.
func (s *SQLiteStore) Accept(rec Record) error {
	return s.inTx(func(tx *sql.Tx) error {
		if err := storeCheckRecord(rec); err != nil {
			return err
		}
		res, err := tx.Exec(`INSERT INTO proofs (proof_id, miner_addr, node_id, att_nonce, work_height, accept_tip, accepted_ns, config_sha256, proof_json)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`,
			rec.ProofID[:], rec.MinerAddr, rec.NodeID, rec.AttNonce[:], int64(rec.WorkHeight), int64(rec.AcceptTip),
			rec.AcceptedNS, rec.ConfigSHA256[:], rec.ProofJSON)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 1 {
			return nil
		}
		var one int
		err = tx.QueryRow(`SELECT 1 FROM proofs WHERE proof_id=?`, rec.ProofID[:]).Scan(&one)
		if err == nil {
			return &Rejection{Kind: KindDuplicate, Detail: fmt.Sprintf("proof %x is already stored", rec.ProofID[:8])}
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		err = tx.QueryRow(`SELECT 1 FROM proofs WHERE node_id=? AND att_nonce=?`, rec.NodeID, rec.AttNonce[:]).Scan(&one)
		if err == nil {
			return &Rejection{Kind: KindNonceConflict, Detail: fmt.Sprintf("attestation nonce %x of this node is already stored", rec.AttNonce[:8])}
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return errors.New("legacymining: proof insert ignored without a UNIQUE conflict")
	})
}

// Pending implements Store.Pending.
func (s *SQLiteStore) Pending() ([]Record, error) {
	var out []Record
	err := s.inTx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT ` + storeRecordColumns + ` FROM proofs WHERE paid_height IS NULL ORDER BY accepted_ns, proof_id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r, err := storeScanRecord(rows)
			if err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Lookup implements Store.Lookup.
func (s *SQLiteStore) Lookup(ids []ProofID) (map[ProofID]Record, error) {
	out := make(map[ProofID]Record, len(ids))
	err := s.inTx(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`SELECT ` + storeRecordColumns + ` FROM proofs WHERE proof_id=?`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, id := range ids {
			if _, done := out[id]; done {
				continue
			}
			r, err := storeScanRecord(stmt.QueryRow(id[:]))
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			out[id] = r
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// MarkPaid implements Store.MarkPaid. A payment with an empty TxID or a
// height above MaxInt64 is invalid and fails with a plain error. A ProofID
// repeated within ps is already paid by the time its second entry is checked,
// so it wraps ErrPaidConflict.
func (s *SQLiteStore) MarkPaid(ps []Payment) error {
	return s.inTx(func(tx *sql.Tx) error {
		for _, p := range ps {
			if err := storeCheckPayment(p); err != nil {
				return err
			}
			var miner string
			var paidTx sql.NullString
			err := tx.QueryRow(`SELECT miner_addr, paid_tx_id FROM proofs WHERE proof_id=?`, p.ProofID[:]).Scan(&miner, &paidTx)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				return fmt.Errorf("%w: proof %x is not stored", ErrPaidConflict, p.ProofID[:])
			case err != nil:
				return err
			case paidTx.Valid:
				return fmt.Errorf("%w: proof %x is already paid by %s", ErrPaidConflict, p.ProofID[:], paidTx.String)
			case miner != p.MinerAddr:
				return fmt.Errorf("%w: proof %x belongs to %s, paid to %s", ErrPaidConflict, p.ProofID[:], miner, p.MinerAddr)
			}
			if err := storeSetPaid(tx, p.ProofID, sql.NullInt64{Int64: int64(p.Height), Valid: true}, sql.NullString{String: p.TxID, Valid: true}); err != nil {
				return err
			}
		}
		return nil
	})
}

// ApplyReconcile implements Store.ApplyReconcile. A ProofID repeated in
// r.Paid (a chain double payment) also wraps ErrPaidConflict. r.Window must
// have a non-zero ConfigSHA256; a zero ActivatedNS is set from the clock. The
// events row has Kind "reconcile" and a JSON Detail with the counts.
func (s *SQLiteStore) ApplyReconcile(r Reconciliation) (ReconcileCounts, error) {
	var c ReconcileCounts
	w := r.Window
	if w.ActivatedNS == 0 {
		w.ActivatedNS = s.clock().UnixNano()
	}
	err := s.inTx(func(tx *sql.Tx) error {
		if err := storeCheckWindow(w); err != nil {
			return err
		}
		onChain := make(map[ProofID]struct{}, len(r.Paid))
		for _, p := range r.Paid {
			if err := storeCheckPayment(p); err != nil {
				return err
			}
			if _, dup := onChain[p.ProofID]; dup {
				return fmt.Errorf("%w: proof %x is paid twice on the chain", ErrPaidConflict, p.ProofID[:])
			}
			onChain[p.ProofID] = struct{}{}
			var miner string
			var paidH sql.NullInt64
			var paidTx sql.NullString
			err := tx.QueryRow(`SELECT miner_addr, paid_height, paid_tx_id FROM proofs WHERE proof_id=?`, p.ProofID[:]).Scan(&miner, &paidH, &paidTx)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				return fmt.Errorf("%w: proof %x paid on the chain is not stored", ErrPaidConflict, p.ProofID[:])
			case err != nil:
				return err
			case miner != p.MinerAddr:
				return fmt.Errorf("%w: proof %x belongs to %s, paid on the chain to %s", ErrPaidConflict, p.ProofID[:], miner, p.MinerAddr)
			}
			if paidH.Valid && paidTx.Valid && uint64(paidH.Int64) == p.Height && paidTx.String == p.TxID {
				continue
			}
			if err := storeSetPaid(tx, p.ProofID, sql.NullInt64{Int64: int64(p.Height), Valid: true}, sql.NullString{String: p.TxID, Valid: true}); err != nil {
				return err
			}
			c.Updated++
		}

		// Cached-paid rows that the chain does not confirm become unpaid.
		var stale []ProofID
		rows, err := tx.Query(`SELECT proof_id FROM proofs WHERE paid_height IS NOT NULL`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var b []byte
			if err := rows.Scan(&b); err != nil {
				rows.Close()
				return err
			}
			var id ProofID
			if len(b) != len(id) {
				rows.Close()
				return fmt.Errorf("%w: corrupt proof_id", ErrSchema)
			}
			copy(id[:], b)
			if _, ok := onChain[id]; !ok {
				stale = append(stale, id)
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, id := range stale {
			if err := storeSetPaid(tx, id, sql.NullInt64{}, sql.NullString{}); err != nil {
				return err
			}
			c.Reset++
		}

		res, err := tx.Exec(`INSERT INTO config_windows (config_sha256, first_height, activated_ns) VALUES (?, ?, ?) ON CONFLICT (config_sha256) DO NOTHING`,
			w.ConfigSHA256[:], int64(w.FirstHeight), w.ActivatedNS)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		c.WindowInserted = n == 1

		detail, err := json.Marshal(struct {
			Paid           int    `json:"paid"`
			Updated        int    `json:"updated"`
			Reset          int    `json:"reset"`
			WindowInserted bool   `json:"window_inserted"`
			ConfigSHA256   string `json:"config_sha256"`
			FirstHeight    uint64 `json:"first_height"`
		}{len(r.Paid), c.Updated, c.Reset, c.WindowInserted, hex.EncodeToString(w.ConfigSHA256[:]), w.FirstHeight})
		if err != nil {
			return err
		}
		return storeInsertEvent(tx, Event{AtNS: s.clock().UnixNano(), Kind: storeEventReconcile, Detail: string(detail)})
	})
	if err != nil {
		return ReconcileCounts{}, err
	}
	return c, nil
}

// Counters implements Store.Counters. Window.ConfigSHA256 is h even when the
// window is unknown; the other Window fields are then zero.
func (s *SQLiteStore) Counters(h ConfigHash) (Counters, error) {
	c := Counters{Window: ConfigWindow{ConfigSHA256: h}}
	err := s.inTx(func(tx *sql.Tx) error {
		var first int64
		err := tx.QueryRow(`SELECT first_height, activated_ns FROM config_windows WHERE config_sha256=?`, h[:]).Scan(&first, &c.Window.ActivatedNS)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		default:
			c.Known = true
			c.Window.FirstHeight = uint64(first) // CHECK(first_height>=0)
		}
		var proofs, paid int64
		if err := tx.QueryRow(`SELECT count(*), count(paid_height) FROM proofs WHERE config_sha256=?`, h[:]).Scan(&proofs, &paid); err != nil {
			return err
		}
		c.Proofs, c.Paid = uint64(proofs), uint64(paid)
		return nil
	})
	if err != nil {
		return Counters{}, err
	}
	return c, nil
}

// Event implements Store.Event. Kind must be non-empty; a zero AtNS is set
// from the clock.
func (s *SQLiteStore) Event(ev Event) error {
	if ev.AtNS == 0 {
		ev.AtNS = s.clock().UnixNano()
	}
	return s.inTx(func(tx *sql.Tx) error { return storeInsertEvent(tx, ev) })
}

// inTx runs fn in one BEGIN IMMEDIATE transaction (L4) and commits if fn
// succeeds.
func (s *SQLiteStore) inTx(fn func(*sql.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return ErrClosed
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// create writes a complete DB (schema plus meta) to a temp file, then links it
// to path. The caller has checked dir and that path does not exist.
func (s *SQLiteStore) create(dir, path string, m Meta) error {
	if m.CreatedNS == 0 {
		m.CreatedNS = s.clock().UnixNano()
	}
	if m.H0 > math.MaxInt64 || m.GenesisHash == "" || m.Funder == "" || m.CreatedNS < 0 {
		return fmt.Errorf("legacymining: invalid meta %+v", m)
	}
	for _, suffix := range storeSidecars {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s%s exists without the database", ErrUnsafePath, path, suffix)
		}
	}
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	tmp := filepath.Join(dir, fmt.Sprintf("%s%s-%d-%x", TempPrefix, DBFile, os.Getpid(), rnd))
	defer func() {
		// After a successful link the temp name is only a second link.
		for _, suffix := range append([]string{""}, storeSidecars...) {
			_ = os.Remove(tmp + suffix)
		}
	}()
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("legacymining: create database: %w", err)
	}
	err = f.Chmod(0o600) // independent of the umask
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("legacymining: create database: %w", err)
	}

	db, err := storeOpenDB(tmp)
	if err != nil {
		return err
	}
	version := int64(StoreUserVersion)
	if s.v2 {
		version = StoreUserVersionOperatorKeys
	}
	err = storeInit(db, m, version)
	if err == nil {
		_, err = storeVerify(db)
	}
	if err == nil {
		var busy, logFrames, done int
		err = db.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &done)
		if err == nil && busy != 0 {
			err = errors.New("legacymining: create database: WAL checkpoint busy")
		}
	}
	if cerr := db.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	// Closing the last connection checkpoints and deletes the WAL. Anything
	// left in it would be lost by linking only the main file.
	if fi, err := os.Lstat(tmp + "-wal"); err == nil && fi.Size() != 0 {
		return errors.New("legacymining: create database: WAL not empty after close")
	}
	if err := storeSyncFile(tmp); err != nil {
		return err
	}
	// Link, unlike rename, never replaces an existing path.
	if err := os.Link(tmp, path); err != nil {
		return fmt.Errorf("legacymining: create database: %w", err)
	}
	for _, suffix := range append([]string{""}, storeSidecars...) {
		if err := os.Remove(tmp + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return storeSyncDir(dir)
}

// storeInit writes the identity, schema and meta row in one transaction.
func storeInit(db *sql.DB, m Meta, version int64) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmts := []string{
		fmt.Sprintf(`PRAGMA application_id = %d`, StoreApplicationID),
		fmt.Sprintf(`PRAGMA user_version = %d`, version),
	}
	for _, o := range storeSchemaFor(version) {
		stmts = append(stmts, o.sql)
	}
	for _, q := range stmts {
		if _, err := tx.Exec(q); err != nil {
			return fmt.Errorf("legacymining: create database: %w", err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO meta (id, h0, genesis_hash, funder, release, created_ns) VALUES (1, ?, ?, ?, ?, ?)`,
		int64(m.H0), m.GenesisHash, m.Funder, m.Release, m.CreatedNS); err != nil {
		return fmt.Errorf("legacymining: create database: %w", err)
	}
	return tx.Commit()
}

// storeDSN is the URI of path with the §3.3 settings. mode=rw means SQLite
// never creates the file; _txlock makes every Begin a BEGIN IMMEDIATE.
func storeDSN(path string, busyTimeoutMS int) string {
	uriPath := filepath.ToSlash(path)
	if runtime.GOOS == "windows" {
		uriPath = "/" + uriPath // file:///C:/...
	}
	q := url.Values{
		"mode":          {"rw"},
		"_journal_mode": {"WAL"},
		"_synchronous":  {"FULL"},
		"_foreign_keys": {"on"},
		"_txlock":       {"immediate"},
		"_busy_timeout": {strconv.Itoa(busyTimeoutMS)},
	}
	u := url.URL{Scheme: "file", Path: uriPath, RawQuery: q.Encode()}
	return u.String()
}

// storeOpenDB opens path with one connection and the §3.3 settings.
func storeOpenDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", storeDSN(path, 5000))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("legacymining: open database: %w", err)
	}
	return db, nil
}

// storeVerify checks identity, schema, settings, integrity and the meta row,
// and returns the schema version: user_version 1 with exactly storeSchema, or
// user_version 2 with exactly storeSchemaFor(2).
func storeVerify(db *sql.DB) (int64, error) {
	var appID, userVersion, syncMode, foreignKeys int64
	var journal string
	for _, p := range []struct {
		q    string
		dest any
	}{
		{`PRAGMA application_id`, &appID},
		{`PRAGMA user_version`, &userVersion},
		{`PRAGMA journal_mode`, &journal},
		{`PRAGMA synchronous`, &syncMode},
		{`PRAGMA foreign_keys`, &foreignKeys},
	} {
		if err := db.QueryRow(p.q).Scan(p.dest); err != nil {
			return 0, fmt.Errorf("%w: %s: %w", ErrSchema, p.q, err)
		}
	}
	schema := storeSchemaFor(userVersion)
	if appID != StoreApplicationID || schema == nil {
		return 0, fmt.Errorf("%w: application_id %#x user_version %d", ErrSchema, appID, userVersion)
	}
	if journal != "wal" || syncMode != 2 || foreignKeys != 1 {
		return 0, fmt.Errorf("%w: journal_mode %s synchronous %d foreign_keys %d", ErrSchema, journal, syncMode, foreignKeys)
	}

	rows, err := db.Query(`SELECT type, name, sql FROM sqlite_master WHERE substr(name, 1, 7) <> 'sqlite_'`)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrSchema, err)
	}
	found := map[string]string{}
	for rows.Next() {
		var typ, name string
		var q sql.NullString
		if err := rows.Scan(&typ, &name, &q); err != nil {
			rows.Close()
			return 0, fmt.Errorf("%w: %w", ErrSchema, err)
		}
		found[typ+":"+name] = strings.TrimSpace(q.String)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrSchema, err)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrSchema, err)
	}
	if len(found) != len(schema) {
		return 0, fmt.Errorf("%w: %d schema objects, want %d", ErrSchema, len(found), len(schema))
	}
	for _, o := range schema {
		if found[o.typ+":"+o.name] != o.sql {
			return 0, fmt.Errorf("%w: %s %s differs", ErrSchema, o.typ, o.name)
		}
	}

	rows, err = db.Query(`PRAGMA quick_check`)
	if err != nil {
		return 0, fmt.Errorf("%w: quick_check: %w", ErrSchema, err)
	}
	var results []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			rows.Close()
			return 0, fmt.Errorf("%w: quick_check: %w", ErrSchema, err)
		}
		results = append(results, line)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("%w: quick_check: %w", ErrSchema, err)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("%w: quick_check: %w", ErrSchema, err)
	}
	if len(results) != 1 || results[0] != "ok" {
		return 0, fmt.Errorf("%w: quick_check: %s", ErrSchema, strings.Join(results, "; "))
	}

	var metaRows int
	if err := db.QueryRow(`SELECT count(*) FROM meta`).Scan(&metaRows); err != nil {
		return 0, fmt.Errorf("%w: meta: %w", ErrSchema, err)
	}
	if metaRows != 1 {
		return 0, fmt.Errorf("%w: %d meta rows", ErrSchema, metaRows)
	}
	return userVersion, nil
}

// storeCheckPath validates the DB path and its directory, and returns the
// directory.
func storeCheckPath(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", fmt.Errorf("%w: %q is not an absolute clean path", ErrUnsafePath, path)
	}
	dir := filepath.Dir(path)
	if filepath.Base(path) != DBFile || filepath.Base(dir) != LegacyDirName {
		return "", fmt.Errorf("%w: %q is not %s/%s", ErrUnsafePath, path, LegacyDirName, DBFile)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnsafePath, err)
	}
	if fi.Mode().Type() != fs.ModeDir {
		return "", fmt.Errorf("%w: %s is not a real directory (mode %v)", ErrUnsafePath, dir, fi.Mode())
	}
	if legacyStorePOSIX && fi.Mode().Perm() != 0o700 {
		return "", fmt.Errorf("%w: %s has mode %04o, want 0700", ErrUnsafePath, dir, fi.Mode().Perm())
	}
	if err := legacyStoreCheckOwner(dir, fi); err != nil {
		return "", err
	}
	return dir, nil
}

// storeCheckFiles requires the DB to be a regular 0600 file owned by the
// process, and every existing sidecar likewise.
func storeCheckFiles(path string) error {
	for _, suffix := range append([]string{""}, storeSidecars...) {
		name := path + suffix
		fi, err := os.Lstat(name)
		if suffix != "" && errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("%w: %w", ErrUnsafePath, err)
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("%w: %s is not a regular file (mode %v)", ErrUnsafePath, name, fi.Mode())
		}
		if legacyStorePOSIX && fi.Mode().Perm() != 0o600 {
			return fmt.Errorf("%w: %s has mode %04o, want 0600", ErrUnsafePath, name, fi.Mode().Perm())
		}
		if err := legacyStoreCheckOwner(name, fi); err != nil {
			return err
		}
	}
	return nil
}

// storeCheckHeader reads the 100-byte SQLite header before SQLite opens the
// file, so a foreign file is refused without being written. application_id
// and user_version never change after creation, and creation checkpoints
// them into the main file, so the main file's header is authoritative.
func storeCheckHeader(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnsafePath, err)
	}
	defer f.Close()
	var h [100]byte
	if _, err := io.ReadFull(f, h[:]); err != nil {
		return fmt.Errorf("%w: header: %w", ErrSchema, err)
	}
	if !bytes.Equal(h[:16], []byte("SQLite format 3\x00")) {
		return fmt.Errorf("%w: not an SQLite database", ErrSchema)
	}
	// A migration to version 2 commits user_version 2 in the WAL first, so
	// the main file may still say 1 after a crash; storeVerify then reads
	// the committed version through SQLite.
	if id, v := binary.BigEndian.Uint32(h[68:72]), binary.BigEndian.Uint32(h[60:64]); id != StoreApplicationID || storeSchemaFor(int64(v)) == nil {
		return fmt.Errorf("%w: header application_id %#x user_version %d", ErrSchema, id, v)
	}
	return nil
}

func storeCheckRecord(r Record) error {
	var why string
	switch {
	case !storeIsLowerHex64(r.MinerAddr):
		why = "miner_addr is not 64 lowercase hex"
	case r.NodeID == "" || len(r.NodeID) > storeMaxNodeID || !utf8.ValidString(r.NodeID):
		why = "node_id is empty, too long or not UTF-8"
	case r.WorkHeight > math.MaxInt64 || r.AcceptTip > math.MaxInt64:
		why = "height out of range"
	case r.AcceptedNS <= 0:
		why = "accepted_ns is not positive"
	case len(r.ProofJSON) == 0 || len(r.ProofJSON) > storeMaxProofJSON:
		why = "proof_json size"
	case r.PaidTxID != "" || r.PaidHeight != 0:
		why = "record is already paid"
	default:
		return nil
	}
	return fmt.Errorf("legacymining: invalid record %x: %s", r.ProofID[:], why)
}

func storeCheckPayment(p Payment) error {
	if p.TxID == "" || p.Height > math.MaxInt64 {
		return fmt.Errorf("legacymining: invalid payment of proof %x: tx %q height %d", p.ProofID[:], p.TxID, p.Height)
	}
	return nil
}

func storeCheckWindow(w ConfigWindow) error {
	if w.ConfigSHA256 == (ConfigHash{}) || w.FirstHeight > math.MaxInt64 || w.ActivatedNS <= 0 {
		return fmt.Errorf("legacymining: invalid config window %x first_height %d activated_ns %d", w.ConfigSHA256[:], w.FirstHeight, w.ActivatedNS)
	}
	return nil
}

func storeIsLowerHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// storeSetPaid sets (or, with invalid values, clears) a row's paid state and
// requires exactly one changed row.
func storeSetPaid(tx *sql.Tx, id ProofID, height sql.NullInt64, txID sql.NullString) error {
	res, err := tx.Exec(`UPDATE proofs SET paid_height=?, paid_tx_id=? WHERE proof_id=?`, height, txID, id[:])
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("legacymining: update paid state of %x changed %d rows", id[:], n)
	}
	return nil
}

func storeInsertEvent(tx *sql.Tx, ev Event) error {
	if ev.Kind == "" || ev.AtNS <= 0 {
		return fmt.Errorf("legacymining: invalid event kind %q at_ns %d", ev.Kind, ev.AtNS)
	}
	_, err := tx.Exec(`INSERT INTO events (at_ns, kind, detail) VALUES (?, ?, ?)`, ev.AtNS, ev.Kind, ev.Detail)
	return err
}

type storeScanner interface{ Scan(dest ...any) error }

func storeScanRecord(sc storeScanner) (Record, error) {
	var r Record
	var id, nonce, cfg []byte
	var work, tip int64
	var paidH sql.NullInt64
	var paidTx sql.NullString
	if err := sc.Scan(&id, &r.MinerAddr, &r.NodeID, &nonce, &work, &tip, &r.AcceptedNS, &cfg, &r.ProofJSON, &paidH, &paidTx); err != nil {
		return Record{}, err
	}
	if len(id) != len(r.ProofID) || len(nonce) != len(r.AttNonce) || len(cfg) != len(r.ConfigSHA256) ||
		work < 0 || tip < 0 || paidH.Valid != paidTx.Valid || paidH.Int64 < 0 {
		return Record{}, fmt.Errorf("%w: corrupt proofs row %x", ErrSchema, id)
	}
	copy(r.ProofID[:], id)
	copy(r.AttNonce[:], nonce)
	copy(r.ConfigSHA256[:], cfg)
	r.WorkHeight, r.AcceptTip = uint64(work), uint64(tip)
	if paidH.Valid {
		r.PaidHeight, r.PaidTxID = uint64(paidH.Int64), paidTx.String
	}
	return r, nil
}

// storeSyncFile fsyncs a file. Windows needs a writable handle.
func storeSyncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = f.Sync()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// storeSyncDir is fsync(dir) (§3.2). Windows cannot fsync a directory; NTFS
// journals the metadata itself.
func storeSyncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = f.Sync()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
