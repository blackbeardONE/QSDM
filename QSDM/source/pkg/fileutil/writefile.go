package fileutil

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// WriteFileAtomic writes data to path through a same-directory temp file.
//
// On POSIX, os.Rename replaces the destination atomically. On Windows, local
// operator machines can reject temp-file replacement and leave the temp file
// locked, so we use a direct overwrite for these small JSON snapshots.
func WriteFileAtomic(path string, data []byte, perm fs.FileMode) error {
	if runtime.GOOS == "windows" {
		if err := os.WriteFile(path, data, perm); err != nil {
			return fmt.Errorf("write file %q: %w", path, err)
		}
		return nil
	}

	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, "."+base+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := replaceFile(tmpName, path); err != nil {
		return fmt.Errorf("replace %q -> %q: %w", tmpName, path, err)
	}
	cleanup = false
	return nil
}
