package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/branding"
	"github.com/blackbeardONE/QSDM/pkg/bridge"
	"github.com/blackbeardONE/QSDM/pkg/contracts"
	"github.com/blackbeardONE/QSDM/pkg/envcompat"
	"github.com/blackbeardONE/QSDM/pkg/mesh3d"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
	"github.com/blackbeardONE/QSDM/pkg/storage"
	"github.com/blackbeardONE/QSDM/pkg/submesh"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

// msgWalletServiceUnavailable is the API error detail when the node started without a WalletService (init failed; API may still run).
const msgWalletServiceUnavailable = "wallet service not available (wallet did not initialize at node startup; check logs — e.g. liboqs/OpenSSL on CGO builds)"

// Handlers contains all API route handlers
type Handlers struct {
	authManager     *AuthManager
	userStore       *UserStore
	walletService   *wallet.WalletService
	storage         StorageInterface
	mesh3dValidator *mesh3d.Mesh3DValidator
	logger          *logging.Logger
	ngcIngestSecret string
	nvidiaLockEnabled bool
	nvidiaLockMaxAge  time.Duration
	nvidiaLockExpectedNodeID string
	nvidiaLockProofHMACSecret string
	nvidiaLockRequireIngestNonce bool
	nvidiaLockIngestNonceTTL     time.Duration
	nvidiaLockGateP2P            bool
	submeshManager               *submesh.DynamicSubmeshManager
	p2pTxBroadcast               func([]byte) error
	contractEngine               *contracts.ContractEngine
	bridgeProtocol               *bridge.BridgeProtocol
	atomicSwap                   *bridge.AtomicSwapProtocol
	bridgeRelay                  *bridge.P2PRelay
	nodeID                       string
	tokenRegistryMu              sync.RWMutex
	tokenRegistry                []TokenInfo
	tokenRegistryPath            string

	// Status endpoint wiring (Major Update Phase 2.2). These are populated by
	// the server at startup; the status handler tolerates nil sources.
	nodeRole        string
	peerCountSource func() int
	chainTipSource  func() uint64

	// csrfManager is the (optional) issuer/validator used by the
	// GET /api/v1/csrf-token endpoint. The server populates it via
	// SetCSRFManager during registerRoutes; tests can leave it nil
	// and the handler will return 503.
	csrfManager *CSRFManager
}

// SetCSRFManager attaches the per-server CSRFManager so the CSRF token
// endpoint can mint + cookie tokens. Wired by Server.registerRoutes.
func (h *Handlers) SetCSRFManager(cm *CSRFManager) { h.csrfManager = cm }

// NewHandlers creates a new handlers instance
func NewHandlers(authManager *AuthManager, userStore *UserStore, walletService *wallet.WalletService, storage StorageInterface, logger *logging.Logger, ngcIngestSecret string, nvidiaLockEnabled bool, nvidiaLockMaxAge time.Duration, nvidiaLockExpectedNodeID string, nvidiaLockProofHMACSecret string, nvidiaLockRequireIngestNonce bool, nvidiaLockIngestNonceTTL time.Duration, nvidiaLockGateP2P bool, submeshManager *submesh.DynamicSubmeshManager) *Handlers {
	if nvidiaLockMaxAge <= 0 {
		nvidiaLockMaxAge = 15 * time.Minute
	}
	return &Handlers{
		authManager:                  authManager,
		userStore:                    userStore,
		walletService:                walletService,
		storage:                      storage,
		mesh3dValidator:              mesh3d.NewMesh3DValidator(),
		logger:                       logger,
		ngcIngestSecret:              ngcIngestSecret,
		nvidiaLockEnabled:            nvidiaLockEnabled,
		nvidiaLockMaxAge:             nvidiaLockMaxAge,
		nvidiaLockExpectedNodeID:     nvidiaLockExpectedNodeID,
		nvidiaLockProofHMACSecret:    nvidiaLockProofHMACSecret,
		nvidiaLockRequireIngestNonce: nvidiaLockRequireIngestNonce,
		nvidiaLockIngestNonceTTL:     nvidiaLockIngestNonceTTL,
		nvidiaLockGateP2P:            nvidiaLockGateP2P,
		submeshManager:               submeshManager,
	}
}

// SetP2PTxBroadcast sets an optional callback invoked after a successful wallet send (e.g. gossip relay).
func (h *Handlers) SetP2PTxBroadcast(fn func([]byte) error) {
	h.p2pTxBroadcast = fn
}

// enforceNvidiaLock returns false if the request must be rejected (response already written).
func (h *Handlers) enforceNvidiaLock(w http.ResponseWriter) bool {
	if !h.nvidiaLockEnabled {
		return true
	}
	ok, msg := monitoring.NvidiaLockProofOK(h.nvidiaLockMaxAge, h.nvidiaLockExpectedNodeID, h.nvidiaLockProofHMACSecret, h.nvidiaLockRequireIngestNonce)
	if ok {
		return true
	}
	monitoring.RecordNvidiaLockHTTPBlock()
	h.logger.Warn("NVIDIA lock blocked state-changing API call", "detail", msg)
	writeErrorResponse(w, http.StatusForbidden, msg)
	return false
}

func (h *Handlers) enforceSubmeshWalletSend(w http.ResponseWriter, fee float64, geoTag string, txBytes []byte) bool {
	if h.submeshManager == nil {
		return true
	}
	if err := h.submeshManager.EnforceWalletSendPolicy(fee, geoTag, txBytes); err != nil {
		h.logger.Warn("Submesh policy rejected wallet send", "error", err)
		switch {
		case errors.Is(err, submesh.ErrSubmeshNoRoute):
			monitoring.RecordSubmeshAPIWalletRejectRoute()
		case errors.Is(err, submesh.ErrSubmeshPayloadTooLarge):
			monitoring.RecordSubmeshAPIWalletRejectSize()
		}
		writeErrorResponse(w, http.StatusUnprocessableEntity, err.Error())
		return false
	}
	return true
}

func (h *Handlers) enforceSubmeshPrivilegedPayload(w http.ResponseWriter, payload []byte) bool {
	if h.submeshManager == nil {
		return true
	}
	if err := h.submeshManager.EnforcePrivilegedLedgerPayloadCap(payload); err != nil {
		h.logger.Warn("Submesh policy rejected ledger operation", "error", err)
		if errors.Is(err, submesh.ErrSubmeshPayloadTooLarge) {
			monitoring.RecordSubmeshAPIPrivilegedRejectSize()
		}
		writeErrorResponse(w, http.StatusUnprocessableEntity, err.Error())
		return false
	}
	return true
}

func envTruthy(key string) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	return v == "1" || v == "true" || v == "yes"
}

func companionSubmeshName(m *submesh.DynamicSubmeshManager, fee float64, geo string, txBytes []byte) string {
	if m == nil {
		return "default-submesh"
	}
	if ds, err := m.MatchP2POrReject(fee, geo, txBytes); err == nil && ds != nil {
		return ds.Name
	}
	if ds, err := m.RouteTransaction(fee, geo); err == nil && ds != nil {
		return ds.Name
	}
	return "default-submesh"
}

