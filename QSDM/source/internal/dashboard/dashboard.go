package dashboard

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/branding"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
	"github.com/blackbeardONE/QSDM/pkg/networking"
)

//go:embed static/*
var staticFiles embed.FS

// DashboardNvidiaLock mirrors API NVIDIA-lock policy for the NGC panel. ProofHMACSecret is used in-process only to compute proof_ok.
type DashboardNvidiaLock struct {
	Enabled               bool
	MaxProofAge           time.Duration
	ExpectedNodeID        string
	ProofHMACSecret       string
	RequireIngestNonce    bool
	IngestNonceTTLSeconds int64
	GateP2P               bool
}

// Dashboard serves the monitoring dashboard
type Dashboard struct {
	metrics             *monitoring.Metrics
	healthChecker       *monitoring.HealthChecker
	topologyMonitor     *monitoring.TopologyMonitor
	port                string
	authManager         *api.AuthManager
	rateLimiter         *api.RateLimiter
	ngcIngestConfigured bool
	nvidiaLock            DashboardNvidiaLock
	metricsScrapeSecret   string
	strictDashboardAuth   bool
	// apiBackendURL is the in-process API base (e.g. http://127.0.0.1:8080). When set, login/register are reverse-proxied so the browser can POST same-origin from the dashboard port.
	apiBackendURL string

	// sessions: ML-DSA JWTs exceed browser cookie size limits (~4KB); we store the token server-side and set a short HttpOnly session id cookie.
	sessionMu sync.Mutex
	sessions  map[string]dashboardSessionEntry

	// WebSocket hub for real-time push updates
	wsHub *WSHub
	// Optional advanced metrics pusher for chain/finality/reputation/prometheus snapshots.
	wsMetricsSource   *MetricsSource
	wsMetricsPusher   *MetricsPusher
	wsMetricsPushMu   sync.Mutex
	wsMetricsInterval time.Duration
}

type dashboardSessionEntry struct {
	token   string
	expires int64 // unix seconds (from JWT claims)
}

type dashboardLoginRequest struct {
	Address  string `json:"address"`
	Password string `json:"password"`
}

// NewDashboard creates a new dashboard instance.
// ngcIngestConfigured mirrors whether the API node has NGC ingest secret set (QSDM_NGC_INGEST_SECRET or QSDM_NGC_INGEST_SECRET).
// If sharedAuth is non-nil, it is used for JWT validation (must match api.Server's AuthManager when using ML-DSA / CGO).
// If sharedAuth is nil, a separate AuthManager is created (tests only; production qsdm passes a shared instance).
func NewDashboard(metrics *monitoring.Metrics, healthChecker *monitoring.HealthChecker, port string, ngcIngestConfigured bool, nvidiaLock DashboardNvidiaLock, jwtHMACSecret string, metricsScrapeSecret string, strictDashboardAuth bool, apiBackendURL string, sharedAuth *api.AuthManager) *Dashboard {
	var authManager *api.AuthManager
	if sharedAuth != nil {
		authManager = sharedAuth
	} else {
		var err error
		authManager, err = api.NewAuthManager()
		if err != nil {
			log.Printf("WARNING: Failed to initialize auth manager for dashboard: %v", err)
			log.Printf("Dashboard will run without authentication (INSECURE)")
			authManager = nil
		} else {
			authManager.SetJWTHMACFallbackSecret(jwtHMACSecret)
		}
	}

	// Initialize rate limiter (50 requests per minute per client)
	rateLimiter := api.NewRateLimiter(50, 1*time.Minute)

	hub := NewWSHub()
	hub.Run()

	return &Dashboard{
		metrics:               metrics,
		healthChecker:         healthChecker,
		topologyMonitor:       nil, // Will be set via SetNetwork
		port:                  port,
		authManager:           authManager,
		rateLimiter:           rateLimiter,
		ngcIngestConfigured:   ngcIngestConfigured,
		nvidiaLock:            nvidiaLock,
		metricsScrapeSecret:   strings.TrimSpace(metricsScrapeSecret),
		strictDashboardAuth:   strictDashboardAuth,
		apiBackendURL:         strings.TrimSpace(apiBackendURL),
		sessions:              make(map[string]dashboardSessionEntry),
		wsHub:                 hub,
	}
}

