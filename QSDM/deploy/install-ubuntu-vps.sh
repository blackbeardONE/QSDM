#!/bin/bash
# One-time install: QSDM node on Ubuntu 24.04+ (as root)
# See docs/docs/UBUNTU_DEPLOYMENT.md. Run: bash install-ubuntu-vps.sh
set -euo pipefail

QSDM_HOME="${QSDM_HOME:-$HOME/QSDM}"
QSDM_GIT="${QSDM_GIT:-https://github.com/blackbeardONE/QSDM.git}"
GO_TGZ="${GO_TGZ:-https://go.dev/dl/go1.23.4.linux-amd64.tar.gz}"
# Install directory keeps the legacy 'qsdm' name during the rebrand
# migration window so systemd units and operator bookmarks remain valid.
# See QSDM/docs/docs/REBRAND_NOTES.md.
INSTALL_DIR="/opt/qsdm"

if [[ "$(id -u)" -ne 0 ]]; then
  echo "Run as root (e.g. sudo bash $0)"
  exit 1
fi

export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get upgrade -y
apt-get install -y --no-install-recommends \
  build-essential cmake git curl wget ufw htop \
  ca-certificates pkg-config libssl-dev libsqlite3-dev

# Go toolchain: use /usr/local/go (1.23.x) for reproducible builds
if [[ ! -x /usr/local/go/bin/go ]]; then
  echo "=== Installing Go from ${GO_TGZ} ==="
  TMP="$(mktemp)"
  wget -qO "$TMP" "$GO_TGZ"
  rm -rf /usr/local/go
  tar -C /usr/local -xzf "$TMP"
  rm -f "$TMP"
  grep -q '/usr/local/go/bin' /root/.profile 2>/dev/null || echo 'export PATH=$PATH:/usr/local/go/bin' >> /root/.profile
fi
export PATH="/usr/local/go/bin:${PATH:-}"
go version

# Use pre-uploaded source if present; otherwise clone from git
if [[ -f "$QSDM_HOME/scripts/rebuild_liboqs.sh" && -d "$QSDM_HOME/source" ]]; then
  echo "=== Using existing QSDM tree at $QSDM_HOME (no git clone) ==="
elif [[ ! -d "$QSDM_HOME/.git" ]]; then
  echo "=== Cloning QSDM ledger ==="
  git clone --depth 1 "$QSDM_GIT" "$QSDM_HOME"
else
  echo "=== Updating existing clone ==="
  git -C "$QSDM_HOME" pull --ff-only || true
fi

cd "$QSDM_HOME"
chmod +x scripts/rebuild_liboqs.sh scripts/build.sh 2>/dev/null || true

echo "=== Building liboqs (long; 10–30+ min on small VPS) ==="
./scripts/rebuild_liboqs.sh

echo "=== Building qsdm ==="
./scripts/build.sh
test -f ./qsdm

if ! getent passwd qsdm &>/dev/null; then
  useradd -r -s /bin/false -d "$INSTALL_DIR" qsdm
fi
mkdir -p "$INSTALL_DIR"
chown -R qsdm:qsdm "$INSTALL_DIR"

install -m 0755 ./qsdm "$INSTALL_DIR/qsdm"
if [[ -d ./liboqs_install ]]; then
  cp -a ./liboqs_install "$INSTALL_DIR/"
  chown -R qsdm:qsdm "$INSTALL_DIR/liboqs_install"
fi

# Production config: HTTP API without TLS to avoid cert requirement; paths under /opt
cat > /tmp/qsdm.production.toml <<'EOF'
[network]
port = 4001
bootstrap_peers = []

[storage]
type = "sqlite"
sqlite_path = "/opt/qsdm/qsdm.db"

[monitoring]
dashboard_port = 8081
log_viewer_port = 8080
log_file = "/opt/qsdm/qsdm.log"
log_level = "INFO"

[api]
port = 8443
enable_tls = false
tls_cert_file = ""
tls_key_file = ""

[wallet]
initial_balance = 1000.0

[governance]
proposal_file = "/opt/qsdm/proposals.json"

[performance]
# Demo/auto-txgen cadence; production should be long or a real client
transaction_interval = "1h"
health_check_interval = "30s"
EOF
install -m 0644 /tmp/qsdm.production.toml "$INSTALL_DIR/qsdm.toml"
rm -f /tmp/qsdm.production.toml
chown qsdm:qsdm "$INSTALL_DIR/qsdm.toml"
touch "$INSTALL_DIR/proposals.json" 2>/dev/null || true
chown qsdm:qsdm "$INSTALL_DIR/proposals.json" 2>/dev/null || true

if [[ -f config/qsdm.service ]]; then
  install -m 0644 config/qsdm.service /etc/systemd/system/qsdm.service
else
  echo "Missing config/qsdm.service in repo" >&2
  exit 1
fi
systemctl daemon-reload
systemctl enable qsdm
systemctl restart qsdm

echo "=== Firewall (ufw) ==="
ufw allow 22/tcp
ufw allow 4001/tcp
ufw allow 8080/tcp
ufw allow 8081/tcp
ufw allow 8443/tcp
ufw --force enable || true

echo ""
echo "=== Done ==="
systemctl --no-pager -l status qsdm || true
echo "Dashboard: http://$(curl -s ifconfig.me 2>/dev/null || echo YOUR_IP):8081"
echo "Logs: journalctl -u qsdm -f"
