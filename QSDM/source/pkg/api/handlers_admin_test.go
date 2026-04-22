package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/config"
	"github.com/blackbeardONE/QSDM/pkg/contracts"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/networking"
)

func newTestAdminAPI() *AdminAPI {
	as := chain.NewAccountStore()
	as.Credit("alice", 10000)
	as.Credit("bob", 5000)

	pool := mempool.New(mempool.DefaultConfig())
	rs := chain.NewReceiptStore()
	peers := networking.NewReputationTracker(networking.DefaultReputationConfig())
	tracer := contracts.NewCallTracer(100)

	fg := chain.NewFinalityGadget(chain.DefaultFinalityConfig())

	return &AdminAPI{
		Accounts: as,
		Mempool:  pool,
		Receipts: rs,
		Peers:    peers,
		Tracer:   tracer,
		Finality: fg,
	}
}

func TestAdminAPI_Accounts(t *testing.T) {
	api := newTestAdminAPI()
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/admin/accounts", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"].(float64) != 2 {
		t.Fatalf("expected 2 accounts, got %v", resp["count"])
	}
}

func TestAdminAPI_AccountByAddress(t *testing.T) {
	api := newTestAdminAPI()
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/admin/account/alice", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var acc map[string]interface{}
	json.NewDecoder(w.Body).Decode(&acc)
	if acc["address"] != "alice" {
		t.Fatalf("expected alice, got %v", acc["address"])
	}
}

func TestAdminAPI_AccountNotFound(t *testing.T) {
	api := newTestAdminAPI()
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/admin/account/nobody", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestAdminAPI_Mempool(t *testing.T) {
	api := newTestAdminAPI()
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/admin/mempool", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestAdminAPI_Finality(t *testing.T) {
	api := newTestAdminAPI()
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/admin/finality", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestAdminAPI_Peers(t *testing.T) {
	api := newTestAdminAPI()
	api.Peers.RecordEvent("peer-1", networking.EventValidBlock, 0)
	api.Peers.RecordEvent("peer-2", networking.EventValidTx, 0)

	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/admin/peers", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["total_peers"].(float64) != 2 {
		t.Fatalf("expected 2 peers, got %v", resp["total_peers"])
	}
}

func TestAdminAPI_BannedPeers(t *testing.T) {
	api := newTestAdminAPI()
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/admin/peers/banned", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestAdminAPI_Traces(t *testing.T) {
	api := newTestAdminAPI()
	tb := api.Tracer.BeginTrace("t1", "c", "fn", "alice", nil)
	api.Tracer.Finish(tb, 100, nil, nil)

	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/admin/traces?n=5", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestAdminAPI_TraceStats(t *testing.T) {
	api := newTestAdminAPI()
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/admin/traces/stats", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestAdminAPI_Overview(t *testing.T) {
	api := newTestAdminAPI()
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/admin/overview", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["accounts"].(float64) != 2 {
		t.Fatalf("expected 2 accounts in overview")
	}
}

