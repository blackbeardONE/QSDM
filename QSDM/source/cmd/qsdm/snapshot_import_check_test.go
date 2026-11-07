package main

// The snapshot import, the export check and the tail replay read the journal
// once (chain.CheckJournal) instead of loading every block with its signature
// and verifying the loaded chain again. These tests hold the new path to the
// old one: snapshotVerifyDirBefore below is snapshotVerifyDir as it was on
// main at ff6a9f8, copied verbatim, and every snapshot here, intact or
// tampered, truncated, under a wrong pin or a wrong configuration, alone or
// several at once, must give both the same error, or the same result and the
// same scratch files. The import must leave the same state directory, the
// tail replay must refuse as and when it did, and both must hold every
// producer signature but the tip's only in the journal, which main did not.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/config"
	"github.com/blackbeardONE/QSDM/pkg/governance/chainparams"
	"github.com/blackbeardONE/QSDM/pkg/ledgersnapshot"
	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

// snapshotVerifyDirBefore is snapshotVerifyDir at main ff6a9f8, before the
// journal was read once: LoadChainNDJSON, a hash loop and
// VerifyProducerTransitionChain. Do not edit it: it is the reference.
func snapshotVerifyDirBefore(dir, scratch string, c snapshotConsensus, pinned *config.SnapshotBootstrapConfig) (snapshotVerifyResult, error) {
	var res snapshotVerifyResult
	log := c.Log
	if log == nil {
		log = hl1Logger()
	}
	// The package-level consensus settings first: the hash and signature
	// checks below depend on them (the content-root activation height changes
	// every block hash from that height on). Applied after them, a real chain
	// that crosses an activation height fails at that height.
	if c.ConsensusSettings != nil {
		c.ConsensusSettings()
	}
	journal := filepath.Join(dir, hl1JournalName)
	if err := c.Transition.ValidateJournalPrefix(journal); err != nil {
		return res, fmt.Errorf("producer transition: %w", err)
	}
	if err := hl1RequireFinalNewline(hl1OSFS{}, journal); err != nil {
		return res, fmt.Errorf("journal: %w", err)
	}
	fi, err := os.Stat(journal)
	if err != nil {
		return res, fmt.Errorf("journal: %w", err)
	}
	res.JournalBytes = fi.Size()
	loadStart := time.Now()
	blocks, err := chain.LoadChainNDJSON(journal)
	if err != nil {
		return res, fmt.Errorf("journal: %w", err)
	}
	res.LoadDuration = time.Since(loadStart)
	if len(blocks) == 0 {
		return res, errors.New("journal holds no block")
	}
	if _, dropped := canonicalPersistedChain(blocks); dropped != 0 {
		return res, fmt.Errorf("journal contains %d forked duplicate block(s)", dropped)
	}
	// The pinned tip hash commits to every block only if the chain is one
	// hash-linked line back to genesis.
	for i, b := range blocks {
		switch {
		case b == nil:
			return res, fmt.Errorf("journal block %d is null", i)
		case b.Height != uint64(i):
			return res, fmt.Errorf("journal is not contiguous from genesis: line %d holds height %d", i+1, b.Height)
		case i == 0 && b.PrevHash != "":
			return res, errors.New("journal does not start at genesis (block 0 has a prev hash)")
		case i > 0 && b.PrevHash != blocks[i-1].Hash:
			return res, fmt.Errorf("journal block %d does not link to block %d", b.Height, b.Height-1)
		case chain.ComputeBlockHash(b) != b.Hash:
			return res, fmt.Errorf("journal block %d hash does not match its contents", b.Height)
		}
	}
	tip := blocks[len(blocks)-1]
	res.Tip, res.Blocks = tip, len(blocks)
	if pinned != nil {
		if tip.Height != pinned.CheckpointHeight {
			return res, fmt.Errorf("snapshot tip is height %d; the trusted checkpoint is height %d", tip.Height, pinned.CheckpointHeight)
		}
		if tip.Hash != pinned.CheckpointBlockHash {
			return res, fmt.Errorf("snapshot block %d hash %s differs from the trusted checkpoint %s", tip.Height, tip.Hash, pinned.CheckpointBlockHash)
		}
		if tip.StateRoot != pinned.CheckpointStateRoot {
			return res, fmt.Errorf("snapshot block %d state root %s differs from the trusted checkpoint %s", tip.Height, tip.StateRoot, pinned.CheckpointStateRoot)
		}
		if res.AuthorizedBy, err = snapshotAuthorizeTip(tip, c, pinned.CheckpointProducerID); err != nil {
			return res, err
		}
	}
	for _, b := range blocks {
		for _, tx := range b.Transactions {
			if tx != nil && tx.ContractID == chainparams.ContractID {
				return res, fmt.Errorf("block %d holds governance parameter transaction %s: the parameter store is not committed by the state root and is not rebuilt from blocks, so it cannot be verified; sync this node from genesis", b.Height, tx.ID)
			}
		}
	}
	if err := snapshotRequireEmptyStaking(filepath.Join(dir, hl1StakingName)); err != nil {
		return res, err
	}

	restoreStart := time.Now()
	st, err := hl1NewReplayStack(scratch, c.GovernanceAuthorities, c.AuthorizedBlockProducers, c.Transition, log)
	if err != nil {
		return res, err
	}
	verified, err := st.producer.VerifyProducerTransitionChain(blocks)
	if err != nil {
		return res, fmt.Errorf("producer transition persisted chain: %w", err)
	}
	if err := st.restore(blocks, verified, filepath.Join(dir, hl1AccountsName), filepath.Join(dir, hl1EnrollmentName),
		filepath.Join(scratch, "absent-receipts.ndjson")); err != nil {
		return res, fmt.Errorf("state restore: %w", err)
	}
	if c.Transition != nil {
		// transitionStateRootHeight: the root commits the enrollment registry
		// from the first hand-over on (version 1: effective_height).
		res.EnrollmentRootChecked = tip.Height >= c.Transition.FirstHandoverHeight()
	} else if act := chain.EnrollmentStateRootActivationHeight(); act > 0 && tip.Height >= act {
		// The boot restore leaves the root height at 0 without a transition;
		// the next appended block commits the enrollment registry, so check
		// the loaded registry against the tip now.
		st.v2.Aware.SetStateRootHeight(tip.Height)
		if root := st.v2.StateApplier.StateRoot(); root != tip.StateRoot {
			return res, fmt.Errorf("the enrollment snapshot does not reproduce block %d's state root (root=%s block_root=%s)", tip.Height, root, tip.StateRoot)
		}
		res.EnrollmentRootChecked = true
	}
	if b, ok := st.producer.LatestBlock(); !ok || b.Hash != tip.Hash {
		return res, errors.New("restored producer tip differs from the journal tip")
	}
	res.RestoreDuration = time.Since(restoreStart)
	return res, nil
}

