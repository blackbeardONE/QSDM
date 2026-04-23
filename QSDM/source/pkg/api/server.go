package api

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/bridge"
	"github.com/blackbeardONE/QSDM/pkg/config"
	"github.com/blackbeardONE/QSDM/pkg/contracts"
	"github.com/blackbeardONE/QSDM/pkg/submesh"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

// Server represents the HTTP API server
type Server struct {
	config        *config.Config
	logger        *logging.Logger
	authManager   *AuthManager
	userStore     *UserStore
	rateLimiter   *RateLimiter
	requestSigner *RequestSigner
	csrfManager   *CSRFManager
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
}

// StorageInterface defines the storage interface for the API
type StorageInterface interface {
	StoreTransaction(tx []byte) error
	Close() error
	GetBalance(address string) (float64, error)
	// Ready returns nil if the storage backend is reachable (used by GET /api/v1/health/ready).
	Ready() error
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

	// Initialize CSRF manager
	csrfManager := NewCSRFManager()

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
		bundle, genErr := GenerateNodeBundle("qsdmplus-node", []string{"localhost", "127.0.0.1"})
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
			return s.httpServer.ListenAndServeTLS(certFile, keyFile)
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

// setupMiddleware configures all security middleware
func (s *Server) setupMiddleware(handler http.Handler) http.Handler {
	// Order matters: outermost to innermost
	
	// 1. Security headers (outermost)
	handler = SecurityHeaders(handler)
	
	// 2. Audit logging (log all requests)
	handler = AuditLogMiddleware(s.logger)(handler)
	
	// 3. Rate limiting (prevent DDoS)
	handler = s.rateLimiter.RateLimitMiddleware(handler)
	
	// 4. CSRF protection (prevent cross-site request forgery)
	handler = CSRFMiddleware(s.csrfManager)(handler)
	
	// 5. Request signing (validate request integrity)
	handler = RequestSigningMiddleware(s.requestSigner, s.logger)(handler)
	
	// 6. Authentication (validate tokens)
	handler = AuthMiddleware(s.authManager, s.logger)(handler)

	// 7. Optional stricter /api/admin access (role + mTLS)
	handler = AdminAccessMiddleware(s.config, s.logger)(handler)

	return handler
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

