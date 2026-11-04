package main

// snapshot_bootstrap.go (QSDM/docs/docs/SNAPSHOT_BOOTSTRAP.md): a verified
// snapshot bootstrap for a new follower.
//
//	qsdm --snapshot-export --state-dir <stopped copy> --out <new dir>
//	qsdm --snapshot-import --from <snapshot dir>
//
// Export is offline and read-only on the source apart from its state lock
// file: ledgersnapshot.Capture copies the four validator files under the
// lock, then the copy is verified exactly as a boot restores it and its tip
// is written to snapshot-checkpoint.json (advisory).
//
// Import runs on an empty follower state directory under the state lock and
// only with [snapshot_bootstrap] enabled. It stages a hash-verified copy of
// the snapshot inside the state directory and refuses on any mismatch:
//   - each staged file has the manifest's size and SHA-256;
//   - the journal ends with "\n", loads, is one canonical chain that starts at
//     genesis (height 0, empty prev hash), every block hash recomputes and
//     links to its parent (so the pinned tip hash commits to all history);
//   - the tip is exactly the pinned checkpoint: height, block hash, state root;
//   - the tip carries producer_auth that verifies (ML-DSA-87, the same
//     chain.VerifyBlockSignature as external block auth) and its producer is
//     authorized by checkpoint_producer_id, the producer transition or
//     authorized_block_producers;
//   - the producer transition (when configured) validates the whole journal
//     and its immutable prefix;
//   - the accounts and enrollment files reproduce the tip's state root through
//     the unmodified boot restore (hl1ReplayStack.restore), and, once the
//     enrollment root is active, with the enrollment registry included;
//   - no state that the state root does not commit is imported: the journal
//     holds no governance parameter transaction (the parameter store is not
//     rebuilt from blocks) and the local staking ledger is empty.
//
// Then it renames the enrollment, accounts and journal files into place
// (journal last), fsyncs the directory and writes qsdm_snapshot_import.json.
// The next normal start restores that state like any other and syncs from
// N+1. Receipts before N are not imported (they are not committed by the
// state root).

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/config"
	"github.com/blackbeardONE/QSDM/pkg/envcompat"
	"github.com/blackbeardONE/QSDM/pkg/governance/chainparams"
	"github.com/blackbeardONE/QSDM/pkg/ledgersnapshot"
	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

const (
	snapshotExportMode = "snapshot-export"
	snapshotImportMode = "snapshot-import"

	// snapshotImportRecordName records a completed import in the state
	// directory (audit only; nothing reads it at boot).
	snapshotImportRecordName = "qsdm_snapshot_import.json"
	hl1StakingName           = "qsdm_staking.json"
	hl1GovernanceName        = "qsdm_governance.json"
	snapshotStagePrefix      = ".snapshot-import-"
)

// snapshotModeRequested reports whether args ask for a snapshot mode. Any
// argument that mentions one counts, so a malformed invocation is refused
// with 78 instead of starting a node.
func snapshotModeRequested(args []string) bool {
	for _, a := range args {
		if strings.Contains(a, snapshotExportMode) || strings.Contains(a, snapshotImportMode) {
			return true
		}
	}
	return false
}

// snapshotMain is the process entry of both snapshot modes. It never
// returns: exit 0 with a JSON report on stdout, or 78 (fatalRestore).
func snapshotMain(cfg *config.Config, args []string) {
	logger = logging.NewLoggerWithLevel(cfg.LogFile, true, cfg.LogLevel)
	out, err := snapshotRun(cfg, args, time.Now)
	if err != nil {
		fatalRestore("snapshot bootstrap: %v", err)
		return
	}
	fmt.Fprintln(os.Stdout, string(out))
	hl1Exit(0)
}

// snapshotConsensus is the consensus configuration a snapshot is verified
// under: the importing (or exporting) node's own.
type snapshotConsensus struct {
	Transition               *producerpolicy.Transition
	AuthorizedBlockProducers []string
	GovernanceAuthorities    []string
	// ConsensusSettings applies main()'s package-level consensus settings
	// (hl1ReplayConsensusSettings). Nil keeps the current ones (tests).
	ConsensusSettings func()
	Log               *logging.Logger
}

