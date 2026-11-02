//go:build hl_crashpoints

package main

import (
	"errors"
	"strings"
	"syscall"
	"testing"
)

func TestHL1CrashpointFaultInjection(t *testing.T) {
	t.Setenv("QSDM_HL1_FAULT", "persist:H3:ENOSPC, persist:H6:5, submit:accept:EIO, bad")
	t.Setenv("QSDM_HL1_CRASH", "")
	hl1ResetCrashpoints()
	defer hl1ResetCrashpoints()

	if err := hl1Fault("persist:H3"); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("persist:H3 fault = %v, want ENOSPC", err)
	}
	if err := hl1Fault("persist:H6"); !errors.Is(err, syscall.Errno(5)) {
		t.Fatalf("persist:H6 fault = %v, want errno 5", err)
	}
	if err := hl1Fault("persist:H4"); err != nil {
		t.Fatalf("unarmed point faulted: %v", err)
	}
	hl1Crashpoint("persist:after-H3") // not armed: must return

	// F1: an injected ENOSPC at H3 fail-stops the hook with that cause.
	rec := &hl1Recorder{}
	fsr := &hl1FailStopRecord{}
	h, _ := hl1TestHook(rec, fsr, &hl1DurableTip{}, false)
	h.OnSealedBlock(hl1TestBlock(5, hl1Heartbeat(9)))
	if len(fsr.causes) != 1 || !strings.HasPrefix(fsr.causes[0], "persist:H3:") {
		t.Fatalf("failStop causes = %q, want persist:H3", fsr.causes)
	}
	for _, op := range rec.Ops() {
		if strings.HasPrefix(op, "H3:accounts") {
			t.Fatalf("H3 ran despite the injected fault: %q", rec.Ops())
		}
	}
	if !hl1CrashpointsEnabled {
		t.Fatal("hl1CrashpointsEnabled is false in an hl_crashpoints build")
	}
}
