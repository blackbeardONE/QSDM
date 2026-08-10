#!/usr/bin/env bash
set -euo pipefail

if [[ ${EUID:-$(id -u)} -ne 0 ]]; then
  echo "Run this deployment test with sudo." >&2
  exit 1
fi

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
mkdir -p "$work/bin"

cat >"$work/original" <<'CADDY'
api.qsdm.tech, node.qsdm.tech {
	import /etc/caddy/qsdm-edge-relay.caddy
}

qsdm.tech {
	root * /var/www/qsdm
	redir /wallet /wallet.html 302
	redir /wallet/ /wallet.html 302
	handle {
		file_server
	}
}
CADDY
cp "$work/original" "$work/Caddyfile"
ln -s "$work/Caddyfile" "$work/Caddyfile-link"

cat >"$work/bin/caddy" <<'SH'
#!/usr/bin/env bash
[[ ${1:-} == validate ]]
SH
cat >"$work/bin/systemctl" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$QSDM_TEST_SYSTEMCTL_LOG"
SH
cat >"$work/bin/verifier" <<'SH'
#!/usr/bin/env bash
if [[ ${1:-} == --local-only ]]; then
  exit 0
fi
[[ ! -f "$QSDM_TEST_FAIL_PUBLIC" ]]
SH
chmod 0755 "$work/bin/caddy" "$work/bin/systemctl" "$work/bin/verifier"

export QSDM_CADDYFILE="$work/Caddyfile-link"
export QSDM_ACCOUNT_CADDY_MERGER="$script_dir/merge_account_caddy_route.py"
export QSDM_ACCOUNT_VERIFIER="$work/bin/verifier"
export QSDM_CADDY_COMMAND="$work/bin/caddy"
export QSDM_SYSTEMCTL_COMMAND="$work/bin/systemctl"
export QSDM_TEST_SYSTEMCTL_LOG="$work/systemctl.log"
export QSDM_TEST_FAIL_PUBLIC="$work/fail-public"

bash "$script_dir/install_account_proxy_route.sh"
[[ -L "$work/Caddyfile-link" ]]
grep -Fq 'import /etc/caddy/qsdm-edge-relay.caddy' "$work/Caddyfile"
grep -Fq 'reverse_proxy 127.0.0.1:8092' "$work/Caddyfile"
grep -Fxq 'reload caddy.service' "$work/systemctl.log"
first_reload_count=$(wc -l <"$work/systemctl.log")
bash "$script_dir/install_account_proxy_route.sh"
[[ $(wc -l <"$work/systemctl.log") -eq $first_reload_count ]]

cp "$work/original" "$work/Caddyfile"
: >"$work/systemctl.log"
export QSDM_CADDY_APPLY_MODE=restart
bash "$script_dir/install_account_proxy_route.sh"
grep -Fxq 'restart caddy.service' "$work/systemctl.log"

{
  printf '%s\n' '{' '  admin off' '}'
  cat "$work/original"
} >"$work/Caddyfile"
: >"$work/systemctl.log"
unset QSDM_CADDY_APPLY_MODE
bash "$script_dir/install_account_proxy_route.sh"
grep -Fxq 'restart caddy.service' "$work/systemctl.log"

cp "$work/original" "$work/Caddyfile"
cp "$work/original" "$work/before-rollback"
: >"$work/systemctl.log"
export QSDM_CADDY_APPLY_MODE=restart
touch "$QSDM_TEST_FAIL_PUBLIC"
if bash "$script_dir/install_account_proxy_route.sh"; then
  echo "Expected public verification failure did not occur." >&2
  exit 1
fi
cmp -s "$work/before-rollback" "$work/Caddyfile"
[[ $(grep -Fxc 'restart caddy.service' "$work/systemctl.log") -eq 2 ]]
echo "Account proxy activation merge, idempotence, and rollback tests passed."
