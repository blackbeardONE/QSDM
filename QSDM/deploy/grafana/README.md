# Grafana — QSDM+ starter dashboard

## Import

1. Add a **Prometheus** data source pointing at your Prometheus server (which scrapes `deploy/prometheus/scrape_qsdmplus.example.yml` or `prometheus.qsdmplus.example.yml`).
2. In Grafana: **Dashboards → New → Import** → upload **`qsdmplus-overview.json`**.
3. When asked for **DS_PROMETHEUS**, pick that Prometheus data source.

### Optional: file provisioning (fixed datasource UID)

To avoid the import picker and match panel UIDs explicitly:

1. Copy **`provisioning/datasources/prometheus.example.yml`** into Grafana’s provisioning folder (e.g. `/etc/grafana/provisioning/datasources/prometheus.yml`). Edit `url:` to your Prometheus (`http://localhost:9090`, `http://prometheus:9090` in Docker, etc.).
2. The example sets datasource **`uid: prometheus`**. Either choose that datasource in the import UI, or in the dashboard JSON replace every `"uid": "${DS_PROMETHEUS}"` with `"uid": "prometheus"` and remove the top-level **`__inputs`** array before import.

### Troubleshooting

- **All panels empty:** Prometheus has no data (check scrape targets / Bearer secret on the node’s `metrics_scrape_secret`) or the wrong datasource was selected at import.
- **Strict dashboard auth:** If the node uses `strict_dashboard_auth` and JWT init failed, UI routes may return 503; scraping still works when `metrics_scrape_secret` matches.

## Metrics

Panels use the `qsdmplus_*` series from **`GET /api/metrics/prometheus`** on the node dashboard (see `deploy/prometheus/README.md`). Includes **submesh** stats (`qsdmplus_submesh_*`) when submesh profiles are in use.

Adjust refresh interval and time range in the UI as needed.
