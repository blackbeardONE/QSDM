package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/config"
)

func startupJournalFixture(t *testing.T, dir string) *chain.BFTExecutor {
	t.Helper()
	signer, _, err := chain.LoadOrCreateBFTSigner(filepath.Join(dir, "signer.json"))
	if err != nil {
		t.Fatal(err)
	}
	executor := chain.NewBFTExecutor(chain.NewBFTConsensus(chain.NewValidatorSet(chain.DefaultValidatorSetConfig()), chain.DefaultConsensusConfig()))
	executor.SetVoteSigner(signer)
	t.Cleanup(func() { _ = executor.CloseSigningJournal() })
	return executor
}

func startupJournalGenesis() *chain.Block {
	genesis := &chain.Block{Height: 0, StateRoot: "genesis-root", Timestamp: time.Unix(1700000000, 0), ProducerID: "genesis-producer"}
	genesis.Hash = chain.ComputeBlockHash(genesis)
	return genesis
}

func TestConsensusJournalStartupRestartAndBindings(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{ConsensusSigningJournal: true}
	consensus := chain.DefaultConsensusConfig()
	executor := startupJournalFixture(t, dir)
	address := chain.BFTValidatorAddress(executor.VoteSigner().GetPublicKey())
	if err := prepareConsensusJournalGate(executor, dir, true); err != nil {
		t.Fatal(err)
	}
	if err := executor.BroadcastPrevote(1, 0, address, "root"); !errors.Is(err, chain.ErrBFTSigningUnavailable) {
		t.Fatalf("startup exposed signer before restore: %v", err)
	}
	if err := configureConsensusJournal(executor, dir, startupJournalGenesis(), cfg, consensus); err != nil {
		t.Fatal(err)
	}
	if err := executor.BroadcastPrevote(1, 0, address, "root"); err != nil {
		t.Fatal(err)
	}
	if err := executor.CloseSigningJournal(); err != nil {
		t.Fatal(err)
	}
	restarted := startupJournalFixture(t, dir)
	if err := configureConsensusJournal(restarted, dir, startupJournalGenesis(), cfg, consensus); err != nil {
		t.Fatal(err)
	}
	if err := restarted.BroadcastPrevote(1, 0, address, "root"); err != nil {
		t.Fatal(err)
	}
	if err := restarted.BroadcastPrevote(1, 0, address, "conflict"); !errors.Is(err, chain.ErrBFTSigningJournalConflict) {
		t.Fatalf("startup lost signing history: %v", err)
	}
	if err := restarted.CloseSigningJournal(); err != nil {
		t.Fatal(err)
	}
	for _, changed := range []string{"genesis", "configuration", "signer"} {
		t.Run(changed, func(t *testing.T) {
			executor := startupJournalFixture(t, dir)
			genesis := startupJournalGenesis()
			rules := consensus
			switch changed {
			case "genesis":
				genesis.StateRoot = "another-genesis"
				genesis.Hash = chain.ComputeBlockHash(genesis)
			case "configuration":
				rules.MaxRounds++
			case "signer":
				other := startupJournalFixture(t, t.TempDir())
				executor.SetVoteSigner(other.VoteSigner())
			}
			if err := configureConsensusJournal(executor, dir, genesis, cfg, rules); !errors.Is(err, chain.ErrBFTSigningJournalBindingMismatch) {
				t.Fatalf("changed %s did not fail closed: %v", changed, err)
			}
		})
	}
}

func TestConsensusJournalGateCannotBeDisabledAfterInitialization(t *testing.T) {
	dir := t.TempDir()
	executor := startupJournalFixture(t, dir)
	if err := prepareConsensusJournalGate(executor, dir, false); err != nil {
		t.Fatalf("disabled default changed startup: %v", err)
	}
	if executor.SigningJournalRequired() {
		t.Fatal("journal enabled by default")
	}
	cfg := &config.Config{ConsensusSigningJournal: true}
	if err := configureConsensusJournal(executor, dir, startupJournalGenesis(), cfg, chain.DefaultConsensusConfig()); err != nil {
		t.Fatal(err)
	}
	if err := executor.CloseSigningJournal(); err != nil {
		t.Fatal(err)
	}
	if err := prepareConsensusJournalGate(startupJournalFixture(t, dir), dir, false); err == nil {
		t.Fatal("disabled setting bypassed existing journal")
	}
	if err := os.Remove(filepath.Join(dir, consensusSigningJournalFile)); err != nil {
		t.Fatal(err)
	}
	if err := prepareConsensusJournalGate(startupJournalFixture(t, dir), dir, false); err == nil {
		t.Fatal("disabled setting bypassed missing initialized journal")
	}
	if err := configureConsensusJournal(startupJournalFixture(t, dir), dir, startupJournalGenesis(), cfg, chain.DefaultConsensusConfig()); err == nil {
		t.Fatal("startup silently recreated missing journal")
	}
}

func TestConsensusJournalRejectsUnrestoredGenesis(t *testing.T) {
	for _, genesis := range []*chain.Block{nil, {}, {Height: 0, Hash: "unverified"}} {
		dir := t.TempDir()
		executor := startupJournalFixture(t, dir)
		if err := configureConsensusJournal(executor, dir, genesis, &config.Config{ConsensusSigningJournal: true}, chain.DefaultConsensusConfig()); err == nil {
			t.Fatal("journal accepted a missing or invalid genesis")
		}
		if !executor.SigningJournalRequired() {
			t.Fatal("failed startup left signing ungated")
		}
		if _, err := os.Stat(filepath.Join(dir, consensusSigningJournalFile)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unverified genesis created a journal: %v", err)
		}
	}
}
