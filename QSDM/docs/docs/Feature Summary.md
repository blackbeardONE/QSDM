# Feature Summary — QSDM

**Last Updated:** July 2026 · Deployed Core candidate **v0.4.7-rc.4** · Hive **1.4.5** · Edge Control **1.3.7**

QSDM (Quantum-Secure Dynamic Mesh) is a post-quantum mesh ledger whose native coin is **Cell (CELL)**. Validators run PoE + BFT consensus; miners mint CELL via NVIDIA-attested Proof-of-Work. Hive is the public desktop client for wallets, signed tasks, integrations, NVIDIA mining, and Mother Hive edge pools. Optional home-gateway, agent, relay, and attestation tools support operators without becoming separate consumer clients.

**QSDM Network** provides the production gateway, chain
status, explorer, HTTP API, trust feeds, and audit evidence. It lets ordinary
Hive users connect without operating a local Core while keeping wallet keys and
signing on their own device.

**QSDM VPN** is the separate private-network-access product at
`https://qsdm.online/`. Its current public surface includes an Android client,
token and device activation, assigned VPN profiles, quota visibility, and an
operator dashboard at `https://vpn.qsdm.online/`. QSDM VPN is a QSDM product;
it is not the CELL network gateway or another Hive client.

---

## Ledger & consensus

- **Proof-of-Entanglement (PoE) + BFT** on a dynamic mesh (not a linear blockchain).
- **ML-DSA-87** transaction signatures (NIST FIPS 204) with Zstd compression and batch signing.
- **3D mesh validation**, rule-based quarantine, and staked reputation penalties.
- **Dynamic submeshes** with fee thresholds, priority routing, and geotags.
- **SQLite + Zstd** storage; **ScyllaDB** path available for high throughput.
- **libp2p + GossipSub** peer mesh; Phase 4 bootstrap via `api.qsdm.tech`.

## CELL tokenomics

- **100M hard cap**, **0% founder allocation**, **10% genesis treasury** (48-month vesting), **90% mining emission** with 4-year halvings.
- Validators earn **transaction fees only** (no block subsidy).
- Tokenomics surface on `GET /api/v1/status` and the operator dashboard.

## Node roles (enforced)

- **Validator** — CPU-only, `mining_enabled=false`, public REST API, consensus.
- **Miner** — separate process/machine, NVIDIA GPU, HTTPS to validator `/api/v1/mining/*`.
- No combined full-node mode.

## Mining (protocol v2)

- NVIDIA-locked proofs (`nvidia-cc-v1`, `nvidia-hmac-v1`); Turing-or-newer GPU required for protocol mining.
- Public mining API: work, challenge, submit, enrollment, emission, blocks, slash.
- On-chain enrollment with **10 CELL** slashable bond; Hashcash anti-spam.
- Consumer path: **QSDM Hive** Miner task (CUDA solver bundled). Miners can start from zero liquid CELL by choosing deferred bond from accepted mining earnings.
- Operator path: `qsdmminer-console`; Tensor-Core fork is a future consensus activation.

## Wallet & self-custody

- Operator wallet API plus **`POST /api/v1/wallet/submit-signed`** self-custody path.
- Browser wallet at `/wallet/` — client-side ML-DSA-87 keystore (WASM + WebCrypto).
- `qsdmcli` for wallet new/show/sign and task/governance helpers.
- Public receipts: `GET /api/v1/receipts`, `GET /api/v1/receipts/{tx_id}`.

## Tasks, staking & rewards

- Consensus task catalog (`qsdm/tasks/v1`): fund, stake, start, stop, submit, claim, unstake, withdraw.
- **Task Studio** in Hive publishes signed `generic-proof-v1` manifests; compatible catalog changes appear without reinstalling Hive.
- Edge-pool settlement split: **70%** contributor / **15%** Mother Hive operator / **15%** ecosystem reserve.

## Governance & bridge

- Snapshot-style token-weighted voting for submesh rules and chain params.
- Atomic swap / lock-redeem-refund bridge (`pkg/bridge`) with audited secret handling.

## QSDM Hive (desktop)

- Windows and Linux client for CELL wallets, signed tasks, mining, edge pools, and integrations.
- Bundles native signer, console miner, CUDA solver, Edge Control/Agent, and the Mother Hive workspace.
- One QSDM wallet serves Hive and connected websites. The Chromium extension automatically reaches the active Hive wallet while exact-origin permissions and per-action approvals keep the keystore and passphrase out of the browser.
- Application Compute Gateway on `127.0.0.1:7742` for bounded local jobs.
- Sky Fang MMORPG wallet-link task (earn-only CELL; no pay-to-win power).

## Edge compute pool

- Topology: **Agent PCs → Relay → QSDM Hive (Mother) → QSDM Core**.
- Walletless Agents; fixed algorithms only (no remote shell/scripts).
- Separate HMAC credentials, resource caps, durable receipts.

## Home / local operator stack

- Local validator scripts and loopback **qsdm-local-gui**.
- **qsdm-home-gateway** — narrow public mining/status allowlist via outbound relay tunnel.
- **qsdm-tray-monitor** — Windows tray health poll → `%APPDATA%\QSDM-Tray-Monitor\status.json`.
- Watchdog, treasury/referral/faucet signer health checks.

## Trust, attestation & transparency

- Optional **NGC sidecar** and NVIDIA-lock API gates (transparency/policy, not consensus).
- Trust APIs: `/api/v1/trust/attestations/summary`, `.../recent`.
- Public audit checklist, explorer, chain status board, and security.txt on [qsdm.tech](https://qsdm.tech).

## SDKs & tooling

- Go SDK (`QSDM/source/sdk/go/`) and JavaScript SDK (`qsdm-sdk` on npm).
- WASM wallet module; OpenAPI + API reference; 25+ operator runbooks.
- Docker / Kubernetes deploy manifests; signed releases (Sigstore) and SBOM.

---

## What is not claimed

- Silence CLI is an optional Cursor/agent helper, not a QSDM product feature.
- Public packages remain unsigned on Windows until the SignPath Foundation application is approved; verify checksums before installing.
- Broad public edge-compute federation and marketplace settlement remain gated on Core lease/escrow records, quotas, and independent security review.
