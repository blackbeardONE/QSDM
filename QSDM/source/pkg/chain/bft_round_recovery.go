package chain

import (
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
	"sort"

	"github.com/blackbeardONE/QSDM/pkg/fileutil"
)

const (
	bftRoundRecoveryVersion    = 1
	maxBFTRoundRecoveryBytes   = 4 << 20
	maxBFTRoundRecoveryRecords = 4096
)

var (
	ErrBFTRoundRecoveryUnavailable   = errors.New("chain: durable BFT round recovery unavailable")
	ErrBFTRoundRecoveryCorrupt       = errors.New("chain: BFT round recovery corrupt")
	ErrBFTRoundRecoveryChainMismatch = errors.New("chain: BFT round recovery chain mismatch")
	ErrBFTRoundRecoveryFull          = errors.New("chain: BFT round recovery capacity reached")
)

// BFTRecoveryChain must be a quiescent canonical chain whose blocks and account
// state have already been verified by the caller. These lookups reconcile local
// safety guards; they are not a substitute for chain/state verification.
type BFTRecoveryChain interface {
	GetBlock(height uint64) (*Block, bool)
	LatestBlock() (*Block, bool)
}

type bftRecoveryCheckpoint struct {
	Height uint64 `json:"height"`
	Hash   string `json:"hash"`
}

// NextRound is the floor to use AFTER a crash, including abandonment of the
// current round. LockHash is a local safety guard, never a quorum certificate.
type bftRecoveryGuard struct {
	Height         uint64 `json:"height"`
	NextRound      uint64 `json:"next_round"`
	LockHash       string `json:"lock_hash,omitempty"`
	CommittedValue string `json:"committed_value,omitempty"`
}

type bftRoundRecoveryFile struct {
	Version          int                      `json:"version"`
	Binding          BFTSigningJournalBinding `json:"binding"`
	RulesFingerprint string                   `json:"rules_fingerprint"`
	Checkpoint       bftRecoveryCheckpoint    `json:"checkpoint"`
	Guards           []bftRecoveryGuard       `json:"guards"`
	Integrity        string                   `json:"integrity"`
}

// Owned by BFTConsensus.mu. The process lock covers the stable sibling path,
// not the inode replaced on each strict atomic write.
type bftRoundRecovery struct {
	path       string
	lock       *StateLock
	file       bftRoundRecoveryFile
	guards     map[uint64]bftRecoveryGuard
	validators []Validator
	writeFile  func(string, []byte, fs.FileMode) error
}

// ConfigureRoundRecovery is an opt-in, fixed-validator-set library path. Call
// after ConfigureSigningJournal and verified chain restoration, before any
// consensus or outbound signing activity. Node startup does not call it yet.
// Any failure permanently blocks this executor and its consensus instance.
// ApplyInbound is disabled in this mode. Direct consensus callers are trusted
// to authenticate and round-bind every vote before applying it.
func (e *BFTExecutor) ConfigureRoundRecovery(chain BFTRecoveryChain) error {
	if e == nil || e.bc == nil {
		return ErrBFTRoundRecoveryUnavailable
	}
	e.signingMu.Lock()
	defer e.signingMu.Unlock()
	bc := e.bc
	bc.mu.Lock()
	defer bc.mu.Unlock()
	bc.roundRecoveryRequired = true
	fail := func(err error) error {
		bc.roundRecoveryErr = fmt.Errorf("%w: %w", ErrBFTRoundRecoveryUnavailable, err)
		return bc.roundRecoveryErr
	}
	if bc.roundRecoveryErr != nil {
		return bc.roundRecoveryErr
	}
	if bc.roundRecovery != nil || e.signingStarted || len(bc.rounds)+len(bc.committed)+len(bc.nextRound) != 0 {
		return fail(errors.New("round recovery must be configured once before consensus activity"))
	}
	if e.signingJournalErr != nil {
		return fail(e.signingJournalErr)
	}
	if e.signingJournal == nil || e.signingJournalLock == nil {
		return fail(errors.New("an exclusively owned signing journal is required"))
	}
	if !e.signingJournal.binding.AllowLegacyMembership || e.MembershipPolicy() != nil {
		return fail(errors.New("round recovery currently supports fixed legacy validator sets only"))
	}
	if bc.validators == nil {
		return fail(errors.New("validator set is missing"))
	}
	validators := bc.validators.ActiveValidators()
	signerActive := false
	for _, v := range validators {
		signerActive = signerActive || v.Address == e.signingJournal.binding.SignerAddress
	}
	if !signerActive {
		return fail(errors.New("signing key is not in the fixed validator set"))
	}
	rules, err := bftRecoveryRules(bc.cfg, validators)
	if err != nil {
		return fail(err)
	}
	recovery, err := openBFTRoundRecovery(e.signingJournal, rules, chain)
	if err != nil {
		return fail(err)
	}
	recovery.validators = validators
	bc.roundRecovery = recovery
	for height, guard := range recovery.guards {
		if height <= recovery.file.Checkpoint.Height {
			continue
		}
		bc.nextRound[height] = guard.NextRound
		if guard.LockHash != "" {
			bc.carryPrevoteLock[height] = guard.LockHash
		}
	}
	return nil
}

