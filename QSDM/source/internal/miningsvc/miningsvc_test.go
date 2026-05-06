package miningsvc

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
)

// buildProducerWithBlock spins up a minimal BlockProducer,
// drops one transaction into the mempool (the producer
// refuses to seal an empty block by design), and produces
// one block so TipHeight() > 0 and GetBlock(1) is resolvable.
// Returns the producer and the canonical header_hash of the
// produced block, ready for a HeaderHashAt-style cross-check.
func buildProducerWithBlock(t *testing.T) (*chain.BlockProducer, [32]byte) {
	t.Helper()
	pool := mempool.New(mempool.DefaultConfig())
	accounts := chain.NewAccountStore()
	// Seed sender so the AccountStore.ApplyTx debit succeeds.
	accounts.Credit("alice", 1_000)
	if err := pool.Add(&mempool.Tx{
		ID: "miningsvc-test-seed", Sender: "alice", Recipient: "bob",
		Amount: 1, Fee: 0,
	}); err != nil {
		t.Fatalf("seed mempool: %v", err)
	}
	bp := chain.NewBlockProducer(pool, accounts, chain.DefaultProducerConfig())
	blk, err := bp.ProduceBlock()
	if err != nil {
		t.Fatalf("ProduceBlock: %v", err)
	}
	if blk == nil {
		t.Fatal("ProduceBlock returned nil block")
	}
	if !bp.HasTip() {
		t.Fatal("producer has no tip after ProduceBlock")
	}
	var hdr [32]byte
	if err := decodeHexInto(hdr[:], blk.Hash); err != nil {
		t.Fatalf("decode block hash: %v", err)
	}
	return bp, hdr
}

func syntheticWS() mining.WorkSet {
	ws := mining.WorkSet{Batches: []mining.Batch{
		{Cells: []mining.ParentCellRef{
			{ID: []byte{0x01, 0x02}, ContentHash: [32]byte{0x11}},
			{ID: []byte{0x03, 0x04}, ContentHash: [32]byte{0x22}},
			{ID: []byte{0x05, 0x06}, ContentHash: [32]byte{0x33}},
		}},
		{Cells: []mining.ParentCellRef{
			{ID: []byte{0x0a, 0x0b}, ContentHash: [32]byte{0xAA}},
			{ID: []byte{0x0c, 0x0d}, ContentHash: [32]byte{0xBB}},
			{ID: []byte{0x0e, 0x0f}, ContentHash: [32]byte{0xCC}},
		}},
	}}
	ws.Canonicalize()
	return ws
}

// validConfig assembles the smallest possible Config that
// passes New's validation. Tests that want to break a single
// field copy this and mutate.
func validConfig(t *testing.T) Config {
	t.Helper()
	bp, _ := buildProducerWithBlock(t)
	return Config{
		Producer:       bp,
		WorkSet:        syntheticWS(),
		DAGSize:        128,
		Difficulty:     big.NewInt(2),
		BlocksPerEpoch: 1024, // make sure tip stays in epoch 0 for a long time
	}
}

// ---- New: validation -----------------------------------------------------

func TestNew_RejectsMissingProducer(t *testing.T) {
	cfg := validConfig(t)
	cfg.Producer = nil
	if _, err := New(cfg); err == nil {
		t.Fatal("expected error when Producer is nil")
	}
}

func TestNew_RejectsTinyDAGSize(t *testing.T) {
	cfg := validConfig(t)
	cfg.DAGSize = 1
	if _, err := New(cfg); err == nil {
		t.Fatal("expected error for DAGSize < 2")
	}
}

func TestNew_RejectsNonPositiveDifficulty(t *testing.T) {
	cfg := validConfig(t)
	cfg.Difficulty = big.NewInt(0)
	if _, err := New(cfg); err == nil {
		t.Fatal("expected error for Difficulty <= 0")
	}
}

