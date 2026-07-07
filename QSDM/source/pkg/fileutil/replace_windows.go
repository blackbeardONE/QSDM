//go:build windows

package fileutil

import (
	"errors"

	"golang.org/x/sys/windows"
)

func replaceFile(src, dst string) error {
	srcp, err := windows.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	dstp, err := windows.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(srcp, dstp, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func retryableReplaceError(err error) bool {
	// Antivirus and indexers commonly hold short-lived sharing locks. Access
	// denied is different: retrying the identical ACL-prohibited operation only
	// stalls every block, so the caller should use its synced fallback at once.
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}

func atomicReplaceUnavailable(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
