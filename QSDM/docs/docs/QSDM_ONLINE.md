# QSDM Online

**Status:** Live public network

**Deployed Core candidate:** `v0.4.7-rc.4` (`f362829`)
**Public gateway:** `https://api.qsdm.tech/api/v1`

QSDM Online is the hosted public-access surface for the QSDM network. It lets
QSDM Hive, supported websites, and integrations read canonical CELL state and
submit signed actions without requiring every user to operate a local
validator.

QSDM Online is **not** a custodial wallet and is not a second desktop client.
QSDM Hive remains the consumer application and keeps wallet secrets and signing
on the user's device.

## Product surfaces

| Surface | Public address | Purpose |
|---|---|---|
| Network status | `https://api.qsdm.tech/api/v1/status` | Current height, revision, peers, and service readiness |
| Explorer | `https://qsdm.tech/explorer.html` | Blocks, transactions, accounts, and search |
| Chain board | `https://qsdm.tech/chain.html` | Continuously refreshed chain and validator status |
| HTTP API | `https://qsdm.tech/api.html` | Stable `/api/v1/*` integration surface |
| Trust feeds | `https://qsdm.tech/trust.html` | Public attestations and scope notes |
| Audit | `https://qsdm.tech/audit.html` | Public checklist and machine-readable audit evidence |

## User workflow

1. Install QSDM Hive for Windows or Linux.
2. Create or import a QSDM keystore wallet in Hive.
3. Hive selects the canonical QSDM Online gateway when a healthy local Core is
   not available.
4. Public reads go to the gateway. Wallet transfers, task actions, staking, and
   supported website approvals are signed locally before submission.
5. Validators verify sender binding, ML-DSA signatures, nonces, balances,
   staking rules, and consensus state.

The QSDM Hive browser extension is a narrow bridge to the running Hive wallet.
It does not store the keystore JSON or passphrase. A supported website receives
the public wallet address and only signatures the user explicitly approves.

## Custody and security boundary

**Stays local:**

- QSDM keystore JSON;
- wallet passphrase;
- private signing operations;
- Hive approval state; and
- Mother Hive relay credentials and local compute-gateway tokens.

**May be public:**

- wallet address and account state;
- signed transaction or task envelope;
- blocks, receipts, stake, rewards, and other consensus records; and
- validator, mining, audit, and attestation data intended for transparency.

QSDM Online must never request a wallet passphrase or raw keystore. A website
that asks for either is outside the supported QSDM wallet-link workflow.

## Local Core and QSDM Online

Hive can use a healthy local Core for an operator-controlled workflow. When no
local Core is present, it can use the canonical gateway. These paths must agree
on chain identity and state; failover must not convert a timeout into a zero
balance or silently submit an action to a different network.

Validator operators should continue to use the validator and home-gateway
runbooks. QSDM Online does not replace validator operation, peer synchronization,
or local backup procedures.

## Availability behavior

Public reads can be retried and cached briefly. Signed writes fail closed when
Core cannot confirm the active network or account nonce. Hive should preserve
the last confirmed balance and height as stale data instead of displaying a
temporary timeout as a confirmed zero.

The live product page at `https://qsdm.tech/online.html` reports current network
status directly from the canonical status endpoint.

## Related pages

- [QSDM Hive](QSDM_HIVE.md)
- [API reference](API_REFERENCE.md)
- [Web wallet](WEB_WALLET.md)
- [Validator quickstart](VALIDATOR_QUICKSTART.md)
- [Home gateway](HOME_GATEWAY.md)
- [Security audit](SECURITY_AUDIT.md)
