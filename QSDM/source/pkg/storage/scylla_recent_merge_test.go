package storage

import (
	"testing"
	"time"

	"github.com/gocql/gocql"
)

func mustUUID(t *testing.T, s string) gocql.UUID {
	t.Helper()
	u, err := gocql.ParseUUID(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestMergeRecentTransactionRows_orderAndLimit(t *testing.T) {
	ts := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	a := []recentTxRow{
		{id: mustUUID(t, "11111111-1111-1111-1111-111111111111"), txID: "a1", sender: "x", recipient: "y", amount: 1, ts: ts.Add(2 * time.Hour)},
		{id: mustUUID(t, "22222222-2222-2222-2222-222222222222"), txID: "a2", sender: "x", recipient: "y", amount: 1, ts: ts},
	}
	b := []recentTxRow{
		{id: mustUUID(t, "33333333-3333-3333-3333-333333333333"), txID: "b1", sender: "z", recipient: "x", amount: 2, ts: ts.Add(1 * time.Hour)},
	}
	out := mergeRecentTransactionRows(a, b, 2)
	if len(out) != 2 {
		t.Fatalf("len=%d", len(out))
	}
	if out[0]["id"] != "a1" || out[1]["id"] != "b1" {
		t.Fatalf("order: %#v", out)
	}
}

func TestMergeRecentTransactionRows_selfTransferDedupes(t *testing.T) {
	ts := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	id := mustUUID(t, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	row := recentTxRow{id: id, txID: "self", sender: "x", recipient: "x", amount: 0.5, ts: ts}
	out := mergeRecentTransactionRows([]recentTxRow{row}, []recentTxRow{row}, 5)
	if len(out) != 1 {
		t.Fatalf("len=%d want 1", len(out))
	}
}

func TestMergeRecentTransactionRowsDedupeSort(t *testing.T) {
	ts := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	id := mustUUID(t, "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	rows := []recentTxRow{
		{id: id, txID: "dup", sender: "a", recipient: "b", amount: 1, ts: ts},
		{id: id, txID: "dup", sender: "a", recipient: "b", amount: 2, ts: ts.Add(time.Hour)},
		{id: mustUUID(t, "cccccccc-cccc-cccc-cccc-cccccccccccc"), txID: "c", sender: "a", recipient: "b", amount: 3, ts: ts.Add(30 * time.Minute)},
	}
	out := mergeRecentTransactionRowsDedupeSort(rows, 10)
	if len(out) != 2 {
		t.Fatalf("len=%d", len(out))
	}
	if out[0]["id"] != "dup" || out[0]["amount"].(float64) != 2 {
		t.Fatalf("newer duplicate should win: %#v", out[0])
	}
}
