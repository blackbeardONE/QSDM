# Home Standby and VPS Independence

This runbook describes a home Windows machine that can continue QSDM
operations when the reference VPS is unavailable. It does not promise
automatic failover: the current validator has no quorum-based leader election
or split-brain lease.

## What is already prepared

The local machine can run as a networked follower with:

- a persistent libp2p host key at `source/.cache/local-validator/run-networked/qsdm_network_host.key`;
- public P2P binding on TCP `4001` when `publicP2P` is true;
- a local API bound to loopback; and
- a watchdog that keeps the selected validator binary and role profile alive.

The local API and P2P listener are different things. Keeping the API on
`127.0.0.1` protects wallet and administration endpoints while TCP `4001`
serves peer traffic.

## Requirements for real independence

A machine is not VPS-independent until all of these are true:

1. It has at least one trusted bootstrap peer that is not hosted by the VPS.
2. It has at least one trusted HTTPS chain-sync API that is not hosted by the
   VPS.
3. It has a live peer connection and has caught up to the trusted chain tip.
4. Its TCP `4001` listener is reachable from the other validator. A LAN
   address is not enough; configure router forwarding or use a trusted relay.
5. Hive, miners, and websites have an alternate API endpoint. A local standby
   alone cannot make a single public URL redundant.

The verifier compares bootstrap and sync hosts with every configured public
reference host: the Core API, gateway relay, reference bootstrap peer, and
release SSH target. This prevents a hostname-only VPS migration from being
mistaken for an independent source. It is a configuration-level check, not a
proof that two different hostnames are operated by different organizations.

If every source resolves to the configured public infrastructure, the machine
is a standby for that infrastructure, not an independent source.

## Prepare the follower

Run the existing launcher from an elevated PowerShell after obtaining the
alternate peer address and API URL:

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\start_local_validator.ps1 `
  -Networked -PublicP2P -Restart `
  -BootstrapPeers "/dns4/peer-a.example/tcp/4001/p2p/<peer-id>,/dns4/api.qsdm.tech/tcp/4001/p2p/<reference-peer-id>" `
  -ChainSyncUrls "https://peer-a.example/api/v1,https://api.qsdm.tech/api/v1"
```

Do not add `-BlockProducer` during preparation. Let the follower restore its
state, connect to a non-VPS peer, and catch up first.

Run the read-only verifier:

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\validate_vps_independence.ps1
```

It must report `independence_ready: true`. A healthy local API with
`peers: 0` is not sufficient.

## Network access

Allow only the peer port on Windows, preferably on the Private profile:

```powershell
Set-Service MpsSvc -StartupType Automatic
Start-Service MpsSvc
New-NetFirewallRule -DisplayName "QSDM Validator P2P TCP 4001" `
  -Direction Inbound -Action Allow -Protocol TCP -LocalPort 4001 -Profile Private
```

Then forward TCP `4001` on the router to this machine's stable LAN address.
Do not forward `8080`, `8081`, or wallet administration ports. If the ISP uses
CGNAT, router forwarding will not provide inbound access; use a trusted relay
or a public validator host instead.

## CGNAT fallback

When CGNAT is controlled by the ISP, treat raw inbound P2P as best-effort and
use the outbound home-gateway relay as the public surface. This keeps the local
validator API on loopback and publishes only the narrow gateway routes through
an outbound tunnel.

Activate the fallback profile from PowerShell:

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\enable_cgnat_fallback.ps1 `
  -Relay "https://api.qsdm.tech" `
  -Slot "home-validator"
```

If the router/ISP later provides real inbound TCP, add `-PublicP2P` to the same
command and retest from an outside network. Do not expose `8080`, `8081`, Hive
wallet ports, or dashboard administration ports.

Verify fallback readiness:

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\validate_vps_independence.ps1 `
  -AcceptFallback
```

A CGNAT fallback result should report `cgnat_fallback_ready: true` and
`operational_posture: relay-fallback`. That is useful for public status, Hive,
and miner-facing gateway routes, but it is not the same as
`independence_ready: true`. Full independence still requires a non-VPS peer and
a non-VPS chain-sync source.

## Manual failover

Failover is an incident procedure, not a background toggle:

1. Stop and fence the current VPS producer. Confirm it cannot produce blocks.
2. Confirm this machine is caught up and connected to at least one non-VPS
   peer.
3. Confirm every validator uses the same consensus activation settings and
   authorizes this machine's consensus signer identity.
4. Start exactly one network producer using `-Networked -BlockProducer`.
5. Verify the chain tip advances and other validators accept the new blocks.
6. Keep the old producer fenced until the handoff is deliberately reversed.

Never use solo mode for failover, never copy the VPS consensus signer, and
never run two network producers against the same chain without an explicit
consensus protocol that supports it.

## Current local result

The local profile can run either as a public-P2P follower or as a CGNAT fallback
follower. In fallback mode, the home gateway is the public surface and raw P2P
reachability is no longer treated as the immediate blocker. It is still not
VPS-independent while its only bootstrap/API source is the reference VPS and it
has zero peers. The next required input for full independence is an alternate
trusted validator peer and API source, not another local restart.