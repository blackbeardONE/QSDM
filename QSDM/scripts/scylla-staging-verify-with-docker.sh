#!/usr/bin/env bash
# Start a single-node Scylla in Docker (same flags as CI), wait until staging verify passes, then remove the container.
# Requires Docker. From monorepo root: bash QSDM/scripts/scylla-staging-verify-with-docker.sh
# Optional: SCYLLA_STAGING_CONTAINER_NAME (default qsdm-scylla-local), SCYLLA_IMAGE (default scylladb/scylla:6.2).

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NAME="${SCYLLA_STAGING_CONTAINER_NAME:-qsdm-scylla-local}"
IMAGE="${SCYLLA_IMAGE:-scylladb/scylla:6.2}"

cleanup() {
  docker rm -f "$NAME" 2>/dev/null || true
}
trap cleanup EXIT

docker rm -f "$NAME" 2>/dev/null || true

echo "=== Starting $IMAGE as $NAME (9042) ==="
docker run -d --name "$NAME" -p 9042:9042 "$IMAGE" \
  --smp 1 --memory 750M --overprovisioned 1 --developer-mode 1

export SCYLLA_HOSTS="${SCYLLA_HOSTS:-127.0.0.1}"
export SCYLLA_KEYSPACE="${SCYLLA_KEYSPACE:-qsdm}"
export SCYLLA_AUTO_CREATE_KEYSPACE="${SCYLLA_AUTO_CREATE_KEYSPACE:-1}"

ok=0
for i in $(seq 1 90); do
  if bash "$SCRIPT_DIR/scylla-staging-verify.sh"; then
    ok=1
    break
  fi
  echo "staging verify attempt $i/90 failed, sleeping 5s..."
  sleep 5
done

if [ "$ok" != 1 ]; then
  echo "Scylla staging verify did not succeed within retry budget" >&2
  exit 1
fi

echo "=== scylla-staging-verify-with-docker OK ==="