// SetNetwork sets the network instance for topology monitoring
func (d *Dashboard) SetNetwork(net *networking.Network) {
	if net == nil {
		return
	}

	// Create topology monitor
	d.topologyMonitor = monitoring.NewTopologyMonitor(net)
	log.Printf("Network topology monitoring enabled")
}

// SetRealtimeMetricsSource configures advanced WebSocket metrics snapshots.
// If StartWSPush already ran, the pusher is started on the next tick or immediately when interval is known.
func (d *Dashboard) SetRealtimeMetricsSource(source MetricsSource) {
	d.wsMetricsPushMu.Lock()
	d.wsMetricsSource = &source
	interval := d.wsMetricsInterval
	d.wsMetricsPushMu.Unlock()
	if interval > 0 {
		d.ensureMetricsPusher(interval)
	}
}

func (d *Dashboard) ensureMetricsPusher(interval time.Duration) {
	d.wsMetricsPushMu.Lock()
	defer d.wsMetricsPushMu.Unlock()
	if d.wsHub == nil || d.wsMetricsSource == nil || d.wsMetricsPusher != nil || interval <= 0 {
		return
	}
	d.wsMetricsPusher = NewMetricsPusher(d.wsHub, *d.wsMetricsSource, interval)
	d.wsMetricsPusher.Start()
}

// buildHandler returns the full HTTP handler (mux + security middleware). Used by Start and tests.
func (d *Dashboard) buildHandler() (http.Handler, error) {
	mux := http.NewServeMux()

	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, fmt.Errorf("failed to create static filesystem: %w", err)
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	mux.HandleFunc("/api/auth/login", d.handleLogin)
	mux.HandleFunc("/api/auth/session", d.handleEstablishSession)

	var authAPIProxy *httputil.ReverseProxy
	if strings.TrimSpace(d.apiBackendURL) != "" {
		backend, perr := url.Parse(d.apiBackendURL)
		if perr != nil {
			return nil, fmt.Errorf("invalid api backend URL %q: %w", d.apiBackendURL, perr)
		}
		authAPIProxy = httputil.NewSingleHostReverseProxy(backend)
		log.Printf("Dashboard auth proxy: POST /api/v1/auth/login and /api/v1/auth/register -> %s", d.apiBackendURL)
	} else {
		log.Printf("Dashboard: no API backend URL — POST /api/v1/auth/login and /register return 503 until configured")
	}
	proxyV1Auth := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if authAPIProxy == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "Service Unavailable",
				"message": "Dashboard is not configured with an API backend URL for login/register (use qsdm or pass apiBackendURL, e.g. http://127.0.0.1:8080).",
				"status":  http.StatusServiceUnavailable,
			})
			return
		}
		authAPIProxy.ServeHTTP(w, r)
	}
	mux.HandleFunc("/api/v1/auth/login", proxyV1Auth)
	mux.HandleFunc("/api/v1/auth/register", proxyV1Auth)

	// Contract / bridge dashboard proxy -> API backend
	proxyAPIDashboard := func(w http.ResponseWriter, r *http.Request) {
		if authAPIProxy == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "API backend not configured"})
			return
		}
		// Rewrite path: /api/contracts/... -> /api/v1/contracts/...
		// and /api/bridge/... -> /api/v1/bridge/...
		orig := r.URL.Path
		if strings.HasPrefix(orig, "/api/contracts/") {
			r.URL.Path = "/api/v1/contracts/" + strings.TrimPrefix(orig, "/api/contracts/")
		} else if strings.HasPrefix(orig, "/api/contracts") {
			r.URL.Path = "/api/v1/contracts"
		} else if strings.HasPrefix(orig, "/api/bridge/") {
			r.URL.Path = "/api/v1/bridge/" + strings.TrimPrefix(orig, "/api/bridge/")
		} else if strings.HasPrefix(orig, "/api/bridge") {
			r.URL.Path = "/api/v1/bridge"
		}
		// Inject session JWT as Bearer token for the backend
		if sid, err2 := r.Cookie("dashboard_session"); err2 == nil {
			d.sessionMu.Lock()
			entry, ok := d.sessions[sid.Value]
			d.sessionMu.Unlock()
			if ok {
				r.Header.Set("Authorization", "Bearer "+entry.token)
			}
		}
		authAPIProxy.ServeHTTP(w, r)
	}
	mux.HandleFunc("/api/contracts/", d.requireAuth(proxyAPIDashboard))
	mux.HandleFunc("/api/contracts", d.requireAuth(proxyAPIDashboard))
	mux.HandleFunc("/api/bridge/", d.requireAuth(proxyAPIDashboard))
	mux.HandleFunc("/api/bridge", d.requireAuth(proxyAPIDashboard))

	mux.HandleFunc("/", d.requireAuth(d.handleDashboard))
	mux.HandleFunc("/api/metrics", d.requireAuth(d.handleMetrics))
	mux.HandleFunc("/api/metrics/prometheus", d.requireMetricsScrapeOrAuth(d.handleMetricsPrometheus))
	mux.HandleFunc("/api/health", d.requireAuth(d.handleHealth))
	mux.HandleFunc("/api/topology", d.requireAuth(d.handleTopology))
	mux.HandleFunc("/api/mesh3d-viz", d.requireAuth(d.handleMesh3DViz))
	mux.HandleFunc("/api/ngc-proofs", d.requireAuth(d.handleNGCProofs))

	// mTLS certificate management (admin only — certs are security-sensitive)
	mux.HandleFunc("/api/mtls/generate", d.requireAdmin(d.handleMTLSGenerate))
	mux.HandleFunc("/api/mtls/status", d.requireAuth(d.handleMTLSStatus))

	// Role / identity
	mux.HandleFunc("/api/whoami", d.requireAuth(d.handleWhoAmI))

	// WebSocket for real-time push updates
	mux.HandleFunc("/ws", d.handleWS)

	return d.setupMiddleware(mux), nil
}

