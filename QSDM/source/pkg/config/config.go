package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"

	"github.com/blackbeardONE/QSDM/pkg/envcompat"
)

// envPreferred is a thin local alias for envcompat.Lookup kept so the large
// number of call sites below read compactly. Callers OUTSIDE this package
// should import pkg/envcompat directly.
func envPreferred(preferred, legacy string) string {
	return envcompat.Lookup(preferred, legacy)
}

// Config holds the application configuration
type Config struct {
	// Node role (two-tier model introduced by the Major Update).
	// NodeRole is the canonical role (validator or miner). When unset at load
	// time it defaults to NodeRoleValidator. MiningEnabled must agree with
	// NodeRole at Validate() time: miner ⇒ true, validator ⇒ false.
	// Env overrides: QSDM_NODE_ROLE, QSDM_MINING_ENABLED.
	NodeRole      NodeRole
	MiningEnabled bool

	// Network
	NetworkPort      int
	BootstrapPeers   []string
	// SubmeshConfigPath: optional file (e.g. config/micropayments.toml) loaded into DynamicSubmeshManager at startup.
	SubmeshConfigPath string
	// ConfigFileUsed is the absolute path to the main config file when one was loaded; used to resolve relative submesh_config.
	ConfigFileUsed string
	
	// Storage
	StorageType      string // "sqlite", "scylla", or "file" (for non-CGO builds)
	SQLitePath       string
	// UserStorePath: path to the JSON file that persists the dashboard
	// user map (Argon2id password hashes). When empty the store is in-
	// memory only, which matches legacy test behaviour but is unsafe in
	// production: every service restart wipes every account (see the
	// 2026-04-23 wipe incident). In production this defaults to
	// <state_dir>/qsdm_users.json where state_dir = filepath.Dir(SQLitePath).
	// Env override: QSDM_USER_STORE_PATH / QSDM_USER_STORE_PATH.
	UserStorePath    string
	ScyllaHosts      []string
	ScyllaKeyspace   string
	ScyllaUsername   string
	ScyllaPassword   string
	ScyllaTLSCaPath  string
	ScyllaTLSCertPath string
	ScyllaTLSKeyPath  string
	ScyllaTLSInsecureSkipVerify bool

	// Monitoring
	DashboardPort    int
	LogViewerPort    int
	LogFile          string
	LogLevel         string
	// DashboardMetricsScrapeSecret: when set, GET /api/metrics/prometheus accepts this via Bearer or
	// X-QSDM-Metrics-Scrape-Secret header (legacy X-QSDM-Metrics-Scrape-Secret also accepted).
	// Env: prefer QSDM_DASHBOARD_METRICS_SCRAPE_SECRET; legacy QSDM_DASHBOARD_METRICS_SCRAPE_SECRET still works.
	DashboardMetricsScrapeSecret string
	// DashboardStrictAuth: when true ([monitoring] strict_dashboard_auth or QSDM_DASHBOARD_STRICT_AUTH
	// / legacy QSDM_DASHBOARD_STRICT_AUTH), JWT routes return 503 if auth manager failed to init;
	// Prometheus still works if metrics_scrape_secret is set.
	DashboardStrictAuth bool
	
	// API Server
	APIPort          int
	TLSCertFile      string
	TLSKeyFile       string
	EnableTLS        bool
	// ACME (Let's Encrypt) auto-provisioned TLS certificates.
	// Set ACMEDomains to enable; takes precedence over TLSCertFile/TLSKeyFile.
	ACMEDomains      []string
	ACMEEmail        string
	ACMECacheDir     string
	// Mutual TLS (mTLS) for node-to-node API authentication.
	MTLSCACertFile   string
	MTLSNodeCertFile string
	MTLSNodeKeyFile  string
	MTLSAutoGenerate bool // auto-generate CA + node cert if files don't exist
	// APIRateLimitMaxRequests: max HTTP requests per client per APIRateLimitWindow (default 100/min). Health routes are exempt.
	APIRateLimitMaxRequests int
	APIRateLimitWindow      time.Duration
	
	// Wallet
	InitialBalance   float64
	
	// Governance
	ProposalFile     string
	
	// Performance
	TransactionInterval time.Duration
	HealthCheckInterval time.Duration

	// NGC sidecar: shared secret for POST /api/v1/monitoring/ngc-proof.
	// Prefer env QSDM_NGC_INGEST_SECRET; QSDM_NGC_INGEST_SECRET is still accepted
	// (deprecated; to be removed after the Major Update deprecation window).
	NGCIngestSecret string

	// NVIDIALockEnabled: when true, state-changing ledger API routes require a recent NGC proof
	// with GPU attestation (gpu_fingerprint.available=true). Requires NGCIngestSecret to be set.
	NvidiaLockEnabled bool
	// NvidiaLockMaxProofAge: proofs older than this (from ingest time) do not satisfy the lock.
	NvidiaLockMaxProofAge time.Duration
	// NvidiaLockExpectedNodeID: when non-empty and nvidia_lock is on, ingested proofs must include
	// JSON string qsdm_node_id (or legacy qsdm_node_id) equal to this value (set
	// QSDM_NGC_PROOF_NODE_ID on the sidecar; QSDM_NGC_PROOF_NODE_ID is still accepted).
	NvidiaLockExpectedNodeID string
	// NvidiaLockProofHMACSecret: when non-empty and nvidia_lock is on, proofs must include a valid
	// qsdm_proof_hmac (or legacy qsdm_proof_hmac) HMAC-SHA256 field.
	NvidiaLockProofHMACSecret string
	// NvidiaLockRequireIngestNonce: proofs must include a fresh server-issued qsdm_ingest_nonce (or
	// legacy qsdm_ingest_nonce) from GET .../ngc-challenge; each nonce is single-use at ingest;
	// each ingested proof satisfies at most one lock check (proof consumed).
	NvidiaLockRequireIngestNonce bool
	// NvidiaLockIngestNonceTTL: lifetime for issued nonces (default 10m when require is on).
	NvidiaLockIngestNonceTTL time.Duration
	// NvidiaLockGateP2P: when true (with nvidia_lock), drop libp2p-received transactions unless a qualifying ingested proof exists (non-consuming check; opt-in).
	NvidiaLockGateP2P bool

	// JWTHMACSecret: HMAC key for JWT and request-signing fallback when Dilithium/CGO is unavailable (non-CGO builds).
	JWTHMACSecret string

	// AdminAPIRequireRole: when true, /api/admin/* requires JWT role "admin" (after AuthMiddleware).
	AdminAPIRequireRole bool
	// AdminAPIRequireMTLS: when true, /api/admin/* requires a verified TLS client certificate.
	AdminAPIRequireMTLS bool

	// AlertWebhookURL: optional webhook URL for the alerting subsystem (env QSDM_ALERT_WEBHOOK).
	AlertWebhookURL string

	// StrictProductionSecrets: when true ([api] strict_secrets or env QSDM_STRICT_SECRETS / legacy
	// QSDM_STRICT_SECRETS), reject short or demo-like NGC/JWT/HMAC secrets at startup.
	StrictProductionSecrets bool

	// ---- Trust / attestation transparency (Major Update §8.5) ----
	// These fields govern the optional /api/v1/trust/attestations/* surface and the
	// widgets that consume it. They do NOT influence consensus — see
	// docs/docs/NVIDIA_LOCK_CONSENSUS_SCOPE.md for the boundary.

	// TrustEndpointsDisabled: when true, the trust endpoints return 404 and the
	// aggregator is not wired. [trust] disabled / QSDM_TRUST_DISABLED.
	TrustEndpointsDisabled bool
	// TrustFreshWithin: how recent an NGC attestation must be to count as fresh
	// in the X/Y transparency ratio. Zero → 15 m. [trust] fresh_within /
	// QSDM_TRUST_FRESH_WITHIN.
	TrustFreshWithin time.Duration
	// TrustRefreshInterval: cadence of the background goroutine that rebuilds the
	// aggregator cache. Zero → 10 s. [trust] refresh_interval /
	// QSDM_TRUST_REFRESH_INTERVAL.
	TrustRefreshInterval time.Duration
	// TrustRegionHint: coarse region ("eu" / "us" / "apac" / "other" / "")
	// served with the local node's row. [trust] region_hint / QSDM_TRUST_REGION.
	TrustRegionHint string
}

