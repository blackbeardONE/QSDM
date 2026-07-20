# QSDM Treasury Policy

Status: production funding and custody policy

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
| 0 | Genesis Treasury Vault | 3-of-5 ML-DSA multisig plus 48-month on-chain vesting | Holds the disclosed protocol treasury allocation. Never connected to an application server. |
| 1 | Operations Treasury | 2-of-3 multisig, monthly budget | Receives only vested releases approved under the public budget. Refills lower tiers. |
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

QSDM does not yet ship the Tier 0 multisig and vesting contract described by
the tokenomics specification. Those contracts, their tests, and an external
audit remain a mainnet gate. Until they exist, use offline key custody and
manual multi-person approval, and do not describe the custody layer as
trustless.

## 3. Funding source

The published supply cap is 100,000,000 CELL:

- 90,000,000 CELL is emitted through protocol mining.
- 10,000,000 CELL is a disclosed genesis protocol-treasury allocation, locked
  and released linearly over 48 months.
- Founder and insider allocation is 0 CELL.

In broad industry terminology, CELL created for a treasury at genesis is a
**premine/genesis allocation**, even when no founder receives it. QSDM should
therefore say "0% founder or insider premine" rather than the ambiguous "0%
premine."

If the official genesis state has not been finalized, the preferred source for
referral and onboarding budgets is the vested 10% protocol allocation. If the
network has already launched without that allocation, do **not** mint it later.
Fund programs from legitimately mined CELL, protocol fee revenue approved by
governance, or disclosed sponsor revenue transferred into the Operations
Treasury.

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

Build the isolated signer once and run two instances:

```text
go build -o qsdm-treasury-signer ./cmd/qsdm-game-signer
```

Referral signer environment:

```text
QSDM_SIGNER_LISTEN=127.0.0.1:8897
QSDM_SIGNER_API_URL=http://127.0.0.1:8080
QSDM_SIGNER_KEYSTORE=/secure/referral-wallet.json
QSDM_SIGNER_PASSPHRASE_FILE=/secure/referral.passphrase
QSDM_SIGNER_TOKEN_FILE=/secure/referral-signer.token
QSDM_SIGNER_ROLE=referral
QSDM_SIGNER_MAX_PAYOUT=5
QSDM_SIGNER_MIN_RESERVE=25
QSDM_SIGNER_FEE=0.001
```

Onboarding signer environment:

```text
QSDM_SIGNER_LISTEN=127.0.0.1:8898
QSDM_SIGNER_API_URL=http://127.0.0.1:8080
QSDM_SIGNER_KEYSTORE=/secure/onboarding-wallet.json
QSDM_SIGNER_PASSPHRASE_FILE=/secure/onboarding.passphrase
QSDM_SIGNER_TOKEN_FILE=/secure/onboarding-signer.token
QSDM_SIGNER_ROLE=faucet
QSDM_SIGNER_MAX_PAYOUT=1
QSDM_SIGNER_MIN_RESERVE=10
QSDM_SIGNER_FEE=0.001
```

Copy `QSDM/deploy/qsdm-treasury.example.json` to the validator run directory as
`qsdm-treasury.json`, fill in the signer URLs, separate tokens, and actual
wallet addresses. Prefer `signerTokenFile` paths, as shown in the example, so
the JSON contains no bearer token. Restrict the JSON and token files to their
respective operating-system accounts. The launcher does not log token content.

On Windows, `autoStart: true` lets `start_local_validator.ps1` supervise the
two loopback signer processes after Core is ready. Configure `keystorePath`,
`passphraseFile`, and `signerTokenFile` as absolute paths. These fields contain
paths only, never secret values. The launcher verifies each signer's role and
wallet address before reporting startup success.

Both signer URLs must use loopback HTTP or authenticated HTTPS. Core refuses
to send a bearer token over plain HTTP to a non-loopback host.

## 6. Production network readiness

Never fund a Tier 2 wallet from a validator merely because its API is healthy.
The validator must be connected to the production network, caught up, and
agree with the production gateway on sampled block hashes and wallet state.

On Windows, start the validator in networked mode:

```powershell
pwsh -File QSDM/scripts/start_local_validator.ps1 -Networked -Restart
```

This choice is persisted in
`QSDM/source/.cache/local-validator/validator-mode.json`, so the watchdog
restarts the networked state after a crash or reboot instead of silently
falling back to the retired solo ledger. Networked mode uses
`https://api.qsdm.tech/api/v1` as its default HTTP chain source and the public
QSDM bootstrap peer for libp2p connectivity.

