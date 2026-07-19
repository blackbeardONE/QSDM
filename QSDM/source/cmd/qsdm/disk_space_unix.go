//go:build linux || darwin

package main

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func availableDiskBytes(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	if stat.Bsize <= 0 {
		return 0, fmt.Errorf("filesystem reported invalid block size %d", stat.Bsize)
	}
	// Statfs block size is signed on Linux and unsigned on Darwin. The
	// positive-value check above makes the Linux narrowing conversion safe.
	blockSize := uint64(stat.Bsize) // #nosec G115 -- guarded against negative values above
	return checkedAvailableDiskBytes(stat.Bavail, blockSize)
}