// LoadConfig loads configuration from config file, environment variables, and defaults
// Priority: Environment variables > Config file > Defaults
func LoadConfig() (*Config, error) {
	// Load .env file if it exists
	_ = godotenv.Load()
	
	// Try to load from config file first
	cfg := &Config{}
	configFile := getEnvString("CONFIG_FILE", "")
	
	// If no config file specified, try common names. The post-rebrand qsdm.*
	// names are preferred; the legacy qsdmplus.* names are still probed so
	// existing deployments continue to work through the deprecation window.
	if configFile == "" {
		configFiles := []string{
			"qsdm.yaml", "qsdm.yml", "qsdm.toml",
			"qsdmplus.yaml", "qsdmplus.yml", "qsdmplus.toml",
		}
		for _, f := range configFiles {
			if _, err := os.Stat(f); err == nil {
				configFile = f
				break
			}
		}
	}
	
	// Check if config file exists
	if configFile != "" {
		if _, err := os.Stat(configFile); err == nil {
			if err := loadConfigFile(configFile, cfg); err != nil {
				return nil, fmt.Errorf("failed to load config file %s: %w", configFile, err)
			}
			if abs, err := filepath.Abs(configFile); err == nil {
				cfg.ConfigFileUsed = abs
			} else {
				cfg.ConfigFileUsed = filepath.Clean(configFile)
			}
		}
	}
	
	// Apply defaults for any unset values
	applyDefaults(cfg)
	
	// Override with environment variables (highest priority)
	applyEnvOverrides(cfg)
	
	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	
	return cfg, nil
}

