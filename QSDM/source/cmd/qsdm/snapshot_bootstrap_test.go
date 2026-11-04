package main

// Snapshot bootstrap tests: qsdm --snapshot-export / --snapshot-import on
// real state directories built by the replay-test core (hl1rStartCore:
// main()'s wiring, a signer, the journal and the normal-mode persistence
// hook).

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

type snapFixture struct {
	src    string
	signer *chain.PersistentBFTSigner // nil: unsigned chain
	tip    *chain.Block
}

// snapBuildChain seals blocks 0..n on a fresh state directory as a stopped
// node leaves it, plus the empty staking ledger main() writes.
func snapBuildChain(t *testing.T, n uint64, signed bool) *snapFixture {
	t.Helper()
	f := &snapFixture{src: t.TempDir()}
	var signer chain.BFTSigner
	if signed {
		f.signer = hl1rSigner(t)
		signer = f.signer
	}
	c := hl1rStartCore(t, f.src, nil, signer)
	for !c.st.producer.HasTip() || c.st.producer.TipHeight() < n {
		c.seal()
	}
	f.tip, _ = c.st.producer.LatestBlock()
	c.crash()
	if err := chain.SaveStakingLedger(chain.NewStakingLedger(), filepath.Join(f.src, hl1StakingName)); err != nil {
		t.Fatal(err)
	}
	return f
}

func snapConsensus() snapshotConsensus {
	return snapshotConsensus{Log: logging.NewSilentLogger()}
}

func (f *snapFixture) pinned() config.SnapshotBootstrapConfig {
	p := config.SnapshotBootstrapConfig{
		Enabled:             true,
		CheckpointHeight:    f.tip.Height,
		CheckpointBlockHash: f.tip.Hash,
		CheckpointStateRoot: f.tip.StateRoot,
	}
	if f.signer != nil {
		p.CheckpointProducerID = f.signer.Address()
	}
	return p
}

func (f *snapFixture) export(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "snap")
	if _, err := snapshotRunExport(snapshotExportOptions{StateDir: f.src, OutDir: out, Consensus: snapConsensus()}); err != nil {
		t.Fatalf("export: %v", err)
	}
	return out
}