// registerRoutes registers all API routes
func (s *Server) registerRoutes(mux *http.ServeMux) {
	handlers := NewHandlers(s.authManager, s.userStore, s.walletService, s.storage, s.logger, s.config.NGCIngestSecret, s.config.NvidiaLockEnabled, s.config.NvidiaLockMaxProofAge, s.config.NvidiaLockExpectedNodeID, s.config.NvidiaLockProofHMACSecret, s.config.NvidiaLockRequireIngestNonce, s.config.NvidiaLockIngestNonceTTL, s.config.NvidiaLockGateP2P, s.submeshManager)
	handlers.contractEngine = s.contractEngine
	handlers.bridgeProtocol = s.bridgeProtocol
	handlers.atomicSwap = s.atomicSwap
	handlers.bridgeRelay = s.bridgeRelay
	handlers.nodeID = s.nodeID
	if s.txGossipBroadcast != nil {
		handlers.SetP2PTxBroadcast(s.txGossipBroadcast)
	}
	if s.config != nil {
		handlers.SetNodeRole(s.config.NodeRole)
	}
	if s.csrfManager != nil {
		handlers.SetCSRFManager(s.csrfManager)
	}
	s.handlers = handlers
	// Apply any pre-Start status-source hooks that were
	// captured before s.handlers existed. Without this,
	// Server.SetChainTipSource called before Start would
	// silently no-op and /api/v1/status would forever
	// report chain_tip=0.
	if s.pendingChainTipSource != nil {
		handlers.SetChainTipSource(s.pendingChainTipSource)
		s.pendingChainTipSource = nil
	}
	if s.pendingPeerCountSource != nil {
		handlers.SetPeerCountSource(s.pendingPeerCountSource)
		s.pendingPeerCountSource = nil
	}

	if s.tokenRegistryPath != "" {
		handlers.tokenRegistryPath = s.tokenRegistryPath
		if n, err := handlers.LoadTokenRegistry(s.tokenRegistryPath); err != nil {
			s.logger.Warn("Failed to load token registry", "error", err)
		} else if n > 0 {
			s.logger.Info("Restored token registry from disk", "tokens", n)
		}
	}

	// Health check (public)
	mux.HandleFunc("/api/v1/health", handlers.HealthCheck)
	mux.HandleFunc("/api/v1/health/live", handlers.HealthLive)
	mux.HandleFunc("/api/v1/health/ready", handlers.HealthReady)

	// Public node status (node_role, coin metadata, branding). Unauthenticated.
	mux.HandleFunc("/api/v1/status", handlers.StatusHandler)

	// Authentication endpoints (public)
	mux.HandleFunc("/api/v1/auth/login", handlers.Login)
	mux.HandleFunc("/api/v1/auth/register", handlers.Register)

	// CSRF token issuer (public): clients fetch a fresh token here before
	// any cookie-authenticated state-changing call. Issuing is read-only
	// (GET) and side-effect-free apart from setting the qsdm_csrf cookie,
	// so it lives in publicPaths (see middleware.isPublicEndpoint) and
	// bypasses auth — the threat model is identical to /auth/login.
	mux.HandleFunc(CSRFTokenEndpoint, handlers.CSRFTokenHandler)

	// Logout (MED-7): authenticated; revokes the caller's JWT via the
	// TokenRevocationStore. Registered after Login/Register so the auth
	// middleware injects claims into the context.
	mux.HandleFunc("/api/v1/auth/logout", handlers.Logout)

	// Versions probe (MED-4): public read of the API version catalogue
	// (status, deprecation, sunset). Lets SDKs detect upcoming breaks
	// without paying the cost of every endpoint emitting Deprecation
	// headers on every response.
	mux.HandleFunc("/api/v1/versions", handlers.Versions)

	// Wallet endpoints (authenticated)
	mux.HandleFunc("/api/v1/wallet/create", handlers.CreateWallet)
	mux.HandleFunc("/api/v1/wallet/balance", handlers.GetBalance)
	mux.HandleFunc("/api/v1/wallet/send", handlers.SendTransaction)
	// v0.4.0 (Session 95): self-custody signed-envelope submission.
	// Unlike /wallet/send (which signs from the validator's own
	// wallet, ignoring JWT claims), /wallet/submit-signed accepts
	// a fully client-signed wallet.TransactionData envelope and
	// only applies the balance change after verifying the
	// envelope's ML-DSA-87 signature against its own embedded
	// public_key, with sender = hex(sha256(public_key)) enforced.
	// See QSDM/docs/docs/V040_WALLET_SEND_DESIGN.md.
	mux.HandleFunc("/api/v1/wallet/submit-signed", handlers.SubmitSignedTransaction)
	// v0.4.1 (Session 100): public-read helper that lets a
	// self-custody client build the next envelope's `nonce` field
	// without an authenticated session. See
	// QSDM/docs/docs/V041_REPLAY_PROTECTION_DESIGN.md §5.2/§5.3.
	mux.HandleFunc("/api/v1/wallet/nonce", handlers.GetWalletNonce)
	mux.HandleFunc("/api/v1/wallet/address", handlers.GetAddress)
	mux.HandleFunc("/api/v1/wallet/mint", handlers.MintMainCoin)

	// Token endpoints (authenticated)
	mux.HandleFunc("/api/v1/tokens/mint", handlers.MintToken)
	mux.HandleFunc("/api/v1/tokens/create", handlers.CreateToken)
	mux.HandleFunc("/api/v1/tokens/list", handlers.ListTokens)

	// Transaction endpoints (authenticated)
	mux.HandleFunc("/api/v1/transactions", handlers.GetTransactions)
	mux.HandleFunc("/api/v1/transactions/", handlers.GetTransactionByID)

	// Validator endpoints (authenticated)
	mux.HandleFunc("/api/v1/validator/validate", handlers.ValidateTransaction)

	// Contract endpoints (authenticated)
	mux.HandleFunc("/api/v1/contracts/deploy", handlers.DeployContract)
	mux.HandleFunc("/api/v1/contracts/list", handlers.ListContracts)
	mux.HandleFunc("/api/v1/contracts/templates", handlers.ListContractTemplates)
	mux.HandleFunc("/api/v1/contracts/traces", handlers.ListContractTraces)
	mux.HandleFunc("/api/v1/contracts/traces/stats", handlers.ContractTraceStats)
	mux.HandleFunc("/api/v1/contracts/traces/ws", handlers.StreamContractTracesWS)
	mux.HandleFunc("/api/v1/contracts/trace/", handlers.GetContractTrace)
	mux.HandleFunc("/api/v1/contracts/", handlers.routeContract)

	// Bridge endpoints (authenticated)
	mux.HandleFunc("/api/v1/bridge/locks", handlers.BridgeListLocks)
	mux.HandleFunc("/api/v1/bridge/lock", handlers.BridgeLockAsset)
	mux.HandleFunc("/api/v1/bridge/locks/", handlers.routeBridgeLock)
	mux.HandleFunc("/api/v1/bridge/swaps", handlers.SwapList)
	mux.HandleFunc("/api/v1/bridge/swap", handlers.SwapInitiate)
	mux.HandleFunc("/api/v1/bridge/swaps/", handlers.routeBridgeSwap)

	// Network topology (live JSON projection, consumed by the dashboard WebGL view)
	mux.HandleFunc("/api/v1/network/topology", handlers.GetNetworkTopology)

	// NGC GPU proof sidecar (shared secret; QSDM_NGC_INGEST_SECRET — the pre-rebrand QSDMPLUS_NGC_INGEST_SECRET env var is no longer read, see pkg/audit/checklist.go rebrand-02)
	mux.HandleFunc("/api/v1/monitoring/ngc-proof", handlers.NGCProofIngest)
	mux.HandleFunc("/api/v1/monitoring/ngc-challenge", handlers.NGCIngestChallenge)
	mux.HandleFunc("/api/v1/monitoring/ngc-proofs", handlers.NGCProofList)

	// Mining endpoints (Major Update Phase 4.3). Return 503 until a
	// MiningService is installed via api.SetMiningService(...).
	mux.HandleFunc("/api/v1/mining/work", handlers.MiningWorkHandler)
	mux.HandleFunc("/api/v1/mining/submit", handlers.MiningSubmitHandler)
	// Solo-mode-only balance probe; returns 503 when no
	// MiningAccountProbe is wired. Lets operators verify
	// mining rewards are landing on the CHAIN AccountStore
	// without going through the (separately-stored) wallet
	// API. Registered unconditionally so miners can probe.
	mux.HandleFunc("/api/v1/mining/account", handlers.MiningAccountHandler)
	// Read-only emission probe; surfaces the §8 schedule,
	// current per-block reward, cumulative emission, and
	// remaining supply. Wired regardless of solo mode
	// because the data is pure schedule state — no
	// AccountStore peek. SDK clients render tokenomics
	// widgets from this endpoint.
	mux.HandleFunc("/api/v1/mining/emission", handlers.MiningEmissionHandler)
	// Read-only block-header probe; surfaces the last N
	// blocks' headers (height, hash, tx_count, timestamp,
	// producer) for the public chain dashboard. Returns
	// 503 until a MiningBlocksProbe is wired. Registered
	// unconditionally so an unwired peer can still answer
	// "no blocks probe" rather than 404.
	mux.HandleFunc("/api/v1/mining/blocks", handlers.MiningBlocksHandler)
	// Tier-2 telemetry advisory probe (pkg/mining/telemetrycheck).
	// Surfaces recent spec-anomalies — proofs whose claimed
	// GPU specs disagreed with the catalog of reference
	// profiles. Non-consensus by design; the listing is
	// strictly advisory. Returns 503 when the validator
	// did not opt into Tier-2 via QSDM_SPEC_CHECK_ENABLED.
	mux.HandleFunc("/api/v1/mining/spec-anomalies", handlers.SpecAnomaliesHandler)
	// Tier-3 reward-downgrade probe (pkg/mining/telemetrycheck).
	// Surfaces per-miner sliding-window state + the current
	// reward multiplier each miner is being paid at. Public
	// so a flagged miner can self-diagnose; returns 503 when
	// the validator did not opt into Tier-3 via
	// QSDM_SPEC_PENALTY_ENABLED.
	mux.HandleFunc("/api/v1/mining/penalty", handlers.SpecPenaltyHandler)
	// Per-tx receipt probe. Path is /api/v1/receipts/{tx_id}
	// to match qsdmcli `receipt <tx-id>` and preserve the
	// stable URL convention used by the CLI. Returns 503
	// until SetMiningReceiptProbe is wired (same posture as
	// the other read-only probes); 404 on tx-id miss.
	mux.HandleFunc("/api/v1/receipts/", handlers.MiningReceiptHandler)
	// Receipts list probe. Note the absence of trailing
	// slash: Go's ServeMux keeps "/api/v1/receipts" (exact)
	// and "/api/v1/receipts/" (prefix-with-tx-id) as separate
	// routes. The list endpoint is the dashboard's "recent
	// transactions" tile feed; same 503/400/200 posture as
	// /mining/blocks. The per-tx receipt route registered
	// above continues to handle requests of the form
	// /api/v1/receipts/<tx-id>.
	mux.HandleFunc("/api/v1/receipts", handlers.MiningReceiptsListHandler)
	// Mining challenge endpoint (Phase 2c-iii,
	// MINING_PROTOCOL_V2.md §6.2). Returns 503 until
	// a ChallengeIssuer is installed via api.SetChallengeIssuer(...).
	// Registered unconditionally so miners can probe readiness.
	mux.HandleFunc("/api/v1/mining/challenge", handlers.MiningChallengeHandler)

	// Mining enrollment endpoints (Phase 2c-x,
	// MINING_PROTOCOL_V2.md §8.1 + §9.1). Two symmetric
	// POSTs accept signed mempool.Tx envelopes carrying enrollment
	// payloads (qsdm/enroll/v1). Return 503 until a MempoolSubmitter
	// is installed via api.SetEnrollmentMempool(...). Stateless
	// payload validation runs in the mempool admission gate
	// (enrollment.AdmissionChecker); stateful checks (balance,
	// node_id uniqueness) happen at block-apply time.
	mux.HandleFunc("/api/v1/mining/enroll", handlers.EnrollmentSubmitHandler)
	mux.HandleFunc("/api/v1/mining/unenroll", handlers.UnenrollmentSubmitHandler)

	// Mining enrollment READ endpoint (Phase 2c-xii). Companion
	// to the write endpoints above. Returns the on-chain record
	// for {node_id} with a sanitized view (HMACKey omitted) plus
	// a derived Phase/Slashable signal so clients don't have to
	// re-derive lifecycle state. Returns 503 until a registry is
	// installed via api.SetEnrollmentRegistry(...). Mounted on
	// the trailing-slash prefix so {node_id} can carry hyphens
	// and other URL-safe characters without per-segment routing.
	mux.HandleFunc("/api/v1/mining/enrollment/", handlers.EnrollmentQueryHandler)

	// Mining enrollment LIST endpoint — paginated walk over the
	// on-chain registry. Companion to the per-record query
	// route above. Cursor + limit + optional phase filter
	// (active | pending_unbond | revoked). Returns 503 until a
	// lister is installed via api.SetEnrollmentLister(...). No
	// trailing slash because there is no path component — the
	// page parameters travel as URL query.
	mux.HandleFunc("/api/v1/mining/enrollments", handlers.EnrollmentListHandler)

	// Mining slashing endpoint (Phase 2c-xi,
	// MINING_PROTOCOL_V2.md §8.2 + §9.1). Symmetric to the
	// enrollment endpoints: accepts a signed mempool.Tx envelope
	// carrying a slashing payload (qsdm/slash/v1). Returns 503
	// until a MempoolSubmitter is installed via
	// api.SetSlashMempool(...). Stateless payload validation runs
	// in the mempool admission gate (slashing.AdmissionChecker);
	// stateful checks (registry lookup, evidence verification,
	// stake debit) happen at block-apply time in
	// chain.SlashApplier.
	mux.HandleFunc("/api/v1/mining/slash", handlers.SlashSubmitHandler)

	// Mining slashing READ endpoint — receipts. Companion to
	// the POST /api/v1/mining/slash write endpoint. Returns the
	// applied/rejected outcome captured by the SlashReceiptStore
	// the chain wires up at boot. Returns 503 until a store is
	// installed via api.SetSlashReceiptStore(...). 404 if the tx
	// id is unknown or has been FIFO-evicted (the store is
	// bounded for OOM safety). Mounted on the trailing-slash
	// prefix so {tx_id} can carry hyphens, hex, etc., without
	// per-segment routing — same idiom as the enrollment query
	// route above.
	mux.HandleFunc("/api/v1/mining/slash/", handlers.SlashReceiptHandler)

	// v2 governance — runtime parameter tuning (MINING_PROTOCOL_V2.md
	// §9.4). Companion to the off-chain qsdm/gov/v1 write path
	// that flows through /api/v1/transactions. Two read routes:
	//
	//   GET /api/v1/governance/params           (full snapshot)
	//   GET /api/v1/governance/params/{name}    (single param)
	//
	// Both return 503 until a provider is installed via
	// api.SetGovernanceProvider(...). The mux pattern order
	// matters: the trailing-slash variant MUST come first so
	// /params/{name} routes through GovernanceParamHandler;
	// /params with no path tail falls through to
	// GovernanceParamsHandler.
	mux.HandleFunc("/api/v1/governance/params/", handlers.GovernanceParamHandler)
	mux.HandleFunc("/api/v1/governance/params", handlers.GovernanceParamsHandler)

	// v2 attestation rejection ring (MINING_PROTOCOL_V2.md §4.6).
	// Per-event detail companion to the
	// qsdm_attest_archspoof_rejected_total / hashrate counters.
	// Returns 503 until a lister is installed via
	// api.SetRecentRejectionLister(...). Collection-only — there
	// is no /{id} variant because rejections have no external
	// primary key (the Seq is store-internal).
	mux.HandleFunc("/api/v1/attest/recent-rejections", handlers.RecentRejectionsHandler)

	// Trust / attestation transparency endpoints (Major Update Phase 5.1).
	// Registered unconditionally. If no aggregator is installed via
	// api.SetTrustAggregator, the handlers return 503 warming-up; if the
	// operator opted out, they return 404. Both endpoints are public
	// (see middleware.isPublicEndpoint) and deliberately anti-claim:
	// the widget must always render "X of Y", never "X".
	mux.HandleFunc("/api/v1/trust/attestations/summary", handlers.TrustSummaryHandler)
	mux.HandleFunc("/api/v1/trust/attestations/recent", handlers.TrustRecentHandler)

	// Audit checklist transparency endpoints (Session 77 wire-up).
	// Public-API mirror of internal/dashboard's /api/audit/{summary,
	// items} so SDK consumers, the public landing page widget, and
	// third-party audit aggregators can read the runtime-verified
	// score without an operator session. Same posture as
	// /api/v1/trust/attestations/* — registered unconditionally,
	// in publicPaths (see middleware.isPublicEndpoint), rate-limited
	// by the per-IP limiter in security.go.
	mux.HandleFunc("/api/v1/audit/summary", handlers.AuditSummaryHandler)
	mux.HandleFunc("/api/v1/audit/items", handlers.AuditItemsHandler)

	if s.adminAPI != nil {
		s.adminAPI.RegisterRoutes(mux)
	}
}

