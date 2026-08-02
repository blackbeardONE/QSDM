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
  wallet.html wallet-provider.js wallet-start.html wallet-start.js \
  assets/wallet-start.css assets/wallet-extension-install.js \
  assets/browser-extension-distribution.json \
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

require_staged_marker() {
  local relative="$1"
  local marker="$2"
  if ! grep -Fq "$marker" "$STAGE/$relative"; then
    echo "staged $relative is not aligned with the current release: missing $marker" >&2
    exit 1
  fi
}

echo "=== validating staged release alignment ==="
if [[ ! -f "$WEBROOT/downloads/latest.yml" ]]; then
  echo "current Hive updater metadata is missing" >&2
  exit 1
fi
hive_version="$(awk '$1 == "version:" { print $2; exit }' "$WEBROOT/downloads/latest.yml")"
if [[ -z "$hive_version" ]]; then
  echo "could not read the current Hive version" >&2
  exit 1
fi

core_status="$(curl --fail --silent --show-error --max-time 15 \
  https://api.qsdm.tech/api/v1/status)"
core_version="$(sed -n 's/.*"version":"\([^"]*\)".*/\1/p' <<<"$core_status")"
if [[ -z "$core_version" ]]; then
  echo "could not read the current Core version" >&2
  exit 1
fi

mapfile -t extension_versions < <(
  find "$WEBROOT/downloads" -maxdepth 1 -type f \
    -name 'qsdm-hive-wallet-extension-*-chromium.zip' -printf '%f\n' |
    sed -n 's/^qsdm-hive-wallet-extension-\(.*\)-chromium\.zip$/\1/p' |
    sort -V
)
if [[ ${#extension_versions[@]} -eq 0 ]]; then
  echo "no published Chromium wallet extension was found" >&2
  exit 1
fi
extension_version="${extension_versions[-1]}"

require_staged_marker index.html "$core_version"
require_staged_marker docs/index.html "$core_version"
require_staged_marker download.html "Core $core_version"
require_staged_marker download.html "Hive $hive_version"
require_staged_marker download.html "Version $extension_version"
require_staged_marker download.html \
  "qsdm-hive-wallet-extension-$extension_version-chromium.zip"
require_staged_marker download.html \
  "qsdm-hive-wallet-extension-$extension_version-firefox.zip"
for browser in chrome edge brave firefox; do
  require_staged_marker download.html \
    "data-wallet-browser-card=\"$browser\""
  require_staged_marker assets/browser-extension-distribution.json \
    "\"$browser\""
done
require_staged_marker assets/browser-extension-distribution.json \
  '"schema": "qsdm.wallet-extension-distribution.v1"'
require_staged_marker wallet.html '/wallet-provider.js'
require_staged_marker wallet-start.html '/wallet-start.js'
require_staged_marker wallet-start.html 'noindex,follow'

for wallet_asset in wasm_exec.js wallet.js wallet-provider.js; do
  wallet_sri="$(openssl dgst -sha384 -binary "$STAGE/$wallet_asset" | openssl base64 -A)"
  require_staged_marker wallet.html "sha384-$wallet_sri"
done
echo "  Core $core_version / Hive $hive_version / Wallet extension $extension_version"

if [[ "${QSDM_SITE_VALIDATE_ONLY:-0}" == "1" ]]; then
  echo "DONE — staged website passed validation; no files were installed."
  exit 0
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
  https://qsdm.tech/wallet-start.html?login=new  \
  https://qsdm.tech/network.html                  \
  https://qsdm.tech/explorer.html                 \
  https://qsdm.tech/validators.html               \
  https://qsdm.tech/docs/                         \
  https://qsdm.tech/docs/docs.css                 \
  https://qsdm.tech/docs/docs.js                  \
  https://qsdm.tech/docs/lib/markdown-it.min.js   \
  https://qsdm.tech/assets/wallet-extension-install.js \
  https://qsdm.tech/assets/browser-extension-distribution.json \
; do
  curl --fail --max-time 15 -s -o /dev/null \
    -w "  %{http_code}  %{size_download} bytes  $u\n" "$u"
done

echo
echo "=== advertised download checks ==="
mapfile -t advertised_downloads < <(
  grep -oE 'href="/downloads/[^"?#]+"' "$WEBROOT/download.html" |
    sed -E 's/^href="([^"]+)"$/\1/' |
    sort -u
)
if [[ ${#advertised_downloads[@]} -eq 0 ]]; then
  echo "download page does not advertise any versioned files" >&2
  exit 1
fi
for path in "${advertised_downloads[@]}"; do
  curl --fail --silent --show-error --head --max-time 30 \
    "https://qsdm.tech$path" >/dev/null
  echo "  available  $path"
done

echo
echo "=== CSP check ==="
home_headers="$(curl --fail --max-time 10 -sI https://qsdm.tech/)"
grep -im1 "content-security-policy" <<<"$home_headers"

echo
echo "=== content checks ==="
home_page="$(curl --fail --max-time 15 -s https://qsdm.tech/)"
download_page="$(curl --fail --max-time 15 -s https://qsdm.tech/download.html)"
network_page="$(curl --fail --max-time 15 -s https://qsdm.tech/network.html)"
grep -Fq 'The public network for CELL.' <<<"$home_page"
grep -Fq 'View Network' <<<"$home_page"
grep -Fq 'QSDM VPN' <<<"$home_page"
grep -Fq "Version $hive_version" <<<"$download_page"
grep -Fq "Version $extension_version" <<<"$download_page"
grep -Fq "qsdm-hive-wallet-extension-$extension_version-chromium.zip" \
  <<<"$download_page"
grep -Fq 'data-wallet-browser-card="chrome"' <<<"$download_page"
grep -Fq 'data-wallet-browser-card="edge"' <<<"$download_page"
grep -Fq 'data-wallet-browser-card="brave"' <<<"$download_page"
grep -Fq 'data-wallet-browser-card="firefox"' <<<"$download_page"
grep -Fq 'QSDM Network' <<<"$network_page"
grep -Fq 'href="/download.html">Download</a>' <<<"$home_page"
echo "  expected homepage, download, and network markers are present"

echo
echo "=== docs SPA pulled markdown-it SRI ==="
grep -o -m 1 -E 'integrity="sha384-[A-Za-z0-9+/=]+"' "$WEBROOT/docs/index.html"
echo
echo "=== markdown-it actual sha384 ==="
openssl dgst -sha384 -binary "$WEBROOT/docs/lib/markdown-it.min.js" | openssl base64 -A
echo

echo "DONE — website updated. Backup: $BACKUP_DIR"
rm -f "$TGZ"
