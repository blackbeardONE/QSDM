package fileutil

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

var (
	directWriteDirectories sync.Map
	directWriteLocks       sync.Map
	replaceFileForWrite    = replaceFile
)

// WriteFileAtomic writes data to path through a same-directory temp file.
//
// The normal path never truncates the destination in place. Windows
// replacement can briefly fail while antivirus or an indexer has the
// destination open, so replacement is retried for a short bounded period. A
// directory that permanently denies replacement uses serialized, synced
// overwrites thereafter; critical state callers pair those writes with a
// separately validated .last-good snapshot.
func WriteFileAtomic(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	directWriteKey := filepath.Clean(dir)
	if runtime.GOOS == "windows" {
		if _, direct := directWriteDirectories.Load(directWriteKey); direct {
			if err := writeFileSyncedSerialized(path, data, perm); err != nil {
				return fmt.Errorf("direct synced write %q: %w", path, err)
			}
			return nil
		}
	}
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

	var replaceErr error
	for attempt := 0; attempt < 8; attempt++ {
		if replaceErr = replaceFileForWrite(tmpName, path); replaceErr == nil {
			cleanup = false
			return nil
		}
		if !retryableReplaceError(replaceErr) {
			break
		}
		time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
	}
	if runtime.GOOS == "windows" {
		// Some operator workspaces deny delete/rename while still allowing
		// writes. Critical state users keep a separately validated .last-good
		// snapshot before calling this for the primary, so a synced overwrite
		// is safe here and avoids permanently disabling block production.
		if err := writeFileSyncedSerialized(path, data, perm); err == nil {
			if atomicReplaceUnavailable(replaceErr) {
				directWriteDirectories.Store(directWriteKey, struct{}{})
			}
			return nil
		} else {
			return fmt.Errorf("replace %q -> %q failed (%v), direct synced write also failed: %w", tmpName, path, replaceErr, err)
		}
	}
	return fmt.Errorf("replace %q -> %q after retries: %w", tmpName, path, replaceErr)
}

func writeFileSyncedSerialized(path string, data []byte, perm fs.FileMode) error {
	lockValue, _ := directWriteLocks.LoadOrStore(filepath.Clean(path), &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	return writeFileSynced(path, data, perm)
}

func writeFileSynced(path string, data []byte, perm fs.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
