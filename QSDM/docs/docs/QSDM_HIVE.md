# QSDM Hive

QSDM Hive is the only QSDM desktop client. It provides CELL wallets, signed
QSDM tasks, integrations, NVIDIA-attested protocol mining, and pooled CPU,
GPU, or RAM participation. Agent, Relay, and Edge Control programs run in the
background or help with local setup. They are not alternative QSDM clients and
do not hold a user wallet.

## Install path

Hive is the recommended desktop app for most users. Use it to manage
CELL wallets, run signed tasks, link integrations, and start eligible mining
work. The standalone console miner remains an advanced operator artifact. QSDM
does not ship a separate consumer GUI miner.

1. Install QSDM Hive.
2. Create or import a QSDM wallet.
3. Back up the QSDM keystore JSON and passphrase.
4. Run CELL tasks, integrations, or qualifying mining work.

## Linux x86-64

Linux Hive connects directly to the production QSDM Network gateway for ledger,
wallet, chain-height, and mining-reward reads. Task catalog metadata continues
through the restricted home-validator gateway. Ordinary desktop users do not
install a local validator. Version 1.4.0 bundles the native `qsdmcli` signer,
supervised console miner, CUDA protocol solver, edge agent, and CUDA edge
helper on the supported Electron 43 runtime.
Open **Settings > Wallet** to create a new
ML-DSA QSDM wallet or import an existing keystore JSON plus passphrase.

The encrypted wallet is stored with private file permissions in Hive's
application-data directory. Hive protects its working passphrase with the
operating-system secret store where a protected backend is available. Back up
the encrypted keystore JSON from **Settings > Wallet**, and keep the passphrase
separately; Hive deliberately does not export a plaintext passphrase beside
the backup. Address
copying uses Electron's native Linux clipboard. The public gateway does not
offer an unauthenticated faucet, so gateway-connected Hive shows **Receive
CELL** and the wallet address instead of a claim action. Hive offers a one-time
starter grant only when a local validator is connected to a separately funded
onboarding treasury signer. The grant is a normal signed transfer, not minted
or directly credited CELL. Task availability, staking,
private tasks, and upgrades use the active QSDM signer's confirmed CELL
balance rather than the legacy Hive profile account.
Legacy `KOII` catalog labels without a token mint are normalized to native CELL
tasks, so a funded signer can stake and run them.

The home gateway supplies operator task details and projected task state. Hive
1.3.93 limits repeated requests when that service is unavailable. One timeout
is retried normally. Two temporary failures within 30 seconds pause that route
for 20 seconds. One slow related endpoint cannot pause a recently healthy API.
Confirmed task, balance, reward, and chain values remain visible with a
**Reconnecting** label during a brief outage. Hive blocks signed actions when
the chain identity does not match. Hive
obtains the mining account nonce and submits the signed `qsdm/enroll/v2`
transaction to the production QSDM Core service. QSDM Core
verifies ML-DSA wallet ownership and deferred-bond work before sharing the
enrollment with validators. Local and explicitly configured custom Core URLs are
left unchanged.

Signed task actions use limited retries for temporary network and HTTP 5xx
failures. Every retry reuses the same signed action ID, nonce, and payload; a
validator duplicate response confirms the earlier submission instead of
creating a second stake or transfer.

## Task Studio

Task Studio is available under **Add Task**. It publishes signed, versioned task manifests to the QSDM consensus catalog using the active QSDM wallet. Compatible catalog changes appear in Hive within about 15 seconds after validator finalization; they do not require a Hive update.

Task Studio initially publishes the built-in `generic-proof-v1` capability. New executable capabilities require reviewed Hive code and a new Hive release. Remote JavaScript is not accepted as a catalog runtime.

## Wallet backup

QSDM CELL wallet recovery uses the **QSDM keystore JSON plus its passphrase**. Hive profile phrases, when present, restore only local Hive profile data. They are not CELL wallet recovery phrases.

