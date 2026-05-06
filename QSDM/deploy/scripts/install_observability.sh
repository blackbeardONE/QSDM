#!/bin/bash
#
# Observability stack installer for a QSDM dashboard host.
#
# Lays down a native systemd-managed Prometheus + Alertmanager + Grafana
# stack scraping the local qsdm.service on 127.0.0.1:8081 with Bearer
# auth, and provisions all the runbook dashboards from
# QSDM/deploy/grafana/dashboards/.
#
# Idempotent: skips already-installed components on re-run.
#
# Layout (on the deploy host):
#   /usr/local/bin/{prometheus,promtool,alertmanager,amtool}
#   /etc/prometheus/{prometheus.yml,alerts_qsdm.yml}
#   /etc/alertmanager/{alertmanager.yml}            (no-receiver default;
#                                                    full example preserved at
#                                                    ${DEPLOY_DIR}/alertmanager/)
#   /etc/grafana/provisioning/datasources/prometheus.yml
#   /etc/grafana/provisioning/dashboards/qsdm-runbooks.yaml
#   /var/lib/grafana/dashboards/qsdm/*.json
#   /etc/systemd/system/{prometheus,alertmanager}.service
#
# All three bind 127.0.0.1 only. Reach via SSH tunnel:
#   ssh -L 9090:127.0.0.1:9090 -L 9093:127.0.0.1:9093 -L 3000:127.0.0.1:3000 host
#
# Assumptions (must hold before running):
#   - qsdm.service is installed and running with secrets at
#     /etc/systemd/system/qsdm.service.d/secrets.conf containing
#     QSDM_DASHBOARD_METRICS_SCRAPE_SECRET=<value>
#   - Ubuntu/Debian host with apt and systemd
#   - Run as root (or via sudo)
#
# Usage:
#   sudo bash QSDM/deploy/scripts/install_observability.sh
#
# Override the source deploy tree (e.g. when running from a snapshot
# instead of the repo checkout):
#   DEPLOY_DIR=/opt/qsdm-deploy sudo bash install_observability.sh
#
# Override the instance label used in Prometheus external_labels:
#   QSDM_INSTANCE=validator-blr1 sudo bash install_observability.sh

set -euo pipefail

PROM_VERSION="${PROM_VERSION:-2.55.1}"
AM_VERSION="${AM_VERSION:-0.27.0}"
QSDM_INSTANCE="${QSDM_INSTANCE:-validator}"
QSDM_CLUSTER="${QSDM_CLUSTER:-default}"

# Resolve the deploy tree: prefer ${DEPLOY_DIR} env override, then the repo
# layout (script lives at QSDM/deploy/scripts/), then /opt/qsdm-deploy/.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ -z "${DEPLOY_DIR:-}" ]]; then
  if [[ -d "$SCRIPT_DIR/../prometheus" ]]; then
    DEPLOY_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
  elif [[ -d /opt/qsdm-deploy/prometheus ]]; then
    DEPLOY_DIR=/opt/qsdm-deploy
  else
    echo "FATAL: cannot locate deploy tree; set DEPLOY_DIR=/path/to/QSDM/deploy" >&2
    exit 1
  fi
fi

# ---------------------------------------------------------------------------
# 0. Verify the deploy package is present.
# ---------------------------------------------------------------------------
test -d "$DEPLOY_DIR/prometheus"    || { echo "FATAL: $DEPLOY_DIR/prometheus missing"; exit 1; }
test -d "$DEPLOY_DIR/alertmanager"  || { echo "FATAL: $DEPLOY_DIR/alertmanager missing"; exit 1; }
test -d "$DEPLOY_DIR/grafana"       || { echo "FATAL: $DEPLOY_DIR/grafana missing"; exit 1; }
echo "Using deploy tree: $DEPLOY_DIR"

