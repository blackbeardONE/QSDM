package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/config"
	"github.com/blackbeardONE/QSDM/pkg/contracts"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
	"github.com/blackbeardONE/QSDM/pkg/networking"
)

// AdminAPI exposes REST endpoints for node administration: validators,
// accounts, finality status, mempool, peer reputation, and contract traces.
type AdminAPI struct {
	Accounts    *chain.AccountStore
	Validators  *chain.ValidatorSet
	Finality    *chain.FinalityGadget
	Mempool     *mempool.Mempool
	Receipts    *chain.ReceiptStore
	Peers       *networking.ReputationTracker
	Tracer      *contracts.CallTracer
	Producer    *chain.BlockProducer
	BFTExecutor *chain.BFTExecutor
	PolFollower *chain.PolFollower
	Audit       *AdminAuditTrail
	HotReloader *config.HotReloader
}

func (a *AdminAPI) writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (a *AdminAPI) writeError(w http.ResponseWriter, status int, msg string) {
	a.writeJSON(w, status, map[string]interface{}{"error": msg})
}

// RegisterRoutes attaches all admin endpoints to the given mux under /api/admin/.
func (a *AdminAPI) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/admin/accounts", a.handleAccounts)
	mux.HandleFunc("/api/admin/account/", a.handleAccountByAddress)
	mux.HandleFunc("/api/admin/validators", a.handleValidators)
	mux.HandleFunc("/api/admin/finality", a.handleFinality)
	mux.HandleFunc("/api/admin/mempool", a.handleMempool)
	mux.HandleFunc("/api/admin/receipts", a.handleReceipts)
	mux.HandleFunc("/api/admin/receipts/stats", a.handleReceiptStats)
	mux.HandleFunc("/api/admin/peers", a.handlePeers)
	mux.HandleFunc("/api/admin/peers/banned", a.handleBannedPeers)
	mux.HandleFunc("/api/admin/traces", a.handleTraces)
	mux.HandleFunc("/api/admin/traces/stats", a.handleTraceStats)
	mux.HandleFunc("/api/admin/chain", a.handleChainInfo)
	mux.HandleFunc("/api/admin/consensus/bft/follower", a.handleBFTFollowerDiag)
	mux.HandleFunc("/api/admin/consensus/pol/summary", a.handlePolFollowerSummary)
	mux.HandleFunc("/api/admin/consensus/pol/prevote-lock/", a.handlePolPrevoteLockByHeight)
	mux.HandleFunc("/api/admin/consensus/pol/round-certificate/", a.handlePolRoundCertificateByHeight)
	mux.HandleFunc("/api/admin/overview", a.handleOverview)
	mux.HandleFunc("/api/admin/audit", a.handleAudit)
	mux.HandleFunc("/api/admin/config/reload-dry-run", a.handleReloadDryRun)
}

// GET /api/admin/accounts — list all accounts
func (a *AdminAPI) handleAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if a.Accounts == nil {
		a.writeError(w, http.StatusServiceUnavailable, "account store not available")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]interface{}{
		"accounts":   a.Accounts.AllAccounts(),
		"count":      a.Accounts.Count(),
		"state_root": a.Accounts.StateRoot(),
	})
}

// GET /api/admin/account/{address}
func (a *AdminAPI) handleAccountByAddress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if a.Accounts == nil {
		a.writeError(w, http.StatusServiceUnavailable, "account store not available")
		return
	}
	addr := r.URL.Path[len("/api/admin/account/"):]
	if addr == "" {
		a.writeError(w, http.StatusBadRequest, "address required")
		return
	}
	acc, ok := a.Accounts.Get(addr)
	if !ok {
		a.writeError(w, http.StatusNotFound, "account not found")
		return
	}
	a.writeJSON(w, http.StatusOK, acc)
}

// GET /api/admin/validators
func (a *AdminAPI) handleValidators(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if a.Validators == nil {
		a.writeError(w, http.StatusServiceUnavailable, "validator set not available")
		return
	}
	active := a.Validators.ActiveValidators()
	var totalStake float64
	for _, v := range active {
		totalStake += v.Stake
	}
	a.writeJSON(w, http.StatusOK, map[string]interface{}{
		"validators":  active,
		"active":      len(active),
		"total_stake": totalStake,
		"epoch":       a.Validators.CurrentEpoch(),
		"total_size":  a.Validators.Size(),
	})
}

