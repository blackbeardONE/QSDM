# QSDM

**QSDM** (Quantum-Secure Dynamic Mesh ledger) is a ledger with
post-quantum (ML-DSA-87) wallet signatures and a two-tier node model —
CPU-only validators are designed to run the PoE + BFT consensus, and miners run an additive, Mesh3D-tied Proof-of-Work that
mints the native coin, **Cell (CELL)**. Consumers use **QSDM Hive**
(Windows/Linux) for wallets, signed tasks, NVIDIA mining, and Mother Hive
edge pools. Operators use Core plus optional home gateway, tray monitor,
and attestation sidecars.

Wallet transactions are signed with **ML-DSA-87** (NIST FIPS 204, via
Cloudflare CIRCL) — the standardised post-quantum signature scheme. It is
designed so that signatures made today stay unforgeable even against a
future cryptographically relevant quantum computer.

Latest tagged ledger release: **v0.4.3**. Public site: [qsdm.tech](https://qsdm.tech).

QSDM began on 10 May 2025 in its first repository,
[github.com/BbOldaccount/QSDM](https://github.com/BbOldaccount/QSDM), with
Proof-of-Entanglement and post-quantum signatures in its code from the next day,
and moved to this repository on 22 April 2026. See
[History](QSDM/docs/docs/HISTORY.md).

## Network status

QSDM is a **public pilot network (pre-mainnet)**. Today:

- Blocks come from **one block producer run by the project**; a backup node
  follows the chain. Live peer count and consensus flags are published at
  [`/api/v1/status`](https://api.qsdm.tech/api/v1/status) and
  [qsdm.tech/network.html](https://qsdm.tech/network.html).
- CELL wallet transactions (Hive, web wallet, `qsdmcli`) are signed with
  ML-DSA-87; since 16 October 2026 only ML-DSA-87 signed transactions are
  accepted. The Ed25519 code path of the generic peer-to-peer transaction
  verifier still exists but cannot be reached on the live chain.
- Signed consensus is enforced from block 827,000 (12 October 2026): every
  BFT vote and consensus message must carry a valid ML-DSA signature
  (`signed_consensus_active: true`).
- Proof-of-Entanglement: every node checks each transfer's ML-DSA-87
  signature under the sender's own key. The parent rules (each transfer names
  committed earlier transactions) are enforced on the public pilot from block
  816,600 (11 October 2026; `consensus_auth.poe_active` in
  `/api/v1/status`). See
  [`PROOF_OF_ENTANGLEMENT.md`](QSDM/docs/docs/PROOF_OF_ENTANGLEMENT.md).
- GPU mining (NVIDIA today) is open to every enrolled QSDM Hive miner (since
  19 October 2026), in 7-day windows that renew automatically.
- Block production by miners (in place of the earlier plan for independent
  validators), a published migration manifest for mainnet and a published
  security review are on the roadmap. The
  security review is an open
  [community review with a bug bounty](QSDM/docs/docs/COMMUNITY_REVIEW.md);
  no independent audit has been completed, and a professional audit remains
  welcome. Until those gates close, treat the chain as a pilot, not a mainnet
  (see [`TREASURY_POLICY.md` §9](QSDM/docs/docs/TREASURY_POLICY.md#9-mainnet-release-gates)).
- Max supply is 90,000,000 CELL, all from mining. There is no genesis
  treasury and no premine of any kind. Transaction fees are burned (credited
  to no account) until independent validators produce blocks. See
  [`GENESIS_POLICY_v1.1.md`](QSDM/docs/docs/GENESIS_POLICY_v1.1.md)
  (approved in writing by the owner; v1.1 of 2026-11-01 drops the counsel review).

### What is running (November 2026)

- **Core:** release `hardened-legacy-20261108-d7ffcd4-hl2-edge-go12610-ids-pqe-rc1108`
  since 2026-11-08, based on this repository's `QSDM/source` (d7ffcd4 plus
  the recovery patches and the HL1/HL2 hardened-legacy mining work listed in
  the [changelog](CHANGELOG.md)). Its peer-to-peer limits (opt-in here) and
  block-request budget are in this repository too; see
  [P2P limits](QSDM/docs/docs/P2P_LIMITS.md). Since
  2026-11-02 the producer accepts peer-to-peer connections on port 4001. The
  chain was stopped on 2026-09-22 and restarted on 2026-09-27; public mining
  reopened on 2026-10-02.
- **Mining:** NVIDIA GPU miners enrolled through QSDM Hive, in 7-day public
  windows that renew automatically, with per-owner limits. The node accepts
  owner-signed (ML-DSA-87 `operator_sig`) proofs; each window's configuration
  decides whether the signature is required, and since 2026-10-19 it is
  required for every proof.
- **Hive:** 1.4.22 for Windows and Linux, from
  [qsdm.tech/downloads/hive-v2/](https://qsdm.tech/downloads/hive-v2/). Hive
  1.4.21 and newer sign mining proofs with the unlocked Hive wallet
  automatically, and 1.4.22 adds Proof-of-Entanglement parents to every
  transfer. Moving from 1.4.20 (Windows) or 1.4.17 (Linux) is a one-time
  manual install from the [download page](https://qsdm.tech/download.html).

> **Rebrand notice.** Folder names such as `apps/qsdm-landing/` and
> configuration identifiers (`qsdm.*` configs, `QSDM_*` env vars,
> `X-QSDM-*` headers) continue to work during the deprecation window. See
> [`QSDM/docs/docs/REBRAND_NOTES.md`](QSDM/docs/docs/REBRAND_NOTES.md)
> for the full migration table.

## Repository layout

| Path | What it is |
|------|------------|
| [**`QSDM/`**](QSDM/) | **Ledger node** — Go implementation (consensus, storage, ML-DSA-87 signatures, wallet/token/mining/governance/bridge APIs). Native coin is **Cell (CELL)**. |
| [**`QSDM/docs/docs/`**](QSDM/docs/docs/) | User-facing documentation: API reference, mining protocol, node-role split, quickstarts, tokenomics, roadmap. |
| [**`QSDM/deploy/landing/`**](QSDM/deploy/landing/) | **Production website** served at [qsdm.tech](https://qsdm.tech) (marketing pages, docs SPA, wallet WASM, explorer, audit, trust). |
| [**`apps/`**](apps/) | **Products and sidecars** that use the node but are not required to run the core ledger. |
| [**`apps/qsdm-hive/`**](apps/qsdm-hive/) | QSDM Hive desktop client (CELL wallets, tasks, mining, edge). |
| [**`apps/qsdm-edge-agent/`**](apps/qsdm-edge-agent/) | Edge Agent / Relay / Edge Control utilities for Mother Hive pools. |
| [**`apps/qsdm-tray-monitor/`**](apps/qsdm-tray-monitor/) | Windows tray health monitor for the local home stack. |
| [**`apps/qsdm-nvidia-ngc/`**](apps/qsdm-nvidia-ngc/) | Optional NVIDIA NGC GPU attestation sidecar — opt-in, per-operator API policy, **not** a consensus rule. See [`NVIDIA_LOCK_CONSENSUS_SCOPE.md`](QSDM/docs/docs/NVIDIA_LOCK_CONSENSUS_SCOPE.md). |
| [**`apps/qsdm-landing/`**](apps/qsdm-landing/) | Legacy marketing stub. Prefer `QSDM/deploy/landing/` for the live site. |

## Start here

- **Feature summary (current capabilities):** [`QSDM/docs/docs/Feature Summary.md`](QSDM/docs/docs/Feature%20Summary.md)
- **Operator wiki (end-to-end):** [`QSDM/docs/docs/OPERATOR_GUIDE.md`](QSDM/docs/docs/OPERATOR_GUIDE.md) ⭐ start here if you are new
- **Run a full node (verify every block yourself):** [`QSDM/docs/docs/RUN_A_FULL_NODE.md`](QSDM/docs/docs/RUN_A_FULL_NODE.md) — signed release, verified snapshot, HTTP sync; values to pin on [qsdm.tech/fullnode.html](https://qsdm.tech/fullnode.html)
- **Download Hive:** [qsdm.tech/download.html](https://qsdm.tech/download.html)
  (Hive 1.4.21 and later: [qsdm.tech/downloads/hive-v2/](https://qsdm.tech/downloads/hive-v2/),
  signed with the v2 release key pinned in
  [`QSDM/deploy/release-trust/qsdm-hive-release-key-v2.json`](QSDM/deploy/release-trust/qsdm-hive-release-key-v2.json))
- **Archived documents:** [`QSDM/docs/archive/`](QSDM/docs/archive/)
- **Live bootstrap peers:** [qsdm.tech/validators.html](https://qsdm.tech/validators.html)
- **Run a validator (CPU-only):** [`QSDM/docs/docs/VALIDATOR_QUICKSTART.md`](QSDM/docs/docs/VALIDATOR_QUICKSTART.md)
- **Run a miner (Hive or console; CUDA solver bundled):** [`QSDM/docs/docs/MINER_QUICKSTART.md`](QSDM/docs/docs/MINER_QUICKSTART.md)
- **Home gateway (narrow public mining/status):** [`QSDM/docs/docs/HOME_GATEWAY.md`](QSDM/docs/docs/HOME_GATEWAY.md)
- **Edge pool / Mother Hive:** [`QSDM/docs/docs/EDGE_POOL.md`](QSDM/docs/docs/EDGE_POOL.md)
- **Run the NGC attestation sidecar:** [`apps/qsdm-nvidia-ngc/QUICKSTART.md`](apps/qsdm-nvidia-ngc/QUICKSTART.md)
- **API reference:** [`QSDM/docs/docs/API_REFERENCE.md`](QSDM/docs/docs/API_REFERENCE.md) and [`openapi.yaml`](QSDM/docs/docs/openapi.yaml)
- **Protocol specs:** [`MINING_PROTOCOL_V2.md`](QSDM/docs/docs/MINING_PROTOCOL_V2.md), [`NODE_ROLES.md`](QSDM/docs/docs/NODE_ROLES.md), [`CELL_TOKENOMICS.md`](QSDM/docs/docs/CELL_TOKENOMICS.md)
- **Release notes:** [`CHANGELOG.md`](CHANGELOG.md)
- **Code signing policy:** [`CODE_SIGNING_POLICY.md`](CODE_SIGNING_POLICY.md)
- **Privacy policy:** [`PRIVACY.md`](PRIVACY.md)
- **Security reporting:** [`SECURITY.md`](SECURITY.md)
- **Bug bounty (up to 20,000 CELL, 50,000 CELL pool) and community review:** [`BUG_BOUNTY.md`](QSDM/docs/docs/BUG_BOUNTY.md), [`COMMUNITY_REVIEW.md`](QSDM/docs/docs/COMMUNITY_REVIEW.md)

> **New?** The operator wiki answers the three questions every new node
> operator asks: *do I need an NVIDIA GPU, do I need a paid NGC plan, and
> do I have to sync to your VPS?* Spoiler: **no, no, and no — but NVIDIA
> is the first-class mining path today and `api.qsdm.tech` is the
> recommended bootstrap peer for Phase 4.**

## Trust surface (live reference node)

The reference deployment at `https://api.qsdm.tech/` publishes endpoints
that make coverage legible:

- `GET /api/v1/trust/attestations/summary` — aggregate
  `attested / total_public` ratio across the validator set.
- `GET /api/v1/trust/attestations/recent` — recent peer attestations with
  coarse region/GPU-arch metadata only (no PII).
- `GET /api/v1/audit/summary` and `/api/v1/audit/items` — public audit checklist.

Consumed by [qsdm.tech](https://qsdm.tech) (`/trust.html`, `/audit.html`)
and the dashboard. See
[`NVIDIA_LOCK_CONSENSUS_SCOPE.md`](QSDM/docs/docs/NVIDIA_LOCK_CONSENSUS_SCOPE.md)
for why NVIDIA-lock is a transparency signal, not a consensus rule.

## License

[MIT](LICENSE) © 2025-2026 Joedel Lopez Dalioan (Blackbeard).

The ledger node and sidecars are permissively licensed. Vendored
third-party dependencies under `QSDM/source/wasmer-go-patched/` retain
their own licences (see [`QSDM/source/wasmer-go-patched/LICENSE`](QSDM/source/wasmer-go-patched/LICENSE)).

## Windows signing

QSDM is preparing an application for free open-source signing through
SignPath Foundation. The application is pending, and no artifact may be
represented as SignPath-signed until it passes the repository's release gates.
See the [code signing policy](CODE_SIGNING_POLICY.md) for roles, provenance,
approval, verification, and incident-response requirements.