# ---------------------------------------------------------------------------
# 1. Prometheus binary
# ---------------------------------------------------------------------------
if ! command -v prometheus &>/dev/null; then
  echo "==== Installing Prometheus ${PROM_VERSION} ===="
  cd /tmp
  wget -nv "https://github.com/prometheus/prometheus/releases/download/v${PROM_VERSION}/prometheus-${PROM_VERSION}.linux-amd64.tar.gz"
  tar xf "prometheus-${PROM_VERSION}.linux-amd64.tar.gz"
  install -m 0755 "prometheus-${PROM_VERSION}.linux-amd64/prometheus" /usr/local/bin/prometheus
  install -m 0755 "prometheus-${PROM_VERSION}.linux-amd64/promtool"   /usr/local/bin/promtool
  rm -rf "prometheus-${PROM_VERSION}.linux-amd64" "prometheus-${PROM_VERSION}.linux-amd64.tar.gz"
fi
prometheus --version 2>&1 | head -1
promtool --version    2>&1 | head -1

# ---------------------------------------------------------------------------
# 2. Alertmanager binary
# ---------------------------------------------------------------------------
if ! command -v alertmanager &>/dev/null; then
  echo "==== Installing Alertmanager ${AM_VERSION} ===="
  cd /tmp
  wget -nv "https://github.com/prometheus/alertmanager/releases/download/v${AM_VERSION}/alertmanager-${AM_VERSION}.linux-amd64.tar.gz"
  tar xf "alertmanager-${AM_VERSION}.linux-amd64.tar.gz"
  install -m 0755 "alertmanager-${AM_VERSION}.linux-amd64/alertmanager" /usr/local/bin/alertmanager
  install -m 0755 "alertmanager-${AM_VERSION}.linux-amd64/amtool"       /usr/local/bin/amtool
  rm -rf "alertmanager-${AM_VERSION}.linux-amd64" "alertmanager-${AM_VERSION}.linux-amd64.tar.gz"
fi
alertmanager --version 2>&1 | head -1

# ---------------------------------------------------------------------------
# 3. Users
# ---------------------------------------------------------------------------
getent passwd prometheus    >/dev/null || useradd --system --no-create-home --shell /usr/sbin/nologin prometheus
getent passwd alertmanager  >/dev/null || useradd --system --no-create-home --shell /usr/sbin/nologin alertmanager
# Grafana adds its own user via apt; nothing to do here.

# ---------------------------------------------------------------------------
# 4. Prometheus config
# ---------------------------------------------------------------------------
mkdir -p /etc/prometheus /var/lib/prometheus
chown prometheus:prometheus /var/lib/prometheus

# Pull the bearer secret from the qsdm.service drop-in.
SCRAPE_SECRET=$(awk -F'=' '/QSDM_DASHBOARD_METRICS_SCRAPE_SECRET=/ && /^Environment="QSDM_DASH/ {gsub(/"$/,"",$3); print $3; exit}' /etc/systemd/system/qsdm.service.d/secrets.conf)
test -n "$SCRAPE_SECRET" || { echo "FATAL: could not extract metrics scrape secret"; exit 1; }

# Production prometheus.yml — derived from the shipped example, with
# DASHBOARD_HOST/PORT and the secret filled in, and the alertmanager
# pointed at our local instance on 127.0.0.1:9093.
cat > /etc/prometheus/prometheus.yml <<PROMYAML
# /etc/prometheus/prometheus.yml — generated by install_observability.sh
# Source template: ${DEPLOY_DIR}/prometheus/prometheus.qsdm.example.yml

global:
  scrape_interval: 30s
  evaluation_interval: 30s
  external_labels:
    cluster: ${QSDM_CLUSTER}
    instance: ${QSDM_INSTANCE}

alerting:
  alertmanagers:
    - static_configs:
        - targets:
            - 127.0.0.1:9093

rule_files:
  - "alerts_qsdm.yml"

scrape_configs:
  - job_name: prometheus
    static_configs:
      - targets: ['127.0.0.1:9090']

  - job_name: qsdm-dashboard
    scrape_interval: 30s
    scrape_timeout: 10s
    metrics_path: /api/metrics/prometheus
    scheme: http
    static_configs:
      - targets: ['127.0.0.1:8081']
    authorization:
      type: Bearer
      credentials: ${SCRAPE_SECRET}
