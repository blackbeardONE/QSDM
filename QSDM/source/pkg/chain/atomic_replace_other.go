//go:build !windows

package chain

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
