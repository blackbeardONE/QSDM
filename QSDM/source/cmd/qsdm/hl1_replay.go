package main

// hl1_replay.go (HL1 WP10): qsdm --hl1-tail-replay, the offline R-C4 mode
// (design rev 4 §4.7 R-C4, §5 "Replay mode", Appendix A). It repairs the
// journal-ahead-of-snapshots tail (C4, C5) by replaying the journal tip J
// onto the J-1 snapshot pair through the unmodified restore, root check and
// TryAppendExternalBlock, with the persistence hook in replay mode. It never
// removes a block, never starts the API server, the network or the driver,
// and runs entirely under the validator state lock:
//
//	S0   state lock; busy -> exit 78, nothing touched
//	S1   FAILSTOP.json is allowed and kept (a replay failStop keeps it)
//	S2   QSDM_LEGACY_MINING_* environment and canary config -> 78
//	S3   stale D1 temps; FAILSTOP.armed only if FAILSTOP.json is absent
//	S4a  transition prefix check under the lock; journal ends with "\n" and
//	     loads; J = tip, J-1 = the block before it
//	S5   W present, J-1 <= W.height <= J, hash match (checked here, before
//	     any change; replay writes W only in hook step H7)
//	0    receipts pre-trim: truncate from the first complete line with
//	     BlockHeight >= J (or after the last "\n"), archived first
//	1    J-1 pair: the .h<J-1> generation links if present, else the main
//	     files; S4 restore of the J-1 prefix and its state-root check
//	2    hook in replay mode: no H1, H2 asserts line J and fsyncs the
//	     journal, H3-H7 (W := J), no H8-H10
//	3    TryAppendExternalBlock(J): root check, then the sweep and the hook
//	4    exit 0 -> R-START
//
// Any refusal exits 78 (fatalRestore). A persistence error inside the hook
// is failStop: exit 86, FAILSTOP.json.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/internal/hl1tail"
	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/internal/v2wiring"
	"github.com/blackbeardONE/QSDM/pkg/buildinfo"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/config"
	"github.com/blackbeardONE/QSDM/pkg/envcompat"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

// hl1ReplayFlag selects replay mode. It must be the only argument.
const hl1ReplayFlag = "--hl1-tail-replay"

// errHL1ReplayFailStop reports that the replay hook fail-stopped. In
// production failStop exits 86 and this is never returned.
var errHL1ReplayFailStop = errors.New("hl1 replay: persistence hook fail-stopped")

// hl1ReplayRequested reports whether args (os.Args[1:]) ask for replay. Any
// argument that mentions hl1-tail-replay counts, so a malformed replay
// invocation is refused with 78 instead of starting a node.
func hl1ReplayRequested(args []string) bool {
	for _, a := range args {
		if strings.Contains(a, "hl1-tail-replay") {
			return true
		}
	}
	return false
}

// hl1ReplayMain is the process entry of replay mode. It never returns: exit 0
// on success, 78 on a refusal (fatalRestore), 86 from failStop. The state
// lock is released by the OS at exit.
func hl1ReplayMain(cfg *config.Config, args []string) {
	if len(args) != 1 || (args[0] != hl1ReplayFlag && args[0] != "-hl1-tail-replay") {
		fatalRestore("hl1 replay: usage: qsdm %s (no other arguments); got %q", hl1ReplayFlag, args)
		return
	}
	logger = logging.NewLoggerWithLevel(cfg.LogFile, true, cfg.LogLevel)
	stateDir := filepath.Dir(cfg.SQLitePath)
	hl1Stderrf("hl1 replay: state directory %s", stateDir)
	res, err := hl1RunReplay(hl1ReplayOptions{
		StateDir:                 stateDir,
		Transition:               cfg.ProducerTransition,
		AuthorizedBlockProducers: cfg.AuthorizedBlockProducers,
		GovernanceAuthorities:    cfg.GovernanceAuthorities,
		Release:                  buildinfo.Version,
		Getenv:                   os.Getenv,
		SyncURLs:                 chainSyncURLsFromEnv(),
		ConsensusSettings:        func() { hl1ReplayConsensusSettings(cfg) },
		Stopper:                  hl1FailStop,
		FailStop:                 failStop,
		Log:                      logger,
		Now:                      time.Now,
	})
	// res.Lock is deliberately never closed: the state lock is held until the
	// process exits below.
	if errors.Is(err, errHL1ReplayFailStop) {
		hl1Exit(legacymining.ExitFailStop)
		return
	}
	if err != nil {
		fatalRestore("hl1 replay: %v", err)
		return
	}
	hl1Stderrf("hl1 replay: OK: block %d (%s) replayed onto the J-1 pair; W := %d; next: R-START", res.J.Height, res.J.Hash, res.Watermark.Height)
	hl1Exit(0)
}

