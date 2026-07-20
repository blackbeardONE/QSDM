#!/bin/bash
# _install_docs_site.sh — install the QSDM public website and /docs/ SPA.
#
# Pre-req: copy the staging tarball to /tmp/qsdm_docs_site.tgz from the
# operator workstation.
#
# The package may contain the public HTML files, assets/, docs/, .well-known/,
# and an optional Caddyfile. The installer never changes downloads/ or
# releases/. It backs up the current website before replacing any content.

set -euo pipefail

TGZ="${1:-/tmp/qsdm_docs_site.tgz}"
WEBROOT="/var/www/qsdm"
STAGE="/tmp/qsdm_docs_site_stage"
BACKUP_ROOT="/var/backups/qsdm-site"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
BACKUP_DIR="$BACKUP_ROOT/$STAMP"
CADDY_CHANGED=false

cleanup() {
  rm -rf "$STAGE"
}
trap cleanup EXIT

if [[ ! -f "$TGZ" ]]; then
  echo "missing tarball: $TGZ" >&2
  exit 1
fi

while IFS= read -r entry; do
  entry="${entry#./}"
  if [[ -z "$entry" ]]; then
    continue
  fi
  if [[ "$entry" == /* || "$entry" == ".." || "$entry" == ../* || \
        "$entry" == */../* || "$entry" == */.. ]]; then
    echo "unsafe path in website package: $entry" >&2
    exit 1
  fi
done < <(tar -tzf "$TGZ")

rm -rf "$STAGE"
mkdir -p "$STAGE"
tar --no-same-owner --no-same-permissions -xzf "$TGZ" -C "$STAGE"

if [[ -n "$(find "$STAGE" -type l -print -quit)" ]]; then
  echo "website package must not contain symbolic links" >&2
  exit 1
fi

for required in \
  index.html download.html network.html explorer.html validators.html \
  assets/site.css assets/site-nav.js docs/index.html docs/docs.css \
  docs/docs.js docs/lib/markdown-it.min.js; do
  if [[ ! -f "$STAGE/$required" ]]; then
    echo "website package is missing $required" >&2
    exit 1
  fi
done

while IFS= read -r entry; do
  case "$entry" in
    .well-known|assets|docs|Caddyfile|*.html|*.js|*.wasm|*.txt|*.xml) ;;
    *)
      echo "unexpected top-level website package entry: $entry" >&2
      exit 1
      ;;
  esac
done < <(find "$STAGE" -mindepth 1 -maxdepth 1 -printf '%f\n')

if [[ -f "$STAGE/Caddyfile" ]]; then
  echo "=== validating staged Caddyfile ==="
  caddy validate --config "$STAGE/Caddyfile" --adapter caddyfile
fi

echo "=== backing up current public website to $BACKUP_DIR ==="
install -d -o root -g root -m 0700 "$BACKUP_DIR"
if [[ -d "$WEBROOT" ]]; then
  tar -C "$WEBROOT" \
    --exclude='./downloads' --exclude='./releases' \
    -czf "$BACKUP_DIR/site-before.tgz" .
fi
if [[ -f /etc/caddy/Caddyfile ]]; then
  cp -a /etc/caddy/Caddyfile "$BACKUP_DIR/Caddyfile.before"
fi

install_file() {
  local source="$1"
  local destination="$2"
  install -d -o caddy -g caddy -m 0755 "$(dirname "$destination")"
  install -o caddy -g caddy -m 0644 "$source" "$destination.new"
  mv -f "$destination.new" "$destination"
}

install_tree() {
  local source_root="$1"
  local destination_root="$2"
  local source relative
  while IFS= read -r -d '' source; do
    relative="${source#"$source_root"/}"
    install_file "$source" "$destination_root/$relative"
  done < <(find "$source_root" -type f -print0)
}

echo "=== installing public website into $WEBROOT ==="
for source in "$STAGE"/*; do
  if [[ -f "$source" && "$(basename "$source")" != "Caddyfile" ]]; then
    install_file "$source" "$WEBROOT/$(basename "$source")"
  fi
done
for directory in .well-known assets docs; do
  if [[ -d "$STAGE/$directory" ]]; then
    install_tree "$STAGE/$directory" "$WEBROOT/$directory"
  fi
done

if [[ -f "$STAGE/Caddyfile" ]]; then
  echo "=== installing Caddyfile ==="
  install -o root -g root -m 0644 "$STAGE/Caddyfile" "/etc/caddy/Caddyfile.new"
  mv -f /etc/caddy/Caddyfile.new /etc/caddy/Caddyfile
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
echo "=== live probes ==="
for u in \
  https://qsdm.tech/                              \
  https://qsdm.tech/download.html                 \
  https://qsdm.tech/network.html                  \
  https://qsdm.tech/explorer.html                 \
  https://qsdm.tech/validators.html               \
  https://qsdm.tech/docs/                         \
  https://qsdm.tech/docs/docs.css                 \
  https://qsdm.tech/docs/docs.js                  \
  https://qsdm.tech/docs/lib/markdown-it.min.js   \
; do
  curl --fail --max-time 15 -s -o /dev/null \
    -w "  %{http_code}  %{size_download} bytes  $u\n" "$u"
done

echo
echo "=== CSP check ==="
curl --max-time 10 -sI https://qsdm.tech/ | grep -i "content-security-policy" | head -n 1

echo
echo "=== content checks ==="
curl --fail --max-time 15 -s https://qsdm.tech/ | grep -q 'QSDM VPN'
curl --fail --max-time 15 -s https://qsdm.tech/download.html | grep -q 'Version 1.4.0'
curl --fail --max-time 15 -s https://qsdm.tech/network.html | grep -q 'QSDM Network'
echo "  expected homepage, download, and network markers are present"

echo
echo "=== docs SPA pulled markdown-it SRI ==="
grep -oE 'integrity="sha384-[A-Za-z0-9+/=]+"' "$WEBROOT/docs/index.html" | head -n 1
echo
echo "=== markdown-it actual sha384 ==="
openssl dgst -sha384 -binary "$WEBROOT/docs/lib/markdown-it.min.js" | openssl base64 -A
echo

echo "DONE — website updated. Backup: $BACKUP_DIR"
rm -f "$TGZ"
