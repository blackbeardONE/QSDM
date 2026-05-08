//go:build windows

package main

import (
	"context"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

// drainStatus collects every svc.Status the handler emits until the
// channel is closed or the timeout fires. Tests assert on the
// observed sequence of states (StartPending → Running → StopPending
// → Stopped) which is the contract SCM expects.
func drainStatus(t *testing.T, ch <-chan svc.Status, timeout time.Duration) []svc.State {
	t.Helper()
	var states []svc.State
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case s, ok := <-ch:
			if !ok {
				return states
			}
			states = append(states, s.State)
			if s.State == svc.Stopped {
				return states
			}
		case <-deadline.C:
			return states
		}
	}
}

// TestWinSvcHandler_GracefulStop simulates SCM sending a Stop
// command while realMain is still running. Expected sequence:
// StartPending → Running → (Stop arrives) → StopPending → Stopped.
// The handler must cancel the realMain context and report Stopped
// to SCM with realMain's actual exit code.
func TestWinSvcHandler_GracefulStop(t *testing.T) {
	realMainStarted := make(chan struct{})
	realMainDone := make(chan struct{})
	fake := func(ctx context.Context) int {
		close(realMainStarted)
		<-ctx.Done()
		close(realMainDone)
		return 0
	}

	h := &winSvcHandler{realMain: fake}

	r := make(chan svc.ChangeRequest, 4)
	status := make(chan svc.Status, 8)

	executeDone := make(chan struct{})
	var ssec bool
	var ec uint32
	go func() {
		ssec, ec = h.Execute(nil, r, status)
		close(executeDone)
	}()

	select {
	case <-realMainStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("realMain did not start within 2s")
	}

	r <- svc.ChangeRequest{Cmd: svc.Stop}

	select {
	case <-executeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Execute did not return within 2s after Stop")
	}

	select {
	case <-realMainDone:
	case <-time.After(2 * time.Second):
		t.Fatal("realMain did not observe ctx cancellation")
	}

	if ssec {
		t.Errorf("svcSpecificEC must be false; got true")
	}
	if ec != 0 {
		t.Errorf("exit code = %d, want 0 (realMain returned 0)", ec)
	}
	if h.exitCode != 0 {
		t.Errorf("h.exitCode = %d, want 0", h.exitCode)
	}

	close(status)
	states := drainStatus(t, status, 1*time.Second)
	wantPrefix := []svc.State{svc.StartPending, svc.Running, svc.StopPending, svc.Stopped}
	if len(states) < len(wantPrefix) {
		t.Fatalf("status states = %v, want at least %v", states, wantPrefix)
	}
	for i, w := range wantPrefix {
		if states[i] != w {
			t.Errorf("states[%d] = %v, want %v", i, states[i], w)
		}
	}
}

// TestWinSvcHandler_RealMainExitsOnOwn covers the "config error
// path" — realMain returns a non-zero exit code without waiting for
// SCM. The handler must propagate that code to SCM as the service
// exit code. Sequence: StartPending → Running → Stopped (no
// StopPending because we never asked for a stop).
func TestWinSvcHandler_RealMainExitsOnOwn(t *testing.T) {
	fake := func(_ context.Context) int { return 2 }
	h := &winSvcHandler{realMain: fake}

	r := make(chan svc.ChangeRequest, 1)
	status := make(chan svc.Status, 8)

	executeDone := make(chan struct{})
	var ec uint32
	go func() {
		_, ec = h.Execute(nil, r, status)
		close(executeDone)
	}()

	select {
	case <-executeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Execute did not return within 2s")
	}

	if ec != 2 {
		t.Errorf("exit code = %d, want 2", ec)
	}
	if h.exitCode != 2 {
		t.Errorf("h.exitCode = %d, want 2", h.exitCode)
	}

	close(status)
	states := drainStatus(t, status, 1*time.Second)
	if got := len(states); got < 3 {
		t.Fatalf("expected at least 3 status updates, got %d (%v)", got, states)
	}
	if states[0] != svc.StartPending {
		t.Errorf("states[0] = %v, want StartPending", states[0])
	}
	if states[1] != svc.Running {
		t.Errorf("states[1] = %v, want Running", states[1])
	}
	if states[len(states)-1] != svc.Stopped {
		t.Errorf("final state = %v, want Stopped", states[len(states)-1])
	}
}

// TestWinSvcHandler_PanicConvertedToExitCode77: a panic in
// realMain must not leave the service stuck in StartPending. The
// recover() in Execute should convert it to exit code 77 (the
// "internal panic" sentinel surfaced in stderr + Event Viewer).
func TestWinSvcHandler_PanicConvertedToExitCode77(t *testing.T) {
	fake := func(_ context.Context) int {
		panic("boom")
	}
	h := &winSvcHandler{realMain: fake}

	r := make(chan svc.ChangeRequest, 1)
	status := make(chan svc.Status, 8)

	executeDone := make(chan struct{})
	var ec uint32
	go func() {
		_, ec = h.Execute(nil, r, status)
		close(executeDone)
	}()

	select {
	case <-executeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Execute did not return within 2s after panic")
	}
	if ec != 77 {
		t.Errorf("panic exit code = %d, want 77", ec)
	}
}

// TestWinSvcHandler_InterrogateEchoesStatus verifies the SCM
// health-check path: receiving an Interrogate must reply with the
// same status we last asserted. SCM polls this for service-state
// dashboards in services.msc.
func TestWinSvcHandler_InterrogateEchoesStatus(t *testing.T) {
	hold := make(chan struct{})
	fake := func(ctx context.Context) int {
		<-hold
		<-ctx.Done()
		return 0
	}
	h := &winSvcHandler{realMain: fake}

	r := make(chan svc.ChangeRequest, 4)
	status := make(chan svc.Status, 16)

	go func() { _, _ = h.Execute(nil, r, status) }()

	// Drain StartPending + Running.
	for i := 0; i < 2; i++ {
		select {
		case <-status:
		case <-time.After(2 * time.Second):
			t.Fatalf("missing status update %d", i)
		}
	}

	probe := svc.Status{State: svc.Running, Accepts: svc.AcceptStop}
	r <- svc.ChangeRequest{Cmd: svc.Interrogate, CurrentStatus: probe}

	select {
	case s := <-status:
		if s.State != svc.Running {
			t.Errorf("interrogate echo state = %v, want Running", s.State)
		}
		if s.Accepts != svc.AcceptStop {
			t.Errorf("interrogate echo accepts = %v, want AcceptStop", s.Accepts)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no echo within 2s")
	}

	close(hold)
	r <- svc.ChangeRequest{Cmd: svc.Stop}
}
