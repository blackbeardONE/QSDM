package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/poe"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

type fakePoEParentSource struct {
	tip  uint64
	refs []PoEParentView
	ok   bool
	gotN int
}

func (f *fakePoEParentSource) RecentParents(n int) (uint64, []PoEParentView, bool) {
	f.gotN = n
	refs := f.refs
	if len(refs) > n {
		refs = refs[:n]
	}
	return f.tip, refs, f.ok
}

func withPoEParentSource(t *testing.T, src PoEParentSource) {
	t.Helper()
	SetPoEParentSource(src)
	t.Cleanup(func() { SetPoEParentSource(nil) })
}

func withChainPoEActivation(t *testing.T, h uint64) {
	t.Helper()
	prev := chain.PoEActivationHeight()
	chain.SetPoEActivationHeight(h)
	t.Cleanup(func() { chain.SetPoEActivationHeight(prev) })
}

func getChainParents(t *testing.T, query string) (*httptest.ResponseRecorder, PoEParentsResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	(&Handlers{}).ChainParentsHandler(rec, httptest.NewRequest(http.MethodGet, "/api/v1/chain/parents"+query, nil))
	var body PoEParentsResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v body=%s", err, rec.Body.String())
		}
	}
	return rec, body
}

func TestChainParents_UnavailableWithoutSource(t *testing.T) {
	withPoEParentSource(t, nil)
	if rec, _ := getChainParents(t, ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", rec.Code)
	}
}

func TestChainParents_SuggestsNewestCommittedIDs(t *testing.T) {
	withChainPoEActivation(t, 1000)
	src := &fakePoEParentSource{tip: 999, ok: true, refs: []PoEParentView{
		{ID: "solo-heartbeat-999-1791611907090994535", Height: 999},
		{ID: "hive_wallet_1791611907090_0011223344556677", Height: 998},
		{ID: "solo-heartbeat-998-1791611897090994535", Height: 998},
	}}
	withPoEParentSource(t, src)
	rec, body := getChainParents(t, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(body.Parents) != poe.MinParents || body.Parents[0] != src.refs[0].ID || body.Parents[1] != src.refs[1].ID {
		t.Fatalf("parents = %v", body.Parents)
	}
	if body.Tip != 999 || body.PoEActivationHeight != 1000 || !body.PoEActive {
		t.Fatalf("tip/activation = %+v (the next block, 1000, is active)", body)
	}
	if body.MinParents != poe.MinParents || body.MaxParents != poe.MaxParents || body.WindowBlocks != poe.ParentWindowBlocks {
		t.Fatalf("limits = %+v", body)
	}
	if len(body.Recent) != 3 || src.gotN != poeParentsDefaultLimit {
		t.Fatalf("recent = %d, requested %d", len(body.Recent), src.gotN)
	}
	if rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatal("parents must not be cached")
	}
}

func TestChainParents_LimitAndMethod(t *testing.T) {
	src := &fakePoEParentSource{tip: 5, ok: true, refs: []PoEParentView{
		{ID: "committed-transaction-0001", Height: 5}, {ID: "committed-transaction-0002", Height: 4},
	}}
	withPoEParentSource(t, src)
	for query, wantN := range map[string]int{"?limit=1": poe.MinParents, "?limit=1000": poeParentsMaxLimit, "?limit=5": 5} {
		if rec, _ := getChainParents(t, query); rec.Code != http.StatusOK || src.gotN != wantN {
			t.Fatalf("%s: code %d, requested %d, want %d", query, rec.Code, src.gotN, wantN)
		}
	}
	if rec, _ := getChainParents(t, "?limit=x"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad limit: code %d", rec.Code)
	}
	rec := httptest.NewRecorder()
	(&Handlers{}).ChainParentsHandler(rec, httptest.NewRequest(http.MethodPost, "/api/v1/chain/parents", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: code %d", rec.Code)
	}
	// Fewer committed transactions than a transfer needs.
	src.refs = src.refs[:1]
	if rec, _ := getChainParents(t, ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("one candidate: code %d, want 503", rec.Code)
	}
}

func TestChainParents_IsPublicHighFrequencyRead(t *testing.T) {
	if !isPublicEndpoint("/api/v1/chain/parents") {
		t.Fatal("/api/v1/chain/parents must be public")
	}
	if !isHighFrequencyPublicReadPath("/api/v1/chain/parents", http.MethodGet) {
		t.Fatal("/api/v1/chain/parents must use the high-frequency public read bucket")
	}
}

func TestStatus_ReportsPoEPosture(t *testing.T) {
	withChainPoEActivation(t, 50)
	h := &Handlers{}
	if info := h.buildConsensusAuthInfo(48); info.PoEActivationHeight != 50 || info.PoEActive {
		t.Fatalf("tip 48: %+v", info)
	}
	if info := h.buildConsensusAuthInfo(49); !info.PoEActive {
		t.Fatalf("tip 49 (next block 50 is active): %+v", info)
	}
	withChainPoEActivation(t, 0)
	if info := h.buildConsensusAuthInfo(1 << 40); info.PoEActive || info.PoEActivationHeight != 0 {
		t.Fatalf("unscheduled: %+v", info)
	}
}

// A PoE rejection from the validator queue is a client error (422) that tells
// the wallet where to get parents, not a 500.
func TestSubmitSigned_PoEViolationIs422(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet unavailable: %v", err)
	}
	h := setupTestHandlersWithSubmesh(nil, ws)
	sender := ws.GetAddress()
	recipient := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	pool := &fakeSubmitter{err: fmt.Errorf("%w: parent 1 %q", poe.ErrParentUnknown, strings.Repeat("b", 32))}
	SetLocalWalletTransferLedger(nil)
	SetWalletTransferMempool(pool)
	SetMiningAccountProbe(&fakeAccountProbe{addrs: map[string]struct {
		bal   float64
		nonce uint64
	}{sender: {bal: 5, nonce: 0}}})
	t.Cleanup(func() {
		SetWalletTransferMempool(nil)
		SetMiningAccountProbe(nil)
	})
	env := buildSignedEnvelopeWithNonce(t, ws, recipient, 1.0, 0.01, []string{
		strings.Repeat("a", 32), strings.Repeat("b", 32),
	}, 1)
	w := postSubmitSigned(t, h, env)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d, want 422; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "/api/v1/chain/parents") {
		t.Fatalf("422 body does not point to the parent source: %s", w.Body.String())
	}
}