func snapErrText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// snapCheckFixture is a stopped node's state directory, the consensus it runs
// under and the pin of its tip.
type snapCheckFixture struct {
	name   string
	src    string
	cons   snapshotConsensus
	pin    config.SnapshotBootstrapConfig
	signed int // the signed blocks of its journal
}

// snapCheckFixtures builds a chain under a version 1 producer transition
// (blocks 0..3 the pinned history, 4..11 the replacement producer's) and a
// signed chain without one (blocks 0..8).
func snapCheckFixtures(t *testing.T) []*snapCheckFixture {
	t.Helper()
	tr := &snapCheckFixture{name: "producer transition", src: t.TempDir(), cons: snapConsensus()}
	policy, repl := hl1rTransitionPrefix(t, tr.src, 3)
	core := hl1rStartCore(t, tr.src, policy, repl)
	for core.st.producer.TipHeight() < 11 {
		core.seal()
	}
	core.crash()
	if err := chain.SaveStakingLedger(chain.NewStakingLedger(), filepath.Join(tr.src, hl1StakingName)); err != nil {
		t.Fatal(err)
	}
	tr.cons.Transition = policy
	plain := &snapCheckFixture{name: "no producer transition", src: snapBuildChain(t, 8, true).src, cons: snapConsensus()}
	for _, f := range []*snapCheckFixture{tr, plain} {
		blocks, err := chain.LoadChainNDJSON(filepath.Join(f.src, hl1JournalName))
		if err != nil || len(blocks) < 9 {
			t.Fatalf("%s: %d blocks, %v", f.name, len(blocks), err)
		}
		for _, b := range blocks {
			if b.ProducerAuth.Signed() {
				f.signed++
			}
		}
		tip := blocks[len(blocks)-1]
		f.pin = config.SnapshotBootstrapConfig{Enabled: true, CheckpointHeight: tip.Height, CheckpointBlockHash: tip.Hash, CheckpointStateRoot: tip.StateRoot}
		if f.cons.Transition == nil {
			f.pin.CheckpointProducerID = tip.ProducerID
		}
	}
	return []*snapCheckFixture{tr, plain}
}

