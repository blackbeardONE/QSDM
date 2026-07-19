//go:build windows

package main

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

func availableDiskBytes(path string) (uint64, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return 0, err
	}
	directory, err := windows.UTF16PtrFromString(absolute)
	if err != nil {
		return 0, err
	}
	var available uint64
	var total uint64
	var totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(directory, &available, &total, &totalFree); err != nil {
		return 0, err
	}
	return available, nil
}
