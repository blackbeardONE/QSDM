# QSDM+ Deployment Guide

**Last Updated:** December 2024

---

## Overview

This directory contains deployment configurations and scripts for **QSDM+**, including Docker Compose and Kubernetes deployments.

---

## Quick Start

### Docker Compose (Recommended for Development)

**Single node (small VPS or laptop):**
```bash
cd deploy   # QSDM/deploy — build context is parent QSDM/
docker compose -f docker-compose.single.yml up -d --build
# API http://localhost:8080  dashboard http://localhost:8081  libp2p :4001  logs :9000
docker compose -f docker-compose.single.yml logs -f
docker compose -f docker-compose.single.yml down -v
```
Optional: set **`BOOTSTRAP_PEERS`** in `docker-compose.single.yml` (or an override file) to comma-separated multiaddrs to join an existing mesh.

**Deploy 3-node cluster:**
```bash
# Linux/Mac
cd deploy
./scripts/deploy.sh --method docker --nodes 3

# Windows
cd deploy
.\scripts\deploy.ps1 -Method docker -NodeCount 3
```

**Access points:**
- Node 1 Dashboard: http://localhost:8081
- Node 1 API: http://localhost:8080
- Node 2 Dashboard: http://localhost:8082
- Node 2 API: http://localhost:8083
- Node 3 Dashboard: http://localhost:8084
- Node 3 API: http://localhost:8085

**Stop cluster:**
```bash
cd deploy
docker-compose -f docker-compose.cluster.yml down
```

---

### Kubernetes (Production)

**Deploy to Kubernetes:**
```bash
cd deploy/kubernetes

# Apply all resources
kubectl apply -f namespace.yaml
kubectl apply -f configmap.yaml
kubectl apply -f secret.yaml
kubectl apply -f pvc.yaml
kubectl apply -f statefulset.yaml
kubectl apply -f service.yaml

# Or use the deployment script
../scripts/deploy.sh --method kubernetes --namespace qsdm
```

**Check status:**
```bash
kubectl get pods -n qsdm
kubectl get svc -n qsdm
```

**Access dashboard:**
```bash
kubectl port-forward -n qsdm svc/qsdm-dashboard 8081:8081
# Then open: http://localhost:8081
```

---

## Files Structure

```
deploy/
├── docker-compose.cluster.yml  # Multi-node Docker Compose
├── kubernetes/                  # Kubernetes manifests
│   ├── namespace.yaml
│   ├── configmap.yaml
│   ├── secret.yaml
│   ├── deployment.yaml
│   ├── service.yaml
│   ├── pvc.yaml
│   └── statefulset.yaml
├── scripts/                     # Deployment scripts
│   ├── deploy.sh
│   ├── deploy.ps1
│   └── undeploy.sh
├── prometheus/                  # Example Prometheus scrape configs (dashboard metrics)
│   ├── README.md
│   ├── scrape_qsdmplus.example.yml
│   ├── prometheus.qsdmplus.example.yml   # minimal full config + rule_files
│   └── alerts_qsdmplus.example.yml
├── grafana/                     # Starter Grafana dashboard (import JSON)
│   ├── README.md
│   ├── qsdmplus-overview.json
│   └── provisioning/datasources/prometheus.example.yml
└── README.md                    # This file
```

---

## Container image (`QSDM/Dockerfile`)

- **Entrypoint:** `qsdmplus` (built from `source/cmd/qsdmplus`).
- **HEALTHCHECK:** `GET http://127.0.0.1:8080/api/v1/health/live` — if you change **`API_PORT`**, override the image healthcheck in Compose/K8s or align **`API_PORT=8080`** in the container.
- **Build** (from repo root that contains the `QSDM` folder):

```bash
docker build -t qsdm:latest -f QSDM/Dockerfile QSDM
```

Helpers: **`QSDM/scripts/docker-build.example.sh`** / **`docker-build.example.ps1`**.

- **Local:** the Docker CLI must reach a running engine (e.g. Docker Desktop on Windows/macOS). The same **`QSDM/Dockerfile`** is built in CI (**`docker-image`** job in **`.github/workflows/qsdmplus-go.yml`**) when workflow paths match.
- **CI publish (GHCR):** On pushes to the repository **default branch** and on **workflow_dispatch**, CI logs into **`ghcr.io`** with **`GITHUB_TOKEN`** and pushes **`ghcr.io/<owner>/qsdmplus`** with tags **`latest`** (default branch only) and **`sha-<full_commit>`**. Pull requests build only (no push). Adjust package visibility under the repo’s **Packages** settings if you need anonymous `docker pull`.
- **Release tags:** Push a semver tag such as **`v1.2.3`** to run **`.github/workflows/release-container.yml`** and publish **`ghcr.io/<owner>/qsdmplus:1.2.3`** (and **`1.2`**, **`1`** aliases).
- **Manifest checks:** **`.github/workflows/validate-deploy.yml`** validates Compose config and Kubernetes YAML with **client dry-run** (no cluster).

