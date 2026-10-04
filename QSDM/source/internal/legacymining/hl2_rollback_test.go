package legacymining

// HL2 WP-H: the offline rollback of schema version 2 for an HL1 binary
// (hl1/tools/hl2-db-rollback.py, OPS.md "Rollback to an HL1 binary"). The
// test runs the real tool on a DB that an HL1 store created and an HL2 store
// migrated and used, then opens the result with the version 1 checks an HL1
// binary applies: the exact HL1 schema, user_version 1 in the main file's
// header, every row kept.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const rbTool = "../../../hl1/tools/hl2-db-rollback.py"

// rbPython returns a working Python 3 interpreter, or skips.
func rbPython(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"python3", "python"} {
		p, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		out, err := exec.Command(p, "-c", "import sqlite3, sys; print(sys.version_info[0])").Output()
		if err == nil && strings.TrimSpace(string(out)) == "3" {
			return p
		}
	}
	t.Skip("no Python 3 with sqlite3")
	return ""
}

func rbRun(t *testing.T, py string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(py, append([]string{rbTool}, args...)...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run tool: %v", err)
		}
		code = ee.ExitCode()
	}
	return code, string(out)
}

func TestHL2DBRollbackTool(t *testing.T) {
	requireHLSQLite(t)
	py := rbPython(t)

	// The tool's idea of version 2 is exactly the Go schema.
	code, out := rbRun(t, py, "--print-schema")
	if code != 0 {
		t.Fatalf("--print-schema: %d %s", code, out)
	}
	var objs []struct{ Type, Name, SQL string }
	if err := json.Unmarshal([]byte(out), &objs); err != nil {
		t.Fatal(err)
	}
	if len(objs) != len(storeSchemaOperatorKeys) {
		t.Fatalf("tool lists %d objects, Go %d", len(objs), len(storeSchemaOperatorKeys))
	}
	for i, o := range storeSchemaOperatorKeys {
		if objs[i].Type != o.typ || objs[i].Name != o.name || objs[i].SQL != o.sql {
			t.Fatalf("object %d: tool %+v, Go %s %s %q", i, objs[i], o.typ, o.name, o.sql)
		}
	}

	// An HL1 DB, migrated and used by HL2: a v2 window, two owners' rows,
	// an operator key.
	path, rec := opHL1Fixture(t)
	a, b := newOpSigner(t), newOpSigner(t)
	s := opOpen(t, NewSQLiteStoreV2(), path)
	stWindow(t, s, stCfg2, 150)
	ra := stRecord(t, 2, stNonce(2))
	ra.MinerAddr, ra.ConfigSHA256 = a.owner, stCfg2
	rb := stRecord(t, 3, stNonce(3))
	rb.MinerAddr, rb.ConfigSHA256 = b.owner, stCfg2
	for _, r := range []Record{ra, rb} {
		if err := s.Accept(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.MarkPaid([]Payment{{ProofID: ra.ProofID, MinerAddr: a.owner, Height: 160, TxID: "t160"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutOperatorKeys([]OperatorKey{{Owner: a.owner, PublicKey: a.pub, Source: "chain", Height: 9}}); err != nil {
		t.Fatal(err)
	}
	pendingBefore, err := s.Pending()
	if err != nil || len(pendingBefore) != 2 { // rec (HL1, stMiner) and rb
		t.Fatalf("pending %v, %v", pendingBefore, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before := stFileSum(t, path)
	backup := stPath(stDir(t)) // a restorable copy: <dir>/legacy-mining/legacy-mining.db

	// Dry run: reports, changes nothing.
	if code, out := rbRun(t, py, "--db", path, "--v1-miner-addr", stMiner, "--ack-deferred-pending"); code != 0 ||
		!strings.Contains(out, "dry run") || !strings.Contains(out, "operator_keys rows 1") || !strings.Contains(out, b.owner+" 1 (deferred under HL1)") {
		t.Fatalf("dry run: %d\n%s", code, out)
	}
	// rb's owner is not the v1 miner: refused without the acknowledgement.
	if code, out := rbRun(t, py, "--db", path, "--v1-miner-addr", stMiner, "--execute", "--backup-to", backup); code != 2 ||
		!strings.Contains(out, "1 unpaid proofs") {
		t.Fatalf("unacknowledged: %d\n%s", code, out)
	}
	if code, out := rbRun(t, py, "--db", path, "--execute", "--backup-to", backup); code != 2 { // no v1 miner: every unpaid row
		t.Fatalf("no v1 miner: %d\n%s", code, out)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("a refusal wrote the backup: %v", err)
	}
	if stFileSum(t, path) != before {
		t.Fatal("dry run or refusal changed the DB")
	}

	code, out = rbRun(t, py, "--db", path, "--v1-miner-addr", stMiner, "--ack-deferred-pending", "--execute", "--backup-to", backup)
	if code != 0 || !strings.Contains(out, "done: user_version 1") {
		t.Fatalf("execute: %d\n%s", code, out)
	}
	// The main file's header says 1 (HL1 reads it before SQLite opens the
	// file), and the WAL holds nothing.
	if v := opHeaderVersion(t, path); v != StoreUserVersion {
		t.Fatalf("header user_version %d", v)
	}
	if fi, err := os.Stat(path + "-wal"); err == nil && fi.Size() != 0 {
		t.Fatalf("WAL has %d bytes", fi.Size())
	}
	// The HL1 checks: exact version 1 schema (storeVerify), every row kept.
	s = opOpen(t, NewSQLiteStore(), path)
	if v := s.SchemaVersion(); v != StoreUserVersion {
		t.Fatalf("version %d after the rollback", v)
	}
	got, err := s.Lookup([]ProofID{rec.ProofID, ra.ProofID, rb.ProofID})
	if err != nil || len(got) != 3 || got[ra.ProofID].PaidTxID != "t160" || got[rb.ProofID].MinerAddr != b.owner {
		t.Fatalf("rows after the rollback: %v, %v", got, err)
	}
	pending, err := s.Pending()
	if err != nil || len(pending) != len(pendingBefore) {
		t.Fatalf("pending after the rollback %v, %v", pending, err)
	}
	if c, err := s.Counters(stCfg2); err != nil || c.Window.FirstHeight != 150 || c.Proofs != 2 {
		t.Fatalf("counters %+v, %v", c, err)
	}
	if n := stEventCount(t, s, "schema-rollback"); n != 1 {
		t.Fatalf("%d rollback events", n)
	}
	var detail string
	if err := s.db.QueryRow(`SELECT detail FROM events WHERE kind='schema-rollback'`).Scan(&detail); err != nil ||
		!strings.Contains(detail, `"operator_keys_rows": 1`) {
		t.Fatalf("event detail %q, %v", detail, err)
	}
	_ = s.Close()

	// The backup is the version 2 DB, key included.
	bs := opOpen(t, NewSQLiteStoreV2(), backup)
	if ks, err := bs.OperatorKeys(); err != nil || len(ks) != 1 || !bytes.Equal(ks[0].PublicKey, a.pub) {
		t.Fatalf("backup keys %v, %v", ks, err)
	}
	_ = bs.Close()

	// A rerun is a no-op; the backup target must be new.
	after := stFileSum(t, path)
	if code, out := rbRun(t, py, "--db", path, "--execute", "--backup-to", backup); code != 0 || !strings.Contains(out, "already user_version 1") {
		t.Fatalf("rerun: %d\n%s", code, out)
	}
	if stFileSum(t, path) != after {
		t.Fatal("rerun changed the DB")
	}

	// Roll forward: an HL2 boot with a v2 config migrates again.
	s = opOpen(t, NewSQLiteStoreV2(), path)
	if v := s.SchemaVersion(); v != StoreUserVersionOperatorKeys || !stHasIndex(t, s) {
		t.Fatalf("roll forward: version %d", v)
	}
	if ks, err := s.OperatorKeys(); err != nil || len(ks) != 0 {
		t.Fatalf("keys after roll forward %v, %v", ks, err)
	}
	if n := stEventCount(t, s, storeEventSchemaMigrate); n != 2 {
		t.Fatalf("%d migrate events", n)
	}
}

// The tool refuses anything that is not a known legacy-mining.db version.
func TestHL2DBRollbackToolRefusals(t *testing.T) {
	requireHLSQLite(t)
	py := rbPython(t)
	path, _ := opHL1Fixture(t)
	// A version 2 DB missing an object (here: proofs_owner) is not touched.
	s := opOpen(t, NewSQLiteStoreV2(), path)
	if _, err := s.db.Exec(`DROP INDEX proofs_owner`); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	before := stFileSum(t, path)
	if code, out := rbRun(t, py, "--db", path, "--execute", "--backup-to", filepath.Join(t.TempDir(), "b.db"), "--ack-deferred-pending"); code != 2 ||
		!strings.Contains(out, "proofs_owner is missing or differs") {
		t.Fatalf("damaged v2: %d\n%s", code, out)
	}
	if stFileSum(t, path) != before {
		t.Fatal("refusal changed the DB")
	}
	other := filepath.Join(t.TempDir(), "other.db")
	if err := os.WriteFile(other, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _ := rbRun(t, py, "--db", other); code != 2 {
		t.Fatalf("non-SQLite file: %d", code)
	}
	if code, _ := rbRun(t, py, "--db", filepath.Join(t.TempDir(), "missing.db")); code != 2 {
		t.Fatalf("missing file: %d", code)
	}
}
