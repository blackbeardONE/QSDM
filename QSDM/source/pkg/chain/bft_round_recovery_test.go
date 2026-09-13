package chain

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/fileutil"
)

type recoveryTestChain []*Block

func (c recoveryTestChain) GetBlock(height uint64) (*Block, bool) {
	for _, b := range c {
		if b.Height == height {
			return b, true
		}
	}
	return nil, false
}

func (c recoveryTestChain) LatestBlock() (*Block, bool) {
	if len(c) == 0 {
		return nil, false
	}
	return c[len(c)-1], true
}

func recoveryChainFixture() recoveryTestChain {
	genesis := &Block{Height: 0, StateRoot: "genesis-state"}
	genesis.Hash = ComputeBlockHash(genesis)
	return recoveryTestChain{genesis}
}

func (c recoveryTestChain) appendBlock(value string) recoveryTestChain {
	previous := c[len(c)-1]
	b := &Block{Height: previous.Height + 1, PrevHash: previous.Hash, StateRoot: value}
	b.Hash = ComputeBlockHash(b)
	return append(c, b)
}

func recoveryExecutorFixture(t *testing.T, dir string, c recoveryTestChain) (*BFTExecutor, *journalTestSigner) {
	t.Helper()
	e, signer, binding := journalExecutorFixture(t, dir)
	binding.ChainID = c[0].Hash
	if err := e.ConfigureSigningJournal(filepath.Join(dir, "signing.json"), binding); err != nil {
		t.Fatal(err)
	}
	return e, signer
}

func mustConfigureRecovery(t *testing.T, e *BFTExecutor, c recoveryTestChain) {
	t.Helper()
	if err := e.ConfigureRoundRecovery(c); err != nil {
		t.Fatal(err)
	}
}

func mustRecoveryPropose(t *testing.T, e *BFTExecutor, height uint64, round uint32, value string) {
	t.Helper()
	proposer, err := e.bc.ProposerForRound(round)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.bc.Propose(height, round, proposer, value); err != nil {
		t.Fatal(err)
	}
}

func readRecoveryState(t *testing.T, path string) bftRoundRecoveryFile {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored bftRoundRecoveryFile
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if err := stored.validate(stored.Binding, stored.RulesFingerprint); err != nil {
		t.Fatal(err)
	}
	return stored
}

