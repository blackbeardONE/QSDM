package chain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/fileutil"
)

// ErrBFTSigningUnavailable means journaling was requested but signing cannot
// safely proceed. There is no unsigned or in-memory fallback.
var ErrBFTSigningUnavailable = errors.New("chain: durable BFT signing unavailable")

// RequireSigningJournal blocks outbound signing until ConfigureSigningJournal
// succeeds. Startup calls it before restoring the chain used for the binding.
func (e *BFTExecutor) RequireSigningJournal() {
	if e == nil {
		return
	}
	e.signingMu.Lock()
	defer e.signingMu.Unlock()
	e.signingJournalRequired = true
}

// SigningJournalRequired reports whether the executor must journal its votes.
func (e *BFTExecutor) SigningJournalRequired() bool {
	if e == nil {
		return false
	}
	e.signingMu.Lock()
	defer e.signingMu.Unlock()
	return e.signingJournalRequired
}

// ConfigureSigningJournal binds an executor once, before its first signing
// attempt. The OS lock survives atomic file replacement and is released on
// process exit. Errors leave signing blocked for this executor's lifetime.
func (e *BFTExecutor) ConfigureSigningJournal(path string, binding BFTSigningJournalBinding) error {
	if e == nil {
		return ErrBFTSigningUnavailable
	}
	e.signingMu.Lock()
	defer e.signingMu.Unlock()
	e.signingJournalRequired = true
	if e.signingJournalErr != nil {
		return e.signingJournalErr
	}
	fail := func(err error) error {
		e.signingJournalErr = fmt.Errorf("%w: %w", ErrBFTSigningUnavailable, err)
		return e.signingJournalErr
	}
	if e.signingStarted || e.signingJournal != nil {
		return fail(errors.New("journal must be configured exactly once before signing"))
	}
	if err := binding.validate(); err != nil {
		return fail(err)
	}
	signer := e.VoteSigner()
	if signer == nil || BFTValidatorAddress(signer.GetPublicKey()) != binding.SignerAddress {
		return fail(ErrBFTSigningJournalBindingMismatch)
	}
	if strings.TrimSpace(path) == "" {
		return fail(errors.New("journal path is empty"))
	}
	path, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return fail(err)
	}
	lock, err := AcquireStateLock(path + ".lock")
	if err != nil {
		return fail(err)
	}
	journal, err := openExecutorSigningJournal(path, binding)
	if err != nil {
		_ = lock.Close()
		return fail(err)
	}
	e.signingJournal = journal
	e.signingJournalLock = lock
	return nil
}

func openExecutorSigningJournal(path string, binding BFTSigningJournalBinding) (*BFTSigningJournal, error) {
	rawBinding, err := json.Marshal(binding)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(rawBinding)
	marker := []byte(hex.EncodeToString(sum[:]) + "\n")
	if info, err := os.Lstat(path + ".binding"); err == nil {
		if !info.Mode().IsRegular() || info.Size() != int64(len(marker)) {
			return nil, fmt.Errorf("%w: invalid initialization marker", ErrBFTSigningJournalCorrupt)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	storedMarker, markerErr := os.ReadFile(path + ".binding")
	if markerErr != nil && !errors.Is(markerErr, os.ErrNotExist) {
		return nil, markerErr
	}
	if markerErr == nil {
		if !bytes.Equal(storedMarker, marker) {
			return nil, ErrBFTSigningJournalBindingMismatch
		}
		if _, err := os.Lstat(path); err != nil {
			return nil, fmt.Errorf("%w: initialized journal is missing or inaccessible: %v", ErrBFTSigningJournalCorrupt, err)
		}
	}
	journal, err := OpenBFTSigningJournal(path, binding)
	if err != nil {
		return nil, err
	}
	// Materialize even an empty journal before its initialization marker. A
	// crash between these writes leaves a valid journal that can be reopened.
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if err := journal.persistLocked(journal.records); err != nil {
			return nil, err
		}
	}
	if errors.Is(markerErr, os.ErrNotExist) {
		if err := fileutil.WriteFileAtomicStrict(path+".binding", marker, bftSigningJournalFileMode); err != nil {
			return nil, err
		}
	}
	return journal, nil
}

// CloseSigningJournal releases file ownership and permanently blocks further
// signing on this executor. A new executor must reopen and verify the journal.
func (e *BFTExecutor) CloseSigningJournal() error {
	if e == nil {
		return nil
	}
	e.signingMu.Lock()
	defer e.signingMu.Unlock()
	if !e.signingJournalRequired {
		return nil
	}
	e.signingJournalErr = fmt.Errorf("%w: journal closed", ErrBFTSigningUnavailable)
	if e.signingJournalLock == nil {
		return nil
	}
	err := e.signingJournalLock.Close()
	e.signingJournalLock = nil
	return err
}

// prepareOutbound serializes signer selection, reservation, signing and durable
// envelope recording. The publisher runs after this lock is released, so it
// can safely call back into the executor. A retry reuses the exact saved bytes.
func (e *BFTExecutor) prepareOutbound(intent BFTSigningIntent, encode func(BFTSigner) ([]byte, error)) ([]byte, error) {
	e.signingMu.Lock()
	defer e.signingMu.Unlock()
	signer := e.VoteSigner()
	if !e.signingJournalRequired {
		e.signingStarted = e.signingStarted || signer != nil
		return encode(signer)
	}
	if e.signingJournalErr != nil {
		return nil, e.signingJournalErr
	}
	journal := e.signingJournal
	if journal == nil {
		return nil, fmt.Errorf("%w: journal not configured", ErrBFTSigningUnavailable)
	}
	if signer == nil || BFTValidatorAddress(signer.GetPublicKey()) != journal.binding.SignerAddress {
		return nil, fmt.Errorf("%w: %w", ErrBFTSigningUnavailable, ErrBFTSigningJournalBindingMismatch)
	}
	policy := e.MembershipPolicy()
	if !journal.binding.AllowLegacyMembership && (policy == nil || !policy.EnabledAt(intent.Height)) {
		return nil, ErrBFTMembershipUnknown
	}
	if policy != nil && policy.EnabledAt(intent.Height) {
		if policy.NetworkID() != journal.binding.NetworkID {
			return nil, fmt.Errorf("%w: membership network changed", ErrBFTSigningJournalBindingMismatch)
		}
		if err := policy.validateMemberKey(intent.Height, intent.MembershipRoot, intent.Validator, signer.GetPublicKey()); err != nil {
			return nil, err
		}
	}
	record, _, err := journal.Reserve(intent)
	if err != nil {
		return nil, e.failJournalLocked(err)
	}
	if record.SignedEnvelope != "" {
		return []byte(record.SignedEnvelope), nil
	}
	e.signingStarted = true
	payload, err := encode(signer)
	if err != nil {
		return nil, err
	}
	if _, _, err := journal.MarkSigned(intent, payload); err != nil {
		return nil, e.failJournalLocked(err)
	}
	return payload, nil
}

func (e *BFTExecutor) failJournalLocked(err error) error {
	// Any ambiguous persistence failure requires reopening from disk. Never
	// overwrite a possibly durable reservation using stale in-memory state.
	e.signingJournalErr = fmt.Errorf("%w: %w", ErrBFTSigningUnavailable, err)
	return e.signingJournalErr
}
