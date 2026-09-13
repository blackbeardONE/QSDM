//go:build !windows

package fileutil

import "os"

func replaceFile(src, dst string) error {
	return os.Rename(src, dst)
}

func retryableReplaceError(error) bool {
	return true
}

func atomicReplaceUnavailable(error) bool {
	return false
}

func syncParentDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
