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

When installing the public website on the staging host, point its release
alignment probe at that host instead of the existing public API:

```bash
QSDM_PUBLIC_API_BASE_URL="https://api.staging.example" \
  bash /tmp/_install_docs_site.sh
```

The installer accepts either the public API root or a value ending in `/api/v1`.
It requires HTTPS and retains `https://api.qsdm.tech` as the default when no
override is supplied.

## Environment Overrides

Use these when you need a one-off override without changing the shared file:

| Variable | Purpose |
|---|---|
| `QSDM_ENDPOINTS_FILE` | Path to a JSON file with endpoint defaults |
| `QSDM_VPS_HOST` | One-off SSH host override for legacy Paramiko deployment helpers |
| `QSDM_VPS_USER` | One-off SSH user override for legacy Paramiko deployment helpers |
| `QSDM_VPS_PORT` | One-off SSH port override for legacy Paramiko deployment helpers |
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

The legacy Paramiko deployment helpers use the same SSH target resolution as
the release publisher: `QSDM_VPS_HOST`, then `QSDM_RELEASE_SSH_TARGET`, then
`vps_ssh_target`. They also accept `QSDM_VPS_USER` and `QSDM_VPS_PORT` for a
one-off target. The installer, service, Caddy, hardening, and verification
helpers write root-owned paths and intentionally require `root` today; do not
point them at an unreviewed sudo account and assume it is equivalent.

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
trustcheck --base https://<new-api-host> --min-attested 0 --check-mining-path
```

The node should report a matching chain identity, advancing height, healthy
`/api/v1/status`, and a working `/api/v1/mining/work` path before the DNS move.

`trustcheck` is a separate released binary, not a `qsdmcli` subcommand. The
zero attestation floor above is intentional during initial staging: it proves
the public read and mining paths without pretending that independent attesters
already exist. After cutover, restore the production floor appropriate to the
network (currently two):

```powershell
trustcheck --base https://api.qsdm.tech --min-attested 2 --check-mining-path
```

Run the public checks from at least two distinct networks: one may be the new
VPS, but the other must be outside that VPS provider. A successful loopback,
LAN, or same-provider request cannot prove that public DNS, TLS, and firewall
rules work for users.

## Public Edge Verification

Before any DNS change, use a temporary HTTPS hostname on the new VPS and
confirm all of the following without mutating chain state:

```powershell
curl.exe --fail --silent --show-error https://<new-api-host>/api/v1/status
trustcheck --base https://<new-api-host> --min-attested 0 --check-mining-path
```

Then repeat those checks from an independent network. The scheduled GitHub
Actions **Trust transparency external probe** is an additional independent
observer after public DNS points to the new host. Treat a timeout there as a
public-edge incident, not as a harmless CI failure.

The independence verifier derives its public reference-host set from all four
endpoint fields above. Keep `core_api_base`, `home_gateway_relay`,
`reference_bootstrap_peer`, and `vps_ssh_target` aligned in the temporary file
before using its result. A new hostname alone is not evidence of independent
infrastructure; add a separately operated peer and sync API for that.

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

1. Record the current DNS records, chain tip, binary hash, and a rollback
   contact for the old provider.
2. Build and smoke-test the new VPS binary.
3. Start the new VPS as a follower and let it catch up from a trusted source.
4. Confirm trust, audit, mining, wallet, and Hive reads through a temporary
   HTTPS hostname from two independent networks.
5. Stop and fence the old producer before promoting another producer. If the
   old host is unreachable, use the provider control plane to power it off or
   block its network access; a failed SSH connection is not proof that it has
   stopped producing blocks.
6. Update DNS for `api.qsdm.tech` and `node.qsdm.tech` only after all checks
   pass.
7. Re-run trustcheck and Hive production acceptance through the public domains,
   then confirm the next scheduled external trustcheck succeeds.
8. Keep the old VPS data and binary available for rollback until the new node
   has survived at least one release cycle.

Do not expose wallet passphrases, keystore JSON, dashboard secrets, or local
admin ports during the migration. Only the intended public API, HTTPS gateway,
and libp2p peer port should be reachable.