func snapFileHashes(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, name := range ledgersnapshot.RequiredFiles() {
		sum, _, err := ledgersnapshot.FileSHA256(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		out[name] = sum
	}
	return out
}

// snapRehash rewrites the manifest of a snapshot whose files were edited, as
// an attacker who controls the whole snapshot would.
func snapRehash(t *testing.T, dir string) {
	t.Helper()
	m, err := ledgersnapshot.ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := range m.Files {
		sum, n, err := ledgersnapshot.FileSHA256(filepath.Join(dir, m.Files[i].Name))
		if err != nil {
			t.Fatal(err)
		}
		m.Files[i].SHA256, m.Files[i].SizeBytes = sum, n
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	hl1WriteFile(t, filepath.Join(dir, ledgersnapshot.ManifestFileName()), string(data)+"\n")
}

// snapEditJournal rewrites journal line index (negative: from the end).
func snapEditJournal(t *testing.T, dir string, index int, edit func(*chain.Block)) {
	t.Helper()
	path := filepath.Join(dir, hl1JournalName)
	lines := strings.Split(strings.TrimSuffix(string(hl1rRead(t, path)), "\n"), "\n")
	if index < 0 {
		index += len(lines)
	}
	var b chain.Block
	if err := json.Unmarshal([]byte(lines[index]), &b); err != nil {
		t.Fatal(err)
	}
	edit(&b)
	data, err := json.Marshal(&b)
	if err != nil {
		t.Fatal(err)
	}
	lines[index] = string(data)
	hl1WriteFile(t, path, strings.Join(lines, "\n")+"\n")
}

func snapImport(t *testing.T, snap, stateDir string, pinned config.SnapshotBootstrapConfig, mod func(*snapshotImportOptions)) (snapshotImportResult, error) {
	t.Helper()
	o := snapshotImportOptions{SnapshotDir: snap, StateDir: stateDir, Pinned: pinned, Consensus: snapConsensus()}
	if mod != nil {
		mod(&o)
	}
	return snapshotRunImport(o)
}

// snapRequireUntouched asserts that a refused import left no chain state and
// no staging directory behind.
func snapRequireUntouched(t *testing.T, stateDir string, preexisting ...string) {
	t.Helper()
	entries, err := os.ReadDir(stateDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	allowed := map[string]bool{"qsdm-validator.state.lock": true}
	for _, p := range preexisting {
		allowed[p] = true
	}
	for _, e := range entries {
		if !allowed[e.Name()] {
			t.Fatalf("refused import left %s in the state directory", e.Name())
		}
	}
}

// -----------------------------------------------------------------------------
// End to end: export -> import -> boot -> continue syncing from N+1
// -----------------------------------------------------------------------------

func TestSnapshotBootstrapExportImportEndToEnd(t *testing.T) {
	f := snapBuildChain(t, 12, true)
	before := snapFileHashes(t, f.src)

	snap := filepath.Join(t.TempDir(), "snap")
	exp, err := snapshotRunExport(snapshotExportOptions{StateDir: f.src, OutDir: snap, Consensus: snapConsensus()})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if exp.Checkpoint.Height != f.tip.Height || exp.Checkpoint.BlockHash != f.tip.Hash || exp.Checkpoint.StateRoot != f.tip.StateRoot ||
		exp.Checkpoint.ProducerID != f.signer.Address() || !exp.Checkpoint.TipSigned || exp.Checkpoint.Blocks != int(f.tip.Height)+1 {
		t.Fatalf("export checkpoint %+v does not describe tip %d %s", exp.Checkpoint, f.tip.Height, f.tip.Hash)
	}
	if after := snapFileHashes(t, f.src); !equalStringMaps(before, after) {
		t.Fatal("export changed the source state files")
	}
	if cp, ok, err := ledgersnapshot.ReadCheckpoint(snap); err != nil || !ok || cp != exp.Checkpoint {
		t.Fatalf("checkpoint file: %+v ok=%v err=%v", cp, ok, err)
	}

	dst := filepath.Join(t.TempDir(), "follower")
	res, err := snapImport(t, snap, dst, f.pinned(), nil)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.AuthorizedBy != "checkpoint_producer_id" || len(res.Installed) != 3 || res.Checkpoint.BlockHash != f.tip.Hash {
		t.Fatalf("import result %+v", res)
	}
	entries, err := os.ReadDir(dst)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if got := strings.Join(names, ","); got != "qsdm-validator.state.lock,qsdm_accounts.json,qsdm_chain.ndjson,qsdm_enrollment.json,qsdm_snapshot_import.json" {
		t.Fatalf("follower state directory holds %s", got)
	}
	for name, sum := range before {
		if name == hl1StakingName {
			continue
		}
		got, _, err := ledgersnapshot.FileSHA256(filepath.Join(dst, name))
		if err != nil || got != sum {
			t.Fatalf("installed %s sha256 %s err %v, want the source bytes %s", name, got, err, sum)
		}
	}

	// The follower's next start: main()'s restore path on the imported dir.
	follower, blocks, err := hl1rRestore(t, dst, nil)
	if err != nil {
		t.Fatalf("follower boot after import: %v", err)
	}
	source, _, err := hl1rRestore(t, f.src, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := follower.v2.StateApplier.StateRoot(), source.v2.StateApplier.StateRoot(); got != want || got != f.tip.StateRoot {
		t.Fatalf("state root after import %s, source %s, checkpoint %s", got, want, f.tip.StateRoot)
	}
	if len(blocks) != int(f.tip.Height)+1 {
		t.Fatalf("follower restored %d blocks", len(blocks))
	}
	if err := snapshotCheckpointGuard(f.pinned(), follower.producer.GetBlock); err != nil {
		t.Fatalf("boot guard on the imported chain: %v", err)
	}
	if !bytes.Equal(snapSaveAccounts(t, follower), snapSaveAccounts(t, source)) {
		t.Fatal("follower accounts differ from the source after import")
	}

	// Continue syncing from N+1: the producer seals more blocks and the
	// follower appends them through the normal external-block path.
	core := hl1rStartCore(t, f.src, nil, f.signer)
	for i := 0; i < 3; i++ {
		blk := core.seal()
		if err := follower.producer.TryAppendExternalBlock(blk); err != nil {
			t.Fatalf("follower append of block %d after import: %v", blk.Height, err)
		}
		if got := follower.v2.StateApplier.StateRoot(); got != blk.StateRoot {
			t.Fatalf("follower root at %d is %s, block says %s", blk.Height, got, blk.StateRoot)
		}
	}
	core.crash()
	if tip, _ := follower.producer.LatestBlock(); tip.Height != f.tip.Height+3 {
		t.Fatalf("follower tip %d, want %d", tip.Height, f.tip.Height+3)
	}
}

func snapSaveAccounts(t *testing.T, st *hl1ReplayStack) []byte {
	t.Helper()
	p := filepath.Join(t.TempDir(), "accounts.json")
	if err := st.accounts.Save(p); err != nil {
		t.Fatal(err)
	}
	return hl1rRead(t, p)
}

func equalStringMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// -----------------------------------------------------------------------------
// Refusals: every mismatch leaves the state directory without chain state
// -----------------------------------------------------------------------------

func TestSnapshotImportRefusals(t *testing.T) {
	f := snapBuildChain(t, 8, true)
	base := f.export(t)
	other := strings.Repeat("ab", 32)

	cases := []struct {
		name   string
		pin    func(*config.SnapshotBootstrapConfig)
		opts   func(*snapshotImportOptions)
		mutate func(t *testing.T, snap string)
		prep   func(t *testing.T, stateDir string) []string
		want   string
	}{
		{name: "disabled", pin: func(p *config.SnapshotBootstrapConfig) { p.Enabled = false }, want: "enabled is false"},
		{name: "producer role", opts: func(o *snapshotImportOptions) { o.ProducerRole = true }, want: "producer role"},
		{name: "checkpoint height", pin: func(p *config.SnapshotBootstrapConfig) { p.CheckpointHeight-- }, mutate: snapDropAdvisory, want: "the trusted checkpoint is height"},
		{name: "checkpoint hash", pin: func(p *config.SnapshotBootstrapConfig) { p.CheckpointBlockHash = other }, mutate: snapDropAdvisory, want: "differs from the trusted checkpoint"},
		{name: "checkpoint root", pin: func(p *config.SnapshotBootstrapConfig) { p.CheckpointStateRoot = other }, mutate: snapDropAdvisory, want: "differs from the trusted checkpoint"},
		{name: "pinned producer", pin: func(p *config.SnapshotBootstrapConfig) { p.CheckpointProducerID = other }, want: "checkpoint_producer_id is"},
		{name: "no producer identity", pin: func(p *config.SnapshotBootstrapConfig) { p.CheckpointProducerID = "" }, want: "no trusted producer identity"},
		{
			name: "producer not in allowlist",
			pin:  func(p *config.SnapshotBootstrapConfig) { p.CheckpointProducerID = "" },
			opts: func(o *snapshotImportOptions) {
				o.Consensus.AuthorizedBlockProducers = []string{"12D3KooWAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}
			},
			want: "not in authorized_block_producers",
		},
		{
			name:   "file differs from manifest",
			mutate: func(t *testing.T, snap string) { snapAppend(t, filepath.Join(snap, hl1AccountsName), " ") },
			want:   "does not match the manifest",
		},
		{
			name: "accounts tampered, manifest rehashed",
			mutate: func(t *testing.T, snap string) {
				acc := chain.NewAccountStore()
				path := filepath.Join(snap, hl1AccountsName)
				if _, err := acc.Load(path); err != nil {
					t.Fatal(err)
				}
				acc.Credit("snap-attacker", 1000)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := acc.Save(path); err != nil {
					t.Fatal(err)
				}
				snapRehash(t, snap)
			},
			want: "does not reproduce block",
		},
		{
			name: "forged producer signature",
			mutate: func(t *testing.T, snap string) {
				snapEditJournal(t, snap, -1, func(b *chain.Block) { b.ProducerAuth.Signature[10] ^= 0xff })
				snapRehash(t, snap)
			},
			want: "producer signature invalid",
		},
		{
			name: "history block tampered",
			mutate: func(t *testing.T, snap string) {
				snapEditJournal(t, snap, 3, func(b *chain.Block) { b.Timestamp = b.Timestamp.Add(time.Second) })
				snapRehash(t, snap)
			},
			want: "hash does not match its contents",
		},
		{
			name: "history block dropped",
			mutate: func(t *testing.T, snap string) {
				path := filepath.Join(snap, hl1JournalName)
				lines := strings.SplitAfter(string(hl1rRead(t, path)), "\n")
				hl1WriteFile(t, path, strings.Join(append(lines[:2:2], lines[3:]...), ""))
				snapRehash(t, snap)
			},
			want: "journal contains",
		},
		{
			name: "journal without final newline",
			mutate: func(t *testing.T, snap string) {
				path := filepath.Join(snap, hl1JournalName)
				hl1WriteFile(t, path, strings.TrimSuffix(string(hl1rRead(t, path)), "\n"))
				snapRehash(t, snap)
			},
			want: "journal:",
		},
		{
			name: "staking ledger not empty",
			mutate: func(t *testing.T, snap string) {
				path := filepath.Join(snap, hl1StakingName)
				hl1WriteFile(t, path, `{"v":1,"bonds":[],"unbond":[{"delegator":"snap-attacker","amount":5,"mature_at":1}]}`)
				snapRehash(t, snap)
			},
			want: "staking ledger holds",
		},
		{
			name: "advisory checkpoint disagrees",
			mutate: func(t *testing.T, snap string) {
				cp, _, err := ledgersnapshot.ReadCheckpoint(snap)
				if err != nil {
					t.Fatal(err)
				}
				cp.Height++
				data, _ := json.Marshal(cp)
				hl1WriteFile(t, filepath.Join(snap, ledgersnapshot.CheckpointFileName()), string(data))
			},
			want: "not the trusted checkpoint",
		},
		{
			name: "state directory not empty",
			prep: func(t *testing.T, stateDir string) []string {
				if err := os.MkdirAll(stateDir, 0o700); err != nil {
					t.Fatal(err)
				}
				hl1WriteFile(t, filepath.Join(stateDir, hl1ReceiptsName), "")
				return []string{hl1ReceiptsName}
			},
			want: "already holds qsdm_receipts.ndjson",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := filepath.Join(t.TempDir(), "snap")
			if err := os.Mkdir(snap, 0o700); err != nil {
				t.Fatal(err)
			}
			hl1rCopyDir(t, base, snap)
			if tc.mutate != nil {
				tc.mutate(t, snap)
			}
			pinned := f.pinned()
			if tc.pin != nil {
				tc.pin(&pinned)
			}
			stateDir := filepath.Join(t.TempDir(), "follower")
			var pre []string
			if tc.prep != nil {
				pre = tc.prep(t, stateDir)
			}
			_, err := snapImport(t, snap, stateDir, pinned, tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("import error = %v, want it to contain %q", err, tc.want)
			}
			snapRequireUntouched(t, stateDir, pre...)
		})
	}

	t.Run("authorized_block_producers accepts the producer", func(t *testing.T) {
		pinned := f.pinned()
		pinned.CheckpointProducerID = ""
		res, err := snapImport(t, base, filepath.Join(t.TempDir(), "follower"), pinned, func(o *snapshotImportOptions) {
			o.Consensus.AuthorizedBlockProducers = []string{f.signer.Address()}
		})
		if err != nil || res.AuthorizedBy != "authorized_block_producers" {
			t.Fatalf("import: %+v %v", res, err)
		}
	})
	t.Run("snapshot inside the state directory", func(t *testing.T) {
		stateDir := t.TempDir()
		_, err := snapImport(t, filepath.Join(stateDir, "snap"), stateDir, f.pinned(), nil)
		if err == nil || !strings.Contains(err.Error(), "disjoint") {
			t.Fatalf("import error = %v", err)
		}
	})
	t.Run("second import refused", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "follower")
		if _, err := snapImport(t, base, stateDir, f.pinned(), nil); err != nil {
			t.Fatal(err)
		}
		if _, err := snapImport(t, base, stateDir, f.pinned(), nil); err == nil || !strings.Contains(err.Error(), "already holds") {
			t.Fatalf("second import error = %v", err)
		}
	})
}

func snapAppend(t *testing.T, path, s string) {
	t.Helper()
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	if _, err := fh.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotImportRefusesUnsignedCheckpoint(t *testing.T) {
	f := snapBuildChain(t, 4, false)
	snap := f.export(t)
	if cp, _, err := ledgersnapshot.ReadCheckpoint(snap); err != nil || cp.TipSigned {
		t.Fatalf("checkpoint %+v err %v: an unsigned tip must be reported", cp, err)
	}
	pinned := f.pinned()
	pinned.CheckpointProducerID = strings.Repeat("cd", 32)
	stateDir := filepath.Join(t.TempDir(), "follower")
	if _, err := snapImport(t, snap, stateDir, pinned, nil); err == nil || !strings.Contains(err.Error(), "carries no producer_auth") {
		t.Fatalf("import error = %v", err)
	}
	snapRequireUntouched(t, stateDir)
}

// TestSnapshotImportUnderProducerTransition: with a version 1 producer
// transition configured (and no checkpoint_producer_id), the transition
// authorizes the checkpoint block: the replacement producer from
// effective_height on, the historical producer below it. The pinned journal
// prefix is checked on the staged copy, and above effective_height the
// enrollment registry is part of the verified root. A transition that names
// another producer is refused. Under a version 2 schedule the producer of the
// epoch that contains the checkpoint authorizes it.
func TestSnapshotImportUnderProducerTransition(t *testing.T) {
	stakingAndPin := func(t *testing.T, dir string) config.SnapshotBootstrapConfig {
		t.Helper()
		if err := chain.SaveStakingLedger(chain.NewStakingLedger(), filepath.Join(dir, hl1StakingName)); err != nil {
			t.Fatal(err)
		}
		blocks, err := chain.LoadChainNDJSON(filepath.Join(dir, hl1JournalName))
		if err != nil || len(blocks) == 0 {
			t.Fatalf("journal: %d blocks, %v", len(blocks), err)
		}
		tip := blocks[len(blocks)-1]
		return config.SnapshotBootstrapConfig{Enabled: true, CheckpointHeight: tip.Height, CheckpointBlockHash: tip.Hash, CheckpointStateRoot: tip.StateRoot}
	}
	exportUnder := func(t *testing.T, dir string, cons snapshotConsensus) string {
		t.Helper()
		out := filepath.Join(t.TempDir(), "snap")
		if _, err := snapshotRunExport(snapshotExportOptions{StateDir: dir, OutDir: out, Consensus: cons}); err != nil {
			t.Fatalf("export: %v", err)
		}
		return out
	}

	t.Run("replacement producer after effective_height", func(t *testing.T) {
		src := t.TempDir()
		policy, repl := hl1rTransitionPrefix(t, src, 3)
		core := hl1rStartCore(t, src, policy, repl)
		for core.st.producer.TipHeight() < 7 {
			core.seal()
		}
		core.crash()
		pinned := stakingAndPin(t, src)
		cons := snapConsensus()
		cons.Transition = policy
		snap := exportUnder(t, src, cons)

		res, err := snapImport(t, snap, filepath.Join(t.TempDir(), "follower"), pinned, func(o *snapshotImportOptions) { o.Consensus = cons })
		if err != nil {
			t.Fatalf("import: %v", err)
		}
		if res.AuthorizedBy != "producer_transition" || !res.EnrollmentInRoot || res.Checkpoint.ProducerID != repl.Address() || len(res.Installed) != 3 {
			t.Fatalf("import result %+v", res)
		}

		// A transition that names another replacement producer (same pinned
		// prefix) does not authorize the checkpoint block.
		other := *policy
		other.ReplacementProducer = strings.Repeat("ef", 32)
		stateDir := filepath.Join(t.TempDir(), "follower")
		_, err = snapImport(t, snap, stateDir, pinned, func(o *snapshotImportOptions) { o.Consensus.Transition = &other })
		if err == nil || !errors.Is(err, chain.ErrExternalProducerNotAuthorized) || !strings.Contains(err.Error(), "the producer transition expects") {
			t.Fatalf("import under another transition: %v", err)
		}
		snapRequireUntouched(t, stateDir)
	})

	t.Run("historical producer below effective_height", func(t *testing.T) {
		src := t.TempDir()
		policy, _ := hl1rTransitionPrefix(t, src, 3)
		pinned := stakingAndPin(t, src)
		if pinned.CheckpointHeight >= policy.EffectiveHeight {
			t.Fatalf("fixture: tip %d is not below effective_height %d", pinned.CheckpointHeight, policy.EffectiveHeight)
		}
		cons := snapConsensus()
		cons.Transition = policy
		snap := exportUnder(t, src, cons)

		res, err := snapImport(t, snap, filepath.Join(t.TempDir(), "follower"), pinned, func(o *snapshotImportOptions) { o.Consensus = cons })
		if err != nil {
			t.Fatalf("import: %v", err)
		}
		if res.AuthorizedBy != "producer_transition" || res.EnrollmentInRoot || res.Checkpoint.ProducerID != policy.HistoricalProducer {
			t.Fatalf("import result %+v", res)
		}
	})

	// A version 2 schedule with a second hand-over: A seals 0..2, B (the
	// version 1 replacement) seals 3..5, C seals 6..8 under A@1, B@3, C@6. The
	// checkpoint block is C's, and only the schedule authorizes it: the
	// version 1 policy (whose replacement is B) refuses the same snapshot.
	t.Run("final epoch's producer under a version 2 schedule", func(t *testing.T) {
		src := t.TempDir()
		v1, signerB := hl1rTransitionPrefix(t, src, 2)
		b := hl1rStartCore(t, src, v1, signerB)
		for b.st.producer.TipHeight() < 5 {
			b.seal()
		}
		cp, ok := b.st.producer.GetBlock(5)
		if !ok {
			t.Fatal("checkpoint block missing")
		}
		b.crash()
		data, err := os.ReadFile(filepath.Join(src, hl1JournalName))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		signerC := hl1rSigner(t)
		v2 := &producerpolicy.Transition{Version: 2, HistoricalPrefixBytes: int64(len(data)), HistoricalPrefixSHA256: hex.EncodeToString(sum[:]),
			Epochs: append(v1.Schedule(), producerpolicy.Epoch{FromHeight: 6, ProducerID: signerC.Address(), CheckpointHash: cp.Hash})}
		if err := v2.Validate(); err != nil {
			t.Fatal(err)
		}
		c := hl1rStartCore(t, src, v2, signerC)
		for c.st.producer.TipHeight() < 8 {
			c.seal()
		}
		c.crash()
		pinned := stakingAndPin(t, src)
		cons := snapConsensus()
		cons.Transition = v2
		snap := exportUnder(t, src, cons)

		res, err := snapImport(t, snap, filepath.Join(t.TempDir(), "follower"), pinned, func(o *snapshotImportOptions) { o.Consensus = cons })
		if err != nil {
			t.Fatalf("import: %v", err)
		}
		if res.AuthorizedBy != "producer_transition" || !res.EnrollmentInRoot || res.Checkpoint.ProducerID != signerC.Address() {
			t.Fatalf("import result %+v", res)
		}

		stateDir := filepath.Join(t.TempDir(), "follower")
		_, err = snapImport(t, snap, stateDir, pinned, func(o *snapshotImportOptions) { o.Consensus.Transition = v1 })
		if err == nil || !errors.Is(err, chain.ErrExternalProducerNotAuthorized) {
			t.Fatalf("import under the version 1 policy: %v", err)
		}
		snapRequireUntouched(t, stateDir)
	})
}

func TestSnapshotExportRefusesUnreconciledState(t *testing.T) {
	f := snapBuildChain(t, 4, true)
	// An accounts file that does not reproduce the journal tip (as after a
	// crash between the journal append and the accounts save): export refuses;
	// a normal start must reconcile the copy first.
	acc := chain.NewAccountStore()
	if _, err := acc.Load(filepath.Join(f.src, hl1AccountsName)); err != nil {
		t.Fatal(err)
	}
	acc.Credit("snap-drift", 1)
	if err := os.Remove(filepath.Join(f.src, hl1AccountsName)); err != nil {
		t.Fatal(err)
	}
	if err := acc.Save(filepath.Join(f.src, hl1AccountsName)); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "snap")
	if _, err := snapshotRunExport(snapshotExportOptions{StateDir: f.src, OutDir: out, Consensus: snapConsensus()}); err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("export error = %v", err)
	}
	if _, err := os.Lstat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused export left %s (%v)", out, err)
	}
}

