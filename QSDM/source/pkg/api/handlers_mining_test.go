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

type fakeReceiptProbe struct {
	receipts map[string]TxReceiptView
}

func (p *fakeReceiptProbe) GetReceipt(txID string) (TxReceiptView, bool) {
	v, ok := p.receipts[txID]
	return v, ok
}

type fakeReceiptsListProbe struct {
	tip      uint64
	byHeight map[uint64][]TxReceiptView
	calledFrom uint64
	calledTo   uint64
	calledLimit int
}

func (p *fakeReceiptsListProbe) Tip() uint64 { return p.tip }
func (p *fakeReceiptsListProbe) ListByHeightRange(from, to uint64, limit int) []TxReceiptView {
	p.calledFrom, p.calledTo, p.calledLimit = from, to, limit
	out := make([]TxReceiptView, 0, limit)
	// Mirror chain.ReceiptStore semantics: walk from `to`
	// down to `from`, capping at limit.
	for h := to; ; h-- {
		for _, r := range p.byHeight[h] {
			out = append(out, r)
			if len(out) >= limit {
				return out
			}
		}
		if h == from || h == 0 {
			break
		}
	}
	return out
}

func TestMiningReceiptsList_503WhenProbeAbsent(t *testing.T) {
	SetMiningReceiptsListProbe(nil)
	t.Cleanup(func() { SetMiningReceiptsListProbe(nil) })
	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/receipts", nil)
	h.MiningReceiptsListHandler(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rec.Code)
	}
}

func TestMiningReceiptsList_DefaultRangeReturnsLastN(t *testing.T) {
	probe := &fakeReceiptsListProbe{
		tip: 10,
		byHeight: map[uint64][]TxReceiptView{
			10: {{TxID: "h10-a", BlockHeight: 10, IndexInBlock: 0}},
			9:  {{TxID: "h9-a", BlockHeight: 9, IndexInBlock: 0}},
			8:  {{TxID: "h8-a", BlockHeight: 8, IndexInBlock: 0}},
		},
	}
	SetMiningReceiptsListProbe(probe)
	t.Cleanup(func() { SetMiningReceiptsListProbe(nil) })

	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/receipts", nil)
	h.MiningReceiptsListHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var got MiningReceiptsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Tip != 10 {
		t.Errorf("Tip = %d, want 10", got.Tip)
	}
	if got.To != 10 {
		t.Errorf("To = %d, want 10 (default = tip)", got.To)
	}
	if got.Limit != DefaultMiningReceiptsListLimit {
		t.Errorf("Limit = %d, want default %d", got.Limit, DefaultMiningReceiptsListLimit)
	}
	// from = max(0, to+1-limit) = max(0, 11 - 20) = 0
	if got.From != 0 {
		t.Errorf("From = %d, want 0 (clamped)", got.From)
	}
	if probe.calledLimit != DefaultMiningReceiptsListLimit {
		t.Errorf("probe.calledLimit = %d, want %d", probe.calledLimit, DefaultMiningReceiptsListLimit)
	}
}

func TestMiningReceiptsList_RespectsLimit(t *testing.T) {
	probe := &fakeReceiptsListProbe{
		tip: 100,
		byHeight: map[uint64][]TxReceiptView{
			100: {{TxID: "h100", BlockHeight: 100}},
			99:  {{TxID: "h99", BlockHeight: 99}},
			98:  {{TxID: "h98", BlockHeight: 98}},
		},
	}
	SetMiningReceiptsListProbe(probe)
	t.Cleanup(func() { SetMiningReceiptsListProbe(nil) })

	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/receipts?limit=2", nil)
	h.MiningReceiptsListHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var got MiningReceiptsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Limit != 2 {
		t.Errorf("Limit = %d, want 2", got.Limit)
	}
	// from = max(0, 101 - 2) = 99
	if got.From != 99 {
		t.Errorf("From = %d, want 99", got.From)
	}
	if len(got.Receipts) != 2 {
		t.Fatalf("len(receipts) = %d, want 2", len(got.Receipts))
	}
	if got.Receipts[0].TxID != "h100" {
		t.Errorf("Receipts[0].TxID = %q, want h100 (newest first)", got.Receipts[0].TxID)
	}
}

func TestMiningReceiptsList_LimitCappedAtMax(t *testing.T) {
	probe := &fakeReceiptsListProbe{tip: 1, byHeight: map[uint64][]TxReceiptView{}}
	SetMiningReceiptsListProbe(probe)
	t.Cleanup(func() { SetMiningReceiptsListProbe(nil) })
	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/receipts?limit=99999", nil)
	h.MiningReceiptsListHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var got MiningReceiptsListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Limit != MiningReceiptsListMaxLimit {
		t.Errorf("Limit = %d, want capped at %d", got.Limit, MiningReceiptsListMaxLimit)
	}
}

