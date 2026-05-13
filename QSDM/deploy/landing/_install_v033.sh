#!/bin/bash
# _install_v033.sh — install v0.3.3 landing files into /var/www/qsdm.
# Run on api.qsdm.tech AFTER scp'ing the three files into /tmp.
set -euo pipefail
for f in index.html wallet.html wallet.js ; do
  install -o caddy -g caddy -m 0644 "/tmp/$f" "/var/www/qsdm/$f"
  echo "updated /var/www/qsdm/$f"
  rm -f "/tmp/$f"
done
systemctl reload caddy
echo "=== live probes ==="
curl -s -o /dev/null -w "index     http=%{http_code} bytes=%{size_download}\n" https://qsdm.tech/
curl -s -o /dev/null -w "wallet    http=%{http_code} bytes=%{size_download}\n" https://qsdm.tech/wallet.html
curl -s -o /dev/null -w "wallet.js http=%{http_code} bytes=%{size_download}\n" https://qsdm.tech/wallet.js
echo
echo "=== version pill markers ==="
curl -s https://qsdm.tech/ | grep -E "ver-pill-text|releases/tag/v|Current release:" | head -n 4
echo
echo "=== wallet.js SRI on wallet.html ==="
grep -E "wallet.js.*integrity" /var/www/qsdm/wallet.html
echo
echo "=== wallet.js actual sha384 (must match SRI above) ==="
openssl dgst -sha384 -binary /var/www/qsdm/wallet.js | openssl base64 -A
echo
echo "DONE."
