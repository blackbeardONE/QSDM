package config

// ConfigTOML represents the TOML structure for configuration file
type ConfigTOML struct {
	Node        NodeConfig        `toml:"node" yaml:"node"`
	Network     NetworkConfig     `toml:"network"`
	Storage     StorageConfig     `toml:"storage"`
	Monitoring  MonitoringConfig  `toml:"monitoring"`
	API         APIConfig         `toml:"api"`
	Wallet      WalletConfig      `toml:"wallet"`
	Governance  GovernanceConfig  `toml:"governance"`
	Consensus   ConsensusConfigTOML `toml:"consensus" yaml:"consensus"`
	Performance PerformanceConfig `toml:"performance"`
	Trust       TrustConfigTOML   `toml:"trust" yaml:"trust"`
}

// TrustConfigTOML controls the opt-in attestation transparency surface
// introduced by Major Update Phase 5 (§8.5). This is *not* a consensus
// rule — it only affects the /api/v1/trust/attestations/* endpoints and
// the landing-page / dashboard widgets that scrape them. See
// docs/docs/NVIDIA_LOCK_CONSENSUS_SCOPE.md for the boundary.
//
// All fields are optional. An empty or missing [trust] block resolves to
// the defaults: endpoints enabled, 15 m freshness window, 10 s
// background refresh, no region hint.
type TrustConfigTOML struct {
	// Disabled: when true, the trust endpoints return HTTP 404 and the
	// aggregator is not wired. Operators set this if they do not want
	// to publish aggregate attestation data at all.
	// Env override: QSDM_TRUST_DISABLED (legacy alias: QSDM_TRUST_DISABLED).
	Disabled bool `toml:"disabled" yaml:"disabled"`
	// FreshWithin: how recent an attestation must be to count as fresh
	// in the X/Y ratio (Go duration string, e.g. "15m", "5m30s"). Zero
	// / empty → 15 m. Env override: QSDM_TRUST_FRESH_WITHIN.
	FreshWithin string `toml:"fresh_within" yaml:"fresh_within"`
	// RefreshInterval: background cache rebuild cadence (Go duration).
	// Zero / empty → 10 s. Env override: QSDM_TRUST_REFRESH_INTERVAL.
	RefreshInterval string `toml:"refresh_interval" yaml:"refresh_interval"`
	// RegionHint: coarse region served to the transparency widget.
	// Must be one of {"eu","us","apac","other",""}; any other value is
	// normalised to "other" by the aggregator.
	// Env override: QSDM_TRUST_REGION.
	RegionHint string `toml:"region_hint" yaml:"region_hint"`
}

// NodeConfig selects the node role and mining policy. Introduced by the Major
// Update (Phase 2.1) to make the validator / miner two-tier model explicit in
// every deployment's config file.
//
// Role may be "validator" (default; CPU-only, runs PoE + BFT) or "miner"
// (GPU, runs additive PoW for Cell emission; never proposes blocks). When Role
// is unset it defaults to validator.
//
// MiningEnabled is an additional guard: it MUST be true when Role is "miner"
// and MUST be false when Role is "validator". The server fails fast at
// startup if the two disagree. A validator build that is tagged
// `validator_only` will also refuse to start with MiningEnabled=true.
type NodeConfig struct {
	// Role: "validator" (default) or "miner". Env override: QSDM_NODE_ROLE.
	Role string `toml:"role" yaml:"role"`
	// MiningEnabled: must be true only for miner nodes. Env override:
	// QSDM_MINING_ENABLED (accepts "true" / "1" / "yes"). Default: false.
	MiningEnabled bool `toml:"mining_enabled" yaml:"mining_enabled"`
}

type NetworkConfig struct {
	Port           int      `toml:"port"`
	BindAddress    string   `toml:"bind_address" yaml:"bind_address"`
	BootstrapPeers []string `toml:"bootstrap_peers"`
	// SubmeshConfig: optional path to a micropayments-style profile (.toml/.yaml) loaded at startup.
	SubmeshConfig string `toml:"submesh_config" yaml:"submesh_config"`
	// HostKeyPath: optional file that persists the libp2p host PrivateKey
	// across qsdm.service restarts so peer.ID is stable. Empty (default)
	// = ephemeral key, generated fresh every restart. Production deploys
	// should set this to e.g. `/opt/qsdm/host_key`. File format: a single
	// line of base64(proto.Marshal(libp2p PrivKey)), mode 0600; see
	// pkg/networking/hostkey.go. Env override: QSDM_NETWORK_HOST_KEY_PATH.
	HostKeyPath string `toml:"host_key_path" yaml:"host_key_path"`
}

