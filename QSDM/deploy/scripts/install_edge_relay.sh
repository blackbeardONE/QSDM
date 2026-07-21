#!/usr/bin/env bash
set -euo pipefail

relay_version="${QSDM_EDGE_RELAY_VERSION:-1.3.7}"
relay_sha256="${QSDM_EDGE_RELAY_SHA256:-ecb0e3ed148880d6fbe0d3d9835b3c500273de0fe8f0602ce1d67844095e64bd}"
source_binary="${QSDM_EDGE_RELAY_SOURCE:-/var/www/qsdm/downloads/qsdm-edge-agent-${relay_version}-linux-x86_64}"
install_root="/opt/qsdm-edge"
config_root="/etc/qsdm-edge"
state_root="/var/lib/qsdm-edge"
service_name="qsdm-edge-relay"
service_user="qsdm-edge"
caddyfile="/etc/caddy/Caddyfile"
caddy_route="/etc/caddy/qsdm-edge-relay.caddy"
caddy_backup=""
caddy_route_backup=""
caddy_route_existed=false

if [[ "${EUID}" -ne 0 ]]; then
  echo "install_edge_relay.sh must run as root" >&2
  exit 77
fi

for command in caddy install runuser sha256sum systemctl useradd; do
  command -v "${command}" >/dev/null
done
test -f "${source_binary}"
test -f "${caddyfile}"

actual_sha256="$(sha256sum "${source_binary}" | awk '{print $1}')"
if [[ "${actual_sha256}" != "${relay_sha256}" ]]; then
  echo "refusing Relay binary with unexpected SHA-256" >&2
  exit 65
fi

if ! getent passwd "${service_user}" >/dev/null; then
  useradd --system --home-dir "${state_root}" --shell /usr/sbin/nologin "${service_user}"
fi

install -d -o root -g root -m 0755 "${install_root}"
install -d -o "${service_user}" -g "${service_user}" -m 0700 "${config_root}" "${state_root}"
install -o root -g root -m 0755 "${source_binary}" "${install_root}/qsdm-edge-agent"

if ! "${install_root}/qsdm-edge-agent" version | grep -Fq "qsdm-edge-agent ${relay_version}"; then
  echo "installed Relay binary reports an unexpected version" >&2
  exit 65
fi

for role in agent mother; do
  token_file="${config_root}/${role}.token"
  if [[ ! -s "${token_file}" ]]; then
    runuser -u "${service_user}" -- \
      "${install_root}/qsdm-edge-agent" token --out "${token_file}" >/dev/null
  fi
  chown "${service_user}:${service_user}" "${token_file}"
  chmod 0600 "${token_file}"
done

cat >"/etc/systemd/system/${service_name}.service" <<EOF
[Unit]
Description=QSDM authenticated Agent and Mother Hive Relay
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${service_user}
Group=${service_user}
UMask=0077
ExecStart=${install_root}/qsdm-edge-agent relay --listen 127.0.0.1:7740 --agent-token-file ${config_root}/agent.token --mother-token-file ${config_root}/mother.token --state-dir ${state_root} --id canonical-production-relay --cpu-percent 50 --gpu-percent 40 --ram-percent 25 --max-verifications 2
Restart=always
RestartSec=5s
TimeoutStopSec=20s
NoNewPrivileges=true
PrivateDevices=true
PrivateTmp=true
ProtectClock=true
ProtectControlGroups=true
ProtectHome=true
ProtectHostname=true
ProtectKernelLogs=true
ProtectKernelModules=true
ProtectKernelTunables=true
ProtectSystem=strict
ReadOnlyPaths=${config_root}
ReadWritePaths=${state_root}
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
RestrictNamespaces=true
RestrictRealtime=true
RestrictSUIDSGID=true
LockPersonality=true
MemoryDenyWriteExecute=true
CapabilityBoundingSet=
AmbientCapabilities=
SystemCallArchitectures=native
LimitNOFILE=4096
MemoryMax=1G
CPUQuota=200%

[Install]
WantedBy=multi-user.target
EOF

if [[ -f "${caddy_route}" ]]; then
  caddy_route_existed=true
  caddy_route_backup="$(mktemp /etc/caddy/qsdm-edge-relay.before.XXXXXX)"
  cp -p "${caddy_route}" "${caddy_route_backup}"
fi

cat >"${caddy_route}.new" <<'EOF'
# Authenticated QSDM Agent/Relay protocol. Core remains under /api/v1/*.
@qsdm_edge_relay path /v1/*
handle @qsdm_edge_relay {
	request_body {
		max_size 64KB
	}
	reverse_proxy 127.0.0.1:7740 {
		header_up X-Forwarded-Proto https
		header_up X-Real-IP {remote_host}
	}
}
EOF
install -o root -g root -m 0644 "${caddy_route}.new" "${caddy_route}"
rm -f "${caddy_route}.new"

if ! grep -Fq 'import /etc/caddy/qsdm-edge-relay.caddy' "${caddyfile}"; then
  caddy_backup="$(mktemp /etc/caddy/Caddyfile.before-edge-relay.XXXXXX)"
  cp -p "${caddyfile}" "${caddy_backup}"
  patched_caddyfile="$(mktemp /etc/caddy/Caddyfile.edge-relay.XXXXXX)"
  awk '
    { print }
    $0 ~ /^api\.qsdm\.tech, node\.qsdm\.tech \{[[:space:]]*$/ { in_public_api = 1; next }
    in_public_api && $0 ~ /^[[:space:]]*encode[[:space:]]/ {
      print "\timport /etc/caddy/qsdm-edge-relay.caddy"
      in_public_api = 0
      inserted = 1
    }
    END { if (!inserted) exit 42 }
  ' "${caddyfile}" >"${patched_caddyfile}"
  install -o root -g root -m 0644 "${patched_caddyfile}" "${caddyfile}.new"
  rm -f "${patched_caddyfile}"
  mv -f "${caddyfile}.new" "${caddyfile}"
fi

if ! caddy validate --config "${caddyfile}" --adapter caddyfile; then
  if [[ -n "${caddy_backup}" ]]; then
    install -o root -g root -m 0644 "${caddy_backup}" "${caddyfile}"
  fi
  if [[ "${caddy_route_existed}" == true ]]; then
    install -o root -g root -m 0644 "${caddy_route_backup}" "${caddy_route}"
  else
    rm -f "${caddy_route}"
  fi
  echo "Caddy validation failed; the previous configuration was restored" >&2
  exit 1
fi
rm -f "${caddy_backup}"
rm -f "${caddy_route_backup}"

systemctl daemon-reload
systemctl enable --now "${service_name}.service"
if grep -Eq '^[[:space:]]*admin[[:space:]]+off([[:space:]]|$)' "${caddyfile}"; then
  # Caddy's systemd ExecReload talks to the admin API. This installation has
  # that API intentionally disabled, so a validated restart is required.
  systemctl restart caddy
else
  systemctl reload caddy
fi

for attempt in {1..20}; do
  if "${install_root}/qsdm-edge-agent" status \
      --relay http://127.0.0.1:7740 \
      --mother-token-file "${config_root}/mother.token" \
      --worker-id relay-install-health >/dev/null 2>&1; then
    echo "QSDM Edge Relay ${relay_version} is active on loopback and routed through node.qsdm.tech /v1/*."
    exit 0
  fi
  sleep 1
done

echo "Relay service did not become healthy" >&2
systemctl status "${service_name}.service" --no-pager -l >&2 || true
exit 1
