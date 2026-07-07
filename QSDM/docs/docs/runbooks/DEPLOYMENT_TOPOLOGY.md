# Deployment Topology — Validator + Home Public Challenge Issuer

This runbook documents the **physical topology** behind
`api.qsdm.tech` after 2026-05-16, and the operator
procedure for bringing up a Public Challenge Issuer (PCI)
from a residential / NAT-bound machine.

It is the companion to
[`source/docs/docs/TUNNEL_QUICKSTART.md`](../../../source/docs/docs/TUNNEL_QUICKSTART.md),
which describes the wire protocol itself
(HTTP/1.1 Upgrade → yamux multiplexing → per-slot
allowlist). This runbook covers only the **deployed
posture and operator-visible steps**, not the protocol.

---

## 1. What runs where

```text
[ home Windows 10 box, 3050         ]                       [ BLR1 VPS                       ]                          [ any miner on the internet ]
  qsdm-attester  ──── outbound TLS ────►   qsdm-relay ◄─── HTTPS ──── Caddy ◄─── HTTPS ────  qsdmminer / curl
  (yamux server)     HTTP/1.1 Upgrade       (yamux client)
  signer-key 32 B    /_tunnel/connect       slot table:
  HMAC-SHA256                                "blackbeard-3050" → live session
                                            tunnel-listen :7700  (handshake)
                                            proxy-listen   :7710 (miner traffic)

  miner GET https://api.qsdm.tech/attest/blackbeard-3050/api/v1/challenge
         └► Caddy /attest/*  (handle_path strips prefix)  → relay :7710
                └► open new yamux stream to slot's session
                       └► tunnel back to home → attester http.Server →
                          mints nonce, signs with 3050's HMAC key, returns
```

Three independent failure domains: the BLR1 validator
(`qsdm.service` on :8443 + Caddy on :443), the BLR1
relay (`qsdm-relay.service` on :7700/:7710/:7720),
and the home attester (`qsdm-attester` on `:7733`). Any
one of these can be restarted or rebuilt without
touching the others. Loss of the home attester returns
**502 Bad Gateway** on `/attest/blackbeard-3050/*`
(the validator and the rest of the public API are
unaffected); the relay reconnects automatically when
the home attester comes back.

---

## 2. Caddy routes that wire the topology

Two `handle` blocks inside the `api.qsdm.tech, node.qsdm.tech`
site block (see [`QSDM/deploy/Caddyfile`](../../../deploy/Caddyfile)):

```caddy
# Tunnel handshake (attester -> relay) inbound on the tunnel-listen port :7700.
# Path is /_tunnel/connect with HTTP/1.1 Upgrade to qsdm-tunnel/1. Caddy passes
# the upgrade through transparently; yamux takes over on the hijacked conn.
handle /_tunnel/connect {
    reverse_proxy 127.0.0.1:7700 {
        header_up X-Forwarded-Proto https
        header_up X-Real-IP {remote_host}
    }
}

# Reverse-tunnel ingress (Public Challenge Issuer surface):
# /attest/<slot>/<path>  ->  qsdm-relay :7710  ->  yamux session  ->  home attester
# handle_path strips the /attest prefix so the relay sees /<slot>/<path>.
handle_path /attest/* {
    reverse_proxy 127.0.0.1:7710 {
        header_up X-Forwarded-Proto https
        header_up X-Real-IP {remote_host}
    }
}
```

**Both** are required. Without `/_tunnel/connect` the
attester cannot establish a session at all (the
handshake falls through to the main-API catch-all and
returns the misleading auth-required 401:
`{"error":"Unauthorized","message":"missing authorization header","status":401}` —
this is the main-API auth middleware, not the relay).
Without `handle_path /attest/*` miner traffic also
falls through to the main API and 401s. The relay's
own no-session response is **502**, which is the
correct signal to operators that the slot is registered
but the home tunnel is offline.

Caddy on BLR1 has `admin off` in the global block, so
**reload via the admin API does not work** — use
`systemctl restart caddy` instead of `caddy reload`.
Restart is a sub-second handover; existing TLS sessions
drain gracefully.

---

## 3. Bring up a home attester (this section is exact for `blackbeard-3050`)

### 3.1 Pre-requisites

- `qsdm-attester.exe` in `~/.qsdm/` (built from
  `source/cmd/qsdm-attester`).