func (f *snapCheckFixture) export(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "snap")
	if _, err := snapshotRunExport(snapshotExportOptions{StateDir: f.src, OutDir: out, Consensus: f.cons}); err != nil {
		t.Fatalf("export: %v", err)
	}
	return out
}

// snapEditLines rewrites the journal of dir with its lines (without "\n")
// passed through edit.
func snapEditLines(t *testing.T, dir string, edit func(lines []string) []string) {
	t.Helper()
	path := filepath.Join(dir, hl1JournalName)
	lines := edit(strings.Split(strings.TrimSuffix(string(hl1rRead(t, path)), "\n"), "\n"))
	hl1WriteFile(t, path, strings.Join(lines, "\n")+"\n")
}

// snapCheckCase is one snapshot defect, or several: a change to the snapshot
// files, to the pin and to the consensus configuration.
type snapCheckCase struct {
	name   string
	only   string // the one fixture it applies to; "" for both
	mutate func(t *testing.T, dir string)
	pin    func(p *config.SnapshotBootstrapConfig)
	cons   func(t *testing.T, c *snapshotConsensus)
}

func snapCheckCases() []snapCheckCase {
	other := strings.Repeat("ab", 32)
	at := func(lines []string, i int) int { // a negative index counts from the end
		if i < 0 {
			i += len(lines)
		}
		return i
	}
	forge := func(i int) func(t *testing.T, dir string) {
		return func(t *testing.T, dir string) {
			snapEditJournal(t, dir, i, func(b *chain.Block) { b.ProducerAuth.Signature[10] ^= 0xff })
		}
	}
	tamper := func(i int, rehash bool) func(t *testing.T, dir string) {
		return func(t *testing.T, dir string) {
			snapEditJournal(t, dir, i, func(b *chain.Block) {
				b.Timestamp = b.Timestamp.Add(time.Second)
				if rehash {
					b.Hash = chain.ComputeBlockHash(b)
				}
			})
		}
	}
	lines := func(edit func([]string) []string) func(t *testing.T, dir string) {
		return func(t *testing.T, dir string) { snapEditLines(t, dir, edit) }
	}
	drop := func(i int) func(t *testing.T, dir string) {
		return lines(func(l []string) []string { i := at(l, i); return append(l[:i:i], l[i+1:]...) })
	}
	insert := func(i int, s string) func(t *testing.T, dir string) {
		return lines(func(l []string) []string {
			i := at(l, i)
			return append(append(append([]string(nil), l[:i]...), s), l[i:]...)
		})
	}
	torn := func(t *testing.T, dir string) {
		snapAppend(t, filepath.Join(dir, hl1JournalName), `{"height":`)
	}
	staking := func(t *testing.T, dir string) {
		hl1WriteFile(t, filepath.Join(dir, hl1StakingName), `{"v":1,"bonds":[],"unbond":[{"delegator":"snap-attacker","amount":5,"mature_at":1}]}`)
	}
	all := func(ms ...func(t *testing.T, dir string)) func(t *testing.T, dir string) {
		return func(t *testing.T, dir string) {
			for _, m := range ms {
				m(t, dir)
			}
		}
	}
	transition := func(edit func(p *producerpolicy.Transition)) func(t *testing.T, c *snapshotConsensus) {
		return func(t *testing.T, c *snapshotConsensus) {
			p := *c.Transition
			edit(&p)
			c.Transition = &p
		}
	}
	contentRoot := func(t *testing.T, c *snapshotConsensus) {
		prev := chain.TxContentRootActivationHeight()
		t.Cleanup(func() { chain.SetTxContentRootActivationHeight(prev) })
		c.ConsensusSettings = func() { chain.SetTxContentRootActivationHeight(2) }
	}
	const tr = "producer transition"
	return []snapCheckCase{
		{name: "intact"},
		{name: "forged tip signature", mutate: forge(-1)},
		{name: "forged signature in history", mutate: forge(-4)},
		{name: "two forged signatures", mutate: all(forge(-6), forge(-3))},
		{name: "history block tampered", mutate: tamper(-4, false)},
		{name: "history block tampered and rehashed", mutate: tamper(-4, true)},
		{name: "history block dropped", mutate: drop(-4)},
		{name: "history block repeated", mutate: lines(func(l []string) []string {
			i := at(l, -4)
			return append(append(append([]string(nil), l[:i+1]...), l[i]), l[i+1:]...)
		})},
		{name: "two blocks swapped", mutate: lines(func(l []string) []string { i := at(l, -4); l[i], l[i+1] = l[i+1], l[i]; return l })},
		{name: "a line that does not decode", mutate: insert(-2, "{not json")},
		{name: "a null line", mutate: insert(-2, "null")},
		{name: "torn last line", mutate: torn},
		{name: "last block cut at a line boundary", mutate: drop(-1)},
		{name: "empty journal", mutate: func(t *testing.T, dir string) { hl1WriteFile(t, filepath.Join(dir, hl1JournalName), "") }},
		{name: "staking ledger not empty", mutate: staking},
		{name: "pinned height", pin: func(p *config.SnapshotBootstrapConfig) { p.CheckpointHeight-- }},
		{name: "pinned hash", pin: func(p *config.SnapshotBootstrapConfig) { p.CheckpointBlockHash = other }},
		{name: "pinned state root", pin: func(p *config.SnapshotBootstrapConfig) { p.CheckpointStateRoot = other }},
		{name: "pinned producer", pin: func(p *config.SnapshotBootstrapConfig) { p.CheckpointProducerID = other }},
		{name: "content root activated at height 2", cons: contentRoot},
		{name: "another replacement producer", only: tr, cons: transition(func(p *producerpolicy.Transition) { p.ReplacementProducer = other })},
		{name: "another historical producer", only: tr, cons: transition(func(p *producerpolicy.Transition) { p.HistoricalProducer = other })},
		{name: "immutable prefix edited", only: tr, mutate: tamper(2, false)},
		// Several defects: the first check that fails reports.
		{name: "forged history signature and a wrong pinned hash", mutate: forge(-4), pin: func(p *config.SnapshotBootstrapConfig) { p.CheckpointBlockHash = other }},
		{name: "forged history signature and a staking bond", mutate: all(forge(-4), staking)},
		{name: "forged early signature and a later block tampered", mutate: all(forge(-6), tamper(-2, false))},
		{name: "forged early signature and a later block dropped", mutate: all(forge(-6), drop(-2))},
		{name: "a line that does not decode and a wrong pin", mutate: insert(-3, "{not json"), pin: func(p *config.SnapshotBootstrapConfig) { p.CheckpointStateRoot = other }},
		{name: "torn last line and a forged signature", mutate: all(forge(-4), torn)},
		{name: "history block tampered and a wrong pinned height", mutate: tamper(-4, false), pin: func(p *config.SnapshotBootstrapConfig) { p.CheckpointHeight-- }},
		{name: "forged history signature and the content root activated", mutate: forge(-4), cons: contentRoot},
		{name: "another historical producer and a forged tip signature", only: tr, mutate: forge(-1), cons: transition(func(p *producerpolicy.Transition) { p.HistoricalProducer = other })},
		{name: "another historical producer and a staking bond", only: tr, mutate: staking, cons: transition(func(p *producerpolicy.Transition) { p.HistoricalProducer = other })},
	}
}

