package networking

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/chain"
)

func TestPublishPolRespectsSigningJournalGate(t *testing.T) {
	for _, configured := range []bool{false, true} {
		dir := t.TempDir()
		signer, _, err := chain.LoadOrCreateBFTSigner(filepath.Join(dir, "key.json"))
		if err != nil {
			t.Fatal(err)
		}
		vs := chain.NewValidatorSet(chain.DefaultValidatorSetConfig())
		if err := vs.Register(signer.Address(), 100); err != nil {
			t.Fatal(err)
		}
		bc := chain.NewBFTConsensus(vs, chain.DefaultConsensusConfig())
		executor := chain.NewBFTExecutor(bc)
		executor.SetVoteSigner(signer)
		executor.RequireSigningJournal()
		t.Cleanup(func() { _ = executor.CloseSigningJournal() })
		binding, err := chain.NewBFTSigningJournalBinding("test", "genesis", strings.Repeat("c", 64), signer)
		if err != nil {
			t.Fatal(err)
		}
		binding.AllowLegacyMembership = true
		path := filepath.Join(dir, "journal.json")
		if configured {
			if err := executor.ConfigureSigningJournal(path, binding); err != nil {
				t.Fatal(err)
			}
		}
		published := 0
		executor.SetPublisher(func([]byte) error { published++; return nil })
		follower := chain.NewPolFollower(vs, 2.0/3.0)
		follower.SetAnchorFinality(true)
		block := &chain.Block{Height: 9, StateRoot: "root"}
		block.Hash = chain.ComputeBlockHash(block)
		PublishPolAfterBlockSeal(logging.NewSilentLogger(), nil, follower, executor, bc, vs, block)
		if configured {
			if !bc.IsCommitted(9) || published != 3 {
				t.Fatalf("journaled POL round did not complete: committed=%t published=%d", bc.IsCommitted(9), published)
			}
			journal, err := chain.OpenBFTSigningJournal(path, binding)
			if err != nil {
				t.Fatal(err)
			}
			records := journal.Records()
			if len(records) != 3 {
				t.Fatalf("journaled POL round wrote %d records, want 3", len(records))
			}
			for _, record := range records {
				if record.SignedEnvelope == "" {
					t.Fatal("POL round committed without durable envelopes")
				}
			}
		} else {
			if bc.IsCommitted(9) || published != 0 || follower.AllowFinalize(9, "root") || follower.CanExtendFromTip(9, "root") {
				t.Fatal("POL bypassed unconfigured signing journal")
			}
		}
	}
}
