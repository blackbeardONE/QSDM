# NGC sidecar — Operator QUICKSTART

This is the **operator runbook** for running the QSDM NVIDIA NGC attestation
sidecar against a QSDM validator node. It complements the deeper README in
this folder and assumes you already have a validator reachable on port
**8080** (HTTP, `/api/v1/*`).

> **Scope.** The NGC sidecar is an **opt-in transparency signal**. It does not
> participate in consensus and never changes block production. What it does:
> it periodically submits a signed proof bundle (GPU fingerprint + compute
> workload hash) to your validator's `/api/v1/monitoring/ngc-proof` endpoint,
> where the validator surfaces aggregated attestation data on its public
> `/api/v1/trust/attestations/summary`. See
> [`../../nvidia_locked_qsdmplus_blockchain_architecture.md`](../../nvidia_locked_qsdmplus_blockchain_architecture.md)
> for the design rationale.
>
> **You do NOT need a GPU to run this runbook end-to-end.** The CPU profile
> produces a real bundle (it just reports `gpu_fingerprint.available=false`),
> which exercises the full path. Run the GPU profile later if you want
> bundles that satisfy NVIDIA-lock (`nvidia_lock = true` on the node).

---

## 0. Prerequisites

- [ ] A running QSDM validator reachable at `http://<node>:8080` (same host
      is fine — we use `host.docker.internal` from inside the sidecar).
