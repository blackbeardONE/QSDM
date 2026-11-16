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
	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
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
	// RequireMiningOperatorSignatures makes validators reject nvidia-hmac-v1
	// proofs unless they carry operator_sig signed by the retained enrollment
	// ML-DSA public key. Default false for staged miner rollout.
	// TOML/YAML: [node] require_mining_operator_signatures; env:
	// QSDM_REQUIRE_MINING_OPERATOR_SIGNATURES.
	RequireMiningOperatorSignatures bool
	// MiningOperatorPublicKeyRetentionHeight is the first block height where
	// signed mining enrollments retain their operator ML-DSA public key for
	// later proof verification. Zero keeps retention disabled.
	// TOML/YAML: [node] mining_operator_public_key_retention_height; env:
	// QSDM_MINING_OPERATOR_PUBLIC_KEY_RETENTION_HEIGHT.
	MiningOperatorPublicKeyRetentionHeight uint64

	// Network
	NetworkPort        int
	NetworkBindAddress string
	BootstrapPeers     []string
	// NetworkHostKeyPath: optional file persisting the libp2p host
	// PrivateKey across restarts so peer.ID is stable. Empty (default)
	// = ephemeral key, generated fresh every restart (acceptable for
	// dev/CI; on production this causes pre-restart trust attestation
	// rows to age out of the freshness window because node_id flips).
	// Env override: QSDM_NETWORK_HOST_KEY_PATH; TOML:
	// [network] host_key_path; YAML: network.host_key_path. File
	// format: single line of base64(proto.Marshal(libp2p PrivKey)),
	// mode 0600; see pkg/networking/hostkey.go.
	NetworkHostKeyPath string
	// P2PLimits bounds libp2p connections/streams and GossipSub traffic.
	// Zero fields use the defaults in pkg/networking/p2p_limits.go.
	// TOML/YAML: keys under [network] (see p2p_limits.go); env: QSDM_P2P_*.
	P2PLimits P2PLimitsConfig
	// BridgeRelay authenticates qsdm-bridge gossip events. With no trusted
	// keys (the default) remote bridge events are never applied.
	// TOML/YAML: bridge_relay_* under [network]; env: QSDM_BRIDGE_RELAY_*.
	BridgeRelay BridgeRelayConfig
	// PrivateP2P restricts libp2p to a private-network bind and a fixed list
	// of peer IDs. Off by default: no change.
	// TOML/YAML: private_p2p, private_p2p_allowed_peers under [network];
	// env: QSDM_P2P_PRIVATE, QSDM_P2P_PRIVATE_ALLOWED_PEERS.
	PrivateP2P PrivateP2PConfig
	// SnapshotBootstrap pins a trusted checkpoint for the verified snapshot
	// bootstrap (snapshot_bootstrap.go). Off by default: no change.
	// TOML/YAML: [snapshot_bootstrap]; env: QSDM_SNAPSHOT_BOOTSTRAP,
	// QSDM_SNAPSHOT_CHECKPOINT_*.
	SnapshotBootstrap SnapshotBootstrapConfig
	// SubmeshConfigPath: optional file (e.g. config/micropayments.toml) loaded into DynamicSubmeshManager at startup.
	SubmeshConfigPath string
	// ConfigFileUsed is the absolute path to the main config file when one was loaded; used to resolve relative submesh_config.
	ConfigFileUsed string

	// Storage
	StorageType string // "sqlite", "scylla", or "file" (for non-CGO builds)
	SQLitePath  string
	// UserStorePath: path to the JSON file that persists the dashboard
	// user map (Argon2id password hashes). When empty the store is in-
	// memory only, which matches legacy test behaviour but is unsafe in
	// production: every service restart wipes every account (see the
	// 2026-04-23 wipe incident). In production this defaults to
	// <state_dir>/qsdm_users.json where state_dir = filepath.Dir(SQLitePath).
	// Env override: QSDM_USER_STORE_PATH (the pre-rebrand QSDMPLUS_USER_STORE_PATH env var
	// is no longer read; see pkg/envcompat godoc).
	UserStorePath               string
	ScyllaHosts                 []string
	ScyllaKeyspace              string
	ScyllaUsername              string
	ScyllaPassword              string
	ScyllaTLSCaPath             string
	ScyllaTLSCertPath           string
	ScyllaTLSKeyPath            string
	ScyllaTLSInsecureSkipVerify bool

	// Monitoring
	DashboardPort        int
	DashboardBindAddress string
	LogViewerPort        int
	LogFile              string
	LogLevel             string
	// NGCProofPersistPath: optional path the in-memory NGC
	// attestation ring (see pkg/monitoring/ngc_proofs.go) is
	// persisted to as JSONL. When set, qsdm.service restart no
	// longer wipes pre-restart bundles; cmd/qsdm replays the file
	// at startup so /api/v1/trust/attestations/summary stays
	// > 0 across restarts (assuming pre-restart timestamps are
	// still inside cfg.TrustFreshWithin, default 15m). Empty
	// (default) = legacy in-memory-only ring.
	// TOML: [monitoring] ngc_proof_persist_path; YAML:
	// monitoring.ngc_proof_persist_path; env:
	// QSDM_NGC_PROOF_PERSIST_PATH. See
	// pkg/monitoring/ngc_proof_persist.go for the on-disk format.
	NGCProofPersistPath string
	// DashboardMetricsScrapeSecret: when set, GET /api/metrics/prometheus accepts this via Bearer or
	// X-QSDM-Metrics-Scrape-Secret header (legacy X-QSDMPLUS-Metrics-Scrape-Secret also accepted).
	// Env: QSDM_DASHBOARD_METRICS_SCRAPE_SECRET (the pre-rebrand QSDMPLUS_DASHBOARD_METRICS_SCRAPE_SECRET
	// env var is no longer read; pkg/envcompat is now a no-op trim helper after db9b590).
	DashboardMetricsScrapeSecret string
	// DashboardStrictAuth: DEPRECATED and no longer read for its original
	// purpose. Protected dashboard routes now return 503 whenever the auth
	// manager failed to initialise, unconditionally -- serving them
	// unauthenticated was never a safe fallback, so it is no longer opt-out.
	// The key is still accepted so deployments that set it keep parsing.
	//
	// Formerly: when true ([monitoring] strict_dashboard_auth or QSDM_DASHBOARD_STRICT_AUTH),
	// JWT routes return 503 if auth manager failed to init; Prometheus still works if
	// metrics_scrape_secret is set. (The pre-rebrand QSDMPLUS_DASHBOARD_STRICT_AUTH env var is no
	// longer read; see pkg/envcompat godoc.)
	DashboardStrictAuth bool

	// API Server
	APIPort        int
	APIBindAddress string
	TLSCertFile    string
	TLSKeyFile     string
	EnableTLS      bool
	// ACME (Let's Encrypt) auto-provisioned TLS certificates.
	// Set ACMEDomains to enable; takes precedence over TLSCertFile/TLSKeyFile.
	ACMEDomains  []string
	ACMEEmail    string
	ACMECacheDir string
	// Mutual TLS (mTLS) for node-to-node API authentication.
	MTLSCACertFile   string
	MTLSNodeCertFile string
	MTLSNodeKeyFile  string
	MTLSAutoGenerate bool // auto-generate CA + node cert if files don't exist
	// APIRateLimitMaxRequests: max HTTP requests per client per APIRateLimitWindow (default 100/min). Health routes are exempt.
	APIRateLimitMaxRequests int
	APIRateLimitWindow      time.Duration

	// Wallet
	InitialBalance float64

	// Governance
	ProposalFile string

	// RequireSignedVotes rejects inbound BFT consensus messages that carry
	// no signature. Defaults to false so a mixed-version validator set can
	// roll forward: signed builds emit authenticated votes immediately,
	// and the operator enables rejection once every validator is upgraded.
	// A present-but-invalid signature is always rejected regardless of this
	// setting. Set via `[consensus] require_signed_votes` or
	// QSDM_REQUIRE_SIGNED_VOTES.
	//
	// Leave this off only during a rollout: while it is false, an
	// unauthenticated peer can still forge votes for any validator.
	RequireSignedVotes bool

	// SignedConsensusActivationHeight is the first block height at which
	// unsigned votes, blocks, POL artifacts, and equivocation evidence are
	// rejected. A shared future height lets upgraded nodes emit signatures
	// before every validator begins enforcing them at the same point.
	// It must be non-zero exactly when RequireSignedVotes is true.
	SignedConsensusActivationHeight uint64

	// TaskActionSignatureActivationHeight is the first height at which a task
	// action must carry a signature ([consensus] or
	// QSDM_TASK_ACTION_SIGNATURE_ACTIVATION_HEIGHT). Zero leaves the
	// requirement off. See chain.VerifyTaskActionSignature.
	TaskActionSignatureActivationHeight uint64

	// TxContentRootActivationHeight is the first height whose block tx root
	// commits transaction contents ([consensus] or
	// QSDM_TX_CONTENT_ROOT_ACTIVATION_HEIGHT). Zero keeps the legacy ID-only
	// root. Changing it changes block hashes; every node must agree.
	TxContentRootActivationHeight uint64

	// EnrollmentStateRootActivationHeight is the first height whose block state
	// root commits mining enrollment/slashing side state ([consensus] or
	// QSDM_ENROLLMENT_STATE_ROOT_ACTIVATION_HEIGHT). Zero keeps the legacy
	// account/task/stream/recovery root. Changing it changes block roots; every
	// node must agree.
	EnrollmentStateRootActivationHeight uint64

	// RC1113RulesActivationHeight is the first height of the rc1113 security rules (chain.SetRC1113RulesHeight;
	// [consensus] rc1113_rules_activation_height or QSDM_RC1113_RULES_ACTIVATION_HEIGHT). It changes what blocks are
	// valid, so every node must use the same value. Unset (or 0) means RC1113RulesDefaultActivationHeight, compiled into
	// the release like rc1112's 1,190,000, so no node's settings need a change; a non-zero key or env value overrides it.
	RC1113RulesActivationHeight uint64

	// PULSE, the non-custodial mining pool rule (chain.SetPulseParams; [consensus] pulse_* or QSDM_PULSE_*). They
	// change what blocks are valid, so every node must use the same values. PulseActivationHeight 0 (the default)
	// means never, and the node then refuses every PULSE transaction and escrow reward.
	PulseActivationHeight   uint64
	PulseMaxFeeBPS          uint16   // network fee ceiling; 0 = the default 300 bps (3 %)
	PulsePools              []string // authorized pools: "<pool id>:<ML-DSA-87 public key hex>[:<max fee bps>]"
	PulsePayeeEpochCapCell  float64  // PULSE payouts per payee per 8,640-block epoch; 0 = the default 2,000 CELL (the solo cap)
	PulseReclaimAfterBlocks uint64   // an unsettled escrowed reward is reclaimable by its owner after this; 0 = 8,640 blocks

	// PoEActivationHeight is the first height at which signed wallet
	// transfers must satisfy the Proof-of-Entanglement parent rules
	// ([consensus] poe_activation_height or QSDM_POE_ACTIVATION_HEIGHT).
	// Zero keeps them off. See chain.SetPoEActivationHeight.
	PoEActivationHeight uint64

	// EdgeRelayOnlyActivationHeight is the first height at which the three
	// built-in resource tasks (qsdm-edge-worker, -gpu, -ram) accept a submit
	// only as a pooled Relay settlement ([consensus]
	// edge_relay_only_activation_height or
	// QSDM_EDGE_RELAY_ONLY_ACTIVATION_HEIGHT). Zero keeps the rule off. See
	// chain.SetEdgeRelayOnlyActivationHeight.
	EdgeRelayOnlyActivationHeight uint64

	// StreamEd25519RetirementHeight is the first height at which a CELL
	// stream open (qsdm/streams/v1), which registers an Ed25519 session
	// key, is refused ([consensus] stream_ed25519_retirement_height or
	// QSDM_STREAM_ED25519_RETIREMENT_HEIGHT). Zero keeps the rule off. See
	// chain.SetStreamEd25519RetirementHeight.
	StreamEd25519RetirementHeight uint64

	// QSDM20ActivationHeight is the first height at which QSDM-20 token
	// transactions (qsdm/token/v1) are valid ([consensus]
	// qsdm20_activation_height or QSDM_QSDM20_ACTIVATION_HEIGHT). Zero keeps
	// them off. See chain.SetQSDM20ActivationHeight.
	QSDM20ActivationHeight uint64

	// ConsensusSignerKeyPath is the validator-only ML-DSA hot key used to
	// authenticate consensus traffic. It is not a wallet and must never hold
	// user or treasury funds. Empty resolves to a file under the node state
	// directory in cmd/qsdm.
	ConsensusSignerKeyPath string

	// ForkDustHeight is reserved for the coordinated integer-dust accounting
	// transition. Config parsing retains the field so operators can inspect old
	// files, but Validate rejects every non-zero value until the deterministic
	// capped-issuance transition and its governance manifest are implemented.
	//
	// Zero (the default) means never active, leaving the legacy float64
	// behaviour bit-for-bit intact. This is a CONSENSUS parameter: every
	// validator must configure the same height, and genesis must first be
	// re-derived to the documented 90M cap with no genesis treasury
	// (QSDM Genesis Policy v1.1) — the historical 1e15 CELL
	// funder allocation is 1e23 dust, which overflows uint64 and will make
	// the migration refuse to run.
	//
	// Set via `[consensus] fork_dust_height` or QSDM_FORK_DUST_HEIGHT.
	ForkDustHeight uint64

	// GovernanceAuthorities lists wallet addresses permitted to submit
	// `qsdm/gov/v1` parameter-set transactions. Empty (the default) keeps
	// on-chain governance disabled — the pre-governance posture in which
	// SlashApplier reads static defaults forever. Populating it via
	// `[governance] authorities` in the config file, or the
	// QSDM_GOVERNANCE_AUTHORITIES env var (comma-separated), activates the
	// governance arm wired in internal/v2wiring.
	GovernanceAuthorities []string

	// AuthorizedBlockProducers pins the producer IDs whose blocks this node will
	// append from the `qsdm-blocks` gossip topic. Empty -- the default -- accepts
	// any producer, which is the pre-existing behaviour and is why this defaults
	// off rather than fail-closed: enabling it wrongly would stop the node
	// following the chain.
	//
	// It matters because TryAppendExternalBlock authenticates the block but not
	// the right to produce one: VerifyBlockSignature returns nil for an unsigned
	// block below the signed-blocks activation height (the deployed posture), and
	// is self-certifying when signed. Pin the operator's known producer, which is
	// the same peer ID already used as the bootstrap peer.
	//
	// Set via `[consensus] authorized_block_producers` or
	// QSDM_AUTHORIZED_BLOCK_PRODUCERS (comma-separated).
	AuthorizedBlockProducers []string
	// ProducerTransition supersedes the flat allowlist only when explicitly configured.
	ProducerTransition *producerpolicy.Transition

	// Performance
	TransactionInterval     time.Duration
	HealthCheckInterval     time.Duration
	DemoTransactionsEnabled bool

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

	// JWTHMACSecretSecondary: VERIFY-ONLY secondary HMAC key used during
	// a key-rotation window (audit row rotation-01). When set, the
	// AuthManager.ValidateToken + RequestSigner.VerifyRequest fallback
	// paths try the primary key first and fall back to this secondary
	// on mismatch, incrementing
	// qsdm_security_jwt_secondary_key_hits_total /
	// qsdm_security_request_signature_secondary_key_hits_total
	// respectively. New tokens / signatures are ALWAYS produced with
	// JWTHMACSecret (the primary) — the secondary is decommissioning-
	// only. Leave empty when no rotation is in flight. Runbook:
	// QSDM/docs/docs/runbooks/JWT_KEY_ROTATION.md.
	JWTHMACSecretSecondary string

	// AdminAPIRequireRole: when true, /api/admin/* requires JWT role "admin" (after AuthMiddleware).
	// LoadConfig (applyEnvOverrides) turns it on unless QSDM_ADMIN_REQUIRE_ROLE is false, 0 or no:
	// account registration always assigns the role "user", so with it off any account could read
	// the admin plane. LoadConfigFile, which skips the environment, leaves it as the file sets it.
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

