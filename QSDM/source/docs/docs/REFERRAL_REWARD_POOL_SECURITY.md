# Referral Reward Pool Security

QSDM referral rewards are production-sensitive. Funding a pool is safe only when
funds enter through a signed wallet transfer and payouts remain disabled until
the signed referral eligibility ledger is active.

## Current status

- Funding source: signed QSDM wallet transfer to `QSDM_REFERRAL_REWARD_POOL_ADDRESS`.
- Runtime source: a separately funded referral hot wallet controlled by an
  isolated signer. Legacy local seed variables are rejected.
- Claim state: disabled by default with `QSDM_REFERRAL_CLAIMS_ENABLED` unset.
- Transparency endpoint: `GET /api/v1/referrals/reward-pool`.
- Signed registration endpoint: `POST /api/v1/referrals/register-signed`.
- Status endpoint: `GET /api/v1/referrals/status?referred=<wallet>`.
- Claim endpoint: `POST /api/v1/referrals/claim`.
- Durable ledger: `QSDM_REFERRAL_LEDGER_PATH`.

## Implemented claim rules

1. The referred wallet signs the registration envelope.
2. `referrer == referred` is rejected.
3. The referral code must match the referrer wallet.
4. Each referred wallet can be registered once.
5. Each referred wallet can generate at most one claim receipt.
6. Claims require the referred wallet to reach `QSDM_REFERRAL_MIN_ACCOUNT_NONCE`
   before payout.
7. Claims ask the isolated signer to submit a normal ML-DSA-signed transfer;
   Core never holds the payout key and never mints the reward.
8. Claim receipts are written as `pending` before money moves, then finalized as
   `claimed`. A crash between those two steps leaves a non-paying pending receipt
   for operator reconciliation instead of allowing duplicate payout.

## Required before enabling production claims

1. Fund the reward pool through a signed wallet transfer.
2. Set `QSDM_REFERRAL_LEDGER_PATH` to durable storage.
3. Review the minimum activity rule for the current release.
4. Set `QSDM_REFERRAL_CLAIMS_ENABLED=1` only after the above are true.
5. Configure a role-locked signer, per-payout cap, minimum reserve, and expected
   wallet address as specified in `TREASURY_POLICY.md`.

## Abuse and sabotage checks

| Risk | Required guardrail |
| --- | --- |
| Fake funding | Production funding must use `/wallet/submit-signed`; direct-credit seed variables are rejected. |
| Pool drain | Claim endpoint checks signed registration, one-time referred wallet, pool balance, and activity nonce. |
| Self-referral | Reject `referrer == referred`. |
| Sybil installs | Require minimum node age/activity before eligibility. |
| Referral-code guessing | Do not pay only from short `refCode`; bind to signed referred wallet. |
| Replay | Use account nonces and idempotent claim IDs. |
| Endpoint spoofing | Hive must prefer QSDM Core HTTPS/gateway status and show unavailable status on failures. |
| Treasury compromise | Use a dedicated low-balance reward wallet, not the main treasury. |
| Operator sabotage | Payouts should be reproducible from public referral ledger and receipts. |
| UI overpromise | Hive should distinguish `funded` from `claimable`. |

## Funding command

Use the guarded script from the repository root:

```powershell
.\QSDM\scripts\fund_referral_reward_pool.ps1 `
  -ApiBaseUrl "https://api.qsdm.tech" `
  -KeystorePath "C:\path\to\funding-wallet.json" `
  -PassphraseFile "C:\path\to\passphrase.txt" `
  -AmountCell 100 `
  -Submit
```

Run without `-Submit` first to preview the source wallet, destination pool,
amount, fee, and current balance.
