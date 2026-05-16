package api

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/bridge"
	"github.com/blackbeardONE/QSDM/pkg/config"
	"github.com/blackbeardONE/QSDM/pkg/contracts"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
	"github.com/blackbeardONE/QSDM/pkg/submesh"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

// getEnvForCORS reads an env var. Wrapped to keep server.go's CORS
// wiring testable — replaced via osGetenv in setupMiddleware.
func getEnvForCORS(name string) string { return os.Getenv(name) }

// Server represents the HTTP API server
type Server struct {
	config        *config.Config
	logger        *logging.Logger
	authManager   *AuthManager
	userStore     *UserStore
	rateLimiter   *RateLimiter
	requestSigner *RequestSigner
	csrfManager   *CSRFManager
	revocations      *TokenRevocationStore
	walletService    *wallet.WalletService
	storage          StorageInterface
	submeshManager   *submesh.DynamicSubmeshManager
	contractEngine   *contracts.ContractEngine
	bridgeProtocol   *bridge.BridgeProtocol
	atomicSwap       *bridge.AtomicSwapProtocol
	bridgeRelay        *bridge.P2PRelay
	nodeID             string
	handlers           *Handlers
	tokenRegistryPath  string
	httpServer         *http.Server
	adminAPI           *AdminAPI
	txGossipBroadcast  func([]byte) error
	// Pending source callbacks captured before Start —
	// registerRoutes is the late-binding point at which the
	// concrete *Handlers exists, so we stash them here and
	// apply at registerRoutes time. Without this stash, every
	// SetChainTipSource call before Start would silently
	// no-op against the nil s.handlers.
	pendingChainTipSource  func() uint64
	pendingPeerCountSource func() int
}

// StorageInterface defines the storage interface for the API
type StorageInterface interface {
	StoreTransaction(tx []byte) error
	Close() error
	GetBalance(address string) (float64, error)
	// Ready returns nil if the storage backend is reachable (used by GET /api/v1/health/ready).
	Ready() error
	// GetTransaction returns the stored envelope for a tx_id, or
	// an error (which on a "not found" miss is a wrapped storage
	// error rather than (nil, nil)). Added in v0.4.0 (Session 95)
	// for the /wallet/submit-signed idempotency check.
	GetTransaction(txID string) (map[string]interface{}, error)
	// GetNonce returns the last-applied nonce for `address`.
	// Returns (0, nil) for an address that has never sent a
	// v0.4.1 envelope (the contract is "0 means new sender").
	// Added in v0.4.1 (Session 100) for /wallet/submit-signed
	// replay protection (see V041_REPLAY_PROTECTION_DESIGN.md
	// §4.1). Backends that don't track per-account nonces
	// (file_storage, legacy scylla pre-LWT) return a wrapped
	// error so the handler can degrade cleanly rather than
	// silently allowing replays.
	GetNonce(address string) (uint64, error)
	// ApplyTransferAtomic is the v0.4.1 single-transaction
	// debit + credit + nonce-bump + tx-insert primitive used
	// by the /wallet/submit-signed handler. Replaces the v0.4.0
	// non-atomic sequence (pre-flight GetBalance + GetTransaction
	// + StoreTransaction → which internally called UpdateBalance
	// twice without an enclosing transaction).
	//
	// Returns one of the sentinels declared in pkg/storage:
	//   ErrInsufficientBalance — sender balance < amount + fee
	//   ErrNonceConflict       — sender's nonce no longer matches
	//                            the pre-image (concurrent submit)
	//   ErrTxAlreadyExists     — tx_id already in transactions
	// or a wrapped backend-internal storage error. See
	// V041_REPLAY_PROTECTION_DESIGN.md §4.2.
	//
	// envelopeNonce == 0 puts ApplyTransferAtomic on the legacy
	// v0.4.0 path: no nonce check, no nonce bump, but the balance
	// + tx_id checks still fire. This is the bi-directional
	// backward-compatibility guarantee for v0.4.0 wallets that
	// have not yet rebuilt against the new wire-format.
	ApplyTransferAtomic(
		ctx context.Context,
		sender, recipient string,
		amount, fee float64,
		envelopeNonce uint64,
		txID string,
		rawEnvelope []byte,
	) error
}

