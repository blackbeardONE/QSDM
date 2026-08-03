//go:build !windows

package account

import "os"

func replaceStoreFile(temporaryPath, targetPath string) error {
	return os.Rename(temporaryPath, targetPath)
}
