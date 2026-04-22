package mining

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"
)

// Stubs ------------------------------------------------------------

type fakeChain struct {
	tip     uint64
	headers map[uint64][32]byte
}

func (f *fakeChain) TipHeight() uint64 { return f.tip }
func (f *fakeChain) HeaderHashAt(h uint64) ([32]byte, bool) {
	v, ok := f.headers[h]
	return v, ok
}

type permissiveAddr struct{}

func (permissiveAddr) ValidateAddress(a string) error {
	if a == "" {
		return errors.New("empty address")
	}
	return nil
}

type goodBatches struct{}

func (goodBatches) ValidateBatch(_ Batch) error { return nil }

type fraudBatches struct{}

func (fraudBatches) ValidateBatch(_ Batch) error { return errors.New("forged content hash") }

// End-to-end happy path -------------------------------------------

func TestVerifyAcceptsValidProof(t *testing.T) {
	ws := makeWorkSet(t, 4)
	epoch := uint64(0)
	const dagN = 64
	dag, err := NewInMemoryDAG(epoch, ws.Root(), dagN)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}

	// Easy difficulty so the solver terminates quickly.
	difficulty := big.NewInt(2)
	target, err := TargetFromDifficulty(difficulty)
	if err != nil {
		t.Fatalf("target: %v", err)
	}

	headerHash := [32]byte{0x01, 0x23, 0x45}
	batchRoot, err := ws.PrefixRoot(1)
	if err != nil {
		t.Fatalf("prefix root: %v", err)
	}

	params := SolverParams{
		Epoch:      epoch,
		Height:     42,
		HeaderHash: headerHash,
		MinerAddr:  "qsdm1test",
		BatchRoot:  batchRoot,
		BatchCount: 1,
		Target:     target,
		DAG:        dag,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := Solve(ctx, params, nil, nil)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if res.Proof == nil {
		t.Fatal("solver returned nil proof")
	}

	cfg := VerifierConfig{
		EpochParams:      NewEpochParams(),
		DifficultyParams: NewDifficultyAdjusterParams(),
		Chain: &fakeChain{
			tip:     42,
			headers: map[uint64][32]byte{42: headerHash},
		},
		Addresses:       permissiveAddr{},
		Batches:         goodBatches{},
		Dedup:           NewProofIDSet(1024),
		Quarantine:      NewQuarantineSet(),
		DAGProvider:     func(e uint64) (DAG, error) { return dag, nil },
		WorkSetProvider: func(e uint64) (WorkSet, error) { return ws, nil },
		DifficultyAt:    func(h uint64) (*big.Int, error) { return difficulty, nil },
	}
	v, err := NewVerifier(cfg)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}

	raw, err := res.Proof.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical JSON: %v", err)
	}
	id, err := v.Verify(raw, 42)
	if err != nil {
		t.Fatalf("verify rejected valid proof: %v", err)
	}
	var zero [32]byte
	if id == zero {
		t.Fatal("verifier returned zero proof ID on success")
	}

	// Second submission must be detected as duplicate.
	_, err = v.Verify(raw, 42)
	if err == nil {
		t.Fatal("duplicate submission must be rejected")
	}
	var rej *RejectError
	if !errors.As(err, &rej) || rej.Reason != ReasonDuplicate {
		t.Fatalf("expected ReasonDuplicate, got %v", err)
	}
}

// Targeted rejection-path tests -----------------------------------

func TestVerifyRejectsWrongEpoch(t *testing.T) {
	v, raw, _ := buildMiniSetup(t)
	// Tamper: bump epoch by 1.
	raw = mutateField(raw, `"epoch":"0"`, `"epoch":"1"`)
	_, err := v.Verify(raw, 42)
	var rej *RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("expected RejectError, got %v", err)
	}
	// After mutation the bytes no longer canonicalise, so the verifier
	// surfaces that first. Either non-canonical or wrong-epoch is an
	// acceptable rejection — both are blockers.
	if rej.Reason != ReasonWrongEpoch && rej.Reason != ReasonNonCanonical {
		t.Fatalf("expected wrong-epoch or non-canonical, got %s", rej.Reason)
	}
}

func TestVerifyRejectsStaleHeight(t *testing.T) {
	v, raw, _ := buildMiniSetup(t)
	// Tip = 100, proof height = 42, grace = 6. 100-42 = 58 > 6.
	_, err := v.Verify(raw, 100)
	var rej *RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("expected RejectError, got %v", err)
	}
	if rej.Reason != ReasonTooLate {
		t.Fatalf("expected ReasonTooLate, got %s", rej.Reason)
	}
}

