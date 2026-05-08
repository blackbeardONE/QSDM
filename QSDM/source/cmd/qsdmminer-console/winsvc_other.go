//go:build !windows

// Non-Windows stub for the SCM dispatcher. Linux uses systemd,
// macOS uses launchd, and both deliver SIGTERM to the process
// rather than going through a userspace dispatcher — so realMain's
// signal.NotifyContext on SIGTERM is the only code path needed.
// runWindowsServiceIfNeeded therefore always returns "not handled"
// here, which causes main() to fall through to the interactive
// path and let realMain's signal handlers do the work.

package main

import "context"

func runWindowsServiceIfNeeded(_ func(context.Context) int) (handled bool, exitCode int) {
	return false, 0
}
