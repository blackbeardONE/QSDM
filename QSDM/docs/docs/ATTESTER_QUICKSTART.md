# `qsdm-attester` — Public Challenge Issuer Quickstart

`qsdm-attester` is a tiny standalone HTTP service that mints v2
challenge nonces signed with **your** HMAC key. Once enrolled in
a validator's peer-signers allowlist, miners anywhere in the
world can pull challenges from your machine and the validator
will accept proofs that reference them.

This is the simplest way to turn a home machine (e.g. a Windows
desktop with an RTX 3050) into **shared QSDM infrastructure**.

## What it does (and what it doesn't)

| Does | Does NOT |
|------|----------|
| Mints fresh `(nonce, issued_at, signature)` triples | Verify proofs |
| Holds a 32-byte HMAC signer key on disk | Hold chain state |
| Exposes 4 HTTP routes (~280 LOC of Go) | Run BFT, mempool, account store |
| Runs on Windows, Linux, macOS | Require a GPU to operate (any NVIDIA + Go runtime is enough) |
| Can be tunnelled through Cloudflare / TOR | Need port-forwarding if you use a tunnel |

The threat model is small: a misbehaving attester can only hurt
its own miners by serving stale or malformed challenges. It
**cannot** cause a validator to accept an invalid proof, because
the validator still verifies every proof's signature against
the registered signer key.

## Routes

| Path | Method | Purpose |
|------|--------|---------|
| `/api/v1/challenge` | GET | Mint a fresh signed challenge (the only consensus-relevant route). Wire-compatible with the validator's `/api/v1/mining/challenge`. |
| `/healthz` | GET | Liveness probe; always 200 once boot is past the issuer wiring. |
| `/info` | GET | Public-safe metadata: `signer_id`, `key_fingerprint`, `note`, `version`, `uptime_seconds`, `issued_total`. |
| `/metrics` | GET | OpenMetrics text exposition (issued, errors, requests, uptime). |

## Quickstart on Windows (your home 3050)

### 1. Place the binary

The pre-built Windows binary lives at `bin\qsdm-attester.exe` in
the QSDM repo root. Copy it anywhere convenient — for example
`C:\qsdm\qsdm-attester.exe`.

### 2. First run (auto-generates a fresh signer key)

```powershell
.\qsdm-attester.exe --listen 127.0.0.1:7733 --note "blackbeard's home 3050"
```

On first boot you'll see a line that looks like:

```
attester: COPY THIS LINE INTO peer_signers.toml ON THE VALIDATOR ↓
attester: signer_id="attester-98826ccb8067d587" key_hex="98826ccb..." note="blackbeard's home 3050"
```

**Save those three values** — `signer_id`, `key_hex`, `note`.
You'll paste them into the validator's `peer_signers.toml`. The
key file itself is persisted at `%USERPROFILE%\.qsdm\attester.key`
(0o600 perms) and re-used on every subsequent boot.

### 3. Smoke-test locally

```powershell
Invoke-RestMethod http://127.0.0.1:7733/healthz
Invoke-RestMethod http://127.0.0.1:7733/info
Invoke-RestMethod http://127.0.0.1:7733/api/v1/challenge
```

You should get JSON back from each. Two consecutive challenge
calls must produce **different nonces** — that's the entropy +
de-dup loop working.

### 4. Expose to the internet

You have three options ranked from simplest to most flexible:

| Option | Setup | Pros | Cons |
|--------|-------|------|------|
| **Cloudflare Tunnel** (recommended) | `cloudflared tunnel --url http://127.0.0.1:7733` | Zero port-forwarding, hides your home IP, free | Requires a Cloudflare account |
| **Tailscale Funnel** | `tailscale funnel 7733` | Same benefits as CF, simpler if you already use Tailscale | Tailscale-only |
| **Direct port-forward** | Forward TCP 7733 on your router | No third party | Exposes your home IP, needs static or DDNS |

Whatever you pick, you get a public URL like
`https://blackbeard-attester.example.com`. That URL is what you
share with miners.

## Validator-side enrollment (BLR1 or any other validator)

The validator decides which attesters to trust. Open
`peer_signers.toml` (anywhere on the validator host — the path
is configurable via `QSDM_PEER_SIGNERS_FILE`):

```toml
[[peer]]
signer_id = "attester-98826ccb8067d587"
key_hex   = "98826ccb8067d587eb66f3a1cc1042b27a7e34fb1785945d74830e842d7c7bf3"
note      = "blackbeard's home 3050 (Manila, Ampere)"
```

Then point the validator at the file via systemd drop-in or env:

