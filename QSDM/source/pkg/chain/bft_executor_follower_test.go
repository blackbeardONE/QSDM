package chain

import (
	"fmt"
	"testing"
	"time"
)

func TestBFTExecutor_NoteFollowerAppend(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	_ = vs.Register("v1", 100)
	ex := NewBFTExecutor(NewBFTConsensus(vs, DefaultConsensusConfig()))
	if len(ex.FollowerAppendDiagnostic()) != 0 {
		t.Fatalf("expected empty diagnostic before any append")
	}
	ex.NoteFollowerAppend(nil)
	ex.NoteFollowerAppend(nil)
	ex.NoteFollowerAppend(errFollowerTest("x"))
	ok, sk, cx := ex.FollowerAppendStats()
	if ok != 2 || sk != 1 || cx != 0 {
		t.Fatalf("stats ok=%d skip=%d conflict=%d", ok, sk, cx)
	}
	d := ex.FollowerAppendDiagnostic()
	if d["last_ok"] != false {
		t.Fatalf("last_ok: %v", d["last_ok"])
	}
	if d["last_error"] != "x" {
		t.Fatalf("last_error: %v", d["last_error"])
	}
	at, _ := d["last_at"].(string)
	if at == "" {
		t.Fatal("expected last_at")
	}
	if _, err := time.Parse(time.RFC3339Nano, at); err != nil {
		t.Fatalf("last_at parse: %v", err)
	}
}

func TestBFTExecutor_NoteFollowerAppendConflictCounter(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	_ = vs.Register("v1", 100)
	ex := NewBFTExecutor(NewBFTConsensus(vs, DefaultConsensusConfig()))
	ex.NoteFollowerAppend(fmt.Errorf("wrapped: %w", ErrExternalAppendConflict))
	_, sk, cx := ex.FollowerAppendStats()
	if sk != 0 || cx != 1 {
		t.Fatalf("expected conflict not skip, skip=%d conflict=%d", sk, cx)
	}
}

type errFollowerTest string

func (e errFollowerTest) Error() string { return string(e) }
