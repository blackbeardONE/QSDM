package chain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type journalTestSigner struct {
	BFTSigner
	calls  atomic.Int32
	before func([]byte)
	after  func()
}

func (s *journalTestSigner) Sign(digest []byte) ([]byte, error) {
	s.calls.Add(1)
	if s.before != nil {
		s.before(digest)
	}
	signature, err := s.BFTSigner.Sign(digest)
	if s.after != nil {
		s.after()
	}
	return signature, err
}

func journalExecutorFixture(t *testing.T, dir string) (*BFTExecutor, *journalTestSigner, BFTSigningJournalBinding) {
	t.Helper()
	key, _, err := LoadOrCreateBFTSigner(filepath.Join(dir, "key.json"))
	if err != nil {
		t.Fatal(err)
	}
	signer := &journalTestSigner{BFTSigner: key}
	binding, err := NewBFTSigningJournalBinding("test-network", "test-genesis", strings.Repeat("c", 64), signer)
	if err != nil {
		t.Fatal(err)
	}
	binding.AllowLegacyMembership = true
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	if err := vs.Register(binding.SignerAddress, 100); err != nil {
		t.Fatal(err)
	}
	e := NewBFTExecutor(NewBFTConsensus(vs, DefaultConsensusConfig()))
	e.SetVoteSigner(signer)
	t.Cleanup(func() { _ = e.CloseSigningJournal() })
	return e, signer, binding
}

func journalBroadcast(e *BFTExecutor, kind, address, value string) error {
	switch kind {
	case BFTWirePropose:
		return e.BroadcastPropose(7, 2, address, value, nil)
	case BFTWirePrevote:
		return e.BroadcastPrevote(7, 2, address, value)
	default:
		return e.BroadcastPrecommit(7, 2, address, value)
	}
}

func TestBFTExecutorJournalWriteAheadAndExactRestartRetry(t *testing.T) {
	for _, kind := range []string{BFTWirePropose, BFTWirePrevote, BFTWirePrecommit} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "journal.json")
			e, signer, binding := journalExecutorFixture(t, dir)
			e.RequireSigningJournal()
			if err := journalBroadcast(e, kind, binding.SignerAddress, "root"); !errors.Is(err, ErrBFTSigningUnavailable) {
				t.Fatalf("unconfigured journal: %v", err)
			}
			if signer.calls.Load() != 0 {
				t.Fatal("signed before journal was configured")
			}
			if err := e.ConfigureSigningJournal(path, binding); err != nil {
				t.Fatal(err)
			}
			signer.before = func(digest []byte) {
				journal, err := OpenBFTSigningJournal(path, binding)
				if err != nil {
					t.Fatal(err)
				}
				records := journal.Records()
				if len(records) != 1 || records[0].Digest != hex.EncodeToString(digest) || records[0].SignedEnvelope != "" {
					t.Fatalf("signer did not observe its durable reservation: %+v", records)
				}
			}
			var published []byte
			networkErr := errors.New("publisher unavailable")
			e.SetPublisher(func(payload []byte) error {
				journal, err := OpenBFTSigningJournal(path, binding)
				if err != nil {
					t.Fatal(err)
				}
				if records := journal.Records(); len(records) != 1 || records[0].SignedEnvelope != string(payload) {
					t.Fatalf("published before envelope was durable: %+v", records)
				}
				published = append([]byte(nil), payload...)
				return networkErr
			})
			if err := journalBroadcast(e, kind, binding.SignerAddress, "root"); !errors.Is(err, networkErr) {
				t.Fatalf("publish error: %v", err)
			}
			if err := journalBroadcast(e, kind, binding.SignerAddress, "root"); !errors.Is(err, networkErr) || signer.calls.Load() != 1 {
				t.Fatalf("retry resigned or lost publisher error: %v, calls=%d", err, signer.calls.Load())
			}
			if err := e.CloseSigningJournal(); err != nil {
				t.Fatal(err)
			}
			if err := journalBroadcast(e, kind, binding.SignerAddress, "root"); !errors.Is(err, ErrBFTSigningUnavailable) {
				t.Fatalf("closed executor signed: %v", err)
			}
			restarted, restartedSigner, _ := journalExecutorFixture(t, dir)
			if err := restarted.ConfigureSigningJournal(path, binding); err != nil {
				t.Fatal(err)
			}
			restarted.SetPublisher(func(payload []byte) error {
				if !bytes.Equal(payload, published) {
					t.Fatal("restart changed the reserved envelope")
				}
				return nil
			})
			if err := journalBroadcast(restarted, kind, binding.SignerAddress, "root"); err != nil {
				t.Fatal(err)
			}
			if kind == BFTWirePropose {
				if _, ok := restarted.lookupProposeExhibit(7, 2, binding.SignerAddress); !ok {
					t.Fatal("recovered proposal lost its signed exhibit")
				}
			}
			if err := journalBroadcast(restarted, kind, binding.SignerAddress, "conflict"); !errors.Is(err, ErrBFTSigningJournalConflict) {
				t.Fatalf("conflicting restart vote: %v", err)
			}
			if restartedSigner.calls.Load() != 0 {
				t.Fatal("restart invoked the signer for a saved or conflicting vote")
			}
		})
	}
}

