# Attestation sidecars — adding more sources to the trust pill

**Purpose.** The `/api/v1/trust/attestations/summary` endpoint reports
`attested` = the count of distinct sources that posted a fresh NGC
proof within `fresh_within` (default 15 min). The qsdm.tech trust pill
and the external CI probe (`.github/workflows/trustcheck-external.yml`
with `--min-attested 2`) both fail when that number drops below 2.
This page is the operator recipe for standing up an Nth attestation
source so the pill shows `N/N` instead of `1/N`.

The node aggregates distinct sources by the `qsdmplus_node_id` field
in the proof bundle (see
`QSDM/source/pkg/monitoring/ngc_proofs.go :: NGCProofDistinctByNodeID`).
Two sidecars with the same node-id collapse to one attestation; two
sidecars with different node-ids both count.

## Reference deployment (2026-04-23)

| # | Host | Role | Shape / OS | `QSDMPLUS_NGC_PROOF_NODE_ID` |
|---|------|------|------------|------------------------------|
| 1 | Windows dev PC | GPU attestation, Scheduled Task every 10 min | RTX 3050, CC 8.6 | `qsdmplus-windows-dev` |
| 2 | DigitalOcean BLR1 | CPU-fallback sidecar, `systemd` timer every 10 min | Ubuntu 22.04 | `qsdmplus-vps` |
| 3 | Oracle Cloud ap-singapore-1 | CPU-fallback sidecar, `systemd` timer every 10 min | E5.Flex (billable) or A1.Flex (free) | `qsdmplus-oci` |

Result: `attested=3/total_public=4` (the BLR1 validator itself counts
once in the denominator, each sidecar adds 1 to the numerator).

## Required invariants

Every attestation source must:

1. **Post to the same ingest URL.** All three sources in the reference
   deployment POST to `https://api.qsdm.tech/api/v1/monitoring/ngc-proof`.
   Self-attestation of the VPS validator from a sidecar running on
   that same VPS uses `http://127.0.0.1:8443/api/v1/monitoring/ngc-proof`.
2. **Share the same `QSDMPLUS_NGC_INGEST_SECRET`.** Set once on the
   ingest-side (BLR1 systemd env) and copied into every sidecar's
   `ngc.env`. HMAC mismatch is the most common rejection cause; it
   surfaces as `qsdm_ngc_proof_ingest_rejected_total{reason="hmac_mismatch"}`.
3. **Use a unique `QSDMPLUS_NGC_PROOF_NODE_ID`.** If two sidecars share
   a node-id they collapse to one attestation and the pill stays at
   `1/N` regardless of how many you run. A good convention:
   `qsdmplus-<platform>-<short-location>` (e.g. `qsdmplus-oci-ap-singapore-1`).
4. **Fire at least every `fresh_within` / 2.** The default 15-min window
   with a 10-min cadence gives a 5-min safety margin; if you raise
   `fresh_within` in `[trust]`, raise the timer cadence too.

## Installing a sidecar

### Windows dev PC (Scheduled Task + PowerShell)

Used for the GPU attestation source. The full recipe is
`apps/qsdmplus-nvidia-ngc/scripts/attest-from-env-file.ps1`; a Scheduled
Task invokes it every 10 min. The env file lives beside it as
`ngc.local.env` (gitignored) with:

```
QSDMPLUS_NGC_REPORT_URL=https://api.qsdm.tech/api/v1/monitoring/ngc-proof
QSDMPLUS_NGC_INGEST_SECRET=<same secret as the BLR1 node>
QSDMPLUS_NGC_PROOF_NODE_ID=qsdmplus-windows-dev
```

Scheduled Task creation (one-shot):

```powershell
$action = New-ScheduledTaskAction `
    -Execute 'powershell.exe' `
    -Argument '-NoProfile -ExecutionPolicy Bypass -File ' +
              '"E:\Projects\QSDM+\apps\qsdmplus-nvidia-ngc\scripts\attest-from-env-file.ps1" ' +
              '-EnvFile "E:\Projects\QSDM+\apps\qsdmplus-nvidia-ngc\ngc.local.env" -Quiet'
$trigger = New-ScheduledTaskTrigger -Once (Get-Date).AddMinutes(1) `
    -RepetitionInterval (New-TimeSpan -Minutes 10)
Register-ScheduledTask -TaskName 'QSDM-NGC-Attest' -Action $action -Trigger $trigger `
    -RunLevel Highest -User $env:USERNAME
```

### Linux VPS (systemd timer)

Used for the primary always-on CPU attestation. One command:

```bash
# From the repo root, with ~/.ssh/id_ed25519 authorized on the VPS:
python QSDM/deploy/install_ngc_sidecar_vps.py
```