// TestSnapshotVerifyRefusesGovernanceHistory: the governance parameter store
// is not committed by the state root, so a journal with any qsdm/gov/v1
// transaction is refused before anything else is evaluated.
func TestSnapshotVerifyRefusesGovernanceHistory(t *testing.T) {
	dir := t.TempDir()
	g := &chain.Block{Height: 0, Timestamp: time.Unix(1700000000, 0).UTC(), StateRoot: strings.Repeat("0", 64)}
	g.Hash = chain.ComputeBlockHash(g)
	b1 := &chain.Block{Height: 1, PrevHash: g.Hash, Timestamp: g.Timestamp.Add(time.Second), StateRoot: g.StateRoot,
		Transactions: []*mempool.Tx{{ID: "gov-1", Sender: "x", ContractID: chainparams.ContractID}}}
	b1.Hash = chain.ComputeBlockHash(b1)
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	for _, b := range []*chain.Block{g, b1} {
		data, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(append(data, '\n'))
	}
	w.Flush()
	hl1WriteFile(t, filepath.Join(dir, hl1JournalName), buf.String())
	_, err := snapshotVerifyDir(dir, t.TempDir(), snapConsensus(), nil)
	if err == nil || !strings.Contains(err.Error(), "governance parameter transaction gov-1") {
		t.Fatalf("verify error = %v", err)
	}
}