func bftRecoveryRules(cfg ConsensusConfig, validators []Validator) (string, error) {
	if len(validators) == 0 || math.IsNaN(cfg.QuorumFraction) || math.IsInf(cfg.QuorumFraction, 0) {
		return "", errors.New("invalid fixed consensus rules")
	}
	type voter struct {
		Address string
		Stake   float64
	}
	voters := make([]voter, 0, len(validators))
	for _, v := range validators {
		if v.Address == "" || v.Stake <= 0 || math.IsNaN(v.Stake) || math.IsInf(v.Stake, 0) {
			return "", errors.New("invalid fixed validator")
		}
		voters = append(voters, voter{v.Address, v.Stake})
	}
	sort.Slice(voters, func(i, j int) bool { return voters[i].Address < voters[j].Address })
	raw, err := json.Marshal(struct {
		Config ConsensusConfig
		Voters []voter
	}{cfg, voters})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func openBFTRoundRecovery(journal *BFTSigningJournal, rules string, chain BFTRecoveryChain) (_ *bftRoundRecovery, err error) {
	if chain == nil {
		return nil, ErrBFTRoundRecoveryChainMismatch
	}
	genesis, ok := chain.GetBlock(0)
	if !ok || !validRecoveryBlock(genesis, 0) || genesis.PrevHash != "" || genesis.Hash != journal.binding.ChainID {
		return nil, fmt.Errorf("%w: verified genesis differs from signing binding", ErrBFTRoundRecoveryChainMismatch)
	}
	tip, ok := chain.LatestBlock()
	if !ok || tip == nil || !validRecoveryBlock(tip, tip.Height) {
		return nil, fmt.Errorf("%w: invalid restored tip", ErrBFTRoundRecoveryChainMismatch)
	}
	indexedTip, ok := chain.GetBlock(tip.Height)
	if !ok || !validRecoveryBlock(indexedTip, tip.Height) || indexedTip.Hash != tip.Hash {
		return nil, fmt.Errorf("%w: inconsistent restored tip", ErrBFTRoundRecoveryChainMismatch)
	}
	r := &bftRoundRecovery{
		path:   journal.path + ".rounds",
		file:   bftRoundRecoveryFile{Version: bftRoundRecoveryVersion, Binding: journal.binding, RulesFingerprint: rules},
		guards: make(map[uint64]bftRecoveryGuard),
	}
	r.lock, err = AcquireStateLock(r.path + ".lock")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = r.lock.Close()
		}
	}()
	marker := []byte("qsdm-bft-round-recovery-v1\n")
	savedMarker, markerErr := readBFTRecoveryFile(r.path+".binding", int64(len(marker)))
	if markerErr != nil && !errors.Is(markerErr, os.ErrNotExist) {
		return nil, markerErr
	}
	if markerErr == nil && !bytes.Equal(savedMarker, marker) {
		return nil, ErrBFTRoundRecoveryCorrupt
	}
	raw, readErr := readBFTRecoveryFile(r.path, maxBFTRoundRecoveryBytes)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return nil, readErr
	}
	if errors.Is(readErr, os.ErrNotExist) {
		if markerErr == nil || len(journal.Records()) != 0 {
			return nil, fmt.Errorf("%w: missing initialized state or unsupported signing-history migration", ErrBFTRoundRecoveryCorrupt)
		}
	} else {
		r.file = bftRoundRecoveryFile{}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&r.file); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrBFTRoundRecoveryCorrupt, err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return nil, ErrBFTRoundRecoveryCorrupt
		}
		if err := r.file.validate(journal.binding, rules); err != nil {
			return nil, err
		}
		checkpoint, ok := chain.GetBlock(r.file.Checkpoint.Height)
		if r.file.Checkpoint.Height > tip.Height || !ok || !validRecoveryBlock(checkpoint, r.file.Checkpoint.Height) || checkpoint.Hash != r.file.Checkpoint.Hash {
			return nil, fmt.Errorf("%w: checkpoint is not in the restored chain", ErrBFTRoundRecoveryChainMismatch)
		}
		for _, guard := range r.file.Guards {
			if guard.CommittedValue != "" {
				block, ok := chain.GetBlock(guard.Height)
				if guard.Height > tip.Height || !ok || !validRecoveryBlock(block, guard.Height) || block.StateRoot != guard.CommittedValue {
					return nil, fmt.Errorf("%w: local commit at height %d is absent or conflicting", ErrBFTRoundRecoveryChainMismatch, guard.Height)
				}
			}
			r.guards[guard.Height] = guard
		}
		for _, signed := range journal.Records() {
			guard, ok := r.guards[signed.Intent.Height]
			if !ok || guard.NextRound <= uint64(signed.Intent.Round) {
				return nil, fmt.Errorf("%w: signing history is ahead of round guards", ErrBFTRoundRecoveryCorrupt)
			}
		}
	}
	r.file.Checkpoint = bftRecoveryCheckpoint{tip.Height, tip.Hash}
	if err := r.persist(r.guards); err != nil {
		return nil, err
	}
	if errors.Is(markerErr, os.ErrNotExist) {
		if err := fileutil.WriteFileAtomicStrict(r.path+".binding", marker, 0o600); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func validRecoveryBlock(block *Block, height uint64) bool {
	return block != nil && block.Height == height && block.Hash != "" && block.Hash == ComputeBlockHash(block)
}

func readBFTRecoveryFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, ErrBFTRoundRecoveryCorrupt
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, ErrBFTRoundRecoveryCorrupt
	}
	return raw, nil
}

