package legacymining

// store_opkeys.go (HL2 WP-C): schema version 2 of legacy-mining.db, the
// operator_keys table, and the version 1 -> 2 migration.
//
// Migration safety on the live HL1 DB:
//   - Only a store from NewSQLiteStoreV2 migrates, and cmd/qsdm builds one
//     only for a version 2 config. A canary v1 deployment (HL1, or the HL2
//     binary in its stage deploy) keeps a byte-identical version 1 DB.
//   - Open first verifies the version 1 DB exactly as HL1 does (identity,
//     exact schema, quick_check, meta row). Only then does it migrate.
//   - The migration is purely additive (one table, two triggers) and runs in
//     one BEGIN IMMEDIATE transaction together with PRAGMA user_version = 2
//     and an events row. SQLite journals the header change with the rest of
//     the transaction, so a crash leaves either the complete version 1 DB or
//     the complete version 2 DB; there is no intermediate schema.
//   - It is idempotent: a version 2 DB is verified and left alone, and the
//     transaction re-reads user_version before it changes anything.
//   - A committed migration may still sit in the WAL when the process dies,
//     so the main file's header can say 1 while SQLite reads 2.
//     storeCheckHeader accepts either known version; storeVerify then decides
//     from the committed state. After the commit Open checkpoints the WAL
//     (best effort) so the main file catches up.
//
// Rollback: an HL2 binary with a version 1 config opens a version 2 DB
// without touching operator_keys. An HL1 binary refuses a version 2 DB
// (ErrSchema, FREEZE db-open). Rolling back to an HL1 binary after a version
// 2 boot therefore needs the table removed offline (WP-H runbook); the keys
// are a cache that HydrateOperatorKeys rebuilds from the chain.

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"math"
)

// storeEventSchemaMigrate is the events.kind written by storeMigrateV2.
const storeEventSchemaMigrate = "schema-migrate"

// storeSchemaOperatorKeys is what version 2 adds to storeSchema. Any edit
// needs a new user_version.
var storeSchemaOperatorKeys = []struct{ typ, name, sql string }{
	{"table", "operator_keys", `CREATE TABLE operator_keys (
 owner TEXT PRIMARY KEY NOT NULL CHECK(typeof(owner)='text' AND length(owner)=64 AND owner NOT GLOB '*[^0-9a-f]*'),
 public_key BLOB NOT NULL CHECK(typeof(public_key)='blob' AND length(public_key)=2592),
 source TEXT NOT NULL CHECK(typeof(source)='text' AND length(source) BETWEEN 1 AND 256),
 height INTEGER NOT NULL CHECK(typeof(height)='integer' AND height>=0),
 added_ns INTEGER NOT NULL CHECK(typeof(added_ns)='integer' AND added_ns>0)
)`},
	{"trigger", "operator_keys_no_update", `CREATE TRIGGER operator_keys_no_update BEFORE UPDATE ON operator_keys BEGIN SELECT RAISE(ABORT, 'operator_keys rows are immutable'); END`},
	{"trigger", "operator_keys_no_delete", `CREATE TRIGGER operator_keys_no_delete BEFORE DELETE ON operator_keys BEGIN SELECT RAISE(ABORT, 'operator_keys rows are permanent'); END`},
}

// storeSchemaV2 is the exact version 2 schema.
var storeSchemaV2 = append(append([]struct{ typ, name, sql string }{}, storeSchema...), storeSchemaOperatorKeys...)

// storeSchemaFor returns the exact schema of user_version v, or nil for an
// unknown version.
func storeSchemaFor(v int64) []struct{ typ, name, sql string } {
	switch v {
	case StoreUserVersion:
		return storeSchema
	case StoreUserVersionOperatorKeys:
		return storeSchemaV2
	}
	return nil
}