- `~/.qsdm/attester.key` — 32 bytes of `crypto/rand`.
  Auto-generated on first launch if missing. The
  **fingerprint** (first 8 bytes hex of `SHA256(key)`)
  must match the `key_hex` recorded for the slot on
  BLR1 at `/opt/qsdm/relay_slots.toml`.
- A pre-allocated slot row on BLR1. For this machine
  the slot is `blackbeard-3050` and the recorded
  fingerprint is `d24618f8ea91c8f0`.

### 3.2 Launcher script (`~/.qsdm/launch-attester.ps1`)

The launcher exists to (a) survive PowerShell's
`NativeCommandError` trap that fires on the attester's
first stderr line under `$ErrorActionPreference = 'Stop'`,
and (b) keep the command line under the 261-character
limit imposed by `schtasks.exe /TR`. It uses
`Start-Process -RedirectStandardError` to redirect at
the Windows API level (bypassing the PS stream
machinery) and blocks on the child process so
Task-Scheduler / shortcut runtime tracks the actual
attester lifetime:

```powershell
$ErrorActionPreference = 'Continue'
$exe       = 'C:\Users\Windows 10\.qsdm\qsdm-attester.exe'
$stderrLog = 'C:\Users\Windows 10\.qsdm\attester-stderr.log'
$stdoutLog = 'C:\Users\Windows 10\.qsdm\attester-stdout.log'

$proc = Start-Process `
    -FilePath $exe `
    -ArgumentList @(
        '-listen', '127.0.0.1:7733',
        '-relay', 'https://api.qsdm.tech',
        '-slot',  'blackbeard-3050',
        '-note',  'blackbeard-3050-home'
    ) `
    -RedirectStandardError  $stderrLog `
    -RedirectStandardOutput $stdoutLog `
    -WindowStyle Hidden -PassThru
$proc.WaitForExit()
exit $proc.ExitCode
```

The argument array passes paths-with-spaces correctly
via Start-Process (which does its own quoting) — note
that we **omit** `-key` to fall back on the binary's
default `~/.qsdm/attester.key`, dodging the
`Start-Process -ArgumentList` space-splitting bug.
The launcher explicitly binds `127.0.0.1:7733`. Public access is provided by
the authenticated outbound relay tunnel; binding `:7733` would unnecessarily
expose the challenge issuer on every local network interface.

### 3.3 Auto-start mechanism

The launcher fires at user logon via a Startup-folder
shortcut at:

```
%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\QSDM Attester.lnk
```

with `TargetPath` = `powershell.exe` and `Arguments` =
`-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "C:\Users\Windows 10\.qsdm\launch-attester.ps1"`
(the embedded double-quotes are required and are
respected by `WScript.Shell.CreateShortcut`).

The shortcut method is used because the standard-user
account `DESKTOP-ULJA2MM\Windows 10` lacks admin
privileges to register a Scheduled Task on this host
(both `Register-ScheduledTask` and `schtasks.exe /Create`
return Access Denied without elevation, regardless of
LogonType). If admin rights become available, the
preferred form is:

```powershell
$action = New-ScheduledTaskAction `
  -Execute 'powershell.exe' `
  -Argument '-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "C:\Users\Windows 10\.qsdm\launch-attester.ps1"'
$trigger = New-ScheduledTaskTrigger -AtStartup
$trigger.Delay = 'PT30S'
$principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
$settings = New-ScheduledTaskSettingsSet `
  -RestartInterval (New-TimeSpan -Minutes 1) `
  -RestartCount 999 `
  -ExecutionTimeLimit (New-TimeSpan -Hours 0) `
  -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
  -StartWhenAvailable -MultipleInstances IgnoreNew
Register-ScheduledTask -TaskName 'QSDMAttester' `
  -Action $action -Trigger $trigger -Principal $principal -Settings $settings
```

Mirrors how `QSDMMiner` is registered (`SERVICE_START_NAME : LocalSystem`).

### 3.4 Start it now

If the launcher is not already running:

```powershell
Start-Process `
  -FilePath 'C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe' `
  -ArgumentList '-NoProfile -ExecutionPolicy Bypass -File "C:\Users\Windows 10\.qsdm\launch-attester.ps1"' `
  -WindowStyle Hidden
