# QSDM Reverse Tunnel — Operator Quickstart

Expose a NAT-bound `qsdm-attester` to the public internet **without
Cloudflare Tunnel, ngrok, frp, chisel, or any third-party daemon**. The
QSDM-native equivalent is two binaries, one config file, one HMAC
shared secret, and one Caddy directive.

## Why we built this

The tier-A "Public Challenge Issuer" plan needs your home 3050's
attester to be reachable from any miner on the internet. The home
machine is behind NAT/CGNAT (no public IPv4, no IPv6 on most ISPs) so
direct port-forwarding doesn't work. The standard answers are
Cloudflare Tunnel, Tailscale Funnel, or ngrok — all third parties.

QSDM's reverse tunnel is the same idea — a long-lived outbound TLS
connection from the home box to a public-IP relay, with HTTP
multiplexed inside — minus the third-party trust root. The relay
runs on the same VPS as the validator, and the auth is a 32-byte HMAC
shared between the attester and the relay's slot allowlist.

## Architecture

```
[ home 3050         ]                       [ BLR1 VPS                       ]                      [ any miner ]
  qsdm-attester  ──── outbound TLS ────►   qsdm-relay ◄─── HTTPS ──── Caddy ◄─── HTTPS ────  curl/qsdmminer
  (yamux server)     HTTP/1.1 Upgrade       (yamux client)
                     101 Switching          slot table:
                     Protocols              "blackbeard-3050" → live session

miner GET https://api.qsdm.tech/attest/blackbeard-3050/api/v1/mining/challenge
       └► Caddy /attest/* → relay :7710
              └► open new yamux stream to slot's session
                     └► tunnel back to home → attester http.Server →
                        mints nonce, signs with 3050's HMAC key, returns
```

A single TCP/TLS connection from the home machine carries unlimited
concurrent miner requests — yamux opens a fresh stream per request.

## Components

| Binary | Role | Default ports |
|--------|------|---------------|
| `qsdm-attester` (Windows/Linux) | Local challenge issuer + tunnel client | `127.0.0.1:7733` |
| `qsdm-relay` (Linux on BLR1) | Public rendezvous + reverse proxy | `127.0.0.1:{7700,7710,7720}` |
| `pkg/tunnel` | Shared protocol primitives (handshake, registry, yamux glue) | — |

## Wire protocol

1. **Connect.** Tunnel client opens a TLS connection to the relay's
   public URL and sends:

   ```
   GET /_tunnel/connect HTTP/1.1
   Host: api.qsdm.tech
   Connection: Upgrade
   Upgrade: qsdm-tunnel/1
   X-QSDM-Version: qsdm-tunnel/1
   X-QSDM-Slot: blackbeard-3050
   X-QSDM-Signer-ID: attester-12a0d1aa082b7e28
   X-QSDM-Timestamp: 1778155018
   X-QSDM-Auth: <hex(HMAC-SHA256(slotkey, msg))>
   ```

   where `msg` is the canonical newline-delimited byte string

   ```
   "qsdm-tunnel-auth\n" + version + "\n" + slot + "\n" + signer + "\n" + ts + "\n"
   ```

2. **Auth.** The relay looks up the slot in its `relay_slots.toml`
   allowlist, verifies the HMAC + a ±60s timestamp window, and on
   success returns:

   ```
   HTTP/1.1 101 Switching Protocols
   Upgrade: qsdm-tunnel/1
   Connection: Upgrade
   ```

3. **Multiplex.** Both sides hand the now-bidirectional connection to
   yamux:

   - The relay runs `yamux.Client(conn)` and **opens** streams when
     miner requests arrive (one per request).
   - The attester runs `yamux.Server(conn)` and **accepts** streams,
     handing each one to its existing `*http.ServeMux` so the public
     behaviour is byte-identical to a direct connection.

4. **Reverse proxy.** A miner request to
   `https://api.qsdm.tech/attest/<slot>/<path>` is path-stripped by
   Caddy (`handle_path /attest/*`) into `/<slot>/<path>` at the relay,
   then the relay strips the slot and forwards `<path>` through the
   yamux stream.

5. **Reconnect.** The tunnel client's `Run` loop reconnects with
   exponential backoff (1s … 60s) on any error, so a flaky home
   internet connection or a relay restart heal automatically.

## Threat model

The relay is a **pure traffic forwarder**. It holds no chain state, no
mining authority, and no validator keys. Compromising it lets an
attacker:

| Attack | Impact |
|--------|--------|
| Read challenge nonces in transit | None — challenges are public information already published by the validator. |
| Forge a tunnel registration | Requires the slot's HMAC key. Without it, every Upgrade is rejected with 401. |
| DoS one slot | The miner falls back to other entries in `challenge_urls` (validator self-issued + any other peer attesters). |
| Inject fake challenges | Cannot — the relay never signs anything. Challenges are signed by the attester's key, which never crosses the wire. |

The HMAC slot key is reused from the attester's own signer key (the
recommended posture). One key per attester keeps configuration
minimal.

## Operator setup (BLR1 side, one-time)

1. **Build the relay** (cross-build from any dev box):

   ```bash
   cd QSDM/source
   GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
     go build -ldflags '-s -w -X main.buildVersion=relay-v2' \
     -o qsdm-relay-linux-amd64 ./cmd/qsdm-relay
   ```

