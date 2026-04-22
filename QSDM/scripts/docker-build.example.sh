#!/usr/bin/env bash
# Build the QSDM+ image (CGO + liboqs in Dockerfile). Run from anywhere.
# Usage: bash QSDM/scripts/docker-build.example.sh [extra docker build args...]
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# Context must be last; pass extra docker build flags before it (e.g. --no-cache --build-arg).
exec docker build -t qsdm:latest -f "$ROOT/Dockerfile" "$@" "$ROOT"
