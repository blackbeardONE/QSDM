# QSDM Network

**Status:** Live public network

**Deployed Core candidate:** `v0.4.7-rc.4` (`f362829`)
**Public gateway:** `https://api.qsdm.tech/api/v1`

QSDM Network provides public access to the CELL ledger. It lets QSDM Hive,
supported websites, and integrations read accepted CELL balances and chain
state, then submit signed actions without requiring every user to run a local
validator.

QSDM Network is **not** a custodial wallet and is not a second desktop client.
QSDM Hive remains the consumer application and keeps wallet secrets and signing
on the user's device.

## Public services

| Surface | Public address | Purpose |
|---|---|---|
| Network status | `https://api.qsdm.tech/api/v1/status` | Current height, revision, peers, and service readiness |
| Explorer | `https://qsdm.tech/explorer.html` | Blocks, transactions, accounts, and search |
| Chain board | `https://qsdm.tech/chain.html` | Continuously refreshed chain and validator status |
| HTTP API | `https://qsdm.tech/api.html` | Stable `/api/v1/*` API for integrations |
| Trust feeds | `https://qsdm.tech/trust.html` | Public attestations and scope notes |
| Audit | `https://qsdm.tech/audit.html` | Public checklist and machine-readable audit evidence |

## User workflow

1. Install QSDM Hive for Windows or Linux.
2. Create or import a QSDM keystore wallet in Hive.
3. Hive selects the production QSDM Network gateway when a healthy local Core is
   not available.
4. Public reads go to the gateway. Wallet transfers, task actions, staking, and
   supported website approvals are signed locally before submission.
5. Validators verify sender binding, ML-DSA signatures, nonces, balances,
   staking rules, and consensus state.

The QSDM Hive browser extension is a small bridge to the running Hive wallet.
It does not store the keystore JSON or passphrase. A supported website receives
the public wallet address and only signatures the user explicitly approves.

## What stays private

**Stays on your device:**

- QSDM keystore JSON;
- wallet passphrase;
- private signing operations;
- Hive approval state; and
- Mother Hive relay credentials and local compute-gateway tokens.

**Is recorded publicly:**

- wallet address and account state;
- signed transaction or task envelope;
- blocks, receipts, stake, rewards, and other consensus records; and
- validator, mining, audit, and attestation data intended for transparency.

QSDM Network must never request a wallet passphrase or raw keystore. A website
that asks for either is outside the supported QSDM wallet-link workflow.

## Local Core and QSDM Network

Hive can use a healthy local Core for an operator-controlled workflow. When no
local Core is present, it can use the production gateway. Both must report the
same chain identity and state. Switching endpoints must not turn a timeout into
a zero balance or send an action to a different network.

Validator operators should continue to use the validator and home-gateway
runbooks. QSDM Network does not replace validator operation, peer synchronization,
or local backup procedures.

## Availability behavior

Public reads can be retried and cached briefly. If Core cannot confirm the
active network or account nonce, Hive does not send the signed action. Hive
keeps the last confirmed balance and height, marks them as stale, and never
shows a temporary timeout as a confirmed zero.

The live network page at `https://qsdm.tech/network.html` reports current network
status directly from the production status endpoint.

## Related pages

- [QSDM Hive](QSDM_HIVE.md)
- [QSDM VPN](QSDM_VPN.md)
- [API reference](API_REFERENCE.md)
- [Web wallet](WEB_WALLET.md)
- [Validator quickstart](VALIDATOR_QUICKSTART.md)
- [Home gateway](HOME_GATEWAY.md)
- [Security audit](SECURITY_AUDIT.md)