// -----------------------------------------------------------------------------
// Boot guard, dispatch and placement
// -----------------------------------------------------------------------------

func TestSnapshotCheckpointGuard(t *testing.T) {
	blk := &chain.Block{Height: 5, Hash: strings.Repeat("1", 64), StateRoot: strings.Repeat("2", 64)}
	get := func(h uint64) (*chain.Block, bool) {
		if h == blk.Height {
			return blk, true
		}
		return nil, false
	}
	pin := config.SnapshotBootstrapConfig{Enabled: true, CheckpointHeight: 5, CheckpointBlockHash: blk.Hash, CheckpointStateRoot: blk.StateRoot}
	if err := snapshotCheckpointGuard(pin, get); err != nil {
		t.Fatalf("matching checkpoint: %v", err)
	}
	short := pin
	short.CheckpointHeight = 9
	if err := snapshotCheckpointGuard(short, get); err != nil {
		t.Fatalf("a chain below the checkpoint is still syncing: %v", err)
	}
	for _, bad := range []config.SnapshotBootstrapConfig{
		{Enabled: true, CheckpointHeight: 5, CheckpointBlockHash: strings.Repeat("3", 64), CheckpointStateRoot: blk.StateRoot},
		{Enabled: true, CheckpointHeight: 5, CheckpointBlockHash: blk.Hash, CheckpointStateRoot: strings.Repeat("3", 64)},
	} {
		if err := snapshotCheckpointGuard(bad, get); err == nil {
			t.Fatalf("guard accepted %+v", bad)
		}
	}
	off := pin
	off.Enabled = false
	off.CheckpointBlockHash = strings.Repeat("3", 64)
	if err := snapshotCheckpointGuard(off, get); err != nil {
		t.Fatalf("disabled guard must be a no-op: %v", err)
	}
}

