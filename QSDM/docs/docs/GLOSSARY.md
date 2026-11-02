# Glossary

> **Status:** terms as they are used on the public pilot network (pre-mainnet),
> October 2026. Where a term describes a design that is not active yet, the
> entry says so.

Terms are in alphabetical order. Links go to the page with the details.

| Term | Meaning |
|---|---|
| **Activation height** | The block height from which a consensus rule applies. Blocks below it keep replaying under the old rules. See [Consensus rules](CONSENSUS_RULES.md). |
| **Agent** | A walletless, outbound-only worker program that runs fixed QSDM CPU, NVIDIA GPU or RAM jobs for a Relay. It is not a remote administration tool. See [Edge pool](EDGE_POOL.md). |
| **Attestation** | Evidence attached to every mining proof that the work came from an enrolled NVIDIA GPU. The network rejects proofs without one. |
| **`nvidia-cc-v1`** | Attestation type for data-center NVIDIA GPUs with Confidential Computing, checked against NVIDIA's signed attestation certificates. |
| **`nvidia-hmac-v1`** | Attestation type for consumer NVIDIA GPUs (GeForce/RTX). It binds a GPU to an enrollment through a private key file and a fresh network challenge. It is backed by the bond, not by a hardware certificate. |
| **BFT** | Byzantine fault tolerant voting between validators to agree on the next block. Signed BFT messages are enforced, but multi-validator BFT is not active yet: there is one producer today. |
| **Block reward** | New CELL paid for accepted mining proofs: 3.56490987 CELL per block in the current halving epoch. See [CELL tokenomics](CELL_TOKENOMICS.md). |
| **Bond** | The 10 CELL (1,000,000,000 dust) stake behind each enrolled GPU. It can be locked up front or taken from the first mining rewards (deferred bond). Unenrolling starts an unbonding period of 201,600 blocks. |
| **Canary window** | A limited mining period for enrolled QSDM Hive miners. Windows currently last 7 days (168 hours), with caps per owner per 24-hour epoch. See [Mining today](MINING_TODAY.md). |
| **CELL** | The coin of the QSDM network (name Cell, symbol CELL, 8 decimals). Max supply is 90,000,000 CELL, all from mining. |
| **`consensus_auth`** | The block of `GET /api/v1/status` that reports which consensus rules are active and from which height. See [API reference](API_REFERENCE.md#the-consensus_auth-block). |
| **Dust** | The smallest unit of CELL. 1 CELL = 100,000,000 dust. Fields named `*_dust` are integers in dust. |
| **Ed25519** | A classical signature scheme. QSDM wallets, task actions and consensus use ML-DSA instead. The generic peer-to-peer gossip verifier code still accepts Ed25519, but it cannot be reached on the live chain; retirement is planned. |
| **Edge Control** | The setup utility for Agents and Relays. It ships with Hive and is not another wallet client. |
| **Enrollment** | Registering an NVIDIA GPU for mining with a signed enrollment (`qsdm/enroll/v2`) tied to your wallet and a 10 CELL bond. New enrollments are paused during the current recovery. |
| **Epoch** | Three different periods share this word: the **halving epoch** of 12,623,040 blocks (about four years) in `/status`, the **mining epoch** of 60,480 blocks in `/mining/work`, and the 24-hour epoch used for per-owner mining caps. |
| **Follower** | A node that replays and checks every block from the producer but does not produce blocks itself. |
| **Halving** | The block reward halves at the end of each halving epoch. The next halving is at height 12,623,041. |
| **Hive (QSDM Hive)** | The QSDM desktop app for Windows and Linux: CELL wallet, NVIDIA mining, signed tasks and pooled edge work. The current version is 1.4.22. See [QSDM Hive](QSDM_HIVE.md). |
| **ML-DSA-87** | The NIST post-quantum signature scheme (FIPS 204, security category 5) used for wallet transfers, task actions, consensus messages, Relay proofs and Hive release manifests. |
| **Mother Hive** | The role a QSDM Hive takes while it coordinates a paired Relay and owns the CELL wallet for that group of Agents. It is a role, not another app. |
| **Nonce** | A per-sender counter in every signed transfer. Each transfer must use the next nonce, which stops replays. |
| **Owner signature** | The ML-DSA-87 signature Hive adds to every mining proof with your unlocked wallet. A locked wallet cannot mine. |
| **Parent cells** | The IDs of 2 to 10 committed transactions from the last 8,640 blocks that a transfer names in `parent_cells`. Wallets read them from `GET /api/v1/chain/parents`. |
| **Pilot network** | QSDM's current stage: a public network before mainnet, with one producer, no independent validators and no independent external audit. Treat balances as pilot data. See [Network status](NETWORK_STATUS.md). |
| **Producer** | The node that builds blocks. QSDM has a single producer, run by the project, with node role `validator`. |
| **Proof-of-Entanglement (PoE)** | The rule that every CELL transfer names committed parent cells. Enforced from block 816,600. See [Proof-of-Entanglement](PROOF_OF_ENTANGLEMENT.md). |
| **Receipt** | A record of a transaction's result: block height, status, fee and logs, from `GET /api/v1/receipts/{tx_id}`. Relays also keep job receipts for verified edge work. |
| **Relay** | The program that authenticates Agents, applies resource limits, verifies results, stores receipts and signs aggregate proofs for one or more Mother Hives. |
| **Release key (v2)** | The ML-DSA-87 key that signs Hive release manifests since Hive 1.4.21. Hive refuses updates signed by any other key. See [Downloads & verification](DOWNLOADS_AND_VERIFICATION.md). |
| **Reserved task IDs** | The built-in system task IDs. Since 15 October 2026 only the project's owner key can register them. |
| **Settlement relay** | A Relay authorized to settle paid edge tasks: its key-derived ID is listed in the task's `authorized_relay_ids`. From block 833,800 paid edge tasks settle only through one. |
| **Signed consensus** | The rule that every BFT vote and consensus message carries a valid ML-DSA signature; unsigned consensus traffic is refused. Enforced from block 827,000. See [Signed consensus rollout](SIGNED_CONSENSUS_ROLLOUT.md). |
| **Task action** | A signed `qsdm/tasks/v1` write, such as registering a task (`catalog-register`), staking or claiming rewards, submitted through `POST /api/v1/tasks/actions/submit-signed`. Signatures are required from block 625,000. |
| **Task manifest** | The versioned definition of a task (title, economics, compatibility, authorized Relays) published as signed catalog state. See [Task registry](QSDM_TASK_REGISTRY.md). |
| **Transaction content root** | The block field that commits the full contents of each transaction, not only its ID. Active from block 625,000. |
| **Validator** | A CPU node that checks blocks and serves the API. Today the only block producer is the project's node. QSDM no longer plans a set of independent validators: block production will move to miners, in stages (see the [Roadmap](ROADMAP.md)). Anyone can [run a full node](RUN_A_FULL_NODE.md). See [Node roles](NODE_ROLES.md). |
