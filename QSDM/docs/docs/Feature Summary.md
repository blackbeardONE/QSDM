# Feature Summary — QSDM

**Last Updated:** November 2026 · Running release **hardened-legacy-20261112-d7ffcd4-hl2-edge-go12611-ids-pqe-rc1109** · Hive **1.4.22** · Edge Control **1.3.7**

QSDM (Quantum-Secure Dynamic Mesh) is a mesh ledger with post-quantum (ML-DSA-87) wallet signatures whose native coin is **Cell (CELL)**. It currently runs as a public pilot network (pre-mainnet) with Proof-of-Entanglement transfers and one block producer run by the project. Instead of a set of independent validators, the plan since 19 October 2026 is for miners to produce blocks, in stages (designed, not built yet); miners mint CELL via NVIDIA-attested Proof-of-Work. Hive is the public desktop client for wallets, signed tasks, integrations, NVIDIA mining, and Mother Hive edge pools. Optional home-gateway, agent, relay, and attestation tools support operators without becoming separate consumer clients.

For the current state of the public network, see [Network status](NETWORK_STATUS.md).

**QSDM Network** provides the public gateway, chain
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

- **Proof-of-Entanglement (PoE) + BFT** on a dynamic mesh is the consensus design; today one block producer run by the project makes the blocks. Every node checks each transfer's signature under the sender's own key, and the PoE parent rules (every transfer names 2 to 10 committed transactions as parents) are enforced from block 816,600 ([details](PROOF_OF_ENTANGLEMENT.md)).
- **ML-DSA-87** wallet transaction signatures (NIST FIPS 204) with Zstd compression and batch signing. Ed25519-signed transactions are refused since 16 October 2026 (`ed25519_tx_accepted: false`); CELL stream session keys still use Ed25519 until a retirement height is set. Signed consensus is enforced from block 827,000.
- **3D mesh validation**, rule-based quarantine, and staked reputation penalties.
- **Dynamic submeshes** with fee thresholds, priority routing, and geotags.
- **SQLite + Zstd** storage; **ScyllaDB** path available for high throughput.
- **libp2p + GossipSub** peer mesh; public API at `api.qsdm.tech`.

## CELL tokenomics

- **Max supply 90,000,000 CELL, all from mining**, **0% founder allocation**, no genesis treasury, 4-year halvings.
- No validator block subsidy. Transaction fees are **burned** (credited to no account) while one producer, run by the project, makes the blocks.
- Tokenomics surface on `GET /api/v1/status`.

## Node roles (enforced)

- **Validator** — CPU-only, `mining_enabled=false`, public REST API, consensus.
- **Full node** — a node with role `validator` that follows the producer over HTTPS and checks every block itself. Anyone can run one ([Run a full node](RUN_A_FULL_NODE.md)); since 11 November 2026 it can follow a change of block maker.
- **Miner** — separate process/machine, NVIDIA GPU, HTTPS to validator `/api/v1/mining/*`.
- No process both validates and mines.

## Mining (protocol v2)

- NVIDIA-locked proofs (`nvidia-cc-v1`, `nvidia-hmac-v1`); Turing-or-newer GPU required for protocol mining.
- Public mining API: work, challenge, submit, enrollment, emission, blocks, slash.
- On-chain enrollment with **10 CELL** slashable bond; Hashcash anti-spam.
- Consumer path: **QSDM Hive** Miner task (CUDA solver bundled). Miners can start from zero liquid CELL by choosing deferred bond from accepted mining earnings. Hive 1.4.22 signs miner operator actions automatically and is published on the hive-v2 channel, signed by the v2 release key. See [Mining today](MINING_TODAY.md).
- Operator path: `qsdmminer-console`.

## Wallet & self-custody

- Operator wallet API plus **`POST /api/v1/wallet/submit-signed`** self-custody path.
- Browser wallet at `/wallet/` — client-side ML-DSA-87 keystore (WASM + WebCrypto). Since 10 November 2026 the [web wallet](WEB_WALLET.md) also installs as a phone app.
- QSDM Hive and `qsdmcli` support 24-word recovery-enabled ML-DSA-87 wallets;
  legacy JSON + passphrase wallets remain compatible.
- `qsdmcli` for wallet new/restore/export/show/sign and task/governance helpers.
- Public receipts: `GET /api/v1/receipts`, `GET /api/v1/receipts/{tx_id}`.

## Tasks, staking & rewards

- Consensus task catalog (`qsdm/tasks/v1`): fund, stake, start, stop, submit, claim, unstake, withdraw.
- **Task Studio** in Hive publishes signed `generic-proof-v1` manifests; compatible catalog changes appear without reinstalling Hive.
- Edge-pool settlement split: **70%** contributor / **15%** Mother Hive operator / **15%** ecosystem reserve.

## Governance & bridge

- Snapshot-style token-weighted voting for submesh rules and chain params.
- Atomic swap / lock-redeem-refund bridge (`pkg/bridge`) with reviewed secret handling (no independent external audit yet).

## QSDM Hive (desktop)

- Windows and Linux client for CELL wallets, signed tasks, mining, edge pools, and integrations.
- Bundles native signer, console miner, CUDA solver, Edge Control/Agent, and the Mother Hive workspace.
- One QSDM wallet serves Hive and connected websites. The Chrome, Edge, and Firefox extension packages reach the active Hive wallet while exact-origin permissions and per-action approvals keep the keystore and passphrase out of the browser.
- Browser extension 0.6.0 was sent to the Chrome Web Store on 6 November 2026 and is still in review; no store listing is published.
- Application Compute Gateway on `127.0.0.1:7742` for bounded local jobs.
- Sky Fang MMORPG wallet-link task (earn-only CELL; no pay-to-win power). The task is not currently published in the on-chain catalog while the pilot network recovers.
- From the first Hive release after 1.4.22 (merged, not in 1.4.22): crash records and "Save a diagnostics report…"; Hive keeps working through a qsdm.tech outage with the last signed release envelope it verified, until that envelope's `expires_at`, and ignores an envelope older than one it has already verified; and it reuses a GPU's existing miner enrollment.

## Edge compute pool

- Topology: **Agent PCs → Relay → QSDM Hive (Mother) → QSDM Core**.
- Walletless Agents; fixed algorithms only (no remote shell/scripts).
- Separate HMAC credentials, resource caps, durable receipts.
- Paid pooled work on the public network is in an operator-only test, not yet open to other miners. From block 833,800 paid edge tasks settle only through an authorized settlement relay.

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

- Go SDK — `go get github.com/blackbeardONE/QSDM/QSDM/source/sdk/go`
  (source at `QSDM/source/sdk/go/`; it is its own Go module so the path
  resolves, since the parent module's declared path does not match its
  location in the repo). JavaScript SDK: `qsdm-sdk` on npm. Version
  **0.5.0** is both the current source and the npm latest, including the
  corrected `/api/v1/transactions/{id}` route and CELL Stream runtime. Do not
  publish the superseded 0.3.1 audit-branch package.
- WASM wallet module; OpenAPI + API reference; 25+ operator runbooks.
- Docker / Kubernetes deploy manifests; signed Core releases (Sigstore) and SBOM; Hive installers are signed with the QSDM v2 release key (see [Downloads and verification](DOWNLOADS_AND_VERIFICATION.md)).

---

## What is not claimed

- Silence CLI is an optional Cursor/agent helper, not a QSDM product feature.
- Public packages remain unsigned on Windows until the SignPath Foundation application is approved; verify checksums before installing.
- Broad public edge-compute federation and marketplace settlement remain gated on Core lease/escrow records, quotas, and independent security review.