type StorageConfig struct {
	Type           string   `toml:"type" yaml:"type"`
	SQLitePath     string   `toml:"sqlite_path" yaml:"sqlite_path"`
	ScyllaHosts    []string `toml:"scylla_hosts" yaml:"scylla_hosts"`
	ScyllaKeyspace string   `toml:"scylla_keyspace" yaml:"scylla_keyspace"`
	// ScyllaUsername / ScyllaPassword: native protocol authentication (password authenticator).
	ScyllaUsername string `toml:"scylla_username" yaml:"scylla_username"`
	ScyllaPassword string `toml:"scylla_password" yaml:"scylla_password"`
	// TLS: PEM paths; prefer SCYLLA_PASSWORD env over committing secrets. scylla_tls_insecure_skip_verify is dev-only.
	ScyllaTLSCaPath             string `toml:"scylla_tls_ca_path" yaml:"scylla_tls_ca_path"`
	ScyllaTLSCertPath           string `toml:"scylla_tls_cert_path" yaml:"scylla_tls_cert_path"`
	ScyllaTLSKeyPath            string `toml:"scylla_tls_key_path" yaml:"scylla_tls_key_path"`
	ScyllaTLSInsecureSkipVerify bool   `toml:"scylla_tls_insecure_skip_verify" yaml:"scylla_tls_insecure_skip_verify"`
}

type MonitoringConfig struct {
	DashboardPort        int    `toml:"dashboard_port" yaml:"dashboard_port"`
	DashboardBindAddress string `toml:"dashboard_bind_address" yaml:"dashboard_bind_address"`
	LogViewerPort        int    `toml:"log_viewer_port" yaml:"log_viewer_port"`
	LogFile              string `toml:"log_file" yaml:"log_file"`
	LogLevel             string `toml:"log_level" yaml:"log_level"`
	MetricsScrapeSecret  string `toml:"metrics_scrape_secret" yaml:"metrics_scrape_secret"`
	StrictDashboardAuth  bool   `toml:"strict_dashboard_auth" yaml:"strict_dashboard_auth"`
	// NGCProofPersistPath: optional file path the in-memory NGC
	// attestation ring is persisted to as JSONL. Empty (default)
	// = legacy in-memory-only ring; non-empty = pre-restart
	// bundles replayed at boot so /api/v1/trust/attestations/
	// summary.attested doesn't drop to 0 on every qsdm.service
	// restart. See pkg/monitoring/ngc_proof_persist.go. Env:
	// QSDM_NGC_PROOF_PERSIST_PATH.
	NGCProofPersistPath string `toml:"ngc_proof_persist_path" yaml:"ngc_proof_persist_path"`
}

