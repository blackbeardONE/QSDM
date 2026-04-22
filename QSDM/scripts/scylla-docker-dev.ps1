# One-node Scylla in Docker for local QSDM storage type "scylla".
# Requires Docker. After container is up, point qsdmplus.toml (or qsdm.toml) at 127.0.0.1:9042 (driver uses native protocol).

param(
    [string]$Name = "qsdm-scylla-dev",
    [int]$Port = 9042
)

Write-Host @"

=== Scylla single-node (dev) ===

Start (foreground, remove --rm if you want data to persist without -v):

  docker run --rm --name $Name -p ${Port}:9042 scylladb/scylla

Or detached:

  docker run -d --name $Name -p ${Port}:9042 scylladb/scylla

QSDM+ config (qsdmplus.toml preferred):

  [storage]
  type = "scylla"
  scylla_hosts = ["127.0.0.1"]
  scylla_keyspace = "qsdm"

Env override:

  STORAGE_TYPE=scylla SCYLLA_HOSTS=127.0.0.1 SCYLLA_KEYSPACE=qsdm

Migrate SQLite -> Scylla (when ready):

  go run ./cmd/migrate path\\to\\qsdmplus.db 127.0.0.1 qsdm

Smoke (schema + GetRecentTransactions + missing GetTransaction), from QSDM\\source:

  go run ./cmd/scyllasmoke

Optional: set environment variables SCYLLA_HOSTS (comma-separated) and SCYLLA_KEYSPACE before go run (defaults: 127.0.0.1, qsdm).

Optional integration test (same cluster; build tag scylla; set SCYLLA_HOSTS first):

  go test -tags scylla ./pkg/storage/... -run TestIntegrationScyllaRecentTxPath

Staging check (runs scyllasmoke then the integration test; defaults SCYLLA_HOSTS=127.0.0.1):

  ..\scripts\scylla-staging-verify.ps1

  (Linux/macOS: bash ../scripts/scylla-staging-verify.sh)

One-liner with Docker (starts Scylla, verifies, removes container; needs Docker running):

  ..\scripts\scylla-staging-verify-with-docker.ps1

  (Linux/macOS/Git Bash: bash ../scripts/scylla-staging-verify-with-docker.sh)

CQL TLS / password auth: set SCYLLA_USERNAME, SCYLLA_PASSWORD, SCYLLA_TLS_* (see SCYLLA_MIGRATION.md §10).

First-time empty cluster: set SCYLLA_AUTO_CREATE_KEYSPACE=1 so qsdmplus/scyllasmoke can CREATE KEYSPACE (dev/CI only; see SCYLLA_MIGRATION.md §10).

CI: GitHub Actions workflow "QSDM+ Scylla staging verify" (.github/workflows/qsdmplus-scylla-staging.yml).

See: pkg/storage/scylla.go, cmd/migrate, cmd/scyllasmoke, docs/docs/SCYLLA_MIGRATION.md

"@ -ForegroundColor Cyan
