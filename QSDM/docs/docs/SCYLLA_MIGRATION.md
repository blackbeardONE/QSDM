# ScyllaDB migration notes (QSDM+)

Use when upgrading an **existing** keyspace before running a `qsdmplus` build that expects the session 54+ layout (`wallet_tx_id_claim`, materialized views on `transactions`, and valid `transactions` DDL).

Replace `your_keyspace` with your keyspace name (same value as Scylla `keyspace` in config).

## 1. `wallet_tx_id_claim` (LWT dedupe for `tx_id`)

```cql
CREATE TABLE IF NOT EXISTS your_keyspace.wallet_tx_id_claim (
    tx_id text PRIMARY KEY,
    row_uuid uuid,
    inserted_at timestamp
);
```

`StoreTransaction` inserts here with `IF NOT EXISTS` before writing the main row so duplicate wallet IDs do not double-apply balances.

## 2. `transactions` table DDL

New installs use a **single-column** primary key on `id` only (no invalid `CLUSTERING ORDER` without clustering columns).

If you created the table earlier with invalid CQL, fix it in a maintenance window (export data if needed, recreate, restore). For a **greenfield** keyspace, `qsdmplus` `initSchema` will create:

```cql
CREATE TABLE IF NOT EXISTS your_keyspace.transactions (
    id UUID PRIMARY KEY,
    tx_id TEXT,
    data BLOB,
    sender TEXT,
    recipient TEXT,
    amount DOUBLE,
    timestamp TIMESTAMP
);
```

## 3. Secondary indexes (optional but expected by code paths)

```cql
CREATE INDEX IF NOT EXISTS ON your_keyspace.transactions (tx_id);
CREATE INDEX IF NOT EXISTS ON your_keyspace.transactions (sender);
CREATE INDEX IF NOT EXISTS ON your_keyspace.transactions (recipient);
```

## 4. Materialized view `transactions_by_tx_id`

Improves **`GetTransaction` by wallet `id`** (partitioned by `tx_id`, clustered by `id`). Scylla will backfill from existing `transactions` rows when the MV is created.

```cql
CREATE MATERIALIZED VIEW IF NOT EXISTS your_keyspace.transactions_by_tx_id AS
SELECT id, tx_id, data, sender, recipient, amount, timestamp
FROM your_keyspace.transactions
WHERE id IS NOT NULL AND tx_id IS NOT NULL
PRIMARY KEY (tx_id, id);
```

## 5. Materialized views `transactions_by_sender` and `transactions_by_recipient`

Used by **`GetRecentTransactions`**: one query per role (as sender / as recipient), merged in the application. This avoids invalid CQL patterns such as `WHERE sender = ? OR recipient = ?` with a single bind list, and avoids relying on a global `ORDER BY timestamp` without a suitable partition key.

```cql
CREATE MATERIALIZED VIEW IF NOT EXISTS your_keyspace.transactions_by_sender AS
SELECT id, tx_id, data, sender, recipient, amount, timestamp
FROM your_keyspace.transactions
WHERE id IS NOT NULL AND sender IS NOT NULL AND timestamp IS NOT NULL
PRIMARY KEY (sender, timestamp, id)
WITH CLUSTERING ORDER BY (timestamp DESC, id ASC);

CREATE MATERIALIZED VIEW IF NOT EXISTS your_keyspace.transactions_by_recipient AS
SELECT id, tx_id, data, sender, recipient, amount, timestamp
FROM your_keyspace.transactions
WHERE id IS NOT NULL AND recipient IS NOT NULL AND timestamp IS NOT NULL
PRIMARY KEY (recipient, timestamp, id)
WITH CLUSTERING ORDER BY (timestamp DESC, id ASC);
```

Scylla backfills these MVs when they are created. Until they exist, **`GetRecentTransactions` falls back** to secondary-index reads on `transactions` (per-column `WHERE sender = ?` / `WHERE recipient = ?` with a bounded `LIMIT`), then merges and sorts in memory (less strict ordering guarantees than the MV path).