// loadConfigFile loads configuration from TOML or YAML file
func loadConfigFile(path string, cfg *Config) error {
	ext := strings.ToLower(filepath.Ext(path))
	
	switch ext {
	case ".toml":
		var tomlCfg ConfigTOML
		if _, err := toml.DecodeFile(path, &tomlCfg); err != nil {
			return fmt.Errorf("failed to decode TOML: %w", err)
		}
		// Map TOML structure to Config
		if r, err := ParseNodeRole(tomlCfg.Node.Role); err == nil {
			cfg.NodeRole = r
		} else {
			return fmt.Errorf("invalid [node] role: %w", err)
		}
		cfg.MiningEnabled = tomlCfg.Node.MiningEnabled
		cfg.NetworkPort = tomlCfg.Network.Port
		cfg.BootstrapPeers = tomlCfg.Network.BootstrapPeers
		cfg.SubmeshConfigPath = strings.TrimSpace(tomlCfg.Network.SubmeshConfig)
		cfg.StorageType = tomlCfg.Storage.Type
		cfg.SQLitePath = tomlCfg.Storage.SQLitePath
		cfg.ScyllaHosts = tomlCfg.Storage.ScyllaHosts
		cfg.ScyllaKeyspace = tomlCfg.Storage.ScyllaKeyspace
		cfg.DashboardPort = tomlCfg.Monitoring.DashboardPort
		cfg.LogViewerPort = tomlCfg.Monitoring.LogViewerPort
		cfg.LogFile = tomlCfg.Monitoring.LogFile
		cfg.LogLevel = tomlCfg.Monitoring.LogLevel
		cfg.DashboardMetricsScrapeSecret = tomlCfg.Monitoring.MetricsScrapeSecret
		cfg.DashboardStrictAuth = tomlCfg.Monitoring.StrictDashboardAuth
		cfg.APIPort = tomlCfg.API.Port
		cfg.EnableTLS = tomlCfg.API.EnableTLS
		cfg.TLSCertFile = tomlCfg.API.TLSCertFile
		cfg.TLSKeyFile = tomlCfg.API.TLSKeyFile
		cfg.NvidiaLockEnabled = tomlCfg.API.NvidiaLock
		if tomlCfg.API.NvidiaLockMaxProofAge != "" {
			if d, err := time.ParseDuration(tomlCfg.API.NvidiaLockMaxProofAge); err == nil {
				cfg.NvidiaLockMaxProofAge = d
			}
		}
		cfg.NvidiaLockExpectedNodeID = tomlCfg.API.NvidiaLockExpectedNodeID
		cfg.NvidiaLockProofHMACSecret = tomlCfg.API.NvidiaLockProofHMACSecret
		cfg.NvidiaLockRequireIngestNonce = tomlCfg.API.NvidiaLockRequireIngestNonce
		cfg.NvidiaLockGateP2P = tomlCfg.API.NvidiaLockGateP2P
		if tomlCfg.API.NvidiaLockIngestNonceTTL != "" {
			if d, err := time.ParseDuration(tomlCfg.API.NvidiaLockIngestNonceTTL); err == nil {
				cfg.NvidiaLockIngestNonceTTL = d
			}
		}
		cfg.JWTHMACSecret = tomlCfg.API.JWTHMACSecret
		cfg.StrictProductionSecrets = tomlCfg.API.StrictProductionSecrets
		if tomlCfg.API.RateLimitMaxRequests > 0 {
			cfg.APIRateLimitMaxRequests = tomlCfg.API.RateLimitMaxRequests
		}
		if tomlCfg.API.RateLimitWindow != "" {
			if d, err := time.ParseDuration(tomlCfg.API.RateLimitWindow); err == nil {
				cfg.APIRateLimitWindow = d
			}
		}
		cfg.InitialBalance = tomlCfg.Wallet.InitialBalance
		cfg.ProposalFile = tomlCfg.Governance.ProposalFile
		if tomlCfg.Performance.TransactionInterval != "" {
			if d, err := time.ParseDuration(tomlCfg.Performance.TransactionInterval); err == nil {
				cfg.TransactionInterval = d
			}
		}
		if tomlCfg.Performance.HealthCheckInterval != "" {
			if d, err := time.ParseDuration(tomlCfg.Performance.HealthCheckInterval); err == nil {
				cfg.HealthCheckInterval = d
			}
		}
		cfg.TrustEndpointsDisabled = tomlCfg.Trust.Disabled
		if tomlCfg.Trust.FreshWithin != "" {
			if d, err := time.ParseDuration(tomlCfg.Trust.FreshWithin); err == nil {
				cfg.TrustFreshWithin = d
			}
		}
		if tomlCfg.Trust.RefreshInterval != "" {
			if d, err := time.ParseDuration(tomlCfg.Trust.RefreshInterval); err == nil {
				cfg.TrustRefreshInterval = d
			}
		}
		cfg.TrustRegionHint = strings.TrimSpace(tomlCfg.Trust.RegionHint)
	case ".yaml", ".yml":
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read YAML file: %w", err)
		}
		var yamlCfg ConfigTOML
		if err := yaml.Unmarshal(data, &yamlCfg); err != nil {
			return fmt.Errorf("failed to decode YAML: %w", err)
		}
		// Map YAML structure to Config (same as TOML)
		if r, err := ParseNodeRole(yamlCfg.Node.Role); err == nil {
			cfg.NodeRole = r
		} else {
			return fmt.Errorf("invalid node.role: %w", err)
		}
		cfg.MiningEnabled = yamlCfg.Node.MiningEnabled
		cfg.NetworkPort = yamlCfg.Network.Port
		cfg.BootstrapPeers = yamlCfg.Network.BootstrapPeers
		cfg.SubmeshConfigPath = strings.TrimSpace(yamlCfg.Network.SubmeshConfig)
		cfg.StorageType = yamlCfg.Storage.Type
		cfg.SQLitePath = yamlCfg.Storage.SQLitePath
		cfg.ScyllaHosts = yamlCfg.Storage.ScyllaHosts
		cfg.ScyllaKeyspace = yamlCfg.Storage.ScyllaKeyspace
		cfg.ScyllaUsername = yamlCfg.Storage.ScyllaUsername
		cfg.ScyllaPassword = yamlCfg.Storage.ScyllaPassword
		cfg.ScyllaTLSCaPath = yamlCfg.Storage.ScyllaTLSCaPath
		cfg.ScyllaTLSCertPath = yamlCfg.Storage.ScyllaTLSCertPath
		cfg.ScyllaTLSKeyPath = yamlCfg.Storage.ScyllaTLSKeyPath
		cfg.ScyllaTLSInsecureSkipVerify = yamlCfg.Storage.ScyllaTLSInsecureSkipVerify
		cfg.DashboardPort = yamlCfg.Monitoring.DashboardPort
		cfg.LogViewerPort = yamlCfg.Monitoring.LogViewerPort
		cfg.LogFile = yamlCfg.Monitoring.LogFile
		cfg.LogLevel = yamlCfg.Monitoring.LogLevel
		cfg.DashboardMetricsScrapeSecret = yamlCfg.Monitoring.MetricsScrapeSecret
		cfg.DashboardStrictAuth = yamlCfg.Monitoring.StrictDashboardAuth
		cfg.APIPort = yamlCfg.API.Port
		cfg.EnableTLS = yamlCfg.API.EnableTLS
		cfg.TLSCertFile = yamlCfg.API.TLSCertFile
		cfg.TLSKeyFile = yamlCfg.API.TLSKeyFile
		cfg.NvidiaLockEnabled = yamlCfg.API.NvidiaLock
		if yamlCfg.API.NvidiaLockMaxProofAge != "" {
			if d, err := time.ParseDuration(yamlCfg.API.NvidiaLockMaxProofAge); err == nil {
				cfg.NvidiaLockMaxProofAge = d
			}
		}
		cfg.NvidiaLockExpectedNodeID = yamlCfg.API.NvidiaLockExpectedNodeID
		cfg.NvidiaLockProofHMACSecret = yamlCfg.API.NvidiaLockProofHMACSecret
		cfg.NvidiaLockRequireIngestNonce = yamlCfg.API.NvidiaLockRequireIngestNonce
		cfg.NvidiaLockGateP2P = yamlCfg.API.NvidiaLockGateP2P
		if yamlCfg.API.NvidiaLockIngestNonceTTL != "" {
			if d, err := time.ParseDuration(yamlCfg.API.NvidiaLockIngestNonceTTL); err == nil {
				cfg.NvidiaLockIngestNonceTTL = d
			}
		}
		cfg.JWTHMACSecret = yamlCfg.API.JWTHMACSecret
		cfg.StrictProductionSecrets = yamlCfg.API.StrictProductionSecrets
		if yamlCfg.API.RateLimitMaxRequests > 0 {
			cfg.APIRateLimitMaxRequests = yamlCfg.API.RateLimitMaxRequests
		}
		if yamlCfg.API.RateLimitWindow != "" {
			if d, err := time.ParseDuration(yamlCfg.API.RateLimitWindow); err == nil {
				cfg.APIRateLimitWindow = d
			}
		}
		cfg.InitialBalance = yamlCfg.Wallet.InitialBalance
		cfg.ProposalFile = yamlCfg.Governance.ProposalFile
		if yamlCfg.Performance.TransactionInterval != "" {
			if d, err := time.ParseDuration(yamlCfg.Performance.TransactionInterval); err == nil {
				cfg.TransactionInterval = d
			}
		}
		cfg.TrustEndpointsDisabled = yamlCfg.Trust.Disabled
		if yamlCfg.Trust.FreshWithin != "" {
			if d, err := time.ParseDuration(yamlCfg.Trust.FreshWithin); err == nil {
				cfg.TrustFreshWithin = d
			}
		}
		if yamlCfg.Trust.RefreshInterval != "" {
			if d, err := time.ParseDuration(yamlCfg.Trust.RefreshInterval); err == nil {
				cfg.TrustRefreshInterval = d
			}
		}
		cfg.TrustRegionHint = strings.TrimSpace(yamlCfg.Trust.RegionHint)
		if yamlCfg.Performance.HealthCheckInterval != "" {
			if d, err := time.ParseDuration(yamlCfg.Performance.HealthCheckInterval); err == nil {
				cfg.HealthCheckInterval = d
			}
		}
	default:
		return fmt.Errorf("unsupported config file format: %s (supported: .toml, .yaml, .yml)", ext)
	}
	
	return nil
}