// GET /api/admin/finality
func (a *AdminAPI) handleFinality(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if a.Finality == nil {
		a.writeError(w, http.StatusServiceUnavailable, "finality gadget not available")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]interface{}{
		"pending_blocks":   a.Finality.PendingCount(),
		"finalized_blocks": a.Finality.FinalizedCount(),
		"last_finalized":   a.Finality.LastFinalized(),
	})
}

// GET /api/admin/mempool
func (a *AdminAPI) handleMempool(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if a.Mempool == nil {
		a.writeError(w, http.StatusServiceUnavailable, "mempool not available")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]interface{}{
		"size":  a.Mempool.Size(),
		"stats": a.Mempool.Stats(),
	})
}

// GET /api/admin/receipts?block=N or GET /api/admin/receipts?n=10
func (a *AdminAPI) handleReceipts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if a.Receipts == nil {
		a.writeError(w, http.StatusServiceUnavailable, "receipt store not available")
		return
	}

	if blockStr := r.URL.Query().Get("block"); blockStr != "" {
		height, err := strconv.ParseUint(blockStr, 10, 64)
		if err != nil {
			a.writeError(w, http.StatusBadRequest, "invalid block number")
			return
		}
		a.writeJSON(w, http.StatusOK, a.Receipts.GetByBlock(height))
		return
	}

	n := 20
	if nStr := r.URL.Query().Get("n"); nStr != "" {
		if parsed, err := strconv.Atoi(nStr); err == nil && parsed > 0 {
			n = parsed
		}
	}
	a.writeJSON(w, http.StatusOK, a.Receipts.Recent(n))
}

// GET /api/admin/receipts/stats
func (a *AdminAPI) handleReceiptStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if a.Receipts == nil {
		a.writeError(w, http.StatusServiceUnavailable, "receipt store not available")
		return
	}
	a.writeJSON(w, http.StatusOK, a.Receipts.Stats())
}

// GET /api/admin/peers
func (a *AdminAPI) handlePeers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if a.Peers == nil {
		a.writeError(w, http.StatusServiceUnavailable, "peer tracker not available")
		return
	}

	n := 50
	if nStr := r.URL.Query().Get("n"); nStr != "" {
		if parsed, err := strconv.Atoi(nStr); err == nil && parsed > 0 {
			n = parsed
		}
	}
	a.writeJSON(w, http.StatusOK, map[string]interface{}{
		"peers":       a.Peers.TopPeers(n),
		"total_peers": a.Peers.PeerCount(),
	})
}

// GET /api/admin/peers/banned
func (a *AdminAPI) handleBannedPeers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if a.Peers == nil {
		a.writeError(w, http.StatusServiceUnavailable, "peer tracker not available")
		return
	}
	a.writeJSON(w, http.StatusOK, a.Peers.BannedPeers())
}

// GET /api/admin/traces?n=10 or ?contract=X&func=Y
func (a *AdminAPI) handleTraces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if a.Tracer == nil {
		a.writeError(w, http.StatusServiceUnavailable, "call tracer not available")
		return
	}

	contractID := r.URL.Query().Get("contract")
	funcName := r.URL.Query().Get("func")
	if contractID != "" && funcName != "" {
		a.writeJSON(w, http.StatusOK, a.Tracer.GetByCall(contractID, funcName))
		return
	}

	n := 20
	if nStr := r.URL.Query().Get("n"); nStr != "" {
		if parsed, err := strconv.Atoi(nStr); err == nil && parsed > 0 {
			n = parsed
		}
	}
	a.writeJSON(w, http.StatusOK, a.Tracer.Recent(n))
}

// GET /api/admin/traces/stats
func (a *AdminAPI) handleTraceStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if a.Tracer == nil {
		a.writeError(w, http.StatusServiceUnavailable, "call tracer not available")
		return
	}
	a.writeJSON(w, http.StatusOK, a.Tracer.Stats())
}

// GET /api/admin/chain
func (a *AdminAPI) handleChainInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	info := map[string]interface{}{
		"time": time.Now().UTC(),
	}
	if a.Producer != nil {
		info["height"] = a.Producer.ChainHeight()
		if blk, ok := a.Producer.LatestBlock(); ok {
			info["latest_block"] = blk.Header()
		}
	}
	if a.Accounts != nil {
		info["accounts"] = a.Accounts.Count()
		info["state_root"] = a.Accounts.StateRoot()
	}
	a.writeJSON(w, http.StatusOK, info)
}