// TestSnapshotVerifyMatchesTheVerificationBefore: for every snapshot, as the
// import checks it (pinned) and as the export checks what it wrote (no pin),
// snapshotVerifyDir gives the error snapshotVerifyDirBefore gives, or the same
// result and the same scratch files, and never writes to the snapshot. Under
// the producer transition it held every signature but the tip's only in the
// journal (main held them all: StrippedBlocks was 0).
func TestSnapshotVerifyMatchesTheVerificationBefore(t *testing.T) {
	for _, f := range snapCheckFixtures(t) {
		base := f.export(t)
		for _, tc := range snapCheckCases() {
			if tc.only != "" && tc.only != f.name {
				continue
			}
			t.Run(f.name+"/"+tc.name, func(t *testing.T) {
				work := t.TempDir()
				dir, scratch := filepath.Join(work, "snap"), filepath.Join(work, "scratch")
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				hl1rCopyDir(t, base, dir)
				if tc.mutate != nil {
					tc.mutate(t, dir)
				}
				cons, pin := f.cons, f.pin
				if tc.cons != nil {
					tc.cons(t, &cons)
				}
				if tc.pin != nil {
					tc.pin(&pin)
				}
				untouched := hl1rTreeHash(t, dir)
				// Both run on the same paths, so that errors naming a file match.
				run := func(verify func(string, string, snapshotConsensus, *config.SnapshotBootstrapConfig) (snapshotVerifyResult, error),
					pinned *config.SnapshotBootstrapConfig) (snapshotVerifyResult, error, string) {
					if err := os.RemoveAll(scratch); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(scratch, 0o700); err != nil {
						t.Fatal(err)
					}
					res, err := verify(dir, scratch, cons, pinned)
					return res, err, hl1rTreeHash(t, scratch)
				}
				for _, pinned := range []*config.SnapshotBootstrapConfig{&pin, nil} {
					was, wasErr, wasScratch := run(snapshotVerifyDirBefore, pinned)
					got, gotErr, gotScratch := run(snapshotVerifyDir, pinned)
					what := "import (pinned)"
					if pinned == nil {
						what = "export check (no pin)"
					}
					if snapErrText(gotErr) != snapErrText(wasErr) {
						t.Fatalf("%s: error\n  %s\nbefore\n  %s", what, snapErrText(gotErr), snapErrText(wasErr))
					}
					t.Logf("%s: %s", what, snapErrText(gotErr))
					for _, kind := range []error{chain.ErrExternalProducerNotAuthorized, chain.ErrBlockBadSignature, chain.ErrBlockUnsigned} {
						if errors.Is(gotErr, kind) != errors.Is(wasErr, kind) {
							t.Fatalf("%s: errors.Is(%v) differs: %v / before %v", what, kind, gotErr, wasErr)
						}
					}
					if gotScratch != wasScratch {
						t.Fatalf("%s: scratch files\n%s\nbefore\n%s", what, gotScratch, wasScratch)
					}
					if wasErr != nil {
						continue
					}
					if got.Tip.Hash != was.Tip.Hash || got.Tip.Height != was.Tip.Height || got.Blocks != was.Blocks || got.JournalBytes != was.JournalBytes ||
						got.AuthorizedBy != was.AuthorizedBy || got.EnrollmentRootChecked != was.EnrollmentRootChecked {
						t.Fatalf("%s: result %+v\nbefore %+v", what, got, was)
					}
					want := 0
					if cons.Transition != nil {
						want = f.signed - 1 // every signed block but the tip
					}
					if got.StrippedBlocks != want {
						t.Fatalf("%s: %d blocks held without their signature, want %d (of %d signed)", what, got.StrippedBlocks, want, f.signed)
					}
				}
				if hl1rTreeHash(t, dir) != untouched {
					t.Fatal("the check wrote to the snapshot directory")
				}
			})
		}
	}
}