The script reads the running `QSDMPLUS_NGC_INGEST_SECRET` out of the
existing `qsdmplus.service` systemd env, writes
`/opt/qsdmplus/ngc-sidecar/ngc.env` (mode 0600), installs
`qsdm-ngc-attest.service` + `.timer`, and starts the timer. Idempotent.

### OCI A1.Flex or E5.Flex (systemd timer, non-root)

Used for the second always-on CPU attestation. OCI images run as
`ubuntu`, not `root`, so this script uses `sudo`-wrapped calls rather
than assuming root login:

```bash
$env:QSDMPLUS_NGC_INGEST_SECRET = '<same secret as BLR1>'
python QSDM/deploy/install_ngc_sidecar_oci.py `
    --host <oci-public-ip> `
    --user ubuntu `
    --node-id qsdmplus-oci-ap-singapore-1
```

## Verifying the new source counts

Five places show the effect, in order of how immediate the feedback is:

1. **Timer log on the new host** (immediate):
   ```bash
   # Linux (VPS / OCI)
   sudo journalctl -u qsdm-ngc-attest.service -n 50 --no-pager
   # Windows
   Get-ScheduledTaskInfo -TaskName QSDM-NGC-Attest
   ```
2. **Ingest counter on the node** (one scrape interval):
   ```bash
   curl -sS -H "X-QSDM-Metrics-Scrape-Secret: $SECRET" \
       http://<node>:8081/api/metrics/prometheus \
     | grep -E '^qsdm_ngc_proof_ingest_(accepted|rejected)_total'
   ```
3. **Trust aggregator gauge** (one refresh tick, default 10 s):
   ```bash
   curl -sS -H "X-QSDM-Metrics-Scrape-Secret: $SECRET" \
       http://<node>:8081/api/metrics/prometheus \
     | grep -E '^qsdm_trust_(attested|total_public|ratio)'
   # qsdm_trust_attested 3
   # qsdm_trust_total_public 4
   # qsdm_trust_ratio 0.75
   ```
4. **Public summary JSON** (same tick):
   ```bash
   curl -sS https://api.qsdm.tech/api/v1/trust/attestations/summary
   # {"attested":3,"total_public":4,"ratio":0.75,...}
   ```
5. **External CI probe** (next schedule or manual dispatch):
   `.github/workflows/trustcheck-external.yml` with `--min-attested 2`
   turns green on the next run — see Actions → *Trust transparency
   external probe*.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `attested` stays at `1` after adding a sidecar | Duplicate `QSDMPLUS_NGC_PROOF_NODE_ID` | Set a distinct id in the new sidecar's `ngc.env`; restart its timer. |
| `qsdm_ngc_proof_ingest_rejected_total{reason="hmac_mismatch"}` climbs | Sidecar's `QSDMPLUS_NGC_INGEST_SECRET` drifted from node's | Re-sync from the node's systemd env; rotate everywhere at once. |
| `qsdm_ngc_proof_ingest_rejected_total{reason="invalid_nonce"}` climbs | Sidecar clock skew > 5 min, or replay protection | `timedatectl set-ntp true`; check for cached old bundles. |
| Timer fires but no POST arrives | Sidecar cannot reach the ingest URL | `curl -v $QSDMPLUS_NGC_REPORT_URL` from the sidecar host; check Caddy/firewall. |
| `attested` = N one minute, `attested=0` the next, oscillating | Only one sidecar, and its cadence > `fresh_within` | Lower cadence to ≤ `fresh_within` / 2, or raise `fresh_within` in `[trust]`. |
| Alert `QSDMTrustAttestationsBelowFloor` fires right after a redeploy | In-memory ring was flushed; sidecars haven't re-posted yet | The alert is gated on `qsdm_trust_warm == 1`, so it should not fire during the first refresh. If it does, one or more sidecars is actually offline — check their timers. |

## Cross-references

- Aggregator implementation: `QSDM/source/pkg/api/handlers_trust.go`
  (`LocalDistinctAttestationSource`) and
  `QSDM/source/pkg/monitoring/ngc_proofs.go`
  (`NGCProofDistinctByNodeID`).
- Prometheus exposure: `QSDM/source/pkg/api/trust_metrics.go`.
- Example alert rules (internal Alertmanager):
  `QSDM/deploy/prometheus/alerts_qsdmplus.example.yml` group
  `qsdm-trust-redundancy`.
- External CI probe: `.github/workflows/trustcheck-external.yml` and
  `QSDM/source/cmd/trustcheck/main.go` (`--min-attested` flag).
- Scope caveat (not a consensus rule): `NVIDIA_LOCK_CONSENSUS_SCOPE.md`.
