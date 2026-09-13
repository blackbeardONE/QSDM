package chain

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/fileutil"
)

const maxBFTAccountCommitBytes = 16 << 20

type bftAccountCommitPaths struct{ chain, accounts string }

type bftAccountCommitPending struct {
	Round       uint32    `json:"round"`
	ChainOffset int64     `json:"chain_offset"`
	Block       *Block    `json:"block"`
	Before      []Account `json:"before"`
	After       []Account `json:"after"`
}

type bftAccountCommitFile struct {
	Version          int                      `json:"version"`
	Binding          BFTSigningJournalBinding `json:"binding"`
	RulesFingerprint string                   `json:"rules_fingerprint"`
	ChainPath        string                   `json:"chain_path"`
	AccountsPath     string                   `json:"accounts_path"`
	Checkpoint       bftRecoveryCheckpoint    `json:"checkpoint"`
	AccountsDigest   string                   `json:"accounts_digest"`
	Pending          *bftAccountCommitPending `json:"pending,omitempty"`
	Integrity        string                   `json:"integrity"`
}

// Owned by BFTConsensus.mu; configuration also holds the executor signing lock.
type bftAccountCommitJournal struct {
	path      string
	file      bftAccountCommitFile
	chainFile *os.File
	locks     []*StateLock
	blocks    []*Block
	accounts  *AccountStore
	prepared  *bftAccountCommitPending
	writeFile func(string, []byte, fs.FileMode) error
	boundary  func(string) error
}

// ConfigureAccountCommitRecovery extends round recovery for an isolated,
// account-only staging chain. It is NOT a BlockProducer or node startup hook.
// The caller must verify the canonical prefix and authenticate admitted blocks
// and votes. Paths must be exclusively owned; no legacy post-seal writers may
// run. Only plain transfers with legacy float accounting are supported.
// On a torn final line, the caller may supply the verified prefix returned by
// LoadChainNDJSON; this path repairs only an exact prefix of its pending block.
func (e *BFTExecutor) ConfigureAccountCommitRecovery(chain BFTRecoveryChain, chainPath, accountsPath string) error {
	return e.configureRoundRecovery(chain, &bftAccountCommitPaths{chainPath, accountsPath})
}

// PrepareAccountCommit validates and copies the exact signed block and its
// account transition before local precommit signing or quorum completion.
// The block must already have been authenticated/admitted by the caller.
func (e *BFTExecutor) PrepareAccountCommit(block *Block) error {
	if e == nil || e.bc == nil {
		return ErrBFTRoundRecoveryUnavailable
	}
	e.signingMu.Lock()
	defer e.signingMu.Unlock()
	e.bc.mu.Lock()
	defer e.bc.mu.Unlock()
	if err := e.bc.recoveryReadyLocked(); err != nil {
		return err
	}
	if e.bc.roundRecovery == nil || e.bc.roundRecovery.accountCommits == nil || block == nil {
		return ErrBFTRoundRecoveryUnavailable
	}
	cr := e.bc.rounds[block.Height]
	if cr == nil || cr.BlockHash != block.StateRoot || cr.Proposer != block.ProducerID {
		return errors.New("chain: commit block does not match the active proposal")
	}
	return e.bc.roundRecovery.accountCommits.prepare(cr, block)
}

// AccountCommitSnapshot returns detached copies after all commit writes have
// completed. It does not populate BFT's committed cache or quorum evidence.
func (e *BFTExecutor) AccountCommitSnapshot() ([]*Block, *AccountStore, error) {
	if e == nil || e.bc == nil {
		return nil, nil, ErrBFTRoundRecoveryUnavailable
	}
	e.bc.mu.RLock()
	defer e.bc.mu.RUnlock()
	if err := e.bc.recoveryReadyLocked(); err != nil {
		return nil, nil, err
	}
	if e.bc.roundRecovery == nil || e.bc.roundRecovery.accountCommits == nil {
		return nil, nil, ErrBFTRoundRecoveryUnavailable
	}
	j := e.bc.roundRecovery.accountCommits
	raw, err := json.Marshal(j.blocks)
	if err != nil {
		return nil, nil, err
	}
	var blocks []*Block
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, nil, err
	}
	return blocks, j.accounts.Clone(), nil
}