// TestSnapshotImportLeavesTheSameStateDirectory: an import of the same
// snapshot leaves the state directory it left before: the snapshot's three
// files byte for byte, and an import record that is byte for byte the one the
// verification before derives, but for the three durations it measures
// (journal_load_seconds, restore_seconds, total_seconds), which differ between
// any two runs.
func TestSnapshotImportLeavesTheSameStateDirectory(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	for _, f := range snapCheckFixtures(t) {
		t.Run(f.name, func(t *testing.T) {
			snap := f.export(t)
			was, err := snapshotVerifyDirBefore(snap, t.TempDir(), f.cons, &f.pin)
			if err != nil {
				t.Fatalf("verification before: %v", err)
			}
			m, err := ledgersnapshot.ReadManifest(snap)
			if err != nil {
				t.Fatal(err)
			}
			stateDir := filepath.Join(t.TempDir(), "follower")
			if _, err := snapshotRunImport(snapshotImportOptions{SnapshotDir: snap, StateDir: stateDir, Pinned: f.pin, Consensus: f.cons,
				Now: func() time.Time { return now }}); err != nil {
				t.Fatalf("import: %v", err)
			}
			entries, err := os.ReadDir(stateDir)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			if got := strings.Join(names, ","); got != "qsdm-validator.state.lock,qsdm_accounts.json,qsdm_chain.ndjson,qsdm_enrollment.json,qsdm_snapshot_import.json" {
				t.Fatalf("state directory holds %s", got)
			}
			for _, name := range snapshotInstallOrder {
				if !bytes.Equal(hl1rRead(t, filepath.Join(stateDir, name)), hl1rRead(t, filepath.Join(snap, name))) {
					t.Fatalf("installed %s differs from the snapshot's", name)
				}
			}
			raw := hl1rRead(t, filepath.Join(stateDir, snapshotImportRecordName))
			var rec snapshotImportResult
			if err := json.Unmarshal(raw, &rec); err != nil {
				t.Fatal(err)
			}
			abs, err := filepath.Abs(stateDir)
			if err != nil {
				t.Fatal(err)
			}
			want := snapshotImportResult{
				StateDir: abs,
				Checkpoint: ledgersnapshot.Checkpoint{Height: was.Tip.Height, BlockHash: was.Tip.Hash, StateRoot: was.Tip.StateRoot,
					ProducerID: was.Tip.ProducerID, TipSigned: true, Blocks: was.Blocks, JournalBytes: was.JournalBytes},
				AuthorizedBy:     was.AuthorizedBy,
				EnrollmentInRoot: was.EnrollmentRootChecked,
				ImportedAt:       now.Format(time.RFC3339Nano),
				// The three durations are measured: the record's own.
				LoadSeconds: rec.LoadSeconds, RestoreSeconds: rec.RestoreSeconds, TotalSeconds: rec.TotalSeconds,
			}
			byName := map[string]ledgersnapshot.FileEvidence{}
			for _, fe := range m.Files {
				byName[fe.Name] = fe
			}
			for _, name := range snapshotInstallOrder {
				want.Installed = append(want.Installed, byName[name])
			}
			data, err := json.MarshalIndent(want, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if data = append(data, '\n'); !bytes.Equal(raw, data) {
				t.Fatalf("import record\n%s\nwant\n%s", raw, data)
			}
		})
	}
}

// TestSnapshotImportUnderTransitionRefusesAsBefore: under the producer
// transition a whole import (staging, manifest, checks) of a snapshot whose
// manifest matches its files but that does not verify is refused with the
// error the verification before gives, and leaves the state directory as it
// found it.
func TestSnapshotImportUnderTransitionRefusesAsBefore(t *testing.T) {
	f := snapCheckFixtures(t)[0]
	if f.cons.Transition == nil {
		t.Fatal("fixture: the first fixture has no producer transition")
	}
	base := f.export(t)
	for _, tc := range snapCheckCases() {
		switch tc.name {
		case "forged signature in history", "two forged signatures", "history block tampered", "history block tampered and rehashed",
			"another historical producer", "forged history signature and a staking bond", "another historical producer and a staking bond":
		default:
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			snap := filepath.Join(t.TempDir(), "snap")
			if err := os.Mkdir(snap, 0o700); err != nil {
				t.Fatal(err)
			}
			hl1rCopyDir(t, base, snap)
			if tc.mutate != nil {
				tc.mutate(t, snap)
				snapRehash(t, snap)
			}
			cons := f.cons
			if tc.cons != nil {
				tc.cons(t, &cons)
			}
			_, wasErr := snapshotVerifyDirBefore(snap, t.TempDir(), cons, &f.pin)
			if wasErr == nil {
				t.Fatal("fixture: the verification before accepts this snapshot")
			}
			stateDir := filepath.Join(t.TempDir(), "follower")
			_, err := snapshotRunImport(snapshotImportOptions{SnapshotDir: snap, StateDir: stateDir, Pinned: f.pin, Consensus: cons})
			if snapErrText(err) != snapErrText(wasErr) {
				t.Fatalf("import error\n  %s\nbefore\n  %s", snapErrText(err), snapErrText(wasErr))
			}
			snapRequireUntouched(t, stateDir)
		})
	}
}

