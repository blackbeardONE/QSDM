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
	"strconv"
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

// fakeAccountProbe + fakeEmissionProbe exercise the read-
// only solo-mode probe handlers without booting the chain.

type fakeAccountProbe struct {
	addrs map[string]struct {
		bal   float64
		nonce uint64
	}
}

func (p *fakeAccountProbe) BalanceOf(addr string) (float64, uint64, bool) {
	if p == nil {
		return 0, 0, false
	}
	v, ok := p.addrs[addr]
	if !ok {
		return 0, 0, false
	}
	return v.bal, v.nonce, true
}

func TestMiningAccount_503WhenProbeAbsent(t *testing.T) {
	SetMiningAccountProbe(nil)
	t.Cleanup(func() { SetMiningAccountProbe(nil) })
	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/account?address=qsdm1foo", nil)
	h.MiningAccountHandler(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rec.Code)
	}
}

func TestMiningAccount_400WhenAddressMissing(t *testing.T) {
	SetMiningAccountProbe(&fakeAccountProbe{})
	t.Cleanup(func() { SetMiningAccountProbe(nil) })
	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/account", nil)
	h.MiningAccountHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestMiningAccount_RoundTripsBalance(t *testing.T) {
	SetMiningAccountProbe(&fakeAccountProbe{
		addrs: map[string]struct {
			bal   float64
			nonce uint64
		}{
			"qsdm1miner": {bal: 12.5, nonce: 7},
		},
	})
	t.Cleanup(func() { SetMiningAccountProbe(nil) })
	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/account?address=qsdm1miner", nil)
	h.MiningAccountHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp MiningAccountResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Address != "qsdm1miner" || resp.Balance != 12.5 || resp.Nonce != 7 || !resp.Present {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

type fakeEmissionProbe struct {
	snap MiningEmissionSnapshot
}

func (p *fakeEmissionProbe) Snapshot() MiningEmissionSnapshot { return p.snap }

func TestMiningEmission_503WhenProbeAbsent(t *testing.T) {
	SetMiningEmissionProbe(nil)
	t.Cleanup(func() { SetMiningEmissionProbe(nil) })
	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/emission", nil)
	h.MiningEmissionHandler(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rec.Code)
	}
}

func TestMiningEmission_RoundTripsSnapshot(t *testing.T) {
	want := MiningEmissionSnapshot{
		ChainTip:               42,
		MiningCapDust:          9_000_000_000_000_000,
		BlocksPerEpoch:         12_623_040,
		TargetBlockTimeSeconds: 10,
		CurrentEpoch:           0,
		BlockRewardDust:        356_490_987,
		BlockRewardCell:        "3.56490987",
		EmittedDust:            14_972_621_454,
		EmittedCell:            "149.72621454",
		RemainingDust:          8_999_999_985_027_378_546,
		NextHalvingHeight:      12_623_040,
		NextHalvingETASeconds:  126_230_388,
	}
	SetMiningEmissionProbe(&fakeEmissionProbe{snap: want})
	t.Cleanup(func() { SetMiningEmissionProbe(nil) })
	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/emission", nil)
	h.MiningEmissionHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp MiningEmissionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ChainTip != want.ChainTip ||
		resp.BlockRewardDust != want.BlockRewardDust ||
		resp.BlockRewardCell != want.BlockRewardCell ||
		resp.NextHalvingHeight != want.NextHalvingHeight {
		t.Fatalf("snapshot did not round-trip: got %+v want %+v", resp, want)
	}
}

type fakeBlocksProbe struct {
	tip     uint64
	headers []MiningBlockHeader
}

func (p *fakeBlocksProbe) Tip() uint64 { return p.tip }
func (p *fakeBlocksProbe) HeadersInRange(from, to uint64) []MiningBlockHeader {
	out := make([]MiningBlockHeader, 0)
	for _, h := range p.headers {
		if h.Height >= from && h.Height <= to {
			out = append(out, h)
		}
	}
	return out
}

func TestMiningBlocks_503WhenProbeAbsent(t *testing.T) {
	SetMiningBlocksProbe(nil)
	t.Cleanup(func() { SetMiningBlocksProbe(nil) })
	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/blocks", nil)
	h.MiningBlocksHandler(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rec.Code)
	}
}

func TestMiningBlocks_DefaultLimitReturnsLastN(t *testing.T) {
	headers := make([]MiningBlockHeader, 0, 50)
	for i := uint64(0); i <= 49; i++ {
		headers = append(headers, MiningBlockHeader{
			Height:     i,
			Hash:       "h" + strconv.FormatUint(i, 10),
			TxCount:    1,
			Timestamp:  "2026-04-29T00:00:00Z",
			ProducerID: "node-x",
		})
	}
	SetMiningBlocksProbe(&fakeBlocksProbe{tip: 49, headers: headers})
	t.Cleanup(func() { SetMiningBlocksProbe(nil) })

	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/blocks", nil)
	h.MiningBlocksHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp MiningBlocksResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Default page is 20 → from = 49-20+1 = 30, to = 49.
	if resp.From != 30 || resp.To != 49 {
		t.Fatalf("default range wrong: from=%d to=%d", resp.From, resp.To)
	}
	if len(resp.Headers) != 20 {
		t.Fatalf("want 20 headers, got %d", len(resp.Headers))
	}
}

func TestMiningBlocks_ExplicitRange(t *testing.T) {
	headers := make([]MiningBlockHeader, 0, 100)
	for i := uint64(0); i <= 99; i++ {
		headers = append(headers, MiningBlockHeader{Height: i, Hash: "h", Timestamp: "t"})
	}
	SetMiningBlocksProbe(&fakeBlocksProbe{tip: 99, headers: headers})
	t.Cleanup(func() { SetMiningBlocksProbe(nil) })

	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/blocks?from=10&to=14", nil)
	h.MiningBlocksHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var resp MiningBlocksResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.From != 10 || resp.To != 14 || len(resp.Headers) != 5 {
		t.Fatalf("explicit range wrong: from=%d to=%d n=%d", resp.From, resp.To, len(resp.Headers))
	}
}

func TestMiningBlocks_FromGreaterThanTo400(t *testing.T) {
	SetMiningBlocksProbe(&fakeBlocksProbe{tip: 100})
	t.Cleanup(func() { SetMiningBlocksProbe(nil) })
	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/blocks?from=20&to=10", nil)
	h.MiningBlocksHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestMiningBlocks_RangeExceedsCap400(t *testing.T) {
	SetMiningBlocksProbe(&fakeBlocksProbe{tip: 1000})
	t.Cleanup(func() { SetMiningBlocksProbe(nil) })
	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/mining/blocks?from=0&to="+strconv.Itoa(MiningBlocksMaxLimit), nil)
	h.MiningBlocksHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 (range exceeds cap), got %d", rec.Code)
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