func openBFTAccountCommitJournal(signing *BFTSigningJournal, rules string, verified BFTRecoveryChain, paths bftAccountCommitPaths, guards map[uint64]bftRecoveryGuard) (_ *bftAccountCommitJournal, err error) {
	if ForkDustHeight() != math.MaxUint64 {
		return nil, errors.New("chain: account commit recovery requires disabled dust accounting")
	}
	paths.chain, err = filepath.Abs(paths.chain)
	if err != nil {
		return nil, err
	}
	paths.accounts, err = filepath.Abs(paths.accounts)
	if err != nil {
		return nil, err
	}
	j := &bftAccountCommitJournal{path: signing.path + ".rounds.commit"}
	defer func() {
		if err != nil {
			_ = j.close()
		}
	}()
	// Prevent aliases of owned data, lock and marker paths before acquiring them.
	owned := []string{signing.path, signing.path + ".binding", signing.path + ".lock", signing.path + ".rounds", signing.path + ".rounds.lock", signing.path + ".rounds.binding", j.path, j.path + ".binding", paths.chain, paths.chain + ".lock", paths.accounts, paths.accounts + ".lock"}
	for i, path := range owned {
		for _, previous := range owned[:i] {
			if strings.EqualFold(path, previous) {
				return nil, errors.New("chain: account commit paths overlap")
			}
			left, le := os.Stat(path)
			right, re := os.Stat(previous)
			if le == nil && re == nil && os.SameFile(left, right) {
				return nil, errors.New("chain: account commit paths alias the same file")
			}
		}
	}
	for _, path := range []string{paths.chain, paths.accounts} {
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("chain: account commit requires an existing regular file at %s", path)
		}
		lock, lockErr := AcquireStateLock(path + ".lock")
		if lockErr != nil {
			return nil, lockErr
		}
		j.locks = append(j.locks, lock)
	}
	j.file = bftAccountCommitFile{Version: 1, Binding: signing.binding, RulesFingerprint: rules, ChainPath: paths.chain, AccountsPath: paths.accounts}
	marker := []byte("qsdm-bft-account-commit-v1\n")
	storedMarker, markerErr := readBFTRecoveryFile(j.path+".binding", int64(len(marker)))
	if markerErr != nil && !errors.Is(markerErr, os.ErrNotExist) {
		return nil, markerErr
	}
	if markerErr == nil && !bytes.Equal(storedMarker, marker) {
		return nil, ErrBFTRoundRecoveryCorrupt
	}
	raw, readErr := readBFTRecoveryFile(j.path, maxBFTAccountCommitBytes)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return nil, readErr
	}
	if errors.Is(readErr, os.ErrNotExist) {
		if markerErr == nil {
			return nil, fmt.Errorf("%w: missing initialized commit journal", ErrBFTRoundRecoveryCorrupt)
		}
	} else {
		var saved bftAccountCommitFile
		if err := decodeBFTCommitJSON(raw, &saved); err != nil {
			return nil, err
		}
		want := saved.Integrity
		saved.Integrity = ""
		digest, err := bftCommitDigest(saved)
		if err != nil || saved.Version != 1 || want != digest {
			return nil, ErrBFTRoundRecoveryCorrupt
		}
		if saved.Binding != signing.binding || saved.RulesFingerprint != rules || saved.ChainPath != paths.chain || saved.AccountsPath != paths.accounts {
			return nil, ErrBFTSigningJournalBindingMismatch
		}
		j.file = saved
	}
	j.chainFile, err = os.OpenFile(paths.chain, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	info, err := j.chainFile.Stat()
	if err != nil {
		return nil, err
	}
	end := info.Size()
	if p := j.file.Pending; p != nil {
		if p.ChainOffset <= 0 || p.ChainOffset > end {
			return nil, ErrBFTRoundRecoveryCorrupt
		}
		end = p.ChainOffset
	}
	j.blocks, err = loadBFTCommitPrefix(j.chainFile, end)
	if err != nil || len(j.blocks) == 0 {
		return nil, fmt.Errorf("%w: invalid account commit chain prefix: %v", ErrBFTRoundRecoveryChainMismatch, err)
	}
	for _, block := range j.blocks {
		trusted, ok := verified.GetBlock(block.Height)
		if !ok || !sameBFTCommitJSON(block, trusted) {
			return nil, fmt.Errorf("%w: disk chain differs from verified prefix", ErrBFTRoundRecoveryChainMismatch)
		}
	}
	tip, _ := j.LatestBlock()
	trustedTip, _ := verified.LatestBlock()
	if trustedTip == nil || trustedTip.Height != tip.Height && (j.file.Pending == nil || !sameBFTCommitJSON(trustedTip, j.file.Pending.Block)) {
		return nil, ErrBFTRoundRecoveryChainMismatch
	}
	// Reject unrelated missing/conflicting commits before repairing any file.
	for _, guard := range guards {
		if guard.CommittedValue == "" {
			continue
		}
		if p := j.file.Pending; p != nil && p.Block != nil && p.Block.Height == guard.Height && p.Block.StateRoot == guard.CommittedValue {
			continue
		}
		block, ok := j.GetBlock(guard.Height)
		if !ok || block.StateRoot != guard.CommittedValue {
			return nil, ErrBFTRoundRecoveryChainMismatch
		}
	}
	accounts, err := readBFTCommitAccounts(paths.accounts)
	if err != nil {
		return nil, err
	}
	if p := j.file.Pending; p != nil {
		if err := j.validatePending(p); err != nil {
			return nil, err
		}
		guard, ok := guards[p.Block.Height]
		if !ok || guard.NextRound != uint64(p.Round)+1 || guard.CommittedValue != "" && guard.CommittedValue != p.Block.StateRoot {
			return nil, ErrBFTRoundRecoveryCorrupt
		}
		if guard.CommittedValue != "" {
			if !reflect.DeepEqual(accounts, p.Before) && !reflect.DeepEqual(accounts, p.After) {
				return nil, ErrBFTRoundRecoveryChainMismatch
			}
			if err := j.finish(); err != nil {
				return nil, err
			}
		} else {
			if info.Size() != p.ChainOffset || !reflect.DeepEqual(accounts, p.Before) {
				return nil, fmt.Errorf("%w: undecided commit changed chain or accounts", ErrBFTRoundRecoveryCorrupt)
			}
			j.accounts, _ = bftCommitAccountStore(accounts)
			if err := j.persist(nil); err != nil {
				return nil, err
			}
		}
	} else {
		j.accounts, err = bftCommitAccountStore(accounts)
		if err != nil || j.accounts.StateRoot() != tip.StateRoot {
			return nil, ErrBFTRoundRecoveryChainMismatch
		}
		digest, err := bftCommitDigest(accounts)
		if err != nil {
			return nil, err
		}
		if readErr == nil && (j.file.Checkpoint != (bftRecoveryCheckpoint{tip.Height, tip.Hash}) || j.file.AccountsDigest != digest) {
			return nil, fmt.Errorf("%w: completed commit checkpoint differs", ErrBFTRoundRecoveryChainMismatch)
		}
		j.file.Checkpoint = bftRecoveryCheckpoint{tip.Height, tip.Hash}
		j.file.AccountsDigest = digest
		if err := j.persist(nil); err != nil {
			return nil, err
		}
	}
	if errors.Is(markerErr, os.ErrNotExist) {
		if err := fileutil.WriteFileAtomicStrict(j.path+".binding", marker, 0o600); err != nil {
			return nil, err
		}
	}
	return j, nil
}