// Start starts the dashboard HTTP server
func (d *Dashboard) Start() error {
	handler, err := d.buildHandler()
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:           ":" + d.port,
		Handler:        handler,
		ReadTimeout:    15 * time.Second,
		WriteTimeout:   15 * time.Second,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1MB
	}

	log.Printf("Dashboard server starting on port %s", d.port)
	if d.authManager != nil {
		log.Printf("Dashboard authentication: ENABLED (JWT tokens required)")
		log.Printf("Login endpoint: http://localhost:%s/api/auth/login", d.port)
		if d.metricsScrapeSecret != "" {
			log.Printf("Metrics scrape secret: configured (use %s [legacy %s also accepted] or Authorization: Bearer for /api/metrics/prometheus)",
				branding.MetricsScrapeSecretHeaderPreferred, branding.MetricsScrapeSecretHeaderLegacy)
		}
	} else if d.strictDashboardAuth {
		log.Printf("Dashboard authentication: UNAVAILABLE (JWT init failed); strict_dashboard_auth=true — protected routes return 503; set metrics_scrape_secret for Prometheus-only scrape")
	} else {
		log.Printf("Dashboard authentication: DISABLED (INSECURE - development only)")
	}
	log.Printf("Dashboard will be available at http://localhost:%s", d.port)
	log.Printf("API endpoints: /api/metrics, /api/metrics/prometheus, /api/health, /api/topology, /api/mesh3d-viz, /api/ngc-proofs (require authentication)")

	// Verify embedded files are available
	entries, err := fs.ReadDir(staticFiles, "static")
	if err != nil {
		log.Printf("WARNING: Could not read static files directory: %v", err)
	} else {
		log.Printf("Found %d static files:", len(entries))
		for _, entry := range entries {
			log.Printf("  - static/%s", entry.Name())
		}
	}

	// Start WebSocket push loop (2s interval matches existing polling)
	d.StartWSPush(2 * time.Second)

	// Start server and handle errors
	log.Printf("Starting HTTP server on :%s", d.port)
	err = server.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		log.Printf("ERROR: Dashboard server failed: %v", err)
		return fmt.Errorf("dashboard server failed: %w", err)
	}

	return nil
}