// routeContract dispatches /api/v1/contracts/{id}[/execute] to the correct handler.
func (h *Handlers) routeContract(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/contracts/")
	if path == "" {
		writeErrorResponse(w, http.StatusBadRequest, "contract_id required")
		return
	}
	if strings.HasSuffix(path, "/execute") {
		h.ExecuteContract(w, r)
	} else {
		h.GetContract(w, r)
	}
}

// routeBridgeLock dispatches /api/v1/bridge/locks/{id}[/redeem|/refund].
func (h *Handlers) routeBridgeLock(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/bridge/locks/")
	if path == "" {
		writeErrorResponse(w, http.StatusBadRequest, "lock_id required")
		return
	}
	switch {
	case strings.HasSuffix(path, "/redeem"):
		h.BridgeRedeemAsset(w, r)
	case strings.HasSuffix(path, "/refund"):
		h.BridgeRefundAsset(w, r)
	default:
		h.BridgeGetLock(w, r)
	}
}

// routeBridgeSwap dispatches /api/v1/bridge/swaps/{id}[/participate|/complete|/refund].
func (h *Handlers) routeBridgeSwap(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/bridge/swaps/")
	if path == "" {
		writeErrorResponse(w, http.StatusBadRequest, "swap_id required")
		return
	}
	switch {
	case strings.HasSuffix(path, "/participate"):
		h.SwapParticipate(w, r)
	case strings.HasSuffix(path, "/complete"):
		h.SwapComplete(w, r)
	case strings.HasSuffix(path, "/refund"):
		h.SwapRefund(w, r)
	default:
		h.SwapGet(w, r)
	}
}

// HealthCheck returns API health status
func (h *Handlers) HealthCheck(w http.ResponseWriter, r *http.Request) {
	lockOK, _ := monitoring.NvidiaLockProofOK(h.nvidiaLockMaxAge, h.nvidiaLockExpectedNodeID, h.nvidiaLockProofHMACSecret, false)
	nodeBinding := strings.TrimSpace(h.nvidiaLockExpectedNodeID) != ""
	hmacOn := strings.TrimSpace(h.nvidiaLockProofHMACSecret) != ""
	ttl := int64(h.nvidiaLockIngestNonceTTL.Seconds())
	if ttl <= 0 {
		ttl = int64((10 * time.Minute).Seconds())
	}
	resp := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().Unix(),
		"version":   "1.0.0",
		"product":   branding.Name,
		"tagline":   branding.Tagline,
		"nvidia_lock": map[string]interface{}{
			"enabled":                 h.nvidiaLockEnabled,
			"proof_ok":                lockOK,
			"max_proof_age_seconds":   int(h.nvidiaLockMaxAge.Seconds()),
			"node_id_binding_enabled": nodeBinding,
			"hmac_required":           hmacOn,
			"ingest_nonce_required":   h.nvidiaLockRequireIngestNonce,
			"ingest_nonce_ttl_seconds": ttl,
			"http_blocks_total":       monitoring.NvidiaLockHTTPBlockCount(),
			"ngc_challenge_issued_total":       monitoring.NGCChallengeIssuedCount(),
			"ngc_challenge_rate_limited_total": monitoring.NGCChallengeRateLimitedCount(),
			"ngc_ingest_nonce_pool_size":         monitoring.NGCIngestNoncePoolSize(),
			"p2p_gate_enabled":                   h.nvidiaLockEnabled && h.nvidiaLockGateP2P,
			"p2p_rejects_total":                  monitoring.NvidiaLockP2PRejectCount(),
			"ngc_proof_ingest":                   monitoring.NGCIngestStatsMap(),
		},
	}
	writeJSONResponse(w, http.StatusOK, resp)
}

// HealthLive is a minimal liveness probe (process accepting HTTP).
func (h *Handlers) HealthLive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"status":    "alive",
		"timestamp": time.Now().Unix(),
		"product":   branding.Name,
	})
}

// HealthReady reports dependency checks for orchestration readiness probes.
// Returns 503 when storage Ready() fails; wallet_service is informational (ok vs unavailable).
func (h *Handlers) HealthReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	checks := map[string]string{}
	storageOK := true
	if h.storage == nil {
		checks["storage"] = "failed: storage not configured"
		storageOK = false
	} else if err := h.storage.Ready(); err != nil {
		checks["storage"] = "failed: " + err.Error()
		storageOK = false
	} else {
		checks["storage"] = "ok"
	}
	if h.walletService == nil {
		checks["wallet_service"] = "unavailable"
	} else {
		checks["wallet_service"] = "ok"
	}
	statusStr := "ready"
	code := http.StatusOK
	if !storageOK {
		statusStr = "not_ready"
		code = http.StatusServiceUnavailable
	}
	writeJSONResponse(w, code, map[string]interface{}{
		"status":    statusStr,
		"timestamp": time.Now().Unix(),
		"checks":    checks,
	})
}

