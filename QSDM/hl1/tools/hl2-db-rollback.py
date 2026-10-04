#!/usr/bin/env python3
"""HL2 -> HL1 binary rollback: undo legacy-mining.db schema version 2 offline.

An HL2 binary with a version 2 config migrates legacy-mining.db from
user_version 1 to 2 (src/internal/legacymining/store_opkeys.go): it adds the
operator_keys table with two triggers (WP-C) and the proofs_owner index
(WP-D). An HL1 binary refuses a version 2 DB (ErrSchema, FREEZE db-open). This
tool removes exactly those four objects and sets user_version back to 1, so
the DB is again the exact HL1 schema. Nothing else changes: proofs,
config_windows, meta and events rows are kept, and one events row
(kind "schema-rollback") records the change. operator_keys is a cache that an
HL2 boot rebuilds from the chain (HydrateOperatorKeys), so nothing is lost; a
later HL2 boot with a version 2 config migrates the DB again.

Run it only with the core stopped (R-STOP), as qsdm-tech, on the live DB
path, after a backup. It is a dry run unless --execute, and --execute needs
--backup-to (a new file: an SQLite online backup of the version 2 DB, taken
before any change).

Unpaid proofs: the HL1 Ledger pays pending proofs only to its v1 allowlisted
address (I6), and a pending proof of any other owner trips FREEZE at its
first payout. So the tool refuses --execute while unpaid rows of other owners
exist (all unpaid rows, without --v1-miner-addr), unless
--ack-deferred-pending: the operator then boots HL1 with KILL in place or in
mode off, and the proofs stay deferred until the roll-forward to HL2
(OPS.md, "Rollback to an HL1 binary").

Usage:
  hl2-db-rollback.py --db /var/lib/qsdm-tech/core/legacy-mining/legacy-mining.db \\
      [--v1-miner-addr <64hex>] [--ack-deferred-pending] \\
      [--execute --backup-to /var/lib/qsdm-tech/hl2-rollback/legacy-mining.v2-<ts>.db]
  hl2-db-rollback.py --print-schema    # the version 2 objects, as JSON

Exit status: 0 done (or dry run / nothing to do), 1 I/O or SQLite error,
2 refused (nothing changed).
"""
import argparse
import json
import os
import re
import sqlite3
import stat
import struct
import sys
import time

APPLICATION_ID = 0x514C4D31        # legacymining.StoreApplicationID
USER_VERSION_HL1 = 1               # legacymining.StoreUserVersion
USER_VERSION_HL2 = 2               # legacymining.StoreUserVersionOperatorKeys
EVENT_KIND = "schema-rollback"

# The objects schema version 2 adds (store_opkeys.go storeSchemaOperatorKeys),
# byte for byte; the Go test TestHL2DBRollbackTool checks them against the Go
# source.
V2_OBJECTS = [
    ("table", "operator_keys", """CREATE TABLE operator_keys (
 owner TEXT PRIMARY KEY NOT NULL CHECK(typeof(owner)='text' AND length(owner)=64 AND owner NOT GLOB '*[^0-9a-f]*'),
 public_key BLOB NOT NULL CHECK(typeof(public_key)='blob' AND length(public_key)=2592),
 source TEXT NOT NULL CHECK(typeof(source)='text' AND length(source) BETWEEN 1 AND 256),
 height INTEGER NOT NULL CHECK(typeof(height)='integer' AND height>=0),
 added_ns INTEGER NOT NULL CHECK(typeof(added_ns)='integer' AND added_ns>0)
)"""),
    ("trigger", "operator_keys_no_update", "CREATE TRIGGER operator_keys_no_update BEFORE UPDATE ON operator_keys BEGIN SELECT RAISE(ABORT, 'operator_keys rows are immutable'); END"),
    ("trigger", "operator_keys_no_delete", "CREATE TRIGGER operator_keys_no_delete BEFORE DELETE ON operator_keys BEGIN SELECT RAISE(ABORT, 'operator_keys rows are permanent'); END"),
    ("index", "proofs_owner", "CREATE INDEX proofs_owner ON proofs(config_sha256, miner_addr)"),
]

# Drop order: the triggers go with the table, but are dropped explicitly so a
# missing one is an error, not silently skipped.
ROLLBACK_SQL = [
    "DROP TRIGGER operator_keys_no_update",
    "DROP TRIGGER operator_keys_no_delete",
    "DROP TABLE operator_keys",
    "DROP INDEX proofs_owner",
    f"PRAGMA user_version = {USER_VERSION_HL1}",
]


