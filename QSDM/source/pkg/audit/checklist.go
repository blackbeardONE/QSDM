package audit

import (
	"fmt"
	"sync"
	"time"
)

// Category groups related audit items.
type Category string

const (
	CatCryptography   Category = "cryptography"
	CatAuthentication Category = "authentication"
	CatAuthorisation  Category = "authorisation"
	CatNetwork        Category = "network"
	CatSmartContracts Category = "smart_contracts"
	CatBridge         Category = "bridge"
	CatStorage        Category = "storage"
	CatAPI            Category = "api"
	CatGovernance     Category = "governance"
	CatInfra          Category = "infrastructure"
	CatSupplyChain    Category = "supply_chain"
	CatRuntime        Category = "runtime"
	CatSecretRotation Category = "secret_rotation"

	// Major Update categories (see docs/docs/history/Major Update.md).
	// Added as part of Phase 5: these track the in-repo deliverables that
	// need wall-clock action (counsel sign-off, external audit, genesis
	// ceremony) before mainnet launch.
	CatRebrand     Category = "rebrand"
	CatTokenomics  Category = "tokenomics"
	CatMiningAudit Category = "mining_audit"
	CatTrustAPI    Category = "trust_api"
)

// Severity indicates the impact of a finding or the priority of a checklist item.
type Severity string

const (
	SevCritical Severity = "critical"
	SevHigh     Severity = "high"
	SevMedium   Severity = "medium"
	SevLow      Severity = "low"
	SevInfo     Severity = "info"
)

// Status tracks whether an audit item has been reviewed.
type Status string

const (
	StatusPending  Status = "pending"
	StatusPassed   Status = "passed"
	StatusFailed   Status = "failed"
	StatusWaived   Status = "waived"
)