// Logout revokes the caller's current access token by adding its nonce
// to the revocation store. Subsequent requests presenting the same JWT
// are rejected by AuthMiddleware via the revocation check in
// AuthManager.ValidateToken.
//
// Authentication is REQUIRED (claims must be present in context) — the
// route is registered on the auth-protected path so AuthMiddleware
// populates the claims before this handler runs. Calling /auth/logout
// without a valid Bearer token returns 401, which is the same posture
// as every other authenticated endpoint.
//
// The handler is safe under concurrent logout-then-reuse races: the
// revocation entry lives for the full natural token lifetime, so any
// in-flight request that already started a handler completes (the
// revocation only blocks NEW calls past the auth middleware).
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	claims, ok := ClaimsFromContext(r.Context())
	if !ok {
		writeErrorResponse(w, http.StatusUnauthorized, "missing authentication")
		return
	}
	if h.authManager == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "auth manager not configured")
		return
	}
	h.authManager.RevokeToken(claims)
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"message": "logged out",
	})
}

// CSRFTokenHandler issues a fresh CSRF token for the calling client.
//
// Contract: GET /api/v1/csrf-token returns
//
//	{ "csrf_token": "<base64url>", "expires_in_seconds": 3600 }
//
// and ALSO sets the qsdm_csrf cookie (Secure, SameSite=Strict) carrying the
// same value. State-changing requests must echo the token in the
// X-CSRF-Token header; the CSRFMiddleware enforces both the double-submit
// (cookie == header) and synchronizer-token (server-side store) checks.
//
// Method-not-allowed and service-unavailable responses match the rest of
// the API: 405 for non-GET, 503 with a clear detail when no manager is
// wired (defense against running an API server that forgot to bootstrap
// CSRF — the dashboard would silently fall over otherwise).
//
// When the caller is authenticated (claims in context), the issued token
// is bound to claims.UserID so it cannot be replayed across users.
func (h *Handlers) CSRFTokenHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.csrfManager == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "CSRF manager not configured")
		return
	}

	var userID string
	if claims, ok := ClaimsFromContext(r.Context()); ok {
		userID = claims.UserID
	}

	token, err := h.csrfManager.IssueToken(w, userID, r.TLS != nil)
	if err != nil {
		if h.logger != nil {
			h.logger.Error("Failed to issue CSRF token", "error", err)
		}
		writeErrorResponse(w, http.StatusInternalServerError, "failed to issue CSRF token")
		return
	}

	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"csrf_token":         token,
		"expires_in_seconds": int(defaultCSRFTokenTTL.Seconds()),
	})
}

// LoginRequest represents a login request
type LoginRequest struct {
	Address  string `json:"address"`
	Password string `json:"password"` // In production, use proper password hashing
}

// LoginResponse represents a login response
type LoginResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// Login handles user authentication
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Address = strings.TrimSpace(req.Address)
	req.Address = strings.ToLower(req.Address)
	if err := ValidateAddress(req.Address); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	// Authenticate user
	// Check if account is locked
	locked, lockErr := h.authManager.IsAccountLocked(req.Address)
	if locked {
		// MED-2: sanitize the address before logging — even though
		// ValidateAddress already rejected non-hex inputs above, defence
		// in depth keeps log forging (CWE-117) impossible if a future
		// validator change loosens the address grammar.
		h.logger.Warn("Login attempt on locked account", "address", SanitizeForLog(req.Address), "error", lockErr)
		writeErrorResponse(w, http.StatusTooManyRequests, lockErr.Error())
		return
	}

	// Authenticate user
	user, err := h.userStore.AuthenticateUser(req.Address, req.Password)
	if err != nil {
		// MED-8: surface the failed-login event in the security metrics
		// stream so SOC alerting can catch credential-stuffing waves
		// before the per-account lockout kicks in.
		monitoring.RecordFailedLogin()
		// Record failed attempt
		h.authManager.RecordFailedAttempt(req.Address)

		// Get remaining attempts
		remaining := h.authManager.GetRemainingAttempts(req.Address)
		h.logger.Warn("Authentication failed", "address", SanitizeForLog(req.Address), "error", err, "remaining_attempts", remaining)
		if remaining <= 0 {
			monitoring.RecordAccountLockout()
		}

		if remaining > 0 {
			writeErrorResponse(w, http.StatusUnauthorized, fmt.Sprintf("invalid credentials. %d attempts remaining", remaining))
		} else {
			writeErrorResponse(w, http.StatusTooManyRequests, "account locked due to too many failed attempts")
		}
		return
	}

	// Record successful attempt (clears failed attempts)
	h.authManager.RecordSuccessfulAttempt(req.Address)

	// Create access token (15 minutes)
	accessToken, err := h.authManager.CreateToken(
		user.Address,
		user.Address,
		user.Role,
		TokenTypeAccess,
		15*time.Minute,
	)
	if err != nil {
		// MED-1: log full details server-side, return correlation id only.
		WriteServerError(w, h.logger, "create_access_token", err)
		return
	}

	// Create refresh token (7 days)
	refreshToken, err := h.authManager.CreateToken(
		user.Address,
		user.Address,
		user.Role,
		TokenTypeRefresh,
		7*24*time.Hour,
	)
	if err != nil {
		WriteServerError(w, h.logger, "create_refresh_token", err)
		return
	}

	writeJSONResponse(w, http.StatusOK, LoginResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    900, // 15 minutes in seconds
	})
}

// RegisterRequest represents a registration request
type RegisterRequest struct {
	Address  string `json:"address"`
	Password string `json:"password"`
}

// Register handles user registration
func (h *Handlers) Register(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate input
	if req.Address == "" {
		writeErrorResponse(w, http.StatusBadRequest, "address is required")
		return
	}
	req.Address = strings.TrimSpace(req.Address)
	req.Address = strings.ToLower(req.Address)
	if err := ValidateAddress(req.Address); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	// Validate password with enhanced security policy
	if err := ValidatePassword(req.Password); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("password validation failed: %v", err))
		return
	}

	// Register user (default role: "user")
	err := h.userStore.RegisterUser(req.Address, req.Password, "user")
	if err != nil {
		if err.Error() == "user already exists" {
			writeErrorResponse(w, http.StatusConflict, "user already exists")
			return
		}
		// MED-1: do not echo raw storage / crypto errors back to the user.
		WriteServerError(w, h.logger, "register_user", err)
		return
	}

	writeJSONResponse(w, http.StatusCreated, map[string]interface{}{
		"message": "user registered successfully",
		"address": req.Address,
	})
}

// CreateWalletRequest represents a wallet creation request
type CreateWalletRequest struct {
	InitialBalance float64 `json:"initial_balance,omitempty"`
}

// CreateWalletResponse represents a wallet creation response
type CreateWalletResponse struct {
	Address string  `json:"address"`
	Balance float64 `json:"balance"`
}

// CreateWallet creates a new wallet
func (h *Handlers) CreateWallet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Create a new wallet instance for each request (generates unique address)
	// This works even without CGO (uses fallback implementation)
	newWallet, err := wallet.NewWalletService()
	if err != nil {
		monitoring.RecordWalletCreate(monitoring.WalletCreateResultFailed)
		h.logger.Error("Failed to create new wallet", "error", err)
		writeErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("failed to create wallet: %v", err))
		return
	}

	address := newWallet.GetAddress()
	balance := float64(newWallet.GetBalance())

	monitoring.RecordWalletCreate(monitoring.WalletCreateResultSuccess)
	writeJSONResponse(w, http.StatusCreated, CreateWalletResponse{
		Address: address,
		Balance: balance,
	})
}

// GetBalance returns the wallet balance
func (h *Handlers) GetBalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Get address from query (required for public access)
	address := r.URL.Query().Get("address")
	if address == "" {
		// Try to get from authenticated user if available
		if claims, ok := ClaimsFromContext(r.Context()); ok {
			address = claims.Address
		} else {
			writeErrorResponse(w, http.StatusBadRequest, "address parameter is required")
			return
		}
	}

	balance, err := h.storage.GetBalance(address)
	if err != nil {
		monitoring.RecordWalletBalanceQuery(monitoring.WalletBalanceResultStorageError)
		h.logger.Error("Failed to get balance", "error", err, "address", address)
		writeErrorResponse(w, http.StatusInternalServerError, "failed to get balance")
		return
	}

	monitoring.RecordWalletBalanceQuery(monitoring.WalletBalanceResultSuccess)
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"address": address,
		"balance": balance,
	})
}

// GetWalletNonceResponse is the shape returned by GET /api/v1/wallet/nonce.
// `nonce` is the LAST-APPLIED nonce — callers building a v0.4.1
// envelope must use `nonce + 1`. Surfaced here as a distinct field
// (rather than pre-incrementing server-side) so a caller who
// crashes mid-flight can re-query without server-side state drift.
type GetWalletNonceResponse struct {
	Sender string `json:"sender"`
	Nonce  uint64 `json:"nonce"`
	// Next is sugar: the value the caller should put in the next
	// envelope's `nonce` field (nonce + 1). Provided so wallets
	// that build envelopes from a template don't need to do the
	// arithmetic themselves.
	Next uint64 `json:"next"`
}

