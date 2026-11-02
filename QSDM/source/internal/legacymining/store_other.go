//go:build !unix

package legacymining

import "os"

// legacyStorePOSIX disables the Store.Open mode checks: they run on Unix
// only. Windows access is governed by directory ACLs.
const legacyStorePOSIX = false

// legacyStoreCheckOwner is a no-op off Unix.
func legacyStoreCheckOwner(string, os.FileInfo) error { return nil }