```ini
# /etc/systemd/system/qsdm.service.d/peer-signers.conf
[Service]
Environment="QSDM_PEER_SIGNERS_FILE=/etc/qsdm/peer_signers.toml"
```

Reload + restart:

```sh
sudo systemctl daemon-reload
sudo systemctl restart qsdm
```

Confirm the new attester is registered:

```sh
journalctl -u qsdm -n 200 | grep peer-signer
# Expect: "v2 peer-signers loaded path=/etc/qsdm/peer_signers.toml registered=1"
```

If a peer is misconfigured (bad hex, empty signer_id, duplicate),
the validator **fails boot** with a clear per-peer warning —
this is intentional, because a silent allowlist drop is worse
than a loud restart.

## Miner-side usage

A miner can now choose which challenge endpoint to pull from.
The miner's existing `qsdmminer-console.exe` config will accept
either:

- the validator's own `/api/v1/mining/challenge` (default), or
- your attester's `https://blackbeard-attester.example.com/api/v1/challenge`

…because the wire shape is identical (`api.ChallengeWire`). A
near-future miner update will accept a comma-separated
`challenge_urls` list with round-robin / failover; until then,
the miner picks one URL.

## Security model

| Risk | Mitigation |
|------|-----------|
| Attester compromised → attacker mints valid signatures | The validator can revoke a peer by deleting its row from `peer_signers.toml` and restarting. Compromise scope is limited to "this attester's miners can't submit proofs" until rotation. |
| Attester serves stale challenges | Validator rejects challenges older than `FreshnessWindow` (60s). Stale issuance just causes the miner's submission to fail — no consensus impact. |
| Attester serves a same-nonce twice | Issuer's internal seen-map dedupes within a 120s window. PRNG collision is statistically impossible at 256-bit entropy. |
| Attester operator runs multiple attester binaries with the same key | Each registers under the same `signer_id`. Validators only need to allowlist once. The seen-map is process-local per binary, so two binaries could in principle issue the same nonce — but the validator's nonce store dedupes on the validator side too. |
| Home IP exposed | Use Cloudflare Tunnel or Tailscale Funnel. The validator never connects to the attester directly — miners do. |

## Operator runbook

| Task | Command |
|------|---------|
| Start attester | `.\qsdm-attester.exe --listen 127.0.0.1:7733 --note "your-tag"` |
| Start at boot (Windows) | `New-ScheduledTaskAction` + `Register-ScheduledTask` (or run as a service via NSSM) |
| Start at boot (Linux) | systemd unit; `ExecStart=/usr/local/bin/qsdm-attester-linux-amd64 --listen :7733` |
| Rotate key | Stop attester, delete `~/.qsdm/attester.key`, start. Will print a NEW signer_id; coordinate with validator operator to update `peer_signers.toml`. |
| Watch traffic | `Invoke-RestMethod http://127.0.0.1:7733/info` for `issued_total`, or scrape `/metrics` |
| Verify validator accepts your challenges | On the validator: `curl -s "http://localhost:8080/api/v1/mining/challenge"` then on your attester: same call. Submit a proof referencing each. Both should land. |

## Configuration reference

| Flag | Env var | Default | Notes |
|------|---------|---------|-------|
| `--listen` | `QSDM_ATTESTER_LISTEN` | `:7733` | Bind address |
| `--key` | `QSDM_ATTESTER_KEY_PATH` | `~/.qsdm/attester.key` | 32-byte raw key, 0o600 |
| `--signer-id` | `QSDM_ATTESTER_SIGNER_ID` | derived `attester-<hex8>` | Override only if you want a human-readable id |
| `--note` | `QSDM_ATTESTER_NOTE` | hostname | Free-form tag on `/info` |
| `--log-every` | `QSDM_ATTESTER_LOG_EVERY` | `0` (off) | If >0, log a sample line every Nth issuance |
| `--version` | — | — | Print build version and exit |

## What's next (Roles 2-5)

This is **Role #1** of the five-role attestation infrastructure
plan. The next roles build on the same daemon process:

| Role | Adds | Status |
|------|------|--------|
| **#1 — Public challenge issuer** | This document | ✅ Shipped |
| **#2 — Reference telemetry oracle** | Publish signed Ampere/Ada/Blackwell fingerprints; cross-check spoofers | Planned |
| **#3 — Hardware CA** | Sign other miners' HMAC enrollment keys after attesting their hardware | Planned |
| **#4 — Tier-1 hashrate calibration peg** | Chain publishes your sustained hashrate as the reference Ampere baseline | Planned |
| **#5 — Relay / gossip node** | Rebroadcast proofs and blocks across the network | Planned |

See the parent design discussion for ordering and rationale.