// handleDashboard serves the main dashboard page
func (d *Dashboard) handleDashboard(w http.ResponseWriter, r *http.Request) {
	// Only serve dashboard on root path
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	log.Printf("Dashboard request received: %s %s", r.Method, r.URL.Path)

	// Read the HTML file directly from embedded FS
	htmlContent, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		log.Printf("ERROR: Failed to read HTML file: %v", err)
		// Try to list what files are available
		entries, listErr := fs.ReadDir(staticFiles, "static")
		if listErr == nil {
			log.Printf("Available static files:")
			for _, entry := range entries {
				log.Printf("  - %s", entry.Name())
			}
		}
		http.Error(w, fmt.Sprintf("Failed to load dashboard: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("Serving dashboard HTML (%d bytes)", len(htmlContent))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	if _, err := w.Write(htmlContent); err != nil {
		log.Printf("ERROR: Failed to write response: %v", err)
	} else {
		log.Printf("Dashboard HTML served successfully")
	}
}

// handleMetrics returns current metrics as JSON
func (d *Dashboard) handleMetrics(w http.ResponseWriter, r *http.Request) {
	log.Printf("Metrics request received: %s %s", r.Method, r.URL.Path)
	stats := d.metrics.GetStats()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if err := json.NewEncoder(w).Encode(stats); err != nil {
		log.Printf("ERROR: Failed to encode metrics: %v", err)
		http.Error(w, "Failed to encode metrics", http.StatusInternalServerError)
	} else {
		log.Printf("Metrics served successfully")
	}
}

// handleMetricsPrometheus serves OpenMetrics/Prometheus text exposition (same auth as /api/metrics).
func (d *Dashboard) handleMetricsPrometheus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(monitoring.PrometheusExposition())); err != nil {
		log.Printf("ERROR: Failed to write prometheus metrics: %v", err)
	}
}

// handleHealth returns health status as JSON
func (d *Dashboard) handleHealth(w http.ResponseWriter, r *http.Request) {
	log.Printf("Health request received: %s %s", r.Method, r.URL.Path)
	report := d.healthChecker.GetHealthReport()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if err := json.NewEncoder(w).Encode(report); err != nil {
		log.Printf("ERROR: Failed to encode health report: %v", err)
		http.Error(w, "Failed to encode health report", http.StatusInternalServerError)
	} else {
		log.Printf("Health status served successfully")
	}
}

// handleNGCProofs returns summarized NGC sidecar proof bundles ingested by the node.
func (d *Dashboard) handleNGCProofs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	summaries := monitoring.NGCProofSummaries()
	nv := d.nvidiaLock
	maxAge := nv.MaxProofAge
	if maxAge <= 0 {
		maxAge = 15 * time.Minute
	}
	lockOK, _ := monitoring.NvidiaLockProofOK(maxAge, nv.ExpectedNodeID, nv.ProofHMACSecret, false)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ingest_configured":  d.ngcIngestConfigured,
		"count":              len(summaries),
		"proofs":             summaries,
		"ngc_proof_ingest":   monitoring.NGCIngestStatsMap(),
		"nvidia_lock": map[string]interface{}{
			"enabled":                          nv.Enabled,
			"proof_ok":                         lockOK,
			"node_id_binding_enabled":          strings.TrimSpace(nv.ExpectedNodeID) != "",
			"hmac_required":                    strings.TrimSpace(nv.ProofHMACSecret) != "",
			"ingest_nonce_required":            nv.RequireIngestNonce,
			"ingest_nonce_ttl_seconds":         nv.IngestNonceTTLSeconds,
			"http_blocks_total":                monitoring.NvidiaLockHTTPBlockCount(),
			"ngc_challenge_issued_total":       monitoring.NGCChallengeIssuedCount(),
			"ngc_challenge_rate_limited_total": monitoring.NGCChallengeRateLimitedCount(),
			"ngc_ingest_nonce_pool_size":       monitoring.NGCIngestNoncePoolSize(),
			"p2p_gate_enabled":                 nv.Enabled && nv.GateP2P,
			"p2p_rejects_total":                monitoring.NvidiaLockP2PRejectCount(),
		},
	})
}