func (j *bftAccountCommitJournal) prepare(cr *ConsensusRound, block *Block) error {
	raw, err := json.Marshal(block)
	if err != nil || len(raw)+1 >= maxBFTRoundRecoveryBytes {
		return ErrBFTRoundRecoveryFull
	}
	var copied Block
	if err := decodeBFTCommitJSON(raw, &copied); err != nil {
		return err
	}
	info, err := j.chainFile.Stat()
	if err != nil {
		return err
	}
	p := &bftAccountCommitPending{Round: cr.Round, ChainOffset: info.Size(), Block: &copied, Before: j.accounts.AllAccounts()}
	after, err := replayBFTCommitAccounts(p.Before, p.Block)
	if err != nil {
		return err
	}
	p.After = after.AllAccounts()
	if err := j.validatePending(p); err != nil {
		return err
	}
	if j.prepared != nil && j.prepared.Block.Height == block.Height && j.prepared.Round == cr.Round && !sameBFTCommitJSON(j.prepared, p) {
		return ErrBFTSigningJournalConflict
	}
	j.prepared = p
	return nil
}

func (j *bftAccountCommitJournal) matchesPrepared(cr *ConsensusRound) error {
	p := j.prepared
	if p == nil || p.Block.Height != cr.Height || p.Round != cr.Round || p.Block.StateRoot != cr.BlockHash || p.Block.ProducerID != cr.Proposer {
		return errors.New("chain: exact account commit block must be prepared first")
	}
	return nil
}