// GetWalletNonce returns the last-applied nonce for `sender` so a
// v0.4.1 self-custody client can build the next envelope with
// `nonce: response.next`. Public-read endpoint (matches the
// balance/{address} posture). The endpoint is fail-closed on
// storage errors: a 500 here keeps the client from defaulting to
// nonce=1 against a storage layer it can't trust.
//
// Added in v0.4.1 (Session 100) per V041_REPLAY_PROTECTION_DESIGN.md
// §5.2 (browser-wallet integration) + §5.3 (qsdmcli wallet sign-tx).
// Validators that expose this endpoint MUST be on v0.4.1+; v0.4.0
// validators 404 (route not registered).
func (h *Handlers) GetWalletNonce(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	sender := r.URL.Query().Get("sender")
	if sender == "" {
		// Allow JWT-authenticated callers to query their own nonce
		// without re-typing the sender field — symmetric with
		// /wallet/balance, where claims.Address backs up the
		// query param.
		if claims, ok := ClaimsFromContext(r.Context()); ok {
			sender = claims.Address
		} else {
			writeErrorResponse(w, http.StatusBadRequest, "sender query parameter is required")
			return
		}
	}
	if err := ValidateAddress(sender); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid sender address: %v", err))
		return
	}

	nonce, err := h.storage.GetNonce(sender)
	if err != nil {
		h.logger.Error("GetWalletNonce: storage error", "error", err, "sender", sender)
		writeErrorResponse(w, http.StatusInternalServerError, "failed to read nonce from storage")
		return
	}

	writeJSONResponse(w, http.StatusOK, GetWalletNonceResponse{
		Sender: sender,
		Nonce:  nonce,
		Next:   nonce + 1,
	})
}

// SendTransactionRequest represents a transaction request
type SendTransactionRequest struct {
	Recipient   string   `json:"recipient"`
	Amount      float64  `json:"amount"`
	Fee         float64  `json:"fee"`
	GeoTag      string   `json:"geotag"`
	ParentCells []string `json:"parent_cells"`
}

// SendTransactionResponse represents a transaction response
type SendTransactionResponse struct {
	TransactionID string `json:"transaction_id"`
	Status        string `json:"status"`
}

// SendTransaction sends a transaction
func (h *Handlers) SendTransaction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if h.walletService == nil {
		monitoring.RecordWalletSend(monitoring.WalletSendResultNoWalletService)
		writeErrorResponse(w, http.StatusServiceUnavailable, msgWalletServiceUnavailable)
		return
	}

	claims, ok := ClaimsFromContext(r.Context())
	if !ok {
		monitoring.RecordWalletSend(monitoring.WalletSendResultUnauthenticated)
		writeErrorResponse(w, http.StatusUnauthorized, "missing authentication")
		return
	}
	_ = claims // Use claims for future enhancements

	if !h.enforceNvidiaLock(w) {
		monitoring.RecordWalletSend(monitoring.WalletSendResultNvidiaLockBlocked)
		return
	}

	var req SendTransactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		monitoring.RecordWalletSend(monitoring.WalletSendResultInvalidRequest)
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate request with comprehensive validation
	if err := ValidateAddress(req.Recipient); err != nil {
		monitoring.RecordWalletSend(monitoring.WalletSendResultInvalidRequest)
		writeErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid recipient address: %v", err))
		return
	}
	if err := ValidateAmount(req.Amount); err != nil {
		monitoring.RecordWalletSend(monitoring.WalletSendResultInvalidRequest)
		writeErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid amount: %v", err))
		return
	}
	if req.Fee < 0 {
		monitoring.RecordWalletSend(monitoring.WalletSendResultInvalidRequest)
		writeErrorResponse(w, http.StatusBadRequest, "fee cannot be negative")
		return
	}
	if err := ValidateAmount(req.Fee); err != nil {
		monitoring.RecordWalletSend(monitoring.WalletSendResultInvalidRequest)
		writeErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid fee: %v", err))
		return
	}
	if err := ValidateGeoTag(req.GeoTag); err != nil {
		monitoring.RecordWalletSend(monitoring.WalletSendResultInvalidRequest)
		writeErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid geotag: %v", err))
		return
	}
	if err := ValidateParentCells(req.ParentCells); err != nil {
		monitoring.RecordWalletSend(monitoring.WalletSendResultInvalidRequest)
		writeErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid parent cells: %v", err))
		return
	}

	// Create transaction
	txBytes, err := h.walletService.CreateTransaction(
		req.Recipient,
		int(req.Amount),
		req.Fee,
		req.GeoTag,
		req.ParentCells,
	)
	if err != nil {
		monitoring.RecordWalletSend(monitoring.WalletSendResultTxCreateFailed)
		h.logger.Error("Failed to create transaction", "error", err)
		writeErrorResponse(w, http.StatusInternalServerError, "failed to create transaction")
		return
	}

	// Submesh-policy rejects are recorded by enforceSubmeshWalletSend
	// itself (qsdm_submesh_api_wallet_reject_*_total counters); we
	// don't double-count here. The send returns without a
	// qsdm_wallet_send_total bump in that case.
	if !h.enforceSubmeshWalletSend(w, req.Fee, req.GeoTag, txBytes) {
		return
	}

	// Store transaction
	if err := h.storage.StoreTransaction(txBytes); err != nil {
		monitoring.RecordWalletSend(monitoring.WalletSendResultStoreFailed)
		h.logger.Error("Failed to store transaction", "error", err)
		writeErrorResponse(w, http.StatusInternalServerError, "failed to store transaction")
		return
	}

	monitoring.RecordWalletSend(monitoring.WalletSendResultSuccess)

	if h.p2pTxBroadcast != nil {
		if err := h.p2pTxBroadcast(txBytes); err != nil {
			h.logger.Warn("P2P tx broadcast after send failed", "error", err)
		}
	}

	if envcompat.Truthy("QSDM_PUBLISH_MESH_COMPANION", "QSDM_PUBLISH_MESH_COMPANION") && h.p2pTxBroadcast != nil && len(req.ParentCells) >= 2 {
		sm := companionSubmeshName(h.submeshManager, req.Fee, req.GeoTag, txBytes)
		companion, err := mesh3d.BuildMeshCompanionFromWalletJSON(txBytes, req.ParentCells[:2], sm)
		if err != nil {
			h.logger.Warn("mesh companion wire build failed", "error", err)
		} else {
			if err := h.p2pTxBroadcast(companion); err != nil {
				h.logger.Warn("P2P mesh companion broadcast failed", "error", err)
			} else {
				monitoring.RecordMeshCompanionPublish()
			}
		}
	}

	// Parse transaction ID from stored transaction
	var txData map[string]interface{}
	if err := json.Unmarshal(txBytes, &txData); err == nil {
		if txID, ok := txData["id"].(string); ok {
			writeJSONResponse(w, http.StatusCreated, SendTransactionResponse{
				TransactionID: txID,
				Status:        "pending",
			})
			return
		}
	}

	writeJSONResponse(w, http.StatusCreated, SendTransactionResponse{
		TransactionID: "unknown",
		Status:        "pending",
	})
}

// SubmitSignedTransactionResponse is the success-path body for the
// v0.4.0 /api/v1/wallet/submit-signed endpoint. The shape is
// intentionally a superset of SendTransactionResponse so a future
// browser/Go client that abstracts over both endpoints (or a CLI
// that's pinned to one) renders the same fields.
type SubmitSignedTransactionResponse struct {
	TransactionID string `json:"transaction_id"`
	Status        string `json:"status"`     // "accepted" or "duplicate"
	Broadcast     string `json:"broadcast"`  // "p2p" or "local-only"
}

