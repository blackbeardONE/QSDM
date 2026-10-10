# Feature Summary — QSDM

**Last Updated:** October 2026 · Running release **hardened-legacy-20261002-d7ffcd4-hl2** · Hive **1.4.21** · Edge Control **1.3.7**

QSDM (Quantum-Secure Dynamic Mesh) is a mesh ledger with post-quantum (ML-DSA-87) wallet signatures whose native coin is **Cell (CELL)**. It currently runs as a public pilot network (pre-mainnet) with a single block producer during recovery. Validators are designed to run PoE + BFT consensus; miners mint CELL via NVIDIA-attested Proof-of-Work. Hive is the public desktop client for wallets, signed tasks, integrations, NVIDIA mining, and Mother Hive edge pools. Optional home-gateway, agent, relay, and attestation tools support operators without becoming separate consumer clients.

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

- **Proof-of-Entanglement (PoE) + BFT** on a dynamic mesh is the target consensus design; today the pilot network runs a single block producer during recovery. Every node checks each transfer's signature under the sender's own key. The PoE parent rules (parents must be committed transactions) are enforced only from an activation height that has not been set yet ([details](PROOF_OF_ENTANGLEMENT.md)).
- **ML-DSA-87** wallet transaction signatures (NIST FIPS 204) with Zstd compression and batch signing. The generic P2P transaction verifier still also accepts Ed25519; signed consensus messages are supported but not yet active.
- **3D mesh validation**, rule-based quarantine, and staked reputation penalties.
- **Dynamic submeshes** with fee thresholds, priority routing, and geotags.
- **SQLite + Zstd** storage; **ScyllaDB** path available for high throughput.
- **libp2p + GossipSub** peer mesh; public API at `api.qsdm.tech`.

## CELL tokenomics

- **Max supply 90,000,000 CELL from mining**, **0% founder allocation**, 4-year halvings. A 10,000,000 CELL genesis treasury is a mainnet proposal (not built, not part of the current chain).
- Validators earn **transaction fees only** (no block subsidy).
- Tokenomics surface on `GET /api/v1/status`.

## Node roles (enforced)

- **Validator** — CPU-only, `mining_enabled=false`, public REST API, consensus.
- **Miner** — separate process/machine, NVIDIA GPU, HTTPS to validator `/api/v1/mining/*`.
- No combined full-node mode.

## Mining (protocol v2)

- NVIDIA-locked proofs (`nvidia-cc-v1`, `nvidia-hmac-v1`); Turing-or-newer GPU required for protocol mining.
- Public mining API: work, challenge, submit, enrollment, emission, blocks, slash.
- On-chain enrollment with **10 CELL** slashable bond; Hashcash anti-spam.
- Consumer path: **QSDM Hive** Miner task (CUDA solver bundled). Miners can start from zero liquid CELL by choosing deferred bond from accepted mining earnings. Hive 1.4.21 signs miner operator actions automatically and is published on the hive-v2 channel, signed by the v2 release key. See [Mining today](MINING_TODAY.md).
- Operator path: `qsdmminer-console`.

## Wallet & self-custody

- Operator wallet API plus **`POST /api/v1/wallet/submit-signed`** self-custody path.
- Browser wallet at `/wallet/` — client-side ML-DSA-87 keystore (WASM + WebCrypto).
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
- Application Compute Gateway on `127.0.0.1:7742` for bounded local jobs.
- Sky Fang MMORPG wallet-link task (earn-only CELL; no pay-to-win power). The task is not currently published in the on-chain catalog while the pilot network recovers.

## Edge compute pool

- Topology: **Agent PCs → Relay → QSDM Hive (Mother) → QSDM Core**.
- Walletless Agents; fixed algorithms only (no remote shell/scripts).
- Separate HMAC credentials, resource caps, durable receipts.
- The public edge Relay is read-only during network recovery; pooled work and settlement are paused.

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
  location in the repo). JavaScript SDK: `qsdm-sdk` on npm. Current source
  is **0.3.3**, including the corrected `/api/v1/transactions/{id}` route
  and CELL Stream runtime; the public registry remains at **0.3.0** until
  the normal authenticated release workflow publishes 0.3.3. Do not
  publish the superseded 0.3.1 audit-branch package.
- WASM wallet module; OpenAPI + API reference; 25+ operator runbooks.
- Docker / Kubernetes deploy manifests; signed Core releases (Sigstore) and SBOM; Hive installers are signed with the QSDM v2 release key (see [Downloads and verification](DOWNLOADS_AND_VERIFICATION.md)).

---

## What is not claimed

- Silence CLI is an optional Cursor/agent helper, not a QSDM product feature.
- Public packages remain unsigned on Windows until the SignPath Foundation application is approved; verify checksums before installing.
- Broad public edge-compute federation and marketplace settlement remain gated on Core lease/escrow records, quotas, and independent security review.
