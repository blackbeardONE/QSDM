#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib/qsdm-caddy-site.sh
source "${script_dir}/lib/qsdm-caddy-site.sh"

work=$(mktemp -d)
trap 'rm -rf -- "${work}"' EXIT

cat >"${work}/Caddyfile" <<'CADDY'
api.old.example, node.old.example {
	import /etc/caddy/qsdm-edge-relay.caddy
	handle {
		respond "old" 200
	}
}

api.next.example, node.next.example {
	encode zstd gzip
	handle {
		respond "new" 200
	}
}
CADDY

labels="$(qsdm_normalize_caddy_site_labels 'API.NEXT.EXAMPLE, node.next.example')"
[[ "${labels}" == 'api.next.example, node.next.example' ]]
if qsdm_caddy_site_has_import "${work}/Caddyfile" "${labels}"; then
  echo "an import on another Caddy site was treated as the selected site" >&2
  exit 1
fi

qsdm_patch_caddy_site_import "${work}/Caddyfile" "${work}/patched" "${labels}"
[[ $(grep -Fc 'import /etc/caddy/qsdm-edge-relay.caddy' "${work}/patched") -eq 2 ]]
qsdm_caddy_site_has_import "${work}/patched" "${labels}"

if qsdm_patch_caddy_site_import "${work}/Caddyfile" "${work}/wrong-site" \
    'api.missing.example, node.missing.example'; then
  echo "expected a missing Caddy site to fail" >&2
  exit 1
else
  [[ $? -eq 42 ]]
fi

if qsdm_normalize_caddy_site_labels 'api.next.example, https://invalid.example' >/dev/null 2>&1; then
  echo "expected an invalid site label to fail" >&2
  exit 1
fi

installer="${script_dir}/install_edge_relay.sh"
grep -Fq 'QSDM_EDGE_RELAY_CADDY_SITE_LABELS' "${installer}"
if grep -Fq '$0 ~ /^api\.qsdm\.tech, node\.qsdm\.tech' "${installer}"; then
  echo "installer still matches the old fixed Caddy site label" >&2
  exit 1
fi

echo "Edge Relay Caddy site-label tests passed."
