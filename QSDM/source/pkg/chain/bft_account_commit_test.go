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
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/fileutil"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

func accountCommitFixture(t *testing.T, dir string) recoveryTestChain {
	t.Helper()
	accounts := NewAccountStore()
	accounts.Credit("alice", 100)
	genesis := &Block{Height: 0, StateRoot: accounts.StateRoot()}
	genesis.Hash = ComputeBlockHash(genesis)
	j, err := OpenChainJournal(filepath.Join(dir, "chain.ndjson"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Append(genesis); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if err := accounts.Save(filepath.Join(dir, "accounts.json")); err != nil {
		t.Fatal(err)
	}
	return recoveryTestChain{genesis}
}

func accountCommitExecutor(t *testing.T, dir string, c recoveryTestChain) (*BFTExecutor, *journalTestSigner) {
	t.Helper()
	e, signer := recoveryExecutorFixture(t, dir, c)
	if err := e.ConfigureAccountCommitRecovery(c, filepath.Join(dir, "chain.ndjson"), filepath.Join(dir, "accounts.json")); err != nil {
		t.Fatal(err)
	}
	return e, signer
}

func accountCommitBlock(t *testing.T, e *BFTExecutor, signer *journalTestSigner, round uint32) *Block {
	t.Helper()
	blocks, accounts, err := e.AccountCommitSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	tip := blocks[len(blocks)-1]
	alice, _ := accounts.Get("alice")
	tx := &mempool.Tx{ID: "transfer-" + time.Now().Format("150405.000000000"), Sender: "alice", Recipient: "bob", Amount: 3, Fee: 1, Nonce: alice.Nonce}
	if err := accounts.ApplyTx(tx); err != nil {
		t.Fatal(err)
	}
	block := &Block{Height: tip.Height + 1, PrevHash: tip.Hash, StateRoot: accounts.StateRoot(), Timestamp: time.Now(), ProducerID: BFTValidatorAddress(signer.GetPublicKey()), Transactions: []*mempool.Tx{tx}, TotalFees: 1}
	block.Hash = ComputeBlockHash(block)
	if err := SignBlock(block, signer.BFTSigner); err != nil {
		t.Fatal(err)
	}
	mustRecoveryPropose(t, e, block.Height, round, block.StateRoot)
	return block
}

func accountCommitPrevote(t *testing.T, e *BFTExecutor, block *Block) {
	t.Helper()
	if err := e.PrepareAccountCommit(block); err != nil {
		t.Fatal(err)
	}
	if err := e.bc.PreVote(block.Height, block.ProducerID, block.StateRoot); err != nil {
		t.Fatal(err)
	}
}

func loadAccountCommitChain(t *testing.T, dir string) recoveryTestChain {
	t.Helper()
	blocks, err := LoadChainNDJSON(filepath.Join(dir, "chain.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	return recoveryTestChain(blocks)
}

func TestBFTAccountCommitCompletesAndReopens(t *testing.T) {
	dir := t.TempDir()
	c := accountCommitFixture(t, dir)
	e, signer := accountCommitExecutor(t, dir, c)
	for h := uint64(1); h <= 2; h++ {
		block := accountCommitBlock(t, e, signer, 0)
		accountCommitPrevote(t, e, block)
		if err := e.BroadcastPrecommit(h, 0, block.ProducerID, block.StateRoot); err != nil {
			t.Fatal(err)
		}
		if err := e.bc.PreCommit(h, block.ProducerID, block.StateRoot); err != nil {
			t.Fatal(err)
		}
		if !e.bc.IsCommitted(h) {
			t.Fatal("durable commit did not become visible")
		}
		if got := loadAccountCommitChain(t, dir); len(got) != int(h+1) || got[h].Hash != block.Hash {
			t.Fatal("wrong block persisted")
		}
	}
	blocks, accounts, err := e.AccountCommitSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	alice, _ := accounts.Get("alice")
	if alice.Nonce != 2 || alice.Balance != 92 {
		t.Fatalf("wrong account transition: %+v", alice)
	}
	blocks[2].StateRoot = "caller mutation"
	accounts.Credit("alice", 999)
	if err := e.CloseSigningJournal(); err != nil {
		t.Fatal(err)
	}
	reopened, _ := accountCommitExecutor(t, dir, loadAccountCommitChain(t, dir))
	blocks, accounts, err = reopened.AccountCommitSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	alice, _ = accounts.Get("alice")
	if len(blocks) != 3 || alice.Nonce != 2 || alice.Balance != 92 {
		t.Fatal("restart changed or reapplied committed accounts")
	}
	if reopened.bc.IsCommitted(2) {
		t.Fatal("restart fabricated consensus evidence")
	}
	if _, err := reopened.bc.BuildRoundCertificate(2); err == nil {
		t.Fatal("restored block became a quorum certificate")
	}
}

func TestBFTAccountCommitInterruptedBoundaries(t *testing.T) {
	for _, boundary := range []string{"pending", "decision", "chain", "accounts", "clear"} {
		t.Run(boundary, func(t *testing.T) {
			dir := t.TempDir()
			c := accountCommitFixture(t, dir)
			e, signer := accountCommitExecutor(t, dir, c)
			block := accountCommitBlock(t, e, signer, 0)
			accountCommitPrevote(t, e, block)
			e.bc.roundRecovery.accountCommits.boundary = func(at string) error {
				if at == boundary {
					return errors.New("interrupted " + at)
				}
				return nil
			}
			if err := e.bc.PreCommit(1, block.ProducerID, block.StateRoot); !errors.Is(err, ErrBFTRoundRecoveryUnavailable) {
				t.Fatalf("missing storage error: %v", err)
			}
			if e.bc.IsCommitted(1) {
				t.Fatal("failed persistence exposed local commit")
			}
			if _, _, err := e.AccountCommitSnapshot(); !errors.Is(err, ErrBFTRoundRecoveryUnavailable) {
				t.Fatal("failed persistence exposed account snapshot")
			}
			beforeCalls := signer.calls.Load()
			if err := e.BroadcastPrevote(1, 1, block.ProducerID, "other"); !errors.Is(err, ErrBFTRoundRecoveryUnavailable) || signer.calls.Load() != beforeCalls {
				t.Fatal("failed persistence allowed signing")
			}
			if err := e.CloseSigningJournal(); err != nil {
				t.Fatal(err)
			}
			reopened, newSigner := accountCommitExecutor(t, dir, loadAccountCommitChain(t, dir))
			blocks, accounts, err := reopened.AccountCommitSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			alice, _ := accounts.Get("alice")
			if boundary == "pending" {
				if len(blocks) != 1 || alice.Nonce != 0 || alice.Balance != 100 || reopened.bc.NextRoundAfterTimeout(1) != 1 {
					t.Fatal("undecided intent was applied or round floor lost")
				}
				block = accountCommitBlock(t, reopened, newSigner, 1)
				accountCommitPrevote(t, reopened, block)
				if err := reopened.bc.PreCommit(1, block.ProducerID, block.StateRoot); err != nil {
					t.Fatal(err)
				}
			} else if len(blocks) != 2 || blocks[1].Hash != block.Hash || alice.Nonce != 1 || alice.Balance != 96 {
				t.Fatal("committed pending transition not reconciled exactly once")
			}
			if err := reopened.CloseSigningJournal(); err != nil {
				t.Fatal(err)
			}
			again, _ := accountCommitExecutor(t, dir, loadAccountCommitChain(t, dir))
			_, accounts, err = again.AccountCommitSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			alice, _ = accounts.Get("alice")
			if alice.Nonce != 1 || alice.Balance != 96 {
				t.Fatal("second restart reapplied the transfer")
			}
		})
	}
}

func TestBFTAccountCommitAmbiguousStrictWrite(t *testing.T) {
	for _, target := range []string{"pending", "decision", "accounts", "clear"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			e, signer := accountCommitExecutor(t, dir, accountCommitFixture(t, dir))
			block := accountCommitBlock(t, e, signer, 0)
			accountCommitPrevote(t, e, block)
			j := e.bc.roundRecovery.accountCommits
			writer := func(path string, raw []byte, mode fs.FileMode) error {
				if err := fileutil.WriteFileAtomicStrict(path, raw, mode); err != nil {
					return err
				}
				var record bftAccountCommitFile
				_ = json.Unmarshal(raw, &record)
				if target == "decision" || target == "accounts" && path == j.file.AccountsPath || target == "pending" && path == j.path && record.Pending != nil || target == "clear" && path == j.path && record.Pending == nil {
					return errors.New("ambiguous write after replacement")
				}
				return nil
			}
			if target == "decision" {
				e.bc.roundRecovery.writeFile = writer
			} else {
				j.writeFile = writer
			}
			if err := e.bc.PreCommit(1, block.ProducerID, block.StateRoot); !errors.Is(err, ErrBFTRoundRecoveryUnavailable) {
				t.Fatalf("ambiguous write accepted: %v", err)
			}
			if err := e.CloseSigningJournal(); err != nil {
				t.Fatal(err)
			}
			reopened, _ := accountCommitExecutor(t, dir, loadAccountCommitChain(t, dir))
			blocks, accounts, err := reopened.AccountCommitSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			alice, _ := accounts.Get("alice")
			want := 2
			if target == "pending" {
				want = 1
			}
			if len(blocks) != want || alice.Nonce != uint64(want-1) {
				t.Fatal("ambiguous write recovered from stale memory")
			}
		})
	}
}

func TestBFTAccountCommitRefusesConflicts(t *testing.T) {
	for _, mode := range []string{"accounts", "precision", "tail", "prefix", "missing-pending", "corrupt", "binding", "plain-recovery", "locked", "undecided-tail"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			c := accountCommitFixture(t, dir)
			e, signer := accountCommitExecutor(t, dir, c)
			block := accountCommitBlock(t, e, signer, 0)
			accountCommitPrevote(t, e, block)
			stop := "decision"
			if mode == "undecided-tail" {
				stop = "pending"
			}
			j := e.bc.roundRecovery.accountCommits
			j.boundary = func(at string) error {
				if at == stop {
					return errors.New("stop")
				}
				return nil
			}
			if err := e.bc.PreCommit(1, block.ProducerID, block.StateRoot); err == nil {
				t.Fatal("missing injected error")
			}
			path := j.path
			if err := e.CloseSigningJournal(); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "accounts", "precision":
				amount := 1.0
				if mode == "precision" {
					amount = 0.00000001
				}
				accounts := NewAccountStore()
				accounts.Credit("alice", 100+amount)
				if err := accounts.Save(filepath.Join(dir, "accounts.json")); err != nil {
					t.Fatal(err)
				}
			case "tail", "undecided-tail":
				f, err := os.OpenFile(filepath.Join(dir, "chain.ndjson"), os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.WriteString("unrelated tail")
				if err != nil {
					t.Fatal(err)
				}
				_ = f.Close()
			case "prefix":
				if err := os.WriteFile(filepath.Join(dir, "chain.ndjson"), []byte("{}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing-pending":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "binding":
				j.file.AccountsPath += ".other"
				if err := j.persist(j.file.Pending); err != nil {
					t.Fatal(err)
				}
			case "locked":
				lock, err := AcquireStateLock(filepath.Join(dir, "chain.ndjson.lock"))
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
			}
			beforeChain, _ := os.ReadFile(filepath.Join(dir, "chain.ndjson"))
			beforeAccounts, _ := os.ReadFile(filepath.Join(dir, "accounts.json"))
			reopened, _ := recoveryExecutorFixture(t, dir, c)
			var err error
			if mode == "plain-recovery" {
				err = reopened.ConfigureRoundRecovery(c)
			} else {
				err = reopened.ConfigureAccountCommitRecovery(c, filepath.Join(dir, "chain.ndjson"), filepath.Join(dir, "accounts.json"))
			}
			if !errors.Is(err, ErrBFTRoundRecoveryUnavailable) {
				t.Fatalf("conflict accepted: %v", err)
			}
			afterChain, _ := os.ReadFile(filepath.Join(dir, "chain.ndjson"))
			afterAccounts, _ := os.ReadFile(filepath.Join(dir, "accounts.json"))
			if !bytes.Equal(beforeChain, afterChain) || !bytes.Equal(beforeAccounts, afterAccounts) {
				t.Fatal("failed reconciliation modified canonical data")
			}
		})
	}
}

func TestBFTAccountCommitCompletesExactTornTail(t *testing.T) {
	for _, length := range []string{"short", "no-newline", "complete"} {
		t.Run(length, func(t *testing.T) {
			dir := t.TempDir()
			c := accountCommitFixture(t, dir)
			e, signer := accountCommitExecutor(t, dir, c)
			block := accountCommitBlock(t, e, signer, 0)
			accountCommitPrevote(t, e, block)
			e.bc.roundRecovery.accountCommits.boundary = func(at string) error {
				if at == "decision" {
					return errors.New("stop")
				}
				return nil
			}
			if err := e.bc.PreCommit(1, block.ProducerID, block.StateRoot); err == nil {
				t.Fatal("missing injected error")
			}
			if err := e.CloseSigningJournal(); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(block)
			switch length {
			case "short":
				raw = raw[:len(raw)/2]
			case "complete":
				raw = append(raw, '\n')
			}
			f, err := os.OpenFile(filepath.Join(dir, "chain.ndjson"), os.O_WRONLY|os.O_APPEND, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Write(raw); err != nil {
				t.Fatal(err)
			}
			_ = f.Close()
			reopened, _ := accountCommitExecutor(t, dir, c)
			blocks, accounts, err := reopened.AccountCommitSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			alice, _ := accounts.Get("alice")
			if len(blocks) != 2 || blocks[1].Hash != block.Hash || alice.Nonce != 1 {
				t.Fatal("known append tail was not reconciled")
			}
			if len(loadAccountCommitChain(t, dir)) != 2 {
				t.Fatal("tail repair appended a duplicate block")
			}
		})
	}
}

func TestBFTAccountCommitPreparationGuards(t *testing.T) {
	dir := t.TempDir()
	e, signer := accountCommitExecutor(t, dir, accountCommitFixture(t, dir))
	block := accountCommitBlock(t, e, signer, 0)
	if err := e.bc.PreVote(1, block.ProducerID, block.StateRoot); err != nil {
		t.Fatal(err)
	}
	beforeCalls := signer.calls.Load()
	if err := e.BroadcastPrecommit(1, 0, block.ProducerID, block.StateRoot); err == nil || signer.calls.Load() != beforeCalls {
		t.Fatal("signed precommit without an exact prepared block")
	}
	if _, err := e.bc.Propose(3, 0, block.ProducerID, "future"); err == nil {
		t.Fatal("noncontiguous height opened")
	}
	if err := e.PrepareAccountCommit(block); err != nil {
		t.Fatal(err)
	}
	block.Transactions[0].Amount = 99
	if err := e.bc.PreCommit(1, block.ProducerID, block.StateRoot); err != nil {
		t.Fatal(err)
	}
	_, accounts, err := e.AccountCommitSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	alice, _ := accounts.Get("alice")
	if alice.Balance != 96 {
		t.Fatal("caller changed prepared transactions")
	}
	SetForkDustHeight(0)
	defer SetForkDustHeight(math.MaxUint64)
	if err := e.bc.RoundRecoveryError(); !errors.Is(err, ErrBFTRoundRecoveryUnavailable) {
		t.Fatal("accounting rule change left recovery enabled")
	}
}

func TestBFTAccountCommitCompletedCheckpointAndDowngrade(t *testing.T) {
	for _, mode := range []string{"precision", "missing-rounds", "missing-commit", "untracked-block", "changed-path"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			c := accountCommitFixture(t, dir)
			e, _ := accountCommitExecutor(t, dir, c)
			if err := e.CloseSigningJournal(); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "precision":
				accounts := NewAccountStore()
				accounts.Credit("alice", 100.00000001)
				if accounts.StateRoot() != c[0].StateRoot {
					t.Fatal("fixture must demonstrate legacy root rounding")
				}
				if err := accounts.Save(filepath.Join(dir, "accounts.json")); err != nil {
					t.Fatal(err)
				}
			case "missing-rounds":
				for _, name := range []string{"signing.json.rounds", "signing.json.rounds.binding"} {
					if err := os.Remove(filepath.Join(dir, name)); err != nil {
						t.Fatal(err)
					}
				}
			case "missing-commit":
				if err := os.Remove(filepath.Join(dir, "signing.json.rounds.commit")); err != nil {
					t.Fatal(err)
				}
			case "untracked-block":
				c = c.appendBlock(c[0].StateRoot)
				if err := AppendBlockToFile(filepath.Join(dir, "chain.ndjson"), c[1]); err != nil {
					t.Fatal(err)
				}
			}
			reopened, _ := recoveryExecutorFixture(t, dir, c)
			accountPath := filepath.Join(dir, "accounts.json")
			if mode == "changed-path" {
				raw, err := os.ReadFile(accountPath)
				if err != nil {
					t.Fatal(err)
				}
				accountPath += ".other"
				if err := os.WriteFile(accountPath, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := reopened.ConfigureAccountCommitRecovery(c, filepath.Join(dir, "chain.ndjson"), accountPath); !errors.Is(err, ErrBFTRoundRecoveryUnavailable) {
				t.Fatalf("incomplete or changed checkpoint accepted: %v", err)
			}
		})
	}
}

func TestBFTAccountCommitRejectsUnsupportedBlock(t *testing.T) {
	for _, mode := range []string{"unsigned", "nil-tx", "payload", "contract", "negative", "nonce-overflow", "bad-hash", "wrong-fees", "bad-signature"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			e, signer := accountCommitExecutor(t, dir, accountCommitFixture(t, dir))
			block := accountCommitBlock(t, e, signer, 0)
			switch mode {
			case "unsigned":
				block.ProducerAuth = BFTWireAuth{}
			case "nil-tx":
				block.Transactions[0] = nil
			case "payload":
				block.Transactions[0].Payload = []byte("task action")
			case "contract":
				block.Transactions[0].ContractID = "enrollment"
			case "negative":
				block.Transactions[0].Amount = -1
			case "nonce-overflow":
				block.Transactions[0].Nonce = math.MaxUint64
			case "bad-hash":
				block.Hash = "not canonical"
			case "wrong-fees":
				block.TotalFees++
			case "bad-signature":
				block.ProducerAuth.Signature = []byte("invalid")
			}
			if err := e.PrepareAccountCommit(block); err == nil {
				t.Fatal("unsupported block was prepared")
			}
			if len(loadAccountCommitChain(t, dir)) != 1 {
				t.Fatal("rejected block was written")
			}
		})
	}
}