Run the fail-closed readiness gate before any funding transfer or payout
activation:

```powershell
pwsh -File QSDM/scripts/test_treasury_readiness.ps1 `
  -WalletAddress <funding-wallet-address> `
  -OutputPath QSDM/source/.cache/local-validator/treasury-readiness.json
```

The gate requires all of the following:

1. At least one live peer.
2. Local height no more than the configured lag behind the production gateway.
3. Matching genesis, near-tip, and tip block hashes.
4. Matching balance and nonce for the funding, referral, and onboarding
   wallets.
5. Correct signer role and wallet identity on both loopback signers.
6. Separate referral and onboarding addresses.
7. Referral claims and onboarding payouts still locked during funding review.

The historical pilot chain contains legacy state transitions that were not all
serialized into block transactions. A clean validator therefore needs a
trusted network checkpoint before ordinary block catch-up can continue. A
checkpoint is acceptable only when its archive hash is verified, its tip hash
and state root match `/api/v1/chain/blocks`, and Core's own restore-time state
root validation succeeds. Do not import an account JSON file by itself.

The previous local solo state must be retained as an archive for audit, never
merged with the accepted production state. Balances that exist only in that
solo archive are not spendable production CELL.

## 7. Funding and payout flow

1. Governance approves a bounded Operations Treasury budget.
2. Operations signs a normal CELL transfer to the relevant Tier 2 wallet.
3. The signer reports that wallet address and balance to Core.
4. Core verifies referral or onboarding eligibility and sends an idempotent
   payout request to the matching signer.
5. The signer enforces role, maximum payout, and minimum reserve; signs a normal
   transfer; and submits it through `/api/v1/wallet/submit-signed`.
6. Core stores the returned transaction ID in the claim receipt.

The referral funding helper at `QSDM/scripts/fund_referral_reward_pool.ps1`
uses the same signed-transfer path. Run it first without `-Submit` for a dry
run. `QSDM/scripts/fund_treasury_wallet.ps1` provides the same dry-run-first
flow for referral, onboarding, integration, and operations wallets.

For the production pilot, the public target balances and expected funding
wallet are stored in
`QSDM/deploy/canonical-pilot-funding-plan.json`. On the Linux workstation that
holds that funding wallet, run the guarded batch helper without `--submit`
first:

```bash
bash QSDM/scripts/fund_ecosystem_wallets_linux.sh \
  --qsdmcli /path/to/qsdmcli \
  --keystore /secure/path/wallet.json \
  --passphrase-file /secure/path/wallet.passphrase
```

After reviewing the exact missing balances, add `--submit`. The helper refuses
a different source wallet, checks the public policy ceilings, transfers only
the difference between the confirmed balance and each target, waits for each
transfer to appear on the production ledger, and stops before the next transfer
if confirmation is ambiguous. It intentionally does not fund Sky Fang, the
protocol reserve, or the pooled-compute reserve.

## 8. Existing development balances

Earlier local launchers directly credited a 500 CELL referral account and
topped local wallets through the faucet. The production runtime ignores that
legacy referral account and rejects the retired seed environment variables.
It does not silently delete historical state.

Before declaring an existing ledger the production state, inventory all
direct credits and environment prefunds. Either start from an audited genesis
snapshot that excludes them or approve a deterministic, network-wide state
migration. Editing one validator's JSON ledger is not an acceptable burn or
migration.

## 9. Mainnet release gates

The referral and onboarding payout paths are production-shaped after this
change: they spend only existing CELL through isolated, policy-limited signer
wallets. That does not by itself make the current chain a finished mainnet.

Before a mainnet declaration, QSDM still needs all of the following:

1. Implement and externally audit the Tier 0 ML-DSA multisig and vesting rules.
2. Publish one official genesis manifest containing the exact treasury
   allocation, vesting schedule, wallet addresses, and cryptographic hash.
3. Replace or formally redesign the solo-validator block driver's synthetic
   `qsdm-system-funder` reserve as consensus-native issuance. Its current
   oversized bookkeeping balance and BFT-bypass mode were built for solo
   testnet continuity, not adversarial mainnet operation.
4. Start from an audited clean state or execute a deterministic, network-wide
   migration that removes every historic development credit.
5. Complete an independent economic, consensus, and custody audit.

Until these gates close, describe the network as an incentivized production
pilot or testnet, not a trust-minimized mainnet.
