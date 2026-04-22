# Major Update — QSDM + Cell (EXECUTED)

> **Archive note (2026-04-22).** Phases 1–5 of this plan have been executed
> in-repo. The wall-clock-blocked items — Phase 0 counsel sign-off,
> trademark filings, Phase 4 incentivized testnet, Phase 6 external CUDA
> audit, and the mainnet genesis ceremony — are tracked in
> [`../../../NEXT_STEPS.md`](../../../../NEXT_STEPS.md) and in
> [`pkg/audit/checklist.go`](../../../source/pkg/audit/checklist.go)
> under the new `rebrand`, `tokenomics`, `mining_audit`, and
> `trust_api` categories.
>
> This file is preserved verbatim for historical reference; do **not**
> edit it. All normative spec lives in:
>
> - [`../REBRAND_NOTES.md`](../REBRAND_NOTES.md) — rebrand scope and deprecation shim.
> - [`../CELL_TOKENOMICS.md`](../CELL_TOKENOMICS.md) — Cell supply, emission schedule, fees.
> - [`../NODE_ROLES.md`](../NODE_ROLES.md) — validator vs miner hardware / build profile.
> - [`../VALIDATOR_QUICKSTART.md`](../VALIDATOR_QUICKSTART.md) — VPS operator runbook.
> - [`../MINING_PROTOCOL.md`](../MINING_PROTOCOL.md) — PoW sub-protocol (normative).
> - [`../MINER_QUICKSTART.md`](../MINER_QUICKSTART.md) — CPU reference miner runbook.
> - [`../NVIDIA_LOCK_CONSENSUS_SCOPE.md`](../NVIDIA_LOCK_CONSENSUS_SCOPE.md) — trust endpoint scope note.

**Status:** Draft — awaiting sign-off before execution. *(original header, kept for history)*
**Owner:** project lead.
**Scope:** product rebrand, native coin launch, GPU-mining economic layer, documentation and website overhaul.
**Target:** first internal milestone in 4 weeks; public launch window to be decided after legal review.

---

## 1. Executive summary

QSDM+ becomes **QSDM**, a quantum-secure dynamic mesh ledger with a home-miner-driven native coin called **Cell (CELL)**.

The product identity changes along three axes at once:

1. **Name change** — drop the `+` suffix. Product, repo, module path, docs, website, and dashboards become plain "QSDM".
2. **Native coin** — introduce **Cell (CELL)** as the chain's native unit. Validators (VPS, CPU-only) earn Cell from transaction fees. Home miners (GPU, NVIDIA-favored) earn newly-emitted Cell by producing mining proofs.
3. **Two-tier node model** — **primary / validator nodes run on VPS hardware (CPU only)** and are responsible for consensus, finality, and tx ordering. **Miners run at home on GPUs** and are responsible for the Cell emission schedule. These are distinct roles with distinct economics.

The existing PoE + BFT consensus is **unchanged**. Mining is additive — a new reward track that sits alongside consensus, not a replacement for it.

The "buy an NVIDIA GPU to mine Cell" pitch drives retail GPU demand. Validators' VPS sizing story remains unchanged: CPU, RAM, disk — no GPU needed.

---

## 2. Before vs. after — the mental model

| Dimension | **Before (QSDM+)** | **After (QSDM with Cell)** |
|---|---|---|
| Product name | QSDM+ | **QSDM** |
| Native coin | (unnamed, functional) | **Cell (CELL)** — minted, emitted, fee-denominated |
| Who produces blocks | Validators (PoE + BFT selection) | **Validators (VPS, CPU only)** — unchanged |
| Who earns by securing the chain | Validators (tx fees) | **Validators** earn Cell tx fees |
| Who earns by emitting new coins | N/A (no emission) | **Miners** (home, GPU, NVIDIA-favored) earn newly-emitted Cell |
| Consensus security | PoE + BFT | **PoE + BFT** — unchanged |
| GPU required on VPS? | No | **No** — explicit, documented, enforced by config defaults |
| GPU required for miners? | N/A | **Yes** — CUDA-optimized mining algorithm |
| Energy cost | PoS-class (negligible) | PoS-class for validators + GPU-mining-class for miners (bounded by emission schedule) |
| Mining algorithm | None | Memory-hard, CUDA-tuned PoW (design detailed §5) |
| Public marketing story | "Quantum-secure ledger" | "Quantum-secure ledger + mine Cell with your gaming GPU at home" |

---

## 3. Goals in this update

1. **Rename** everything user-facing from "QSDM+" to "QSDM". Repo directories, Go module path, binaries, docs, dashboards, CI workflows, website, SDKs.
2. **Introduce Cell as the native coin** with a fair-launch, fixed-supply tokenomics model. No pre-mine. No team allocation larger than 10% (with vesting).
3. **Split node roles** explicitly into *primary/validator nodes* (VPS, CPU) and *miner nodes* (home, GPU). Document hardware profiles for both. Make it impossible to misconfigure.
4. **Design and ship a CUDA-optimized mining algorithm** that issues Cell on a published emission schedule.
5. **Overhaul documentation** — `ROADMAP.md`, `NEXT_STEPS_QSDMPLUS.md` → `NEXT_STEPS.md`, every `README.md`, `SCYLLA_CAPACITY.md`, `NVIDIA_LOCK_CONSENSUS_SCOPE.md`, and produce new docs for tokenomics, mining, home-miner setup, validator operator runbook.
6. **Overhaul `qsdm.tech`** — landing page, product pitch, miner quick-start, validator quick-start, tokenomics page, block explorer, brand kit.

Non-goals (explicitly out of scope for this update):

- Changing the underlying PoE + BFT consensus mechanics.
- Changing the PQ signature scheme (ML-DSA-87 stays).
- Changing the storage backends (SQLite + ScyllaDB stay).
- Running our own mining pool (miners can join a third-party pool or run solo).
- Listing CELL on any exchange (handled separately after launch).

---

## 4. The Cell coin — tokenomics

This section is the **business decision layer**, not the implementation. Values here are proposals. Nothing in this section is legally binding until reviewed by counsel.

### 4.1 Supply model — proposal

| Parameter | Value | Rationale |
|---|---|---|
| **Total cap** | **100,000,000 CELL** (100 M) | Round number; enough divisibility for retail; not so small it invites hoarding. |
| **Decimals** | 8 | Matches Bitcoin. Smallest unit = 1 satoshi-equivalent = `0.00000001 CELL`. Call it a **"micell"** or **"cytoplasm"** if you want a thematic name. |
| **Pre-mine** | **0%** | Fair launch. Maximum regulatory defense. Maximum community credibility. |
| **Genesis allocation** | **10% treasury / dev fund**, vested linearly over 48 months, locked on-chain | Funds ongoing development. Vesting prevents dump. This is 10 M CELL. Treasury address is published in genesis. |
| **Mining emission** | **90%** (90 M CELL) issued to miners over ~20 years | Classic Bitcoin-style curve with **halvings every 4 years**. |
| **Fee model** | **Fee-only for validators** (no block subsidy) | Validators never dilute holders. Miners dilute on a predictable schedule; validators don't. |
| **Burn** | **Optional EIP-1559-style base-fee burn** | Keeps supply deflationary post-emission. Decide before genesis. |

### 4.2 Emission schedule — proposal

Halving every 4 years, target block time of 10 seconds (same as Ethereum-class; configurable in `pkg/chain` today):

| Epoch | Years | Block reward | Cumulative emission | % of mining cap |
|---|---|---|---|---|
| 1 | 0–4 | 1.4280 CELL / block | 45,000,000 | 50% |
| 2 | 4–8 | 0.7140 CELL / block | 67,500,000 | 75% |
| 3 | 8–12 | 0.3570 CELL / block | 78,750,000 | 87.5% |
| 4 | 12–16 | 0.1785 CELL / block | 84,375,000 | 93.75% |
| 5+ | 16–20+ | halves every 4y | → 90,000,000 asymptotically | → 100% |

(Numbers assume 10 s blocks = ~12.6 M blocks per 4-year epoch. Adjust if target block time differs.)

