//go:build !windows && !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package chain

import (
	"errors"
	"os"
)

func lockStateFile(*os.File) error {
	return errors.New("state-directory locking is unsupported on this platform")
}

func unlockStateFile(*os.File) error { return nil }
