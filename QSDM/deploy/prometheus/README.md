# Prometheus scraping (QSDM+ dashboard)

The node exposes OpenMetrics text at:

`GET http://<dashboard-host>:<dashboard-port>/api/metrics/prometheus`

## Authentication

1. **Scrape secret (recommended for Prometheus)** — Set `[monitoring] metrics_scrape_secret` or `QSDMPLUS_DASHBOARD_METRICS_SCRAPE_SECRET`, then use **Bearer** with that value (see `scrape_qsdmplus.example.yml`).

2. **JWT** — Same token as other dashboard `/api/*` routes if no scrape secret is configured.

3. **Custom header** — `X-QSDMPLUS-Metrics-Scrape-Secret: <secret>` also works when a scrape secret is set.

If a scrape secret is configured, a **wrong** Bearer value returns **401** (it is not interpreted as a JWT).

## Files

| File | Purpose |
|------|---------|
| `scrape_qsdmplus.example.yml` | Paste into `scrape_configs` (adjust target and secret) |
| `prometheus.qsdmplus.example.yml` | Standalone minimal `prometheus.yml` (scrape job + `rule_files`) |
| `alerts_qsdmplus.example.yml` | Example `rule_files` (NVIDIA-lock, **submesh** P2P/API 422, throughput heuristics) |

### Standalone Prometheus

1. Copy `prometheus.qsdmplus.example.yml` and `alerts_qsdmplus.example.yml` into the **same directory**.
2. Replace placeholders (`DASHBOARD_HOST`, `DASHBOARD_PORT`, Bearer secret).
3. Start: `prometheus --config.file=prometheus.qsdmplus.example.yml`

If you already have a `prometheus.yml`, merge in the `rule_files` block and the `qsdmplus-dashboard` job from this example instead.

Series names use the `qsdmplus_` prefix (e.g. `qsdmplus_nvidia_lock_http_blocks_total`, `qsdmplus_transactions_processed_total`, `qsdmplus_submesh_p2p_reject_route_total`, `qsdmplus_submesh_api_wallet_reject_size_total`).

**Grafana:** starter dashboard JSON is in **`../grafana/qsdmplus-overview.json`** (see `../grafana/README.md`).

**Quick check:** from repo root, **`scripts/verify-submesh-metrics.example.sh`** or **`scripts/verify-submesh-metrics.example.ps1`** curls **`/api/metrics/prometheus`** and greps **`qsdmplus_submesh_*`** (set **`METRICS_SECRET`** / Bearer when using **`metrics_scrape_secret`**).

**NGC proof ingest:** **`scripts/verify-ngc-ingest-metrics.example.sh`** / **`.ps1`** greps **`qsdmplus_ngc_proof_ingest_*`** (same auth as above).
