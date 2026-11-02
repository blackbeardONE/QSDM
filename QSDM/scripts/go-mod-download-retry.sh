#!/usr/bin/env bash
# Retry transient module-proxy failures without retrying build or test failures.
set -euo pipefail

attempts="${QSDM_GO_DOWNLOAD_ATTEMPTS:-4}"
delay="${QSDM_GO_DOWNLOAD_RETRY_DELAY_SECONDS:-5}"

if ! [[ "$attempts" =~ ^[1-9][0-9]*$ ]] || ! [[ "$delay" =~ ^[0-9]+$ ]]; then
	printf 'Invalid Go download retry settings: attempts=%s delay=%s\n' "$attempts" "$delay" >&2
	exit 2
fi

for ((attempt = 1; attempt <= attempts; attempt++)); do
	if go mod download; then
		exit 0
	fi

	if ((attempt == attempts)); then
		break
	fi

	printf 'go mod download failed (attempt %d/%d); retrying in %ds\n' \
		"$attempt" "$attempts" "$delay" >&2
	sleep "$delay"
	delay=$((delay * 2))
done

printf 'go mod download failed after %d attempts\n' "$attempts" >&2
exit 1