// NewServer creates a new API server instance.
// If sharedAuth is non-nil, it is used as the API AuthManager (must be the same instance as the dashboard so JWTs verify; each NewAuthManager generates a new ML-DSA keypair).
// If sharedAuth is nil, a new AuthManager is created (tests and standalone API).
func NewServer(cfg *config.Config, logger *logging.Logger, walletService *wallet.WalletService, storage StorageInterface, submeshManager *submesh.DynamicSubmeshManager, sharedAuth *AuthManager) (*Server, error) {
	var authManager *AuthManager
	var err error
	if sharedAuth != nil {
		authManager = sharedAuth
	} else {
		authManager, err = NewAuthManager()
		if err != nil {
			return nil, fmt.Errorf("failed to create auth manager: %w", err)
		}
		authManager.SetJWTHMACFallbackSecret(cfg.JWTHMACSecret)
		// rotation-01: optional VERIFY-ONLY secondary key
		authManager.SetJWTHMACFallbackSecondarySecret(cfg.JWTHMACSecretSecondary)
	}

	// Initialize user store. When UserStorePath is configured (the
	// normal case in production), load any accounts that were
	// registered before the last restart. Without this, every redeploy
	// silently wipes every dashboard login — see the 2026-04-23
	// incident. Tests and embedded callers can leave UserStorePath
	// empty to keep the old volatile behaviour.
	var userStore *UserStore
	if cfg.UserStorePath != "" {
		userStore, err = LoadOrNewUserStore(cfg.UserStorePath)
		if err != nil {
			return nil, fmt.Errorf("failed to load user store at %s: %w", cfg.UserStorePath, err)
		}
		logger.Info("User store persistence", "path", cfg.UserStorePath, "users_loaded", userStore.Count())
	} else {
		userStore = NewUserStore()
		logger.Warn("User store persistence is DISABLED (UserStorePath empty); every restart will wipe dashboard accounts")
	}

	maxRL := cfg.APIRateLimitMaxRequests
	winRL := cfg.APIRateLimitWindow
	if maxRL < 1 {
		maxRL = 100
	}
	if winRL < time.Second {
		winRL = time.Minute
	}
	rateLimiter := NewRateLimiter(maxRL, winRL)
	logger.Info("API rate limiting", "max_requests_per_client", maxRL, "window", winRL.String())

	// Initialize request signer
	requestSigner, err := NewRequestSigner(cfg.JWTHMACSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to create request signer: %w", err)
	}
	// rotation-01: optional VERIFY-ONLY secondary key on the same surface
	requestSigner.SetSecondaryHMACSecret(cfg.JWTHMACSecretSecondary)

	// Initialize CSRF manager
	csrfManager := NewCSRFManager()

	// Initialize token revocation store (MED-7). Attaching it now means
	// every JWT validation runs through the revocation check, and the
	// /auth/logout handler has somewhere to record explicit logouts.
	revocations := NewTokenRevocationStore()
	authManager.SetRevocationStore(revocations)

	return &Server{
		config:           cfg,
		logger:           logger,
		authManager:      authManager,
		userStore:        userStore,
		rateLimiter:      rateLimiter,
		requestSigner:    requestSigner,
		walletService:    walletService,
		storage:          storage,
		submeshManager:   submeshManager,
		csrfManager:      csrfManager,
		revocations:      revocations,
	}, nil
}