// ChecklistItem represents a single audit check.
type ChecklistItem struct {
	ID          string   `json:"id"`
	Category    Category `json:"category"`
	Severity    Severity `json:"severity"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Status      Status   `json:"status"`
	Notes       string   `json:"notes,omitempty"`
	ReviewedBy  string   `json:"reviewed_by,omitempty"`
	ReviewedAt  *time.Time `json:"reviewed_at,omitempty"`
}

// Checklist is the full audit checklist with review tracking.
type Checklist struct {
	mu    sync.RWMutex
	items map[string]*ChecklistItem
	order []string // preserves insertion order
}

// NewChecklist creates a pre-populated security audit checklist for QSDM+.
func NewChecklist() *Checklist {
	cl := &Checklist{
		items: make(map[string]*ChecklistItem),
	}
	for _, item := range defaultItems() {
		cp := item
		if cp.Status == "" {
			cp.Status = StatusPending
		}
		cl.items[item.ID] = &cp
		cl.order = append(cl.order, item.ID)
	}
	return cl
}

// Items returns all checklist items in order.
func (cl *Checklist) Items() []ChecklistItem {
	cl.mu.RLock()
	defer cl.mu.RUnlock()
	out := make([]ChecklistItem, 0, len(cl.order))
	for _, id := range cl.order {
		if item, ok := cl.items[id]; ok {
			out = append(out, *item)
		}
	}
	return out
}

// Get returns a single item by ID.
func (cl *Checklist) Get(id string) (*ChecklistItem, bool) {
	cl.mu.RLock()
	defer cl.mu.RUnlock()
	item, ok := cl.items[id]
	if !ok {
		return nil, false
	}
	cp := *item
	return &cp, true
}

// UpdateStatus marks an item as reviewed.
func (cl *Checklist) UpdateStatus(id string, status Status, reviewer, notes string) error {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	item, ok := cl.items[id]
	if !ok {
		return fmt.Errorf("checklist item %q not found", id)
	}
	item.Status = status
	item.ReviewedBy = reviewer
	item.Notes = notes
	now := time.Now()
	item.ReviewedAt = &now
	return nil
}

// Summary returns aggregate counts by status.
func (cl *Checklist) Summary() map[string]int {
	cl.mu.RLock()
	defer cl.mu.RUnlock()
	counts := map[string]int{
		"total":   len(cl.items),
		"pending": 0,
		"passed":  0,
		"failed":  0,
		"waived":  0,
	}
	for _, item := range cl.items {
		counts[string(item.Status)]++
	}
	return counts
}

// ByCategory returns items filtered by category.
func (cl *Checklist) ByCategory(cat Category) []ChecklistItem {
	cl.mu.RLock()
	defer cl.mu.RUnlock()
	var out []ChecklistItem
	for _, id := range cl.order {
		item := cl.items[id]
		if item.Category == cat {
			out = append(out, *item)
		}
	}
	return out
}

// BySeverity returns items filtered by severity.
func (cl *Checklist) BySeverity(sev Severity) []ChecklistItem {
	cl.mu.RLock()
	defer cl.mu.RUnlock()
	var out []ChecklistItem
	for _, id := range cl.order {
		item := cl.items[id]
		if item.Severity == sev {
			out = append(out, *item)
		}
	}
	return out
}

// PendingCritical returns critical/high items that are still pending.
func (cl *Checklist) PendingCritical() []ChecklistItem {
	cl.mu.RLock()
	defer cl.mu.RUnlock()
	var out []ChecklistItem
	for _, id := range cl.order {
		item := cl.items[id]
		if item.Status == StatusPending && (item.Severity == SevCritical || item.Severity == SevHigh) {
			out = append(out, *item)
		}
	}
	return out
}

func defaultItems() []ChecklistItem {
	return []ChecklistItem{
		// Cryptography
		{ID: "crypto-01", Category: CatCryptography, Severity: SevCritical, Title: "ML-DSA key generation", Description: "Verify ML-DSA-87 (Dilithium) keypair generation uses CSPRNG and follows NIST FIPS 204."},
		{ID: "crypto-02", Category: CatCryptography, Severity: SevCritical, Title: "HMAC fallback security", Description: "Confirm HMAC fallback uses random ephemeral key (not hardcoded) when ML-DSA is unavailable."},
		{ID: "crypto-03", Category: CatCryptography, Severity: SevHigh, Title: "JWT signature verification", Description: "Verify JWT tokens are validated with constant-time comparison and proper expiry checks."},
		{ID: "crypto-04", Category: CatCryptography, Severity: SevMedium, Title: "Secret generation entropy", Description: "Audit all secret/nonce generation (bridge secrets, CSRF tokens, session IDs) for crypto/rand usage."},
		{ID: "crypto-05", Category: CatCryptography, Severity: SevHigh, Title: "mTLS certificate validation", Description: "Verify mTLS rejects connections with untrusted CAs, expired certs, and wrong CN/SAN."},

		// Authentication
		{ID: "auth-01", Category: CatAuthentication, Severity: SevCritical, Title: "Password hashing", Description: "Verify passwords are hashed with bcrypt/argon2 and never stored in plaintext or reversible form."},
		{ID: "auth-02", Category: CatAuthentication, Severity: SevHigh, Title: "Account lockout", Description: "Confirm brute-force protection: account lockout after N failed attempts with configurable cooldown."},
		{ID: "auth-03", Category: CatAuthentication, Severity: SevHigh, Title: "Session management", Description: "Verify session cookies are HttpOnly, Secure (on TLS), SameSite=Lax, and have reasonable expiry."},
		{ID: "auth-04", Category: CatAuthentication, Severity: SevMedium, Title: "Token replay prevention", Description: "Confirm JWT nonces and timestamp windows prevent replay attacks."},
		{ID: "auth-05", Category: CatAuthentication, Severity: SevHigh, Title: "Password policy enforcement", Description: "Verify minimum length (12+), complexity (upper, lower, digit, symbol) requirements."},

		// Authorisation
		{ID: "authz-01", Category: CatAuthorisation, Severity: SevCritical, Title: "RBAC enforcement", Description: "Verify admin-only endpoints reject non-admin users (mTLS gen, governance execution)."},
		{ID: "authz-02", Category: CatAuthorisation, Severity: SevHigh, Title: "Multi-sig threshold", Description: "Confirm multi-sig actions cannot execute with fewer than required signatures."},
		{ID: "authz-03", Category: CatAuthorisation, Severity: SevMedium, Title: "Contract upgrade authorisation", Description: "Verify only owner or authorised upgraders can upgrade contracts; freeze policy is enforced."},
		{ID: "authz-04", Category: CatAuthorisation, Severity: SevHigh, Title: "Rate limit per role", Description: "Confirm admin/user/anonymous tiers are applied correctly and cannot be bypassed."},

		// Network
		{ID: "net-01", Category: CatNetwork, Severity: SevHigh, Title: "P2P message authentication", Description: "Verify GossipSub messages are signed and unauthenticated peers are rejected."},
		{ID: "net-02", Category: CatNetwork, Severity: SevMedium, Title: "DHT Sybil resistance", Description: "Assess Kademlia DHT bootstrap configuration for Sybil attack resistance."},
		{ID: "net-03", Category: CatNetwork, Severity: SevHigh, Title: "TLS configuration", Description: "Verify TLS 1.2+ only, strong cipher suites, and no fallback to plaintext."},
		{ID: "net-04", Category: CatNetwork, Severity: SevMedium, Title: "WebSocket origin validation", Description: "Confirm WS upgrade validates Origin header in production (currently permissive for dev)."},

		// Smart Contracts
		{ID: "sc-01", Category: CatSmartContracts, Severity: SevCritical, Title: "WASM sandbox isolation", Description: "Verify wazero sandboxes provide memory isolation between contracts (no shared state leaks)."},
		{ID: "sc-02", Category: CatSmartContracts, Severity: SevHigh, Title: "Gas metering enforcement", Description: "Confirm out-of-gas halts execution and cannot be bypassed by malicious WASM code."},
		{ID: "sc-03", Category: CatSmartContracts, Severity: SevHigh, Title: "Contract event integrity", Description: "Verify emitted events are tamper-proof and indexing cannot be manipulated."},
		{ID: "sc-04", Category: CatSmartContracts, Severity: SevMedium, Title: "Simulation fallback correctness", Description: "Audit simulation execution for determinism and state consistency."},

		// Bridge
		{ID: "bridge-01", Category: CatBridge, Severity: SevCritical, Title: "Atomic swap secret handling", Description: "Verify bridge secrets are generated with crypto/rand, hashed before storage, and never leaked in P2P."},
		{ID: "bridge-02", Category: CatBridge, Severity: SevHigh, Title: "Lock expiry enforcement", Description: "Confirm expired locks cannot be redeemed and refunds work correctly after expiry."},
		{ID: "bridge-03", Category: CatBridge, Severity: SevHigh, Title: "Fee calculation integrity", Description: "Verify fee collector cannot be manipulated to under-charge or double-collect."},
		{ID: "bridge-04", Category: CatBridge, Severity: SevMedium, Title: "Relayer retry safety", Description: "Confirm relayer retries are idempotent and nonce tracking prevents double-submission."},

		// Storage
		{ID: "store-01", Category: CatStorage, Severity: SevHigh, Title: "State persistence integrity", Description: "Verify contract/bridge/governance JSON files use atomic writes (tmp + rename)."},
		{ID: "store-02", Category: CatStorage, Severity: SevMedium, Title: "Snapshot hash verification", Description: "Confirm snapshot hashes are verified on load to detect corruption."},
		{ID: "store-03", Category: CatStorage, Severity: SevLow, Title: "File permission hardening", Description: "Verify sensitive files (certs, keys, state) are written with restrictive permissions (0600/0644)."},

		// API
		{ID: "api-01", Category: CatAPI, Severity: SevHigh, Title: "Input validation", Description: "Verify all API inputs (addresses, amounts, IDs) are validated with length/format/range checks."},
		{ID: "api-02", Category: CatAPI, Severity: SevHigh, Title: "CSRF protection", Description: "Confirm CSRF middleware is applied to state-changing endpoints and bypassed only for Bearer auth."},
		{ID: "api-03", Category: CatAPI, Severity: SevMedium, Title: "Security headers", Description: "Verify HSTS, CSP, X-Frame-Options, X-Content-Type-Options are set on all responses."},
		{ID: "api-04", Category: CatAPI, Severity: SevMedium, Title: "Error information leakage", Description: "Confirm error responses do not leak internal state, stack traces, or file paths."},

		// Governance
		{ID: "gov-01", Category: CatGovernance, Severity: SevHigh, Title: "Vote manipulation prevention", Description: "Verify voters cannot double-vote and vote weights are correctly applied."},
		{ID: "gov-02", Category: CatGovernance, Severity: SevHigh, Title: "Proposal execution safety", Description: "Confirm proposal executor prevents double execution and respects quorum/majority."},
		{ID: "gov-03", Category: CatGovernance, Severity: SevMedium, Title: "Multi-sig expiry enforcement", Description: "Verify expired multi-sig actions cannot be signed or executed."},

		// Infrastructure
		{ID: "infra-01", Category: CatInfra, Severity: SevHigh, Title: "Docker image hardening", Description: "Verify Docker images use non-root user, minimal base, and no unnecessary packages."},
		{ID: "infra-02", Category: CatInfra, Severity: SevMedium, Title: "Secret management", Description: "Confirm no hardcoded secrets in source; all secrets from env vars or secure config."},
		{ID: "infra-03", Category: CatInfra, Severity: SevLow, Title: "Dependency audit", Description: "Run go mod tidy + govulncheck for known CVEs in dependencies."},

		// Supply-chain integrity
		{ID: "supply-01", Category: CatSupplyChain, Severity: SevCritical, Title: "Go module verification", Description: "CI runs `go mod verify` on every build; go.sum changes require PR review."},
		{ID: "supply-02", Category: CatSupplyChain, Severity: SevHigh, Title: "Transitive CVE scanning", Description: "`govulncheck ./...` runs in CI and blocks merges on critical/high vulnerabilities."},
		{ID: "supply-03", Category: CatSupplyChain, Severity: SevHigh, Title: "Container image scanning", Description: "Trivy (or equivalent) scans the published container image; critical/high findings block release."},
		{ID: "supply-04", Category: CatSupplyChain, Severity: SevHigh, Title: "Build provenance / SBOM", Description: "Release workflow generates an SBOM (CycloneDX or SPDX) and attaches it to the container image / release."},
		{ID: "supply-05", Category: CatSupplyChain, Severity: SevMedium, Title: "Signed releases", Description: "Release artefacts (binaries, container images) are signed with cosign / sigstore and signatures are verified in the deploy pipeline."},
		{ID: "supply-06", Category: CatSupplyChain, Severity: SevMedium, Title: "Reproducible builds", Description: "Build pipeline is deterministic: pinned Go version, pinned toolchain, no network access during `go build` beyond module cache."},
		{ID: "supply-07", Category: CatSupplyChain, Severity: SevMedium, Title: "Dependency pinning policy", Description: "Third-party Go modules are pinned to specific versions; dependabot / renovate monitors for security updates with PR review."},

		// Container runtime / deployment
		{ID: "runtime-01", Category: CatRuntime, Severity: SevHigh, Title: "Non-root container user", Description: "Runtime container runs as a non-root UID; /proc/1/status confirms `Uid: <non-zero>`."},
		{ID: "runtime-02", Category: CatRuntime, Severity: SevHigh, Title: "Read-only root filesystem", Description: "Kubernetes / Compose spec sets `readOnlyRootFilesystem: true`; writable paths are explicit emptyDirs or PVCs."},
		{ID: "runtime-03", Category: CatRuntime, Severity: SevMedium, Title: "Linux capability drop", Description: "Container drops ALL capabilities except the minimum required (no CAP_NET_RAW, CAP_SYS_ADMIN, etc.)."},
		{ID: "runtime-04", Category: CatRuntime, Severity: SevMedium, Title: "Seccomp / AppArmor profile", Description: "Container uses RuntimeDefault seccomp profile (or custom strict profile); privilege escalation blocked via `allowPrivilegeEscalation: false`."},
		{ID: "runtime-05", Category: CatRuntime, Severity: SevHigh, Title: "Resource limits", Description: "CPU / memory limits and requests are set; no unbounded pods that can DoS the node."},
		{ID: "runtime-06", Category: CatRuntime, Severity: SevMedium, Title: "Liveness / readiness probes", Description: "Both probes are wired to `/api/v1/health/live` and `/api/v1/health/ready` with realistic timeouts."},
		{ID: "runtime-07", Category: CatRuntime, Severity: SevMedium, Title: "NetworkPolicy / egress control", Description: "Default-deny NetworkPolicy restricts pod egress to known Scylla / metrics / P2P peers only."},

		// Secret rotation lifecycle
		{ID: "rotation-01", Category: CatSecretRotation, Severity: SevHigh, Title: "JWT / API key rotation", Description: "Documented rotation procedure for JWT signing keys and API keys; rotation can be performed without downtime (dual-accept window)."},
		{ID: "rotation-02", Category: CatSecretRotation, Severity: SevHigh, Title: "mTLS certificate rotation", Description: "Node certificates rotate before expiry (monitored); CA rotation procedure is documented and rehearsed."},
		{ID: "rotation-03", Category: CatSecretRotation, Severity: SevHigh, Title: "Scylla auth credential rotation", Description: "SCYLLA_USERNAME / SCYLLA_PASSWORD rotate at least quarterly; new credentials deploy via secret manager without client restart where possible."},
		{ID: "rotation-04", Category: CatSecretRotation, Severity: SevMedium, Title: "Bridge secret rotation", Description: "Bridge atomic-swap secret seed rotates on schedule; compromised secrets can be revoked and audited."},
		{ID: "rotation-05", Category: CatSecretRotation, Severity: SevMedium, Title: "Rotation monitoring", Description: "Alerts fire when any secret / certificate is within 30 days of expiry; dashboard surfaces rotation status per component."},

		// Major Update Phase 1: rebrand completeness.
		{ID: "rebrand-01", Category: CatRebrand, Severity: SevHigh, Title: "Brand-name completeness sweep", Description: "All user-visible QSDM+ surfaces have been rebranded to QSDM (README, landing page, dashboard pills, CLI output). Historical session-log entries remain verbatim. See REBRAND_NOTES.md."},
		{ID: "rebrand-02", Category: CatRebrand, Severity: SevHigh, Title: "Env var / header deprecation shim", Description: "QSDMPLUS_* env vars and X-QSDMPLUS-* HTTP headers remain accepted for one minor version, logged once per process as deprecated; QSDM_* / X-QSDM-* are preferred. Unit tests cover both shapes."},
		{ID: "rebrand-03", Category: CatRebrand, Severity: SevMedium, Title: "Trademark filings initiated", Description: "Trademark search and filing for 'QSDM' and 'Cell (CELL)' completed or tracked with counsel. BLOCKED on wall-clock action — document status in NEXT_STEPS.md."},
		{ID: "rebrand-04", Category: CatRebrand, Severity: SevInfo, Title: "SDK package aliases", Description: "Go sdk/qsdm and JS sdk/qsdm.js re-export the legacy qsdmplus packages; QSDMClient aliases QSDMPlusClient. Covered by sdk tests."},
		{ID: "rebrand-05", Category: CatRebrand, Severity: SevHigh, Title: "Prometheus metric prefix dual-emit", Description: "Every qsdmplus_* metric is also emitted under the qsdm_* prefix via pkg/monitoring/prometheus_prefix_migration.go. Knobs: QSDM_METRICS_EMIT_LEGACY, QSDM_METRICS_EMIT_QSDM. Self-observability gauges (qsdm_metrics_legacy_emission_enabled, qsdm_metrics_qsdm_emission_enabled, qsdm_metrics_emit_both_suppressed_total) expose current emission state. Tests in prometheus_prefix_migration_test.go cover default / legacy-off / new-off / both-off fallback. See REBRAND_NOTES.md §3.7 for the cutover schedule."},
		{ID: "rebrand-06", Category: CatRebrand, Severity: SevMedium, Title: "Release artefacts reproducibility", Description: "Every release tag produces Go binaries (qsdmminer, trustcheck, genesis-ceremony) cross-compiled with -trimpath -ldflags=\"-s -w\" CGO_ENABLED=0 for linux/{amd64,arm64}, darwin/{amd64,arm64}, windows/amd64. SHA256SUMS attached to the GitHub Release. Container images published under both the legacy (qsdmplus) and new (qsdm-validator, qsdm-miner) names. Workflow: .github/workflows/release-container.yml."},
		{ID: "rebrand-07", Category: CatRebrand, Severity: SevHigh, Title: "Trust aggregator wired into node startup", Description: "cmd/qsdmplus/main.go constructs a TrustAggregator with a ValidatorSetPeerProvider (over nodeValidatorSet.ActiveValidators) and a MonitoringLocalSource (NGC ring buffer). A background goroutine calls Refresh() every cfg.TrustRefreshInterval (default 10 s) and exits with ctx.Done(). Config knobs: [trust] disabled/fresh_within/refresh_interval/region_hint in TOML and YAML; env aliases QSDM_TRUST_DISABLED, QSDM_TRUST_FRESH_WITHIN, QSDM_TRUST_REFRESH_INTERVAL, QSDM_TRUST_REGION (legacy QSDMPLUS_* still accepted). When disabled, SetTrustAggregator(nil, true) makes the /api/v1/trust/attestations/* endpoints return HTTP 404 per §8.5.3. Covered by pkg/api/trust_peer_provider_test.go, pkg/config/trust_config_test.go, pkg/api/handlers_trust_dashboard_integration_test.go, and cmd/trustcheck/integration_test.go."},

		// Major Update Phase 3: tokenomics and genesis policy.
		{ID: "tok-01", Category: CatTokenomics, Severity: SevCritical, Title: "Genesis policy sign-off", Description: "Tokenomics genesis parameters (100 M cap, 10 M treasury, 90 M mining emission, 4-year halvings, 10 s block time) ratified per Phase 0 recommendation. BLOCKED on external counsel review — document in CELL_TOKENOMICS.md front-matter."},
		{ID: "tok-02", Category: CatTokenomics, Severity: SevHigh, Title: "Emission schedule determinism", Description: "pkg/chain/emission computes block reward and cumulative supply with integer-only math. Tests cover: epoch boundaries, halving edges, MaxHalvings overflow guard, cap convergence."},
		{ID: "tok-03", Category: CatTokenomics, Severity: SevHigh, Title: "Tokenomics API transparency", Description: "/api/v1/status publishes the live emission snapshot (cap, emitted, block reward, next halving) so independent tools can verify on-chain issuance matches CELL_TOKENOMICS.md."},

		// Major Update Phase 4: mining protocol external audit.
		{ID: "mining-01", Category: CatMiningAudit, Severity: SevCritical, Title: "Mining protocol external audit", Description: "MINING_PROTOCOL.md and pkg/mining reviewed by an independent cryptography / consensus auditor before mainnet CUDA launch. Auditor entry-point packet lives at docs/docs/AUDIT_PACKET_MINING.md (threat model, invariants I-1..I-10, test coverage matrix, reproducible build). BLOCKED on engagement — track in NEXT_STEPS.md."},
		{ID: "mining-02", Category: CatMiningAudit, Severity: SevHigh, Title: "CUDA miner isolation from consensus", Description: "cmd/qsdmminer never imports pkg/consensus and never runs in the validator process. validator_only build tag excludes pkg/mining/cuda. roleguard.MustMatchRole fails fast on config drift."},
		{ID: "mining-03", Category: CatMiningAudit, Severity: SevHigh, Title: "Proof canonicalisation determinism", Description: "pkg/mining.Proof canonical JSON round-trips bit-exact across architectures; Proof.ID excludes attestation; duplicate IDs are rejected via ProofIDSet."},
		{ID: "mining-04", Category: CatMiningAudit, Severity: SevHigh, Title: "DAG + difficulty retarget unit coverage", Description: "pkg/mining/dag and pkg/mining/difficulty pass unit tests covering: InMemoryDAG vs LazyDAG agreement, retarget on-target / slow / fast / clamp / floor paths."},
		{ID: "mining-05", Category: CatMiningAudit, Severity: SevMedium, Title: "Incentivized testnet readiness", Description: "Reference CPU miner (cmd/qsdmminer) is functional and documented in MINER_QUICKSTART.md. BLOCKED on wall-clock — incentivized testnet launch requires operational infra and marketing."},

		// Major Update Phase 5: trust endpoint anti-claim guardrails.
		{ID: "trust-01", Category: CatTrustAPI, Severity: SevHigh, Title: "Trust summary always serves a ratio", Description: "GET /api/v1/trust/attestations/summary always returns 'attested' and 'total_public' so widgets can render X of Y, never just X (§8.5.2 guardrail). Tested via TestWidgetState_* cases."},
		{ID: "trust-02", Category: CatTrustAPI, Severity: SevHigh, Title: "Scope-note non-strippable", Description: "Summary response embeds the fixed scope_note referencing NVIDIA_LOCK_CONSENSUS_SCOPE.md on every 200 response; scraping tools cannot strip the caveat without tampering with the body."},
		{ID: "trust-03", Category: CatTrustAPI, Severity: SevHigh, Title: "Node-ID redaction rule", Description: "recent endpoint emits node_id_prefix as first 8 + last 4 only; full libp2p IDs never leak. Covered by TestTrustRecentHandler_RedactsAndSorts."},
		{ID: "trust-04", Category: CatTrustAPI, Severity: SevMedium, Title: "Region hint coarse-bucketing", Description: "region_hint is restricted to eu/us/apac/other; city- or AS-granularity data is never exposed. Enforced by normaliseRegion."},
		{ID: "trust-05", Category: CatTrustAPI, Severity: SevMedium, Title: "Consensus-independence assertion", Description: "Trust endpoints are served by the HTTP surface only; no consensus or block-validity path reads trust state. Verified by grep and by the CatMiningAudit guard mining-02."},
		{ID: "trust-06", Category: CatTrustAPI, Severity: SevMedium, Title: "Four widget states implemented", Description: "Landing widget + /trust page handle healthy / degraded / zero-opt-in / NGC-outage states without flashing 'loading forever' (§8.5.4). Covered by TestWidgetState_* cases."},
	}
}