// handleTopology returns network topology as JSON
func (d *Dashboard) handleTopology(w http.ResponseWriter, r *http.Request) {
	log.Printf("Topology request received: %s %s", r.Method, r.URL.Path)

	var topology map[string]interface{}
	if d.topologyMonitor != nil {
		topology = d.topologyMonitor.GetTopology()
	} else {
		topology = map[string]interface{}{
			"error":          "Topology monitoring not available",
			"nodes":          []interface{}{},
			"edges":          []interface{}{},
			"peerCount":      0,
			"connectedCount": 0,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if err := json.NewEncoder(w).Encode(topology); err != nil {
		log.Printf("ERROR: Failed to encode topology: %v", err)
		http.Error(w, "Failed to encode topology", http.StatusInternalServerError)
	} else {
		log.Printf("Topology served successfully")
	}
}

// handleMesh3DViz returns illustrative Phase-3 parent-cell geometry for the WebGL dashboard panel.
func (d *Dashboard) handleMesh3DViz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	peerCount := 0
	if d.topologyMonitor != nil {
		t := d.topologyMonitor.GetTopology()
		switch v := t["peerCount"].(type) {
		case int:
			peerCount = v
		case int64:
			peerCount = int(v)
		case float64:
			peerCount = int(v)
		}
	}
	payload := mesh3DReferenceViz(peerCount)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("ERROR: Failed to encode mesh3d viz: %v", err)
		http.Error(w, "Failed to encode mesh3d viz", http.StatusInternalServerError)
	}
}

// extractMetricsScrapeCredential returns a bearer token or dedicated header for Prometheus scrape auth.
// Accepts the preferred X-QSDM-Metrics-Scrape-Secret and the legacy X-QSDM-Metrics-Scrape-Secret
// during the Major Update rebrand deprecation window.
func (d *Dashboard) extractMetricsScrapeCredential(r *http.Request) string {
	if h := strings.TrimSpace(r.Header.Get(branding.MetricsScrapeSecretHeaderPreferred)); h != "" {
		return h
	}
	if h := strings.TrimSpace(r.Header.Get(branding.MetricsScrapeSecretHeaderLegacy)); h != "" {
		return h
	}
	auth := r.Header.Get("Authorization")
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

func writeJSONUnauthorized(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error":   "Unauthorized",
		"message": message,
		"status":  http.StatusUnauthorized,
	})
}

func writeDashboardAuthUnavailable(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error":   "Service Unavailable",
		"message": "Dashboard JWT authentication is not available; fix auth configuration or disable strict_dashboard_auth for local dev",
		"status":  http.StatusServiceUnavailable,
	})
}

// requireMetricsScrapeOrAuth allows GET /api/metrics/prometheus with a configured scrape secret
// (header or Bearer) or falls back to normal dashboard JWT auth.
func (d *Dashboard) requireMetricsScrapeOrAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if sec := d.metricsScrapeSecret; sec != "" {
			got := strings.TrimSpace(d.extractMetricsScrapeCredential(r))
			if got != "" {
				if subtle.ConstantTimeCompare([]byte(got), []byte(sec)) == 1 {
					next(w, r)
					return
				}
				writeJSONUnauthorized(w, "Invalid metrics scrape secret")
				return
			}
		}
		if d.strictDashboardAuth && d.authManager == nil {
			writeDashboardAuthUnavailable(w)
			return
		}
		d.requireAuth(next)(w, r)
	}
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

// redirectToLogin sends browsers to the login page with an optional machine-readable reason (shown by login.js).
func redirectToLogin(w http.ResponseWriter, r *http.Request, reason string) {
	q := url.Values{}
	if red := r.URL.Query().Get("redirect"); red != "" {
		q.Set("redirect", red)
	} else {
		q.Set("redirect", r.URL.Path)
	}
	if reason != "" {
		q.Set("reason", reason)
	}
	http.Redirect(w, r, "/api/auth/login?"+q.Encode(), http.StatusFound)
}

// setupMiddleware configures security middleware
func (d *Dashboard) setupMiddleware(handler http.Handler) http.Handler {
	// 1. Security headers (outermost)
	handler = api.SecurityHeaders(handler)

	// 2. Rate limiting
	handler = d.rateLimiter.RateLimitMiddleware(handler)

	return handler
}