func TestAdminAPI_ChainInfo(t *testing.T) {
	api := newTestAdminAPI()
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/admin/chain", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestAdminAPI_MethodNotAllowed(t *testing.T) {
	api := newTestAdminAPI()
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	req := httptest.NewRequest("POST", "/api/admin/accounts", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 405 {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestAdminAPI_ReceiptsRecent(t *testing.T) {
	api := newTestAdminAPI()
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/admin/receipts?n=5", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestAdminAPI_ReceiptStats(t *testing.T) {
	api := newTestAdminAPI()
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/admin/receipts/stats", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestAdminAPI_NilSubsystems(t *testing.T) {
	api := &AdminAPI{}
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	endpoints := []string{
		"/api/admin/accounts",
		"/api/admin/account/x",
		"/api/admin/validators",
		"/api/admin/finality",
		"/api/admin/mempool",
		"/api/admin/receipts",
		"/api/admin/receipts/stats",
		"/api/admin/peers",
		"/api/admin/peers/banned",
		"/api/admin/traces",
		"/api/admin/traces/stats",
		"/api/admin/audit",
		"/api/admin/config/reload-dry-run",
		"/api/admin/consensus/bft/follower",
		"/api/admin/consensus/pol/summary",
		"/api/admin/consensus/pol/prevote-lock/1",
		"/api/admin/consensus/pol/round-certificate/1",
	}
	for _, ep := range endpoints {
		req := httptest.NewRequest("GET", ep, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != 503 {
			t.Fatalf("%s: expected 503 for nil subsystem, got %d", ep, w.Code)
		}
	}
}

func TestAdminAPI_PolFollowerEndpoints(t *testing.T) {
	vs := chain.NewValidatorSet(chain.DefaultValidatorSetConfig())
	_ = vs.Register("v1", 100)
	_ = vs.Register("v2", 100)
	f := chain.NewPolFollower(vs, 2.0/3.0)
	p := &chain.PrevoteLockProof{
		Height:          42,
		Round:           0,
		LockedBlockHash: "root",
		Prevotes: []chain.BlockVote{
			{Validator: "v1", BlockHash: "root", Height: 42, Round: 0, Type: chain.VotePreVote, Timestamp: time.Now()},
			{Validator: "v2", BlockHash: "root", Height: 42, Round: 0, Type: chain.VotePreVote, Timestamp: time.Now()},
		},
	}
	if err := f.IngestPrevoteLockProof(p); err != nil {
		t.Fatal(err)
	}

	api := newTestAdminAPI()
	api.Validators = vs
	api.PolFollower = f
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/admin/consensus/pol/summary", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("summary: %d %s", w.Code, w.Body.String())
	}

	req2 := httptest.NewRequest("GET", "/api/admin/consensus/pol/prevote-lock/42", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != 200 {
		t.Fatalf("prevote-lock: %d %s", w2.Code, w2.Body.String())
	}

	req3 := httptest.NewRequest("GET", "/api/admin/consensus/pol/prevote-lock/99", nil)
	w3 := httptest.NewRecorder()
	mux.ServeHTTP(w3, req3)
	if w3.Code != 404 {
		t.Fatalf("expected 404 missing height, got %d", w3.Code)
	}
}

func TestAdminAPI_BFTFollowerEndpoint(t *testing.T) {
	vs := chain.NewValidatorSet(chain.DefaultValidatorSetConfig())
	_ = vs.Register("v1", 100)
	_ = vs.Register("v2", 100)
	_ = vs.Register("v3", 100)
	bc := chain.NewBFTConsensus(vs, chain.DefaultConsensusConfig())
	ex := chain.NewBFTExecutor(bc)
	ex.NoteFollowerAppend(nil)
	ex.NoteFollowerAppend(errFollowerAdminTest("replay failed"))

	api := newTestAdminAPI()
	api.BFTExecutor = ex
	api.Producer = chain.NewBlockProducer(api.Mempool, api.Accounts, chain.DefaultProducerConfig())
	api.Accounts.Credit("alice", 10000)
	api.Mempool.Add(&mempool.Tx{ID: "z1", Sender: "alice", Recipient: "bob", Amount: 1, Fee: 1, GasLimit: 0, Nonce: 0})
	if _, err := api.Producer.ProduceBlock(); err != nil {
		t.Fatal(err)
	}

	prop, _ := bc.ProposerForRound(0)
	if _, err := bc.Propose(8, 0, prop, "sr8"); err != nil {
		t.Fatal(err)
	}
	for _, v := range vs.ActiveValidators() {
		_ = bc.PreVote(8, v.Address, "sr8")
	}
	for _, v := range vs.ActiveValidators() {
		_ = bc.PreCommit(8, v.Address, "sr8")
	}

	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/admin/consensus/bft/follower", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("follower diag: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["append_ok_total"].(float64) != 1 {
		t.Fatalf("append_ok_total: %v", resp["append_ok_total"])
	}
	if resp["append_skip_total"].(float64) != 1 {
		t.Fatalf("append_skip_total: %v", resp["append_skip_total"])
	}
	if resp["append_conflict_total"].(float64) != 0 {
		t.Fatalf("append_conflict_total: %v", resp["append_conflict_total"])
	}
	if resp["last_ok"] != false {
		t.Fatalf("last_ok: %v", resp["last_ok"])
	}
	if resp["last_error"] != "replay failed" {
		t.Fatalf("last_error: %v", resp["last_error"])
	}
	if _, ok := resp["chain_height"]; !ok {
		t.Fatal("expected chain_height")
	}
	if resp["bft_committed_count"].(float64) < 1 {
		t.Fatalf("bft_committed_count: %v", resp["bft_committed_count"])
	}
	tail, ok := resp["bft_committed_heights_tail"].([]interface{})
	if !ok || len(tail) == 0 {
		t.Fatalf("expected bft_committed_heights_tail, got %v", resp["bft_committed_heights_tail"])
	}
}

func TestAdminAPI_BFTFollowerPendingProposeQuery(t *testing.T) {
	vs := chain.NewValidatorSet(chain.DefaultValidatorSetConfig())
	for _, reg := range []struct{ addr string; stake float64 }{
		{"v1", 100}, {"v2", 100}, {"v3", 100},
	} {
		if err := vs.Register(reg.addr, reg.stake); err != nil {
			t.Fatal(err)
		}
	}
	bc := chain.NewBFTConsensus(vs, chain.DefaultConsensusConfig())
	ex := chain.NewBFTExecutor(bc)
	prop, _ := bc.ProposerForRound(0)
	vote := "admin-query-root"
	blk := &chain.Block{
		Height: 11, PrevHash: "", Timestamp: time.Unix(1700000011, 0),
		StateRoot: vote, ProducerID: "node",
	}
	blk.Hash = chain.ComputeBlockHash(blk)
	b, err := chain.MarshalBFTWire(chain.BFTWirePropose, chain.BFTWireProposeMsg{
		Height: 11, Round: 0, Proposer: prop, BlockHash: vote, Block: blk,
	})
	if err != nil {
		t.Fatal(err)
	}
	ex.SetLastInboundBFTGossipPeer("diag-peer-z")
	if err := ex.ApplyInbound(b); err != nil {
		t.Fatal(err)
	}

	api := newTestAdminAPI()
	api.BFTExecutor = ex
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	u := "/api/admin/consensus/bft/follower?height=11&vote=" + vote
	req := httptest.NewRequest("GET", u, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("query: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["pending_propose_relay_peer_known"].(bool) != true {
		t.Fatalf("known: %v", resp["pending_propose_relay_peer_known"])
	}
	if resp["pending_propose_relay_peer"].(string) != "diag-peer-z" {
		t.Fatalf("peer: %v", resp["pending_propose_relay_peer"])
	}
}

func TestAdminAPI_BFTFollowerPendingProposeQueryBadRequest(t *testing.T) {
	vs := chain.NewValidatorSet(chain.DefaultValidatorSetConfig())
	_ = vs.Register("v1", 100)
	ex := chain.NewBFTExecutor(chain.NewBFTConsensus(vs, chain.DefaultConsensusConfig()))
	api := newTestAdminAPI()
	api.BFTExecutor = ex
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	for _, u := range []string{
		"/api/admin/consensus/bft/follower?height=1",
		"/api/admin/consensus/bft/follower?vote=x",
	} {
		req := httptest.NewRequest("GET", u, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != 400 {
			t.Fatalf("%s: expected 400, got %d", u, w.Code)
		}
	}
}

type errFollowerAdminTest string

func (e errFollowerAdminTest) Error() string { return string(e) }

func TestAdminAPI_Audit(t *testing.T) {
	api := newTestAdminAPI()
	api.Audit = NewAdminAuditTrail("test-secret")
	api.Audit.Record("admin", "rotate_key", "hsm", nil)

	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/admin/audit?limit=5&actor=admin", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	entries := resp["entries"].([]interface{})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

func TestAdminAPI_ReloadDryRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.toml")
	if err := os.WriteFile(path, []byte("[network]\nport = 1111\n"), 0644); err != nil {
		t.Fatal(err)
	}
	initial := &config.Config{NetworkPort: 1111}
	hr, err := config.NewHotReloader(config.HotReloadConfig{FilePath: path, PollInterval: time.Hour}, initial)
	if err != nil {
		t.Fatal(err)
	}
	api := newTestAdminAPI()
	api.HotReloader = hr

	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/admin/config/reload-dry-run", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	time.Sleep(15 * time.Millisecond)
	_ = os.WriteFile(path, []byte("[network]\nport = 2222\n"), 0644)
	req2 := httptest.NewRequest("GET", "/api/admin/config/reload-dry-run", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != 200 {
		t.Fatalf("expected 200 after file change, got %d: %s", w2.Code, w2.Body.String())
	}
	var body map[string]interface{}
	json.NewDecoder(w2.Body).Decode(&body)
	if !body["file_changed"].(bool) {
		t.Fatal("expected file_changed true")
	}
	if hr.Current().NetworkPort != 1111 {
		t.Fatal("dry-run must not apply port change")
	}
}
