#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <qsdm-edge-agent-linux-x86_64.tar.gz> [port]" >&2
  exit 64
fi

archive="$(readlink -f "$1")"
port="${2:-17740}"
work_dir="$(mktemp -d)"
coordinator_pid=""
agent_pid=""

cleanup() {
  if [[ -n "$agent_pid" ]]; then
    kill "$agent_pid" 2>/dev/null || true
    wait "$agent_pid" 2>/dev/null || true
  fi
  if [[ -n "$coordinator_pid" ]]; then
    kill "$coordinator_pid" 2>/dev/null || true
    wait "$coordinator_pid" 2>/dev/null || true
  fi
  rm -rf "$work_dir"
}
trap cleanup EXIT

tar -xzf "$archive" -C "$work_dir"
agent_binary="$(find "$work_dir" -mindepth 2 -maxdepth 2 -type f -name qsdm-edge-agent -print -quit)"
if [[ -z "$agent_binary" || ! -x "$agent_binary" ]]; then
  echo "archive does not contain an executable qsdm-edge-agent" >&2
  exit 65
fi

"$agent_binary" version
"$agent_binary" token --out "$work_dir/edge-pool.token"
"$agent_binary" configure-agent \
  --out "$work_dir/agent.json" \
  --coordinator "http://127.0.0.1:$port" \
  --token-file "$work_dir/edge-pool.token" \
  --worker-id linux-smoke-worker \
  --resources cpu \
  --cpu-units 1000 \
  --poll-seconds 1 \
  --log-file "$work_dir/agent.log"

start_coordinator() {
  "$agent_binary" coordinator \
    --listen "127.0.0.1:$port" \
    --token-file "$work_dir/edge-pool.token" \
    --state-dir "$work_dir/coordinator-state" \
    --cpu-units 1000 \
    >"$work_dir/coordinator.log" 2>&1 &
  coordinator_pid=$!
}

wait_for_receipts() {
  local minimum="$1"
  local deadline=$((SECONDS + 20))
  while (( SECONDS < deadline )); do
    if status="$("$agent_binary" status \
      --coordinator "http://127.0.0.1:$port" \
      --token-file "$work_dir/edge-pool.token" \
      --worker-id linux-smoke-status 2>/dev/null)"; then
      count="$(printf '%s\n' "$status" | sed -n 's/.*"cpu": \([0-9][0-9]*\).*/\1/p' | head -n 1)"
      if [[ -n "$count" ]] && (( count >= minimum )); then
        printf '%s\n' "$status"
        return 0
      fi
    fi
    sleep 1
  done
  echo "timed out waiting for $minimum verified CPU receipts" >&2
  cat "$work_dir/coordinator.log" >&2 || true
  cat "$work_dir/agent.log" >&2 || true
  return 1
}

start_coordinator
"$agent_binary" agent --config "$work_dir/agent.json" --silent &
agent_pid=$!
wait_for_receipts 1 >/dev/null

kill "$coordinator_pid"
wait "$coordinator_pid" || true
coordinator_pid=""
start_coordinator
wait_for_receipts 2 >/dev/null

if ! kill -0 "$agent_pid" 2>/dev/null; then
  echo "agent exited during coordinator restart" >&2
  exit 1
fi

service_home="$work_dir/service-home"
service_config="$work_dir/service-config"
fake_bin="$work_dir/fake-bin"
mkdir -p "$service_home" "$service_config" "$fake_bin"
cat >"$fake_bin/systemctl" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod 0755 "$fake_bin/systemctl"
HOME="$service_home" XDG_CONFIG_HOME="$service_config" PATH="$fake_bin:/usr/bin:/bin" \
  "$agent_binary" install-service --config "$work_dir/agent.json" >/dev/null
installed_agent="$service_home/.local/bin/qsdm-edge-agent"
installed_token="$service_config/QSDM/edge-pool/edge-pool.token"
installed_config="$service_config/QSDM/edge-pool/agent.json"
installed_unit="$service_config/systemd/user/qsdm-edge-agent.service"
test -x "$installed_agent"
test "$(stat -c '%a' "$installed_token")" = "600"
test "$(stat -c '%a' "$installed_config")" = "600"
grep -q '^Restart=always$' "$installed_unit"
grep -q '^NoNewPrivileges=true$' "$installed_unit"
grep -q "$installed_config" "$installed_unit"

HOME="$service_home" XDG_CONFIG_HOME="$service_config" PATH="$fake_bin:/usr/bin:/bin" \
  "$agent_binary" install-coordinator-service \
  --listen 127.0.0.1:17741 \
  --token-file "$work_dir/edge-pool.token" >/dev/null
installed_coordinator_unit="$service_config/systemd/user/qsdm-edge-coordinator.service"
test "$(stat -c '%a' "$installed_coordinator_unit")" = "600"
grep -q '^Restart=always$' "$installed_coordinator_unit"
grep -q 'coordinator --listen "127.0.0.1:17741"' "$installed_coordinator_unit"
grep -q "$service_config/QSDM/edge-pool/coordinator" "$installed_coordinator_unit"

echo "Linux edge-agent smoke test passed: receipts survived restart and isolated worker/coordinator services verified."
