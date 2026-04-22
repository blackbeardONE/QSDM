package api

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mining"
)

// fakeService is a minimal in-memory MiningService used to exercise the
// HTTP wire layer. It owns a single workset and DAG so tests can solve a
// real proof against it.
type fakeService struct {
	epoch   uint64
	height  uint64
	header  [32]byte
	diff    *big.Int
	ws      mining.WorkSet
	dag     mining.DAG
	verify  *mining.Verifier
}

func (s *fakeService) WorkAt(height uint64) (*MiningWork, error) {
	if height != s.height {
		return nil, ErrMiningUnavailable
	}
	return WorkFromMiningCore(s.epoch, s.height, s.header, s.diff, s.dag.N(), s.ws, mining.DefaultBlocksPerMiningEpoch)
}
func (s *fakeService) Submit(raw []byte) ([32]byte, error) {
	return s.verify.Verify(raw, s.height)
}
func (s *fakeService) TipHeight() uint64 { return s.height }

type permAddr struct{}

func (permAddr) ValidateAddress(a string) error {
	if a == "" {
		return errors.New("empty")
	}
	return nil
}

type okBatch struct{}

func (okBatch) ValidateBatch(_ mining.Batch) error { return nil }

func buildFakeService(t *testing.T) *fakeService {
	t.Helper()
	ws := mining.WorkSet{Batches: []mining.Batch{
		{Cells: []mining.ParentCellRef{
			{ID: []byte{0x01}, ContentHash: [32]byte{0x11}},
			{ID: []byte{0x02}, ContentHash: [32]byte{0x22}},
			{ID: []byte{0x03}, ContentHash: [32]byte{0x33}},
		}},
		{Cells: []mining.ParentCellRef{
			{ID: []byte{0x0a}, ContentHash: [32]byte{0xAA}},
			{ID: []byte{0x0b}, ContentHash: [32]byte{0xBB}},
			{ID: []byte{0x0c}, ContentHash: [32]byte{0xCC}},
		}},
	}}
	ws.Canonicalize()
	const N = 64
	dag, err := mining.NewInMemoryDAG(0, ws.Root(), N)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}
	diff := big.NewInt(2)
	header := [32]byte{0xDE, 0xAD}
	type chainStub struct {
		h [32]byte
	}
	svc := &fakeService{
		epoch:  0,
		height: 100,
		header: header,
		diff:   diff,
		ws:     ws,
		dag:    dag,
	}
	v, err := mining.NewVerifier(mining.VerifierConfig{
		EpochParams:      mining.EpochParams{BlocksPerEpoch: 1000}, // so h=100 stays in epoch 0
		DifficultyParams: mining.NewDifficultyAdjusterParams(),
		Chain:            fakeChainAdapter{h: header, height: 100},
		Addresses:        permAddr{},
		Batches:          okBatch{},
		Dedup:            mining.NewProofIDSet(1024),
		Quarantine:       mining.NewQuarantineSet(),
		DAGProvider:      func(_ uint64) (mining.DAG, error) { return dag, nil },
		WorkSetProvider:  func(_ uint64) (mining.WorkSet, error) { return ws, nil },
		DifficultyAt:     func(_ uint64) (*big.Int, error) { return diff, nil },
	})
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	svc.verify = v
	return svc
}

type fakeChainAdapter struct {
	h      [32]byte
	height uint64
}

func (f fakeChainAdapter) TipHeight() uint64 { return f.height }
func (f fakeChainAdapter) HeaderHashAt(h uint64) ([32]byte, bool) {
	if h == f.height {
		return f.h, true
	}
	return [32]byte{}, false
}

func TestMiningWorkReturns503WhenServiceAbsent(t *testing.T) {
	SetMiningService(nil)
	t.Cleanup(func() { SetMiningService(nil) })
	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/work", nil)
	h.MiningWorkHandler(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rec.Code)
	}
}

func TestMiningSubmitReturns503WhenServiceAbsent(t *testing.T) {
	SetMiningService(nil)
	t.Cleanup(func() { SetMiningService(nil) })
	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mining/submit", bytes.NewReader([]byte(`{}`)))
	h.MiningSubmitHandler(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rec.Code)
	}
}

func TestMiningEndpointsEndToEnd(t *testing.T) {
	svc := buildFakeService(t)
	SetMiningService(svc)
	t.Cleanup(func() { SetMiningService(nil) })

	// /work round-trip
	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/work?height=100", nil)
	h.MiningWorkHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("work: got %d body=%s", rec.Code, rec.Body.String())
	}
	var work MiningWork
	if err := json.Unmarshal(rec.Body.Bytes(), &work); err != nil {
		t.Fatalf("decode work: %v", err)
	}
	if work.Height != 100 || work.Epoch != 0 || work.DAGSize == 0 {
		t.Fatalf("unexpected work payload: %+v", work)
	}

	// Reconstruct the core types and solve.
	ws, hdr, diff, err := WorkToMiningCore(&work)
	if err != nil {
		t.Fatalf("to core: %v", err)
	}
	if hdr != svc.header {
		t.Fatalf("header roundtrip diff")
	}
	if diff.Cmp(svc.diff) != 0 {
		t.Fatalf("difficulty roundtrip diff")
	}
	// Must canonicalise to match verifier's internal derivation.
	ws.Canonicalize()
	batchRoot, err := ws.PrefixRoot(1)
	if err != nil {
		t.Fatalf("prefix: %v", err)
	}
	tgt, _ := mining.TargetFromDifficulty(diff)

	// Build fresh DAG (miner doesn't trust server's DAG; re-derives).
	localDAG, err := mining.NewInMemoryDAG(work.Epoch, ws.Root(), work.DAGSize)
	if err != nil {
		t.Fatalf("local dag: %v", err)
	}

	sres, err := mining.Solve(context.Background(), mining.SolverParams{
		Epoch:      work.Epoch,
		Height:     work.Height,
		HeaderHash: hdr,
		MinerAddr:  "miner1",
		BatchRoot:  batchRoot,
		BatchCount: 1,
		Target:     tgt,
		DAG:        localDAG,
	}, nil, nil)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	raw, _ := sres.Proof.CanonicalJSON()

	// /submit round-trip.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/mining/submit", bytes.NewReader(raw))
	h.MiningSubmitHandler(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("submit: got %d body=%s", rec2.Code, rec2.Body.String())
	}
	var sub MiningSubmitResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &sub); err != nil {
		t.Fatalf("decode submit: %v", err)
	}
	if !sub.Accepted || sub.ProofID == "" {
		t.Fatalf("submit not accepted: %+v", sub)
	}
	if _, err := hex.DecodeString(sub.ProofID); err != nil {
		t.Fatalf("invalid proof id hex: %v", err)
	}

	// Duplicate submit must yield 400 with reject reason.
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/mining/submit", bytes.NewReader(raw))
	h.MiningSubmitHandler(rec3, req3)
	if rec3.Code != http.StatusBadRequest {
		t.Fatalf("duplicate submit: want 400 got %d", rec3.Code)
	}
	var dup MiningSubmitResponse
	_ = json.Unmarshal(rec3.Body.Bytes(), &dup)
	if dup.Accepted || dup.RejectReason != string(mining.ReasonDuplicate) {
		t.Fatalf("duplicate response unexpected: %+v", dup)
	}
}
