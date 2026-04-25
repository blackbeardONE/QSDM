#!/usr/bin/env sh
# Set env vars so docker-compose validators POST proof bundles to a local QSDM API.
# Usage: ./scripts/wire-qsdm.sh <api_port> <ngc_ingest_secret> [proof_node_id] [proof_hmac_secret]
# Example: ./scripts/wire-qsdm.sh 8080 "Charming123" "validator-1" "Charming123"
# Then: docker compose up --build  (from apps/qsdm-nvidia-ngc)

API_PORT="${1:-}"
SECRET="${2:-}"
PROOF_NODE="${3:-}"
HMAC_SECRET="${4:-}"

if [ -z "$SECRET" ] || [ -z "$API_PORT" ]; then
	echo "Usage: $0 <api_port> <ngc_ingest_secret> [proof_node_id_optional] [proof_hmac_secret_optional]" >&2
	exit 1
fi

export QSDM_NGC_INGEST_SECRET="$SECRET"
export QSDM_NGC_REPORT_URL="http://host.docker.internal:${API_PORT}/api/v1/monitoring/ngc-proof"
export QSDM_NGC_INGEST_SECRET="$SECRET"
export QSDM_NGC_REPORT_URL="$QSDM_NGC_REPORT_URL"

if [ -n "$PROOF_NODE" ]; then
	export QSDM_NGC_PROOF_NODE_ID="$PROOF_NODE"
	export QSDM_NGC_PROOF_NODE_ID="$PROOF_NODE"
fi
if [ -n "$HMAC_SECRET" ]; then
	export QSDM_NGC_PROOF_HMAC_SECRET="$HMAC_SECRET"
	export QSDM_NGC_PROOF_HMAC_SECRET="$HMAC_SECRET"
fi

echo "QSDM_NGC_REPORT_URL=$QSDM_NGC_REPORT_URL"
echo "QSDM_NGC_INGEST_SECRET=(set)"
if [ -n "$PROOF_NODE" ]; then
	echo "QSDM_NGC_PROOF_NODE_ID=$PROOF_NODE"
fi
if [ -n "$HMAC_SECRET" ]; then
	echo "QSDM_NGC_PROOF_HMAC_SECRET=(set)"
fi
echo "Run: docker compose up --build"
echo ""
echo "Ops checklist (better NGC utilization):"
echo "  - Node: set QSDM_NGC_INGEST_SECRET to the same value (enables POST .../ngc-proof)."
echo "  - NVIDIA-lock: align QSDM_NGC_PROOF_NODE_ID with node QSDM_NVIDIA_LOCK_EXPECTED_NODE_ID when binding."
echo "  - If node uses ingest nonce: set QSDM_NGC_FETCH_CHALLENGE=true on sidecar; optional QSDM_NGC_CHALLENGE_JITTER_MAX_SEC for many validators."
echo "  - GPU proofs: use Dockerfile.ngc / validator-gpu so gpu_fingerprint.available=true in bundles."
echo "  - Metrics: qsdm_ngc_proof_ingest_* on /api/metrics/prometheus (dashboard + alerts)."
