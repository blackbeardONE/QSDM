//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// benchProcessMemoryCounters is PROCESS_MEMORY_COUNTERS (psapi.h).
type benchProcessMemoryCounters struct {
	cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

// benchPeakMemMB returns the process's peak working set and peak private
// commit in MiB, or -1 for a value it cannot read. Under memory pressure
// Windows trims working sets, so the peak commit is the steadier figure.
func benchPeakMemMB() (rss, commit float64) {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")
	if proc.Find() != nil {
		return -1, -1
	}
	h, err := syscall.GetCurrentProcess()
	if err != nil {
		return -1, -1
	}
	var c benchProcessMemoryCounters
	c.cb = uint32(unsafe.Sizeof(c))
	if r, _, _ := proc.Call(uintptr(h), uintptr(unsafe.Pointer(&c)), uintptr(c.cb)); r == 0 {
		return -1, -1
	}
	return float64(c.PeakWorkingSetSize) / (1 << 20), float64(c.PeakPagefileUsage) / (1 << 20)
}