---

## Docker Compose Deployment

### Configuration

Edit `docker-compose.cluster.yml` to customize:
- Number of nodes
- Port mappings
- Resource limits
- Environment variables

### Environment Variables

- `NODE_ID` - Unique node identifier
- `NETWORK_PORT` - libp2p network port (default: 4001)
- `API_PORT` - API server port (default: 8080)
- `DASHBOARD_PORT` - Dashboard port (default: 8081)
- `STORAGE_TYPE` - Storage backend (sqlite/scylla)
- `SQLITE_PATH` - SQLite database path
- `LOG_LEVEL` - Logging level (DEBUG/INFO/WARN/ERROR)
- `BOOTSTRAP_PEERS` - Comma-separated list of bootstrap peers
- `API_RATE_LIMIT_MAX` / `API_RATE_LIMIT_WINDOW` (or `QSDMPLUS_API_RATE_LIMIT_*`) — global API rate limit per client window

### Volumes

- `nodeX-data` - Persistent data storage
- `nodeX-logs` - Log files

---

## Kubernetes Deployment

### Prerequisites

- Kubernetes cluster (1.20+)
- kubectl configured
- PersistentVolume storage class

### Deployment Steps

1. **Create namespace:**
   ```bash
   kubectl apply -f kubernetes/namespace.yaml
   ```

2. **Apply configuration:**
   ```bash
   kubectl apply -f kubernetes/configmap.yaml
   kubectl apply -f kubernetes/secret.yaml
   ```
   **`qsdm-config`** must exist before the StatefulSet or Deployment: both inject **`network_port`**, **`api_port`**, **`api_rate_limit_*`**, etc., from that ConfigMap.

3. **Create storage:**
   ```bash
   kubectl apply -f kubernetes/pvc.yaml
   ```

4. **Deploy StatefulSet:**
   ```bash
   kubectl apply -f kubernetes/statefulset.yaml
   ```

5. **Create services:**
   ```bash
   kubectl apply -f kubernetes/service.yaml
   ```

### Scaling

**Scale to 5 nodes:**
```bash
kubectl scale statefulset qsdm-node -n qsdm --replicas=5
```

### Updating

**Update image:**
```bash
kubectl set image statefulset/qsdm-node -n qsdm qsdm=qsdm:new-version
kubectl rollout restart statefulset/qsdm-node -n qsdm
```

---

## Health Checks

### Docker Compose

Health checks use the **HTTP API** (public, no JWT):
- **Docker Compose:** `GET http://localhost:<api-port>/api/v1/health/live` (container internal port **8080**)
- Interval: 30 seconds
- Timeout: 10 seconds
- Retries: 3

### Kubernetes

**Liveness probe:**
- Port: **api** (8080), path: **`/api/v1/health/live`**
- Initial delay: 60 seconds
- Period: 30 seconds
- If the node runs **TLS** on the API, set `scheme: HTTPS` on the probe (and trust or use `tcpSocket` for self-signed).

**Readiness probe:**
- Port: **api**, path: **`/api/v1/health/ready`** (503 when storage `Ready()` fails)
- Initial delay: 30 seconds
- Period: 10 seconds

---

## Monitoring

### Dashboard Access

**Docker Compose:**
- Node 1: http://localhost:8081
- Node 2: http://localhost:8082
- Node 3: http://localhost:8084

**Kubernetes:**
```bash
kubectl port-forward -n qsdm svc/qsdm-dashboard 8081:8081
```

### Metrics

All nodes expose metrics at `/api/metrics`:
```bash
curl http://localhost:8081/api/metrics
```

### Health Status

**Dashboard** (JWT): `GET /api/health` on the dashboard port, e.g. `http://localhost:8081/api/health`.

The dashboard UI includes **2D** and **WebGL 3D** views: live **libp2p** topology (`/api/topology`) and an illustrative **Phase-3 parent mesh** (`/api/mesh3d-viz`). The 3D panels load **Three.js** from **jsDelivr** in the browser; the node must be able to reach `cdn.jsdelivr.net`, or host a mirror and adjust CSP / import map in `internal/dashboard/static/index.html`.