```

Note the **single-string** `-ArgumentList` form: the
array form re-tokenizes elements on whitespace
boundaries and strips embedded quotes, splitting
`C:\Users\Windows 10\.qsdm\launch-attester.ps1` into
two args (`C:\Users\Windows` and `10\.qsdm\launch-attester.ps1`)
which makes powershell.exe fail with "the file does
not have a '.ps1' extension". The single-string form
preserves the embedded quotes.

---

## 4. Verification — three probes that must all return 200

```powershell
foreach ($path in @('/healthz','/info','/api/v1/challenge')) {
  $r = Invoke-WebRequest -UseBasicParsing -Uri "https://api.qsdm.tech/attest/blackbeard-3050$path" -TimeoutSec 10
  '{0,-22} {1}  {2}' -f $path, $r.StatusCode, $r.Content
}
```

Expected output (timestamps and nonces will differ):

```
/healthz               200  {"status":"ok"}
/info                  200  {"signer_id":"attester-12a0d1aa082b7e28","key_fingerprint":"d24618f8ea91c8f0","note":"blackbeard-3050-home","version":"dev","uptime_seconds":N,"issued_total":K,"telemetry_enabled":true,"telemetry_gpus":1,"telemetry_ticks":M}
/api/v1/challenge      200  {"nonce":"<32B hex>","issued_at":<unix>,"signer_id":"attester-12a0d1aa082b7e28","signature":"<32B hex>"}
```

The `signer_id` and `key_fingerprint` must match the
slot's recorded fingerprint on BLR1
(`d24618f8ea91c8f0` for `blackbeard-3050`). Any
mismatch means a different key is signing for the same
slot — treat as a key-substitution incident: rotate
both the local `attester.key` and the slot's recorded
fingerprint, then drop and re-add the slot row.

---

## 5. Operator quick reference

| Goal                                  | Command (run on the home box, PowerShell)                                                                                     |
|---------------------------------------|--------------------------------------------------------------------------------------------------------------------------------|
| Stop the attester                     | `Get-Process qsdm-attester \| Stop-Process -Force`                                                                             |
| Restart the attester                  | Stop as above, then re-run the launcher (§3.4)                                                                                 |
| Tail the attester log                 | `Get-Content -Wait 'C:\Users\Windows 10\.qsdm\attester-stderr.log'`                                                            |
| Check the tunnel session is up        | `Get-Process qsdm-attester ; Invoke-WebRequest -UseBasicParsing https://api.qsdm.tech/attest/blackbeard-3050/healthz`          |
| Rotate the signer key                 | Stop attester, `Remove-Item ~/.qsdm/attester.key`, restart (key regenerates), copy new fingerprint to BLR1 `relay_slots.toml`  |

| Goal                                  | Command (run on BLR1, root)                                                                                                    |
|---------------------------------------|--------------------------------------------------------------------------------------------------------------------------------|
| Restart the relay                     | `systemctl restart qsdm-relay`                                                                                                 |
| Tail the relay log                    | `journalctl -u qsdm-relay -f`                                                                                                  |
| Reload Caddy routes                   | `systemctl restart caddy` (admin API is `admin off`, so `caddy reload` does not work)                                          |
| Inspect slot allowlist                | `cat /opt/qsdm/relay_slots.toml`                                                                                               |
| Relay metrics                         | `curl -s http://127.0.0.1:7720/metrics`                                                                                        |

---

## 6. Failure modes