func TestBFTRoundRecoveryRetainsGuardsWithoutReplayingVotes(t *testing.T) {
	for _, retire := range []string{"crash-active", "fail", "timeout"} {
		t.Run(retire, func(t *testing.T) {
			dir, c := t.TempDir(), recoveryChainFixture()
			e, signer := recoveryExecutorFixture(t, dir, c)
			mustConfigureRecovery(t, e, c)
			address := BFTValidatorAddress(signer.GetPublicKey())
			mustRecoveryPropose(t, e, 1, 0, "locked")
			if err := e.BroadcastPropose(1, 0, address, "locked", nil); err != nil {
				t.Fatal(err)
			}
			if err := e.bc.PreVote(1, address, "locked"); err != nil {
				t.Fatal(err)
			}
			if err := e.BroadcastPrecommit(1, 0, address, "locked"); err != nil {
				t.Fatal(err)
			}
			switch retire {
			case "fail":
				if err := e.bc.FailRound(1); err != nil {
					t.Fatal(err)
				}
			case "timeout":
				if got := e.bc.TickRoundTimeouts(time.Now().Add(time.Hour)); len(got) != 1 {
					t.Fatalf("timeouts: %v", got)
				}
			}
			if err := e.CloseSigningJournal(); err != nil {
				t.Fatal(err)
			}
			reopened, reopenedSigner := recoveryExecutorFixture(t, dir, c)
			mustConfigureRecovery(t, reopened, c)
			if got := reopened.bc.NextRoundAfterTimeout(1); got != 1 {
				t.Fatalf("floor=%d", got)
			}
			if _, err := reopened.bc.Propose(1, 0, address, "other"); !errors.Is(err, ErrBFTRoundRetired) {
				t.Fatalf("stale propose: %v", err)
			}
			if err := reopened.BroadcastPropose(1, 0, address, "locked", nil); !errors.Is(err, ErrBFTRoundRetired) {
				t.Fatalf("stale saved envelope: %v", err)
			}
			mustRecoveryPropose(t, reopened, 1, 1, "locked")
			cr, _ := reopened.bc.GetRound(1)
			if cr.LockedBlockHash != "locked" || len(cr.PreVotes)+len(cr.Commits) != 0 || cr.Status != StatusProposed {
				t.Fatalf("restored fake votes or lost lock: %+v", cr)
			}
			if _, err := reopened.bc.BuildPrevoteLockProof(1); err == nil {
				t.Fatal("local recovered lock became a portable proof")
			}
			if _, err := reopened.bc.BuildRoundCertificate(1); err == nil {
				t.Fatal("local recovery fabricated a certificate")
			}
			if err := reopened.BroadcastPrevote(1, 1, address, "other"); !errors.Is(err, ErrBFTSigningJournalConflict) {
				t.Fatalf("conflicting locked vote: %v", err)
			}
			if err := reopened.BroadcastPrecommit(1, 1, address, "locked"); err == nil {
				t.Fatal("precommit without fresh quorum")
			}
			if reopenedSigner.calls.Load() != 0 {
				t.Fatal("unsafe request reached signer")
			}
			if err := reopened.BroadcastPrevote(1, 1, address, "locked"); err != nil {
				t.Fatal(err)
			}
			if err := reopened.bc.PreVote(1, address, "locked"); err != nil {
				t.Fatal(err)
			}
			if err := reopened.BroadcastPrecommit(1, 1, address, "locked"); err != nil {
				t.Fatal(err)
			}
			if err := reopened.bc.PreCommit(1, address, "locked"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBFTRoundRecoveryReconcilesCommitAndCheckpoint(t *testing.T) {
	dir, c := t.TempDir(), recoveryChainFixture()
	e, signer := recoveryExecutorFixture(t, dir, c)
	mustConfigureRecovery(t, e, c)
	address := BFTValidatorAddress(signer.GetPublicKey())
	mustRecoveryPropose(t, e, 1, 0, "committed")
	if err := e.bc.PreVote(1, address, "committed"); err != nil {
		t.Fatal(err)
	}
	if err := e.bc.PreCommit(1, address, "committed"); err != nil {
		t.Fatal(err)
	}
	if err := e.CloseSigningJournal(); err != nil {
		t.Fatal(err)
	}
	for _, badChain := range []recoveryTestChain{c, c.appendBlock("conflict")} {
		reopened, _ := recoveryExecutorFixture(t, dir, badChain)
		if err := reopened.ConfigureRoundRecovery(badChain); !errors.Is(err, ErrBFTRoundRecoveryChainMismatch) {
			t.Fatalf("missing/conflicting local commit: %v", err)
		}
		if err := reopened.bc.PreVote(2, address, "other"); !errors.Is(err, ErrBFTRoundRecoveryUnavailable) {
			t.Fatalf("failure did not latch: %v", err)
		}
		if err := reopened.CloseSigningJournal(); err != nil {
			t.Fatal(err)
		}
	}
	c = c.appendBlock("committed")
	reopened, _ := recoveryExecutorFixture(t, dir, c)
	mustConfigureRecovery(t, reopened, c)
	if _, err := reopened.bc.Propose(1, 9, address, "other"); !errors.Is(err, ErrBFTRoundRetired) {
		t.Fatalf("finalized height reopened: %v", err)
	}
	if reopened.bc.IsCommitted(1) {
		t.Fatal("recovery invented an in-memory quorum")
	}
	if _, err := reopened.bc.BuildRoundCertificate(1); err == nil {
		t.Fatal("recovery invented a certificate")
	}
	mustRecoveryPropose(t, reopened, 2, 0, "next")
	if err := reopened.CloseSigningJournal(); err != nil {
		t.Fatal(err)
	}
	rolledBack, _ := recoveryExecutorFixture(t, dir, c[:1])
	if err := rolledBack.ConfigureRoundRecovery(c[:1]); !errors.Is(err, ErrBFTRoundRecoveryChainMismatch) {
		t.Fatalf("checkpoint rollback: %v", err)
	}
}

func TestBFTRoundRecoveryWriteFailureIsAtomicAndSticky(t *testing.T) {
	for _, stage := range []string{"propose", "prevote", "precommit", "timeout", "fail"} {
		t.Run(stage, func(t *testing.T) {
			dir, c := t.TempDir(), recoveryChainFixture()
			e, signer := recoveryExecutorFixture(t, dir, c)
			mustConfigureRecovery(t, e, c)
			address := BFTValidatorAddress(signer.GetPublicKey())
			if stage != "propose" {
				mustRecoveryPropose(t, e, 1, 0, "value")
			}
			if stage == "precommit" {
				if err := e.bc.PreVote(1, address, "value"); err != nil {
					t.Fatal(err)
				}
			}
			r := e.bc.roundRecovery
			originalPath := r.path
			r.path = filepath.Join(dir, "missing", "state.json")
			var err error
			switch stage {
			case "propose":
				_, err = e.bc.Propose(1, 0, address, "value")
			case "prevote":
				err = e.bc.PreVote(1, address, "value")
			case "precommit":
				err = e.bc.PreCommit(1, address, "value")
			case "fail":
				err = e.bc.FailRound(1)
			case "timeout":
				if got := e.bc.TickRoundTimeouts(time.Now().Add(time.Hour)); len(got) != 0 {
					t.Fatalf("failed timeout was announced: %v", got)
				}
				err = e.bc.RoundRecoveryError()
			}
			if !errors.Is(err, ErrBFTRoundRecoveryUnavailable) {
				t.Fatalf("expected sticky storage failure: %v", err)
			}
			cr, ok := e.bc.GetRound(1)
			if stage == "propose" && ok {
				t.Fatal("failed proposal became visible")
			}
			if stage != "propose" && (!ok || cr.Status == StatusFailed || len(cr.Commits) != 0) {
				t.Fatalf("failed transition became visible: %+v", cr)
			}
			if stage == "prevote" && (cr.LockedBlockHash != "" || len(cr.PreVotes) != 0) {
				t.Fatal("failed prevote mutated state")
			}
			if e.bc.IsCommitted(1) {
				t.Fatal("failed persistence committed locally")
			}
			r.path = originalPath
			if err := e.BroadcastPrevote(1, 0, address, "value"); !errors.Is(err, ErrBFTRoundRecoveryUnavailable) {
				t.Fatalf("storage recovery unlatched signing: %v", err)
			}
			if signer.calls.Load() != 0 {
				t.Fatal("storage failure reached signer")
			}
			if _, err := e.bc.BuildPrevoteLockProof(1); !errors.Is(err, ErrBFTRoundRecoveryUnavailable) {
				t.Fatalf("failed engine emitted proof: %v", err)
			}
		})
	}
}

func TestBFTRoundRecoveryPersistencePrecedesSigning(t *testing.T) {
	dir, c := t.TempDir(), recoveryChainFixture()
	e, signer := recoveryExecutorFixture(t, dir, c)
	mustConfigureRecovery(t, e, c)
	address := BFTValidatorAddress(signer.GetPublicKey())
	mustRecoveryPropose(t, e, 1, 0, "value")
	signer.before = func([]byte) {
		stored := readRecoveryState(t, e.bc.roundRecovery.path)
		if len(stored.Guards) != 1 || stored.Guards[0].NextRound != 1 {
			t.Fatal("signature preceded durable crash floor")
		}
	}
	if err := e.BroadcastPrevote(1, 0, address, "value"); err != nil {
		t.Fatal(err)
	}
	if err := e.bc.PreVote(1, address, "value"); err != nil {
		t.Fatal(err)
	}
	signer.before = func([]byte) {
		stored := readRecoveryState(t, e.bc.roundRecovery.path)
		if stored.Guards[0].LockHash != "value" {
			t.Fatal("precommit signature preceded durable lock")
		}
	}
	if err := e.BroadcastPrecommit(1, 0, address, "value"); err != nil {
		t.Fatal(err)
	}
	if err := e.bc.PreCommit(1, address, "value"); err != nil {
		t.Fatal(err)
	}
	stored := readRecoveryState(t, e.bc.roundRecovery.path)
	if stored.Guards[0].CommittedValue != "value" {
		t.Fatal("commit not durable on return")
	}
}

func TestBFTRoundRecoveryRefusesIncompleteOrInvalidPair(t *testing.T) {
	for _, kind := range []string{"missing", "corrupt", "oversized", "changed-rules", "changed-validator", "journal-ahead", "lock-held"} {
		t.Run(kind, func(t *testing.T) {
			dir, c := t.TempDir(), recoveryChainFixture()
			e, _ := recoveryExecutorFixture(t, dir, c)
			mustConfigureRecovery(t, e, c)
			path := e.bc.roundRecovery.path
			if err := e.CloseSigningJournal(); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.WriteFile(path, []byte(strings.Repeat("x", maxBFTRoundRecoveryBytes+1)), 0o600); err != nil {
					t.Fatal(err)
				}
			case "lock-held":
				lock, err := AcquireStateLock(path + ".lock")
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
			}
			reopened, signer := recoveryExecutorFixture(t, dir, c)
			switch kind {
			case "changed-rules":
				reopened.bc.cfg.MaxRounds++
			case "changed-validator":
				if err := reopened.bc.validators.Register("outsider", 100); err != nil {
					t.Fatal(err)
				}
			case "journal-ahead":
				intent := BFTSigningIntentForPrevote(BFTWirePrevoteMsg{Height: 1, Round: 7, Validator: BFTValidatorAddress(signer.GetPublicKey()), BlockHash: "value"})
				if _, _, err := reopened.signingJournal.Reserve(intent); err != nil {
					t.Fatal(err)
				}
			}
			if err := reopened.ConfigureRoundRecovery(c); !errors.Is(err, ErrBFTRoundRecoveryUnavailable) {
				t.Fatalf("invalid pair accepted: %v", err)
			}
			if err := reopened.ConfigureRoundRecovery(c); !errors.Is(err, ErrBFTRoundRecoveryUnavailable) {
				t.Fatalf("configuration failure not sticky: %v", err)
			}
			if err := reopened.BroadcastPrevote(1, 0, BFTValidatorAddress(signer.GetPublicKey()), "value"); !errors.Is(err, ErrBFTRoundRecoveryUnavailable) {
				t.Fatalf("invalid pair reached signing: %v", err)
			}
		})
	}
}

func TestBFTRoundRecoveryRetainsExhaustionAndNilLock(t *testing.T) {
	for _, value := range []string{"value", NilVoteHash} {
		t.Run(value, func(t *testing.T) {
			dir, c := t.TempDir(), recoveryChainFixture()
			e, signer := recoveryExecutorFixture(t, dir, c)
			mustConfigureRecovery(t, e, c)
			address := BFTValidatorAddress(signer.GetPublicKey())
			mustRecoveryPropose(t, e, 1, 0, "value")
			if err := e.bc.PreVote(1, address, value); err != nil {
				t.Fatal(err)
			}
			mustRecoveryPropose(t, e, 2, math.MaxUint32, "value")
			if err := e.bc.FailRound(2); err != nil {
				t.Fatal(err)
			}
			if err := e.CloseSigningJournal(); err != nil {
				t.Fatal(err)
			}
			reopened, _ := recoveryExecutorFixture(t, dir, c)
			mustConfigureRecovery(t, reopened, c)
			for _, round := range []uint32{0, math.MaxUint32} {
				if _, err := reopened.bc.Propose(2, round, address, "value"); !errors.Is(err, ErrBFTRoundRetired) {
					t.Fatalf("exhaustion lost: %v", err)
				}
			}
			mustRecoveryPropose(t, reopened, 1, 1, "next-value")
			cr, _ := reopened.bc.GetRound(1)
			if cr.LockedBlockHash != value {
				t.Fatalf("lock=%q want=%q", cr.LockedBlockHash, value)
			}
			err := reopened.BroadcastPrevote(1, 1, address, "next-value")
			if value == NilVoteHash && err != nil {
				t.Fatal(err)
			}
			if value != NilVoteHash && !errors.Is(err, ErrBFTSigningJournalConflict) {
				t.Fatalf("lost concrete lock: %v", err)
			}
		})
	}
}

func TestBFTRoundRecoverySnapshotsDoNotMutateSafetyState(t *testing.T) {
	dir, c := t.TempDir(), recoveryChainFixture()
	e, signer := recoveryExecutorFixture(t, dir, c)
	mustConfigureRecovery(t, e, c)
	address := BFTValidatorAddress(signer.GetPublicKey())
	cr, err := e.bc.Propose(1, 0, address, "value")
	if err != nil {
		t.Fatal(err)
	}
	cr.Round, cr.BlockHash = 99, "other"
	if err := e.bc.PreVote(1, address, "value"); err != nil {
		t.Fatal(err)
	}
	cr, _ = e.bc.GetRound(1)
	cr.LockedBlockHash, cr.PreVotes[0].BlockHash = "other", "other"
	if err := e.bc.PreCommit(1, address, "value"); err != nil {
		t.Fatal(err)
	}
	cr, _ = e.bc.GetCommitted(1)
	cr.BlockHash, cr.Commits[0].BlockHash = "other", "other"
	cr, _ = e.bc.GetCommitted(1)
	if cr.BlockHash != "value" || cr.Commits[0].BlockHash != "value" {
		t.Fatal("snapshot changed committed state")
	}
	// The fixed-set mode owns a snapshot, not the caller's mutable voter weights.
	if err := e.bc.validators.Register("new-validator", 10000); err != nil {
		t.Fatal(err)
	}
	mustRecoveryPropose(t, e, 2, 0, "next")
	if err := e.bc.PreVote(2, "new-validator", "next"); err == nil {
		t.Fatal("external validator change bypassed fixed rules")
	}
}

func TestBFTRoundRecoveryFixedSetRequiresFreshQuorum(t *testing.T) {
	dir, c := t.TempDir(), recoveryChainFixture()
	open := func() (*BFTExecutor, string) {
		t.Helper()
		e, signer := recoveryExecutorFixture(t, dir, c)
		for _, peer := range []string{"peer-1", "peer-2", "peer-3"} {
			if err := e.bc.validators.Register(peer, 100); err != nil {
				t.Fatal(err)
			}
		}
		mustConfigureRecovery(t, e, c)
		return e, BFTValidatorAddress(signer.GetPublicKey())
	}
	e, address := open()
	mustRecoveryPropose(t, e, 1, 0, "value")
	for _, voter := range []string{address, "peer-1", "peer-2"} {
		if err := e.bc.PreVote(1, voter, "value"); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.CloseSigningJournal(); err != nil {
		t.Fatal(err)
	}
	e, address = open()
	mustRecoveryPropose(t, e, 1, 1, "value")
	if err := e.bc.PreVote(1, address, "value"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.bc.BuildPrevoteLockProof(1); err == nil {
		t.Fatal("recovered lock counted as missing validators' votes")
	}
	if err := e.BroadcastPrecommit(1, 1, address, "value"); err == nil {
		t.Fatal("single fresh vote authorized precommit in four-voter set")
	}
	for _, voter := range []string{"peer-1", "peer-2"} {
		if err := e.bc.PreVote(1, voter, "value"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.bc.BuildPrevoteLockProof(1); err != nil {
		t.Fatal(err)
	}
	if err := e.BroadcastPrecommit(1, 1, address, "value"); err != nil {
		t.Fatal(err)
	}
}

func TestBFTRoundRecoveryTimeoutCannotRaceSigning(t *testing.T) {
	dir, c := t.TempDir(), recoveryChainFixture()
	e, signer := recoveryExecutorFixture(t, dir, c)
	mustConfigureRecovery(t, e, c)
	address := BFTValidatorAddress(signer.GetPublicKey())
	mustRecoveryPropose(t, e, 1, 0, "value")
	entered, release := make(chan struct{}), make(chan struct{})
	signer.before = func([]byte) { close(entered); <-release }
	result := make(chan error, 1)
	go func() { result <- e.BroadcastPrevote(1, 0, address, "value") }()
	<-entered
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); e.bc.TickRoundTimeouts(time.Now().Add(time.Hour)) }()
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if err := e.BroadcastPrevote(1, 0, address, "value"); !errors.Is(err, ErrBFTRoundRetired) {
		t.Fatalf("timeout did not block subsequent retry: %v", err)
	}
	if signer.calls.Load() != 1 {
		t.Fatal("retired vote was signed again")
	}
}

func TestBFTRoundRecoveryCrashRestart(t *testing.T) {
	for _, stage := range []string{"propose", "lock", "lock-write", "retire", "commit", "commit-write"} {
		t.Run(stage, func(t *testing.T) {
			dir, c := t.TempDir(), recoveryChainFixture()
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(binary, "-test.run=^TestBFTRoundRecoveryCrashHelper$")
			cmd.Env = append(os.Environ(), "QSDM_ROUND_CRASH_DIR="+dir, "QSDM_ROUND_CRASH_STAGE="+stage)
			output, err := cmd.CombinedOutput()
			var exited *exec.ExitError
			if !errors.As(err, &exited) || exited.ExitCode() != 23 {
				t.Fatalf("crash helper: %v %s", err, output)
			}
			e, _ := recoveryExecutorFixture(t, dir, c)
			err = e.ConfigureRoundRecovery(c)
			if strings.HasPrefix(stage, "commit") {
				if !errors.Is(err, ErrBFTRoundRecoveryChainMismatch) {
					t.Fatalf("unsealed commit accepted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := e.bc.NextRoundAfterTimeout(1); got != 1 {
				t.Fatalf("crash floor=%d", got)
			}
			mustRecoveryPropose(t, e, 1, 1, "value")
			cr, _ := e.bc.GetRound(1)
			if stage != "propose" && cr.LockedBlockHash != "value" {
				t.Fatal("crash lost prevote lock")
			}
			if len(cr.PreVotes)+len(cr.Commits) != 0 {
				t.Fatal("crash recovery replayed unauthenticated votes")
			}
		})
	}
}

func TestBFTRoundRecoveryCrashHelper(t *testing.T) {
	dir := os.Getenv("QSDM_ROUND_CRASH_DIR")
	if dir == "" {
		t.Skip("subprocess only")
	}
	c := recoveryChainFixture()
	e, signer := recoveryExecutorFixture(t, dir, c)
	mustConfigureRecovery(t, e, c)
	address := BFTValidatorAddress(signer.GetPublicKey())
	stage := os.Getenv("QSDM_ROUND_CRASH_STAGE")
	mustRecoveryPropose(t, e, 1, 0, "value")
	if stage == "propose" {
		os.Exit(23)
	}
	crashAfterWrite := func(path string, data []byte, mode fs.FileMode) error {
		if err := fileutil.WriteFileAtomicStrict(path, data, mode); err != nil {
			return err
		}
		os.Exit(23)
		return nil
	}
	if stage == "lock-write" {
		e.bc.roundRecovery.writeFile = crashAfterWrite
	}
	if err := e.bc.PreVote(1, address, "value"); err != nil {
		t.Fatal(err)
	}
	if stage == "lock" {
		os.Exit(23)
	}
	if stage == "retire" {
		if err := e.bc.FailRound(1); err != nil {
			t.Fatal(err)
		}
		os.Exit(23)
	}
	if stage == "commit-write" {
		e.bc.roundRecovery.writeFile = crashAfterWrite
	}
	if err := e.bc.PreCommit(1, address, "value"); err != nil {
		t.Fatal(err)
	}
	os.Exit(23)
}

func TestBFTRoundRecoveryAmbiguousWriteMustReopenDisk(t *testing.T) {
	dir, c := t.TempDir(), recoveryChainFixture()
	e, signer := recoveryExecutorFixture(t, dir, c)
	mustConfigureRecovery(t, e, c)
	address := BFTValidatorAddress(signer.GetPublicKey())
	mustRecoveryPropose(t, e, 1, 0, "value")
	e.bc.roundRecovery.writeFile = func(path string, data []byte, mode fs.FileMode) error {
		if err := fileutil.WriteFileAtomicStrict(path, data, mode); err != nil {
			return err
		}
		return errors.New("simulated error after durable replacement")
	}
	if err := e.bc.PreVote(1, address, "value"); !errors.Is(err, ErrBFTRoundRecoveryUnavailable) {
		t.Fatalf("ambiguous write: %v", err)
	}
	cr, _ := e.bc.GetRound(1)
	if cr.LockedBlockHash != "" || len(cr.PreVotes) != 0 {
		t.Fatal("failed write exposed candidate in memory")
	}
	if err := e.bc.FailRound(1); !errors.Is(err, ErrBFTRoundRecoveryUnavailable) {
		t.Fatalf("stale in-memory state could overwrite disk: %v", err)
	}
	if err := e.CloseSigningJournal(); err != nil {
		t.Fatal(err)
	}
	reopened, _ := recoveryExecutorFixture(t, dir, c)
	mustConfigureRecovery(t, reopened, c)
	mustRecoveryPropose(t, reopened, 1, 1, "value")
	cr, _ = reopened.bc.GetRound(1)
	if cr.LockedBlockHash != "value" {
		t.Fatal("restart did not recover ambiguous durable lock")
	}
}

func TestBFTRoundRecoveryCapacityPreservesOldFile(t *testing.T) {
	dir, c := t.TempDir(), recoveryChainFixture()
	e, _ := recoveryExecutorFixture(t, dir, c)
	mustConfigureRecovery(t, e, c)
	r := e.bc.roundRecovery
	guards := make(map[uint64]bftRecoveryGuard)
	for height := uint64(1); height <= maxBFTRoundRecoveryRecords; height++ {
		guards[height] = bftRecoveryGuard{Height: height, NextRound: 1}
	}
	if err := r.persist(guards); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(r.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.bc.Propose(maxBFTRoundRecoveryRecords+1, 0, r.validators[0].Address, "value"); !errors.Is(err, ErrBFTRoundRecoveryFull) {
		t.Fatalf("record capacity: %v", err)
	}
	after, err := os.ReadFile(r.path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("capacity failure replaced journal: %v", err)
	}
	for height, guard := range guards {
		guard.LockHash = strings.Repeat("x", bftSigningJournalStringMaxLen)
		guard.CommittedValue = guard.LockHash
		guards[height] = guard
	}
	if err := r.persist(guards); !errors.Is(err, ErrBFTRoundRecoveryFull) {
		t.Fatalf("byte capacity: %v", err)
	}
	after, err = os.ReadFile(r.path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("byte capacity replaced journal: %v", err)
	}
}

func TestBFTRoundRecoveryConfigurationAndCloseAreFailClosed(t *testing.T) {
	for _, mode := range []string{"no-signing-journal", "late-signing", "late-consensus", "missing-chain", "wrong-genesis", "close", "double-configure"} {
		t.Run(mode, func(t *testing.T) {
			dir, c := t.TempDir(), recoveryChainFixture()
			var e *BFTExecutor
			var signer *journalTestSigner
			if mode == "no-signing-journal" {
				e, signer, _ = journalExecutorFixture(t, dir)
			} else {
				e, signer = recoveryExecutorFixture(t, dir, c)
			}
			address := BFTValidatorAddress(signer.GetPublicKey())
			switch mode {
			case "late-signing":
				if err := e.BroadcastPrevote(1, 0, address, "value"); err != nil {
					t.Fatal(err)
				}
			case "late-consensus":
				mustRecoveryPropose(t, e, 1, 0, "value")
			case "missing-chain":
				c = nil
			case "wrong-genesis":
				c[0].StateRoot = "other-genesis"
				c[0].Hash = ComputeBlockHash(c[0])
			case "close", "double-configure":
				mustConfigureRecovery(t, e, c)
				if mode == "close" {
					if err := e.CloseRoundRecovery(); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := e.ConfigureRoundRecovery(c); !errors.Is(err, ErrBFTRoundRecoveryUnavailable) {
				t.Fatalf("unsafe configuration: %v", err)
			}
			if _, err := e.bc.Propose(2, 0, address, "value"); !errors.Is(err, ErrBFTRoundRecoveryUnavailable) {
				t.Fatalf("consensus gate: %v", err)
			}
			if err := e.BroadcastPrevote(2, 0, address, "value"); !errors.Is(err, ErrBFTRoundRecoveryUnavailable) {
				t.Fatalf("signing gate: %v", err)
			}
		})
	}
}

func TestBFTRoundRecoveryDoesNotEnableUnreviewedGossipIngress(t *testing.T) {
	dir, c := t.TempDir(), recoveryChainFixture()
	e, signer := recoveryExecutorFixture(t, dir, c)
	mustConfigureRecovery(t, e, c)
	address := BFTValidatorAddress(signer.GetPublicKey())
	mustRecoveryPropose(t, e, 1, 1, "value")
	for _, round := range []uint32{0, 1} {
		msg := BFTWirePrevoteMsg{Height: 1, Round: round, Validator: address, BlockHash: "value"}
		if err := SignPrevote(&msg, signer.BFTSigner); err != nil {
			t.Fatal(err)
		}
		payload, err := MarshalBFTWire(BFTWirePrevote, msg)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.ApplyInbound(payload); !errors.Is(err, ErrBFTRoundRecoveryUnavailable) {
			t.Fatalf("unreviewed ingress accepted round %d: %v", round, err)
		}
	}
	cr, _ := e.bc.GetRound(1)
	if len(cr.PreVotes) != 0 || cr.LockedBlockHash != "" {
		t.Fatal("gossip bypassed recovery activation gate")
	}
}

func TestBFTRoundRecoveryCannotDowngradeToSigningOnly(t *testing.T) {
	for _, missing := range []bool{false, true} {
		name := "intact"
		if missing {
			name = "missing-guards"
		}
		t.Run(name, func(t *testing.T) {
			dir, c := t.TempDir(), recoveryChainFixture()
			e, signer := recoveryExecutorFixture(t, dir, c)
			mustConfigureRecovery(t, e, c)
			address := BFTValidatorAddress(signer.GetPublicKey())
			mustRecoveryPropose(t, e, 1, 0, "value")
			if err := e.BroadcastPrevote(1, 0, address, "value"); err != nil {
				t.Fatal(err)
			}
			path := e.bc.roundRecovery.path
			if err := e.CloseSigningJournal(); err != nil {
				t.Fatal(err)
			}
			if missing {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			reopened, newSigner := recoveryExecutorFixture(t, dir, c)
			if err := reopened.bc.RoundRecoveryError(); !errors.Is(err, ErrBFTRoundRecoveryUnavailable) {
				t.Fatalf("existing recovery was not required: %v", err)
			}
			if _, err := reopened.bc.Propose(1, 0, address, "other"); !errors.Is(err, ErrBFTRoundRecoveryUnavailable) {
				t.Fatalf("omitted recovery opened consensus: %v", err)
			}
			if err := reopened.BroadcastPrevote(1, 1, address, "other"); !errors.Is(err, ErrBFTRoundRecoveryUnavailable) {
				t.Fatalf("omitted recovery opened signing: %v", err)
			}
			if newSigner.calls.Load() != 0 {
				t.Fatal("downgraded executor invoked signer")
			}
			if missing {
				if err := reopened.ConfigureRoundRecovery(c); !errors.Is(err, ErrBFTRoundRecoveryCorrupt) {
					t.Fatalf("missing guards reopened: %v", err)
				}
			} else {
				mustConfigureRecovery(t, reopened, c)
				mustRecoveryPropose(t, reopened, 1, 1, "value")
			}
		})
	}
}
