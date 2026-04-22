#!/usr/bin/env bash
# Install Caddy 2.x, deploy Caddyfile, open ports 80/443/4001 on ufw,
# reload / start the service.
set -euo pipefail

CADDYFILE_SRC="/root/qsdm-deploy/Caddyfile"

if ! command -v caddy >/dev/null 2>&1; then
  echo "[+] Installing Caddy via official apt repo"
  apt-get install -y debian-keyring debian-archive-keyring apt-transport-https curl gnupg
  curl -fsSL 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
    | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
  curl -fsSL 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
    | tee /etc/apt/sources.list.d/caddy-stable.list >/dev/null
  apt-get update -y
  apt-get install -y caddy
fi

install -d -m0755 /etc/caddy
install -m0644 "${CADDYFILE_SRC}" /etc/caddy/Caddyfile
install -d -m0755 /var/log/caddy
# chown AFTER the caddy user exists (it is created by the caddy .deb postinst).
if getent passwd caddy >/dev/null 2>&1; then
  chown -R caddy:caddy /var/log/caddy
fi

echo "[+] Validating Caddyfile"
caddy validate --config /etc/caddy/Caddyfile

echo "[+] Opening ufw ports (80, 443, 4001 for libp2p)"
ufw allow 80/tcp  || true
ufw allow 443/tcp || true
ufw allow 4001/tcp || true
# 8080/8081/8443 can stay open (direct) or be closed later — we keep them for now
# so that node operators can still reach the raw API during migration.
ufw --force reload || true

echo "[+] Enabling and restarting caddy"
systemctl enable caddy
systemctl restart caddy
sleep 2
systemctl --no-pager --full status caddy | sed -n '1,20p' || true

echo "[+] Current listeners"
ss -tlnp | grep -E ':(80|443|4001|8080|8081|8443)\b' || true
