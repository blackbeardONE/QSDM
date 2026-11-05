//go:build !windows

package main

// benchPeakMemMB returns the process's peak resident memory in MiB (VmHWM,
// Linux; -1 elsewhere). The peak commit is a Windows figure: -1 here.
func benchPeakMemMB() (rss, commit float64) { return snapBenchVmHWM(), -1 }