### 4.3 What Cell is used for

- **Transaction fees** on QSDM. Paid to validators (VPS operators).
- **Smart contract execution** (WASM contracts already support a gas model — denominate it in Cell).
- **Staking / validator bonds** (future — lets validators put skin in the game).
- **Bridge collateral** (future — the bridge protocol already exists; denominate bond in Cell).
- **Governance weight** (future — one CELL = one vote on protocol upgrades).

### 4.4 Distribution philosophy — the legal posture

**Fair launch, utility-first.** No public sale, no private sale, no ICO, no presale allocations. The treasury allocation is transparent on-chain, time-locked, and spent only on development per a published policy. Miners earn by mining. Validators earn by validating. Users use Cell to pay fees. **At no point do we sell Cell.**

This is the cleanest posture against securities classification in the US, UK, and EU. It is not a guarantee — get counsel involved before public launch.

---

## 5. Mining layer — design

This is the new technical work. The mining layer is a bolt-on subsystem that lives alongside PoE, not inside it.

### 5.1 Goals

| Goal | How it's met |
|---|---|
| **Home-miner friendly** | GPU-accessible, runs on Windows + Linux, single-binary miner, clear setup docs |
| **NVIDIA-favored** | CUDA-tuned kernels with architecture-specific optimizations (tensor cores, async memory, specific shader patterns) |
| **ASIC-resistant** | Memory-hard algorithm with large (2–4 GB) dataset per epoch, mutates periodically |
| **Useful work (optional v2)** | Miners' proofs double as mesh3D parent-cell validators; the chain benefits from their compute |
| **Separable from consensus** | PoE validators keep producing blocks even if mining halts — no circular dependency |
| **Verifiable cheaply** | Validators (CPU-only VPS) must be able to verify miner proofs in <100 ms; mining is hard, verification is fast |

### 5.2 Algorithm choice — three candidates