func TestVerifyRejectsHeaderMismatch(t *testing.T) {
	v, raw, _ := buildMiniSetup(t)
	fc := v.cfg.Chain.(*fakeChain)
	fc.headers[42] = [32]byte{0xFF}
	_, err := v.Verify(raw, 42)
	var rej *RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("expected RejectError, got %v", err)
	}
	if rej.Reason != ReasonHeaderMismatch {
		t.Fatalf("expected header-mismatch, got %s", rej.Reason)
	}
}

func TestVerifyRejectsBatchFraudAndQuarantines(t *testing.T) {
	ws := makeWorkSet(t, 4)
	epoch := uint64(0)
	const dagN = 64
	dag, _ := NewInMemoryDAG(epoch, ws.Root(), dagN)
	difficulty := big.NewInt(2)
	target, _ := TargetFromDifficulty(difficulty)
	headerHash := [32]byte{0x01}
	batchRoot, _ := ws.PrefixRoot(1)

	res, err := Solve(context.Background(), SolverParams{
		Epoch: epoch, Height: 42, HeaderHash: headerHash,
		MinerAddr: "badminer", BatchRoot: batchRoot, BatchCount: 1,
		Target: target, DAG: dag,
	}, nil, nil)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	raw, _ := res.Proof.CanonicalJSON()

	q := NewQuarantineSet()
	v, err := NewVerifier(VerifierConfig{
		EpochParams:      NewEpochParams(),
		DifficultyParams: NewDifficultyAdjusterParams(),
		Chain:            &fakeChain{tip: 42, headers: map[uint64][32]byte{42: headerHash}},
		Addresses:        permissiveAddr{},
		Batches:          fraudBatches{},
		Dedup:            NewProofIDSet(1024),
		Quarantine:       q,
		DAGProvider:      func(e uint64) (DAG, error) { return dag, nil },
		WorkSetProvider:  func(e uint64) (WorkSet, error) { return ws, nil },
		DifficultyAt:     func(h uint64) (*big.Int, error) { return difficulty, nil },
	})
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	_, err = v.Verify(raw, 42)
	var rej *RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("expected RejectError, got %v", err)
	}
	if rej.Reason != ReasonBatchFraud {
		t.Fatalf("expected batch-fraud, got %s", rej.Reason)
	}
	if !q.IsQuarantined("badminer", 42) {
		t.Fatal("fraud must quarantine the miner")
	}
}

// -----------------------------------------------------------------

// buildMiniSetup returns a verifier plus a valid proof (raw canonical
// JSON) for it at height 42, easy difficulty. Tests then mutate either
// the verifier state or the raw bytes to exercise rejection branches.
func buildMiniSetup(t *testing.T) (*Verifier, []byte, WorkSet) {
	t.Helper()
	ws := makeWorkSet(t, 4)
	epoch := uint64(0)
	const dagN = 64
	dag, err := NewInMemoryDAG(epoch, ws.Root(), dagN)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}
	difficulty := big.NewInt(2)
	target, _ := TargetFromDifficulty(difficulty)
	headerHash := [32]byte{0x01, 0x23, 0x45}
	batchRoot, _ := ws.PrefixRoot(1)
	res, err := Solve(context.Background(), SolverParams{
		Epoch: epoch, Height: 42, HeaderHash: headerHash,
		MinerAddr: "qsdm1test", BatchRoot: batchRoot, BatchCount: 1,
		Target: target, DAG: dag,
	}, nil, nil)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	raw, _ := res.Proof.CanonicalJSON()
	v, err := NewVerifier(VerifierConfig{
		EpochParams:      NewEpochParams(),
		DifficultyParams: NewDifficultyAdjusterParams(),
		Chain:            &fakeChain{tip: 42, headers: map[uint64][32]byte{42: headerHash}},
		Addresses:        permissiveAddr{},
		Batches:          goodBatches{},
		Dedup:            NewProofIDSet(1024),
		Quarantine:       NewQuarantineSet(),
		DAGProvider:      func(e uint64) (DAG, error) { return dag, nil },
		WorkSetProvider:  func(e uint64) (WorkSet, error) { return ws, nil },
		DifficultyAt:     func(h uint64) (*big.Int, error) { return difficulty, nil },
	})
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	return v, raw, ws
}

// mutateField swaps a literal substring in the canonical JSON and
// returns the new bytes. Used by tests to forge a subtly-bad proof.
func mutateField(raw []byte, from, to string) []byte {
	s := string(raw)
	if idx := indexFrom(s, from, 0); idx >= 0 {
		return []byte(s[:idx] + to + s[idx+len(from):])
	}
	return raw
}