// requireAuth wraps a handler to require authentication
func (d *Dashboard) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.authManager == nil {
			if d.strictDashboardAuth {
				writeDashboardAuthUnavailable(w)
				return
			}
			log.Printf("WARNING: Dashboard access without authentication (auth manager not available)")
			next(w, r)
			return
		}

		// Extract token from Authorization header or cookie
		token := d.extractToken(r)
		if token == "" {
			if wantsJSON(r) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":   "Unauthorized",
					"message": "Authentication required. Please login at /api/auth/login",
					"status":  http.StatusUnauthorized,
				})
			} else {
				redirectToLogin(w, r, "no_session")
			}
			return
		}

		// Validate token
		claims, err := d.authManager.ValidateToken(token)
		if err != nil {
			log.Printf("Dashboard authentication failed: %v", err)
			if wantsJSON(r) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":   "Unauthorized",
					"message": "Invalid or expired token",
					"status":  http.StatusUnauthorized,
				})
			} else {
				redirectToLogin(w, r, "bad_token")
			}
			return
		}

		// Check role (dashboard requires at least "user" role, "admin" for full access)
		if claims.Role != "admin" && claims.Role != "user" {
			log.Printf("Dashboard access denied for role: %s", claims.Role)
			if wantsJSON(r) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":   "Forbidden",
					"message": "Insufficient permissions",
					"status":  http.StatusForbidden,
				})
			} else {
				redirectToLogin(w, r, "forbidden")
			}
			return
		}

		// Add claims to request context
		ctx := context.WithValue(r.Context(), "claims", claims)
		next(w, r.WithContext(ctx))
	}
}

// requireAdmin wraps a handler so only admin-role users can access it.
func (d *Dashboard) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return d.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := r.Context().Value("claims").(*api.Claims)
		if !ok || claims.Role != "admin" {
			if wantsJSON(r) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":   "Forbidden",
					"message": "Admin role required",
					"status":  http.StatusForbidden,
				})
			} else {
				http.Error(w, "Admin role required", http.StatusForbidden)
			}
			return
		}
		next(w, r)
	})
}

// handleWhoAmI returns the current user's role and identity.
func (d *Dashboard) handleWhoAmI(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value("claims").(*api.Claims)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"role": "anonymous", "authenticated": false,
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"user_id":       claims.UserID,
		"address":       claims.Address,
		"role":          claims.Role,
		"authenticated": true,
	})
}

// extractToken extracts JWT token from request
func (d *Dashboard) extractToken(r *http.Request) string {
	// Try Authorization header first
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		parts := strings.Split(authHeader, " ")
		if len(parts) == 2 && parts[0] == "Bearer" {
			return parts[1]
		}
	}

	if t := d.tokenFromSessionCookie(r); t != "" {
		return t
	}

	// Legacy: small HMAC-only tokens may fit in a cookie (dev / non-CGO)
	cookie, err := r.Cookie("dashboard_token")
	if err == nil && cookie != nil {
		return cookie.Value
	}

	// Try query parameter (for API testing)
	return r.URL.Query().Get("token")
}

func (d *Dashboard) tokenFromSessionCookie(r *http.Request) string {
	c, err := r.Cookie("dashboard_session")
	if err != nil || c.Value == "" {
		return ""
	}
	now := time.Now().Unix()
	d.sessionMu.Lock()
	entry, ok := d.sessions[c.Value]
	if !ok {
		d.sessionMu.Unlock()
		return ""
	}
	if now >= entry.expires {
		delete(d.sessions, c.Value)
		d.sessionMu.Unlock()
		return ""
	}
	d.sessionMu.Unlock()
	return entry.token
}

func randomDashboardSessionID() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// handleEstablishSession stores a validated API access_token and sets a small HttpOnly cookie (ML-DSA JWTs are too large for document.cookie).
func (d *Dashboard) handleEstablishSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if d.authManager == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "Service Unavailable",
			"message": "Dashboard authentication is not available",
			"status":  http.StatusServiceUnavailable,
		})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<22) // 4 MiB — large ML-DSA JWT
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "Bad Request",
			"message": "invalid request body",
			"status":  http.StatusBadRequest,
		})
		return
	}
	if strings.TrimSpace(body.AccessToken) == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "Bad Request",
			"message": "access_token is required",
			"status":  http.StatusBadRequest,
		})
		return
	}

	if code, msg := d.establishSessionFromAccessToken(w, r, body.AccessToken); code != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   http.StatusText(code),
			"message": msg,
			"status":  code,
		})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d *Dashboard) establishSessionFromAccessToken(w http.ResponseWriter, r *http.Request, accessToken string) (int, string) {
	if d.authManager == nil {
		return http.StatusServiceUnavailable, "Dashboard authentication is not available"
	}
	claims, err := d.authManager.ValidateToken(accessToken)
	if err != nil {
		return http.StatusUnauthorized, "invalid or expired token"
	}
	if claims.Role != "admin" && claims.Role != "user" {
		return http.StatusForbidden, "Insufficient permissions"
	}
	sid, err := randomDashboardSessionID()
	if err != nil {
		return http.StatusInternalServerError, "failed to create session"
	}

	d.sessionMu.Lock()
	d.sessions[sid] = dashboardSessionEntry{token: accessToken, expires: claims.ExpiresAt}
	d.sessionMu.Unlock()

	maxAge := int(claims.ExpiresAt - time.Now().Unix())
	if maxAge < 60 {
		maxAge = 60
	}
	secure := r.TLS != nil
	http.SetCookie(w, &http.Cookie{
		Name:     "dashboard_session",
		Value:    sid,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	})
	log.Printf("Dashboard session cookie set (id prefix %.8s…, max_age=%ds)", sid, maxAge)
	return 0, ""
}

