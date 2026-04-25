package api

import (
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
}

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
	s.handlers = handlers

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

	// Wallet endpoints (authenticated)
	mux.HandleFunc("/api/v1/wallet/create", handlers.CreateWallet)
	mux.HandleFunc("/api/v1/wallet/balance", handlers.GetBalance)
	mux.HandleFunc("/api/v1/wallet/send", handlers.SendTransaction)
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

	// NGC GPU proof sidecar (shared secret; prefer QSDM_NGC_INGEST_SECRET, legacy QSDMPLUS_NGC_INGEST_SECRET still accepted)
	mux.HandleFunc("/api/v1/monitoring/ngc-proof", handlers.NGCProofIngest)
	mux.HandleFunc("/api/v1/monitoring/ngc-challenge", handlers.NGCIngestChallenge)
	mux.HandleFunc("/api/v1/monitoring/ngc-proofs", handlers.NGCProofList)

	// Mining endpoints (Major Update Phase 4.3). Return 503 until a
	// MiningService is installed via api.SetMiningService(...).
	mux.HandleFunc("/api/v1/mining/work", handlers.MiningWorkHandler)
	mux.HandleFunc("/api/v1/mining/submit", handlers.MiningSubmitHandler)
	// Mining challenge endpoint (Phase 2c-iii,
	// MINING_PROTOCOL_V2_NVIDIA_LOCKED.md §6.2). Returns 503 until
	// a ChallengeIssuer is installed via api.SetChallengeIssuer(...).
	// Registered unconditionally so miners can probe readiness.
	mux.HandleFunc("/api/v1/mining/challenge", handlers.MiningChallengeHandler)

	// Mining enrollment endpoints (Phase 2c-x,
	// MINING_PROTOCOL_V2_NVIDIA_LOCKED.md §7). Two symmetric
	// POSTs accept signed mempool.Tx envelopes carrying enrollment
	// payloads (qsdm/enroll/v1). Return 503 until a MempoolSubmitter
	// is installed via api.SetEnrollmentMempool(...). Stateless
	// payload validation runs in the mempool admission gate
	// (enrollment.AdmissionChecker); stateful checks (balance,
	// node_id uniqueness) happen at block-apply time.
	mux.HandleFunc("/api/v1/mining/enroll", handlers.EnrollmentSubmitHandler)
	mux.HandleFunc("/api/v1/mining/unenroll", handlers.UnenrollmentSubmitHandler)

	// Trust / attestation transparency endpoints (Major Update Phase 5.1).
	// Registered unconditionally. If no aggregator is installed via
	// api.SetTrustAggregator, the handlers return 503 warming-up; if the
	// operator opted out, they return 404. Both endpoints are public
	// (see middleware.isPublicEndpoint) and deliberately anti-claim:
	// the widget must always render "X of Y", never "X".
	mux.HandleFunc("/api/v1/trust/attestations/summary", handlers.TrustSummaryHandler)
	mux.HandleFunc("/api/v1/trust/attestations/recent", handlers.TrustRecentHandler)

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
		h.logger.Warn("Login attempt on locked account", "address", req.Address, "error", lockErr)
		writeErrorResponse(w, http.StatusTooManyRequests, lockErr.Error())
		return
	}

	// Authenticate user
	user, err := h.userStore.AuthenticateUser(req.Address, req.Password)
	if err != nil {
		// Record failed attempt
		h.authManager.RecordFailedAttempt(req.Address)

		// Get remaining attempts
		remaining := h.authManager.GetRemainingAttempts(req.Address)
		h.logger.Warn("Authentication failed", "address", req.Address, "error", err, "remaining_attempts", remaining)

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
		h.logger.Error("Failed to create access token", "error", err)
		writeErrorResponse(w, http.StatusInternalServerError, "failed to create token")
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
		h.logger.Error("Failed to create refresh token", "error", err)
		writeErrorResponse(w, http.StatusInternalServerError, "failed to create token")
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
		h.logger.Error("Failed to register user", "error", err)
		writeErrorResponse(w, http.StatusInternalServerError, "failed to register user")
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
		h.logger.Error("Failed to create new wallet", "error", err)
		writeErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("failed to create wallet: %v", err))
		return
	}

	address := newWallet.GetAddress()
	balance := float64(newWallet.GetBalance())

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
		if claims, ok := r.Context().Value("claims").(*Claims); ok {
			address = claims.Address
		} else {
			writeErrorResponse(w, http.StatusBadRequest, "address parameter is required")
			return
		}
	}

	balance, err := h.storage.GetBalance(address)
	if err != nil {
		h.logger.Error("Failed to get balance", "error", err, "address", address)
		writeErrorResponse(w, http.StatusInternalServerError, "failed to get balance")
		return
	}

	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"address": address,
		"balance": balance,
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
		writeErrorResponse(w, http.StatusServiceUnavailable, msgWalletServiceUnavailable)
		return
	}

	claims, ok := r.Context().Value("claims").(*Claims)
	if !ok {
		writeErrorResponse(w, http.StatusUnauthorized, "missing authentication")
		return
	}
	_ = claims // Use claims for future enhancements

	if !h.enforceNvidiaLock(w) {
		return
	}

	var req SendTransactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate request with comprehensive validation
	if err := ValidateAddress(req.Recipient); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid recipient address: %v", err))
		return
	}
	if err := ValidateAmount(req.Amount); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid amount: %v", err))
		return
	}
	if req.Fee < 0 {
		writeErrorResponse(w, http.StatusBadRequest, "fee cannot be negative")
		return
	}
	if err := ValidateAmount(req.Fee); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid fee: %v", err))
		return
	}
	if err := ValidateGeoTag(req.GeoTag); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid geotag: %v", err))
		return
	}
	if err := ValidateParentCells(req.ParentCells); err != nil {
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
		h.logger.Error("Failed to create transaction", "error", err)
		writeErrorResponse(w, http.StatusInternalServerError, "failed to create transaction")
		return
	}

	if !h.enforceSubmeshWalletSend(w, req.Fee, req.GeoTag, txBytes) {
		return
	}

	// Store transaction
	if err := h.storage.StoreTransaction(txBytes); err != nil {
		h.logger.Error("Failed to store transaction", "error", err)
		writeErrorResponse(w, http.StatusInternalServerError, "failed to store transaction")
		return
	}

	if h.p2pTxBroadcast != nil {
		if err := h.p2pTxBroadcast(txBytes); err != nil {
			h.logger.Warn("P2P tx broadcast after send failed", "error", err)
		}
	}

	if envcompat.Truthy("QSDM_PUBLISH_MESH_COMPANION", "QSDMPLUS_PUBLISH_MESH_COMPANION") && h.p2pTxBroadcast != nil && len(req.ParentCells) >= 2 {
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

	claims, ok := r.Context().Value("claims").(*Claims)
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

// MintMainCoinRequest represents a main coin minting request
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
func (h *Handlers) MintMainCoin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req MintMainCoinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate request
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

	// Log the mint operation with $CELL coin name
	h.logger.Info("Main coin minted",
		"coin", "$CELL",
		"amount", req.Amount,
		"recipient", req.Recipient,
	)

	// Generate transaction ID
	txID := fmt.Sprintf("mint_cell_%d", time.Now().UnixNano())

	cellMintPayload := []byte(fmt.Sprintf(`{"type":"mint","coin":"CELL","amount":%f,"recipient":"%s","tx_id":"%s"}`, req.Amount, req.Recipient, txID))
	if !h.enforceSubmeshPrivilegedPayload(w, cellMintPayload) {
		return
	}

	// Store the mint transaction in storage
	if err := h.storage.StoreTransaction(cellMintPayload); err != nil {
		h.logger.Error("Failed to store mint transaction", "error", err)
		writeErrorResponse(w, http.StatusInternalServerError, "failed to store transaction")
		return
	}

	writeJSONResponse(w, http.StatusOK, MintMainCoinResponse{
		TransactionID: txID,
		Amount:        req.Amount,
		Recipient:     req.Recipient,
		Status:        "minted",
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

// NGCProofIngest accepts JSON proof bundles from the apps/qsdmplus-nvidia-ngc validator (nvidia_locked_qsdmplus_blockchain_architecture.md).
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
		"qsdmplus_ingest_nonce": nonce,
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