// GET /api/admin/overview — combined dashboard summary
func (a *AdminAPI) handleOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}

	overview := map[string]interface{}{
		"time": time.Now().UTC(),
	}

	if a.Accounts != nil {
		overview["accounts"] = a.Accounts.Count()
	}
	if a.Mempool != nil {
		overview["mempool_size"] = a.Mempool.Size()
	}
	if a.Receipts != nil {
		overview["receipts"] = a.Receipts.Stats()
	}
	if a.Peers != nil {
		overview["peers_total"] = a.Peers.PeerCount()
		overview["peers_banned"] = len(a.Peers.BannedPeers())
	}
	if a.Tracer != nil {
		overview["traces"] = a.Tracer.Stats()
	}
	if a.Validators != nil {
		overview["validators_active"] = len(a.Validators.ActiveValidators())
		overview["validators_total"] = a.Validators.Size()
	}
	if a.Finality != nil {
		overview["pending_blocks"] = a.Finality.PendingCount()
		overview["finalized_blocks"] = a.Finality.FinalizedCount()
	}
	if a.Producer != nil {
		overview["chain_height"] = a.Producer.ChainHeight()
	}
	if a.PolFollower != nil {
		overview["pol_follower"] = a.PolFollower.Summary()
	}
	if a.BFTExecutor != nil {
		ok, sk, cx := a.BFTExecutor.FollowerAppendStats()
		overview["bft_follower"] = map[string]interface{}{
			"append_ok_total":       ok,
			"append_skip_total":     sk,
			"append_conflict_total": cx,
		}
	}
	if a.HotReloader != nil {
		overview["hot_reload"] = a.HotReloader.LastDryRunInfo()
		ms := monitoring.GetMetrics().GetStats()
		overview["hot_reload_apply_success_total"] = ms["hot_reload_apply_success_total"]
		overview["hot_reload_apply_failure_total"] = ms["hot_reload_apply_failure_total"]
		overview["hot_reload_dry_run_total"] = ms["hot_reload_dry_run_total"]
	}

	a.writeJSON(w, http.StatusOK, overview)
}

// GET /api/admin/consensus/bft/follower — BFT follower append counters + last TryAppendExternalBlock diagnostic.
// Optional query: height=<uint64> and vote=<string> (or vote_value=) to resolve PendingProposeSource for ops.
func (a *AdminAPI) handleBFTFollowerDiag(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if a.BFTExecutor == nil {
		a.writeError(w, http.StatusServiceUnavailable, "BFT executor not configured")
		return
	}
	ok, sk, cx := a.BFTExecutor.FollowerAppendStats()
	out := map[string]interface{}{
		"append_ok_total":       ok,
		"append_skip_total":     sk,
		"append_conflict_total": cx,
	}
	for k, v := range a.BFTExecutor.FollowerAppendDiagnostic() {
		out[k] = v
	}
	if a.Producer != nil {
		out["chain_height"] = a.Producer.ChainHeight()
	}
	if p := a.BFTExecutor.LastInboundBFTGossipPeer(); p != "" {
		out["last_bft_gossip_peer"] = p
	}
	if bc := a.BFTExecutor.Consensus(); bc != nil {
		out["bft_committed_count"] = bc.CommittedCount()
		hs := bc.CommittedHeights()
		if n := len(hs); n > 0 {
			start := 0
			if n > 16 {
				start = n - 16
			}
			out["bft_committed_heights_tail"] = hs[start:]
		}
	}

	q := r.URL.Query()
	heightStr := strings.TrimSpace(q.Get("height"))
	vote := strings.TrimSpace(q.Get("vote"))
	if vote == "" {
		vote = strings.TrimSpace(q.Get("vote_value"))
	}
	if heightStr != "" || vote != "" {
		if heightStr == "" {
			a.writeError(w, http.StatusBadRequest, "height is required when vote or vote_value is set")
			return
		}
		if vote == "" {
			a.writeError(w, http.StatusBadRequest, "vote or vote_value is required when height is set")
			return
		}
		h, err := strconv.ParseUint(heightStr, 10, 64)
		if err != nil {
			a.writeError(w, http.StatusBadRequest, "height must be a non-negative integer")
			return
		}
		out["query_height"] = h
		out["query_vote"] = vote
		if peer, ok := a.BFTExecutor.PendingProposeSource(h, vote); ok {
			out["pending_propose_relay_peer"] = peer
			out["pending_propose_relay_peer_known"] = true
		} else {
			out["pending_propose_relay_peer"] = nil
			out["pending_propose_relay_peer_known"] = false
		}
	}

	a.writeJSON(w, http.StatusOK, out)
}