// TestHL1ReplayHoldsOnlyJsSignatureInMemory: under the producer transition
// the tail replay holds every signature but J's only in the journal (main
// held them all), and recovers as before: W := J and the next boot passes.
func TestHL1ReplayHoldsOnlyJsSignatureInMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("state-directory recovery test")
	}
	const j = 5
	f := hl1rCrash(t, true, j, "H3")
	blocks, err := chain.LoadChainNDJSON(f.path(hl1JournalName))
	if err != nil {
		t.Fatal(err)
	}
	signed := 0
	for _, b := range blocks {
		if b.ProducerAuth.Signed() {
			signed++
		}
	}
	res, err, ex := hl1rReplay(t, f.dir, f.policy)
	if err != nil || ex.code != -1 {
		t.Fatalf("replay: %v (exit %d)", err, ex.code)
	}
	if res.StrippedBlocks != signed-1 {
		t.Fatalf("replay held %d of %d signed blocks without their signature, want all but J's", res.StrippedBlocks, signed)
	}
	if w := hl1rW(t, f.dir); w.Height != j || w.Hash != f.blkJ.Hash {
		t.Fatalf("W after replay = %+v", w)
	}
	if _, _, err := hl1rBoot(t, f.dir, f.policy); err != nil {
		t.Fatalf("boot after replay: %v", err)
	}
}