func TestBFTAccountCommitProcessCrash(t *testing.T) {
	for _, at := range []string{"pending", "decision", "chain", "accounts", "clear"} {
		t.Run(at, func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestBFTAccountCommitCrashHelper$", "-test.timeout=1m")
			cmd.Env = append(os.Environ(), "QSDM_ACCOUNT_COMMIT_CRASH_DIR="+dir, "QSDM_ACCOUNT_COMMIT_CRASH_AT="+at)
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 23 {
				t.Fatalf("crash helper: %v\n%s", err, out)
			}
			e, _ := accountCommitExecutor(t, dir, loadAccountCommitChain(t, dir))
			blocks, accounts, err := e.AccountCommitSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			want := 2
			if at == "pending" {
				want = 1
			}
			alice, _ := accounts.Get("alice")
			if len(blocks) != want || alice.Nonce != uint64(want-1) {
				t.Fatal("OS crash did not recover exact committed state")
			}
		})
	}
}

func TestBFTAccountCommitSerializesTimeoutAndSigning(t *testing.T) {
	dir := t.TempDir()
	e, signer := accountCommitExecutor(t, dir, accountCommitFixture(t, dir))
	block := accountCommitBlock(t, e, signer, 0)
	accountCommitPrevote(t, e, block)
	entered, release := make(chan struct{}), make(chan struct{})
	e.bc.roundRecovery.accountCommits.boundary = func(at string) error {
		if at == "decision" {
			close(entered)
			<-release
		}
		return nil
	}
	commitDone := make(chan error, 1)
	go func() { commitDone <- e.bc.PreCommit(1, block.ProducerID, block.StateRoot) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("commit did not reach persistence boundary")
	}
	beforeCalls := signer.calls.Load()
	signingDone := make(chan error, 1)
	timeoutDone := make(chan []uint64, 1)
	go func() { signingDone <- e.BroadcastPrevote(1, 1, block.ProducerID, block.StateRoot) }()
	go func() { timeoutDone <- e.bc.TickRoundTimeouts(time.Now().Add(time.Hour)) }()
	select {
	case <-signingDone:
		close(release)
		t.Fatal("signing ran through an unfinished commit")
	case <-timeoutDone:
		close(release)
		t.Fatal("timeout ran through an unfinished commit")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-commitDone; err != nil {
		t.Fatal(err)
	}
	if err := <-signingDone; !errors.Is(err, ErrBFTRoundRetired) || signer.calls.Load() != beforeCalls {
		t.Fatalf("completed height allowed signing: %v", err)
	}
	if heights := <-timeoutDone; len(heights) != 0 {
		t.Fatalf("durable commit was retired by timeout: %v", heights)
	}
}

func TestBFTAccountCommitCrashHelper(t *testing.T) {
	dir := os.Getenv("QSDM_ACCOUNT_COMMIT_CRASH_DIR")
	if dir == "" {
		t.Skip("subprocess helper")
	}
	e, signer := accountCommitExecutor(t, dir, accountCommitFixture(t, dir))
	block := accountCommitBlock(t, e, signer, 0)
	accountCommitPrevote(t, e, block)
	e.bc.roundRecovery.accountCommits.boundary = func(at string) error {
		if at == os.Getenv("QSDM_ACCOUNT_COMMIT_CRASH_AT") {
			os.Exit(23)
		}
		return nil
	}
	if err := e.bc.PreCommit(1, block.ProducerID, block.StateRoot); err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash boundary was not reached")
}