// applyDefaults sets default values for unset configuration fields
func applyDefaults(cfg *Config) {
	if cfg.NodeRole == "" {
		cfg.NodeRole = NodeRoleValidator
	}
	if cfg.NetworkPort == 0 {
		cfg.NetworkPort = 4001
	}
	if cfg.BootstrapPeers == nil {
		cfg.BootstrapPeers = []string{}
	}
	if cfg.StorageType == "" {
		cfg.StorageType = "sqlite"
	}
	if cfg.SQLitePath == "" {
		// Default filename: prefer qsdm.db for new deployments, but if a pre-rebrand
		// qsdmplus.db exists in the CWD and no qsdm.db exists yet, continue to use it
		// so existing operators aren't silently switched to a fresh empty database.
		if _, err := os.Stat("qsdm.db"); err == nil {
			cfg.SQLitePath = "qsdm.db"
		} else if _, err := os.Stat("qsdmplus.db"); err == nil {
			cfg.SQLitePath = "qsdmplus.db"
		} else {
			cfg.SQLitePath = "qsdm.db"
		}
	}
	if cfg.ScyllaHosts == nil {
		cfg.ScyllaHosts = []string{"127.0.0.1"}
	}
	if cfg.ScyllaKeyspace == "" {
		cfg.ScyllaKeyspace = "qsdm"
	}
	if cfg.DashboardPort == 0 {
		cfg.DashboardPort = 8081
	}
	if cfg.LogViewerPort == 0 {
		// Default below APIPort so both can run without binding the same port (Dockerfile EXPOSE 9000).
		cfg.LogViewerPort = 9000
	}
	if cfg.LogFile == "" {
		// As with SQLitePath, default to qsdm.log but keep appending to a pre-existing
		// qsdmplus.log if that's where operators have historical events already.
		if _, err := os.Stat("qsdm.log"); err == nil {
			cfg.LogFile = "qsdm.log"
		} else if _, err := os.Stat("qsdmplus.log"); err == nil {
			cfg.LogFile = "qsdmplus.log"
		} else {
			cfg.LogFile = "qsdm.log"
		}
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "INFO"
	}
	if cfg.APIPort == 0 {
		cfg.APIPort = 8080 // Default API HTTP port for development
	}
	if cfg.TLSCertFile == "" {
		cfg.TLSCertFile = ""
	}
	if cfg.TLSKeyFile == "" {
		cfg.TLSKeyFile = ""
	}
	// TLS defaults to false (HTTP) for easier development
	// Set enable_tls: true in config or ENABLE_TLS=true env var to enable HTTPS
	if cfg.EnableTLS {
		// Keep TLS enabled if explicitly set
	} else {
		cfg.EnableTLS = false  // Default to HTTP
	}
	if cfg.InitialBalance == 0 {
		cfg.InitialBalance = 1000.0
	}
	if cfg.ProposalFile == "" {
		cfg.ProposalFile = "proposals.json"
	}
	if cfg.TransactionInterval == 0 {
		cfg.TransactionInterval = 30 * time.Second
	}
	if cfg.HealthCheckInterval == 0 {
		cfg.HealthCheckInterval = 30 * time.Second
	}
	if cfg.NvidiaLockMaxProofAge == 0 {
		cfg.NvidiaLockMaxProofAge = 15 * time.Minute
	}
	if cfg.NvidiaLockRequireIngestNonce && cfg.NvidiaLockIngestNonceTTL == 0 {
		cfg.NvidiaLockIngestNonceTTL = 10 * time.Minute
	}
	if cfg.APIRateLimitMaxRequests <= 0 {
		cfg.APIRateLimitMaxRequests = 100
	}
	if cfg.APIRateLimitWindow <= 0 {
		cfg.APIRateLimitWindow = time.Minute
	}
	if cfg.TrustFreshWithin <= 0 {
		cfg.TrustFreshWithin = 15 * time.Minute
	}
	if cfg.TrustRefreshInterval <= 0 {
		cfg.TrustRefreshInterval = 10 * time.Second
	}
}

