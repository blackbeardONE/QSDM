# QSDM Treasury Policy

Status: funding and custody policy (pilot network)

## 1. Non-negotiable rules

1. QSDM Core must never create referral or onboarding balances with
   `AccountStore.Credit`.
2. Every payout must be a normal ML-DSA-signed CELL transfer with an on-chain
   transaction ID.
3. Core must not hold a treasury private key. A loopback-only signer process
   holds one narrowly funded hot-wallet key and enforces its own spending cap.
4. Referral, onboarding, integration, and task rewards use separate wallets.
5. Treasury balances, transfers, budgets, and policy changes are public and
   auditable. Secrets, keystores, bearer tokens, and passphrases are not.

## 2. Wallet hierarchy

| Tier | Wallet | Suggested control | Purpose |
|---|---|---|---|
| 1 | Operations Treasury | 2-of-3 multisig, monthly budget | Receives only existing CELL (mined CELL, governance-approved fee revenue, or disclosed sponsor funds) approved under the public budget. Refills lower tiers. |
| 2A | Referral Payout Wallet | Isolated signer, maximum 5 CELL per payout, 30-day refill | Pays one qualified referral reward. |
| 2B | Onboarding Payout Wallet | Separate isolated signer, maximum 1 CELL per payout, 7-day refill | Pays a one-time starter grant. |
| 2C | Integration Payout Wallets | One wallet per integration | Keeps Sky Fang or future partner risk out of core treasury funds. |
| 2D | Task Reward Pools | One funded pool per task | Pays verified task submissions under task-specific policy. |
| 2E | Pooled Compute Ecosystem Reserve | Dedicated public wallet, governance-approved sweeps | Receives the 15% ecosystem share from settled Agent/Relay workloads. |

Use different keystores, passphrases, bearer tokens, operating-system users,
ports, and refill transactions for Tier 2A and Tier 2B. Never point referral and
faucet configuration at the same wallet.

Tier 2E must also be distinct from referral, onboarding, integration, task,
Mother Hive operator, and contributor wallets. It receives existing CELL from
funded workload settlement; it does not mint CELL. The production Tier 2E
address is
`651a79b2b1790820dd73bda81be24057e1bc27377c1f1117c6db2ab79dc038ea`.
The same address is consensus-bound in QSDM Core. Pooled settlement remains
fail-closed unless the task manifest authorizes the Relay's key-derived ID and
the task reward pool is funded.

There is no Tier 0 genesis treasury vault: [Genesis Policy v1.0](GENESIS_POLICY_v1.0.md)
withdrew the 10,000,000 CELL treasury proposal, so no treasury multisig or
vesting contract is planned. Use offline key custody and manual multi-person
approval for the Operations Treasury, and do not describe the custody layer as
trustless.

## 3. Funding source

The max supply is 90,000,000 CELL, all from mining
([Genesis Policy v1.0](GENESIS_POLICY_v1.0.md)):

- 90,000,000 CELL is emitted through protocol mining.
- There is no genesis treasury allocation. The earlier 10,000,000 CELL
  treasury proposal is withdrawn.
- Founder, insider, team, advisor, sale and airdrop allocations are 0 CELL.

Any future proposal to add an allocation needs a new published policy version,
a counsel opinion and the governance process, and it may never mint CELL on a
chain that has already launched without it.

Fund programs from legitimately mined CELL, protocol fee revenue approved by
governance (fees are burned today, so there is none until the fee destination
changes under a new policy version), or disclosed sponsor revenue transferred
into the Operations Treasury.

Never fund a production reward with a faucet credit, an environment prefund,
or a direct state-file edit.

## 4. Budget defaults

Initial conservative limits:

| Program | Hot-wallet refill | Per payout | Minimum retained reserve |
|---|---:|---:|---:|
| Referral | 500 CELL maximum per 30 days | 5 CELL | 25 CELL |
| Onboarding | 100 CELL maximum per 7 days | up to 1 CELL | 10 CELL |

These are operating ceilings, not promises to spend. Stop payouts when a
budget is exhausted. Refill only after reviewing claim counts, unique active
wallets, payout transaction IDs, and abuse alerts.

The onboarding grant is one transaction per wallet. This alone does not stop a
Sybil attacker from generating wallets; a public grant also needs an external
eligibility control such as verified account age, a trusted integration claim,
or a rate-limited invitation. Until that control exists, keep the endpoint
loopback-only for operator-managed onboarding.

## 5. Runtime configuration

Operational custody procedures are kept in the operators' private runbooks.
In summary: each Tier 2 payout wallet is held by its own isolated, role-locked
signer with a per-payout cap and minimum reserve, and Core never holds a
treasury private key.

## 6. Production network readiness

Never fund a Tier 2 wallet from a validator merely because its API is healthy.
The validator must be caught up with the network and agree with the public
gateway on sampled block hashes and wallet state. The detailed readiness
checks are part of the operators' private runbooks.

## 7. Funding and payout flow

1. Governance approves a bounded Operations Treasury budget.
2. Operations signs a normal CELL transfer to the relevant Tier 2 wallet.
3. The signer reports that wallet address and balance to Core.
4. Core verifies referral or onboarding eligibility and sends an idempotent
   payout request to the matching signer.
5. The signer enforces role, maximum payout, and minimum reserve; signs a normal
   transfer; and submits it through `/api/v1/wallet/submit-signed`.
6. Core stores the returned transaction ID in the claim receipt.

## 8. Existing development balances

Earlier local launchers directly credited a 500 CELL referral account and
topped local wallets through the faucet. The production runtime ignores that
legacy referral account and rejects the retired seed environment variables.
It does not silently delete historical state.

Before declaring an existing ledger the production state, inventory all
direct credits and environment prefunds. Either start from an audited genesis
snapshot that excludes them or approve a deterministic, network-wide state
migration. Editing one validator's JSON ledger is not an acceptable burn or
migration. Genesis Policy v1.0 §5 chooses the migration path: mainnet
continues the current chain.

## 9. Mainnet release gates

The referral and onboarding payout paths are production-shaped after this
change: they spend only existing CELL through isolated, policy-limited signer
wallets. That does not by itself make the current chain a finished mainnet.

Before a mainnet declaration, QSDM still needs all of the following:

1. Withdrawn: Genesis Policy v1.0 has no genesis treasury, so there is no
   Tier 0 multisig or vesting contract to build.
2. Publish the official, aggregate-only migration manifest, its SHA-256 and
   the fork height (Genesis Policy v1.0 §5). It contains no treasury
   allocation.
3. Replace or formally redesign the solo-validator block driver's synthetic
   `qsdm-system-funder` reserve as consensus-native issuance. Its current
   oversized bookkeeping balance and BFT-bypass mode were built for solo
   testnet continuity, not adversarial mainnet operation.
4. Execute the deterministic, network-wide migration of Genesis Policy v1.0
   §5: remove the synthetic funder balance, and burn or explicitly classify
   every historic development credit. If the manifest cannot reach a "ready"
   state, the fallback is an audited clean genesis under a new policy
   version.
5. Complete and publish a security review of economics, consensus and
   custody, with its findings fixed. Owner decision 2026-10-17: the path is
   the open [community review and bug bounty](COMMUNITY_REVIEW.md) instead of a
   paid audit firm. An independent professional audit remains welcome. No
   independent audit has been completed.

Until these gates close, describe the network as a pilot network (pre-mainnet), not a
trust-minimized mainnet.