class Refused(Exception):
    pass


def log(msg):
    print(f"hl2-db-rollback: {msg}")


def read_header(path):
    """(application_id, user_version) from the main file's header."""
    with open(path, "rb") as f:
        h = f.read(100)
    if len(h) < 100 or h[:16] != b"SQLite format 3\x00":
        raise Refused(f"{path} is not an SQLite database")
    (user_version,) = struct.unpack(">I", h[60:64])
    (app_id,) = struct.unpack(">I", h[68:72])
    return app_id, user_version


def check_path(path):
    try:
        st = os.lstat(path)
    except FileNotFoundError:
        raise Refused(f"{path} does not exist")
    if not stat.S_ISREG(st.st_mode):
        raise Refused(f"{path} is not a regular file")


def connect(path):
    uri = "file:" + path.replace("?", "%3f").replace("#", "%23") + "?mode=rw"
    con = sqlite3.connect(uri, uri=True, isolation_level=None, timeout=0)
    con.execute("PRAGMA busy_timeout = 0")
    return con


def objects(con):
    rows = con.execute("SELECT type, name, sql FROM sqlite_master WHERE substr(name, 1, 7) <> 'sqlite_'").fetchall()
    return {(t, n): (s or "").strip() for t, n, s in rows}


def survey(con, v1_miner):
    """Facts about the DB; raises Refused when it is not a known version."""
    app_id = con.execute("PRAGMA application_id").fetchone()[0]
    version = con.execute("PRAGMA user_version").fetchone()[0]
    journal = con.execute("PRAGMA journal_mode").fetchone()[0]
    if app_id != APPLICATION_ID:
        raise Refused(f"application_id {app_id:#x}, want {APPLICATION_ID:#x}: not legacy-mining.db")
    if journal != "wal":
        raise Refused(f"journal_mode {journal}, want wal")
    have = objects(con)
    present = [(t, n) for t, n, _ in V2_OBJECTS if (t, n) in have]
    if version == USER_VERSION_HL1:
        if present:
            raise Refused(f"user_version 1 with version 2 objects {present}: escalate (R-X)")
    elif version == USER_VERSION_HL2:
        for t, n, sql in V2_OBJECTS:
            if have.get((t, n)) != sql:
                raise Refused(f"user_version 2 but {t} {n} is missing or differs: escalate (R-X)")
    else:
        raise Refused(f"user_version {version}, want 1 or 2")
    unpaid = con.execute("SELECT miner_addr, count(*) FROM proofs WHERE paid_height IS NULL "
                         "GROUP BY miner_addr ORDER BY miner_addr").fetchall()
    keys = con.execute("SELECT count(*) FROM operator_keys").fetchone()[0] if version == USER_VERSION_HL2 else 0
    others = [(a, n) for a, n in unpaid if a != v1_miner]
    return {"version": version, "operator_keys": keys, "unpaid": unpaid, "unpaid_other": others}