// SubmitSignedTransaction accepts a fully client-signed
// wallet.TransactionData envelope and applies the balance change
// iff the envelope's ML-DSA-87 signature verifies against its own
// embedded public_key AND sender == hex(sha256(public_key)). This
// is the self-custody counterpart to /api/v1/wallet/send (which
// signs from the validator's own wallet).
//
// Design contract (v0.4.0, Session 95): see
// QSDM/docs/docs/V040_WALLET_SEND_DESIGN.md. Audit row: api-06 in
// pkg/audit/checklist.go.
//
// Known v0.4.0 limitations, documented and intentionally shipped:
//
//	(1) No per-account nonce. The current TransactionData has only
//	    `id` (hex16 of sha256(sender||recipient||timestamp_ns)), so
//	    a client controlling the nanosecond-timestamp can craft
//	    arbitrarily many distinct tx_ids for the same logical
//	    transfer. Replay protection inside a single tx_id is solid
//	    (storage.StoreTransaction skips on duplicate); replay across
//	    distinct tx_ids is the v0.4.1 nonce-schema fix.
//	(2) pkg/storage/sqlite.go::UpdateBalance warns-and-proceeds on
//	    negative balance. The pre-flight GetBalance check we do
//	    here closes the obvious case (caller-honest insufficient-
//	    funds), but a concurrent race between two simultaneous
//	    submit-signed calls from the same sender can still drop the
//	    on-disk balance below zero. Atomic debit/credit is the
//	    v0.4.1 storage-layer fix.
//
// Both gaps are tracked in the api-06 audit row description and
// must close before incentivised-testnet exposure (mining-05).
//
// Endpoint is intentionally listed in publicPaths: the
// cryptographic identity is IN the envelope, so demanding a JWT
// adds nothing. The per-IP rate-limit (security.go) bucketizes
// abuse, and `result=signature_invalid` / `result=sender_mismatch`
// counters surface a misbehaving caller.
func (h *Handlers) SubmitSignedTransaction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if h.walletService == nil {
		monitoring.RecordWalletSend(monitoring.WalletSendResultNoWalletService)
		writeErrorResponse(w, http.StatusServiceUnavailable, msgWalletServiceUnavailable)
		return
	}

	// Decode the envelope. We accept wallet.TransactionData
	// verbatim so the wire format is the exact byte shape produced
	// by pkg/wallet.WalletService.CreateTransaction (which is what
	// qsdmcli, the WASM signer, and any third-party SDK will
	// produce). Field-order is fixed by the struct definition.
	var env wallet.TransactionData
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&env); err != nil {
		monitoring.RecordWalletSend(monitoring.WalletSendResultInvalidRequest)
		writeErrorResponse(w, http.StatusBadRequest, "invalid envelope: "+err.Error())
		return
	}

	// Shape validation (reuse existing validators from the
	// /wallet/send path so a misbehaving caller can't escape one
	// gate by switching endpoints).
	if err := ValidateAddress(env.Sender); err != nil {
		monitoring.RecordWalletSend(monitoring.WalletSendResultInvalidRequest)
		writeErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid sender address: %v", err))
		return
	}
	if err := ValidateAddress(env.Recipient); err != nil {
		monitoring.RecordWalletSend(monitoring.WalletSendResultInvalidRequest)
		writeErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid recipient address: %v", err))
		return
	}
	if err := ValidateAmount(env.Amount); err != nil {
		monitoring.RecordWalletSend(monitoring.WalletSendResultInvalidRequest)
		writeErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid amount: %v", err))
		return
	}
	if env.Fee < 0 {
		monitoring.RecordWalletSend(monitoring.WalletSendResultInvalidRequest)
		writeErrorResponse(w, http.StatusBadRequest, "fee cannot be negative")
		return
	}
	if env.Fee > 0 {
		if err := ValidateAmount(env.Fee); err != nil {
			monitoring.RecordWalletSend(monitoring.WalletSendResultInvalidRequest)
			writeErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid fee: %v", err))
			return
		}
	}
	if err := ValidateGeoTag(env.GeoTag); err != nil {
		monitoring.RecordWalletSend(monitoring.WalletSendResultInvalidRequest)
		writeErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid geotag: %v", err))
		return
	}
	if err := ValidateParentCells(env.ParentCells); err != nil {
		monitoring.RecordWalletSend(monitoring.WalletSendResultInvalidRequest)
		writeErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid parent cells: %v", err))
		return
	}
	if env.ID == "" {
		monitoring.RecordWalletSend(monitoring.WalletSendResultInvalidRequest)
		writeErrorResponse(w, http.StatusBadRequest, "envelope.id is required")
		return
	}
	if env.PublicKey == "" {
		monitoring.RecordWalletSend(monitoring.WalletSendResultInvalidRequest)
		writeErrorResponse(w, http.StatusBadRequest, "envelope.public_key is required")
		return
	}
	if env.Signature == "" {
		monitoring.RecordWalletSend(monitoring.WalletSendResultInvalidRequest)
		writeErrorResponse(w, http.StatusBadRequest, "envelope.signature is required")
		return
	}

	// Cryptographic identity bind: sender MUST equal
	// hex(sha256(public_key)). Without this, a caller could put
	// any victim address in `sender`, attach their own pubkey, and
	// sign correctly with their own key — the signature would
	// verify and the storage layer would happily debit the
	// victim's balance.
	pubBytes, err := hex.DecodeString(env.PublicKey)
	if err != nil {
		monitoring.RecordWalletSend(monitoring.WalletSendResultInvalidRequest)
		writeErrorResponse(w, http.StatusBadRequest, "envelope.public_key is not valid hex")
		return
	}
	derivedAddr := hex.EncodeToString(sha256Sum(pubBytes))
	if derivedAddr != env.Sender {
		monitoring.RecordWalletSend(monitoring.WalletSendResultSenderMismatch)
		writeErrorResponse(w, http.StatusBadRequest, "envelope.sender does not match hex(sha256(public_key))")
		return
	}

	sigBytes, err := hex.DecodeString(env.Signature)
	if err != nil {
		monitoring.RecordWalletSend(monitoring.WalletSendResultInvalidRequest)
		writeErrorResponse(w, http.StatusBadRequest, "envelope.signature is not valid hex")
		return
	}

	// Build the canonical signing payload: the same envelope with
	// `signature` and `public_key` cleared, then json.Marshal-ed
	// with Go's default field ordering. This must match what the
	// signer (qsdmcli, WASM, or a third-party SDK) produced.
	unsigned := env
	unsigned.Signature = ""
	unsigned.PublicKey = ""
	canonical, err := json.Marshal(unsigned)
	if err != nil {
		monitoring.RecordWalletSend(monitoring.WalletSendResultInvalidRequest)
		writeErrorResponse(w, http.StatusBadRequest, "failed to canonicalise envelope")
		return
	}
	ok, verr := h.walletService.VerifySignature(canonical, sigBytes, pubBytes)
	if verr != nil || !ok {
		monitoring.RecordWalletSend(monitoring.WalletSendResultSignatureInvalid)
		writeErrorResponse(w, http.StatusUnprocessableEntity, "signature does not verify under envelope.public_key")
		return
	}

	rawEnvelope, err := json.Marshal(env)
	if err != nil {
		monitoring.RecordWalletSend(monitoring.WalletSendResultInvalidRequest)
		writeErrorResponse(w, http.StatusBadRequest, "failed to remarshal envelope")
		return
	}

	// v0.4.1 (Session 100): nonce-replay gate, fires only for
	// envelopes that opt in to the new wire format
	// (envelope.nonce >= 1). v0.4.0 envelopes (nonce omitted →
	// Go zero-value 0) fall through unchanged for the
	// backward-compat window documented in
	// V041_REPLAY_PROTECTION_DESIGN.md §2.3.
	//
	// We intentionally run this BEFORE the submesh-policy gate
	// so that nonce-replay rejections don't bump the submesh
	// metric counters. ErrTxAlreadyExists from
	// ApplyTransferAtomic below covers the v0.4.0 cross-tx_id
	// case; this branch covers the new cross-nonce case which
	// no v0.4.0 envelope can hit.
	if env.Nonce >= 1 {
		last, nerr := h.storage.GetNonce(env.Sender)
		if nerr != nil {
			monitoring.RecordWalletSend(monitoring.WalletSendResultNonceLookupFailed)
			h.logger.Error("Nonce lookup failed", "error", nerr, "sender", env.Sender)
			writeErrorResponse(w, http.StatusInternalServerError, "nonce lookup failed")
			return
		}
		if env.Nonce <= last {
			monitoring.RecordWalletSend(monitoring.WalletSendResultNonceReplay)
			writeErrorResponse(w, http.StatusConflict,
				fmt.Sprintf("nonce replay: envelope nonce %d <= last-seen %d", env.Nonce, last))
			return
		}
	}

	// Submesh policy gate (matches /wallet/send posture; counts
	// its own rejects under qsdm_submesh_api_wallet_reject_*).
	// Runs before ApplyTransferAtomic so policy-rejected
	// envelopes don't touch storage at all.
	if !h.enforceSubmeshWalletSend(w, env.Fee, env.GeoTag, rawEnvelope) {
		return
	}

	// v0.4.1: single ACID step replaces the v0.4.0 sequence of
	//   storageHasTransaction(env.ID) → 409 duplicate
	//   storage.GetBalance(sender)    → 402 insufficient
	//   storage.StoreTransaction(raw) → INSERT + UpdateBalance×2
	// The new primitive enforces all four invariants
	// (tx_id uniqueness, nonce CAS, balance >= amount+fee,
	// debit+credit atomicity) inside a single BEGIN; COMMIT;
	// transaction. See V041_REPLAY_PROTECTION_DESIGN.md §4.2.
	if err := h.storage.ApplyTransferAtomic(
		r.Context(),
		env.Sender, env.Recipient,
		env.Amount, env.Fee,
		env.Nonce, env.ID,
		rawEnvelope,
	); err != nil {
		switch {
		case errors.Is(err, storage.ErrTxAlreadyExists):
			monitoring.RecordWalletSend(monitoring.WalletSendResultDuplicate)
			writeJSONResponse(w, http.StatusConflict, SubmitSignedTransactionResponse{
				TransactionID: env.ID,
				Status:        "duplicate",
			})
			return
		case errors.Is(err, storage.ErrInsufficientBalance):
			monitoring.RecordWalletSend(monitoring.WalletSendResultInsufficientBalance)
			writeErrorResponse(w, http.StatusPaymentRequired,
				"insufficient balance for amount + fee")
			return
		case errors.Is(err, storage.ErrNonceConflict):
			// ErrNonceConflict from ApplyTransferAtomic means
			// the sender's stored nonce moved between our
			// GetNonce probe above and the atomic UPDATE — a
			// concurrent submit-signed raced us. The caller
			// can safely retry after re-reading the nonce.
			monitoring.RecordWalletSend(monitoring.WalletSendResultNonceConflict)
			writeErrorResponse(w, http.StatusConflict,
				"nonce conflict: concurrent submit raced; retry after re-reading nonce")
			return
		default:
			monitoring.RecordWalletSend(monitoring.WalletSendResultStoreFailed)
			h.logger.Error("ApplyTransferAtomic failed", "error", err, "tx_id", env.ID, "sender", env.Sender)
			writeErrorResponse(w, http.StatusInternalServerError, "failed to apply transfer")
			return
		}
	}

	monitoring.RecordWalletSend(monitoring.WalletSendResultSuccess)

	broadcast := "local-only"
	if h.p2pTxBroadcast != nil {
		if err := h.p2pTxBroadcast(rawEnvelope); err != nil {
			h.logger.Warn("P2P broadcast after submit-signed failed (tx still stored locally)", "error", err, "tx_id", env.ID)
		} else {
			broadcast = "p2p"
		}
	}

	writeJSONResponse(w, http.StatusOK, SubmitSignedTransactionResponse{
		TransactionID: env.ID,
		Status:        "accepted",
		Broadcast:     broadcast,
	})
}

// sha256Sum is a thin helper that returns the sha256 digest as a
// fresh []byte (vs sha256.Sum256 which returns [32]byte). Used by
// the submit-signed handler's address-derivation check.
func sha256Sum(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}

// storageHasTransaction returns true iff the storage layer already
// has a transaction with the given tx_id. Uses the indexed
// GetTransaction primitive on StorageInterface (O(1) on the sqlite
// backend — `WHERE tx_id = ? LIMIT 1`). On a backend miss the
// underlying storage returns a wrapped error whose string contains
// "transaction not found" — we treat that as (false, nil) rather
// than propagating it, so the caller doesn't get a 500 on the
// happy-path "not a duplicate" case.
func (h *Handlers) storageHasTransaction(txID string) (bool, error) {
	if h.storage == nil || txID == "" {
		return false, nil
	}
	tx, err := h.storage.GetTransaction(txID)
	if err != nil {
		if strings.Contains(err.Error(), "transaction not found") ||
			strings.Contains(err.Error(), "not found") {
			return false, nil
		}
		return false, err
	}
	return tx != nil, nil
}

