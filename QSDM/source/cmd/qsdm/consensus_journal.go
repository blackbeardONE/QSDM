package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/config"
)

const consensusSigningJournalFile = "qsdm_bft_signing_journal.json"

// prepareConsensusJournalGate runs before any BFT signer is exposed. Once a
// journal exists, changing the flag must not bypass its reservations.
func prepareConsensusJournalGate(executor *chain.BFTExecutor, stateDir string, enabled bool) error {
	if enabled {
		executor.RequireSigningJournal()
		return nil
	}
	for _, suffix := range []string{"", ".binding"} {
		path := filepath.Join(stateDir, consensusSigningJournalFile+suffix)
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			executor.RequireSigningJournal()
			return fmt.Errorf("consensus signing journal exists or cannot be inspected at %s; enable consensus.signing_journal to resume safely", path)
		}
	}
	return nil
}

// configureConsensusJournal runs only after canonical chain/account restore.
// Genesis is the immutable chain binding; a fresh or unverified chain cannot
// initialize durable signing through this staged startup path.
func configureConsensusJournal(executor *chain.BFTExecutor, stateDir string, genesis *chain.Block, cfg *config.Config, consensus chain.ConsensusConfig) error {
	if !cfg.ConsensusSigningJournal {
		return nil
	}
	executor.RequireSigningJournal()
	if genesis == nil || genesis.Height != 0 || genesis.PrevHash != "" || genesis.Hash == "" || chain.ComputeBlockHash(genesis) != genesis.Hash {
		return errors.New("consensus signing journal requires a restored, verified genesis; bootstrap the staging chain before opting in")
	}
	// These settings affect signing or the block/state roots being signed.
	rules := struct {
		Version        int
		Consensus      chain.ConsensusConfig
		RequireSigned  bool
		SignedHeight   uint64
		TaskHeight     uint64
		TxRootHeight   uint64
		EnrollmentRoot uint64
		DustHeight     uint64
	}{1, consensus, cfg.RequireSignedVotes, cfg.SignedConsensusActivationHeight,
		cfg.TaskActionSignatureActivationHeight, cfg.TxContentRootActivationHeight,
		cfg.EnrollmentStateRootActivationHeight, cfg.ForkDustHeight}
	raw, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	binding, err := chain.NewBFTSigningJournalBinding("qsdm", genesis.Hash, hex.EncodeToString(sum[:]), executor.VoteSigner())
	if err != nil {
		return err
	}
	// Runtime membership is still singleton/legacy. Preserve the exact empty
	// wire root instead of inventing a supposedly chain-committed membership.
	binding.AllowLegacyMembership = true
	return executor.ConfigureSigningJournal(filepath.Join(stateDir, consensusSigningJournalFile), binding)
}