// applyEnvOverrides applies environment variable overrides (highest priority)
func applyEnvOverrides(cfg *Config) {
	if val := strings.TrimSpace(envPreferred("QSDM_NODE_ROLE", "QSDM_NODE_ROLE")); val != "" {
		if r, err := ParseNodeRole(val); err == nil {
			cfg.NodeRole = r
		}
	}
	if val := strings.TrimSpace(envPreferred("QSDM_MINING_ENABLED", "QSDM_MINING_ENABLED")); val != "" {
		cfg.MiningEnabled = envcompat.Truthy("QSDM_MINING_ENABLED", "QSDM_MINING_ENABLED")
	}
	if val := getEnvString("NETWORK_PORT", ""); val != "" {
		cfg.NetworkPort = getEnvInt("NETWORK_PORT", cfg.NetworkPort)
	}
	if val := getEnvString("BOOTSTRAP_PEERS", ""); val != "" {
		cfg.BootstrapPeers = getEnvStringSlice("BOOTSTRAP_PEERS", cfg.BootstrapPeers)
	}
	if val := getEnvString("STORAGE_TYPE", ""); val != "" {
		cfg.StorageType = val
	}
	if val := getEnvString("SQLITE_PATH", ""); val != "" {
		cfg.SQLitePath = val
	}
	if val := strings.TrimSpace(envPreferred("QSDM_USER_STORE_PATH", "QSDM_USER_STORE_PATH")); val != "" {
		cfg.UserStorePath = val
	}
	if val := getEnvString("SCYLLA_HOSTS", ""); val != "" {
		cfg.ScyllaHosts = getEnvStringSlice("SCYLLA_HOSTS", cfg.ScyllaHosts)
	}
	if val := getEnvString("SCYLLA_KEYSPACE", ""); val != "" {
		cfg.ScyllaKeyspace = val
	}
	if val := getEnvString("SCYLLA_USERNAME", ""); val != "" {
		cfg.ScyllaUsername = val
	}
	if val := getEnvString("SCYLLA_PASSWORD", ""); val != "" {
		cfg.ScyllaPassword = val
	}
	if val := getEnvString("SCYLLA_TLS_CA_PATH", ""); val != "" {
		cfg.ScyllaTLSCaPath = val
	}
	if val := getEnvString("SCYLLA_TLS_CERT_PATH", ""); val != "" {
		cfg.ScyllaTLSCertPath = val
	}
	if val := getEnvString("SCYLLA_TLS_KEY_PATH", ""); val != "" {
		cfg.ScyllaTLSKeyPath = val
	}
	if val := getEnvString("SCYLLA_TLS_INSECURE_SKIP_VERIFY", ""); val != "" {
		cfg.ScyllaTLSInsecureSkipVerify = val == "true" || val == "1" || strings.EqualFold(val, "yes")
	}
	if val := getEnvString("DASHBOARD_PORT", ""); val != "" {
		cfg.DashboardPort = getEnvInt("DASHBOARD_PORT", cfg.DashboardPort)
	}
	if val := getEnvString("LOG_VIEWER_PORT", ""); val != "" {
		cfg.LogViewerPort = getEnvInt("LOG_VIEWER_PORT", cfg.LogViewerPort)
	}
	if val := getEnvString("LOG_FILE", ""); val != "" {
		cfg.LogFile = val
	}
	if val := getEnvString("LOG_LEVEL", ""); val != "" {
		cfg.LogLevel = val
	}
	if val := envPreferred("QSDM_DASHBOARD_METRICS_SCRAPE_SECRET", "QSDM_DASHBOARD_METRICS_SCRAPE_SECRET"); val != "" {
		cfg.DashboardMetricsScrapeSecret = val
	}
	if val := envPreferred("QSDM_DASHBOARD_STRICT_AUTH", "QSDM_DASHBOARD_STRICT_AUTH"); val != "" {
		cfg.DashboardStrictAuth = val == "true" || val == "1" || strings.EqualFold(val, "yes")
	}
	if val := envPreferred("QSDM_ADMIN_REQUIRE_ROLE", "QSDM_ADMIN_REQUIRE_ROLE"); val != "" {
		cfg.AdminAPIRequireRole = val == "true" || val == "1" || strings.EqualFold(val, "yes")
	}
	if val := envPreferred("QSDM_ADMIN_REQUIRE_MTLS", "QSDM_ADMIN_REQUIRE_MTLS"); val != "" {
		cfg.AdminAPIRequireMTLS = val == "true" || val == "1" || strings.EqualFold(val, "yes")
	}
	if val := strings.TrimSpace(envPreferred("QSDM_SUBMESH_CONFIG", "QSDM_SUBMESH_CONFIG")); val != "" {
		cfg.SubmeshConfigPath = val
	}
	if val := getEnvString("API_PORT", ""); val != "" {
		cfg.APIPort = getEnvInt("API_PORT", cfg.APIPort)
	}
	if val := envPreferred("QSDM_API_RATE_LIMIT_MAX", "QSDM_API_RATE_LIMIT_MAX"); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			cfg.APIRateLimitMaxRequests = n
		}
	} else if val := getEnvString("API_RATE_LIMIT_MAX", ""); val != "" {
		cfg.APIRateLimitMaxRequests = getEnvInt("API_RATE_LIMIT_MAX", cfg.APIRateLimitMaxRequests)
	}
	if val := envPreferred("QSDM_API_RATE_LIMIT_WINDOW", "QSDM_API_RATE_LIMIT_WINDOW"); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			cfg.APIRateLimitWindow = d
		}
	} else if val := getEnvString("API_RATE_LIMIT_WINDOW", ""); val != "" {
		cfg.APIRateLimitWindow = getEnvDuration("API_RATE_LIMIT_WINDOW", cfg.APIRateLimitWindow)
	}
	if val := getEnvString("TLS_CERT_FILE", ""); val != "" {
		cfg.TLSCertFile = val
	}
	if val := getEnvString("TLS_KEY_FILE", ""); val != "" {
		cfg.TLSKeyFile = val
	}
	if val := getEnvString("ENABLE_TLS", ""); val != "" {
		cfg.EnableTLS = val == "true"
	}
	if val := getEnvString("ACME_DOMAINS", ""); val != "" {
		parts := strings.Split(val, ",")
		for _, p := range parts {
			if d := strings.TrimSpace(p); d != "" {
				cfg.ACMEDomains = append(cfg.ACMEDomains, d)
			}
		}
	}
	if val := getEnvString("ACME_EMAIL", ""); val != "" {
		cfg.ACMEEmail = val
	}
	if val := getEnvString("ACME_CACHE_DIR", ""); val != "" {
		cfg.ACMECacheDir = val
	}
	if val := getEnvString("MTLS_CA_CERT", ""); val != "" {
		cfg.MTLSCACertFile = val
	}
	if val := getEnvString("MTLS_NODE_CERT", ""); val != "" {
		cfg.MTLSNodeCertFile = val
	}
	if val := getEnvString("MTLS_NODE_KEY", ""); val != "" {
		cfg.MTLSNodeKeyFile = val
	}
	if val := getEnvString("MTLS_AUTO_GENERATE", ""); val == "true" {
		cfg.MTLSAutoGenerate = true
	}
	if val := getEnvString("INITIAL_BALANCE", ""); val != "" {
		cfg.InitialBalance = getEnvFloat("INITIAL_BALANCE", cfg.InitialBalance)
	}
	if val := getEnvString("PROPOSAL_FILE", ""); val != "" {
		cfg.ProposalFile = val
	}
	if val := getEnvString("TRANSACTION_INTERVAL", ""); val != "" {
		cfg.TransactionInterval = getEnvDuration("TRANSACTION_INTERVAL", cfg.TransactionInterval)
	}
	if val := getEnvString("HEALTH_CHECK_INTERVAL", ""); val != "" {
		cfg.HealthCheckInterval = getEnvDuration("HEALTH_CHECK_INTERVAL", cfg.HealthCheckInterval)
	}
	if val := envPreferred("QSDM_NGC_INGEST_SECRET", "QSDM_NGC_INGEST_SECRET"); val != "" {
		cfg.NGCIngestSecret = val
	}
	if val := envPreferred("QSDM_NVIDIA_LOCK", "QSDM_NVIDIA_LOCK"); val != "" {
		cfg.NvidiaLockEnabled = val == "true" || val == "1"
	}
	if val := envPreferred("QSDM_NVIDIA_LOCK_MAX_PROOF_AGE", "QSDM_NVIDIA_LOCK_MAX_PROOF_AGE"); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			cfg.NvidiaLockMaxProofAge = d
		}
	}
	if val := envPreferred("QSDM_NVIDIA_LOCK_EXPECTED_NODE_ID", "QSDM_NVIDIA_LOCK_EXPECTED_NODE_ID"); val != "" {
		cfg.NvidiaLockExpectedNodeID = val
	}
	if val := envPreferred("QSDM_NVIDIA_LOCK_PROOF_HMAC_SECRET", "QSDM_NVIDIA_LOCK_PROOF_HMAC_SECRET"); val != "" {
		cfg.NvidiaLockProofHMACSecret = val
	}
	if val := envPreferred("QSDM_JWT_HMAC_SECRET", "QSDM_JWT_HMAC_SECRET"); val != "" {
		cfg.JWTHMACSecret = val
	}
	if val := envPreferred("QSDM_NVIDIA_LOCK_REQUIRE_INGEST_NONCE", "QSDM_NVIDIA_LOCK_REQUIRE_INGEST_NONCE"); val != "" {
		cfg.NvidiaLockRequireIngestNonce = val == "true" || val == "1"
	}
	if val := envPreferred("QSDM_NVIDIA_LOCK_INGEST_NONCE_TTL", "QSDM_NVIDIA_LOCK_INGEST_NONCE_TTL"); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			cfg.NvidiaLockIngestNonceTTL = d
		}
	}
	if val := envPreferred("QSDM_NVIDIA_LOCK_GATE_P2P", "QSDM_NVIDIA_LOCK_GATE_P2P"); val != "" {
		cfg.NvidiaLockGateP2P = val == "true" || val == "1"
	}
	if val := envPreferred("QSDM_STRICT_SECRETS", "QSDM_STRICT_SECRETS"); val != "" {
		cfg.StrictProductionSecrets = val == "true" || val == "1" || strings.EqualFold(val, "yes")
	}
	if val := getEnvString("QSDM_ALERT_WEBHOOK", ""); val != "" {
		cfg.AlertWebhookURL = val
	}

	if val := envPreferred("QSDM_TRUST_DISABLED", "QSDM_TRUST_DISABLED"); val != "" {
		cfg.TrustEndpointsDisabled = val == "true" || val == "1" || strings.EqualFold(val, "yes")
	}
	if val := envPreferred("QSDM_TRUST_FRESH_WITHIN", "QSDM_TRUST_FRESH_WITHIN"); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			cfg.TrustFreshWithin = d
		}
	}
	if val := envPreferred("QSDM_TRUST_REFRESH_INTERVAL", "QSDM_TRUST_REFRESH_INTERVAL"); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			cfg.TrustRefreshInterval = d
		}
	}
	if val := envPreferred("QSDM_TRUST_REGION", "QSDM_TRUST_REGION"); val != "" {
		cfg.TrustRegionHint = strings.TrimSpace(val)
	}
}