**API** (public): liveness / readiness for ops and probes:
```bash
curl -sS http://localhost:8080/api/v1/health/live
curl -sS http://localhost:8080/api/v1/health/ready
```

---

## Troubleshooting

### Docker Compose

**Check logs:**
```bash
docker-compose -f docker-compose.cluster.yml logs -f qsdm-node1
```

**Restart node:**
```bash
docker-compose -f docker-compose.cluster.yml restart qsdm-node1
```

**Check health:**
```bash
docker-compose -f docker-compose.cluster.yml ps
```

### Kubernetes

**Check pods:**
```bash
kubectl get pods -n qsdm
kubectl describe pod qsdm-node-0 -n qsdm
```

**Check logs:**
```bash
kubectl logs -n qsdm qsdm-node-0
kubectl logs -n qsdm qsdm-node-0 -f
```

**Check events:**
```bash
kubectl get events -n qsdm --sort-by='.lastTimestamp'
```

---

## Production Considerations

### Security

- ✅ Enable TLS (`ENABLE_TLS=true`)
- ✅ Use proper certificates
- ✅ Configure firewall rules
- ✅ Enable authentication on dashboard
- ✅ Use secrets for sensitive data
- ✅ Production: set `[monitoring] strict_dashboard_auth = true` (or `QSDMPLUS_DASHBOARD_STRICT_AUTH`) so the dashboard does not fall back to an open UI if JWT init fails; keep `[monitoring] metrics_scrape_secret` set so Prometheus can still scrape `/api/metrics/prometheus`

### Performance

- ✅ Adjust resource limits based on load
- ✅ Use ScyllaDB for high throughput
- ✅ Configure connection pooling
- ✅ Enable compression

### High Availability

- ✅ Deploy multiple nodes (minimum 3)
- ✅ Use StatefulSet for stable network identities
- ✅ Configure persistent storage
- ✅ Set up monitoring and alerting

---

## Cleanup

### Docker Compose

```bash
cd deploy
docker-compose -f docker-compose.cluster.yml down -v
```

### Kubernetes

```bash
kubectl delete namespace qsdm
```

Or use the undeploy script:
```bash
./scripts/undeploy.sh kubernetes qsdm
```

---

## NGC proof ingest (optional)

When the node has `QSDMPLUS_NGC_INGEST_SECRET` (or legacy `QSDM_NGC_INGEST_SECRET`) set, the API accepts proof bundles from the **`apps/qsdmplus-nvidia-ngc`** sidecar. Operators can view summaries on the monitoring dashboard (**NGC GPU proofs** panel) or via `GET /api/v1/monitoring/ngc-proofs` with header `X-QSDMPLUS-NGC-Secret` (legacy: `X-QSDM-NGC-Secret`).

**NVIDIA-lock (optional):** set `[api] nvidia_lock = true` (or env `QSDMPLUS_NVIDIA_LOCK=true`) and keep ingest secret set. **HTTP scope:** gates routes that persist wallet/token ledger data (`/api/v1/wallet/send`, `/wallet/mint`, `/tokens/mint`, `/tokens/create`); they return **403** unless a **recent** ingested proof has NVIDIA architecture and `gpu_fingerprint.available == true` (use the **GPU** profile sidecar). Tune freshness with `nvidia_lock_max_proof_age` / `QSDMPLUS_NVIDIA_LOCK_MAX_PROOF_AGE`.

**P2P scope (optional, off by default):** set `[api] nvidia_lock_gate_p2p = true` or `QSDMPLUS_NVIDIA_LOCK_GATE_P2P=true` **with** `nvidia_lock` so **libp2p-received** transactions are **dropped after PoE validation** if no qualifying proof is present. This uses the **same proof criteria** as HTTP but does **not** consume ring-buffer rows (HTTP single-use nonce behavior stays separate). It is **node-local storage policy**, not network-wide consensus attestation.

**Proof binding (optional):** set `[api] nvidia_lock_expected_node_id = "your-id"` or `QSDMPLUS_NVIDIA_LOCK_EXPECTED_NODE_ID` so proofs must include JSON string `qsdmplus_node_id` with the same value. On the sidecar set `QSDMPLUS_NGC_PROOF_NODE_ID` (or `QSDM_NGC_PROOF_NODE_ID`) to match.

`GET /api/v1/health` includes `nvidia_lock` (same fields as above, plus `ngc_proof_ingest` counters and `ngc_challenge_*` / `ngc_ingest_nonce_pool_size`). For probes use **`/api/v1/health/live`** and **`/api/v1/health/ready`** (public, on the API port).