| Candidate | Design | Pros | Cons |
|---|---|---|---|
| **A. KawPow-class** (memory-hard, GPU-favored, Ravencoin's algorithm) | 3 GB epoch DAG + programmatic shader | Battle-tested, exactly the "GPU mining" UX people recognize | Mining work is not useful; pure economic lottery |
| **B. Autolykos v2** (Ergo) | Memory-hard, solution-based, friendly to small solo miners | Small-miner friendly, well-documented | Same: work is not useful |
| **C. Mesh3D-tied useful PoW** (custom) | Miners batch-validate mesh3D parent-cells; valid batches with lowest hash wins the block reward | Work is genuinely useful; ties directly to QSDM's mesh3D identity | Needs fresh design, audit, and validation path on VPS |

**Recommendation: start with (C) as the design target, ship (A) as a v1 if (C) isn't audit-ready by launch.** (C) is the most honest story ("GPUs are doing real work for the network"). (A) is the most boring but most predictable.

### 5.3 Proof-and-reward flow (target design)

```
┌─────────────────────────────────────────────────────────────┐
│ 1. Home miner (NVIDIA GPU)                                  │
│    - Subscribes to the current epoch DAG + mesh3D work set. │
│    - Runs CUDA kernel: finds nonce such that                │
│      H(block_header || nonce || mesh3d_batch_root) < target │
│    - Submits {nonce, batch_root, attestation}               │
└────────────────────────┬────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────┐
│ 2. Primary/validator node (VPS, CPU)                        │
│    - Verifies proof in O(1) hash ops + mesh3d batch check.  │
│    - If valid, includes mining-reward tx paying miner.      │
│    - Mining-reward tx mints `block_reward` CELL → miner.    │
│    - Block enters PoE + BFT finality as normal.             │
└─────────────────────────────────────────────────────────────┘
```

Validators still decide block order — mining does not decide who proposes. Miners earn by *contributing* the winning proof for a block the proposer assembles. This keeps PoE's safety properties intact.

### 5.4 NVIDIA-favored, not NVIDIA-exclusive

Two stances, one recommendation:

**Stance 1 — "Friendly":** Algorithm uses memory access patterns, shader instruction mixes, and (optionally) tensor-core ops that are **dramatically more efficient on NVIDIA architectures** than AMD or integrated GPUs. AMD miners can technically participate but earn ~30–60% of what an equivalent-TDP NVIDIA card earns. This is **legally defensible, ethically fine, and still drives 80%+ of purchases toward NVIDIA** in practice. This is what Ergo, Kaspa, and old Ravencoin did.

**Stance 2 — "Exclusive":** Algorithm actively rejects non-NVIDIA proofs (checks CUDA runtime signatures, device IDs, attestations). **Strongly not recommended** — creates antitrust exposure in several jurisdictions, fragile against spoofing, and places the project in the category of "single-vendor chains" which historically die from lack of ecosystem support.

**Recommendation: Stance 1.** Market it as "optimized for NVIDIA GPUs" — technically accurate, legally safe, and operationally identical to what people buy NVIDIA for. Plan to revisit Stance 2 only after a formal partnership with NVIDIA (not before).

### 5.5 What this means for `pkg/mesh3d` and `pkg/monitoring/nvidia_lock`

- `pkg/mesh3d/cuda.go` — becomes the **core** of the mining algorithm under design (C). Production-harden it. Today it's tagged as "reference / hardening external" in the roadmap; it moves to P0.
- `pkg/monitoring/nvidia_lock.go` — **renamed and repurposed**. Today it gates API calls. Going forward, keep it as an *optional operator policy* for validator nodes, but the miner software uses a different, simpler GPU-attestation scheme (signed CUDA driver handshake) that's mining-specific.

### 5.6 New packages / binaries to build

| New artifact | Purpose |
|---|---|
| `pkg/mining/` | Core mining protocol — epoch management, target difficulty, proof verification. CPU-safe (verification only). |
| `pkg/mining/cuda/` (CGO, build-tagged) | CUDA kernels for the mining algorithm. Miners only. |
| `cmd/qsdmminer/` | Standalone miner binary. Talks to any QSDM validator over HTTP/gRPC. Ships for Windows + Linux. |
| `sdk/go/mining.go` | Go bindings for pool operators and miner UX. |
| `sdk/javascript/mining.js` | JS bindings for browser-based miner dashboards (optional). |
| `docs/docs/MINING_PROTOCOL.md` | Spec. Normative. Auditable. |
| `docs/docs/MINER_QUICKSTART.md` | Home-user tutorial. Screenshots of downloading + running. |
| `docs/docs/CELL_TOKENOMICS.md` | Supply schedule, emission curve, fee model, burn policy. |

---

## 6. VPS (primary/validator) node — the "no-GPU" guarantee

To make the "VPS = no GPU" rule real (not just documentation):

1. **Config default:** `mining_enabled = false` on validator nodes. Validators never initialize CUDA, never link liboqs GPU paths, never expose mining endpoints.
2. **Build tag:** a pure-Go validator build tag (`-tags validator_only`) that **strips** any CUDA/CGO GPU code from the binary at compile time. Validators ship this build; miners ship a separate build.
3. **Dockerfile split:** `Dockerfile.validator` (CPU-only, slim, Alpine-based) and `Dockerfile.miner` (CUDA base image, ~6× larger). Different CI lanes publish different images.
4. **K8s manifests:** `deploy/kubernetes/validator-statefulset.yaml` with `nodeSelector` requiring CPU-only nodes; no GPU tolerations.
5. **`SCYLLA_CAPACITY.md` updated:** reaffirm that validator sizing has zero GPU line items. Add an explicit negative statement: *"Validator nodes must not be provisioned on GPU-enabled hardware. GPU instances cost more and provide no consensus benefit."*
6. **Dashboard indicator:** `/api/v1/status` returns `"node_role": "validator" | "miner" | "both"` so operators can verify their node is configured correctly at a glance.
7. **Refuse to start with the wrong config:** validator builds fail-fast if `mining_enabled=true` is set; miner builds fail-fast if no GPU is detected at startup. This makes misconfiguration impossible instead of merely unsupported.

---

## 7. Rebrand — QSDM+ → QSDM

This is a large mechanical change touching every layer. Below is the complete inventory.

### 7.1 Code

| Target | Current | After |
|---|---|---|
| Go module path | `github.com/blackbeardONE/QSDM` | **unchanged** (already `QSDM`, no `+`) |
| Root directory | `e:\Projects\QSDM+\` | `e:\Projects\QSDM\` |
| `pkg/branding/branding.go` `Name` | `QSDM+` | `QSDM` |
| `pkg/branding` — new constants | — | `CoinName = "Cell"`, `CoinSymbol = "CELL"`, `CoinDecimals = 8` |
| Env var prefix | `QSDMPLUS_*` / `QSDM_*` (mixed) | **`QSDM_*` only** — legacy `QSDMPLUS_*` accepted with a deprecation warning for 6 months, then removed |
| HTTP header `X-QSDMPLUS-NGC-Secret` | preferred | deprecated; `X-QSDM-NGC-Secret` is now preferred |
| Binary names | `qsdmplus`, `qsdmcli` | **`qsdm`, `qsdmcli`**; add new `qsdmminer` |
| Log prefix | `QSDM+: ` | `QSDM: ` |
| `NEXT_STEPS_QSDMPLUS.md` | filename | rename to `NEXT_STEPS.md` |
| Go SDK package name | `qsdmplus` | **`qsdm`** (breaking; version bump to v2.0) |
| JS SDK package name | `qsdmplus` | **`qsdm`** (breaking; version bump to v2.0) |
| `QSDMPlusClient` class | name | `QSDMClient` — keep `QSDMPlusClient` as a deprecated alias for one minor release |

### 7.2 Documentation

Files that must be updated (every mention of "QSDM+", every mention of absent coin, every mention of "optional GPU" where it's now role-specific):

- `README.md` (root)
- `QSDM/README.md`
- `QSDM/docs/docs/ROADMAP.md`
- `QSDM/docs/docs/SCYLLA_MIGRATION.md`
- `QSDM/docs/docs/SCYLLA_CAPACITY.md`
- `QSDM/docs/docs/NVIDIA_LOCK_CONSENSUS_SCOPE.md` — **rewrite** to reflect the new two-tier model
- `QSDM/docs/docs/FEATURE_ENHANCEMENTS_COMPLETE.md`
- `NEXT_STEPS_QSDMPLUS.md` → rename to `NEXT_STEPS.md`
- `Major Update.md` (this file — archive after execution)

New files to create:

- `QSDM/docs/docs/CELL_TOKENOMICS.md` — supply schedule, emission curve, fee model, distribution philosophy.
- `QSDM/docs/docs/MINING_PROTOCOL.md` — normative mining spec.
- `QSDM/docs/docs/MINER_QUICKSTART.md` — home-user tutorial, ~10 minutes start to first mined block.
- `QSDM/docs/docs/VALIDATOR_QUICKSTART.md` — VPS operator runbook.
- `QSDM/docs/docs/NODE_ROLES.md` — the two-tier model explainer.
- `QSDM/docs/docs/REBRAND_NOTES.md` — developer-facing note on env-var migration and deprecations.
- `QSDM/docs/docs/BRAND_KIT.md` — logo usage, colour palette, tagline rules.

### 7.3 CI / CD

- `.github/workflows/qsdmplus-go.yml` → `qsdm-go.yml`
- `.github/workflows/qsdmplus-scylla-staging.yml` → `qsdm-scylla-staging.yml`
- `release-container.yml` — split into **two** publishing lanes: `qsdm-validator:*` and `qsdm-miner:*`.
- Add **`mining-kernels.yml`** — builds and tests the CUDA kernels on a CUDA-capable runner (GitHub Actions `gpu` runner, self-hosted if needed).
- Add **`sdk-js.yml`** — runs `node --test` on every push to `sdk/javascript/**`.
- Add **`audit-gate.yml`** — runs `cmd/auditreport -gate` on every PR to prevent regressions on the 52-item checklist.

### 7.4 Dashboard

- Change title, header, favicon to plain "QSDM".
- Add **"Network: CELL"** pill next to the logo.
- Add a **Tokenomics** panel showing current supply, current block reward, next halving ETA, and total treasury balance.
- Add a **Mining** panel showing current network hashrate (from miner proofs), difficulty, and the last 10 miners to win a block.
- Keep the existing topology / peers / metrics panels unchanged.

---

## 8. Website — qsdm.tech

Current `qsdm.tech` positions the product as a generic quantum-secure chain. Post-update it must position **QSDM + Cell + home mining** as the consumer-facing story.

### 8.1 Site map (proposed)

```
/                           Landing — "Quantum-secure. Mine Cell at home."
/mine                       Miner quick-start (download, plug in GPU, start earning)
/validate                   Validator / VPS operator quick-start
/cell                       Tokenomics page — supply, emission curve, halving countdown
/tech                       Technical overview — PoE + BFT + PQ crypto + Scylla
/trust                      Attested network — live NVIDIA-lock / NGC attestation feed
/docs                       Links to markdown docs
/explorer                   Block explorer (subdomain or embed)
/brand                      Brand kit — logos, colours, usage
/about                      Team + mission + governance
```

### 8.2 Landing page — must-have elements

1. **Hero.** Headline: *"Mine Cell. Secure the future."* Subhead: *"QSDM is a quantum-secure blockchain. Turn your NVIDIA GPU into a home miner."* One primary CTA: *"Start mining →"*. One secondary CTA: *"Run a validator →"*.
2. **Live network stats strip.** Total CELL mined / remaining. Current block. Active miners. Active validators. Block time. (Backed by `/api/v1/status` + `/api/v1/network/topology`.)
3. **"How it works" 3-panel.** (1) Quantum-secure chain. (2) NVIDIA GPU mines Cell. (3) VPS validators confirm blocks. Each with a 40-word explainer.
4. **Why Cell / why QSDM.** Three bullets: quantum-safe signatures, fair launch with 0% pre-mine, GPU mining from your bedroom.
5. **Attested validators widget** — live count of validators running **NVIDIA-lock with verified NGC attestation**, plus a rolling feed of the most recent attestations (timestamp + anonymised node ID + GPU model). Subhead: *"Every attested validator has cryptographically proved to NVIDIA's NGC service that it is running on the hardware it claims to run on."* One-click link to `/trust` for the full story. See §8.5.
6. **Halving countdown.** Live ticker to the next halving — drives urgency.
7. **Miner quick-start teaser.** "Download, plug in GPU, start earning — 10 minutes." Link to `/mine`.
8. **Roadmap ribbon.** 4 milestones with statuses. (Auto-generated from `docs/ROADMAP.md` ideally.)
9. **Footer.** Docs, GitHub, Discord/Telegram, legal, brand kit.

### 8.3 `/mine` page — must-have elements

1. Download links: Windows `.exe`, Linux `.deb`, Linux `.tar.gz`, macOS (later).
2. Minimum GPU table: "CUDA 11.0+, ≥ 8 GB VRAM, NVIDIA RTX 20xx / 30xx / 40xx / 50xx."
3. 60-second setup video.
4. Mining pool directory (when pools exist) + instructions for solo mining.
5. FAQ: heat, noise, power, tax implications (with a disclaimer), is this legal in my country (with a disclaimer), what happens at halving.

### 8.4 Content write-up

All marketing copy must clear three filters before publication:

1. **No promises of financial return.** Avoid "earn X per day", "ROI in Y months", "investment opportunity". Stick to utility framing: *"Mining Cell is how new coins enter circulation. Your earnings depend on network difficulty and the current block reward."*
2. **No GPU vendor lock-in claims that aren't true.** Say *"optimized for NVIDIA"* or *"NVIDIA-favored"* — do not say *"NVIDIA-only"* or *"AMD cannot mine"* unless Stance 2 (§5.4) is adopted after legal review.
3. **No over-claiming the attestation story.** NVIDIA-lock is an **opt-in, per-operator policy** — not a network-wide consensus rule. Marketing must say *"X% of validators have opted into NVIDIA-lock with fresh NGC attestations"* — it must **not** say *"the QSDM network is NVIDIA-attested"* or *"all validators are hardware-verified"*. See `NVIDIA_LOCK_CONSENSUS_SCOPE.md` for the boundary; §8.5 for the copy pattern that works.

### 8.5 Trust & attestation story — using NVIDIA-lock as a credibility signal

NVIDIA-lock (`pkg/monitoring/nvidia_lock.go`) is today an optional API-boundary policy where a validator refuses state-changing API calls unless a fresh NGC GPU-attestation proof is present. Post-launch, it becomes a **public trust signal** on the website — a cryptographic statement that opted-in validators are running on real, NVIDIA-attested hardware.

**What we can claim truthfully:**

- *"X of Y public validators have opted into NVIDIA-lock."*
- *"The newest attestation was verified against NVIDIA NGC at HH:MM UTC."*
- *"Attestations are signed by NVIDIA's NGC service and carry a hardware fingerprint — we cannot forge them, and neither can operators."*
- *"Validators who opt in are accepting a stricter API policy in exchange for a public 'Attested' badge on the explorer."*

**What we must not claim:**

- *"QSDM requires NVIDIA GPUs on validators."* (False — validators are CPU-only by design; only *miners* use GPUs.)
- *"Every validator is NVIDIA-attested."* (False — the policy is opt-in, by design, per `NVIDIA_LOCK_CONSENSUS_SCOPE.md`.)
- *"Attested validators are more secure."* (Misleading — they have proved *hardware provenance*, which is a distinct property from consensus security.)
- *"NVIDIA endorses QSDM."* (False — we consume a public NGC service; there is no partnership unless/until one is signed.)

**Landing-page copy pattern (approved wording):**

> **Attested network.** A growing subset of QSDM validators opt in to **NVIDIA-lock** — a policy that requires a fresh, cryptographic NGC attestation from NVIDIA's service before any state-changing API call is accepted. It's not a consensus rule; it's a transparency layer. Operators who opt in earn a verifiable **"Attested"** badge on the public explorer, and the signed attestation trail is published block-by-block. **`X of Y validators are attested right now`.**

**`/trust` page content (proposed):**

1. **Header + live attested counter.** Same widget as landing page §8.2 item 5, larger.
2. **What NVIDIA-lock is.** Plain-English explainer with one architectural diagram. Explicitly state: "This is optional, per-operator, and does not affect consensus. It is a hardware-provenance transparency tool."
3. **How the attestation works.** Three-step diagram: (a) validator runs NGC proof sidecar → (b) sidecar submits to `POST /api/v1/monitoring/ngc-proof` → (c) validator serves proof under its public "attested" badge. Link to source: `pkg/monitoring/nvidia_lock.go`.
4. **Live attestation feed.** Table of the last 50 attestations: anonymised node ID prefix, attested-at timestamp, GPU architecture (from NGC payload), freshness window. Pulled from a read-only API (see §8.5.1 below).
5. **Explorer integration.** Each validator page on the block explorer shows an "Attested" badge (green / amber / red) and the most recent NGC proof time.
6. **Opt-in guide for operators.** Short config snippet + link to the validator quick-start. The carrot: "Operators who opt in get surface real-estate on the landing page and improved discoverability in the peer list."
7. **Limitations, stated clearly.** One paragraph listing the boundary — not consensus, not a security proof, not an NVIDIA endorsement, dependent on NGC service availability.
8. **FAQ.** Why NVIDIA specifically (we consume an existing public attestation service; no reciprocal partnership assumed). Can AMD validators be attested (not today — there is no equivalent AMD NGC-class public attestation service; if one emerges, we'll support it). What happens if NVIDIA changes NGC terms (the badge disappears until a replacement attestation path exists; consensus is unaffected).

#### 8.5.1 Backend support needed

For §8.2 item 5 and the `/trust` page to function, we add two read-only, aggregated endpoints to the API (no auth required — these are already public information):

| Endpoint | Purpose |
|---|---|
| `GET /api/v1/trust/attestations/summary` | Returns `{ "attested": int, "total_public": int, "last_attested_at": RFC3339, "fresh_within": "15m" }`. Aggregates across opt-in validators from the peer list. |
| `GET /api/v1/trust/attestations/recent?limit=50` | Returns the last N attestation events: `[ { "node_id_prefix": "abc…", "attested_at": ..., "gpu_architecture": "nvidia-ada", "ngc_hmac_ok": true } ]`. Anonymised node IDs (first 8 chars + last 4) so operators can self-identify without exposing full node identity. |

Both endpoints are served by the validator receiving public traffic for the landing-page widget — **no new daemon**. They consume the existing `ngcProofs` ring buffer (already present in `pkg/monitoring/ngc_proofs.go`) plus a new peer-reputation cross-reference of which peers have submitted recent proofs. Implementation estimate: ~1 day in Phase 5.

#### 8.5.2 Guardrails baked into the trust page

- The landing-page widget and `/trust` page must always display the **`X of Y`** denominator, not just `X`. A number alone implies the whole network; a ratio makes opt-in visible.
- When no attestations are fresh (NGC outage, zero opt-ins), the widget must render as **"0 of Y attested"** with a muted-neutral colour — never hidden, never "loading forever". Transparency over optics.
- Attestation data never enters consensus or block-validity paths. It lives strictly in the API boundary and the dashboard/explorer UI. This is a marketing and transparency feature; not a security claim.
- The page must link to `NVIDIA_LOCK_CONSENSUS_SCOPE.md` in plain language so a technical reader can verify our claims.

#### 8.5.3 JSON schemas — the new trust endpoints

These are the exact shapes the website and explorer will consume. Both endpoints are **public (no auth)**, **aggregated** (no full node IDs, no operator-identifying data), and **cache-safe** (`Cache-Control: public, max-age=15`). Served from validator nodes that have public inbound HTTP.

**`GET /api/v1/trust/attestations/summary`**

```json
{
  "attested": 37,
  "total_public": 84,
  "ratio": 0.44,
  "fresh_within": "15m",
  "last_attested_at": "2026-05-14T09:21:07Z",
  "last_checked_at": "2026-05-14T09:22:31Z",
  "ngc_service_status": "healthy",
  "scope_note": "NVIDIA-lock is an opt-in, per-operator API policy — not a consensus rule. See NVIDIA_LOCK_CONSENSUS_SCOPE.md."
}
```

| Field | Type | Notes |
|---|---|---|
| `attested` | int | Validators in the public peer set with a fresh NGC proof within `fresh_within`. |
| `total_public` | int | Validators in the public peer set (opt-in or not). Denominator for the ratio. |
| `ratio` | float | `attested / total_public`, 0–1. Pre-computed so the widget doesn't have to divide. |
| `fresh_within` | string | Human-readable window (also Go-`time.Duration`-parseable). Default `"15m"`. |
| `last_attested_at` | RFC3339 or `null` | Most recent attestation across the public set. `null` when zero. |
| `last_checked_at` | RFC3339 | When the aggregator last ran. Always present. |
| `ngc_service_status` | enum | `"healthy"` / `"degraded"` / `"outage"` — derived from proof-ingest success rate. Drives widget colour state (§8.5.4). |
| `scope_note` | string | Fixed disclaimer string, included verbatim in every response so scraping tools/bots can surface the caveat. |

**`GET /api/v1/trust/attestations/recent?limit=50`**

```json
{
  "fresh_within": "15m",
  "count": 3,
  "attestations": [
    {
      "node_id_prefix": "abcdef12…8ace",
      "attested_at": "2026-05-14T09:21:07Z",
      "fresh_age_seconds": 84,
      "gpu_architecture": "nvidia-ada",
      "gpu_available": true,
      "ngc_hmac_ok": true,
      "region_hint": "eu"
    },
    {
      "node_id_prefix": "11223344…7c0d",
      "attested_at": "2026-05-14T09:20:15Z",
      "fresh_age_seconds": 136,
      "gpu_architecture": "nvidia-hopper",
      "gpu_available": true,
      "ngc_hmac_ok": true,
      "region_hint": "us"
    },
    {
      "node_id_prefix": "9a8b7c6d…1f20",
      "attested_at": "2026-05-14T09:17:42Z",
      "fresh_age_seconds": 289,
      "gpu_architecture": "nvidia-ada",
      "gpu_available": true,
      "ngc_hmac_ok": true,
      "region_hint": "apac"
    }
  ]
}
```

Rules baked into the server response shape:

- `node_id_prefix` is **always** truncated: first 8 + last 4 chars of the libp2p peer ID, joined by `"…"`. Never the full ID (keeps operators pseudonymous).
- `region_hint` is coarse (`"eu"` / `"us"` / `"apac"` / `"other"`) — derived from GeoIP buckets, never a city or an AS number.
- `attestations` is sorted newest first.
- `limit` is clamped server-side to `[1, 200]`; default 50.
- Entries older than `fresh_within` (default 15 m) are **never** returned — the feed is intentionally a "fresh attestations only" view. Use the explorer for the full history.

**Error states (both endpoints):**

| HTTP | Body | When |
|---|---|---|
| `200` | payload above, with `attested: 0` | Everything up, nobody opted in / no fresh proofs. |
| `200` | payload with `ngc_service_status: "outage"` | Aggregator running but no fresh NGC proofs ingested in the last 15 m. |
| `503` | `{ "error": "trust aggregator warming up" }` | Endpoint enabled but aggregator hasn't had a scrape pass yet (first 60 s after node start). |
| `404` | `{ "error": "trust endpoints disabled on this node" }` | Operator opted out of serving the public trust API. |

#### 8.5.4 Widget state matrix

The landing-page widget (§8.2 item 5) must render one of four states based on the summary response. Design must ship all four — **especially the zero and outage states** (§8.5.2 guardrail).

| State | Trigger | Colour token | Primary label | Secondary label |
|---|---|---|---|---|
| **Healthy** | `attested > 0` and `ngc_service_status == "healthy"` | `tone.success` | `37 of 84 validators attested` | `Last attestation 84 s ago · EU` |
| **Partial / degraded** | `attested > 0` and `ngc_service_status == "degraded"` | `tone.warning` | `37 of 84 validators attested` | `NGC attestations slower than usual` |
| **Zero opt-in** | `attested == 0` and `ngc_service_status == "healthy"` | `tone.neutral` | `0 of 84 validators attested` | `No validators have opted into NVIDIA-lock yet` |
| **NGC outage** | `ngc_service_status == "outage"` | `tone.warning` | `0 of 84 attested — NGC outage` | `Attestations will resume when NVIDIA NGC is reachable` |

Every state links to `/trust` via the secondary label. No state is ever hidden. No state flashes "loading" for more than 250 ms — if the response is slow, render the **zero** state with a skeleton on the timestamp until data arrives.

#### 8.5.5 Landing-page widget — reference HTML

Self-contained, no frameworks, no gradients, flat tokens. The design team can swap `--qsdm-*` CSS variables for their final palette. Accessibility: widget is a `<section>` with a descriptive `aria-label`; live region for the count.

```html
<section
  class="qsdm-trust-widget"
  aria-label="NVIDIA attestation summary"
  data-state="healthy"
>
  <header class="qsdm-trust-widget__header">
    <h3 class="qsdm-trust-widget__title">Attested validators</h3>
    <a class="qsdm-trust-widget__more" href="/trust">How this works →</a>
  </header>

  <div class="qsdm-trust-widget__body">
    <p class="qsdm-trust-widget__count" aria-live="polite">
      <span class="qsdm-trust-widget__count-num">37</span>
      <span class="qsdm-trust-widget__count-sep">of</span>
      <span class="qsdm-trust-widget__count-denom">84</span>
      <span class="qsdm-trust-widget__count-label">validators attested</span>
    </p>

    <p class="qsdm-trust-widget__subline">
      Last attestation <time datetime="2026-05-14T09:21:07Z">84 s ago</time> · EU
    </p>

    <p class="qsdm-trust-widget__caveat">
      NVIDIA-lock is opt-in per operator. It is not a consensus rule.
      <a href="/trust">Read the scope →</a>
    </p>
  </div>

  <ul class="qsdm-trust-widget__feed" aria-label="Recent attestations">
    <li><span class="qsdm-trust-widget__feed-id">abcdef12…8ace</span> <time>84 s</time> · nvidia-ada</li>
    <li><span class="qsdm-trust-widget__feed-id">11223344…7c0d</span> <time>2 m</time> · nvidia-hopper</li>
    <li><span class="qsdm-trust-widget__feed-id">9a8b7c6d…1f20</span> <time>5 m</time> · nvidia-ada</li>
  </ul>
</section>
```

```css
.qsdm-trust-widget {
  border: 1px solid var(--qsdm-stroke, #1f2937);
  border-radius: 8px;
  padding: 20px 24px;
  background: var(--qsdm-surface, #0b0f14);
  color: var(--qsdm-text, #e5e7eb);
  font: 14px/1.5 system-ui, sans-serif;
  max-width: 560px;
}
.qsdm-trust-widget[data-state="healthy"]   { --qsdm-accent: var(--qsdm-tone-success, #2fbf71); }
.qsdm-trust-widget[data-state="degraded"]  { --qsdm-accent: var(--qsdm-tone-warning, #d29922); }
.qsdm-trust-widget[data-state="zero"]      { --qsdm-accent: var(--qsdm-tone-neutral, #8b949e); }
.qsdm-trust-widget[data-state="outage"]    { --qsdm-accent: var(--qsdm-tone-warning, #d29922); }

.qsdm-trust-widget__header {
  display: flex; justify-content: space-between; align-items: baseline;
  margin-bottom: 12px;
}
.qsdm-trust-widget__title { margin: 0; font-size: 13px; font-weight: 600; letter-spacing: .04em; text-transform: uppercase; color: var(--qsdm-text-secondary, #9ca3af); }
.qsdm-trust-widget__more  { font-size: 13px; color: var(--qsdm-accent); text-decoration: none; }
.qsdm-trust-widget__more:hover { text-decoration: underline; }

.qsdm-trust-widget__count { margin: 0 0 4px; font-size: 28px; font-weight: 600; letter-spacing: -.01em; }
.qsdm-trust-widget__count-num   { color: var(--qsdm-accent); }
.qsdm-trust-widget__count-sep   { font-weight: 400; color: var(--qsdm-text-secondary, #9ca3af); margin: 0 6px; }
.qsdm-trust-widget__count-denom { color: var(--qsdm-text, #e5e7eb); }
.qsdm-trust-widget__count-label { font-size: 14px; font-weight: 400; color: var(--qsdm-text-secondary, #9ca3af); margin-left: 8px; }

.qsdm-trust-widget__subline { margin: 0 0 16px; color: var(--qsdm-text-secondary, #9ca3af); }
.qsdm-trust-widget__caveat  { margin: 0 0 16px; padding: 10px 12px; border-left: 2px solid var(--qsdm-stroke, #1f2937); color: var(--qsdm-text-secondary, #9ca3af); font-size: 13px; }
.qsdm-trust-widget__caveat a { color: var(--qsdm-accent); }

.qsdm-trust-widget__feed { list-style: none; margin: 0; padding: 12px 0 0; border-top: 1px solid var(--qsdm-stroke, #1f2937); font-variant-numeric: tabular-nums; }
.qsdm-trust-widget__feed li { display: flex; gap: 12px; padding: 4px 0; color: var(--qsdm-text-secondary, #9ca3af); font-size: 13px; }
.qsdm-trust-widget__feed-id { font-family: ui-monospace, SF Mono, monospace; color: var(--qsdm-text, #e5e7eb); min-width: 13ch; }
```

Client fetch pattern (dependency-free, cache-aware, respects the guardrail that "loading" never shows for >250 ms):

```js
async function mountTrustWidget(root) {
  const setState = (state) => root.setAttribute('data-state', state);

  // Render the zero state immediately so we never show an empty shell.
  setState('zero');

  let summary, recent;
  try {
    [summary, recent] = await Promise.all([
      fetch('/api/v1/trust/attestations/summary', { cache: 'default' }).then(r => r.json()),
      fetch('/api/v1/trust/attestations/recent?limit=3', { cache: 'default' }).then(r => r.json()),
    ]);
  } catch {
    setState('outage');
    return;
  }

  const num = root.querySelector('.qsdm-trust-widget__count-num');
  const denom = root.querySelector('.qsdm-trust-widget__count-denom');
  num.textContent = summary.attested;
  denom.textContent = summary.total_public;

  if (summary.ngc_service_status === 'outage') {
    setState('outage');
  } else if (summary.attested === 0) {
    setState('zero');
  } else if (summary.ngc_service_status === 'degraded') {
    setState('degraded');
  } else {
    setState('healthy');
  }

  // Feed items: recent.attestations → <li>s, keeping the reference markup shape.
  const feedList = root.querySelector('.qsdm-trust-widget__feed');
  feedList.innerHTML = recent.attestations.map(a => `
    <li>
      <span class="qsdm-trust-widget__feed-id">${escapeHtml(a.node_id_prefix)}</span>
      <time datetime="${a.attested_at}">${formatAge(a.fresh_age_seconds)}</time> ·
      ${escapeHtml(a.gpu_architecture)}
    </li>
  `).join('');
}
```

#### 8.5.6 `/trust` page — reference HTML skeleton

The full page uses the same CSS tokens. Structure mirrors §8.5 items 1–8. Showing only the skeleton so the design team can design against it:

```html
<main class="qsdm-trust-page">
  <header>
    <h1>Attested network</h1>
    <p class="qsdm-trust-page__lede">
      A transparency layer over QSDM's validator set. Not a consensus rule — a
      cryptographic statement that opted-in validators are running on the
      hardware they claim, verified by NVIDIA's NGC attestation service.
    </p>
  </header>

  <!-- 1. Live counter (large variant of the landing widget) -->
  <section class="qsdm-trust-page__counter">
    <!-- re-use .qsdm-trust-widget with data-variant="large" -->
  </section>

  <!-- 2. What NVIDIA-lock is -->
  <section>
    <h2>What NVIDIA-lock is</h2>
    <p>…plain-English explainer…</p>
    <figure>
      <!-- architectural diagram: sidecar → /ngc-proof → validator badge -->
    </figure>
    <aside class="qsdm-trust-page__scope">
      <strong>Scope.</strong> This is optional, per-operator, and does not
      affect consensus. It is a hardware-provenance transparency tool.
      <a href="/docs/NVIDIA_LOCK_CONSENSUS_SCOPE">Read the formal scope →</a>
    </aside>
  </section>

  <!-- 3. How the attestation works -->
  <section>
    <h2>How the attestation works</h2>
    <ol class="qsdm-trust-page__steps">
      <li><strong>NGC proof.</strong> The validator runs a sidecar that queries NVIDIA NGC and produces a signed proof bundle.</li>
      <li><strong>Ingest.</strong> The sidecar POSTs to <code>/api/v1/monitoring/ngc-proof</code> with a shared secret.</li>
      <li><strong>Badge.</strong> The validator exposes the proof under <code>/api/v1/trust/attestations/*</code>; our explorer renders an <em>Attested</em> badge.</li>
    </ol>
  </section>

  <!-- 4. Live attestation feed (last 50) -->
  <section>
    <h2>Recent attestations</h2>
    <table class="qsdm-trust-page__feed" aria-label="Last 50 attestations">
      <thead>
        <tr><th>Node</th><th>Attested</th><th>Architecture</th><th>Region</th></tr>
      </thead>
      <tbody><!-- filled by /api/v1/trust/attestations/recent?limit=50 --></tbody>
    </table>
  </section>

  <!-- 5. Explorer integration (screenshot + badge legend) -->
  <section>
    <h2>On the explorer</h2>
    <p>Each validator page shows one of three badges based on attestation freshness:</p>
    <dl class="qsdm-trust-page__legend">
      <dt><span data-badge="green">Attested</span></dt><dd>Fresh NGC proof within 15 minutes.</dd>
      <dt><span data-badge="amber">Stale</span></dt><dd>Last NGC proof older than 15 minutes but within 24 hours.</dd>
      <dt><span data-badge="neutral">Unattested</span></dt><dd>Validator has not opted in, or never submitted a proof.</dd>
    </dl>
  </section>

  <!-- 6. Opt-in guide -->
  <section>
    <h2>Opt in as an operator</h2>
    <pre><code># qsdm validator config
[nvidia_lock]
enabled = true
ngc_ingest_secret = "…"
proof_max_age = "15m"
publish_trust_endpoints = true
</code></pre>
    <p><a href="/validate">Full validator quick-start →</a></p>
  </section>

  <!-- 7. Limitations -->
  <section>
    <h2>Limitations</h2>
    <ul>
      <li>Attestation is per-operator opt-in. It does not extend to validators who do not run the sidecar.</li>
      <li>It is not a consensus rule; blocks are not rejected for missing attestations.</li>
      <li>It does not imply NVIDIA endorsement. We consume a public NVIDIA service.</li>
      <li>It depends on NVIDIA NGC service availability. If NGC changes terms or goes offline, the badge disappears.</li>
    </ul>
  </section>

  <!-- 8. FAQ -->
  <section>
    <h2>FAQ</h2>
    <!-- details/summary pairs covering: why NVIDIA, AMD support, NGC outage, privacy of the feed, can I verify a proof myself -->
  </section>
</main>
```

#### 8.5.7 Go server handler sketch

Goes into the new `pkg/api/handlers_trust.go`. Aggregator reads the existing `pkg/monitoring/ngc_proofs` ring buffer and the peer list. Shown at sketch depth — full implementation lands in Phase 5.

```go
// NewTrustAggregator returns an aggregator that backs the public trust endpoints.
// freshWithin is the "fresh attestation" window (default 15m).
// totalPublic returns the current count of validators in the public peer set.
func NewTrustAggregator(
    ngc *monitoring.NGCProofRing,
    peers PeerSnapshotter,
    freshWithin time.Duration,
) *TrustAggregator { /* … */ }