// storeMigrateV2 migrates a verified version 1 DB to version 2 in one
// transaction, verifies the result and checkpoints the WAL (best effort).
func storeMigrateV2(db *sql.DB, nowNS int64, hook func() error) error {
	if nowNS <= 0 {
		return fmt.Errorf("legacymining: migrate database: invalid clock %d", nowNS)
	}
	tx, err := db.Begin() // BEGIN IMMEDIATE
	if err != nil {
		return fmt.Errorf("legacymining: migrate database: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var v int64
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return fmt.Errorf("legacymining: migrate database: %w", err)
	}
	switch v {
	case StoreUserVersionOperatorKeys:
		// Already migrated (by another opener): change nothing, verify below.
		if err := tx.Rollback(); err != nil {
			return fmt.Errorf("legacymining: migrate database: %w", err)
		}
	case StoreUserVersion:
		if err := storeMigrateV2Tx(tx, nowNS, hook); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: migrate from user_version %d", ErrSchema, v)
	}
	got, err := storeVerify(db)
	if err != nil {
		return err
	}
	if got != StoreUserVersionOperatorKeys {
		return fmt.Errorf("%w: user_version %d after migration", ErrSchema, got)
	}
	// Best effort: bring the main file's header to version 2. A failure only
	// leaves the commit in the WAL, which SQLite replays.
	var busy, logFrames, done int
	_ = db.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &done)
	return nil
}

// storeMigrateV2Tx adds the version 2 objects, sets user_version and logs an
// events row, then commits tx.
func storeMigrateV2Tx(tx *sql.Tx, nowNS int64, hook func() error) error {
	for _, o := range storeSchemaOperatorKeys {
		if _, err := tx.Exec(o.sql); err != nil {
			return fmt.Errorf("legacymining: migrate database: %w", err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, StoreUserVersionOperatorKeys)); err != nil {
		return fmt.Errorf("legacymining: migrate database: %w", err)
	}
	if err := storeInsertEvent(tx, Event{AtNS: nowNS, Kind: storeEventSchemaMigrate,
		Detail: fmt.Sprintf("user_version %d -> %d: operator_keys", StoreUserVersion, StoreUserVersionOperatorKeys)}); err != nil {
		return fmt.Errorf("legacymining: migrate database: %w", err)
	}
	if hook != nil {
		if err := hook(); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("legacymining: migrate database: %w", err)
	}
	return nil
}

// errStoreV1 is returned by the operator-key methods on a version 1 DB.
func errStoreV1(v int64) error {
	return fmt.Errorf("%w: operator_keys needs user_version %d, the DB has %d", ErrSchema, StoreUserVersionOperatorKeys, v)
}

// PutOperatorKeys implements OperatorKeyStore.
func (s *SQLiteStore) PutOperatorKeys(keys []OperatorKey) (int, error) {
	for _, k := range keys {
		if _, err := CheckOperatorKey(k.Owner, k.PublicKey); err != nil {
			return 0, err
		}
		if len(k.Source) == 0 || len(k.Source) > 256 || k.Height > math.MaxInt64 || k.AddedNS < 0 {
			return 0, fmt.Errorf("%w: owner %s: source %q height %d added_ns %d", ErrOperatorKey, k.Owner, k.Source, k.Height, k.AddedNS)
		}
	}
	now := s.clock().UnixNano()
	added := 0
	err := s.inTx(func(tx *sql.Tx) error {
		if s.version < StoreUserVersionOperatorKeys {
			return errStoreV1(s.version)
		}
		added = 0
		for _, k := range keys {
			at := k.AddedNS
			if at == 0 {
				at = now
			}
			res, err := tx.Exec(`INSERT INTO operator_keys (owner, public_key, source, height, added_ns) VALUES (?, ?, ?, ?, ?) ON CONFLICT(owner) DO NOTHING`,
				k.Owner, k.PublicKey, k.Source, int64(k.Height), at)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if n == 1 {
				added++
				continue
			}
			var have []byte
			if err := tx.QueryRow(`SELECT public_key FROM operator_keys WHERE owner=?`, k.Owner).Scan(&have); err != nil {
				return err
			}
			if !bytes.Equal(have, k.PublicKey) {
				return fmt.Errorf("%w: owner %s is stored with a different public key", ErrOperatorKey, k.Owner)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return added, nil
}

// OperatorKeys implements OperatorKeyStore.
func (s *SQLiteStore) OperatorKeys() ([]OperatorKey, error) {
	var out []OperatorKey
	err := s.inTx(func(tx *sql.Tx) error {
		if s.version < StoreUserVersionOperatorKeys {
			return errStoreV1(s.version)
		}
		rows, err := tx.Query(`SELECT owner, public_key, source, height, added_ns FROM operator_keys ORDER BY owner`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k OperatorKey
			var h int64
			if err := rows.Scan(&k.Owner, &k.PublicKey, &k.Source, &h, &k.AddedNS); err != nil {
				return err
			}
			if h < 0 {
				return errors.New("legacymining: corrupt operator_keys row")
			}
			k.Height = uint64(h)
			out = append(out, k)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
