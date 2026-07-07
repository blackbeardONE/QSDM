//go:build windows

// File-level rationale: native Windows Service Control Manager
// dispatch for qsdmminer-console.
//
// Why native and not nssm/winsw: bundling a third-party service
// wrapper in the installer adds a 700 KiB binary, a maintenance
// dependency, and an extra log file. The standard library extension
// `golang.org/x/sys/windows/svc` gives us the SCM protocol in ~80
// lines of Go and produces a single self-supervising binary that's
// indistinguishable from a hand-written Win32 service.
//
// What "service mode" actually means here: when SCM launches us
// (because the operator ran `Start-Service QSDMMiner`), we MUST
// call `svc.Run` within ~30 seconds or SCM kills us with error
// 1053 ("service did not respond in a timely fashion"). svc.Run
// itself blocks until SCM tells us to stop. So the design is:
//
//   1. main() detects via `svc.IsWindowsService` whether we were
//      launched by SCM or by an interactive user. The check is
//      free (no syscall, just the parent-process check Windows
//      already did at exec time).
//   2. If SCM, call `svc.Run(name, handler)` on the main goroutine.
//      svc.Run handshakes with SCM (StartPending → Running) and
//      then dispatches every Stop/Shutdown command to handler.Execute.
//   3. handler.Execute creates a cancellable context and spawns
//      realMain(ctx) on a worker goroutine. The mining loop runs
//      there; SCM commands run here.
//   4. On SCM Stop/Shutdown, Execute cancels the context, waits
//      for realMain to return, reports Stopped, returns the exit
//      code to svc.Run.
//
// This means every signal-handler path in realMain (Ctrl-C,
// SIGTERM, ctx.Done from any other source) collapses to the same
// single shutdown sequence. SCM-stop is just "another Ctrl-C".

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// winServiceName is the canonical service name. It must match the
// name passed to `sc.exe create QSDMMiner ...` by the Inno Setup
// installer; case is significant because Windows compares service
// names case-sensitively in some APIs (most notably the SCM
// dispatch protocol itself).
const winServiceName = "QSDMMiner"

// runWindowsServiceIfNeeded is the platform shim main() calls
// before falling through to the interactive path. Returns
// (handled=true, exitCode) when the SCM path was taken — the
// caller should os.Exit with that code. Returns (false, 0) when
// running interactively and main should proceed normally.
//
// realMain is the regular entrypoint, here invoked with a context
// that this package controls; cancellation of that context
// matches "SCM Stop or Shutdown".
func runWindowsServiceIfNeeded(realMain func(context.Context) int) (handled bool, exitCode int) {
	isService, err := svc.IsWindowsService()
	if err != nil {
		// Defensive: if Windows can't tell us whether we're a
		// service, assume interactive. Wrong answer just means
		// "regular CLI". Right answer for an SCM launch would
		// have us hang waiting for SCM and then get killed with
		// 1053 — which is the same outcome as "wrong answer"
		// from the operator's POV but with worse logging.
		fmt.Fprintf(os.Stderr,
			"qsdmminer: svc.IsWindowsService failed (%v); assuming interactive\n", err)
		return false, 0
	}
	if !isService {
		return false, 0
	}

	h := &winSvcHandler{realMain: realMain}
	if err := svc.Run(winServiceName, h); err != nil {
		// svc.Run failure here means the SCM handshake itself
		// broke (e.g. wrong service name, named-pipe error).
		// Exit non-zero so SCM logs the failure rather than
		// silently retrying forever.
		fmt.Fprintf(os.Stderr, "qsdmminer: svc.Run: %v\n", err)
		return true, 1
	}
	return true, h.exitCode
}

