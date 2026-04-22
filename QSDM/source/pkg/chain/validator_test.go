package chain

import (
	"testing"
	"time"
)

func TestValidatorSet_Register(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())

	if err := vs.Register("val1", 200); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if vs.Size() != 1 {
		t.Fatalf("expected 1 validator, got %d", vs.Size())
	}

	// duplicate
	if err := vs.Register("val1", 200); err == nil {
		t.Fatal("expected error for duplicate registration")
	}
}

func TestValidatorSet_MinStake(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())

	if err := vs.Register("val1", 10); err == nil {
		t.Fatal("expected error for insufficient stake")
	}
}

func TestValidatorSet_MaxValidators(t *testing.T) {
	cfg := DefaultValidatorSetConfig()
	cfg.MaxValidators = 2
	vs := NewValidatorSet(cfg)

	vs.Register("v1", 200)
	vs.Register("v2", 200)
	if err := vs.Register("v3", 200); err == nil {
		t.Fatal("expected error for full validator set")
	}
}

func TestValidatorSet_AddStake(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	vs.Register("val1", 200)

	vs.AddStake("val1", 50)
	v, _ := vs.GetValidator("val1")
	if v.Stake != 250 {
		t.Fatalf("expected stake 250, got %f", v.Stake)
	}
}

func TestValidatorSet_Slash(t *testing.T) {
	cfg := DefaultValidatorSetConfig()
	cfg.SlashFraction = 0.10
	cfg.JailDuration = 10 * time.Millisecond
	vs := NewValidatorSet(cfg)
	vs.Register("val1", 1000)

	event, err := vs.Slash("val1", SlashDoubleSign)
	if err != nil {
		t.Fatalf("Slash: %v", err)
	}
	if event.Amount != 100 {
		t.Fatalf("expected slash 100, got %f", event.Amount)
	}

	v, _ := vs.GetValidator("val1")
	if v.Status != ValidatorJailed {
		t.Fatal("expected jailed status")
	}
	if v.Stake != 900 {
		t.Fatalf("expected 900 remaining, got %f", v.Stake)
	}
}

func TestValidatorSet_Unjail(t *testing.T) {
	cfg := DefaultValidatorSetConfig()
	cfg.SlashFraction = 0.01
	cfg.JailDuration = 1 * time.Millisecond
	vs := NewValidatorSet(cfg)
	vs.Register("val1", 1000)
	vs.Slash("val1", SlashDowntime)

	// Too early
	if err := vs.Unjail("val1"); err == nil {
		t.Fatal("expected error when jail time not elapsed")
	}

	time.Sleep(5 * time.Millisecond)

	if err := vs.Unjail("val1"); err != nil {
		t.Fatalf("Unjail: %v", err)
	}
	v, _ := vs.GetValidator("val1")
	if v.Status != ValidatorActive {
		t.Fatal("expected active after unjail")
	}
}

func TestValidatorSet_Exit(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	vs.Register("val1", 200)

	v, err := vs.Exit("val1")
	if err != nil {
		t.Fatalf("Exit: %v", err)
	}
	if v.Status != ValidatorExited {
		t.Fatal("expected exited status")
	}

	active := vs.ActiveValidators()
	if len(active) != 0 {
		t.Fatalf("expected 0 active, got %d", len(active))
	}
}

func TestValidatorSet_EpochRotation(t *testing.T) {
	cfg := DefaultValidatorSetConfig()
	cfg.EpochBlocks = 3
	vs := NewValidatorSet(cfg)
	vs.Register("val1", 200)

	if vs.CurrentEpoch() != 0 {
		t.Fatal("expected epoch 0")
	}

	vs.RecordBlock("val1")
	vs.RecordBlock("val1")
	advanced := vs.RecordBlock("val1")

	if !advanced {
		t.Fatal("expected epoch to advance after 3 blocks")
	}
	if vs.CurrentEpoch() != 1 {
		t.Fatalf("expected epoch 1, got %d", vs.CurrentEpoch())
	}
}

func TestValidatorSet_ActiveSortedByStake(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	vs.Register("low", 150)
	vs.Register("mid", 500)
	vs.Register("high", 1000)

	active := vs.ActiveValidators()
	if len(active) != 3 {
		t.Fatalf("expected 3 active, got %d", len(active))
	}
	if active[0].Address != "high" {
		t.Fatalf("expected 'high' first, got %s", active[0].Address)
	}
}

func TestValidatorSet_ProposerForEpoch(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	vs.Register("low", 200)
	vs.Register("high", 5000)

	proposer, err := vs.ProposerForEpoch()
	if err != nil {
		t.Fatalf("ProposerForEpoch: %v", err)
	}
	if proposer != "high" {
		t.Fatalf("expected 'high' as proposer, got %s", proposer)
	}
}

func TestValidatorSet_SlashLog(t *testing.T) {
	cfg := DefaultValidatorSetConfig()
	cfg.JailDuration = 1 * time.Millisecond
	vs := NewValidatorSet(cfg)
	vs.Register("v1", 1000)
	vs.Register("v2", 1000)

	vs.Slash("v1", SlashDoubleSign)
	time.Sleep(2 * time.Millisecond)
	vs.Unjail("v1")
	vs.Slash("v2", SlashDowntime)

	log := vs.SlashLog()
	if len(log) != 2 {
		t.Fatalf("expected 2 slash events, got %d", len(log))
	}
}

func TestValidatorSet_CannotSlashExited(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	vs.Register("v1", 500)
	vs.Exit("v1")

	_, err := vs.Slash("v1", SlashDoubleSign)
	if err == nil {
		t.Fatal("expected error slashing exited validator")
	}
}