## 6. Apply order

1. Ensure `transactions` matches the expected shape (step 2).
2. Create `wallet_tx_id_claim` (step 1).
3. Create indexes (step 3) if missing.
4. Create `transactions_by_tx_id` (step 4).
5. Create `transactions_by_sender` and `transactions_by_recipient` (step 5).

After this, restart nodes so `NewScyllaStorage` `initSchema` can run idempotently (`IF NOT EXISTS` everywhere).

## 7. Read path fallback (`GetTransaction`)

`GetTransaction` reads **`transactions_by_tx_id` first**; if that query fails (e.g. MV not yet deployed), it **falls back** to `SELECT ... FROM transactions WHERE tx_id = ?` so you can roll out the MV without downtime.

## 8. Repair / rebuild runbook (after bulk backfills or data surgery)

Materialized views stay consistent with the base table under normal writes. After **large historical loads**, **restore from backup**, or **manual row repair** on `transactions`, verify view consistency against your cluster policy.

**Operational checks**

- Confirm MV build state in Scylla Manager or `system_distributed.view_build_status` (depending on version) so new MVs finish backfilling before cutting read traffic that depends on them.
- Watch for **view update backlog** or timeouts during heavy bulk inserts; throttle ingest or temporarily scale writers if the cluster shows pressure.

**Repairs**

- Run **`nodetool repair`** (or **incremental repair** if that is your standard) on the keyspace or relevant tables on a schedule that matches your RF and multi-DC layout. Repairs help replicas of the **base** `transactions` table agree; view replicas are derived from base-table events, but a healthy base repair policy is the foundation after any incident.
- In **multi-datacenter** clusters, follow your playbook for **repair per DC** and **subrange repair** to avoid overlapping full repairs.

**When an MV is wrong or stale beyond repair**

- **Drop and recreate** the MV (`DROP MATERIALIZED VIEW ...` then `CREATE MATERIALIZED VIEW ...` as in steps 4–5). Scylla will rebuild the view from the current `transactions` data. Plan a maintenance window: during rebuild, code paths that read only the MV may be empty or incomplete until the build completes; the application fallbacks (`GetTransaction` on base+index, `GetRecentTransactions` on legacy index reads) reduce but do not remove that window if you force reads before the build finishes.
- After recreation, **re-run repair** if your policy requires it post-schema-change.

**Bulk backfill workflow (suggested)**

1. Pause or throttle writers if possible.
2. Load or repair `transactions` in controlled batches.
3. If you recreated MVs, wait for **view build** to complete.
4. Run **`nodetool repair`** per your schedule.
5. Smoke-test **`GetTransaction`** and **`GetRecentTransactions`** for known wallet addresses.

From a dev machine with Go and cluster reachability, you can run **`go run ./cmd/scyllasmoke`** from the `QSDM/source` module (env: `SCYLLA_HOSTS`, `SCYLLA_KEYSPACE`; optional CQL auth/TLS: same variables as §10; see `scripts/scylla-docker-dev.ps1`). To run **scyllasmoke** and the **`scylla`**-tagged integration test in one step, use **`QSDM/scripts/scylla-staging-verify.ps1`** or **`scylla-staging-verify.sh`** from the repo (defaults `SCYLLA_HOSTS=127.0.0.1` if unset). If **Docker** is available, **`scylla-staging-verify-with-docker.ps1`** / **`.sh`** starts a single-node **scylladb/scylla** container (same tuning as CI), sets **`SCYLLA_AUTO_CREATE_KEYSPACE=1`**, retries until verify succeeds, then removes the container.

## 9. Bulk SQLite → Scylla via `cmd/migrate`

Build **`migrate`** with **CGO enabled** so SQLite export works. From `QSDM/source`:

```bash
go run ./cmd/migrate /path/to/qsdmplus.db 127.0.0.1 qsdm
```

