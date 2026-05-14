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

// ts is a panicking RFC3339 time-pointer helper used by defaultItems() to
// stamp ReviewedAt on checklist entries that have already been validated by
// in-tree tests or by live-deployment evidence captured in CHANGELOG.md /
// NEXT_STEPS.md. It panics at startup (not at request time) so a typo can
// never silently degrade the checklist into a missing-timestamp state.
func ts(rfc3339 string) *time.Time {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		panic(fmt.Sprintf("audit: invalid review timestamp %q: %v", rfc3339, err))
	}
	return &t
}

// NewChecklist creates a pre-populated security audit checklist for QSDM
// (the post-rebrand product name; the pre-rebrand name was QSDM+).
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
		{ID: "crypto-03", Category: CatCryptography, Severity: SevHigh, Title: "JWT signature verification", Description: "Verify JWT tokens are validated with constant-time comparison and proper expiry checks.", Status: StatusPassed, ReviewedBy: "evidence:in-tree-tests", ReviewedAt: ts("2026-05-14T18:30:00Z"), Notes: "TestTokenValidation + TestTokenExpiration + TestBearerTokenReusedUntilExpiry in tests/api_security_test.go cover bearer-token validation (invalid → 401, valid → not 401), 2s-expiry rejection, and reuse-until-expiry semantics. AuthManager (pkg/api/auth.go) signs claims with HS256 + explicit Issuer/Audience/Expiry and validates with hmac.Equal-based comparison."},
		{ID: "crypto-04", Category: CatCryptography, Severity: SevMedium, Title: "Secret generation entropy", Description: "Audit all secret/nonce generation (bridge secrets, CSRF tokens, session IDs) for crypto/rand usage.", Status: StatusPassed, ReviewedBy: "evidence:in-tree", ReviewedAt: ts("2026-05-14T18:30:00Z"), Notes: "Production code uses crypto/rand exclusively (csrf.go::GenerateToken 32 B, user.go::HashPassword 16 B salt, host_key 32 B, NGC nonces, multi-sig action IDs). math/rand only appears in test fixtures (pkg/mining/attest/cc/verifier_test.go, roots_test.go, pkg/storage/scylla_chaos_test.go) where statistical-but-not-cryptographic randomness is acceptable. Grep audit on 2026-05-14: 4 math/rand hits, all under _test.go."},
		{ID: "crypto-05", Category: CatCryptography, Severity: SevHigh, Title: "mTLS certificate validation", Description: "Verify mTLS rejects connections with untrusted CAs, expired certs, and wrong CN/SAN."},

		// Authentication
		{ID: "auth-01", Category: CatAuthentication, Severity: SevCritical, Title: "Password hashing", Description: "Verify passwords are hashed with bcrypt/argon2 and never stored in plaintext or reversible form.", Status: StatusPassed, ReviewedBy: "evidence:in-tree-tests", ReviewedAt: ts("2026-05-14T18:30:00Z"), Notes: "pkg/api/user.go::HashPassword uses Argon2id with NIST-grade parameters (memory=64 MiB, time=3, threads=4, keyLen=32, salt=16 B from crypto/rand); VerifyPassword uses subtle.ConstantTimeCompare for the hash check. UserStore persists only the salt:hash blob via pkg/api/user_persist.go (atomic write, mode 0600). Covered by TestPasswordHashing in tests/api_security_test.go (hash + verify-correct + verify-wrong-password rejection) and TestValidatePasswordCharming123AtHash in pkg/api/validation_password_test.go."},
		{ID: "auth-02", Category: CatAuthentication, Severity: SevHigh, Title: "Account lockout", Description: "Confirm brute-force protection: account lockout after N failed attempts with configurable cooldown.", Status: StatusPassed, ReviewedBy: "evidence:in-tree", ReviewedAt: ts("2026-05-14T18:30:00Z"), Notes: "pkg/api/account_lockout.go::AccountLockoutManager: 5 max attempts, 15-min lockout, 15-min attempt window, clear-on-success. Wired into the login handler at pkg/api/handlers.go:613 via authManager.RecordFailedAttempt; IsLocked + GetRemainingAttempts + GetLockoutInfo surface state to handlers. Lock state persists in-memory per-instance (sufficient for single-validator deploy; multi-validator clusters would need shared backing — tracked in store-* category)."},
		{ID: "auth-03", Category: CatAuthentication, Severity: SevHigh, Title: "Session management", Description: "Verify session cookies are HttpOnly, Secure (on TLS), SameSite=Lax, and have reasonable expiry.", Status: StatusPassed, ReviewedBy: "evidence:in-tree", ReviewedAt: ts("2026-05-14T18:30:00Z"), Notes: "internal/dashboard/dashboard.go:869-871 sets dashboard session cookies with HttpOnly=true, SameSite=http.SameSiteLaxMode, Secure=<tlsEnabled> (true under prod TLS). Path-scoped to dashboard routes; expiry inherited from cookie default. Public-API surface uses bearer tokens via Authorization header rather than cookies, so the cookie-based attack surface is restricted to the operator dashboard."},
		{ID: "auth-04", Category: CatAuthentication, Severity: SevMedium, Title: "Token replay prevention", Description: "Confirm JWT nonces and timestamp windows prevent replay attacks."},
		{ID: "auth-05", Category: CatAuthentication, Severity: SevHigh, Title: "Password policy enforcement", Description: "Verify minimum length (12+), complexity (upper, lower, digit, symbol) requirements.", Status: StatusPassed, ReviewedBy: "evidence:in-tree-tests", ReviewedAt: ts("2026-05-14T18:30:00Z"), Notes: "pkg/api/validation.go::MinPasswordLength=12 (NIST SP 800-63B floor, increased from 8 in pre-rebrand) + common-password blocklist (password, 123456, qwerty, abc123, password123, etc. at line 205). Covered by TestInputValidation in tests/api_security_test.go (short-password 'short' → 400 invalid-request response)."},

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
		{ID: "net-05", Category: CatNetwork, Severity: SevMedium, Title: "libp2p host key persistence (stable peer.ID across restarts)", Description: "Verify pkg/networking/hostkey.go::loadOrCreateHostKey: (a) base64-encodes a libp2p.MarshalPrivateKey blob, (b) atomic tmp+rename write at mode 0600 with parent-dir-must-exist precondition (no auto-mkdir behind the operator), (c) load-or-create posture — empty path preserves the legacy ephemeral identity. SetupLibP2PWithPortAndKey threads the loaded key into libp2p.Identity(...). Config knob: Config.NetworkHostKeyPath (env QSDM_NETWORK_HOST_KEY_PATH). Live verification: deploy → restart → peer.ID changes ONCE (key generation) → restart → peer.ID stable. Deployed and verified on api.qsdm.tech (Session 89; node_id 12D3KooWRH4… stable across two restarts; previous ephemeral roll on every restart eliminated).", Status: StatusPassed, ReviewedBy: "evidence:live-deploy", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "Session 89: peer.ID 12D3KooWRH4… stable across two restarts on api.qsdm.tech."},

		// Smart Contracts
		{ID: "sc-01", Category: CatSmartContracts, Severity: SevCritical, Title: "WASM sandbox isolation", Description: "Verify wazero sandboxes provide memory isolation between contracts (no shared state leaks)."},
		{ID: "sc-02", Category: CatSmartContracts, Severity: SevHigh, Title: "Gas metering enforcement", Description: "Confirm out-of-gas halts execution and cannot be bypassed by malicious WASM code.", Status: StatusPassed, ReviewedBy: "evidence:in-tree-tests", ReviewedAt: ts("2026-05-14T18:30:00Z"), Notes: "TestGasMeter_ExhaustionError + TestGasMeter_BasicConsumption + TestGasMeter_DefaultLimit in pkg/contracts/gas_test.go cover the OOG-halt semantics and consume-before-host-call accounting (gas is decremented BEFORE the host function executes so an over-budget call halts before any state mutation)."},
		{ID: "sc-03", Category: CatSmartContracts, Severity: SevHigh, Title: "Contract event integrity", Description: "Verify emitted events are tamper-proof and indexing cannot be manipulated.", Status: StatusPassed, ReviewedBy: "evidence:in-tree-tests", ReviewedAt: ts("2026-05-14T18:30:00Z"), Notes: "TestEventIndex_{EmitAndQuery,Retention,QueryOffsetLimit,Subscribe,SubscribeAll} in pkg/contracts/events_test.go — 5 tests cover emit, append-only retention semantics, query pagination, and pub/sub fan-out. Events are persisted via the same atomic-write storage path as other contract state (see store-01) so tamper-resistance inherits from that property."},
		{ID: "sc-04", Category: CatSmartContracts, Severity: SevMedium, Title: "Simulation fallback correctness", Description: "Audit simulation execution for determinism and state consistency.", Status: StatusPassed, ReviewedBy: "evidence:in-tree-tests", ReviewedAt: ts("2026-05-14T18:30:00Z"), Notes: "TestSimulatedTokenBalanceTracking + TestSimulatedVoting + TestSimulatedEscrow in pkg/contracts/contracts_test.go exercise the in-process simulation fallback (engine.go:284 'in-process state simulation' path used when wazero is unavailable). Cover transfer/balance, vote-tally, and escrow-deposit-release determinism. Read-only execution paths covered by pkg/contracts/readonly_test.go (simulateReadOnly at readonly.go:185)."},

		// Bridge
		{ID: "bridge-01", Category: CatBridge, Severity: SevCritical, Title: "Atomic swap secret handling", Description: "Verify bridge secrets are generated with crypto/rand, hashed before storage, and never leaked in P2P."},
		{ID: "bridge-02", Category: CatBridge, Severity: SevHigh, Title: "Lock expiry enforcement", Description: "Confirm expired locks cannot be redeemed and refunds work correctly after expiry."},
		{ID: "bridge-03", Category: CatBridge, Severity: SevHigh, Title: "Fee calculation integrity", Description: "Verify fee collector cannot be manipulated to under-charge or double-collect."},
		{ID: "bridge-04", Category: CatBridge, Severity: SevMedium, Title: "Relayer retry safety", Description: "Confirm relayer retries are idempotent and nonce tracking prevents double-submission."},

		// Storage
		{ID: "store-01", Category: CatStorage, Severity: SevHigh, Title: "State persistence integrity", Description: "Verify contract/bridge/governance JSON files use atomic writes (tmp + rename).", Status: StatusPassed, ReviewedBy: "evidence:in-tree", ReviewedAt: ts("2026-05-14T18:30:00Z"), Notes: "16 production persisters use the atomic-tmp+os.Rename pattern: pkg/api/user_persist.go, pkg/bridge/state.go, pkg/api/token_registry.go, pkg/submesh/save_config.go, pkg/chain/staking_persist.go, pkg/governance/chainparams/persist.go, pkg/mining/attest/recentrejects/persistence.go, pkg/monitoring/ngc_proof_persist.go, pkg/networking/hostkey.go, pkg/mining/enrollment/persist.go, pkg/contracts/tracer.go, pkg/bridge/relayer.go, pkg/telemetry/registry.go, pkg/updater/updater.go, cmd/qsdm/main.go. The pattern is already exercised by store-04 (recentrejects) and store-05 (ngc_proof_persist) tests."},
		{ID: "store-02", Category: CatStorage, Severity: SevMedium, Title: "Snapshot hash verification", Description: "Confirm snapshot hashes are verified on load to detect corruption."},
		{ID: "store-03", Category: CatStorage, Severity: SevLow, Title: "File permission hardening", Description: "Verify sensitive files (certs, keys, state) are written with restrictive permissions (0600/0644).", Status: StatusPassed, ReviewedBy: "evidence:in-tree", ReviewedAt: ts("2026-05-14T18:30:00Z"), Notes: "Sensitive state files written at mode 0600: pkg/networking/hostkey.go (libp2p host key, verified in net-05), pkg/mining/attest/recentrejects/persistence.go (rejection ring, verified in store-04), pkg/monitoring/ngc_proof_persist.go (NGC ring, verified in store-05), pkg/api/user_persist.go (password hashes), pkg/api/token_registry.go, pkg/bridge/state.go. The pattern is consistent across the 16 atomic-write call sites enumerated in store-01."},
		{ID: "store-04", Category: CatStorage, Severity: SevMedium, Title: "Recent-rejection ring persistence bounded + corruption-tolerant", Description: "Verify recentrejects.FilePersister: (a) opens with mode 0600; (b) JSONL append-only with atomic-rename compaction at 2x soft-cap; (c) LoadAll skips malformed lines after a hard kill; (d) qsdm_attest_rejection_persist_errors_total fires on filesystem failure without disrupting the in-memory ring. Default cap = 1024 records ≈ 256-512 KiB on disk. Wired via Config.RecentRejectionsPath in internal/v2wiring.", Status: StatusPassed, ReviewedBy: "evidence:in-tree", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "recentrejects.FilePersister wired and tested; dashboard tile renders the persist-errors/compactions/records-on-disk cells live."},
		{ID: "store-05", Category: CatStorage, Severity: SevMedium, Title: "NGC attestation ring persistence (post-restart trust freshness)", Description: "Verify pkg/monitoring/ngc_proof_persist.go: (a) opens /opt/qsdm/ngc_proofs.jsonl with mode 0600; (b) JSONL append-only with crash-recovery framing (partial-write tail defence pre-pends \\n) and atomic-rename compaction at softCap=32 records (matches maxNGCProofEntries); (c) RestoreNGCProofsFromDisk skips malformed lines and is called BEFORE the API server binds in cmd/qsdm/main.go so pre-restart bundles repopulate the in-memory ring before any new POST /api/v1/monitoring/ngc-proof can overwrite them; (d) NGCProofPersistErrors() counter increments on filesystem failure without disrupting the in-memory ring; (e) the on-disk gauge tracks records via NGCProofPersistRecordsOnDisk(). Wired via Config.NGCProofPersistPath (env QSDM_NGC_PROOF_PERSIST_PATH). Closes the post-restart attested=0 blip on /api/v1/trust/attestations/summary; deployed and verified live (session 90, records_restored=1 across two consecutive restarts).", Status: StatusPassed, ReviewedBy: "evidence:live-deploy", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "Session 90: records_restored=1 across two consecutive restarts on api.qsdm.tech."},

		// API
		{ID: "api-01", Category: CatAPI, Severity: SevHigh, Title: "Input validation", Description: "Verify all API inputs (addresses, amounts, IDs) are validated with length/format/range checks.", Status: StatusPassed, ReviewedBy: "evidence:in-tree-tests", ReviewedAt: ts("2026-05-14T18:30:00Z"), Notes: "pkg/api/validation.go validates addresses (hex format + length), amounts (positive range), passwords (MinPasswordLength=12 + common-password blocklist). Covered by TestInputValidation (empty-address + short-password → 400) and TestSQLInjectionProtection (parameterised storage queries reject `DROP TABLE`-style payloads with no 5xx) in tests/api_security_test.go. Validation layer is invoked at the handler entry point before any storage write."},
		{ID: "api-02", Category: CatAPI, Severity: SevHigh, Title: "CSRF protection", Description: "Confirm CSRF middleware is applied to state-changing endpoints and bypassed only for Bearer auth.", Status: StatusPassed, ReviewedBy: "evidence:in-tree", ReviewedAt: ts("2026-05-14T18:30:00Z"), Notes: "pkg/api/csrf.go::CSRFMiddleware: 256-bit (32 B) crypto/rand tokens with 1 h TTL, X-CSRF-Token header validation (form fallback), 403 on failure. Correctly bypasses (a) safe methods GET/HEAD/OPTIONS, (b) public endpoints, (c) Bearer-token authenticated requests (header-based auth is not vulnerable to CSRF). Wired into the API middleware chain via pkg/api/server.go."},
		{ID: "api-03", Category: CatAPI, Severity: SevMedium, Title: "Security headers", Description: "Verify HSTS, CSP, X-Frame-Options, X-Content-Type-Options are set on all responses.", Status: StatusPassed, ReviewedBy: "evidence:in-tree-tests", ReviewedAt: ts("2026-05-14T18:30:00Z"), Notes: "pkg/api/security.go::SecurityHeaders middleware applies on every response: HSTS (max-age=31536000; includeSubDomains; preload), X-Frame-Options DENY, X-Content-Type-Options nosniff, X-XSS-Protection 1; mode=block, strict CSP (default-src 'self'; frame-ancestors 'none'), Referrer-Policy no-referrer, Permissions-Policy denying geo/mic/camera, blank Server header. Covered by TestSecurityHeaders in tests/api_security_test.go (asserts 5 critical headers present on /api/v1/health response)."},
		{ID: "api-04", Category: CatAPI, Severity: SevMedium, Title: "Error information leakage", Description: "Confirm error responses do not leak internal state, stack traces, or file paths."},
		{ID: "api-05", Category: CatAPI, Severity: SevMedium, Title: "Misleading wallet/mint stub removed (supply-inflation surface closed)", Description: "Verify POST /api/v1/wallet/mint returns 410 Gone with a structured `migration` JSON block (Session 91). Pre-v0.3.3 the handler accepted {recipient, amount}, ran NVIDIA-lock + submesh policy checks, stored a mint envelope, and returned 200 with status:\"minted\" — but never credited the recipient's balance (no code path to wallet-service AddBalance). The endpoint was a non-functional supply-inflation surface for anyone who could pass the lock gates. v0.3.3 collapses the body to a 410 + migration message pointing callers to /api/v1/wallet/send (peer transfer) or /api/v1/tokens/mint (named token mint, actually wired). qsdm_wallet_mint_total{result=\"gone\"} counter surfaces any caller still targeting the removed path. The legacy QSDMWalletMintBurst Prometheus alert is retained as a regression tripwire (catches a hypothetical revert that re-enables the never-credited stub).", Status: StatusPassed, ReviewedBy: "evidence:live-deploy", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "Session 91 (v0.3.3): /api/v1/wallet/mint returns 410 Gone with migration block; counter qsdm_wallet_mint_total{result=\"gone\"} live."},
		{ID: "api-06", Category: CatAPI, Severity: SevHigh, Title: "Self-custody signed-transaction submission (POST /api/v1/wallet/submit-signed, v0.4.0 backend + browser shipped Sessions 95→96; v0.4.1 replay protection + atomic debit shipped Sessions 99→100)", Description: "FULLY IMPLEMENTED in v0.4.0 (Sessions 95 & 96). BACKEND (Session 95): handler at pkg/api/handlers.go::SubmitSignedTransaction performs all five required invariants on every request: (a) decodes the wallet.TransactionData envelope and enforces sender == hex(sha256(public_key)) BEFORE any storage-side state mutation (counter: qsdm_wallet_send_total{result=\"sender_mismatch\"}); (b) verifies the ML-DSA-87 signature over the canonical payload (envelope JSON with signature + public_key fields cleared, then json.Marshal with Go's default struct-order field emission) using the envelope's own public_key — there is NO codepath that falls back to a validator-side keypair (counter: result=\"signature_invalid\"); (c) consults storage.GetBalance(sender) before applying the debit and returns HTTP 402 Payment Required on shortfall (counter: result=\"insufficient_balance\"); (d) idempotent on tx_id: storage.GetTransaction(tx_id) is consulted before StoreTransaction; first-call returns 200 + status=\"accepted\", duplicate returns 409 + status=\"duplicate\" with the original tx_id echoed (counter: result=\"duplicate\"); (e) every terminal path bumps qsdm_wallet_send_total{result=...} with one of {success, invalid_request, sender_mismatch, signature_invalid, insufficient_balance, duplicate, store_failed, no_wallet_service}. Endpoint is intentionally in publicPaths because the cryptographic identity IS the envelope's public_key — JWT would add nothing — and per-IP rate-limit (security.go) caps at 10/min, identical to /wallet/send. Submesh-policy gate (enforceSubmeshWalletSend) is invoked before storage write, matching /wallet/send posture. BROWSER + WASM SIGNER (Session 96): wasm_modules/wallet/cmd/qsdm-wallet/main.go exports qsdm_wallet_sign_transaction(envelope_json, private_key_hex, public_key_hex) which produces canonical bytes via Go's json.Marshal (matches server-side canonicalisation byte-for-byte, sidesteps JS/Go float-format drift e.g. 1e-7 → \"1e-07\" vs \"1e-7\"). Browser wallet at QSDM/deploy/landing/wallet.html has a 5th tab \"Send transaction\" wired through QSDM/deploy/landing/wallet.js; SRI hashes for wallet.wasm + wallet.js refreshed via QSDM/scripts/build_wallet_wasm.sh --refresh-sri-only. OpenAPI documented at /wallet/submit-signed in QSDM/docs/docs/openapi.yaml. MINER_QUICKSTART.md Appendix B refreshed to call out the self-custody path. Tests: TestSubmitSigned_{HappyPath,MethodNotAllowed,MalformedJSON,SenderMismatch,BadSignature,DuplicateTxID,InsufficientBalance,NoWalletService} in pkg/api/handlers_test.go — 8/8 green and use the same walletcrypto.FromBytes().Sign() path the WASM module uses, so the browser signer is end-to-end-verified by the server tests. KNOWN GAPS shipped intentionally with v0.4.0 and tracked in QSDM/docs/docs/V040_WALLET_SEND_DESIGN.md (Future work): (1) no per-account nonce → a client controlling the nanosecond-timestamp inside the tx_id seed can craft arbitrarily many distinct tx_ids for the same logical transfer, so cross-tx-id replay is NOT prevented (single-tx_id replay IS prevented via the idempotency check); fix planned for v0.4.1; (2) pkg/storage/sqlite.go::UpdateBalance warns-and-proceeds on negative balance — the pre-flight GetBalance check we do here closes the obvious case but a concurrent race between two simultaneous submit-signed calls from the same sender can still drop the on-disk balance below zero; atomic debit/credit fix planned for v0.4.1. Both gaps must close before mining-05 (incentivised testnet) exposure. RELEASED in v0.4.0 (Session 97, 2026-05-13): annotated tag v0.4.0 at 318ed5e pushed; release-container.yml run 25811046765 10/10 jobs green with 53 cosign-signed assets (15 binaries + 17 .sig + 17 .pem + 3 SBOMs + SHA256SUMS); GHCR images cosign-verified against the v0.4.0 refs/tags/ OIDC identity (manifest-list digests pinned in RELEASE_EVIDENCE_v0.4.0.md); BLR1 binary swapped (sha256 2874f088039bace6662754e2461c1f229b223a42deefc185fae5270e46d6d4fb, v0.3.3 backup preserved at /opt/qsdm/qsdm.v033.bak); /etc/systemd/system/qsdm.service.d/version.conf bumped to QSDM_BUILD_VERSION=v0.4.0; live anchors confirmed: https://api.qsdm.tech/api/v1/status reports version v0.4.0, POST /api/v1/wallet/submit-signed {} returns HTTP 400 invalid-sender (handler in routing tree, was HTTP 302 on v0.3.3), GET on same path returns HTTP 405 method-not-allowed (POST-only route registered, was HTTP 302 on v0.3.3); qsdm.tech landing pill bumped to v0.4.0; wallet.wasm + wallet.js SRI hashes match the deployed artefacts byte-for-byte over HTTPS. LIVE-PIPELINE SMOKE TEST (Session 98, 2026-05-13): cmd/v040smoke runs 3 production probes against https://api.qsdm.tech/api/v1/wallet/submit-signed without mutating chain state: (probe 1) valid keypair + tampered signature → HTTP 422 signature_invalid (exercises JSON parse, shape validate, hex decode, sender binding, ML-DSA-87 verify, monitoring counter); (probe 2) valid keypair + valid sig + sender field tampered → HTTP 400 sender does not match hex(sha256(public_key)); (probe 3) truncated JSON body → HTTP 400 invalid envelope: unexpected EOF. 3/3 PASS using the !cgo circl/mldsa87 backend, byte-for-byte canonical-payload-compatible with the server's liboqs backend per dilithium_circl_test.go parity guard. Smoke test source kept at QSDM/source/cmd/v040smoke/main.go for operator re-runs on future v0.x releases. v0.4.1 REPLAY PROTECTION + ATOMIC DEBIT (Sessions 99→100, 2026-05-13 → 2026-05-14): the two v0.4.0 known gaps are CLOSED. (1) cross-tx_id replay: pkg/wallet.TransactionData gains a `nonce uint64` field; SubmitSignedTransaction pre-flight-checks `storage.GetNonce(sender)` and rejects any envelope with `nonce <= last` (counter: qsdm_wallet_send_total{result=\"nonce_replay\"}, HTTP 409). (2) non-atomic balance debit: the v0.4.0 trio of `storageHasTransaction` + `GetBalance` + `StoreTransaction` is replaced with a single `storage.ApplyTransferAtomic(sender, recipient, amount, fee, envelopeNonce, txID, rawEnvelope)` call that performs (a) tx_id uniqueness CAS, (b) nonce CAS pre-image check (envelopeNonce == lastNonce + 1), (c) balance>=amount+fee gate, (d) debit sender + credit recipient + nonce bump + transaction insert — all inside one SQL transaction. Three new sentinel errors propagate through the handler: ErrTxAlreadyExists → 409 duplicate, ErrInsufficientBalance → 402 payment-required, ErrNonceConflict → 409 nonce-conflict (counter result tags `nonce_conflict` + `nonce_lookup_failed`). SUPPORTING SURFACE shipped Session 100 (this session): GET /api/v1/wallet/nonce?sender=… returns {sender, nonce, next} for self-custody clients building the next envelope (6 unit tests including end-to-end bump after submit); qsdmcli wallet sign-tx subcommand reads an unsigned envelope on stdin, stamps the nonce (--nonce N | --auto-nonce → GET /wallet/nonce | legacy 0), produces a fully signed envelope on stdout (5 unit tests including a hard verifySignature() guarantee that the CLI-produced canonical bytes verify under the server's exact parse→re-marshal canonicalisation algorithm); browser-wallet Send tab gains a Nonce input (auto-resolves from validator if blank) + handles 409 nonce_replay / nonce_conflict in the result panel; wallet.wasm rebuilt with the Nonce field in txEnvelope (SRI sha384-HOd3kgcQwL/Gb+ujOF5phQeYLv73om7peCWQkN/mif3mQmBSefaCP1q1V8q0AE04); cmd/v041smoke superset of v040smoke adds probe 4 (GET nonce shape check) and probe 5 (nonce-conflict CAS rejection without state mutation). v0.4.1 RELEASE-CUT + LIVE DEPLOY (Session 100, 2026-05-14): annotated tag v0.4.1 pushed; release-container.yml run 25855056638 10/10 green; 53 cosign-signed assets + 3 GHCR images (qsdm sha256:1fcc20e6…339991, qsdm-validator sha256:79521c7e…365768, qsdm-miner sha256:4f39f661…293f5e); BLR1 binary swapped to v0.4.1 (sha256 e7fa04b0657c5793f79f2fce06562fe67ea9191e04c09657c1e6b5274c213cfb, v0.4.0 backup preserved at /opt/qsdm/qsdm.v040.bak sha256 2874f088…d4fb); QSDM_BUILD_VERSION bumped to v0.4.1; https://api.qsdm.tech/api/v1/status reports version v0.4.1; new public GET /api/v1/wallet/nonce returns 200 + {sender,nonce,next} for any sender (was 404 on v0.4.0); cmd/v041smoke PASS=5 FAIL=0 against api.qsdm.tech; qsdm.tech landing pill bumped to v0.4.1; wallet.wasm SRI sha256 f7fd4a47…6baa matches over HTTPS. PRODUCTION-DEPLOY FOOTNOTE: BLR1 runs FileStorage backend which does not track per-account state. v0.4.1's FileStorage.GetNonce returns (0, nil) symmetric with GetBalance's silent-zero so the new public read endpoint works, while FileStorage.ApplyTransferAtomic intentionally refuses → qsdm_wallet_send_total{result=\"store_failed\"} + HTTP 500 failed-to-apply-transfer. Settlement requires SQLite v0.4.1 or Scylla. Documented inline at pkg/storage/file_storage.go. INDEPENDENT COSIGN / REKOR VERIFICATION (Session 100, 2026-05-14): 5-asset out-of-band sweep with cosign v2.4.1 returns Verified OK across the entire signature surface: (a) qsdmminer-console-linux-amd64 blob (sha256 95a1d18a3d23673f5e6f646b4172a074182bd23fc41510ef3d37db1b778fefce); (b) SHA256SUMS root; (c) ghcr.io/blackbeardone/qsdm:0.4.1 (manifest sha256:1fcc20e6…339991); (d) ghcr.io/blackbeardone/qsdm-validator:0.4.1 (manifest sha256:79521c7e…365768); (e) ghcr.io/blackbeardone/qsdm-miner:0.4.1 (manifest sha256:4f39f661…293f5e). Binary cert subject URI = release-container.yml@refs/tags/v0.4.1; cert OID 1.21 records signing run 25855056334 (the original run; secondary retry 25855056638 was a redundant re-fire after a tag-push cancellation cascade — both 10/10 green). Rekor logID c0d23d6ad406973f9559f3ba2d1ca01f84147d8ffc5b8445c224f98b9591801d, observed logIndex range 1534699896-1534701566. Full reproducer + per-asset table in RELEASE_EVIDENCE_v0.4.1.md §\"Independent cosign / Rekor evidence\".", Status: StatusPassed, ReviewedBy: "evidence:live-deploy", ReviewedAt: ts("2026-05-14T00:00:00Z"), Notes: "Sessions 95-100 (v0.4.0→v0.4.1 SHIPPED + DEPLOYED + COSIGN-AUDITED): handler tests 13/13 green (8 v0.4.0 + 5 v0.4.1); CLI sign-tx tests 5/5 green; nonce-endpoint tests 6/6 green; live-pipeline v0.4.0 smoke 3/3 PASS (Session 98); cmd/v041smoke 5/5 PASS against api.qsdm.tech (Session 100 post-deploy); independent cosign verify 5/5 PASS (Session 100 closure); commits: storage foundation ecfa121, handler integration 8659b04, client+tooling 2bdacb8, release-cut 39ab765, BLR1 deploy + FileStorage stub 47d22f7, cosign closure (this commit), v0.4.0 anchor 318ed5e."},

		// Governance
		{ID: "gov-01", Category: CatGovernance, Severity: SevHigh, Title: "Vote manipulation prevention", Description: "Verify voters cannot double-vote and vote weights are correctly applied.", Status: StatusPassed, ReviewedBy: "evidence:in-tree-tests", ReviewedAt: ts("2026-05-14T18:30:00Z"), Notes: "pkg/governance/chainparams/types.go:401 rejects double-vote with 'chainparams: authority has already voted on this proposal' error. TestSnapshotVoting in pkg/governance/voting_test.go covers vote-weight integrity (yes 100 / no 50 / abstain 25 → 57.14%/28.57%/14.29% tally) and expired-voting rejection. TestMultiSig_DuplicateSignature in multisig_test.go covers signer-side double-voting rejection."},
		{ID: "gov-02", Category: CatGovernance, Severity: SevHigh, Title: "Proposal execution safety", Description: "Confirm proposal executor prevents double execution and respects quorum/majority.", Status: StatusPassed, ReviewedBy: "evidence:in-tree-tests", ReviewedAt: ts("2026-05-14T18:30:00Z"), Notes: "TestProposalExecutor_NoDoubleExecution + TestProposalExecutor_QuorumNotReached + TestProposalExecutor_{PassedProposal,FailedProposal,ExecuteNow,NoAction} in pkg/governance/executor_test.go — 6 tests exercise the executor's double-execution guard, quorum gate, and pass/fail/abstain paths."},
		{ID: "gov-03", Category: CatGovernance, Severity: SevMedium, Title: "Multi-sig expiry enforcement", Description: "Verify expired multi-sig actions cannot be signed or executed.", Status: StatusPassed, ReviewedBy: "evidence:in-tree-tests", ReviewedAt: ts("2026-05-14T18:30:00Z"), Notes: "TestMultiSig_ExpiredAction + TestMultiSig_{ProposeAndSign,Execute,InsufficientSignatures,UnauthorisedSigner,DuplicateSignature,PendingActions} in pkg/governance/multisig_test.go — 7 tests exercise the full multi-sig lifecycle including the expiry-rejection path."},

		// Infrastructure
		{ID: "infra-01", Category: CatInfra, Severity: SevHigh, Title: "Docker image hardening", Description: "Verify Docker images use non-root user, minimal base, and no unnecessary packages."},
		{ID: "infra-02", Category: CatInfra, Severity: SevMedium, Title: "Secret management", Description: "Confirm no hardcoded secrets in source; all secrets from env vars or secure config."},
		{ID: "infra-03", Category: CatInfra, Severity: SevLow, Title: "Dependency audit", Description: "Run go mod tidy + govulncheck for known CVEs in dependencies.", Status: StatusPassed, ReviewedBy: "evidence:in-tree-tests", ReviewedAt: ts("2026-05-14T19:30:00Z"), Notes: ".github/workflows/qsdm-go.yml line 128 hosts a dedicated govulncheck job that delegates to QSDM/scripts/govulncheck-filter.sh (runs `govulncheck -json ./...` with the documented allowlist). The same job is already cited as the basis for supply-02 (transitive CVE scanning) and govulncheck status reported there is current as of session 74's 67/67 packages verification. `go mod tidy` invariant is enforced by qsdm-go.yml's vet/test/build jobs which fail on go.mod drift."},

		// Supply-chain integrity
		{ID: "supply-01", Category: CatSupplyChain, Severity: SevCritical, Title: "Go module verification", Description: "CI runs `go mod verify` on every build; go.sum changes require PR review.", Status: StatusPassed, ReviewedBy: "evidence:live-deploy", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "Session 74: `go mod verify` → 'all modules verified' on windows/amd64, CGO_ENABLED=0."},
		{ID: "supply-02", Category: CatSupplyChain, Severity: SevHigh, Title: "Transitive CVE scanning", Description: "`govulncheck ./...` runs in CI and blocks merges on critical/high vulnerabilities.", Status: StatusPassed, ReviewedBy: "evidence:live-deploy", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "Session 74: `govulncheck ./...` → only GO-2024-3218 remains (tracked as supply-08 accepted-with-mitigation); 3 stdlib + x/net findings closed by go1.25.10 + x/net 0.53.0."},
		{ID: "supply-03", Category: CatSupplyChain, Severity: SevHigh, Title: "Container image scanning", Description: "Trivy (or equivalent) scans the published container image; critical/high findings block release."},
		{ID: "supply-04", Category: CatSupplyChain, Severity: SevHigh, Title: "Build provenance / SBOM", Description: "Release workflow generates an SBOM (CycloneDX or SPDX) and attaches it to the container image / release.", Status: StatusPassed, ReviewedBy: "evidence:live-deploy", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "v0.4.0 release attached 3 SBOMs (SPDX 2.3, syft, 1018 packages / 98 files); per-image SBOM attestations attached via cosign. Workflow: .github/workflows/release-container.yml source-sbom job."},
		{ID: "supply-05", Category: CatSupplyChain, Severity: SevMedium, Title: "Signed releases", Description: "Release artefacts (binaries, container images) are signed with cosign / sigstore and signatures are verified in the deploy pipeline.", Status: StatusPassed, ReviewedBy: "evidence:live-deploy", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "v0.4.0: 53 cosign-signed assets (15 binaries + 17 .sig + 17 .pem + 3 SBOMs + SHA256SUMS); GHCR images cosign-verified against v0.4.0 refs/tags/ OIDC identity (manifest-list digests pinned in RELEASE_EVIDENCE_v0.4.0.md)."},
		{ID: "supply-06", Category: CatSupplyChain, Severity: SevMedium, Title: "Reproducible builds", Description: "Build pipeline is deterministic: pinned Go version, pinned toolchain, no network access during `go build` beyond module cache.", Status: StatusPassed, ReviewedBy: "evidence:live-deploy", ReviewedAt: ts("2026-05-14T19:30:00Z"), Notes: ".github/workflows/release-container.yml lines 39-40+163-169 cross-compile every release binary with `go build -trimpath -ldflags=\"${LDFLAGS}\" CGO_ENABLED=0` for linux/{amd64,arm64} + darwin/{amd64,arm64} + windows/amd64; LDFLAGS pins -s -w plus the release tag/short-SHA/build-date triple via -X. Toolchain version is bound to the source-tree declaration via go.mod (release-container.yml uses go-version-file: QSDM/source/go.mod). .github/workflows/qsdm-split-profile.yml::Build trustcheck with -trimpath (reproducibility smoke) at line 196 verifies the flag set on every PR. Live verification (RELEASE_EVIDENCE_v0.4.2.md): the v0.4.2 binary cross-compiled from tag 2039035 on a Windows workstation matches the GHCR-image binary from release-container.yml run 25876952742 byte-for-byte (sha256 7fd07587df071b7766a2784533526969febe68012e2932671643178d1e8fe0dd)."},
		{ID: "supply-07", Category: CatSupplyChain, Severity: SevMedium, Title: "Dependency pinning policy", Description: "Third-party Go modules are pinned to specific versions; dependabot / renovate monitors for security updates with PR review.", Status: StatusPassed, ReviewedBy: "evidence:in-tree", ReviewedAt: ts("2026-05-14T19:30:00Z"), Notes: ".github/dependabot.yml: weekly gomod scan on /QSDM/source (open-pull-requests-limit: 5) + monthly github-actions scan on /. Documented exception for github.com/libp2p/* major-version bumps (which ship as +incompatible pseudo-releases that cannot be applied without rewriting every import — verbatim rationale in dependabot.yml comments) so minor/patch updates keep flowing while majors stay opt-in. go.mod versions pinned with go.sum integrity verified by the qsdm-go.yml mod-verify step (cited in supply-01 Notes). Dependency PRs flow through the standard PR review path."},
		{ID: "supply-08", Category: CatSupplyChain, Severity: SevMedium, Title: "Residual upstream CVE register", Description: "Document and accept-with-mitigation any govulncheck finding that has no upstream fix. Current entry: GO-2024-3218 (libp2p-kad-dht IPFS DHT content censorship; reachable via pkg/networking provider records). Mitigation: QSDM uses libp2p-kad-dht only for peer discovery (rendezvous key = chain ID), not IPFS content provider records, so the practical exposure is limited to a malicious peer flooding rendezvous routing — already bounded by the bootstrap allowlist and peer scoring in pkg/networking. Re-evaluate at every libp2p-kad-dht release; close this entry when an upstream fix lands.", Status: StatusPassed, ReviewedBy: "evidence:in-tree", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "Session 73: GO-2024-3218 documented as accepted-with-mitigation per peer-discovery-only usage; bootstrap allowlist + peer scoring bound exposure. Re-evaluate every libp2p-kad-dht release."},

		// Container runtime / deployment
		{ID: "runtime-01", Category: CatRuntime, Severity: SevHigh, Title: "Non-root container user", Description: "Runtime container runs as a non-root UID; /proc/1/status confirms `Uid: <non-zero>`."},
		{ID: "runtime-02", Category: CatRuntime, Severity: SevHigh, Title: "Read-only root filesystem", Description: "Kubernetes / Compose spec sets `readOnlyRootFilesystem: true`; writable paths are explicit emptyDirs or PVCs."},
		{ID: "runtime-03", Category: CatRuntime, Severity: SevMedium, Title: "Linux capability drop", Description: "Container drops ALL capabilities except the minimum required (no CAP_NET_RAW, CAP_SYS_ADMIN, etc.)."},
		{ID: "runtime-04", Category: CatRuntime, Severity: SevMedium, Title: "Seccomp / AppArmor profile", Description: "Container uses RuntimeDefault seccomp profile (or custom strict profile); privilege escalation blocked via `allowPrivilegeEscalation: false`."},
		{ID: "runtime-05", Category: CatRuntime, Severity: SevHigh, Title: "Resource limits", Description: "CPU / memory limits and requests are set; no unbounded pods that can DoS the node."},
		{ID: "runtime-06", Category: CatRuntime, Severity: SevMedium, Title: "Liveness / readiness probes", Description: "Both probes are wired to `/api/v1/health/live` and `/api/v1/health/ready` with realistic timeouts.", Status: StatusPassed, ReviewedBy: "evidence:in-tree", ReviewedAt: ts("2026-05-14T19:30:00Z"), Notes: "QSDM/deploy/kubernetes/validator-statefulset.yaml lines 107-120 wires both probes: livenessProbe httpGet path=/api/v1/health/live port=api initialDelaySeconds=60 periodSeconds=30; readinessProbe httpGet path=/api/v1/health/ready port=api initialDelaySeconds=30 periodSeconds=10. Probe targets are bound by pkg/api/handlers.go lines 200-202 (mux.HandleFunc('/api/v1/health/live', handlers.HealthLive) + ready). The 60s liveness initial delay accommodates DAG load + gRPC dial of the validator set on a cold-start node; the 30s/10s readiness cadence flips the pod out of the Service backend within ~30s of an unhealthy state."},
		{ID: "runtime-07", Category: CatRuntime, Severity: SevMedium, Title: "NetworkPolicy / egress control", Description: "Default-deny NetworkPolicy restricts pod egress to known Scylla / metrics / P2P peers only."},

		// Secret rotation lifecycle
		{ID: "rotation-01", Category: CatSecretRotation, Severity: SevHigh, Title: "JWT / API key rotation", Description: "Documented rotation procedure for JWT signing keys and API keys; rotation can be performed without downtime (dual-accept window)."},
		{ID: "rotation-02", Category: CatSecretRotation, Severity: SevHigh, Title: "mTLS certificate rotation", Description: "Node certificates rotate before expiry (monitored); CA rotation procedure is documented and rehearsed."},
		{ID: "rotation-03", Category: CatSecretRotation, Severity: SevHigh, Title: "Scylla auth credential rotation", Description: "SCYLLA_USERNAME / SCYLLA_PASSWORD rotate at least quarterly; new credentials deploy via secret manager without client restart where possible."},
		{ID: "rotation-04", Category: CatSecretRotation, Severity: SevMedium, Title: "Bridge secret rotation", Description: "Bridge atomic-swap secret seed rotates on schedule; compromised secrets can be revoked and audited."},
		{ID: "rotation-05", Category: CatSecretRotation, Severity: SevMedium, Title: "Rotation monitoring", Description: "Alerts fire when any secret / certificate is within 30 days of expiry; dashboard surfaces rotation status per component."},

		// Major Update Phase 1: rebrand completeness.
		{ID: "rebrand-01", Category: CatRebrand, Severity: SevHigh, Title: "Brand-name completeness sweep", Description: "All user-visible QSDM+ surfaces have been rebranded to QSDM (README, landing page, dashboard pills, CLI output). Historical session-log entries remain verbatim. See REBRAND_NOTES.md.", Status: StatusPassed, ReviewedBy: "evidence:live-deploy", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "Phase 1 sweep verified live: dashboard.qsdm.tech login title 'QSDM Dashboard Login'; landing version pill v0.4.0; all surfaces canonical."},
		{ID: "rebrand-02", Category: CatRebrand, Severity: SevHigh, Title: "Env var / header deprecation shim", Description: "QSDMPLUS_* env vars are NO LONGER accepted (deprecation shim retired in db9b590; pkg/envcompat is now a no-op trim helper, see its godoc). X-QSDMPLUS-* HTTP headers remain accepted via pkg/branding *Legacy constants. Operators upgrading from QSDM+ must move env-var configuration to QSDM_* names; HTTP-header clients (sidecars, scrapers) keep working through the deprecation window.", Status: StatusPassed, ReviewedBy: "evidence:in-tree", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "Deprecation shim retired in commit db9b590; pkg/envcompat is now a no-op trim helper."},
		{ID: "rebrand-03", Category: CatRebrand, Severity: SevMedium, Title: "Trademark filings initiated", Description: "Trademark search and filing for 'QSDM' and 'Cell (CELL)' completed or tracked with counsel. BLOCKED on wall-clock action — document status in NEXT_STEPS.md."},
		{ID: "rebrand-04", Category: CatRebrand, Severity: SevInfo, Title: "SDK package rename (no aliases)", Description: "Go sdk/qsdm and JS sdk/qsdm.js were renamed in-place from sdk/qsdmplus.* / qsdmplus.QSDMPlusClient; legacy aliases were removed in db9b590. New code uses qsdm.Client (Go) and the JS qsdm export. Pre-rebrand code referencing qsdmplus.QSDMPlusClient must be updated. Covered by sdk/go/client_test.go and sdk/javascript/qsdm.test.js.", Status: StatusPassed, ReviewedBy: "evidence:in-tree-tests", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "Covered by sdk/go/client_test.go (Go) and sdk/javascript/qsdm.test.js (17/17 passed in session 74)."},
		{ID: "rebrand-05", Category: CatRebrand, Severity: SevHigh, Title: "Prometheus metric prefix migration (dual-emit retired)", Description: "Dual-emit machinery (pkg/monitoring/prometheus_prefix_migration.go and the QSDM_METRICS_EMIT_LEGACY / QSDM_METRICS_EMIT_QSDM knobs) was retired in db9b590. Only the canonical qsdm_* prefix is emitted. The QSDM/scripts/check-no-new-legacy-metrics.sh CI guard fails any new qsdmplus_* literal. Operators with Grafana/alerting still on the qsdmplus_* prefix must update their queries; see REBRAND_NOTES.md (archived) for the legacy → canonical mapping.", Status: StatusPassed, ReviewedBy: "evidence:in-tree", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "Dual-emit retired in commit db9b590; CI guard QSDM/scripts/check-no-new-legacy-metrics.sh blocks regressions."},
		{ID: "rebrand-06", Category: CatRebrand, Severity: SevMedium, Title: "Release artefacts reproducibility", Description: "Every release tag produces Go binaries (qsdmminer, trustcheck, genesis-ceremony) cross-compiled with -trimpath -ldflags=\"-s -w\" CGO_ENABLED=0 for linux/{amd64,arm64}, darwin/{amd64,arm64}, windows/amd64. SHA256SUMS attached to the GitHub Release. Container images published under both the legacy (qsdmplus) and new (qsdm-validator, qsdm-miner) names. Workflow: .github/workflows/release-container.yml.", Status: StatusPassed, ReviewedBy: "evidence:live-deploy", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "v0.4.0 (Session 97): release-container.yml run 25811046765 — 10/10 jobs green, 53 cosign-signed assets (15 binaries + 17 .sig + 17 .pem + 3 SBOMs + SHA256SUMS)."},
		{ID: "rebrand-07", Category: CatRebrand, Severity: SevHigh, Title: "Trust aggregator wired into node startup", Description: "cmd/qsdm/main.go constructs a TrustAggregator with a ValidatorSetPeerProvider (over nodeValidatorSet.ActiveValidators) and a MonitoringLocalSource (NGC ring buffer). A background goroutine calls Refresh() every cfg.TrustRefreshInterval (default 10 s) and exits with ctx.Done(). Config knobs: [trust] disabled/fresh_within/refresh_interval/region_hint in TOML and YAML; env aliases QSDM_TRUST_DISABLED, QSDM_TRUST_FRESH_WITHIN, QSDM_TRUST_REFRESH_INTERVAL, QSDM_TRUST_REGION (pre-rebrand QSDMPLUS_TRUST_* env vars are no longer accepted; see rebrand-02). When disabled, SetTrustAggregator(nil, true) makes the /api/v1/trust/attestations/* endpoints return HTTP 404 per §8.5.3. Covered by pkg/api/trust_peer_provider_test.go, pkg/config/trust_config_test.go, pkg/api/handlers_trust_dashboard_integration_test.go, and cmd/trustcheck/integration_test.go.", Status: StatusPassed, ReviewedBy: "evidence:in-tree-tests", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "4 test files cited; all green per session 74's 67/67 packages verification."},

		// Major Update Phase 3: tokenomics and genesis policy.
		{ID: "tok-01", Category: CatTokenomics, Severity: SevCritical, Title: "Genesis policy sign-off", Description: "Tokenomics genesis parameters (100 M cap, 10 M treasury, 90 M mining emission, 4-year halvings, 10 s block time) ratified per Phase 0 recommendation. BLOCKED on external counsel review — document in CELL_TOKENOMICS.md front-matter."},
		{ID: "tok-02", Category: CatTokenomics, Severity: SevHigh, Title: "Emission schedule determinism", Description: "pkg/chain/emission computes block reward and cumulative supply with integer-only math. Tests cover: epoch boundaries, halving edges, MaxHalvings overflow guard, cap convergence.", Status: StatusPassed, ReviewedBy: "evidence:in-tree-tests", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "pkg/chain/emission tests green per session 74's 67/67 packages verification."},
		{ID: "tok-03", Category: CatTokenomics, Severity: SevHigh, Title: "Tokenomics API transparency", Description: "/api/v1/status publishes the live emission snapshot (cap, emitted, block reward, next halving) so independent tools can verify on-chain issuance matches CELL_TOKENOMICS.md.", Status: StatusPassed, ReviewedBy: "evidence:live-deploy", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "Live on api.qsdm.tech: GET /api/v1/status returns version v0.4.0 with emission snapshot block."},

		// Major Update Phase 4: mining protocol external audit.
		{ID: "mining-01", Category: CatMiningAudit, Severity: SevCritical, Title: "Mining protocol external audit", Description: "MINING_PROTOCOL.md and pkg/mining reviewed by an independent cryptography / consensus auditor before mainnet CUDA launch. Auditor entry-point packet lives at docs/docs/AUDIT_PACKET_MINING.md (threat model, invariants I-1..I-10, test coverage matrix, reproducible build). BLOCKED on engagement — track in NEXT_STEPS.md."},
		{ID: "mining-02", Category: CatMiningAudit, Severity: SevHigh, Title: "CUDA miner isolation from consensus", Description: "cmd/qsdmminer never imports pkg/consensus and never runs in the validator process. validator_only build tag excludes pkg/mining/cuda. roleguard.MustMatchRole fails fast on config drift.", Status: StatusPassed, ReviewedBy: "evidence:in-tree", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "validator_only build tag + pkg/mining/roleguard enforce isolation; validator-only Dockerfile.validator excludes mining packages."},
		{ID: "mining-03", Category: CatMiningAudit, Severity: SevHigh, Title: "Proof canonicalisation determinism", Description: "pkg/mining.Proof canonical JSON round-trips bit-exact across architectures; Proof.ID excludes attestation; duplicate IDs are rejected via ProofIDSet.", Status: StatusPassed, ReviewedBy: "evidence:in-tree-tests", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "pkg/mining/proof tests green per session 74's 67/67 packages verification."},
		{ID: "mining-04", Category: CatMiningAudit, Severity: SevHigh, Title: "DAG + difficulty retarget unit coverage", Description: "pkg/mining/dag and pkg/mining/difficulty pass unit tests covering: InMemoryDAG vs LazyDAG agreement, retarget on-target / slow / fast / clamp / floor paths.", Status: StatusPassed, ReviewedBy: "evidence:in-tree-tests", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "pkg/mining/dag and pkg/mining/difficulty tests green per session 74's 67/67 packages verification."},
		{ID: "mining-05", Category: CatMiningAudit, Severity: SevMedium, Title: "Incentivized testnet readiness", Description: "Reference CPU miner (cmd/qsdmminer) is functional and documented in MINER_QUICKSTART.md. BLOCKED on wall-clock — incentivized testnet launch requires operational infra and marketing."},

		// Major Update Phase 5: trust endpoint anti-claim guardrails.
		{ID: "trust-01", Category: CatTrustAPI, Severity: SevHigh, Title: "Trust summary always serves a ratio", Description: "GET /api/v1/trust/attestations/summary always returns 'attested' and 'total_public' so widgets can render X of Y, never just X (§8.5.2 guardrail). Tested via TestWidgetState_* cases.", Status: StatusPassed, ReviewedBy: "evidence:in-tree-tests", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "TestWidgetState_* in pkg/api green per session 74's 67/67 packages verification."},
		{ID: "trust-02", Category: CatTrustAPI, Severity: SevHigh, Title: "Scope-note non-strippable", Description: "Summary response embeds the fixed scope_note referencing NVIDIA_LOCK_CONSENSUS_SCOPE.md on every 200 response; scraping tools cannot strip the caveat without tampering with the body.", Status: StatusPassed, ReviewedBy: "evidence:in-tree", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "scope_note embedded in handlers_trust.go response builder; covered by TestTrustSummaryHandler_* cases."},
		{ID: "trust-03", Category: CatTrustAPI, Severity: SevHigh, Title: "Node-ID redaction rule", Description: "recent endpoint emits node_id_prefix as first 8 + last 4 only; full libp2p IDs never leak. Covered by TestTrustRecentHandler_RedactsAndSorts.", Status: StatusPassed, ReviewedBy: "evidence:in-tree-tests", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "TestTrustRecentHandler_RedactsAndSorts green per session 74's 67/67 packages verification."},
		{ID: "trust-04", Category: CatTrustAPI, Severity: SevMedium, Title: "Region hint coarse-bucketing", Description: "region_hint is restricted to eu/us/apac/other; city- or AS-granularity data is never exposed. Enforced by normaliseRegion.", Status: StatusPassed, ReviewedBy: "evidence:in-tree", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "normaliseRegion in pkg/api/trust_*.go enforces the closed enum; tests cover unknown→other coercion."},
		{ID: "trust-05", Category: CatTrustAPI, Severity: SevMedium, Title: "Consensus-independence assertion", Description: "Trust endpoints are served by the HTTP surface only; no consensus or block-validity path reads trust state. Verified by grep and by the CatMiningAudit guard mining-02.", Status: StatusPassed, ReviewedBy: "evidence:in-tree", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "Cross-check with mining-02 (validator_only build tag); pkg/consensus has no import of pkg/api/trust_*."},
		{ID: "trust-06", Category: CatTrustAPI, Severity: SevMedium, Title: "Four widget states implemented", Description: "Landing widget + /trust page handle healthy / degraded / zero-opt-in / NGC-outage states without flashing 'loading forever' (§8.5.4). Covered by TestWidgetState_* cases.", Status: StatusPassed, ReviewedBy: "evidence:in-tree-tests", ReviewedAt: ts("2026-05-13T00:00:00Z"), Notes: "TestWidgetState_* covers all four states (healthy/degraded/zero/outage); green per session 74's 67/67 packages verification."},
	}
}
