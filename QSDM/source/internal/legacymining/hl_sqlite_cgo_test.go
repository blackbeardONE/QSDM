//go:build cgo

package legacymining

import "testing"

// requireHLSQLite is a no-op when cgo is enabled: the HL1/HL2 SQLite store
// (github.com/mattn/go-sqlite3) is available.
func requireHLSQLite(t testing.TB) { t.Helper() }