func TestSnapshotModeRequested(t *testing.T) {
	for _, args := range [][]string{{"--snapshot-export"}, {"-snapshot-import", "--from", "x"}, {"--version", "--snapshot-importx"}} {
		if !snapshotModeRequested(args) {
			t.Fatalf("%q not detected", args)
		}
	}
	for _, args := range [][]string{nil, {"--version"}, {"--hl1-tail-replay"}} {
		if snapshotModeRequested(args) {
			t.Fatalf("%q detected", args)
		}
	}
	cfg := &config.Config{SQLitePath: filepath.Join(t.TempDir(), "qsdm.db")}
	for _, args := range [][]string{
		{"--snapshot-export"},
		{"--snapshot-export", "--state-dir", "a"},
		{"--snapshot-import"},
		{"--snapshot-import", "--from", "a", "extra"},
		{"--snapshot-importx", "--from", "a"},
	} {
		if _, err := snapshotRun(cfg, args, time.Now); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Fatalf("%q: error = %v, want usage", args, err)
		}
	}
}

func TestSnapshotDispatchPlacement(t *testing.T) {
	s := hl1ParseMain(t)
	s.before(t, "config.LoadConfig", "snapshotModeRequested")
	s.before(t, "hl1ReplayMain", "snapshotModeRequested")
	s.before(t, "snapshotModeRequested", "snapshotMain")
	s.before(t, "snapshotMain", "cfg.ProducerTransition.ValidateJournalPrefix")
	s.before(t, "snapshotMain", "hl1AcquireStateLock")
	// The guard runs on the restored chain, before S5 and the network.
	s.before(t, "adminProducer.RestoreChain", "snapshotCheckpointGuard")
	s.before(t, "snapshotCheckpointGuard", "hl1StartupWatermark")
	s.before(t, "snapshotCheckpointGuard", "chain.NewBlockPropagator")
}

