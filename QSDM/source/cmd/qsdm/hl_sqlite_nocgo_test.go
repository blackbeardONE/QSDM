//go:build !cgo

package main

import "testing"

// requireHLSQLite skips tests that open the HL1/HL2 SQLite store. That store
// uses github.com/mattn/go-sqlite3, which is a stub without cgo, so these
// tests only run in the CGO_ENABLED=1 jobs (go-test-all, Windows/macOS CGO).
func requireHLSQLite(t testing.TB) {
	t.Helper()
	t.Skip("HL SQLite store needs cgo (go-sqlite3); covered by the CGO_ENABLED=1 test jobs")
}