The tool walks **`transactions`** (decrypt/decompress blobs), inserts each row with **`StoreTransactionMigrate`** (no balance deltas), then copies **`balances`** with **`SetBalance`**. Re-runs skip duplicate wallet `tx_id` rows via Scylla LWT. Use on a maintenance window and verify counts before switching production reads. Each phase prints **wall-clock elapsed** plus **total wall time** at the end (useful when benchmarking large SQLite files).

**Dry stats (no Scylla):** `go run ./cmd/migrate -stats-only /path/to/qsdmplus.db` opens SQLite only, counts transactions (same decrypt walk as migrate) and balance rows, and prints timings—useful to size a large file before you point **`migrate`** at a cluster.

## 10. Client TLS and CQL authentication (gocql)

`NewScyllaStorage(hosts, keyspace, extra)` accepts an optional **`extra *ScyllaClusterConfig`** (`pkg/storage`). When `extra` is nil, behavior matches the historical plaintext default (typical local dev). When set, the driver applies **`gocql.PasswordAuthenticator`** if `Username` is non-empty, and **`gocql.SslOptions`** when any TLS option is enabled (CA path, client cert/key pair, or dev-only insecure mode).

### TOML / YAML (`[storage]`)

| Field | Purpose |
|------|---------|
| `scylla_username` | Native protocol user (password authenticator). |
| `scylla_password` | Password for that user. Prefer **`SCYLLA_PASSWORD`** in production so secrets are not committed. |
| `scylla_tls_ca_path` | PEM file: CA bundle to verify the Scylla server certificate. |
| `scylla_tls_cert_path` | PEM client certificate (mutual TLS). **Must** be set together with `scylla_tls_key_path`, or both left empty. |
| `scylla_tls_key_path` | PEM client private key. |
| `scylla_tls_insecure_skip_verify` | **Dev only**: disables server certificate verification when TLS is on; do not use in production. |

### Environment variables

The same knobs can be set via environment ( **`pkg/config`** applies these when loading `qsdmplus` config; **`cmd/migrate`**, **`scyllasmoke`**, **`benchmark`**, **`profiler`** read them only through `ScyllaClusterConfigFromEnv()`):

| Variable | Maps to |
|----------|---------|
| `SCYLLA_HOSTS` | Comma-separated contact points (used by smoke/integration helpers; `qsdmplus` uses config `scylla_hosts`). |
| `SCYLLA_KEYSPACE` | Keyspace name for smoke/tests. |
| `SCYLLA_USERNAME` | CQL username. |
| `SCYLLA_PASSWORD` | CQL password. |
| `SCYLLA_TLS_CA_PATH` | PEM CA bundle path. |
| `SCYLLA_TLS_CERT_PATH` | Client cert PEM path. |
| `SCYLLA_TLS_KEY_PATH` | Client key PEM path. |
| `SCYLLA_TLS_INSECURE_SKIP_VERIFY` | If `true` / `1` / `yes` / `on` (case-insensitive), skips server cert verification (**dev only**). |
| `SCYLLA_AUTO_CREATE_KEYSPACE` | **Dev/CI only:** before opening the app keyspace, runs `CREATE KEYSPACE IF NOT EXISTS` with `SimpleStrategy` / `replication_factor` 1 (keyspace name must match `[a-zA-Z0-9_]+`). Used by **GitHub Actions** `QSDM+ Scylla staging verify` on empty single-node clusters. |

If none of the auth/TLS env vars above are set, `ScyllaClusterConfigFromEnv()` returns nil and the client dials without those options.

### Operations notes

- Align **native transport** port, **RF/DC**, and TLS with your Scylla operator runbook.
- Prefer **secrets management** or env injection for passwords and key material rather than committing them in repo config.
- After enabling TLS on the cluster, verify with **`go run ./cmd/scyllasmoke`** (from `QSDM/source`) using the env table above, or run **`qsdmplus`** with matching `[storage]` / env overrides.