// snapDropAdvisory removes the advisory checkpoint file, so the journal
// itself is checked against the pinned checkpoint.
func snapDropAdvisory(t *testing.T, snap string) {
	t.Helper()
	if err := os.Remove(filepath.Join(snap, ledgersnapshot.CheckpointFileName())); err != nil {
		t.Fatal(err)
	}
}

// TestSnapshotVerifyAppliesConsensusSettingsFirst: snapshotVerifyDir used to
// recompute every block hash before it applied the consensus settings. The
// content-root activation height changes block hashes from that height on,
// so an offline export or import of a chain that crosses it failed with
// "journal block <activation height> hash does not match its contents". An
// offline run starts with the package defaults (content root off); the
// settings must come first.
func TestSnapshotVerifyAppliesConsensusSettingsFirst(t *testing.T) {
	const act = 2
	prev := chain.TxContentRootActivationHeight()
	t.Cleanup(func() { chain.SetTxContentRootActivationHeight(prev) })
	chain.SetTxContentRootActivationHeight(act)
	f := snapBuildChain(t, 6, true)

	// The fixture must make the setting matter: some block at or above the
	// activation height has transactions, so its hash depends on the setting.
	blocks, err := chain.LoadChainNDJSON(filepath.Join(f.src, hl1JournalName))
	if err != nil {
		t.Fatal(err)
	}
	differs := false
	for _, b := range blocks {
		if b.Height < act || len(b.Transactions) == 0 {
			continue
		}
		chain.SetTxContentRootActivationHeight(0)
		legacy := chain.ComputeBlockHash(b)
		chain.SetTxContentRootActivationHeight(act)
		if legacy != b.Hash {
			differs = true
			break
		}
	}
	if !differs {
		t.Fatal("fixture: no block's hash depends on the content-root setting; the test would prove nothing")
	}

	settings := func() { chain.SetTxContentRootActivationHeight(act) }
	cons := snapConsensus()
	cons.ConsensusSettings = settings

	chain.SetTxContentRootActivationHeight(0) // a fresh offline process: the package default
	out := filepath.Join(t.TempDir(), "snap")
	if _, err := snapshotRunExport(snapshotExportOptions{StateDir: f.src, OutDir: out, Consensus: cons}); err != nil {
		t.Fatalf("export with the settings supplied by ConsensusSettings: %v", err)
	}

	chain.SetTxContentRootActivationHeight(0)
	if _, err := snapImport(t, out, t.TempDir(), f.pinned(), func(o *snapshotImportOptions) { o.Consensus = cons }); err != nil {
		t.Fatalf("import with the settings supplied by ConsensusSettings: %v", err)
	}

	// Without the settings the same snapshot is refused (the check is real).
	chain.SetTxContentRootActivationHeight(0)
	if _, err := snapImport(t, out, t.TempDir(), f.pinned(), nil); err == nil || !strings.Contains(err.Error(), "hash does not match its contents") {
		t.Fatalf("import without the content-root setting: want a hash mismatch, got %v", err)
	}
}
