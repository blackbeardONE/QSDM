#!/usr/bin/env bash
# GPU stack: gossip + validator-cpu + validator-gpu (NGC PyTorch / Dockerfile.ngc).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
if [[ "${1:-}" == "--build-only" ]]; then
  exec docker compose --profile gpu build
elif [[ "${1:-}" == "-d" ]] || [[ "${1:-}" == "--detach" ]]; then
  exec docker compose --profile gpu up --build -d
else
  exec docker compose --profile gpu up --build
fi