// LoadConfigFile loads one TOML or YAML configuration file as LoadConfig maps
// it, with the defaults, and validates it, but without the environment
// overrides and without a .env file: the result is what the file itself
// configures. Tools that check or rewrite a node's file use it
// (cmd/qsdm-handover).
func LoadConfigFile(path string) (*Config, error) {
	cfg := &Config{}
	if err := loadConfigFile(path, cfg); err != nil {
		return nil, fmt.Errorf("failed to load config file %s: %w", path, err)
	}
	if abs, err := filepath.Abs(path); err == nil {
		cfg.ConfigFileUsed = abs
	} else {
		cfg.ConfigFileUsed = filepath.Clean(path)
	}
	applyDefaults(cfg)
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
		cfg.RequireMiningOperatorSignatures = tomlCfg.Node.RequireMiningOperatorSignatures
		cfg.MiningOperatorPublicKeyRetentionHeight = tomlCfg.Node.MiningOperatorPublicKeyRetentionHeight
		cfg.NetworkPort = tomlCfg.Network.Port
		cfg.NetworkBindAddress = strings.TrimSpace(tomlCfg.Network.BindAddress)
		cfg.BootstrapPeers = tomlCfg.Network.BootstrapPeers
		cfg.SubmeshConfigPath = strings.TrimSpace(tomlCfg.Network.SubmeshConfig)
		cfg.NetworkHostKeyPath = strings.TrimSpace(tomlCfg.Network.HostKeyPath)
		if lim, err := tomlCfg.Network.P2PLimitsTOML.toConfig(); err != nil {
			return err
		} else {
			cfg.P2PLimits = lim
		}
		if br, err := tomlCfg.Network.BridgeRelayTOML.toConfig(); err != nil {
			return err
		} else {
			cfg.BridgeRelay = br
		}
		cfg.PrivateP2P = tomlCfg.Network.PrivateP2PTOML.toConfig()
		cfg.SnapshotBootstrap = tomlCfg.SnapshotBootstrap.toConfig()
		cfg.StorageType = tomlCfg.Storage.Type
		cfg.SQLitePath = tomlCfg.Storage.SQLitePath
		cfg.ScyllaHosts = tomlCfg.Storage.ScyllaHosts
		cfg.ScyllaKeyspace = tomlCfg.Storage.ScyllaKeyspace
		cfg.DashboardPort = tomlCfg.Monitoring.DashboardPort
		cfg.DashboardBindAddress = strings.TrimSpace(tomlCfg.Monitoring.DashboardBindAddress)
		cfg.LogViewerPort = tomlCfg.Monitoring.LogViewerPort
		cfg.LogFile = tomlCfg.Monitoring.LogFile
		cfg.LogLevel = tomlCfg.Monitoring.LogLevel
		cfg.DashboardMetricsScrapeSecret = tomlCfg.Monitoring.MetricsScrapeSecret
		cfg.DashboardStrictAuth = tomlCfg.Monitoring.StrictDashboardAuth
		cfg.NGCProofPersistPath = strings.TrimSpace(tomlCfg.Monitoring.NGCProofPersistPath)
		cfg.APIPort = tomlCfg.API.Port
		cfg.APIBindAddress = strings.TrimSpace(tomlCfg.API.BindAddress)
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
		cfg.GovernanceAuthorities = tomlCfg.Governance.Authorities
		cfg.AuthorizedBlockProducers = tomlCfg.Consensus.AuthorizedBlockProducers
		cfg.ProducerTransition = tomlCfg.Consensus.ProducerTransition
		cfg.RequireSignedVotes = tomlCfg.Consensus.RequireSignedVotes
		cfg.SignedConsensusActivationHeight = tomlCfg.Consensus.SignedMessageActivationHeight
		cfg.TaskActionSignatureActivationHeight = tomlCfg.Consensus.TaskActionSignatureActivationHeight
		cfg.TxContentRootActivationHeight = tomlCfg.Consensus.TxContentRootActivationHeight
		cfg.EnrollmentStateRootActivationHeight = tomlCfg.Consensus.EnrollmentStateRootActivationHeight
		cfg.RC1113RulesActivationHeight = tomlCfg.Consensus.RC1113RulesActivationHeight
		cfg.PulseActivationHeight = tomlCfg.Consensus.PulseActivationHeight
		cfg.PulseMaxFeeBPS = tomlCfg.Consensus.PulseMaxFeeBPS
		cfg.PulsePools = append([]string(nil), tomlCfg.Consensus.PulsePools...)
		cfg.PulsePayeeEpochCapCell = tomlCfg.Consensus.PulsePayeeEpochCapCell
		cfg.PulseReclaimAfterBlocks = tomlCfg.Consensus.PulseReclaimAfterBlocks
		cfg.PoEActivationHeight = tomlCfg.Consensus.PoEActivationHeight
		cfg.EdgeRelayOnlyActivationHeight = tomlCfg.Consensus.EdgeRelayOnlyActivationHeight
		cfg.StreamEd25519RetirementHeight = tomlCfg.Consensus.StreamEd25519RetirementHeight
		cfg.QSDM20ActivationHeight = tomlCfg.Consensus.QSDM20ActivationHeight
		cfg.ConsensusSignerKeyPath = strings.TrimSpace(tomlCfg.Consensus.SignerKeyPath)
		cfg.ForkDustHeight = tomlCfg.Consensus.ForkDustHeight
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
		cfg.DemoTransactionsEnabled = tomlCfg.Performance.DemoTransactions
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
		cfg.RequireMiningOperatorSignatures = yamlCfg.Node.RequireMiningOperatorSignatures
		cfg.MiningOperatorPublicKeyRetentionHeight = yamlCfg.Node.MiningOperatorPublicKeyRetentionHeight
		cfg.NetworkPort = yamlCfg.Network.Port
		cfg.NetworkBindAddress = strings.TrimSpace(yamlCfg.Network.BindAddress)
		cfg.BootstrapPeers = yamlCfg.Network.BootstrapPeers
		cfg.SubmeshConfigPath = strings.TrimSpace(yamlCfg.Network.SubmeshConfig)
		cfg.NetworkHostKeyPath = strings.TrimSpace(yamlCfg.Network.HostKeyPath)
		if lim, err := yamlCfg.Network.P2PLimitsTOML.toConfig(); err != nil {
			return err
		} else {
			cfg.P2PLimits = lim
		}
		if br, err := yamlCfg.Network.BridgeRelayTOML.toConfig(); err != nil {
			return err
		} else {
			cfg.BridgeRelay = br
		}
		cfg.PrivateP2P = yamlCfg.Network.PrivateP2PTOML.toConfig()
		cfg.SnapshotBootstrap = yamlCfg.SnapshotBootstrap.toConfig()
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
		cfg.DashboardBindAddress = strings.TrimSpace(yamlCfg.Monitoring.DashboardBindAddress)
		cfg.LogViewerPort = yamlCfg.Monitoring.LogViewerPort
		cfg.LogFile = yamlCfg.Monitoring.LogFile
		cfg.LogLevel = yamlCfg.Monitoring.LogLevel
		cfg.DashboardMetricsScrapeSecret = yamlCfg.Monitoring.MetricsScrapeSecret
		cfg.DashboardStrictAuth = yamlCfg.Monitoring.StrictDashboardAuth
		cfg.NGCProofPersistPath = strings.TrimSpace(yamlCfg.Monitoring.NGCProofPersistPath)
		cfg.APIPort = yamlCfg.API.Port
		cfg.APIBindAddress = strings.TrimSpace(yamlCfg.API.BindAddress)
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
		cfg.GovernanceAuthorities = yamlCfg.Governance.Authorities
		cfg.AuthorizedBlockProducers = yamlCfg.Consensus.AuthorizedBlockProducers
		cfg.ProducerTransition = yamlCfg.Consensus.ProducerTransition
		cfg.RequireSignedVotes = yamlCfg.Consensus.RequireSignedVotes
		cfg.SignedConsensusActivationHeight = yamlCfg.Consensus.SignedMessageActivationHeight
		cfg.TaskActionSignatureActivationHeight = yamlCfg.Consensus.TaskActionSignatureActivationHeight
		cfg.TxContentRootActivationHeight = yamlCfg.Consensus.TxContentRootActivationHeight
		cfg.EnrollmentStateRootActivationHeight = yamlCfg.Consensus.EnrollmentStateRootActivationHeight
		cfg.RC1113RulesActivationHeight = yamlCfg.Consensus.RC1113RulesActivationHeight
		cfg.PulseActivationHeight = yamlCfg.Consensus.PulseActivationHeight
		cfg.PulseMaxFeeBPS = yamlCfg.Consensus.PulseMaxFeeBPS
		cfg.PulsePools = append([]string(nil), yamlCfg.Consensus.PulsePools...)
		cfg.PulsePayeeEpochCapCell = yamlCfg.Consensus.PulsePayeeEpochCapCell
		cfg.PulseReclaimAfterBlocks = yamlCfg.Consensus.PulseReclaimAfterBlocks
		cfg.PoEActivationHeight = yamlCfg.Consensus.PoEActivationHeight
		cfg.EdgeRelayOnlyActivationHeight = yamlCfg.Consensus.EdgeRelayOnlyActivationHeight
		cfg.StreamEd25519RetirementHeight = yamlCfg.Consensus.StreamEd25519RetirementHeight
		cfg.QSDM20ActivationHeight = yamlCfg.Consensus.QSDM20ActivationHeight
		cfg.ConsensusSignerKeyPath = strings.TrimSpace(yamlCfg.Consensus.SignerKeyPath)
		cfg.ForkDustHeight = yamlCfg.Consensus.ForkDustHeight
		if yamlCfg.Performance.TransactionInterval != "" {
			if d, err := time.ParseDuration(yamlCfg.Performance.TransactionInterval); err == nil {
				cfg.TransactionInterval = d
			}
		}
		cfg.DemoTransactionsEnabled = yamlCfg.Performance.DemoTransactions
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
// RC1113RulesDefaultActivationHeight is the rc1113 rules height of the rc1113 release (owner, 2026-11-15: block
// 1,230,000, about 29 November 2026). Every node on this release uses it unless its config names another value.
const RC1113RulesDefaultActivationHeight uint64 = 1_230_000

// PulseDefaultActivationHeight is the PULSE rule's first height in the rc1114 release (2026-11-16: block 1,210,000,
// about 26-27 November 2026), applied when [consensus] pulse_activation_height is unset or 0, like the rc1113 rules
// height. Every node on rc1114 agrees with no settings change; a non-zero key or QSDM_PULSE_ACTIVATION_HEIGHT overrides it.
const PulseDefaultActivationHeight uint64 = 1_210_000

// PulseHostedPoolID is the pool hosted at qsdm.tech/pool (key generated on its server on 2026-11-16, never exported);
// the ID is hex(sha256(PulseHostedPoolPublicKey)). The owner decided: only this pool is authorized at first.
const PulseHostedPoolID = "37c1aaf5b64028204b0cad91ea4156b2a98ab722c63cece24e0af104d3653e2d"

// PulseHostedPoolPublicKey is that pool's ML-DSA-87 public key (2,592 bytes, hex), read from
// https://qsdm.tech/pool/api/v1/info on 2026-11-16.
const PulseHostedPoolPublicKey = "8de53c7d1b4cbbb417771b8a17d9b4e2842e303f038e22afbaa34aae04bc245c454dabd8f928473360298599fe02b097" +
	"b8c55b189984460be33ad88112ebb3e50004c9da336c76546d715d7abc98d4f2412e6dd6b7e8e0611cca77801e500c23" +
	"2802aaaf2ebc5eba7dad8e6fd4a47756a59d5e7d58cc65a94710a794e78b38c8f91548fec844d2df7c27185aa300f704" +
	"a93217ca8ba0dafddea6f4b34fd699f77269299141ef6c87f1ee53f53eb5e61e17138ba53c92bb4eed7d66a9fa67c4ea" +
	"d3da18db8047f7de656d3d652852ec71e6573478eb6cee09bcf7c239ace671c9a523b2503490895f87970a7b5fc2a4a2" +
	"912217c0bb4946fa267d611747bb64dab9a846c239515c26d605eadb1b8d1cb9dd1c3d9505351fe8bd532246e55c64e6" +
	"763c7bb2d7e84d3c6a6d348c000932937a50737e313776c10faca00b0ea64c7d1eb06c2f7f2bf28a3a6216b8af3841d2" +
	"a57a4ebeefb611a8398c87e3967b06d44b4670118085bd709c66669d777f092d8aead1d1e6edd06829f9065aa0d9cc9e" +
	"64fc7f9f6c38888a4f9c5f14aa4382ac79738e669372d4f8188724b37927d82a6bb0b8106766cc52f3638d6ebfd5bda6" +
	"54c70f2e9b8c3344a1c8b7dbe7b8e09c022c1e8419eb39ea22112c2b0ba7de7de757823532d02cfe6dac68031a86abfe" +
	"f8a9a4c2fc58c0a6bad5a53950af48f35ef2e4ed611be829b40b60970dda445b65d3c2cabba34505e717535308b6de7e" +
	"f9b4f2c4fcc1272f186d11f5183215eefa54f4545ad19568721b6a4ab129729b064c92aadc7dc5a1a8c458be386ea2b3" +
	"b94ae962e3d78222b30dcf5568364ff0120902c05720f2e024780cea270f8ada8c8ce6abef2b1725b50e91791f9c0684" +
	"881a3c743b7d6108522893c62a698323c0e3f3db4825b3493441d63f10b43f68357112099bc08b6c275513127e653889" +
	"b8eea07087382905d2dfe9074d54bcad73dce3aec0c38dc7579e6ede5ec34b1bd2d210a25d3da95ed4735b144ebe6734" +
	"be456ad20dcb6b8b6f9d1431c22160d8879ba3313f52579cbf4cac9e478b62aa9d9269ec64a46abcbfc8169faba0e4b4" +
	"c0456c378f58bae3e74bad451952d210b759d4a784c46f5e8595286e8ea1cf122abc9f08f56aa045361d518dc67f1158" +
	"aafad3aaa1e939c6f0f10348c1f6b9cba3966f35f3464374759188058a8fc7e9fb2f5a1f76e40f1a8341a58a6c3b530e" +
	"3654a54e58d5be25f8a70d18e61f17e643120438b16846bcc5f604faa3146ac818b95a525c2a4002fb7b35be4971b8a0" +
	"af73ad7b0077f8d32f51fad3630182175479bdf24d30f427e0b399862cc55daebbe1b63c930a8b6c6edcacbe9bbb2ffe" +
	"580ed1fc0941fc1d6de33d489d1f86848c2bce6064a497658ef65eccbdac4ab93eb43807f5ab98dc50220a1c6a11169f" +
	"7010a6cf5db79824a92e4ee27161a7bab1f0ffacd2740a2953376e43505a6673bf40377bc42e25ce4c255067627b60ad" +
	"319ccf1d061f30ce0bd452618b23c65fa2202831460657a645100ea5707379f637f21f32ca34d648c73a108c3de6aed1" +
	"c699680e8e189b21de98132f726c05e2cef12fb1327528d85aef67bb280015160db38860958a01474071b711ff247058" +
	"747887bc9643aac446f17b04af09a410470d4fbc08f686432adf0b7f7453b05b1a2f9e1460dcbbbc6fa4c6e53137cd34" +
	"03883d7497e203458eb45b19f11f536dd379589e985e2885b618ae0cc2cfea76d027aff7bc4ea8b8fb1c72ad3babb750" +
	"9c65245ceb6243468c918ce892802e85e643c33105025319720e5fcde27587d74f19c0a1569652e47fc337dd2fcc66d8" +
	"d5c5df7116fb4ca7dcac33692a6ac3281be439b13381b94884173cfb098f605f2021ef84d43431630511f2a344618663" +
	"6d0adb01e7a0e099cbbd26f9fb48d8dcfbfb50396e1c2de03e4dbe2429a5a0520ceaea67a42b00ec9851e1e664e8f261" +
	"2dd4c7e94acb7806056fdf6d432d848597971da0dbb7a6475aaacc22fd677103f4ead8a3604fcbd57ca19f5d97c0e463" +
	"87980495ba0fe6ab0d1c99265921c8fb6c977c2339cfe6ce87633a02acde8f770e075290f72cdae89a72ab6dc87bc83c" +
	"b5bc9c9f8a7c12b35dc056c22fa13bf647c906e30f58fb03ceaa46c2306c2f91219f60bc6d3b5ad79443bc5ecfd67c2f" +
	"6ec8b1560ba7f573d9cab0ee48ca2d0c5077a0df9a8f0f07959a528da54a8dcbeade045b8e781762f2736db516711f74" +
	"5ebb7ea31ff6176aff759a05ec0066d5757f302a8df812ebecb69389a2ba9f52c21e2e24029401473ca65db9d72ea7b9" +
	"5248926f1d08040226997d70f6a1528cf98f3f675262561c03572dee5dfa50a289d97c5a8089e7f018a31275f10e4bbc" +
	"5b891f0d529b1da8bdb1d23d45890be364b5046f0f20e5785ddedd9f87b3638c572a0b07fdddfbb8354af7b3cfe93a15" +
	"5f1858f40e6b7ef28a8ea8aada50cbef9575272df15e16b15bf3286dab17da9638d98932accd74494a1a0c2f9c64bf6a" +
	"07138d15683dade39b36986f57c3dc03ff5d7cf2201328113de0c1073d9b02145b7085245d86f2fd064718e835ceec1e" +
	"54f85df3db29830ee3054119427b1b056a9cf34a2e22e182b083f962781a4e482c87ad7da3aa1fece6e542d3593acc6e" +
	"c67d0fa0dcfdf9da7deb76306d914ef1f63bce70ef120e495c09e5eb7b2ae4163f9f09e6c66752081cd97ae7b6eaaaf1" +
	"f791dcd64003e2a83581c117f5c8aca591339d827f3086c6685b5c111b25b897b4feaacb1b92ff41e18e5e098474f17d" +
	"d3b5ff1c4c19d77e329061fa8655c49bb11313d14d88208bcd3635d1f556a30dd38619e62d1c9b538f6b403f57049907" +
	"2a1c5ce80d06bb860222871bcd3d11edc5e8f19e4ea4890e48ffaea2f8e9284b7b1d08015740918ecd91e9a4d94bc189" +
	"bebf051d661744bf358dde468a2711d6037f214d6c3cfbd5cc54b85d152d2be77891d7a6d19d91ae76c7afaababffacf" +
	"a6fb19d9e75c71ef68e13654200b8726289efd9330ec805f4daa9a7ab6c08a2aa50b1bab32dfb2731769caee05ca8bc0" +
	"cdec8a23f031d9c78e1f6674be80812ec63eb91bdb9eb1a6321b9fed1809bee3fe935d69cb02cc6cc01a761da4868530" +
	"4426ff90f97d0fef064dff02f4114fe52593fd5dd5d56bff3d36d256acaceac17345f46def101ea97978331176bcbe44" +
	"1b4c59edbb292431ee3d81870b8de53794ee20e61993f68d3e186989f67e67450437e067045ffcf81b8930718e946eae" +
	"49e7456cacd2611439f5cde082e4a283cc170b4c086729762d99998cbb55e9bdb252b1654722e1b4e8ffe1fd7da91cec" +
	"1c6f3ba41754a8a9a6a0c8aa3a737aa4834097e8d745b173588656864f76871d2d3174e1126e0d9256404adb5e1b6908" +
	"e3c5c5ebcba425fe28934ecde8433fdf4970aaef9846250bcc3f48b136b3c9b5936e4edbe8a390355e0b960b68d79382" +
	"3149dcad37c9cfb38f670137e72c73a2810b4395cea50b1ac9dcf34c80e17dbf430567f0c20f0ce3bf8f892cddc3c8a5" +
	"5edc3559d5734f505ecfdf0051f1203a6173d5ffd5d117773c2964774cdc4a024dd37d4e95220d2923ad733f0c226c93" +
	"dead83e77133f0f37c1f15133fe26eab379485b165b3aa71139ea2a82e606408361c12632b4e2a8aa4cb972b9533cb93"

// PulseDefaultPools is the authorized-pool manifest of the rc1114 release, applied when [consensus] pulse_pools is
// empty; a non-empty key or QSDM_PULSE_POOLS replaces it.
func PulseDefaultPools() []string {
	return []string{PulseHostedPoolID + ":" + PulseHostedPoolPublicKey}
}

func applyDefaults(cfg *Config) {
	if cfg.RC1113RulesActivationHeight == 0 {
		cfg.RC1113RulesActivationHeight = RC1113RulesDefaultActivationHeight
	}
	if cfg.PulseActivationHeight == 0 {
		cfg.PulseActivationHeight = PulseDefaultActivationHeight
	}
	if len(cfg.PulsePools) == 0 {
		cfg.PulsePools = PulseDefaultPools()
	}
	if cfg.NodeRole == "" {
		cfg.NodeRole = NodeRoleValidator
	}
	if cfg.NetworkPort == 0 {
		cfg.NetworkPort = 4001
	}
	if cfg.BootstrapPeers == nil {
		cfg.BootstrapPeers = []string{}
	}
	if cfg.GovernanceAuthorities == nil {
		cfg.GovernanceAuthorities = []string{}
	}
	if cfg.AuthorizedBlockProducers == nil {
		cfg.AuthorizedBlockProducers = []string{}
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
		cfg.EnableTLS = false // Default to HTTP
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
	if val := strings.TrimSpace(envPreferred("QSDM_REQUIRE_MINING_OPERATOR_SIGNATURES", "QSDM_REQUIRE_MINING_OPERATOR_SIGNATURES")); val != "" {
		cfg.RequireMiningOperatorSignatures = envcompat.Truthy("QSDM_REQUIRE_MINING_OPERATOR_SIGNATURES", "QSDM_REQUIRE_MINING_OPERATOR_SIGNATURES")
	}
	if v := strings.TrimSpace(getEnvString("QSDM_MINING_OPERATOR_PUBLIC_KEY_RETENTION_HEIGHT", "")); v != "" {
		if h, err := strconv.ParseUint(v, 10, 64); err == nil {
			cfg.MiningOperatorPublicKeyRetentionHeight = h
		}
	}
	if val := getEnvString("NETWORK_PORT", ""); val != "" {
		cfg.NetworkPort = getEnvInt("NETWORK_PORT", cfg.NetworkPort)
	}
	if val := strings.TrimSpace(envPreferred("QSDM_NETWORK_BIND_ADDRESS", "QSDM_NETWORK_BIND_ADDRESS")); val != "" {
		cfg.NetworkBindAddress = val
	}
	if val := getEnvString("BOOTSTRAP_PEERS", ""); val != "" {
		cfg.BootstrapPeers = getEnvStringSlice("BOOTSTRAP_PEERS", cfg.BootstrapPeers)
	}
	if val := getEnvString("QSDM_GOVERNANCE_AUTHORITIES", ""); val != "" {
		cfg.GovernanceAuthorities = getEnvStringSlice("QSDM_GOVERNANCE_AUTHORITIES", cfg.GovernanceAuthorities)
	}
	if val := getEnvString("QSDM_AUTHORIZED_BLOCK_PRODUCERS", ""); val != "" {
		cfg.AuthorizedBlockProducers = getEnvStringSlice("QSDM_AUTHORIZED_BLOCK_PRODUCERS", cfg.AuthorizedBlockProducers)
	}
	if v := getEnvString("QSDM_FORK_DUST_HEIGHT", ""); v != "" {
		if h, err := strconv.ParseUint(v, 10, 64); err == nil {
			cfg.ForkDustHeight = h
		}
	}
	if getEnvString("QSDM_REQUIRE_SIGNED_VOTES", "") != "" {
		cfg.RequireSignedVotes = envcompat.Truthy("QSDM_REQUIRE_SIGNED_VOTES", "QSDM_REQUIRE_SIGNED_VOTES")
	}
	if v := strings.TrimSpace(getEnvString("QSDM_SIGNED_MESSAGE_ACTIVATION_HEIGHT", "")); v != "" {
		if h, err := strconv.ParseUint(v, 10, 64); err == nil {
			cfg.SignedConsensusActivationHeight = h
		}
	}
	if v := strings.TrimSpace(getEnvString("QSDM_TASK_ACTION_SIGNATURE_ACTIVATION_HEIGHT", "")); v != "" {
		if h, err := strconv.ParseUint(v, 10, 64); err == nil {
			cfg.TaskActionSignatureActivationHeight = h
		}
	}
	if v := strings.TrimSpace(getEnvString("QSDM_TX_CONTENT_ROOT_ACTIVATION_HEIGHT", "")); v != "" {
		if h, err := strconv.ParseUint(v, 10, 64); err == nil {
			cfg.TxContentRootActivationHeight = h
		}
	}
	if v := strings.TrimSpace(getEnvString("QSDM_ENROLLMENT_STATE_ROOT_ACTIVATION_HEIGHT", "")); v != "" {
		if h, err := strconv.ParseUint(v, 10, 64); err == nil {
			cfg.EnrollmentStateRootActivationHeight = h
		}
	}
	if v := strings.TrimSpace(getEnvString("QSDM_PULSE_ACTIVATION_HEIGHT", "")); v != "" {
		if h, err := strconv.ParseUint(v, 10, 64); err == nil {
			cfg.PulseActivationHeight = h
		}
	}
	if v := strings.TrimSpace(getEnvString("QSDM_PULSE_MAX_FEE_BPS", "")); v != "" {
		if b, err := strconv.ParseUint(v, 10, 16); err == nil {
			cfg.PulseMaxFeeBPS = uint16(b)
		}
	}
	if v := strings.TrimSpace(getEnvString("QSDM_PULSE_POOLS", "")); v != "" {
		cfg.PulsePools = nil
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				cfg.PulsePools = append(cfg.PulsePools, p)
			}
		}
	}
	if v := strings.TrimSpace(getEnvString("QSDM_PULSE_PAYEE_EPOCH_CAP_CELL", "")); v != "" {
		if c, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.PulsePayeeEpochCapCell = c
		}
	}
	if v := strings.TrimSpace(getEnvString("QSDM_PULSE_RECLAIM_AFTER_BLOCKS", "")); v != "" {
		if h, err := strconv.ParseUint(v, 10, 64); err == nil {
			cfg.PulseReclaimAfterBlocks = h
		}
	}
	if v := strings.TrimSpace(getEnvString("QSDM_RC1113_RULES_ACTIVATION_HEIGHT", "")); v != "" {
		if h, err := strconv.ParseUint(v, 10, 64); err == nil {
			cfg.RC1113RulesActivationHeight = h
		}
	}
	if v := strings.TrimSpace(getEnvString("QSDM_POE_ACTIVATION_HEIGHT", "")); v != "" {
		if h, err := strconv.ParseUint(v, 10, 64); err == nil {
			cfg.PoEActivationHeight = h
		}
	}
	if v := strings.TrimSpace(getEnvString("QSDM_EDGE_RELAY_ONLY_ACTIVATION_HEIGHT", "")); v != "" {
		if h, err := strconv.ParseUint(v, 10, 64); err == nil {
			cfg.EdgeRelayOnlyActivationHeight = h
		}
	}
	if v := strings.TrimSpace(getEnvString("QSDM_STREAM_ED25519_RETIREMENT_HEIGHT", "")); v != "" {
		if h, err := strconv.ParseUint(v, 10, 64); err == nil {
			cfg.StreamEd25519RetirementHeight = h
		}
	}
	if v := strings.TrimSpace(getEnvString("QSDM_QSDM20_ACTIVATION_HEIGHT", "")); v != "" {
		if h, err := strconv.ParseUint(v, 10, 64); err == nil {
			cfg.QSDM20ActivationHeight = h
		}
	}
	if v := strings.TrimSpace(getEnvString("QSDM_CONSENSUS_SIGNER_KEY_PATH", "")); v != "" {
		cfg.ConsensusSignerKeyPath = v
	}
	if val := strings.TrimSpace(envPreferred("QSDM_NETWORK_HOST_KEY_PATH", "QSDM_NETWORK_HOST_KEY_PATH")); val != "" {
		cfg.NetworkHostKeyPath = val
	}
	applyP2PLimitsEnv(&cfg.P2PLimits)
	applyBridgeRelayEnv(&cfg.BridgeRelay)
	applyPrivateP2PEnv(&cfg.PrivateP2P)
	applySnapshotBootstrapEnv(&cfg.SnapshotBootstrap)
	if val := strings.TrimSpace(envPreferred("QSDM_NGC_PROOF_PERSIST_PATH", "QSDM_NGC_PROOF_PERSIST_PATH")); val != "" {
		cfg.NGCProofPersistPath = val
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
	if val := strings.TrimSpace(envPreferred("QSDM_DASHBOARD_BIND_ADDRESS", "QSDM_DASHBOARD_BIND_ADDRESS")); val != "" {
		cfg.DashboardBindAddress = val
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
	// Secure by default: /api/admin/* requires the JWT role "admin" unless an operator turns
	// the check off explicitly (QSDM_ADMIN_REQUIRE_ROLE=false, 0 or no).
	cfg.AdminAPIRequireRole = true
	if val := strings.TrimSpace(envPreferred("QSDM_ADMIN_REQUIRE_ROLE", "QSDM_ADMIN_REQUIRE_ROLE")); val != "" {
		cfg.AdminAPIRequireRole = !(val == "false" || val == "0" || strings.EqualFold(val, "no"))
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
	if val := strings.TrimSpace(envPreferred("QSDM_API_BIND_ADDRESS", "QSDM_API_BIND_ADDRESS")); val != "" {
		cfg.APIBindAddress = val
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
	if val := strings.TrimSpace(getEnvString("QSDM_DEMO_TRANSACTIONS", "")); val != "" {
		cfg.DemoTransactionsEnabled = val == "true" || val == "1" || strings.EqualFold(val, "yes")
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
	// Optional VERIFY-ONLY secondary key for the rotation window.
	// Env var only — there is intentionally no TOML/YAML field for the
	// secondary because the rotation window is operational (deploy +
	// restart driven), not part of the long-lived service config.
	if val := envPreferred("QSDM_JWT_HMAC_SECRET_SECONDARY", "QSDM_JWT_HMAC_SECRET_SECONDARY"); val != "" {
		cfg.JWTHMACSecretSecondary = val
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
	if err := c.ProducerTransition.Validate(); err != nil {
		return err
	}
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
	if c.ForkDustHeight != 0 {
		return fmt.Errorf(
			"consensus fork_dust_height=%d is not activation-ready: leave it at 0 until the synthetic funder is replaced by a persisted capped-issuance ledger and validators approve one reconciled migration manifest",
			c.ForkDustHeight,
		)
	}
	if c.RequireSignedVotes && c.SignedConsensusActivationHeight == 0 {
		return fmt.Errorf("consensus require_signed_votes=true requires a non-zero signed_message_activation_height shared by every validator")
	}
	if !c.RequireSignedVotes && c.SignedConsensusActivationHeight != 0 {
		return fmt.Errorf("consensus signed_message_activation_height=%d is set while require_signed_votes=false; either enable coordinated enforcement or clear the height", c.SignedConsensusActivationHeight)
	}
	if c.RequireMiningOperatorSignatures && c.MiningOperatorPublicKeyRetentionHeight == 0 {
		return fmt.Errorf("node require_mining_operator_signatures=true requires non-zero mining_operator_public_key_retention_height shared by every validator")
	}

	if c.NetworkPort < 1 || c.NetworkPort > 65535 {
		return fmt.Errorf("invalid network port: %d", c.NetworkPort)
	}
	if err := c.P2PLimits.Validate(); err != nil {
		return err
	}
	if err := c.BridgeRelay.Validate(c.ConsensusSignerKeyPath); err != nil {
		return err
	}
	if err := c.PrivateP2P.Validate(c.NetworkBindAddress, c.BootstrapPeers); err != nil {
		return err
	}
	if err := c.SnapshotBootstrap.Validate(); err != nil {
		return err
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
			return fmt.Errorf("nvidia_lock is enabled but NGC ingest secret is empty; set QSDM_NGC_INGEST_SECRET so the node can receive GPU proof bundles")
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
		if c.NodeRole.IsValidator() && authorizedBlockProducerCount(c.AuthorizedBlockProducers) == 0 {
			return fmt.Errorf("consensus authorized_block_producers must include at least one pinned producer when strict_secrets=true for a validator")
		}
	}

	return nil
}

func authorizedBlockProducerCount(producers []string) int {
	count := 0
	for _, producer := range producers {
		if strings.TrimSpace(producer) != "" {
			count++
		}
	}
	return count
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
