//go:build unix

package legacymining

import (
	"fmt"
	"os"
	"syscall"
)

// legacyStorePOSIX enables the Store.Open mode checks (Unix only).
const legacyStorePOSIX = true

// legacyStoreCheckOwner requires name to be owned by the effective user.
func legacyStoreCheckOwner(name string, fi os.FileInfo) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%w: %s: no owner information", ErrUnsafePath, name)
	}
	if int64(st.Uid) != int64(os.Geteuid()) {
		return fmt.Errorf("%w: %s is owned by uid %d, not %d", ErrUnsafePath, name, st.Uid, os.Geteuid())
	}
	return nil
}
