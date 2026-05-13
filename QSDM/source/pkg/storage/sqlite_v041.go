//go:build cgo
// +build cgo

// Session 99 — v0.4.1 storage-layer foundation for replay
// protection + atomic balance debit. See
// QSDM/docs/docs/V041_REPLAY_PROTECTION_DESIGN.md.
//
// What this file contains:
//
//   1. Schema migration `migrateBalancesToV041` — idempotent at
//      startup. Detects a v0.4.0-shape balances table (no
//      CHECK(balance>=0) constraint, no nonce column) and
//      atomically rewrites it to the v0.4.1 shape. Logs the
//      count of rows clamped from negative-balance to 0 (which
//      should be 0 in a healthy validator; any non-zero count
//      is forensic evidence of a past concurrent-debit race).
//
//   2. `GetNonce(address)` — returns the last-applied nonce for
//      `address`. Zero for unknown addresses (so the v0.4.0
//      legacy-envelope path passes the `env.Nonce > stored`
//      check trivially when env.Nonce == 0). Returns the SQLite
//      "no rows" sentinel error unwrapped as a normal (0, nil)
//      tuple — the caller's contract is "0 means new sender."
//
//   3. `ApplyTransferAtomic(...)` — single-transaction debit +
//      credit + nonce-bump + tx-insert. Enforces:
//        balance >= amount + fee     (CHECK constraint)
//        sender's stored nonce < envelopeNonce   (CAS)
//        tx_id is not already present in transactions
//      Returns the sentinel errors documented in storage_errors.go
//      so the handler can map them to the right HTTP code +
//      monitoring counter.
//
// The handler integration ships in Session 100 (next session); this
// file lands the foundation so the next change can be a small
// targeted edit to pkg/api/handlers.go + tests.

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"log"
	"strings"
	"sync/atomic"

	"github.com/blackbeardONE/QSDM/pkg/errors"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
	"github.com/klauspost/compress/zstd"
)

// V0.4.1 storage sentinels. Wrapped via fmt.Errorf("...: %w", ...)
// so callers can errors.Is(...) without caring about the exact
// wrapping layer.
var (
	// ErrInsufficientBalance is returned by ApplyTransferAtomic when
	// the sender's balance after the proposed debit would go
	// negative. The CHECK constraint on `balances.balance >= 0` is
	// the wire-side enforcement; this sentinel is the application
	// surface so handlers can return HTTP 402 cleanly.
	ErrInsufficientBalance = stderrors.New("storage: insufficient balance")

	// ErrNonceConflict is returned by ApplyTransferAtomic when the
	// sender's stored nonce no longer matches the pre-image (typically
	// another concurrent submit-signed for the same sender slipped
	// through and bumped the nonce between our SELECT and our UPDATE).
	// The handler maps this to HTTP 409 + nonce_conflict.
	ErrNonceConflict = stderrors.New("storage: nonce conflict")

	// ErrTxAlreadyExists is returned by ApplyTransferAtomic when
	// the tx_id is already present in the transactions table. The
	// handler maps this to HTTP 409 + duplicate (matches v0.4.0
	// semantics).
	ErrTxAlreadyExists = stderrors.New("storage: tx_id already exists")
)

// v041MigrationClampedRows is set once at startup by
// migrateBalancesToV041 to the count of rows we clamped from
// negative-balance to 0 during the v0.4.0 → v0.4.1 migration.
// Exposed via /metrics as the gauge
// `qsdm_storage_v041_migration_clamped` once the metric collector
// in pkg/monitoring/storage_metrics.go picks it up (Session 100).
var v041MigrationClampedRows atomic.Int64

// V041MigrationClampedRows returns the gauge value for
// /metrics exposition. Always returns the value set at the most
// recent startup migration; never decreases.
func V041MigrationClampedRows() int64 { return v041MigrationClampedRows.Load() }