// hl1ReplayConsensusSettings applies the package-level consensus settings
// that main() applies before the restore block, from the same config. The
// cmd/qsdm tests pin this list against main().
func hl1ReplayConsensusSettings(cfg *config.Config) {
	if envcompat.Truthy("QSDM_V2_ACTIVE", "QSDM_V2_ACTIVE") {
		mining.SetForkV2Height(0)
	}
	if cfg.MiningOperatorPublicKeyRetentionHeight > 0 {
		enrollment.SetOperatorPublicKeyRetentionHeight(cfg.MiningOperatorPublicKeyRetentionHeight)
	}
	chain.SetSignedCertificateActivationHeight(cfg.SignedConsensusActivationHeight)
	chain.SetSignedBlockActivationHeight(cfg.SignedConsensusActivationHeight)
	chain.SetEvidenceProofActivationHeight(cfg.SignedConsensusActivationHeight)
	chain.SetTaskActionSignatureActivationHeight(cfg.TaskActionSignatureActivationHeight)
	chain.SetTxContentRootActivationHeight(cfg.TxContentRootActivationHeight)
	chain.SetEnrollmentStateRootActivationHeight(cfg.EnrollmentStateRootActivationHeight)
	chain.SetRequireSignedCertificates(cfg.RequireSignedVotes)
	chain.SetRequireSignedBlocks(cfg.RequireSignedVotes)
	chain.SetRequireEvidenceProof(cfg.RequireSignedVotes)
}

// hl1ReplayOptions are the inputs of hl1RunReplay.
type hl1ReplayOptions struct {
	StateDir                 string
	Transition               *producerpolicy.Transition
	AuthorizedBlockProducers []string
	GovernanceAuthorities    []string
	Release                  string
	Getenv                   func(string) string
	SyncURLs                 []string
	// ConsensusSettings applies main()'s package-level consensus settings
	// (hl1ReplayConsensusSettings). Nil keeps the current settings (tests).
	ConsensusSettings func()
	Stopper           *hl1FailStopper           // armed at S3; nil: a private one
	FailStop          legacymining.FailStopFunc // the hook's failStop; nil: Stopper.Stop
	Log               *logging.Logger
	Now               func() time.Time
}

// hl1ReplayResult reports a replay run.
type hl1ReplayResult struct {
	// Lock is the state lock, held from S0 until the process exits (the
	// caller releases it only in tests). Nil when S0 failed.
	Lock          *chain.StateLock
	J             *chain.Block
	FailStopLatch bool   // FAILSTOP.json was present (and was kept)
	PairAccounts  string // the J-1 accounts snapshot used
	PairEnroll    string // the J-1 enrollment snapshot used
	PreTrim       hl1tail.PreTrimResult
	Watermark     legacymining.Watermark // W after H7
}

// State-directory file names (main.go restore block).
const (
	hl1JournalName        = "qsdm_chain.ndjson"
	hl1AccountsName       = "qsdm_accounts.json"
	hl1EnrollmentName     = "qsdm_enrollment.json"
	hl1ReceiptsName       = "qsdm_receipts.ndjson"
	hl1ReceiptsLegacyName = "qsdm_receipts.json"
)