## QSDM Wallet browser extension

The QSDM Wallet extension gives Chromium websites a small `window.qsdm`
provider without copying a wallet into the browser. Create or import the wallet
once in **Settings > Wallet** and keep Hive running in the notification area.
The extension uses that same active wallet and sees only its public address;
the encrypted keystore and passphrase stay in Hive.

Connect a website once from the site or extension popup. Hive remembers that
exact HTTPS origin until it is disconnected or revoked under **Settings >
Wallet > Connected Sites**. Signing and CELL transfers remain separate actions:
Hive comes to the foreground and shows the exact site and operation before each
approval. HTTP is accepted only for local development on `localhost` or
`127.0.0.1`.

Hive 1.4.0 automatically registers the secure native bridge for the current
user on Chrome, Edge, Chromium, and Brave without administrator access. The
official extension has a stable pinned ID. The bridge listens only on loopback,
authenticates each browser-host request with an ephemeral 256-bit token, and is
not a public network API.

Download the versioned extension package from the QSDM download page, verify
its SHA-256 checksum, unzip it, and load that folder once from the browser's
extension page. Browser-store installation will replace this one-time setup
after the extension is approved by the relevant stores.

## Tasks in Hive

- **QSDM Miner** requires an NVIDIA Turing-or-newer GPU (CUDA compute capability 7.5+). Hive 1.3.93 runs the current SHA3/DAG proof search through the packaged CUDA solver and refuses to start the task if that solver, a compatible driver, or the GPU is unavailable. Windows and Linux release builds fail before publication if either mining executable is missing. Concurrent restore and startup requests share one launch operation, so one Hive task supervises one CUDA miner. On Linux it recognizes the same packaged miner across AppImage mount changes and adopts that process after an unclean Hive restart instead of launching a conflicting duplicate. It also ignores obsolete protected Windows miner services instead of adopting them as the current task. `fork_v2_tc_active` describes the future Tensor-Core consensus algorithm; it is separate from today's CUDA SHA3 backend. A zero-balance signer may choose **Use mining earnings**: accepted mining rewards fill the 10 CELL slashable bond first, then subsequent rewards become spendable. Operators who already hold CELL may still lock the bond immediately.
- **QSDM Edge Worker CPU** shares bounded CPU capacity locally or through an authenticated QSDM Relay.
- **QSDM Edge Worker GPU** shares bounded NVIDIA CUDA capacity. This is pooled compute, not protocol mining.
- **QSDM Edge Worker RAM** shares a configured memory allowance for fixed memory-backed jobs.
- **Mother Hive Task** turns the active QSDM Hive into the wallet-owning coordinator for a paired Relay. Hive displays the acknowledged virtual CPU, GPU, and RAM pools, active Agents, jobs, and verified receipts. These pools are schedulable QSDM capacity, not transparent local operating-system devices.
- **Sky Fang - MMORPG** verifies that a Sky Fang account is linked to the active QSDM wallet before reward proofs are submitted.

For a computer laboratory, walletless Agent computers send fixed bounded work to a QSDM Relay. Agents run independently and do not require Hive. The Relay applies CPU/GPU/RAM policy, verifies receipts, and can serve one or more QSDM Hives. **Mother Hive** is only the role assumed by each active QSDM Hive; it is not another application. Agents cannot receive arbitrary scripts or shell commands, and their credential cannot impersonate a Hive role.

Open **Mother Hive** from Hive's main navigation. In Edge Control on the Relay computer, name this Hive and create a dedicated `QSDM-EDGE-3` Mother Hive code. Hive stores that scoped credential with private file permissions and immediately shows Relay health, connected Agents, resource totals, jobs, and receipts. Each Hive receives an independently revocable identity; its jobs, cancellation requests, receipts, payout binding, and settlement acknowledgements are not visible to another Hive. Agent codes are deliberately rejected. Disconnecting Hive does not revoke it; use Edge Control's paired-Hive list when access must be removed.