type APIConfig struct {
	Port                         int    `toml:"port" yaml:"port"`
	BindAddress                  string `toml:"bind_address" yaml:"bind_address"`
	EnableTLS                    bool   `toml:"enable_tls" yaml:"enable_tls"`
	TLSCertFile                  string `toml:"tls_cert_file" yaml:"tls_cert_file"`
	TLSKeyFile                   string `toml:"tls_key_file" yaml:"tls_key_file"`
	NvidiaLock                   bool   `toml:"nvidia_lock" yaml:"nvidia_lock"`
	NvidiaLockMaxProofAge        string `toml:"nvidia_lock_max_proof_age" yaml:"nvidia_lock_max_proof_age"`
	NvidiaLockExpectedNodeID     string `toml:"nvidia_lock_expected_node_id" yaml:"nvidia_lock_expected_node_id"`
	NvidiaLockProofHMACSecret    string `toml:"nvidia_lock_proof_hmac_secret" yaml:"nvidia_lock_proof_hmac_secret"`
	NvidiaLockRequireIngestNonce bool   `toml:"nvidia_lock_require_ingest_nonce" yaml:"nvidia_lock_require_ingest_nonce"`
	NvidiaLockGateP2P            bool   `toml:"nvidia_lock_gate_p2p" yaml:"nvidia_lock_gate_p2p"`
	NvidiaLockIngestNonceTTL     string `toml:"nvidia_lock_ingest_nonce_ttl" yaml:"nvidia_lock_ingest_nonce_ttl"`
	JWTHMACSecret                string `toml:"jwt_hmac_secret" yaml:"jwt_hmac_secret"`
	// StrictProductionSecrets: reject short/demo-like NGC/JWT/HMAC secrets at startup
	// (env QSDM_STRICT_SECRETS, or legacy QSDM_STRICT_SECRETS, overrides file).
	StrictProductionSecrets bool `toml:"strict_secrets" yaml:"strict_secrets"`
	// RateLimitMaxRequests / RateLimitWindow: per-IP (or X-API-Key) sliding window on the HTTP API (e.g. 200 and "2m").
	RateLimitMaxRequests int    `toml:"rate_limit_max_requests" yaml:"rate_limit_max_requests"`
	RateLimitWindow      string `toml:"rate_limit_window" yaml:"rate_limit_window"`
}

type WalletConfig struct {
	InitialBalance float64 `toml:"initial_balance"`
}

type GovernanceConfig struct {
	ProposalFile string `toml:"proposal_file"`

	// Authorities lists the wallet addresses permitted to submit
	// `qsdm/gov/v1` parameter-set transactions. Empty (the default)
	// leaves on-chain governance disabled and every gov tx rejects with
	// chainparams.ErrGovernanceNotConfigured. Populating it is what
	// activates the governance arm.
	Authorities []string `toml:"authorities" yaml:"authorities"`
}

// ConsensusConfigTOML holds BFT consensus policy knobs.
type ConsensusConfigTOML struct {
	// RequireSignedVotes rejects unsigned inbound BFT messages. Turn this
	// on once every validator runs a build that signs its votes.
	RequireSignedVotes bool `toml:"require_signed_votes" yaml:"require_signed_votes"`

	// SignedMessageActivationHeight is the first height where unsigned
	// consensus traffic is rejected. All validators must use the same value.
	SignedMessageActivationHeight uint64 `toml:"signed_message_activation_height" yaml:"signed_message_activation_height"`

	// TaskActionSignatureActivationHeight is the first height at which a
	// qsdm/tasks/v1 action must carry a signature. Zero leaves the
	// requirement off, which is the shipped default: turning it on rejects
	// any unsigned task action at or above the height, so an operator must
	// first confirm their own chain carries none above the value they pick.
	// A signature that IS present is verified at every height regardless.
	TaskActionSignatureActivationHeight uint64 `toml:"task_action_signature_activation_height" yaml:"task_action_signature_activation_height"`

	// SignerKeyPath stores the validator-only ML-DSA consensus hot key.
	SignerKeyPath string `toml:"signer_key_path" yaml:"signer_key_path"`

	// ForkDustHeight is a reserved consensus parameter. Config validation
	// currently rejects every non-zero value because the live capped-issuance
	// transition is not implemented. Unset (0) keeps the unsafe path disabled.
	ForkDustHeight uint64 `toml:"fork_dust_height" yaml:"fork_dust_height"`

	// AuthorizedBlockProducers pins the producer IDs whose blocks this node
	// appends from the `qsdm-blocks` topic. Empty accepts any producer, which
	// is the pre-existing behaviour. Populate it with the operator's known
	// producer -- the same peer ID used as the bootstrap peer -- to close the
	// path where an arbitrary peer injects a block that every node replays.
	AuthorizedBlockProducers []string `toml:"authorized_block_producers" yaml:"authorized_block_producers"`
}

type PerformanceConfig struct {
	TransactionInterval string `toml:"transaction_interval" yaml:"transaction_interval"`
	HealthCheckInterval string `toml:"health_check_interval" yaml:"health_check_interval"`
	DemoTransactions    bool   `toml:"demo_transactions" yaml:"demo_transactions"`
}