PROMYAML

# Copy the alert rules from the deploy tree. v1-only deployments may want
# to strip the qsdm-v2-mining-*, qsdm-v2-attest-*, and qsdm-v2-governance
# groups; see ../prometheus/README.md for the rationale.
cp "$DEPLOY_DIR/prometheus/alerts_qsdm.example.yml" /etc/prometheus/alerts_qsdm.yml

# Validate.
promtool check config /etc/prometheus/prometheus.yml
promtool check rules  /etc/prometheus/alerts_qsdm.yml

chown -R prometheus:prometheus /etc/prometheus

# ---------------------------------------------------------------------------
# 5. Prometheus systemd unit
# ---------------------------------------------------------------------------
cat > /etc/systemd/system/prometheus.service <<'UNIT'
[Unit]
Description=Prometheus
After=network-online.target qsdm.service
Wants=network-online.target

[Service]
Type=simple
User=prometheus
Group=prometheus
ExecStart=/usr/local/bin/prometheus \
  --config.file=/etc/prometheus/prometheus.yml \
  --storage.tsdb.path=/var/lib/prometheus/ \
  --storage.tsdb.retention.time=30d \
  --web.listen-address=127.0.0.1:9090 \
  --web.enable-lifecycle
# --web.enable-lifecycle exposes /-/reload, /-/quit, etc. Use SIGHUP via
# kill -HUP for an in-place rules/config reload (no restart, no scrape gap).
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RestartSec=5

NoNewPrivileges=true
ProtectSystem=full
ProtectHome=true
ReadWritePaths=/var/lib/prometheus

LimitNOFILE=65536

StandardOutput=journal
StandardError=journal
SyslogIdentifier=prometheus

[Install]
WantedBy=multi-user.target
UNIT

# ---------------------------------------------------------------------------
# 6. Alertmanager config
#
# Stripped no-receiver config: routes everything to a null receiver. Alerts
# are visible via the AM web UI (127.0.0.1:9093) and the v2 API
# (/api/v2/alerts). To wire real Slack/PagerDuty/email, copy
# /opt/qsdm-deploy/alertmanager/alertmanager.example.yml + templates and
# fill in the REPLACE_ME tokens.
# ---------------------------------------------------------------------------
mkdir -p /etc/alertmanager /var/lib/alertmanager
chown alertmanager:alertmanager /var/lib/alertmanager

cat > /etc/alertmanager/alertmanager.yml <<AMYAML
# /etc/alertmanager/alertmanager.yml — generated by install_observability.sh
#
# This is a stripped no-receiver configuration: alerts route to a single
# 'null-receiver' that has no delivery configs, so Alertmanager only stores
# them in memory and surfaces them via the web UI and /api/v2/alerts.
#
# To enable real notification delivery (Slack/PagerDuty/email), replace this
# file with ${DEPLOY_DIR}/alertmanager/alertmanager.example.yml after
# substituting the four REPLACE_ME tokens (see README.md in that directory).

global:
  resolve_timeout: 5m

route:
  receiver: 'null-receiver'
  group_by: ['alertname']
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 12h

receivers:
  - name: 'null-receiver'
    # Empty receiver = no delivery. Alerts are visible only via the
    # Alertmanager UI and /api/v2/alerts. Operators tail those when
    # there is no external pager wired.
AMYAML

amtool check-config /etc/alertmanager/alertmanager.yml

chown -R alertmanager:alertmanager /etc/alertmanager

# ---------------------------------------------------------------------------
# 7. Alertmanager systemd unit
# ---------------------------------------------------------------------------
cat > /etc/systemd/system/alertmanager.service <<'UNIT'
[Unit]
Description=Alertmanager
After=network-online.target prometheus.service
Wants=network-online.target

[Service]
Type=simple
User=alertmanager
Group=alertmanager
ExecStart=/usr/local/bin/alertmanager \
  --config.file=/etc/alertmanager/alertmanager.yml \
  --storage.path=/var/lib/alertmanager \
  --web.listen-address=127.0.0.1:9093
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RestartSec=5