// TestHL1ReplayAppliesConsensusSettingsBeforeTheRead: the tail replay now
// checks every block hash as it reads the journal, so the consensus settings
// must be applied before the read. With the content root active from height
// 2 every later block hash depends on it; an offline process starts with the
// package default (off), and the replay must still recover.
func TestHL1ReplayAppliesConsensusSettingsBeforeTheRead(t *testing.T) {
	if testing.Short() {
		t.Skip("state-directory recovery test")
	}
	const act, j = 2, 5
	prev := chain.TxContentRootActivationHeight()
	t.Cleanup(func() { chain.SetTxContentRootActivationHeight(prev) })
	chain.SetTxContentRootActivationHeight(act)
	f := hl1rCrash(t, true, j, "H3")
	blocks, err := chain.LoadChainNDJSON(f.path(hl1JournalName))
	if err != nil {
		t.Fatal(err)
	}
	chain.SetTxContentRootActivationHeight(0)
	if b := blocks[j-1]; len(b.Transactions) == 0 || chain.ComputeBlockHash(b) == b.Hash {
		t.Fatal("fixture: the block hashes do not depend on the content-root setting; the test would prove nothing")
	}
	ex := &hl1rExit{code: -1}
	stopper := &hl1FailStopper{exit: func(code int) { ex.code = code }, logf: func(string, ...any) {}}
	res, err := hl1RunReplay(hl1ReplayOptions{
		StateDir: f.dir, Transition: f.policy, Release: "test", Getenv: func(string) string { return "" },
		ConsensusSettings: func() { chain.SetTxContentRootActivationHeight(act) },
		Stopper:           stopper, FailStop: stopper.Stop, Log: logging.NewSilentLogger(), Now: func() time.Time { return hl1rNow },
	})
	if res.Lock != nil {
		if cerr := res.Lock.Close(); cerr != nil {
			t.Fatal(cerr)
		}
	}
	if err != nil || ex.code != -1 {
		t.Fatalf("replay with the settings supplied by ConsensusSettings: %v (exit %d)", err, ex.code)
	}
	if w := hl1rW(t, f.dir); w.Height != j || w.Hash != f.blkJ.Hash {
		t.Fatalf("W after replay = %+v", w)
	}
}

