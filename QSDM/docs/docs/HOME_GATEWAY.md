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

Build:

```powershell
cd QSDM\source
go build -o .cache\local-validator\qsdm-home-gateway.exe .\cmd\qsdm-home-gateway
```

Generate a slot key:

```powershell
.\.cache\local-validator\qsdm-home-gateway.exe --generate-key
```

Run after the relay slot is configured:

```powershell
.\scripts\start_home_gateway.ps1 -Relay https://relay.example -Slot your-slot-id
```

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
- Keep the relay as a dumb forwarder. Consensus authority remains in the
  validator and mining proof verification remains on the validator.