func (j *bftAccountCommitJournal) stage(cr *ConsensusRound) error {
	if err := j.matchesPrepared(cr); err != nil {
		return err
	}
	if err := j.validatePending(j.prepared); err != nil {
		return err
	}
	if err := j.persist(j.prepared); err != nil {
		return err
	}
	return j.at("pending")
}

func (j *bftAccountCommitJournal) validatePending(p *bftAccountCommitPending) error {
	tip, _ := j.LatestBlock()
	if tip.Height == math.MaxUint64 || p.Block == nil || p.Block.Height != tip.Height+1 || p.Block.PrevHash != tip.Hash {
		return ErrBFTRoundRecoveryChainMismatch
	}
	digest, err := bftCommitDigest(p.Before)
	if err != nil || j.file.Checkpoint != (bftRecoveryCheckpoint{tip.Height, tip.Hash}) || j.file.AccountsDigest != digest {
		return ErrBFTRoundRecoveryChainMismatch
	}
	before, err := bftCommitAccountStore(p.Before)
	if err != nil || before.StateRoot() != tip.StateRoot {
		return ErrBFTRoundRecoveryChainMismatch
	}
	after, err := replayBFTCommitAccounts(p.Before, p.Block)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(after.AllAccounts(), p.After) {
		return ErrBFTRoundRecoveryCorrupt
	}
	return nil
}

func (j *bftAccountCommitJournal) finish() error {
	p := j.file.Pending
	if p == nil {
		return ErrBFTRoundRecoveryCorrupt
	}
	if err := j.validatePending(p); err != nil {
		return err
	}
	accounts, err := readBFTCommitAccounts(j.file.AccountsPath)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(accounts, p.Before) && !reflect.DeepEqual(accounts, p.After) {
		return ErrBFTRoundRecoveryChainMismatch
	}
	raw, err := json.Marshal(p.Block)
	if err != nil || len(raw)+1 >= maxBFTRoundRecoveryBytes {
		return ErrBFTRoundRecoveryCorrupt
	}
	raw = append(raw, '\n')
	info, err := j.chainFile.Stat()
	if err != nil {
		return err
	}
	if info.Size() < p.ChainOffset || info.Size()-p.ChainOffset > int64(len(raw)) {
		return ErrBFTRoundRecoveryChainMismatch
	}
	prefix, err := loadBFTCommitPrefix(j.chainFile, p.ChainOffset)
	if err != nil || !sameBFTCommitJSON(prefix, j.blocks) {
		return ErrBFTRoundRecoveryChainMismatch
	}
	tail, err := io.ReadAll(io.NewSectionReader(j.chainFile, p.ChainOffset, int64(len(raw))))
	if err != nil || !bytes.HasPrefix(raw, tail) {
		return ErrBFTRoundRecoveryChainMismatch
	}
	// Complete only the known pending block's exact byte prefix. Never trim or
	// rewrite an unrelated malformed line or a different canonical block.
	if len(tail) != len(raw) {
		if err := j.chainFile.Truncate(p.ChainOffset); err != nil {
			return err
		}
		if n, err := j.chainFile.WriteAt(raw, p.ChainOffset); err != nil || n != len(raw) {
			return fmt.Errorf("chain: commit append failed: %w", errors.Join(err, io.ErrShortWrite))
		}
	}
	if err := j.chainFile.Sync(); err != nil {
		return err
	}
	if err := j.at("chain"); err != nil {
		return err
	}
	accountBytes, err := json.MarshalIndent(p.After, "", "  ")
	if err != nil {
		return err
	}
	if err := j.write(j.file.AccountsPath, accountBytes); err != nil {
		return err
	}
	if err := j.at("accounts"); err != nil {
		return err
	}
	completed := j.file
	completed.Pending = nil
	completed.Checkpoint = bftRecoveryCheckpoint{p.Block.Height, p.Block.Hash}
	completed.AccountsDigest, err = bftCommitDigest(p.After)
	if err != nil {
		return err
	}
	if err := j.persistFile(completed); err != nil {
		return err
	}
	if err := j.at("clear"); err != nil {
		return err
	}
	j.blocks = append(j.blocks, p.Block)
	j.accounts, _ = bftCommitAccountStore(p.After)
	j.prepared = nil
	return nil
}

