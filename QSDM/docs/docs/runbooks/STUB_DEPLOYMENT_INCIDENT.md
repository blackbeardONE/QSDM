# Stub Deployment Incident — Operator Runbook

The single alert `QSDMStubActive` fires when a **stub-shipped
code path** is active in the running binary for ≥5m. The alert's
`kind` label tells the on-call which stub. Some stubs are
operationally **dangerous in production** (silent acceptance of
unsigned transactions, quantum-safe signature downgrade); others
are softer (CPU fallback, WASM unavailable) but still indicate
a deploy that doesn't match what the operator probably intended.

This runbook has one section per `kind` label value. The
`runbook_url` annotation in the alert anchors directly to the
right section, so an on-call clicking the page jumps to the
correct remediation immediately.

| `kind` | Severity in prod | Section |
|---|---|---|
| `poe`          | **CRITICAL — security event**    | [§ kind-poe](#kind-poe) |
| `dilithium`    | **CRITICAL — crypto downgrade**  | [§ kind-dilithium](#kind-dilithium) |
| `wallet`       | **CRITICAL — crypto downgrade**  | [§ kind-wallet](#kind-wallet) |
| `cc`           | **HIGH — CC mining offline**     | [§ kind-cc](#kind-cc) |
| `slashing`     | **HIGH — slashing partly offline** | [§ kind-slashing](#kind-slashing) |
| `mesh3d-cuda`  | LOW — performance only           | [§ kind-mesh3d-cuda](#kind-mesh3d-cuda) |
| `wasm-sdk`     | LOW — WASM modules unavailable   | [§ kind-wasm-sdk](#kind-wasm-sdk) |

> **What this runbook closes.** Before this commit, a node
> deployed without CGO would silently start, accept transactions
> *without signature verification*, and emit no Prometheus signal
> indicating that the validator was running degraded. The
> `qsdm_stub_active{kind="..."}` gauge added in
> `pkg/monitoring/stub_active_metrics.go` plus the
> `QSDMStubActive` alert close that gap: every stub-shipped path
> is now visible to a Prometheus scrape, and a deploy that hits
> any of the dangerous kinds pages on-call within 5 minutes.

---

## 1. Glossary (60-second skim)

- **Stub-shipped** — a code path that compiles into the
  binary as a placeholder for a real implementation that
  isn't available in the current build (CGO disabled,
  CUDA absent, Phase 2c-iv pending, or operator wiring
  chose the placeholder explicitly).
- **CGO** — Go's C interoperation. Required for liboqs
  (ML-DSA-87 / Dilithium quantum-safe signatures), for
  WASM via wasmer, and for the entanglement consensus
  primitives in `pkg/consensus/`.
- **liboqs** — the [Open Quantum Safe](https://openquantumsafe.org)
  library that provides ML-DSA-87 quantum-safe signatures.
  QSDM links it through CGO.
- **`qsdm_stub_active{kind="..."}`** — the gauge added in
  `pkg/monitoring/stub_active_metrics.go`. Pre-populates a
  row per canonical kind in
  `pkg/monitoring/stubactive/AllKinds()` (always present,
  value 0 or 1). The alert expression `qsdm_stub_active == 1`
  produces one alert instance per kind whose gauge is 1.
- **Phase 2c-iv** — the planned Hopper/Blackwell + NVIDIA CC
  SDK integration for the `nvidia-cc-v1` attestation path.
  Until it ships, `cc.NewStubVerifier()` is the placeholder
  registered under `mining.AttestationTypeCC`.
- **`StubVerifier` (slashing)** — the always-rejecting
  `EvidenceVerifier` registered in
  `slashing.NewProductionDispatcher` for any `EvidenceKind`
  whose real verifier hasn't shipped. A slash transaction of
  that kind gets rejected with `"<kind> verifier is a stub
  (not yet implemented)"`.

---

## 2. Pre-flight: confirm which kind is firing

```promql
qsdm_stub_active == 1
```

groups by `kind` and shows every currently-active stub. The
alert's `kind` label is the same value — anchor below.

To watch the full state at a glance (including kinds at 0):

```promql
qsdm_stub_active
```

### 2a. Wrong-binary deploy? (5-second triage)

Before opening the per-kind anchor below, check if this is
just a wrong-binary deploy:

```promql
qsdm_binary_capabilities
```

This is a single info-metric (value=1) with the binary's
build-tag-determined backends. On a Stage B+ deploy of the
QSDM tree, you expect:

```text
qsdm_binary_capabilities{dilithium="circl",mesh3d="cpu_fallback",wasm="wazero"} 1
```

If `dilithium != "circl"` (and you're not on a CGO+liboqs
host) **or** `wasm != "wazero"`, the kinds `poe`,
`dilithium`, `wallet`, `wasm_sdk` are not structurally pinned
on the running binary and the alert is detecting a real
stub-shipped path that should not exist on Stage B+. The fix
is the same: **rebuild from current head and redeploy** (see
[`STAGE_B_DEPLOY_BLR1.md`](../STAGE_B_DEPLOY_BLR1.md) for the
operational steps on the BLR1 validator). No need to read the
per-kind anchor — those four anchors below all reduce to
"wrong binary, redeploy" in 2026-05-06+.

If `qsdm_binary_capabilities` shows the expected Stage B+
labels, the firing kind is one of the still-live ones
(`mesh3d_cuda`, `cc`) and you should jump to its anchor
below for the real triage path.

---

## kind-poe

> **Severity in production: CRITICAL — historical security event.**
> **As of 2026-05-06 (Stage B), no production binary built from
> the QSDM tree can fire this alert.** The stub
> (`pkg/consensus/poe_stub.go`) has been deleted and the real
> `*ProofOfEntanglement` is unconditionally constructed under
> both CGO+liboqs and non-CGO builds. The
> `qsdm_stub_active{kind="poe"}` gauge is retained in the
> registry for forward compatibility, but no code path flips it
> on. If you see this alert, the binary on the affected node
> predates the Stage B commit — treat as a wrong-binary
> deployment.

The historical failure mode (which the alert was written to
catch) was:

```go
// Pre-Stage-B poe_stub.go, since deleted:
if poe == nil {
    if logger != nil {
        logger.Warn("ProofOfEntanglement not available (CGO disabled), accepting transaction without signature verification")
    }
    return true, nil
}
```

A node running this stub in production accepted forged
transactions on the wire as if they had valid PoE signatures.

### kind-poe — triage

1. **Identify the affected nodes.** From the alert's
   `instance` label and the dashboard, list every validator
   firing `QSDMStubActive{kind="poe"}`. Treat all of them as
   compromised (they accepted unsigned txs for ≥5m).
2. **Stop accepting from these nodes immediately.** Disable
   their libp2p peering or take them out of validator
   rotation. Operators of downstream services that rely on
   QSDM should be told the affected nodes accepted unsigned
   transactions during the incident window.
3. **Forensics.** For the incident window
   (`ALERTS{alertname="QSDMStubActive",kind="poe"}` start
   timestamp through resolution), enumerate every transaction
   accepted by the affected node — they are all suspect.
4. **Redeploy with a current binary.** Any binary built from
   2026-05-06 (Stage B) onwards has the real PoE wired
   regardless of CGO state. See remediation below.

### kind-poe — remediation

```bash
# Linux VPS (most common production target), pure-Go path:
cd QSDM/source
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ../qsdm ./cmd/qsdm
# OR, if liboqs is already on the build host and you want
# the AVX2-accelerated path:
CGO_ENABLED=1 go build -o ../qsdm ./cmd/qsdm
```

After redeploy, `qsdm_stub_active{kind="poe"}` stays at `0`
and `QSDMStubActive` auto-resolves. Stage B's parity tests
(`pkg/crypto/dilithium_circl_test.go`) cover the round-trip
that PoE depends on for both backends, so wire-format
compatibility holds across heterogeneous validator sets.

### kind-poe — escalation

If `qsdm_stub_active{kind="poe"}` somehow re-appears on a
binary that should have Stage B (verify with `qsdm --version`
and the build commit hash), that is itself a P0:
`pkg/consensus/poe_stub.go` should not exist in the tree. File
an incident with the build artifact's `git rev-parse HEAD` and
the `qsdm` binary's `--version` string — someone has
reintroduced the stub.

---

## kind-dilithium

> **Severity in production: CRITICAL — historical quantum-safe crypto downgrade.**
> **As of 2026-05-06 (Stage B), no production binary built from
> the QSDM tree can fire this alert.** `pkg/crypto/dilithium_stub.go`
> has been deleted; non-CGO builds now use
> `pkg/crypto/dilithium_circl.go` (cloudflare/circl pure-Go,
> FIPS 204 byte-compatible with the CGO+liboqs path).

The historical stub returned `nil` from `NewDilithium()` and
errored out every `Sign`/`Verify` call. In practice, every code
path that needed to produce or verify ML-DSA-87 signatures —
v2 mining proof attestation signature verification,
freshness-cheat signature recomputation, forged-attestation
evidence verification — failed.

> **Backend selection (post-Stage-B).** Two backends ship; both
> produce wire-compatible FIPS 204 ML-DSA-87 signatures:
>
> 1. **CGO + liboqs** — `pkg/crypto/dilithium.go`. Default for
>    CGO builds. Fastest (AVX2-accelerated), depends on the
>    liboqs C library being present at build and runtime.
> 2. **Pure-Go via cloudflare/circl** — `pkg/crypto/dilithium_circl.go`.
>    Default for **non-CGO builds**. No build flag required.
>    Same on-the-wire signatures as liboqs, no CGO toolchain
>    needed.
>
> The previous opt-in `dilithium_circl` build tag is now a
> no-op (the file is selected by `!cgo` alone). The
> `dilithium_stub.go` file has been removed entirely.

### kind-dilithium — triage

If this alert is firing, the binary on the node is older than
the Stage B commit. Treat it the same as `kind-poe` — wrong
binary, redeploy.

1. **Confirm the build flavour.** SSH onto the affected
   instance: check `qsdm --version` against the post-Stage-B
   commit hash. If the binary predates Stage B, that's the
   root cause.
2. **Check what's failing.** `qsdm_attestation_rejected_total`
   per `reason` label — stub builds typically see a flood
   of `signature_verification_failed` rejections.
3. **Redeploy.**
   ```bash
   cd QSDM/source
   # Pure-Go path (no CGO toolchain needed; works on Alpine,
   # Windows-cross-compile-to-Linux, etc.):
   CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ../qsdm ./cmd/qsdm
   ```
   For the perf-sensitive Linux+liboqs path see
   [§ kind-poe](#kind-poe) remediation — the same binary
   covers both kinds.

---

## kind-wallet

> **Severity in production: CRITICAL — historical quantum-safe crypto downgrade.**
> **As of 2026-05-06 (Stage B), no production binary built from
> the QSDM tree can fire this alert.** `pkg/wallet/wallet_stub.go`
> has been deleted; the real `pkg/wallet/wallet.go` now
> compiles unconditionally (`pkg/crypto.NewDilithium` supplies
> a real ML-DSA-87 signer in every build, via either liboqs or
> cloudflare/circl).

The historical stub used **SHA-256** as a stand-in for
ML-DSA-87 signatures. That was functionally a hash, not a
signature — no non-repudiation, no key-binding, no quantum
safety. The wallet's `CreateTransaction` produced JSON that
other v2 QSDM nodes (running the real wallet) rejected as
invalid.

### kind-wallet — triage

If this alert is firing, the binary predates Stage B. Same
remediation as [§ kind-dilithium](#kind-dilithium): rebuild
from current head and redeploy. The `qsdm_stub_active{kind="wallet"}`
gauge is retained for forward compatibility but no code path
flips it on under any supported build configuration.

---

## kind-cc

> **Severity in production: HIGH — nvidia-cc-v1 mining admission offline.**

The `cc.StubVerifier` (`pkg/mining/attest/cc/stub.go`) is the
**Phase 2c-iv placeholder**: it satisfies the
`mining.AttestationVerifier` interface so the dispatcher's
`AssertAllRegistered` check passes at boot, but its
`VerifyAttestation` method **rejects every nvidia-cc-v1 proof**
with `ErrNotYetAvailable`. From the package doc:

> Replacing this stub with the real implementation is expected
> to be a single-file swap; the registration surface via
> `cc.NewStubVerifier` is the contract.

A validator running the stub admits **zero** v2 mining proofs
of `Type="nvidia-cc-v1"`. This is the right behaviour while
the real verifier (AIK chain validation, quote parsing, PCR
comparison against a pinned reference manifest) is in
development — but in a production deploy that expects to admit
nvidia-cc-v1 proofs, it's an outage of the entire CC mining
admission path.

### kind-cc — triage

1. **Confirm the validator is supposed to admit CC proofs.**
   Some deploys only run the v1 NGC path; the CC stub being
   active on those is harmless and the alert can be silenced
   at the Alertmanager level for that operator. The default
   shipped wiring registers the stub, so a fresh deploy will
   page on this until the operator explicitly silences it
   for non-CC subnets.
2. **If CC proofs ARE expected:** the validator binary
   must be rebuilt with the real verifier wired in place
   of `cc.NewStubVerifier()`. The wiring point is the
   dispatcher registration in the validator binary's
   main wiring code. Search:

   ```bash
   rg 'cc\.NewStubVerifier|AttestationTypeCC' QSDM/source
   ```

3. Phase 2c-iv timeline: track upstream issue / changelog
   entry. Until it ships, the stub is the correct behaviour.

---

## kind-slashing

> **Severity in production: HIGH — slashing for stub-wired EvidenceKinds is silently disabled.**
>
> **As of `internal/v2wiring`'s switch to `freshnesscheat.NewProductionSlashingDispatcher`, no production binary wires a `StubVerifier`** — every `EvidenceKind` in `slashing.AllEvidenceKinds` (`forged-attestation`, `double-mining`, `freshness-cheat`) reaches its real `EvidenceVerifier`. If `qsdm_stub_active{kind="slashing"} == 1` you are looking at either (a) a binary that still uses a hand-rolled `slashing.NewProductionDispatcher` call without all three slots filled, or (b) a future `EvidenceKind` that was added to `AllEvidenceKinds` without a matching wiring update. The integration test `TestWire_SlashingDispatcherCoversAllKinds` in `internal/v2wiring/v2wiring_test.go` is the regression guard.

`slashing.StubVerifier` (in `pkg/mining/slashing/verifier.go`) is
an always-rejecting `EvidenceVerifier`. The production
dispatcher (`slashing.NewProductionDispatcher`) wires it in for
any `EvidenceKind` whose `ProductionConfig` slot is left nil.

The flag is set by `Dispatcher.Register()` in
`pkg/mining/slashing/verifier.go` when the verifier passed in is
a `StubVerifier`. So `qsdm_stub_active{kind="slashing"} == 1`
means **at least one EvidenceKind is wired to the stub**, not
that the entire slashing surface is offline.

A slash transaction submitted for a stub-wired EvidenceKind is
rejected by every validator with the message
`"<kind> verifier is a stub (not yet implemented)"`. This is
fail-closed (the slash doesn't apply) but it also means the
attack vector that EvidenceKind was supposed to deter is
**unprotected** until the real verifier ships.

> **freshness-cheat is NOT a stub.** The freshness-cheat
> verifier ships fully implemented; in production it runs
> against `freshnesscheat.RejectAllWitness` because the BFT-
> finality block-inclusion oracle hasn't shipped yet (see
> `MINING_PROTOCOL_V2.md §12.3`). Slash txs of that kind are
> still rejected, but with kind-specific structural / staleness /
> witness errors (richer operator diagnostics) — the
> `qsdm_stub_active` flag stays at 0.

### kind-slashing — triage

1. **Identify which EvidenceKinds are stub-wired.** Look at
   the validator's startup log for messages like
   `"slashing: registered StubVerifier for kind <kind>"`.
   Cross-reference with `slashing.AllEvidenceKinds`.
2. **If the binary uses `internal/v2wiring`, this should be
   impossible.** Check whether the binary calls
   `v2wiring.Wire(...)` (which uses
   `freshnesscheat.NewProductionSlashingDispatcher`) or rolls
   its own dispatcher. If the latter, switch it to
   `freshnesscheat.NewProductionSlashingDispatcher` — that
   covers all three current EvidenceKinds without manual
   slot bookkeeping.
3. **If a NEW EvidenceKind is the offender:** add a real
   verifier sub-package (mirroring `forgedattest/`,
   `doublemining/`, `freshnesscheat/`) and extend
   `freshnesscheat.NewProductionSlashingDispatcher` (and the
   `slashing.ProductionConfig`) with a slot for it.
4. **For stub-wired kinds, surface the gap publicly.** The
   subnet community needs to know that slashing for that
   EvidenceKind isn't enforced until the real verifier ships.
5. **Cross-link:** see also
   [SLASHING_INCIDENT.md](./SLASHING_INCIDENT.md) for the
   slashing-pipeline runbook (apply rates, reject reasons,
   forfeiture caps).

---

## kind-mesh3d-cuda

> **Severity in production: LOW — CUDA acceleration unavailable; CPU fallback in use.**

`pkg/mesh3d/cuda_stub.go` is selected at build time when CUDA
isn't available. `NewCUDAAccelerator()` returns nil, and the
mesh3d package falls back to the CPU path. **The CPU path is
correct** — meshes are still validated, transactions still
flow — but throughput is far below what a CUDA-enabled deploy
can handle.

### kind-mesh3d-cuda — triage

1. **Confirm the operator expected CUDA.** Some deploys are
   intentionally CPU-only (test clusters, CI). For those,
   silence the alert per-subnet at Alertmanager.
2. **For deploys that should have CUDA:** the build host
   needs CUDA toolkit installed and the build needs to
   target a supported platform (Linux x86_64 with CUDA, in
   particular). Check the build flag matrix in
   `pkg/mesh3d/cuda_stub.go`'s `//go:build` directive:

   ```go
   //go:build cgo && !(windows || (linux && cuda))
   ```

3. Rebuild with `tags='cgo cuda'` on a CUDA-enabled host.

---

## kind-wasm-sdk

> **Severity in production: LOW — historical "WASM modules will
> not load" stub.**
> **As of 2026-05-06 (Stage B, commit 57ef2cf + wasm Stage B),
> no production binary built from the QSDM tree can fire this
> alert.** Both stub backends (`pkg/wasm/sdk_stub.go` and
> `pkg/wasm/sdk_wasmtime_disabled.go`) have been deleted; the
> wazero pure-Go backend in `pkg/wasm/sdk_wazero.go` is now
> the unconditional default for every native target the
> binary ships on (`!js || !wasm`). `qsdm_stub_active{kind="wasm_sdk"}`
> stays at 0 in every supported build configuration.

The historical stub returned an error for every `NewWASMSDK()`
call when CGO was off, or when CGO was on but wasmtime DLLs
weren't installed. WASM module hooks (`[wasm.*]` config
sections) couldn't be loaded — though the validator itself
ran fine without them.

> **Backend selection (post-Stage-B).** A single backend ships:
>
> - **Pure-Go wazero** — `pkg/wasm/sdk_wazero.go`. No CGO,
>   no DLLs, runs everywhere the QSDM binary already runs.
>   Real `*WASMSDK` with `NewWASMSDK`, `CallFunction`, and
>   `preflightP2PTransactionJSON` all backed by
>   `github.com/tetratelabs/wazero`.
>
> The opt-in `wasm_wazero` build tag from Stage A is now a
> no-op alias. CGO+wasmtime as a separate backend was never
> actually wired in the tree (only stubs existed); if a
> future Stage C wants to add it, the `wasmtime_available`
> tag is the path.

### kind-wasm-sdk — triage

If this alert is firing on a Stage-B-or-later binary, the
expectation is that it cannot. Treat as a wrong-binary
deployment:

1. **Confirm the build flavour.** SSH onto the affected
   instance: `qsdm --version` should show the build commit.
   If it predates `57ef2cf` (or the wasm Stage B commit),
   that's the root cause.
2. **Redeploy.** Same procedure as the
   [§ kind-dilithium](#kind-dilithium) /
   [§ kind-poe](#kind-poe) remediation — rebuild from current
   head, `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build`.
3. **If you don't need WASM:** remove the WASM config and
   any flicker resolves on the next scrape.

If `qsdm_stub_active{kind="wasm_sdk"}` re-appears on a binary
that should have wasm Stage B (verify with `qsdm --version`
and the build commit hash), that is itself a P0:
`pkg/wasm/sdk_stub.go` and `pkg/wasm/sdk_wasmtime_disabled.go`
should not exist in the tree. File an incident with the build
artifact's `git rev-parse HEAD` and the `qsdm` binary's
`--version` string — someone has reintroduced a stub.

---

## 3. Cross-references

- `pkg/monitoring/stubactive/stubactive.go` — the leaf
  registry every stub init() writes to.
- `pkg/monitoring/stub_active_metrics.go` — the bridge that
  emits `qsdm_stub_active{kind="..."}` in OpenMetrics output.
- `QSDM/deploy/prometheus/alerts_qsdm.example.yml` — the
  `qsdm-stub-active` group with the `QSDMStubActive` alert
  definition.
- `QSDM/deploy/grafana/dashboards/qsdm-runbook-stub-deployment-incident.json`
  — the auto-generated panel for this runbook.
- [OPERATOR_HYGIENE_INCIDENT.md](./OPERATOR_HYGIENE_INCIDENT.md)
  — for adjacent operator-resolvable hygiene alerts.
- [SLASHING_INCIDENT.md](./SLASHING_INCIDENT.md) — for the
  slashing-pipeline view that complements [§ kind-slashing](#kind-slashing).

---

## 4. Why a single alert with `kind` labels (not 7 alerts)

The same operational pattern (stub active, redeploy, repeat
on next scrape until resolved) applies to every kind. Each
alert instance has its own `kind` label, so Alertmanager
groups them by alertname (and the on-call sees one combined
ticket per scrape) but routing/silencing/dashboards still
discriminate by kind via standard Prometheus selectors. The
runbook anchor template
`STUB_DEPLOYMENT_INCIDENT.md#kind-{{ reReplaceAll "_" "-" $labels.kind }}`
points the on-call directly at the right section, avoiding
the "one giant runbook with seven sections, no anchor"
anti-pattern.