2. **Install on BLR1:**

   ```bash
   scp qsdm-relay-linux-amd64 root@blr1:/tmp/
   ssh root@blr1 'install -o qsdm -g qsdm -m 0755 /tmp/qsdm-relay-linux-amd64 /opt/qsdm/qsdm-relay'
   ```

3. **Slot allowlist** at `/opt/qsdm/relay_slots.toml`:

   ```toml
   [[slot]]
   slot_id = "blackbeard-3050"
   key_hex = "<32-byte hex>"
   note    = "blackbeard's home 3050 (RTX 3050 Ampere CC 8.6, Manila)"
   ```

   The `key_hex` must match the attester's signer key (read with
   `xxd -p ~/.qsdm/attester.key | tr -d '\n'`).

4. **systemd unit** at `/etc/systemd/system/qsdm-relay.service`:

   ```ini
   [Unit]
   Description=QSDM reverse-tunnel relay
   After=network.target

   [Service]
   Type=simple
   User=qsdm
   Group=qsdm
   WorkingDirectory=/opt/qsdm
   ExecStart=/opt/qsdm/qsdm-relay --slots /opt/qsdm/relay_slots.toml --tunnel-listen 127.0.0.1:7700 --proxy-listen 127.0.0.1:7710 --metrics-listen 127.0.0.1:7720
   Restart=always
   RestartSec=5
   NoNewPrivileges=true
   PrivateTmp=true
   ProtectSystem=strict
   ProtectHome=true
   ReadWritePaths=/opt/qsdm
   StandardOutput=journal
   StandardError=journal

   [Install]
   WantedBy=multi-user.target
   ```

5. **Caddy snippet** — append to the `api.qsdm.tech, node.qsdm.tech`
   block in `/etc/caddy/Caddyfile`, **above** the existing
   `@health`/`@scrape` matchers:

   ```caddy
   handle /_tunnel/connect {
       reverse_proxy 127.0.0.1:7700
   }
   handle_path /attest/* {
       reverse_proxy 127.0.0.1:7710
   }
   ```

   Then `systemctl restart caddy && systemctl enable --now qsdm-relay`.

## Operator setup (home machine side, per attester)

1. Run the attester with the `--relay` and `--slot` flags:

   ```bash
   qsdm-attester \
     --listen=127.0.0.1:7733 \
     --relay=https://api.qsdm.tech \
     --slot=blackbeard-3050
   ```

2. The attester logs:

   ```
   attester: tunnel client starting relay=https://api.qsdm.tech slot=blackbeard-3050 signer_id=attester-XXXX
   tunnel: session established slot=blackbeard-3050 relay=https://api.qsdm.tech
   ```

3. Verify from any external machine:

   ```bash
   curl https://api.qsdm.tech/attest/blackbeard-3050/api/v1/mining/challenge
   # → {"nonce":"...","issued_at":...,"signer_id":"attester-XXXX","signature":"..."}
   ```

## Miner setup

Add the public URL to `challenge_urls` in your `miner.toml`:

```toml
challenge_urls = [
    "http://127.0.0.1:7733",                          # local 3050 (low latency)
    "https://api.qsdm.tech/attest/blackbeard-3050",   # SAME 3050 via relay
]
```

The `qsdmminer-console` round-robins challenges across these URLs and
falls over to the validator's self-issued signer if all custom URLs
fail. Any miner anywhere on the internet can use the relay URL —
they don't need to be on the same network as the home machine.

## Observability

| Endpoint | What you see |
|----------|--------------|
| `http://127.0.0.1:7720/info` (BLR1) | Live slot table — JSON list of currently-connected tunnels |
| `http://127.0.0.1:7720/metrics` (BLR1) | OpenMetrics: slots_live, registers_total, proxy_requests_total |
| `http://127.0.0.1:7720/healthz` (BLR1) | Liveness probe |
| `journalctl -u qsdm-relay -f` (BLR1) | Per-tunnel register/deregister + per-request errors |

The attester's own `/info`, `/metrics`, `/healthz` are reachable via
the same relay path: `https://api.qsdm.tech/attest/<slot>/info` etc.

## Limits and future work

- **One tunnel per slot.** A duplicate `Register` for a live slot is
  rejected with 409 (prevents a misconfigured second instance from
  silently evicting the first). Increment a counter in the slot ID
  or use `--slot blackbeard-3050-b` for a second machine.
- **No load-balancing across tunnels.** A relay with two tunnels for
  the same slot would need a load-balancer policy. Today: out of
  scope. Workaround: use distinct slot IDs and miner-side
  `challenge_urls` round-robin.
- **No mTLS on the tunnel-ingress port.** The HMAC over (slot+ts) is
  the only auth. Adequate for a public-information service like
  challenge minting; would need upgrading if we ever ferry secrets.
- **Relay is a SPOF for tunneled traffic.** Adding a second relay (in
  e.g. a different region) and registering both with the miner
  fleet's `challenge_urls` is a one-line operator change.

## Troubleshooting

| Symptom | Cause |
|---------|-------|
| `curl /attest/.../...` returns 502 | No live tunnel for that slot. Check `journalctl -u qsdm-relay`. |
| Attester logs `relay rejected upgrade: status=401` | Slot key on the home box doesn't match `relay_slots.toml`. |
| Attester logs `relay rejected upgrade: status=400` | Caddy stripped the Upgrade header. Verify `handle /_tunnel/connect` block in Caddyfile. |
| Tunnel reconnects every minute | Likely yamux ping timeout — check `journalctl -u qsdm-relay` for context. The default keepalive is 30s, ping timeout 60s. |