func (j *bftAccountCommitJournal) persist(pending *bftAccountCommitPending) error {
	f := j.file
	f.Pending = pending
	return j.persistFile(f)
}

func (j *bftAccountCommitJournal) persistFile(f bftAccountCommitFile) error {
	f.Integrity = ""
	digest, err := bftCommitDigest(f)
	if err != nil {
		return err
	}
	f.Integrity = digest
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if len(raw) > maxBFTAccountCommitBytes {
		return ErrBFTRoundRecoveryFull
	}
	if err := j.write(j.path, raw); err != nil {
		return err
	}
	j.file = f
	return nil
}

func (j *bftAccountCommitJournal) write(path string, raw []byte) error {
	fn := j.writeFile
	if fn == nil {
		fn = fileutil.WriteFileAtomicStrict
	}
	return fn(path, raw, 0o600)
}

func (j *bftAccountCommitJournal) at(name string) error {
	if j.boundary != nil {
		return j.boundary(name)
	}
	return nil
}

func (j *bftAccountCommitJournal) close() error {
	var err error
	if j.chainFile != nil {
		err = j.chainFile.Close()
		j.chainFile = nil
	}
	for _, lock := range j.locks {
		err = errors.Join(err, lock.Close())
	}
	j.locks = nil
	return err
}

func (j *bftAccountCommitJournal) GetBlock(height uint64) (*Block, bool) {
	if height >= uint64(len(j.blocks)) {
		return nil, false
	}
	return j.blocks[height], true
}

func (j *bftAccountCommitJournal) LatestBlock() (*Block, bool) {
	if len(j.blocks) == 0 {
		return nil, false
	}
	return j.blocks[len(j.blocks)-1], true
}

func bftCommitDigest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func sameBFTCommitJSON(a, b any) bool {
	left, le := json.Marshal(a)
	right, re := json.Marshal(b)
	return le == nil && re == nil && bytes.Equal(left, right)
}

func decodeBFTCommitJSON(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return fmt.Errorf("%w: %v", ErrBFTRoundRecoveryCorrupt, err)
	}
	var trailing any
	if err := d.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrBFTRoundRecoveryCorrupt
	}
	return nil
}

func bftCommitAccountStore(accounts []Account) (*AccountStore, error) {
	store := NewAccountStore()
	for i, acc := range accounts {
		if acc.Address == "" || len(acc.Address) > bftSigningJournalStringMaxLen || acc.Balance < 0 || math.IsNaN(acc.Balance) || math.IsInf(acc.Balance, 0) || acc.BalanceDust != 0 || i > 0 && accounts[i-1].Address >= acc.Address {
			return nil, ErrBFTRoundRecoveryCorrupt
		}
		cp := acc
		store.accounts[acc.Address] = &cp
	}
	return store, nil
}