// handleLogin handles dashboard login
func (d *Dashboard) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req dashboardLoginRequest
	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query()
		req.Address = strings.TrimSpace(q.Get("address"))
		req.Password = q.Get("password")
		// Plain GET without credentials still serves the login page.
		if req.Address == "" && req.Password == "" {
			d.serveLoginPage(w, r)
			return
		}
	case http.MethodPost:
		ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
		if strings.Contains(ct, "application/json") {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "Invalid request body", http.StatusBadRequest)
				return
			}
		} else {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "Invalid form body", http.StatusBadRequest)
				return
			}
			req.Address = strings.TrimSpace(r.FormValue("address"))
			req.Password = r.FormValue("password")
		}
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	req.Address = strings.TrimSpace(req.Address)
	if req.Address == "" || req.Password == "" {
		if wantsJSON(r) || r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "Bad Request",
				"message": "Address and password are required",
				"status":  http.StatusBadRequest,
			})
			return
		}
		http.Redirect(w, r, "/api/auth/login?reason=credentials_in_url", http.StatusFound)
		return
	}

	accessToken, code, msg := d.loginThroughAPI(req.Address, req.Password)
	if code != http.StatusOK {
		if wantsJSON(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   http.StatusText(code),
				"message": msg,
				"status":  code,
			})
		} else {
			log.Printf("Dashboard login failed (%d): %s", code, msg)
			http.Redirect(w, r, "/api/auth/login?reason=bad_token", http.StatusFound)
		}
		return
	}

	if code, msg := d.establishSessionFromAccessToken(w, r, accessToken); code != 0 {
		if wantsJSON(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   http.StatusText(code),
				"message": msg,
				"status":  code,
			})
		} else {
			http.Redirect(w, r, "/api/auth/login?reason=bad_token", http.StatusFound)
		}
		return
	}

	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "login successful",
			"status":  http.StatusOK,
		})
		return
	}
	redirectTarget := "/"
	if red := strings.TrimSpace(r.URL.Query().Get("redirect")); red != "" {
		redirectTarget = red
	}
	http.Redirect(w, r, redirectTarget, http.StatusFound)
}

func (d *Dashboard) loginThroughAPI(address, password string) (string, int, string) {
	base := strings.TrimSpace(d.apiBackendURL)
	if base == "" {
		return "", http.StatusServiceUnavailable, "Dashboard is not configured with an API backend URL for login"
	}
	loginURL := strings.TrimRight(base, "/") + "/api/v1/auth/login"
	payload, err := json.Marshal(dashboardLoginRequest{
		Address:  address,
		Password: password,
	})
	if err != nil {
		return "", http.StatusInternalServerError, "failed to encode login payload"
	}
	req, err := http.NewRequest(http.MethodPost, loginURL, bytes.NewReader(payload))
	if err != nil {
		return "", http.StatusInternalServerError, "failed to create login request"
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return "", http.StatusBadGateway, "could not reach API login endpoint"
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var parsed map[string]interface{}
	_ = json.Unmarshal(raw, &parsed)
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(raw))
		if m, ok := parsed["message"].(string); ok && strings.TrimSpace(m) != "" {
			msg = m
		}
		if msg == "" {
			msg = "login failed"
		}
		return "", resp.StatusCode, msg
	}
	token, _ := parsed["access_token"].(string)
	token = strings.TrimSpace(token)
	if token == "" {
		return "", http.StatusBadGateway, "API login succeeded but did not return access_token"
	}
	return token, http.StatusOK, ""
}