// Summary serves GET /api/v1/trust/attestations/summary.
func (h *Handlers) TrustSummary(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
        return
    }
    s := h.trust.Summary(time.Now())
    w.Header().Set("Cache-Control", "public, max-age=15")
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(s)
}

// Recent serves GET /api/v1/trust/attestations/recent?limit=N, clamped to [1, 200].
func (h *Handlers) TrustRecent(w http.ResponseWriter, r *http.Request) { /* … */ }
```

The aggregator's `Summary` method returns the struct shape in §8.5.3; its `Recent` method projects ring entries into the per-item shape, redacting node IDs via the prefix rule and dropping anything older than `freshWithin`. Unit tests must cover **each of the four widget states** in §8.5.4 plus the 503 / 404 paths.

---

## 9. Execution plan — phases

### Phase 0 — Decision + legal (Week 0, 1 week)

- [ ] Review this doc end-to-end; sign off or request changes.
- [ ] Engage counsel on: tokenomics posture (fair launch + utility framing), treasury vesting legality in target jurisdictions, "NVIDIA-favored" vs "NVIDIA-exclusive" decision, trademark clearance on "QSDM" and "Cell".
- [ ] Decide mining-algorithm candidate: A (KawPow-class), B (Autolykos v2), or C (mesh3D-tied).
- [ ] Commit tokenomics numbers (the proposals in §4 become binding).
- [ ] Announce the plan internally. Freeze new feature merges to `pkg/mesh3d` and `pkg/branding` until Phase 1 lands.

### Phase 1 — Rebrand (Week 1–2, non-breaking path first)

- [ ] Rename `NEXT_STEPS_QSDMPLUS.md` → `NEXT_STEPS.md`.
- [ ] Update `pkg/branding/branding.go` — `Name = "QSDM"`, add `CoinName`, `CoinSymbol`, `CoinDecimals`.
- [ ] Thread coin symbol through: API responses, Go SDK, JS SDK, dashboard labels.
- [ ] Replace "QSDM+" with "QSDM" in every `.md`, every log line, every dashboard string. Keep technical compatibility: `QSDMPLUS_*` env vars still accepted but emit a deprecation warning.
- [ ] Rename binaries: `qsdmplus` → `qsdm`. Keep `qsdmplus` as a symlink for one release.
- [ ] Rename CI workflows.
- [ ] Add `REBRAND_NOTES.md` documenting every deprecated name and its migration path.
- [ ] **Milestone:** `grep -r "QSDM+" docs/ sdk/ internal/ pkg/ cmd/` returns zero hits. `go test ./... -short` stays green. JS SDK tests stay green.

### Phase 2 — Node roles + VPS hard guarantee (Week 2–3)

- [ ] Add `mining_enabled` config flag (default `false`).
- [ ] Introduce build tag `validator_only` that strips CUDA code from the validator build.
- [ ] Add `node_role` to `/api/v1/status` response.
- [ ] Split Dockerfiles: `Dockerfile.validator` (CPU-only), `Dockerfile.miner` (CUDA base).
- [ ] Split K8s manifests accordingly; add `nodeSelector`s.
- [ ] Update `SCYLLA_CAPACITY.md` with the explicit "no GPU on validators" statement.
- [ ] Add startup guards: validator binary refuses to start if `mining_enabled=true`; miner binary refuses to start if no GPU detected.
- [ ] Add tests verifying the validator binary cannot link CUDA symbols.
- [ ] **Milestone:** both Docker images publish in CI; running `docker inspect qsdm-validator` shows no GPU base layer.

### Phase 3 — Cell coin identity (Week 3–4)

- [ ] Write `CELL_TOKENOMICS.md` with the approved numbers from Phase 0.
- [ ] Add genesis block wiring in `pkg/chain` — mint the 10 M treasury allocation to a known locked address.
- [ ] Add fee denomination plumbing — transactions declare fees in `CELL` units; validator rewards paid in `CELL`.
- [ ] Update Go SDK to use `CELL` symbol in balance/fee responses.
- [ ] Update JS SDK likewise.
- [ ] Update dashboard: add "Current supply", "Block reward", "Next halving" panels.
- [ ] **Milestone:** a testnet genesis with the full tokenomics in place runs for 72 h without issue. Explorer shows correct supply math.

### Phase 4 — Mining protocol + miner binary (Week 4–10)

- [ ] Design doc `MINING_PROTOCOL.md` — normative spec for the chosen algorithm.
- [ ] Implement `pkg/mining/` — epoch management, difficulty adjustment, proof verification (CPU-safe).
- [ ] Implement `pkg/mining/cuda/` — CUDA kernels under build tag. **External security review required** before mainnet.
- [ ] Build `cmd/qsdmminer` — single-binary miner for Windows + Linux.
- [ ] Run **incentivized testnet** for 4 weeks with the tokenomics and mining protocol. Invite 50–200 home miners. Log everything.
- [ ] Patch issues found on testnet.
- [ ] **Milestone:** testnet with ≥ 100 concurrent home miners, no consensus faults, difficulty adjusts correctly across two simulated halvings.

### Phase 5 — Website + launch (Week 10–12)

- [ ] Rebuild `qsdm.tech` per §8.
- [ ] Publish brand kit.
- [ ] Publish miner quick-start video.
- [ ] Set a mainnet genesis time.
- [ ] Publish mainnet genesis file and bootstrap peer list.
- [ ] Publish validator runbook.
- [ ] Announce.
- [ ] **Milestone:** mainnet block #1 mined by a home miner on an NVIDIA GPU, live on the explorer.

### Phase 6 — Post-launch ops (ongoing)

- [ ] First halving dry-run at testnet (simulate time compression to validate).
- [ ] Audit the mining algorithm (externally — budget $40–80 k for a serious review).
- [ ] Audit the tokenomics logic (genesis, treasury vesting, emission) — same auditor.
- [ ] Pool software reference implementation (or bless a third-party one).
- [ ] Block explorer hardening for real traffic.
- [ ] Monitor real-world GPU distribution per-block for 90 days to confirm NVIDIA-favored behavior is working as intended.

---

## 10. Risks, honestly

### 10.1 Legal / regulatory

- **Securities classification.** Fair launch + utility-first framing is the strongest posture but not airtight. The treasury allocation is the most exposed piece — vesting, transparency, and spending policy are all scrutinized. **Mitigation:** counsel review in Phase 0; consider a non-profit foundation holding the treasury.
- **Vendor tie-in.** "NVIDIA-favored" (Stance 1) is defensible. "NVIDIA-exclusive" (Stance 2) is not recommended absent a partnership. **Mitigation:** commit to Stance 1 in Phase 0.
- **"Buy hardware to earn coin" framing.** Crossing the line from "mining is how coins are issued" to "buy NVIDIA to earn money" is the difference between utility and investment framing. Marketing must be policed. **Mitigation:** §8.4 filter, enforced by legal review before any landing-page copy ships.
- **Trademark.** "QSDM" and "Cell" need clearance in at least US/UK/EU. "Cell" is generic; may face prior art from existing projects. **Mitigation:** trademark search in Phase 0; if "Cell" is blocked, fall back to "QCell", "CellQ", or "Cytoplasm" (fits the biology theme).
- **"NVIDIA-attested" trust claims on the website.** Using NVIDIA-lock attestations on the landing page is a strong credibility signal, but the claim is easy to over-stretch: "our network is NVIDIA-attested" is false (attestation is opt-in and non-consensus), while "X of Y validators opted into NVIDIA attestation" is true. **Mitigation:** §8.4 filter #3 plus §8.5.2 guardrails — always show the `X of Y` ratio, never hide the widget when the count is zero, and link the `/trust` page to `NVIDIA_LOCK_CONSENSUS_SCOPE.md` for verifiable scope. **Also:** the word "NVIDIA" in marketing material needs NVIDIA-trademark counsel review before launch, independent of any partnership discussion.
- **NGC service dependency.** The attestation story depends on NVIDIA's public NGC service continuing to operate under usable terms. If NVIDIA changes, restricts, or deprecates NGC, the "Attested" badge disappears. **Mitigation:** treat the attestation widget as a *best-effort* transparency feature, not a guarantee; publish a fallback plan (other attestation services, or a self-hosted hardware-attestation scheme) if NGC terms change.

### 10.2 Technical

- **Consensus regression from mining integration.** Mining is additive but touches block assembly. A careless design could let a miner influence block ordering. **Mitigation:** the design in §5.3 keeps the proposer path unchanged — miners attach a proof to a block the proposer assembled, they don't pick the block. Formal review required.
- **CUDA kernel bugs.** First-version CUDA mining kernels are historically where exploits hide (fake proofs, double-solve, timing attacks). **Mitigation:** external audit; bug bounty with real CELL payouts; 4-week incentivized testnet before mainnet.
- **ASIC drift.** Any GPU-friendly algorithm is eventually ASIC-developed. **Mitigation:** epoch-based dataset rotation (like Ethash); if ASICs appear, fork the algorithm. Budget for this — it is inevitable within 18–24 months of launch if the coin is valuable.
- **Validator sybil under mining rewards.** If miners also run validators to bias proposer selection, security weakens. **Mitigation:** the two roles are economically separate in our design (validators earn fees, miners earn emission) — running both is allowed but unprofitable compared to specializing. Monitor in phase 6.

### 10.3 Brand / market

- **"Is this a shitcoin?" perception.** Any GPU-mining launch in 2026 will face skepticism. **Mitigation:** lead with the quantum-safe story; mining is the distribution mechanism, not the thesis. The website hero line reflects this.
- **Competition from established mined chains.** Kaspa, Ergo, Alephium all compete for the same GPU miner mindshare. **Mitigation:** quantum-safe + fair-launch + clear docs are the differentiators. Don't compete on raw hashrate or emission rate.
- **Centralization around large home miners.** "Home" can mean 500-GPU basement farms. **Mitigation:** algorithm favors single-GPU efficiency (memory bandwidth, not raw FLOPS); document this as an explicit design goal.

---

## 11. What you need to decide (open questions)

Before Phase 0 ends:

1. **Mining algorithm candidate** — A (KawPow-class), B (Autolykos v2), or C (mesh3D-tied useful PoW)? *My recommendation: C as the target, A as the fallback if C isn't audit-ready in 10 weeks.*
2. **Total supply cap** — 100 M as proposed, or a different number? (21 M Bitcoin-style round would also work but makes per-block rewards smaller.)
3. **Treasury allocation** — 10% as proposed, or smaller? (0% is cleanest legally but starves development funding.)
4. **Halving cadence** — every 4 years as proposed, or faster/slower?
5. **Burn mechanism** — EIP-1559-style base-fee burn, or no burn?
6. **NVIDIA stance** — Stance 1 ("favored") or Stance 2 ("exclusive")? *My recommendation: Stance 1.*
7. **Ticker** — `CELL` as proposed, or something distinct like `QCL` / `QCELL`?
8. **Coin name fallback** — if "Cell" fails trademark clearance, prefer `QCell`, `Cytoplasm`, or `Vertex`? (Vertex ties to mesh3D cells too.)
9. **Launch region** — which jurisdictions do we target for the initial validator/miner community? Determines where trademark and securities review focus.
10. **Treasury custody** — who holds the keys to the 10 M treasury address until vesting? Multisig, DAO, or foundation?

---

## 12. Success metrics

90 days after mainnet launch, we want:

- **≥ 1,000 unique home miners** (distinct payout addresses with ≥ 1 mined block)
- **≥ 50 validator VPS nodes** across ≥ 15 unique operators
- **≥ 5 PB/day Scylla write throughput sustained across the network** (scale the ScyllaDB case study into a real number)
- **Zero consensus incidents** (no finality reverts past `ReorgLimit`, no equivocation, no mining exploit)
- **NVIDIA GPUs > 70% of miner-reported hardware**, confirming Stance 1 is working as designed
- **Fair launch posture upheld**: 0 CELL sold by the team, treasury vesting on schedule and public on-chain

---

## 13. Out-of-scope for this doc (handled elsewhere later)

- Exchange listings (requires launch first + market maker relationships)
- Formal partnerships (NVIDIA, Cloudflare, etc.)
- Governance token mechanics beyond the "one CELL = one vote" stub
- Layer-2 / rollups on QSDM
- Mobile wallet (phase 7+)
- Hardware wallet integration (phase 7+)
- Cross-chain bridges beyond what `pkg/bridge` already does

---

## 14. Appendix — inventory of things that must change on day one

This is a paste-ready checklist the engineering team can open as a tracking issue.

### 14.1 Code rename / symbol checklist

```
[ ] pkg/branding/branding.go — Name, add CoinName/Symbol/Decimals
[ ] pkg/branding/branding_test.go — update assertions
[ ] pkg/config/config.go — default coin symbol, mining_enabled flag, node_role
[ ] pkg/config/config_toml.go — TOML keys for above
[ ] pkg/api/handlers.go — /api/v1/status includes node_role + coin symbol
[ ] pkg/api/handlers_trust.go — NEW: GET /api/v1/trust/attestations/summary + /recent (public, §8.5.1)
[ ] pkg/monitoring/nvidia_lock.go — expose an aggregator helper for the trust endpoints (fresh count, last-seen, per-peer ring)
[ ] sdk/go/qsdmplus.go → sdk/go/qsdm.go  (keep shim for v1)
[ ] sdk/go/qsdmplus_test.go → sdk/go/qsdm_test.go
[ ] sdk/javascript/package.json — name → "qsdm"
[ ] sdk/javascript/qsdmplus.js → sdk/javascript/qsdm.js
[ ] sdk/javascript/qsdmplus.test.js → sdk/javascript/qsdm.test.js
[ ] sdk/javascript/qsdmplus.d.ts → sdk/javascript/qsdm.d.ts
[ ] cmd/qsdmplus/ → cmd/qsdm/
[ ] cmd/qsdmplus/transaction/ (references update)
[ ] NEW cmd/qsdmminer/ — scaffolding only in Phase 1
[ ] .github/workflows/qsdmplus-go.yml → qsdm-go.yml
[ ] .github/workflows/qsdmplus-scylla-staging.yml → qsdm-scylla-staging.yml
[ ] NEW .github/workflows/sdk-js.yml
[ ] NEW .github/workflows/audit-gate.yml
[ ] NEW .github/workflows/mining-kernels.yml
[ ] Dockerfile → Dockerfile.validator + NEW Dockerfile.miner
[ ] QSDM/deploy/ — kubernetes manifests updated, add miner manifests
[ ] QSDM/scripts/ — rebrand script names + log prefixes
[ ] All *.md files: "QSDM+" → "QSDM"
[ ] NEXT_STEPS_QSDMPLUS.md → NEXT_STEPS.md
```

### 14.2 Documentation checklist

```
[ ] README.md — rewritten around the two-tier + coin story
[ ] QSDM/README.md — likewise
[ ] ROADMAP.md — updated phases, mining added
[ ] NEXT_STEPS.md — reflects rebrand + phases 1-5
[ ] NEW CELL_TOKENOMICS.md
[ ] NEW MINING_PROTOCOL.md
[ ] NEW MINER_QUICKSTART.md
[ ] NEW VALIDATOR_QUICKSTART.md
[ ] NEW NODE_ROLES.md
[ ] NEW REBRAND_NOTES.md
[ ] NEW BRAND_KIT.md
[ ] SCYLLA_CAPACITY.md — add "no GPU on validators" language
[ ] NVIDIA_LOCK_CONSENSUS_SCOPE.md — rewrite for two-tier model
[ ] FEATURE_ENHANCEMENTS_COMPLETE.md — reflect rebrand
```

### 14.3 Website checklist

```
[ ] qsdm.tech / — landing rewrite per §8.2 (incl. attested-validators widget, item 5)
[ ] qsdm.tech/mine — new miner page per §8.3
[ ] qsdm.tech/validate — new validator page
[ ] qsdm.tech/cell — tokenomics page with halving countdown
[ ] qsdm.tech/tech — technical overview (PoE + BFT + PQ + Scylla)
[ ] qsdm.tech/trust — NEW: NVIDIA-lock attestation story per §8.5, linked from landing page
[ ] qsdm.tech/docs — mirror of markdown docs
[ ] qsdm.tech/brand — brand kit download
[ ] qsdm.tech/explorer — block explorer (subdomain or embed) with per-validator "Attested" badge
[ ] Open Graph / Twitter card images — QSDM branded
[ ] Favicon + app icons — QSDM "Q" mark
[ ] SEO — meta descriptions per page
[ ] Analytics + consent banner
[ ] Legal page — ToS, privacy, risk disclosure, non-securities disclaimer
```

### 14.4 Operations checklist

```
[ ] Trademark search + filings for "QSDM" and "Cell"
[ ] Domain check — qsdm.tech secured; grab qsdm-miner.*, cell-coin.*, qsdm.foundation
[ ] Discord / Telegram / X accounts renamed
[ ] GitHub org + repo rename (breaks clone URLs — coordinate with the team)
[ ] Download infrastructure for miner binary (CDN-backed, GPG-signed)
[ ] Genesis ceremony plan — who signs, when, where, how it's witnessed
[ ] Incentivized testnet comms plan
[ ] Mainnet launch comms plan
```

---

## 15. Sign-off

This doc is ready for review. When signed off, it becomes the source of truth for the pivot, and the engineering team will execute Phase 1 immediately.

| Role | Reviewer | Decision | Date |
|---|---|---|---|
| Product / vision | (project lead) | ☐ approve ☐ change | |
| Engineering | | ☐ approve ☐ change | |
| Legal / compliance | (external counsel) | ☐ approve ☐ change | |
| Brand / marketing | | ☐ approve ☐ change | |

On full approval, this file should be renamed to `MAJOR_UPDATE_EXECUTED.md` and moved under `QSDM/docs/docs/history/` as the historical record of the pivot. Phase 1 commits can reference it by that final path.
