//go:build !windows && !linux && !darwin

package main

import (
	"fmt"
	"runtime"
)

func availableDiskBytes(string) (uint64, error) {
	return 0, fmt.Errorf("disk-space inspection is unsupported on %s", runtime.GOOS)
}