func readBFTCommitAccounts(path string) ([]Account, error) {
	raw, err := readBFTRecoveryFile(path, maxBFTRoundRecoveryBytes)
	if err != nil {
		return nil, err
	}
	var accounts []Account
	if err := decodeBFTCommitJSON(raw, &accounts); err != nil {
		return nil, err
	}
	if _, err := bftCommitAccountStore(accounts); err != nil {
		return nil, err
	}
	return accounts, nil
}

func replayBFTCommitAccounts(before []Account, block *Block) (*AccountStore, error) {
	if ForkDustHeight() != math.MaxUint64 || block == nil || len(block.Transactions) == 0 || !block.ProducerAuth.Signed() {
		return nil, errors.New("chain: account commit requires signed plain-transfer block with dust disabled")
	}
	seen := make(map[string]bool)
	var fees float64
	var gas int64
	for _, tx := range block.Transactions {
		if tx == nil || tx.ID == "" || seen[tx.ID] || tx.Sender == "" || tx.Recipient == "" || tx.Amount <= 0 || math.IsNaN(tx.Amount) || math.IsInf(tx.Amount, 0) || tx.Fee < 0 || math.IsNaN(tx.Fee) || math.IsInf(tx.Fee, 0) || tx.GasLimit < 0 || tx.GasLimit > math.MaxInt64-gas || tx.Nonce == math.MaxUint64 || len(tx.Payload) != 0 || tx.ContractID != "" {
			return nil, errors.New("chain: unsupported account commit transaction")
		}
		seen[tx.ID] = true
		fees += tx.Fee
		gas += tx.GasLimit
	}
	if !validRecoveryBlock(block, block.Height) || fees != block.TotalFees || gas != block.GasUsed {
		return nil, ErrBFTRoundRecoveryCorrupt
	}
	if err := VerifyBlockSignature(block); err != nil {
		return nil, err
	}
	store, err := bftCommitAccountStore(before)
	if err != nil {
		return nil, err
	}
	for _, tx := range block.Transactions {
		if err := store.ApplyTx(tx); err != nil {
			return nil, err
		}
	}
	after := store.AllAccounts()
	if _, err := bftCommitAccountStore(after); err != nil {
		return nil, err
	}
	raw, err := json.MarshalIndent(after, "", "  ")
	if err != nil || len(raw) > maxBFTRoundRecoveryBytes {
		return nil, ErrBFTRoundRecoveryFull
	}
	if store.StateRoot() != block.StateRoot {
		return nil, ErrBFTRoundRecoveryChainMismatch
	}
	return store, nil
}

func loadBFTCommitPrefix(file *os.File, end int64) ([]*Block, error) {
	if end <= 0 {
		return nil, ErrBFTRoundRecoveryCorrupt
	}
	var last [1]byte
	if _, err := file.ReadAt(last[:], end-1); err != nil || last[0] != '\n' {
		return nil, ErrBFTRoundRecoveryCorrupt
	}
	scanner := bufio.NewScanner(io.NewSectionReader(file, 0, end))
	scanner.Buffer(make([]byte, 64<<10), maxBFTRoundRecoveryBytes)
	var blocks []*Block
	for scanner.Scan() {
		var block Block
		if err := decodeBFTCommitJSON(scanner.Bytes(), &block); err != nil {
			return nil, err
		}
		for _, tx := range block.Transactions {
			if tx == nil {
				return nil, ErrBFTRoundRecoveryCorrupt
			}
		}
		if !validRecoveryBlock(&block, uint64(len(blocks))) || len(blocks) == 0 && block.PrevHash != "" || len(blocks) > 0 && block.PrevHash != blocks[len(blocks)-1].Hash {
			return nil, ErrBFTRoundRecoveryChainMismatch
		}
		blocks = append(blocks, &block)
	}
	return blocks, scanner.Err()
}
