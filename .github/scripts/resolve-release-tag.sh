#!/usr/bin/env bash

set -euo pipefail

if [[ "${GITHUB_EVENT_NAME:-}" == "workflow_dispatch" ]]; then
  tag="${REQUESTED_TAG:-}"
else
  tag="${GITHUB_REF#refs/tags/}"
fi

if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z][0-9A-Za-z.-]*)?(\+[0-9A-Za-z][0-9A-Za-z.-]*)?$ ]]; then
  echo "invalid release tag: $tag" >&2
  exit 1
fi

if ! git show-ref --verify --quiet "refs/tags/$tag"; then
  echo "release tag does not exist in this repository: $tag" >&2
  exit 1
fi

tag_commit="$(git rev-list -n 1 "$tag")"
head_commit="$(git rev-parse HEAD)"
if [[ "$tag_commit" != "$head_commit" ]]; then
  echo "release tag $tag resolves to $tag_commit, but checkout HEAD is $head_commit" >&2
  exit 1
fi

echo "Validated release tag $tag at $head_commit"
echo "tag=$tag" >> "$GITHUB_OUTPUT"
