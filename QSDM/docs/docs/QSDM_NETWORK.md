# QSDM Network

**Status:** Live public network

**Latest Core candidate:** `v0.4.7-rc.9` (`ee1d5e2`)
**Public gateway:** `https://api.qsdm.tech/api/v1`

The latest downloadable candidate and a running validator can differ during a
staged update. The public status endpoint reports the version and revision that
are actually running.

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

## VPS-independent operation

QSDM Hive and validators can keep local state when the public reference server
is unavailable, but independence is not automatic failover. A follower needs a
second trusted validator for both peer bootstrap and chain catch-up. Clients
need a second API endpoint as well; a single public URL cannot be made
redundant by desktop configuration alone.

A prepared standby should:

- use `networked` follower mode with a persistent network host key;
- listen on TCP `4001` only when its operator has a real inbound path;
- configure at least one non-VPS `bootstrap_peers` multiaddr and one non-VPS
  HTTPS `QSDM_CHAIN_SYNC_URLS` source;
- retain its own consensus signer and SQLite state; and
- remain a follower until it is caught up and the current producer is fenced.

QSDM currently has no automatic proposer election or split-brain lease. A
manual producer handoff is required, and two simultaneous producers can create
conflicting histories. See [`runbooks/HOME_STANDBY.md`](runbooks/HOME_STANDBY.md)
for the operator checklist.

### CGNAT fallback

If a home or office ISP blocks inbound TCP with carrier-grade NAT, the supported
fallback is the QSDM home gateway. The local validator remains a networked
follower, keeps its Core API on `127.0.0.1`, and publishes only the restricted
status/mining/Hive routes through an outbound relay. This is enough to keep a
home node usable behind CGNAT, but it does not replace a second reachable peer
for validator-to-validator redundancy.

Use `scripts/enable_cgnat_fallback.ps1` on Windows to record the fallback
profile and restart the read-only gateway tunnel. Use
`scripts/validate_vps_independence.ps1 -AcceptFallback` to distinguish
`operational_posture: relay-fallback` from true `independence_ready`.
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