| Symptom on `https://api.qsdm.tech/attest/<slot>/...` | Most likely cause                                                                                                                      | Recovery                                                                                          |
|-----|-----|-----|
| **502 Bad Gateway**                                 | Caddy → relay OK, but no live tunnel session for the slot. Home attester is offline, crashed, or its `attester.key` mismatches the slot.| Verify `Get-Process qsdm-attester` on the home box; restart launcher; check fingerprint match.    |
| **401 Unauthorized** with `"missing authorization header"` body | Caddy is routing `/attest/*` (or `/_tunnel/connect`) to the **main API** instead of the relay. Caddyfile is missing one of §2's handle blocks. | Re-apply §2, then `systemctl restart caddy`. Validate with `caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile`. |
| **502** persists after attester restart             | `attester.key` length is wrong (key file is corrupt, was overwritten, or path got space-split on a launcher bug). Attester boot fails with "expected 32" and exits before the tunnel handshake. | Inspect `~/.qsdm/attester-stderr.log`; if file is wrong size, `Remove-Item ~/.qsdm/attester.key` and restart — a new key auto-generates, then update the slot's `key_hex` on BLR1. |
| Tunnel-client log shows `relay rejected upgrade: status=401` | `/_tunnel/connect` is missing from Caddy or the relay's tunnel-listen port disagrees with what Caddy reverse-proxies to.                  | Confirm `--tunnel-listen 127.0.0.1:7700` in `qsdm-relay.service`'s `ExecStart`, and the Caddy `handle /_tunnel/connect` block points at `127.0.0.1:7700`. |
| Tunnel-client log shows `slot not allowed`           | Slot row is missing from `relay_slots.toml`, OR the slot is registered but its `key_hex` doesn't match the local key's fingerprint.       | Update the row on BLR1 (verify fingerprint with `openssl dgst -sha256 < ~/.qsdm/attester.key`), then `systemctl restart qsdm-relay`. |
| Public probe times out at the TLS layer             | Caddy is down on BLR1.                                                                                                                  | `systemctl status caddy` on BLR1; if it has failed reload, `systemctl restart caddy`.             |

---

## 7. The four trust layers (and why all four matter)

The home Windows 10 box hosts an RTX 3050 GPU and is
**also** an active QSDM miner (config:
`C:\Users\Windows 10\.qsdm\miner.toml`, service:
`QSDMMiner`). The same hardware simultaneously serves
the validator across four independent trust layers,
each wired through the reverse tunnel:

| Layer | Validator-side wiring                                      | Home-side endpoint served via tunnel                                              | What this defends against                                                                                       |
|-------|------------------------------------------------------------|-----------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------|
| 1     | `QSDM_PEER_SIGNERS_FILE=/opt/qsdm/peer_signers.toml`       | `/api/v1/challenge` issues HMAC-SHA256 signed v2 challenges                       | Single-issuer collusion. A miner who suspects the validator's self-signer can cross-check against the home PCI. |
| 2     | `QSDM_PEER_ATTESTER_KEYS_FILE=/etc/qsdm/peer-attester-keys.txt` (strict, max-age 1h, skew 5m) | (key pinning is verification-side only; uses the same key as Layer 1)             | MITM on the relay path. A forged or replayed reference profile is rejected at the validator before catalog apply. |
| 3     | `QSDM_PEER_ATTESTER_URLS=https://api.qsdm.tech/attest/blackbeard-3050/api/v1/telemetry/reference` + `QSDM_PEER_ATTESTER_REFRESH=5m` | `/api/v1/telemetry/reference` serves a signed NVIDIA SKU profile (from `nvidia-smi`) | A spoofer claiming hardware that isn't in any allowlisted attester's profile (e.g. forged compute-capability). |
| 4     | (operator boot log: `spec-check: peer profile applied`)    | (Layer 3's profile flows directly into the validator's catalog)                   | Closes the loop: every 5 minutes the validator pulls + verifies + applies a fresh GPU catalog from the home box. |

**All four layers were configured in advance but only
went live on 2026-05-16 when the home attester was
brought up and Caddy's two `handle` blocks (§2) were
added.** Before that, Layer 3's polling was returning
the misleading
`{"error":"Unauthorized","message":"missing authorization header"}`
from the main API auth middleware (because Caddy was
routing `/attest/*` to `:8443` instead of `:7710`),
which the validator was silently treating as a fetch
failure and falling back on its own attester for the
catalog. Now the home box is consensus-relevant: its
GPU profile and HMAC signer are both being consumed.

**Verification commands (operator on BLR1)** —

```bash
# Layer 1: v2 peer signer registered.
journalctl -u qsdm --since '5 minutes ago' -q | grep 'v2 peer-signers loaded'
# expect: "registered":1

# Layer 2: telemetry-oracle key pinning active.
journalctl -u qsdm --since '5 minutes ago' -q | grep 'peer-attester key pinning ACTIVE'
# expect: "pins":1,"signer_ids":"attester-12a0d1aa082b7e28","strict_mode":true

# Layer 3 + 4: profile fetched, signature verified, catalog applied.
journalctl -u qsdm --since '5 minutes ago' -q | grep 'spec-check: peer profile applied'
# expect: "signature_verified":true,"signer_id":"attester-12a0d1aa082b7e28","gpu_entries":1

# Layer 3 endpoint directly (sanity check the bytes the validator sees).
curl -s https://api.qsdm.tech/attest/blackbeard-3050/api/v1/telemetry/reference | jq .gpus[0].name
# expect: "NVIDIA GeForce RTX 3050"
```