func TestNew_RejectsEmptyWorkSet(t *testing.T) {
	cfg := validConfig(t)
	cfg.WorkSet = mining.WorkSet{}
	if _, err := New(cfg); err == nil {
		t.Fatal("expected error for empty WorkSet")
	}
}

func TestNew_HappyPath(t *testing.T) {
	svc, err := New(validConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if svc == nil {
		t.Fatal("svc nil")
	}
}

// ---- WorkAt --------------------------------------------------------------

func TestWorkAt_NoTipReturns503(t *testing.T) {
	pool := mempool.New(mempool.DefaultConfig())
	accounts := chain.NewAccountStore()
	bp := chain.NewBlockProducer(pool, accounts, chain.DefaultProducerConfig())
	cfg := Config{
		Producer:       bp,
		WorkSet:        syntheticWS(),
		DAGSize:        128,
		Difficulty:     big.NewInt(2),
		BlocksPerEpoch: 1024,
	}
	svc, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := svc.WorkAt(0); !errors.Is(err, api.ErrMiningUnavailable) {
		t.Fatalf("want ErrMiningUnavailable on no-tip, got %v", err)
	}
}

func TestWorkAt_ClampsFutureToTip(t *testing.T) {
	cfg := validConfig(t)
	svc, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tip := cfg.Producer.TipHeight()
	// Handler default arrives as tip+1 — must NOT 503.
	work, err := svc.WorkAt(tip + 1)
	if err != nil {
		t.Fatalf("clamp tip+1: %v", err)
	}
	if work.Height != tip {
		t.Fatalf("want clamped to tip=%d, got %d", tip, work.Height)
	}
}

func TestWorkAt_HeaderHashRoundTripsToVerifier(t *testing.T) {
	cfg := validConfig(t)
	svc, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tip := cfg.Producer.TipHeight()
	work, err := svc.WorkAt(tip)
	if err != nil {
		t.Fatalf("WorkAt: %v", err)
	}
	// The header_hash served in /work must be exactly what the
	// verifier resolves via chainAdapter.HeaderHashAt — otherwise
	// step 5 of the verifier would reject every proof.
	adapter := chainAdapter{producer: cfg.Producer}
	verifierHdr, ok := adapter.HeaderHashAt(work.Height)
	if !ok {
		t.Fatal("HeaderHashAt failed at work height")
	}
	_, hdrFromWork, _, err := api.WorkToMiningCore(work)
	if err != nil {
		t.Fatalf("WorkToMiningCore: %v", err)
	}
	if hdrFromWork != verifierHdr {
		t.Fatal("/work header_hash != HeaderHashAt — verifier would reject")
	}
}

// ---- end-to-end mine + submit -------------------------------------------

// TestEndToEnd_SolveAndSubmit exercises the full loop a real
// miner runs: GET work → build DAG → solve → POST submit →
// expect acceptance. Confirms the verifier accepts a proof
// produced against this Service's served work.
func TestEndToEnd_SolveAndSubmit(t *testing.T) {
	cfg := validConfig(t)
	svc, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tip := cfg.Producer.TipHeight()
	work, err := svc.WorkAt(tip)
	if err != nil {
		t.Fatalf("WorkAt: %v", err)
	}
	ws, hdr, diff, err := api.WorkToMiningCore(work)
	if err != nil {
		t.Fatalf("WorkToMiningCore: %v", err)
	}
	ws.Canonicalize()
	batchRoot, err := ws.PrefixRoot(1)
	if err != nil {
		t.Fatalf("PrefixRoot: %v", err)
	}
	target, err := mining.TargetFromDifficulty(diff)
	if err != nil {
		t.Fatalf("Target: %v", err)
	}
	dag, err := mining.NewInMemoryDAG(work.Epoch, ws.Root(), work.DAGSize)
	if err != nil {
		t.Fatalf("NewInMemoryDAG: %v", err)
	}
	res, err := mining.Solve(context.Background(), mining.SolverParams{
		Epoch:      work.Epoch,
		Height:     work.Height,
		HeaderHash: hdr,
		MinerAddr:  "qsdm1selftest",
		BatchRoot:  batchRoot,
		BatchCount: 1,
		Target:     target,
		DAG:        dag,
	}, nil, nil)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	raw, err := res.Proof.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	id, err := svc.Submit(raw)
	if err != nil {
		t.Fatalf("Submit accepted-proof: %v", err)
	}
	if id == ([32]byte{}) {
		t.Fatal("Submit returned zero proof_id")
	}

	// Duplicate must reject with ReasonDuplicate (the dedup set
	// is a verifier-internal sentinel, not a miningsvc concern,
	// but exercising it confirms the wiring).
	if _, err := svc.Submit(raw); err == nil {
		t.Fatal("duplicate Submit should have been rejected")
	}
}

// TestEndToEnd_TamperedProofRejected confirms a tampered
// header_hash does not pass the verifier's step 5 cross-check.
func TestEndToEnd_TamperedProofRejected(t *testing.T) {
	cfg := validConfig(t)
	svc, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tip := cfg.Producer.TipHeight()
	work, err := svc.WorkAt(tip)
	if err != nil {
		t.Fatalf("WorkAt: %v", err)
	}
	ws, _, diff, _ := api.WorkToMiningCore(work)
	ws.Canonicalize()
	batchRoot, _ := ws.PrefixRoot(1)
	target, _ := mining.TargetFromDifficulty(diff)
	dag, _ := mining.NewInMemoryDAG(work.Epoch, ws.Root(), work.DAGSize)

	// Wrong header on purpose — verifier must reject step 5.
	var wrong [32]byte
	wrong[0] = 0xFF
	res, err := mining.Solve(context.Background(), mining.SolverParams{
		Epoch:      work.Epoch,
		Height:     work.Height,
		HeaderHash: wrong,
		MinerAddr:  "qsdm1bogus",
		BatchRoot:  batchRoot,
		BatchCount: 1,
		Target:     target,
		DAG:        dag,
	}, nil, nil)
	if err != nil {
		t.Fatalf("Solve (bogus header): %v", err)
	}
	raw, _ := res.Proof.CanonicalJSON()
	if _, err := svc.Submit(raw); err == nil {
		t.Fatal("tampered proof should have been rejected")
	}
}

// ---- DAG cache ----------------------------------------------------------

func TestDAGCache_ReturnsSameInstanceWithinEpoch(t *testing.T) {
	cfg := validConfig(t)
	svc, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d1, err := svc.dagFor(0)
	if err != nil {
		t.Fatalf("dagFor 1st: %v", err)
	}
	d2, err := svc.dagFor(0)
	if err != nil {
		t.Fatalf("dagFor 2nd: %v", err)
	}
	if d1 != d2 {
		t.Fatal("dag cache should return the same instance within an epoch")
	}
}

func TestDAGCache_BoundedAtCap(t *testing.T) {
	cfg := validConfig(t)
	svc, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for e := uint64(0); e < uint64(dagCacheCap)+5; e++ {
		if _, err := svc.dagFor(e); err != nil {
			t.Fatalf("dagFor epoch=%d: %v", e, err)
		}
	}
	svc.dagMu.RLock()
	got := len(svc.dagCache)
	svc.dagMu.RUnlock()
	if got > dagCacheCap {
		t.Fatalf("cache size %d exceeds cap %d", got, dagCacheCap)
	}
}

// ---- compile-time guard --------------------------------------------------

// Ensures Service satisfies api.MiningService. The package
// already includes a `var _ api.MiningService = (*Service)(nil)`
// directive, but this test fails the build with a clearer
// error when an interface drift happens.
func TestServiceImplementsMiningService(t *testing.T) {
	var svc *Service
	var _ api.MiningService = svc
}
