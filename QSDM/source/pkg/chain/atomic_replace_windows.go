//go:build windows

package chain

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func replaceFile(source, destination string) error {
	src, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	dst, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	moveErr := windows.MoveFileEx(src, dst, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
	if moveErr == nil {
		return nil
	}

	// Antivirus, indexers, and desktop clients commonly open the destination
	// with read/write sharing but without FILE_SHARE_DELETE. In that case
	// MoveFileEx cannot replace the pathname even though a durable in-place
	// write is allowed. Keep the complete temp file until after the destination
	// has been written and fsynced, so a crash during this fallback still leaves
	// a recoverable snapshot beside the destination.
	data, readErr := os.ReadFile(source)
	if readErr != nil {
		return fmt.Errorf("atomic replace failed (%v); read fallback source: %w", moveErr, readErr)
	}

	var fallbackErr error
	for attempt := 0; attempt < 10; attempt++ {
		file, err := os.OpenFile(destination, os.O_WRONLY|os.O_TRUNC, 0o644)
		if err == nil {
			if _, err = file.Write(data); err == nil {
				err = file.Sync()
			}
			if closeErr := file.Close(); err == nil {
				err = closeErr
			}
		}
		if err == nil {
			_ = os.Remove(source)
			return nil
		}
		fallbackErr = err
		time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
	}

	return fmt.Errorf("atomic replace failed (%v); in-place fallback failed: %w", moveErr, fallbackErr)
}
