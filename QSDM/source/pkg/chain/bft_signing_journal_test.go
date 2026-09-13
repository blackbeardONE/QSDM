package chain

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestBFTSigningJournalReserveDurableAndIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bft-signing-journal.json")
	binding := testBFTSigningJournalBinding()
	journal, err := OpenBFTSigningJournal(path, binding)
	if err != nil {
		t.Fatal(err)
	}
	intent := testBFTSigningJournalIntent(binding, BFTWirePrevote)
	reserved, created, err := journal.Reserve(intent)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first reservation must be created")
	}
	if reserved.Digest == "" || reserved.ReservedAt.IsZero() {
		t.Fatalf("reservation is incomplete: %+v", reserved)
	}

	again, created, err := journal.Reserve(intent)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("same reservation must be idempotent")
	}
	if again != reserved {
		t.Fatalf("idempotent reservation = %+v, want %+v", again, reserved)
	}

	reopened, err := OpenBFTSigningJournal(path, binding)
	if err != nil {
		t.Fatal(err)
	}
	records := reopened.Records()
	if len(records) != 1 || records[0] != reserved {
		t.Fatalf("reopened records = %+v, want [%+v]", records, reserved)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != bftSigningJournalFileMode {
			t.Fatalf("journal mode = %o, want %o", got, bftSigningJournalFileMode)
		}
	}
}

func TestBFTSigningJournalRefusesConflictingValues(t *testing.T) {
	binding := testBFTSigningJournalBinding()
	journal, err := OpenBFTSigningJournal(filepath.Join(t.TempDir(), "journal.json"), binding)
	if err != nil {
		t.Fatal(err)
	}
	intent := testBFTSigningJournalIntent(binding, BFTWirePropose)
	if _, _, err := journal.Reserve(intent); err != nil {
		t.Fatal(err)
	}

	for name, conflicting := range map[string]BFTSigningIntent{
		"block hash": func() BFTSigningIntent {
			value := intent
			value.BlockHash = strings.Repeat("f", 64)
			return value
		}(),
		"membership root": func() BFTSigningIntent {
			value := intent
			value.MembershipRoot = strings.Repeat("a", 64)
			return value
		}(),
		"proposal body": func() BFTSigningIntent {
			value := intent
			value.ProposalBodyHash = strings.Repeat("b", 64)
			return value
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := journal.Reserve(conflicting); !errors.Is(err, ErrBFTSigningJournalConflict) {
				t.Fatalf("Reserve() error = %v, want signing conflict", err)
			}
		})
	}
	if records := journal.Records(); len(records) != 1 {
		t.Fatalf("conflicting reservations changed journal: %+v", records)
	}
}