func TestMiningReceiptsList_FromGreaterThanTo400(t *testing.T) {
	probe := &fakeReceiptsListProbe{tip: 100, byHeight: map[uint64][]TxReceiptView{}}
	SetMiningReceiptsListProbe(probe)
	t.Cleanup(func() { SetMiningReceiptsListProbe(nil) })
	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/receipts?from=50&to=10", nil)
	h.MiningReceiptsListHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestMiningReceiptsList_InvalidLimit400(t *testing.T) {
	probe := &fakeReceiptsListProbe{tip: 5, byHeight: map[uint64][]TxReceiptView{}}
	SetMiningReceiptsListProbe(probe)
	t.Cleanup(func() { SetMiningReceiptsListProbe(nil) })
	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/receipts?limit=banana", nil)
	h.MiningReceiptsListHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestMiningReceiptsList_405OnPost(t *testing.T) {
	probe := &fakeReceiptsListProbe{tip: 1, byHeight: map[uint64][]TxReceiptView{}}
	SetMiningReceiptsListProbe(probe)
	t.Cleanup(func() { SetMiningReceiptsListProbe(nil) })
	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/receipts", nil)
	h.MiningReceiptsListHandler(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rec.Code)
	}
}

func TestMiningReceiptsList_ToClampedAtTip(t *testing.T) {
	probe := &fakeReceiptsListProbe{
		tip: 10,
		byHeight: map[uint64][]TxReceiptView{
			10: {{TxID: "h10", BlockHeight: 10}},
		},
	}
	SetMiningReceiptsListProbe(probe)
	t.Cleanup(func() { SetMiningReceiptsListProbe(nil) })
	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/receipts?to=99999&limit=1", nil)
	h.MiningReceiptsListHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var got MiningReceiptsListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.To != 10 {
		t.Errorf("To = %d, want 10 (clamped at tip)", got.To)
	}
}

func TestMiningReceipt_503WhenProbeAbsent(t *testing.T) {
	SetMiningReceiptProbe(nil)
	t.Cleanup(func() { SetMiningReceiptProbe(nil) })
	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/receipts/abc", nil)
	h.MiningReceiptHandler(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rec.Code)
	}
}

func TestMiningReceipt_404OnMiss(t *testing.T) {
	SetMiningReceiptProbe(&fakeReceiptProbe{receipts: map[string]TxReceiptView{}})
	t.Cleanup(func() { SetMiningReceiptProbe(nil) })
	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/receipts/missing-tx", nil)
	h.MiningReceiptHandler(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMiningReceipt_200OnHit(t *testing.T) {
	want := TxReceiptView{
		TxID:        "found-tx-id",
		BlockHeight: 42,
		BlockHash:   "blkhash",
		Status:      1,
		GasUsed:     1000,
		Fee:         0.1,
		Timestamp:   "2026-05-07T03:00:00Z",
		ContractID:  "qsdm/enroll/v1",
		Logs: []TxReceiptLogView{
			{Topic: "TxApplied", Data: map[string]interface{}{"sender": "alice"}, Index: 0},
		},
	}
	SetMiningReceiptProbe(&fakeReceiptProbe{receipts: map[string]TxReceiptView{"found-tx-id": want}})
	t.Cleanup(func() { SetMiningReceiptProbe(nil) })
	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/receipts/found-tx-id", nil)
	h.MiningReceiptHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var got TxReceiptView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.TxID != want.TxID || got.BlockHeight != want.BlockHeight ||
		got.Status != want.Status || got.ContractID != want.ContractID {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", got, want)
	}
	if len(got.Logs) != 1 || got.Logs[0].Topic != "TxApplied" {
		t.Fatalf("logs round-trip: got %+v", got.Logs)
	}
}

func TestMiningReceipt_400OnEmptyTxID(t *testing.T) {
	SetMiningReceiptProbe(&fakeReceiptProbe{receipts: map[string]TxReceiptView{}})
	t.Cleanup(func() { SetMiningReceiptProbe(nil) })
	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/receipts/", nil)
	h.MiningReceiptHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestMiningReceipt_400OnOversizeTxID(t *testing.T) {
	SetMiningReceiptProbe(&fakeReceiptProbe{receipts: map[string]TxReceiptView{}})
	t.Cleanup(func() { SetMiningReceiptProbe(nil) })
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'a'
	}
	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/receipts/"+string(long), nil)
	h.MiningReceiptHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestMiningReceipt_405OnPost(t *testing.T) {
	SetMiningReceiptProbe(&fakeReceiptProbe{receipts: map[string]TxReceiptView{}})
	t.Cleanup(func() { SetMiningReceiptProbe(nil) })
	h := &Handlers{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/receipts/abc", nil)
	h.MiningReceiptHandler(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rec.Code)
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