// GET /api/admin/consensus/pol/summary — POL follower index (gossip-derived lock proofs + round certs).
func (a *AdminAPI) handlePolFollowerSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if a.PolFollower == nil {
		a.writeError(w, http.StatusServiceUnavailable, "POL follower not configured")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]interface{}{
		"summary": a.PolFollower.Summary(),
		"locks":   a.PolFollower.LockHeights(256),
		"certs":   a.PolFollower.CertHeights(256),
	})
}

// GET /api/admin/consensus/pol/prevote-lock/{height}
func (a *AdminAPI) handlePolPrevoteLockByHeight(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if a.PolFollower == nil {
		a.writeError(w, http.StatusServiceUnavailable, "POL follower not configured")
		return
	}
	const prefix = "/api/admin/consensus/pol/prevote-lock/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		a.writeError(w, http.StatusBadRequest, "invalid path")
		return
	}
	raw := strings.TrimPrefix(r.URL.Path, prefix)
	raw = strings.TrimSuffix(raw, "/")
	if raw == "" {
		a.writeError(w, http.StatusBadRequest, "missing height")
		return
	}
	h, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid height")
		return
	}
	p, ok := a.PolFollower.GetPrevoteLockProof(h)
	if !ok {
		a.writeError(w, http.StatusNotFound, "no prevote lock proof for height")
		return
	}
	a.writeJSON(w, http.StatusOK, p)
}

// GET /api/admin/consensus/pol/round-certificate/{height}
func (a *AdminAPI) handlePolRoundCertificateByHeight(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if a.PolFollower == nil {
		a.writeError(w, http.StatusServiceUnavailable, "POL follower not configured")
		return
	}
	const prefix = "/api/admin/consensus/pol/round-certificate/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		a.writeError(w, http.StatusBadRequest, "invalid path")
		return
	}
	raw := strings.TrimPrefix(r.URL.Path, prefix)
	raw = strings.TrimSuffix(raw, "/")
	if raw == "" {
		a.writeError(w, http.StatusBadRequest, "missing height")
		return
	}
	h, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid height")
		return
	}
	c, ok := a.PolFollower.GetRoundCertificate(h)
	if !ok {
		a.writeError(w, http.StatusNotFound, "no round certificate for height")
		return
	}
	a.writeJSON(w, http.StatusOK, c)
}

// GET /api/admin/audit?limit=&offset=&actor=&action=
func (a *AdminAPI) handleAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if a.Audit == nil {
		a.writeError(w, http.StatusServiceUnavailable, "audit trail not available")
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	actor := r.URL.Query().Get("actor")
	action := r.URL.Query().Get("action")
	a.writeJSON(w, http.StatusOK, map[string]interface{}{
		"entries": a.Audit.List(limit, offset, actor, action),
		"total":   a.Audit.Count(),
	})
}

// GET /api/admin/config/reload-dry-run — validate on-disk config vs ReloadPolicy without applying.
func (a *AdminAPI) handleReloadDryRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if a.HotReloader == nil {
		a.writeError(w, http.StatusServiceUnavailable, "hot reloader not available")
		return
	}
	changed, keys, polErr, loadErr := a.HotReloader.DryRunReload()
	monitoring.GetMetrics().RecordHotReloadDryRun(changed, polErr == nil, loadErr == nil)
	if a.Audit != nil {
		_ = a.Audit.Record("admin", "config.reload_dry_run", "", map[string]interface{}{
			"file_changed": changed,
			"policy_ok":    polErr == nil,
			"load_ok":      loadErr == nil,
		})
	}
	resp := map[string]interface{}{
		"file_changed":  changed,
		"changed_keys":  keys,
		"policy_ok":     polErr == nil,
		"load_ok":       loadErr == nil,
		"reload_count":  a.HotReloader.ReloadCount(),
		"current_port":  a.HotReloader.Current().NetworkPort,
		"hot_reload":    a.HotReloader.LastDryRunInfo(),
	}
	if polErr != nil {
		resp["policy_error"] = polErr.Error()
	}
	if loadErr != nil {
		resp["load_error"] = loadErr.Error()
	}
	status := http.StatusOK
	if loadErr != nil {
		status = http.StatusBadRequest
	} else if polErr != nil {
		status = http.StatusConflict
	}
	a.writeJSON(w, status, resp)
}