// Start starts the HTTP API server with TLS
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Register routes
	s.registerRoutes(mux)

	// Create HTTP server with security middleware, then body size cap (must be on the handler the server uses).
	handler := s.setupMiddleware(mux)
	handler = RequestSizeLimitMiddleware(1 << 20)(handler)

	// ACME auto-provisioned TLS (Let's Encrypt) takes highest precedence
	if len(s.config.ACMEDomains) > 0 {
		acmeCfg := ACMEConfig{
			Domains:  s.config.ACMEDomains,
			Email:    s.config.ACMEEmail,
			CacheDir: s.config.ACMECacheDir,
		}
		acmeTLS, challengeHandler, acmeErr := ConfigureACME(acmeCfg)
		if acmeErr != nil {
			return fmt.Errorf("ACME setup: %w", acmeErr)
		}

		s.httpServer = &http.Server{
			Addr:           fmt.Sprintf(":%d", s.config.APIPort),
			Handler:        handler,
			TLSConfig:      acmeTLS,
			ReadTimeout:    15 * time.Second,
			WriteTimeout:   15 * time.Second,
			IdleTimeout:    120 * time.Second,
			MaxHeaderBytes: 1 << 20,
		}

		s.logger.Info("Starting API server with ACME auto-TLS",
			"domains", s.config.ACMEDomains,
			"port", s.config.APIPort,
		)

		go func() {
			httpSrv := &http.Server{
				Addr:    ":80",
				Handler: challengeHandler,
			}
			if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				s.logger.Warn("ACME HTTP challenge listener failed", "error", err)
			}
		}()

		return s.httpServer.ListenAndServeTLS("", "")
	}

	// mTLS: mutual TLS with client certificate verification
	if s.config.MTLSCACertFile != "" && s.config.MTLSNodeCertFile != "" && s.config.MTLSNodeKeyFile != "" {
		mtlsCfg := MTLSConfig{
			CACertFile:   s.config.MTLSCACertFile,
			NodeCertFile: s.config.MTLSNodeCertFile,
			NodeKeyFile:  s.config.MTLSNodeKeyFile,
		}
		mtlsTLS, mtlsErr := ConfigureMTLS(mtlsCfg)
		if mtlsErr != nil {
			return fmt.Errorf("mTLS setup: %w", mtlsErr)
		}

		s.httpServer = &http.Server{
			Addr:           fmt.Sprintf(":%d", s.config.APIPort),
			Handler:        handler,
			TLSConfig:      mtlsTLS,
			ReadTimeout:    15 * time.Second,
			WriteTimeout:   15 * time.Second,
			IdleTimeout:    120 * time.Second,
			MaxHeaderBytes: 1 << 20,
		}

		s.logger.Info("Starting API server with mutual TLS (mTLS)",
			"port", s.config.APIPort,
			"ca_cert", s.config.MTLSCACertFile,
		)

		return s.httpServer.ListenAndServeTLS(s.config.MTLSNodeCertFile, s.config.MTLSNodeKeyFile)
	} else if s.config.MTLSAutoGenerate {
		bundle, genErr := GenerateNodeBundle("qsdm-node", []string{"localhost", "127.0.0.1"})
		if genErr != nil {
			return fmt.Errorf("mTLS auto-generate: %w", genErr)
		}
		caCert, nodeCert, nodeKey, writeErr := bundle.WriteBundleToDisk("certs")
		if writeErr != nil {
			return fmt.Errorf("mTLS write certs: %w", writeErr)
		}
		s.logger.Info("Auto-generated mTLS certificates", "ca", caCert, "cert", nodeCert, "key", nodeKey)

		mtlsCfg := MTLSConfig{CACertFile: caCert, NodeCertFile: nodeCert, NodeKeyFile: nodeKey}
		mtlsTLS, _ := ConfigureMTLS(mtlsCfg)
		s.httpServer = &http.Server{
			Addr:           fmt.Sprintf(":%d", s.config.APIPort),
			Handler:        handler,
			TLSConfig:      mtlsTLS,
			ReadTimeout:    15 * time.Second,
			WriteTimeout:   15 * time.Second,
			IdleTimeout:    120 * time.Second,
			MaxHeaderBytes: 1 << 20,
		}
		return s.httpServer.ListenAndServeTLS(nodeCert, nodeKey)
	}

	if s.config.EnableTLS {
		tlsConfig := &tls.Config{
			MinVersion:               tls.VersionTLS13,
			PreferServerCipherSuites: true,
			CipherSuites: []uint16{
				tls.TLS_AES_256_GCM_SHA384,
				tls.TLS_AES_128_GCM_SHA256,
				tls.TLS_CHACHA20_POLY1305_SHA256,
			},
			CurvePreferences: []tls.CurveID{
				tls.X25519,
				tls.CurveP256,
				tls.CurveP384,
			},
		}

		s.httpServer = &http.Server{
			Addr:           fmt.Sprintf(":%d", s.config.APIPort),
			Handler:        handler,
			TLSConfig:      tlsConfig,
			ReadTimeout:    15 * time.Second,
			WriteTimeout:   15 * time.Second,
			IdleTimeout:    120 * time.Second,
			MaxHeaderBytes: 1 << 20,
		}

		s.logger.Info("Starting secure API server with TLS",
			"port", s.config.APIPort,
			"tls_version", "1.3",
		)

		certFile := s.config.TLSCertFile
		keyFile := s.config.TLSKeyFile

		if certFile == "" || keyFile == "" {
			s.logger.Warn("TLS certificates not configured, generating self-signed certificates")
			certFile, keyFile, err := s.generateSelfSignedCert()
			if err != nil {
				return fmt.Errorf("failed to generate self-signed certificate: %w", err)
			}
			// rotation-05: register the self-signed cert's NotAfter
			// for the rotation-monitoring gauge. Self-signed is the
			// dev path so the resulting alert says "rotate me" loud
			// when the operator forgets to swap in a real cert.
			if notAfter, gErr := monitoring.RecordCertExpiryFromFile(monitoring.SecretExpiryKindTLSCert, certFile, certFile); gErr == nil {
				s.logger.Info("TLS cert expiry registered with rotation gauge",
					"audit_row", "rotation-05",
					"path", certFile,
					"not_after", notAfter.Format(time.RFC3339),
					"days_until_expiry", int(time.Until(notAfter).Hours()/24),
				)
			} else {
				s.logger.Warn("TLS cert expiry gauge registration failed", "path", certFile, "error", gErr)
			}
			return s.httpServer.ListenAndServeTLS(certFile, keyFile)
		}

		// rotation-05: register the operator-supplied cert's NotAfter
		// for the rotation-monitoring gauge.
		if notAfter, gErr := monitoring.RecordCertExpiryFromFile(monitoring.SecretExpiryKindTLSCert, certFile, certFile); gErr == nil {
			s.logger.Info("TLS cert expiry registered with rotation gauge",
				"audit_row", "rotation-05",
				"path", certFile,
				"not_after", notAfter.Format(time.RFC3339),
				"days_until_expiry", int(time.Until(notAfter).Hours()/24),
			)
		} else {
			s.logger.Warn("TLS cert expiry gauge registration failed", "path", certFile, "error", gErr)
		}
		return s.httpServer.ListenAndServeTLS(certFile, keyFile)
	} else {
		// HTTP mode (development only - NOT recommended for production)
		s.logger.Warn("Starting API server in HTTP mode (INSECURE - development only)")
		s.httpServer = &http.Server{
			Addr:         fmt.Sprintf(":%d", s.config.APIPort),
			Handler:      handler,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
			IdleTimeout:  120 * time.Second,
			MaxHeaderBytes: 1 << 20,
		}
		return s.httpServer.ListenAndServe()
	}
}