// End to end through the real validator queue: with PoE active, an envelope
// naming parents that were never committed is refused with 422 and never
// queued, and the same wallet's envelope naming the parents
// GET /api/v1/chain/parents would return is accepted.
func TestSubmitSigned_PoEThroughWalletTransferSubmitter(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet unavailable: %v", err)
	}
	withChainPoEActivation(t, 2)
	const funder = "poe-api-test-funder-00000000000000"
	accounts := chain.NewAccountStore()
	accounts.Credit(funder, 1)
	pool := mempool.New(mempool.DefaultConfig())
	bp := chain.NewBlockProducer(pool, chain.NewEnrollmentAwareApplier(accounts, nil), chain.DefaultProducerConfig())
	bp.SetAppendReceiptStore(chain.NewReceiptStore())
	for i := 0; i < 2; i++ { // heights 0 and 1
		acc, _ := accounts.Get(funder)
		if err := pool.Add(&mempool.Tx{ID: fmt.Sprintf("solo-heartbeat-%d-%d", acc.Nonce, 1_790_000_000_000_000_000+acc.Nonce),
			Sender: funder, Recipient: funder, Nonce: acc.Nonce}); err != nil {
			t.Fatal(err)
		}
		if _, err := bp.ProduceBlock(); err != nil {
			t.Fatal(err)
		}
	}
	refs := bp.PoEParentCandidates(bp.TipHeight(), 2)
	if len(refs) != 2 {
		t.Fatalf("parent candidates: %v", refs)
	}

	h := setupTestHandlersWithSubmesh(nil, ws)
	sender := ws.GetAddress()
	SetLocalWalletTransferLedger(nil)
	SetWalletTransferMempool(bp.WalletTransferSubmitter())
	SetMiningAccountProbe(&fakeAccountProbe{addrs: map[string]struct {
		bal   float64
		nonce uint64
	}{sender: {bal: 5, nonce: 0}}})
	t.Cleanup(func() {
		SetWalletTransferMempool(nil)
		SetMiningAccountProbe(nil)
	})
	recipient := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	bad := buildSignedEnvelopeWithNonce(t, ws, recipient, 1.0, 0.01, []string{
		strings.Repeat("a", 32), strings.Repeat("b", 32),
	}, 1)
	if w := postSubmitSigned(t, h, bad); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("uncommitted parents: code = %d body=%s", w.Code, w.Body.String())
	}
	if pool.Size() != 0 {
		t.Fatal("rejected transfer was queued")
	}
	good := buildSignedEnvelopeWithNonce(t, ws, recipient, 1.0, 0.01, []string{refs[0].ID, refs[1].ID}, 1)
	if w := postSubmitSigned(t, h, good); w.Code != http.StatusAccepted {
		t.Fatalf("committed parents: code = %d body=%s", w.Code, w.Body.String())
	}
	if pool.Size() != 1 {
		t.Fatalf("queued = %d, want 1", pool.Size())
	}
}