// hl1RunReplay runs replay mode. A returned error is a refusal (exit 78),
// except errHL1ReplayFailStop (exit 86, tests only).
func hl1RunReplay(o hl1ReplayOptions) (res hl1ReplayResult, err error) {
	log := o.Log
	if log == nil {
		log = hl1Logger()
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	getenv := o.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	dir := o.StateDir
	journal := filepath.Join(dir, hl1JournalName)
	accountsMain := filepath.Join(dir, hl1AccountsName)
	enrollMain := filepath.Join(dir, hl1EnrollmentName)
	receipts := filepath.Join(dir, hl1ReceiptsName)

	// S0: the state lock, before anything in the state directory is touched.
	lock, err := chain.AcquireStateLock(filepath.Join(dir, legacymining.StateLockFile))
	if err != nil {
		return res, fmt.Errorf("validator state lock: %w", err)
	}
	res.Lock = lock

	// S1 (replay): FAILSTOP.json is allowed and is not removed.
	if _, err := os.Lstat(filepath.Join(dir, legacymining.FailStopFile)); err == nil {
		res.FailStopLatch = true
		log.Warn("hl1 replay: FAILSTOP.json is present; replay runs and keeps it (R-FS still applies)")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return res, fmt.Errorf("hl1 S1: stat %s: %w", legacymining.FailStopFile, err)
	}
	// S2.
	boot, err := hl1LoadBootConfig(getenv, o.SyncURLs)
	if err != nil {
		return res, fmt.Errorf("hl1 S2: %w", err)
	}
	// S3: stale temps; FAILSTOP.armed only if FAILSTOP.json is absent
	// (ArmMarker). The stopper is armed either way, so a replay failStop
	// exits 86 and leaves FAILSTOP.json present.
	stopper := o.Stopper
	if stopper == nil {
		stopper = &hl1FailStopper{}
	}
	if err := hl1ArmFailStop(stopper, dir, hl1LegacyDirs(dir, boot.Env), o.Release, now()); err != nil {
		return res, fmt.Errorf("hl1 S3: %w", err)
	}
	failStopFn := o.FailStop
	if failStopFn == nil {
		failStopFn = stopper.Stop
	}

	// S4 (read-only part): the transition prefix under the lock, then the
	// journal, which must end with "\n" and load completely.
	if err := o.Transition.ValidateJournalPrefix(journal); err != nil {
		return res, fmt.Errorf("producer transition locked preflight: %w", err)
	}
	if err := hl1RequireFinalNewline(hl1OSFS{}, journal); err != nil {
		return res, fmt.Errorf("journal: %w", err)
	}
	blocks, err := chain.LoadChainNDJSON(journal)
	if err != nil {
		return res, fmt.Errorf("journal: %w", err)
	}
	if len(blocks) < 2 {
		return res, fmt.Errorf("journal %s holds %d block(s): replay needs the tip J and the block J-1 before it", journal, len(blocks))
	}
	if _, dropped := canonicalPersistedChain(blocks); dropped != 0 {
		return res, fmt.Errorf("journal contains %d forked duplicate block(s); replay never rewrites the journal (R-X)", dropped)
	}
	j, prev := blocks[len(blocks)-1], blocks[len(blocks)-2]
	if j == nil || prev == nil || j.Height != prev.Height+1 || j.PrevHash != prev.Hash {
		return res, errors.New("the journal tip does not extend the block before it (R-X)")
	}
	res.J = j

	// S5 (replay): W present, J-1 <= W.height <= J, and the hash of the
	// matching journal block. Checked before any change.
	w, err := hl1ReadWatermark(dir)
	if err != nil {
		return res, fmt.Errorf("hl1 S5: %w (R-X)", err)
	}
	switch w.Height {
	case prev.Height:
		if w.Hash != prev.Hash {
			return res, fmt.Errorf("hl1 S5: journal block %d has hash %s, W has %s (R-X)", prev.Height, prev.Hash, w.Hash)
		}
	case j.Height:
		if w.Hash != j.Hash {
			return res, fmt.Errorf("hl1 S5: journal block %d has hash %s, W has %s (R-X)", j.Height, j.Hash, w.Hash)
		}
	default:
		return res, fmt.Errorf("hl1 S5: W=%d is outside [J-1, J] = [%d, %d] (R-X)", w.Height, prev.Height, j.Height)
	}
	log.Info("hl1 replay: preconditions hold", "journal_tip", j.Height, "journal_tip_hash", j.Hash, "watermark_height", w.Height)

	// Step 0: receipts pre-trim.
	if _, err := os.Lstat(receipts); errors.Is(err, fs.ErrNotExist) {
		if _, lerr := os.Lstat(filepath.Join(dir, hl1ReceiptsLegacyName)); lerr == nil {
			return res, fmt.Errorf("only the legacy receipts file %s exists; replay does not migrate receipts (R-X)", hl1ReceiptsLegacyName)
		}
	}
	res.PreTrim, err = hl1tail.PreTrimReceipts(receipts, j.Height, now())
	if err != nil {
		return res, fmt.Errorf("receipts pre-trim: %w", err)
	}
	if res.PreTrim.Archive != "" {
		log.Warn("hl1 replay: receipts pre-trimmed", "cut_offset", res.PreTrim.Cut, "removed_bytes", res.PreTrim.Size-res.PreTrim.Cut, "archive", res.PreTrim.Archive)
		hl1Stderrf("hl1 replay: receipts pre-trim: removed %d bytes at offset %d (archived to %s)", res.PreTrim.Size-res.PreTrim.Cut, res.PreTrim.Cut, res.PreTrim.Archive)
	}

	// Step 1: the J-1 pair.
	if res.PairAccounts, err = hl1ReplayPairFile(accountsMain, prev.Height); err != nil {
		return res, err
	}
	if res.PairEnroll, err = hl1ReplayPairFile(enrollMain, prev.Height); err != nil {
		return res, err
	}
	log.Info("hl1 replay: J-1 snapshot pair", "height", prev.Height, "accounts", res.PairAccounts, "enrollment", res.PairEnroll)
	if o.ConsensusSettings != nil {
		o.ConsensusSettings()
	}
	st, err := hl1NewReplayStack(dir, o.GovernanceAuthorities, o.AuthorizedBlockProducers, o.Transition, log)
	if err != nil {
		return res, err
	}
	if err := st.producer.ValidateProducerTransitionChain(blocks); err != nil {
		return res, fmt.Errorf("producer transition persisted chain: %w", err)
	}
	if err := st.restore(blocks[:len(blocks)-1], res.PairAccounts, res.PairEnroll, receipts); err != nil {
		return res, fmt.Errorf("restore of the J-1 prefix: %w", err)
	}

	// Step 2: the hook in replay mode.
	var hookErr error
	hook := &hl1PersistHook{
		Mode:             hl1HookReplay,
		ProducerRole:     true,
		StateDir:         dir,
		JournalPath:      journal,
		AccountsPath:     accountsMain,
		FS:               hl1OSFS{},
		Prior:            st.v2.SealedBlockHook, // H0: the v2wiring sweep and gov promotion
		SaveAccounts:     st.accounts.Save,
		FailStop:         failStopFn,
		OnFailStopReturn: func(err error) { hookErr = err },
		Now:              now,
		Log:              log,
	}
	if st.v2.EnrollmentState != nil {
		hook.EnrollmentPath = enrollMain
		hook.SaveEnrollment = st.v2.EnrollmentState.Save
	}
	hook.ReceiptsPath = receipts
	hook.AppendReceipts = st.receipts.AppendBlockNDJSON
	st.producer.OnSealedBlock = hook.OnSealedBlock

	// Step 3.
	if err := st.producer.TryAppendExternalBlock(j); err != nil {
		return res, fmt.Errorf("replay of block %d onto the J-1 pair failed: %w (if J > W: hl1-tail trim --above %d; otherwise R-X)", j.Height, err, prev.Height)
	}
	if hookErr != nil {
		return res, fmt.Errorf("%w: %v", errHL1ReplayFailStop, hookErr)
	}
	if tip, ok := st.producer.LatestBlock(); !ok || tip.Hash != j.Hash {
		return res, fmt.Errorf("block %d was not appended", j.Height)
	}
	res.Watermark, err = hl1ReadWatermark(dir)
	if err != nil {
		return res, fmt.Errorf("watermark after replay: %w", err)
	}
	if res.Watermark.Height != j.Height || res.Watermark.Hash != j.Hash || res.Watermark.Source != legacymining.WatermarkSourceReplay {
		return res, fmt.Errorf("watermark after replay is %d/%s (%s), want %d/%s (replay)", res.Watermark.Height, res.Watermark.Hash, res.Watermark.Source, j.Height, j.Hash)
	}
	log.Info("hl1 replay: block replayed; W advanced", "height", j.Height, "hash", j.Hash, "watermark_height", res.Watermark.Height)
	return res, nil
}

// hl1ReplayPairFile returns the generation link <main>.h<height> if it
// exists, else main.
func hl1ReplayPairFile(main string, height uint64) (string, error) {
	link := hl1GenerationLink(main, height)
	fi, err := os.Lstat(link)
	switch {
	case err == nil && fi.Mode().IsRegular():
		return link, nil
	case err == nil:
		return "", fmt.Errorf("generation link %s is not a regular file (R-X)", link)
	case errors.Is(err, fs.ErrNotExist):
		return main, nil
	default:
		return "", fmt.Errorf("stat %s: %w", link, err)
	}
}

// hl1ReplayStack is the chain state that replay hydrates: the collaborators
// main() builds before its restore block, minus the network, API, signer and
// driver.
type hl1ReplayStack struct {
	accounts   *chain.AccountStore
	pool       *mempool.Mempool
	receipts   *chain.ReceiptStore
	v2         *v2wiring.Wired
	producer   *chain.BlockProducer
	transition *producerpolicy.Transition
}

// hl1NewReplayStack wires the state applier and producer as main() does:
// v2wiring.Wire with the same state-affecting config, AttachToProducer, the
// append receipt store, the external producer allowlist and the transition
// policy. The cmd/qsdm tests pin the v2wiring.Config keys against main().
func hl1NewReplayStack(stateDir string, governance, authorized []string, transition *producerpolicy.Transition, log *logging.Logger) (*hl1ReplayStack, error) {
	s := &hl1ReplayStack{
		accounts:   chain.NewAccountStore(),
		pool:       mempool.New(mempool.DefaultConfig()),
		receipts:   chain.NewReceiptStore(),
		transition: transition,
	}
	govStorePath := filepath.Join(stateDir, "qsdm_governance.json")
	slashReceiptsPath := filepath.Join(stateDir, "qsdm_slash_receipts.ndjson")
	v2, err := v2wiring.Wire(v2wiring.Config{
		Accounts:              s.accounts,
		Pool:                  s.pool,
		BaseAdmit:             nil,
		SlashRewardBPS:        chain.SlashRewardCap,
		GovernanceAuthorities: governance,
		LogSweepError: func(h uint64, err error) {
			log.Warn("v2 mining: enrollment sweep failed", "height", h, "error", err)
		},
		GovParamStorePath: govStorePath,
		LogSnapshotError: func(h uint64, err error) {
			log.Warn("gov param store: snapshot failed", "height", h, "path", govStorePath, "error", err)
		},
		SlashReceiptsPath: slashReceiptsPath,
		LogSlashReceiptsError: func(err error) {
			log.Warn("slash receipts: persistence error", "path", slashReceiptsPath, "error_str", err.Error())
		},
	})
	if err != nil {
		return nil, fmt.Errorf("v2 mining wiring: %w", err)
	}
	s.v2 = v2
	s.producer = chain.NewBlockProducer(s.pool, v2.StateApplier, chain.DefaultProducerConfig())
	v2.AttachToProducer(s.producer)
	s.producer.SetAppendReceiptStore(s.receipts)
	s.producer.SetAuthorizedBlockProducers(authorized)
	if err := s.producer.SetProducerTransition(transition); err != nil {
		return nil, fmt.Errorf("producer transition: %w", err)
	}
	return s, nil
}

// restore is main()'s restore block for blocks and the given snapshot pair,
// without its journal rewrites: the pair must reproduce the tip's state root
// exactly (no tail reconciliation), and the receipts (already pre-trimmed)
// must end with "\n" and load.
func (s *hl1ReplayStack) restore(blocks []*chain.Block, accountsPath, enrollmentPath, receiptsPath string) error {
	if len(blocks) == 0 {
		return errors.New("no blocks to restore")
	}
	tip := blocks[len(blocks)-1]
	restored, err := hl1EvaluatePair(s.accounts, s.v2.EnrollmentState, s.transition, blocks, accountsPath, enrollmentPath)
	if err != nil {
		return err
	}
	if err := s.producer.RestoreChain(blocks); err != nil {
		return fmt.Errorf("producer hydrate (%d blocks): %w", len(blocks), err)
	}
	if err := s.v2.TaskState.RestoreFromChainReplay(restored.taskState); err != nil {
		return fmt.Errorf("install replayed task state: %w", err)
	}
	if err := s.v2.StreamState.RestoreFromChainReplay(restored.streamState); err != nil {
		return fmt.Errorf("install replayed CELL stream state: %w", err)
	}
	if err := s.v2.RecoveryState.RestoreFromChainReplay(restored.recoveryState); err != nil {
		return fmt.Errorf("install replayed wallet recovery capsule state: %w", err)
	}
	if s.transition != nil {
		s.v2.Aware.SetStateRootHeight(transitionStateRootHeight(s.transition, tip))
	}
	if root := s.v2.StateApplier.StateRoot(); root != tip.StateRoot {
		return fmt.Errorf("reconciled state does not match block %d (snapshot_root=%s tip_root=%s)", tip.Height, root, tip.StateRoot)
	}
	if s.transition == nil {
		if _, err := s.v2.EnrollmentState.Load(enrollmentPath); err != nil {
			return fmt.Errorf("enrollment snapshot %s: %w", enrollmentPath, err)
		}
	}
	if _, err := os.Lstat(receiptsPath); err == nil {
		if err := hl1RequireFinalNewline(hl1OSFS{}, receiptsPath); err != nil {
			return fmt.Errorf("receipts: %w", err)
		}
		if _, err := s.receipts.LoadNDJSON(receiptsPath); err != nil {
			return fmt.Errorf("receipts: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("receipts: %w", err)
	}
	return nil
}

// hl1EvaluatePair loads the snapshot pair into accounts (and, under the
// producer transition, enroll), replays the task/stream/recovery state of
// blocks and requires the pair to reproduce the state root of the last block
// exactly (no tail reconciliation). Without the transition the enrollment
// snapshot is not read here: the caller loads it after the root check, as
// main() does. Used by replay (restore) and by the boot step S4r
// (hl1RepairTipReceipts).
func hl1EvaluatePair(accounts *chain.AccountStore, enroll *enrollment.InMemoryState, transition *producerpolicy.Transition, blocks []*chain.Block, accountsPath, enrollmentPath string) (persistedStateRestore, error) {
	if len(blocks) == 0 {
		return persistedStateRestore{}, errors.New("no blocks to restore")
	}
	tip := blocks[len(blocks)-1]
	if _, err := accounts.Load(accountsPath); err != nil {
		return persistedStateRestore{}, fmt.Errorf("accounts snapshot %s: %w", accountsPath, err)
	}
	var restored persistedStateRestore
	var err error
	if transition != nil {
		if _, err := loadTransitionEnrollmentSnapshot(enroll, enrollmentPath); err != nil {
			return persistedStateRestore{}, fmt.Errorf("enrollment snapshot %s: %w", enrollmentPath, err)
		}
		restored, err = evaluateTransitionPersistedState(accounts, blocks, enroll, transition)
	} else {
		restored, err = evaluatePersistedState(accounts, blocks)
	}
	if err != nil {
		return persistedStateRestore{}, err
	}
	if restored.stateRoot != tip.StateRoot {
		return persistedStateRestore{}, fmt.Errorf("the snapshot pair (%s, %s) does not reproduce block %d's state root (snapshot_root=%s block_root=%s)",
			filepath.Base(accountsPath), filepath.Base(enrollmentPath), tip.Height, restored.stateRoot, tip.StateRoot)
	}
	return restored, nil
}