func TestBFTSigningJournalConcurrentConflictsFailClosed(t *testing.T) {
	binding := testBFTSigningJournalBinding()
	journal, err := OpenBFTSigningJournal(filepath.Join(t.TempDir(), "journal.json"), binding)
	if err != nil {
		t.Fatal(err)
	}
	first := testBFTSigningJournalIntent(binding, BFTWirePrevote)
	second := first
	second.BlockHash = strings.Repeat("a", 64)

	type result struct {
		created bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var workers sync.WaitGroup
	for _, intent := range []BFTSigningIntent{first, second} {
		intent := intent
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, created, err := journal.Reserve(intent)
			results <- result{created: created, err: err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	created := 0
	conflicts := 0
	for result := range results {
		switch {
		case result.err == nil && result.created:
			created++
		case errors.Is(result.err, ErrBFTSigningJournalConflict):
			conflicts++
		default:
			t.Fatalf("concurrent Reserve() result = %+v, want created or conflict", result)
		}
	}
	if created != 1 || conflicts != 1 {
		t.Fatalf("concurrent Reserve() created=%d conflicts=%d, want 1 each", created, conflicts)
	}
	if records := journal.Records(); len(records) != 1 {
		t.Fatalf("concurrent conflicts wrote %d records, want 1: %+v", len(records), records)
	}
}
func TestBFTSigningJournalRefusesToPersistPastCapacity(t *testing.T) {
	binding := testBFTSigningJournalBinding()
	journal, err := OpenBFTSigningJournal(filepath.Join(t.TempDir(), "journal.json"), binding)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maxBFTSigningJournalRecords; index++ {
		journal.records[bftSigningJournalKey{
			Kind:      BFTWirePrevote,
			Height:    uint64(index + 1),
			Validator: binding.SignerAddress,
		}] = BFTSigningJournalRecord{}
	}
	intent := testBFTSigningJournalIntent(binding, BFTWirePrevote)
	intent.Height = uint64(maxBFTSigningJournalRecords + 1)
	if _, _, err := journal.Reserve(intent); !errors.Is(err, ErrBFTSigningJournalFull) {
		t.Fatalf("Reserve() at capacity error = %v, want journal full", err)
	}
	if got := len(journal.records); got != maxBFTSigningJournalRecords {
		t.Fatalf("Reserve() at capacity changed records to %d, want %d", got, maxBFTSigningJournalRecords)
	}

	overCapacity := make(map[bftSigningJournalKey]BFTSigningJournalRecord, maxBFTSigningJournalRecords+1)
	for index := 0; index <= maxBFTSigningJournalRecords; index++ {
		overCapacity[bftSigningJournalKey{
			Kind:      BFTWirePrevote,
			Height:    uint64(index + 1),
			Validator: binding.SignerAddress,
		}] = BFTSigningJournalRecord{}
	}
	if err := journal.persistLocked(overCapacity); !errors.Is(err, ErrBFTSigningJournalFull) {
		t.Fatalf("persistLocked() over capacity error = %v, want journal full", err)
	}
}
func TestBFTSigningJournalRequiresReservationBeforeSignedRecord(t *testing.T) {
	dir := t.TempDir()
	signer, _, err := LoadOrCreateBFTSigner(filepath.Join(dir, "consensus-signer.json"))
	if err != nil {
		t.Fatal(err)
	}
	binding, err := NewBFTSigningJournalBinding(
		"qsdm-test-network",
		strings.Repeat("b", 64),
		strings.Repeat("c", 64),
		signer,
	)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := OpenBFTSigningJournal(filepath.Join(dir, "journal.json"), binding)
	if err != nil {
		t.Fatal(err)
	}
	intent := testBFTSigningJournalIntent(binding, BFTWirePrecommit)
	if _, _, err := journal.MarkSigned(intent, []byte("signed-wire")); !errors.Is(err, ErrBFTSigningJournalUnreserved) {
		t.Fatalf("MarkSigned() before Reserve error = %v, want unreserved", err)
	}
	if _, _, err := journal.Reserve(intent); err != nil {
		t.Fatal(err)
	}
	if _, _, err := journal.MarkSigned(intent, []byte("not a BFT wire envelope")); !errors.Is(err, ErrBFTSigningJournalEnvelopeInvalid) {
		t.Fatalf("MarkSigned() malformed envelope error = %v, want invalid envelope", err)
	}
	otherSigner, _, err := LoadOrCreateBFTSigner(filepath.Join(dir, "other-consensus-signer.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := journal.MarkSigned(intent, testBFTSigningJournalSignedEnvelope(t, intent, otherSigner)); !errors.Is(err, ErrBFTSigningJournalEnvelopeInvalid) {
		t.Fatalf("MarkSigned() wrong signer error = %v, want invalid envelope", err)
	}

	envelope := testBFTSigningJournalSignedEnvelope(t, intent, signer)
	first, created, err := journal.MarkSigned(intent, envelope)
	if err != nil {
		t.Fatal(err)
	}
	if !created || first.SignedAt.IsZero() || first.SignedEnvelopeHash == "" {
		t.Fatalf("first signed record = %+v, created=%t", first, created)
	}
	second, created, err := journal.MarkSigned(intent, envelope)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("a re-sign of the same reserved intent must not overwrite audit data")
	}
	if second != first {
		t.Fatalf("second signed record = %+v, want %+v", second, first)
	}

	conflicting := intent
	conflicting.BlockHash = strings.Repeat("a", 64)
	if _, _, err := journal.MarkSigned(intent, testBFTSigningJournalSignedEnvelope(t, conflicting, signer)); !errors.Is(err, ErrBFTSigningJournalConflict) {
		t.Fatalf("MarkSigned() changed value error = %v, want signing conflict", err)
	}
	if got := journal.Records(); len(got) != 1 || got[0] != first {
		t.Fatalf("invalid MarkSigned attempts changed journal: %+v", got)
	}
}

func testBFTSigningJournalSignedEnvelope(t *testing.T, intent BFTSigningIntent, signer BFTSigner) []byte {
	t.Helper()
	switch intent.Kind {
	case BFTWirePropose:
		var block *Block
		if intent.ProposalBodyHash != "" {
			block = &Block{Hash: intent.ProposalBodyHash}
		}
		message := BFTWireProposeMsg{
			Height:         intent.Height,
			Round:          intent.Round,
			Proposer:       intent.Validator,
			BlockHash:      intent.BlockHash,
			MembershipRoot: intent.MembershipRoot,
			Block:          block,
		}
		if err := SignPropose(&message, signer); err != nil {
			t.Fatal(err)
		}
		envelope, err := MarshalBFTWire(BFTWirePropose, message)
		if err != nil {
			t.Fatal(err)
		}
		return envelope
	case BFTWirePrevote:
		message := BFTWirePrevoteMsg{
			Height:         intent.Height,
			Round:          intent.Round,
			Validator:      intent.Validator,
			BlockHash:      intent.BlockHash,
			MembershipRoot: intent.MembershipRoot,
		}
		if err := SignPrevote(&message, signer); err != nil {
			t.Fatal(err)
		}
		envelope, err := MarshalBFTWire(BFTWirePrevote, message)
		if err != nil {
			t.Fatal(err)
		}
		return envelope
	case BFTWirePrecommit:
		message := BFTWirePrecommitMsg{
			Height:         intent.Height,
			Round:          intent.Round,
			Validator:      intent.Validator,
			BlockHash:      intent.BlockHash,
			MembershipRoot: intent.MembershipRoot,
		}
		if err := SignPrecommit(&message, signer); err != nil {
			t.Fatal(err)
		}
		envelope, err := MarshalBFTWire(BFTWirePrecommit, message)
		if err != nil {
			t.Fatal(err)
		}
		return envelope
	default:
		t.Fatalf("unsupported test BFT wire kind %q", intent.Kind)
		return nil
	}
}

func TestBFTSigningJournalAcceptsVerifiedEnvelopeForEveryVoteKind(t *testing.T) {
	dir := t.TempDir()
	signer, _, err := LoadOrCreateBFTSigner(filepath.Join(dir, "consensus-signer.json"))
	if err != nil {
		t.Fatal(err)
	}
	binding, err := NewBFTSigningJournalBinding(
		"qsdm-test-network",
		strings.Repeat("b", 64),
		strings.Repeat("c", 64),
		signer,
	)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := OpenBFTSigningJournal(filepath.Join(dir, "journal.json"), binding)
	if err != nil {
		t.Fatal(err)
	}

	for _, kind := range []string{BFTWirePropose, BFTWirePrevote, BFTWirePrecommit} {
		t.Run(kind, func(t *testing.T) {
			intent := testBFTSigningJournalIntent(binding, kind)
			if _, _, err := journal.Reserve(intent); err != nil {
				t.Fatal(err)
			}
			record, created, err := journal.MarkSigned(intent, testBFTSigningJournalSignedEnvelope(t, intent, signer))
			if err != nil {
				t.Fatal(err)
			}
			if !created || record.SignedEnvelopeHash == "" || record.SignedAt.IsZero() {
				t.Fatalf("MarkSigned() = %+v, created=%t", record, created)
			}
		})
	}

	reopened, err := OpenBFTSigningJournal(filepath.Join(dir, "journal.json"), binding)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Records(); len(got) != 3 {
		t.Fatalf("reopened signed records = %+v, want 3", got)
	}
}
func TestBFTSigningJournalRejectsMismatchedOrCorruptFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	binding := testBFTSigningJournalBinding()
	journal, err := OpenBFTSigningJournal(path, binding)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := journal.Reserve(testBFTSigningJournalIntent(binding, BFTWirePrevote)); err != nil {
		t.Fatal(err)
	}

	wrongBinding := binding
	wrongBinding.ChainID = strings.Repeat("9", 64)
	if _, err := OpenBFTSigningJournal(path, wrongBinding); !errors.Is(err, ErrBFTSigningJournalBindingMismatch) {
		t.Fatalf("OpenBFTSigningJournal() wrong binding error = %v, want binding mismatch", err)
	}

	if err := os.WriteFile(path, []byte(`{"version":1,"unexpected":true}`), bftSigningJournalFileMode); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenBFTSigningJournal(path, binding); !errors.Is(err, ErrBFTSigningJournalCorrupt) {
		t.Fatalf("OpenBFTSigningJournal() corrupt file error = %v, want corruption", err)
	}
}

func TestBFTSigningIntentHelpersMatchBFTMessageMeaning(t *testing.T) {
	binding := testBFTSigningJournalBinding()
	proposal := BFTWireProposeMsg{
		Height:         42,
		Round:          3,
		Proposer:       binding.SignerAddress,
		BlockHash:      strings.Repeat("e", 64),
		MembershipRoot: strings.Repeat("d", 64),
		Block:          &Block{Hash: strings.Repeat("c", 64)},
	}
	first := BFTSigningIntentForPropose(proposal)
	if _, err := first.validate(binding); err != nil {
		t.Fatal(err)
	}
	proposal.Block = &Block{Hash: strings.Repeat("b", 64)}
	second := BFTSigningIntentForPropose(proposal)
	if first == second {
		t.Fatal("proposal body change must change the signing intent")
	}

	prevote := BFTSigningIntentForPrevote(BFTWirePrevoteMsg{
		Height:         42,
		Round:          3,
		Validator:      binding.SignerAddress,
		BlockHash:      strings.Repeat("e", 64),
		MembershipRoot: strings.Repeat("d", 64),
	})
	if _, err := prevote.validate(binding); err != nil {
		t.Fatal(err)
	}
	if prevote.ProposalBodyHash != "" {
		t.Fatalf("prevote body hash = %q, want empty", prevote.ProposalBodyHash)
	}
}

type testBFTSigningJournalSigner struct {
	publicKey []byte
}

func (s testBFTSigningJournalSigner) Sign([]byte) ([]byte, error) { return []byte("signature"), nil }
func (s testBFTSigningJournalSigner) GetPublicKey() []byte {
	return append([]byte(nil), s.publicKey...)
}

func TestNewBFTSigningJournalBindingUsesSignerIdentity(t *testing.T) {
	signer := testBFTSigningJournalSigner{publicKey: []byte("journal-test-public-key")}
	binding, err := NewBFTSigningJournalBinding("qsdm-test", strings.Repeat("1", 64), strings.Repeat("2", 64), signer)
	if err != nil {
		t.Fatal(err)
	}
	want := BFTValidatorAddress(signer.publicKey)
	if binding.SignerAddress != want || binding.SignerPublicKeyFingerprint != want {
		t.Fatalf("binding identity = %+v, want %s", binding, want)
	}
}

func testBFTSigningJournalBinding() BFTSigningJournalBinding {
	identity := strings.Repeat("a", 64)
	return BFTSigningJournalBinding{
		NetworkID:                  "qsdm-test-network",
		ChainID:                    strings.Repeat("b", 64),
		SignerAddress:              identity,
		SignerPublicKeyFingerprint: identity,
		ConsensusConfigFingerprint: strings.Repeat("c", 64),
	}
}

func testBFTSigningJournalIntent(binding BFTSigningJournalBinding, kind string) BFTSigningIntent {
	intent := BFTSigningIntent{
		Kind:           kind,
		Height:         77,
		Round:          4,
		Validator:      binding.SignerAddress,
		BlockHash:      strings.Repeat("e", 64),
		MembershipRoot: strings.Repeat("d", 64),
	}
	if kind == BFTWirePropose {
		intent.ProposalBodyHash = strings.Repeat("f", 64)
	}
	return intent
}