// GetAddress returns the wallet address
func (h *Handlers) GetAddress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if h.walletService == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, msgWalletServiceUnavailable)
		return
	}

	address := h.walletService.GetAddress()
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"address": address,
	})
}

// GetTransactions returns recent transactions
func (h *Handlers) GetTransactions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Get limit from query (default 50, max 1000)
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 1000 {
			limit = l
		}
	}

	claims, ok := ClaimsFromContext(r.Context())
	if !ok {
		writeErrorResponse(w, http.StatusUnauthorized, "missing authentication")
		return
	}

	// Get address from query or use authenticated user's address
	address := r.URL.Query().Get("address")
	if address == "" {
		address = claims.Address
	}

	// Check if storage supports GetRecentTransactions
	// Convert to interface{} first, then type assert
	type GetRecentTransactionsStorage interface {
		GetRecentTransactions(address string, limit int) ([]map[string]interface{}, error)
	}

	var transactions []map[string]interface{}
	var err error

	if txStorage, ok := interface{}(h.storage).(GetRecentTransactionsStorage); ok {
		transactions, err = txStorage.GetRecentTransactions(address, limit)
		if err != nil {
			h.logger.Error("Failed to get transactions", "error", err, "address", address)
			writeErrorResponse(w, http.StatusInternalServerError, "failed to get transactions")
			return
		}
	} else {
		writeErrorResponse(w, http.StatusNotImplemented, "transaction history not available with current storage backend")
		return
	}
	if err != nil {
		h.logger.Error("Failed to get transactions", "error", err, "address", address)
		writeErrorResponse(w, http.StatusInternalServerError, "failed to get transactions")
		return
	}

	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"transactions": transactions,
		"limit":        limit,
		"count":        len(transactions),
	})
}

// GetTransactionByID returns a specific transaction
func (h *Handlers) GetTransactionByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Extract transaction ID from path
	txID := r.URL.Path[len("/api/v1/transactions/"):]
	if txID == "" {
		writeErrorResponse(w, http.StatusBadRequest, "transaction ID required")
		return
	}

	// Check if storage supports GetTransaction
	// Define interface for type assertion
	type GetTransactionStorage interface {
		GetTransaction(txID string) (map[string]interface{}, error)
	}

	var transaction map[string]interface{}
	var err error

	// Type assertion - storage.Storage is a concrete type, but we can check if it implements the interface
	if txStorage, ok := interface{}(h.storage).(GetTransactionStorage); ok {
		transaction, err = txStorage.GetTransaction(txID)
		if err != nil {
			if err.Error() == "transaction not found" {
				writeErrorResponse(w, http.StatusNotFound, "transaction not found")
				return
			}
			h.logger.Error("Failed to get transaction", "error", err, "tx_id", txID)
			writeErrorResponse(w, http.StatusInternalServerError, "failed to get transaction")
			return
		}
	} else {
		writeErrorResponse(w, http.StatusNotImplemented, "transaction lookup not available with current storage backend")
		return
	}

	writeJSONResponse(w, http.StatusOK, transaction)
}

// ValidateTransactionRequest represents a validation request
type ValidateTransactionRequest struct {
	TransactionID string              `json:"transaction_id"`
	ParentCells   []mesh3d.ParentCell `json:"parent_cells"`
	Data          []byte              `json:"data"`
}

// ValidateTransactionResponse represents a validation response
type ValidateTransactionResponse struct {
	Valid   bool   `json:"valid"`
	Message string `json:"message,omitempty"`
}

// ValidateTransaction validates a transaction
func (h *Handlers) ValidateTransaction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req ValidateTransactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Create transaction for validation
	tx := &mesh3d.Transaction{
		ID:          req.TransactionID,
		ParentCells: req.ParentCells,
		Data:        req.Data,
	}

	// Validate
	valid, err := h.mesh3dValidator.ValidateTransaction(tx)
	if err != nil {
		writeJSONResponse(w, http.StatusOK, ValidateTransactionResponse{
			Valid:   false,
			Message: err.Error(),
		})
		return
	}

	writeJSONResponse(w, http.StatusOK, ValidateTransactionResponse{
		Valid: valid,
	})
}

// MintTokenRequest represents a token minting request
type MintTokenRequest struct {
	TokenSymbol string  `json:"token_symbol"` // e.g., "JOLLY"
	Recipient   string  `json:"recipient"`
	Amount      float64 `json:"amount"`
}

// MintTokenResponse represents a token minting response
type MintTokenResponse struct {
	TransactionID string  `json:"transaction_id"`
	TokenSymbol   string  `json:"token_symbol"`
	Amount        float64 `json:"amount"`
	Recipient     string  `json:"recipient"`
	Status        string  `json:"status"`
}

// MintToken mints tokens (like $JOLLY) to a recipient address
func (h *Handlers) MintToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req MintTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate request
	if req.TokenSymbol == "" {
		writeErrorResponse(w, http.StatusBadRequest, "token_symbol is required")
		return
	}
	if req.Recipient == "" {
		writeErrorResponse(w, http.StatusBadRequest, "recipient address is required")
		return
	}
	if req.Amount <= 0 {
		writeErrorResponse(w, http.StatusBadRequest, "amount must be positive")
		return
	}

	if !h.enforceNvidiaLock(w) {
		return
	}

	// Log the mint operation with $JOLLY token name
	h.logger.Info("Token minted",
		"token_symbol", fmt.Sprintf("$%s", req.TokenSymbol),
		"amount", req.Amount,
		"recipient", req.Recipient,
	)

	// Generate transaction ID
	txID := fmt.Sprintf("mint_%s_%d", req.TokenSymbol, time.Now().UnixNano())

	mintPayload := []byte(fmt.Sprintf(`{"type":"mint","token":"%s","amount":%f,"recipient":"%s","tx_id":"%s"}`, req.TokenSymbol, req.Amount, req.Recipient, txID))
	if !h.enforceSubmeshPrivilegedPayload(w, mintPayload) {
		return
	}

	// Store the mint transaction in storage
	if err := h.storage.StoreTransaction(mintPayload); err != nil {
		h.logger.Error("Failed to store mint transaction", "error", err)
		writeErrorResponse(w, http.StatusInternalServerError, "failed to store transaction")
		return
	}

	writeJSONResponse(w, http.StatusOK, MintTokenResponse{
		TransactionID: txID,
		TokenSymbol:   req.TokenSymbol,
		Amount:        req.Amount,
		Recipient:     req.Recipient,
		Status:        "minted",
	})
}

// MintMainCoinRequest is the historical request shape for POST
// /api/v1/wallet/mint. RETAINED on the symbol surface for SDK /
// generator compatibility; the endpoint now returns 410 Gone and
// the field values are ignored. See MintMainCoin for the migration
// path.
type MintMainCoinRequest struct {
	Recipient string  `json:"recipient"`
	Amount    float64 `json:"amount"`
}

// MintMainCoinResponse represents a main coin minting response
type MintMainCoinResponse struct {
	TransactionID string  `json:"transaction_id"`
	Amount        float64 `json:"amount"`
	Recipient     string  `json:"recipient"`
	Status        string  `json:"status"`
}

// MintMainCoin mints the main coin ($CELL) to a recipient address
// MintMainCoin is the handler for POST /api/v1/wallet/mint.
//
// HISTORY. This endpoint was wired in the early-product window as a
// stub for a hypothetical game-server integration. It accepted
// `{recipient, amount}`, ran NVIDIA-lock + submesh policy checks,
// stored a `{"type":"mint","coin":"CELL",...}` envelope into the
// transaction log, and returned HTTP 200 with `status:"minted"`.
//
// It **never actually credited the recipient's balance** — there is
// no code path from POST /api/v1/wallet/mint to the wallet-service
// `AddBalance(addr, amount)` operation that `GET /wallet/balance`
// reads from. A balance lookup after a "successful" mint always
// returned 0. Confirmed by audit (Session 85) and by direct
// reproduction on the BLR1 testnet.
//
// REMOVED IN v0.3.3 (Session 91). The handler now returns
// HTTP 410 Gone with a JSON body that explains the migration:
//
//   * To acquire CELL as a new operator, follow the peer-transfer
//     workflow in MINER_QUICKSTART.md Appendix B (an existing
//     operator with funds POSTs `/api/v1/wallet/send` to your
//     address), or wait for the public incentivised testnet
//     (`mining-05` blocker in NEXT_STEPS.md / RELEASE_NOTES_v0.3.0.md).
//   * To mint named secondary tokens (e.g. game tokens), use
//     POST /api/v1/tokens/mint, which IS wired through the
//     wallet-service and DOES update balances.
//
// The function symbol, MintMainCoinRequest, and MintMainCoinResponse
// types are RETAINED so generated SDK code still compiles. The 410
// is intentionally surfaced via the regular `monitoring.WalletMint*`
// counters with a new `gone` result tag so dashboards / alerts that
// were watching for inflation see the now-impossible mint surface
// transition cleanly from `success` to `gone`.
//
// Reversal: if a real game-server integration ever lands, the
// handler can be re-implemented to actually call
// `h.walletService.AddBalance(req.Recipient, req.Amount)` (note:
// h.walletService is currently nil on most node profiles, hence
// the original stub posture). Any reversal MUST land WITH an
// admin-credential check + rate-limit tightening; this endpoint
// is in publicPaths and a working mint without an admin gate is
// an open supply-inflation surface.
func (h *Handlers) MintMainCoin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	monitoring.RecordWalletMint(monitoring.WalletMintResultGone)
	writeJSONResponse(w, http.StatusGone, map[string]interface{}{
		"status":        "gone",
		"reason":        "POST /api/v1/wallet/mint was a non-functional stub (returned 200 but never credited balance). Removed in v0.3.3.",
		"migration": map[string]string{
			"new_operator_funding": "See MINER_QUICKSTART.md Appendix B (peer transfer via POST /api/v1/wallet/send from an existing-balance operator).",
			"named_token_minting":  "Use POST /api/v1/tokens/mint (this IS wired through the wallet service and DOES update balances).",
			"public_testnet":       "Tracked as `mining-05` blocker. See RELEASE_NOTES_v0.3.0.md \"Remaining external blockers\".",
		},
	})
}