// Stop gracefully stops the server
func (s *Server) Stop() error {
	// Release background goroutines owned by per-server stores so a
	// repeated start/stop sequence (test suites, hot-reload paths) does
	// not leak workers.
	if s.csrfManager != nil {
		s.csrfManager.Stop()
	}
	if s.revocations != nil {
		s.revocations.Stop()
	}
	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}

// SetContractEngine attaches the contract engine to the server (call before Start).
func (s *Server) SetContractEngine(ce *contracts.ContractEngine) { s.contractEngine = ce }

// SetBridgeProtocol attaches the bridge protocol to the server (call before Start).
func (s *Server) SetBridgeProtocol(bp *bridge.BridgeProtocol) { s.bridgeProtocol = bp }

// SetAtomicSwapProtocol attaches the atomic swap protocol to the server (call before Start).
func (s *Server) SetAtomicSwapProtocol(asp *bridge.AtomicSwapProtocol) { s.atomicSwap = asp }

// SetBridgeRelay attaches the P2P bridge relay so API handlers can broadcast events.
func (s *Server) SetBridgeRelay(r *bridge.P2PRelay, nodeID string) {
	s.bridgeRelay = r
	s.nodeID = nodeID
}

// SetTokenRegistryPath sets the path for persistent token registry.
// If set, tokens are loaded during route registration and saved on shutdown.
func (s *Server) SetTokenRegistryPath(path string) { s.tokenRegistryPath = path }

// SetAdminAPI attaches the admin REST subsystem (call before Start).
func (s *Server) SetAdminAPI(a *AdminAPI) { s.adminAPI = a }

// SetTxGossipBroadcast sets optional P2P publish after wallet/API sends (call before Start).
func (s *Server) SetTxGossipBroadcast(fn func([]byte) error) { s.txGossipBroadcast = fn }

// SetChainTipSource wires a live chain-tip callback into the
// status handler so GET /api/v1/status returns the real
// producer height instead of a hardcoded 0. Safe to call any
// time before or after Start; the callback must be safe for
// concurrent use and return quickly because it runs on every
// status hit. Pre-Start calls are stashed in
// pendingChainTipSource and applied in registerRoutes when
// the concrete *Handlers exists.
func (s *Server) SetChainTipSource(fn func() uint64) {
	if s == nil {
		return
	}
	if s.handlers != nil {
		s.handlers.SetChainTipSource(fn)
		return
	}
	s.pendingChainTipSource = fn
}

// SetPeerCountSource is the matching accessor for the live
// peer-count callback. Same concurrency / Start contract as
// SetChainTipSource.
func (s *Server) SetPeerCountSource(fn func() int) {
	if s == nil {
		return
	}
	if s.handlers != nil {
		s.handlers.SetPeerCountSource(fn)
		return
	}
	s.pendingPeerCountSource = fn
}