// serveLoginPage serves a simple login page
func (d *Dashboard) serveLoginPage(w http.ResponseWriter, r *http.Request) {
	loginHTML := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
	<title>%s Dashboard Login</title>
	<link rel="stylesheet" href="/static/login.css?v=6">
</head>
<body>
	<h1>%s Dashboard Login</h1>
	<noscript><div class="info"><strong>JavaScript is disabled.</strong> Login still works via normal form POST.</div></noscript>
	<form id="loginForm" method="post" action="/api/auth/login">
		<div class="form-group">
			<label>Address:</label>
			<input type="text" name="address" autocomplete="username" required>
		</div>
		<div class="form-group">
			<label>Password:</label>
			<input type="password" name="password" autocomplete="current-password" required>
		</div>
		<button type="submit" id="loginSubmit">Login</button>
		<div id="status" class="status" aria-live="polite"></div>
		<div id="error" class="error" role="alert"></div>
	</form>
	<script src="/static/login.js?v=6"></script>
</body>
</html>`, branding.Name, branding.Name)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Write([]byte(loginHTML))
}

// handleMTLSGenerate creates a new CA + node certificate bundle and returns it as JSON.
func (d *Dashboard) handleMTLSGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		NodeID string   `json:"node_id"`
		Hosts  []string `json:"hosts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.NodeID == "" {
		req.NodeID = "qsdm-node"
	}
	if len(req.Hosts) == 0 {
		req.Hosts = []string{"localhost", "127.0.0.1"}
	}

	bundle, err := api.GenerateNodeBundle(req.NodeID, req.Hosts)
	if err != nil {
		http.Error(w, fmt.Sprintf("certificate generation failed: %v", err), http.StatusInternalServerError)
		return
	}

	resp := map[string]interface{}{
		"node_id":   req.NodeID,
		"hosts":     req.Hosts,
		"ca_cert":   string(bundle.CACertPEM),
		"node_cert": string(bundle.NodeCertPEM),
		"node_key":  string(bundle.NodeKeyPEM),
		"generated": time.Now().UTC().Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleMTLSStatus returns current mTLS configuration status.
func (d *Dashboard) handleMTLSStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := map[string]interface{}{
		"mtls_available": true,
		"features": []string{
			"CA generation (ECDSA P-256)",
			"Node certificate signing",
			"Mutual TLS handshake",
			"Auto-generation mode",
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleWS upgrades the connection to WebSocket for real-time push updates.
func (d *Dashboard) handleWS(w http.ResponseWriter, r *http.Request) {
	if d.wsHub == nil {
		http.Error(w, "WebSocket not available", http.StatusServiceUnavailable)
		return
	}
	d.wsHub.ServeWS(w, r)
}

// StartWSPush starts a goroutine that periodically pushes metrics/health to WS clients.
func (d *Dashboard) StartWSPush(interval time.Duration) {
	if d.wsHub == nil || interval <= 0 {
		return
	}

	d.wsMetricsPushMu.Lock()
	d.wsMetricsInterval = interval
	d.wsMetricsPushMu.Unlock()
	d.ensureMetricsPusher(interval)

	go func(iv time.Duration) {
		ticker := time.NewTicker(iv)
		defer ticker.Stop()
		for range ticker.C {
			d.ensureMetricsPusher(iv)
			if d.wsHub.ClientCount() == 0 {
				continue
			}
			// Legacy fallback when advanced source is not configured.
			if d.wsMetricsPusher == nil {
				d.wsHub.Broadcast("metrics", d.metrics.GetStats())
			}
			d.wsHub.Broadcast("health", d.healthChecker.GetHealthReport())
			if d.topologyMonitor != nil {
				d.wsHub.Broadcast("topology", d.topologyMonitor.GetTopology())
			}
		}
	}(interval)
}

// FormatDuration formats a duration in seconds to a human-readable string
func FormatDuration(seconds float64) string {
	d := time.Duration(seconds) * time.Second
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", seconds)
	}
	if d < time.Hour {
		return fmt.Sprintf("%.1fm", d.Minutes())
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%.1fh", d.Hours())
	}
	return fmt.Sprintf("%.1fd", d.Hours()/24)
}
