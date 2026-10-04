package legacymining

// HL2 WP-D: the proofs_owner index of schema version 2 and the OwnerCounts
// query, on a created version 2 DB, a migrated HL1 DB and a version 1 DB.

import (
	"reflect"
	"strings"
	"testing"
)

// stHasIndex reports whether sqlite_master lists the proofs_owner index.
func stHasIndex(t *testing.T, s *SQLiteStore) bool {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='proofs_owner'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func stOwnerRecords(t *testing.T, s *SQLiteStore, h ConfigHash, seq0 uint64, owners ...string) []Record {
	t.Helper()
	var out []Record
	for i, o := range owners {
		rec := stRecord(t, seq0+uint64(i), stNonce(byte(seq0)+byte(i)))
		rec.MinerAddr, rec.ConfigSHA256 = o, h
		if err := s.Accept(rec); err != nil {
			t.Fatal(err)
		}
		out = append(out, rec)
	}
	return out
}

func TestStoreOwnerIndexAndCounts(t *testing.T) {
	t.Run("created version 2", func(t *testing.T) {
		dir := stDir(t)
		s := NewSQLiteStoreV2()
		s.now = stNewStore(t).now
		m := stMeta
		if err := s.Open(stPath(dir), &m); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		if !stHasIndex(t, s) {
			t.Fatal("version 2 DB without proofs_owner")
		}
		// The query uses the index.
		var plan strings.Builder
		rows, err := s.db.Query(`EXPLAIN QUERY PLAN SELECT miner_addr, count(*), count(paid_height) FROM proofs WHERE config_sha256=? GROUP BY miner_addr`, stCfg1[:])
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(detail + "; ")
		}
		rows.Close()
		if !strings.Contains(plan.String(), "proofs_owner") {
			t.Fatalf("OwnerCounts plan does not use proofs_owner: %s", plan.String())
		}

		stWindow(t, s, stCfg1, 10)
		stWindow(t, s, stCfg2, 20)
		a, b := strings.Repeat("a1", 32), strings.Repeat("b2", 32)
		recs := stOwnerRecords(t, s, stCfg1, 1, a, b, a, a)
		stOwnerRecords(t, s, stCfg2, 9, b)
		if err := s.MarkPaid([]Payment{{ProofID: recs[0].ProofID, MinerAddr: a, Height: 11, TxID: "t"}}); err != nil {
			t.Fatal(err)
		}
		got, err := s.OwnerCounts(stCfg1)
		want := map[string]OwnerCount{a: {Proofs: 3, Paid: 1}, b: {Proofs: 1}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("OwnerCounts = %+v, %v; want %+v", got, err, want)
		}
		if got, _ := s.OwnerCounts(ConfigHash{9}); len(got) != 0 {
			t.Fatalf("unknown window: %+v", got)
		}
	})
	t.Run("migrated HL1 DB and a version 1 DB", func(t *testing.T) {
		path, rec := opHL1Fixture(t)
		s1 := opOpen(t, NewSQLiteStore(), path)
		if stHasIndex(t, s1) {
			t.Fatal("version 1 DB has proofs_owner")
		}
		if got, err := s1.OwnerCounts(stCfg1); err != nil || !reflect.DeepEqual(got, map[string]OwnerCount{rec.MinerAddr: {Proofs: 1}}) {
			t.Fatalf("v1 OwnerCounts = %+v, %v", got, err)
		}
		_ = s1.Close()
		s2 := opOpen(t, NewSQLiteStoreV2(), path)
		if !stHasIndex(t, s2) {
			t.Fatal("the migration did not add proofs_owner")
		}
		var detail string
		if err := s2.db.QueryRow(`SELECT detail FROM events WHERE kind=?`, storeEventSchemaMigrate).Scan(&detail); err != nil ||
			!strings.Contains(detail, "proofs_owner") {
			t.Fatalf("migrate event %q, %v", detail, err)
		}
		if got, err := s2.OwnerCounts(stCfg1); err != nil || got[rec.MinerAddr].Proofs != 1 {
			t.Fatalf("v2 OwnerCounts = %+v, %v", got, err)
		}
	})
}