// ResolvedSubmeshConfigPath returns the filesystem path to the optional submesh profile (relative paths are resolved against the main config file directory when known).
func (c *Config) ResolvedSubmeshConfigPath() string {
	p := strings.TrimSpace(c.SubmeshConfigPath)
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	if c.ConfigFileUsed != "" {
		return filepath.Join(filepath.Dir(c.ConfigFileUsed), p)
	}
	return filepath.Clean(p)
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.NodeRole == "" {
		c.NodeRole = NodeRoleValidator
	}
	if !c.NodeRole.IsValid() {
		return fmt.Errorf("invalid node.role: %q (valid: %q, %q; env QSDM_NODE_ROLE)", c.NodeRole, NodeRoleValidator, NodeRoleMiner)
	}
	if c.NodeRole.IsValidator() && c.MiningEnabled {
		return fmt.Errorf("node.role=%q but mining_enabled=true: validators must not mine. Set node.role=%q (env QSDM_NODE_ROLE=miner) to run as a miner, or clear mining_enabled", c.NodeRole, NodeRoleMiner)
	}
	if c.NodeRole.IsMiner() && !c.MiningEnabled {
		return fmt.Errorf("node.role=%q but mining_enabled=false: miner role requires mining_enabled=true (env QSDM_MINING_ENABLED=true)", c.NodeRole)
	}

	if c.NetworkPort < 1 || c.NetworkPort > 65535 {
		return fmt.Errorf("invalid network port: %d", c.NetworkPort)
	}
	
	if c.DashboardPort < 1 || c.DashboardPort > 65535 {
		return fmt.Errorf("invalid dashboard port: %d", c.DashboardPort)
	}
	
	if c.LogViewerPort < 1 || c.LogViewerPort > 65535 {
		return fmt.Errorf("invalid log viewer port: %d", c.LogViewerPort)
	}
	
	if c.APIPort < 1 || c.APIPort > 65535 {
		return fmt.Errorf("invalid API port: %d", c.APIPort)
	}

	mr := c.APIRateLimitMaxRequests
	if mr <= 0 {
		mr = 100
	}
	rw := c.APIRateLimitWindow
	if rw <= 0 {
		rw = time.Minute
	}
	if mr < 1 || mr > 10_000_000 {
		return fmt.Errorf("invalid API rate_limit_max_requests: %d (allowed 1..10000000)", c.APIRateLimitMaxRequests)
	}
	if rw < time.Second || rw > 24*time.Hour {
		return fmt.Errorf("invalid API rate_limit_window: %v (allowed 1s..24h)", c.APIRateLimitWindow)
	}
	
	if c.StorageType != "sqlite" && c.StorageType != "scylla" && c.StorageType != "file" {
		return fmt.Errorf("invalid storage type: %s (must be 'sqlite', 'scylla', or 'file')", c.StorageType)
	}
	
	if c.InitialBalance < 0 {
		return fmt.Errorf("initial balance cannot be negative: %f", c.InitialBalance)
	}

	if rp := c.ResolvedSubmeshConfigPath(); rp != "" {
		if _, err := os.Stat(rp); err != nil {
			return fmt.Errorf("submesh_config: cannot access %q: %w", rp, err)
		}
	}

	if c.NvidiaLockEnabled {
		if c.NGCIngestSecret == "" {
			return fmt.Errorf("nvidia_lock is enabled but NGC ingest secret is empty; set QSDM_NGC_INGEST_SECRET (legacy QSDM_NGC_INGEST_SECRET still accepted) so the node can receive GPU proof bundles")
		}
		if c.NvidiaLockMaxProofAge <= 0 {
			return fmt.Errorf("nvidia_lock_max_proof_age must be positive")
		}
	}
	if strings.TrimSpace(c.NvidiaLockProofHMACSecret) != "" && !c.NvidiaLockEnabled {
		return fmt.Errorf("nvidia_lock_proof_hmac_secret is set but nvidia_lock is disabled; enable nvidia_lock or clear the HMAC secret")
	}
	if c.NvidiaLockGateP2P && !c.NvidiaLockEnabled {
		return fmt.Errorf("nvidia_lock_gate_p2p requires nvidia_lock enabled")
	}
	if c.NvidiaLockRequireIngestNonce {
		if !c.NvidiaLockEnabled {
			return fmt.Errorf("nvidia_lock_require_ingest_nonce requires nvidia_lock enabled")
		}
		if strings.TrimSpace(c.NvidiaLockProofHMACSecret) == "" {
			return fmt.Errorf("nvidia_lock_require_ingest_nonce requires nvidia_lock_proof_hmac_secret (HMAC v2 binds the nonce into the bundle)")
		}
		if c.NvidiaLockIngestNonceTTL <= 0 {
			return fmt.Errorf("nvidia_lock_ingest_nonce_ttl must be positive when ingest nonce is required")
		}
	}

	if c.StrictProductionSecrets {
		if err := validateProductionSecret("QSDM_NGC_INGEST_SECRET", c.NGCIngestSecret); err != nil {
			return err
		}
		if err := validateProductionSecret("nvidia_lock_proof_hmac_secret", c.NvidiaLockProofHMACSecret); err != nil {
			return err
		}
		if err := validateProductionSecret("QSDM_JWT_HMAC_SECRET", c.JWTHMACSecret); err != nil {
			return err
		}
		if err := validateProductionSecret("QSDM_DASHBOARD_METRICS_SCRAPE_SECRET", c.DashboardMetricsScrapeSecret); err != nil {
			return err
		}
	}

	return nil
}

