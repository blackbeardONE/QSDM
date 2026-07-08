# QSDM Hive

QSDM Hive is the only QSDM desktop client. It provides CELL wallets, signed QSDM tasks, integrations, NVIDIA-attested protocol mining, and pooled CPU, GPU, or RAM participation. Agent, Relay, and Edge Control binaries are headless or local setup utilities; they are not alternative QSDM clients and do not hold a user wallet.

## Install path

Hive is the public release path for most users. It is the recommended way to use
CELL wallets, run signed tasks, link integrations, and start eligible mining
work. The standalone console miner remains an advanced operator artifact. QSDM
does not ship a separate consumer GUI miner.

1. Install QSDM Hive.
2. Create or import a QSDM wallet.
3. Back up the QSDM keystore JSON and passphrase.
4. Run CELL tasks, integrations, or qualifying mining work.

## Linux x86-64

Linux Hive connects directly to the canonical public QSDM Core for ledger,
wallet, chain-height, and mining-reward reads. Task catalog metadata continues
through the restricted home-validator gateway. Ordinary desktop users do not
install a local validator. Version 1.3.89 bundles the native `qsdmcli` signer,
supervised console miner, CUDA protocol solver, edge agent, and CUDA edge
helper.
Open **Settings > Wallet** to create a new
ML-DSA QSDM wallet or import an existing keystore JSON plus passphrase.

The wallet and passphrase are stored with private file permissions in Hive's
application-data directory. Back up both from **Settings > Wallet**. Address
copying uses Electron's native Linux clipboard. The public gateway does not
offer an unauthenticated faucet, so gateway-connected Hive shows **Receive
CELL** and the wallet address instead of a claim action. Hive offers a one-time
starter grant only when a local validator is connected to a separately funded
onboarding treasury signer. The grant is a normal signed transfer, not minted
or directly credited CELL. Task availability, staking,
private tasks, and upgrades use the active QSDM signer's canonical CELL
balance rather than the legacy Hive profile account.
Legacy `KOII` catalog labels without a token mint are normalized to native CELL
tasks, so a funded signer can stake and run them.

The home gateway intentionally supplies operator task metadata and projected
task state. Hive 1.3.89 isolates that route behind a bounded outage circuit.
One isolated timeout retries normally; two transient failures within 30 seconds
open the route for 20 seconds. A recently healthy API cannot be quarantined by
one slow sibling route. Confirmed task, balance, reward, and chain values remain
visible with a **Reconnecting** state for brief outages, while actual chain
mismatches continue to fail closed. Hive
obtains the mining account nonce and submits the signed `qsdm/enroll/v2`
transaction to canonical Core. Canonical Core
verifies ML-DSA wallet ownership and deferred-bond work before gossiping the
enrollment to validators. Local and explicitly configured custom Core URLs are
left unchanged.

Signed task actions use bounded retries for transient network and HTTP 5xx
failures. Every retry reuses the same signed action ID, nonce, and payload; a
validator duplicate response confirms the earlier submission instead of
creating a second stake or transfer.

## Task Studio

Task Studio is available under **Add Task**. It publishes signed, versioned task manifests to the QSDM consensus catalog using the active QSDM wallet. Compatible catalog changes appear in Hive within about 15 seconds after validator finalization; they do not require a Hive update.

Task Studio initially publishes the built-in `generic-proof-v1` capability. New executable capabilities require reviewed Hive code and a new Hive release. Remote JavaScript is not accepted as a catalog runtime.

## Wallet backup

QSDM CELL wallet recovery uses the **QSDM keystore JSON plus its passphrase**. Hive profile phrases, when present, restore only local Hive profile data. They are not CELL wallet recovery phrases.

## Tasks in Hive

- **QSDM Miner** requires an NVIDIA Turing-or-newer GPU (CUDA compute capability 7.5+). Hive 1.3.89 runs the current SHA3/DAG proof search through the packaged CUDA solver and refuses to start the task if that solver, a compatible driver, or the GPU is unavailable. Windows and Linux release builds fail before publication if either mining executable is missing. On Linux it recognizes the same packaged miner across AppImage mount changes and adopts that process after an unclean Hive restart instead of launching a conflicting duplicate. It also ignores obsolete protected Windows miner services instead of adopting them as the current task. `fork_v2_tc_active` describes the future Tensor-Core consensus algorithm; it is separate from today's CUDA SHA3 backend. A zero-balance signer may choose **Use mining earnings**: accepted mining rewards fill the 10 CELL slashable bond first, then subsequent rewards become spendable. Operators who already hold CELL may still lock the bond immediately.
- **QSDM Edge Worker CPU** shares bounded CPU capacity locally or through an authenticated QSDM Relay.
- **QSDM Edge Worker GPU** shares bounded NVIDIA CUDA capacity. This is pooled compute, not protocol mining.
- **QSDM Edge Worker RAM** shares a configured memory allowance for fixed memory-backed jobs.
- **Mother Hive Task** turns the active QSDM Hive into the wallet-owning coordinator for a paired Relay. Hive displays the acknowledged virtual CPU, GPU, and RAM pools, active Agents, jobs, and verified receipts. These pools are schedulable QSDM capacity, not transparent local operating-system devices.
- **Sky Fang - MMORPG** verifies that a Sky Fang account is linked to the active QSDM wallet before reward proofs are submitted.

For a computer laboratory, walletless Agent computers send fixed bounded work to a QSDM Relay. The Relay applies CPU/GPU/RAM policy, verifies receipts, and reports aggregate proofs to QSDM Hive. **Mother Hive** is only the role assumed by that active QSDM Hive; it is not another application. Agents cannot receive arbitrary scripts or shell commands, and their credential cannot impersonate the Hive role.

Open **Mother Hive** from Hive's main navigation. Paste the dedicated Mother Hive pairing code generated by Edge Control on the Relay computer. Hive stores that role-specific credential with private file permissions and immediately shows Relay health, connected Agents, resource totals, jobs, and receipts. Agent pairing codes are deliberately rejected. Disconnecting Hive does not delete or rotate the Relay-owned credential.

New Relay configurations default to 50% CPU, 40% GPU, and 25% RAM. Existing
policies are not silently changed. Hive 1.3.89 warns when a paired Relay grants
90% or more of any resource because a 100% policy can make the Relay workstation
unresponsive and worsen network or desktop stalls. Verified capacity receipts
show that an Agent is alive and eligible; they are not paid work when the active
job count is zero.

For an authorized Relay batch, QSDM Core atomically allocates **70% to the contributor-owner wallet, 15% to the Mother Hive operator, and 15% to the CELL ecosystem reserve** at `651a79b2b1790820dd73bda81be24057e1bc27377c1f1117c6db2ab79dc038ea`. Agents remain walletless, so the paired Hive binds the owner wallet for the trusted group. Every validator verifies the Relay's ML-DSA-87 signature, manager-approved Relay ID, payout binding, round, time window, and global proof/receipt replay state. No payout occurs unless the corresponding task reward pool already contains enough CELL.

Hive includes the matching agent and CUDA helper on Windows and Linux. Standalone bundles for additional laboratory computers are available from the [Hive download page](https://qsdm.tech/download.html).

Resource-worker rewards are paid only from an existing funded task pool. Hive does not charge a participant to manufacture its own reward, and completed work does not mint CELL by itself.

## Console mining

Advanced operators can run `qsdmminer-console` directly when they need a
terminal-first service workflow. Consumer setups should use Hive. The retired
GUI miner is not a public release path.

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
- [Wallet explanation](WALLET_EXPLANATION.md)
