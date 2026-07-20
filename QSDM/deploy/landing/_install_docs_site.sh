#!/bin/bash
# _install_docs_site.sh — install the new simplified landing + /docs/ SPA
# on api.qsdm.tech (BLR1).
#
# Pre-req: scp the staging tarball to /tmp/qsdm_docs_site.tgz from the
# operator workstation (see deploy_docs_site.ps1 on the windows side).
#
# Layout (server-side):
#   /var/www/qsdm/index.html              ← simplified landing
#   /var/www/qsdm/docs/index.html         ← docs SPA shell
#   /var/www/qsdm/docs/docs.css           ← docs SPA styles
#   /var/www/qsdm/docs/docs.js            ← docs SPA logic
#   /var/www/qsdm/docs/lib/markdown-it.min.js
#   /etc/caddy/Caddyfile                  ← extended connect-src CSP
#
# Then: validate any replacement Caddyfile, restart Caddy when required, and
# probe live URLs. The production Caddy admin API is intentionally disabled,
# so systemctl reload cannot be used there.

set -euo pipefail

TGZ="${1:-/tmp/qsdm_docs_site.tgz}"
WEBROOT="/var/www/qsdm"
STAGE="/tmp/qsdm_docs_site_stage"
CADDY_CHANGED=false

if [[ ! -f "$TGZ" ]]; then
  echo "missing tarball: $TGZ" >&2
  exit 1
fi

rm -rf "$STAGE"
mkdir -p "$STAGE"
tar -xzf "$TGZ" -C "$STAGE"

echo "=== installing landing + docs SPA into $WEBROOT ==="
install -o caddy -g caddy -m 0644 "$STAGE/index.html"             "$WEBROOT/index.html"
install -d -o caddy -g caddy -m 0755                              "$WEBROOT/docs"
install -d -o caddy -g caddy -m 0755                              "$WEBROOT/docs/lib"
install -o caddy -g caddy -m 0644 "$STAGE/docs/index.html"        "$WEBROOT/docs/index.html"
install -o caddy -g caddy -m 0644 "$STAGE/docs/docs.css"          "$WEBROOT/docs/docs.css"
install -o caddy -g caddy -m 0644 "$STAGE/docs/docs.js"           "$WEBROOT/docs/docs.js"
install -o caddy -g caddy -m 0644 "$STAGE/docs/lib/markdown-it.min.js" "$WEBROOT/docs/lib/markdown-it.min.js"

if [[ -f "$STAGE/Caddyfile" ]]; then
  echo "=== installing Caddyfile ==="
  install -o root -g root -m 0644 "$STAGE/Caddyfile" "/etc/caddy/Caddyfile"
  CADDY_CHANGED=true
fi

if [[ "$CADDY_CHANGED" == true ]]; then
  echo "=== Caddyfile validate ==="
  caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile

  echo "=== bounded systemctl restart caddy ==="
  timeout 20 systemctl restart caddy
fi
systemctl is-active --quiet caddy

echo
echo "=== live probes (HEAD) ==="
for u in \
  https://qsdm.tech/                              \
  https://qsdm.tech/docs/                         \
  https://qsdm.tech/docs/docs.css                 \
  https://qsdm.tech/docs/docs.js                  \
  https://qsdm.tech/docs/lib/markdown-it.min.js   \
; do
  curl --max-time 10 -s -o /dev/null -w "  %{http_code}  %{size_download} bytes  $u\n" -I "$u"
done

echo
echo "=== CSP check ==="
curl --max-time 10 -sI https://qsdm.tech/ | grep -i "content-security-policy" | head -n 1

echo
echo "=== landing nav menu items ==="
curl --max-time 10 -s https://qsdm.tech/ | grep -oE 'navlink"[^>]*>[A-Za-z]+' | sed 's/.*>//' | head -n 6

echo
echo "=== docs SPA pulled markdown-it SRI ==="
grep -oE 'integrity="sha384-[A-Za-z0-9+/=]+"' "$WEBROOT/docs/index.html" | head -n 1
echo
echo "=== markdown-it actual sha384 ==="
openssl dgst -sha384 -binary "$WEBROOT/docs/lib/markdown-it.min.js" | openssl base64 -A
echo

echo "DONE — visit https://qsdm.tech/docs/ to verify the SPA renders."
rm -rf "$STAGE" "$TGZ"
