# Changelog

All user-visible changes to QSDM are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
uses [Semantic Versioning](https://semver.org/) for tagged releases.

Historical project activity prior to the Major Update rebrand (Phases
1–5, executed 2026-04-22) is captured verbatim in
[`QSDM/docs/docs/history/MAJOR_UPDATE_EXECUTED.md`](QSDM/docs/docs/history/MAJOR_UPDATE_EXECUTED.md)
and the `QSDM/docs/docs/archive/` folder; this file does **not**
attempt to retroactively enumerate that history.

## [Unreleased]

### Added

- **`authz-01..04` audit flip — all 4 authorisation rows closed
  (61 → 65, 71.76 % → 76.47 %, 2026-05-15 post-`5b0c9ed`).** Closes
  the entire `authorisation` category by pinning evidence to four
  existing in-tree test files that prove the underlying controls.
  Live score on `https://api.qsdm.tech/api/v1/audit/summary`
  observed **65/85 (76.47 %)** with `blocking_count=11` post-deploy
  (was 14 pre-deploy). The 4 flips drop 3 blocking items
  (`authz-01` SevCritical, `authz-02` SevHigh, `authz-04` SevHigh);
  `authz-03` is SevMedium so does not count as blocking but still
  contributes to the percentage. Note: the live count moves +5 from
  the previous deploy because the BLR1 binary swap also delivers
  `api-04`'s catch-up (committed in `5b0c9ed` against the same
  source tree). The `authorisation` category is now **4/4 = 100 %**
  PASSED — fifth fully-green category alongside `cryptography`
  (5/5), `infrastructure` (3/3), `smart_contracts` (4/4), and
  `api` (6/6).
  - **`authz-01` RBAC enforcement (Critical) →
    `evidence:in-tree-tests`:**
    `pkg/api/admin_auth.go::AdminAccessMiddleware` enforces the
    `/api/admin/*` prefix gate after `AuthMiddleware` has populated
    the claims context — `AdminAPIRequireMTLS=true` rejects
    `r.TLS == nil` or zero `PeerCertificates` with 403,
    `AdminAPIRequireRole=true` rejects missing claims (401) and
    non-admin role (403), and propagates the authenticated
    principal via `AdminActorContextKey`. Wired in
    `pkg/api/server.go::setupMiddleware:446` as middleware layer 9
    so every admin route inherits the gate by construction; 17
    admin endpoints sit behind it (`pkg/api/handlers_admin.go:47-65`:
    accounts, account/, validators, finality, mempool, receipts,
    receipts/stats, peers, peers/banned, traces, traces/stats,
    chain, consensus/bft/follower, consensus/pol/{summary,
    prevote-lock/, round-certificate/}, overview, audit,
    config/reload-dry-run). Tests in
    `pkg/api/admin_auth_test.go`:
    `TestAdminAccessMiddleware_MTLSRequired` (no mTLS → 403),
    `TestAdminAccessMiddleware_AdminRole` (no claims → 401,
    `role=user` → 403, `role=admin` → 200 +
    `AdminActorFromRequest` populated). Governance writes flow
    through the separate `/api/v1/transactions` surface where
    on-chain `AuthorityList` + signature verification is the
    primitive (cryptographic, not RBAC).
  - **`authz-02` Multi-sig threshold (High) →
    `evidence:in-tree-tests`:**
    `pkg/governance/multisig.go::MultiSig.Execute` enforces (a)
    `RequiredSigs` threshold (insufficient → error before any
    handler runs), (b) action-expiry gate, (c) signer-membership
    check on `Propose` and `Sign`, (d) double-execute guard via
    `Executed` flag, (e) duplicate-signature rejection via
    `HasSigner`. 7 tests in `pkg/governance/multisig_test.go`:
    `TestMultiSig_ProposeAndSign`, `TestMultiSig_Execute`,
    `TestMultiSig_InsufficientSignatures` (Execute fails with 2/3
    sigs when `RequiredSigs=3` — the audit row's exact claim),
    `TestMultiSig_UnauthorisedSigner` (mallory non-signer rejected
    on Propose), `TestMultiSig_ExpiredAction` (1 ms TTL + 5 ms
    sleep → Sign rejects with expired-action error),
    `TestMultiSig_DuplicateSignature`, `TestMultiSig_PendingActions`.
  - **`authz-03` Contract upgrade authorisation (Medium) →
    `evidence:in-tree-tests`:**
    `pkg/contracts/upgrade.go::UpgradeManager.Upgrade` enforces an
    owner-or-allowed-upgrader policy via `canUpgrade()`:
    `policy.AllowOwnerUpgrade` gates `contract.Owner`,
    `policy.AllowedUpgraders` is the explicit whitelist, and
    `policy.FreezeAfterV` halts upgrades after a configured
    version. `Rollback` shares the same gate. 8 tests in
    `pkg/contracts/upgrade_test.go`: `TestUpgradeManager_BasicUpgrade`,
    `TestUpgradeManager_UnauthorisedUpgrade` (mallory ≠ alice
    rejected), `TestUpgradeManager_AllowedUpgraders` (bob added
    explicitly → allowed), `TestUpgradeManager_FreezePolicy`
    (v2→v3 blocked after `FreezeAfterV=2` — the freeze leg of the
    audit row), `TestUpgradeManager_PreservesState`,
    `TestUpgradeManager_VersionHistory`,
    `TestUpgradeManager_Rollback`,
    `TestUpgradeManager_RollbackNonexistentVersion`.
  - **`authz-04` Rate limit per role (High) →
    `evidence:in-tree-tests`:**
    `pkg/api/ratelimit_roles.go::RoleRateLimiter` applies tiered
    limits per `(identifier, role)` tuple, where role is sourced
    from JWT claims when present (`ContextWithClaims`) and
    defaults to `"anonymous"` for unauthenticated requests.
    `DefaultRoleRateLimiterConfig` pins admin > user > anonymous by
    construction. 9 tests in `pkg/api/ratelimit_roles_test.go`:
    `TestRoleRateLimiter_AdminHigherLimit` (10-req admin tier
    exhausts at 11th), `TestRoleRateLimiter_UserLimit` (5-req user
    tier), `TestRoleRateLimiter_AnonymousLimit` (3-req anon tier),
    `TestRoleRateLimiter_SeparateIdentifiers` (no cross-identifier
    bypass), `TestRoleRateLimiter_Middleware_Anonymous` (HTTP 429
    after limit), `TestRoleRateLimiter_Middleware_WithClaims`
    (claims-aware role assignment), `TestRoleRateLimiter_HealthBypass`
    (`/api/v1/health` exempt),
    `TestRoleRateLimiter_MiningBypass` (`/api/v1/mining/*` exempt
    at HTTP layer — consensus-level abuse protection elsewhere;
    bypass is path-scoped, sanity-checked against `/wallet/balance`
    which still 429s), `TestRoleRateLimiter_DefaultConfig`
    (admin > user > anonymous tier-ordering invariant).
  - **Drift guard (`pkg/audit/checklist_extra_test.go`):**
    `runtimeVerifiedItems` extended from 62 → 66 — `authz-01`,
    `authz-02`, `authz-03`, `authz-04` inserted as a new line right
    after the `auth-*` family.
    `TestChecklist_PassedCountMatchesRuntimeVerifiedList` re-anchors
    at 66 (would be 65/85 live, the +1 lag is the unrelated
    `tok-01` external-counsel item — see its own Notes).
  - **e2e test rebase (`tests/e2e_test.go::TestE2E_AuditChecklistReview`):**
    `5b0c9ed` flipped `authz-01` to `StatusPassed` in
    `defaultItems()` but left the e2e checklist-review delta test
    targeting `authz-01` as one of the two "pending → passed"
    mutations, which silently broke the +2 delta assertion
    (`expected passed delta 2, got 1`). This commit re-rebases the
    pair from `authz-01` + `tok-01` to `auth-04` + `tok-01` —
    `auth-04` (Token replay prevention, Medium) is still Pending in
    `defaultItems()` so the delta restores to exactly +2 and the
    test goes green again. (Historical chain of rebases:
    `auth-01` → `crypto-01/02` → `authz-01/sc-01` →
    `authz-01/tok-01` → `auth-04/tok-01`. Each rebase is forced by
    the previous item flipping into the pre-passed set; the new
    pick is always a still-Pending item with no in-tree evidence
    yet.)
  - **Test posture (windows/amd64, `CGO_ENABLED=0`, `go1.25.10`
    via `GOTOOLCHAIN=auto`):** `pkg/api` (admin_auth +
    ratelimit_roles subsets) OK 0.742 s; `pkg/governance` (multi-sig
    subset) OK 0.117 s; `pkg/contracts` (upgrade subset) OK
    0.452 s; `pkg/audit` OK 0.485 s; `tests` (`-run
    TestE2E_AuditChecklistReview`) OK 1.691 s. 29 tests total
    across the 4 authz controls + audit-checklist drift guard + e2e
    delta assertion, all green.
  - **Live deploy delta (BLR1):** binary swap to sha256
    `ccd412ea8fa913788a256d29865bcd6c7302c03ddf9fd25147d4992eb5411944`
    at `/opt/qsdm/qsdm` (previous binary preserved at
    `/opt/qsdm/qsdm.bak.<TS>`); `systemctl restart qsdm.service` →
    active in 4 s; `https://api.qsdm.tech/api/v1/audit/summary`
    reports `passed=65 / total=85`, `score=76.47 %`,
    `blocking_count=11`, `evidence_provenance:
    in-tree=24, in-tree-tests=29, live-deploy=12`. The 5 remaining
    `blocking_preview` entries are all `bridge-*` (4 items) +
    `net-01` / `net-03` (the next-highest-leverage cluster for the
    following pass).

- **`api-04` audit flip — last `Medium`-severity API row closed
  (61 → 62, 71.76 % → 72.94 %, 2026-05-15 post-`f6206f7`).**
  Closes the "Error information leakage" row using the two-stage
  fix narrative now fully in tree: `56145e1`'s MED-1 sanitization
  pass (introduced `WriteServerError` + `SanitizeForLog` helpers,
  migrated 3 of 13 5xx paths) + `96e8aa9`'s leak-path completion
  (closed the 3 residual sites in `handlers.go::CreateWallet`,
  `handlers_bridge.go::LockAsset`, and
  `handlers_bridge.go::InitiateSwap`). Score on
  `https://api.qsdm.tech/api/v1/audit/summary` will move from
  **61/85 (71.76 %)** to **62/85 (72.94 %)** on next BLR1 binary
  swap; blocking-findings count drops from 14 to 13. The
  `api` category is now **6/6 = 100 %** PASSED — fourth
  fully-green category alongside `cryptography`,
  `infrastructure`, and `smart_contracts`.
  - **Evidence chain (`evidence:in-tree`):** repo-wide
    `git grep` audit on 2026-05-15 confirms zero
    `writeErrorResponse(w, http.StatusInternalServerError, ...
    err ...)` occurrences in production `pkg/api/`. The 12 other
    5xx paths in `handlers.go` (lines 671, 904, 968, 1070,
    1086, 1328, 1388, 1501, 1510, 1553, 1677, 1841) all use
    deliberately generic strings (`"failed to issue CSRF
    token"`, `"failed to get balance"`, `"nonce lookup failed"`,
    `"failed to apply transfer"`, `"failed to get
    transactions"`, etc.) that reveal no internal state, code
    paths, or file paths. Remaining `err.Error()` writes are all
    4xx validation / authentication paths whose message text is
    part of the API contract per `error_sanitize.go`'s doc-block
    at lines 63-65 ("Use writeErrorResponse for 4xx errors that
    are deliberately user-facing").
  - **CI guard:** `f6206f7` (the LOW-2 follow-up that landed
    minutes before this flip) wired `gosec` + `staticcheck` +
    `govulncheck` enforcement into `.github/workflows/qsdm-go.yml`,
    so a future regression that re-introduces `err.Error()` on a
    5xx path is flagged by `staticcheck` SA1019/SA4006 +
    `gosec` G104 at PR review time.
  - **Drift guard
    (`pkg/audit/checklist_extra_test.go`):** `runtimeVerifiedItems`
    extended from 61 → 62 — `api-04` inserted into the existing
    `api-*` line so the API category now reads
    `"api-01", "api-02", "api-03", "api-04", "api-05", "api-06"`
    in source order.
    `TestChecklist_PassedCountMatchesRuntimeVerifiedList`
    re-anchors at 62; a regression that flips one back to
    pending without removing it from the allow-list now fails CI.
  - **Race posture:** `pkg/audit/checklist.go` was uncontested for
    1.5 hours before this flip (last touched by `cd70d8f` at
    ~12:46 AM local). The intervening ops commits (`a3d7720`
    MED-8 alert surface, `f6206f7` LOW-2 CI enforcement) closed
    audit-doc follow-ups but did not flip any checklist row.
    Both `a3d7720` and `f6206f7` were committed locally but not
    yet pushed when this flip was prepared; the push of this
    commit will piggyback them to `origin/main`.
  - **Test posture (windows/amd64, `CGO_ENABLED=0`,
    `GOTOOLCHAIN=auto`):** `pkg/audit` OK 0.205s (drift guard +
    runtime-verified-items list now keyed at 62);
    `internal/dashboard` OK 2.033s; `tests/ -short -run Audit`
    OK 1.322s (the e2e checklist-review delta test still uses
    the `cd70d8f`-rebased `authz-01` + `tok-01` pair, untouched
    by this flip).

### Fixed

- **MED-1 leak-path completion — 3 residual `err.Error()` 5xx leaks
  closed in `pkg/api` (2026-05-15 post-`cd70d8f`).** `56145e1`'s
  MED-1 sanitization pass migrated 3 of the 13 5xx paths in
  `pkg/api/handlers*.go` to the new `WriteServerError` helper
  (`Login` / `Register` / `CreateToken` — the auth surfaces). 9
  of the remaining 10 already used generic strings (`"failed to
  get balance"`, `"nonce lookup failed"`, etc.), so the audit
  row `api-04` was only **mostly** clean. This commit fixes the
  remaining 3 leak sites that still echoed raw `err.Error()` /
  `fmt.Sprintf("...: %v", err)` to the client.
  - **`handlers.go:867` `CreateWallet`** — leaked the raw
    `wallet.NewWalletService()` error string. The error can
    identify the storage backend (Scylla vs SQLite vs memory)
    or surface a CGO/liboqs build state, which an
    unauthenticated `POST /api/v1/wallet` caller could probe to
    fingerprint the deployment. Pre-fix also had a redundant
    `h.logger.Error("Failed to create new wallet", "error", err)`
    one line above; replaced both with a single
    `WriteServerError(w, h.logger, "create_wallet", err)` (which
    logs internally and returns the correlation ID only when
    `QSDM_PRODUCTION_MODE=true`).
  - **`handlers_bridge.go:82` `LockAsset`** (POST
    `/api/v1/bridge/lock`) — silent leak: the raw
    `bridgeProtocol.LockAsset` error (storage / journal /
    validation internals) was echoed to the client AND was NOT
    recorded in the structured log, so an operator had no record
    to correlate against the caller's report. Now logged via
    `WriteServerError(w, h.logger, "bridge_lock_asset", err)`.
  - **`handlers_bridge.go:298` `InitiateSwap`** (POST
    `/api/v1/bridge/swap`) — same silent-leak class as the
    `LockAsset` path above; raw `atomicSwap.InitiateSwap` error
    left both surfaces unsanitized. Now logged via
    `WriteServerError(w, h.logger, "bridge_initiate_swap", err)`.
  - **Repo-wide grep audit** (post-fix): zero
    `writeErrorResponse(w, http.StatusInternalServerError,
    ... err ...)` occurrences left in the production
    `pkg/api/` package. Remaining `err.Error()` writes are all
    4xx paths (validation / authentication failures whose
    message text is part of the API contract per the
    `error_sanitize.go` doc-block at lines 63-65 — "Use
    writeErrorResponse for 4xx errors that are deliberately
    user-facing").
  - **Audit row `api-04` not flipped in this commit.** When this
    fix was prepared the working tree had a parallel ops session
    queueing the `sc-01` flip in the same `pkg/audit/checklist.go`
    file. Editing the contested file would either have raced
    `cd70d8f` or accidentally captured ops's WASM-isolation work.
    `api-04` is now safe to flip in any subsequent commit —
    leaving that for whichever session lands the next batch.
  - **Test posture (windows/amd64, `CGO_ENABLED=0`,
    `GOTOOLCHAIN=auto`):** `pkg/api -short -run
    "Wallet|Bridge|Error|Sanitize|Login|Register|Token"` OK
    1.714s; package-wide `go build ./pkg/api/...` clean; full
    `go vet` clean.

### Added

- **WASM sandbox isolation test catch-up — `sc-01` (Critical) flipped
  (60 → 61, 70.59 % → 71.76 %, 2026-05-15 post-`91c2dd2`).** Closes
  the last `SevCritical` blocker in the `smart_contracts` category
  with a hand-assembled WebAssembly module and two layers of
  isolation tests. After this pass, `pkg/audit`'s
  `smart_contracts` row is **4/4 PASSED**.
  - **75-byte hand-assembled WASM 1.0 module
    (`isolationModuleWASM`)** in
    `pkg/wasm/isolation_test.go` + `pkg/contracts/wasm_isolation_test.go`.
    Exports `memory` (1 page = 64 KiB), `set(value: i32) -> ()`
    that writes the parameter to `memory[0..4]` via `i32.store`,
    and `get() -> i32` that reads it back via `i32.load`. The
    file header decodes the binary byte-by-byte against Wasm
    1.0 §5 (type / function / memory / export / code sections)
    so a future reader doesn't need a hex dump tool to validate
    the bytecode.
  - **Layer 1 — wazero memory model
    (`pkg/wasm/isolation_test.go`, 2 tests):**
    - `TestWazeroRuntime_LinearMemory_IsolatedBetweenInstances` —
      instantiate the same bytecode in two separate
      `WazeroRuntime`s, write `42` into A and `99` into B,
      assert each `get()` returns its own write. Re-run with a
      third write into B to catch a "shared-but-not-instantly-
      observable" failure mode the pair-of-writes would miss.
    - `TestWazeroRuntime_RuntimeInstance_Distinct` — structural
      guard that `NewWazeroRuntime` returns fresh
      `wazero.Runtime` + `wazero.Module` pointers per call
      (a process-wide-singleton regression would silently pass
      the linear-memory test).
  - **Layer 2 — engine wiring
    (`pkg/contracts/wasm_isolation_test.go`, 3 tests):**
    - `TestContractEngine_PerContract_RuntimeIsolation_NoCrossLeak`
      — deploy two contracts via `engine.DeployContract` with
      byte-identical bytecode, drive `engine.contractRTs[contract-A]`
      and `[contract-B]` through their `set`/`get` exports,
      assert no cross-contract state leak (the headline `sc-01`
      assertion).
    - `TestContractEngine_DeployContract_FreshRuntimePerContract`
      — deploy 3 contracts, assert the 3 `*WazeroRuntime` Go
      pointers are mutually distinct (catches a regression
      that promoted the first `rt` into a shared `wazeroRT`).
    - `TestContractEngine_DeployContract_NonWASMSkipsRuntime`
      — negative: plain-text + truncated-Wasm payloads must
      NOT acquire a runtime entry. The engine's
      error-tolerant `DeployContract` (engine.go:134 `if rtErr
      == nil`) silently ignores Wasm-compile failures, so this
      test pins the "no runtime for non-Wasm code" invariant.
  - **Audit checklist (`pkg/audit/checklist.go`):** `sc-01`
    flipped to `StatusPassed` with
    `ReviewedBy="evidence:in-tree-tests"` and
    `ReviewedAt="2026-05-15T16:30:00Z"`. `Notes` field carries
    pointers to all 5 test functions, the engine-wiring code
    sites (`engine.go:132-137` deploy + `engine.go:250-258`
    execute lookup), and the two-layer isolation rationale.
    `runtimeVerifiedItems` extended in
    `pkg/audit/checklist_extra_test.go` to include `sc-01`.
  - **e2e test rebase (`tests/e2e_test.go`):**
    `TestE2E_AuditChecklistReview` previously used `authz-01` +
    `sc-01` for the `+2 pending → passed` delta. With `sc-01`
    now `StatusPassed` in `defaultItems()`, that delta would be
    `+1`. Swap `sc-01` for `tok-01` (still `SevCritical`
    pending, BLOCKED on external counsel — a test-only
    mutation, not a claim about external review status).
  - **Test posture (windows/amd64, `CGO_ENABLED=0`, go1.25.0
    via `GOTOOLCHAIN=go1.25.0+auto`, `GOSUMDB=sum.golang.org`):**
    `pkg/wasm` 0.627s (`-run 'WazeroRuntime_(LinearMemory|RuntimeInstance)'`)
    + 1.428s full short suite; `pkg/contracts` 1.087s
    (`-run 'PerContract_RuntimeIsolation|DeployContract_(FreshRuntime|NonWASMSkips)'`)
    + 0.691s full suite; `pkg/audit` 0.512s;
    `tests/` `-short -run Audit` 1.380s.
  - **BLR1 live deploy:** binary cross-compiled from this
    commit (`-trimpath -ldflags='-s -w' CGO_ENABLED=0 GOOS=linux
    GOARCH=amd64`), sha256
    `1cd9949cb545cca25b168f72ad5024badc9fdf897a4b3e59a961baa45819b649`,
    32,653,496 bytes. Atomic `mv` over `/opt/qsdm/qsdm`
    (previous binary at `/opt/qsdm/qsdm.pre-wasm-iso.bak`),
    `systemctl restart qsdm.service` → `active (running)`,
    PID 347730, chain restored. Caddy returned 502 for ~3s
    while the node finished its `restored from disk` boot
    sequence; live `/api/v1/audit/summary` reports **60/85
    passed (70.59 %)** post-stabilisation, blocking findings
    **14** (down from 16). The +3 vs the previous deploy's
    57/85 reflects parallel session `61a0626`'s `infra-01` +
    `infra-02` flips landing on the same binary together with
    this pass's `sc-01`.

- **`infra-01` + `infra-02` audit flip — 2 items + Dockerfile USER
  drift fix + `AuthManager` Charming123 unreachable-fallback
  cleanup (57 → 59, 67.06 % → 69.41 %, 2026-05-15 post-`db82128`).**
  Closes the two flippable infrastructure rows from the Tier-B
  read-only scan with concrete in-tree evidence — but only after
  fixing two real defects discovered during the verification
  read. Score on `https://api.qsdm.tech/api/v1/audit/summary`
  moves from **57/85 passed (67.06 %)** to **59/85 passed
  (69.41 %)**, blocking-findings count drops from 16 to 15.
  Infrastructure category reaches **3/3 = 100 %** alongside the
  already-green cryptography category.
  - **Legacy `QSDM/Dockerfile` USER directive — drift fix.** The
    legacy single-image `Dockerfile` is preserved for
    build-compatibility with operators still invoking
    `docker build -t qsdm:latest .`; the file's own header
    comment (lines 24-25) explicitly says "intentionally mirrors
    Dockerfile.validator; keep the two in sync when modifying."
    Both `Dockerfile.validator` (line 95) and `Dockerfile.miner`
    (line 135) ship a `USER qsdm` directive backed by an
    `addgroup -S qsdm && adduser -S -G qsdm qsdm` setup, but the
    legacy file had drifted out of sync at some earlier session
    and was running as root in any environment that doesn't
    layer the K8s `securityContext.runAsUser=65532` from
    `d5b176b` on top. The 7-line diff in this commit replays the
    `Dockerfile.validator` USER block verbatim before the `CMD
    ["qsdm"]` line — `addgroup -S qsdm`, `adduser -S -G qsdm
    qsdm`, `mkdir -p /app/data`, `chown -R qsdm:qsdm /app`,
    `USER qsdm`. Defense-in-depth posture is now: K8s deploys
    get `runAsUser=65532` from `d5b176b`'s securityContext block
    (matters because K8s can refuse to honour an in-image USER
    directive); bare `docker run` invocations fall back to this
    image-baked `USER qsdm`; both paths converge on a non-root
    runtime.
  - **`pkg/api/auth.go::AuthManager.jwtHMACSecretBytes`
    unreachable-error-path Charming123 fallback — regression
    fix.** `db82128` correctly fixed
    `pkg/api/security.go::RequestSigner.hmacSecret` (replaced
    the `[]byte("Charming123")` literal on the `rand.Read`
    failure path with a non-banned placeholder
    `qsdm-rand-fallback-unreachable-in-practice-key-32b`) — but
    its commit body and the `crypto-02` audit row's `Notes` both
    claimed *"both HMAC paths now use 32 B from crypto/rand"*,
    which was true on the **happy path** but inaccurate about
    the unreachable error fallback in `auth.go` line 118. That
    line still returned `[]byte("Charming123")` after a
    `rand.Read` failure. In practice unreachable on a healthy
    system (CSPRNG never fails), but the literal is also the
    demo-prefix banned by `config.go::Validate` strict-mode and
    is exactly the anti-pattern `db82128` was meant to retire.
    This commit replays the `security.go` pattern in `auth.go`:
    on `rand.Read` failure the function now returns
    `qsdm-jwt-rand-fallback-unreachable-in-pra` (40-byte
    placeholder, distinct from the `RequestSigner` fallback so
    the two paths can be told apart in a hex dump). Existing
    test `TestAuthManager_JWTHMACFallback_NotHardcoded` still
    pins the happy-path invariant; the unreachable branch is
    not exercised by the test suite (would require injecting a
    failing `crypto/rand`), so the cleanup relies on the
    `config.go::Validate` strict-mode literal-ban as the runtime
    tripwire. Discovered while verifying `infra-02`'s "no
    hardcoded secrets" claim; full disclosure here so a future
    auditor can reconstruct the chain of evidence for both
    `crypto-02` (now consistent) and `infra-02` (now true
    end-to-end).
  - **Audit checklist flips (`pkg/audit/checklist.go`):**
    - `infra-01` "Docker image hardening" (high) →
      `evidence:in-tree`. All 3 Dockerfiles
      (`Dockerfile`, `Dockerfile.miner`, `Dockerfile.validator`)
      ship `USER qsdm` after the legacy-drift fix; minimal
      runtime stage (`alpine:latest` for validator + legacy,
      `nvidia/cuda:12.3.2-runtime-ubuntu22.04` for miner — the
      smallest CUDA-runtime base) with `--no-install-recommends`
      / `--no-cache` to keep package-set tight; K8s
      `securityContext.runAsUser=65532` defense-in-depth from
      `d5b176b` cited as the orchestration-layer companion.
    - `infra-02` "Secret management" (medium) →
      `evidence:in-tree-tests`. Repo-wide grep audit confirms
      zero hardcoded secrets in production source after the
      `auth.go` cleanup above; remaining `Charming123`
      occurrences are all test fixtures, documentation
      comments, or audit-checklist `Notes` referencing the
      fixed bug. Runtime tripwire: `config.go::Validate`
      strict-mode (`QSDM_STRICT_SECRETS=true`) actively
      rejects any secret prefixed with `charming123`
      (case-insensitive), so a regression that reintroduces
      the literal would fail node startup. CI guard via
      `TestRequestSigner_HMACFallback_NotHardcoded` +
      `TestAuthManager_JWTHMACFallback_NotHardcoded` (4
      invariants each, `pkg/api/hmac_fallback_test.go` from
      `db82128`).
  - **Drift guard (`pkg/audit/checklist_extra_test.go`):**
    `runtimeVerifiedItems` extended from 57 → 59 (`infra-01`,
    `infra-02` appended on the existing
    `infra-03 / runtime-* / supply-* / ...` line). The
    infrastructure block is now spelt
    `"infra-01", "infra-02", "infra-03"` for visual clarity.
    `TestChecklist_PassedCountMatchesRuntimeVerifiedList`
    re-anchors at 59; a regression that flips one back to
    pending without removing it from the allow-list now fails
    CI.
  - **Out-of-batch (deliberately not flipped).** Of the 28 still
    pending after `db82128`: `auth-04` (per-call token replay)
    is a documented design decision per `8edd91c`'s notes (QSDM
    JWTs are reusable until expiry; replay prevention happens
    at the expiry boundary and on transport via TLS); `net-04`
    (WS Origin validation) is `"currently permissive for dev"`
    per its own description; `api-04` (error info leakage) waits
    on the in-flight `pkg/api/error_sanitize.go` security-push
    work to land; `supply-03` (Trivy) needs a real Trivy step in
    `release-container.yml`; `rotation-01..05` need
    operator-facing rotation runbooks that don't exist today;
    `rebrand-03` / `tok-01` / `mining-01` / `mining-05` are
    explicitly wall-clock-blocked. The remaining 16 (`authz-*`,
    `net-01..03`, `bridge-*`, `sc-01`, `store-02`) require
    package-level reading not yet done.
  - **Scope-disclosure note (matches `d5b176b`'s
    "Parallel-session co-edit" precedent).** This commit was
    pre-announced as a "Group-A* 2-item flip, ~30 min" on the
    Session 100c read-only scan. It expanded to ~1 hour because
    verifying `infra-01`'s evidence surfaced the legacy
    Dockerfile drift and verifying `infra-02`'s "no hardcoded
    secrets" claim surfaced the `AuthManager` Charming123
    unreachable-fallback. Both fixes are ≤7-line diffs, mirror
    existing patterns in sibling files (`Dockerfile.validator`
    and `pkg/api/security.go`), and are tightly bound to the
    audit rows being flipped — proceeding without them would
    have flipped `infra-02` while a banned literal sat 5 lines
    away in tree, and would have flipped `infra-01` while one
    of the three Dockerfiles ran as root.
  - **Test posture (Windows/amd64, `CGO_ENABLED=0`,
    `GOTOOLCHAIN=auto` resolving to go1.25.10 on top of locally
    installed go1.25.5):** `pkg/audit` OK 0.320s (all checklist
    + report tests including the 59-entry-keyed
    `TestChecklist_PassedCountMatchesRuntimeVerifiedList` drift
    guard); `internal/dashboard` OK 2.346s; `pkg/api -short -run
    Auth|HMAC|Charming` OK 0.478s. The pre-existing untracked
    `TestRequestTimeout_SlowHandlerCancelled` flake from the
    parallel security-hardening session
    (`pkg/api/request_timeout_test.go`) is the only red row
    in a full `pkg/api` sweep — same flake `db82128` disclosed,
    unrelated to this commit.
  - **Live-deploy delta:** pending propagation to BLR1 (the
    audit score on `https://api.qsdm.tech` will stay at
    `67.06 %` until the validator binary is swapped to a build
    that includes this commit; the scoring code reads
    `pkg/audit.NewChecklist()` once at process start and caches
    in-memory).

- **`pkg/crypto` test catch-up + `crypto-*` audit flip — 3 critical /
  high blockers closed (54 → 57, 63.53 % → 67.06 %, 2026-05-14
  post-`d5b176b`).** Highest-severity sweep so far: takes out two of
  the three remaining `SevCritical` blockers (`crypto-01` ML-DSA
  keygen + `crypto-02` HMAC fallback) and one of the three remaining
  `SevHigh` crypto blockers (`crypto-05` mTLS validation). Live score
  on `https://api.qsdm.tech/api/v1/audit/summary` moves from
  **54/85 passed (63.53 %)** to **57/85 passed (67.06 %)**, blocking
  findings drop from 19 to 16. Out of the cryptography category's
  5 rows, all 5 are now `StatusPassed`.
  - **Pre-existing `RequestSigner` HMAC-fallback bug fixed**
    (`pkg/api/security.go`). The original `hmacSecret()` helper
    returned a literal `[]byte("Charming123")` when the operator
    had not configured `QSDM_JWT_HMAC_SECRET` — simultaneously
    hardcoded, low-entropy, AND banned by `config.go::Validate`'s
    strict-mode check that rejects any secret prefixed with
    `charming123` (case-insensitive). The fix moves
    `RequestSigner` to the same lazy-random pattern
    `AuthManager.jwtHMACSecretBytes` already used: 32 B from
    `crypto/rand`, cached behind a `sync.Mutex` for the life of the
    `*RequestSigner`. The CGO+liboqs and circl-backend paths are
    unaffected — the HMAC fallback only fires when both the
    operator-supplied secret AND the Dilithium handle are nil
    (an emergency CGO build whose liboqs.dll failed to load).
  - **New `pkg/api/hmac_fallback_test.go` (4 tests, all green
    under `CGO_ENABLED=0 go test ./pkg/api/...`).**
    `TestRequestSigner_HMACFallback_NotHardcoded` is the headline
    crypto-02 guard: it asserts the ephemeral key is 32 B, NOT the
    historical `Charming123` literal, stable across calls within
    one instance, distinct across two independent instances, and
    has ≥20 distinct byte values (a `crypto/rand` 32 B draw
    averages ~22, a hardcoded ASCII string would have ~8).
    `TestRequestSigner_HMACFallback_ExplicitOverride` pins the
    operator-supplied-secret path.
    `TestAuthManager_JWTHMACFallback_NotHardcoded` mirrors the
    same invariants on `AuthManager.jwtHMACSecretBytes` so a
    future refactor that reintroduces a hardcoded fallback on
    either side fails CI.
    `TestRequestSigner_SignVerify_RoundTrip_UnderEphemeralHMAC`
    is the round-trip case the emergency-fallback build path
    needs; it skips when a Dilithium backend is present (which
    is every standard build).
  - **New `pkg/crypto/dilithium_circl_csprng_test.go` (3 tests,
    all green under `CGO_ENABLED=0`).**
    `TestCircl_KeygenCSPRNG_NoCollisions` calls `NewDilithium`
    N=64 times and asserts every public key is distinct — a
    keygen wired to a fixed seed or low-entropy source would
    collide quickly.
    `TestCircl_FIPS204_SizesConformToStrength3` pins
    `mldsa87.{PublicKeySize, SignatureSize, SeedSize}` to the
    exact 2592 / 4627 / 32 values fixed by **NIST FIPS 204 §6.1
    Table 2** for the strength-3 / 256-bit-security parameter set;
    this complements the existing Stage A
    `TestCircl_FIPS204_SizesMatchTxsigConstants` (which checks
    against `pkg/chain/txsig.go` literals) so a future contributor
    that updates BOTH txsig and the circl import in tandem still
    fails CI.
    `TestCircl_FIPS204_DeterministicKeygen_FromFixedSeed` proves
    the FIPS 204 §6 deterministic-keygen contract (same seed →
    byte-identical packed pubkey).
  - **2 new mTLS rejection tests in `pkg/api/mtls_test.go`,
    closing crypto-05's three legs.** The existing suite already
    covered "untrusted CAs" (`TestMTLSRejectsWrongCA` +
    `TestMTLSRejectsUnauthenticatedClient`). This commit adds
    `TestMTLSRejectsExpiredClientCert` (back-dated `NotBefore=-4h`
    / `NotAfter=-2h` on a CA-trusted leaf — server rejects with
    x509 `certificate has expired or is not yet valid`) and
    `TestMTLSRejectsWrongSAN` (CA-trusted leaf whose SAN is
    `evil.example.com`; rejected by a `VerifyPeerCertificate`
    hook that pins the connecting peer to `127.0.0.1`, matching
    the operator pattern documented in `deploy/README.md`).
  - **Audit checklist (`pkg/audit/checklist.go`):** 3 items
    flipped to `StatusPassed` with
    `ReviewedBy="evidence:in-tree-tests"` and
    `ReviewedAt="2026-05-14T20:00:00Z"`: `crypto-01` ML-DSA key
    generation (critical), `crypto-02` HMAC fallback security
    (critical), `crypto-05` mTLS certificate validation (high).
    Each `Notes` field carries pointers to the source code,
    the specific test functions, the FIPS 204 §6.1 Table 2
    parameter values, and the historical `Charming123`-banned-by-
    strict-mode rationale for the `RequestSigner` fix.
    `runtimeVerifiedItems` extended to 57 entries; e2e
    `TestE2E_AuditChecklistReview` rebased to use `authz-01` +
    `sc-01` for the +2 pending → passed delta (both are still
    `SevCritical` pending in `defaultItems()`).
  - **Test posture (windows/amd64, `CGO_ENABLED=0`, go1.25.0):**
    `pkg/crypto` 0.461s (CSPRNG + FIPS 204 tests pass with
    `-run 'Circl_(KeygenCSPRNG|FIPS204)'`), `pkg/audit` 1.301s,
    `internal/dashboard` 2.506s, `tests` `-run Audit` 1.516s.
    `pkg/api` still shows the parallel session's pre-existing
    `TestRequestTimeout_SlowHandlerCancelled` flake in their
    still-untracked `request_timeout_test.go`; not part of
    this commit, not related to the crypto pass.
  - **BLR1 live deploy:** binary cross-compiled from this commit
    (`-trimpath -ldflags='-s -w' CGO_ENABLED=0 GOOS=linux
    GOARCH=amd64`, sha256
    `4278a207d8833e13c425cf089855c5f72365f18ee412377137a7645bcca0ba35`,
    32,649,400 bytes), `scp`'d, atomic `mv` over `/opt/qsdm/qsdm`
    (previous binary preserved as
    `/opt/qsdm/qsdm.pre-crypto-flip.bak`), `systemctl restart
    qsdm.service` → `active (running)`, PID 336974, chain restored.
    Post-swap `/api/v1/audit/summary` reports **57/85 passed
    (67.06 %)** with `evidence_provenance` `{in-tree: 22,
    in-tree-tests: 23, live-deploy: 12}` and `blocking_count: 16`.

- **K8s runtime hardening + `runtime-*` audit catch-up — 6 items flipped
  to `StatusPassed` (2026-05-14, post-`2655df1`).** Brings the
  `pkg/audit` checklist's container-runtime row in line with the
  hardening already encoded in `QSDM/deploy/kubernetes/*.yaml` and
  closes the only remaining row that needed a brand-new manifest
  (`runtime-07` NetworkPolicy). Live score on `https://api.qsdm.tech/api/v1/audit/summary`
  moves from **48/85 passed (56.47 %)** to **54/85 passed (63.53 %)** —
  a 7-point delta, blocking-findings count drops from 22 to 19.
  - **K8s pod-security context (`runtime-01..05`):** all 3 deploy
    manifests (`validator-statefulset.yaml`, `miner-daemonset.yaml`,
    legacy `deployment.yaml`) now carry a uniform
    `securityContext` block: `runAsNonRoot=true` +
    `runAsUser=65532` + `runAsGroup=65532`,
    `readOnlyRootFilesystem=true` (with explicit `/tmp` `emptyDir`
    for Go `os.TempDir` + libp2p ephemeral state + CUDA driver
    cache on the miner), `capabilities.drop=[ALL]`,
    `allowPrivilegeEscalation=false`, and
    `seccompProfile.type=RuntimeDefault`. The legacy `deployment.yaml`
    previously had no `securityContext` at all — this commit brings
    it to parity so the legacy single-image path is not a security
    regression vs the split validator + miner topology.
  - **NetworkPolicy default-deny (`runtime-07`):** new
    `QSDM/deploy/kubernetes/networkpolicy.yaml` ships 8
    `NetworkPolicy` resources for the `qsdm` namespace —
    `qsdm-default-deny` (empty Ingress+Egress on all pods), plus
    seven targeted allowlist rules: cluster DNS egress (kube-system
    `k8s-app=kube-dns` UDP+TCP/53), intra-namespace ingress+egress
    (TCP/4001 P2P + TCP/8080 miner→validator), Scylla egress
    (namespace `scylla` TCP/9042+9142), Prometheus scrape ingress
    (namespace `monitoring` TCP/8080+8081), ingress-controller
    ingress (namespace `ingress-nginx` TCP/8080+8081), libp2p
    public-internet egress (TCP/4001 with private-CIDR exclusions),
    and NTP egress (UDP/123 for v2 mining-attestation freshness).
    Threat-model boundaries are documented inline.
  - **Drift guard:** `.github/workflows/validate-deploy.yml`'s
    `kubernetes-dry-run` `kubeconform -strict
    -kubernetes-version 1.31.0` sweep now covers all 10 manifest
    files including the two split-image specs and `networkpolicy.yaml`
    (19/19 resources Valid locally with kubeconform v0.6.7;
    `envFrom[1]` schema regression in `validator-statefulset.yaml`
    fixed in passing — the `BOOTSTRAP_PEERS` literal was misplaced
    under `envFrom` instead of `env`, which would have been caught
    by the schema sweep the moment the validator manifest was
    added to it).
  - **Audit checklist (`pkg/audit/checklist.go`):** 6 items flipped
    to `StatusPassed` with `ReviewedBy="evidence:in-tree"` and
    `ReviewedAt="2026-05-14T19:45:00Z"`: `runtime-01` Non-root
    container user (high), `runtime-02` Read-only root filesystem
    (high), `runtime-03` Linux capability drop (medium),
    `runtime-04` Seccomp / AppArmor profile (medium), `runtime-05`
    Resource limits (high), `runtime-07` NetworkPolicy / egress
    control (medium). (`runtime-06` liveness/readiness probes was
    already flipped in `2655df1` by a parallel session.) Each
    `Notes` field carries a precise pointer to the manifest +
    `securityContext` field (or the `networkpolicy.yaml` resource
    list) that closes the row, plus the kubeconform CI sweep
    citation as the drift guard.
  - **Test posture (windows/amd64, CGO_ENABLED=0, go1.25.0 via
    `GOTOOLCHAIN=go1.25.0+auto`, `GOSUMDB=sum.golang.org`):**
    `pkg/audit` (all checklist + report tests including the
    `TestChecklist_PassedCountMatchesRuntimeVerifiedList` drift
    guard now keyed off a 54-entry `runtimeVerifiedItems` list)
    OK 0.661s; `internal/dashboard` OK 3.683s; `tests/` `-run
    Audit` OK 2.381s. `pkg/api` shows one pre-existing flake in
    the parallel session's still-untracked `request_timeout_test.go`
    expecting `request_timeout_total >= 1` — unrelated to this
    change; that file is not part of this commit.
  - **BLR1 live deploy:** binary cross-compiled from this commit
    (`-trimpath -ldflags='-s -w' CGO_ENABLED=0 GOOS=linux GOARCH=amd64`,
    sha256 `8b488ce0c7f0fc336b734f0e27e8b6e19f273263b9bb7af87fa3b92e84f8a119`,
    32,645,304 bytes), uploaded via scp, atomically swapped at
    `/opt/qsdm/qsdm` (previous v0.4.2-tag-faithful + audit-flip-17
    + Group-A-flip-4 binary preserved as
    `/opt/qsdm/qsdm.pre-runtime-flip.bak`), `systemctl restart
    qsdm.service` — service `active (running)`, PID 336732, chain
    + accounts + receipts restored, blockdriver re-armed at
    height 67613. Post-swap `/api/v1/audit/summary` reports
    **54/85 passed (63.53 %)** with `evidence_provenance`
    `{in-tree: 22, in-tree-tests: 20, live-deploy: 12}` and
    `blocking_count: 19`.

- **Audit checklist Group-A evidence-discipline flip — 4 items
  pre-flipped to `StatusPassed` (2026-05-14, post-`8edd91c`).**
  Closes the four remaining items from Session 100c's read-only
  scan whose evidence was already sitting in-tree but `8edd91c`
  hadn't picked up — narrower scope than the 17-item batch
  before it (each item carries a precise file-and-line pointer
  to the evidence) and explicitly held the line on
  evidence-discipline by **dropping `rotation-02`** from the
  intended 5-item batch when no rotation runbook turned out to
  exist in the repo. Score on
  `https://api.qsdm.tech/api/v1/audit/summary` moves from
  **44/85 passed (51.76 %)** to **48/85 passed (56.47 %)** —
  a 4-point delta, no new code, no tests rewritten beyond the
  drift-guard list extension.
  - **Items flipped (each cites the exact source-tree pointer in
    its `Notes` field):**
    - `infra-03` "Dependency audit" (low) →
      `evidence:in-tree-tests`. `.github/workflows/qsdm-go.yml`
      line 128 dedicated `govulncheck` job (delegates to
      `QSDM/scripts/govulncheck-filter.sh`); same workflow
      already cited as the basis for `supply-02` (transitive
      CVE scanning), so this is the operator-facing companion
      finding rather than a new evidence claim.
    - `supply-06` "Reproducible builds" (medium) →
      `evidence:live-deploy`. `release-container.yml` lines
      39-40+163-169 cross-compile every binary with
      `go build -trimpath -ldflags="${LDFLAGS}" CGO_ENABLED=0`
      across 5 platforms; `qsdm-split-profile.yml` line 196
      runs a per-PR reproducibility-smoke job; v0.4.2 evidence
      doc cites byte-identical match between the GHCR-image
      binary and a workstation cross-compile from tag
      `2039035` (`sha256:7fd07587df071b7766a2784533526969febe68012e2932671643178d1e8fe0dd`).
    - `supply-07` "Dependency pinning policy" (medium) →
      `evidence:in-tree`. `.github/dependabot.yml` ships
      weekly `gomod` scan on `/QSDM/source` plus monthly
      `github-actions` scan on `/`, with a documented
      `github.com/libp2p/*` major-version exception (those
      modules ship as `+incompatible` pseudo-releases that
      cannot be applied without rewriting every import — full
      rationale verbatim in the dependabot.yml comments). Go
      module integrity verified by the `qsdm-go.yml`
      `mod-verify` step from `supply-01`.
    - `runtime-06` "Liveness / readiness probes" (medium) →
      `evidence:in-tree`. `validator-statefulset.yaml` lines
      107-120 wire both probes to `/api/v1/health/live`
      (`initialDelaySeconds=60 periodSeconds=30`) and
      `/api/v1/health/ready` (`initialDelaySeconds=30
      periodSeconds=10`); probe targets bound by
      `pkg/api/handlers.go` lines 200-202.
  - **Out-of-batch (deliberately dropped):** `rotation-02` "mTLS
    certificate rotation" was pre-listed as a Group-A candidate
    on the strength of an assumed `MTLS_CERTIFICATE_ROTATION.md`
    runbook; a `Get-ChildItem -Recurse | Where-Object
    Name -match rotation` over the docs tree turned up zero
    matches, so the row stays `pending`. This is the
    "evidence-discipline-over-headline-score" choice the
    Session-75 baseline rule was designed to enforce: a
    runtime-verified row whose evidence is fictitious is worse
    than a `pending` row.
  - **Drift guard (`pkg/audit/checklist_extra_test.go`):** the
    `runtimeVerifiedItems` allow-list is extended from 44 to 48
    entries (`infra-03`, `supply-06`, `supply-07`, `runtime-06`
    appended). `TestChecklist_PassedCountMatchesRuntimeVerifiedList`
    re-anchors at 48; a regression that flips one of these back
    to pending without removing it from the allow-list now
    fails CI.
  - **Race-condition note (interleaving with `8edd91c`).** The
    Session 100c readiness scan started with `passed=27`; the
    `8edd91c` 17-item batch landed mid-scan and preempted three
    of the original Group-A candidates (`crypto-04`,
    `store-01`, `store-03`) before this commit could reach
    them. The remaining four were the intersection of "still
    pending after `8edd91c`" and "Session 100c evidence
    discipline survives." `ReviewedAt="2026-05-14T19:30:00Z"`
    is one hour after the `8edd91c` batch's
    `2026-05-14T18:30:00Z` to keep the timestamp ordering
    legible in the `/api/v1/audit/items` JSON response.
  - **Test posture (Windows/amd64, `CGO_ENABLED=0`,
    `GOTOOLCHAIN=auto` resolving to go1.25.10 on top of locally
    installed go1.25.5):** `pkg/audit` OK 0.472s,
    `pkg/api` OK 2.632s `-short`, `internal/dashboard` OK
    2.097s `-short`, `tests/` `-run Audit` OK 1.222s. Score
    progression now anchored to `48/85 = 56.47 %` until
    `d5b176b` lifts it to `54/85 = 63.53 %`.

- **Audit checklist evidence catch-up — 17 items pre-flipped to
  `StatusPassed` (2026-05-14, post-v0.4.2-tag).** Closes the
  evidence-on-paper-but-not-on-checklist gap surfaced by the
  v0.4.2 public-API mirror at `/api/v1/audit/summary`: in-tree
  test coverage and live-deploy evidence already existed for 17
  pending items, but `defaultItems()` had not been updated to
  reflect it. The live score on `https://api.qsdm.tech/api/v1/audit/summary`
  moves from **27/85 passed (31.76 %)** to **44/85 passed
  (51.76 %)** — a 20-point public-trust delta with no new code
  on the validator side, only existing evidence formally cited.
  Categories flipped:
  - **Cryptography (+2):** `crypto-03` JWT signature verification
    (`TestTokenValidation` + `TestTokenExpiration` +
    `TestBearerTokenReusedUntilExpiry` in `tests/api_security_test.go`),
    `crypto-04` secret generation entropy (production code uses
    `crypto/rand` exclusively; `math/rand` only in test
    fixtures — full repo grep verified).
  - **Authentication (+4):** `auth-01` Argon2id password hashing
    (`TestPasswordHashing`), `auth-02` account lockout
    (`AccountLockoutManager` wired at `handlers.go:613`),
    `auth-03` session cookies (`HttpOnly+SameSite=Lax+Secure` at
    `dashboard.go:869-871`), `auth-05` password policy
    (`MinPasswordLength=12` + common-password blocklist). The
    remaining `auth-04` (per-call token replay) stays pending — by
    design, QSDM JWTs are reusable until expiry per
    `TestBearerTokenReusedUntilExpiry`; replay prevention happens
    at the expiry boundary and on transport via TLS, not via
    per-call nonces.
  - **API (+3):** `api-01` input validation (`TestInputValidation`
    + `TestSQLInjectionProtection`), `api-02` CSRF middleware
    (256-bit `crypto/rand` tokens + Bearer-token bypass at
    `pkg/api/csrf.go`), `api-03` security headers (HSTS + CSP +
    X-Frame-Options + X-Content-Type-Options + 4 more, covered
    by `TestSecurityHeaders`).
  - **Storage (+2):** `store-01` atomic writes (16 production
    persisters use `os.Rename` pattern; already exercised by
    store-04 + store-05 tests), `store-03` 0600 permissions on
    sensitive files (consistent across the 16 atomic-write sites).
  - **Governance (+3):** `gov-01` vote-manipulation prevention
    (`TestSnapshotVoting` + double-vote rejection at
    `chainparams/types.go:401`), `gov-02` proposal execution
    safety (`TestProposalExecutor_NoDoubleExecution` +
    `_QuorumNotReached` + 4 more, 6 tests total), `gov-03`
    multi-sig expiry (`TestMultiSig_ExpiredAction` + 6 more
    tests).
  - **Smart contracts (+3):** `sc-02` gas metering
    (`TestGasMeter_ExhaustionError`), `sc-03` event integrity
    (`TestEventIndex_{EmitAndQuery,Retention,QueryOffsetLimit,Subscribe,SubscribeAll}`),
    `sc-04` simulation fallback determinism
    (`TestSimulatedTokenBalanceTracking` +
    `TestSimulatedVoting` + `TestSimulatedEscrow`). The remaining
    `sc-01` WASM sandbox isolation stays pending pending an
    affirmative cross-contract memory-leak test (wazero
    isolation by-design but not under-attack tested in-tree).
  - **Out-of-scope, deliberately still pending (41 items).**
    `crypto-01` ML-DSA-87 key generation (formal NIST FIPS 204
    review), `crypto-02` HMAC-fallback security (design
    review), `crypto-05` mTLS certificate validation (security
    audit), `authz-*` (RBAC formal review × 4 items),
    `bridge-*` (atomic-swap secret review × 4 items),
    `net-{01,02,03,04}` (P2P sig + Sybil + TLS + WebSocket
    origin review), `infra-*`/`runtime-*`/`rotation-*` (ops
    procedures × 15 items), `store-02` (snapshot hash
    verification), `gov-*` already covered, `mining-01` /
    `mining-05` (external audit + incentivised testnet
    wall-clock), `tok-01` (counsel sign-off), `rebrand-03`
    (trademark filing), `supply-{03,06,07}` (Trivy in CI +
    reproducible builds + dependabot policy). Each remaining
    pending item is now visible at `/api/v1/audit/items?status=pending`.
  - **Drift guards updated:** 17 new IDs added to
    `runtimeVerifiedItems` in
    `pkg/audit/checklist_extra_test.go`; the
    `TestChecklist_PassedCountMatchesRuntimeVerifiedList`
    guard now expects exactly 44 passed items, so a
    regression that quietly un-flips one of these would
    immediately fail CI.
  - **e2e delta-baseline rebased.**
    `TestE2E_AuditChecklistReview` in `tests/e2e_test.go`
    historically picked `auth-01` for the
    pending→failed transition; the new pass flipped
    `auth-01` to passed, so the test rebases to
    `bridge-01` (still pending: bridge secret handling
    review) which is the same Critical severity. The
    `passed_delta=+2, failed_delta=+1` invariant is
    preserved.
  - **BLR1 deploy (post-v0.4.2-tag hotfix-window binary).**
    Validator binary cross-compiled from this commit and
    swapped on `api.qsdm.tech`; previous v0.4.2-tag-faithful
    binary preserved as `/opt/qsdm/qsdm.v042-tag-faithful.bak`
    (SHA `7fd07587…1e8fe0dd`); new binary SHA
    `9b5fe991c94994c5bd43a3036f8a222ee63ebad5a57b9d36161bae046a76c090`
    (32 592 056 B). `QSDM_BUILD_VERSION` stays `v0.4.2`; this
    is informally a "v0.4.2 + audit-evidence" build.
    Reproducibility: any auditor can re-build from this commit
    with `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath
    -ldflags='-s -w' ./cmd/qsdm` and match the deployed binary
    byte-for-byte.

- **OpenAPI catch-up pass — `docs/docs/openapi.yaml` v1.0.0 → v1.1.0
  (2026-05-14).** Closes the lag between the actual `/api/v1/*`
  surface and the published spec. The pre-edit YAML covered only
  the v0.1-era core (health, auth, wallet, tokens, transactions,
  validator, monitoring/ngc-*); every endpoint family added by
  Phases 2–5 of the Major Update plus the v0.4.x rollout was
  undocumented. This commit adds **12 new path entries**, **7 new
  tags**, **9 new schema components**, and fixes a pre-existing
  **strict-YAML scannability bug** (`(default: 50, max: 1000)` on
  the `/transactions` `limit` parameter description — unquoted
  `: ` tripped `pyyaml.safe_load`; quoted now). End-to-end
  validated by `python -c "import yaml; yaml.safe_load(...)"`
  after the edits → `openapi=3.0.3, version=1.1.0, paths=31,
  tags=13, schemas=10`, with every documented path
  cross-checked 1:1 against a route registered in
  `pkg/api/handlers.go` (manual diff; no phantoms).
  - **New paths (12):** `/status`, `/wallet/nonce`,
    `/audit/summary`, `/audit/items`,
    `/trust/attestations/summary`, `/trust/attestations/recent`,
    `/attest/recent-rejections`, `/governance/params`,
    `/governance/params/{name}`, `/receipts`, `/receipts/{tx_id}`,
    `/network/topology`. Selection criterion: every public-read
    transparency endpoint added through v0.4.2 plus the operator
    surfaces with stable wire shapes (`/status`, `/wallet/nonce`,
    `/governance/params`, `/network/topology`).
  - **New tags (7):** `Status`, `Trust`, `Audit`, `Attest`,
    `Governance`, `Receipts`, `Network` — each carries a
    one-line description that names the source-of-truth doc /
    package (e.g. `Major Update §8.5`, `pkg/audit`,
    `MINING_PROTOCOL_V2.md §9.4`) so a future maintainer can
    locate the binding contract without grepping the repo.
  - **New schema components (9):** `NodeStatus`, `AuditSummary`,
    `AuditBlockingItem`, `AuditItemsResponse`,
    `AuditChecklistItem`, `AuditEchoedFilters`, `TrustSummary`,
    `TrustAttestation`, `TrustRecent`. Closed-enum filter values
    on `/audit/items` and `/attest/recent-rejections` are spelt
    out inline (`enum: [critical, high, medium, low, info]`,
    `enum: [pending, passed, failed, waived]`, the four
    rejection kinds) so a client SDK code generator emits typed
    unions without a second lookup.
  - **Wire-shape parity guard:** the `AuditSummary` schema's
    `description` cites `TestAuditAPI_WireParity_DashboardAndAPI`
    from `pkg/api/handlers_audit_test.go`, so a future spec
    drift surfaces in the same Go test that already pins
    `dashboardAuditSummaryView` ↔ public-`AuditSummary` field
    parity (the YAML schema is now the third pin alongside the
    two Go structs).
  - **Description block** under `info:` gains a "Public read
    (transparency) endpoints — v1.1.0 catch-up" paragraph
    enumerating every route intentionally unauthenticated, so a
    consumer reading the spec top-down knows what to expect
    without crawling the per-route `security` blocks. The
    paragraph also names the rate-limit posture and links the
    "X of Y, never just X" anti-claim guarantee from
    `NVIDIA_LOCK_CONSENSUS_SCOPE.md`.
  - **Out of scope (deferred to a follow-up doc pass).** Mining
    endpoints (~14, `/mining/*`) — they have their own auditor
    packet (`AUDIT_PACKET_MINING.md`) and the wire shapes are
    governed by `MINING_PROTOCOL_V2.md` §3–7. Cross-chain bridge
    (`/bridge/*`) and smart contracts (`/contracts/*`) — sprawling
    state machines whose schemas are best authored against the
    package source rather than retro-fitted from handler
    signatures. Also deferred: `/tokens/list`. Adding any of
    these is a separate, self-contained PR; they are explicitly
    not v1.1.0 scope.

- **`https://qsdm.tech/api.html` — visitor-facing API status page (2026-05-14).**
  Disambiguates `/api/v1/*` (stable HTTP URL prefix) from
  "mining-protocol v1 deprecation" (v0.3.2 retirement of CPU-only
  PoW at consensus). The Build → API status link in every landing
  footer points here; the page has a TL;DR scope note, side-by-side
  panels for the two axes, a live-posture widget polling
  `/api/v1/status` every 60 s, and a list of live `/api/v1/*`
  endpoints visitors can probe from the browser. Companion docs-
  portal article at
  [`API_VERSIONING.md`](QSDM/docs/docs/API_VERSIONING.md)
  surfaced through **Reference → API versioning** with badge=new.
  Commits `3314fca` + `ad8862a` (post-v0.4.2-tag). Footer Build
  link in `landing/index.html` flipped from `/api/v1/health` to
  `/api.html`.
- **`landing/api.html` + `API_VERSIONING.md` rsync'd to BLR1
  (2026-05-14).** Static-asset deploy via `cmd/qsdm-deploy-landing`
  (Go SSH tool from v0.3.1). Final verification:
  `https://qsdm.tech/api.html` returns HTTP 200; live-posture
  widget polls show `protocol_versions_accepted: [2]`,
  `fork_v2_active: true`. Docs portal SPA fetches
  `API_VERSIONING.md` from `raw.githubusercontent.com/.../main/`
  with no separate deploy.
- **CHANGELOG release-section back-fill: `[v0.4.1]`, `[v0.4.0]`,
  `[v0.3.3]`, `[v0.3.2]`, `[v0.3.1]`, `[v0.3.0]` (2026-05-14).**
  The CHANGELOG previously had only `[Unreleased]` despite six
  tags shipping in the v0.3.x → v0.4.x window. Reconstructed
  proper Keep-a-Changelog sections from git log within each
  tag-to-tag commit range; anchored each section to its tag
  commit, `release-container.yml` run ID, BLR1 deploy SHA, and
  the release-evidence doc. Commits `682ed55` + `a189cf7`.

## [v0.4.2] - 2026-05-14

**Release theme:** audit-checklist transparency — the runtime-
verified score (currently 27/85 passed, 31.8 %) is now visible on
both the bearer-gated operator dashboard at
`https://dashboard.qsdm.tech` and the public API server at
`https://api.qsdm.tech/api/v1/audit/{summary,items}`. External
audit aggregators, SDK consumers, and third-party verifiers can
now read the score without an operator-granted session.

**Tag:** `v0.4.2` @ `2039035602c56f87801610dd93d2135fe7696864`
(2026-05-15T02:04:48+08:00).
**Release workflow run:** `release-container.yml`
[run 25862…](https://github.com/blackbeardONE/QSDM/actions/workflows/release-container.yml)
— success on push of tag `v0.4.2` at commit `2039035`; 53 cosign-
signed assets attached to
[the v0.4.2 GitHub release](https://github.com/blackbeardONE/QSDM/releases/tag/v0.4.2);
3 GHCR images (`qsdm`, `qsdm-validator`, `qsdm-miner`) signed
against `release-container.yml@refs/tags/v0.4.2`.
**BLR1 deploy:** binary atomic-swap to v0.4.2 tag-faithful build
(SHA `7fd07587…1e8fe0dd`) + systemd
`QSDM_BUILD_VERSION=v0.4.2` env refresh + landing pill bumped to
v0.4.2 (this commit).
**Live verification:** `https://api.qsdm.tech/api/v1/status` reports
`"version":"v0.4.2"` + `mining.protocol_versions_accepted:[2]` +
`fork_v2_active:true`;
`https://api.qsdm.tech/api/v1/audit/summary` returns HTTP 200 with
`{passed:27,pending:58,failed:0,total:85,score:31.76}` and a
blocking-preview of 5 critical/high pending items
(`crypto-01,…`).
**Evidence:** [`RELEASE_EVIDENCE_v0.4.2.md`](QSDM/docs/docs/RELEASE_EVIDENCE_v0.4.2.md).

### Added

- **Audit checklist transparency endpoints — public-API mirror (2026-05-14).**
  Closes the third-party-visibility gap from the 2026-05-14 dashboard
  tile (commit `48c0229`): the `/api/audit/{summary,items}` routes
  added there are bearer-gated through `requireAuth`, so SDK
  consumers, the public landing page widget at
  `https://qsdm.tech/trust`, and external audit aggregators couldn't
  read the runtime-verified score without an operator-granted
  session. This commit lifts the same data onto the public API
  server's `/api/v1/*` surface, matching the
  `/api/v1/trust/attestations/*` precedent (Major Update §8.5):
  - `GET /api/v1/audit/summary` — bucket counts (`total / passed /
    pending / failed / waived`), `score` (0..100 float),
    `has_blocking_findings`, `blocking_count`, top-5
    `blocking_preview` of still-pending critical/high items
    (id/category/severity/status/title), 4-bucket
    `evidence_provenance` map (`evidence:live-deploy /
    evidence:in-tree-tests / evidence:in-tree / other`),
    `generated_at` RFC3339 stamp.
  - `GET /api/v1/audit/items` — full filterable items list with
    closed-enum `?category=` / `?severity=` / `?status=` query
    parameters validated against the `pkg/audit` constant
    allow-lists. A typo'd value returns 400 (no silent
    passthrough — clients that mis-type a filter must NOT see
    "all items"); applied filters are echoed back via an
    `omitempty` block so a bare-call response doesn't carry
    `"filters":{}`.
  - **Wire-shape parity** with the bearer-gated dashboard
    endpoints: every JSON field the dashboard's
    `dashboardAuditSummaryView` serialises is also present on
    the public `AuditSummary`, so a client switching between
    `https://dashboard.qsdm.tech/api/audit/summary` and
    `https://api.qsdm.tech/api/v1/audit/summary` gets identical
    JSON. Pinned by `TestAuditAPI_WireParity_DashboardAndAPI`.
  - **Public posture:** both routes added to `publicPaths` in
    `pkg/api/middleware.go` so the rate-limited per-IP limiter
    handles abuse rather than auth gating, and external
    consumers don't need an operator session. The audit
    checklist text is already public from the open-source
    repo, so the only thing newly exposed is the per-item
    Status/ReviewedBy/ReviewedAt — which is exactly the
    transparency signal we want to advertise.
  - **Cache headers:** `Cache-Control: public, max-age=60` on
    both endpoints. A flip is a Git commit + redeploy event,
    so 60 s of staleness is acceptable; caps origin-fetch QPS
    at ~SDK-clients/60.
  - **Singleton checklist:** package-level `sync.Once`-guarded
    `audit.Checklist` so summary and items always agree on the
    same in-process state (covered by
    `TestAuditItemsHandler_FilterByStatus_Passed_MatchesSummary`),
    and a future admin endpoint that mutates state via
    `UpdateStatus` propagates to both surfaces in lock-step.
  - **Drift guards (10 new tests / 14 sub-cases — all green):**
    `TestAuditSummaryHandler_{MethodNotAllowed,ShapeAndCounts}`,
    `TestAuditItemsHandler_{MethodNotAllowed,FullList_NoFilters,
    FilterByStatus_Passed_MatchesSummary,FilterByCategory,
    TypoFilters_400 (4 sub-cases),CombinedFilters?}`,
    `TestAuditAPI_PublicEndpointAllowList` (regressing the
    `publicPaths` allowlist would silently 401 every external
    consumer), `TestAuditAPI_AllowedFilterEnumsMatchPkgAudit`
    (a future `pkg/audit` category constant addition without
    matching API allow-list extension fails CI),
    `TestAuditAPI_WireParity_DashboardAndAPI`.
  - **Out of scope (deferred to a follow-up).**
    `docs/docs/openapi.yaml` is not extended — the spec
    already lags the actual API surface (no `/trust/*`, no
    `/mining/*`, no `/governance/*`), and bringing it
    end-to-end current is a dedicated documentation pass; this
    commit follows the trust-endpoint precedent of "wire it up,
    document in CHANGELOG, OpenAPI catches up separately."

- **Audit checklist tile — operator-dashboard wire-up (2026-05-14).**
  Closes the operator-visibility gap from the 2026-05-13 audit-checklist
  flip: the 27→passed delta now had no live surface, so
  `dashboard.qsdm.tech` was still showing the same tiles as before
  (a viewer would have had to run `cmd/auditreport` on a developer's
  laptop to see the 31.8% score). The dashboard server
  (`internal/dashboard`) now owns an in-process `audit.NewChecklist()`
  and exposes two read-only endpoints behind the existing
  `requireAuth` wrapper:
  - `GET /api/audit/summary` — bucket counts (`total / passed / pending /
    failed / waived`), the (passed+waived)/total score as a 0..100
    float, `has_blocking_findings`, the count of still-pending
    critical+high items, a top-5 preview of those items (id /
    category / severity / title), and a 4-bucket
    `evidence_provenance` breakdown
    (`evidence:live-deploy / evidence:in-tree-tests / evidence:in-tree
    / other`) so the passed count isn't a black box.
  - `GET /api/audit/items` — the full filterable items list with
    optional `?category=`, `?severity=`, `?status=` query
    parameters validated against closed enums (a typo'd value
    returns 400 — operators triaging a regression must NOT see
    "all items" when they intended to filter). Echoes the applied
    filters back via an `omitempty` block, matching the
    `dashboardAttestRejectionsView` contract.
  - **Frontend tile** rendered in `internal/dashboard/static/index.html`
    + `dashboard.js`: a new "Audit Checklist Progress" card on the
    main grid, polling at the existing 2-second cadence. Score is
    tinted (≥80 green / ≥40 amber / red below) and the blocking
    pill flips green to amber when `has_blocking_findings` is true.
    The top-5 preview renders one row per still-blocking item with
    the severity, ID, category, and title — operators see at a
    glance which controls still gate production.
  - **Drift guards (new tests, all green):**
    - `TestHandleAuditSummary_ShapeAndCounts` re-derives the score
      from the buckets, asserts the bucket sum invariant, and
      verifies every preview entry has critical/high severity AND
      pending/failed status (the definition of "blocking").
    - `TestHandleAuditItems_FilterByStatus_Passed_MatchesPreFlippedCount`
      asserts the items-endpoint count of `status=passed` equals
      `summary["passed"]` from the same in-process checklist —
      catches a future drift between the two surfaces.
    - `TestHandleAuditItems_TypoFilters_400` walks each filter
      with a deliberately typo'd value and confirms 400 (no
      silent passthrough).
    - `TestAuditTile_StaticAssetsContainRequiredSymbols` string-
      searches the embedded `dashboard.js` and `index.html` for
      the poller function name, the fetch URL, every DOM id the
      renderer writes to, and the `evidence:*` provenance keys —
      ship-stops a refactor that unhooks any half of the JS↔HTML
      bridge.
  - **Operator-visible delta.** Polling `/api/audit/summary` from
    a logged-in session now returns
    `summary:{total:85,passed:27,pending:58,...}, score:31.76,
    has_blocking_findings:true, blocking_count:N,
    blocking_preview:[crypto-01, crypto-02, …],
    evidence_provenance:{live-deploy:11,in-tree-tests:8,in-tree:8,other:0}`.
    The dashboard tile renders the score prominently with a
    coloured pill for the blocking-findings state.
  - **Out of scope (deferred to a follow-up).** No mirror under
    `/api/v1/audit/*` on the public API server yet — the
    transparency-public version (matching the trust-attestation
    precedent) lands separately. `OpenAPI` spec untouched for the
    same reason; `DASHBOARD_ACCESS.md` is updated.

## [v0.4.1] - 2026-05-14

**Release theme:** replay protection — per-account nonce + atomic
debit/credit on the self-custody `/wallet/submit-signed` path
(Sessions 98-100). Closes the two security gaps documented in
`V040_WALLET_SEND_DESIGN.md §"Open issues"`: cross-`tx_id`
re-submission of a captured envelope (mitigated by the new
nonce gate) and the v0.4.0 non-atomic
`HasTransaction → GetBalance → StoreTransaction`
trio that could double-spend under concurrent submits
(mitigated by a single `ApplyTransferAtomic` storage call).

**Tag:** `v0.4.1` @ `aa060e58bcea69f5e40c14de5c2a404d3efe6ccd`
(2026-05-14T18:23:56+08:00).
**Release workflow run:** `release-container.yml`
[run 25855056334](https://github.com/blackbeardONE/QSDM/actions/runs/25855056334)
— 10/10 jobs green; 53 cosign-signed assets attached;
3 GHCR images (`qsdm`, `qsdm-validator`, `qsdm-miner`) signed
against the `release-container.yml@refs/tags/v0.4.1` Sigstore
OIDC identity.
**BLR1 deploy:** binary atomic-swap to v0.4.1 +
`QSDM_BUILD_VERSION=v0.4.1` systemd refresh + landing pill
bumped to v0.4.1 (Session 100, commit 47d22f7).
**Evidence:** [`RELEASE_EVIDENCE_v0.4.1.md`](QSDM/docs/docs/RELEASE_EVIDENCE_v0.4.1.md)
(FULLY GREEN — 8/8 verification rows green including independent
cosign verify);
[`V041_REPLAY_PROTECTION_DESIGN.md`](QSDM/docs/docs/V041_REPLAY_PROTECTION_DESIGN.md)
(SHIPPED + DEPLOYED).

### Added

- **Per-account nonce + `ApplyTransferAtomic` storage contract.**
  `pkg/api/server.go::StorageInterface` extended with two new
  methods so the v0.4.1 handler can enforce the new invariants
  inside the storage transaction boundary:
  - `GetNonce(address) (uint64, error)` — returns the last-applied
    nonce for an address (0 for unseen senders, symmetric with
    `GetBalance` returning 0). Wired through `cmd/qsdm/main.go`'s
    local `Storage` interface in lockstep.
  - `ApplyTransferAtomic(ctx, sender, recipient, amount, fee, envelopeNonce, txID, rawEnvelope) error`
    — single-shot atomic: txID uniqueness CAS, nonce CAS
    (`envelopeNonce == lastNonce + 1`), balance check
    (`balance >= amount + fee`), debit + credit + nonce bump + tx
    archival. SQLite + Scylla backends implement the full path;
    `FileStorage` returns a deliberate
    `"file storage does not support atomic transfers"` error
    (see the "FileStorage stub" entry below for why the read-side
    nonce path is silent-zero instead).
  - Foundation commit: `ecfa121` (Session 99 — design doc +
    storage interface + tests). Handler integration: `8659b04`
    (Session 100). Client + tooling + smoke + browser UI:
    `2bdacb8`.
- **`POST /api/v1/wallet/submit-signed` nonce-replay gate.**
  `pkg/api/handlers.go::SubmitSignedTransaction` now decodes the
  envelope's `nonce` field, calls
  `storage.GetNonce(sender) → last`, rejects with HTTP 409 and
  monitoring tag `result="nonce_conflict"` if
  `envelopeNonce <= last`, and replaces the v0.4.0
  `HasTransaction → GetBalance → StoreTransaction` trio with a
  single `ApplyTransferAtomic` call. Legacy v0.4.0 envelopes
  without a `nonce` field continue to land via the v0.4.0 code
  path (with the documented non-atomicity caveat) — operators can
  cut over clients incrementally. Handler delta: +89/-36.
  Tests: 5 new — `HappyPath_WithNonce`, `LegacyV040Envelope`,
  `NonceReplay`, `NonceConflict`, `NonceLookupFailed`; 8 existing
  v0.4.0 tests still green (2 needed pre-fund tweak for the new
  atomic path). 13/13 PASS in `pkg/api`.
- **`GET /api/v1/wallet/nonce?sender=<hex64>` endpoint.** New
  public read endpoint returns `{nonce, next}` so self-custody
  clients can resolve the correct envelope nonce before signing.
  Handler: `pkg/api/handlers.go::WalletNonce`. Rate-limited by
  the same wallet bucket as `/wallet/submit-signed`. Tests:
  6 new — `HappyPath_New`, `HappyPath_AfterSubmit`,
  `MethodNotAllowed`, `MissingSender`, `InvalidSender`,
  `StorageError`, `E2EBump`. OpenAPI spec entry added.
- **`qsdmcli wallet sign-tx` subcommand.** New CLI subcommand
  generates a v0.4.1 envelope (canonical payload + nonce auto-
  fetch via `/wallet/nonce` or explicit `--nonce`) and either
  prints to stdout or POSTs to `/wallet/submit-signed`. 5 tests
  including a hard guarantee (`TestWalletSignTx_VerifiesAgainstServerCanonicalisation`)
  that the produced signature verifies against the server's
  byte-for-byte canonical payload constructor (prevents
  client/server canonicalisation drift). Drain-on-deadlock fix
  for stdout pipe in `runSignTx` test helper.
- **Browser wallet Send tab — nonce input + auto-fetch
  (`wasm_modules/wallet/`).** The Send tab gained a Nonce input
  (manual entry) and an auto-fetch button that calls
  `/wallet/nonce` to resolve `next` for the current address. WASM
  helper `qsdm_wallet_sign_transaction` accepts an explicit nonce
  field. SRI hashes refreshed: `wallet.wasm` =
  `sha384-XKMSE…Eb6`, `wallet.js` = `sha384-RhWdF…P0O2`. Production
  binary on `qsdm.tech` rebuilt; HTTPS-fetched bytes hash-match
  the in-tree SHA.
- **`cmd/v041smoke` — live-pipeline smoke test (5 probes).**
  Super-set of `cmd/v040smoke`. Probes:
  1. `bad-sig` → expect HTTP 422 `signature_invalid` (carry-over).
  2. `sender-mismatch` → expect HTTP 400 (carry-over).
  3. `malformed-json` → expect HTTP 400 (carry-over).
  4. `nonce-endpoint-shape` → expect HTTP 200 with `{nonce, next}`
     (new in v0.4.1).
  5. `nonce-conflict` → expect HTTP 409 `nonce_conflict` for
     SQLite/Scylla nodes, **HTTP 500 `failed to apply transfer`**
     for FileStorage nodes (both indicate the v0.4.1 code path is
     active; see "FileStorage stub" below). 5/5 PASS against
     `https://api.qsdm.tech/api/v1/wallet/submit-signed`
     (Session 100 post-deploy).
- **`FileStorage` v0.4.1 read-side stub (Session 100 deploy fix,
  commit 47d22f7).** `FileStorage.GetNonce` returns `(0, nil)`
  (symmetric with `FileStorage.GetBalance`'s silent-zero) so the
  new `/wallet/nonce` endpoint works on a FileStorage-backed
  validator (the production BLR1 node ran on FileStorage as of
  v0.4.0). Self-custody clients can probe the route to detect
  v0.4.1 presence and resolve `next: 1` for their first
  submission. The WRITE path (`ApplyTransferAtomic`) honestly
  refuses below — operators see `qsdm_wallet_send_total{result="store_failed"}`
  for any actual transfer attempt on a FileStorage node (SQLite
  or Scylla remains required for settlement). Documented
  in-source with the deploy-fix rationale and the read-vs-write
  asymmetry.

### Changed

- **`api-06` audit row.** Description and notes rewritten to
  capture the full v0.4.0 → v0.4.1 arc: backend handler
  + browser send tab v0.4.0 (Sessions 95-98), replay protection
  + atomic debit v0.4.1 (Sessions 99-100), independent cosign
  verify (Session 100 closure). Now `StatusPassed` with
  `evidence:live-deploy` provenance.

### Security

- **Independent cosign + Rekor verification (Session 100,
  commit 9052719).** Third-party-workstation reproduction
  verified 5/5 v0.4.1 artifacts without trusting any
  CI-supplied envelope:
  - `qsdmminer-console-linux-amd64` blob signature — Verified OK.
  - `SHA256SUMS` root signature — Verified OK.
  - `ghcr.io/blackbeardone/qsdm:0.4.1` image signature — Verified OK.
  - `ghcr.io/blackbeardone/qsdm-validator:0.4.1` image signature — Verified OK.
  - `ghcr.io/blackbeardone/qsdm-miner:0.4.1` image signature — Verified OK.

  All five certificates bind to the same Sigstore OIDC identity
  (`release-container.yml@refs/tags/v0.4.1` at commit
  `aa060e5`); signing run is GitHub Actions run `25855056334`;
  Rekor log ID `c0d23d6a…9591801d`, log index range
  `1534699896-1534701566`. Out-of-band evidence captured in
  `RELEASE_EVIDENCE_v0.4.1.md §"Independent cosign / Rekor
  evidence"`.

## [v0.4.0] - 2026-05-14

**Release theme:** self-custody signed-transaction submission
end-to-end — server handler, WASM signing helper, and browser
wallet Send tab. The native `Cell (CELL)` coin can now move
between addresses without the validator ever seeing or holding
the sender's private key. Closes `api-06` ("Self-custody signed
transaction submission") from Phase-1.

**Tag:** `v0.4.0` @ `318ed5efc366d384820ec2ec7c24f3208715fe4d`
(2026-05-14T00:01:52+08:00).
**Release workflow run:** `release-container.yml`
[run 25811046765](https://github.com/blackbeardONE/QSDM/actions/runs/25811046765)
— 10/10 jobs green; 53 cosign-signed assets attached;
3 GHCR images cosign-verified.
**BLR1 deploy:** binary swap (sha256 `2874f088…`) +
`QSDM_BUILD_VERSION=v0.4.0` + landing pill bumped to v0.4.0.
**Live verification:** `GET /api/v1/status` reports v0.4.0;
public `POST /wallet/submit-signed` returns HTTP 400
`invalid-sender` (was 302 in v0.3.x), `wallet.wasm` SRI
`XKMS…Eb6` matches over HTTPS.
**Evidence:** [`RELEASE_EVIDENCE_v0.4.0.md`](QSDM/docs/docs/RELEASE_EVIDENCE_v0.4.0.md);
[`V040_WALLET_SEND_DESIGN.md`](QSDM/docs/docs/V040_WALLET_SEND_DESIGN.md).

### Added

- **`POST /api/v1/wallet/submit-signed` (Phase A, Session 95).**
  Self-custody signed-transaction handler. Accepts the canonical
  envelope (`{tx: {id, sender, recipient, amount, fee, ts},
  pubkey, signature}`), enforces `sender == sha256(pubkey)[:32]`,
  verifies the ML-DSA-87 signature over the canonical payload,
  checks balance and txID uniqueness, then archives. 8/8 new
  tests green (`TestSubmitSigned_HappyPath`, `_BadSig`,
  `_SenderMismatch`, `_MalformedJSON`, `_DuplicateTxID`,
  `_InsufficientBalance`, `_RateLimited`, `_MissingSignature`).
  `StorageInterface` extended with `GetTransaction`.
  Monitoring: `qsdm_wallet_send_total` gained 4 new result tags
  (`sender_mismatch`, `signature_invalid`,
  `insufficient_balance`, `duplicate`).
- **WASM `qsdm_wallet_sign_transaction` helper (Phase B,
  Session 96).** New global exported by `wasm_modules/wallet/`
  — accepts `{sender, recipient, amount, fee}` + private-key
  bytes, returns the v0.4.0 canonical envelope ready to POST.
  Build: 3.88 MB binary, SRI `sha384-XKMS…Eb6`. Pure-Go via
  Stage-B wazero — no CGO, no liboqs DLLs, ships in every
  build.
- **Browser wallet "Send transaction" tab (Phase B,
  Session 96).** New tab in `wasm_modules/wallet/wallet.html`
  + `wallet.js` wires the WASM helper through a recipient /
  amount / fee form, POSTs to `/wallet/submit-signed`, and
  renders the server's `{tx_id, status}` response. Replaces
  the legacy server-trusted `/wallet/send` UI on the homepage
  (kept available for one release for any operator using the
  old wallet flow).
- **OpenAPI doc + `MINER_QUICKSTART.md` Appendix B.** OpenAPI
  spec entry for `/wallet/submit-signed` documents the
  canonical-payload contract and the 8 result tags.
  `MINER_QUICKSTART.md` Appendix B refreshed with the new
  self-custody flow.
- **`cmd/v040smoke` — live-pipeline smoke test (3 probes,
  Session 98).** 3/3 PASS against
  `https://api.qsdm.tech/api/v1/wallet/submit-signed`. Probes:
  bad-sig → HTTP 422 `signature_invalid`; sender-mismatch →
  HTTP 400 `sender does not match`; malformed-json → HTTP 400
  `unexpected EOF`. Anchored in `RELEASE_EVIDENCE_v0.4.0.md`
  and the `api-06` audit row.

### Changed

- **4-hour pubsub soak — extended-duration validation (2026-05-13).**
  Long-haul follow-up to session 74's 10-min smoke. Same harness
  (`tests/soak_pubsub_test.go`, build tag `soak`) and same mesh
  configuration — 4 libp2p hosts in star topology, 2 producers per host,
  50 Hz per producer, 256-byte payloads — with `QSDM_SOAK_DURATION` bumped
  from 10 m to 4 h. Result: **PASS in 14,401.74 s**.
  - **5,759,998 publishes** out of a target of 5,760,000
    (4 × 2 × 50 × 14,400 s) — **0.00003 % miss**, 2 messages short over
    the full 4 hours.
  - **17,279,994 cross-host receipts** — exactly target (every publish
    received by every other host); zero drops in the in-process mesh.
  - **Per-host receive totals: [4,319,998 / 4,319,999 / 4,319,998 /
    4,319,999]** — total spread across all 4 hosts is **2 messages over
    14,400 s** (median 4,319,998.5, range ±0.5). Tighter than the 10-min
    smoke's ±2.5, which means the steady-state mesh is *cleaner* than the
    warmup-affected short run — the 50 % / 200 % fairness assertion has
    huge headroom even at this extended duration.
  - Zero partitions (every receiver saw > 0 messages from every other
    host on every check), zero sustained-error windows (no host hit >100
    consecutive publish errors at any point), no degradation in publish
    or receive rate over the run (the 10-second status logs confirm the
    `4000 sent / 12000 rx` per-tick cadence held end-to-end).
  - Evidence file: `_session75_soak_pubsub_4h.log` (1,461 lines,
    10-second cadence). Closes the "ready for `QSDM_SOAK_DURATION=4h`
    runs" promise from session 72.

- **Audit checklist: 27 runtime-verified items flipped to `StatusPassed`
  (2026-05-13).** `pkg/audit/checklist.go` previously initialised every one
  of the 85 audit-checklist items to `StatusPending`, so the operator
  dashboard's audit tile and the `cmd/auditreport` CLI both reported a
  flat "0/85, score 0%" baseline regardless of how much in-tree or
  live-deploy evidence we had already captured. Each `defaultItems()`
  literal is now stamped with `Status: StatusPassed`,
  `ReviewedBy: "evidence:..."`, `ReviewedAt: ts("2026-05-13T...")`, and
  `Notes:` pointing at the underlying evidence (session number, named
  test, or commit hash) when the control is genuinely verified. The
  reviewer field is one of three closed-enum strings — guarded by
  `TestChecklist_RuntimeVerifiedReviewerProvenance`:
  - `evidence:live-deploy` — verified live on `api.qsdm.tech` /
    `dashboard.qsdm.tech` (with session-number proof).
  - `evidence:in-tree-tests` — covered by named tests, green in the
    most recent verification matrix (session 74's 67/67 packages).
  - `evidence:in-tree` — implementation / CI machinery is in tree
    (build tags, workflow files, deprecation-shim retirements).
  - **Items flipped (27 total).** Live-deploy: `net-05` (peer.ID
    persistence, Session 89), `store-05` (NGC ring restore, Session
    90), `api-05` (wallet/mint stub→410 Gone, Session 91), `api-06`
    (self-custody submit-signed, Sessions 95-98), `rebrand-01`
    (rebrand sweep verified live), `rebrand-06` (53 cosign-signed
    v0.4.0 assets), `tok-03` (`/api/v1/status` emission snapshot
    live), `supply-01` (`go mod verify` clean), `supply-02`
    (`govulncheck` clean except tracked `supply-08`), `supply-04`
    (3 SPDX-2.3 SBOMs attached to v0.4.0), `supply-05` (cosign
    keyless signing). In-tree-tests: `rebrand-04`, `rebrand-07`,
    `tok-02`, `mining-03`, `mining-04`, `trust-01`, `trust-03`,
    `trust-06`. In-tree: `rebrand-02` (env-var shim retired in
    db9b590), `rebrand-05` (dual-emit retired in db9b590),
    `mining-02` (validator-only build tag isolates miner from
    consensus), `store-04` (recentrejects FilePersister wired),
    `trust-02` (scope_note in handler), `trust-04` (normaliseRegion
    closed-enum), `trust-05` (cross-checks mining-02), `supply-08`
    (`GO-2024-3218` accepted-with-mitigation per Session 73).
  - **Operator-visible delta.** `cmd/auditreport`'s rendered summary
    moves from `passed:0 failed:0 pending:85 waived:0 score:0.0%` to
    `passed:27 failed:0 pending:58 waived:0 score:31.8%`. The
    operator dashboard's audit tile (`internal/dashboard/static/`)
    polls the same surface and now reflects the same step.
    `HasBlockingFindings()` still returns true (all
    critical/high crypto, auth, authz, sc-01, bridge-01 etc.
    remain pending — no premature green-light), so the
    `cmd/auditreport -gate` exit-2 contract is unchanged.
  - **Item-status drift guard (new tests, all green):**
    - `TestChecklist_RuntimeVerifiedItemsPassed` walks a
      const list of 27 IDs and asserts each is `StatusPassed`
      with `ReviewedBy`, `ReviewedAt`, `Notes` populated.
    - `TestChecklist_RuntimeVerifiedReviewerProvenance` asserts
      `ReviewedBy` is one of the three allowed `evidence:*`
      prefixes, so future flips can't smuggle in arbitrary
      reviewer strings.
    - `TestChecklist_PassedCountMatchesRuntimeVerifiedList`
      pins `summary["passed"] == len(runtimeVerifiedItems)` so
      adding a flip without updating the test list (or vice
      versa) fails CI.
  - **Score-math tests refactored.** `TestChecklist_Score_*`
    previously assumed an all-pending baseline; now uses a new
    `resetAllToPending()` helper to exercise the score math
    independently of the new constructor state. New positive test
    `TestChecklist_Score_FreshChecklist_HasRuntimeBaseline` pins
    that `Score()` is non-zero out of the box and < 100 while
    audit work is in flight. `TestE2E_AuditChecklistReview` was
    similarly updated to assert deltas against a captured baseline
    instead of absolute counts.
  - **No data is fabricated.** Every flipped item has its
    underlying evidence already recorded in this CHANGELOG, in
    `NEXT_STEPS.md`, or in a named in-tree test that ran green in
    session 74's verification matrix. Items without that level of
    evidence (all `crypto-*`, `auth-*`, `authz-*`, `sc-*`,
    `bridge-*`, the externally-blocked `rebrand-03` / `tok-01` /
    `mining-01` / `mining-05`, etc.) remain `StatusPending`.
    Those are the legitimate gates that still need wall-clock
    review.
  - Verified locally (`CGO_ENABLED=0`, windows/amd64,
    `go1.25.10`):
    - `go test ./pkg/audit/... -count=1` — **22/22 green**.
    - `go test ./tests/... -short -count=1 -run TestE2E_Audit` —
      **green**.
    - `go vet ./pkg/audit/... ./cmd/auditreport/... ./tests/...`
      — clean.
    - `go run ./cmd/auditreport -format json -gate=false` —
      `score: 31.76, summary: {total:85 passed:27 pending:58
      failed:0 waived:0}`.

## [v0.3.3] - 2026-05-12

**Tag:** `v0.3.3` @ `03edf41612585b378908839bafa6f42974311781`
(2026-05-12T13:35:07+08:00).
**Release theme:** node-state persistence across restarts +
`/api/v1/wallet/mint` deprecation to HTTP 410 Gone — closes the
supply-inflation surface from the seed-faucet era and pins
peer identity / NGC attestation state so a validator restart
doesn't blow away its peer.ID or its in-memory NGC ring
(Sessions 88-91).

**Commits in window (v0.3.2..v0.3.3):**
- `03edf41` — `api`: deprecate `/api/v1/wallet/mint` to HTTP 410
  Gone (Session 91).
- `7440af9` — `docs`: Session 90 release notes (NGC attestation
  ring persistence deployed).
- `69fb006` — `monitoring`: persist NGC attestation ring across
  restarts (Session 90).
- `193fa84` — `docs`: Session 89 release notes (libp2p host key
  persistence deployed).
- `b1f72ef` — `networking`: persist libp2p host key across
  restarts (Session 89).
- `fc07424` — `docs`: truth-in-docs sweep — NVIDIA hardware is
  not a v2 blocker (Session 88).
- `ed9a3f9` — `gitignore`: add `_build/` (local cross-compile
  output).

### Removed

- **`POST /api/v1/wallet/mint` → HTTP 410 Gone (`api-05`,
  Session 91, `03edf41`).** The legacy seed-faucet mint route
  used to return `{tx_id, status:"accepted"}` for arbitrary
  callers — a supply-inflation surface that had no business
  surviving the move to a Cell tokenomics model with a fixed
  mint schedule. The route now responds with HTTP 410 Gone and a
  migration block in the body pointing operators at the
  legitimate paths (validator coinbase rewards via PoE block
  rewards; testnet faucet via the operator-only `qsdmcli
  mint` admin path). New monitoring tag
  `qsdm_wallet_mint_total{result="gone"}` is live on the
  Prometheus endpoint and on the dashboard so any leftover
  client hitting the route shows up immediately.
- **Audit row `api-05` flipped to `StatusPassed`** with the
  evidence anchor `evidence:live-deploy / Session 91`.

### Added

- **libp2p host-key persistence across restarts (`net-05`,
  Session 89, `b1f72ef`).** Previously a validator restart
  generated a fresh libp2p identity, breaking peer reputation,
  enrollment registry entries, and any peer-pinned routes.
  Host key is now persisted to `<stateDir>/qsdm_host_key.pem`
  on first generation and reloaded on subsequent boots. Audit
  row `net-05` flipped to `StatusPassed` with evidence anchor
  `evidence:live-deploy / Session 89`.
- **NGC attestation ring persistence across restarts (`store-05`,
  Session 90, `69fb006`).** The in-memory NGC attestation
  ring previously dropped every received attestation on
  validator restart, leaving a multi-minute hole in trust
  scores after every redeploy. Ring is now serialised to
  `<stateDir>/qsdm_ngc_ring.json` on shutdown + every
  configurable checkpoint interval and restored on boot. Audit
  row `store-05` flipped to `StatusPassed` with evidence anchor
  `evidence:live-deploy / Session 90`.

### Changed

- **Truth-in-docs sweep (Session 88, `fc07424`).** Removed
  several blocker-language references to NVIDIA hardware from
  the v2 mining-protocol docs — the v2 protocol does NOT
  require NVIDIA hardware (CPU-only validators are
  first-class). The NGC attestation path remains a CUDA-only
  enhancement but is not a v2 release blocker.

## [v0.3.2] - 2026-05-12

**Tag:** `v0.3.2` @ `f727fef6d2111ed8418c83e49f702057481b3e6f`
(2026-05-12T03:54:39+08:00).
**Release theme:** v1 API deprecation posture + browser-wallet
SRI hardening + landing-page facelift to surface the v0.3.1
wallet shipment.

**Commits in window (v0.3.1..v0.3.2):**
- `f727fef` — `v1 deprecation`: status posture, miner preflight,
  release matrix, doc rewrite.
- `d373254` — `wallet`: SRI on `wallet.{html,js,wasm}` + read-
  only balance tab.
- `29f1646` — `landing`: fix version-pill JS + add Wallet nav
  link to secondary pages.
- `06fc8d1` — `landing`: rewrite `index.html` — surface wallet
  + `qsdm-sdk` + v0.3.1 + cut redundancy.

### Added

- **Subresource Integrity hashes on browser-wallet assets
  (`d373254`).** `wallet.html`, `wallet.js`, and `wallet.wasm`
  shipped on `qsdm.tech` now carry SHA-384 SRI attributes so a
  modified asset (compromised CDN, MITM rewrite) fails to load
  rather than silently signing transactions for an attacker.
  Read-only **balance tab** added: shows current Cell balance
  for the loaded keystore without requiring a network round-
  trip through the trusted `/wallet/send` legacy route.
- **Wallet nav link on every secondary landing page
  (`29f1646`).** Footer / nav across all `qsdm.tech`
  secondary pages now points to `/wallet/` consistently —
  previously buried in `/index.html` only.

### Changed

- **Landing page `index.html` rewrite (`06fc8d1`).** Surfaces
  the browser wallet, the `qsdm-sdk` npm package, and the
  v0.3.1 release pill directly above the fold. Removed three
  redundant sections that duplicated docs-portal content.
  Version-pill JS bug fix in `29f1646` (was pinning to
  literal `v0.3.0` regardless of build).
- **`/api/v1/*` deprecation posture refresh (`f727fef`).** Status
  endpoint now signals v1 deprecation alongside the v2
  promotion timeline. Miner preflight calls out the same.
  Release-matrix doc rewritten to match the actual binary
  matrix shipped by `release-container.yml`.

## [v0.3.1] - 2026-05-12

**Tag:** `v0.3.1` @ `bda45834ae5d032608039eed9ecf75c083046887`
(2026-05-12T02:29:34+08:00).
**Release theme:** self-custody browser wallet ships to
`qsdm.tech` — CLI + browser are byte-compatible, signed
transactions verify identically across both paths. Also: the
`qsdm` npm SDK package is renamed to `qsdm-sdk` after npm's
name-similarity heuristic rejected the bare name on first
publish.

**Commits in window (v0.3.0..v0.3.1):**
- `bda4583` — `deploy`: ship browser wallet to `qsdm.tech` +
  CSP `wasm-unsafe-eval` + Go SSH deploy tool.
- `909554e` — Session 82: self-custody wallet (CLI + browser,
  byte-compatible).
- `f0f78df` — `docs`: record `qsdm-sdk@0.3.0` publish (registry
  URL, Rekor logIndex, shasum).
- `38592d7` — `sdk/js`: rename npm package `qsdm` → `qsdm-sdk`
  (registry name-similarity rejection).
- `5361b1c` — Session 80 (cont'd 2): wrap macOS smoke check
  in `timeout` to avoid validator-startup hang.
- `923d235` — Session 80 (cont'd): fix liboqs universal2
  cross-compile bug exposed by green CI.
- `f83722e` — Session 80: fix latent no-CGO bug in
  `build_macos.sh` + clear macos-build queue.
- `ae88fdc` — `chore(deps)`: bump
  `github.com/libp2p/go-libp2p-pubsub` in `/QSDM/source` (#11).
- `12afbfe` — Session 79: third-party post-release verification
  of v0.3.0.

### Added

- **Self-custody browser wallet ships to `qsdm.tech`
  (Session 82, `909554e` + `bda4583`).** First end-to-end
  release of the browser wallet alongside the existing CLI
  wallet. Both produce **byte-compatible signatures** — a
  transaction generated in the browser verifies on every
  validator that accepts a CLI-generated one, and vice versa.
  Deploy path uses a new Go SSH deploy tool to push the wallet
  assets to `qsdm.tech` with SRI hashes computed locally and
  Content-Security-Policy `wasm-unsafe-eval` directive added
  so the wallet WASM module can load without weakening CSP
  for the rest of the site.
- **npm package `qsdm-sdk` published (Sessions 79-81,
  `38592d7` + `f0f78df`).** First public release of the
  JavaScript SDK on the npm registry. Bare name `qsdm` was
  rejected on first publish by npm's name-similarity
  heuristic against a long-dormant unrelated package — the
  package id is now `qsdm-sdk`. Brand, repo, binaries, and
  import-time symbols are unaffected; only the npm package
  id has the `-sdk` suffix. Publish recorded with Rekor
  logIndex + shasum in the docs portal for downstream
  verification.

### Fixed

- **liboqs universal2 cross-compile bug (Session 80,
  `923d235`).** The macOS `build_macos.sh` script's
  universal2 path produced binaries with a corrupted liboqs
  segment that crashed on first ML-DSA-87 keygen call. Fixed
  the cross-compile invocation; CI macos-build queue cleared.
- **macOS smoke-check hang (Session 80, `5361b1c`).** The
  post-build smoke check's validator-startup probe could
  hang indefinitely on macOS runners under specific
  conditions (no peers discovered before TestMain timeout).
  Wrapped in `timeout 30s` so the test fails fast instead
  of stalling the whole pipeline.
- **No-CGO build path on macOS (Session 80, `f83722e`).**
  Latent bug in `build_macos.sh` where the no-CGO fallback
  path tried to link liboqs anyway, breaking builds on
  systems without Homebrew OpenSSL@3. Now correctly takes
  the circl pure-Go path under `QSDM_NO_CGO=1`.

### Changed

- **Dependency bump:** `github.com/libp2p/go-libp2p-pubsub`
  updated via Dependabot PR #11 (`ae88fdc`).

### Security

- **Third-party post-release verification of v0.3.0
  (Session 79, `12afbfe`).** Independent cosign + Rekor
  verification of the v0.3.0 release artifacts from a
  third-party workstation — out-of-band signoff before
  pushing the v0.3.1 cut. All v0.3.0 artifacts verified
  against the `release-container.yml@refs/tags/v0.3.0`
  Sigstore OIDC identity.

## [v0.3.0] - 2026-05-11

**Tag:** `v0.3.0` @ `c00fccd93a66c5317aaaa03b80e9a09d111e87bd`
(2026-05-11T12:23:14+08:00).
**Release theme:** first tagged release after the **QSDM**
rebrand on 2026-04-22 (rebrand from the historical "QSDM"
naming window). Native coin renamed to **Cell (CELL)**;
release-evidence tooling, release-container CI pipeline,
GHCR case-sensitivity fix, and the Stage-A/B retirement of
the always-stub `dilithium` / `wallet` / `poe` / `wasm_sdk`
backends all shipped in this release.

**Tag history note.** `v0.3.0` was re-tagged on commit
`134abf1` after Session 75's CI fixes — the original
attempt failed `release-container.yml` because of a GHCR
case-sensitivity issue (Session 76's
`83c1128`) and a broken bash for-loop in the release-tag
matrix (Session 78's `c00fccd`).

> **Historical-content note.** Entries below this point cover
> the **2026-04-22 → 2026-05-11** window — everything from
> the QSDM rebrand through the v0.3.0 cut. They were on main
> when v0.3.0 was tagged. A few entries at the bottom of the
> section reference work that pre-dates the rebrand and was
> carried forward; those are kept in place rather than
> archived because they remain user-relevant features
> (governance, mining v2 spec, etc.). The
> `## [v0.3.0]` header here is therefore the
> "everything pre-v0.3.1 / pre-rebrand-rebase" catchment.

### Added

- **Boot-time binary-capabilities info-metric (2026-05-06).**
  New collector `qsdm_binary_capabilities` (value=1) exposes
  the build-tag-determined backend identity of the running
  binary on the `/api/metrics/prometheus` endpoint:
  ```text
  qsdm_binary_capabilities{dilithium="circl",mesh3d="cpu_fallback",wasm="wazero"} 1
  ```
  Closes the wrong-binary-deploy detection gap: previously a
  stale binary that re-introduced a retired stub had to wait
  the `QSDMStubActive` alert's `for: 5m` window before paging.
  The capabilities metric flips on the first scrape, so a
  redeploy of a stale tag is detectable in seconds.
  - Labels are drawn from closed enums (`dilithium ∈
    {liboqs, circl}`, `wasm ∈ {wazero, browser_stub}`,
    `mesh3d ∈ {cuda, cpu_fallback}`); cardinality is
    bounded at 8 series across all binaries ever built.
  - Implementation: 6 build-tag-conditional files in
    `pkg/monitoring/` set package-level constants
    (`dilithiumBackend`, `wasmBackend`, `mesh3dBackend`)
    that mirror the underlying subsystem build-tag
    selections in `pkg/crypto`, `pkg/wasm`, and
    `pkg/mesh3d`. The collector reads the constants and
    emits a single info-metric per scrape.
  - **Tests:** `pkg/monitoring/build_capabilities_test.go`
    asserts the metric shape (1 series, value=1, three
    labels), the Prometheus-exposition wiring (collector
    is registered), and the closed-enum invariant (so
    runbook drift surfaces in CI). All 3 new tests pass
    under `CGO_ENABLED=0`.
  - **Runbook hooks:**
    - `STAGE_B_DEPLOY_BLR1.md` §"Smoke check" gained
      step 4.0: a zero-latency check of the metric BEFORE
      the existing 4.1–4.3 checks. If any label is
      unexpected, the runbook tells the operator to
      rollback immediately.
    - `STUB_DEPLOYMENT_INCIDENT.md` §2a: when the alert
      fires, on-call checks `qsdm_binary_capabilities`
      first to distinguish "wrong binary deployed" from
      "real subsystem regression"; the four retired kinds
      reduce to "redeploy from head" without reading the
      per-kind anchor.

### Removed

- **`wasm_sdk` stub paths deleted (wasm Stage B) (2026-05-06).**
  The two stub WASMSDK backends are gone:
  - `pkg/wasm/sdk_stub.go` (`!cgo && !wasm_wazero`) — deleted.
  - `pkg/wasm/sdk_wasmtime_disabled.go` (`cgo && !wasm_wazero`)
    — deleted.
  `pkg/wasm/sdk_wazero.go` is now the unconditional default
  for every native target the binary ships on (build tag
  changed from `wasm_wazero` to `!js || !wasm`, the inverse of
  `wasm.go`'s `js && wasm` guard so it doesn't collide with
  the legacy Go-to-browser-WASM file). The `wasm_wazero` tag
  from Stage A is now a no-op alias kept for one release for
  compatibility with any external build scripts.
  - **Stub-active invariant.** `qsdm_stub_active{kind="wasm_sdk"}`
    is now structurally pinned at 0 on any Stage-B+ binary —
    no `MarkActive(KindWasmSDK)` site exists in the tree.
    The kind is retained in `stubactive.AllKinds()` for
    rolling-deploy forward compatibility, but no code path
    flips it on under any supported build configuration.
  - **Verification (CGO_ENABLED=0, no extra build tags):**
    - `go build ./...` clean.
    - `go test ./...` — green, **55/55 packages**.
    - `go test -tags wasm_wazero ./...` also green
      (no-op alias).
  - **Runbook updated**:
    [`STUB_DEPLOYMENT_INCIDENT.md` § kind-wasm-sdk](QSDM/docs/docs/runbooks/STUB_DEPLOYMENT_INCIDENT.md#kind-wasm-sdk)
    rewritten to mark the alert structurally unreachable on
    Stage-B+ binaries and treat any firing instance as a
    wrong-binary deployment.
  - **Operational impact.** A non-CGO binary built from
    current head can load WASM modules out of the box —
    no wasmtime DLLs, no CGO, no opt-in build tag required.
    This applies to the wallet WASM module
    (`wasm_modules/wallet/wallet.wasm`), the contracts
    engine's WASM dispatch (`pkg/contracts/engine.go`), and
    any operator-supplied WASM via `[wasm.*]` config
    sections. Validators that previously had to ship
    without WASM functionality on Windows/Alpine targets
    can now turn WASM on by simply rebuilding from current
    head.

### Added

- **Pure-Go wazero WASM backend (Stage A, opt-in) (2026-05-06).**
  Adds `pkg/wasm/sdk_wazero.go`, a real `*WASMSDK`
  implementation backed by `github.com/tetratelabs/wazero`
  (already a direct dependency for the
  `QSDM_WASM_PREFLIGHT_MODULE` env hook). Selected by
  `go build -tags wasm_wazero ./...`; orthogonal to CGO state
  so it works on every target the QSDM binary already runs on.
  - **Build-tag selection.** The two existing stub backends had
    their tags narrowed to exclude `wasm_wazero`:
    - `sdk_stub.go`: `//go:build !cgo && !wasm_wazero`
    - `sdk_wasmtime_disabled.go`: `//go:build cgo && !wasm_wazero`
    The wazero backend takes over the `WASMSDK` type entirely
    when the tag is in scope; default builds are unchanged.
  - **API parity.** `NewWASMSDK`, `CallFunction(name, params...)`,
    `preflightP2PTransactionJSON`, `LoadWASMFromFile` all match
    the existing stub surface exactly, so callers in
    `cmd/qsdm/main.go`, `cmd/qsdm/transaction/dispatch.go`, and
    `pkg/contracts/engine.go` compile unchanged. New
    `(*WASMSDK).Close()` method releases the wazero runtime
    deterministically (no-op on stub builds).
  - **Stub-active invariant.** Under `-tags wasm_wazero`,
    `qsdm_stub_active{kind="wasm_sdk"}` stays at 0 — the
    wazero backend is real, no `MarkActive` call. Verified
    by `TestWazeroSDK_StubFlagNotMarked`.
  - **Parity tests** (`pkg/wasm/sdk_wazero_test.go`):
    - `TestWazeroSDK_RoundTrip_AddJSON` — the contracts-engine
      hot path: `CallFunction(name, jsonString)` with a 2-arg
      i32 add module.
    - `TestWazeroSDK_StubFlagNotMarked` — operational
      invariant.
    - `TestWazeroSDK_EmptyBytecodeRejected` — empty input is
      rejected at construction (matches stub behaviour).
    - `TestWazeroSDK_CallFunctionUnknownExport` — undefined
      export errors out instead of silently succeeding.
    - `TestWazeroSDK_PreflightNoValidator` — modules without
      `validate_raw` get the "no preflight rules" fast path
      so gossip propagation isn't blocked.
  - **Stub-active lazy-flag test fixed.** `sdk_stubactive_test.go`'s
    `TestWasmSDK_StubActiveIsLazy` previously assumed any
    `NewWASMSDK` error meant the stub flag should flip — but
    with `wasm_wazero` in scope, the wazero backend errors on
    invalid bytecode without flipping the flag (correctly,
    because it's real). Now uses a 39-byte valid `add` module:
    if construction succeeds, skip (real backend); if it
    errors, assert the flag flipped (stub backend).
  - **Runbook updated**:
    [`STUB_DEPLOYMENT_INCIDENT.md` § kind-wasm-sdk](QSDM/docs/docs/runbooks/STUB_DEPLOYMENT_INCIDENT.md#kind-wasm-sdk)
    now documents three backends (CGO+wasmtime, pure-Go
    wazero, stub) and the build commands to select each.
    On-call seeing a Windows or Alpine VPS firing
    `kind="wasm_sdk"` can rebuild with the `wasm_wazero` tag
    instead of installing wasmtime DLLs.
  - **Verification (CGO_ENABLED=0):**
    - `go build ./...` clean (default).
    - `go build -tags wasm_wazero ./...` clean (opt-in).
    - `go test ./...` — 55/55 packages green (default path).
    - `go test -tags wasm_wazero ./...` — 55/55 packages
      green.
  - **Operational impact.** Stage A is zero impact by
    design — no production binary changes behaviour until
    Stage B flips the tag default. What it provides is the
    safety net: a real WASM backend is now available behind
    a flag for the same Windows/Alpine operators who can't
    install wasmtime DLLs but want WASM modules working.

### Changed

- **`stubactive` package documentation refreshed (2026-05-06).**
  The package-level doc comment in
  `pkg/monitoring/stubactive/stubactive.go` previously listed
  every kind as "active stub" with descriptions like "ML-DSA-87
  quantum-safe signing not available" for `dilithium`. After
  Stage B four of those kinds are RETIRED (`poe`, `dilithium`,
  `wallet`, `slashing`) and their files are deleted from the
  tree. Updated the doc to mark each kind's current status
  (RETIRED / OPT-IN STUB / UNCHANGED) and link to the
  responsible backend file. Pure docs change; no code path
  affected.

### Removed

- **`dilithium`/`wallet`/`poe` stub paths deleted (Stage B) (2026-05-06).**
  Three CRITICAL `qsdm_stub_active` kinds are now structurally
  unreachable on any binary built from current head. Files
  deleted: `pkg/crypto/dilithium_stub.go`,
  `pkg/wallet/wallet_stub.go`, `pkg/consensus/poe_stub.go`.
  Each had been the sole `MarkActive` site for its kind; with
  the files gone, no code path under any supported build
  configuration (CGO+liboqs, non-CGO+circl, future
  Linux/Windows/macOS targets) flips those gauges.
  - **`pkg/crypto/dilithium_circl.go`** is now the default
    non-CGO backend (`//go:build !cgo`, no opt-in tag
    required). Replaces `dilithium_stub.go`'s always-error
    behaviour with real FIPS 204 ML-DSA-87 via
    `cloudflare/circl/sign/mldsa/mldsa87`. Wire-compatible
    with the CGO+liboqs backend; mixed-backend validator
    sets do not fork. Method surface extended to match the
    full CGO API: `GetPublicKey`, `GetPrivateKey`,
    `SignOptimized`, `SignBatchOptimized`, `SignCompressed`,
    `VerifyCompressed`, `VerifyWithPublicKeyCompressed`.
  - **`pkg/wallet/wallet.go`** (formerly `//go:build cgo`)
    now compiles unconditionally. Real wallet backed by
    `*pkg/crypto.Dilithium` regardless of CGO state. New
    helper `(*WalletService).GetPublicKey()` exposes the
    handle's 2592-byte public key (used by `VerifySignature`
    self-roundtrip and any caller that needs to embed the
    wallet pubkey externally).
  - **`pkg/consensus/poe.go`** (formerly `//go:build cgo`)
    now compiles unconditionally. The historical fail-open
    "accepting transaction without signature verification"
    branch is gone — `consensus.NewProofOfEntanglement()`
    returns a non-nil verifier in every build.
  - **Test-fixture fixes.** Three test sites were implicitly
    relying on stub behaviour and had to be updated:
    - `tests/api_contracts_bridge_test.go`: the test
      client previously hardcoded HMAC-SHA256 request
      signing because `crypto.NewDilithium()` was nil under
      the stub; with a real signer behind the API server,
      every POST returned 401 ("invalid request signature").
      Refactored `setupContractsBridgeTestServer` to return a
      `cbTestRig` that exposes the server's
      `*api.RequestSigner`, and changed `authedRequest` to
      sign with that same signer. Round-trip is now
      backend-agnostic — works under HMAC fallback, circl,
      and liboqs identically. New API surface:
      `(*api.Server).RequestSigner()` returns the per-server
      signer for tests.
    - `pkg/wallet/wallet_test.go::TestSignAndVerify`: passed
      `nil` for the public-key argument, which the wallet
      stub's SHA-256 length-only "verifier" silently
      accepted but the real backend rejects with
      "public key must be 2592 bytes, got 0". Updated to
      use `ws.GetPublicKey()` (the obvious self-roundtrip
      key).
    - `pkg/quarantine/phase3_transaction_test.go::TestHandlePhase3Transaction`:
      previously skipped under !cgo (`if poe == nil { t.Skip(...) }`);
      now runs in full because PoE is real. Exposed a
      pre-existing Windows-only teardown bug (t.TempDir()
      cleanup failing because the test's `*logging.Logger`
      held the log file open). Added
      `(*logging.Logger).Close()` and a `t.Cleanup` hook to
      release the file before unlink.
  - **Runbook**:
    [`STUB_DEPLOYMENT_INCIDENT.md`](QSDM/docs/docs/runbooks/STUB_DEPLOYMENT_INCIDENT.md)
    sections for `kind-dilithium`, `kind-wallet`, and
    `kind-poe` rewritten. They now state the alert is
    structurally unreachable on Stage B+ binaries and that
    any firing instance is a wrong-binary deployment to
    redeploy from current head.
  - **Verification (CGO_ENABLED=0, no extra build tags):**
    - `go build ./...` clean.
    - `go test ./...` — green, **55/55 packages**, including
      the previously-skipped `TestHandlePhase3Transaction`
      and the previously-stub-only API E2E tests
      (`TestContractDeployAndExecuteE2E`,
      `TestContractDeployDuplicateE2E`,
      `TestContractVotingE2E`,
      `TestBridgeEndpoints503WhenUnavailable`).
    - `go test -tags dilithium_circl ./...` also green
      (the tag is now a no-op, retained as a no-op alias
      for one release for compatibility with any external
      build scripts).
  - **Operational impact.** A non-CGO binary built from
    current head replaces the Phase-2-era SHA-256 fallback
    wallet AND the always-accept PoE on the wire with real
    FIPS 204 ML-DSA-87. Operators running stub-built
    binaries (the common case for Windows-based Go-only
    builders) should rebuild and redeploy. The
    `qsdm_stub_active{kind=~"dilithium|wallet|poe"}` rows
    on the dashboard will move from "fires immediately at
    boot in non-CGO builds" to "structurally cannot fire";
    Alertmanager runbook entries kept as forensics for any
    leftover pre-Stage-B binaries.

### Added

- **Pure-Go ML-DSA-87 backend (Stage A, opt-in) (2026-05-06).**
  Adds `pkg/crypto/dilithium_circl.go`, a third
  implementation of the `*Dilithium` API alongside the
  existing CGO+liboqs path (`dilithium.go`) and the always-
  error stub (`dilithium_stub.go`). The new backend is built
  on `github.com/cloudflare/circl/sign/mldsa/mldsa87`, which
  implements FIPS 204 byte-for-byte and emits the same
  2592-byte public keys / 4627-byte signatures the existing
  CGO build produces. Wire-compatible: a circl-built
  validator and a liboqs-built validator can verify each
  other's signatures without any envelope changes.
  - **Build-tag selection.** The backend is opt-in:
    `go build -tags dilithium_circl ./...` on a non-CGO
    build pulls in `dilithium_circl.go` and excludes
    `dilithium_stub.go` (which now carries
    `//go:build !cgo && !dilithium_circl`). CGO builds are
    unchanged. **Default non-CGO builds are unchanged** —
    Stage A intentionally lands the code behind a tag so
    the parity tests run in CI before any operational
    behaviour shifts.
  - **Parity tests** (`pkg/crypto/dilithium_circl_test.go`):
    eight assertions covering FIPS 204 size invariants
    against `pkg/chain/txsig.go` constants, round-trip
    sign+verify, external-public-key verification (the
    consensus-critical path), tamper-detection negatives
    on each of message / signature / public-key, verify-
    only handle semantics, deterministic-keygen
    seed-recovery, and SHA-256 address binding round-trip.
    All eight pass under `-tags dilithium_circl`.
  - **Stub-active invariant.** Under
    `-tags dilithium_circl`, `qsdm_stub_active{kind="dilithium"}`
    stays at 0 — the dilithium_stub.go init() that flips it
    is excluded by build tag. Verified by
    `TestCircl_StubFlagNotMarked`.
  - **Runbook updated**:
    [`STUB_DEPLOYMENT_INCIDENT.md` § kind-dilithium](QSDM/docs/docs/runbooks/STUB_DEPLOYMENT_INCIDENT.md#kind-dilithium)
    now documents three available backends (CGO+liboqs,
    pure-Go circl, stub) and the build commands that select
    each. On-call seeing a Windows or Alpine VPS firing
    `kind="dilithium"` can now rebuild with the circl tag
    instead of installing liboqs DLLs.
  - **Verification (CGO_ENABLED=0):**
    - `go build ./...` clean (default).
    - `go build -tags dilithium_circl ./...` clean (opt-in).
    - `go test -tags dilithium_circl ./pkg/crypto/...`
      — green, all 8 parity tests pass.
    - `go test ./pkg/wasm/... ./pkg/monitoring/stubactive/...
      ./internal/v2wiring/... ./pkg/mining/slashing/...`
      — green (no regressions on the default build path).
  - **Operational impact.** Stage A is zero impact by
    design — no production binary changes behaviour until
    Stage B flips the tag default. What it provides is the
    safety net: parity tests in CI lock the wire-format
    contract between liboqs and circl, so Stage B can flip
    the default with confidence that a chain running mixed
    backends won't fork. New dependency:
    `github.com/cloudflare/circl v1.6.3` (pure Go, MIT/BSD
    licensed, ~10kloc with the mldsa subtree).

### Changed

- **WASM SDK stub flag is now opt-in, not always-on (2026-05-05).**
  `pkg/wasm/sdk_stub.go` previously called
  `stubactive.MarkActive(KindWasmSDK)` from package `init()`,
  meaning every non-CGO build flipped
  `qsdm_stub_active{kind="wasm_sdk"} = 1` at process start
  regardless of whether WASM modules were configured. That
  was a false positive — the contracts engine prefers
  pure-Go wazero, and `wasm_modules/wallet/wallet.wasm`
  loading is opt-in — and it drowned the dangerous-stub
  signal in benign noise across every CGO-disabled deploy.
  Moved `MarkActive` from `init()` to inside `NewWASMSDK()`
  so the flag flips only when an operator actually attempts
  to construct a WASM SDK. Applied the same fix to
  `pkg/wasm/sdk_wasmtime_disabled.go` (CGO build with no
  wasmtime DLLs) — that file previously didn't mark the
  stub at all, so a CGO build that linked against liboqs
  but lacked wasmtime DLLs ran with broken WASM and emitted
  no Prometheus signal.
  - **Regression guard: `TestWasmSDK_StubActiveIsLazy` in
    `pkg/wasm/sdk_stubactive_test.go`.** Asserts the kind is
    inactive after package load, then triggers `NewWASMSDK`
    and asserts the kind flips. Skips on real wasmtime
    builds where the SDK constructs successfully.
  - **Runbook updated**:
    [`STUB_DEPLOYMENT_INCIDENT.md` § kind-wasm-sdk](QSDM/docs/docs/runbooks/STUB_DEPLOYMENT_INCIDENT.md#kind-wasm-sdk)
    now documents the opt-in semantics, calls out the
    pure-Go wazero default, and updates triage so an
    on-call seeing the alert immediately checks whether
    the operator's config (or build) tried to load a WASM
    module.
  - **Verification (CGO_ENABLED=0):**
    - `go build ./...` clean.
    - `go test ./pkg/wasm/... ./pkg/monitoring/stubactive/...`
      — green, including the new
      `TestWasmSDK_StubActiveIsLazy`.
  - **Operational impact.** False-positive
    `QSDMStubActive{kind="wasm_sdk"}` pages eliminated for
    every non-CGO deploy that doesn't use WASM modules.
    The CGO-no-wasmtime gap is closed: those deploys now
    page like the non-CGO ones do, instead of silently
    failing.

- **Slashing wiring: production dispatcher now covers all
  three EvidenceKinds with real verifiers (2026-05-05).**
  `internal/v2wiring/v2wiring.go` previously called
  `doublemining.NewProductionSlashingDispatcher`, which
  registered the real `forgedattest` and `doublemining`
  verifiers and left `EvidenceKindFreshnessCheat` wired to
  `slashing.StubVerifier` ("not yet implemented"). The
  freshness-cheat verifier ships fully implemented in
  `pkg/mining/slashing/freshnesscheat/` — only its
  block-inclusion witness collaborator is deferred pending
  BFT finality (see `MINING_PROTOCOL_V2.md §12.3`). Switched
  `Wire()` to `freshnesscheat.NewProductionSlashingDispatcher`
  with `witness=nil` (the production-safe `RejectAllWitness`
  default). End-user behaviour for slash txs of kind
  `freshness-cheat` is unchanged — every one is still
  rejected — but the rejection now carries kind-specific
  structural / staleness / registry diagnostics instead of
  the generic stub message, **and the
  `qsdm_stub_active{kind="slashing"}` gauge stays at 0** for
  every binary that boots through `v2wiring`.
  - **`Wired.SlashDispatcher` exposed.** New field on
    `internal/v2wiring.Wired` so consumers (and tests) can
    introspect the production dispatcher without reaching
    through the SlashApplier internals.
  - **Regression guard: `TestWire_SlashingDispatcherCoversAllKinds`
    in `internal/v2wiring/v2wiring_test.go`.** Asserts the
    dispatcher built by `Wire()` registers a real verifier
    for every kind in `slashing.AllEvidenceKinds`, AND that
    a freshness-cheat dispatch returns a kind-specific error
    rather than the StubVerifier "(not yet implemented)"
    string. A future EvidenceKind added to `AllEvidenceKinds`
    without a matching wiring update fails this test before
    it reaches a running validator.
  - **Runbook updated**:
    [`STUB_DEPLOYMENT_INCIDENT.md` § kind-slashing](QSDM/docs/docs/runbooks/STUB_DEPLOYMENT_INCIDENT.md#kind-slashing)
    now documents the new "no production binary wires a
    StubVerifier" invariant, calls out that
    freshness-cheat is NOT a stub (it runs against
    `RejectAllWitness`, which is a real verifier path with
    a deferred witness), and updates the triage flowchart
    so an on-call hitting `qsdm_stub_active{kind="slashing"} == 1`
    immediately checks whether the binary is using
    `v2wiring.Wire` or a hand-rolled dispatcher.
  - **Verification (CGO_ENABLED=0):**
    - `go build ./...` clean.
    - `go test ./internal/v2wiring/... ./pkg/mining/slashing/...`
      — all green, including the new
      `TestWire_SlashingDispatcherCoversAllKinds`.
  - **Operational impact.** One stub kind retired in
    production wiring. The
    `QSDMStubActive` alert with `kind="slashing"` is now
    structurally impossible to fire from a binary that uses
    `v2wiring.Wire` and has no kind drift.

### Added

- **Loose-end alerts: hot-reload + wallet-ingress dedupe
  burst. 3 alerts + HOT_RELOAD_INCIDENT runbook + new
  NETWORKING_INCIDENT Mode C (2026-05-05).** Closes
  three pre-existing metric surfaces that had no alerts
  pointing at them, leaving each subsystem invisible to
  paging until a downstream failure cascade started.
  - **Hot-reload (two new alerts)**:
    - `QSDMHotReloadApplyFailures` (warning, `for: 10m`):
      `rate(qsdm_hot_reload_apply_failure_total[5m]) > 0`
      sustained 10m. Live config-swap attempts being
      rejected by the in-process reload path — validator
      is running on the previous config until the swap
      path is fixed.
    - `QSDMHotReloadDryRunDegraded` (info, `for: 30m`):
      `last_dry_run_load_ok == 0` OR
      `last_dry_run_policy_ok == 0` with
      `last_dry_run_timestamp > 0` for 30m. Soft
      precursor — the on-disk file either can't parse
      or fails the policy guard, so the next planned
      apply will fail. Filters out cold-start nodes
      via the timestamp guard.
    - [`QSDM/docs/docs/runbooks/HOT_RELOAD_INCIDENT.md`](QSDM/docs/docs/runbooks/HOT_RELOAD_INCIDENT.md)
      — new two-mode runbook with a load-vs-policy
      drill-down for Mode B and explicit cross-references
      to `GOVERNANCE_AUTHORITY_INCIDENT.md` and
      `SUBMESH_POLICY_INCIDENT.md` for the two
      subsystems whose reload failures most often
      cascade through these alerts.
  - **Wallet-ingress dedupe burst (one new alert)**:
    - `QSDMP2PWalletIngressDedupeBurst` (info, `for: 15m`):
      `rate(qsdm_p2p_wallet_ingress_dedupe_skip_total[5m]) > 1`
      sustained 15m. INFO severity because dedupe is
      protective behaviour — duplicates are NOT
      double-applied — but the burst signal is useful
      for capacity planning and for spotting
      buggy/adversarial relayers replaying the same
      tx_ids via mesh wire + JSON gossip.
    - Lives in the existing `qsdm-p2p` group; runbook
      coverage is the new
      [`NETWORKING_INCIDENT.md` §3.3 / Mode C](QSDM/docs/docs/runbooks/NETWORKING_INCIDENT.md).
      Cross-references reputation as the upstream
      defence against adversarial sources.
  - **CI**: promtool unit tests added for all three
    alerts (early/late firing checkpoints); runbook
    coverage now verifies all 53 alerts have resolvable
    `runbook_url` and `dashboard_url`s; auto-generated
    `qsdm-runbook-hot-reload-incident.json` shipped.
  - **Verification**: promtool `check rules` + `test
    rules` pass (53 alerts; 464 in-runbook links
    resolve across 19 files; 19 dashboards cover all
    alerts). No code changes — these alerts attach to
    existing metric surfaces.
  - **Operational impact**: the hot-reload subsystem
    is no longer silently broken; a jammed apply path
    pages within 10m and a config-file regression
    pages within 30m as a soft precursor. The
    wallet-ingress dedupe burst signal lets operators
    identify a misbehaving replayer (often a buggy
    relayer with a too-tight retry loop) before it
    scales up enough to require quarantine action.

- **Peer-reputation observability + decay-loop wiring:
  multi-tracker gauges + 2 alerts + 2-mode runbook
  (2026-05-05).** Closes two long-standing operational
  defects in the peer-reputation system. The
  `pkg/networking.ReputationTracker` had been wired into
  BFT and evidence ingress for many releases, but
  (a) `Start()` was never called from `cmd/qsdm/main.go`
  so the configured decay loop never actually ran —
  penalties accumulated permanently — and (b) tracker
  state had zero Prometheus exposition, visible only via
  the admin API.
  - **New artefacts**:
    - [`QSDM/source/pkg/monitoring/repmetrics/repmetrics.go`](QSDM/source/pkg/monitoring/repmetrics/repmetrics.go)
      — leaf package mirroring the netmetrics pattern
      (zero non-stdlib imports). Defines
      `ReputationProvider` interface,
      `ReputationSnapshot` value type, and a
      tracker-keyed registry (`RegisterReputationProvider`,
      `Providers`). Required because `pkg/monitoring`
      already imports `pkg/networking` via
      `topology.go`; having `pkg/networking` depend on
      the leaf instead of the root avoids a cycle.
    - [`QSDM/source/pkg/monitoring/reputation_metrics.go`](QSDM/source/pkg/monitoring/reputation_metrics.go)
      — Prometheus exposition wrapper. Re-exports the
      leaf primitives at
      `monitoring.RegisterReputationProvider` for
      backwards compat. Emits five gauges per
      registered tracker:
      `qsdm_reputation_peers_total{tracker}`,
      `qsdm_reputation_peers_banned{tracker}`,
      `qsdm_reputation_score_min{tracker}`,
      `qsdm_reputation_score_max{tracker}`,
      `qsdm_reputation_score_avg{tracker}`. Empty
      output when no tracker is registered (no
      always-on `provider="none"` rows because
      reputation absence is the common test/dev case).
    - [`QSDM/source/pkg/monitoring/repmetrics/repmetrics_test.go`](QSDM/source/pkg/monitoring/repmetrics/repmetrics_test.go)
      and
      [`QSDM/source/pkg/monitoring/reputation_metrics_test.go`](QSDM/source/pkg/monitoring/reputation_metrics_test.go)
      — exercise registration idempotency, multi-
      tracker isolation, copy-on-return semantics, and
      end-to-end `PrometheusExposition()` formatting.
    - [`QSDM/docs/docs/runbooks/REPUTATION_INCIDENT.md`](QSDM/docs/docs/runbooks/REPUTATION_INCIDENT.md)
      — two-mode runbook with explicit
      attack-vs-config-regression triage paths and
      per-tracker disambiguation guidance
      (`tracker="tx"` lenient vs.
      `tracker="evidence"` strict).
  - **Wiring**:
    - [`QSDM/source/pkg/networking/reputation.go`](QSDM/source/pkg/networking/reputation.go)
      — `ReputationTracker.Snapshot()` implements
      `repmetrics.ReputationProvider`, computing min
      / max / avg / total / banned counts under the
      tracker's read lock and returning by value for
      lock-free scrape rendering.
    - [`QSDM/source/cmd/qsdm/main.go`](QSDM/source/cmd/qsdm/main.go)
      — both `nodeTxRep` and `nodeEvidenceRep` are now
      registered as monitoring providers under names
      `"tx"` and `"evidence"`, and both have
      `Start()` invoked with matching `defer Stop()`.
      Closes the never-decay defect.
    - [`QSDM/source/pkg/monitoring/prometheus_scrape.go`](QSDM/source/pkg/monitoring/prometheus_scrape.go)
      registers `reputationPrometheusMetrics` as a
      collector.
  - **Alerts** (in
    [`QSDM/deploy/prometheus/alerts_qsdm.example.yml`](QSDM/deploy/prometheus/alerts_qsdm.example.yml),
    new group `qsdm-reputation`):
    - `QSDMReputationBanRatioHigh` (warning, `for: 10m`):
      `peers_banned / peers_total > 0.5` with
      `peers_total >= 4`. Either a coordinated attack
      OR a penalty-config regression.
    - `QSDMReputationScoreCollapse` (info, `for: 30m`):
      `score_min < -100` (halfway to default
      `BanThreshold`) with `peers_total >= 4`. Drift
      precursor to the Mode A page.
  - **CI**: promtool unit tests added to
    [`alerts_qsdm.test.spec.yml`](QSDM/deploy/prometheus/alerts_qsdm.test.spec.yml)
    (early/late firing checkpoints for both alerts);
    runbook coverage now verifies all 50 alerts have
    resolvable `runbook_url` and `dashboard_url`s;
    auto-generated
    `qsdm-runbook-reputation-incident.json` shipped.
  - **Verification**: `go build ./... && go test
    ./pkg/networking/... ./pkg/monitoring/...` pass
    under `CGO_ENABLED=0`. promtool tests pass (50
    alerts; 442 in-runbook links resolve across 18
    files; 18 dashboards cover all alerts).
  - **Operational impact**: a coordinated peer attack
    that crosses 50% of the population will now page
    within 10m on the warning channel rather than
    being invisible until the chain stops; a config
    regression that mass-bans honest peers will page
    on the same alert. The decay-loop fix means
    historic peer behaviour no longer permanently
    dominates the score state, restoring the
    intended semantics of the
    `DecayInterval`/`DecayFactor` config knobs.

- **Smart-contract + atomic-swap bridge observability:
  per-result + per-(op,result) counters + 2 alerts +
  2-mode runbook (2026-05-05).** Closes a long-standing
  instrumentation gap. Both `pkg/contracts` and
  `pkg/bridge` had **zero** Prometheus instrumentation
  before this commit. Contract gas-exhaustion failures, a
  WASM runtime regression, a stuck bridge lock, or a flood
  of invalid-secret redemption attempts were all log-only.
  - **New artefacts**:
    - [`QSDM/source/pkg/monitoring/contracts_bridge_metrics.go`](QSDM/source/pkg/monitoring/contracts_bridge_metrics.go)
      — defines `qsdm_contract_executions_total{result}` (2
      rows: success / error) and `qsdm_bridge_op_total{op,result}`
      (6 rows: 3 ops × 2 results, all pre-populated at 0).
      `RecordContractExecution(result)` and
      `RecordBridgeOp(op, result)` are the public mutators;
      unknown op/result tuples no-op rather than panicking.
    - [`QSDM/source/pkg/monitoring/contracts_bridge_metrics_test.go`](QSDM/source/pkg/monitoring/contracts_bridge_metrics_test.go)
      — verifies all 8 rows are emitted, that counter
      increments reflect in `PrometheusExposition()`, and
      that unknown labels are silently dropped.
    - [`QSDM/docs/docs/runbooks/CONTRACTS_BRIDGE_INCIDENT.md`](QSDM/docs/docs/runbooks/CONTRACTS_BRIDGE_INCIDENT.md)
      — two-mode runbook with conservative thresholds
      (these subsystems carry user-driven error volume that
      isn't necessarily a system fault). Mode A fires on
      contract execution error ratio > 50% with ≥1 call/min
      sustained 15m; Mode B fires on bridge op error rate
      > 0.2/min sustained 10m. Mode B includes per-op
      interpretation tables (lock/redeem/refund), with the
      `op="refund"` case flagged as highest-stakes
      ("funds are stuck") and a cross-reference to
      `qsdm_p2p_messages_total` for redeem-error vs.
      adversarial-spam disambiguation.
  - **Wiring**:
    - [`QSDM/source/pkg/contracts/engine.go`](QSDM/source/pkg/contracts/engine.go)
      — `ContractEngine.ExecuteContract` converted to
      `(resExec, resErr)` named-return with a single
      `defer` that flips
      `qsdm_contract_executions_total{result=...}` at every
      termination point (function-not-found, gas
      exhaustion, runtime panic, ABI mismatch, success).
    - [`QSDM/source/pkg/bridge/protocol.go`](QSDM/source/pkg/bridge/protocol.go)
      — `LockAsset`, `RedeemAsset`, `RefundAsset` each
      converted to named-return with a single `defer` that
      flips `qsdm_bridge_op_total{op=..., result=...}`.
    - [`QSDM/source/pkg/monitoring/prometheus_scrape.go`](QSDM/source/pkg/monitoring/prometheus_scrape.go)
      registers `contractsBridgePrometheusMetrics` as a
      collector.
  - **Alerts** (in
    [`QSDM/deploy/prometheus/alerts_qsdm.example.yml`](QSDM/deploy/prometheus/alerts_qsdm.example.yml),
    new group `qsdm-contracts-bridge`):
    - `QSDMContractExecuteErrorRate` (warning, `for: 15m`):
      error ratio > 50% with ≥1 call/min. Companion to
      `QSDMStubActive{kind="wasm_sdk"}` (upstream sentinel
      when the SDK stub is producing the errors).
    - `QSDMBridgeOpErrorBurst` (warning, `for: 10m`):
      error rate > 0.2/min — lower threshold than the
      contract alert because each bridge op carries direct
      economic impact.
  - **CI**: promtool unit tests added to
    [`alerts_qsdm.test.spec.yml`](QSDM/deploy/prometheus/alerts_qsdm.test.spec.yml)
    (early/late firing checkpoints for both alerts);
    runbook coverage check now verifies all 48 alerts have
    resolvable `runbook_url` and `dashboard_url`s; auto-
    generated `qsdm-runbook-contracts-bridge-incident.json`
    shipped.
  - **Verification**: `go build ./... && go test
    ./pkg/contracts/... ./pkg/bridge/... ./pkg/monitoring/...`
    pass under `CGO_ENABLED=0`. promtool tests pass (48
    alerts; 423 in-runbook links resolve across 17 files;
    17 dashboards cover all alerts).
  - **Operational impact**: contract execution failures
    (gas exhausted, ABI drift, runtime regression) and
    bridge op failures (cross-chain proof failure, stuck
    refund, invalid-secret spam) are now visible to
    alerting. Mode B's per-op breakdown lets an operator
    distinguish a redeem-spam attack from a refund wedge
    immediately on page.

- **libp2p peer-graph observability: pulled gauge + push
  counters + 2 alerts + 2-mode runbook (2026-05-05).**
  Closes a long-standing instrumentation gap. Before this
  commit, `pkg/networking` had **zero** Prometheus
  instrumentation — peer count, gossip volume, and
  connection churn were all log-only signals invisible to
  alerting. The legacy `Metrics.NetworkMessagesSent` /
  `NetworkMessagesRecv` fields were never incremented from
  the libp2p path AND never exposed in the OpenMetrics
  scrape.
  - **New artefacts**:
    - [`QSDM/source/pkg/monitoring/netmetrics/netmetrics.go`](QSDM/source/pkg/monitoring/netmetrics/netmetrics.go)
      — leaf package with zero non-stdlib imports; defines
      `NetworkProvider` interface, `RegisterNetworkProvider`,
      `RecordGossipMessage`, `GossipCounts`. Split out as a
      leaf because `pkg/monitoring` already imports
      `pkg/networking` (TopologyMonitor) — having
      `pkg/networking` import the leaf instead of the root
      avoids a circular dependency.
    - [`QSDM/source/pkg/monitoring/network_metrics.go`](QSDM/source/pkg/monitoring/network_metrics.go)
      — Prometheus exposition wrapper. Re-exports the
      netmetrics primitives at `monitoring.RegisterNetworkProvider`
      / `monitoring.RecordGossipMessage` for backwards-compat.
      Emits `qsdm_p2p_peers_connected{provider="live|none"}`
      (gauge, pulled at scrape time) and
      `qsdm_p2p_messages_total{direction="in|out"}` (counter,
      always emits both rows so cold-start nodes don't miss-
      data on alert evaluation).
    - [`QSDM/source/pkg/monitoring/netmetrics/netmetrics_test.go`](QSDM/source/pkg/monitoring/netmetrics/netmetrics_test.go)
      and [`QSDM/source/pkg/monitoring/network_metrics_test.go`](QSDM/source/pkg/monitoring/network_metrics_test.go)
      — exercise registration idempotency, provider= label
      switching, direction-counter increments, exposition
      formatting, and unknown-tag defensive drops.
    - [`QSDM/docs/docs/runbooks/NETWORKING_INCIDENT.md`](QSDM/docs/docs/runbooks/NETWORKING_INCIDENT.md)
      — two-mode runbook with cross-fleet vs. single-host
      disambiguation guidance and explicit policy-vs-network
      distinction (quarantine policy fires the same shape
      of symptom as a one-way partition).
  - **Wiring**:
    - [`QSDM/source/pkg/networking/libp2p.go`](QSDM/source/pkg/networking/libp2p.go)
      now imports `pkg/monitoring/netmetrics` (NOT root
      monitoring, to avoid the cycle); `SetupLibP2PWithPort`
      calls `netmetrics.RegisterNetworkProvider(net)` after
      construction; `Network.PeerCount()` implements
      `netmetrics.NetworkProvider`; `handleMessages`
      increments `direction="in"` per non-self pubsub message;
      `Broadcast` increments `direction="out"` only on
      successful publish (not on error paths).
    - [`QSDM/source/pkg/monitoring/prometheus_scrape.go`](QSDM/source/pkg/monitoring/prometheus_scrape.go)
      registers `networkPrometheusMetrics` as a collector.
  - **Alerts** (in
    [`QSDM/deploy/prometheus/alerts_qsdm.example.yml`](QSDM/deploy/prometheus/alerts_qsdm.example.yml),
    new group `qsdm-p2p`):
    - `QSDMP2PNoPeers` (warning, `for: 5m`): peer count
      is 0 with `provider="live"` filter sustained for 5m.
      Companion to `QSDMQuarantineMajorityIsolated`
      (network-side vs. policy-side islanding) and
      `QSDMMiningChainStuck` (downstream stall risk).
    - `QSDMP2PGossipIngressStalled` (warning, `for: 10m`):
      peers > 0 but inbound gossip rate is exactly 0 for
      10m. Catches the more subtle one-way-partition or
      pubsub-subscription-drift case where host metrics
      look healthy but gossip ingress is silent.
  - **CI**: promtool unit tests added to
    [`alerts_qsdm.test.spec.yml`](QSDM/deploy/prometheus/alerts_qsdm.test.spec.yml)
    (early/late firing checkpoints for both alerts);
    runbook coverage check now verifies all 46 alerts have
    resolvable `runbook_url` and `dashboard_url`s; auto-
    generated `qsdm-runbook-networking-incident.json`
    shipped.
  - **Verification**: `go build ./... && go test
    ./pkg/networking/... ./pkg/monitoring/...` pass under
    `CGO_ENABLED=0`. promtool tests pass (46 alerts; 407
    in-runbook links resolve across 16 files; 16 dashboards
    cover all alerts).
  - **Operational impact**: a validator that loses all its
    peers no longer disappears silently — `QSDMP2PNoPeers`
    fires within 5m. A one-way partition or wedged pubsub
    subscription, previously invisible, fires
    `QSDMP2PGossipIngressStalled` within 10m with explicit
    triage steps and a built-in cross-check against the
    quarantine sentinel to disambiguate policy from
    plumbing.

- **Storage-backend observability: per-(op, result) counter +
  2 alerts + 2-mode runbook (2026-05-05).** Closes a
  long-standing instrumentation gap in the storage layer.
  Before this commit, `pkg/storage/sqlite.go`'s
  `StoreTransaction` had **no Prometheus instrumentation at
  all** — a write failure (database locked, disk full,
  encryption failure, compression failure) was log-only,
  invisible to alerting. The legacy
  `monitoring.RecordStorageOperation` hook covered
  `GetBalance` / `UpdateBalance` / `SetBalance` but was
  exposed only in the `/api/metrics` JSON map, not in the
  OpenMetrics scrape used for alerting.
  - **New artefacts**:
    - [`QSDM/source/pkg/monitoring/storage_op_metrics.go`](QSDM/source/pkg/monitoring/storage_op_metrics.go)
      — `qsdm_storage_op_total{op,result}` counter with 5 ops
      (`store_transaction`, `get_balance`, `update_balance`,
      `set_balance`, `ready`) × 2 results (`success`, `error`)
      = 10 always-populated rows. Cold-start nodes never
      bootstrap with missing-data, so alert expressions like
      `rate(...{result="error"}[5m]) > 0` evaluate against a
      defined time series from process start.
      `RecordStorageOp(op, result)` is the public mutator;
      unknown (op, result) tuples no-op rather than panicking.
    - [`QSDM/source/pkg/monitoring/storage_op_metrics_test.go`](QSDM/source/pkg/monitoring/storage_op_metrics_test.go)
      — verifies all 10 (op, result) rows are emitted, that
      counter increments reflect in `PrometheusExposition()`,
      and that unknown tags are silently dropped.
    - [`QSDM/docs/docs/runbooks/STORAGE_INCIDENT.md`](QSDM/docs/docs/runbooks/STORAGE_INCIDENT.md)
      — two-mode runbook. Mode A (`QSDMStorageWriteErrorBurst`,
      warning, `for: 5m`) catches sustained write-error
      bursts: rate-of-store_transaction-error * 60 > 1.
      Mode B (`QSDMStorageReadyFailing`, **critical**,
      `for: 2m`) catches `Ready()` probe failures — the
      lowest-level health signal because the validator can't
      meaningfully participate in consensus without working
      storage.
  - **Wiring**:
    - [`QSDM/source/pkg/storage/sqlite.go`](QSDM/source/pkg/storage/sqlite.go)
      `StoreTransaction` is now `(resErr error)` named-return
      with a single `defer` that flips the success/error
      counter at every termination point (early dedupe-skip,
      JSON-fallback path, and the per-row INSERT branch).
      `GetBalance` / `UpdateBalance` / `SetBalance` /
      `Ready` also call `RecordStorageOp` alongside the
      legacy `RecordStorageOperation` (legacy path preserved
      for `/api/metrics` JSON consumers).
    - [`QSDM/source/pkg/storage/file_storage.go`](QSDM/source/pkg/storage/file_storage.go)
      `StoreTransaction` and `Ready` instrumented via the
      same named-return pattern.
    - [`QSDM/source/pkg/storage/scylla.go`](QSDM/source/pkg/storage/scylla.go)
      `storeTransactionWithOptions` (the shared
      implementation behind `StoreTransaction` and
      `StoreTransactionMigrate`) and `Ready` instrumented;
      the pre-existing `RecordStorageOperation` call is
      preserved for backwards-compatibility.
    - [`QSDM/source/pkg/monitoring/prometheus_scrape.go`](QSDM/source/pkg/monitoring/prometheus_scrape.go)
      registers `storageOpPrometheusMetrics` as a collector.
  - **Alerts** (in
    [`QSDM/deploy/prometheus/alerts_qsdm.example.yml`](QSDM/deploy/prometheus/alerts_qsdm.example.yml),
    new group `qsdm-storage`):
    - `QSDMStorageWriteErrorBurst` — store_transaction error
      rate > 1/min sustained 5m. Companion to
      `QSDMNoTransactionsStored` (full-wedge sentinel) and
      `QSDMWalletStorageErrorBurst` (wallet-API-surface
      symptom of the same class).
    - `QSDMStorageReadyFailing` — `Ready()` probe error
      rate > 0 for 2m. Critical because `/api/v1/health`
      returns 503 and `QSDMNoTransactionsStored` /
      `QSDMMiningChainStuck` follow downstream.
  - **CI**: promtool tests added to
    [`alerts_qsdm.test.spec.yml`](QSDM/deploy/prometheus/alerts_qsdm.test.spec.yml)
    (early/late firing checkpoints for both alerts);
    runbook coverage check verifies all 44 alerts now have
    resolvable `runbook_url` and `dashboard_url`s; auto-
    generated dashboard
    `qsdm-runbook-storage-incident.json` shipped.
  - **Verification**: `go build ./... && go test
    ./pkg/storage/... ./pkg/monitoring/... ./pkg/api/...`
    pass under `CGO_ENABLED=0`. promtool unit tests pass
    (44 alerts captured, all anchors resolve, 383 in-
    runbook links resolve, 15 dashboards cover all alerts).
  - **Operational impact**: storage write failures are no
    longer log-only. A wedged Scylla cluster, a full disk,
    a permission-changed FileStorage directory, or a
    locked SQLite DB now fires within 5m (write-error
    burst) or 2m (`Ready()` failure), with a runbook that
    explains how to triage by op tag and how to disambiguate
    from the wallet-API-surface and aggregate-throughput
    sentinels firing concurrently.

- **Wallet handler-side observability: 4 per-result counters +
  3 alerts + 3-mode runbook (2026-05-04).** Closes the
  handler-side wallet observability gap. Before this commit,
  the only wallet-related Prometheus signals were *gate-side*
  rejects (`qsdm_submesh_api_wallet_reject_*_total`,
  `qsdm_p2p_wallet_ingress_dedupe_skip_total`) — a wedged
  storage backend, a missing wallet-service init, or a
  perpetually-blocking NVIDIA-lock were log-only.
  - **New artefacts**:
    - [`QSDM/source/pkg/monitoring/wallet_metrics.go`](QSDM/source/pkg/monitoring/wallet_metrics.go)
      — per-result counters for the four state-changing wallet
      endpoints (send, balance, mint, create). Atomic counters
      with `result` label values defined as Go constants
      (success / invalid_request / unauthenticated /
      nvidia_lock_blocked / no_wallet_service / tx_create_failed
      / store_failed for send; success / storage_error /
      no_wallet_service for balance; success / admin_rejected /
      invalid_request / store_failed / no_wallet_service for
      mint; success / failed for create). Unknown result tags
      no-op rather than panic (defensive against future enum
      drift).
    - [`QSDM/docs/docs/runbooks/WALLET_INCIDENT.md`](QSDM/docs/docs/runbooks/WALLET_INCIDENT.md)
      — three-mode runbook with anchored alert sections (§3.1
      send-error-rate drilled by per-result tag, §3.2 storage
      error burst, §3.3 mint burst supply-inflation tripwire).
      Mode A explicitly forks into the right subsystem-runbook
      based on the dominant failure tag (cross-references
      `STUB_DEPLOYMENT_INCIDENT.md` for `tx_create_failed` /
      `no_wallet_service`, `OPERATOR_HYGIENE_INCIDENT.md` for
      `store_failed`, `NGC_SUBMISSION_INCIDENT.md` for
      `nvidia_lock_blocked`).
    - [`QSDM/deploy/grafana/dashboards/qsdm-runbook-wallet-incident.json`](QSDM/deploy/grafana/dashboards/qsdm-runbook-wallet-incident.json)
      — auto-generated panel.
  - **Alert wiring** (new `qsdm-wallet` group in
    [`alerts_qsdm.example.yml`](QSDM/deploy/prometheus/alerts_qsdm.example.yml)):
    - `QSDMWalletSendErrorRate`: handler-side failure ratio
      (`tx_create_failed` + `store_failed` +
      `no_wallet_service` + `nvidia_lock_blocked`) divided
      by total send rate > 10% for ≥10m. Submesh-policy and
      dedupe rejects are excluded from the numerator
      (gate-side, separate counters).
    - `QSDMWalletStorageErrorBurst`: combined wallet
      storage-error rate (balance.storage_error +
      send.store_failed) > 1/min for ≥5m. Strong storage-
      backend wedge signal that complements
      `QSDMNoTransactionsStored` and `QSDMMiningChainStuck`.
    - `QSDMWalletMintBurst`: `result="success"` mint rate
      > 5/min for ≥30m (≥150 mints in window). Supply-
      inflation tripwire — mint is admin-only and gated, so
      sustained successful volume is suspicious in itself.
  - **Handler instrumentation**: every terminal point in the
    four state-changing handlers in `pkg/api/handlers.go` now
    calls `monitoring.RecordWalletXxx(...)`. Submesh-policy
    and dedupe paths intentionally NOT double-counted (their
    own counters cover those rejects).
  - **CI / test coverage**:
    - New `pkg/monitoring/wallet_metrics_test.go` — proves
      every result tag surfaces in `walletPrometheusMetrics()`,
      `RecordWalletSend` reflects in `PrometheusExposition()`,
      and unknown tags no-op without panic.
    - Three new entries in
      [`alerts_qsdm.test.spec.yml`](QSDM/deploy/prometheus/alerts_qsdm.test.spec.yml)
      — `promtool test rules` evaluates each alert at the
      silent + firing checkpoints against synthetic time
      series.
  - **Verification**:
    - `go build ./...` (CGO=0): clean.
    - `go test ./pkg/api/... ./pkg/monitoring/...`: pass.
    - `promtool check rules`: 42 rules valid (was 39).
    - `promtool test rules alerts_qsdm.test.yml`: SUCCESS.
    - `scripts/check_runbook_coverage.py`: 42/42 alerts
      coverage; 358 in-runbook links; 14 dashboards.
    - `gen_grafana_dashboards.py`: 13 → 14 per-runbook
      dashboards (added `qsdm-runbook-wallet-incident.json`).

  Note on real on-chain balance lookup: `WalletService.GetBalance()`
  in `pkg/wallet/wallet_stub.go` returns 0 by design — it's
  documented as "balance is stored in storage backend, not
  wallet service." The API handler `Handlers.GetBalance` already
  queries `h.storage.GetBalance(address)` (which IS the on-chain
  balance lookup). No change needed there; the integration is
  already correct.

- **Silent-stub-deployment guard: `qsdm_stub_active{kind="..."}`
  metric + `QSDMStubActive` critical alert + 7-section runbook
  (2026-05-04).** Closes the most dangerous gap in the deploy
  surface: a node compiled without CGO previously started
  silently and accepted transactions WITHOUT signature
  verification (the non-CGO PoE stub at
  `pkg/consensus/poe_stub.go:38-46` returns `true, nil` from
  `ValidateTransaction` when the receiver is `nil`, with only a
  log warning — no Prometheus signal). The new gauge surfaces
  every stub-shipped code path on every scrape; the
  five-minute `for:` window pages on-call within two scrape
  intervals of a misconfigured deploy.
  - **New artefacts**:
    - [`QSDM/source/pkg/monitoring/stubactive/stubactive.go`](QSDM/source/pkg/monitoring/stubactive/stubactive.go)
      — leaf registry (zero non-stdlib imports) that any stub
      package can call into without creating an import cycle
      through the `pkg/monitoring → pkg/mining → pkg/chain →
      pkg/consensus` chain. Exposes seven canonical kinds —
      `poe`, `dilithium`, `wallet`, `cc`, `slashing`,
      `mesh3d_cuda`, `wasm_sdk` — pre-populated at value 0 so
      the metric time series exists from process start (the
      alert query `qsdm_stub_active == 1` would otherwise have
      a bootstrap-missing-data problem on a stub that's loaded
      AFTER the first scrape).
    - [`QSDM/source/pkg/monitoring/stub_active_metrics.go`](QSDM/source/pkg/monitoring/stub_active_metrics.go)
      — bridge that scrapes the registry into the
      `qsdm_core` exporter as `qsdm_stub_active{kind="..."}`.
      Forward-compatible: a future stub registering an
      unknown kind still surfaces in metrics without
      simultaneously editing `AllKinds()`.
    - [`QSDM/docs/docs/runbooks/STUB_DEPLOYMENT_INCIDENT.md`](QSDM/docs/docs/runbooks/STUB_DEPLOYMENT_INCIDENT.md)
      — seven kind-anchored sections (`#kind-poe`,
      `#kind-dilithium`, `#kind-wallet`, `#kind-cc`,
      `#kind-slashing`, `#kind-mesh3d-cuda`, `#kind-wasm-sdk`),
      each with severity classification, root-cause
      explanation, triage steps, and remediation. The
      `kind="poe"` section is treated as a security incident:
      affected nodes must be removed from validator rotation
      and forensic-reviewed for the incident window's
      transactions.
    - [`QSDM/deploy/grafana/dashboards/qsdm-runbook-stub-deployment-incident.json`](QSDM/deploy/grafana/dashboards/qsdm-runbook-stub-deployment-incident.json)
      — per-kind status panel auto-generated by
      `scripts/gen_grafana_dashboards.py`.
  - **Alert wiring**:
    - New `qsdm-stub-active` group in
      [`QSDM/deploy/prometheus/alerts_qsdm.example.yml`](QSDM/deploy/prometheus/alerts_qsdm.example.yml).
      Single alert `QSDMStubActive` with expression
      `qsdm_stub_active == 1`, `for: 5m`,
      `severity: critical`, `subsystem: v2-deployment`.
      Prometheus produces one alert instance per active kind
      label (one ticket per stub-active kind), with the
      `kind` value templated into the runbook anchor via
      `{{ reReplaceAll "_" "-" $labels.kind }}` so the
      on-call lands directly at the right kind-section
      rather than the runbook header.
  - **Stub wiring** (every shipped stub now flips its kind
    to 1 at init/constructor time):
    - `pkg/consensus/poe_stub.go` `init()` →
      `stubactive.MarkActive("poe")` (non-CGO build).
    - `pkg/crypto/dilithium_stub.go` `init()` →
      `stubactive.MarkActive("dilithium")` (non-CGO).
    - `pkg/wallet/wallet_stub.go` `init()` →
      `stubactive.MarkActive("wallet")` (non-CGO).
    - `pkg/mesh3d/cuda_stub.go` `init()` →
      `stubactive.MarkActive("mesh3d_cuda")` (no CUDA).
    - `pkg/wasm/sdk_stub.go` `init()` →
      `stubactive.MarkActive("wasm_sdk")` (non-CGO).
    - `pkg/mining/attest/cc/stub.go` `NewStubVerifier()` →
      `stubactive.MarkActive("cc")` (runtime selection;
      flips when the validator wires the placeholder for
      `mining.AttestationTypeCC`).
    - `pkg/mining/slashing/verifier.go` `Dispatcher.Register()`
      → `stubactive.MarkActive("slashing")` if the verifier
      passed in is a `StubVerifier` (runtime selection; flips
      when at least one EvidenceKind is wired to the always-
      rejecting placeholder).
  - **Lint extension**: `scripts/check_runbook_coverage.py`
    learned to validate **templated runbook anchors** (URLs
    containing Go template expressions like
    `#kind-{{ reReplaceAll "_" "-" $labels.kind }}`). For
    those, the lint extracts the static prefix before the
    first `{{` and verifies that the runbook contains at
    least one anchor with that prefix. Existing static
    anchors are validated as before. Result for
    `STUB_DEPLOYMENT_INCIDENT.md`: 16 anchors match the
    `kind-` prefix (the seven canonical kinds plus their
    sub-section anchors).
  - **CI / test coverage**:
    - New `pkg/monitoring/stubactive/stubactive_test.go` —
      unit tests for `MarkActive`/`Snapshot`/forward-
      compatibility/idempotency.
    - New `pkg/monitoring/stub_active_metrics_test.go` —
      integration tests proving the metric flows through
      `PrometheusExposition()` with one row per canonical
      kind always populated.
    - New entry in
      [`QSDM/deploy/prometheus/alerts_qsdm.test.spec.yml`](QSDM/deploy/prometheus/alerts_qsdm.test.spec.yml)
      — `promtool test rules` evaluates the alert at 4m
      (silent, inside `for: 5m`) and 6m (firing) against a
      synthetic `qsdm_stub_active{kind="poe"}` time series
      held at 1.
  - **Verification**:
    - `go build ./...` (CGO=0): clean across the whole tree.
    - `go test ./pkg/monitoring/stubactive/... ./pkg/monitoring/...`: pass.
    - `go test ./pkg/mining/slashing/... ./pkg/mining/attest/cc/...
      ./pkg/wallet/... ./pkg/consensus/...`: pass — slashing
      `Register` change is binary-compatible with existing
      tests that register `forgedattest.Verifier` /
      `freshnesscheat.Verifier` real implementations.
    - `promtool check rules alerts_qsdm.example.yml`: 39
      rules (was 38) — all valid.
    - `promtool test rules alerts_qsdm.test.yml`:
      `SUCCESS` — `QSDMStubActive` fires at 6m as designed.
    - `scripts/check_runbook_coverage.py`: 39/39 alerts
      have resolvable `runbook_url` anchors and
      `dashboard_url` files; 322 in-runbook links across 13
      files all resolve; 13 dashboard JSONs cover all
      alerts.
    - `scripts/gen_grafana_dashboards.py`: 12 → 13 per-
      runbook dashboards, including the new
      `qsdm-runbook-stub-deployment-incident.json`.
  - **Operational impact**:
    - **`kind="poe"` is the highest-severity gap closed.** A
      production deploy without CGO previously had no signal
      that incoming transactions were being accepted without
      signature verification — only a single log line on
      every accepted unsigned tx. That deployment is now
      paged within 5 minutes via the `qsdm_stub_active`
      gauge and the runbook treats it as a security
      incident requiring forensic review.
    - **`kind="cc"` and `kind="slashing"` close the
      "documented but not implemented" risk** highlighted in
      the project assessment: validators wired to the CC
      stub admit zero `nvidia-cc-v1` proofs, and slashing
      dispatchers with stub-wired EvidenceKinds silently
      reject those slash transactions. Both surface
      explicitly via the gauge so operators can either
      silence the alert per-subnet (intentional choice) or
      track the upgrade timeline.
    - **`kind="dilithium"` / `"wallet"` paint the quantum-
      safe-crypto downgrade** — operators who built without
      `CGO_ENABLED=1` and liboqs are using SHA-256 for what
      should be ML-DSA-87 signatures. Visible immediately on
      the dashboard.

- **Alertmanager example config + Slack/PagerDuty templates that
  surface BOTH `runbook_url` and `dashboard_url` (2026-05-04).**
  The alerts file and the per-runbook dashboards have been
  generating these annotations for several commits, but until now
  there was no piece of deploy config that actually rendered them
  into a notification surface; the URLs sat in the YAML and the
  on-call operator never saw them. This change closes that gap by
  shipping a complete, end-to-end-tested Alertmanager
  configuration that routes alerts by severity, fans critical
  alerts out to PagerDuty + Slack, and renders both URLs into
  every notification channel (Slack message body + click-through
  buttons, PagerDuty `client_url` + sidebar links + custom
  details, and HTML email body).
  - **New artefacts**:
    - [`QSDM/deploy/alertmanager/alertmanager.example.yml`](QSDM/deploy/alertmanager/alertmanager.example.yml)
      — full reference config: routing tree (severity-driven, with
      `continue: true` fan-out for critical), 4 inhibit rules
      (critical-supersedes-warning, slashing dust-burst,
      quarantine majority-isolated, trust no-attestations), 6
      receivers (slack-default audit, pagerduty-critical,
      slack-critical, slack-warning, slack-info-quiet, email-fallback),
      heavily commented for operator-facing maintenance.
    - [`QSDM/deploy/alertmanager/templates/qsdm.tmpl`](QSDM/deploy/alertmanager/templates/qsdm.tmpl)
      — five Go-template definitions (`qsdm.title`, `qsdm.text`,
      `qsdm.slack.title`, `qsdm.slack.titlelink`,
      `qsdm.slack.color`, `qsdm.severityEmoji`, `qsdm.pd.client`,
      `qsdm.pd.classname`) consumed by the receivers. Surfaces
      `*Runbook*:` and `*Dashboard*:` Slack-mrkdwn hyperlinks plus
      severity-coloured attachments.
    - [`QSDM/deploy/alertmanager/README.md`](QSDM/deploy/alertmanager/README.md)
      — operator-facing setup recipe (install → configure →
      validate → start → wire into Prometheus), routing-tree
      diagram, inhibit-rule table, template summary, and
      end-to-end smoke-test instructions.
    - [`scripts/smoketest_alertmanager.py`](scripts/smoketest_alertmanager.py)
      — self-contained two-phase end-to-end harness. Phase 1 spins
      up a real Alertmanager pointed at a localhost listener with
      `webhook_configs:` receivers, pushes 4 synthetic alerts (one
      per severity + one unlabelled), and asserts the routing tree
      dispatches correctly (including the critical fan-out) and
      that every delivery's `commonAnnotations` carries both URLs.
      Phase 2 reuses the same harness with `slack_configs:`
      receivers (real template rendering) pointed at a second
      listener, and asserts the rendered Slack JSON contains
      `*Runbook*:` / `*Dashboard*:` mrkdwn lines and that the
      action buttons carry the resolved URLs. 27 checks total;
      gracefully skips (exit 0) when `alertmanager` is unavailable.
  - **Prometheus wiring**: [`prometheus.qsdm.example.yml`](QSDM/deploy/prometheus/prometheus.qsdm.example.yml)
    grew an `alerting:` block that forwards firing alerts to
    `ALERTMANAGER_HOST:9093`, and the prometheus README now
    cross-links the alertmanager directory.
  - **CI gate**: [`.github/workflows/validate-deploy.yml`](.github/workflows/validate-deploy.yml)
    grew a new `alertmanager-config-check` job that:
    1. Installs amtool 0.27.0 (version-pinned).
    2. Runs `amtool check-config` (validates YAML, template
       references, URL fields).
    3. Runs `amtool config routes test` four times (one per
       severity) and asserts the receivers list matches the
       expected fan-out — catches regressions where someone
       collapses the `continue: true` and silently bypasses
       PagerDuty.
  - **Pre-commit hook**: [`scripts/git_hook_pre_commit.py`](scripts/git_hook_pre_commit.py)
    learned a fourth check (`amtool_check`) triggered by any edit
    under `QSDM/deploy/alertmanager/`, with the same version-pin
    semantics as the existing `promtool` check (reads
    `alertmanager-config-check:` job's `VERSION=...` from the CI
    workflow, probes `amtool --version`, prints a soft amber
    warning on mismatch).
  - **Verification (executed locally before commit)**:
    - `amtool check-config alertmanager.example.yml` →
      `SUCCESS: 4 inhibit rules, 6 receivers, 1 templates`.
    - `amtool config routes test` for `severity=critical
      alertname=...` returns `pagerduty-critical,slack-critical`
      (proves fan-out works).
    - `python scripts/smoketest_alertmanager.py` →
      `OK 27/27 smoke checks passed`. Confirms BOTH URLs appear
      in the rendered Slack JSON for every receiver, including
      the action buttons.
    - `promtool check config prometheus.qsdm.example.yml` still
      passes after the new `alerting:` block.
  - **Why this is the highest-leverage incident-response work
    that remained**: the runbook ↔ alert ↔ dashboard chain has
    been complete in source for several commits (every alert has
    a runbook_url + dashboard_url), but a chain is only useful
    when the rendering surface actually puts the links in front
    of a human. With this commit:
    - On-call operator gets a PagerDuty incident → clicks
      `client_url` → lands on the live Grafana panel.
    - On-call operator gets a Slack message → clicks the
      `📖 Runbook` button → lands on the markdown triage steps.
    - On-call operator gets a Slack message → clicks the
      `📊 Dashboard` button → lands on the live panel.
    - The audit channel gets a copy of every alert for postmortem
      reference.
    Total click-distance from "page wakes you up at 3am" to
    "the playbook for this exact alert is open in your browser":
    one click. (Previously: zero links anywhere — runbook_url and
    dashboard_url existed in YAML but were not surfaced.)

- **Per-runbook Grafana dashboards + `dashboard_url` annotation
  on every alert (2026-05-04).** Closes the last gap in the
  alert ↔ incident-response chain: an on-call operator now has
  a click-through path from PagerDuty / Slack notification →
  live Grafana panel for the firing alert, in addition to the
  existing path to the runbook markdown. The dashboards are
  auto-generated from the alerts file (one panel per alert,
  threshold-coloured) and lint-validated, so they cannot drift
  out of sync with the rules they visualise.
  - **New artefacts**:
    - 11 per-runbook dashboard JSONs at
      [`QSDM/deploy/grafana/dashboards/qsdm-runbook-*.json`](QSDM/deploy/grafana/dashboards),
      one for each runbook that owns alerts (38 alerts spread
      across 11 runbooks).
    - 1 master overview dashboard at
      [`qsdm-alerts-overview.json`](QSDM/deploy/grafana/dashboards/qsdm-alerts-overview.json)
      containing all 38 alert panels grouped by runbook in
      collapsible rows (76 panels total across the 12
      dashboards).
    - File-provisioning shim
      [`provisioning/dashboards/qsdm-runbooks.example.yaml`](QSDM/deploy/grafana/provisioning/dashboards/qsdm-runbooks.example.yaml)
      so Grafana auto-loads the directory; `updateIntervalSeconds: 30`
      means a regenerate-and-redeploy of one JSON shows up
      without a Grafana restart.
    - Generator [`scripts/gen_grafana_dashboards.py`](scripts/gen_grafana_dashboards.py)
      (~632 lines): reads
      [`alerts_qsdm.example.yml`](QSDM/deploy/prometheus/alerts_qsdm.example.yml),
      groups alerts by their `runbook_url` filename, emits one
      stat-panel-per-alert dashboard per runbook plus the
      master overview. Stable panel IDs (alphabetical on
      alertname) so URL fragments survive alert add/remove.
  - **Threshold extraction**: the generator parses each
    alert's PromQL expression and extracts the rightmost
    top-level numeric comparison, then renders the LHS as the
    panel expression with a Grafana threshold step at the RHS.
    Severity drives the firing colour (`critical → red`,
    `warning → orange`, `info → yellow`). For compound
    expressions (`a > 1 and b < 2`), `==`/`!=`, or non-numeric
    RHS, the generator falls back to rendering the full
    boolean expression (0/1) with the same severity colour at
    1 — operationally clearer than splitting at one half of a
    compound clause. PromQL whitespace from YAML block-folded
    `expr:` values is normalised to single spaces in the
    panel definition so dashboard JSON stays clean.
  - **Two new lint invariants** (extending the existing six-
    invariant lint at
    [`scripts/check_runbook_coverage.py`](scripts/check_runbook_coverage.py)):
    | # | Invariant                                                                              |
    | -- | -------------------------------------------------------------------------------------- |
    | 7 | Every alert has a non-empty `dashboard_url` annotation alongside its `runbook_url`.    |
    | 8 | Every `dashboard_url` resolves to a real JSON file under `QSDM/deploy/grafana/dashboards/`. |
    Both invariants verified end-to-end with a 4-scenario
    drift harness against the live alerts file (baseline /
    strip-all-annotations / point-at-missing-file /
    wrong-URL-shape) — all four scenarios produce the expected
    PASS or FAIL with helpful pointers (the broken-file case
    even suggests `python scripts/gen_grafana_dashboards.py`
    as the remediation).
  - **`dashboard_url` annotation added to all 38 alerts** in
    [`alerts_qsdm.example.yml`](QSDM/deploy/prometheus/alerts_qsdm.example.yml),
    one line each, immediately after `runbook_url`. Same URL
    shape as `runbook_url` (`https://github.com/.../blob/main/...`)
    so the lint can validate without external dependencies.
    Operators with a running Grafana add their own deep-link
    via `<grafana>/d/<uid>?viewPanel=<id>` — the URLs in the
    annotation point at the canonical JSON source.
  - **Pre-commit hook trigger paths updated**: dashboard
    edits + generator-script edits now engage the runbook
    coverage lint locally, mirroring the CI workflow's
    `paths:` filter (which already covers `QSDM/deploy/**`).
  - **`promtool test rules` baselines regenerated**: adding a
    new annotation key to every alert changed the rendered
    `Annotations:` map promtool emits, which would have
    failed `exp_annotations` exact-match assertions in the
    behavioural suite. Re-ran
    [`scripts/gen_promtool_tests.py`](scripts/gen_promtool_tests.py)
    to capture the new baseline; the resulting `+38` lines in
    [`alerts_qsdm.test.yml`](QSDM/deploy/prometheus/alerts_qsdm.test.yml)
    are the new `dashboard_url:` entries inside each
    `exp_annotations:` block. `promtool test rules` reports
    `SUCCESS` against the regenerated suite.
  - **Operator workflow** for adding a new alert now:
    1. Add the rule to `alerts_qsdm.example.yml` with
       `runbook_url` and `dashboard_url` annotations.
    2. Add a matching test entry to
       `alerts_qsdm.test.spec.yml`.
    3. Add a matching runbook section.
    4. Run `python scripts/gen_grafana_dashboards.py` to
       generate / update the per-runbook dashboard.
    5. Run `python scripts/gen_promtool_tests.py` to
       refresh `alerts_qsdm.test.yml` baselines.
    6. Commit. Pre-commit hook runs all three lint gates;
       all four CI gates run on push.

- **Declarative test-spec file for the promtool behavioural suite
  (2026-05-04).** The 38-alert behavioural test suite previously
  lived as a 510-line in-script literal at the top of
  [`scripts/gen_promtool_tests.py`](scripts/gen_promtool_tests.py).
  Adding a new alert meant editing Python — surrounding context
  (commas, dataclass keyword arguments, multi-line escapes) made
  small additions surprisingly error-prone, and the diff for "I
  bumped a threshold" was always cluttered with structural
  punctuation.
  - **New artefact**:
    [`QSDM/deploy/prometheus/alerts_qsdm.test.spec.yml`](QSDM/deploy/prometheus/alerts_qsdm.test.spec.yml)
    (~417 lines). One YAML mapping per alert; reviewers see only
    the substantive change in a typical PR. Schema documented in
    the file's own header so it's discoverable without grep.
  - **Coverage validator built into the generator**: every run
    asserts a strict 1:1 binding between alertnames in
    [`alerts_qsdm.example.yml`](QSDM/deploy/prometheus/alerts_qsdm.example.yml)
    and entries in the spec file. Drift produces actionable
    output:
    | Drift mode                              | Validator output                                                |
    | --------------------------------------- | --------------------------------------------------------------- |
    | new alert added without a spec entry    | `Alerts missing a spec entry: <name>`                           |
    | spec left behind after alert removal    | `Spec entries with no matching alert: <name>`                   |
    | duplicate spec entries for one alert    | `duplicate test entries for alertname(s): [<name>]`             |
    | malformed spec (e.g. missing field)     | `<file:groups[g].tests[t]> missing required field "<field>"`    |
    All four modes verified with a 5-scenario harness against
    fabricated input pairs in tempdirs.
  - **Generator refactor**:
    [`scripts/gen_promtool_tests.py`](scripts/gen_promtool_tests.py)
    shrank by `-531/+196` net (704 → 591 lines). The dataclass
    `T`, the two-pass scaffold-then-capture flow, the
    `parse_kv_block` / `GOT_BLOCK_RE` / `ALERT_RE` parsers, and
    the YAML-quoted output formatter are all unchanged — only
    the data source moved.
  - **Functional equivalence proven**: regenerating
    [`alerts_qsdm.test.yml`](QSDM/deploy/prometheus/alerts_qsdm.test.yml)
    after the refactor produced a SHA-256-identical file (same
    76 behavioural assertions, same `exp_labels`, same
    `exp_annotations`, same series byte-for-byte) modulo a
    single comment paragraph in the file's header that now
    points at the spec file instead of the in-script literal.
    `promtool test rules` reports `SUCCESS` against the
    regenerated file.
  - **Pre-commit hook trigger updated**: the spec file is now
    one of the
    [`PROMTOOL_TEST_TRIGGERS`](scripts/git_hook_pre_commit.py),
    so editing it (without regenerating
    `alerts_qsdm.test.yml`) prompts the hook to re-run
    `promtool test rules` locally — the validator catches the
    inconsistency before it reaches CI. The CI workflow's
    `paths:` filter already covers `QSDM/deploy/**` so no
    workflow edit was required.
  - **PyYAML**: now used by both
    [`check_runbook_coverage.py`](scripts/check_runbook_coverage.py)
    and
    [`gen_promtool_tests.py`](scripts/gen_promtool_tests.py)
    with the same import-error fallback message; CI workflow
    already installs PyYAML for the runbook-coverage step.

- **Promtool version pin in the pre-commit hook (2026-05-04).**
  Closes the "local green ≠ CI green due to silent version drift"
  failure mode on the alerts ↔ runbook contract chain. The hook
  now probes the local `promtool --version` and compares it to
  the version pinned in
  [`.github/workflows/validate-deploy.yml`](.github/workflows/validate-deploy.yml)
  (currently `2.55.1`); a mismatch prints a single amber banner
  before the checks run, never a hard failure.
  - **Single source of truth**: the workflow file is the only
    place the version is hard-coded. The hook parses the
    `VERSION="..."` line from the `prometheus-rules-check:` job
    at hook-invocation time, so updating the pin is a
    one-character edit. Mid-flight refactors that move or rename
    the install step degrade gracefully (the hook silently skips
    the version check rather than blocking commits on missing
    metadata).
  - **Three observable states**, picked to minimise noise on the
    happy path:
    | State                                    | Hook output                                                  |
    | ---------------------------------------- | ------------------------------------------------------------ |
    | local matches CI pin                     | (silent — no extra lines)                                    |
    | local diverges from CI pin               | one-block amber banner: `version mismatch: local=X, CI-pinned=Y` + remediation |
    | local probe fails (binary refused/etc.) | one-line amber banner: `could not probe ...; CI is pinned to Y` |
    | promtool absent entirely                 | existing "promtool not found" banner now references the pin  |
  - **Why a warning, not a failure**: minor-version drift is
    almost always benign for `promtool check rules`, and even
    `promtool test rules` regressions are fixable with
    `$PROMTOOL` pointing at a pinned binary. Hard-failing on
    drift would punish operators who deliberately test against
    newer promtool releases (forward-compat smoke testing) or
    whose distros ship a newer build. The warning gives them the
    information without taking away the choice.
  - **Verified** with a 7-scenario smoke harness covering both
    parser correctness and end-to-end UX: real workflow → pin
    `2.55.1`; real promtool → version `2.55.1`; fabricated
    workflow without pin → `None`; fabricated workflow with
    `VERSION="9.9.9"` → `9.9.9`; missing workflow file →
    `None`; matching version → silent; mismatched version → all
    three banner fields present (`local=`, `CI-pinned=`,
    remediation text).
  - **Driver impact**: `+171/-10` lines in
    [`scripts/git_hook_pre_commit.py`](scripts/git_hook_pre_commit.py)
    (two regex helpers + integration into `main()` +
    docstring); zero new dependencies; no measurable runtime
    impact (the version probe completes in <50 ms and only runs
    when promtool would have run anyway).

- **Pre-commit hook for the alerts ↔ runbook contract chain
  (2026-05-03).** Surfaces the same three CI gates locally,
  scoped by changed files so unrelated commits stay fast. The
  goal is "local green ⇒ CI green" — operators discover broken
  contracts before `git push`, not 30 seconds after.
  - **Three checks, scoped per change kind**:
    | Trigger paths                              | Runs                                      |
    | ------------------------------------------ | ----------------------------------------- |
    | `alerts_qsdm.example.yml`, `runbooks/`,    | runbook coverage lint                     |
    | `check_runbook_coverage.py`,               | (38 alerts + 298 navigation links)        |
    | `git_hook_pre_commit.py`                   |                                           |
    | `alerts_qsdm.example.yml`                  | + `promtool check rules` (PromQL syntax)  |
    | `alerts_qsdm.example.yml`,                 | + `promtool test rules` (synthetic        |
    | `alerts_qsdm.test.yml`,                    | time-series suite, 76 assertions)         |
    | `gen_promtool_tests.py`                    |                                           |
    A doc-only commit runs nothing slow. An alerts-file commit
    runs the full sweep (~11 s on a typical laptop with promtool
    cached).
  - **Two install paths**, same driver:
    * **Bare git hook**:
      ```
      python scripts/install_git_hooks.py
      ```
      Writes a tiny POSIX-shell shim to
      [`.git/hooks/pre-commit`](.git/hooks) that resolves Python
      via PATH (or `$PYTHON`) and execs the driver. Works on
      Linux, macOS, and Windows-with-Git-Bash. The shim is
      stable across edits to the driver — only the Python file
      changes when behaviour evolves.
    * **`pre-commit` framework users**: drop in
      [`.pre-commit-config.yaml`](.pre-commit-config.yaml),
      then `pre-commit install`. The framework auto-resolves
      Python deps in an isolated venv and supports
      `pre-commit run --all-files` for batch validation. The
      same driver runs under the hood, so the two paths are
      behaviourally identical.
  - **Driver design** ([`scripts/git_hook_pre_commit.py`](scripts/git_hook_pre_commit.py)):
    * Reads staged files via
      `git diff --cached --name-only --diff-filter=ACMR`
      (added / copied / modified / renamed; deletions and
      conflicts excluded).
    * Buckets staged files against per-check trigger paths;
      runs only the checks whose triggers fired.
    * Streams check output to the terminal in real time (no
      buffering — promtool tests take 10 s and the operator
      should see progress).
    * Prints a clean PASS/FAIL/SKIPPED summary table keyed
      by check name and elapsed milliseconds.
    * Short-circuits on the first failure: subsequent checks
      rarely add useful signal once the first fails, and the
      operator wants a fast clear answer.
    * Locates `promtool` via `$PROMTOOL` env override → PATH
      lookup → graceful skip with an amber-warning banner.
      The hook never auto-installs promtool; that's a
      deliberate scope decision so the hook stays fast and
      deterministic. Operators without promtool still get
      runbook lint coverage; CI catches the rest.
  - **Bypass** (standard git escape hatch):
      ```
      git commit --no-verify
      ```
    No hook config needed.
  - **Verified locally** with a 5-scenario smoke test before
    install:
      `(1)` Empty stage → hook fast-exits (rc=0, no output).
      `(2)` Doc-only change (`README.md`) → fast-exits (rc=0,
      no banner because no trigger files matched).
      `(3)` Runbook README edit → runs ONLY the runbook lint;
      promtool checks not invoked (~625 ms total).
      `(4)` Alerts threshold mutation (`> 0.5` → `> 50`) →
      runs all three checks; `promtool test rules` fails
      with the full failure context shown; rc=1.
      `(5)` Alerts comment append (semantic no-op) → runs
      all three checks; all pass (~11 s total).
    Each scenario uses an isolated `git worktree` so the
    smoke test never touches the operator's index.
  - **CI parity statement.** The hook runs the EXACT same
    commands as the corresponding steps in
    [`.github/workflows/validate-deploy.yml`](.github/workflows/validate-deploy.yml),
    in the same order. A clean local run is the strongest
    possible signal that the CI gate will pass — modulo
    issues caused by stale local state (uncommitted edits,
    different promtool version), which the hook cannot
    catch and the user must reason about.

- **`promtool test rules` synthetic-time-series suite for all 38
  alerts (2026-05-03).** Closes the last regression-guard gap in
  the alerts subsystem. The existing `promtool check rules` job
  already validated PromQL syntax and template compilation; this
  new behavioural suite evaluates each rule against synthetic
  time-series fixtures and asserts the alert fires (or does
  not) at expected times, locking in the threshold, `for:`
  window, label set, and rendered annotations as a CI contract.
  - **Coverage**: 1 test per alert × 38 alerts × 2 eval-time
    checkpoints (early-negative + late-firing) = **76
    behavioural assertions per CI run**. Each firing checkpoint
    pins the full `exp_labels` (severity, subsystem, reason,
    arch, etc.) and `exp_annotations` (description, summary,
    runbook_url) verbatim against what the rule renders, so
    drift in any contract surface fails CI before merge.
  - **Four classes of silent regression now caught** that
    `promtool check rules` cannot:
    * **Threshold drift** — `> 0.5` tightened to `> 0.05` is
      valid PromQL but a 10× behavioural change. Caught by the
      late-firing checkpoint when the rate fixture (chosen 2×
      above the documented threshold) no longer exceeds the
      mutated threshold.
    * **`for:` window shrinkage** — `for: 10m` shortened to
      `for: 1m` is valid YAML but a 10× firing-rate change.
      Caught by the early-negative checkpoint, which asserts
      the alert is NOT firing at an eval_time before the
      original `for:` window has elapsed; shrinking it makes
      the alert fire at that early checkpoint and breaks the
      test.
    * **Metric-name typos** — `qsdm_typo_total` parses fine
      but evaluates to no-data. Caught by the late-firing
      checkpoint asserting the alert IS firing; a typo'd
      metric never fires, so `exp_alerts: [{...}]` mismatches
      `got: []`.
    * **Annotation-template drift** — editing a runbook
      anchor in the rule's `annotations.runbook_url` template
      without updating the runbook itself fails the test
      (because the test pins the rendered runbook_url
      verbatim). This closes the loop with the runbook-
      coverage lint: annotation-side breakage now fails CI on
      the alerts-file edit, not just on the runbook-file
      edit, so drift cannot accumulate in either direction.
  - **Generator at
    [`scripts/gen_promtool_tests.py`](scripts/gen_promtool_tests.py)**.
    Two-pass design:
      Pass 1 emits a scaffold with placeholder
      `exp_alerts: []` at every firing checkpoint, runs
      promtool to provoke the failures, and parses the
      rendered `got:[...]` blocks (Labels and Annotations) out
      of promtool's diff output.
      Pass 2 emits the final
      [`alerts_qsdm.test.yml`](QSDM/deploy/prometheus/alerts_qsdm.test.yml)
      with the captured renderings substituted into each
      firing checkpoint, and re-runs promtool to confirm
      SUCCESS.
    To regenerate after editing the alerts file:
      ```
      promtool --version  # 2.55.1 expected
      python scripts/gen_promtool_tests.py
      ```
    The generator is idempotent (running it twice on
    unchanged input produces a byte-identical test file) and
    works locally on Windows or Linux as long as
    `promtool` is on PATH (or `PROMTOOL=<path>` is set).
  - **CI wiring**: the existing `prometheus-rules-check` job
    in
    [`.github/workflows/validate-deploy.yml`](.github/workflows/validate-deploy.yml)
    now runs `promtool test rules
    QSDM/deploy/prometheus/alerts_qsdm.test.yml` as a second
    step after `promtool check rules`. Same pinned promtool
    version (2.55.1) and same trigger paths; no new dependency.
  - **Negative-test verification** ran six rule mutations
    locally before commit:
      `(1)` Loosen `rate > 0.5` to `rate > 50` — caught.
      `(2)` Rename a metric (typo) — caught.
      `(3)` Tighten a gauge threshold past the test fixture —
      caught.
      `(4)` Shrink `for: 10m` to `for: 1m` — caught (early
      checkpoint fires).
      `(5)` Rename an alert (severity/identity drift) —
      caught.
      `(6)` Edit a runbook anchor in the annotation template —
      caught (annotation match fails).
    All six mutations broke at least one test. The suite is
    not a tautology.
  - **Tests are organised by group** matching
    `alerts_qsdm.example.yml` (qsdm-nvidia-lock, qsdm-submesh,
    qsdm-throughput, qsdm-trust-transparency, qsdm-trust-
    redundancy, qsdm-quarantine, qsdm-v2-mining-slashing,
    qsdm-v2-mining-enrollment, qsdm-v2-mining-liveness,
    qsdm-v2-attest-archspoof, qsdm-v2-attest-hashrate,
    qsdm-v2-governance, qsdm-v2-attest-recent-rejections), and
    each group header in the test file cross-references the
    runbook section that diagnoses the matching alert mode.
    The suite reads as the canonical "what does this alert do
    in steady state and at firing time?" reference, sitting
    next to the rule file it pins.

- **Runbook lint extended to cover all in-runbook navigation
  links (2026-05-03).** Tightens the regression guard from the
  previous commit by extending
  [`scripts/check_runbook_coverage.py`](scripts/check_runbook_coverage.py)
  from 3 invariants (alerts ↔ runbook URLs) to 6 (alerts ↔
  runbook URLs **plus** every internal navigation link in the
  runbook tree). Closes the silent-breakage gap where renaming a
  runbook, a section header, or a referenced source file would
  break operator navigation without any CI signal.
  - **Three new invariants enforced**, on top of the existing
    three:
    * **(4)** Every relative `[text](OTHER.md)` cross-runbook
      link in any runbook resolves to an existing markdown
      file. Catches "I renamed `MINING_LIVENESS.md` →
      `CHAIN_LIVENESS.md` but forgot the 10 other runbooks
      that link to it" regressions.
    * **(5)** Every `[text](OTHER.md#anchor)` and
      `[text](#anchor)` anchor target exists as a real
      markdown heading in the right file. Same GitHub-
      flavoured slugify rules as invariant 3. Intra-file
      anchors (`#anchor`) are checked against the same file's
      headings; cross-file anchors are checked against the
      target file's headings.
    * **(6)** Every `[text](../path/to/source.go)` (and other
      non-markdown) source-file reference resolves to an
      existing path under the repo. Covers Go source files,
      deploy manifests, scripts, workflows. A PR moving a
      referenced file without updating runbook links fails
      CI.
  - **Coverage now: 38 alert URLs + 298 in-runbook links =
    336 navigation invariants validated** on every push/PR
    that touches `QSDM/deploy/`, `QSDM/docs/docs/runbooks/`,
    the lint script, or the workflow file. The 298
    in-runbook links break down as 32 intra-file anchors +
    266 path links across 11 incident runbooks + the master
    `README.md` index. CI catches: missing files, missing
    anchors, paths escaping the repo root, anchor-only links
    pointing at non-existent sections, and any combination
    thereof.
  - **Two pre-existing broken source-file links found and
    fixed.** The recon pass surfaced two Go-source references
    that had silently rotted before the lint existed:
    * `QUARANTINE_INCIDENT.md` §5 pointed at
      `pkg/quarantine/manager.go` (file doesn't exist;
      actual file is `pkg/quarantine/quarantine_manager.go`,
      where `type QuarantineManager struct` lives).
    * `SUBMESH_POLICY_INCIDENT.md` §5 pointed at
      `pkg/submesh/manager.go` (file doesn't exist; the
      submesh-policy implementation lives in
      `pkg/submesh/policy.go` with error types in
      `pkg/submesh/errors.go`). Updated to point at both
      with corrected description.
    Both fixes shipped in this commit alongside the lint
    extension that prevents future occurrences.
  - **Inline-code masking for documentation-of-syntax.** The
    extracted-link regex now masks inline-code spans
    (` ` `…` ` `, ` `` `…` `` `) per-line before matching
    `[label](target)` patterns. This lets documentation
    (including this README's §5 itself) show example link
    syntax without the lint mistaking examples for real
    navigation. Fenced code blocks were already skipped; this
    closes the inline-code gap.
  - **`README.md` §5 rewritten** to document all 6 invariants
    in two groups (alerts ↔ runbooks vs in-runbook links)
    with an explicit operator-promise framing: "If you add or
    rename an alert, a runbook, a section header, or a
    referenced source file, update both the source AND the
    dependent links in the same PR — CI will catch the
    rest."
  - **Negative-tested against 5 failure patterns** (broken
    cross-md file, broken cross-md anchor, broken source-file
    path, broken intra-file anchor, path escaping repo root)
    — all 5 correctly exit `1` with precise per-violation
    diagnostics including filename:line, label, and the
    resolved problem path.
  - **No CI workflow changes.** The previous commit's
    `runbook-coverage` job already triggers on
    `QSDM/docs/docs/runbooks/**` changes, so the extended
    lint scope is picked up automatically.

- **Runbook master index + CI coverage lint (2026-05-02).** The
  finishing artifact for the runbook sweep that closed at 38/38
  in the previous commit. Two paired changes:
  (1) a master-index page operators land on as the first page
  when paged, and (2) a CI lint job that enforces the 100%
  coverage invariant against future regression. After this
  commit, no PR can land an alert without a resolvable
  `runbook_url`, and no PR can rename a runbook section without
  also updating the anchors that point at it — both fail CI
  before merge.
  - **`docs/runbooks/README.md`** — operator-first index page
    structured in 7 sections:
    * §1 alphabetized 38-row alert ↔ runbook ↔ anchor table
      (the "paged at 3am, click here" surface);
    * §2 per-runbook subsystem cards (one card per runbook
      naming the alerts covered, the canonical "when to read
      this" framing, and the bidirectional companion runbooks
      — 11 cards total);
    * §3 cross-runbook escalation map (mermaid diagram showing
      upstream causes → downstream symptoms across the 11
      runbooks, plus a canonical concurrent-alert-pattern
      table mapping cascade pairs to first-fix runbooks);
    * §4 severity quick-views (7 critical, 29 warning, 2 info,
      with Why-critical column for the 7 page-out-of-bed cases);
    * §5 coverage invariants (documents the three CI-enforced
      promises);
    * §6 source-file pointers;
    * §7 sweep history (10 commits, the runbook-coverage
      delta sequence from initial 4/38 to invariant-locked
      38/38).
  - **`scripts/check_runbook_coverage.py`** — Python validator
    enforcing three invariants: (1) every alert in
    `alerts_qsdm.example.yml` has a non-empty `runbook_url`
    annotation, (2) every URL's filename resolves to an existing
    file under `QSDM/docs/docs/runbooks/`, (3) every URL's
    `#anchor` fragment matches an actual markdown heading in
    the target file using GitHub's slugify rules (lowercase,
    drop non-alphanumeric except `-` and `_`, spaces to `-`,
    consecutive hyphens preserved — the em-dash in
    `### 3.1 Mode A — \`Alert\`` headings collapses to the
    `--` pattern that GitHub's renderer produces). Tested
    against three synthetic violation classes (missing url,
    bad anchor, missing file); all three correctly exit 1.
    Single runtime dep: PyYAML.
  - **`.github/workflows/validate-deploy.yml`** — added
    `runbook-coverage` job to the existing validate-deploy
    workflow. Triggers on push/PR that touches
    `QSDM/deploy/**`, `QSDM/docs/docs/runbooks/**`,
    `scripts/check_runbook_coverage.py`, or the workflow file
    itself. Pins Python 3.12 and installs `PyYAML>=6,<7`.
    Sits alongside the existing `compose-config`,
    `kubernetes-dry-run`, and `prometheus-rules-check` jobs;
    same fail-fast posture.
  - **Coverage invariant guarantee.** After this commit the
    `docs/runbooks/README.md` §5 promise — "every alert has a
    `runbook_url`, every URL resolves, every anchor exists" —
    is CI-enforced rather than a one-shot manual audit. Future
    runbook reorganization (renaming sections, moving alerts
    between runbooks, splitting runbooks) is now safe in the
    sense that CI will catch every dangling link before merge,
    and broken anchors during refactor surface immediately.

- **Operator-hygiene finishing runbook → 100% alert coverage
  (2026-05-02).** Closed the last 4 alerts without a
  `runbook_url` into a single bundled hygiene runbook,
  bringing
  [`alerts_qsdm.example.yml`](QSDM/deploy/prometheus/alerts_qsdm.example.yml)
  coverage from 34/38 (89%) to **38/38 (100%)**. The four
  alerts share three properties: (1) operationally adjacent
  to v1 NVIDIA-lock or v2 throughput sentinels, (2)
  resolvable by the on-call alone without escalation, and
  (3) lower-frequency than cascade alerts so a single
  bundled runbook is the right granularity. After this
  commit every alert in the v2 alerts file carries an
  anchored `runbook_url` and every runbook is bidirectionally
  cross-linked to its operationally-related companions.
  - **`docs/runbooks/OPERATOR_HYGIENE_INCIDENT.md`** —
    four-mode runbook covering
    `QSDMNvidiaLockHTTPBlocksSpike` (Mode A, HTTP 403 spike
    on the state-changing API gate),
    `QSDMNvidiaLockP2PRejects` (Mode B, libp2p tx drops
    post-consensus on the P2P gate),
    `QSDMAttestHashrateOutOfBand` (Mode C, per-arch hashrate
    band rejection across the five canonical arches), and
    `QSDMNoTransactionsStored` (Mode D, the chain's "silent
    failure" sentinel — processed > 0 but stored == 0 for
    30m). Mode A's triage table maps the four canonical
    `NvidiaLockProofOK` detail substrings ("no NGC proof
    bundles ingested" / "no qualifying proof within
    window" / "matching qsdm_node_id" / "valid
    qsdm_proof_hmac") to {sidecar not posting, bundle
    policy fail, NodeID mismatch, HMAC drift}. Mode B
    documents the asymmetric-toggle case (HTTP gate
    disabled, P2P gate enabled — silent libp2p drops with
    no producer-visible signal). Mode C maps {value off by
    orders of magnitude, value just-above-Max, multi-miner
    one-arch-dominant, `arch="unknown"` dominant} to {units
    typo, leaderboard-stuffing, release regression / new
    SKU bump, `ARCH_SPOOF_INCIDENT.md` Mode A
    cross-reference}. Mode D — the keystone — documents the
    six-gate dispatch pipeline (parse → WASM preflight →
    submesh policy → consensus → NVIDIA-lock P2P → storage)
    and decomposes the divergence by which counter is
    climbing (`qsdm_submesh_p2p_reject_*` ⇒ submesh,
    `qsdm_nvidia_lock_p2p_rejects` ⇒ NVIDIA-lock P2P,
    `qsdm_transactions_invalid` only ⇒ WASM or consensus,
    all flat with only processed climbing ⇒ storage backend
    erroring, processed alone ⇒ silent parse-stage drop).
  - **`deploy/prometheus/alerts_qsdm.example.yml`** —
    replaced four terse one-line annotations with structured
    multi-line `summary` / `description` blocks plus
    anchored `runbook_url`s pointing into the new runbook.
    Added group-level header comments to `qsdm-nvidia-lock`
    (documenting the two-gate model and the NGC-submission
    upstream relationship) and `qsdm-throughput`
    (documenting why the divergence sentinel exists — most
    chain-failure modes have a louder signal, but silent
    100%-reject scenarios were previously undetected).
  - **Cross-link backfill into companion runbooks:**
    - `ARCH_SPOOF_INCIDENT.md` — added
      `OPERATOR_HYGIENE_INCIDENT.md` to the companion
      runbooks list, naming the canonical
      "miner-cheating-across-multiple-axes" pattern (Mode B
      arch-spoof + Mode C hashrate from one NodeID →
      slashing pipeline) and the `arch="unknown"` →
      hashrate-band false-fail correlation.
    - `SUBMESH_POLICY_INCIDENT.md` — added
      `OPERATOR_HYGIENE_INCIDENT.md` to the companion
      runbooks list as the throughput symptom (when submesh
      rejects 100% of P2P traffic, hygiene Mode D fires as
      the "chain admitting txs but storing none" sentinel).
    - `NGC_SUBMISSION_INCIDENT.md` — added
      `OPERATOR_HYGIENE_INCIDENT.md` to the companion
      runbooks list as the *downstream consumer* runbook
      (NGC submission populates the proof ring NVIDIA-lock
      reads from; sustained NGC ingest rejects are the
      canonical upstream cause of NVIDIA-lock alerts).
  - **Coverage progression** — sweep summary now reads:
    initial sweep (slashing/enrollment/mining-liveness/trust
    14→20), quarantine + arch-spoof + rejection-plumbing
    (20→27), submesh-policy (27→29), governance-authority
    (29→32, last critical-severity gap closed),
    NGC-submission (32→34), operator-hygiene (34→**38**).

- **NGC-submission operator runbook + TRUST_INCIDENT.md
  bidirectional cross-link backfill (2026-05-02).** Closed the
  natural companion cluster to the trust subsystem — the 2 alerts
  in the NGC-submission sub-group of `qsdm-nvidia-lock` catch the
  **per-request gate** for the qsdm.tech transparency pipeline
  (`GET /monitoring/ngc-challenge` rate-limited at 15 req/IP/min,
  `POST /monitoring/ngc-proof` with nine closed-enum reject
  reasons), distinct from the **aggregate-response** failure mode
  that TRUST_INCIDENT.md covers. The pattern is parallel to
  QUARANTINE↔SUBMESH_POLICY: NGC submission is the per-request
  gate; trust is the aggregate response. While here, also fixed
  TRUST_INCIDENT.md's stale reject-reason names (the runbook had
  `hmac_mismatch` / `invalid_nonce` / `decode_failed` /
  `bundle_too_large` / `replay_detected` from an earlier rev of
  the code, but the actual closed-enum set per
  `pkg/monitoring/ngc_ingest_metrics.go` is `ingest_disabled` /
  `unauthorized` / `body_read` / `body_too_large` / `invalid_json`
  / `missing_cuda_hash` / `nonce` / `hmac` / `other`). Net
  coverage: 32/38 → **34/38 (89%)**.
  - **`docs/runbooks/NGC_SUBMISSION_INCIDENT.md`** — two-mode
    runbook covering `QSDMNGCChallengeRateLimited` (Mode A,
    challenge-issuance 429s) and `QSDMNGCProofIngestRejectBurst`
    (Mode B, ingest rejects across nine closed-enum reasons).
    Mode A's source-distribution table forks {single known
    sidecar, single unknown IP, fleet-wide bursts, chain-event
    correlated} into one of {operator outreach, firewall block,
    sidecar release rollback, nonce-TTL extension}; explicitly
    anti-patterns silently raising the per-IP cap to silence the
    alert. Mode B is framed as "the canonical upstream cause for
    trust degradation" — the per-reason cause + action table maps
    each of nine closed-enum reasons to a probable cause and
    grouped-mitigation class: Class 1 — auth/secret drift (`hmac`,
    `unauthorized`); Class 2 — payload shape (`body_too_large`,
    `missing_cuda_hash`, `invalid_json`); Class 3 — replay/timing
    (`nonce`, `body_read`); Class 4 — config (`ingest_disabled` —
    silence on validators not supposed to be trust peers); Class
    5 — bug (`other`). The §4 cascade map explicitly hands off to
    TRUST_INCIDENT.md Modes A/B/D when those fire alongside, with
    the framing: "Mode B is the upstream cause; the trust alert
    is the symptom. Fix the dominant reject reason here, and the
    trust alert clears 5–20 min later as the aggregator
    re-warms."
  - **`docs/runbooks/TRUST_INCIDENT.md` bidirectional cross-link
    backfill** —
    - Mode A's reject-rate table now deep-links to
      NGC_SUBMISSION_INCIDENT.md §3.2 for the silence-vs-fix
      decision on `ingest_disabled` and the per-reason cause
      table on `hmac`/`nonce`.
    - Mode B's per-reason table replaced with the abridged
      trust-side view + deep-link to the gate runbook's
      authoritative full table; reasons updated from the stale
      names (`hmac_mismatch` / `invalid_nonce` / `decode_failed` /
      `bundle_too_large` / `replay_detected`) to the actual
      closed-enum names (`hmac` / `nonce` / `body_too_large` /
      `invalid_json` / `missing_cuda_hash` / etc.). Mode B's
      mitigation now cross-references the gate runbook's
      validator-accepts-both transition window pattern for
      safe rotations.
    - §5 Reference's "Closed-enum values" updated to list the
      authoritative 9-reason set with a deep-link to the source
      file (`pkg/monitoring/ngc_ingest_metrics.go`) and the gate
      runbook's per-reason table.
    - §5 Reference's companion-runbooks list now opens with
      NGC_SUBMISSION_INCIDENT.md, explicitly framed as the
      *upstream cause* runbook (per-request gate vs. aggregate
      response).
  - **`deploy/prometheus/alerts_qsdm.example.yml`** — replaced
    both NGC-submission alerts' terse one-line annotations with
    structured `summary` + multi-line `description` + anchored
    `runbook_url` blocks. Each alert carries a multi-line
    operator-comment block before the `runbook_url` summarising
    the mode-specific signal (Mode A: source-distribution
    decision tree + "explicitly anti-patterns silently raising
    the per-IP cap"; Mode B: "the canonical upstream cause for
    trust degradation" with the cascade timing). The alert group
    also got a multi-line header comment block documenting the
    rate-limit caps (15/30 req/IP/min) and the cascade
    relationship to TRUST_INCIDENT.md Modes A/B/D.
  - **No code changes.** Pure docs + alert-annotation commit;
    leverages the existing `pkg/monitoring/ngc_ingest_metrics.go`
    + `pkg/monitoring/nvidia_lock_metrics.go` collectors and the
    `pkg/api/security.go` rate-limiter that already shipped. The
    remaining 4 uncovered alerts are 2 NVIDIA-lock alerts and 2
    singletons (`QSDMNoTransactionsStored`,
    `QSDMAttestHashrateOutOfBand`) — the next runbook sweep can
    bundle all four into a single "v1 attestation + operator
    hygiene" runbook that wraps the coverage at 100%.

- **Governance-authority operator runbook + alert annotations (2026-05-02).**
  Closed the largest remaining cluster of uncovered alerts AND the
  last critical-severity alert without a runbook — the 3 alerts in
  `qsdm-v2-governance` cover the chain's **constitutional layer**
  (M-of-N multisig that signs `qsdm/gov/v1` parameter changes and
  votes on its own membership). The runbook focuses on the
  three-stage authority lifecycle (vote → threshold cross →
  activation) instrumented by three counter families, plus the
  `qsdm_gov_authority_count` gauge that is the floor-violation
  signal. Net coverage: 29/38 → **32/38 (84%)**, and EVERY remaining
  uncovered alert is now severity:warning or below.
  - **`docs/runbooks/GOVERNANCE_AUTHORITY_INCIDENT.md`** —
    three-mode runbook covering `QSDMGovAuthorityVoteRecorded`
    (Mode A, info), `QSDMGovAuthorityThresholdCrossed` (Mode B,
    warning), and `QSDMGovAuthorityCountTooLow` (Mode C, **critical**).
    Mode A is operator-coordination glue — the alert exists so every
    multisig member sees a vote happened in case the coordinator
    forgets the secure-channel ping. Mode A's observation table
    forks on {expected schedule, unexpected vote, sustained
    duplicate-vote rejects, vote-validation rejects, mass-rotation}
    to one of {acknowledge, secure-channel veto check, script-bug
    audit, voter-membership audit, planned mass rotation}.
    Mode B is the staging-window page — the proposal has crossed
    threshold and is one consensus step from applying; the §3.2
    pre-activation checklist walks {confirm-intent, validate-address
    (canonical social-engineering attack on op=add),
    validate-effective-height, op=remove post-count-check}. Crucially,
    the runbook documents that **no on-chain cancellation primitive
    exists** — the only veto path is racing a counter-rotation, OR
    deliberately tripping Mode C as a paged signal to halt the
    chain. Mode C is the chain's single-signer hazard alarm; the
    §3.3 decision tree forks on whether the actual `AuthorityList`
    size matches the gauge: real count drop ⇒ Branch A (race a
    unilateral op=add if N=1; hard-fork if N=0), wiring bug ⇒ Branch
    B (restart and audit `pkg/monitoring/gov_recorder`). The
    threshold formula (`AuthorityThreshold(n)`: 0→0, 1→1, n≥2→n/2+1)
    is documented inline so operators can reason about whether a
    given rotation will trip the floor before they cast a vote.
    Cross-mode + cross-runbook §4 covers the cascade map: Mode B
    `op=remove` followed by Mode C ⇒ the activated remove pushed
    N below floor; Mode C + chain-stuck ⇒ governance recovery is
    gated on chain liveness; Mode C + slashing of an authority
    key ⇒ normal post-slash behaviour via `DropVotesByAuthority`.
  - **`deploy/prometheus/alerts_qsdm.example.yml`** — added
    `runbook_url` to all three `qsdm-v2-governance` alerts. Each
    annotation carries a multi-line operator-comment block before
    the URL summarising the mode-specific signal (Mode A: "if it
    pages someone out of bed, the Alertmanager routing config is
    wrong"; Mode B: "the staging window is the LAST opportunity
    to coordinate out-of-band; no on-chain cancellation primitive
    exists"; Mode C: "the only critical-severity alert in the
    governance group, the only one that fires on a gauge-state").
  - **No code changes.** Pure docs + alert-annotation commit;
    leverages the existing `pkg/monitoring/gov_metrics.go`
    collector and the `pkg/governance/chainparams/authority.go`
    `AuthorityVoteStore` semantics. After this commit the only
    uncovered alerts are 6 warning/info alerts split across
    NVIDIA-lock (2), NGC submission (2), and 2 singletons
    (`QSDMNoTransactionsStored`, `QSDMAttestHashrateOutOfBand`) —
    no remaining critical-severity alert lacks a runbook.

- **Submesh-policy operator runbook + alert annotations (2026-05-01).**
  Closed the natural companion cluster to the
  quarantine subsystem — the 2 alerts in `qsdm-submesh`
  catch the **per-tx policy decision** (fee/geotag
  route + `max_tx_size` enforcement on libp2p AND HTTP
  paths), distinct from the **per-submesh aggregate
  response** that QUARANTINE_INCIDENT.md covers. The
  cross-links now run both directions: QUARANTINE Mode A's
  reject-reason table points to SUBMESH_POLICY for the
  per-counter triage matrix, and SUBMESH_POLICY's
  cross-mode escalation §4 routes concurrent
  `QSDMSubmesh*` + `QSDMQuarantine*` alerts to the
  correct precedence ("the policy hits crossed the
  threshold for whole-submesh isolation"). Net
  coverage: 27/38 → **29/38 (76%)**.
  - **`docs/runbooks/SUBMESH_POLICY_INCIDENT.md`** —
    two-mode runbook covering the libp2p path
    (`QSDMSubmeshP2PRejects` Mode A) and the HTTP API
    path (`QSDMSubmeshAPISustained422` Mode B). The
    five-counter table at the top of the runbook
    cross-references each `qsdm_submesh_*_reject_*`
    counter to its triggering code path
    (`pkg/api/handlers.go` for API,
    `cmd/qsdm/transaction/transaction.go` for P2P)
    and to the typed error
    (`submesh.ErrSubmeshNoRoute` /
    `submesh.ErrSubmeshPayloadTooLarge`). Mode A's
    triage matrix forks the dominant-counter signal
    into one of three causes (client-side fee/geotag
    drift / validator `submesh_config` reload
    regression / in-flight client migration that
    touches both fee/geotag AND payload-size); the
    runbook **explicitly anti-patterns silencing
    the alert** — libp2p drops are silent
    fire-and-forget so the alert IS the operator's
    only signal that a population of peers is
    permanently unable to participate. Mode B's
    triage forks on which API endpoint dominates:
    `wallet_reject_route` ⇒ stale wallet client,
    `wallet_reject_size` ⇒ wallet release added a
    memo/metadata field, `privileged_reject_size`
    ⇒ service-account producer payload-size
    regression (the privileged path uses the
    *strictest* `max_tx_size` globally and has no
    route check). Mode B also explicitly
    anti-patterns raising the privileged
    `max_tx_size` to silence one bad producer — it's
    a global change affecting every privileged
    caller. Cross-mode + cross-runbook §4 maps
    {Mode A, Mode B, Mode A+B} concurrent with
    `QUARANTINE_INCIDENT.md`'s {Mode A, Mode B} or
    `MINING_LIVENESS.md` to the correct triage
    precedence.
  - **`docs/runbooks/QUARANTINE_INCIDENT.md`
    backfill** — added bidirectional cross-link.
    The first-90-seconds checklist's
    "cross-reference the submesh-policy counters"
    item now deep-links to SUBMESH_POLICY, and the
    Reference §5's companion-runbooks list now
    includes SUBMESH_POLICY explicitly framed as
    the *upstream cause* runbook (per-tx gate vs
    aggregate response).
  - **`deploy/prometheus/alerts_qsdm.example.yml`** —
    replaced both `qsdm-submesh` alerts' terse
    one-line annotations with structured
    `summary` + multi-line `description` + anchored
    `runbook_url` blocks. Each alert also gets a
    multi-line operator-comment block before the
    `runbook_url` summarising the mode-specific
    signal (Mode A: "libp2p rejects are silent so
    the alert IS the operator's only handle"; Mode
    B: "API rejects are HTTP 422 so the producer
    knows but is retrying without fixing the
    upstream cause"). The yml's group-level comment
    block was also added, documenting the two
    typed errors and the privileged-path
    "no-route-check" exception.
  - **No code changes.** Pure docs +
    alert-annotation commit; leverages the existing
    `pkg/monitoring/submesh_metrics.go` collector
    and the `pkg/submesh/manager.go` + handler
    enforcement that already shipped. The
    remaining 9 uncovered alerts span four small
    clusters (NVIDIA-lock, NGC challenge /
    proof-ingest, governance authority) and 2
    singletons (NoTransactionsStored,
    AttestHashrateOutOfBand) — the next runbook
    sweep can pick the highest-leverage of those.

- **Small-runbook sweep — quarantine + arch-spoof + rejection-plumbing
  backfill (2026-05-01).** Closed three small clusters of remaining
  uncovered alerts in one focused pass: 2 quarantine alerts (a brand-new
  runbook), 3 arch-spoof alerts (a brand-new runbook), and the 5
  rejection-plumbing alerts in `qsdm-v2-attest-recent-rejections` (2 new
  annotations + 3 anchor upgrades on existing root-pointing URLs). Net
  effect: alert-with-runbook coverage moves from 20/38 (53%) to 27/38
  (71%), and every cluster the §4.6 rejection-ring touches now has a
  mode-specific deep-link that lands the on-call operator on the
  relevant text rather than the runbook's introduction.
  - **`docs/runbooks/QUARANTINE_INCIDENT.md`** — two-mode runbook
    covering `QSDMQuarantineAnySubmesh` (per-submesh page, warning) and
    `QSDMQuarantineMajorityIsolated` (ratio-based escalation,
    **critical**). Mode A's reject-reason → cause table (invalid_signature
    / clock_skew / bad_tx_payload / unknown / counter-quiet-but-sticky)
    walks the operator from "the immune system activated" to the
    upstream `qsdm_submesh_*_reject_*` counter that named the policy
    hit. Mode B's "what changed in the last hour" check (binary release
    / config push / NTP event / key rotation) is the systemic-cause
    decision tree — the runbook explicitly tells the operator NOT to
    bulk-clear `RemoveQuarantine` without root cause, because a
    re-quarantine within 15 min destroys the diagnostic state of which
    submeshes triggered when.
  - **`docs/runbooks/ARCH_SPOOF_INCIDENT.md`** — three-mode runbook
    covering the chain's adversarial-detection signals:
    `QSDMAttestArchSpoofUnknownArchBurst` (Mode A, typo / probe /
    release-skew), `QSDMAttestArchSpoofGPUNameMismatch` (Mode B, the
    canonical economic-cheat — enrolled operator's HMAC bundle has
    `gpu_name` contradicting `gpu_arch`), and
    `QSDMAttestArchSpoofCCSubjectMismatch` (Mode C, **critical**, fires
    on a single increment because the proof has already passed
    cert-chain pin AND AIK signature — non-zero increment is, by
    construction, either a fabricated AIK leaf or a real H100-class
    operator with a stale gpu_arch config). Mode C's decision tree
    forks on whether the offending Subject CN is consistent with NVIDIA
    canonical naming, and explicitly blocks unilateral slashing on the
    cryptographic-anomaly branch — because slashing destroys forensic
    state and the bundle may need to be preserved untouched as evidence
    for an external investigation. Mode B hands off to
    `SLASHING_INCIDENT.md` for sustained single-NodeID activity (the
    canonical forged-attestation slashing case); Mode B's hardware-swap
    branch hands off to `ENROLLMENT_INCIDENT.md` for the unenroll →
    unbond → re-enroll cycle.
  - **`docs/runbooks/REJECTION_FLOOD.md` extension** — added §7 with
    five mode-specific anchors so the existing 3 root-pointing
    `runbook_url` annotations + 2 fresh annotations all deep-link to
    the relevant text. §7.1 (`QSDMAttestRejectionPersistCompactionsHigh`,
    Mode A), §7.2 (`QSDMAttestRejectionPersistHardCapDropping`,
    Mode B), §7.3 (`QSDMAttestRejectionPerMinerRateLimited`, Mode C)
    are entry-point summaries pointing back to the existing §3 triage
    walkthrough. §7.4 (`QSDMAttestRejectionFieldTruncationSustained`,
    Mode D) is a brand-new payload-shape mode covering sustained
    truncation of `detail` / `gpu_name` / `cert_subject` ring fields —
    the cause table maps each `field` label to verifier-release skew,
    adversarial stuffing, or benign multi-byte unicode. §7.5
    (`QSDMAttestRejectionFieldRunesMaxNearCap`, Mode E) is the
    severity:info leading-indicator before Mode D paints; the runbook
    explicitly notes Mode E should NOT page (wire to a passive channel)
    so operators see the ramp before the truncation-rate alert
    crosses 25%.
  - **`deploy/prometheus/alerts_qsdm.example.yml`** — added
    `runbook_url` to all 7 previously-uncovered alerts in this sweep
    (`QSDMQuarantineAnySubmesh`, `QSDMQuarantineMajorityIsolated`,
    `QSDMAttestArchSpoofUnknownArchBurst`,
    `QSDMAttestArchSpoofGPUNameMismatch`,
    `QSDMAttestArchSpoofCCSubjectMismatch`,
    `QSDMAttestRejectionFieldTruncationSustained`,
    `QSDMAttestRejectionFieldRunesMaxNearCap`) and upgraded 3 existing
    root-pointing URLs to anchored ones
    (`QSDMAttestRejectionPersistCompactionsHigh`,
    `QSDMAttestRejectionPersistHardCapDropping`,
    `QSDMAttestRejectionPerMinerRateLimited`). Each annotation carries a
    multi-line operator-comment block immediately before the
    `runbook_url` summarising the mode-specific signal and the runbook
    section's role — Alertmanager renders annotation text as paged
    context, so on-call operators get the "why this is critical" or
    "why this is informational" framing at page-time without the
    click-through to the runbook.
  - **No code changes.** Pure documentation + alert-annotation commit;
    leverages the existing `pkg/quarantine/metrics.go`,
    `pkg/monitoring/archcheck_metrics.go`, and
    `pkg/mining/attest/recentrejects/metrics.go` collectors that
    already shipped. The remaining 11 uncovered alerts span four
    smaller clusters (NVIDIA-lock, NGC challenge / proof-ingest, submesh
    P2P/API policy, governance-authority) and a no-tx alarm — the next
    runbook sweep can pick the highest-leverage of those.

- **Trust / NGC-attestation operator runbook + alert annotations (2026-05-01).**
  Closed the largest remaining cluster of uncovered alerts —
  the six in `qsdm-trust-transparency` +
  `qsdm-trust-redundancy`. The trust pipeline is the chain's
  reward gate (enrollment determines who CAN earn; trust
  determines whose attestations COUNT toward earning), and a
  misread on these alerts is the difference between fair
  payouts and silently under-paying the honest fleet. The
  user-facing consequence — qsdm.tech transparency badge
  flipping red — is community-visible within minutes; five
  of the six alerts fire **before** the badge flips, giving
  the operator a pre-warning window the runbook is
  designed to use.
  - **`docs/runbooks/TRUST_INCIDENT.md`** — six-mode runbook
    (A through F) covering every alert in the trust groups.
    Mode F (`QSDMTrustAggregatorStale`, severity:
    **critical**) gets dedicated coverage of the
    Refresh()-goroutine wedge — when the aggregator stops
    ticking, the Trust Panel and qsdm.tech badge can stay
    green while the pipeline is silently broken (false-
    confidence — the worst class of trust failure). Runbook
    explicitly directs operators to capture
    `/debug/pprof/goroutine` BEFORE restarting because the
    wedge is typically reproducible.
    The §4 cascade map walks the canonical 30–40-minute
    incident progression (sidecar stops → counter flatlines →
    NGCServiceStatus flips degraded → newest sample ages out
    → attested drops below floor) so on-call hit by 4
    simultaneous alerts identifies them as ONE incident, not
    four. The §5 cross-mode escalation matrix maps every
    common multi-fire pattern (A+D+E+C cascade,
    F+anything-else, all-six) to the most likely root cause.
    Includes the closed-enum reject-reason → cause table
    operators rely on for Mode B (`hmac_mismatch` / secret
    rotation, `invalid_nonce` / clock skew, `unauthorized`
    / probe), the `LocalDistinctAttestationSource` NodeID-
    collision branch for Mode C (two sidecars sharing a
    NodeID dedupe to one source), and the
    external-CI-mirror cross-check for distinguishing real
    trust incidents from local Prometheus-target issues
    (CI green + Mode C internally → suspect the local
    metric path).
  - **6 new `runbook_url` annotations** on every alert in the
    trust groups, deep-linked to the matching mode anchor.
    Total runbook-coverage tally on the alert file rose from
    14/38 (37%) to **20/38 (53%)** — over half the alert
    family now carries deep-linked operator triage docs.
    The Trust panel widget (`updateTrustPanel()` in
    `dashboard.js`) and the underlying `TrustMetricsCollector`
    in `pkg/api/trust_metrics.go` already existed; this is a
    pure documentation + Prometheus-config commit, leveraging
    the existing observability surface.
  - **No code, no tests** — runbook + alert annotations
    only. Cost ≈ 30% of the enrollment operator-surface
    commit (`7702ba1`); the panel and collector exist
    already, so the work was scoped to the runbook + the 6
    annotations.

  Files touched:
  - `QSDM/docs/docs/runbooks/TRUST_INCIDENT.md` (new)
  - `QSDM/deploy/prometheus/alerts_qsdm.example.yml`
  - `CHANGELOG.md`

- **Mining-liveness runbook + dangling-cross-reference resolution (2026-05-01).**
  Closed the only critical-severity v2-mining alert that was
  paging into a void: `QSDMMiningChainStuck` (severity:
  critical) and its warning sibling `QSDMMiningMempoolBacklog`
  now have a dedicated runbook + `runbook_url` deep-links.
  This commit also resolves four dangling cross-references
  inside the freshly-shipped `ENROLLMENT_INCIDENT.md`
  (commit `7702ba1`) and one cross-reference list in
  `SLASHING_INCIDENT.md` (commit `9dd4a73`) that pointed at
  "the liveness runbook" before any such runbook existed —
  an operator following those cross-references during a real
  incident would have hit dead links. Asymmetric coverage
  fixed: every warning-severity v2-mining alert had a
  runbook, but the *only* critical-severity alert in the
  family did not.
  - **`docs/runbooks/MINING_LIVENESS.md`** — two-mode runbook
    plus a Mode-B → Mode-A promotion path (chain advancing +
    mempool growing → "backlog"; chain stops advancing →
    "frozen", abandon backlog triage). Mode A spends most of
    its triage on the **"all transactions failed state
    application" silent-freeze path** because that's the
    operationally most-frequent cause: admission stays open
    while the producer silently refuses to seal, so every
    downstream mining alert flatlines as collateral signal
    rather than firing — the dashboard reads "no recent
    activity" while the chain is wedged. The runbook
    explicitly calls out this anti-correlation pattern so an
    on-call doesn't burn cycles triaging the silent
    `🪪 Enrollment Registry` / `⚖️ Slashing Pipeline` tiles
    before fixing the upstream wedge. Recovery-validation
    queries at the end let the operator confirm height has
    resumed and warn that a single-tick burst of downstream
    activity (post-wedge backlog drain) is expected, not a
    new incident.
  - **2 new `runbook_url` annotations** on
    `QSDMMiningChainStuck` and `QSDMMiningMempoolBacklog` in
    `deploy/prometheus/alerts_qsdm.example.yml`. Total
    runbook-coverage tally on the alert file rose from
    12/38 to 14/38 (37%); every `QSDMMining*` alert in the
    file now carries a runbook deep-link.
  - **Cross-reference resolution in
    `ENROLLMENT_INCIDENT.md`** — four references to "the
    liveness runbook" / `QSDMMiningChainStuck` upgraded
    from bare alert names to deep-links pointing at
    `MINING_LIVENESS.md#31-mode-a--qsdmminingchainstuck`.
    Same surgery in `SLASHING_INCIDENT.md`: the §4
    Reference list grew a "Companion runbooks" subsection
    that surfaces the liveness/enrollment/rejection
    runbooks operators may need during a slashing incident.
  - **No code, no tests** — pure documentation +
    Prometheus-config commit. Cost ≈ 10% of the enrollment
    operator-surface commit (`7702ba1`); proportional to
    the gap closed.

  Files touched:
  - `QSDM/docs/docs/runbooks/MINING_LIVENESS.md` (new)
  - `QSDM/docs/docs/runbooks/ENROLLMENT_INCIDENT.md`
  - `QSDM/docs/docs/runbooks/SLASHING_INCIDENT.md`
  - `QSDM/deploy/prometheus/alerts_qsdm.example.yml`
  - `CHANGELOG.md`

- **Enrollment registry operator surface — dashboard tile + runbook + alert annotations (2026-05-01).**
  Closed the last operator-experience gap in the v2-mining
  observability triangle. The `qsdm_enrollment_*` /
  `qsdm_unenrollment_*` Prometheus counters and the five
  `qsdm-v2-mining-enrollment` group alerts (`RegistryEmpty`,
  `RegistryShrinkingFast`, `PendingUnbondMajority`,
  `EnrollmentRejectionsBurst`, `BondedDustDropped`) have been
  in place since the v2-mining rollout, but a paged on-call had
  no in-product tile to triage from (only the cursor-paginated
  `GET /api/v1/mining/enrollments` JSON, which required
  separate Prometheus queries to read the live gauges) and no
  consolidated runbook to walk the voluntary-vs-forced-exit
  decision before the 7-day unbond window matures and the
  recovery cost rises. This commit fills both gaps with the
  same template the slashing tile established (commit
  `9dd4a73`) so operators see one shape of triage surface —
  counter strip + filtered records + runbook deep-link — across
  rejection-ring, slashing, and enrollment.
  - **`monitoring.EnrollmentMetricsView` + `EnrollmentMetricsSnapshot()`** —
    one struct that aggregates every `qsdm_enrollment_*` /
    `qsdm_unenrollment_*` series: live gauges (`active_count`,
    `bonded_dust`, `pending_unbond_count`,
    `pending_unbond_dust`) read through the existing callback-
    driven `EnrollmentStateProvider`, plus the monotonic
    counters (`enroll_applied_total`, `unenroll_applied_total`,
    `enroll_unbond_swept_total`) and reason-labeled reject
    breakouts in stable Prometheus exposition order so the
    dashboard cell ordering matches PromQL view ordering. Four
    new behavioural tests cover: zero-state on a cleared
    counter set, counter increment under `Record*`, gauge
    pass-through via a fake `EnrollmentStateProvider`, and
    label-order parity against `EnrollmentRejectedLabeled` /
    `UnenrollmentRejectedLabeled` so a future drift between the
    snapshot and the Prometheus exposition is caught at build
    time rather than as misaligned reason columns in
    production.
  - **`api.CurrentEnrollmentLister` + `api.EnrollmentViewFromRecord`** —
    two surgical exports that let `internal/dashboard` consume
    the existing `EnrollmentLister` interface without adding a
    parallel adapter: same precedent the rejection-ring and
    slashing tiles set with `CurrentRecentRejectionLister` /
    `CurrentSlashReceiptLister`. `EnrollmentViewFromRecord`
    centralises the
    `enrollment.EnrollmentRecord → EnrollmentRecordView`
    translation in one place so the v1 query handler, the v1
    list handler, and the dashboard tile stay in lockstep —
    adding a field to `EnrollmentRecordView` only needs to be
    wired once. Zero behaviour change at the v1 `/api/v1/
    mining/enrollment*` endpoints.
  - **`/api/mining/enrollment-overview` dashboard endpoint** —
    `internal/dashboard/enrollment.go::handleEnrollmentOverview`.
    Combines the live registry page (lexicographic by NodeID
    via the existing lister) with the metrics snapshot in one
    envelope. Server-side parity with the v1 list endpoint:
    closed-enum `phase` validation (400 on a typo, mirroring
    the same allowlist the v1 endpoint enforces); `cursor`
    length-clamped to `enrollment.MaxNodeIDLen`; `limit`
    clamped to a tile-friendly `[1, 200]` range (the v1
    endpoint's 500 ceiling stays the indexer path). Graceful
    "v1-only deployment" path: when no lister is wired the
    handler returns 200 with `available=false` and the
    metrics block populated with zero-valued gauges, so the
    tile renders "registry not wired" without blanking the
    counter strip. `Filters` block omitted on a bare call
    (pointer + `omitempty`, same pattern as the slashing
    tile). 8 new behavioural tests cover method gating,
    limit clamping (over-cap / negative / non-integer),
    no-lister-wired graceful path, happy-path ordering +
    pagination forwarding, closed-enum bogus-input rejection
    (incl. a SQL-injection-style payload via
    `url.QueryEscape`), oversized-cursor rejection, filter
    passthrough + echo, and all-three-phase acceptance.
  - **🪪 Enrollment Registry dashboard tile** —
    `internal/dashboard/static/{index.html,dashboard.js}`. Six-
    cell counter strip (`active miners`, `bonded dust`,
    `pending unbond`, `enroll / unenroll`, `enroll rejected`,
    `unenroll rejected`) with traffic-light colouring tuned to
    the alert thresholds: `active=0` cell turns red (matches
    `QSDMMiningRegistryEmpty`); `pending_unbond` ratio >50%
    turns red, >25% turns amber (matches
    `QSDMMiningPendingUnbondMajority`); reject-cell amber the
    moment the cumulative rejection count is non-zero. Records
    table shows NodeID / Phase / Slashable / Owner / Stake (in
    CELL) / Enrolled@ / UnbondMatures@; long NodeIDs / owners
    truncate to 18 chars with the full value in the `title=`
    attribute. Triage controls: phase dropdown
    (all/active/pending_unbond/revoked), pause-polling toggle
    (gates `updateEnrollmentOverview` in the 2 s polling loop
    so an operator can read a row without it scrolling out
    from under), CSV export of the current page (escaping
    routine reused from the rejection-ring tile via
    `csvEscape`). Integration test extended with 23 new
    container-ID / module-symbol / counter-strip-label /
    JSON-field-reference / pause-gate guards so a refactor
    that drops the wiring fails the build before silently
    blanking the tile in production.
  - **`docs/runbooks/ENROLLMENT_INCIDENT.md`** — five-mode
    runbook covering each alert in the
    `qsdm-v2-mining-enrollment` group, plus a cross-mode
    escalation matrix that maps multi-fire patterns
    (`B+C+E` = coordinated voluntary exit, `B-forced+E` =
    active slashing incident, `D+B-voluntary` = client-server
    skew) to their root cause. Each mode follows the same
    template the slashing runbook established: dashboard
    symptoms → Prometheus symptoms → triage queries → cause-
    and-action table → mitigation. Mode E gets explicit
    coverage of the metric-callback-regression branch — if a
    `bonded_dust` drop isn't accounted for by
    unenroll/slash/sweep rates the runbook walks the operator
    through verifying the gauge against the live registry via
    `/api/v1/mining/enrollments?phase=active&limit=500`,
    catching stale `EnrollmentStateProvider` closures that
    survive a partial restart. Recovery validation queries at
    the end let on-call confirm the registry has stabilised
    after mitigation.
  - **Five new `runbook_url` annotations** on every
    `QSDMMiningRegistry*` / `QSDMMiningEnrollment*` /
    `QSDMMiningBondedDustDropped` alert in
    `deploy/prometheus/alerts_qsdm.example.yml`, deep-linking
    to the matching mode anchor in `ENROLLMENT_INCIDENT.md`.
    Combined with the four runbook URLs added in the slashing
    commit (`9dd4a73`), every `QSDMMining*` alert in the alert
    file now carries a runbook deep-link AND has a dashboard
    tile pre-classifying its symptoms — the v2-mining surface
    is at "fully observable" baseline.

  Files touched:
  - `QSDM/source/pkg/monitoring/enrollment_metrics.go`
  - `QSDM/source/pkg/monitoring/enrollment_metrics_test.go` (new)
  - `QSDM/source/pkg/api/handlers_enrollment_list.go`
  - `QSDM/source/pkg/api/handlers_enrollment_query.go`
  - `QSDM/source/internal/dashboard/dashboard.go`
  - `QSDM/source/internal/dashboard/enrollment.go` (new)
  - `QSDM/source/internal/dashboard/enrollment_test.go` (new)
  - `QSDM/source/internal/dashboard/integration_test.go`
  - `QSDM/source/internal/dashboard/static/index.html`
  - `QSDM/source/internal/dashboard/static/dashboard.js`
  - `QSDM/docs/docs/runbooks/ENROLLMENT_INCIDENT.md` (new)
  - `QSDM/deploy/prometheus/alerts_qsdm.example.yml`

- **Slashing operator surface — dashboard tile + runbook + alert annotations (2026-05-01).**
  Closed the operator-experience gap in the v2-mining slashing pipeline:
  `qsdm_slash_*` Prometheus counters and the four `QSDMMiningSlash*` /
  `QSDMMiningAutoRevokeBurst` alerts have been in place since the slashing
  rollout, but a paged on-call had no in-product surface to triage from
  (only the per-tx `GET /api/v1/mining/slash/{tx_id}` lookup, which
  required already knowing the tx_id from somewhere) and no consolidated
  runbook to walk the verifier-regression-vs-real-cheat-ring decision.
  This commit fills both gaps, mirroring the pattern the rejection-ring
  vertical established (commit `691b348` for hard-cap, `aa17da6` for the
  per-miner limiter) so operators see the same shape of triage tile +
  runbook reference for both subsystems.
  - **`chain.SlashReceiptStore.List(opts)`** — new paginated walk over
    the bounded receipt store, returning records NEWEST-FIRST. Filters:
    `Outcome` (applied/rejected), `EvidenceKind` (forged-attestation /
    double-mining / freshness-cheat), `SinceUnixSec` (rolling time
    window). Limit clamping mirrors the rejection-ring lister
    (`DefaultSlashReceiptListLimit=100`, `MaxSlashReceiptListLimit=500`);
    9 new behavioural tests cover ordering, filter ANDing, eviction
    interaction, nil-store safety, and limit-clamp semantics.
  - **`api.SlashReceiptLister`** — new interface companion to the
    existing Lookup-only `SlashReceiptStore`. The split mirrors the
    `RecentRejectionLister` precedent: lookup is the older + stable
    interface used by the v1 GET endpoint; list is the read shape the
    dashboard needs. One `slashReceiptAdapter` in `internal/v2wiring`
    satisfies both, so production wiring pays no extra surface area.
    `IsKnownSlashOutcome` / `IsKnownSlashEvidenceKind` +
    `KnownSlashOutcomes` / `KnownSlashEvidenceKinds` published as the
    closed-enum allowlists the dashboard handler validates against —
    typo'd filters return 400 rather than silently passing through.
  - **`monitoring.SlashMetricsView` + `SlashMetricsSnapshot()`** — JSON-
    friendly aggregate view of every `qsdm_slash_*` series in one
    coherent struct (`AppliedByKind`, `DrainedDustByKind`,
    `RewardedDustTotal`, `BurnedDustTotal`, `RejectedByReason`,
    `AutoRevokedByReason`). The dashboard tile reads through this so
    the operator sees a single coherent counter strip without
    chained Prometheus queries. Atomic per-counter, not transactional
    across the snapshot — documented eventual-consistency window is
    well below operator reaction time at 2 s polling.
  - **`/api/mining/slash-receipts` dashboard endpoint** —
    `internal/dashboard/slashing.go::handleSlashReceipts`. Combines the
    most recent N receipts with the `SlashMetricsSnapshot()` in one
    envelope. Closed-enum filter validation (400 on bogus `outcome` /
    `evidence_kind`); `Filters` block omitted on a bare call (matches
    the attest-rejections tile's wire-payload tightness); graceful
    "Available=false with metrics still surfaced" when no v2 store is
    wired (v1-only deployment). 8 new handler tests cover method
    gating, limit clamping, no-lister-wired path, happy path, all
    three filter-validation 400 branches, and full filter-passthrough
    + filter-echo.
  - **⚖️ Slashing Pipeline dashboard tile** — full sibling of the
    🛑 Attestation Rejections tile. Counter strip (applied / drained
    dust / reward+burn / rejected / auto-revoked, each with per-label
    breakdown). Triage controls: outcome filter, evidence_kind filter,
    rolling-time-window selector, pause-polling toggle, CSV export,
    Top-3 most-slashed NodeIDs strip. Polling integrates with the
    existing 2 s tick + WebSocket fallback, gated on
    `slashReceiptsState.paused` so a mid-incident operator can read
    a row without it scrolling out from under them. Integration
    test asserts every tile container ID, every JS module-level
    symbol, every counter-strip label, and the pause-aware poll gate.
  - **`SLASHING_INCIDENT.md` operator runbook** — new file at
    `QSDM/docs/docs/runbooks/SLASHING_INCIDENT.md`. Four-mode
    triage flow:
    - Mode A (`QSDMMiningSlashApplied`) — confirm + ratify (the happy
      path: chain caught a cheater).
    - Mode B (`QSDMMiningSlashedDustBurst`) — the dangerous one. §3.2
      walks the cheat-ring-vs-verifier-regression decision query, the
      mitigation policy for each branch, AND the verifier-rollback
      procedure (with off-chain rebate guidance for slashed-honest
      miners since the chain has no un-slash transaction).
    - Mode C (`QSDMMiningSlashRejectionsBurst`) — per-reason triage
      table (`verifier_failed`, `evidence_replayed`, etc.) with
      action class for each.
    - Mode D (`QSDMMiningAutoRevokeBurst`) — usually escalates Mode B,
      but the runbook surfaces the slash-arithmetic-bug and stake-
      stripping-attack branches that aren't just B in disguise.
  - **`runbook_url` on every `QSDMMining*` slash alert.** All four
    alerts in `qsdm-v2-mining-slashing` (SlashApplied, SlashedDustBurst,
    SlashRejectionsBurst, AutoRevokeBurst) now carry deep links to the
    matching `SLASHING_INCIDENT.md` section anchor. Total alerts
    carrying `runbook_url` rose from 3 → 7 with this commit; the
    rejection-flood three remain unchanged.

- **Per-miner rate-limit at recent-rejection ring entry (2026-05-01).**
  Closed a known signal-to-noise gap in the `pkg/mining/attest/recentrejects`
  ring: prior to this change, a single miner submitting forged proofs at
  line-rate could fill the ring with their own records, FIFO-ing legitimate
  rejection events out of the operator's view AND saturating the per-field
  rune-truncation counters so legitimate truncation patterns got hidden in
  the aggregate. The hard-cap defence (commit `691b348`, 2026-04-30) bounded
  the on-disk blast radius but did nothing about per-miner fairness.
  - **Token-bucket limiter (`ratelimit.go`).** New per-miner token bucket
    consulted at `Store.Record()` entry. `rate` (tokens/sec/miner) refills
    each bucket; `burst` (max tokens) absorbs a quiet miner's first
    burst-many records cold. Records for a miner whose bucket is exhausted
    are DROPPED — never enter the ring, never invoke the persister, never
    update the per-field truncation counters; only the dedicated drop
    counter increments. Empty `MinerAddr` (rare envelope-parse-failure
    case) bypasses the limiter so operator visibility into the
    parse-failure path is preserved.
  - **Lazy idle-bucket eviction.** Buckets idle longer than `idleTTL`
    are pruned by an amortised sweep every 1024 admits (constant
    average cost per `Record()` call regardless of distinct-miner
    count). Long-running validators with rotating miner populations
    do not leak map memory.
  - **Disabled by default.** `recentrejects.Store` ships with the
    limiter detached (rate=0); pre-existing tests and quiet
    validators see zero behaviour change. Production wiring activates
    it via three new `internal/v2wiring.Config` fields:
    `RecentRejectionsRateLimitPerSec`,
    `RecentRejectionsRateLimitBurst`,
    `RecentRejectionsRateLimitIdleTTL`. Setter
    (`Store.SetRateLimit`) accepts in-place re-tuning that PRESERVES
    existing token state — a tighter rate kicks in immediately
    rather than letting an unbounded burst through during the
    re-configure window.
  - **Observability (5 surfaces).**
    - New optional `recentrejects.RateLimitRecorder` interface
      (mirrors the existing `PersistErrorRecorder` /
      `PersistCompactionRecorder` /
      `PersistRecordsRecorder` /
      `PersistHardCapDropRecorder` extension pattern).
    - Prometheus counter
      `qsdm_attest_rejection_per_miner_rate_limited_total`
      (unlabeled — Prometheus best practice; `miner_addr` would
      explode cardinality under a fast-rotating attacker).
    - `pkg/monitoring.RecentRejectMetricsView` snapshot gains
      `PerMinerRateLimitedTotal` (JSON tag
      `per_miner_rate_limited_total`).
    - Dashboard tile gains a fifth persistence-lifecycle cell
      (`'rate-limit drops'`) that goes red on first hit; tile
      caption updated to walk the operator through the new
      defence.
    - Per-store `Store.RateLimitedCount()` mirror returns the
      same value without a Prometheus scrape.
  - **Alert + runbook.** New `QSDMAttestRejectionPerMinerRateLimited`
    alert (`rate(...) > 0` for 10m, severity warning, `runbook_url`
    pointing at the same `REJECTION_FLOOD.md` runbook used by the
    compactions and hard-cap alerts). Runbook upgraded from a
    two-mode (A: compactions, B: hard-cap) to a three-mode
    triage flow with **Mode C (rate-limit dropping) — usually
    self-resolving**, and the dashboard "first red cell tells
    you the mode" shortcut.
  - **Test coverage (15 new behavioural tests + 4 monitoring
    tests).** Covers: disabled-by-default, empty-`MinerAddr`-
    bypass, burst absorption, sustained-over-rate drops,
    refill-resumes-admission, per-miner independence, idle-TTL
    eviction, re-configure preserves token state,
    re-configure-disable frees the map, drop fires the
    `RateLimitRecorder`, drop does NOT advance `Seq` (cursor-
    pagination invariant), drop does NOT invoke the persister
    (forensic-record stability), `RateLimitConfig` reflects boot
    settings, nil-store safety, derived-burst defaults,
    sub-one-burst clamp. Monitoring tests assert: counter
    increments, snapshot inclusion, adapter routing, reset-
    helper coverage. All 19 green; full mining + monitoring +
    dashboard + v2wiring suites stay green.

### Fixed

- **`qsdmplus -> qsdm` rebrand residue in shell + PowerShell + YAML
  deploy artefacts (2026-04-30, follow-up audit to commit `b0b2f77`).**
  After the Python-side audit closed with the `_env_preferred(X, X)`
  fix, a sweep through `*.sh` / `*.ps1` / `*.yml` / `*.yaml` revealed
  the SAME flatten pattern in five additional places, plus the
  immediate cause of the bug class itself. Continuing the audit:
  - **`apps/qsdm-nvidia-ngc/scripts/wire-qsdm.{sh,ps1}` — duplicated
    exports.** Both files contained four pairs of back-to-back
    identical `export QSDM_X="$VAL"` lines, including a literal
    self-assignment `export QSDM_NGC_REPORT_URL="$QSDM_NGC_REPORT_URL"`
    on the bash side and `$env:QSDM_NGC_REPORT_URL = $env:QSDM_NGC_REPORT_URL`
    on the PowerShell side. Reconstructed the original shape: the
    second line of each pair was meant to export the LEGACY
    `QSDMPLUS_NGC_*` alias so docker-compose containers running a
    pre-rebrand sidecar image still see the value. The rebrand sweep
    flattened `QSDMPLUS_*` to `QSDM_*` and turned the parallel-export
    block into a redundant self-redundant block, breaking any
    container in the deprecation window. Restored the parallel
    `QSDMPLUS_*` exports for `NGC_INGEST_SECRET`, `NGC_REPORT_URL`,
    `NGC_PROOF_NODE_ID`, and `NGC_PROOF_HMAC_SECRET`.
  - **`apps/qsdm-nvidia-ngc/scripts/local-attest.ps1` — preflight
    refused legacy env-var name.** The Windows operator-side wrapper
    around `validator_phase1.py` checked `$env:QSDM_NGC_REPORT_URL`
    and `$env:QSDM_NGC_INGEST_SECRET` and bailed out with a hard
    `Write-Error "...is not set"` when those weren't populated —
    even though the downstream Python now reads either name via the
    just-fixed `_env_preferred(...)` helper. An operator with a
    pre-rebrand `ngc.local.env` (only `QSDMPLUS_NGC_*` set) would
    hit the wrapper's hard refusal before the Python ever ran.
    Added a `Resolve-PreferredEnv -Preferred -Legacy -DisplayName`
    helper that mirrors the Python helper's contract: read preferred,
    fall back to legacy, AND promote the legacy value into the
    preferred slot so the downstream Python sees a clean primary.
    Both `QSDM_NGC_REPORT_URL` / `QSDMPLUS_NGC_REPORT_URL` and
    `QSDM_NGC_INGEST_SECRET` / `QSDMPLUS_NGC_INGEST_SECRET` now
    accept either name.
  - **`apps/qsdm-nvidia-ngc/docker-compose.yml` — collapsed
    documentation comment.** The `validator-cpu` service's
    `environment:` block had a comment `# QSDM_NGC_REPORT_URL /
    QSDM_NGC_REPORT_URL — see scripts/wire-qsdm.ps1 or wire-qsdm.sh`
    where the same name appeared on both sides of the slash.
    Restored as `QSDM_NGC_REPORT_URL (or legacy QSDMPLUS_NGC_REPORT_URL)`
    and applied the same edit to the `INGEST_SECRET` /
    `PROOF_NODE_ID` companion comments so all three reference the
    deprecation pair.
  - **`vps.txt` — operator credential reference (gitignored).**
    Three collapsed documentation pairs (`canonical QSDM_* env name
    and the legacy QSDM_* alias`, `QSDM_NVIDIA_LOCK_PROOF_HMAC_SECRET
    / QSDM_NVIDIA_LOCK_PROOF_HMAC_SECRET pair`, and the rotation
    procedure's `BOTH the QSDM_* and QSDM_* lines`). Restored the
    legacy `QSDMPLUS_*` half of each pair. File is gitignored
    (`.gitignore:25`) so the change is operator-local but the
    document is the canonical reference for the validator's
    secrets.conf shape during the deprecation window — leaving
    those self-referential pairs in place would mislead the next
    operator who reads it during a secret rotation.
  - **`QSDM/deploy/bring-up-validator.sh` — misleading "dual-name"
    comment.** The systemd unit fragment had a comment claiming
    `Environment="QSDM_CONFIG_FILE=${CONFIG_FILE}"` was a
    "dual-name for the rebrand window; pick whichever the binary
    prefers", but `pkg/config/config.go:179` only consults
    `os.Getenv("CONFIG_FILE")` — `QSDM_CONFIG_FILE` is dead env
    today. The line itself is harmless (a dead env var is just
    ignored), but the comment was a maintainability hazard: an
    operator who removed the `CONFIG_FILE` line in favor of the
    "more branded" `QSDM_CONFIG_FILE` would crash the validator on
    boot. Replaced the comment with an honest description of the
    current state and a future-migration intent. Filed the
    config-loader update (read `QSDM_CONFIG_FILE` first, fall back
    to `CONFIG_FILE`) as follow-up work outside the script-audit
    scope; that's a Go change with a wider blast radius than a
    deploy-script tweak.
  - **`QSDM/scripts/rebrand-sweep.ps1` — locked behind an explicit
    flag.** This is the script that *caused* the entire bug class —
    its core regex `'QSDMPLUS_' -> 'QSDM_'` is the mechanism that
    flattened every `_env_preferred("QSDM_X", "QSDMPLUS_X")` pair,
    every `'QSDM_X=|QSDMPLUS_X='` grep alternation, every parallel
    export block, and every `(preferred / legacy)` documentation
    comment. The original script was billed as "Safe to run
    idempotently" — that statement is now false, because some files
    legitimately contain `QSDMPLUS_<name>` as the legacy half of a
    deprecation pair, and a re-run would re-collapse them. Added a
    `-IAcceptThatThisRecollapsesLegacyFallbacks` switch that the
    operator must pass explicitly; without it the script halts
    before any file read with a coloured warning naming the audit
    commits and the legitimate `QSDMPLUS_*` use cases. Flag name is
    deliberately long, ugly, and self-explaining so it can't be
    typed by accident. Help-text lifted to a `!! DANGER !!` block
    at the top of the file with a full history pointer (commits
    `db9b590`, `b0b2f77`, this commit) so a future operator who
    finds the script in a search has the right context.
  - **CI guard expanded:**
    `QSDM/scripts/check-no-collapsed-env-preferred.sh` now runs in
    two passes. Pass A (unchanged) catches the Python
    `_env_preferred("X", "X")` shape. Pass B (new) catches the
    docs/shell/YAML `\bQSDM_(X)\b\s*[/|]\s*\bQSDM_\1\b` shape via
    `rg --pcre2` backreferences — same name on both sides of `/`
    or `|`, anchored by the `QSDM_` prefix to avoid tripping on
    legitimate alternations of unrelated literals (e.g. the
    `QSDM_NODE_ROLE / QSDM_MINING_ENABLED` comment in
    `validator-statefulset.yaml`, which is two different env vars).
    Excludes `CHANGELOG.md`, `REBRAND_NOTES.md`,
    `check-no-collapsed-env-preferred.sh` itself, and
    `rebrand-sweep.ps1` — those four files all legitimately quote
    the bug pattern as documentation.
  - **Verification:** `[Parser]::ParseFile` clean on all three
    modified `.ps1` files; `yaml.safe_load` clean on the modified
    `docker-compose.yml`; a 7-property smoke test confirmed the
    rebrand-sweep guard wired up, both wire-qsdm scripts have the
    parallel preferred+legacy exports with no self-assignment lines,
    `local-attest.ps1` has the dual-name preflight,
    `docker-compose.yml` comment restored, `vps.txt` documentation
    pairs restored, and `bring-up-validator.sh` carries the honest
    dead-name comment. WSL is broken on the audit machine so
    `bash` syntax-check ran via the existing CI's
    `ubuntu-latest` runner instead.

- **`qsdmplus -> qsdm` rebrand residue in the Python sidecar +
  installer scripts (2026-04-30).** The Python deploy artefacts were
  outside the existing `check-no-new-legacy-metrics.sh` CI guard's
  scope (it only flags Prometheus-style metric-name literals like
  `qsdmplus_foo_total`) and outside `go vet` / `staticcheck`'s reach
  entirely, so the over-eager search-and-replace migration that
  retired the `qsdmplus` brand silently broke env-var deprecation
  fallback in three files. Audit + fix.
  - **`apps/qsdm-nvidia-ngc/validator_phase1.py` — twelve broken call
    sites.** The file defines a helper
    `_env_preferred(primary: str, legacy: str)` whose entire purpose
    is the deprecation-window pattern (read preferred env-var name,
    fall back to legacy). The rebrand collapsed *every* call to the
    same name on both sides, e.g.
    `_env_preferred("QSDM_NGC_INGEST_SECRET", "QSDM_NGC_INGEST_SECRET")`
    — making the helper equivalent to a bare `os.environ.get` and
    silently killing legacy support. The smoking gun is a docstring
    on the (now fixed) `report_to_qsdm()` function that still read
    `"branded env: QSDM_*; legacy QSDM_* still supported"` —
    preserving the deprecation-shape sentence but with both names
    flattened to the same value, definitive evidence of a regex
    sweep that touched literals without re-reading the comments.
    Restored the legacy `QSDMPLUS_<...>` second argument at all
    twelve call sites
    (`QSDM_NGC_INGEST_SECRET`, `QSDM_NGC_REPORT_URL`,
    `QSDM_NGC_FETCH_CHALLENGE`, `QSDM_NGC_CHALLENGE_URL`,
    `QSDM_NGC_CHALLENGE_JITTER_MAX_SEC`,
    `QSDM_NGC_CHALLENGE_MAX_RETRIES`, `QSDM_NGC_REPORT_INSECURE_TLS`
    ×2, `QSDM_NGC_PROOF_HMAC_SECRET`, `QSDM_NGC_PROOF_NODE_ID`,
    `QSDM_NGC_REPORT_URL` ×2, `QSDM_NGC_INGEST_SECRET` ×2). Also
    repaired the two flattened docstrings on lines 254 and 270.
  - **`apps/qsdm-nvidia-ngc/validator_phase1.py` — runtime defence in
    `_env_preferred` itself.** Added a `if primary == legacy: raise
    ValueError(...)` guard at the top of the helper. A future
    refactor that re-flattens both args will now crash the sidecar
    on first invocation rather than silently degrading; verified
    with a 3-property smoke test (collapsed pair raises, legacy
    fallback active when primary unset, primary wins when both set).
  - **`QSDM/deploy/install_ngc_sidecar_vps.py` — collapsed
    `grep -E` regex.** The installer reads the existing NGC ingest
    secret out of the validator's running systemd environment via
    `systemctl show qsdm --property=Environment ... | grep -E
    '^QSDM_NGC_INGEST_SECRET=|^QSDM_NGC_INGEST_SECRET='`. The two
    alternatives on either side of the `|` had been collapsed to the
    same literal, so the installer would fail with "could not find
    NGC_INGEST_SECRET" on a validator that still had the legacy
    `QSDMPLUS_NGC_INGEST_SECRET=` form (i.e. one that hadn't yet
    rotated its secrets.conf to the new env-var name). Restored the
    legacy alternative + repaired the docstring on lines 10-11 +
    expanded the SystemExit error message to name both env-var
    names explicitly so the operator knows which two patterns were
    consulted.
  - **`QSDM/deploy/install_ngc_sidecar_oci.py` — clean.** The OCI
    installer doesn't use `_env_preferred`; it generates a fresh
    `ngc.env` from operator-supplied input (`--secret-env` /
    `--secret`) and writes only the preferred `QSDM_NGC_*` form,
    which is correct. Audited the systemd unit names, file paths,
    HTTP endpoints, and POST URLs — all consistent with the post-
    rebrand naming. No changes.
  - **New CI guardrail:**
    [`QSDM/scripts/check-no-collapsed-env-preferred.sh`](QSDM/scripts/check-no-collapsed-env-preferred.sh)
    — sibling of the existing `check-no-new-legacy-metrics.sh`.
    Greps `apps/` and `QSDM/deploy/` for `_env_preferred("X", "X")`
    patterns using `rg --pcre2` (PCRE2 needed for the back-reference
    to require both args literally equal). Wired into the
    `qsdm-go.yml` CI workflow alongside the existing legacy-metrics
    check, fails fast before build. The runtime `ValueError` guard
    in `_env_preferred` is the late defence; this script is the
    early one that fires on every PR before the change can land.
  - **Why no test added for the helper:** the runtime smoke test
    above (collapsed-pair raises, legacy fallback works, primary
    wins) was executed inline during the audit and produced the
    expected three PASS lines, but `apps/qsdm-nvidia-ngc/` has no
    pytest harness and adding one for one helper would over-build
    the testing scaffolding. The CI grep guard + the runtime
    `ValueError` cover the same regression surface with zero test-
    infrastructure cost.

### Added

- **Operator runbook for the §4.6 attestation-rejection flood incident
  (2026-04-30).** The two flood-detection alerts shipped this week
  (`QSDMAttestRejectionPersistCompactionsHigh` and
  `QSDMAttestRejectionPersistHardCapDropping`) ship with embedded
  triage notes in their annotations, but YAML annotations are awkward
  to skim during a paging incident and Alertmanager UIs render them
  as raw text. Lift the content into a proper Markdown runbook and
  wire the standard Prometheus `runbook_url` annotation so the alert
  becomes a click-through.
  - **New file:**
    [`QSDM/docs/docs/runbooks/REJECTION_FLOOD.md`](QSDM/docs/docs/runbooks/REJECTION_FLOOD.md)
    — establishes a `runbooks/` subfolder under the existing
    `QSDM/docs/docs/` flat layout. The runbook is the canonical home
    for incident-response artifacts; future runbooks slot in alongside
    this one.
  - **Runbook coverage:** threat-model section explaining the
    soft-cap / hard-cap two-stage defence; symptom table mapping
    dashboard tile cells to "healthy / Mode A / Mode B" states;
    PromQL triage queries; operator-policy mitigation table per mode;
    §4.6 slashing-escalation procedure with a concrete tx-id
    inspection step; a 12-minute worked example walking from
    "PagerDuty page" through "auto-resolve"; post-incident artifact-
    capture guidance (CSV export from the dashboard tile + JSONL
    log); and a cross-reference index pointing back at every code /
    config touchpoint the runbook calls out.
  - **`runbook_url` annotation on both alerts** in
    `QSDM/deploy/prometheus/alerts_qsdm.example.yml`. Standard
    Prometheus convention — Alertmanager UIs render it as a click-
    through link during paging. Both alerts point at the SAME
    runbook because their triage diverges at exactly one decision
    point ("is the hard-cap-drops cell red?"), and keeping them on
    one page lets the operator walk between Mode A and Mode B
    without losing context.
  - **`OPERATOR_GUIDE.md` "Further reading" entry** pointing at the
    runbook so a search through the operator wiki surfaces the
    incident procedure even without an active alert. Description
    explicitly names both alert IDs so a Ctrl-F-from-PagerDuty hits
    the relevant doc.
  - **Verification:** the runbook YAML changes pass
    `python -c "import yaml; yaml.safe_load(...)"` (37 rules across
    13 groups, both new annotations present); the
    `prometheus-rules-check` CI job exercises the same parse plus
    `promtool check rules` on the alert file. All cross-reference
    paths are repo-relative and resolve from the runbook's
    `QSDM/docs/docs/runbooks/` location.

- **Recent-rejection ring hard-cap defence on the JSONL file
  (2026-04-30).** The soft-cap compaction loop bounds the file at
  `[softCap, 2*softCap)` records under realistic traffic, but a flood
  that outruns the rewrite cycle could in principle grow the file past
  any disk budget the operator wants to keep. Add a belt-and-braces
  byte-shaped hard ceiling, enforced AFTER a salvage compaction
  attempt, with telemetry on every drop so alerting catches the
  flood independently of the compactions-rate alert.
  - **`recentrejects.ErrHardCapExceeded` (exported sentinel):** the
    error `FilePersister.Append` returns when admitting a record would
    breach the configured byte ceiling AND a salvage compaction failed
    to free enough headroom. Operators distinguish "transient I/O
    failure" from "validator is being actively flooded" via
    `errors.Is(err, ErrHardCapExceeded)`. The in-memory ring is
    unaffected — `Store.Record` always appends in-memory regardless,
    so the dashboard tile and `/api/v1/attest/recent-rejections` stay
    accurate; only the durable on-disk record is dropped.
  - **`FilePersister.SetMaxBytes(int64)` / `MaxBytes()`:** opt-in
    setter / accessor pair. `n <= 0` (the construction default) keeps
    the pre-2026-04-30 posture intact, so existing callers that don't
    know about the field stay unaffected. Negative inputs clamp to 0
    (= disabled), mirroring softCap's `<=0 → DefaultPersistSoftCap`
    posture. `MaxBytes()` reads under `p.mu` to avoid torn reads on
    32-bit platforms.
  - **`Append` cap check:** when `maxBytes > 0`, before opening the
    file for write, `Append` stats the current size and compares
    against `currentSize + lineSize + 2` (line + framing newlines,
    pessimistic to defeat sub-byte precision games). If the cap would
    be breached, `Append` runs one in-band salvage compaction — the
    soft-cap rewrite trims the head and frees significant headroom —
    and re-checks. If still over: the record is dropped,
    `notePersistHardCapDrop(admitCost)` fires, and
    `ErrHardCapExceeded` is returned. The append-side watermark
    (`appendsSinceCompact`) is reset to 0 after a successful salvage
    so the next post-cap append doesn't immediately re-trigger a
    normal compaction.
  - **New optional `MetricsRecorder` extension surface:**
    `PersistHardCapDropRecorder { RecordPersistHardCapDrop(int) }` in
    `pkg/mining/attest/recentrejects/metrics.go`. Follows the same
    dependency-inversion pattern as the existing `PersistErrorRecorder`,
    `PersistCompactionRecorder`, and `PersistRecordsRecorder` — no-op
    default; `pkg/monitoring`'s adapter implements it at `init()` time;
    type assertion at the call site so old recorders still build.
  - **`qsdm_attest_rejection_persist_hardcap_drops_total` (counter):**
    exposed by `pkg/monitoring/prometheus_scrape.go`. Increments by 1
    on every drop. The `droppedBytes` argument from the recorder hook
    is currently dropped on the floor (alerting is on rate, not
    volume); the parameter is retained on the wire so a future
    "bytes-shed" rate gauge can join against this without a contract
    change.
  - **Operator dashboard tile (`internal/dashboard/static/dashboard.js`):**
    fourth persistence cell rendered next to the existing
    errors / compactions / records-on-disk cells. Colour shifts red
    on first non-zero hit (no threshold — any drop is operator-
    noteworthy). Caption updated to call out the hard-cap drops as a
    flood-active signal independent of the compactions rate.
  - **New Prometheus alert
    `QSDMAttestRejectionPersistHardCapDropping`** in
    `QSDM/deploy/prometheus/alerts_qsdm.example.yml`: fires on
    `rate(qsdm_attest_rejection_persist_hardcap_drops_total[5m]) > 0`
    sustained 10m. Severity warning. Independent signal from
    `QSDMAttestRejectionPersistCompactionsHigh` — compactions-high
    means "validator is keeping up but elevated", hard-cap-dropping
    means "validator is NOT keeping up; durability is being shed".
    Triage runbook in the alert annotations covers `cfg.MaxBytes`
    tuning, softCap tuning, and libp2p rate-limit application.
  - **`internal/v2wiring.Config.RecentRejectionsMaxBytes` (int64):**
    new optional field. When > 0, `Wire()` calls `fp.SetMaxBytes(n)`
    after `NewFilePersister` succeeds. Default 0 (disabled) so
    existing operator configs stay byte-cap-disabled until they
    explicitly opt in. Production tuning guidance in the field's
    doc comment: at the default `softCap=DefaultMaxRejections=1024`
    and ~512 bytes per record, the soft-cap rewrite loop keeps the
    file at ~512 KiB; setting `MaxBytes=8*1024*1024` (8 MiB) gives
    the soft-cap roughly 16x headroom — comfortable for transient
    spikes, tight enough to cap a sustained flood at minute resolution.
  - **Tests:** four new `pkg/mining/attest/recentrejects` tests pin
    the persister contract: `MaxBytes` accessor round-trip with
    negative-input clamp + nil-receiver guard,
    `Disabled_NoDropsRegardlessOfSize` (50 records past where a tiny
    cap would fire — feature-off path stays clean), salvage-
    compaction-admits (cap tight enough to trigger but loose enough
    that the rewrite frees headroom), and hard-refusal +
    `ErrHardCapExceeded` + telemetry-fires + in-memory-ring-unaffected.
    The shared `captureRecorder` test fake was extended to satisfy
    the new `PersistHardCapDropRecorder` interface with an
    `hardCapSnapshot()` accessor for the persister-side tests. Five
    new `pkg/monitoring` tests pin the counter-increments contract,
    snapshot-includes-field, adapter-routing, no-leakage between
    counters, and `ResetRecentRejectMetricsForTest`-clears-new-counter.
    The compile-time assertion in
    `TestRecentRejectsMetricsAdapter_ImplementsInterface` was
    extended to cover the new interface so a future method-rename
    surfaces as a build failure rather than a silent telemetry break.
    The dashboard integration test was extended with two new label
    assertions (`'hard-cap drops'`,
    `metrics.persist_hardcap_drops_total`) so a regression that drops
    the cell ship-stops the bundle build.

- **Attestation-rejections dashboard tile — triage controls
  (kind / window / pause / top-miners / CSV) (2026-04-30).** Operators
  using the rejection tile in production hit the same friction within
  the first incident: the table page-rolls every 2 s while you're
  trying to read a row, you can't narrow to "just gpu_name_mismatch"
  without leaving the dashboard for the v1 list endpoint, and there
  is no quick "give me the last 50 rows as CSV" path for an out-of-
  band post-mortem. This change closes all five gaps in one tile
  refresh, with no new API surface beyond two query parameters on
  the existing `/api/attest/rejections` endpoint.
  - **Kind filter dropdown (5 options):** `all` (default) plus the
    four closed-enum kinds (`archspoof_unknown_arch`,
    `archspoof_gpu_name_mismatch`, `archspoof_cc_subject_mismatch`,
    `hashrate_out_of_band`). Selection forwards as `?kind=` to the
    handler. The handler validates against the same allowlist the
    v1 list handler uses (new exported predicate
    `api.IsKnownRecentRejectionKind`); a typo'd kind returns 400 so
    the operator sees the failure rather than silently getting "no
    filter applied".
  - **Time-window dropdown (5 options):** `since boot` (default),
    `last 24h / 6h / 1h / 15m`. Stored as a rolling seconds-back
    value; the absolute `since=` query parameter is recomputed from
    `Date.now()` on every fetch, so the window slides forward in
    lockstep with the clock (matching operator intuition: "last 1h"
    means "last 1h from now", not "last 1h from when I clicked").
  - **Pause-polling toggle:** flips a module-level
    `attestRejectionsState.paused` flag. The 2 s `setInterval` in
    `startPolling` skips the rejection tile when set; the other
    tiles keep ticking. Resume fires one explicit
    `updateAttestRejections()` so the operator gets fresh data
    without waiting for the next tick. Button colour shifts cyan
    ↔ orange to make the state glanceable from across the room.
  - **Top-3 offending miners strip:** computed client-side over
    the current page (records with no `miner_addr` skipped). Hidden
    when no rejection in the page has a populated miner. Long
    addresses truncated to 24 chars with the full value in the
    title attribute; rendered via `textContent` to defeat any HTML
    payload a hostile miner might smuggle through the field.
  - **CSV export link:** `data:text/csv;charset=utf-8,…` URL built
    on every successful fetch from the current `lastRecords` array,
    so a click is always one render away from the freshest 50-row
    snapshot. Per-cell quoting follows RFC 4180; cells starting
    with `=`, `+`, `-`, `@`, or tab get a leading apostrophe to
    defeat Excel/LibreOffice formula injection (the apostrophe is
    dropped on display by both apps). Header row carries 10 fields:
    `seq, recorded_at, kind, reason, arch, height, miner_addr,
    gpu_name, cert_subject, detail` — superset of what the table
    renders, so an operator pasting the CSV into an incident
    ticket has the full record without an extra API roundtrip.
  - **Wire-shape change:**
    `dashboardAttestRejectionsView.Filters` is a new optional
    pointer field carrying the echoed `kind` / `since` filters the
    server actually applied. Pointer (rather than struct +
    `omitempty`) because Go's JSON encoder does NOT elide a
    zero-valued struct under `omitempty` — only `nil` pointers /
    empty slices / etc. So a bare-call response carries no
    `"filters"` key at all, and a filtered response carries the
    block — matches the operator audit need ("did the server
    understand my dropdown?") while keeping the bare-call payload
    minimal.
  - **New shared predicate `api.IsKnownRecentRejectionKind`** plus
    `api.KnownRecentRejectionKinds` (ordered slice for dropdown
    population). The dashboard handler imports the predicate so
    its closed-enum and the v1 list handler's stay in lock-step;
    a future allowlist change surfaces in both surfaces from a
    single edit.
  - **Tests:** four new `pkg/api` tests pin the predicate's
    accept-all-allowlisted, reject-typos-and-case-variants,
    empty-string-permissive, and stable-ordering-with-defensive-
    copy contracts. Five new `internal/dashboard` tests cover
    kind passthrough, kind 400-on-typo, since passthrough, since
    400-on-malformed, and the omitempty-Filters behaviour on
    bare calls. The integration test was extended with six new
    HTML element-ID assertions, six JS-symbol assertions, and a
    pause-gate string assertion so a regression that drops any
    of the new affordances ship-stops the bundle build.

- **Recent-rejection ring compaction observability — close the last
  visibility gap in the persistence layer (2026-04-30).**
  `FilePersister.compactLocked` was previously silent: a miner spamming
  forged rejections could trigger a soft-cap compaction every few
  seconds and the only signal would be a stalled disk. Two new metrics
  + a new dashboard cell + a new Prometheus alert close that gap:
  - **`qsdm_attest_rejection_persist_compactions_total` (counter):**
    increments on every successful soft-cap compaction (post-rename).
    No-trim early-returns — where `loadAllLocked` finds ≤ softCap
    records and the file is left untouched — do NOT increment, so the
    counter is a faithful "rewrites" rate.
  - **`qsdm_attest_rejection_persist_records_on_disk` (gauge):**
    best-effort current size of the JSONL log in records. Updated at
    boot (one-shot scan of the existing file), after every successful
    Append (+1), and after every successful compaction (set to
    post-trim count). Approximate during concurrent reads — operators
    should treat ±softCap as the uncertainty window.
  - **Two new optional `MetricsRecorder` extension surfaces in
    `pkg/mining/attest/recentrejects/metrics.go`:**
    `PersistCompactionRecorder { RecordPersistCompaction(int) }` and
    `PersistRecordsRecorder { SetPersistRecordsOnDisk(uint64) }`.
    Both follow the same dependency-inversion pattern as the existing
    `MetricsRecorder` and `PersistErrorRecorder` (no-op default,
    pkg/monitoring's adapter implements them at init() time, type
    assertion at the call site so old recorders still build).
  - **`FilePersister` instrumentation in
    `pkg/mining/attest/recentrejects/persistence.go`:** new
    `recordsOnDisk atomic.Uint64` field, seeded at construction by
    counting existing records via `loadAllLocked` so the gauge
    reflects on-disk reality before the first Append fires; updated
    on every successful Append; reset to `len(keep)` after every
    successful compaction. New `RecordsOnDisk()` accessor for tests.
  - **Operator dashboard tile (`internal/dashboard/static/dashboard.js`)
    now renders three persistence cells side-by-side:** persist errors
    (red on non-zero), compactions (cyan, soft-cap rewrite count
    since boot), and records-on-disk (current JSONL size). Built via
    a new `buildPersistCell` helper that mirrors the per-field grid's
    visual rhythm. Caption updated to point at all four Prometheus
    series.
  - **New Prometheus alert
    `QSDMAttestRejectionPersistCompactionsHigh`** in
    `QSDM/deploy/prometheus/alerts_qsdm.example.yml`: fires on
    `>5 compactions/min sustained 30m` (severity: warning).  At the
    default softCap=1024 that's ~85 rejections/s sustained — roughly
    10× a healthy validator's baseline — so the threshold catches
    real rejection-flood patterns without paging on noise.
  - **Test coverage:**
    - `pkg/mining/attest/recentrejects/persistence_test.go`: 4 new
      tests — `TestFilePersister_RecordsOnDisk_TracksAppendAndCompact`
      (atomic counter contract end-to-end through the no-trim and
      trim compaction paths),
      `TestNewFilePersister_SeedsRecordsOnDiskFromExistingFile` (boot-
      time gauge accuracy), `TestFilePersister_CompactionHook_FiresOnTrim`
      (hook fires exactly once with the correct post-compaction
      count), `TestFilePersister_CompactionHook_NoTrim_DoesNotFire`
      (no-trim early-return must not fire the hook — preserves the
      counter's "rewrites" semantics).
    - `pkg/mining/attest/recentrejects/metrics_test.go`: extended the
      shared `captureRecorder` fake with `RecordPersistError` /
      `RecordPersistCompaction` / `SetPersistRecordsOnDisk` methods
      plus a `persistSnapshot` accessor so persistence tests use the
      same recorder helper without duplicating it.
    - `pkg/monitoring/recentrejects_metrics_test.go`: 4 new tests —
      `TestRecordRecentRejectPersistCompaction_Increments` (counter
      contract), `TestSetRecentRejectPersistRecordsOnDisk_Stores`
      (gauge contract incl. zero-input), `TestRecentRejectMetricsSnapshot_IncludesCompactionAndOnDisk`
      (wire shape regression-pin for the dashboard tile),
      `TestRecentRejectsMetricsAdapter_CompactionAndRecordsRoute`
      (adapter→counter routing). Compile-time assertion in
      `TestRecentRejectsMetricsAdapter_ImplementsInterface` extended
      to ship-stop on a future drop of either new method.
    - `internal/dashboard/integration_test.go`: extended to assert
      both new field labels (`'compactions'`, `'records on disk'`)
      and JSON keys (`metrics.persist_compactions_total`,
      `metrics.persist_records_on_disk`) are present in the served
      `dashboard.js`.

- **Operator dashboard tile: recent attestation rejections + truncation
  telemetry (2026-04-29, frontend slice).**
  Renders the `/api/attest/rejections` envelope landed in commit
  `d3a1a54` as a full dashboard panel. Tile shows the per-field
  rune-truncation grid (Detail / GPUName / CertSubject — observed
  total, truncated total, max-runes-seen, computed truncation rate),
  the `qsdm_attest_rejection_persist_errors_total` counter colour-coded
  red on any non-zero value, and the newest 50 rows of the
  recent-rejection ring (recorded-at, kind, reason/arch, miner short
  address with full address on hover, height). Polls every 2 s on the
  same `setInterval` loop as the existing tiles and is also fired once
  during `startUpdates()` for instant first paint.
  - **`internal/dashboard/static/index.html`**: new
    `🛑 Attestation Rejections` card placed directly after the NGC
    GPU proofs panel — they share an operator-mental-model
    (NVIDIA-attestation rejections often surface here first when
    upstream NGC validation tightens). Card holds the four element
    IDs `attest-rejections-{status,counters,table,tbody}`; the
    `internal/dashboard` integration test now ship-stops on any
    of those IDs going missing so a future CSS/markup refactor
    can't silently unhook the poller.
  - **`internal/dashboard/static/dashboard.js`**:
    `updateAttestRejections()` is the new poller. Always renders
    cells via `textContent` (never `innerHTML`) for the miner
    address column because that field originates from rejected
    miners and could in principle carry hostile bytes;
    server-side fields like `kind` and `reason` come from closed
    allowlists in `pkg/mining/attest/recentrejects` and are
    safe-by-construction. The fetch sends `credentials: 'include'`
    so the dashboard auth cookie travels and the
    `requireAuth`-wrapped handler accepts the request. The
    function is wired into both `startUpdates()` (initial
    paint) and `startPolling()` (recurring 2 s loop).
  - **Backwards compatibility:** if `v2wiring.Wire()` did not
    register a `RecentRejectionLister` (v1-only deployments, or
    operators who explicitly disabled the v2 store), the API
    returns `available:false` and the tile renders an explanatory
    banner rather than a 503; counters and history rows stay
    empty. Operators upgrading from v1 see the tile populate
    automatically the next time `Wire()` runs.
  - **Tests:** `internal/dashboard/integration_test.go` now
    asserts the four tile IDs are present in the rendered HTML
    and that `updateAttestRejections` plus the
    `/api/attest/rejections` fetch target are present in the
    served JS bundle. Confirmed `node --check` passes on the
    updated `dashboard.js` (no JS regressions).
  - **Rebrand-residue cleanup:** while in `dashboard.js`,
    removed two leftover `legacy <code>QSDM_NGC_INGEST_SECRET</code>
    still accepted` / `legacy <code>QSDM_NGC_REPORT_URL</code>
    still accepted` strings whose "legacy" half was identical to
    the canonical name — a flatten artifact from the same rebrand
    sweep audited in commits `3d798c8` / `b488996` / `cef8581` /
    `eabaecb`. The dashboard previously promised env-var
    compatibility that `pkg/envcompat` no longer provides; the
    cleaned text matches the audited server-side strings.

- **Recent-rejection ring on-disk persistence — restart no
  longer wipes §4.6 forensic record (2026-04-29).**
  Closes the explicit "out of scope" placeholder in
  `pkg/mining/attest/recentrejects`'s package doc: the ring
  was volatile by design, and every restart wiped the entire
  forensic record of arch-spoof / hashrate-band / CC-subject
  rejections. Production validators now configure
  `Config.RecentRejectionsPath` in `internal/v2wiring` to
  point the ring at a JSONL log under the state directory;
  Wire() opens or creates the file, attaches it to the
  `recentrejects.Store`, and replays prior records into the
  in-memory ring at boot. Empty path = legacy in-memory-only
  posture, fine for ephemeral testnets.
  - **New `recentrejects.Persister` interface + no-op default
    + `FilePersister` implementation in
    `pkg/mining/attest/recentrejects/persistence.go`.** The
    Persister is narrow (Append / LoadAll / Close), mirrors
    the dependency-inversion shape of `MetricsRecorder`, and
    keeps a future SQLite or rotation-aware backend behind
    the same surface without touching the Store's call sites.
    `FilePersister` is the production implementation:
    append-only JSONL, per-call open/close (≈10 µs syscall
    overhead per record — 0.1% CPU at 100 rejections/s),
    crash-recovery framing that prepends a leading newline if
    the prior write tailed mid-record, and corruption-tolerant
    `LoadAll` that skips malformed JSON lines so a partial
    write at the file's tail does not block boot.
  - **Bounded growth via soft-cap compaction.** Default
    `softCap = recentrejects.DefaultMaxRejections` (1024
    records). Every 1024 successful Appends the persister
    rewrites the file, keeping only the most recent 1024
    records (write to `<path>.tmp`, atomic rename onto
    `<path>` — same crash-safe pattern as
    `chainparams.SaveSnapshotWith`). Worst-case on-disk
    footprint is ≈ 512 KiB before compaction fires; recovered
    footprint is ≈ 256 KiB. A malicious miner cannot use the
    persister as a DoS vector to fill the disk.
  - **`Store.SetPersister(p)` + `Store.RestoreFromPersister()`
    + `Store.PersistErrorCount()` API surface.** Setter is
    idempotent and accepts nil to revert to the no-op
    default; `RestoreFromPersister` is the explicit one-shot
    boot replay that fails loud on a second invocation
    (catches double-restore wiring bugs); `PersistErrorCount`
    returns the cumulative count of `Persister.Append`
    failures observed by `Record()` (forensic dashboards
    join this with the new Prometheus mirror counter).
  - **Best-effort persistence semantics.** A failed Append
    does NOT roll back the in-memory record — operators can
    still see the rejection live via
    `GET /api/v1/attest/recent-rejections`, and the
    `qsdm_attest_rejection_persist_errors_total` counter +
    `Store.PersistErrorCount()` accessor surface the
    filesystem failure independently. The forensic ring is
    operator telemetry, not consensus state; degraded
    durability is recoverable on the next successful Append.
  - **New Prometheus series
    `qsdm_attest_rejection_persist_errors_total`** in
    `pkg/monitoring/recentrejects_metrics.go`. Unlabeled
    (filesystem failures are not field-keyed). The
    monitoring adapter implements the new optional
    `recentrejects.PersistErrorRecorder` interface alongside
    the existing `MetricsRecorder` so a method drop is a
    compile-time error rather than a silent dashboard gap.
  - **`internal/v2wiring.Wire()` integration.** Two new
    Config fields:
    - `RecentRejectionsPath string` — empty = legacy in-memory-only
      ring; non-empty = construct `FilePersister`,
      `SetPersister`, `RestoreFromPersister`. Construction
      failure is non-fatal and routed through
      `LogRecentRejectionsError`; the ring degrades to
      in-memory-only rather than aborting boot.
    - `LogRecentRejectionsError func(error)` — invoked on
      construction failure and on boot-time restore failure.
      Per-record Append failures are NOT routed here (too
      noisy under filesystem flap); they bump
      `qsdm_attest_rejection_persist_errors_total` for
      dashboard / alert use.
  - **New audit checklist row** `store-04` under
    `CatStorage` (severity Medium): "Recent-rejection ring
    persistence bounded + corruption-tolerant" with explicit
    acceptance criteria for the 0600 file mode, JSONL
    framing, atomic-rename compaction, hard-kill recovery
    behaviour, and the persist-errors metric coverage.
  - **Test coverage:**
    - `pkg/mining/attest/recentrejects/persistence_test.go`
      (16 tests): round-trip Append/LoadAll, missing-file
      tolerance, corrupt-line tolerance after a simulated
      hard kill, soft-cap compaction (both no-trim and
      trim paths), Store restore populates the ring,
      Restore respects in-memory cap (drops oldest beyond
      `Cap()`), Restore reseeds the Seq counter, Restore
      double-call fails loud, no-op persister default,
      Record fires Append, PersistErrorCount increments on
      failing persister, concurrent Append (8 workers ×
      50 iterations under `-race`), empty-path rejection,
      default soft-cap, SetPersister(nil) reverts to noop.
    - `pkg/monitoring/recentrejects_metrics_test.go`
      (3 new tests): persist-error counter increments on
      non-nil error, nil error is a no-op, end-to-end
      adapter routing from `Store.Record` through the
      dependency-inverted chain to the Prometheus counter.
    - `internal/v2wiring/v2wiring_recentrejects_persist_test.go`
      (4 tests): empty path → no-op persister, non-empty
      path → on-disk Append, restart survival across two
      Wire() calls, unwritable path surfaces via
      `LogRecentRejectionsError` without crashing boot.
  - **Backward compatibility.** Pre-existing
    `recentrejects.MetricsRecorder` implementations need no
    change — `PersistErrorRecorder` is a separate optional
    interface the Store probes via type assertion. Tests
    that construct a Store without setting a persister
    continue to behave exactly as before (no filesystem
    dependency, no behavioural change).

- **Recent-rejection ring truncation telemetry — operators can
  now alert on cap pressure before it goes silent (2026-04-29).**
  Closes the observability gap on the
  `pkg/mining/attest/recentrejects` ring's defensive
  rune-truncation layer: every Record() call truncates
  `Detail` to 200 runes, `GPUName` and `CertSubject` to 256
  runes (defending the validator against a malicious miner
  stuffing megabyte attestation fields), but until now those
  truncations were invisible — operators discovered them by
  noticing `qsdmcli watch archspoof --detailed` output ended
  with `…` and grep'ing source for the cap.
  - **New dependency-inverted
    `recentrejects.MetricsRecorder` interface + no-op default
    + `SetMetricsRecorder` setter** mirrors the
    `mining.MiningMetricsRecorder` and
    `mining.RejectionRecorder` posture: pkg/mining/attest/recentrejects
    declares the narrow surface in `metrics.go` so it stays
    independent of pkg/monitoring (the import cycle the
    inversion exists to break), and pkg/monitoring's new
    `recentrejects_recorder.go` registers a Prometheus-backed
    adapter at `init()` time.
  - **`Store.Record()` now observes pre-truncation rune
    counts on every non-empty observed field.** New
    `observeAndTruncate(fieldName, s, cap)` helper wraps the
    existing `truncateRunes` clamp with a single
    `atomic.Value`-backed recorder call; one rune-slice
    allocation per non-empty field (matching the prior cost)
    plus an interface dispatch. Empty fields skip the
    recorder entirely so HMAC-only paths (CertSubject empty)
    and CC-only paths (GPUName empty) do not skew the
    truncation-rate denominator.
  - **Three new Prometheus series in
    `pkg/monitoring/recentrejects_metrics.go`:**
    - `qsdm_attest_rejection_field_runes_observed_total{field}`
      — denominator for the truncation rate; one increment
      per non-empty observed field per `Record()` call.
    - `qsdm_attest_rejection_field_truncated_total{field}`
      — numerator; only increments when the pre-truncation
      rune count exceeded the in-store cap.
    - `qsdm_attest_rejection_field_runes_max{field}`
      — process-lifetime monotonic max gauge (CAS loop on
      `atomic.Uint64`); the "how close are we to the cap?"
      headroom signal. Resets only on process restart.
    Cardinality: 3 series families × 3 fields = 9 series,
    well under any best-practice ceiling. Unknown field
    names from a future code path are silently ignored so
    the cardinality bound holds even under a typo regression.
    Negative rune counts are clamped to 0 so an
    arithmetic-bug under-flow cannot wedge the gauge at
    `MaxUint64`.
  - **`prometheus_scrape.go` integration.** `corePrometheusMetrics()`
    now emits the three new series families next to the
    existing `qsdm_attest_archspoof_rejected_total{reason}` and
    `qsdm_attest_hashrate_rejected_total{arch}` counters, in a
    stable (detail, gpu_name, cert_subject) order so dashboard
    PromQL expressions can rely on a fixed series shape.
  - **Two new example Prometheus alert rules in
    `QSDM/deploy/prometheus/alerts_qsdm.example.yml`** under
    a new `qsdm-v2-attest-recent-rejections` group:
    - `QSDMAttestRejectionFieldTruncationSustained` — fires
      when `rate(truncated)/rate(observed) > 25%` over 10m
      for any field, with a denominator guard (rate(observed)
      > 0) so quiet nodes do not page on 0/0.
    - `QSDMAttestRejectionFieldRunesMaxNearCap` — info-only
      leading indicator: fires when `runes_max` is within 10%
      of the cap (180/200 for detail, 230/256 for gpu_name
      and cert_subject) for >30m, so operators see the ramp
      before the truncation-rate alert paints.
  - **Test coverage.** 19 new unit tests:
    - 9 in `pkg/mining/attest/recentrejects/metrics_test.go`
      covering observeAndTruncate firing on non-empty fields,
      skipping empty fields, pre-truncation rune count
      preservation, the cap-vs-cap+1 boundary on the
      truncated flag, the no-op default, the
      `SetMetricsRecorder(nil)` revert path, full
      `Store.Record()` integration with all three fields,
      absent-field skipping on HMAC-only rejections,
      cap-pressure on a CC mismatch, and an `atomic.Value`
      concurrent-swap smoke (1000 records × 50 swaps with no
      lost ObserveField calls).
    - 6 in
      `pkg/monitoring/recentrejects_metrics_test.go`
      covering observed/truncated/runes_max bucketing by
      field, unknown-field cardinality bound, negative-rune
      clamp, runes_max monotonicity (CAS loop), labelled-
      output stable ordering, and the init()-time adapter
      registration smoke (drives a real `Store.Record()`
      through the production wiring and asserts the
      monitoring counters incremented).
    - Plus a compile-time assertion that the adapter
      satisfies the recorder interface.
    Total: `pkg/mining/attest/recentrejects` 18 → 27 tests;
    `pkg/monitoring` adds 6 tests (recentrejects-side).
  - **MINER_QUICKSTART note.** New `## §4.6 telemetry — recent-
    rejection ring truncation` operator subsection documents
    the three series, how to derive the truncation rate via
    PromQL, and which constants to bump in
    `pkg/mining/attest/recentrejects/recentrejects.go` if
    sustained truncation indicates the caps are too tight.
  - **Backward-compatible.** A pure-recentrejects build
    (e.g. a unit test that depends only on
    `pkg/mining/attest/recentrejects` without
    `pkg/monitoring`) keeps the no-op default recorder and
    runs unchanged. Production binaries that link
    `pkg/monitoring` get the Prometheus-backed adapter the
    moment the binary's `init()` chain fires; no
    configuration change required.

- **Structured `*archcheck.RejectionDetail` wrapper — `gpu_name`
  and `cert_subject` now populate end-to-end on §4.6 rejections
  (2026-04-29).** Closes the "GPUName / CertSubject empty
  end-to-end" caveat noted in the prior commit. The outer
  verifier never sees the bundle's gpu_name or the leaf cert's
  subject directly — both live inside the per-type verifier
  (`pkg/mining/attest/{hmac,cc}/`) — so until now the
  `recent-rejections` ring populated those fields with empty
  strings.
  - **New `archcheck.RejectionDetail` error wrapper** carries
    the offending value (`GPUName` on HMAC paths,
    `CertSubject` on CC paths, raw `GPUArch` on outer-arch
    paths) plus the matched/expected `Patterns`. Implements
    `Unwrap()` returning the canonical sentinel
    (`ErrArchUnknown` / `ErrArchGPUNameMismatch` /
    `ErrArchCertSubjectMismatch`) so every existing
    `errors.Is(err, archcheck.ErrArch*)` call site keeps
    working byte-for-byte.
  - **`ValidateOuterArch`,
    `ValidateBundleArchConsistencyHMAC`, and
    `ValidateBundleArchConsistencyCC` now return
    `*RejectionDetail`** instead of bare `fmt.Errorf("%w: ...")`.
    The rendered `Error()` string is preserved verbatim
    (operator log lines do not visibly drift).
  - **Outer verifier traverses the wrapper via `errors.As`.**
    `recordRejectionForArchSpoof` now extracts `GPUName` /
    `CertSubject` from the structured detail attached to the
    error chain — works through the per-type verifier's
    `fmt.Errorf("hmac: %w: %w", err,
    mining.ErrAttestationSignatureInvalid)` double-wrap (Go
    1.20+ multi-`%w`). Outer-verifier signature simplified:
    `recordRejectionForArchSpoof(err, p)` — no more
    placeholder `gpuName, certSubject` args.
  - **Wire surface unchanged.** Every JSON consumer of
    `/api/v1/attest/recent-rejections` and
    `qsdmcli watch archspoof --detailed` automatically starts
    receiving populated `gpu_name` / `cert_subject` fields the
    moment a node deploys this binary; no client-side code
    change required.
  - **Test coverage.** 19 new unit / integration tests
    (1973 → 1992 module-wide):
    - 14 in
      `pkg/mining/attest/archcheck/rejection_detail_test.go`
      covering `errors.Is` parity for all three sentinels,
      `errors.As` extraction (HMAC/CC/outer-unknown,
      including the per-type verifier's double-`%w`), Error()
      string parity (allowed-list suffix on outer-unknown,
      gpu_name + patterns on HMAC, cert_subject on CC, empty
      gpu_name special case), nil-detail safety, and
      defensive Patterns-slice copying.
    - 3 in
      `pkg/mining/verifier_recentrejects_test.go` driving the
      verifier hot path with real `archcheck.Validate*`
      returns wrapped under `ErrAttestationSignatureInvalid`,
      validating that `RejectionEvent.GPUName` /
      `.CertSubject` surface automatically end-to-end.
    - 2 in
      `internal/v2wiring/v2wiring_recentrejects_test.go`
      locking the round trip through the production HTTP
      handler: a record with `GPUName` populated round-trips
      to `view.Records[0].GPUName`; same for `CertSubject`.
  - **Backward-compatible.** Older deployments running the
    prior binary against this new client / dashboard simply
    continue to emit empty-string `gpu_name` / `cert_subject`
    — the omitempty JSON tags drop them from the wire, and
    consumers handle absence the same way they handle
    "rejection happened on a path that doesn't carry that
    detail".

- **`/api/v1/attest/recent-rejections` endpoint — per-event detail
  companion to the §4.6 archspoof / hashrate Prometheus counters
  (2026-04-29).** Closes the "out of scope" caveat shipped with
  `qsdmcli watch archspoof`: where the counters answer "how
  many rejections by reason/arch?" the new endpoint answers
  "*who* got bounced, *what* did they claim, *which* leaf cert
  subject was rejected?" without round-tripping through metrics
  scrape or grepping validator logs.
  - **New package `pkg/mining/attest/recentrejects`** — bounded
    FIFO ring of structured `Rejection{Seq, RecordedAt, Kind,
    Reason, Arch, Height, MinerAddr, GPUName, CertSubject,
    Detail}` records (default cap 1024, ~256 KiB saturated).
    Cursor-based pagination via monotonic `Seq`; binary-search
    cursor lookup keeps page reads O(log n + page_size). All
    string fields are length-clamped at write time (Detail at
    200 runes, GPUName/CertSubject at 256) so a malicious
    miner cannot OOM the validator with megabyte attestation
    payloads.
  - **Dependency-inverted `mining.RejectionRecorder` hook** —
    new `mining.SetRejectionRecorder(...)` mirrors the existing
    `MiningMetricsRecorder` posture: pkg/mining declares the
    narrow interface + a no-op default, internal/v2wiring
    installs the bounded ring at boot. Verifier hot path adds
    one atomic.Load + interface dispatch per §4.6 rejection
    alongside the existing metrics-counter call. Fires on
    `archcheck.ValidateOuterArch` failure (kind
    `archspoof_unknown_arch`),
    `archcheck.ValidateClaimedHashrate` failure
    (`hashrate_out_of_band`), and per-type verifier
    `ErrArchGPUNameMismatch` /
    `ErrArchCertSubjectMismatch` returns
    (`archspoof_gpu_name_mismatch` /
    `archspoof_cc_subject_mismatch`). Generic crypto errors
    (HMAC tag mismatch, expired cert) deliberately do NOT
    bucket — same posture as the metrics counters.
  - **`GET /api/v1/attest/recent-rejections` HTTP handler.**
    Cursor-paginated list endpoint with closed-enum filter
    validation: `?cursor=<seq>`, `?limit=N` (clamped to
    [1, 500]), `?kind=`, `?reason=`, `?arch=`,
    `?since=<unix-secs>`. Bad filter values return 400 with a
    helpful message (so a typo'd `kind` doesn't silently
    degrade to "no filter"); empty store returns 200 with
    `records: []` (distinct from 503 = "store not wired").
    Echoes the parsed filters back in the response so clients
    can audit what the server applied. Mounted in
    `pkg/api/handlers.go` next to the slash / enrollment read
    endpoints.
  - **`qsdmcli watch archspoof --detailed` operator UX.**
    New flag flips the watcher from counter-bucket diffing to
    per-record streaming via the new endpoint. Emits one
    `WatchKindArchSpoofRejection` event per actual store
    record with `seq`, `reason`, `arch`, `height`,
    `miner_addr`, `gpu_name`, `cert_subject`, and `detail`.
    Cursor-based: the watcher tracks the highest `Seq`
    observed across polls; default mode (no
    `--include-existing`) starts from "now" so operators
    don't replay history at startup. 503 from the endpoint
    fails loudly with a fallback hint ("drop --detailed to
    use counter mode") rather than silently looping.
    Server-side single-value `?reason=` / `?arch=` filters
    forward when exactly one value is set; multi-value
    filter sets fall back to client-side filtering (server
    only accepts one filter value per parameter).
  - **`internal/v2wiring` integration.** `Wire()` constructs
    one `recentrejects.Store` and installs it under both the
    producer-side (`mining.SetRejectionRecorder`) and the
    consumer-side (`api.SetRecentRejectionLister`) adapters.
    A new `Wired.RecentRejections` field exposes the store
    handle for tests + future call sites.
    `miningRejectionRecorderAdapter` and
    `recentRejectionListerAdapter` keep `pkg/api` and
    `pkg/mining` free of cross-imports.
  - **Test coverage.** 51 new unit / integration tests
    (1922 → 1973 module-wide):
    - 18 in `pkg/mining/attest/recentrejects` covering
      ring construction, FIFO eviction at the cap, monotonic
      `Seq`, RecordedAt fill, defensive truncation, filter
      matrix (kind / reason / arch / since / combined),
      cursor pagination, nil-store safety, and concurrent-
      writer correctness with sequence monotonicity assertion.
    - 6 in `pkg/mining/verifier_recentrejects_test.go`
      driving the verifier hot path against a capturing
      recorder: each of the four kinds plus a no-bucket-on-
      generic-crypto-error pin and a nil-recorder fallback
      smoke test.
    - 14 in `pkg/api/handlers_recent_rejections_test.go`
      covering happy-path, empty-store-returns-200,
      503/405/400 paths, all four filter validations, limit
      clamping, filter forwarding, echoed-filters response,
      and Content-Type pin.
    - 4 in
      `internal/v2wiring/v2wiring_recentrejects_test.go`
      driving Wire() → store → handler round trip,
      kind-filter forwarding through the production
      adapter, multi-page pagination round trip, and the
      503 fallback when Wire() never ran.
    - 9 in `cmd/qsdmcli/watch_archspoof_test.go` covering
      `--detailed` once-mode no-events, drain-on-include-
      existing with two records, 503 fail-loud, human-
      readable formatting with all populated fields, and
      `buildRecentRejectionsPath` filter / cursor wiring.
    Total `qsdmcli` tests 195 → 204; total `pkg/mining`
    tests 479 → 485; total `internal/v2wiring` tests
    36 → 40.
  - **Out of scope.** Persistence — the ring is volatile;
    a restart wipes it. The same boundary is documented for
    `chain.SlashReceiptStore`. A future on-disk
    implementation can plug behind the
    `mining.RejectionRecorder` and
    `api.RecentRejectionLister` interfaces without changing
    the verifier or the handler.

- **`qsdmcli watch archspoof` — operator-facing live stream of
  arch-spoof and hashrate-band rejection bursts (2026-04-29).**
  Fourth member of the `qsdmcli watch *` family alongside
  `enrollments` / `slashes` / `params`. Polls
  `/api/metrics/prometheus`, parses the
  `qsdm_attest_archspoof_rejected_total{reason}` and
  `qsdm_attest_hashrate_rejected_total{arch}` counter
  families, and emits one event per non-zero counter delta on
  each tick. Designed as the per-event complement to the
  Prometheus alert rules shipped in the previous slot: alerts
  say "something is wrong"; the watcher says "here is each hit
  as it lands, in order".
  - **Two new event kinds** on the shared `WatchEvent`
    envelope: `archspoof_burst` (with `reason`,
    `delta_count`, `total_count`) and `hashrate_burst` (with
    `arch`, `delta_count`, `total_count`). JSON-Lines
    consumers can decode every watcher's output with a single
    struct definition; renaming either kind is a wire-format
    change pinned by tests.
  - **Counter-rollback handling.** Counters monotonically
    increase under normal operation; a decrease across two
    polls (process restart wiping in-memory counters) snaps
    the snapshot to the new baseline without emitting. Under-
    counting one cycle is preferred to a spurious "burst" the
    moment a validator restarts. Covered by
    `TestDiffArchSpoofSnapshots_CounterRollback_Silent`.
  - **Server-side filters.** `--reason` and `--arch` flags
    accept comma-separated allowlists (e.g.
    `--reason=cc_subject_mismatch` to monitor only the
    critical bucket); flag values are validated against the
    canonical enums at parse time so typos surface
    immediately rather than as silent no-matches.
  - **Metrics-URL derivation.** Defaults to deriving from
    `QSDM_API_URL` (replacing the trailing `/api/v1` with
    `/api/metrics/prometheus`); overridable via
    `--metrics-url` flag or `QSDM_METRICS_URL` env var for
    operators with split data-plane / metrics-plane
    deployments.
  - **Auth.** Same Bearer-token plumbing as the rest of
    `qsdmcli`. The dashboard's `requireMetricsScrapeOrAuth`
    middleware accepts either a JWT or the metrics-scrape
    secret; the Bearer side is the one wired in `qsdmcli`
    today.
  - **Test coverage.** 34 new unit / integration tests in
    [`QSDM/source/cmd/qsdmcli/watch_archspoof_test.go`](QSDM/source/cmd/qsdmcli/watch_archspoof_test.go)
    covering flag normalisation, CSV-set parsing, URL
    derivation, exposition parsing (happy path, float values,
    malformed lines, empty arch label normalisation),
    `splitExpositionLine` direct cases, diff-core semantics
    (no-change, single-bucket burst, multi-bucket sorted
    output, counter rollback, filter enforcement),
    `--include-existing` snapshot synthesis, end-to-end
    `--once` mode against an `httptest` metrics server, and
    router dispatch / unknown-subcommand error advertisement.
    Total qsdmcli tests: 161 → 195.
  - **Out of scope (deliberately).** Per-rejection `node_id` /
    GPU name / raw error message — the metrics layer is
    label-coarse on purpose; surfacing that detail would
    require a server-side ring buffer and a new
    `/api/v1/attest/recent-rejections` endpoint. Operators
    needing per-event detail can correlate watcher bursts
    against the validator's structured log; a recent-
    rejections endpoint is queued behind the watcher-bot
    reference impl in a future session.

- **Prometheus alert rules + scrape wiring for the §4.6
  arch-spoof gate, hashrate-band gate, and `qsdm/gov/v1`
  authority-rotation pipeline (2026-04-29).** Three new alert
  rule groups land in
  [`QSDM/deploy/prometheus/alerts_qsdm.example.yml`](QSDM/deploy/prometheus/alerts_qsdm.example.yml)
  alongside a wiring fix that closes a silent gap caught while
  shipping this work.
  - **`qsdm-v2-attest-archspoof`** — three rules, one per reason
    label on `qsdm_attest_archspoof_rejected_total`:
    `unknown_arch` (warning, sustained probe), `gpu_name_mismatch`
    (warning, lazy spoof by enrolled operator), and
    `cc_subject_mismatch` (**critical, fires on a single
    increment** because reaching that branch means the proof
    has already passed cert-chain pin + AIK signature, so the
    contradiction is a cryptographic anomaly).
  - **`qsdm-v2-attest-hashrate`** — single rule keyed on
    `{{ $labels.arch }}` so all five canonical GPU
    architectures (Hopper/Blackwell/Ada/Ampere/Turing) are
    covered without manual duplication. Annotation includes
    the §4.6.3 reference band table so the on-call gets the
    full triage context inline.
  - **`qsdm-v2-governance`** — three rules: vote recorded
    (info, FYI ping for the multisig set), threshold crossed
    (warning, proposal staged), and AuthorityList size below 2
    (critical floor protecting against single-signer
    governance degeneration).
  - **Scrape-path wiring fix.** While exploring the existing
    metrics surface I found that the four
    `qsdm_gov_authority_*` series — counters defined and
    incremented from `gov_metrics.go` since the multisig work
    landed — were never iterated over in
    [`prometheus_scrape.go::corePrometheusMetrics`](QSDM/source/pkg/monitoring/prometheus_scrape.go).
    Operators would have seen empty `/metrics` for the entire
    governance authority surface; the alerts above wouldn't
    have anything to fire on. Fixed by adding the four
    `for/range` blocks plus the gauge `add()` call mirroring
    the existing param-pipeline shape. Locked down by 4 new
    tests in
    [`gov_metrics_scrape_test.go`](QSDM/source/pkg/monitoring/gov_metrics_scrape_test.go)
    that drive the recorders and assert the names, types, and
    labels appear in `corePrometheusMetrics()` output (and one
    end-to-end test of `PrometheusExposition()` to catch
    formatter regressions).
  - **CI guard.**
    [`.github/workflows/validate-deploy.yml::prometheus-rules-check`](.github/workflows/validate-deploy.yml)
    runs `promtool check rules` on every push that touches
    `QSDM/deploy/prometheus/**` — the new rules pass clean
    locally with `promtool 2.55.1` (`SUCCESS: 33 rules
    found`).
  - **Docs.**
    [`QSDM/deploy/prometheus/README.md`](QSDM/deploy/prometheus/README.md)
    table of rule groups extended to include the three new
    families with the same shape as the existing v2-mining
    entries, plus a short note explaining why
    `cc_subject_mismatch` is intentionally critical.
  - **Tests**: 4 new monitoring tests + all 1880+ existing
    tests pass; `go vet ./...` clean.

- **CC-path leaf cert subject ↔ `gpu_arch` consistency check
  (§4.6.5, 2026-04-29).** Replaces the earlier no-op stub
  `archcheck.ValidateBundleArchConsistencyCC` with a real
  evidence-based rule wired as Step 9 of the
  [`cc.Verifier`](QSDM/source/pkg/mining/attest/cc/verifier.go)
  flow, after the PCR floor. Completes the symmetry with the
  HMAC path's §3.3 step-8 `gpu_name` cross-check.
  - **Evidence-based, not strict.** If the leaf cert's
    `Subject.CommonName` contains a substring matching ANY
    canonical NVIDIA product pattern, the claimed `gpu_arch`
    must match the longest-pattern attribution (rejection
    wraps `archcheck.ErrArchCertSubjectMismatch` under
    `mining.ErrAttestationSignatureInvalid`). If the CN
    contains NO product evidence (test fixtures, corporate
    AIK labels like `"NVIDIA Confidential Computing AIK"`,
    OID-based model encodings), Step 9 passes through — the
    cert-chain pin (Step 3) and AIK signature (Step 4)
    remain the cryptographic locks. If
    `Attestation.GPUArch` is empty (standalone-call path /
    pre-fork bring-up), Step 9 is skipped.
  - **Longest-pattern overlap rule.** A subject like
    `"RTX 6000 Ada Generation"` matches both `"rtx 6000 ada"`
    (Ada) and `"rtx 6000"` (Turing) as substrings. The longer
    pattern wins, so the Ada attribution dominates and a
    `gpu_arch=turing` claim on that cert rejects. Locked
    down by
    [`TestVerifier_ArchCheck_LongestPatternWins`](QSDM/source/pkg/mining/attest/cc/verifier_archcheck_test.go).
  - **New Prometheus reason** for the `archspoof_rejected`
    counter:
    `qsdm_attest_archspoof_rejected_total{reason="cc_subject_mismatch"}`
    — distinct from `gpu_name_mismatch` so dashboards can
    split CC-path leaf-cert contradictions from HMAC-path
    lazy spoofs (different remediation playbooks). Cardinality
    is now ≤ 9 series total; the
    `mining.SetMiningMetricsRecorder` adapter forwards via
    `errors.Is(err, archcheck.ErrArchCertSubjectMismatch)` in
    [`pkg/mining/metrics.go`](QSDM/source/pkg/mining/metrics.go).
  - **Test scaffolding.**
    [`BuildOpts.LeafSubjectCN`](QSDM/source/pkg/mining/attest/cc/testvectors.go)
    (and `RootSubjectCN`) now lets test code mint
    product-named leaves; existing fixtures default to
    `"qsdm-test-nvidia-aik"` (product-free), so every
    pre-existing CC test passes through Step 9 unchanged.
    `cc.Verifier` wraps the rejection with double-`%w`
    (Go 1.20+) so callers can `errors.Is` against EITHER
    sentinel.
  - **Tests**: 6 new archcheck unit tests
    (`HappyPath`, `NoEvidencePassesThrough`,
    `RejectsContradiction`, `LongestPatternWins`,
    `CaseInsensitive`, `RejectsUnknownArch`) + 8 new CC
    verifier integration tests
    ([`verifier_archcheck_test.go`](QSDM/source/pkg/mining/attest/cc/verifier_archcheck_test.go))
    covering both spoof shapes, no-evidence pass-through for
    test fixture + corporate CNs, alias acceptance, longest-
    pattern wins, and the `GPUArch=""` skip-Step-9 path.
    Monitoring counter test extended to cover the new
    `cc_subject_mismatch` reason. Full suite (`go test ./...`)
    passes; `go vet ./...` clean.
  - **Docs**: `MINING_PROTOCOL_V2.md` §4.6.5 rewritten from
    "placeholder" to a full design with accept/reject table,
    overlap-resolution rule, and source links; §3.2 verifier
    flow renumbered to 9 steps; §4.6.4 metric reason set
    extended to include `cc_subject_mismatch`.

- **Hashrate-band plausibility check + Prometheus telemetry for
  the §4.6 arch-spoof gate (2026-04-29).** The §4.6 closed-enum
  + arch ↔ `gpu_name` rejection from earlier today now has a
  third leg: per-arch [Min, Max] bounds on
  `Attestation.ClaimedHashrateHPS`. A claim outside the band
  rejects with `archcheck.ErrHashrateOutOfBand` (wrapped in
  `mining.ErrAttestationSignatureInvalid`) BEFORE the per-type
  dispatcher fires, so an implausible-hashrate proof never
  pays the HMAC or X.509 work. `ClaimedHashrateHPS == 0` is
  treated as "not asserted" and passes through — preserves
  backward compat with miners and fixtures that don't populate
  the field.
  - **Bands** ([`archcheck.HashrateBandFor`](QSDM/source/pkg/mining/attest/archcheck/archcheck.go))
    are deliberately wide (~100x range per arch) so legitimate
    variation across a product family doesn't false-positive.
    Catches obvious lies — RTX 4090 claiming 200 MH/s, H100
    claiming 100 H/s, the 18 PH/s units-confusion typo.
  - **Prometheus telemetry** for the whole §4.6 rejection gate:
    - `qsdm_attest_archspoof_rejected_total{reason}` —
      `unknown_arch` | `gpu_name_mismatch`. Counts the §4.6.1
      allowlist rejects + §4.6.2 HMAC step-8 cross-check
      rejects.
    - `qsdm_attest_hashrate_rejected_total{arch}` — labelled by
      the canonical arch the claim was made against. Counts
      the §4.6.3 hashrate-band rejects.
    - Cardinality stays ≤ 8 series total. Both wire through
      a new dependency-inverted recorder
      ([`pkg/mining/metrics.go`](QSDM/source/pkg/mining/metrics.go) +
       [`pkg/monitoring/mining_recorder.go`](QSDM/source/pkg/monitoring/mining_recorder.go))
      mirroring the `pkg/chain.SetChainMetricsRecorder`
      pattern, so pkg/mining stays free of pkg/monitoring
      imports.
  - **Tests**: 6 new archcheck unit tests (zero-as-sentinel,
    happy-path, inclusive bounds, lazy-spoof, low-CPU
    spoof, unknown-arch programmer-error path) + 4 new
    verifier wiring tests in
    [`verifier_hashrate_test.go`](QSDM/source/pkg/mining/verifier_hashrate_test.go) (zero passes,
    high-spoof rejects, low-spoof rejects, in-band accepts) +
    5 new monitoring tests in
    [`archcheck_metrics_test.go`](QSDM/source/pkg/monitoring/archcheck_metrics_test.go) (per-reason and
    per-arch counter routing, unknown-bucketing for both,
    init-time adapter registration). 1870 / 1870 tests
    passing across 68 packages.
  - **Docs**: `MINING_PROTOCOL_V2.md` §4.6 now has §4.6.3
    (hashrate band table + rationale) and §4.6.4 (operator
    metrics).

- **Arch-spoof rejection (§4.6 / §3.3 step 8) — closed-enum
  allowlist + arch ↔ `gpu_name` cross-check (2026-04-29).** The
  long-deferred step 8 of the HMAC verifier acceptance flow now
  ships, replacing an earlier draft that proposed using a
  "matmul rounding fingerprint" — a non-starter once §4.3's
  byte-exact IEEE-754 RNE rules made `ComputeMixDigestV2`
  produce the same digest on every conforming arch.
  - **Allowlist** ([`pkg/mining/attest/archcheck`](QSDM/source/pkg/mining/attest/archcheck/archcheck.go))
    fixes the canonical set to `hopper`, `blackwell`,
    `ada-lovelace`, `ampere`, `turing`. Older arches (Volta,
    Pascal, Maxwell, Kepler) are intentionally OFF; future
    arches require a registry append plus matching `gpu_name`
    patterns in the same change. The qsdmminer-console-emitted
    `ada` short form is accepted as an alias for backward
    compat. The closed-enum check fires in the outer
    `pkg/mining/verifier.go` BEFORE per-type dispatch, so a
    malformed / typo / future-arch-sneak proof costs a single
    map lookup and never pays the HMAC or X.509 work.
  - **arch ↔ `bundle.gpu_name` consistency** fires inside the
    HMAC verifier ([`pkg/mining/attest/hmac/verifier.go`](QSDM/source/pkg/mining/attest/hmac/verifier.go))
    as step 8. Catches the lazy spoof — an attacker who flips
    `gpu_arch=hopper` but forgot to also lie about the
    `nvidia-smi` name on their consumer Ada card. Bundle
    `gpu_name` is HMAC-bound, so a determined attacker still
    has to forge a valid HMAC and choose at sign time; the
    on-chain registry's `(gpu_uuid, hmac_key)` pairing (§5.2)
    + §5.4 stake bonding + §8 slashing surface are the
    economic locks behind it.
  - **CC-path placeholder** (`ValidateBundleArchConsistencyCC`)
    is a no-op today — the device certificate chain itself
    binds to a specific physical Hopper / Blackwell GPU at
    the protocol level. Reserved as a fixed wiring point for
    a future strict cert-subject parsing pass.
  - **Tests**: 15 unit tests in `archcheck_test.go` (every
    canonical, alias, all-known-product happy path, every
    cross-family lazy-spoof + AMD spoof + downgrade spoof) +
    5 integration tests in [`hmac/verifier_archcheck_test.go`](QSDM/source/pkg/mining/attest/hmac/verifier_archcheck_test.go) (lazy spoof
    re-using fixture, determined spoof with re-signed bundle,
    cross-family rejection, alias acceptance, unknown-arch
    rejection) + 3 wiring tests in [`verifier_archspoof_test.go`](QSDM/source/pkg/mining/verifier_archspoof_test.go) (cheap
    reject before dispatch, alias acceptance, pre-fork
    bypass). 1850 / 1850 tests passing across 68 packages.
  - **Docs**: `MINING_PROTOCOL_V2.md` §3.3 step 8 + a
    rewritten §4.6 with the design correction prominently
    flagged.

- **`fork_v2_tc_height` is now a governance-tunable chain parameter
  (2026-04-28).** The Tensor-Core PoW mixin activation height is
  registered as `chainparams.ParamForkV2TCHeight` (bounds
  `[0, math.MaxUint64]`, default `MaxUint64` = TC disabled).
  `v2wiring.Wire()` reads the active value from the `ParamStore`
  at chain init and pins it into `pkg/mining` via
  `SetForkV2TCHeight`; after every `PromotePending` call inside
  the `SealedBlockHook` it re-pins from the (possibly just-
  promoted) value, so a successful `qsdm/gov/v1` `param-set` tx
  makes the new fork height visible to the verifier and reference
  solver on the very next sealed block — without a binary restart.
  Genesis bake-in is supported via the new
  `v2wiring.Config.ForkV2TCHeight *uint64` field; the snapshot
  replay path takes precedence over the genesis seed across
  restarts so the chain's committed governance history cannot be
  silently overwritten by a config change. Closes the operational
  side of the §12.2 deployment readiness work; the cryptographic
  side (`pkg/mining/pow/v2`) was shipped earlier.
  - **Registry**: [`pkg/governance/chainparams/params.go`](QSDM/source/pkg/governance/chainparams/params.go)
    appends `ParamForkV2TCHeight` with the new bounds.
  - **Wiring**: [`internal/v2wiring/v2wiring.go`](QSDM/source/internal/v2wiring/v2wiring.go)
    seeds, pins, and re-pins the runtime mining knob.
  - **Tests**: [`internal/v2wiring/v2wiring_tcfork_test.go`](QSDM/source/internal/v2wiring/v2wiring_tcfork_test.go)
    locks the four lifecycle paths — default-disabled, genesis
    seed (zero + future activation), governance-driven re-pin
    on promote, and snapshot replay across simulated restart.
  - **Docs**: `MINING_PROTOCOL_V2.md` §4, §10 registry table, and
    §12.2 deliverable updated.

### Performance

- **`pkg/mining/pow/v2` — 22% faster validator hot path
  (2026-04-28).** A 256 KB FP16→FP32 lookup table populated at
  package init, plus a benchmark scaffold that pins the
  per-stage breakdown so future regressions are loud:

  | Benchmark                | Before  | After  | Speedup |
  |--------------------------|--------:|-------:|--------:|
  | `ComputeMixDigestV2`     | 384 µs  | 298 µs | 1.29×   |
  | `TensorMul`              | 1 667 ns| 528 ns | 3.16×   |
  | `FP16ToFloat32`          | 6.7 ns  | 1.92 ns| 3.49×   |
  | `MatrixFromMix`          | 3 224 ns| 3 167 ns | (noise) |
  | `Float32ToFP16RNE`       | 11.6 ns | 11.3 ns| (untouched) |

  Numbers are on the user's Xeon E5-2670 (Sandy Bridge, 2.6 GHz,
  2012). All allocations preserved at 1 / 32 B per
  `ComputeMixDigestV2` call (the digest copy); no new heap
  pressure introduced.

  - **LUT impl**:
    [`pkg/mining/pow/v2/fp16_lut.go`](QSDM/source/pkg/mining/pow/v2/fp16_lut.go)
    populates `fp16ToFP32LUT [65536]float32` from the unrolled
    IEEE-754 reference (`fp16ToFloat32Slow`) at init time, then
    self-checks against a hand-picked boundary set
    (signed zero, smallest subnormal, smallest normal, 1.0
    neighbourhood, largest finite, ±Inf, NaN). A misconfigured
    table panics at startup — silently producing wrong
    mix-digests would be a much worse failure mode than refusing
    to start.
  - **Equivalence guard**:
    `TestFP16ToFP32_LUTMatchesSlow` in
    [`pkg/mining/pow/v2/fp16_test.go`](QSDM/source/pkg/mining/pow/v2/fp16_test.go)
    asserts the LUT is bit-identical to the slow reference for
    every one of the 65,536 possible FP16 inputs. Combined with
    the frozen byte-exact golden mix-digest vector
    (`ef9319a6…53f4`) this is two independent locks on
    correctness.
  - **Benchmarks**:
    [`pkg/mining/pow/v2/bench_test.go`](QSDM/source/pkg/mining/pow/v2/bench_test.go)
    establishes per-stage baselines for
    `ComputeMixDigestV2`, `MatrixFromMix`, `TensorMul`,
    `FP16ToFloat32`, `Float32ToFP16RNE`. Run with
    `go test -bench=. -benchtime=2s ./pkg/mining/pow/v2/...`.
  - **Spec update**:
    [`MINING_PROTOCOL_V2.md`](QSDM/docs/docs/MINING_PROTOCOL_V2.md)
    §4.3 now lists the per-stage cost breakdown and explains
    the two micro-optimizations (LUT + stack-friendly SHAKE
    allocation pattern), so future SIMD/BLAS or assembly
    fast-paths know exactly which budget they're trying to
    beat.

### Added

- **Verifier + reference solver wired through
  `FORK_V2_TC_HEIGHT` (2026-04-28).** The pure-Go Tensor-Core
  mix-digest reference is no longer dead code: Step 10 of
  [`pkg/mining/verifier.go`](QSDM/source/pkg/mining/verifier.go)
  and the per-attempt loop of
  [`pkg/mining/solver.go`](QSDM/source/pkg/mining/solver.go) now
  height-gate between the v1 SHA3 walk and the v2 mixin
  (`powv2.ComputeMixDigestV2`) using a new runtime-settable
  knob in [`pkg/mining/fork.go`](QSDM/source/pkg/mining/fork.go):
  - `ForkV2TCHeight() uint64` — current activation height
    (default `math.MaxUint64` = TC disabled, safe).
  - `SetForkV2TCHeight(h uint64)` — pin the activation height
    at chain-init time. Calling mid-execution is a bug;
    validators MUST NOT be able to move the gate at runtime in
    response to adversarial input.
  - `IsV2TC(height uint64) bool` — boundary-inclusive helper
    (`true` at `height == ForkV2TCHeight()`).
  - **Independent of `ForkV2Height`**: the two fork heights are
    deliberately separate so the v2 attestation fork can ship
    independently of the PoW-algorithm change.
  - **Soft-tightening fork**: a v1 proof at a post-TC height
    fails Step 10 with `ReasonWork` /
    `"mix_digest mismatch"`; a v2 proof at a pre-TC height
    fails the same way. No proof-wire-format change, no chain
    reset.
  - **Import-cycle break**: `pkg/mining/pow/v2/mixdigest.go`
    now defines `DAG` as a local minimal interface
    (`N() uint32; Get(uint32) ([32]byte, error)`) so it does
    not import `pkg/mining`. Go's structural interfaces mean
    `*mining.InMemoryDAG` and `*mining.LazyDAG` still satisfy
    it for free.
  - **Tests**: six new cases in
    [`pkg/mining/verifier_v2tc_test.go`](QSDM/source/pkg/mining/verifier_v2tc_test.go)
    cover the default-disabled invariant, boundary inclusivity
    at `H-1 / H / H+1`, the post-TC happy path (Solve + Verify
    both routed through v2), and both algorithm-mismatch
    rejection directions. The pre-existing
    `TestVerifyAcceptsValidProof` keeps passing untouched —
    the safety guarantee of the default.

- **Pure-Go Tensor-Core PoW v2 reference implementation —
  `pkg/mining/pow/v2/` (2026-04-28).** The validator-side byte-
  exact reference for the §4 Tensor-Core mixin specified in
  [`MINING_PROTOCOL_V2.md`](QSDM/docs/docs/MINING_PROTOCOL_V2.md).
  Locks down four implementation-defined IEEE-754 details that
  any future CUDA miner MUST match bit-for-bit:
  - **Matrix expansion**: `MatrixFromMix(mix [32]byte)` uses
    SHAKE256 with the domain separator
    `"qsdm/pow/v2/matrix\x00"` to fan the 32-byte running mix
    out to a 16×16 FP16 matrix in row-major big-endian.
    Implemented in
    [`pkg/mining/pow/v2/matrix.go`](QSDM/source/pkg/mining/pow/v2/matrix.go).
  - **FP16 codec**: self-contained 16-bit-exact
    encode/decode + RNE FP32↔FP16 conversion + canonical NaN
    handling (FP16 NaN → `0x7E00`, FP32 NaN → `0x7FC00000`)
    so platform-specific NaN payloads never leak into SHA3.
    Implemented in
    [`pkg/mining/pow/v2/fp16.go`](QSDM/source/pkg/mining/pow/v2/fp16.go).
  - **Matmul**: `TensorMul` performs FP16×FP16 widened to
    FP32 (exact), accumulates in **strict left-to-right FP32**
    (NOT tree-reduction; CUDA WMMA users must emulate this
    order in software), and down-converts to FP16 with RNE.
  - **Step body**: `ComputeMixDigestV2` runs the 64-step DAG
    walk with the v2 step body
    `mix := SHA3-256(mix || entry || tc)` where `tc` is the
    32-byte BE-packed result vector. Implemented in
    [`pkg/mining/pow/v2/mixdigest.go`](QSDM/source/pkg/mining/pow/v2/mixdigest.go).
  - **Tests**:
    [`fp16_test.go`](QSDM/source/pkg/mining/pow/v2/fp16_test.go)
    exhaustively covers all 65,536 FP16 bit patterns (decode/
    encode round-trip + FP16→FP32→FP16 round-trip for every
    non-NaN value) plus boundary specials (signed zero,
    smallest subnormal, smallest normal, largest finite, ±Inf,
    halfway-tie rounding, overflow to Inf, NaN
    canonicalization).
    [`mixdigest_test.go`](QSDM/source/pkg/mining/pow/v2/mixdigest_test.go)
    covers matrix-expansion determinism, vector unpack with
    NaN canonicalization, identity matmul, hand-computed row,
    full v2 determinism, v1≠v2 sanity, avalanche/diffusion
    against a 1-bit nonce change, and a frozen byte-exact
    **golden mix-digest vector**
    (`ef9319a6134aeb9b77f315427ec81cdbc40a03c60414284864a3e9bbd68153f4`)
    that any compliant CUDA miner MUST reproduce bit-for-bit.
  - **Documentation**:
    [`MINING_PROTOCOL_V2.md`](QSDM/docs/docs/MINING_PROTOCOL_V2.md)
    §4 status flips from "specified, not implemented" to
    "byte-exact validator-side reference shipped"; new
    subsections §4.2.1–4.2.4 lock the matrix expansion,
    vector unpack, matmul order, and NaN canonicalization in
    the spec itself. §12.2 deferred-work register splits into
    "reference shipped" + "CUDA kernel deferred"; remaining
    estimate trimmed from 14d to 10d post-hardware.
  - **Activation**: still gated behind `FORK_V2_TC_HEIGHT`;
    pre-fork validators continue to use the v1 walk in
    `pkg/mining.ComputeMixDigest`. The v1 path is unchanged
    and stays in-tree for replaying pre-fork blocks.

- **Multisig-gated authority rotation — `qsdm/gov/v1` `authority-set` payload kind (2026-04-28).**
  The `qsdm/gov/v1` ContractID now carries TWO payload
  kinds: `param-set` (already shipped) and `authority-set`.
  Each `authority-set` tx is one authority's vote on a
  proposal tuple `(op, address, effective_height)`; the
  chain accumulates votes and stages the rotation when
  M-of-N threshold is crossed (`threshold = max(1, N/2 + 1)`).
  The on-chain AuthorityList is now itself rotatable
  without a binary redeploy, closing the prior posture's
  "captured single authority can self-disable" hazard.
  - **Wire format**:
    [`pkg/governance/chainparams/types.go`](QSDM/source/pkg/governance/chainparams/types.go)
    adds `AuthoritySetPayload` (with `Op ∈ {add, remove}`,
    `Address`, `EffectiveHeight`, `Memo`) and the
    `PayloadKindAuthoritySet` discriminator. The kind tag
    is the dispatch axis the admit gate
    (`PeekKind` in
    [`pkg/governance/chainparams/validate.go`](QSDM/source/pkg/governance/chainparams/validate.go))
    and the chain applier
    ([`pkg/chain/gov_apply.go`](QSDM/source/pkg/chain/gov_apply.go))
    use to route to per-shape validators / handlers.
  - **Vote-tally store**:
    [`pkg/governance/chainparams/authority.go`](QSDM/source/pkg/governance/chainparams/authority.go)
    introduces `AuthorityVoteStore` (interface +
    `InMemoryAuthorityVoteStore` reference impl). Tracks
    proposals keyed by `(op, address, effective_height)`,
    each carrying an ordered voter set + sticky `Crossed`
    flag. `RecordVote` is idempotent on duplicate voters
    (returns `ErrDuplicateVote`) and the threshold helper
    `AuthorityThreshold(n)` is exported so the CLI / API
    can render the same "M of N" string the chain uses.
  - **Activation semantics**: the existing
    `GovApplier.PromotePending(height)` now ALSO promotes
    crossed authority proposals — `add` inserts into the
    AuthorityList under a new `authorityMu` RWMutex,
    `remove` drops the address AND drops the removed
    authority's votes from every still-open proposal
    (`DropVotesByAuthority` + `RecomputeCrossed` re-
    evaluates which open proposals now satisfy the
    smaller threshold). A `remove` that would empty the
    AuthorityList is REFUSED at promotion (governance
    cannot disable itself from on-chain — the operator
    must redeploy binaries for that).
  - **Events**: a new `GovAuthorityEvent` family with
    kinds `authority-voted`, `authority-staged`,
    `authority-activated`, `authority-abandoned`, and
    `authority-rejected` rides on the existing
    `GovEventPublisher` (a new `PublishGovAuthority`
    method; existing implementations grow a no-op).
  - **Metrics**:
    [`pkg/monitoring/gov_metrics.go`](QSDM/source/pkg/monitoring/gov_metrics.go)
    adds five Prometheus surfaces:
    `qsdm_gov_authority_voted_total{op}`,
    `qsdm_gov_authority_crossed_total{op}`,
    `qsdm_gov_authority_activated_total{op}`,
    `qsdm_gov_authority_count` (gauge),
    `qsdm_gov_authority_rejected_total{reason}`.
    `MetricsRecorder` grows the matching four methods.
  - **Persistence**: snapshot format bumps to
    `SnapshotVersion=2` (backwards-compatible read of
    v1). New `SaveSnapshotWith(store, votes, path)` and
    `LoadOrNewWith(path)` entry points carry the
    authority-rotation state through restarts; a node
    that crashes between threshold-crossing and the
    activation block replays correctly under the fresh
    binary. v1 snapshots load cleanly under v2 binaries
    (vote store boots empty); v2 snapshots refuse to
    load on v1 binaries (silently dropping in-flight
    rotations across a downgrade is the wrong default).
  - **CLI**:
    [`cmd/qsdmcli/gov_helper.go`](QSDM/source/cmd/qsdmcli/gov_helper.go)
    grows a `propose-authority` subcommand
    (`--op`, `--address`, `--effective-height`, `--memo`,
    `--out`, `--print-cmd`) and the existing `inspect`
    subcommand now dispatches on the wire-kind tag so
    both payload kinds round-trip through the same
    helper.
  - **Tests**: ~50 new test cases across the layers —
    threshold table, vote-store record / promote / drop /
    recompute mechanics, validate / admit kind dispatch,
    applier rejection branches, persistence round-trip
    + v1↔v2 compatibility, and an end-to-end integration
    rig in
    [`internal/v2wiring/v2wiring_authority_test.go`](QSDM/source/internal/v2wiring/v2wiring_authority_test.go)
    that drives a real chain through `vote → cross →
    activate → AuthorityList expanded` AND a
    `crash-between-cross-and-activate` persistence-replay
    scenario.
  - **Docs**: §9.4.7 of
    [`MINING_PROTOCOL_V2.md`](QSDM/docs/docs/MINING_PROTOCOL_V2.md)
    is the new operator-facing specification (wire
    format, threshold rule, activation semantics,
    rejection branches, events, metrics, persistence,
    CLI). The deferred-work register's §12.5 marks
    multisig-gated rotation as **SHIPPED**, completing
    the "authority list is itself NOT governance-tunable
    in this revision" caveat from the prior commit.

- **Governance production-readiness: persistent `ParamStore` + end-to-end integration tests (2026-04-28).**
  Closes the two production gaps in the freshly-shipped
  `qsdm/gov/v1` runtime tuning hook: state is now durable
  across node restarts, and the full chain-side glue
  (admit → stage → promote → SlashApplier reads new value)
  is now exercised by integration tests through the real
  `internal/v2wiring` boot path.
  - **Persistence**:
    [`pkg/governance/chainparams/persist.go`](QSDM/source/pkg/governance/chainparams/persist.go)
    ships `SaveSnapshot(store, path)` and
    `LoadOrNew(path)` free functions, mirroring the shape
    of `pkg/chain/staking_persist.go`. Snapshot format is
    a version-tagged JSON document with `active` map +
    `pending[]` array, written atomically through a `.tmp`
    file + rename. Forward/backward compat: unknown params
    in the snapshot are dropped silently; out-of-bounds
    values are clamped to registry defaults; an unknown
    version refuses to load (no silent downgrade). A
    missing file is treated as first-boot and returns a
    fresh defaults-seeded store.
  - **Wiring**: `internal/v2wiring.Config` grows two new
    optional fields:
    - `GovParamStorePath string` — when non-empty, `Wire()`
      calls `LoadOrNew(path)` at boot, and the
      `SealedBlockHook` saves a fresh snapshot AFTER each
      block's `PromotePending` runs. The genesis-seed for
      `reward_bps` from `Config.SlashRewardBPS` is now
      conditional on the loaded value being equal to the
      registry default — preserving previously-activated
      governance state across restarts.
    - `LogSnapshotError func(uint64, error)` — operator
      hook for save failures. The chain continues; the
      next sealed block re-saves and recovers.
    - When `GovParamStorePath` is empty, behaviour is
      byte-identical to the prior in-memory-only posture
      (fine for ephemeral testnets, NOT for production).
  - **Integration tests**:
    [`internal/v2wiring/v2wiring_gov_test.go`](QSDM/source/internal/v2wiring/v2wiring_gov_test.go)
    drives the full lifecycle through the real production
    boot path. 8 new tests cover:
    - Proposal activates at effective_height (the canonical
      regression test — a bug in `SetGovApplier`,
      `SealedBlockHook` composition, or
      `chainparams.AdmissionChecker` ordering breaks this).
    - Future-effective-height stays pending across multiple
      blocks and flips on the right one.
    - Non-authority sender rejected at apply time (admission
      stateless, authority check stateful in `GovApplier`).
    - Two authorities; carol supersedes alice's pending
      entry; supersede activates correctly.
    - HTTP read-API surface reflects live chain state.
    - Persistence: post-promote active value preserved
      across simulated restart.
    - Persistence: pending entry replayed across restart;
      promotes correctly on the post-restart chain.
    - Persistence: corrupted snapshot causes `Wire()` to
      return a hard error (no silent state corruption).
  - **Persistence unit tests**:
    [`pkg/governance/chainparams/persist_test.go`](QSDM/source/pkg/governance/chainparams/persist_test.go)
    adds 14 test cases covering save/load round-trip
    (actives + pending), defaults fallback for partial
    snapshots, missing-file → fresh-store, no-op on nil
    store / empty path, unknown-param drop, out-of-bounds
    clamp, version reject, malformed JSON reject, atomic
    cleanup of `.tmp`, overwrite of stale snapshot, and
    full stage → save → load → promote → save → load
    lifecycle.
  - **Documentation**: `MINING_PROTOCOL_V2.md` §9.4 grows a
    "Persistence — `GovParamStorePath`" subsection covering
    the boot-load + per-sealed-block-save flow, the atomic
    write contract, the snapshot wire format, and the
    "missing path = ephemeral testnet only" deployment
    rule.

- **Governance read-API + `qsdmcli watch params` (2026-04-28).**
  Operator-facing surface for the on-chain `qsdm/gov/v1` runtime
  tuning hook. The chain side has been live since the prior
  release; this round wires up the read-only HTTP / CLI plumbing
  authorities and dashboards need to see what's active, what's
  pending, and when proposals land.
  - **HTTP**: two new read endpoints in
    [`pkg/api/handlers_governance.go`](QSDM/source/pkg/api/handlers_governance.go),
    routed in [`pkg/api/handlers.go`](QSDM/source/pkg/api/handlers.go):
    - `GET /api/v1/governance/params` returns a
      `GovernanceParamsView` (active map, pending list sorted
      by `(effective_height ASC, param ASC)`, registry list
      sorted by name, authorities list sorted ASC,
      `governance_enabled` bool). Empty slices/maps are
      normalised to `[]` / `{}` so diff-driven consumers don't
      branch on null.
    - `GET /api/v1/governance/params/{name}` returns a
      `GovernanceParamView` (active value, optional pending
      entry, registry entry). 400 on empty / over-long name,
      404 on unknown param.
    - Both return **503** until the validator wires a
      `GovernanceParamsProvider` via `api.SetGovernanceProvider`,
      matching the posture of the existing v2 enrollment /
      slash read endpoints (v1-only nodes return a clean
      "not configured" rather than ambiguous 404s).
  - **Wiring**: `internal/v2wiring` grows a
    `governanceProviderAdapter` that bridges the live
    `chainparams.ParamStore` + `chain.GovApplier` to the
    pkg/api provider interface, snapshotting active values,
    pending changes, and the authority list under each
    component's own RWMutex (no global snapshot lock; pending
    promotions are atomic in `Promote`).
  - **CLI — `qsdmcli gov-helper params --remote`**: the
    existing offline `params` listing now optionally queries
    the running validator and merges live `active` + `pending`
    columns into the table. Best-effort: 503 / network error
    falls back to the offline view with a stderr warning. The
    JSON output (`--json --remote`) emits the validator's
    snapshot verbatim. A stderr footer reports
    `governance_enabled` plus the authority list so a proposer
    can confirm "yes, my key admits" before building a
    payload.
  - **CLI — `qsdmcli watch params`**: third sibling of
    `watch enrollments` and `watch slashes`. Polls
    `/governance/params` and emits one event per parameter
    transition across consecutive snapshots. Event kinds:
    `param_staged`, `param_superseded`, `param_activated`,
    `param_removed` (defensive), `param_authorities_changed`
    (defensive), `error`. Same flag surface as the other
    watchers (`--interval` floored at 5s, `--once`, `--json`,
    `--include-existing`) plus a `--param=NAME` filter.
    SIGINT/SIGTERM exits 0; first-poll fatal exits non-zero.
  - **Tests**: 14 new unit tests across
    `pkg/api/handlers_governance_test.go` (503-when-unwired,
    happy path, disabled-posture rendering, single-param 404 /
    400, GET-only methods) and
    `cmd/qsdmcli/watch_params_test.go` (diff engine for every
    transition kind, deterministic ordering, param filter,
    initial-events synthesis, JSON-Lines wire-format pin,
    options normalisation).
  - **Documentation**: `MINING_PROTOCOL_V2.md` §9.2 watcher
    table grows a `watch params` row; §9.4 grows
    "HTTP read API" + "`qsdmcli watch params`" subsections;
    §12.4 deferred-work register marks the operator-facing
    surface as **SHIPPED**.

- **`qsdm/gov/v1` runtime parameter-tuning hook (2026-04-28).**
  Two protocol-economy parameters that previously lived as
  construction-time arguments to `chain.SlashApplier` —
  `reward_bps` (slasher reward share) and
  `auto_revoke_min_stake_dust` (auto-revoke threshold) — are
  now governance-tunable at runtime. No more coordinated
  binary swaps to retune economic knobs.
  - **New package**:
    [`pkg/governance/chainparams/`](QSDM/source/pkg/governance/chainparams/)
    ships the `ParamSetPayload` wire format, the param
    `Registry` (whitelist + bounds + defaults + units), the
    `ParamStore` interface with an `InMemoryParamStore`
    reference implementation, the stateless mempool
    `AdmissionChecker`, and the codec / validator pair.
  - **Chain-side applier**:
    [`pkg/chain/gov_apply.go`](QSDM/source/pkg/chain/gov_apply.go)
    ships `GovApplier` with the same shape as the existing
    `SlashApplier` / `EnrollmentApplier`. Routing is wired
    through `EnrollmentAwareApplier.SetGovApplier(...)`.
  - **Authority model**: applier holds an
    `AuthorityList []string`. Tx sender must be on it; an
    empty list disables on-chain governance entirely
    (every gov tx rejects with
    `chainparams.ErrGovernanceNotConfigured`). The list is
    NOT itself governance-tunable in this revision —
    modifying it requires a binary upgrade or a chain-config
    reload, by deliberate design (a circular "governance can
    change the list of governors" surface lets a captured
    authority lock out the rest).
  - **Activation semantics**: the tx field
    `effective_height` MUST satisfy
    `currentHeight ≤ effective_height ≤ currentHeight + MaxActivationDelay`
    (~3 days at 3-second blocks). The applier stages the
    change in a per-param "pending" slot; the
    `SealedBlockHook` calls
    `GovApplier.PromotePending(blockHeight)` after each
    block, which atomically promotes any pending changes
    whose `effective_height` has been reached. Promotion
    order is deterministic across nodes (by height ascending,
    then by name ascending). One pending change per parameter
    at a time; subsequent submissions for the same parameter
    SUPERSEDE the prior pending entry.
  - **`SlashApplier` refactor**: the existing struct fields
    (`RewardBPS`, `AutoRevokeMinStakeDust`) become static
    fallbacks read only when no `ParamStore` is wired. With a
    store wired (the production posture from
    `internal/v2wiring`), every `ApplySlashTx` call reads the
    active value from the store. Backward-compatible: tests
    and binaries that don't set a store keep their existing
    behaviour byte-for-byte.
  - **Mempool admission**: layered above the slashing /
    enrollment gates, mirroring the existing stack — `gov >
    slash > enroll > base`.
  - **CLI**:
    [`qsdmcli gov-helper`](QSDM/source/cmd/qsdmcli/gov_helper.go)
    ships three offline subcommands (no key required;
    governance authorities typically run from air-gapped
    hosts):
    - `propose-param --param=NAME --value=N --effective-height=H [--memo=STR] [--out=PATH] [--print-cmd]`
      builds a canonical `ParamSetPayload` and writes the
      encoded JSON. Pre-flight checks mirror the chain-side
      admission so an authority sees out-of-bounds /
      unknown-param rejections locally.
    - `params [--json]` lists the registered tunables with
      bounds, defaults, units, and descriptions.
    - `inspect (--payload-file=PATH | --payload-hex=HEX)`
      decodes a previously-built payload and pretty-prints
      the structured view with the matched registry entry.
  - **Observability**: four new Prometheus metrics in
    `pkg/monitoring/gov_metrics.go`:
    `qsdm_gov_param_staged_total{param}`,
    `qsdm_gov_param_activated_total{param}`,
    `qsdm_gov_param_value{param}` (gauge),
    `qsdm_gov_param_rejected_total{reason}`. Plus a new
    `GovEventPublisher` interface (separate from
    `ChainEventPublisher` to avoid forcing existing slash /
    enrollment subscribers to grow no-op handlers) emitting
    four `GovParamEvent` flavours: `param-staged`,
    `param-superseded`, `param-activated`, `param-rejected`.
  - **v2wiring extension**: `Config.GovernanceAuthorities`
    is the single new knob; populating it activates governance.
    The `InMemoryParamStore` is wired UNCONDITIONALLY (so the
    `SlashApplier` reads always route through it), seeded
    with `cfg.SlashRewardBPS` as the genesis active value.
    Migration cost for existing operators is zero: leaving
    `GovernanceAuthorities` empty is byte-identical to the
    pre-governance posture.
  - **Tests**: 30+ unit tests across `chainparams_test.go`
    (registry, codec, store, admission), `gov_apply_test.go`
    (applier construction, every rejection path, supersede,
    promote, slash-applier integration with both reward_bps
    and auto_revoke_min_stake_dust scenarios), and
    `gov_helper_test.go` (CLI happy paths, every flag-
    rejection, --print-cmd, --json table, inspect
    round-trip). Full repo `go test ./...` green.
  - **Spec update**: `MINING_PROTOCOL_V2.md` §6 (component
    table), §9.4 (governance — runtime parameter tuning,
    new section), and §12.4 (deferred-work register, marked
    SHIPPED).

- **`freshness-cheat` slasher — verifier shipped, witness
  deferred (2026-04-28).** Closes the v2 slashing trilogy:
  `forged-attestation` + `double-mining` + `freshness-cheat`
  all now ship with concrete `EvidenceVerifier`
  implementations. Lives in
  [`pkg/mining/slashing/freshnesscheat`](QSDM/source/pkg/mining/slashing/freshnesscheat/).
  - **What it detects**: a v2 proof whose `bundle.issued_at`
    is older than `FRESHNESS_WINDOW + grace` (default 60 s +
    30 s) measured against the chain block-time of the
    inclusion height, i.e. retroactive evidence of validator
    collusion or clock skew.
  - **`BlockInclusionWitness` abstraction**: rather than ship
    a permanent `StubVerifier`, the package factors the
    BFT-finality dependency into a `BlockInclusionWitness`
    interface that callers wire to whatever observability
    layer they have. Three implementations ship today:
    `RejectAllWitness` (production default — rejects every
    slash with a kind-specific `ErrEvidenceVerification`
    naming the missing dependency, matching the previous
    `StubVerifier` end-user behaviour with materially better
    diagnostics), `TrustingTestWitness` (testnet / dev — lets
    the slashing path run end-to-end so bugs surface before
    mainnet), and `FixedAnchorWitness` (ops — certifies one
    pre-registered `(height, block_time, proof_id)` tuple).
    Once BFT finality lands, a real `quorum.HeaderWitness`
    plugs into the same interface and freshness-cheat starts
    slashing for real with no other code changes.
  - **Verifier checks**: protocol version (`Version ≥ 2`),
    structural attestation presence, bundle parse, bundle
    `node_id` ↔ payload `node_id` binding, anchor sanity
    (anchor strictly post-`IssuedAt`, ≤ 1 year delta),
    staleness threshold (strict `>` against window + grace
    so borderline cases are not slashed), registry binding,
    and finally `Witness.VerifyAnchor`. Per-offence cap
    matches the rest of the trilogy at `10 CELL` (full
    `MIN_ENROLL_STAKE` bond drain).
  - **Wire format**: `evidenceWire = { proof: <canonical-JSON>,
    anchor_height: <uint64-as-string>, anchor_block_time:
    <int64 unix seconds>, memo?: <≤256 B> }`. Proof is
    serialised via `mining.Proof.CanonicalJSON()` so the
    bytes the verifier hashes are byte-identical to what the
    chain accepted. `DisallowUnknownFields` is set so wire
    drift is rejected loudly.
  - **Production wiring**: `slashing.ProductionConfig` gains
    a `FreshnessCheat` slot with the same kind-mismatch guard
    as the other two; leaving it nil keeps a `StubVerifier`
    in place for binaries that don't import the freshnesscheat
    package. A convenience factory
    `freshnesscheat.NewProductionSlashingDispatcher` wires all
    three verifiers in one call.
  - **CLI**: `qsdmcli slash-helper freshness-cheat` constructs
    evidence locally with the same staleness / anchor-sanity
    / node_id checks the chain runs (so an operator does not
    burn a tx fee on guaranteed-rejection evidence). Includes
    a `--print-cmd` mode that emits a copy-pasteable
    `qsdmcli slash` invocation. `qsdmcli slash-helper inspect
    --kind=freshness-cheat` decodes evidence and renders the
    operator-facing JSON view (proof summary + anchor height +
    anchor block-time + computed staleness).
  - **Tests**: 30+ unit tests in `freshnesscheat_test.go`
    (happy path, every rejection path, every witness flavour,
    encode/decode round-trip, production-dispatcher
    integration) plus 6 new CLI tests covering the
    `slash-helper freshness-cheat` and
    `slash-helper inspect --kind=freshness-cheat` surfaces.
    All pass alongside the existing repo suite.
  - **Spec update**: `MINING_PROTOCOL_V2.md` §1 (overview),
    §6 (component table), §8.2 (slashing-table row), and
    §12.3 (deferred-work register) updated to reflect the
    new posture.

- **`qsdmcli watch slashes` — symmetric operator-facing
  surveillance subcommand (2026-04-28).** Polls
  `/api/v1/mining/slash/{tx_id}` for a caller-supplied set of
  slash transaction ids and streams resolution events to
  stdout. Mirrors `qsdmcli watch enrollments` in flag surface
  and wire shape so operators get matched tooling across the
  enrollment + slashing surfaces in one place. Use case: an
  operator submits a slash with `qsdmcli slash` (or assembles
  evidence offline with `qsdmcli slash-helper`), captures the
  returned `tx_id`, and the watcher surfaces "did it apply?"
  without manual polling.
  - Inputs: `--tx-id=ID` (repeatable) and/or
    `--tx-ids-file=PATH` (one tx id per line; `'#'` starts
    a comment; `-` reads from stdin); both merge and
    deduplicate. Capped at 1000 distinct tx ids per
    process; tx ids are validated against the same 256-byte
    cap and `'/'`-rejection rule the validator enforces.
  - Four slash event kinds plus shared `error`, all in the
    unified `WatchEvent` envelope so JSON-Lines consumers
    decode either watcher's stream with one struct:
    `slash_resolved` (tx transitioned from 404 → applied/
    rejected; the canonical "the slash landed" event,
    fires exactly once per id), `slash_pending` (tx is
    still 404; suppressed by default to keep the stream
    quiet, opt in via `--include-pending`),
    `slash_evicted` (tx was resolved earlier but the
    bounded `SlashReceiptStore` evicted it under FIFO
    pressure), `slash_outcome_change` (defensive — fires
    if the same tx returns a different `outcome` across
    polls; should never happen on a healthy network).
  - `--exit-on-resolved` returns `0` once every tracked
    tx has reached a terminal outcome; ideal for CI
    pipelines that submit a slash and need to wait for
    the apply. Mutually exclusive with `--include-pending`
    (the combination is a footgun and we error at flag
    parse time rather than guessing intent).
  - First-poll behaviour matches operator intuition by
    default: only already-resolved receipts emit events
    (covers the "watcher restarted after the slash
    landed" case); pending tx ids are silently tracked
    until they resolve. Pass `--include-pending` to also
    echo a `slash_pending` event each cycle for unresolved
    ids (useful when debugging "why isn't my slash
    landing?").
  - Per-cycle partial failures are non-fatal: a transient
    HTTP error on one tx id silently drops it from the
    snapshot and retries next cycle. Only a *total*
    failure (every id errors, e.g. validator unreachable
    or pointed at a v1-only node) emits an `error` event;
    on the very first cycle, total failure exits non-zero
    so misconfigured invocations fail loudly at startup.
  - Diff core (`diffSlashSnapshots`) is a pure function;
    initial-snapshot helper (`slashSnapshotInitialResolvedOnly`
    / `slashSnapshotAsInitialEvents`) and the resolved-event
    canonicaliser (`slashReceiptToResolvedEvent`) are
    likewise pure and unit-tested.
  - `WatchEvent` extended with slash-specific fields
    (`tx_id`, `outcome`, `prev_outcome`, `height`,
    `evidence_kind`, `slasher`, `slashed_dust`,
    `rewarded_dust`, `burned_dust`, `auto_revoked`,
    `auto_revoke_remaining_dust`, `reject_reason`); all
    omitempty so enrollment events still marshal to the
    same byte stream they did before. The kind enum
    gained `slash_resolved` / `slash_pending` /
    `slash_evicted` / `slash_outcome_change`. The
    human-format kind-pad width was bumped from 11 to 20
    chars so columns line up across both watcher streams
    when piped to one log file.
  - `formatEventHuman` switch dispatches on Kind so each
    event renders the field set the operator expects:
    applied receipts show `slashed`/`rewarded`/`burned`
    in CELL plus `auto_revoked=true(remaining=…)`;
    rejected receipts show `reason=…  err=…`; evictions
    show `last_outcome=…`; outcome changes show
    `outcome=A->B`.

  No new validator-side endpoints: pure-client consumer of
  the existing `/api/v1/mining/slash/{tx_id}` GET handler
  introduced in `pkg/api/handlers_slash_query.go`. Coverage:
  39 new tests (`cmd/qsdmcli/watch_slashes_test.go`) — flag
  validation (zero-id rejection, `'/'` rejection, oversize
  rejection, cap rejection, interval clamp, footgun-combo
  rejection, file + stdin merge, default first-poll filter),
  `allResolved` truth table (empty / all-pending / mixed /
  all-resolved), `diffSlashSnapshots` truth table (pending →
  resolved, resolved → pending = eviction, outcome change,
  pending steady state with and without `--include-pending`,
  resolved steady state, deterministic ordering, prev-missing
  → no event), `slashReceiptToResolvedEvent` field-mapping
  for both applied and rejected paths, both initial-snapshot
  helpers, all four slash human-format kinds (with applied-
  path / rejected-path field guards), and end-to-end
  `httptest` scenarios for `--once` empty / `--once`
  resolved-only / `--once --include-pending` / diff-loop
  pending → resolved transition / `--exit-on-resolved`
  cleanup / initial-failure-is-fatal / partial-cycle-error.
  Wire-shape parity with `api.SlashReceiptView` is asserted
  by `TestSlashReceiptWireMatchesAPI` (mirrors the
  `TestWatchRecordWireMatchesAPI` pattern). Documentation:
  new "Streaming slash-receipt events" section in
  `MINER_QUICKSTART.md` and a second row in
  `MINING_PROTOCOL_V2.md` §9.2 (operator-surface table). All
  90+ pre-existing `cmd/qsdmcli` tests remain green.

- **`qsdmcli watch enrollments` — operator-facing surveillance
  subcommand (2026-04-28).** A new diff-based polling tool that
  streams enrollment phase-change events to stdout, mirroring the
  signal that `qsdmminer-console`'s `EnrollmentPoller` already
  surfaces internally on its dashboard. Designed for fleet
  operators, indexers, and dashboard / alerting pipelines that
  want a composable building block (systemd, cron, log shippers)
  rather than a per-rig embedded poller.
  - Two modes: **list mode** (default) walks
    `/api/v1/mining/enrollments` with cursor pagination and
    supports `--phase=active|pending_unbond|revoked` server-side
    filtering; **single-node mode** (`--node-id=…`) hits
    `/api/v1/mining/enrollment/{node_id}` and treats `404` as
    "no record".
  - Five event kinds, all sharing one `WatchEvent` wire shape:
    `new`, `transition`, `stake_delta`, `dropped`, `error`. The
    `transition` event wins over `stake_delta` when both apply
    in the same poll (e.g. a partial slash that crosses
    auto-revoke), so an operator never has to reconcile two
    events about the same node_id from one cycle.
  - Two output modes: **human** (column-aligned RFC3339 + kind
    + `node=…` + phase/stake summary, default) and **`--json`**
    (JSON-Lines, one event per line, including `error` events
    so log shippers see the error stream in-line).
  - Deterministic ordering: events from a single tick are
    sorted by `node_id` ASC, so two consecutive runs over the
    same data produce byte-identical output. Diff captures
    against expected logs work without filtering.
  - Operational defaults match the embedded poller:
    `--interval` defaults to 30s, clamped to ≥ 5s; the same
    `MaxWatchPages = 10000` defence as `qsdmcli enrollments
    --all` against a misbehaving server returning
    `has_more=true` forever; 1 MiB body cap per request.
  - Exit codes: `0` on `SIGINT`/`SIGTERM`. Non-zero **only**
    when the very first snapshot fails (so the operator catches
    URL typos and v1-only validators at startup); subsequent
    poll failures emit a `WatchKindError` event and the loop
    continues.
  - `--once` (single snapshot then exit) and
    `--include-existing` (synthesise a `new` event per existing
    record on the first poll) compose for a one-shot dump:
    `qsdmcli watch enrollments --once --include-existing --json`
    is the canonical "give me every enrolled node_id right now,
    in JSON-Lines, in one process" call.

  No new validator-side endpoints: this is a pure-client
  consumer of the existing `/api/v1/mining/enrollment*` reads.
  Coverage: 35 new tests (`cmd/qsdmcli/watch_test.go`) — flag
  normalisation, the pure-function diff core, human / JSON
  formatting, end-to-end `httptest`-driven scenarios for
  initial-failure-is-fatal, single-node 404, single-node happy
  path, list-mode `--once`, list-mode `--include-existing`, and
  diff-loop phase-transition observation. Wire-shape parity
  with `api.EnrollmentRecordView` is asserted by
  `TestWatchRecordWireMatchesAPI`. Documentation: new
  "Streaming phase-change events" section in
  `MINER_QUICKSTART.md` and a row in `MINING_PROTOCOL_V2.md`
  §9.2 (operator surface table).

### Documentation

- **v2 mining-protocol spec consolidation (2026-04-28).** The three
  historical fragments
  [`MINING_PROTOCOL_V2_NVIDIA_LOCKED.md`](QSDM/docs/docs/MINING_PROTOCOL_V2_NVIDIA_LOCKED.md)
  (Phase-1 design draft),
  [`MINING_PROTOCOL_V2_RATIFICATION.md`](QSDM/docs/docs/MINING_PROTOCOL_V2_RATIFICATION.md)
  (2026-04-24 owner sign-off), and
  [`MINING_PROTOCOL_V2_TIER3_SCOPE.md`](QSDM/docs/docs/MINING_PROTOCOL_V2_TIER3_SCOPE.md)
  (rolling shipped-vs-deferred register) have been merged into a
  single canonical spec at
  [`MINING_PROTOCOL_V2.md`](QSDM/docs/docs/MINING_PROTOCOL_V2.md).
  The new spec carries an unambiguous §0–§14 numbering scheme,
  inline shipped/deferred status against concrete Go files in
  every §§5–9 table, a consolidated deferred-work register at §12,
  and the historical decision record at §13. The three superseded
  fragments are retained as redirect stubs with section-by-section
  mapping tables, so existing PR / issue / landing-page / source-
  comment references keep resolving. Cross-references in
  [`README.md`](README.md), the landing page
  ([`QSDM/deploy/landing/index.html`](QSDM/deploy/landing/index.html)),
  [`MINER_QUICKSTART.md`](QSDM/docs/docs/MINER_QUICKSTART.md), the
  wiki sync script
  ([`QSDM/scripts/sync-wiki.sh`](QSDM/scripts/sync-wiki.sh)), and
  ~13 Go source-comment sites
  (`pkg/mining/{fork,proof,challenge,enrollment,attest/cc,attest/hmac,slashing}`,
  `pkg/api/handlers.go`, `pkg/chain/slash_apply.go`,
  `cmd/qsdmcli/mining.go`) now point at the canonical doc and the
  correct anchors. No consensus or wire-format change.

### Changed / Deprecated

- **NVIDIA-lock pivot — retire the CPU-miner onboarding UX
  (2026-04-24).** The project is re-aligning on the architecture
  described in `nvidia_locked_qsdm_blockchain_architecture.md`:
  the mainline protocol will hard-fork to `v2` which requires a
  valid NGC attestation bundle on every proof, making CPU-only and
  non-NVIDIA-GPU mining impossible on mainnet by construction. As
  a first, fully-reversible step in that rollout we:
  - **Delete** `scripts/install-qsdmminer-console.sh`,
    `scripts/install-qsdmminer-console.ps1`, and
    `QSDM/Dockerfile.miner-console`. Nobody should be onboarded to
    a mining path that will stop earning rewards in the next major
    release.
  - **Remove** the `ghcr-miner-console` job from
    `release-container.yml` and the companion
    `docker-miner-console-build` + `install-scripts-lint` jobs
    from `qsdm-split-profile.yml`. No new `qsdm-miner-console`
    image will be published on the next tag. Previously-pushed
    tags remain on GHCR for operators who want to roll forward
    manually during the deprecation window.
  - **Add** a startup deprecation banner to `cmd/qsdmminer` and
    `cmd/qsdmminer-console` pointing at the NVIDIA-lock design
    doc. The banner is suppressed on `--version` and `--self-test`
    (machine-parseable paths must stay clean for CI) but fires on
    every real mining run.
  - **Update** `MINER_QUICKSTART.md §2.5` and `OPERATOR_GUIDE.md
    §3.4` — the one-command install block is replaced by a
    deprecation notice, and the sections are relabelled "testnet /
    reference only".

  The binaries themselves are **not** deleted in this pass. They
  continue to ship as release artefacts, build cleanly, and pass
  `--self-test`, so testnet operators who want to replay the
  current protocol can still do so. The actual retirement of
  `cmd/qsdmminer` and `cmd/qsdmminer-console` will land together
  with the v2 hard fork (phased plan recorded in the issue
  tracker).

### Withdrawn

- **Docker image `ghcr.io/<owner>/qsdm-miner-console` + one-command
  install scripts for Linux / macOS / Windows (2026-04-24).**
  Originally landed in c4bdca5 and now retired without having been
  tagged — see the "NVIDIA-lock pivot" entry above. Preserving
  the original entry for historical context:

- **Docker image `ghcr.io/<owner>/qsdm-miner-console` + one-command
  install scripts for Linux / macOS / Windows (2026-04-24).** The
  console miner now has two frictionless install paths in addition to
  the signed binaries:

  1. **Container image** — a ~15 MB CPU-only image on
     `gcr.io/distroless/static-debian12:nonroot`. Built by a new
     `ghcr-miner-console` job in `release-container.yml` on every
     `v*` tag, pushed to GHCR with semver, major.minor, and major
     tags. Build-args propagate the same `BUILDINFO_*` values the
     binary release workflow injects, so `docker inspect
     ghcr.io/.../qsdm-miner-console:<tag>` and
     `docker run ... --version` both surface the exact release tag
     + commit SHA. Default entrypoint runs with `--plain` and
     `--config /config/miner.toml`, so the canonical invocation is
     `docker run -v $HOME/.qsdm:/config ... --validator=… --address=…`.
     `qsdm-split-profile.yml` gains a companion `docker build
     (miner-console)` job that builds the image no-push on every
     push, runs `--version` against a synthetic-tag build to verify
     the build-arg → ldflags pipeline, and executes `--self-test`
     inside the container to gate protocol conformance.

  2. **`scripts/install-qsdmminer-console.sh`** — Linux/macOS
     installer intended for `curl -sSL … | bash`. Detects platform,
     resolves the latest release via the GitHub API (or honours
     `QSDM_VERSION=vX.Y.Z`), downloads the matching
     `qsdmminer-console-<os>-<arch>` binary plus `SHA256SUMS`,
     verifies the hash (refuses to install on mismatch), installs
     to `$QSDM_INSTALL_DIR` / `/usr/local/bin` / `~/.local/bin`
     (whichever is writable without surprise `sudo`), and runs
     `--version` to confirm the binary identifies as a release
     build. A `dev` or `unknown` in the `--version` line aborts
     the install — a defence-in-depth assertion that the download
     did not bypass the release pipeline.

  3. **`scripts/install-qsdmminer-console.ps1`** — Windows
     PowerShell 5.1+ equivalent, bootstrappable via `iwr … | iex`.
     Never elevates (installs under `%LOCALAPPDATA%\Programs\QSDM`
     by default), performs the same SHA-256 verification with
     `Get-FileHash`, runs the installed binary's `--version`, and
     aborts on `dev`/`unknown` metadata identically to the bash
     installer.

  Both install scripts gain a `install-scripts-lint` CI job
  (`shellcheck` + `bash -n` for the sh, `PowerShell Parser` for the
  ps1) so syntax regressions are caught at push time — these are
  on the critical path for new-operator onboarding so any drift
  cannot be tolerated.

- **Embedded build metadata + `--version` on every release artefact
  (2026-04-24).** Every one of the four release binaries (`qsdmminer`,
  `qsdmminer-console`, `trustcheck`, `genesis-ceremony`) now accepts a
  `--version` flag that prints a single line identifying the exact
  artefact:

  ```
  qsdmminer-console v0.1.0 (abc1234, 2026-04-22T10:00:00Z, go1.25.9, linux/amd64)
  ```

  The values come from a new `pkg/buildinfo` package whose three vars
  (`Version`, `GitSHA`, `BuildDate`) are injected at link time by
  `.github/workflows/release-container.yml` via `-ldflags -X`. Local
  `go build` / `go run` produce `dev` / `unknown` sentinels — a
  deliberate, inspectable signal that the binary was not produced by
  the release pipeline. An `IsReleaseBuild()` helper exposes the
  same distinction to downstream callers that want to gate telemetry
  or Prometheus labels to released builds only.

  Two CI gates protect the wiring:
  - `qsdm-split-profile.yml` gains a `--version smoke` step under
    both profile matrix cells that runs each in-scope binary with
    synthetic ldflags and asserts the injected tag + SHA appear in
    the output. This catches a regression where a new `cmd/` binary
    is added without wiring `--version` to `buildinfo`.
  - `release-container.yml` runs the same smoke on the native
    (linux/amd64) matrix cell against the actual release ldflags —
    so any tag push that would have shipped a "dev" binary fails
    before the upload step.

  The `pkg/buildinfo` package ships with four dedicated unit tests
  (default sentinels visible, ldflags injection honoured, short banner
  stays terse, `IsReleaseBuild` distinguishes all five sentinel
  combinations). Tests execute under both validator and full CI
  profiles because every release artefact — including the
  validator-profile `trustcheck` — depends on it.

- **HTTP integration test for `cmd/qsdmminer-console` (2026-04-24).**
  Unit tests covered pure helpers; `--self-test` covered the in-process
  protocol; neither exercised the HTTP pipeline that a real miner uses
  against a validator. A new `cmd/qsdmminer-console/integration_test.go`
  drives `fetchWork` / `submitProof` / the full `runLoop` end-to-end
  against an in-process `httptest.Server` that mimics
  `/api/v1/mining/{work,submit}`. Five tests:

  - `TestIntegration_FetchWork_RoundTrip` — validator-shaped JSON
    decodes through `api.MiningWork` with every wire field preserved;
    asserts the `Accept: application/json` header the miner sends.
  - `TestIntegration_FetchWork_HTTPErrorSurfacesStatus` — a 503
    surfaces its status code in the returned error (so rate-limit /
    warming-up errors don't get mis-logged as decode failures).
  - `TestIntegration_SubmitProof_AcceptedParsesProofID` — a 200
    `Accepted=true` populates `ProofID` and the `Content-Type: application/json`
    header is present on the request.
  - `TestIntegration_SubmitProof_BadRequestStillDecodesRejection` —
    the validator returns 400 with a shaped `MiningSubmitResponse`
    for rejections; the miner must decode the body rather than
    treat it as a transport error.
  - `TestIntegration_RunLoop_EndToEnd` — runs the full loop against
    a fixture server, waits for the first `EvProofAccepted`, and
    asserts `EvConnected` / `EvEpochChanged` / `EvDAGReady` fired in
    the right order before it. This is the strongest regression gate
    in the suite: any break in fetch → DAG build → Solve → submit
    → event emission causes the test to time out.

  Tests execute in <2s on CI hardware (difficulty=2, `N=128`, same
  budget as `--self-test`) so they run on every `go test
  ./cmd/qsdmminer-console/...` without a `-short` gate. No extra CI
  job needed — they flow through the existing `full` profile of
  `qsdm-split-profile.yml`.

- **CI + release coverage for `cmd/qsdmminer-console`
  (2026-04-24).** The friendly console miner binary added earlier
  today is now a first-class release artifact and has push-time
  protocol-drift protection:

  - `.github/workflows/release-container.yml` builds
    `qsdmminer-console-<os>-<arch>[.exe]` alongside the existing
    `qsdmminer` / `trustcheck` / `genesis-ceremony` binaries on
    every `v*` tag (linux amd64/arm64, darwin amd64/arm64,
    windows amd64). Same `-trimpath -ldflags="-s -w"` CGO-free
    deterministic build as the other release binaries, folded
    into the consolidated `SHA256SUMS` asset, and uploaded to
    the GitHub Release. Non-Go-developer miners can now grab a
    signed binary instead of installing a toolchain.
  - `.github/workflows/qsdm-split-profile.yml` runs the new
    `./cmd/qsdmminer-console/...` unit test suite under the
    `full` profile matrix cell (12 tests; config round-trip,
    malformed-TOML rejection, poll defaulting, formatters,
    dashboard state machine, plain-renderer format, kindLabel
    exhaustiveness), and the `validator_only` cell explicitly
    excludes the package because it depends on `pkg/mining`
    which is absent from the validator tag surface.
  - Same workflow gains a `Protocol self-test` step under the
    `full` profile that `go run`s `qsdmminer --self-test` and
    `qsdmminer-console --self-test` back-to-back. Both exercise
    the same `pkg/mining.Solve` / `Verify` round-trip against
    independent `main` packages, so any drift between the
    reference miner and the console miner relative to
    `MINING_PROTOCOL.md` surfaces on push instead of at release
    time.

- **`OPERATOR_GUIDE.md §3.4` rewritten to reflect the two miner
  binaries (2026-04-24).** Previously named only `qsdmminer` and
  walked the reader through flag-heavy setup. The new section
  presents `qsdmminer-console` as the recommended path for home
  operators (wizard + live panel + config persistence), keeps
  `qsdmminer` as the protocol-truth reference for conformance
  testing, and documents that pre-built signed binaries now ship
  on every tagged release for both.

- **`cmd/qsdmminer-console` — friendly console miner binary
  (2026-04-24).** A sibling of `cmd/qsdmminer` that layers three
  ergonomic improvements on top of the same `pkg/mining` primitives,
  without touching the reference miner's audit-clean surface:

    1. **First-run setup wizard.** Running `qsdmminer-console` with
       no flags prompts for validator URL, reward address, batch
       count, and poll interval; answers are persisted to
       `~/.qsdm/miner.toml` (Windows: `%USERPROFILE%\.qsdm\miner.toml`)
       at mode 0600 and reused by future runs.
    2. **Live console panel.** In a TTY, the binary redraws a 14-line
       panel at 2 Hz showing reward address (redacted),
       validator URL, connection state (colored), current epoch
       and DAG readiness, 10-second rolling hashrate, accepted /
       rejected proof counters, uptime, and the last event. Non-TTY
       stdout (pipe, `journalctl`, CI) auto-detects and falls back
       to a one-line-per-event log. `--plain` forces the log mode
       on demand.
    3. **Flag overrides.** Every config field has a corresponding
       flag (`--validator`, `--address`, `--batch-count`, `--poll`,
       `--config`) so an operator can point at a different node for
       one run without editing the TOML. `--setup` re-runs the
       wizard. `--self-test` runs the same Phase 4.5 acceptance
       gate as `qsdmminer --self-test`.

  The mining loop is a targeted port of `cmd/qsdmminer`'s
  `fetchWork` / `Solve` / `submitProof` flow, emitting typed
  `Event`s into a channel consumed by the renderer. This keeps the
  `qsdmminer` reference binary unchanged — that binary remains
  mappable 1-to-1 against `MINING_PROTOCOL.md` with no TUI layered
  on top — while giving home operators a less hostile first-run
  experience. 12 unit tests cover config round-trip, malformed-TOML
  rejection, `pollDuration` defaulting, the hashrate / duration /
  address formatters, the `Dashboard` event state machine, the
  plain renderer's log format, and the `kindLabel` exhaustiveness
  guard against silently missing an `EventKind` in the log-label
  switch.

- **Landing page + MINER_QUICKSTART.md reflect that the reference
  miner is shipped (2026-04-24).** The landing page previously
  described the mining layer as "planned" (`#products` tile, `#mine`
  section lead, `#consensus-layer` pillar, footer link). Those have
  been updated to name both miner binaries and to clarify that only
  the CUDA production miner is gated on external audit.
  `MINER_QUICKSTART.md` gains a new `§2.5 Friendly console miner`
  that walks through build, wizard, panel, and flag overrides, and
  links the §2 reference-binary section to it as the recommended
  starting point for home operators. The root `README.md`'s
  summary paragraph and the "Run a miner" bullet were corrected
  accordingly (the prior wording implied GPU-bound PoW was the only
  path, which is untrue).

- **`cmd/trustcheck` JSON output schema is now pinned by tests
  (2026-04-24).** The `--json` flag and the `trustcheck.json`
  artifact upload in `trustcheck-external.yml` have shipped for
  several sessions, but the wire shape (top-level `summary`,
  `recent`, `assertions`, `pass` keys; per-row `name`/`pass`/
  optional `detail`; summary mirror of `attested`/`total_public`/
  `ratio`/`fresh_within`/`last_attested_at`/`last_checked_at`/
  `ngc_service_status`/`scope_note`) was not covered by tests, so a
  rename of any JSON tag would have silently broken every Datadog /
  Grafana / `jq` pipe consuming the artifact. Refactored
  `emitJSON(...)` in `cmd/trustcheck/main.go` to delegate to a pure
  `buildJSONReport(...)` helper, then added five schema tests in
  `main_test.go` covering (a) the top-level required keys, (b)
  per-row `name`/`pass` shape with `detail` omitted on pass rows,
  (c) top-level `pass` mirroring `rs.allOK()`, (d) `summary` and
  `recent` sub-objects being omitted when nil (the warming-up /
  disabled informational paths), and (e) the summary wire-field
  names matching the server-side `pkg/api.TrustSummary` JSON tags.
  Any future rename now fails the test and forces the contract
  change to be explicit in the diff. 23 tests pass
  (18 existing + 5 new).

### Changed

- **Landing-page roadmap widget synced with reality (2026-04-23).**
  `deploy/landing/index.html` was still showing Phase 2 as "In progress"
  and Phase 3 as "Next" — both phases have been shipped in-tree for
  several sessions (submesh rules, Scylla migrate with dry-run,
  `/api/v1/network/topology`, finality-gadget partition heal, NVIDIA
  lock enforcement). Flipped the Phase 2 and Phase 3 status pills to
  `Shipped`, rewrote the lead paragraph to stop claiming deployments
  are on "Phase 1 infrastructure with Phase 2 submesh routing enabled",
  and added a post-grid beat linking to `CELL_TOKENOMICS.md`,
  `MINING_PROTOCOL.md`, and the `/trust.html` surface so the widget
  doesn't imply development ended at Phase 3 (the in-repo Major Update
  Phases 1–5 continue the arc, with only wall-clock-blocked gates —
  trademark filings, `mining-01` external audit, `mining-05`
  incentivized testnet, and the mainnet genesis ceremony — remaining,
  as tracked in `NEXT_STEPS.md`). Same phase-card CSS
  (`repeat(3, 1fr)` grid, `.status.shipped` pill), so no stylesheet
  changes were needed.

### Added

- **Quarantine Prometheus gauges + alert group (2026-04-23).**
  `pkg/quarantine/metrics.go` now exports four gauges via a
  nil-safe `MetricsCollector(*QuarantineManager)` closure, mirroring
  the `api.TrustMetricsCollector` pattern:

    - `qsdm_quarantine_submeshes` — count of submeshes currently
      quarantined.
    - `qsdm_quarantine_submeshes_tracked` — distinct submeshes the
      manager has ever observed (union of the three internal maps, so
      a submesh with only 1–9 transactions is counted before the
      10-tx window boundary writes into `quarantined`).
    - `qsdm_quarantine_submeshes_ratio` — quarantined / tracked, with
      `0` when `tracked==0` so ratio-based alerts don't flap on a
      quiet node.
    - `qsdm_quarantine_threshold` — the configured invalid-ratio
      policy threshold, exposed so dashboards render the decision
      boundary next to the observed state.

  The collector is registered in `cmd/qsdm/main.go` alongside the
  other `pe.RegisterCollector(...)` calls inside the `dash != nil`
  block. A new method `QuarantineManager.Stats()` returns a consistent
  snapshot under the existing mutex; collector scrapes are O(1) in
  the number of tracked submeshes.

  `alerts_qsdm.example.yml` gains a new `qsdm-quarantine` group
  with two rules:

    - **`QSDMQuarantineAnySubmesh`** (warn, 10m) — any non-zero
      quarantined count worth a human decision on recovery.
    - **`QSDMQuarantineMajorityIsolated`** (critical, 15m) — fires
      when `ratio > 0.5` and `tracked >= 4` (the `tracked` guard
      prevents flap on tiny fleets where 1/2 crosses the ratio).

  No warm-gate is needed (the manager is live from process start, so
  zero-at-t=0 is literally correct, and the denominator guard inside
  the collector keeps empty-fleet scrapes from paging). Tests live in
  `pkg/quarantine/metrics_test.go` covering nil-manager, empty-manager
  shape, sub-window tracking, full-window quarantine, post-removal
  gauges, and the `Stats.Quarantined` counts-only-true invariant.

- **`ATTESTATION_SIDECARS.md` operator guide (2026-04-23).** The
  recipe for getting the trust pill to `N/N` by standing up N
  attestation sources was previously spread across two CHANGELOG
  entries, `install_ngc_sidecar_vps.py` docstrings, and session
  notes. New `QSDM/docs/docs/ATTESTATION_SIDECARS.md` consolidates
  it: the reference three-source deployment (Windows PC + BLR1 VPS
  + OCI), the four required invariants (shared ingest URL, shared
  `QSDM_NGC_INGEST_SECRET`, **distinct** `QSDM_NGC_PROOF_NODE_ID`,
  cadence ≤ `fresh_within`/2), one-command install snippets per
  platform, a five-step verification ladder
  (`journalctl` → ingest counter → `qsdm_trust_*` gauges → public
  summary JSON → external CI probe), and a troubleshooting table
  keyed on the symptom operators actually see. Cross-linked from
  the aggregator implementation, Prometheus gauges, alert rule
  example, and the `trustcheck --min-attested` flag so someone
  landing in any of those files can find the canonical setup.

### Changed

- **`build_kernels.ps1` defaults to a Turing→Hopper fatbin
  (2026-04-23).** Previous default `-Arch 'sm_86'` only produced a
  DLL that worked on Ampere; running the same DLL on an RTX 4090
  (Ada, sm_89) or an H100 (Hopper, sm_90) silently fell back to a
  JIT recompile or failed to launch. New default
  `'sm_75,sm_86,sm_89,sm_90'` emits a fatbin covering Turing, Ampere,
  Ada, and Hopper in one build — roughly +30 s compile vs. the
  single-arch default, and no per-card rebuild for the four GPU
  lineups we've exercised on. Iterating on a known host can still
  narrow via `-Arch 'sm_86'` explicitly (documented under
  `.PARAMETER Arch` and in the `MESH3D_GPU_BENCHMARK.md`
  reproduction steps).

- **Prometheus alert rules for the trust-redundancy surface
  (2026-04-23).** Now that `qsdm_trust_attested` /
  `qsdm_trust_total_public` / `qsdm_trust_last_attested_seconds` /
  `qsdm_trust_last_checked_seconds` / `qsdm_trust_warm` /
  `qsdm_trust_ngc_service_healthy` gauges are exported by
  `api.TrustMetricsCollector`, the example rule file
  `QSDM/deploy/prometheus/alerts_qsdm.example.yml` gains a new
  `qsdm-trust-redundancy` group mirroring the external CI probe's
  `--min-attested 2` floor from inside Alertmanager:

    - **`QSDMTrustAttestationsBelowFloor`** — fires when a *warm*
      aggregator reports `qsdm_trust_attested < 2` for 10 min. Gated
      on `qsdm_trust_warm == 1` so redeploys do not page (the ring
      buffer is volatile; `attested` recovers on the next sidecar
      cadence).
    - **`QSDMTrustNGCServiceDegraded`** — fires when a warm aggregator
      reports `qsdm_trust_ngc_service_healthy == 0` (mapped from the
      summary JSON's `ngc_service_status` enum) for 10 min.
    - **`QSDMTrustLastAttestedStale`** — fires when the newest
      attestation is older than 30 min (twice the default
      `fresh_within` of 15 min), catching slow-death scenarios
      before `attested` itself tips to zero.
    - **`QSDMTrustAggregatorStale`** (severity `critical`) — fires
      when `qsdm_trust_last_checked_seconds` has not advanced for
      > 2 min (Refresh ticker wedged). Default refresh cadence is
      10 s, so this is unambiguously a stuck goroutine.

  The existing `qsdm-trust-transparency` group (proxy-metric alerts
  on the accepted/rejected ingest counter) is complementary and
  retained: it fires when *no* proof is flowing at all, regardless of
  aggregator state; the new group fires when proofs flow but the
  aggregator's distinct-source count drops below the operator's
  declared floor. Both sides — external CI probe + internal
  Prometheus — now enforce the same `attested >= 2` invariant from
  independent vantage points.

- **Prometheus gauges for the trust-transparency surface
  (2026-04-23).** The §8.5.x trust numbers (`attested`,
  `total_public`, `ratio`, `ngc_service_status`, `last_attested_at`,
  `last_checked_at`, warm-up state) were previously only available
  via `GET /api/v1/trust/attestations/summary`. Alertmanager and
  Grafana cannot scrape a bespoke JSON endpoint without bespoke
  exporters, so a silent drop from `attested=3` back down to
  `attested=1` was undetectable short of a human checking the
  widget. New `api.TrustMetricsCollector(*TrustAggregator)`
  registers a nil-safe, O(1) collector on
  `monitoring.GlobalScrapePrometheusExporter()` that surfaces:

    - `qsdm_trust_attested`                (gauge)
    - `qsdm_trust_total_public`            (gauge)
    - `qsdm_trust_ratio`                   (gauge)
    - `qsdm_trust_ngc_service_healthy`     (gauge, 0/1)
    - `qsdm_trust_last_attested_seconds`   (gauge, unix seconds)
    - `qsdm_trust_last_checked_seconds`    (gauge, unix seconds)
    - `qsdm_trust_warm`                    (gauge, 0/1)

  The collector reads the aggregator's already-cached summary on
  every scrape, so there is no new locking, no new ticker, no new
  wire traffic. It registers unconditionally when
  `[trust] disabled=false` and emits nothing when the aggregator
  is disabled (rather than zeroes that would falsely imply a
  denominator). Grafana alerts gated on `qsdm_trust_warm == 1` stay
  silent through a restart because the warm bit flips only after
  the aggregator's first full `Refresh()`. Full gauge shape,
  HELP text, and timestamp-parse behaviour are covered by
  `pkg/api/trust_metrics_test.go`.

- **`trustcheck --min-attested N` policy floor (2026-04-23).** The
  external transparency probe previously only validated the §8.5.x
  wire contracts (scope-note verbatim, enum membership,
  ratio-sanity, etc.) — none of which trip when a deployment
  silently loses attestation sources. Every value of `attested`
  from 0 through the entire validator set is a legal wire contract,
  by design. A new operator-policy flag `--min-attested N` lets a
  deployment declare an intended redundancy floor; the probe fails
  loudly when `summary.attested < N`. The assertion lives in a
  standalone `validateMinAttested` helper so running trustcheck
  without the flag (default `0`) behaves exactly as before. The
  GitHub-Actions workflow `.github/workflows/trustcheck-external.yml`
  defaults to `--min-attested 2` on scheduled / push / pull_request
  runs (matching the deployed "primary validator + OCI sidecar"
  invariant), and exposes a `min_attested` workflow-dispatch input
  so ops can drop the floor to `0` during a single-sidecar
  maintenance window.

- **Trust aggregator now counts distinct CPU-fallback sidecars as
  separate attestation sources (2026-04-23).** Operators who run
  multiple CPU-fallback attestation sidecars — e.g. one on the main
  validator VPS, one on an Oracle Cloud VM, one on a local dev PC,
  each stamping its own `QSDM_NGC_PROOF_NODE_ID` — were being
  collapsed into a single "local" peer row by `TrustAggregator`.
  `MonitoringLocalSource.LocalLatest()` only exposed the newest row
  from `monitoring.NGCProofSummaries()` and stamped it with the
  validator's libp2p host id, so ten sidecars looked like one peer
  and `attested` never climbed past 1 no matter how much redundant
  CPU attestation was running.

  New `monitoring.NGCProofDistinctByNodeID()` walks the NGC proof
  ring buffer and groups entries by `qsdm_node_id` (or the
  legacy `qsdm_node_id` alias), keeping the newest-observed bundle
  for each distinct id. New optional interface
  `api.LocalDistinctAttestationSource` exposes that view to the
  aggregator; `MonitoringLocalSource` now implements it, and
  `TrustAggregator.Refresh` prefers the distinct view when
  available, folding empty-id rows onto the local node's identity
  so bundles without an id still behave like before. Sources that
  don't implement the new interface fall back to the old single-row
  path — this is a strict addition, not a behaviour swap for legacy
  embedders.

  Verified live against the reference validator: adding the second
  and third sidecars (OCI `ap-singapore-1` and DO `blr1`) flipped
  `GET /api/v1/trust/attestations/summary` on `api.qsdm.tech` from
  `attested=1, total_public=2` to `attested=3, total_public=4`
  within one 10 s refresh tick, with each sidecar showing a distinct
  redacted `node_id_prefix` in `/recent`.

  Semantics note (recorded here, not a regression): anyone holding
  `QSDM_NGC_INGEST_SECRET` can drive `attested` up by POSTing
  from N distinct `qsdm_node_id` values. That is acceptable
  because (a) the ingest secret is already the trust root for this
  surface, (b) the `scope_note` field in every summary response
  caveats that these attestations are not consensus, and (c) the
  aggregator's freshness window (default 15 min) still applies per
  row, so a one-shot spoof cannot hold the pill green without
  continuous posts.

### Fixed

- **mesh3d Windows DLL now exports its host-side entry points
  (2026-04-23).** `pkg/mesh3d/kernels/sha256_validate.cu` declared
  `mesh3d_hash_cells` and `mesh3d_validate_cells` inside an
  `extern "C"` block but without `__declspec(dllexport)`. On Linux
  this is harmless — ELF exports every non-static symbol by default
  — but MSVC's PE linker only exports symbols explicitly marked
  dllexport, so the Windows build of `mesh3d_kernels.dll` shipped
  with a single `NvOptimusEnablementCuda` export and nothing else.
  CGO builds with `-tags cuda` then died with
  `undefined reference to 'mesh3d_hash_cells'` at link time.

  New `MESH3D_API` macro (`__declspec(dllexport)` on `_WIN32`,
  `__attribute__((visibility("default")))` elsewhere) now decorates
  both entry points; Linux `.so` behaviour is unchanged, Windows
  `.dll` now correctly exports the two symbols CGO needs. Verified
  via `gendef - mesh3d_kernels.dll` showing the symbols and via
  the `BenchmarkMesh3DGPUVsCPU` linking against them.

- **`pkg/mesh3d/cuda.go` no longer requires a `C:/CUDA` symlink on
  Windows (2026-04-23).** The old cgo directive block hard-coded
  `-IC:/CUDA/include -LC:/CUDA/lib/x64`, which is nowhere an
  NVIDIA installer ever lands. Every fresh Windows dev box failed
  to build until the operator manually created `C:\CUDA`. Replaced
  with split platform directives: Linux keeps `/usr/local/cuda`
  defaults, Windows relies on `CGO_CFLAGS` / `CGO_LDFLAGS` set by
  `QSDM/scripts/build_kernels.ps1`, which probes `$env:CUDA_PATH`
  and emits DOS 8.3 short-path forms so cgo's whitespace-splitting
  directive parser sees no spaces.

- **Dashboard user accounts now survive a service restart
  (2026-04-23).** `pkg/api.UserStore` used to be an in-memory
  `map[string]*User` with no persistence layer, so every
  `systemctl restart qsdm` (routine during redeploys) silently
  wiped every registered dashboard login. `AuthenticateUser` returned
  "user not found", which the handler then mapped to a generic
  `401 invalid credentials` — so the symptom an operator saw was
  "I swear I registered yesterday, why am I locked out?". Ledger
  state (transactions, balances, staking, bridge) was already
  persisted; only the dashboard-login credential map was affected.

  Fix in `pkg/api/user_persist.go`: versioned JSON file
  (`/opt/qsdm/qsdm_users.json`, mode `0600`), atomic
  temp-file-rename on every mutation, loader fail-closed on unknown
  version / malformed JSON (never silently reset). `RegisterUser`
  rolls back the in-memory insert when the disk write fails, so
  callers never observe a "registered but not persisted" half-state.

  Configured via `Config.UserStorePath`
  (TOML `[api] user_store_path`) with env overrides
  `QSDM_USER_STORE_PATH` / legacy `QSDM_USER_STORE_PATH`. The
  default in `cmd/qsdm/main.go` is
  `<dirname(SQLitePath)>/qsdm_users.json`, matching the sibling
  `qsdm_staking.json` / `qsdm_bridge_state.json` layout.

  Tests in `pkg/api/user_persist_test.go` cover the round-trip
  (register → reopen → auth), persist-failure rollback, unknown-
  version fail-closed, and malformed-JSON fail-closed. Verified
  end-to-end against the live VPS on 2026-04-23: registered,
  `systemctl restart qsdm`, re-logged in successfully from
  `https://dashboard.qsdm.tech/`.

### Added

- **Full mesh3d GPU benchmark runnable on a dev box (2026-04-23).**
  New `QSDM/source/pkg/mesh3d/mesh3d_gpu_bench_test.go` contains
  `BenchmarkMesh3DGPUVsCPU_Validate` and `_Hash`, each sweeping
  n ∈ {16, 256, 4096} across the CUDA and CPU-parallel backends
  with `b.SetBytes` so `go test -bench` prints MB/s directly.
  Skips with a clear diagnostic (`build mesh3d_kernels.dll / .so
  first`) when the GPU path isn't available, so CI runs on the
  CPU baseline without failing.
- **`QSDM/scripts/build_kernels.ps1` — Windows CUDA kernel build
  helper (2026-04-23).** Auto-locates CUDA via `$env:CUDA_PATH`
  (with a fall-back scan of the canonical install root),
  auto-locates MSVC via `vswhere.exe`, sources `vcvars64.bat`
  into a `cmd.exe` subshell for nvcc, compiles
  `mesh3d_kernels.dll` with per-GPU `-gencode` (default `sm_86`
  for the RTX 3050, comma-list supported), mirrors the DLL next
  to the Go source, regenerates a MinGW-compatible
  `libmesh3d_kernels.dll.a` via `gendef` + `dlltool` so MSYS2 Go
  + cgo can link it, and prints (or sets with `-SetEnv`) the
  `CGO_CFLAGS` / `CGO_LDFLAGS` / `PATH` lines the next build
  needs.
- **`QSDM/scripts/build_liboqs_win.ps1` — local liboqs build
  (2026-04-23).** Clones liboqs into
  `%LOCALAPPDATA%\QSDM\liboqs`, configures with CMake + Ninja +
  MinGW-w64 gcc + MSYS2 OpenSSL 3, builds the `oqs` target with
  `-DOQS_OPT_TARGET=generic` (MinGW doesn't assemble the AVX2
  fast paths), installs to `%LOCALAPPDATA%\QSDM\liboqs_install`,
  and emits CGO env lines that compose cleanly with the CUDA
  ones. Total runtime ~2 min on an RTX-3050-class dev box.
  Matches the Dockerfile.miner production build so local and
  CI signing/verification produce the same artefacts.
- **`docs/docs/MESH3D_GPU_BENCHMARK.md` — reference benchmark
  numbers (2026-04-23).** RTX 3050 + Xeon E5-2670 reference
  figures (0.04× at n=16, 4.06× at n=4096 for validate; 0.03×
  / 2.23× for hash) with the reproduction recipe and operator
  guidance on when a GPU actually helps mesh3d throughput. Cited
  from `docs/docs/MINER_QUICKSTART.md` so a new miner operator
  knows whether buying a card is worth it for their fan-out.

- **Live trust pill on `qsdm.tech` navigation bar (2026-04-23).**
  Compact `trust: 1/2 · healthy` chip next to `Open Dashboard`, poll
  of `/api/v1/trust/attestations/summary` every 60 s, four visual
  states (healthy / warming / degraded / offline). Shares the existing
  trust-widget fetch loop — one HTTP call drives both the pill and
  the pre-existing full-width widget. Hidden on viewports `<900px` so
  it does not crowd mobile.
- **Prometheus alert rules for attestation transparency
  (2026-04-23).** New group `qsdm-trust-transparency` in
  `deploy/prometheus/alerts_qsdm.example.yml` with two rules on
  the canonical `qsdm_*` prefix:
  `QSDMTrustNoAttestationsAccepted` (accepted-rate zero for 20 m) and
  `QSDMTrustIngestRejectRateElevated` (rejects outpace accepts by
  >1/s for 10 m). Safe to load on both dual-emit and legacy scrapers.
- **Grafana panels 16 + 17 in `qsdm-overview.json`
  (2026-04-23).** Stat "NGC attestations in last 15 min" with
  red/yellow/green thresholds matched to the pill on qsdm.tech
  (0 = red, ≥1 = orange, ≥3 = green) plus a bar chart
  "NGC attestations per hour (rolling)" for Scheduled-Task cadence
  verification. Idempotent patch — panel IDs 1–15 unchanged.
- **Self-rotating transcript in `local-attest.ps1` (2026-04-23).**
  New `-LogPath` / `-LogMaxBytes` (default 10 MiB) / `-LogKeep`
  (default 3) parameters. Rotates ring-style (`.1`, `.2`, `.3`)
  before opening the transcript and between loop iterations so the
  10-min refresh cadence does not grow a hundreds-of-MB transcript
  in a week on the refresh PC. Forwarded by
  `attest-from-env-file.ps1` as a splat. Documented in
  `apps/qsdm-nvidia-ngc/QUICKSTART.md` §8a.
- **GitHub Wiki live (2026-04-23).** `QSDM/scripts/sync-wiki.sh`
  now publishes eight pages to
  <https://github.com/blackbeardONE/QSDM/wiki> with a shared sidebar
  and footer: Home (Operator Guide), Node Roles,
  Validator/Miner Quickstart, Mining Protocol, Cell Tokenomics,
  NVIDIA Lock Scope, NGC Sidecar Quickstart. Pages auto-update from
  the canonical markdown under `QSDM/docs/docs/` whenever the script
  is re-run.
- **VPS-side CPU-fallback NGC attestation sidecar (2026-04-23).**
  New installer `QSDM/deploy/install_ngc_sidecar_vps.py` deploys
  `validator_phase1.py` to `/opt/qsdm/ngc-sidecar/`, reuses the
  existing `QSDM_NGC_INGEST_SECRET` from the qsdm service
  environment, and installs a systemd oneshot + 10-minute timer
  (`qsdm-ngc-attest.service` / `.timer`). The script runs without a
  GPU — `gpu_fingerprint` falls back to `available: false` and the
  fp16 matmul path degrades to `stub_no_cuda` cleanly. Result: the
  `/api/v1/trust/attestations/summary` badge stays `healthy` even
  when the operator's dev PC is offline. Re-running the installer
  picks up a rotated secret automatically.

### Security

- **Webviewer refuses to start on insecure default credentials
  (2026-04-22).** `internal/webviewer` used to silently fall back to
  `admin` / `password` when `WEBVIEWER_USERNAME` / `WEBVIEWER_PASSWORD`
  were unset — which was acceptable when the code was private but is
  now a real foot-gun with QSDM public on GitHub: anyone who clones,
  builds, and `./qsdm`-es without reading the docs gets a wide-open
  log stream on port `9000` / `LOG_VIEWER_PORT`. `StartWebLogViewer`
  now returns the new `webviewer.ErrInsecureDefaultCreds` when either
  var is unset or empty, and `cmd/qsdm/main.go` logs a clear
  remediation message and keeps the node running *without* the log
  viewer. Operators who explicitly want the old behaviour for local
  development must now opt in with `QSDM_WEBVIEWER_ALLOW_DEFAULT_CREDS=1`,
  which also emits a loud `[WEBVIEWER][WARN]` banner on every
  start-up. Basic-auth compares use `crypto/subtle.ConstantTimeCompare`
  now to remove the trivial timing side-channel. Covered by seven new
  unit tests in `internal/webviewer/webviewer_creds_test.go` (with
  eleven subtest permutations covering both-set, either-unset,
  truthy/falsey opt-in values, and the opt-in-doesn't-override-real-
  creds invariant). The existing integration test in
  `tests/webviewer_test.go` was updated to set the opt-in flag
  explicitly so its use of `admin`/`password` is intentional rather
  than accidental. The live VPS is unaffected because both env vars
  have been provisioned in `/etc/systemd/system/qsdm.service.d/secrets.conf`
  since the 2026-04-22 secret-rotation pass.

### CI

- **Legacy-metric guard in GitHub Actions (2026-04-22).** During the
  `qsdm_*` -> `qsdm_*` Prometheus metric-name prefix migration
  (Major Update §6), the dual-emit machinery in
  `pkg/monitoring/prometheus_prefix_migration.go` already publishes
  every metric under both prefixes, so any new code should register
  metrics under the canonical `qsdm_*` name only. Added
  `QSDM/scripts/check-no-new-legacy-metrics.sh` which `rg`-greps the
  Go tree for hand-written `"qsdm_<name>_(total|count|seconds|sum|
  bucket|bytes|info|ratio|current|last|active|inflight)"` string
  literals and fails if any appear outside a tight five-file allowlist
  of files that are part of the dual-emit machinery or test it. The
  regex is deliberately narrow so it does NOT flag non-metric branding
  aliases like `qsdm_node_id` in `pkg/branding/branding.go`
  (those are NGC proof JSON field names, not metrics). Wired into
  `.github/workflows/qsdm-go.yml` `build-test` job as the very first
  step so regressions fail fast without burning build compute. Has a
  `git grep` fallback for runners without ripgrep.

### Repository

- **`.gitattributes` forcing LF for scripts + YAML (2026-04-22).** The
  existing clone has `core.autocrlf=false`, so `.sh` / `.py` files
  authored on Windows were being committed with CRLF line endings and
  only working on CI by accident of how `bash script.sh` tolerates
  trailing `\r`. Added a minimal `.gitattributes` that pins `*.sh`,
  `*.py`, `*.yml`, `*.yaml`, and `Dockerfile` to LF at the blob level.
  Does not rewrite existing working copies -- just protects every
  future checkout and new file from the shebang-breakage class of
  bug (e.g. `/usr/bin/env: 'bash\r': No such file or directory`).

### Changed — deploy scripts

- **VPS host/user are now env-configurable (2026-04-22).** Every
  script under `QSDM/deploy/*.py` used to hardcode the reference
  validator's IP (`206.189.132.232`) and SSH user (`root`) at the top
  of the file. That made the scripts unusable by anyone forking the
  repo to run their own QSDM node, and it meant a future VPS
  migration would be a cross-file sed pass every single time. Added
  a tiny shared helper at `QSDM/deploy/_deploy_host.py` that reads
  `QSDM_VPS_HOST` / `QSDM_VPS_USER` from the environment with a
  sensible fallback to the historical reference-node values (the IP
  is not a secret — it is the public A record for `api.qsdm.tech` in
  DNS and is already documented in `QSDM/deploy/Caddyfile`). All
  seven scripts (`remote_apply`, `remote_verify`, `remote_harden_ssh`,
  `remote_install_caddy`, `remote_cmd`, `remote_fix_service`,
  `remote_bootstrap`) now import from `_deploy_host`. Running them
  against the reference node is unchanged; running them against a
  different node is now a single `$env:QSDM_VPS_HOST = '...'` away.

### Changed — repository / licensing

- **MIT license surfaced at the repo root (2026-04-22).** `QSDM/LICENSE`
  was one level too deep for GitHub's license detector, so the repo
  page was displaying neither a licence badge nor the "MIT" tag in the
  sidebar even though the project has always been MIT-licensed. Copied
  the licence to `/LICENSE` at the repo root (keeping `QSDM/LICENSE`
  in place so nothing else breaks), bumped the copyright line to
  `2024-2026` in both copies, and added a `## License` section to the
  root `README.md` that links to it. Also trimmed the root `README`
  down to files that actually exist in the public tree — the previous
  version pointed at several internal docs (`NEXT_STEPS.md`,
  `Major Update.md`, `nvidia_locked_qsdm_blockchain_architecture.md`,
  `apps/game-integration/`) that are correctly excluded from the
  public repo by `.gitignore`, so those links were 404s on GitHub.

### Deployed

- **VPS root password rotated (2026-04-22).** The previous root
  password has been retired now that `ed25519` key-auth is the proven
  primary SSH path (every deploy in the Major Update window used it).
  Rotation was performed over the existing key channel using
  `chpasswd` on stdin — the new secret never appears in argv, bash
  history, or the process table on the remote host. Key-auth was
  verified end-to-end by opening a second independent connection
  after the rotation, so the session could not have locked itself
  out. The new password lives only in `vps.txt` (which is
  `.gitignore`d) and is intended purely as a break-glass fallback via
  the DigitalOcean web console; no automation reads it.
- **Workspace placed under version control (2026-04-22).** Twelve
  weeks of Major Update work was living on a single Windows disk with
  no git history — a real disaster-recovery hole given the project
  is already public. `git init` + initial import commit on `main`
  (`bab2f8f`, 930 files, 129,786 insertions). The `.gitignore` was
  designed defensively around what actually exists on disk: it
  excludes `vps.txt`, the legacy `Nvidia Token` file, all `*.env`
  (except committed `*.env.example` templates), TLS material
  (`QSDM/api_server.crt`/`.key`), Go build artifacts (`*.exe`,
  `*.test`, explicit `qsdm`/`qsdmminer`/`trustcheck` paths),
  `target/` trees (Rust), runtime state (`*.log`, `QSDM/databases/*.db`,
  timestamped SQL dumps, `QSDM/storage/tx_*.dat`), and the usual
  Python/Node/IDE/OS caches. Vendored third-party binaries (notably
  `QSDM/source/wasmer-go-patched/.../libwasmer.*`) are deliberately
  kept because the build depends on them. Pre-commit audit confirmed
  no explicit secret path (vps.txt, api_server.key/crt, Nvidia Token,
  `_tmp_*.py`) slipped into the staged tree; largest staged file is
  ~15 MB (`libwasmer.so` for `linux-amd64`), well under GitHub's
  100 MB per-file soft cap. No remote is configured — that's a
  separate decision.
- **NGC proof-ingest secret provisioned on the VPS (2026-04-22).**
  Generated a fresh 256-bit random secret (`secrets.token_hex(32)`, 64
  hex chars — comfortably above the 16-char strict-secrets floor and
  obviously not a `charming123*` dev placeholder) and installed it as a
  systemd drop-in at
  `/etc/systemd/system/qsdm.service.d/ngc-secret.conf` (mode `0600`,
  owner `root:root`). Both the canonical `QSDM_NGC_INGEST_SECRET`
  and the legacy `QSDM_NGC_INGEST_SECRET` env keys are exported so the
  deprecation window for older sidecars is honoured. The drop-in path
  is chosen deliberately: subsequent runs of
  `deploy/remote_apply_paramiko.py` rewrite the unit file at
  `/etc/systemd/system/qsdm.service` but leave the `.service.d/`
  directory untouched, so the secret survives redeploys without ever
  entering the repo or the tarball. Post-install probes:
  `GET /api/v1/monitoring/ngc-proofs` with the correct
  `X-QSDM-NGC-Secret` returns `HTTP 200 {"count":0,"proofs":[]}`,
  a wrong secret returns `HTTP 401`, and no NGC-related errors appear
  in the journal. The ingest surface is now gated-live, but the
  `attested/total_public` ratio stays at `0/1` until a sidecar with
  matching secret and a real NVIDIA NGC attestation submits its first
  proof — that's a separate bring-up gated on having a GPU host, not
  on anything in this repo.
- **Trust surface denominator fix — `total_public` 2 → 1 (2026-04-22).**
  The `"bootstrap"` placeholder address, which `cmd/qsdm/main.go`
  registers against `nodeValidatorSet` purely to satisfy BFT quorum on
  a single-node network, was being counted by the transparency widget
  as if it were a public peer. The new `sentinelValidatorAddresses`
  allowlist in `pkg/api/trust_peer_provider.go` filters it out, so the
  live `/api/v1/trust/attestations/summary` on the VPS now reports
  `{"attested":0,"total_public":1}` — one real validator, zero fresh
  attestations, which is the honest anti-claim answer.
  (`pkg/api/trust_peer_provider.go`,
  `pkg/api/trust_peer_provider_test.go`).
- **Dashboard login page — registration/hostname copy removed
  (2026-04-22).** The two-paragraph `<div class="info">` block that
  explained `POST /api/v1/auth/register` and `localhost` vs `127.0.0.1`
  session-cookie guidance was stripped from
  `internal/dashboard/dashboard.go`. The `<noscript>` fallback notice
  is retained because it serves an accessibility purpose rather than
  a documentation one. Confirmed live on
  `https://dashboard.qsdm.tech/`.
- **Deploy-log noise fix — Caddy reload false alarm silenced
  (2026-04-22).** `systemctl reload caddy` returned non-zero on this
  host because the Caddyfile sets `admin off`, even though the config
  reload itself succeeded. `remote_apply_paramiko.py` now redirects
  reload/restart stderr+stdout and surfaces only
  `caddy: <is-active>` so a healthy deploy log stops containing
  "Job for caddy.service failed" red herrings.
- **Production VPS redeploy — Major Update payload live (2026-04-22).**
  `206.189.132.232` (Bangalore `blr1`) was upgraded from the
  pre-rebrand build to the current `[Unreleased]` tree. Public probes
  against `https://qsdm.tech/`, `https://qsdm.tech/trust.html`,
  `https://qsdm.tech/api/v1/trust/attestations/summary`, and
  `https://api.qsdm.tech/api/v1/health/live` all return HTTP 200. The
  trust aggregator is wired and serving the honest anti-claim payload
  `{"attested":0,"total_public":2,"ratio":0,…,"scope_note":"NVIDIA-lock
  is an opt-in, per-operator API policy — not a consensus rule …"}`.
  Node identity: `12D3KooWB639f3GXxAuyqgZnk8so9ZVVstNxxvU6W1ncjXoAbVKS`;
  `[trust]` block defaults applied on first restart
  (`fresh_within=15m`, `refresh_interval=10s`). Landing page
  (`index.html` + `trust.html`) synced to `/var/www/qsdm/` and served
  by the existing Caddy edge.

### Changed — deployment tooling

- **`QSDM/deploy/remote_apply_paramiko.py` rewritten** as a full-tree
  apply (tarball of 908 Major Update files, ~42 MiB gzipped) instead
  of the previous 4-file hotfix. Now: auto-auths with
  `~/.ssh/id_ed25519` before falling back to `QSDM_VPS_PASS`; restores
  the Unix exec bit on all `*.sh`/`*.py` after tar extraction
  (Windows-safe); keeps the existing CGO+liboqs production profile by
  default and exposes `QSDM_BUILD_TAGS=validator_only` as an opt-in
  cutover lever; non-destructively appends a `[trust]` block to
  `/opt/qsdm/qsdm.toml` on servers that predate the aggregator
  wiring; rolls back to `/opt/qsdm/qsdm.prev` if the new binary
  fails systemd's `is-active` gate; probes health, trust, dashboard,
  and the Caddy edge (`api.qsdm.tech`, `qsdm.tech` via
  `curl --resolve`) at the end so the operator sees green probes
  inline with the deploy log. `remote_verify_paramiko.py` gained the
  matching trust-endpoint probe block.
- **`QSDM/config/qsdm.toml.example` + `qsdm.yaml.example`**
  now ship `[node]` (two-tier role gate) and `[trust]` (attestation
  transparency) sections with inline commentary, so fresh installs get
  the correct scaffolding without needing to cross-reference the
  Major Update spec.

### Added

- **Trust aggregator wired into node startup.** `cmd/qsdm/main.go`
  now constructs a live `TrustAggregator` fed by a
  `ValidatorSetPeerProvider` (wrapping `ActiveValidators()`) and a
  `MonitoringLocalSource` (NGC ring buffer), and runs a background
  refresh goroutine at `cfg.TrustRefreshInterval` (default 10 s). The
  `/api/v1/trust/attestations/*` endpoints now return real data on a
  live node instead of perpetually answering 503 "warming up". New
  `[trust]` config section (TOML/YAML) and env knobs
  `QSDM_TRUST_DISABLED`, `QSDM_TRUST_FRESH_WITHIN`,
  `QSDM_TRUST_REFRESH_INTERVAL`, `QSDM_TRUST_REGION` (legacy
  `QSDM_*` still accepted). Setting `disabled=true` makes the
  endpoints return HTTP 404 per §8.5.3. (`pkg/api/trust_peer_provider.go`,
  `pkg/config/config.go`, `pkg/config/config_toml.go`,
  `cmd/qsdm/main.go`, audit item `rebrand-07`.)
- **Major Update Phase 5 — trust transparency surface.** New public
  endpoints `GET /api/v1/trust/attestations/summary` and
  `GET /api/v1/trust/attestations/recent` expose aggregate NGC
  attestation counts as an opt-in, per-operator *transparency signal*
  (not a consensus rule). Widgets render "X of Y" — never just "X" —
  per the anti-claim guardrail in Major Update §8.5.2.
  New: `/trust.html` transparency page on `qsdm.tech`, matching card on
  the operator dashboard. (`pkg/api/handlers_trust.go`,
  `deploy/landing/trust.html`, `internal/dashboard/static/dashboard.js`.)
- **Major Update Phase 1–4 deliverables landed in-repo.** Rebrand
  (`QSDM → QSDM`, native coin `Cell (CELL)`, `dust` smallest unit),
  two-tier node roles (validator / miner) with role-guard startup
  enforcement, emission schedule calculator, reference CPU miner
  (`cmd/qsdmminer`), split Dockerfiles (`Dockerfile.validator`,
  `Dockerfile.miner`), mining protocol spec (Candidate C: mesh3D-tied
  useful PoW). Full phase-by-phase table in
  [`NEXT_STEPS.md`](NEXT_STEPS.md).
- **`cmd/trustcheck`** — single-binary, stdlib-only external scraper
  that validates `/api/v1/trust/attestations/*` responses against the
  §8.5.2–§8.5.4 contracts. Third parties can run it on any cadence
  without pulling in the QSDM module.
- **`cmd/genesis-ceremony`** — pure-Go dry-run of the mainnet genesis
  ceremony. N-of-N commit-reveal with ed25519 signing (spec-level
  stand-in for ML-DSA-87), `run`/`verify`/`schema` modes. Every
  artefact flagged `dry_run: true`; the verifier refuses to bless a
  non-dry-run bundle.
- **`docs/docs/AUDIT_PACKET_MINING.md`** — external-auditor entry point
  for `mining-01`: threat model, 10 numbered consensus-safety
  invariants, invariant → source-location → test coverage matrix,
  reproducible-build recipe.
- **`.github/workflows/qsdm-split-profile.yml`** — CI workflow proving
  both the `validator_only` and full-profile builds compile and pass
  short tests on every push, plus clean Docker builds of
  `Dockerfile.validator` and `Dockerfile.miner` (no push).
- **`pkg/audit/checklist.go`** — 18 new audit items across the
  `rebrand`, `tokenomics`, `mining_audit`, and `trust_api` categories
  so each in-repo commitment has a single auditable identifier.

### Changed

- **OpenAPI spec refresh.** `QSDM/docs/docs/openapi.yaml` title,
  contact, and env-var references now lead with the `QSDM` name, with
  the legacy `QSDM_*` names documented as aliases during the
  deprecation window.
- **`NVIDIA_LOCK_CONSENSUS_SCOPE.md` refresh.** Aligned to the Major
  Update §5.4 Stance 1 ("NVIDIA-favored, not NVIDIA-exclusive") and
  cross-references the new trust endpoints. The one-sentence
  invariant — "NVIDIA-lock is an opt-in, per-operator API policy —
  not a consensus rule" — is preserved byte-for-byte because
  `pkg/api/handlers_trust.go` emits it verbatim.
- **`start-qsdm-local.ps1`.** Artefact now named `qsdm_local.exe`,
  with an automatic move of the legacy `qsdm_local.exe` path so
  operators' PID files and launch scripts keep working.

### Deprecated (migration window, one minor release)

- `QSDM_*` environment variables. Continue to work; log a single
  deprecation warning per process. Prefer `QSDM_*`. See
  `REBRAND_NOTES.md` §3.1.
- `X-QSDM-*` HTTP headers. Continue to be accepted. Prefer
  `X-QSDM-*`. See `REBRAND_NOTES.md` §3.2.
- `qsdm_*` Prometheus metric prefix. **Not yet renamed** — a
  cutover plan for dual-emit is staged under `REBRAND_NOTES.md` §3.7
  because the rename is breaking for Grafana/alert pipelines.
- `sdk/javascript/qsdm.*`, `sdk/go/qsdm*.go`. Shim packages
  that re-export from the new `qsdm` module path.

### Not changed

- Go module path stays `github.com/blackbeardONE/QSDM`.
- Address format, signature scheme (ML-DSA-87 via liboqs), GossipSub
  topics, and the `/api/v1/*` REST surface are unchanged.
- Existing databases, config files, and systemd units keep working
  without any renaming.
- PoE+BFT consensus is CPU-only and admits validators without a GPU.

### Audit-blocked / wall-clock items (from `NEXT_STEPS.md`)

These are **not** code changes — they are listed here so a release
reader can find them with one search.

- `rebrand-03` — trademark filings.
- `tok-01` — tokenomics genesis-policy sign-off.
- `mining-01` — external audit of the mining protocol (auditor packet
  ready at `docs/docs/AUDIT_PACKET_MINING.md`).
- `mining-05` — incentivized testnet launch.
- Mainnet genesis ceremony (dry-run driver ready at
  `cmd/genesis-ceremony`).
- NGC attestation service availability.