func TestBFTExecutorJournalFailuresBlockSigning(t *testing.T) {
	for _, stage := range []string{"reserve", "mark-signed", "missing", "corrupt", "binding", "nil-signer", "changed-signer", "locked"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "journal.json")
			e, signer, binding := journalExecutorFixture(t, dir)
			if err := e.ConfigureSigningJournal(path, binding); err != nil {
				t.Fatal(err)
			}
			e.SetPublisher(func([]byte) error { t.Fatal("unsafe payload reached publisher"); return nil })
			switch stage {
			case "reserve":
				e.signingJournal.path = filepath.Join(dir, "unavailable", "journal.json")
			case "mark-signed":
				signer.after = func() { e.signingJournal.path = filepath.Join(dir, "unavailable", "journal.json") }
			case "nil-signer":
				e.SetVoteSigner(nil)
			case "changed-signer":
				_, other, _ := journalExecutorFixture(t, t.TempDir())
				e.SetVoteSigner(other)
			default:
				if stage != "locked" {
					if err := e.CloseSigningJournal(); err != nil {
						t.Fatal(err)
					}
				}
				switch stage {
				case "missing":
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				case "corrupt":
					if err := os.WriteFile(path, []byte("torn journal"), 0o600); err != nil {
						t.Fatal(err)
					}
				case "binding":
					binding.ChainID = "another-genesis"
				}
				other, otherSigner, _ := journalExecutorFixture(t, dir)
				if err := other.ConfigureSigningJournal(path, binding); err == nil {
					t.Fatal("unsafe journal opened")
				}
				e, signer = other, otherSigner
			}
			if err := e.BroadcastPrevote(7, 2, binding.SignerAddress, "root"); err == nil {
				t.Fatal("unsafe broadcast succeeded")
			}
			wantCalls := int32(0)
			if stage == "mark-signed" {
				wantCalls = 1
			}
			if signer.calls.Load() != wantCalls {
				t.Fatalf("signer calls=%d, want %d", signer.calls.Load(), wantCalls)
			}
			if stage == "reserve" || stage == "mark-signed" {
				e.signingJournal.path = path
				if err := e.BroadcastPrevote(8, 0, binding.SignerAddress, "new-root"); !errors.Is(err, ErrBFTSigningUnavailable) {
					t.Fatalf("persistence fault did not remain fail-closed: %v", err)
				}
			}
		})
	}
}

func TestBFTExecutorJournalConcurrentConflictingBroadcasts(t *testing.T) {
	dir := t.TempDir()
	e, signer, binding := journalExecutorFixture(t, dir)
	if err := e.ConfigureSigningJournal(filepath.Join(dir, "journal.json"), binding); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errorsOut := make(chan error, 2)
	for _, value := range []string{"first", "second"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errorsOut <- e.BroadcastPrevote(7, 2, binding.SignerAddress, value)
		}()
	}
	wg.Wait()
	close(errorsOut)
	accepted, conflicts := 0, 0
	for err := range errorsOut {
		if err == nil {
			accepted++
		} else if errors.Is(err, ErrBFTSigningJournalConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 1 || conflicts != 1 || signer.calls.Load() != 1 {
		t.Fatalf("accepted=%d conflicts=%d signatures=%d", accepted, conflicts, signer.calls.Load())
	}
}

func TestBFTExecutorJournalMembershipCheckedBeforeSigning(t *testing.T) {
	for _, scenario := range []string{"root-changed", "outsider", "network-changed"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			e, signer, binding := journalExecutorFixture(t, dir)
			_, other, _ := journalExecutorFixture(t, t.TempDir())
			membership := membershipForBFTSigners(t, 0, signer)
			binding.NetworkID = membership.NetworkID
			binding.AllowLegacyMembership = false
			install := func(membership ConsensusMembership) {
				t.Helper()
				schedule, err := NewConsensusMembershipSchedule([]ConsensusMembership{membership})
				if err != nil {
					t.Fatal(err)
				}
				policy, err := NewBFTMembershipPolicy(schedule, 0)
				if err != nil {
					t.Fatal(err)
				}
				e.SetMembershipPolicy(policy)
			}
			install(membership)
			if err := e.ConfigureSigningJournal(filepath.Join(dir, "journal.json"), binding); err != nil {
				t.Fatal(err)
			}
			if scenario == "root-changed" {
				if err := e.BroadcastPrevote(7, 2, binding.SignerAddress, "root"); err != nil {
					t.Fatal(err)
				}
				install(membershipForBFTSigners(t, 0, signer, other))
			} else if scenario == "outsider" {
				install(membershipForBFTSigners(t, 0, other))
			} else {
				membership.NetworkID = "other-network"
				install(membership)
			}
			calls := signer.calls.Load()
			e.SetPublisher(func([]byte) error { t.Fatal("invalid membership was published"); return nil })
			if err := e.BroadcastPrevote(7, 2, binding.SignerAddress, "root"); err == nil {
				t.Fatal("changed membership authorized signing")
			}
			if signer.calls.Load() != calls {
				t.Fatal("invalid membership reached the signer")
			}
		})
	}
}

