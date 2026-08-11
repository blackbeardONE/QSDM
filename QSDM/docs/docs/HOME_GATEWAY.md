# QSDM Home Gateway

`qsdm-home-gateway` lets a home validator publish a narrow public mining/status
surface without exposing the local computer, dashboard, wallet, or admin API.

## Shape

```
miners / validators -> public qsdm-relay -> outbound tunnel -> qsdm-home-gateway -> 127.0.0.1:8080
```

The home machine only dials outbound to the relay. No router port-forward is
required. The validator can stay bound to `127.0.0.1`.

## Default Public Allowlist

Allowed by default:

- `GET /api/v1/status`
- `GET /api/v1/health/live`
- `GET /api/v1/health/ready`
- `GET /api/v1/mining/work`
- `GET /api/v1/mining/challenge`
- `POST /api/v1/mining/submit`
- `GET /api/v1/mining/enrollment/<node_id>`
- `GET /api/v1/mining/emission`
- `GET /api/v1/mining/blocks`

Blocked by default:

- dashboard root and dashboard APIs
- `/api/admin/*`
- `/api/v1/wallet/*`
- contracts, bridge, governance mutation routes
- enrollment mutation routes unless `--allow-enrollment` is passed

## Home Side

Download the gateway for your platform from the matching
[QSDM GitHub release](https://github.com/blackbeardONE/QSDM/releases):

```text
qsdm-home-gateway-linux-amd64
qsdm-home-gateway-linux-arm64
qsdm-home-gateway-windows-amd64.exe
```

Verify the file against the release-level `SHA256SUMS` and its Sigstore
certificate before running it. The gateway `--version` output identifies the
exact release tag and source commit.

Building from source remains available for operators:

```powershell
cd QSDM\source
go build -o .cache\local-validator\qsdm-home-gateway.exe .\cmd\qsdm-home-gateway
```

Generate a slot key directly into the durable key file:

```powershell
$keyDir = Join-Path $HOME ".qsdm"
New-Item -ItemType Directory -Force $keyDir | Out-Null
.\.cache\local-validator\qsdm-home-gateway.exe --generate-key |
    Set-Content -NoNewline (Join-Path $keyDir "home-gateway.key")
```

Run after the relay slot is configured:

```powershell
.\scripts\start_home_gateway.ps1 -Relay https://relay.example -Slot your-slot-id
```

When `validator-mode.json` identifies the local Core as a network follower,
the launcher forces the gateway into read-only mode. Status, chain, task, and
mining-work reads remain available, but proof submissions, signed wallet/task
actions, enrollment, and every other mutation must go directly to the
authoritative Core. `-AllowEnrollment` is rejected in follower mode.

The launcher restricts the key file to the current user, the operating system,
and local administrators. It passes only the file path to the gateway through
`QSDM_HOME_GATEWAY_KEY_FILE`; the key itself is not placed in process
arguments. Direct launches should use `--key-file <path>`.

`--key-hex` and `QSDM_HOME_GATEWAY_KEY_HEX` remain temporarily available for
older deployments, but they are deprecated because inline secrets can appear
in process inspection tools.

## Disk Resilience

Validators reserve 2 GiB of free space for ledger persistence by default. Block
sealing and peer block admission pause before that reserve is crossed, then
resume automatically after space is restored. Override the threshold only when
the state volume has been sized deliberately:

```text
QSDM_MIN_PERSISTENCE_FREE_BYTES=2147483648
```

The value must be an unsigned byte count of at least 256 MiB. A persistence
write error still fails closed until restart; startup can repair only one fully
appended journal block when the saved state root proves that the block was not
committed.

On Windows home-validator installations, `watch_local_stack.ps1` runs
`maintain_generated_cache.ps1` every 30 minutes. It retains the newest builds
and may remove only old entries directly under `.cache/release` and
`.cache/releases`. It never removes validator state, recovery archives,
wallets, signing material, or `.cache/private`.

## Relay Side

Add a slot to the relay allowlist:

```toml
[[slot]]
slot_id = "your-slot-id"
key_hex = "<the 64 hex chars generated on the home machine>"
note = "home validator gateway"
```

Then run `qsdm-relay` as described in `TUNNEL_QUICKSTART.md`.

## Security Rules

- Do not expose `8080`, `8081`, or `4001` directly from a home router.
- Keep the relay slot key private; rotate it if it is pasted into chat or logs.
- Store the relay slot key in a protected file (`0600` on Linux). Do not pass
  the key value through command-line arguments.
- Keep the relay as a dumb forwarder. Consensus authority remains in the
  validator and mining proof verification remains on the validator.