// CreateTokenRequest represents a token creation request
type CreateTokenRequest struct {
	Name        string  `json:"name"`         // e.g., "Jolly Token"
	Symbol      string  `json:"symbol"`       // e.g., "JOLLY"
	Decimals    int     `json:"decimals"`     // e.g., 18
	TotalSupply float64 `json:"total_supply"` // Initial supply
	Description string  `json:"description,omitempty"`
}

// CreateTokenResponse represents a token creation response
type CreateTokenResponse struct {
	TokenID     string  `json:"token_id"`
	Name        string  `json:"name"`
	Symbol      string  `json:"symbol"`
	Decimals    int     `json:"decimals"`
	TotalSupply float64 `json:"total_supply"`
	Status      string  `json:"status"`
}

// CreateToken creates a new token on the QSDM ledger
func (h *Handlers) CreateToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req CreateTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate request
	if req.Name == "" {
		writeErrorResponse(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Symbol == "" {
		writeErrorResponse(w, http.StatusBadRequest, "symbol is required")
		return
	}
	if req.Decimals < 0 || req.Decimals > 18 {
		writeErrorResponse(w, http.StatusBadRequest, "decimals must be between 0 and 18")
		return
	}
	if req.TotalSupply < 0 {
		writeErrorResponse(w, http.StatusBadRequest, "total_supply cannot be negative")
		return
	}

	if !h.enforceNvidiaLock(w) {
		return
	}

	// Generate token ID
	tokenID := fmt.Sprintf("token_%s_%d", req.Symbol, time.Now().UnixNano())

	// Log token creation
	h.logger.Info("Token created",
		"token_id", tokenID,
		"name", req.Name,
		"symbol", req.Symbol,
		"total_supply", req.TotalSupply,
	)

	// Store token metadata
	tokenData := fmt.Sprintf(`{"type":"token_creation","token_id":"%s","name":"%s","symbol":"%s","decimals":%d,"total_supply":%f,"description":"%s"}`, tokenID, req.Name, req.Symbol, req.Decimals, req.TotalSupply, req.Description)
	tokenPayload := []byte(tokenData)
	if !h.enforceSubmeshPrivilegedPayload(w, tokenPayload) {
		return
	}
	if err := h.storage.StoreTransaction(tokenPayload); err != nil {
		h.logger.Error("Failed to store token creation", "error", err)
		writeErrorResponse(w, http.StatusInternalServerError, "failed to store token")
		return
	}

	h.tokenRegistryMu.Lock()
	h.tokenRegistry = append(h.tokenRegistry, TokenInfo{
		TokenID:     tokenID,
		Name:        req.Name,
		Symbol:      req.Symbol,
		Decimals:    req.Decimals,
		TotalSupply: req.TotalSupply,
	})
	h.tokenRegistryMu.Unlock()

	if h.tokenRegistryPath != "" {
		if err := h.SaveTokenRegistry(h.tokenRegistryPath); err != nil {
			h.logger.Warn("Failed to persist token registry", "error", err)
		}
	}

	writeJSONResponse(w, http.StatusCreated, CreateTokenResponse{
		TokenID:     tokenID,
		Name:        req.Name,
		Symbol:      req.Symbol,
		Decimals:    req.Decimals,
		TotalSupply: req.TotalSupply,
		Status:      "created",
	})
}

// ListTokensResponse represents a list of tokens
type ListTokensResponse struct {
	Tokens []TokenInfo `json:"tokens"`
	Count  int         `json:"count"`
}

// TokenInfo represents token information
type TokenInfo struct {
	TokenID     string  `json:"token_id"`
	Name        string  `json:"name"`
	Symbol      string  `json:"symbol"`
	Decimals    int     `json:"decimals"`
	TotalSupply float64 `json:"total_supply"`
}

// ListTokens lists all tokens on the QSDM ledger (built-in + user-created).
// The canonical native coin is Cell (CELL); see QSDM/docs/docs/CELL_TOKENOMICS.md.
// The legacy "main_coin" token ID remains as a deprecated alias for the same
// Cell coin so external integrations written against the pre-rebrand API
// keep working through the Major Update deprecation window.
func (h *Handlers) ListTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	cell := TokenInfo{
		TokenID:     "main_cell",
		Name:        branding.CoinName,
		Symbol:      branding.CoinSymbol,
		Decimals:    branding.CoinDecimals,
		TotalSupply: 0,
	}
	cellLegacyAlias := cell
	cellLegacyAlias.TokenID = "main_coin"

	tokens := []TokenInfo{cell, cellLegacyAlias}

	h.tokenRegistryMu.RLock()
	tokens = append(tokens, h.tokenRegistry...)
	h.tokenRegistryMu.RUnlock()

	writeJSONResponse(w, http.StatusOK, ListTokensResponse{
		Tokens: tokens,
		Count:  len(tokens),
	})
}

const ngcProofMaxBody = 512 * 1024

type ngcIngestAuthOutcome int

const (
	ngcIngestAuthOK ngcIngestAuthOutcome = iota
	ngcIngestAuthDisabled
	ngcIngestAuthBadSecret
)

func (h *Handlers) ngcIngestAuth(r *http.Request) ngcIngestAuthOutcome {
	if strings.TrimSpace(h.ngcIngestSecret) == "" {
		return ngcIngestAuthDisabled
	}
	got := strings.TrimSpace(r.Header.Get(branding.NGCSecretHeaderPreferred))
	if got == "" {
		got = strings.TrimSpace(r.Header.Get(branding.NGCSecretHeaderLegacy))
	}
	if !SecureCompare(got, h.ngcIngestSecret) {
		return ngcIngestAuthBadSecret
	}
	return ngcIngestAuthOK
}

func (h *Handlers) ngcIngestAuthFailureResponse(w http.ResponseWriter, o ngcIngestAuthOutcome) {
	switch o {
	case ngcIngestAuthDisabled:
		writeErrorResponse(w, http.StatusNotFound, "not found")
	case ngcIngestAuthBadSecret:
		writeErrorResponse(w, http.StatusUnauthorized, "unauthorized")
	default:
		writeErrorResponse(w, http.StatusUnauthorized, "unauthorized")
	}
}

// NGCProofIngest accepts JSON proof bundles from the apps/qsdm-nvidia-ngc validator (nvidia_locked_qsdm_blockchain_architecture.md).
func (h *Handlers) NGCProofIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	auth := h.ngcIngestAuth(r)
	if auth != ngcIngestAuthOK {
		switch auth {
		case ngcIngestAuthDisabled:
			monitoring.RecordNGCProofIngestRejected("ingest_disabled")
		case ngcIngestAuthBadSecret:
			monitoring.RecordNGCProofIngestRejected("unauthorized")
		}
		h.ngcIngestAuthFailureResponse(w, auth)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, ngcProofMaxBody+1))
	if err != nil {
		monitoring.RecordNGCProofIngestRejected("body_read")
		writeErrorResponse(w, http.StatusBadRequest, "invalid body")
		return
	}
	if len(body) > ngcProofMaxBody {
		monitoring.RecordNGCProofIngestRejected("body_too_large")
		writeErrorResponse(w, http.StatusRequestEntityTooLarge, "body too large")
		return
	}
	if err := monitoring.RecordNGCProofBundleForIngest(body, h.nvidiaLockRequireIngestNonce, h.nvidiaLockProofHMACSecret); err != nil {
		monitoring.RecordNGCProofIngestRejected(monitoring.NGCProofIngestRejectReason(err))
		h.logger.Warn("NGC proof rejected", "error", err.Error())
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	monitoring.RecordNGCProofIngestAccepted()
	h.logger.Info("NGC proof bundle ingested", "bytes", len(body))
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// NGCIngestChallenge issues a single-use nonce for proof ingest (requires same secret headers as ingest).
func (h *Handlers) NGCIngestChallenge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	auth := h.ngcIngestAuth(r)
	if auth != ngcIngestAuthOK {
		h.ngcIngestAuthFailureResponse(w, auth)
		return
	}
	if !h.nvidiaLockRequireIngestNonce {
		writeErrorResponse(w, http.StatusNotFound, "ingest nonce challenge disabled")
		return
	}
	ttl := h.nvidiaLockIngestNonceTTL
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	nonce, exp, err := monitoring.IssueNGCIngestNonce(ttl)
	if err != nil {
		h.logger.Error("NGC challenge issue failed", "error", err)
		writeErrorResponse(w, http.StatusServiceUnavailable, "failed to issue nonce")
		return
	}
	monitoring.RecordNGCChallengeIssued()
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"qsdm_ingest_nonce": nonce,
		"expires_at_unix":       exp,
		"ttl_seconds":           int(ttl.Seconds()),
	})
}

// NGCProofList returns summarized ingested proofs for operators (same secret as ingest).
func (h *Handlers) NGCProofList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	auth := h.ngcIngestAuth(r)
	if auth != ngcIngestAuthOK {
		h.ngcIngestAuthFailureResponse(w, auth)
		return
	}
	summaries := monitoring.NGCProofSummaries()
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"proofs": summaries,
		"count":  len(summaries),
	})
}