NoNewPrivileges=true
ProtectSystem=full
ProtectHome=true
ReadWritePaths=/var/lib/alertmanager

LimitNOFILE=65536

StandardOutput=journal
StandardError=journal
SyslogIdentifier=alertmanager

[Install]
WantedBy=multi-user.target
UNIT

# ---------------------------------------------------------------------------
# 8. Grafana via apt
# ---------------------------------------------------------------------------
if ! command -v grafana-server &>/dev/null; then
  echo "==== Installing Grafana ===="
  apt-get install -y software-properties-common gnupg wget
  install -d -m 0755 /etc/apt/keyrings
  if [[ ! -f /etc/apt/keyrings/grafana.gpg ]]; then
    wget -qO- https://apt.grafana.com/gpg.key | gpg --dearmor > /etc/apt/keyrings/grafana.gpg
  fi
  if [[ ! -f /etc/apt/sources.list.d/grafana.list ]]; then
    echo "deb [signed-by=/etc/apt/keyrings/grafana.gpg] https://apt.grafana.com stable main" > /etc/apt/sources.list.d/grafana.list
  fi
  apt-get update
  apt-get install -y grafana
fi
# Grafana 13.x changed the binary to `grafana` and `grafana server -v` panics
# trying to load embedded static files in some setups; pull the version from
# dpkg, which is reliable.
dpkg-query -W -f='Grafana version: ${Version}\n' grafana || true

# ---------------------------------------------------------------------------
# 9. Grafana — bind 127.0.0.1, generate admin password if first install
# ---------------------------------------------------------------------------
GRAFANA_INI=/etc/grafana/grafana.ini

# Generate a strong admin password on first install. Grafana 13's
# `grafana-cli admin reset-admin-password` has a static-asset init bug that
# panics before the password is set, so we instead inject the password via
# grafana.ini's [security] admin_password — Grafana picks this up on first
# start and re-hashes it into the database.
if [[ ! -f /etc/grafana/.admin-password-set ]]; then
  ADMIN_PW=$(head -c 32 /dev/urandom | base64 | tr -d '+/=' | head -c 24)
  echo "$ADMIN_PW" > /etc/grafana/.admin-password
  chmod 0600 /etc/grafana/.admin-password
  chown root:root /etc/grafana/.admin-password
  python3 - "$ADMIN_PW" <<'PY'
import re, sys, pathlib
pw = sys.argv[1]
p = pathlib.Path('/etc/grafana/grafana.ini')
s = p.read_text()

def setkey(s, section, key, value):
    pat = re.compile(
        rf'(\[{re.escape(section)}\][^\[]*?\n)\s*;?\s*{re.escape(key)}\s*=\s*[^\n]*',
        re.S,
    )
    if pat.search(s):
        return pat.sub(rf'\g<1>{key} = {value}', s, count=1)
    return s + f'\n[{section}]\n{key} = {value}\n'

s = setkey(s, 'server',   'http_addr',      '127.0.0.1')
s = setkey(s, 'server',   'http_port',      '3000')
s = setkey(s, 'security', 'admin_user',     'admin')
s = setkey(s, 'security', 'admin_password', pw)
p.write_text(s)
PY
  touch /etc/grafana/.admin-password-set
  chmod 0600 /etc/grafana/.admin-password-set
  echo "==== Grafana admin password set (saved to /etc/grafana/.admin-password) ===="
fi

# ---------------------------------------------------------------------------
# 10. Grafana provisioning — datasource + dashboards
# ---------------------------------------------------------------------------
install -d -m 0755 /etc/grafana/provisioning/datasources
install -d -m 0755 /etc/grafana/provisioning/dashboards
install -d -m 0755 /var/lib/grafana/dashboards/qsdm

