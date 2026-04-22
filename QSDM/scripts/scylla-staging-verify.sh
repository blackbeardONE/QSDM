#!/usr/bin/env bash
# Run scyllasmoke then the optional Scylla integration test against a reachable cluster.
# From repo: bash QSDM/scripts/scylla-staging-verify.sh
# Defaults SCYLLA_HOSTS to 127.0.0.1 if unset. Forwards any SCYLLA_* env (keyspace, TLS, auth).
# Empty single-node cluster: set SCYLLA_AUTO_CREATE_KEYSPACE=1 (see SCYLLA_MIGRATION.md §10); CI sets this.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SOURCE_ROOT="$(cd "$SCRIPT_DIR/../source" && pwd)"
cd "$SOURCE_ROOT"

export SCYLLA_HOSTS="${SCYLLA_HOSTS:-127.0.0.1}"
export SCYLLA_KEYSPACE="${SCYLLA_KEYSPACE:-qsdm}"

echo "=== scylla-staging-verify (hosts=$SCYLLA_HOSTS keyspace=$SCYLLA_KEYSPACE) ==="
echo "1. scyllasmoke"
go run ./cmd/scyllasmoke

echo ""
echo "2. integration test (build tag scylla)"
go test -tags scylla ./pkg/storage/... -run TestIntegrationScyllaRecentTxPath -count=1

echo ""
echo "=== scylla-staging-verify OK ==="