// migrateBalancesToV041 inspects the current schema of the
// `balances` table and, if it's still in v0.4.0 shape (no CHECK
// constraint, no nonce column), atomically rewrites it to the
// v0.4.1 shape:
//
//	CREATE TABLE balances (
//	    address    TEXT    PRIMARY KEY,
//	    balance    REAL    NOT NULL DEFAULT 0.0 CHECK(balance >= 0),
//	    nonce      INTEGER NOT NULL DEFAULT 0   CHECK(nonce >= 0),
//	    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
//	)
//
// Called once from NewStorage during startup; safe to call again
// (it short-circuits if the v0.4.1 schema is already in place).
//
// Why we cannot use ALTER TABLE: SQLite supports
// ALTER TABLE ADD COLUMN but not ALTER TABLE ADD CHECK
// CONSTRAINT. The portable recipe is the rename-and-rebuild
// dance below.
func (s *Storage) migrateBalancesToV041() error {
	// Probe the existing CREATE TABLE statement. If it already
	// mentions "CHECK(balance" we're on the v0.4.1 schema and
	// can short-circuit.
	var schemaSQL sql.NullString
	err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='balances'`).Scan(&schemaSQL)
	if err == sql.ErrNoRows {
		// The balances table doesn't exist yet. CreateBalancesTable
		// in sqlite.go's NewStorage path already runs the v0.4.0
		// CREATE before we get called; if THIS shows ErrNoRows
		// something is structurally wrong. Return a clear error
		// rather than silently doing nothing.
		return fmt.Errorf("v041 migration: balances table missing (NewStorage create-table step skipped?)")
	}
	if err != nil {
		return fmt.Errorf("v041 migration: probe schema: %w", err)
	}
	if schemaSQL.Valid && strings.Contains(schemaSQL.String, "CHECK(balance") {
		// Already on the v0.4.1 schema (or a strictly-greater
		// schema). Nothing to do.
		return nil
	}

	// We're on the v0.4.0 schema. Run the rename-and-rebuild
	// migration inside a single transaction. Any failure
	// ROLLBACKs and leaves the v0.4.0 table intact, so the
	// validator can keep starting on v0.4.0 until the operator
	// investigates.
	log.Println("v041 migration: detected v0.4.0 balances schema; rewriting to v0.4.1 (CHECK(balance>=0) + nonce column)")

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("v041 migration: begin: %w", err)
	}
	defer func() {
		// If Commit hasn't run, Rollback is a no-op when called
		// after a successful Commit, so this is always safe.
		_ = tx.Rollback()
	}()

	createNewSQL := `CREATE TABLE balances_v041 (
        address    TEXT    PRIMARY KEY,
        balance    REAL    NOT NULL DEFAULT 0.0 CHECK(balance >= 0),
        nonce      INTEGER NOT NULL DEFAULT 0   CHECK(nonce >= 0),
        updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
    )`
	if _, err = tx.Exec(createNewSQL); err != nil {
		return fmt.Errorf("v041 migration: create balances_v041: %w", err)
	}

	// Copy rows, clamping negative balances to 0. The pre-flight
	// GetBalance check in v0.4.0's submit-signed handler should
	// have prevented this in normal operation, but a concurrent-
	// debit race COULD have driven a balance below zero on a
	// live validator. We surface that forensic event via the
	// `qsdm_storage_v041_migration_clamped` gauge (Section 8 of
	// V041_REPLAY_PROTECTION_DESIGN.md).
	res, err := tx.Exec(`
        INSERT INTO balances_v041 (address, balance, nonce, updated_at)
        SELECT address,
               CASE WHEN balance < 0 THEN 0.0 ELSE balance END,
               0,
               updated_at
        FROM balances`)
	if err != nil {
		return fmt.Errorf("v041 migration: copy rows: %w", err)
	}
	_ = res // INSERT row count is informational; we report the
	// clamped count via a separate query below.

	// Count rows that were clamped, for the forensic gauge.
	var clamped int64
	if err = tx.QueryRow(`SELECT COUNT(*) FROM balances WHERE balance < 0`).Scan(&clamped); err != nil {
		// Don't fail the migration on the count query — the
		// schema rewrite is the load-bearing part.
		log.Printf("v041 migration: clamped-count probe failed (continuing): %v", err)
		clamped = -1
	}

	if _, err = tx.Exec(`DROP TABLE balances`); err != nil {
		return fmt.Errorf("v041 migration: drop old: %w", err)
	}
	if _, err = tx.Exec(`ALTER TABLE balances_v041 RENAME TO balances`); err != nil {
		return fmt.Errorf("v041 migration: rename: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("v041 migration: commit: %w", err)
	}

	v041MigrationClampedRows.Store(clamped)
	if clamped > 0 {
		log.Printf("v041 migration: SUCCESS — clamped %d row(s) from negative balance to 0 (forensic: a v0.4.0 concurrent-debit race likely occurred; investigate via GetRecentTransactions)", clamped)
	} else if clamped == 0 {
		log.Printf("v041 migration: SUCCESS — 0 rows clamped (healthy validator)")
	} else {
		log.Printf("v041 migration: SUCCESS — clamped-count probe failed; migration itself succeeded")
	}
	return nil
}

// GetNonce returns the last-applied nonce for `address`. Returns
// (0, nil) for an address that has never sent a v0.4.1 envelope
// (which is also the value an unknown address gets — the caller's
// contract is "0 means new sender").
//
// Wrapped in monitoring instrumentation so a v0.4.1 storage
// regression surfaces on the existing storage-op dashboard
// without a new metric per primitive.
func (s *Storage) GetNonce(address string) (uint64, error) {
	// We intentionally do NOT add a new monitoring.StorageOp
	// constant for GetNonce in this session — the handler-side
	// counters (qsdm_wallet_send_total{result=nonce_lookup_failed})
	// already give us a directly-attributable signal for "the
	// nonce lookup failed during a submit-signed request." Adding
	// a storage-side counter would just duplicate that.
	var nonce int64
	err := s.db.QueryRow(`SELECT nonce FROM balances WHERE address = ?`, address).Scan(&nonce)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, errors.NewStorageError("GetNonce", fmt.Errorf("nonce lookup for %s: %w", address, err))
	}
	if nonce < 0 {
		// CHECK(nonce >= 0) on the table makes this impossible
		// under normal operation, but the check happens at
		// schema-write time — if a future migration backdoor
		// inserted a negative, we surface it clearly.
		return 0, fmt.Errorf("GetNonce: stored nonce for %s is negative (%d) — schema corrupt", address, nonce)
	}
	return uint64(nonce), nil
}

// ApplyTransferAtomic is the v0.4.1 single-transaction
// debit + credit + nonce-bump + tx-insert primitive that
// the /api/v1/wallet/submit-signed handler uses (Session 100
// integration). All four operations happen inside one BEGIN;
// COMMIT; — so a power loss, OS crash, or sqlite-error mid-flight
// leaves the on-disk state unchanged.
//
// Invariants enforced (per-line):
//
//	(a) tx_id must not already exist  → ErrTxAlreadyExists
//	(b) sender's stored nonce must equal envelopeNonce - 1
//	    if envelopeNonce >= 1; if envelopeNonce == 0 (legacy
//	    v0.4.0 path) the nonce check is skipped       → ErrNonceConflict
//	(c) sender's balance >= amount + fee              → ErrInsufficientBalance
//	(d) UPDATE balances SET balance = balance - (amount+fee),
//	      nonce = envelopeNonce WHERE address = sender
//	(e) UPSERT balances SET balance += amount FOR recipient
//	(f) INSERT into transactions (with encrypted+compressed blob)
//
// rawEnvelope is the canonical JSON of the signed envelope; this
// function is responsible for the encrypt+compress+store step so
// the handler can call us with the raw bytes it already has.
//
// Why we wire `ctx` even though sqlite3 driver-level ctx support
// is patchy: a future migration to a context-respecting driver
// (mattn/sqlite3 supports it; modernc.org/sqlite is the explicit
// goal) gives us cancellation for free if we plumb ctx through
// here from day one.
func (s *Storage) ApplyTransferAtomic(
	ctx context.Context,
	sender, recipient string,
	amount, fee float64,
	envelopeNonce uint64,
	txID string,
	rawEnvelope []byte,
) (resErr error) {
	defer func() {
		if resErr != nil {
			monitoring.RecordStorageOp(monitoring.StorageOpStoreTransaction, monitoring.StorageOpResultError)
		} else {
			monitoring.RecordStorageOp(monitoring.StorageOpStoreTransaction, monitoring.StorageOpResultSuccess)
		}
	}()

	if amount < 0 || fee < 0 {
		return fmt.Errorf("ApplyTransferAtomic: amount and fee must be non-negative (got amount=%v fee=%v)", amount, fee)
	}
	totalDebit := amount + fee

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.NewStorageError("ApplyTransferAtomic", fmt.Errorf("begin: %w", err))
	}
	defer func() {
		// Rollback after Commit is a no-op; safe.
		_ = tx.Rollback()
	}()

	// (a) tx_id uniqueness
	var dup int64
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM transactions WHERE tx_id = ?`, txID).Scan(&dup); err != nil {
		return errors.NewStorageError("ApplyTransferAtomic", fmt.Errorf("tx_id probe: %w", err))
	}
	if dup > 0 {
		return ErrTxAlreadyExists
	}

	// (b) + (c) Read sender state for nonce + balance check.
	// CAS pre-image — we read it inside the same transaction so
	// the UPDATE below can detect a concurrent write (RETURNING
	// or rows-affected count).
	var (
		sBalance float64
		sNonce   int64
		hasRow   bool
	)
	row := tx.QueryRowContext(ctx, `SELECT balance, nonce FROM balances WHERE address = ?`, sender)
	switch err = row.Scan(&sBalance, &sNonce); err {
	case nil:
		hasRow = true
	case sql.ErrNoRows:
		// Sender has no row yet → balance 0, nonce 0. A new
		// sender cannot afford anything > 0 → we fall through
		// to the InsufficientBalance check below.
		hasRow = false
	default:
		return errors.NewStorageError("ApplyTransferAtomic", fmt.Errorf("sender state: %w", err))
	}

	if envelopeNonce >= 1 {
		want := uint64(sNonce) + 1
		// Strict serialisation: envelopeNonce must equal want.
		// Tolerant mode (>= want) is rejected in v0.4.1 — see
		// Section 4.1 of V041_REPLAY_PROTECTION_DESIGN.md. A
		// configurable mode could be added in v0.4.2.
		if envelopeNonce != want {
			return ErrNonceConflict
		}
	}
	// envelopeNonce == 0 → legacy v0.4.0 path: no nonce check,
	// no nonce bump. The handler-level rate-limit on this path
	// is the only mitigation (Section 2.3 of the design doc).

	if sBalance < totalDebit {
		return ErrInsufficientBalance
	}

	// (d) Debit sender + bump nonce. We compute the new nonce
	// here rather than depending on the CHECK at write-time so we
	// surface the conflict as ErrNonceConflict above (clearer
	// error path than "constraint violation").
	newNonce := sNonce
	if envelopeNonce >= 1 {
		newNonce = int64(envelopeNonce)
	}
	newBalance := sBalance - totalDebit
	if hasRow {
		res, derr := tx.ExecContext(ctx,
			`UPDATE balances SET balance = ?, nonce = ?, updated_at = CURRENT_TIMESTAMP
              WHERE address = ? AND balance = ? AND nonce = ?`,
			newBalance, newNonce, sender, sBalance, sNonce)
		if derr != nil {
			// Most likely a CHECK(balance >= 0) violation if our
			// pre-flight math was wrong; surface as
			// InsufficientBalance for the handler.
			if strings.Contains(derr.Error(), "CHECK") {
				return ErrInsufficientBalance
			}
			return errors.NewStorageError("ApplyTransferAtomic", fmt.Errorf("debit sender: %w", derr))
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			// CAS lost — another connection bumped sender's
			// balance or nonce between our read and our write.
			return ErrNonceConflict
		}
	} else {
		// Sender had no row → totalDebit must have been 0 to
		// reach here (otherwise InsufficientBalance fired). We
		// still record the nonce bump if envelopeNonce >= 1.
		if envelopeNonce >= 1 {
			if _, derr := tx.ExecContext(ctx,
				`INSERT INTO balances (address, balance, nonce, updated_at)
                  VALUES (?, 0.0, ?, CURRENT_TIMESTAMP)`,
				sender, int64(envelopeNonce)); derr != nil {
				return errors.NewStorageError("ApplyTransferAtomic", fmt.Errorf("init sender row: %w", derr))
			}
		}
	}

	// (e) Credit recipient. UPSERT with ON CONFLICT — recipient
	// may or may not already have a row.
	if amount > 0 {
		if _, cerr := tx.ExecContext(ctx,
			`INSERT INTO balances (address, balance, nonce, updated_at)
              VALUES (?, ?, 0, CURRENT_TIMESTAMP)
              ON CONFLICT(address) DO UPDATE SET
                  balance = balance + excluded.balance,
                  updated_at = CURRENT_TIMESTAMP`,
			recipient, amount); cerr != nil {
			if strings.Contains(cerr.Error(), "CHECK") {
				return errors.NewStorageError("ApplyTransferAtomic", fmt.Errorf("credit recipient CHECK violation: %w", cerr))
			}
			return errors.NewStorageError("ApplyTransferAtomic", fmt.Errorf("credit recipient: %w", cerr))
		}
	}

	// (f) INSERT into transactions, with the same encrypt+compress
	// pipeline StoreTransaction uses. We DO NOT call
	// StoreTransaction directly because that method also calls
	// UpdateBalance internally (the v0.4.0 non-atomic path we're
	// replacing).
	encrypted, err := s.encryptData(rawEnvelope)
	if err != nil {
		return errors.NewStorageError("ApplyTransferAtomic", fmt.Errorf("encrypt: %w", err))
	}
	var compressed []byte
	{
		// Local scope so the encoder doesn't outlive the txn.
		// Reusing the StoreTransaction compression posture
		// (zstd BestCompression).
		encoder, zerr := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
		if zerr != nil {
			return errors.NewStorageError("ApplyTransferAtomic", fmt.Errorf("zstd init: %w", zerr))
		}
		compressed = encoder.EncodeAll(encrypted, nil)
		encoder.Close()
	}

	if _, ierr := tx.ExecContext(ctx,
		`INSERT INTO transactions (data, tx_id, sender, recipient, amount) VALUES (?, ?, ?, ?, ?)`,
		compressed, txID, sender, recipient, amount); ierr != nil {
		return errors.NewStorageError("ApplyTransferAtomic", fmt.Errorf("insert tx: %w", ierr))
	}

	if err = tx.Commit(); err != nil {
		return errors.NewStorageError("ApplyTransferAtomic", fmt.Errorf("commit: %w", err))
	}
	return nil
}

// applyTransferAtomicAssertRawIsJSON is a defensive check that
// ApplyTransferAtomic's rawEnvelope arg is parseable JSON. Called
// from tests, not from the hot path. Exposed at package scope so
// the test file (sqlite_v041_test.go) can use it without an
// internal_test.go indirection.
func applyTransferAtomicAssertRawIsJSON(raw []byte) error {
	var v map[string]interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("rawEnvelope is not valid JSON: %w", err)
	}
	return nil
}