// TestHL1ReplayRefusesAsBefore: a journal that does not verify is refused
// with the error the replay gave before, at the step it gave it: a line that
// does not decode at S4, before the receipts pre-trim; a block that fails the
// producer transition at step 1, after it, where the loaded journal was
// verified before. The journal and W are never changed.
func TestHL1ReplayRefusesAsBefore(t *testing.T) {
	if testing.Short() {
		t.Skip("state-directory recovery test")
	}
	const j = 6
	for _, tc := range []struct {
		name    string
		edit    func(t *testing.T, dir string)
		policy  func(p *producerpolicy.Transition)
		preTrim bool // refused after the receipts pre-trim
	}{
		{name: "forged signature after the hand-over", preTrim: true, edit: func(t *testing.T, dir string) {
			snapEditJournal(t, dir, 4, func(b *chain.Block) { b.ProducerAuth.Signature[10] ^= 0xff })
		}},
		{name: "another historical producer", preTrim: true, policy: func(p *producerpolicy.Transition) { p.HistoricalProducer = strings.Repeat("ab", 32) }},
		{name: "a line that does not decode", edit: func(t *testing.T, dir string) {
			snapEditLines(t, dir, func(l []string) []string {
				return append(append(append([]string(nil), l[:5]...), "{not json"), l[5:]...)
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := hl1rCrash(t, true, j, "H3")
			f.hl1rTearReceipts(t) // a pre-trim changes the receipts
			policy := *f.policy
			if tc.policy != nil {
				tc.policy(&policy)
			}
			if tc.edit != nil {
				tc.edit(t, f.dir)
			}
			// What the replay reported before: the journal load at S4, then the
			// producer transition check of the loaded chain at step 1.
			journal := f.path(hl1JournalName)
			var want string
			if blocks, err := chain.LoadChainNDJSON(journal); err != nil {
				want = "journal: " + err.Error()
			} else {
				st, err := hl1NewReplayStack(t.TempDir(), nil, nil, &policy, logging.NewSilentLogger())
				if err != nil {
					t.Fatal(err)
				}
				if _, err := st.producer.VerifyProducerTransitionChain(blocks); err == nil {
					t.Fatal("fixture: the journal verifies")
				} else {
					want = "producer transition persisted chain: " + err.Error()
				}
			}
			journalBefore, receiptsBefore, w := hl1rRead(t, journal), hl1rRead(t, f.path(hl1ReceiptsName)), hl1rW(t, f.dir)
			res, err, ex := hl1rReplay(t, f.dir, &policy)
			if err == nil || err.Error() != want {
				t.Fatalf("replay error\n  %v\nbefore\n  %s", err, want)
			}
			if ex.code != -1 {
				t.Fatalf("replay exited %d", ex.code)
			}
			if !bytes.Equal(hl1rRead(t, journal), journalBefore) || hl1rW(t, f.dir) != w {
				t.Fatal("a refused replay changed the journal or W")
			}
			trimmed := !bytes.Equal(hl1rRead(t, f.path(hl1ReceiptsName)), receiptsBefore)
			if trimmed != tc.preTrim || (res.PreTrim.Archive != "") != tc.preTrim {
				t.Fatalf("receipts pre-trimmed: %v (archive %q), want %v", trimmed, res.PreTrim.Archive, tc.preTrim)
			}
		})
	}
}
