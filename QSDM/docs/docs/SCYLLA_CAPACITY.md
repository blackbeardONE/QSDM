# ScyllaDB Capacity & Operator Runbook

Companion to `SCYLLA_MIGRATION.md`. This document captures the operational
guidance needed to validate a real-cluster Scylla adoption: capacity sizing,
migration pacing, TLS/auth validation, repair cadence, and rollback.

---

## 0. Node-role constraint (Major Update)

QSDM validator nodes (`node.role = "validator"`) MUST NOT be provisioned on
GPU-enabled hardware. This is a **hard operational constraint**, not a
preference:

- Validators run BFT + Proof-of-Entanglement consensus, which is CPU-only by
  design. Adding a GPU to a validator host buys zero consensus throughput
  and introduces meaningful attack surface (proprietary driver stack,
  undocumented firmware, side-channel surfaces between mining and consensus
  processes).
- The network's public trust signal (NVIDIA NGC attestation, surfaced on
  https://qsdm.tech/trust) is intentionally *optional* and applied only to
  the GPU-side mining path. Co-locating a miner on a validator host breaks
  the "CPU-only validator" statement that the project makes on its landing
  page and in `QSDM/docs/docs/NODE_ROLES.md`.
- The validator-only Docker image (`Dockerfile.validator`) is built with
  `-tags validator_only`, which strips the CUDA mining path from the binary
  at link time and enforces `node.role == "validator"` at process startup
  (see `pkg/mining/roleguard`). Scylla-adjacent infrastructure — the QSDM
  processes talking CQL to the cluster — runs inside this validator image.

Operators deploying a Scylla-backed QSDM cluster on Kubernetes should apply
the label `qsdm.tech/node-class=cpu` to any node hosting a validator pod and
MUST NOT apply that label to any node that advertises
`nvidia.com/gpu.product`. The provided
`deploy/kubernetes/validator-statefulset.yaml` manifest enforces both
conditions via `nodeSelector` and a `DoesNotExist` node-affinity rule; do not
remove those guards when adapting the manifest to your cluster.

Miner nodes (`node.role = "miner"`) are deployed separately via
`deploy/kubernetes/miner-daemonset.yaml` and MUST sit behind a network
boundary (different node pool, ideally different subnet) from the validator
Scylla write path.

---

## 1. Node sizing heuristics

QSDM writes two hot tables plus three materialized views:

| Table / MV | Partition key | Rows (per tx) | Notes |
|------------|---------------|---------------|-------|
| `transactions` | `id` (uuid) | 1 | base write; small row |
| `wallet_tx_id_claim` | `tx_id` (text) | 1 (LWT) | `INSERT ... IF NOT EXISTS` — Paxos round |
| `transactions_by_tx_id` (MV) | wallet `tx_id` | 1 | read-path hot MV |
| `transactions_by_sender` (MV) | `sender_address` | 1 | recent-tx read |
| `transactions_by_recipient` (MV) | `recipient_address` | 1 | recent-tx read |
| `balances` | `address` | 0..1 | only `SetBalance` |

Each successful wallet transaction therefore produces **~5 write amplifications**
(1 base + 1 LWT + 3 MV fan-out). Capacity planning should budget for write
throughput ≈ 5× logical TPS, plus a headroom factor of 2× for compaction and
repair traffic.

**Starting point for a staging cluster**

- 3 nodes, RF=3 (NetworkTopologyStrategy, single DC)
- 8 vCPU, 32 GiB RAM, 500 GiB NVMe per node
- `scylladb/scylla:6.2` with `--developer-mode 0` (production tuning)
- Reserve ≥ 40% of disk free for compaction throughput

**Baseline targets** (to validate with `cmd/migrate -stats-only` against your
real SQLite data, then a full migrate):

| Logical TPS | Write TPS (×5) | Min nodes | Notes |
|-------------|----------------|-----------|-------|
| 500 | 2,500 | 3 | comfortable at default settings |
| 2,000 | 10,000 | 3–5 | tune `commitlog_sync_period_in_ms`, enable encryption |
| 10,000 | 50,000 | 6+ | shard-aware routing strictly required; LWT becomes hot |

LWT in `wallet_tx_id_claim` is the first bottleneck at scale. If you expect
sustained > 5k logical TPS, evaluate sharding `tx_id` by a salt prefix or
switching to a non-LWT idempotency strategy after a production review.

---

## 2. Pre-migration dry-run

1. **Inventory the source** with the SQLite-only mode:

    ```bash
    cd QSDM/source
    go build -o migrate ./cmd/migrate
    ./migrate -stats-only ./qsdm.db
    ```

    Output includes the number of stored transactions and balance rows plus the
    phase/total wall-clock — use this to project Scylla write volume and
    migration window.

2. **Validate connectivity** (TLS + CQL auth end-to-end):

    ```bash
    export SCYLLA_HOSTS=scylla-0.example.com,scylla-1.example.com
    export SCYLLA_KEYSPACE=qsdm
    export SCYLLA_USERNAME=qsdm
    export SCYLLA_PASSWORD=... # from secret manager
    export SCYLLA_TLS_CA_PATH=/etc/qsdm/scylla-ca.pem
    export SCYLLA_TLS_CERT_PATH=/etc/qsdm/client.pem  # optional mTLS
    export SCYLLA_TLS_KEY_PATH=/etc/qsdm/client.key
    bash QSDM/scripts/scylla-staging-verify.sh
    ```

    The script runs `cmd/scyllasmoke` (schema init, `Ready()`, empty reads) and
    then `go test -tags scylla ./pkg/storage/... -run TestIntegrationScyllaRecentTxPath`.