// setupMiddleware configures all security middleware.
//
// Composition convention: each wrap returns a handler that calls the
// inner one, so the LAST call in this function is the OUTERMOST layer
// on the wire. We layer security from "infrastructure" (TLS-ish, CORS,
// headers) outward, then resource limits (rate limit, request timeout),
// then policy (CSRF, signing, auth, admin gate).
//
// Specifically:
//
//	wire → SecurityHeaders → CORS → AuditLog → DeprecationMiddleware
//	     → RequestTimeout → RateLimit → CSRF → RequestSigning
//	     → AuthMiddleware → AdminAccessMiddleware → mux
//
// SecurityHeaders is OUTERMOST so even an early-rejection 4xx response
// (e.g. CORS denial) still carries HSTS/CSP/etc. — without this,
// preflight failures would emit unhardened bodies.
//
// RequestTimeout sits before RateLimit so a request that would have been
// rate-limited cannot also pin a worker via the deadline; conversely it
// sits outside CSRF/Auth so handler logic always runs under the deadline.
func (s *Server) setupMiddleware(handler http.Handler) http.Handler {
	// 9. Optional stricter /api/admin access (role + mTLS)
	handler = AdminAccessMiddleware(s.config, s.logger)(handler)

	// 8. Authentication (validate tokens, populates claims context)
	handler = AuthMiddleware(s.authManager, s.logger)(handler)

	// 7. Request signing (validate request integrity)
	handler = RequestSigningMiddleware(s.requestSigner, s.logger)(handler)

	// 6. CSRF protection (prevent cross-site request forgery)
	handler = CSRFMiddleware(s.csrfManager)(handler)

	// 5. Rate limiting (prevent DDoS / brute force)
	handler = s.rateLimiter.RateLimitMiddleware(handler)

	// 4. Per-request context timeout (MED-5). Default 30s; bypass for
	//    websocket / streaming routes is built into the middleware.
	handler = RequestTimeoutMiddleware(DefaultRequestTimeout)(handler)

	// 3. API version deprecation handling (MED-4). Emits Deprecation /
	//    Sunset / Link headers; returns 410 Gone for sunset versions.
	handler = DeprecationMiddleware()(handler)

	// 2. Audit logging (log all requests). Sits outside the security
	//    decisions so we capture both accepted and rejected traffic.
	handler = AuditLogMiddleware(s.logger)(handler)

	// 1b. CORS (MED-6). Applied OUTSIDE the audit log so denied
	//     preflights still produce one log line, but INSIDE security
	//     headers so the 403 carries HSTS/CSP.
	handler = CORSMiddleware(LoadCORSConfigFromEnv(osGetenv))(handler)

	// 1a. Security headers (HIGH-5) — outermost so every response,
	//     including early CORS / rate-limit / auth denials, carries the
	//     canonical security header set.
	handler = SecurityHeaders(handler)

	return handler
}

// osGetenv is a tiny indirection so tests can shim out os.Getenv. We
// avoid pulling in the test-only "envcompat" path here because the CORS
// loader only needs the bare reader; richer fallback semantics are
// reserved for the structured config loader.
var osGetenv = func(name string) string {
	return getEnvForCORS(name)
}

// LoadTokenRegistry loads persisted user-created tokens from path.
func (s *Server) LoadTokenRegistry(path string) (int, error) {
	if s.handlers == nil {
		return 0, nil
	}
	return s.handlers.LoadTokenRegistry(path)
}

// SaveTokenRegistry persists user-created tokens to path.
func (s *Server) SaveTokenRegistry(path string) error {
	if s.handlers == nil {
		return nil
	}
	return s.handlers.SaveTokenRegistry(path)
}

// SetupTestHandler creates a test handler without TLS for testing
func (s *Server) SetupTestHandler() http.Handler {
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	return s.setupMiddleware(mux)
}

// RequestSigner returns the per-server RequestSigner so tests can
// produce signatures that match the server's verification path
// regardless of which backend the build selected (Dilithium via
// CGO+liboqs, Dilithium via cloudflare/circl pure-Go, or the
// HMAC-SHA256 fallback in non-CGO stub builds). Production
// callers do NOT need this — the middleware reads the same
// signer internally.
//
// Test-only contract: signatures produced by this RequestSigner
// (RequestSigner.SignRequest) are accepted by the same Server's
// VerifyRequest. This is true for all three backends because
// SignRequest and VerifyRequest both consult the same underlying
// (Dilithium handle | HMAC secret) state.
func (s *Server) RequestSigner() *RequestSigner {
	return s.requestSigner
}