The tunnel architecture exists because the home box is
NAT-bound (no inbound 443) but the network's trust
posture requires publicly addressable peer attesters.
The attester dials **outbound** to the relay over TLS,
holds a persistent yamux session, and miner / validator
fetches are reverse-routed through that session by
Caddy → relay → tunnel. From the validator's
perspective the home GPU is at `api.qsdm.tech`; from
the home GPU's perspective it has only ever made one
outbound TLS connection. See
[`source/docs/docs/TUNNEL_QUICKSTART.md`](../../../source/docs/docs/TUNNEL_QUICKSTART.md)
for the wire protocol detail.

---

## 8. Alerts

The validator's Prometheus instance scrapes every peer
attester's `/metrics` endpoint over the public tunnel
(scrape job `qsdm-peer-attester-<slot>` in
[`prometheus.qsdm.example.yml`](../../../deploy/prometheus/prometheus.qsdm.example.yml)).
Scraping the **public** HTTPS path rather than
`127.0.0.1:7710` directly is deliberate: every successful
scrape proves the full Caddy → relay → yamux → home
attester chain is alive, so a single `up=0` signal
catches all five failure modes in §6 at once.

### Mode A — `QSDMPeerAttesterAbsent`

**Trigger.** `up{job=~"qsdm-peer-attester-.+"} == 0`
sustained for ≥5m (one full validator
`QSDM_PEER_ATTESTER_REFRESH` interval; we want the
alert to fire **after** the validator's next scheduled
fetch would also fail, not before).

**Severity.** `warning`. Not a page: the validator's
spec-check tolerates losing one peer attester's
catalog feed for hours by falling back on its own
attester (`validator-e3d2e0907042b24e` for BLR1). The
fallback is silent in the spec-check pipeline, so the
operator must drive recovery within the shift rather
than overnight.

**Diagnose with the runbook §6 failure-mode table.**
The alert annotation embeds the triage map but the
canonical version is in this runbook. From a shell:

```bash
# 1. What HTTP status does the public path return right now?
curl -sS -o /dev/null -w "%{http_code}\n" \
  "https://api.qsdm.tech/attest/${peer_slot}/healthz"
```

| Status | Cause                                                                                                    | Recovery                                                                                                       |
|--------|----------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------|
| 502    | Caddy → relay is healthy, but no live tunnel session for the slot. Home attester is offline or crashed.   | On the home box: restart the launcher (§5 "Restart the attester"). Verify the public probes (§4) before clearing the alert. |
| 401    | Caddy is mis-routing `/attest/*` to the main API (the misleading 401 trap). One of the `handle` blocks in §2 has been removed or reordered. | Re-apply both `handle` directives in §2 to `/etc/caddy/Caddyfile`, then `systemctl restart caddy` on BLR1 (`caddy reload` does not work — `admin off` is set). |
| timeout / connection refused | Caddy or the relay itself is down on BLR1.                                                | `systemctl status caddy qsdm-relay` on BLR1; restart whichever is failing. Health check returns once the relay is back and the home tunnel has re-handshaked (the attester reconnects automatically). |
| 200 but Prometheus still says `up=0` | Prometheus is scraping a different hostname than Caddy terminates TLS on; or a mid-flight TLS cert rotation; or scrape timeout < tunnel handshake latency. | Inspect `qsdm-peer-attester-<slot>` target in Prometheus UI for the exact scrape error. Bump `scrape_timeout` to `30s` if a handshake is genuinely the cause. |

**What does NOT need recovery action.**

- One-off scrape blips below the 5m `for:` window — by
  design.
- `qsdm_attester_telemetry_collection_errors_total > 0`
  in isolation. The collector retries; what matters
  for this alert is whether the `/metrics` endpoint
  is reachable at all.

**Suppressing during planned maintenance.** If the
home box is intentionally rebooting (`shutdown /r`
issued by the operator), silence the alert via
Alertmanager for the expected downtime window
(`amtool silence add alertname=QSDMPeerAttesterAbsent peer_slot=<slot> -d 10m -a "<operator>" -c "planned reboot"`).
Do NOT silence on `severity=warning` — that pattern
catches unrelated alerts.
