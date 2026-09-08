# Public Endpoint Migration

This runbook moves QSDM public infrastructure from one VPS to another without
editing source files just to change hostnames.

Current production defaults remain:

- public site: `https://qsdm.tech`
- public API: `https://api.qsdm.tech`
- public SSH target for release deploys: `root@node.qsdm.tech`
- home gateway slot: `home-validator`

Those values are defaults, not permanent source-code assumptions. The shared
operator config is `QSDM/config/public-endpoints.json`, and every local script
that needs the public API, relay, bootstrap peer, or release SSH host now reads
from that file or from environment variables.

## What To Change First

For a new VPS, prepare the new node while DNS still points at the old one.
Create a temporary endpoints file, for example:

```json
{
  "public_site": "https://qsdm.tech",
  "public_api_base": "https://api.qsdm.tech",
  "core_api_base": "https://api.qsdm.tech/api/v1",
  "home_gateway_relay": "https://api.qsdm.tech",
  "home_gateway_slot": "home-validator",
  "reference_bootstrap_peer": "/dns4/api.qsdm.tech/tcp/4001/p2p/<new-peer-id>",
  "vps_ssh_target": "root@<new-vps-host>"
}
```

Set this only for the terminal doing migration tests:

```powershell
$env:QSDM_ENDPOINTS_FILE = "E:\Projects\QSDM+\QSDM\config\public-endpoints.new-vps.json"
```

If DNS will stay on `api.qsdm.tech`, leave `public_api_base`, `core_api_base`,
and `home_gateway_relay` unchanged. If you are testing a staging hostname first,
point those fields to the staging hostname and switch DNS only after the staging
checks pass.

## Environment Overrides

Use these when you need a one-off override without changing the shared file:

| Variable | Purpose |
|---|---|
| `QSDM_ENDPOINTS_FILE` | Path to a JSON file with endpoint defaults |
| `QSDM_PUBLIC_API_BASE_URL` | Public API root, usually `https://api.qsdm.tech` |
| `QSDM_CHAIN_SYNC_URLS` | Comma-separated `/api/v1` sync endpoints |
| `QSDM_HOME_GATEWAY_RELAY` | Home gateway relay root |
| `QSDM_HOME_GATEWAY_SLOT` | Home gateway slot name |
| `QSDM_REFERENCE_BOOTSTRAP_PEER` | libp2p bootstrap multiaddr |
| `QSDM_RELEASE_SSH_TARGET` | SSH target used by release publication scripts |
| `QSDM_WALLET_API_URL` | Wallet CLI default API root |
| `QSDM_MINER_DEFAULT_VALIDATOR_URL` | Console miner default validator URL |
| `QSDM_TRUSTCHECK_BASE_URL` | Trustcheck default public surface |

Environment values win over `public-endpoints.json`; the JSON file wins over
built-in defaults.

## Required Checks Before DNS Cutover

Run these against the new VPS before sending users to it:

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\validate_vps_independence.ps1
```

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\hive_production_acceptance.ps1 `
  -HiveExePath <path-to-hive-build>
```

```powershell
qsdmcli trustcheck --base https://<new-api-host> --min-attested 2 --check-mining-path
```

The node should report a matching chain identity, advancing height, healthy
`/api/v1/status`, and a working `/api/v1/mining/work` path before the DNS move.

## CGNAT Fallback

A home or office machine behind CGNAT can still publish restricted routes
through the home gateway relay, but it is not a replacement for a public
validator peer. For fallback mode:

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\enable_cgnat_fallback.ps1
```

Then verify with:

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\validate_vps_independence.ps1 -AcceptFallback
```

A good fallback result says `operational_posture: relay-fallback`. Full VPS
independence still requires a reachable non-VPS peer and a non-VPS chain-sync
API.

## Cutover Order

1. Build and smoke-test the new VPS binary.
2. Start the new VPS as a follower and let it catch up.
3. Confirm trust, audit, mining, wallet, and Hive reads all work on the new API.
4. Stop and fence the old producer before promoting another producer.
5. Update DNS for `api.qsdm.tech` and `node.qsdm.tech` only after checks pass.
6. Re-run trustcheck and Hive production acceptance through the public domains.
7. Keep the old VPS data and binary available for rollback until the new node
   has survived at least one release cycle.

Do not expose wallet passphrases, keystore JSON, dashboard secrets, or local
admin ports during the migration. Only the intended public API, HTTPS gateway,
and libp2p peer port should be reachable.