New Relay configurations default to 50% CPU, 40% GPU, and 25% RAM. Existing
policies are not silently changed. Hive 1.3.91 warns when a paired Relay grants
90% or more of any resource because a 100% policy can make the Relay workstation
unresponsive and worsen network or desktop stalls. Verified capacity receipts
show that an Agent is alive and eligible; they are not paid work when the active
job count is zero.

Hive 1.3.93 adds an **Application Compute Gateway** to the Mother Hive task. While the task and its paired Relay are online, native applications can submit fixed CPU, NVIDIA GPU, or RAM jobs through the authenticated loopback endpoint at `http://127.0.0.1:7742`. Hive shows its status and private credential path on the Mother Hive page. The gateway queues work durably on the Relay, returns verified results and receipts, and never accepts uploaded code, scripts, commands, or executables. Unmodified applications cannot see the pool as local hardware; they must use this API or the packaged `qsdm-edge-agent compute` commands.

Hive 1.3.94 added the **Virtual Compute Runtime** workbench. Operators can select a live pooled resource, run a bounded workload, monitor queue and Agent state, cancel active work, and see verified receipts directly from the Mother Hive page. Discovery routes report available capacity and the reviewed workload catalog without exposing the private gateway credential to renderer code. Hive 1.3.95 added expiring, workload-scoped HTTPS federation invitations for fixed-trust remote Relays. Hive 1.3.96 authenticates update metadata and installers with a pinned QSDM ML-DSA-87 release key. See the [pooled edge-compute guide](EDGE_POOL.md#virtual-compute-runtime), [private federation guide](EDGE_FEDERATION.md), and [QSDM-native release signing](QSDM_NATIVE_RELEASE_SIGNING.md).

For an authorized Relay batch, QSDM Core atomically allocates **70% to the contributor-owner wallet, 15% to the Mother Hive operator, and 15% to the CELL ecosystem reserve** at `651a79b2b1790820dd73bda81be24057e1bc27377c1f1117c6db2ab79dc038ea`. Agents remain walletless, so the paired Hive binds the owner wallet for the trusted group. Every validator verifies the Relay's ML-DSA-87 signature, manager-approved Relay ID, payout binding, round, time window, and global proof/receipt replay state. No payout occurs unless the corresponding task reward pool already contains enough CELL.

Hive includes the matching agent and CUDA helper on Windows and Linux. Standalone bundles for additional laboratory computers are available from the [Hive download page](https://qsdm.tech/download.html).

Resource-worker rewards are paid only from an existing funded task pool. Hive does not charge a participant to manufacture its own reward, and completed work does not mint CELL by itself.

## Console mining

Advanced operators can run `qsdmminer-console` directly when they need a
terminal-first service workflow. Consumer setups should use Hive. The retired
GUI miner is not a supported consumer app.

Deferred enrollment is explicit, not a stake bypass. Hive displays the locked
bond, target, and spendable wallet balance separately. The enrollment carries
one-time Hashcash work to discourage zero-cost registry spam, and a signed
zero-fee unenrollment remains available to a miner that has not earned liquid
CELL yet.

## Networking

Hive uses local services for the desktop app and node monitor. Public reachability should go through the QSDM home gateway or network tunnel unless an operator intentionally exposes validator services.

## Related pages

- [Download QSDM Hive](https://qsdm.tech/download.html)
- [CELL tokenomics](CELL_TOKENOMICS.md)
- [Sky Fang official website](https://skyfang.xyz/)
- [Sky Fang integration notes](https://skyfang.xyz/docs)
- [Miner quickstart](MINER_QUICKSTART.md)
- [Pooled edge-compute guide](EDGE_POOL.md)
- [Mother Hive federation design](EDGE_FEDERATION.md)
- [Wallet explanation](WALLET_EXPLANATION.md)