**Prometheus scrape (dashboard):** `GET http://<dashboard-host>:<dashboard_port>/api/metrics/prometheus` returns text exposition (`qsdmplus_nvidia_lock_*`, `qsdmplus_ngc_*`, transaction/network counters). Either use a normal **dashboard JWT** (same as `/api/metrics`) or set **`[monitoring] metrics_scrape_secret`** / env **`QSDMPLUS_DASHBOARD_METRICS_SCRAPE_SECRET`** (legacy **`QSDM_DASHBOARD_METRICS_SCRAPE_SECRET`**) and scrape with header **`X-QSDMPLUS-Metrics-Scrape-Secret: <secret>`** or **`Authorization: Bearer <secret>`**. If a scrape secret is configured, a **wrong** Bearer/header value returns **401** (it is not treated as a JWT).

**Many validators, one NAT IP:** `GET /api/v1/monitoring/ngc-challenge` is **15/min per client key** (IP or `X-API-Key`). Sidecars behind the same egress IP should **stagger** polls, lower validator frequency, or use **per-host** challenge URLs so each node has its own rate bucket.

**Prometheus / Grafana:** **`deploy/prometheus/`** (scrape examples) and **`deploy/grafana/`** (import **`qsdmplus-overview.json`** after Prometheus is wired).

**Proof HMAC (optional):** set `nvidia_lock_proof_hmac_secret` / `QSDMPLUS_NVIDIA_LOCK_PROOF_HMAC_SECRET` on the node (requires `nvidia_lock` enabled). The sidecar must set the same value as `QSDMPLUS_NGC_PROOF_HMAC_SECRET` so each bundle includes `qsdmplus_proof_hmac` (HMAC-SHA256 over UTF-8 lines: `v1`, node id, `cuda_proof_hash`, `timestamp_utc`, each line newline-terminated — see `pkg/monitoring/nvidia_hmac.go`). When an ingest nonce is present, the payload version is **`v2`** and appends the nonce line (see the same file).

**Ingest nonce / replay hardening (optional):** set `nvidia_lock_require_ingest_nonce = true` / `QSDMPLUS_NVIDIA_LOCK_REQUIRE_INGEST_NONCE=true` (requires `nvidia_lock` and **`nvidia_lock_proof_hmac_secret`**). The sidecar calls **`GET /api/v1/monitoring/ngc-challenge`** with the same ingest-secret headers; the JSON field `qsdmplus_ingest_nonce` must be embedded in the bundle. Each nonce is **single-use at ingest**; with this mode enabled, each ingested proof also satisfies **at most one** successful NVIDIA-lock-gated API call (the proof row is removed after use). Tune TTL with `nvidia_lock_ingest_nonce_ttl` / `QSDMPLUS_NVIDIA_LOCK_INGEST_NONCE_TTL`. Sidecar: set **`QSDMPLUS_NGC_FETCH_CHALLENGE=true`** (or legacy `QSDM_NGC_FETCH_CHALLENGE`).

**Challenge rate limit:** `GET /api/v1/monitoring/ngc-challenge` is capped at **15 requests per minute per client** (per-IP or `X-API-Key` bucket), separate from the global API rate limit.

**Strict secrets (production):** set `QSDMPLUS_STRICT_SECRETS=true` (or `1` / `yes`) so startup **fails** if any configured secret is shorter than **16** characters or starts with the demo prefix `charming123` (case-insensitive), for non-empty `QSDMPLUS_NGC_INGEST_SECRET`, `nvidia_lock_proof_hmac_secret`, and `QSDMPLUS_JWT_HMAC_SECRET`.

**Non-CGO JWT HMAC:** set `jwt_hmac_secret` or `QSDMPLUS_JWT_HMAC_SECRET` so JWT and request-signing fallbacks use your key instead of the built-in dev default.

Local wiring: `apps/qsdmplus-nvidia-ngc/scripts/wire-qsdmplus.ps1` (Windows) or `wire-qsdmplus.sh` (Linux/macOS; optional 4th arg for HMAC secret). Validators use `extra_hosts: host.docker.internal:host-gateway` so Linux Docker can reach the host API. Then `docker compose up` in `apps/qsdmplus-nvidia-ngc/`.

## ScyllaDB (local dev)

For high-throughput storage experiments, run a single Scylla container and set `storage.type = "scylla"`. See `scripts/scylla-docker-dev.ps1` in the main `QSDM/scripts` folder for a copy-paste recipe and migration pointer.

---

*For more details, see the main project documentation.*