- [ ] Docker 24+ with `docker compose` plugin.
- [ ] NGC account + NGC CLI API key (free,
      [https://ngc.nvidia.com/setup](https://ngc.nvidia.com/setup)).
- [ ] (GPU profile only) Host with a recent NVIDIA driver + NVIDIA Container
      Toolkit so `docker run --gpus all` works.

You should NOT need to edit the Go node source to run this — everything is
driven by environment variables the node already honors.

---

## 1. Generate a shared ingest secret (5 seconds)

The node and the sidecar authenticate proof bundles with a shared secret
sent in the `X-QSDMPLUS-NGC-Secret` header. Generate one now and keep it:

```bash
openssl rand -hex 32
# => e.g. 7f2c3c1d0b...ef9a
```

> **Do NOT reuse the same secret across validators you do not own.** One
> leaked secret lets an attacker spam your node's ingest endpoint. Rotate
> by restarting the sidecar + node with a new value — no state migration is
> needed because the node keys per-bundle and purges on restart.

Export it into your shell for the rest of this runbook:

```bash
export NGC_INGEST_SECRET="7f2c3c1d0b...ef9a"
```

---

## 2. Turn ingest ON on the node

On the host running `qsdm-validator` / `qsdmplus`, set the ingest secret and
restart the service. The route is **404** when unset (feature off), so the
node stays closed by default.

**systemd (recommended):**

```bash
sudo systemctl edit qsdm-validator    # or `qsdmplus` on pre-rebrand units
# Paste, save, close:
[Service]
Environment=QSDMPLUS_NGC_INGEST_SECRET=7f2c3c1d0b...ef9a

sudo systemctl restart qsdm-validator
```

**Docker (the `VALIDATOR_QUICKSTART.md` compose shape):** add
`-e QSDMPLUS_NGC_INGEST_SECRET=$NGC_INGEST_SECRET` to the `docker run`
line in `qsdm-validator.service` and `systemctl restart qsdm-validator`.

Verify the node now answers the challenge route (expected: `200` or `401`,
NOT `404`):

```bash
curl -sS -o /dev/null -w '%{http_code}\n' \
  -H "X-QSDMPLUS-NGC-Secret: $NGC_INGEST_SECRET" \
  http://127.0.0.1:8080/api/v1/monitoring/ngc-challenge
```

A `404` here means the env var did not reach the process — check
`systemctl show qsdm-validator -p Environment` (or `docker inspect
qsdm-validator | jq '.[0].Config.Env'`).

---

## 3. Clone + configure the sidecar

```bash
git clone https://github.com/blackbeardONE/QSDM.git
cd QSDM/apps/qsdmplus-nvidia-ngc

cp ngc.env.example ngc.env
```

Edit `ngc.env`:

```ini
# From https://ngc.nvidia.com/setup/api-key
NGC_CLI_API_KEY=nvapi-xxxxxxxxxxxxxxxxxxxxxxxxxxxx

# Must match the node
QSDMPLUS_NGC_REPORT_URL=http://host.docker.internal:8080/api/v1/monitoring/ngc-proof
QSDMPLUS_NGC_INGEST_SECRET=7f2c3c1d0b...ef9a
```

`ngc.env` is gitignored (see `.gitignore` in this folder). Do **not** check
it in.

---

## 4. Authenticate to nvcr.io (one time per host)

Only needed if you plan to use the GPU profile (step 6). Safe to skip for
the CPU-only smoke test in step 5.

```bash
# Linux / macOS
. ./ngc.env && echo "$NGC_CLI_API_KEY" | \
  docker login nvcr.io -u '$oauthtoken' --password-stdin
```

```powershell
# Windows PowerShell (the literal string $oauthtoken is the username)
Get-Content ngc.env | Select-String '^NGC_CLI_API_KEY=' | `
  ForEach-Object { $_.Line -replace '^NGC_CLI_API_KEY=','' } | `
  docker login nvcr.io -u '$oauthtoken' --password-stdin
```

Smoke-test registry access without a huge download:

```powershell
.\scripts\verify-ngc-docker.ps1       # Windows
```

```bash
docker manifest inspect nvcr.io/nvidia/pytorch:24.07-py3   # Linux / macOS
```

---

## 5. First run — CPU profile (no GPU required)

Helps you validate wiring before the GPU base image is pulled.

```bash
set -a; . ./ngc.env; set +a
docker compose up --build
```

On another terminal, from the host:

```bash
curl -sS -H "X-QSDMPLUS-NGC-Secret: $NGC_INGEST_SECRET" \
  http://127.0.0.1:8080/api/v1/monitoring/ngc-proofs | jq '.[] | {
    node_id:      .qsdmplus_node_id,
    gpu:          .gpu_fingerprint.available,
    received_at:  .server_received_at
  }'
```

You should see at least one entry with `gpu: false` within ~30 s. That
proves: ingest secret matches, challenge path works, bundle JSON parses.

**Prometheus check** — the node exposes ingest counters on its scrape
endpoint. Replace `$SCRAPE_TOKEN` with the same value you use for regular
Prometheus scrapes (see `VALIDATOR_QUICKSTART.md` §7):

```bash
curl -sS -H "Authorization: Bearer $SCRAPE_TOKEN" \
  http://127.0.0.1:8080/api/metrics/prometheus \
  | grep -E '(qsdm|qsdmplus)_ngc_proof_ingest_(accepted|rejected)_total'
```

`qsdm_ngc_proof_ingest_accepted_total` should be `>= 1`. During the
`qsdmplus_*` → `qsdm_*` dual-emit deprecation window (Major Update §6)
the legacy series `qsdmplus_ngc_proof_ingest_accepted_total` is emitted
with the same value, so either name works in dashboards and alerts.

---

## 6. GPU profile (real attestation)

Only run this on a host with an NVIDIA GPU + toolkit. The GPU profile pulls
`nvcr.io/nvidia/pytorch:24.07-py3` (~8 GB) and produces bundles where
`gpu_fingerprint.available=true`, which is what NVIDIA-lock mode requires.

```bash
docker compose --profile gpu up --build
# or, shortcut from this folder:
./scripts/run-gpu.sh           # detached: ./scripts/run-gpu.sh -d
```

Re-check `/api/v1/monitoring/ngc-proofs`; entries for the GPU container
should carry a real device list:

```json
{
  "gpu_fingerprint": {
    "available": true,
    "devices": [
      { "index": "0", "name": "NVIDIA H100 80GB HBM3", "driver_version": "...", "compute_capability": "9.0" }
    ]
  }
}
```

---

## 7. Optional — bind the bundle to this node's identity

Set `QSDMPLUS_NVIDIA_LOCK_EXPECTED_NODE_ID=my-validator-1` on the node,
and the same value as `QSDMPLUS_NGC_PROOF_NODE_ID=my-validator-1` on the
sidecar (inside `ngc.env`). Now every bundle includes
`qsdmplus_node_id: "my-validator-1"` and the node rejects any bundle that
targets a different node id. This is the safe shape for running multiple
validators on shared infrastructure.

Optional extras, each of which tightens the coupling further:

| Env on SIDECAR                           | Env on NODE                               | What it buys |
|------------------------------------------|-------------------------------------------|--------------|
| `QSDMPLUS_NGC_PROOF_HMAC_SECRET=<x>`     | `QSDMPLUS_NVIDIA_LOCK_PROOF_HMAC_SECRET=<x>` | Each bundle is also HMAC'd with `<x>`; node rejects unsigned bundles. |
| `QSDMPLUS_NGC_FETCH_CHALLENGE=true`      | `nvidia_lock_require_ingest_nonce = true` (or equivalent env) | Sidecar pulls a fresh nonce per bundle → replay resistance. Node rate-limits the route to 15/min; sidecar honors `Retry-After`. |
| `QSDMPLUS_NGC_CHALLENGE_JITTER_MAX_SEC=8` | — | Random 0…8 s sleep before each challenge fetch; use when many validators share one egress NAT. |

---

## 8. Production-hardening checklist

- [ ] `ngc.env` permissions: `chmod 600 ngc.env` (systemd won't read 0644
      secrets when `PrivateTmp=true`).
- [ ] Rotate `QSDMPLUS_NGC_INGEST_SECRET` on a schedule (≤ 90 days
      recommended). Rotation = restart node with new value, then restart
      sidecar with new value. No downtime on the consensus path.
- [ ] Monitor `qsdm_ngc_proof_ingest_rejected_total{reason=...}` (legacy
      alias during the dual-emit window: `qsdmplus_ngc_proof_ingest_rejected_total`)
      — a spike in `reason="hmac_mismatch"` or `"nonce_invalid"` usually means
      sidecar and node drifted (different secrets, clock skew, or a stale
      container).
- [ ] Dashboard panel — import
      `QSDM/deploy/grafana/qsdmplus-overview.json`, which already charts the
      ingest accept/reject rate.
- [ ] Do not expose port `9910/udp` (gossip) to the public internet. It is
      only meant for peer-to-peer mesh summaries within your validator
      fleet.

---

## 8a. Windows: keep the trust badge fresh with a Scheduled Task

If you don't want to run Docker just to keep a proof flowing, the
repo now ships two tiny PowerShell wrappers that call
`validator_phase1.py` directly:

- `scripts/local-attest.ps1` — one-shot (or `-LoopMinutes 12` for an
  infinite foreground loop).
- `scripts/attest-from-env-file.ps1` — reads `KEY=VALUE` pairs out of
  a local env file, then invokes the one-shot wrapper. Designed to be
  called from Windows Task Scheduler so you don't leak secrets into
  task arguments.

Example: post one bundle every 10 minutes from a Windows PC with an
NVIDIA GPU already visible to PyTorch.

1. Put the ingest secret in `apps/qsdmplus-nvidia-ngc/ngc.local.env`
   (already covered by `.gitignore` → never committed):

   ```dotenv
   QSDMPLUS_NGC_REPORT_URL=https://<your-node>/api/v1/monitoring/ngc-proof
   QSDMPLUS_NGC_INGEST_SECRET=<32-byte-hex>
   QSDMPLUS_NGC_PROOF_NODE_ID=<free-form-label>
   ```

2. Smoke-test once:

   ```powershell
   .\apps\qsdmplus-nvidia-ngc\scripts\attest-from-env-file.ps1 `
     -EnvFile .\apps\qsdmplus-nvidia-ngc\ngc.local.env
   ```

3. Register the scheduled task (user-level, no admin required).
   `local-attest.ps1` now handles its own transcript + rotation
   (10 MiB cap, 3 archives) when you pass `-LogPath`, so the command
   line stays short and the log file never grows unbounded:

   ```powershell
   $repo = (Resolve-Path .).Path
   $script  = "$repo\apps\qsdmplus-nvidia-ngc\scripts\attest-from-env-file.ps1"
   $envfile = "$repo\apps\qsdmplus-nvidia-ngc\ngc.local.env"
   $log     = "$env:LOCALAPPDATA\QSDM\ngc-attest.log"
   New-Item -ItemType Directory -Force -Path (Split-Path $log) | Out-Null

   $argline = "-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden " +
              "-File `"$script`" -EnvFile `"$envfile`" -Quiet " +
              "-LogPath `"$log`" -LogMaxBytes 10485760 -LogKeep 3"
   $action    = New-ScheduledTaskAction -Execute "powershell.exe" `
                  -Argument $argline
   $trigger   = New-ScheduledTaskTrigger -Once -At (Get-Date).AddMinutes(1) `
                  -RepetitionInterval (New-TimeSpan -Minutes 10) `
                  -RepetitionDuration (New-TimeSpan -Days 365)
   $settings  = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries `
                  -DontStopIfGoingOnBatteries -StartWhenAvailable `
                  -MultipleInstances IgnoreNew `
                  -ExecutionTimeLimit (New-TimeSpan -Minutes 5)
   $principal = New-ScheduledTaskPrincipal -UserId $env:USERNAME `
                  -LogonType Interactive -RunLevel Limited

   Register-ScheduledTask -TaskName "QSDM-NGC-Attest" `
     -Action $action -Trigger $trigger -Settings $settings -Principal $principal
   Start-ScheduledTask -TaskName "QSDM-NGC-Attest"
   ```

4. Verify:

   ```powershell
   Get-ScheduledTaskInfo -TaskName "QSDM-NGC-Attest" |
     Format-List LastRunTime, LastTaskResult, NextRunTime
   Invoke-RestMethod -Uri "https://<your-node>/api/v1/trust/attestations/summary"
   ```

   `last_attested_at` should advance every 10 minutes, and
   `ngc_service_status` should stay `healthy` as long as the PC is on
   and can reach the node.

To uninstall: `Unregister-ScheduledTask -TaskName "QSDM-NGC-Attest" -Confirm:$false`.

---

## 9. Uninstall

```bash
docker compose --profile gpu down -v          # or without --profile gpu
sudo systemctl edit qsdm-validator            # remove QSDMPLUS_NGC_INGEST_SECRET=
sudo systemctl restart qsdm-validator
```

After the restart, `/api/v1/monitoring/ngc-proof` returns **404**
(feature off) and any lingering sidecar POSTs will be dropped.

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Sidecar logs `401 Unauthorized` on POST | Secret mismatch between sidecar and node | Re-export `QSDMPLUS_NGC_INGEST_SECRET` in both places; `docker compose up --force-recreate`. |
| Sidecar logs `429 Too Many Requests` on `/ngc-challenge` | Multiple validators behind one NAT hammering the route | Set `QSDMPLUS_NGC_CHALLENGE_JITTER_MAX_SEC=8` on each sidecar. |
| Sidecar gets `404` on POST | Node has `QSDMPLUS_NGC_INGEST_SECRET` unset | See §2 — ingest is off by default. |
| GPU bundle has `gpu_fingerprint.available=false` | Running CPU profile, or `nvidia-smi` not visible in container | Use `docker compose --profile gpu up`, confirm `nvidia-smi` works on host. |
| Node rejects bundle with `qsdmplus_node_id mismatch` | Sidecar's `QSDMPLUS_NGC_PROOF_NODE_ID` differs from node's `QSDMPLUS_NVIDIA_LOCK_EXPECTED_NODE_ID` | Align them in `ngc.env` and restart the sidecar only — no node restart needed. |
| `docker login nvcr.io` fails with 401 | API key revoked or wrong scope | Regenerate at `ngc.nvidia.com/setup/api-key`, update `NGC_CLI_API_KEY` in `ngc.env`. |

For anything else, open an issue at
[https://github.com/blackbeardONE/QSDM/issues](https://github.com/blackbeardONE/QSDM/issues)
with the sidecar logs (`docker compose logs validator-cpu` / `validator-gpu`)
and the node's `journalctl -u qsdm-validator --since '-10 min'`.
