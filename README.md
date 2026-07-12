# QSDM

**QSDM** (Quantum-Secure Dynamic Mesh ledger) is a post-quantum-secure
ledger with a two-tier node model — CPU-only validators run the PoE + BFT
consensus, and miners run an additive, Mesh3D-tied Proof-of-Work that
mints the native coin, **Cell (CELL)**. Consumers use **QSDM Hive**
(Windows/Linux) for wallets, signed tasks, NVIDIA mining, and Mother Hive
edge pools. Operators use Core plus optional home gateway, tray monitor,
and attestation sidecars.

Transaction signatures use **ML-DSA-87** (NIST FIPS 204) — the
standardised post-quantum replacement for classical Ed25519 / Ed448 —
so transactions signed today remain unforgeable against cryptographically
relevant quantum adversaries tomorrow.

Latest tagged ledger release: **v0.4.3**. Public site: [qsdm.tech](https://qsdm.tech).

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
- **Download Hive:** [qsdm.tech/download.html](https://qsdm.tech/download.html)
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

[MIT](LICENSE) © 2024-2026 Joedel Lopez Dalioan (Blackbeard).

The ledger node and sidecars are permissively licensed. Vendored
third-party dependencies under `QSDM/source/wasmer-go-patched/` retain
their own licences (see [`QSDM/source/wasmer-go-patched/LICENSE`](QSDM/source/wasmer-go-patched/LICENSE)).

## Windows signing

QSDM is preparing an application for free open-source signing through
SignPath Foundation. The application is pending, and no artifact may be
represented as SignPath-signed until it passes the repository's release gates.
See the [code signing policy](CODE_SIGNING_POLICY.md) for roles, provenance,
approval, verification, and incident-response requirements.