def backup(con, dest):
    if os.path.lexists(dest):
        raise Refused(f"--backup-to {dest} exists")
    fd = os.open(dest, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    os.close(fd)
    out = sqlite3.connect(dest, isolation_level=None)
    try:
        con.backup(out)
        if out.execute("PRAGMA quick_check").fetchone()[0] != "ok":
            raise RuntimeError(f"backup {dest} fails quick_check")
        if out.execute("PRAGMA user_version").fetchone()[0] != USER_VERSION_HL2:
            raise RuntimeError(f"backup {dest} is not user_version 2")
    finally:
        out.close()
    with open(dest, "r+b") as f:   # fsync needs a writable handle on Windows
        os.fsync(f.fileno())


def rollback(con, facts):
    now_ns = time.time_ns()
    detail = json.dumps({"from": USER_VERSION_HL2, "to": USER_VERSION_HL1,
                         "dropped": [n for _, n, _ in V2_OBJECTS], "operator_keys_rows": facts["operator_keys"],
                         "unpaid_rows": sum(n for _, n in facts["unpaid"])}, sort_keys=True)
    con.execute("BEGIN IMMEDIATE")
    try:
        if con.execute("PRAGMA user_version").fetchone()[0] != USER_VERSION_HL2:
            raise Refused("user_version changed under the tool")
        for q in ROLLBACK_SQL:
            con.execute(q)
        con.execute("INSERT INTO events (at_ns, kind, detail) VALUES (?, ?, ?)", (now_ns, EVENT_KIND, detail))
        con.execute("COMMIT")
    except BaseException:
        con.execute("ROLLBACK")
        raise


def checkpoint(con):
    busy, _, _ = con.execute("PRAGMA wal_checkpoint(TRUNCATE)").fetchone()
    if busy:
        raise RuntimeError("wal_checkpoint(TRUNCATE) was blocked: another process has the DB open (R-STOP?)")


def verify(con, path):
    if con.execute("PRAGMA quick_check").fetchone()[0] != "ok":
        raise RuntimeError("quick_check failed after the rollback")
    if con.execute("PRAGMA user_version").fetchone()[0] != USER_VERSION_HL1:
        raise RuntimeError("user_version is not 1 after the rollback")
    have = objects(con)
    left = [(t, n) for t, n, _ in V2_OBJECTS if (t, n) in have]
    if left:
        raise RuntimeError(f"objects left after the rollback: {left}")


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--db", help="legacy-mining.db (core stopped)")
    ap.add_argument("--v1-miner-addr", help="the miner_addr of the v1 config HL1 will run (its unpaid rows are payable)")
    ap.add_argument("--ack-deferred-pending", action="store_true",
                    help="unpaid rows of other owners stay deferred: HL1 boots with KILL or in mode off")
    ap.add_argument("--execute", action="store_true", help="change the DB (default: dry run)")
    ap.add_argument("--backup-to", help="new file for the version 2 backup (required with --execute)")
    ap.add_argument("--print-schema", action="store_true", help="print the version 2 objects as JSON and exit")
    a = ap.parse_args(argv)
    if a.print_schema:
        print(json.dumps([{"type": t, "name": n, "sql": s} for t, n, s in V2_OBJECTS], indent=1))
        return 0
    if not a.db:
        ap.error("--db is required")
    if a.v1_miner_addr is not None and not re.fullmatch(r"[0-9a-f]{64}", a.v1_miner_addr):
        ap.error("--v1-miner-addr must be 64 lowercase hex characters")
    if a.execute and not a.backup_to:
        ap.error("--execute needs --backup-to")
    os.umask(0o077)
    try:
        check_path(a.db)
        app_id, hdr_version = read_header(a.db)
        if app_id != APPLICATION_ID:
            raise Refused(f"header application_id {app_id:#x}: not legacy-mining.db")
        con = connect(a.db)
        try:
            facts = survey(con, a.v1_miner_addr)
            log(f"db {a.db}: user_version {facts['version']} (header {hdr_version}), "
                f"operator_keys rows {facts['operator_keys']}")
            for addr, n in facts["unpaid"]:
                tag = "payable by the v1 config" if addr == a.v1_miner_addr else "deferred under HL1"
                log(f"unpaid: {addr} {n} ({tag})")
            if facts["version"] == USER_VERSION_HL1:
                if a.execute and hdr_version != USER_VERSION_HL1:
                    checkpoint(con)
                    log("checkpointed the WAL into the main file")
                log("already user_version 1: nothing to roll back")
                return 0
            if facts["unpaid_other"] and not a.ack_deferred_pending:
                n = sum(c for _, c in facts["unpaid_other"])
                raise Refused(f"{n} unpaid proofs of owners the v1 config cannot pay; drain them first "
                              "(OPS.md, Rollback to an HL1 binary) or pass --ack-deferred-pending and boot HL1 "
                              "with KILL or in mode off")
            if not a.execute:
                log("dry run: would drop " + ", ".join(n for _, n, _ in V2_OBJECTS) + " and set user_version 1")
                return 0
            backup(con, a.backup_to)
            log(f"backup written: {a.backup_to}")
            rollback(con, facts)
            checkpoint(con)
            verify(con, a.db)
        finally:
            con.close()
        _, hdr_version = read_header(a.db)
        if hdr_version != USER_VERSION_HL1:
            raise RuntimeError(f"main file header still says user_version {hdr_version}")
        log("done: user_version 1, header 1, quick_check ok; the HL1 binary opens this DB")
        return 0
    except Refused as e:
        print(f"hl2-db-rollback: refused: {e}", file=sys.stderr)
        return 2
    except (OSError, sqlite3.Error, RuntimeError) as e:
        print(f"hl2-db-rollback: error: {e}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