func TestBFTExecutorJournalCannotAttachAfterSigning(t *testing.T) {
	dir := t.TempDir()
	e, _, binding := journalExecutorFixture(t, dir)
	if err := e.BroadcastPrevote(7, 2, binding.SignerAddress, "root"); err != nil {
		t.Fatal(err)
	}
	if err := e.ConfigureSigningJournal(filepath.Join(dir, "journal.json"), binding); !errors.Is(err, ErrBFTSigningUnavailable) {
		t.Fatalf("journal attached after unjournaled signing: %v", err)
	}
	if err := e.BroadcastPrevote(7, 2, binding.SignerAddress, "conflict"); !errors.Is(err, ErrBFTSigningUnavailable) {
		t.Fatalf("failed attachment did not block signing: %v", err)
	}
}

func TestBFTExecutorJournalRestoreVerifiesSavedSignature(t *testing.T) {
	dir := t.TempDir()
	e, _, binding := journalExecutorFixture(t, dir)
	path := filepath.Join(dir, "journal.json")
	if err := e.ConfigureSigningJournal(path, binding); err != nil {
		t.Fatal(err)
	}
	if err := e.BroadcastPrevote(7, 2, binding.SignerAddress, "root"); err != nil {
		t.Fatal(err)
	}
	if err := e.CloseSigningJournal(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file bftSigningJournalFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	_, payload, err := UnmarshalBFTWire([]byte(file.Records[0].SignedEnvelope))
	if err != nil {
		t.Fatal(err)
	}
	var vote BFTWirePrevoteMsg
	if err := json.Unmarshal(payload, &vote); err != nil {
		t.Fatal(err)
	}
	vote.Auth.Signature[0] ^= 1
	corrupt, err := MarshalBFTWire(BFTWirePrevote, vote)
	if err != nil {
		t.Fatal(err)
	}
	file.Records[0].SignedEnvelope = string(corrupt)
	sum := sha256.Sum256(corrupt)
	file.Records[0].SignedEnvelopeHash = hex.EncodeToString(sum[:])
	// Recomputing both public checksums must not bypass signature verification.
	raw, err = marshalBFTSigningJournal(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	restarted, signer, _ := journalExecutorFixture(t, dir)
	if err := restarted.ConfigureSigningJournal(path, binding); !errors.Is(err, ErrBFTSigningJournalCorrupt) {
		t.Fatalf("tampered signature restored: %v", err)
	}
	if err := restarted.BroadcastPrevote(7, 2, binding.SignerAddress, "root"); !errors.Is(err, ErrBFTSigningUnavailable) {
		t.Fatalf("corruption did not block signing: %v", err)
	}
	if signer.calls.Load() != 0 {
		t.Fatal("corrupt recovery invoked signer")
	}
}

func TestBFTExecutorJournalRefusesOversizedEnvelopeAndOldSchema(t *testing.T) {
	dir := t.TempDir()
	e, _, binding := journalExecutorFixture(t, dir)
	path := filepath.Join(dir, "journal.json")
	if err := e.ConfigureSigningJournal(path, binding); err != nil {
		t.Fatal(err)
	}
	if err := e.BroadcastPrevote(7, 2, binding.SignerAddress, "root"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	record := e.signingJournal.Records()[0]
	record.SignedEnvelope = strings.Repeat("x", maxBFTSigningJournalBytes)
	candidate := map[bftSigningJournalKey]BFTSigningJournalRecord{record.Intent.key(): record}
	if err := e.signingJournal.persistLocked(candidate); !errors.Is(err, ErrBFTSigningJournalFull) {
		t.Fatalf("oversized journal persisted: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("capacity failure changed durable file")
	}
	if err := e.CloseSigningJournal(); err != nil {
		t.Fatal(err)
	}
	var file bftSigningJournalFile
	if err := json.Unmarshal(before, &file); err != nil {
		t.Fatal(err)
	}
	file.Version = 1
	file.Records[0].SignedEnvelope = ""
	raw, err := marshalBFTSigningJournal(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	restarted, _, _ := journalExecutorFixture(t, dir)
	if err := restarted.ConfigureSigningJournal(path, binding); !errors.Is(err, ErrBFTSigningJournalCorrupt) {
		t.Fatalf("hash-only schema was silently migrated: %v", err)
	}
}

func TestBFTExecutorJournalClockCorrectionDoesNotCorruptRecord(t *testing.T) {
	dir := t.TempDir()
	e, _, binding := journalExecutorFixture(t, dir)
	path := filepath.Join(dir, "journal.json")
	if err := e.ConfigureSigningJournal(path, binding); err != nil {
		t.Fatal(err)
	}
	intent := BFTSigningIntentForPrevote(BFTWirePrevoteMsg{Height: 7, Round: 2, Validator: binding.SignerAddress, BlockHash: "root"})
	record, _, err := e.signingJournal.Reserve(intent)
	if err != nil {
		t.Fatal(err)
	}
	record.ReservedAt = time.Now().Add(24 * time.Hour).UTC()
	e.signingJournal.records[intent.key()] = record
	if err := e.BroadcastPrevote(7, 2, binding.SignerAddress, "root"); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenBFTSigningJournal(path, binding); err != nil {
		t.Fatalf("clock correction made journal unreadable: %v", err)
	}
}

func TestBFTExecutorJournalSyntheticFailureCannotCommit(t *testing.T) {
	for _, failOn := range []int32{1, 2, 3} {
		dir := t.TempDir()
		e, signer, binding := journalExecutorFixture(t, dir)
		if err := e.ConfigureSigningJournal(filepath.Join(dir, "journal.json"), binding); err != nil {
			t.Fatal(err)
		}
		signer.after = func() {
			if signer.calls.Load() == failOn {
				e.signingJournal.path = filepath.Join(dir, "unavailable", "journal.json")
			}
		}
		block := &Block{Height: 1, StateRoot: "root"}
		block.Hash = ComputeBlockHash(block)
		if err := RunSyntheticBFTRoundWithExecutor(e, e.bc.validators, block); !errors.Is(err, ErrBFTSigningUnavailable) {
			t.Fatalf("journal failure at signature %d was ignored: %v", failOn, err)
		}
		if e.bc.IsCommitted(block.Height) {
			t.Fatalf("journal failure at signature %d committed locally", failOn)
		}
	}
}

func TestBFTExecutorJournalCrashRestart(t *testing.T) {
	for _, stage := range []string{"reserved", "signed", "publish"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(binary, "-test.run=^TestBFTExecutorJournalCrashHelper$")
			cmd.Env = append(os.Environ(), "QSDM_TEST_JOURNAL_CRASH="+stage, "QSDM_TEST_JOURNAL_DIR="+dir)
			output, err := cmd.CombinedOutput()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 23 {
				t.Fatalf("crash helper did not reach %s: %v\n%s", stage, err, output)
			}
			e, signer, binding := journalExecutorFixture(t, dir)
			path := filepath.Join(dir, "journal.json")
			if err := e.ConfigureSigningJournal(path, binding); err != nil {
				t.Fatal(err)
			}
			records := e.signingJournal.Records()
			if len(records) != 1 || (records[0].SignedEnvelope != "") != (stage == "publish") {
				t.Fatalf("unexpected durable state after %s: %+v", stage, records)
			}
			if err := e.BroadcastPrevote(7, 2, binding.SignerAddress, "root"); err != nil {
				t.Fatal(err)
			}
			if stage == "publish" && signer.calls.Load() != 0 {
				t.Fatal("resigned persisted envelope")
			}
			if err := e.BroadcastPrevote(7, 2, binding.SignerAddress, "conflict"); !errors.Is(err, ErrBFTSigningJournalConflict) {
				t.Fatalf("crash lost the original reservation: %v", err)
			}
		})
	}
}

func TestBFTExecutorJournalCrashHelper(t *testing.T) {
	stage := os.Getenv("QSDM_TEST_JOURNAL_CRASH")
	if stage == "" {
		return
	}
	dir := os.Getenv("QSDM_TEST_JOURNAL_DIR")
	e, signer, binding := journalExecutorFixture(t, dir)
	if err := e.ConfigureSigningJournal(filepath.Join(dir, "journal.json"), binding); err != nil {
		t.Fatal(err)
	}
	if stage == "reserved" {
		signer.before = func([]byte) { os.Exit(23) }
	}
	if stage == "signed" {
		signer.after = func() { os.Exit(23) }
	}
	if stage == "publish" {
		e.SetPublisher(func([]byte) error { os.Exit(23); return nil })
	}
	if err := e.BroadcastPrevote(7, 2, binding.SignerAddress, "root"); err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash stage was not reached")
}