func validateProductionSecret(label, value string) error {
	s := strings.TrimSpace(value)
	if s == "" {
		return nil
	}
	if len(s) < 16 {
		return fmt.Errorf("%s: when QSDM_STRICT_SECRETS is enabled, secrets must be at least 16 characters", label)
	}
	lower := strings.ToLower(s)
	// Block obvious demo placeholder even if padded to pass length.
	if strings.HasPrefix(lower, "charming123") {
		return fmt.Errorf("%s: when QSDM_STRICT_SECRETS is enabled, secret appears weak or demo-like", label)
	}
	return nil
}

// UseScylla returns true if ScyllaDB should be used
func (c *Config) UseScylla() bool {
	return c.StorageType == "scylla" || os.Getenv("USE_SCYLLA") == "true"
}

// Helper functions

func getEnvString(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if floatValue, err := strconv.ParseFloat(value, 64); err == nil {
			return floatValue
		}
	}
	return defaultValue
}

func getEnvStringSlice(key string, defaultValue []string) []string {
	if value := os.Getenv(key); value != "" {
		// Simple comma-separated parsing
		parts := []string{}
		for _, part := range splitString(value, ",") {
			if trimmed := trimString(part); trimmed != "" {
				parts = append(parts, trimmed)
			}
		}
		if len(parts) > 0 {
			return parts
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}

// Simple string helpers (avoid importing strings package for minimal dependencies)
func splitString(s, sep string) []string {
	parts := []string{}
	current := ""
	for _, char := range s {
		if string(char) == sep {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else {
			current += string(char)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

func trimString(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

