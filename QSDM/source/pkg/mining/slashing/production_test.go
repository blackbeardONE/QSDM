package slashing_test

// Smoke tests for NewProductionDispatcher. Behavioural coverage
// of forgedattest.Verifier itself lives in
// pkg/mining/slashing/forgedattest/forgedattest_test.go; here
// we only assert the wiring contract: every reserved
// EvidenceKind dispatches, the configured cap propagates, and
// missing required collaborators are rejected with a specific
// error.

import (
	"errors"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
)

// fakeFAVerifier implements slashing.EvidenceVerifier with a
// configurable Kind so we can test both the happy path and the
// "wrong kind injected" failure mode of production validation.
type fakeFAVerifier struct {
	kind slashing.EvidenceKind
}

func (f fakeFAVerifier) Kind() slashing.EvidenceKind { return f.kind }
func (f fakeFAVerifier) Verify(_ slashing.SlashPayload, _ uint64) (uint64, error) {
	return 1, nil
}

func TestNewProductionDispatcher_RejectsMissingFAVerifier(t *testing.T) {
	t.Parallel()
	_, err := slashing.NewProductionDispatcher(slashing.ProductionConfig{})
	if err == nil {
		t.Fatalf("expected error on missing ForgedAttestation")
	}
	if !strings.Contains(err.Error(), "ForgedAttestation is required") {
		t.Fatalf("error %v does not name the missing field", err)
	}
}

func TestNewProductionDispatcher_RejectsWrongKindFAVerifier(t *testing.T) {
	t.Parallel()
	_, err := slashing.NewProductionDispatcher(slashing.ProductionConfig{
		ForgedAttestation: fakeFAVerifier{kind: slashing.EvidenceKindDoubleMining},
	})
	if err == nil {
		t.Fatalf("expected error when FA verifier reports wrong Kind")
	}
}

func TestNewProductionDispatcher_RegistersAllReservedKinds(t *testing.T) {
	t.Parallel()
	d, err := slashing.NewProductionDispatcher(slashing.ProductionConfig{
		ForgedAttestation: fakeFAVerifier{kind: slashing.EvidenceKindForgedAttestation},
	})
	if err != nil {
		t.Fatalf("NewProductionDispatcher: %v", err)
	}

	got := d.Kinds()
	if len(got) != len(slashing.AllEvidenceKinds) {
		t.Fatalf("kinds count = %d, want %d (%v vs %v)",
			len(got), len(slashing.AllEvidenceKinds),
			got, slashing.AllEvidenceKinds)
	}

	gotSet := make(map[slashing.EvidenceKind]bool, len(got))
	for _, k := range got {
		gotSet[k] = true
	}
	for _, want := range slashing.AllEvidenceKinds {
		if !gotSet[want] {
			t.Errorf("missing kind %q in production dispatcher", want)
		}
	}
}

// TestProductionDispatcher_StubKindsRejectWithErrEvidenceVerification
// asserts the wire reservation contract: the deferred kinds
// route through StubVerifier, which returns
// ErrEvidenceVerification — not ErrUnknownEvidenceKind. This is
// the difference between "the chain knows this kind exists but
// hasn't shipped a verifier yet" and "the chain has no idea
// what this kind is."
//
// At this revision both ForgedAttestation and DoubleMining have
// concrete verifiers; only FreshnessCheat remains stubbed. We
// exercise the stub posture explicitly by leaving DoubleMining
// nil so the dispatcher falls back to its StubVerifier.
func TestProductionDispatcher_StubKindsRejectWithErrEvidenceVerification(t *testing.T) {
	t.Parallel()
	d, err := slashing.NewProductionDispatcher(slashing.ProductionConfig{
		ForgedAttestation: fakeFAVerifier{kind: slashing.EvidenceKindForgedAttestation},
	})
	if err != nil {
		t.Fatalf("NewProductionDispatcher: %v", err)
	}

	for _, k := range []slashing.EvidenceKind{
		slashing.EvidenceKindDoubleMining,
		slashing.EvidenceKindFreshnessCheat,
	} {
		_, verr := d.Verify(slashing.SlashPayload{
			NodeID:       "any",
			EvidenceKind: k,
			EvidenceBlob: []byte("{}"),
		}, 0)
		if verr == nil {
			t.Errorf("kind %q: stub verifier accepted", k)
			continue
		}
		if !errors.Is(verr, slashing.ErrEvidenceVerification) {
			t.Errorf("kind %q: error %v does not wrap ErrEvidenceVerification", k, verr)
		}
	}
}

// fakeDMVerifier mirrors fakeFAVerifier but for double-mining,
// used to exercise the optional DoubleMining slot.
type fakeDMVerifier struct {
	kind slashing.EvidenceKind
	cap_ uint64
}

func (f fakeDMVerifier) Kind() slashing.EvidenceKind { return f.kind }
func (f fakeDMVerifier) Verify(_ slashing.SlashPayload, _ uint64) (uint64, error) {
	return f.cap_, nil
}

// TestNewProductionDispatcher_RejectsWrongKindDMVerifier asserts
// that an injected DoubleMining slot whose Kind() lies is
// rejected at boot — silent kind-mismatch on the consensus path
// would be a determinism bug.
func TestNewProductionDispatcher_RejectsWrongKindDMVerifier(t *testing.T) {
	t.Parallel()
	_, err := slashing.NewProductionDispatcher(slashing.ProductionConfig{
		ForgedAttestation: fakeFAVerifier{kind: slashing.EvidenceKindForgedAttestation},
		DoubleMining:      fakeDMVerifier{kind: slashing.EvidenceKindForgedAttestation},
	})
	if err == nil {
		t.Fatalf("expected error when DM verifier reports wrong Kind")
	}
}

// TestProductionDispatcher_RoutesInjectedDM asserts that an
// injected double-mining verifier is reached through the
// dispatcher (i.e. it overrides the default StubVerifier).
func TestProductionDispatcher_RoutesInjectedDM(t *testing.T) {
	t.Parallel()
	const wantCap uint64 = 4_242
	d, err := slashing.NewProductionDispatcher(slashing.ProductionConfig{
		ForgedAttestation: fakeFAVerifier{kind: slashing.EvidenceKindForgedAttestation},
		DoubleMining: fakeDMVerifier{
			kind: slashing.EvidenceKindDoubleMining,
			cap_: wantCap,
		},
	})
	if err != nil {
		t.Fatalf("NewProductionDispatcher: %v", err)
	}
	got, verr := d.Verify(slashing.SlashPayload{
		NodeID:       "any",
		EvidenceKind: slashing.EvidenceKindDoubleMining,
		EvidenceBlob: []byte("{}"),
	}, 0)
	if verr != nil {
		t.Fatalf("expected injected DM verifier to accept, got %v", verr)
	}
	if got != wantCap {
		t.Fatalf("dispatcher returned cap %d, want %d (StubVerifier may have shadowed the injected one)",
			got, wantCap)
	}
}