func snapshotConsensusFromConfig(cfg *config.Config) snapshotConsensus {
	return snapshotConsensus{
		Transition:               cfg.ProducerTransition,
		AuthorizedBlockProducers: cfg.AuthorizedBlockProducers,
		GovernanceAuthorities:    cfg.GovernanceAuthorities,
		ConsensusSettings:        func() { hl1ReplayConsensusSettings(cfg) },
		Log:                      logger,
	}
}

func snapshotUsage() error {
	return fmt.Errorf("usage: qsdm --%s --state-dir <stopped state dir copy> --out <new dir> | qsdm --%s --from <snapshot dir>", snapshotExportMode, snapshotImportMode)
}

// snapshotRun parses args and runs one mode, returning its JSON report.
func snapshotRun(cfg *config.Config, args []string, now func() time.Time) ([]byte, error) {
	if len(args) == 0 {
		return nil, snapshotUsage()
	}
	mode := strings.TrimLeft(args[0], "-")
	fset := flag.NewFlagSet("qsdm "+mode, flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	var res any
	switch mode {
	case snapshotExportMode:
		stateDir := fset.String("state-dir", "", "")
		outDir := fset.String("out", "", "")
		if err := fset.Parse(args[1:]); err != nil || fset.NArg() != 0 || *stateDir == "" || *outDir == "" {
			return nil, snapshotUsage()
		}
		r, err := snapshotRunExport(snapshotExportOptions{
			StateDir: *stateDir, OutDir: *outDir, Consensus: snapshotConsensusFromConfig(cfg), Now: now,
		})
		if err != nil {
			return nil, err
		}
		res = r
	case snapshotImportMode:
		from := fset.String("from", "", "")
		if err := fset.Parse(args[1:]); err != nil || fset.NArg() != 0 || *from == "" {
			return nil, snapshotUsage()
		}
		role, err := resolveBlockProductionRole(
			envcompat.Truthy("QSDM_SOLO_VALIDATOR_MODE", "QSDM_SOLO_VALIDATOR_MODE"),
			envcompat.Truthy("QSDM_NETWORK_BLOCK_PRODUCER", "QSDM_NETWORK_BLOCK_PRODUCER"))
		if err != nil {
			return nil, err
		}
		r, err := snapshotRunImport(snapshotImportOptions{
			SnapshotDir:  *from,
			StateDir:     filepath.Dir(cfg.SQLitePath),
			Pinned:       cfg.SnapshotBootstrap,
			ProducerRole: role.localProductionEnabled(),
			Consensus:    snapshotConsensusFromConfig(cfg),
			Now:          now,
		})
		if err != nil {
			return nil, err
		}
		res = r
	default:
		return nil, snapshotUsage()
	}
	return json.MarshalIndent(res, "", "  ")
}

// -----------------------------------------------------------------------------
// Verification (shared by export and import)
// -----------------------------------------------------------------------------

// snapshotVerifyResult describes a verified snapshot directory.
type snapshotVerifyResult struct {
	Tip                   *chain.Block
	Blocks                int
	JournalBytes          int64
	AuthorizedBy          string // import only: which configuration authorized the tip producer
	EnrollmentRootChecked bool   // the enrollment registry is part of the verified root
	LoadDuration          time.Duration
	RestoreDuration       time.Duration
}

// snapshotVerifyDir verifies the four validator files in dir as the boot
// restore does, building the replay stack's own files in scratch. With pinned
// non-nil the tip must be exactly the pinned checkpoint and be signed by an
// authorized producer. It never writes to dir.
func snapshotVerifyDir(dir, scratch string, c snapshotConsensus, pinned *config.SnapshotBootstrapConfig) (snapshotVerifyResult, error) {
	var res snapshotVerifyResult
	log := c.Log
	if log == nil {
		log = hl1Logger()
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

	if c.ConsensusSettings != nil {
		c.ConsensusSettings()
	}
	restoreStart := time.Now()
	st, err := hl1NewReplayStack(scratch, c.GovernanceAuthorities, c.AuthorizedBlockProducers, c.Transition, log)
	if err != nil {
		return res, err
	}
	if err := st.producer.ValidateProducerTransitionChain(blocks); err != nil {
		return res, fmt.Errorf("producer transition persisted chain: %w", err)
	}
	if err := st.restore(blocks, filepath.Join(dir, hl1AccountsName), filepath.Join(dir, hl1EnrollmentName),
		filepath.Join(scratch, "absent-receipts.ndjson")); err != nil {
		return res, fmt.Errorf("state restore: %w", err)
	}
	if c.Transition != nil {
		// transitionStateRootHeight: the root commits the enrollment registry
		// from effective_height on.
		res.EnrollmentRootChecked = tip.Height >= c.Transition.EffectiveHeight
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

// snapshotAuthorizeTip requires the checkpoint block to be signed, the
// signature to verify (ML-DSA-87 producer_auth, chain.VerifyBlockSignature)
// and its producer to be authorized. It returns what authorized it.
func snapshotAuthorizeTip(tip *chain.Block, c snapshotConsensus, pinnedProducer string) (string, error) {
	if !tip.ProducerAuth.Signed() {
		return "", fmt.Errorf("checkpoint block %d carries no producer_auth: a snapshot can only be anchored at a producer-signed block", tip.Height)
	}
	if err := chain.VerifyBlockSignature(tip); err != nil {
		return "", fmt.Errorf("checkpoint block %d: %w", tip.Height, err)
	}
	by := ""
	if pinnedProducer != "" {
		if tip.ProducerID != pinnedProducer {
			return "", fmt.Errorf("checkpoint block %d is signed by %s, checkpoint_producer_id is %s", tip.Height, tip.ProducerID, pinnedProducer)
		}
		by = "checkpoint_producer_id"
	}
	switch {
	case c.Transition != nil:
		// The producer the transition authorizes at this height, as
		// validateTransitionBlock (pkg/chain) enforces it: the replacement
		// producer from effective_height on, the historical producer below it
		// (a signed block below historical_signature_height included).
		want := c.Transition.HistoricalProducer
		if tip.Height >= c.Transition.EffectiveHeight {
			want = c.Transition.ReplacementProducer
		}
		if tip.ProducerID != want {
			return "", fmt.Errorf("%w: checkpoint block %d producer %s, the producer transition expects %s", chain.ErrExternalProducerNotAuthorized, tip.Height, tip.ProducerID, want)
		}
		by = "producer_transition"
	case len(c.AuthorizedBlockProducers) > 0:
		for _, id := range c.AuthorizedBlockProducers {
			if strings.TrimSpace(id) == tip.ProducerID {
				return "authorized_block_producers", nil
			}
		}
		if by == "" {
			return "", fmt.Errorf("%w: checkpoint block %d producer %s is not in authorized_block_producers (set checkpoint_producer_id to pin the ML-DSA-87 producer address)", chain.ErrExternalProducerNotAuthorized, tip.Height, tip.ProducerID)
		}
	}
	if by == "" {
		return "", errors.New("no trusted producer identity: set [snapshot_bootstrap] checkpoint_producer_id, a producer transition or authorized_block_producers")
	}
	return by, nil
}

// snapshotRequireEmptyStaking refuses a staking ledger with any bond or
// unbonding entry: it is local state that the state root does not commit
// (an unbonding entry credits balances when it matures).
func snapshotRequireEmptyStaking(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("staking ledger: %w", err)
	}
	var doc struct {
		V      int               `json:"v"`
		Bonds  []json.RawMessage `json:"bonds"`
		Unbond []json.RawMessage `json:"unbond"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("staking ledger: %w", err)
	}
	if len(doc.Bonds) != 0 || len(doc.Unbond) != 0 {
		return fmt.Errorf("staking ledger holds %d bond(s) and %d unbonding entr(ies): local state the state root does not commit cannot be imported; sync this node from genesis", len(doc.Bonds), len(doc.Unbond))
	}
	return nil
}

// -----------------------------------------------------------------------------
// Export
// -----------------------------------------------------------------------------

type snapshotExportOptions struct {
	StateDir  string
	OutDir    string
	Consensus snapshotConsensus
	Now       func() time.Time
}

type snapshotExportResult struct {
	OutDir           string                    `json:"out_dir"`
	Manifest         ledgersnapshot.Manifest   `json:"manifest"`
	Checkpoint       ledgersnapshot.Checkpoint `json:"checkpoint"`
	LoadSeconds      float64                   `json:"journal_load_seconds"`
	RestoreSeconds   float64                   `json:"restore_seconds"`
	EnrollmentInRoot bool                      `json:"enrollment_root_checked"`
}

// snapshotRunExport captures a stopped state directory copy and verifies the
// captured files. The output directory is removed on any failure.
func snapshotRunExport(o snapshotExportOptions) (res snapshotExportResult, err error) {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	outDir, err := filepath.Abs(o.OutDir)
	if err != nil {
		return res, err
	}
	m, err := ledgersnapshot.Capture(ledgersnapshot.Options{StateDir: o.StateDir, OutDir: outDir, Now: now()})
	if err != nil {
		return res, err
	}
	ok := false
	defer func() {
		if !ok {
			ledgersnapshot.RemoveSnapshot(outDir)
		}
	}()
	scratch, err := os.MkdirTemp("", "qsdm-snapshot-verify-")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(scratch)
	v, err := snapshotVerifyDir(outDir, scratch, o.Consensus, nil)
	if err != nil {
		return res, fmt.Errorf("captured snapshot does not verify: %w", err)
	}
	cp := ledgersnapshot.Checkpoint{
		Height: v.Tip.Height, BlockHash: v.Tip.Hash, StateRoot: v.Tip.StateRoot, ProducerID: v.Tip.ProducerID,
		TipSigned: v.Tip.ProducerAuth.Signed(), Blocks: v.Blocks, JournalBytes: v.JournalBytes,
	}
	if err := ledgersnapshot.WriteCheckpoint(outDir, cp); err != nil {
		return res, err
	}
	ok = true
	return snapshotExportResult{
		OutDir: outDir, Manifest: m, Checkpoint: cp,
		LoadSeconds: v.LoadDuration.Seconds(), RestoreSeconds: v.RestoreDuration.Seconds(),
		EnrollmentInRoot: v.EnrollmentRootChecked,
	}, nil
}

// -----------------------------------------------------------------------------
// Import
// -----------------------------------------------------------------------------

type snapshotImportOptions struct {
	SnapshotDir  string
	StateDir     string
	Pinned       config.SnapshotBootstrapConfig
	ProducerRole bool
	Consensus    snapshotConsensus
	Now          func() time.Time
}

type snapshotImportResult struct {
	StateDir         string                        `json:"state_dir"`
	Checkpoint       ledgersnapshot.Checkpoint     `json:"checkpoint"`
	AuthorizedBy     string                        `json:"authorized_by"`
	EnrollmentInRoot bool                          `json:"enrollment_root_checked"`
	Installed        []ledgersnapshot.FileEvidence `json:"installed"`
	ImportedAt       string                        `json:"imported_at"`
	LoadSeconds      float64                       `json:"journal_load_seconds"`
	RestoreSeconds   float64                       `json:"restore_seconds"`
	TotalSeconds     float64                       `json:"total_seconds"`
}

// snapshotInstallOrder is the rename order: the journal last, so that an
// interrupted install never leaves a journal without its state files.
var snapshotInstallOrder = []string{hl1EnrollmentName, hl1AccountsName, hl1JournalName}

// snapshotRunImport verifies the snapshot against the pinned checkpoint and
// installs it into an empty follower state directory.
func snapshotRunImport(o snapshotImportOptions) (res snapshotImportResult, err error) {
	start := time.Now()
	now := o.Now
	if now == nil {
		now = time.Now
	}
	if !o.Pinned.Enabled {
		return res, errors.New("[snapshot_bootstrap] enabled is false (env QSDM_SNAPSHOT_BOOTSTRAP): import refused")
	}
	if err := o.Pinned.Validate(); err != nil {
		return res, err
	}
	if o.ProducerRole {
		return res, errors.New("import is for a follower: refused in the producer role (QSDM_SOLO_VALIDATOR_MODE / QSDM_NETWORK_BLOCK_PRODUCER)")
	}
	stateDir, err := filepath.Abs(o.StateDir)
	if err != nil {
		return res, err
	}
	snapDir, err := filepath.Abs(o.SnapshotDir)
	if err != nil {
		return res, err
	}
	if snapshotPathWithin(stateDir, snapDir) || snapshotPathWithin(snapDir, stateDir) {
		return res, errors.New("the snapshot and state directories must be disjoint")
	}
	lock, err := chain.AcquireStateLock(filepath.Join(stateDir, legacymining.StateLockFile))
	if err != nil {
		return res, fmt.Errorf("validator state lock: %w", err)
	}
	defer lock.Close()
	for _, name := range []string{hl1JournalName, hl1AccountsName, hl1EnrollmentName, hl1StakingName,
		hl1ReceiptsName, hl1ReceiptsLegacyName, hl1GovernanceName, snapshotImportRecordName} {
		if _, err := os.Lstat(filepath.Join(stateDir, name)); err == nil {
			return res, fmt.Errorf("state directory already holds %s: import only fills an empty follower state directory", name)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return res, err
		}
	}

	stage := filepath.Join(stateDir, snapshotStagePrefix+now().UTC().Format("20060102T150405.000000000Z"))
	manifest, err := ledgersnapshot.StageVerified(snapDir, stage)
	if err != nil {
		return res, err
	}
	defer func() { _ = ledgersnapshot.RemoveStage(stage) }()
	if cp, ok, err := ledgersnapshot.ReadCheckpoint(snapDir); err != nil {
		return res, err
	} else if ok && (cp.Height != o.Pinned.CheckpointHeight || cp.BlockHash != o.Pinned.CheckpointBlockHash || cp.StateRoot != o.Pinned.CheckpointStateRoot) {
		return res, fmt.Errorf("the snapshot's %s names height %d hash %s root %s, not the trusted checkpoint", ledgersnapshot.CheckpointFileName(), cp.Height, cp.BlockHash, cp.StateRoot)
	}
	scratch := filepath.Join(stage, "verify")
	if err := os.Mkdir(scratch, 0o700); err != nil {
		return res, err
	}
	v, err := snapshotVerifyDir(stage, scratch, o.Consensus, &o.Pinned)
	if err != nil {
		return res, err
	}

	byName := map[string]ledgersnapshot.FileEvidence{}
	for _, f := range manifest.Files {
		byName[f.Name] = f
	}
	for _, name := range snapshotInstallOrder {
		dst := filepath.Join(stateDir, name)
		if _, err := os.Lstat(dst); !errors.Is(err, fs.ErrNotExist) {
			return res, fmt.Errorf("refusing to overwrite %s", dst)
		}
		if err := os.Rename(filepath.Join(stage, name), dst); err != nil {
			return res, fmt.Errorf("install %s: %w (remove the installed files before retrying)", name, err)
		}
		res.Installed = append(res.Installed, byName[name])
	}
	if err := (hl1OSFS{}).SyncDir(stateDir); err != nil {
		return res, fmt.Errorf("fsync state directory: %w", err)
	}
	res.StateDir = stateDir
	res.Checkpoint = ledgersnapshot.Checkpoint{
		Height: v.Tip.Height, BlockHash: v.Tip.Hash, StateRoot: v.Tip.StateRoot, ProducerID: v.Tip.ProducerID,
		TipSigned: true, Blocks: v.Blocks, JournalBytes: v.JournalBytes,
	}
	res.AuthorizedBy = v.AuthorizedBy
	res.EnrollmentInRoot = v.EnrollmentRootChecked
	res.ImportedAt = now().UTC().Format(time.RFC3339Nano)
	res.LoadSeconds, res.RestoreSeconds = v.LoadDuration.Seconds(), v.RestoreDuration.Seconds()
	res.TotalSeconds = time.Since(start).Seconds()
	if err := snapshotWriteRecord(filepath.Join(stateDir, snapshotImportRecordName), res); err != nil {
		return res, fmt.Errorf("write import record (the snapshot is installed): %w", err)
	}
	if err := (hl1OSFS{}).SyncDir(stateDir); err != nil {
		return res, fmt.Errorf("fsync state directory: %w", err)
	}
	return res, nil
}

func snapshotWriteRecord(path string, res snapshotImportResult) error {
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func snapshotPathWithin(parent, candidate string) bool {
	rel, err := filepath.Rel(parent, candidate)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

// -----------------------------------------------------------------------------
// Boot guard
// -----------------------------------------------------------------------------

// snapshotCheckpointGuard runs after the boot restore. With the snapshot
// bootstrap enabled, a restored chain that reaches the checkpoint height
// must hold the checkpoint block there; a shorter chain passes (it is still
// syncing). Disabled, it does nothing.
func snapshotCheckpointGuard(c config.SnapshotBootstrapConfig, getBlock func(uint64) (*chain.Block, bool)) error {
	if !c.Enabled {
		return nil
	}
	b, ok := getBlock(c.CheckpointHeight)
	if !ok || b == nil {
		return nil
	}
	if b.Hash != c.CheckpointBlockHash || b.StateRoot != c.CheckpointStateRoot {
		return fmt.Errorf("restored block %d (hash %s, state root %s) is not the trusted checkpoint (hash %s, state root %s)",
			b.Height, b.Hash, b.StateRoot, c.CheckpointBlockHash, c.CheckpointStateRoot)
	}
	return nil
}