func (f bftRoundRecoveryFile) validate(binding BFTSigningJournalBinding, rules string) error {
	if f.Version != bftRoundRecoveryVersion || len(f.Guards) > maxBFTRoundRecoveryRecords || f.Checkpoint.Hash == "" {
		return ErrBFTRoundRecoveryCorrupt
	}
	if f.Binding != binding || f.RulesFingerprint != rules {
		return ErrBFTSigningJournalBindingMismatch
	}
	want := f.Integrity
	f.Integrity = ""
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	if want != hex.EncodeToString(sum[:]) {
		return ErrBFTRoundRecoveryCorrupt
	}
	for i, guard := range f.Guards {
		if guard.NextRound == 0 || guard.NextRound > uint64(math.MaxUint32)+1 || (i > 0 && f.Guards[i-1].Height >= guard.Height) {
			return ErrBFTRoundRecoveryCorrupt
		}
		for _, value := range []string{guard.LockHash, guard.CommittedValue} {
			if value != "" {
				if err := validateBFTSigningJournalString("round guard value", value); err != nil {
					return ErrBFTRoundRecoveryCorrupt
				}
			}
		}
		if guard.CommittedValue != "" && (guard.CommittedValue == NilVoteHash || guard.LockHash != guard.CommittedValue) {
			return ErrBFTRoundRecoveryCorrupt
		}
	}
	return nil
}

