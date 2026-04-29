# Prometheus scraping (QSDM dashboard)

The node exposes OpenMetrics text at:

`GET http://<dashboard-host>:<dashboard-port>/api/metrics/prometheus`

## Authentication

1. **Scrape secret (recommended for Prometheus)** — Set `[monitoring] metrics_scrape_secret` or `QSDM_DASHBOARD_METRICS_SCRAPE_SECRET`, then use **Bearer** with that value (see `scrape_qsdm.example.yml`).

2. **JWT** — Same token as other dashboard `/api/*` routes if no scrape secret is configured.

3. **Custom header** — `X-QSDM-Metrics-Scrape-Secret: <secret>` also works when a scrape secret is set.

If a scrape secret is configured, a **wrong** Bearer value returns **401** (it is not interpreted as a JWT).

## Files

| File | Purpose |
|------|---------|
| `scrape_qsdm.example.yml` | Paste into `scrape_configs` (adjust target and secret) |
| `prometheus.qsdm.example.yml` | Standalone minimal `prometheus.yml` (scrape job + `rule_files`) |
| `alerts_qsdm.example.yml` | Example `rule_files` (NVIDIA-lock, **submesh** P2P/API 422, throughput heuristics, **v2-mining** slashing/enrollment/liveness) |

### Standalone Prometheus

1. Copy `prometheus.qsdm.example.yml` and `alerts_qsdm.example.yml` into the **same directory**.
2. Replace placeholders (`DASHBOARD_HOST`, `DASHBOARD_PORT`, Bearer secret).
3. Start: `prometheus --config.file=prometheus.qsdm.example.yml`

If you already have a `prometheus.yml`, merge in the `rule_files` block and the `qsdm-dashboard` job from this example instead.

Series names use the `qsdm_` prefix (e.g. `qsdm_nvidia_lock_http_blocks_total`, `qsdm_transactions_processed_total`, `qsdm_submesh_p2p_reject_route_total`, `qsdm_submesh_api_wallet_reject_size_total`).

**Grafana:** starter dashboard JSON is in **`../grafana/qsdm-overview.json`** (see `../grafana/README.md`).

**Quick check:** from repo root, **`scripts/verify-submesh-metrics.example.sh`** or **`scripts/verify-submesh-metrics.example.ps1`** curls **`/api/metrics/prometheus`** and greps **`qsdm_submesh_*`** (set **`METRICS_SECRET`** / Bearer when using **`metrics_scrape_secret`**).

**NGC proof ingest:** **`scripts/verify-ngc-ingest-metrics.example.sh`** / **`.ps1`** greps **`qsdm_ngc_proof_ingest_*`** (same auth as above).

## v2 mining alert groups

`alerts_qsdm.example.yml` contains rule groups for the v2 NVIDIA-locked mining protocol:

| Group | Series consumed | Pages on |
|-------|-----------------|----------|
| `qsdm-v2-mining-slashing` | `qsdm_slash_*` | applied slash (warning), >50 CELL drained / 15m (critical), rejection burst (warning), auto-revoke burst (critical) |
| `qsdm-v2-mining-enrollment` | `qsdm_enrollment_*`, `qsdm_unenrollment_*` | empty registry after warm-up, fast shrink (>25%/1h), pending-unbond majority, rejection burst, bonded-dust drop (>50 CELL/30m) |
| `qsdm-v2-mining-liveness` | `qsdm_chain_height`, `qsdm_mempool_size` | chain height stuck (critical), mempool >10k for 10m |
| `qsdm-v2-attest-archspoof` | `qsdm_attest_archspoof_rejected_total{reason}` | unknown-arch burst (warning), HMAC `gpu_name_mismatch` (warning), CC `cc_subject_mismatch` (critical, single fire) |
| `qsdm-v2-attest-hashrate` | `qsdm_attest_hashrate_rejected_total{arch}` | per-arch out-of-band burst — single rule covers all five canonical arches via `{{ $labels.arch }}` |
| `qsdm-v2-governance` | `qsdm_gov_authority_*`, `qsdm_gov_authority_count` | vote recorded (info, FYI ping), threshold crossed (warning), AuthorityList size <2 (critical) |

Thresholds (50 CELL drained, 25% shrink, 10k mempool depth, 0.05–0.1 rejections/sec) are calibrated for a small-to-medium fleet. Tune per environment after observing one week of baseline. Every rule carries `subsystem: v2-mining`, `subsystem: v2-attest`, or `subsystem: v2-governance` so Alertmanager can route the three families to dedicated channels without rewriting expressions.

**v1-only deployments:** drop the `qsdm-v2-mining-*`, `qsdm-v2-attest-*`, and `qsdm-v2-governance` groups — `qsdm_enrollment_active_count` legitimately stays at 0 on a v1 node, the attest counters never increment, and `qsdm_gov_authority_count` is also zero by construction. Loading these groups on a v1 node would page indefinitely.

### CC-subject mismatch is intentionally critical

The `QSDMAttestArchSpoofCCSubjectMismatch` alert pages on a **single** increment of `qsdm_attest_archspoof_rejected_total{reason="cc_subject_mismatch"}` over 15 minutes — much louder than the `unknown_arch` and `gpu_name_mismatch` siblings. Rationale: to reach this rejection branch the proof has already passed the cert-chain pin (genesis-trusted NVIDIA CA root) AND the AIK ECDSA signature, so a non-zero increment means an operator with an NVIDIA-issued AIK is submitting a proof whose leaf cert subject contradicts the claimed `gpu_arch`. That's either a fabricated AIK leaf (security event) or a serious operator misconfiguration; both warrant immediate attention.

**CI smoke test:** `.github/workflows/validate-deploy.yml` runs `promtool check rules` against this file on every push that touches `QSDM/deploy/prometheus/**`.