// handoffWindowsServiceIfNeeded handles the child process created by the
// staged updater on Windows. The old service process promotes and starts the
// new executable immediately before it exits. That child is not SCM-owned, so
// it must wait for the old service instance to stop and then ask SCM to launch
// the promoted binary from the registered service path. Without this handoff,
// Task Manager can show an untracked SYSTEM miner while SCM reports Stopped.
func handoffWindowsServiceIfNeeded(args []string) (handled bool, exitCode int) {
	if !hasWindowsServiceFlag(args) {
		return false, 0
	}
	isService, err := svc.IsWindowsService()
	if err == nil && isService {
		return false, 0
	}

	manager, err := mgr.Connect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "qsdmminer: connect to service manager for update handoff: %v\n", err)
		return true, 1
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(winServiceName)
	if err != nil {
		// --service remains usable in development before the service is
		// installed; only take over when the registered service exists.
		return false, 0
	}
	defer service.Close()

	deadline := time.Now().Add(30 * time.Second)
	startRequested := false
	for time.Now().Before(deadline) {
		status, queryErr := service.Query()
		if queryErr != nil {
			fmt.Fprintf(os.Stderr, "qsdmminer: query service during update handoff: %v\n", queryErr)
			return true, 1
		}
		switch status.State {
		case svc.Running:
			return true, 0
		case svc.Stopped:
			if !startRequested {
				if startErr := service.Start(); startErr != nil {
					// SCM recovery may win the race between Query and Start.
					if raced, racedErr := service.Query(); racedErr == nil &&
						(raced.State == svc.Running || raced.State == svc.StartPending) {
						startRequested = true
						break
					}
					fmt.Fprintf(os.Stderr, "qsdmminer: start promoted service: %v\n", startErr)
					return true, 1
				}
				startRequested = true
			}
		case svc.StartPending, svc.StopPending:
			// Wait for SCM to finish the in-flight transition.
		default:
			fmt.Fprintf(os.Stderr, "qsdmminer: service is in unsupported handoff state %d\n", status.State)
			return true, 1
		}
		time.Sleep(250 * time.Millisecond)
	}
	fmt.Fprintln(os.Stderr, "qsdmminer: timed out waiting for promoted service to start")
	return true, 1
}

func hasWindowsServiceFlag(args []string) bool {
	for _, arg := range args {
		name := strings.TrimLeft(strings.SplitN(arg, "=", 2)[0], "-")
		if strings.EqualFold(name, "service") {
			return true
		}
	}
	return false
}

// winSvcHandler implements svc.Handler. The single Execute call
// owns the lifetime of one service start; svc.Run will call
// Execute again if the service is restarted by SCM, but Windows
// generally just exits the process and lets SCM relaunch.
type winSvcHandler struct {
	realMain func(context.Context) int
	exitCode int
}

// Execute is svc.Handler's one method. The contract is:
//
//   - args is whatever was passed via `sc start ServiceName arg1
//     arg2 ...` (NOT the binPath args, which are the ones we
//     register at install time and that arrive on os.Args).
//     We ignore args here because the installer pre-bakes every
//     flag we care about into binPath.
//   - r is the channel SCM uses to deliver control codes.
//   - status is how we report state back to SCM.
//
// The boolean return is "service-specific exit code?" — false
// means "the uint32 is a regular Win32 error code". We never
// invent service-specific codes because they're hard to surface
// in the Event Viewer.
func (h *winSvcHandler) Execute(_ []string, r <-chan svc.ChangeRequest, status chan<- svc.Status) (svcSpecificEC bool, exitCode uint32) {
	// Tell SCM we're booting. Keep StartPending short — SCM
	// has a 30 s deadline; everything heavy in realMain happens
	// AFTER we transition to Running so SCM is immediately happy.
	status <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan int, 1)
	go func() {
		defer func() {
			// realMain panicking would otherwise leak the
			// service in StartPending forever. Convert the
			// panic into an exit-code-77 ("internal panic")
			// so the SCM event log carries it.
			if rec := recover(); rec != nil {
				fmt.Fprintf(os.Stderr, "qsdmminer: panic in realMain: %v\n", rec)
				done <- 77
			}
		}()
		done <- h.realMain(ctx)
	}()

	// Tell SCM we're up. From this point onward we accept Stop
	// and PreShutdown control codes; Pause/Continue are not
	// implemented (a paused mining loop would stop earning
	// rewards without freeing system resources, so the right
	// answer is always "stop").
	status <- svc.Status{
		State:   svc.Running,
		Accepts: svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPreShutdown,
	}

	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				// Standard SCM health probe. Reply with
				// the same status we last asserted.
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown, svc.PreShutdown:
				// Graceful shutdown sequence. Cancelling
				// ctx propagates through every goroutine
				// realMain has spun up (mining loop,
				// renderer, idle probe, auto-updater,
				// enrollment poller). We then wait for
				// realMain to return its own exit code
				// before telling SCM we're done.
				cancel()
				status <- svc.Status{State: svc.StopPending}
				code := <-done
				h.exitCode = code
				status <- svc.Status{State: svc.Stopped}
				return false, uint32(code)
			}
		case code := <-done:
			// realMain exited on its own (e.g. config
			// error, --setup with no continuation flags).
			// Report Stopped with realMain's exit code.
			h.exitCode = code
			status <- svc.Status{State: svc.Stopped}
			return false, uint32(code)
		}
	}
}