func (r *bftRoundRecovery) persist(guards map[uint64]bftRecoveryGuard) error {
	if len(guards) > maxBFTRoundRecoveryRecords {
		return ErrBFTRoundRecoveryFull
	}
	f := r.file
	f.Guards = make([]bftRecoveryGuard, 0, len(guards))
	for _, guard := range guards {
		f.Guards = append(f.Guards, guard)
	}
	sort.Slice(f.Guards, func(i, j int) bool { return f.Guards[i].Height < f.Guards[j].Height })
	f.Integrity = ""
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	f.Integrity = hex.EncodeToString(sum[:])
	if err := f.validate(f.Binding, f.RulesFingerprint); err != nil {
		return err
	}
	raw, err = json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if len(raw)+1 > maxBFTRoundRecoveryBytes {
		return ErrBFTRoundRecoveryFull
	}
	writeFile := r.writeFile
	if writeFile == nil {
		writeFile = fileutil.WriteFileAtomicStrict
	}
	if err := writeFile(r.path, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	r.file, r.guards = f, guards
	return nil
}

func (bc *BFTConsensus) recoveryReadyLocked() error {
	if bc.roundRecoveryErr != nil {
		return bc.roundRecoveryErr
	}
	if bc.roundRecoveryRequired && bc.roundRecovery == nil {
		return ErrBFTRoundRecoveryUnavailable
	}
	return nil
}

func (bc *BFTConsensus) persistRecoveryRoundLocked(cr *ConsensusRound) error {
	if bc.roundRecovery == nil {
		return nil
	}
	r := bc.roundRecovery
	guard := bftRecoveryGuard{Height: cr.Height, NextRound: uint64(cr.Round) + 1, LockHash: cr.LockedBlockHash}
	if cr.Status == StatusCommitted {
		guard.CommittedValue = cr.BlockHash
	}
	previous := r.guards[cr.Height]
	if previous.NextRound > guard.NextRound || previous.CommittedValue != "" && previous.CommittedValue != guard.CommittedValue {
		bc.roundRecoveryErr = fmt.Errorf("%w: non-monotonic round guard", ErrBFTRoundRecoveryUnavailable)
		return bc.roundRecoveryErr
	}
	guards := make(map[uint64]bftRecoveryGuard, len(r.guards)+1)
	for height, saved := range r.guards {
		guards[height] = saved
	}
	guards[cr.Height] = guard
	if err := r.persist(guards); err != nil {
		bc.roundRecoveryErr = fmt.Errorf("%w: %w", ErrBFTRoundRecoveryUnavailable, err)
		return bc.roundRecoveryErr
	}
	return nil
}

// RoundRecoveryError also exposes timeout persistence failures: the legacy
// timeout API returns only heights. Callers must stop driving on this error.
func (bc *BFTConsensus) RoundRecoveryError() error {
	if bc == nil {
		return ErrBFTRoundRecoveryUnavailable
	}
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.recoveryReadyLocked()
}

// CloseRoundRecovery releases file ownership and permanently blocks further
// consensus activity and outbound votes on this executor.
func (e *BFTExecutor) CloseRoundRecovery() error {
	if e == nil || e.bc == nil {
		return nil
	}
	e.signingMu.Lock()
	defer e.signingMu.Unlock()
	e.bc.mu.Lock()
	defer e.bc.mu.Unlock()
	return e.bc.closeRoundRecoveryLocked()
}

func (bc *BFTConsensus) closeRoundRecoveryLocked() error {
	if !bc.roundRecoveryRequired {
		return nil
	}
	bc.roundRecoveryErr = fmt.Errorf("%w: closed", ErrBFTRoundRecoveryUnavailable)
	if bc.roundRecovery == nil || bc.roundRecovery.lock == nil {
		return nil
	}
	err := bc.roundRecovery.lock.Close()
	bc.roundRecovery.lock = nil
	return err
}

func (bc *BFTConsensus) recoverySigningGuardLocked(intent BFTSigningIntent) error {
	if err := bc.recoveryReadyLocked(); err != nil {
		return err
	}
	if !bc.roundRecoveryRequired {
		return nil
	}
	if !bc.activeValidatorLocked(intent.Validator) {
		return ErrBFTSigningJournalBindingMismatch
	}
	if intent.Height <= bc.roundRecovery.file.Checkpoint.Height {
		return ErrBFTRoundRetired
	}
	cr := bc.rounds[intent.Height]
	if cr == nil || cr.Round != intent.Round {
		return ErrBFTRoundRetired
	}
	switch intent.Kind {
	case BFTWirePropose:
		if cr.Proposer != intent.Validator || cr.BlockHash != intent.BlockHash {
			return ErrBFTSigningJournalConflict
		}
	case BFTWirePrevote, BFTWirePrecommit:
		if intent.BlockHash != NilVoteHash && cr.LockedBlockHash != "" && cr.LockedBlockHash != NilVoteHash && cr.LockedBlockHash != intent.BlockHash {
			return ErrBFTSigningJournalConflict
		}
		if intent.Kind == BFTWirePrecommit {
			if cr.Status != StatusPreVoted {
				return errors.New("chain: recovered precommit requires a fresh prevote quorum")
			}
			return bc.validatePreCommitAgainstLock(cr, intent.BlockHash)
		}
	}
	return nil
}

func (bc *BFTConsensus) activeValidatorsLocked() []Validator {
	if bc.roundRecovery != nil {
		return append([]Validator(nil), bc.roundRecovery.validators...)
	}
	return bc.validators.ActiveValidators()
}

func (bc *BFTConsensus) activeValidatorLocked(address string) bool {
	if bc.roundRecovery != nil {
		for _, v := range bc.roundRecovery.validators {
			if v.Address == address {
				return true
			}
		}
		return false
	}
	v, ok := bc.validators.GetValidator(address)
	return ok && v.Status == ValidatorActive
}

func cloneRecoveryRound(cr *ConsensusRound) *ConsensusRound {
	if cr == nil {
		return nil
	}
	clone := *cr
	clone.PreVotes = append([]BlockVote(nil), cr.PreVotes...)
	clone.Commits = append([]BlockVote(nil), cr.Commits...)
	return &clone
}
