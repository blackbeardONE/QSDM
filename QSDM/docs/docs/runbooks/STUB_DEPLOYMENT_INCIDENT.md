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

---

## kind-poe

> **Severity in production: CRITICAL — security event.**

The non-CGO PoE stub (`pkg/consensus/poe_stub.go`) returns
`nil` from `NewProofOfEntanglement()` and its
`ValidateTransaction(...)` method **accepts every transaction
without signature verification** when invoked on a `nil`
receiver. From `pkg/consensus/poe_stub.go:38-46`:

```go
if poe == nil {
    if logger != nil {
        logger.Warn("ProofOfEntanglement not available (CGO disabled), accepting transaction without signature verification")
    }
    // Without crypto, we can't validate signatures, but we can still accept transactions
    // for testing purposes
    return true, nil
}
```

A node running this stub in production accepts forged
transactions on the wire as if they had valid PoE signatures.
This is **not an alertable transient** — it's a wrong-binary
deployment, and the only correct response is to redeploy.

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
4. **Redeploy with CGO.** Rebuild with `CGO_ENABLED=1` and
   liboqs available on the build host. The real PoE
   implementation in `pkg/consensus/poe.go` (CGO-tagged)
   replaces the stub at link time.

### kind-poe — remediation

```bash
# On the build host:
export CGO_ENABLED=1
# Ensure liboqs is installed (oqs-provider for OpenSSL works
# for the link step; the runtime needs the .so/.dylib).
cd QSDM/source
go build -tags='cgo' -o ../qsdm ./cmd/qsdm
```

After redeploy, `qsdm_stub_active{kind="poe"}` flips to `0`
and `QSDMStubActive` auto-resolves.

### kind-poe — escalation

If you cannot rebuild with CGO (build environment doesn't
support liboqs), the node **must not run as a validator**.
Take it out of rotation and treat it as a non-mining read
replica only.

---

## kind-dilithium

> **Severity in production: CRITICAL — quantum-safe crypto downgrade.**

`pkg/crypto/dilithium_stub.go` (non-CGO build) returns `nil`
from `NewDilithium()` and every `Sign`/`Verify`/`VerifyWithPublicKey`
returns `errors.New("Dilithium not available: CGO and liboqs required")`.

In practice, this means any code path that needs to **produce or
verify ML-DSA-87 signatures** errors out — including v2 mining
proof attestation signature verification, freshness-cheat
signature recomputation, and forged-attestation evidence
verification.

When this fires alone (without `kind="poe"`), it usually means
PoE itself doesn't need Dilithium on the hot path but a downstream
verifier does.

### kind-dilithium — triage

1. **Confirm the build flavour.** SSH onto the affected
   instance: `qsdm --version` should show whether CGO was on
   at build time. If the binary has CGO disabled, that's the
   root cause.
2. **Check what's failing.** Look at `qsdm_attestation_rejected_total`
   per `reason` label — non-CGO builds typically see a flood
   of `signature_verification_failed` rejections.
3. **Redeploy with CGO + liboqs**, same as
   [§ kind-poe](#kind-poe) remediation.

---

## kind-wallet

> **Severity in production: CRITICAL — quantum-safe crypto downgrade.**

`pkg/wallet/wallet_stub.go` (non-CGO build) uses **SHA-256**
to "sign" transactions and "verify" signatures. This is
functionally a hash, not a signature — it provides no
non-repudiation, no key-binding, and no quantum safety.

The wallet's `CreateTransaction` produces JSON that other v2
QSDM nodes will reject as having an invalid signature, **unless
those other nodes are also running the stub**. In a mixed
deployment (some CGO, some not), this stub produces
transactions that the network rejects.

### kind-wallet — triage

1. Same root cause as [§ kind-dilithium](#kind-dilithium):
   CGO not enabled at build time.
2. Confirm via `qsdm --version` and look for the binary's
   CGO marker.
3. Redeploy with CGO + liboqs.

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

> **Severity in production: LOW — WASM modules will not load.**

`pkg/wasm/sdk_stub.go` (non-CGO build) and
`pkg/wasm/sdk_wasmtime_disabled.go` (CGO build with no wasmtime
DLLs) both return an error for every `NewWASMSDK()` call. WASM
module hooks (`[wasm.*]` configuration sections) cannot be
loaded — but the validator itself runs fine without them.

> **The flag is opt-in.** The stub flag flips on the FIRST
> `NewWASMSDK()` call, NOT at process start. A non-CGO node
> that doesn't configure any WASM module will never trigger
> `qsdm_stub_active{kind="wasm_sdk"}` — the contracts engine
> uses pure-Go wazero, and wallet WASM is opt-in via the
> `wasm_modules/wallet/wallet.wasm` path. If you ARE seeing
> the alert fire, the operator's config (or build) tried to
> wire a WASM module, the construction failed, and the
> validator is now running with the simulation/wazero
> fallbacks instead.

### kind-wasm-sdk — triage

1. **Confirm whether WASM modules are configured.** If the
   operator's config has no `[wasm.*]` sections AND no
   `wasm_modules/wallet/wallet.wasm` is loaded at startup,
   the alert was triggered by a one-off load attempt during
   the current process lifetime; restart the validator to
   clear the flag, or silence the alert if the dangling
   load attempt is benign.
2. **If WASM modules ARE configured:** they will fail to
   load on every restart. The operator should either remove
   the WASM config or rebuild with CGO + wasmtime DLLs (or
   migrate the module to wazero, which runs CGO-free).

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