# Datasource: edit the shipped example to point at our local Prometheus.
sed 's|http://prometheus:9090|http://127.0.0.1:9090|g' \
    "$DEPLOY_DIR/grafana/provisioning/datasources/prometheus.example.yml" \
    > /etc/grafana/provisioning/datasources/prometheus.yml

# Dashboard provider: rewrite the example's path: line to point at the
# directory we populate below. Sed substitution is anchored on the example's
# default path.
sed -e 's|^      path: .*|      path: /var/lib/grafana/dashboards/qsdm|' \
    "$DEPLOY_DIR/grafana/provisioning/dashboards/qsdm-runbooks.example.yaml" \
    > /etc/grafana/provisioning/dashboards/qsdm-runbooks.yaml

# Copy dashboard JSONs
cp "$DEPLOY_DIR/grafana/qsdm-overview.json"                  /var/lib/grafana/dashboards/qsdm/
cp "$DEPLOY_DIR/grafana/dashboards/"*.json                   /var/lib/grafana/dashboards/qsdm/
chown -R grafana:grafana /var/lib/grafana/dashboards /etc/grafana/provisioning

# ---------------------------------------------------------------------------
# 11. Bring all three up
# ---------------------------------------------------------------------------
systemctl daemon-reload
systemctl enable --now prometheus.service
sleep 2
systemctl enable --now alertmanager.service
sleep 2
systemctl enable --now grafana-server.service
sleep 5

echo "==== Service states ===="
systemctl is-active prometheus.service
systemctl is-active alertmanager.service
systemctl is-active grafana-server.service

echo
echo "==== Smoke checks ===="
echo "Prometheus /-/ready: $(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:9090/-/ready)"
echo "Alertmanager /-/ready: $(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:9093/-/ready)"
echo "Grafana /api/health: $(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:3000/api/health)"
echo
echo "==== Prometheus targets (qsdm-dashboard should be UP) ===="
sleep 3
curl -sS 'http://127.0.0.1:9090/api/v1/targets?state=active' | python3 -c 'import json,sys; t=json.load(sys.stdin); [print(f"  {x[\"labels\"][\"job\"]:20} {x[\"health\"]:10} last_scrape={x.get(\"lastScrape\",\"\")[:19]} err={x.get(\"lastError\",\"-\")[:60]}") for x in t["data"]["activeTargets"]]'

echo
echo "==== Alert rule count loaded ===="
curl -sS 'http://127.0.0.1:9090/api/v1/rules' | python3 -c 'import json,sys; d=json.load(sys.stdin); rules=[r for g in d["data"]["groups"] for r in g["rules"] if r["type"]=="alerting"]; print(f"  alert rules: {len(rules)}")'

echo
echo "==== Currently-firing alerts ===="
curl -sS 'http://127.0.0.1:9090/api/v1/alerts' | python3 -c 'import json,sys; d=json.load(sys.stdin); a=d["data"]["alerts"]; print(f"  total: {len(a)}"); [print(f"    {al[\"labels\"][\"alertname\"]:50} state={al[\"state\"]} severity={al[\"labels\"].get(\"severity\",\"-\")}") for al in a[:20]]'

echo
echo "==== Grafana datasource health ===="
ADMIN_PW=$(cat /etc/grafana/.admin-password 2>/dev/null || echo "admin")
curl -sS -u "admin:$ADMIN_PW" 'http://127.0.0.1:3000/api/datasources' | python3 -c 'import json,sys; d=json.load(sys.stdin); [print(f"  {x[\"name\"]} {x[\"type\"]} url={x[\"url\"]}") for x in d]' 2>/dev/null || echo "  (could not query, may need auth)"

echo
echo "==== Done ===="
echo "Loopback access (SSH tunnel from your workstation):"
echo "  ssh -L 9090:127.0.0.1:9090 -L 9093:127.0.0.1:9093 -L 3000:127.0.0.1:3000 root@206.189.132.232"
echo "Then open in browser:"
echo "  Prometheus: http://localhost:9090"
echo "  Alertmanager: http://localhost:9093"
echo "  Grafana:    http://localhost:3000  (login: admin / password from /etc/grafana/.admin-password)"