3. **Capacity ceiling test** — fire a synthetic migration against staging using
    a redacted copy of production SQLite:

    ```bash
    ./migrate \
        -source ./qsdm.copy.db \
        -scylla-hosts $SCYLLA_HOSTS \
        -scylla-keyspace $SCYLLA_KEYSPACE
    ```

    Record phase elapsed in the migrate output. Expected ratios on the
    baseline 3-node cluster above:

    | Metric | Target | Warning |
    |--------|--------|---------|
    | SQLite iterate TPS | ≥ 20k rows/sec | < 5k indicates IO bottleneck |
    | Scylla ingest TPS | ≥ 2k tx/sec | < 500 indicates LWT contention |
    | p95 write latency | ≤ 25 ms | > 100 ms investigate hot shard |

---

## 3. Repair / rebuild cadence

MVs in Scylla are eventually consistent. After any bulk backfill, run:

```bash
# On each node:
nodetool repair -pr qsdm
nodetool repair -pr qsdm transactions
nodetool repair -pr qsdm transactions_by_tx_id
nodetool repair -pr qsdm transactions_by_sender
nodetool repair -pr qsdm transactions_by_recipient
```

Schedule weekly primary-range repair during off-peak. After adding a new node
or rebuilding a downed one, always run `nodetool rebuild` before letting it
serve reads.

---

## 4. TLS / auth validation checklist

- [ ] `SCYLLA_TLS_CA_PATH` points at the issuing CA; `TLSInsecureSkipVerify`
      is **off** in production (`SCYLLA_TLS_INSECURE_SKIP_VERIFY` unset).
- [ ] Client certificates, when used, are rotated before expiry (monitor with
      `openssl x509 -enddate -noout -in client.pem`).
- [ ] `SCYLLA_USERNAME` / `SCYLLA_PASSWORD` come from a secret manager; never
      committed to config files.
- [ ] `SCYLLA_AUTO_CREATE_KEYSPACE` is **unset** in production; keyspaces are
      created explicitly with the correct replication topology.
- [ ] `audit/crypto-05` (mTLS CA/CN/SAN validation) is signed off in the audit
      report (see `cmd/auditreport`).
- [ ] Run `bash QSDM/scripts/security-local-check.sh` to confirm
      `go mod verify` and `govulncheck` are clean before promotion.

---

## 5. Observability

Enable QSDM Prometheus exposition (JSON and text) and scrape these series.
Metric names are given under the canonical `qsdm_*` prefix; during the
dual-emit deprecation window (Major Update §6) the same samples are also
published under the legacy `qsdm_*` prefix, so either name resolves:

| Series | Meaning |
|--------|---------|
| `qsdm_tx_total` | logical transactions processed |
| `qsdm_scylla_store_duration_seconds` | server-side write latency |
| `qsdm_scylla_dedupe_skip_total` | duplicate `tx_id` claims skipped |
| `qsdm_p2p_wallet_ingress_dedupe_skip_total` | cross-transport dedupe hits |

Alert suggestions (see `QSDM/deploy/prometheus/alerts_qsdm.example.yml`
— note: the example file and the rules inside it still use the legacy
`qsdm_*` prefix on purpose, so operators who already pasted it into
their Prometheus do not have to re-paste mid-migration):

- p95 `qsdm_scylla_store_duration_seconds` above 100 ms for 5m.
- `qsdm_scylla_dedupe_skip_total` increasing > 1 Hz for 10m (possible
  replay attack or relay loop).

---

## 6. Rollback plan

1. Stop all QSDM writers (scale `qsdm` deployments — or the legacy `qsdm` name — to zero or restart
   nodes in read-only mode — see admin API `/api/v1/admin/readonly`).
2. Snapshot Scylla with `nodetool snapshot qsdm`.
3. Repoint QSDM at the SQLite backup (set `[storage] backend = "sqlite"` in
   the TOML config or switch the config map / secret).
4. Resume writers against SQLite; confirm `/api/v1/health/ready` is green.
5. Investigate the Scylla issue in the background; re-run `cmd/migrate` once
   mitigations are in place.

Keep the last SQLite snapshot for at least one migration window (default:
2×`FinalityDepth` in blocks, or 24h — whichever is longer).

---

## 7. Pre-production sign-off

A Scylla deployment is considered production-ready when all of the following
are true:

- [ ] `scylla-staging-verify-with-docker.*` passes locally and in CI
      (`qsdm-scylla-staging.yml` green on `main`).
- [ ] `cmd/migrate` has completed at least one full dry-run against a copy of
      production data and the timing figures meet the targets in §2.
- [ ] `cmd/auditreport -gate` exits 0 for the current checklist state
      (i.e. no critical/high items pending or failed).
- [ ] Repair + backup cadence is scheduled and alerting is wired.
- [ ] Rollback steps in §6 have been rehearsed with the on-call team.
