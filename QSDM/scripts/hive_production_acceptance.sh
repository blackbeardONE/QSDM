#!/usr/bin/env bash
set -uo pipefail

expected_version=""
expected_commit=""
installed_version=""
release_base="https://qsdm.tech/downloads"
output_path=""
wallet_address=""
qsdmcli_path=""
gpu_sample_seconds=5
log_window_minutes=30
require_gpu=0
strict_warnings=0
skip_gpu=0
skip_logs=0
release_version_result=""

usage() {
  cat <<'EOF'
usage: hive_production_acceptance.sh [options]

Read-only production acceptance checks for QSDM Hive.

  --expected-version VERSION
  --expected-commit COMMIT
  --installed-version VERSION
  --release-base URL
  --wallet ADDRESS
  --qsdmcli PATH
  --output PATH
  --gpu-sample-seconds N
  --log-window-minutes N
  --require-gpu-mining
  --strict-warnings
  --skip-gpu
  --skip-logs
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --expected-version) expected_version="${2:-}"; shift 2 ;;
    --expected-commit) expected_commit="${2:-}"; shift 2 ;;
    --installed-version) installed_version="${2:-}"; shift 2 ;;
    --release-base) release_base="${2:-}"; shift 2 ;;
    --wallet) wallet_address="${2:-}"; shift 2 ;;
    --qsdmcli) qsdmcli_path="${2:-}"; shift 2 ;;
    --output) output_path="${2:-}"; shift 2 ;;
    --gpu-sample-seconds) gpu_sample_seconds="${2:-}"; shift 2 ;;
    --log-window-minutes) log_window_minutes="${2:-}"; shift 2 ;;
    --require-gpu-mining) require_gpu=1; shift ;;
    --strict-warnings) strict_warnings=1; shift ;;
    --skip-gpu) skip_gpu=1; shift ;;
    --skip-logs) skip_logs=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage >&2; exit 64 ;;
  esac
done

if ! command -v curl >/dev/null 2>&1 || ! command -v python3 >/dev/null 2>&1; then
  echo "curl and python3 are required" >&2
  exit 69
fi
if [[ ! "$gpu_sample_seconds" =~ ^[1-9][0-9]*$ ]] ||
   [[ ! "$log_window_minutes" =~ ^[1-9][0-9]*$ ]]; then
  echo "sample and log windows must be positive integers" >&2
  exit 64
fi

script_source="${BASH_SOURCE[0]:-$0}"
script_dir="$(cd "$(dirname "$script_source")" && pwd)"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/qsdm-hive-acceptance.XXXXXX")"
checks_file="$work_dir/checks.jsonl"
started_epoch_ms="$(python3 -c 'import time; print(int(time.time()*1000))')"
touch "$checks_file"
trap 'rm -rf -- "$work_dir"' EXIT

add_check() {
  local name="$1" status="$2" summary="$3" data="${4:-{}}"
  python3 - "$checks_file" "$name" "$status" "$summary" "$data" <<'PY'
import json, sys
path, name, status, summary, raw = sys.argv[1:]
try:
    data = json.loads(raw)
except Exception:
    data = {}
with open(path, "a", encoding="utf-8") as handle:
    json.dump({"name": name, "status": status, "summary": summary, "data": data}, handle, separators=(",", ":"))
    handle.write("\n")
PY
  printf '[%s] %s: %s\n' "${status^^}" "$name" "$summary"
}

process_pids() {
  local pattern="$1"
  python3 - "$pattern" <<'PY'
import os, re, sys
pattern = re.compile(sys.argv[1], re.I)
ignored = {os.getpid(), os.getppid()}
for entry in os.listdir("/proc"):
    if not entry.isdigit() or int(entry) in ignored:
        continue
    try:
        raw = open(f"/proc/{entry}/cmdline", "rb").read().replace(b"\0", b" ")
        comm = open(f"/proc/{entry}/comm", "rb").read().strip()
        text = (raw + b" " + comm).decode("utf-8", "replace")
    except (FileNotFoundError, PermissionError, ProcessLookupError):
        continue
    if pattern.search(text):
        print(entry)
PY
}

discover_installed_version() {
  local pid="$1"
  python3 - "$pid" <<'PY'
import os, re, sys
pid = sys.argv[1]
values = []
try:
    env = open(f"/proc/{pid}/environ", "rb").read().split(b"\0")
    values.extend(item.decode("utf-8", "replace") for item in env if item.startswith(b"APPIMAGE="))
except (FileNotFoundError, PermissionError, ProcessLookupError):
    pass
try:
    values.append(open(f"/proc/{pid}/cmdline", "rb").read().replace(b"\0", b" ").decode("utf-8", "replace"))
except (FileNotFoundError, PermissionError, ProcessLookupError):
    pass
try:
    values.append(os.readlink(f"/proc/{pid}/exe"))
except (FileNotFoundError, PermissionError, ProcessLookupError):
    pass
for value in values:
    match = re.search(r"qsdm[-_ ]?hive[^0-9]*(\d+\.\d+\.\d+)", value, re.I)
    if match:
        print(match.group(1))
        break
PY
}

discover_qsdmcli() {
  if [[ -n "$qsdmcli_path" && -x "$qsdmcli_path" ]]; then
    printf '%s\n' "$qsdmcli_path"
    return
  fi
  local candidates=(
    "$script_dir/../source/.cache/local-validator/qsdmcli"
    "$script_dir/../../apps/qsdm-hive/qsdm-hive-main/native/linux/x64/qsdmcli"
    "$HOME/.config/QSDM-Hive/executables/qsdmcli"
    "$HOME/.config/qsdm-hive/executables/qsdmcli"
  )
  local process_exe
  process_exe="$(process_pids 'qsdm[-_ ]hive' | head -n1 || true)"
  if [[ -n "$process_exe" && -e "/proc/$process_exe/exe" ]]; then
    local resolved
    resolved="$(readlink -f "/proc/$process_exe/exe" 2>/dev/null || true)"
    if [[ -n "$resolved" ]]; then
      candidates+=("$(dirname "$resolved")/resources/native/qsdmcli")
    fi
  fi
  local candidate
  for candidate in "${candidates[@]}"; do
    if [[ -x "$candidate" ]]; then
      printf '%s\n' "$candidate"
      return
    fi
  done
}

test_release_channel() {
  local platform="$1" updater_name="$2" envelope_name="$3"
  release_version_result=""
  local updater="$work_dir/$updater_name" envelope="$work_dir/$envelope_name"
  local payload="$work_dir/$platform-payload.json"
  if ! curl -fsS --max-time 30 "$release_base/$updater_name" -o "$updater" 2>/dev/null ||
     ! curl -fsS --max-time 30 "$release_base/$envelope_name" -o "$envelope" 2>/dev/null; then
    add_check "Release channel ($platform)" fail "release metadata is unavailable"
    return 1
  fi

  local fields=()
  mapfile -t fields < <(python3 - "$platform" "$updater" "$envelope" "$payload" <<'PY'
import base64, datetime, json, re, sys
platform, updater_path, envelope_path, payload_path = sys.argv[1:]
updater = open(updater_path, "rb").read()
text = updater.decode("utf-8")
version_match = re.search(r"^version:\s*['\"]?([^'\"\r\n]+)", text, re.M)
path_match = re.search(r"^path:\s*['\"]?([^'\"\r\n]+)", text, re.M)
if not path_match:
    path_match = re.search(r"^\s*-\s*url:\s*['\"]?([^'\"\r\n]+)", text, re.M)
if not version_match or not path_match:
    raise SystemExit("invalid updater metadata")
envelope = json.load(open(envelope_path, encoding="utf-8"))
if envelope.get("schema") != "qsdm.signed-release.v1" or envelope.get("algorithm") != "ML-DSA-87":
    raise SystemExit("invalid signed release envelope")
payload = base64.b64decode(envelope["manifest_base64"], validate=True)
open(payload_path, "wb").write(payload)
manifest = json.loads(payload)
expires_at = str(manifest.get("expires_at", "")).replace("Z", "+00:00")
try:
    expires = datetime.datetime.fromisoformat(expires_at)
except ValueError:
    raise SystemExit("signed release expiry is invalid")
if expires.tzinfo is None:
    expires = expires.replace(tzinfo=datetime.timezone.utc)
if expires <= datetime.datetime.now(datetime.timezone.utc):
    raise SystemExit("signed release manifest is expired")
version = version_match.group(1).strip()
package = path_match.group(1).strip()
if manifest.get("platform") != platform or manifest.get("version") != version:
    raise SystemExit("signed release identity mismatch")
artifacts = {item["name"]: item for item in manifest.get("artifacts", [])}
if updater_path.split("/")[-1] not in artifacts or package not in artifacts:
    raise SystemExit("signed release omits required artifacts")
import hashlib
updater_artifact = artifacts[updater_path.split("/")[-1]]
if hashlib.sha256(updater).hexdigest() != updater_artifact["sha256"] or len(updater) != updater_artifact["size"]:
    raise SystemExit("updater metadata differs from signed artifact")
print(version)
print(package)
print(manifest.get("commit", ""))
print(manifest.get("key_id", ""))
print(envelope.get("signature", ""))
print(artifacts[package]["size"])
print(envelope.get("key_id", ""))
PY
  )
  if [[ ${#fields[@]} -ne 7 ]]; then
    add_check "Release channel ($platform)" fail "release metadata failed structural validation"
    return 1
  fi

  local version="${fields[0]}" package="${fields[1]}" commit="${fields[2]}"
  local key_id="${fields[3]}" signature="${fields[4]}" package_size="${fields[5]}"
  local envelope_key_id="${fields[6]}"
  if [[ -n "$expected_version" && "$version" != "$expected_version" ]]; then
    add_check "Release channel ($platform)" fail "published $version; required $expected_version"
    return 1
  fi
  if [[ -n "$expected_commit" && "$commit" != "$expected_commit" ]]; then
    add_check "Release channel ($platform)" fail "signed commit does not match the expected commit"
    return 1
  fi

  local public_size package_size_verified=0
  public_size="$(curl -fsSIL --max-time 30 "$release_base/$package" 2>/dev/null | awk 'BEGIN{IGNORECASE=1} /^content-length:/{gsub("\r", "", $2); value=$2} END{print value}')"
  if [[ -n "$public_size" && "$public_size" != "$package_size" ]]; then
    add_check "Release channel ($platform)" fail "public package size differs from signed metadata"
    return 1
  fi
  [[ -n "$public_size" ]] && package_size_verified=1

  local trust_path="$script_dir/../deploy/release-trust/qsdm-hive-release-key.json"
  local cli signature_verified=0
  cli="$(discover_qsdmcli || true)"
  if [[ -f "$trust_path" && -n "$cli" ]]; then
    local trust=()
    mapfile -t trust < <(python3 - "$trust_path" <<'PY'
import json, sys
trust = json.load(open(sys.argv[1], encoding="utf-8"))
print(trust.get("key_id", ""))
print(trust.get("public_key", ""))
PY
    )
    if [[ "${trust[0]:-}" != "$key_id" || "$envelope_key_id" != "$key_id" ||
          -z "${trust[1]:-}" ]]; then
      add_check "Release channel ($platform)" fail "release key differs from the pinned trust root"
      return 1
    fi
    if "$cli" wallet verify --public-key "${trust[1]}" --message-file "$payload" --signature "$signature" >/dev/null 2>&1; then
      signature_verified=1
    else
      add_check "Release channel ($platform)" fail "ML-DSA release signature verification failed"
      return 1
    fi
  fi

  local status=warn summary="$version metadata is consistent; full verification is unavailable"
  if [[ $signature_verified -eq 1 && $package_size_verified -eq 1 ]]; then
    status=pass
    summary="$version is current; signature and package size verified"
  elif [[ $signature_verified -eq 1 ]]; then
    summary="$version signature verified; public package size header is unavailable"
  elif [[ $package_size_verified -eq 1 ]]; then
    summary="$version package size verified; signature verifier is unavailable"
  fi
  add_check "Release channel ($platform)" "$status" "$summary" \
    "$(python3 -c 'import json,sys; print(json.dumps({"version":sys.argv[1],"commit":sys.argv[2],"package":sys.argv[3],"package_size":int(sys.argv[4]),"signature_verified":sys.argv[5]=="1","package_size_verified":sys.argv[6]=="1"}))' "$version" "$commit" "$package" "$package_size" "$signature_verified" "$package_size_verified")"
  release_version_result="$version"
}

echo "QSDM Hive production acceptance"
echo "Read-only checks; no wallet or task state will be changed."
echo

test_release_channel windows latest.yml qsdm-hive-release-windows.json || true
windows_version="$release_version_result"
test_release_channel linux latest-linux.yml qsdm-hive-release-linux.json || true
linux_version="$release_version_result"

if [[ -z "$expected_version" && "$windows_version" == "$linux_version" &&
      "$windows_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  expected_version="$windows_version"
fi
if [[ -n "$expected_version" && -n "$windows_version" &&
      -n "$linux_version" && "$windows_version" == "$linux_version" ]]; then
  add_check "Cross-platform release parity" pass "both updater channels require $expected_version"
else
  add_check "Cross-platform release parity" fail "Windows and Linux updater versions differ"
fi

hive_pids="$(process_pids 'qsdm[-_ ]hive' || true)"
if [[ -z "$hive_pids" ]]; then
  add_check "Hive runtime" fail "QSDM Hive is not running"
else
  hive_count="$(wc -w <<<"$hive_pids" | tr -d ' ')"
  if [[ -z "$installed_version" ]]; then
    installed_version="$(discover_installed_version "$(head -n1 <<<"$hive_pids")" || true)"
  fi
  if [[ -z "$installed_version" ]]; then
    for log in "$HOME/.config/QSDM-Hive/logs/main.log" "$HOME/.config/qsdm-hive/logs/main.log"; do
      if [[ -f "$log" ]]; then
        installed_version="$(grep -Eo 'version[: ]+[0-9]+\.[0-9]+\.[0-9]+' "$log" | tail -n1 | grep -Eo '[0-9]+\.[0-9]+\.[0-9]+' || true)"
        [[ -n "$installed_version" ]] && break
      fi
    done
  fi
  if [[ -n "$installed_version" && -n "$expected_version" && "$installed_version" != "$expected_version" ]]; then
    add_check "Hive runtime" fail "installed $installed_version; required $expected_version" "{\"process_count\":$hive_count}"
  elif [[ -n "$installed_version" ]]; then
    add_check "Hive runtime" pass "Hive $installed_version is running" "{\"process_count\":$hive_count}"
  else
    add_check "Hive runtime" warn "Hive is running; pass --installed-version to prove its version" "{\"process_count\":$hive_count}"
  fi
fi

core_bases=(
  "http://127.0.0.1:8080/api/v1"
  "https://api.qsdm.tech/attest/home-validator/api/v1"
  "https://api.qsdm.tech/api/v1"
)
reachable_bases=()
reachable_tips=()
selected_core=""
canonical_reachable=0
for base in "${core_bases[@]}"; do
  status_file="$work_dir/status-$((${#reachable_bases[@]} + 1)).json"
  if curl -fsS --max-time 15 "$base/status" -o "$status_file" 2>/dev/null; then
    tip="$(python3 -c 'import json,sys; print(int(json.load(open(sys.argv[1])).get("chain_tip",0)))' "$status_file" 2>/dev/null || echo 0)"
    if [[ "$tip" -gt 0 ]]; then
      reachable_bases+=("$base")
      reachable_tips+=("$tip")
      if [[ "$base" == https://* ]]; then
        canonical_reachable=1
        [[ -z "$selected_core" ]] && selected_core="$base"
      fi
      continue
    fi
  fi
  add_check "Core endpoint" warn "$base is unavailable"
done
if [[ ${#reachable_bases[@]} -eq 0 ]]; then
  add_check "Core connectivity" fail "no configured QSDM Core endpoint answered"
else
  [[ -z "$selected_core" ]] && selected_core="${reachable_bases[0]}"
  if [[ $canonical_reachable -eq 0 ]]; then
    add_check "Canonical gateway" fail "no HTTPS canonical gateway answered"
  else
    add_check "Canonical gateway" pass "canonical QSDM Core is reachable"
  fi
  read -r minimum_tip maximum_tip < <(printf '%s\n' "${reachable_tips[@]}" | sort -n | awk 'NR==1{min=$1}{max=$1}END{print min,max}')
  spread=$((maximum_tip - minimum_tip))
  sync_status=pass
  [[ $spread -gt 50 ]] && sync_status=warn
  add_check "Chain synchronization" "$sync_status" "${#reachable_bases[@]} endpoint(s), height spread $spread" \
    "{\"endpoint_count\":${#reachable_bases[@]},\"minimum_height\":$minimum_tip,\"maximum_height\":$maximum_tip,\"height_spread\":$spread}"
fi

if [[ -n "$selected_core" ]]; then
  tasks_file="$work_dir/tasks.json"
  if curl -fsS --max-time 20 "$selected_core/tasks" -o "$tasks_file" 2>/dev/null; then
    task_fields=()
    mapfile -t task_fields < <(python3 - "$tasks_file" <<'PY'
import json, sys
value = json.load(open(sys.argv[1]))
tasks = value if isinstance(value, list) else value.get("tasks", [])
ids = {str(item.get("task_id") or item.get("id") or "") for item in tasks}
required = {
    "qsdm-edge-worker",
    "qsdm-edge-worker-gpu",
    "qsdm-edge-worker-ram",
    "qsdm-mother-hive",
    "qsdm-skyfang-wallet-link",
    "qsdm-system-miner",
}
print(len(tasks))
print(",".join(sorted(required - ids)))
PY
    )
    task_count="${task_fields[0]:-0}"
    missing_tasks="${task_fields[1]:-}"
    if [[ -n "$missing_tasks" ]]; then
      add_check "Task catalog" fail \
        "task catalog omits required production tasks: $missing_tasks"
    elif [[ "$task_count" -gt 0 ]]; then
      add_check "Task catalog" pass \
        "$task_count task(s); required production set is present" \
        "{\"task_count\":$task_count,\"required_task_count\":6}"
    else
      add_check "Task catalog" fail "task catalog is empty"
    fi
  else
    add_check "Task catalog" fail "task catalog request failed"
  fi
  mother_state="$work_dir/mother-hive-state.json"
  if curl -fsS --max-time 15 \
       "$selected_core/tasks/qsdm-mother-hive/state" -o "$mother_state" 2>/dev/null; then
    read -r mother_configured mother_running < <(python3 - "$mother_state" <<'PY'
import json, sys
value = json.load(open(sys.argv[1], encoding="utf-8"))
task = value.get("task") or {}
print(1 if value.get("configured") else 0, int(task.get("running_count", 0)))
PY
    )
    if [[ "$mother_configured" -eq 1 ]]; then
      add_check "Mother Hive protocol" pass \
        "chain task is configured; $mother_running active participant(s)" \
        "{\"running_count\":$mother_running}"
    else
      add_check "Mother Hive protocol" fail \
        "Mother Hive protocol task is not configured"
    fi
  else
    add_check "Mother Hive protocol" fail \
      "Mother Hive protocol state is unavailable"
  fi
else
  add_check "Task catalog" skip "Core is unavailable"
  add_check "Mother Hive protocol" skip "Core is unavailable"
fi

if [[ -z "$wallet_address" ]]; then
  for wallet in "$HOME/.config/QSDM-Hive/hive-signer/wallet.json" "$HOME/.config/qsdm-hive/hive-signer/wallet.json"; do
    if [[ -f "$wallet" ]]; then
      wallet_address="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("address",""))' "$wallet" 2>/dev/null || true)"
      [[ -n "$wallet_address" ]] && break
    fi
  done
fi
if [[ -z "$selected_core" ]]; then
  add_check "Signer wallet" skip "Core is unavailable"
elif [[ "$wallet_address" =~ ^[0-9a-fA-F]{64}$ ]]; then
  if curl -fsS --max-time 15 "$selected_core/wallet/balance?address=$wallet_address" -o "$work_dir/balance.json" 2>/dev/null &&
     curl -fsS --max-time 15 "$selected_core/wallet/nonce?sender=$wallet_address" -o "$work_dir/nonce.json" 2>/dev/null; then
    masked="${wallet_address:0:8}...${wallet_address: -8}"
    wallet_data="$(python3 - "$work_dir/balance.json" "$work_dir/nonce.json" "$masked" <<'PY'
import json, sys
balance = json.load(open(sys.argv[1]))
nonce = json.load(open(sys.argv[2]))
print(json.dumps({"address":sys.argv[3],"balance_cell":str(balance.get("balance_cell",balance.get("balance","available"))),"nonce":nonce.get("nonce")}))
PY
    )"
    add_check "Signer wallet" pass "wallet reads succeeded without exposing key material" "$wallet_data"
  else
    add_check "Signer wallet" fail "wallet balance or nonce lookup failed"
  fi
else
  add_check "Signer wallet" warn "no valid QSDM signer address was discovered"
fi

if [[ $skip_gpu -eq 1 ]]; then
  add_check "NVIDIA mining" skip "GPU checks were disabled"
elif ! command -v nvidia-smi >/dev/null 2>&1; then
  [[ $require_gpu -eq 1 ]] && gpu_status=fail || gpu_status=skip
  add_check "NVIDIA mining" "$gpu_status" "nvidia-smi is not available"
else
  miner_pids="$(process_pids 'qsdmminer-console' || true)"
  solver_pids="$(process_pids 'qsdm-miner-cuda-solver' || true)"
  miner_count="$(awk 'NF{n++}END{print n+0}' <<<"$miner_pids")"
  solver_count="$(awk 'NF{n++}END{print n+0}' <<<"$solver_pids")"
  if [[ "$miner_count" -eq 0 ]]; then
    [[ $require_gpu -eq 1 ]] && gpu_status=fail || gpu_status=warn
    add_check "NVIDIA mining" "$gpu_status" "NVIDIA is available, but the miner task is not running"
  elif [[ "$solver_count" -eq 0 ]]; then
    add_check "NVIDIA mining" fail "miner is running without the CUDA solver"
  else
    max_util=0
    max_memory=0
    gpu_name="NVIDIA GPU"
    for ((sample=0; sample<gpu_sample_seconds; sample++)); do
      row="$(nvidia-smi --query-gpu=name,utilization.gpu,memory.used --format=csv,noheader,nounits 2>/dev/null | head -n1 || true)"
      IFS=',' read -r name util memory <<<"$row"
      gpu_name="$(xargs <<<"${name:-NVIDIA GPU}")"
      util="$(xargs <<<"${util:-0}")"; memory="$(xargs <<<"${memory:-0}")"
      [[ "$util" =~ ^[0-9]+$ && "$util" -gt "$max_util" ]] && max_util="$util"
      [[ "$memory" =~ ^[0-9]+$ && "$memory" -gt "$max_memory" ]] && max_memory="$memory"
      (( sample + 1 < gpu_sample_seconds )) && sleep 1
    done
    if ! nvidia-smi --query-compute-apps=process_name --format=csv,noheader 2>/dev/null | grep -q 'qsdm-miner-cuda-solver'; then
      add_check "NVIDIA mining" fail "CUDA solver is not visible to the NVIDIA driver"
    else
      [[ $max_util -gt 0 ]] && gpu_status=pass || gpu_status=warn
      if [[ $max_util -gt 0 ]]; then
        gpu_summary="CUDA solver is active; observed up to $max_util% GPU use"
      else
        gpu_summary="CUDA solver is registered, but this short sample caught an idle interval"
      fi
      gpu_data="$(python3 -c 'import json,sys; print(json.dumps({"gpu":sys.argv[1],"maximum_utilization_percent":int(sys.argv[2]),"maximum_memory_mib":int(sys.argv[3]),"sample_seconds":int(sys.argv[4])}))' "$gpu_name" "$max_util" "$max_memory" "$gpu_sample_seconds")"
      add_check "NVIDIA mining" "$gpu_status" "$gpu_summary" "$gpu_data"
    fi
  fi
fi

agent_pids="$(process_pids 'qsdm-edge-agent' || true)"
agent_count="$(awk 'NF{n++}END{print n+0}' <<<"$agent_pids")"
if [[ "$agent_count" -gt 0 ]]; then
  add_check "Edge Agent" pass "$agent_count Edge Agent process(es) running" "{\"process_count\":$agent_count}"
else
  add_check "Edge Agent" warn "Edge Agent is not running; pooled-resource tests are inactive"
fi
control_pids="$(process_pids 'qsdm-edge-control' || true)"
control_count="$(awk 'NF{n++}END{print n+0}' <<<"$control_pids")"
if [[ "$control_count" -gt 0 ]]; then
  if curl -sS --max-time 5 -o /dev/null -w '%{http_code}' http://127.0.0.1:7741/ 2>/dev/null | grep -Eq '^(200|401|303)$'; then
    add_check "Edge Control" pass "local authenticated control service is listening"
  else
    add_check "Edge Control" fail "Edge Control process is running but port 7741 is unavailable"
  fi
else
  add_check "Edge Control" skip "Edge Control is not running on this computer"
fi

config_root="${XDG_CONFIG_HOME:-$HOME/.config}"
mother_token_configured=0
for token_file in \
  "$config_root/qsdm-hive/namespace/qsdm-mother-hive/compute-gateway.token" \
  "$config_root/QSDM-Hive/namespace/qsdm-mother-hive/compute-gateway.token"; do
  [[ -f "$token_file" ]] && mother_token_configured=1 && break
done
mother_relay_paired=0
[[ -f "$config_root/QSDM/edge-pool/mother-hive.json" ]] && mother_relay_paired=1
mother_relay_json=false
[[ $mother_relay_paired -eq 1 ]] && mother_relay_json=true
if [[ $mother_token_configured -eq 0 && $mother_relay_paired -eq 0 ]]; then
  add_check "Mother Hive runtime" skip \
    "Mother Hive is not configured on this computer"
else
  mother_gateway_online="$(python3 - <<'PY'
import socket
sock = socket.socket()
sock.settimeout(3)
try:
    print(1 if sock.connect_ex(("127.0.0.1", 7742)) == 0 else 0)
finally:
    sock.close()
PY
  )"
  if [[ $mother_token_configured -eq 1 && "$mother_gateway_online" -eq 1 ]]; then
    add_check "Mother Hive runtime" pass "local compute gateway is online" \
      "{\"relay_paired\":$mother_relay_json,\"compute_gateway_online\":true}"
  else
    add_check "Mother Hive runtime" warn \
      "Mother Hive is configured but its compute gateway is not running" \
      "{\"relay_paired\":$mother_relay_json,\"compute_gateway_online\":false}"
  fi
fi

if [[ $skip_logs -eq 1 ]]; then
  add_check "Recent Hive logs" skip "log scan was disabled"
else
  hive_log=""
  for log in "$HOME/.config/QSDM-Hive/logs/main.log" "$HOME/.config/qsdm-hive/logs/main.log"; do
    [[ -f "$log" ]] && hive_log="$log" && break
  done
  if [[ -z "$hive_log" ]]; then
    add_check "Recent Hive logs" warn "Hive main.log was not found"
  else
    log_data="$(python3 - "$hive_log" "$log_window_minutes" <<'PY'
import datetime, json, re, sys
path, minutes = sys.argv[1], int(sys.argv[2])
cutoff = datetime.datetime.now() - datetime.timedelta(minutes=minutes)
counts = {"errors":0,"fatal_or_uncaught":0,"timeouts":0,"disconnects":0,"window_minutes":minutes}
timestamp = datetime.datetime.min
lines = open(path, encoding="utf-8", errors="replace").readlines()[-5000:]
for line in lines:
    match = re.match(r"^\[?(\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2})", line)
    if match:
        try: timestamp = datetime.datetime.fromisoformat(match.group(1))
        except ValueError: pass
    if timestamp < cutoff: continue
    lower = line.lower()
    if re.search(r"\[(error|fatal)\]|uncaught runtime|unhandled", lower): counts["errors"] += 1
    if re.search(r"fatal|uncaught runtime|unhandled rejection", lower): counts["fatal_or_uncaught"] += 1
    if re.search(r"timeout|econnaborted|etimedout", lower): counts["timeouts"] += 1
    if re.search(r"disconnect|econnreset|econnrefused", lower): counts["disconnects"] += 1
print(json.dumps(counts))
PY
    )"
    read -r errors fatals timeouts disconnects < <(python3 -c 'import json,sys; x=json.loads(sys.argv[1]); print(x["errors"],x["fatal_or_uncaught"],x["timeouts"],x["disconnects"])' "$log_data")
    if [[ $fatals -gt 0 ]]; then log_status=fail
    elif [[ $errors -gt 0 || $timeouts -gt 5 || $disconnects -gt 5 ]]; then log_status=warn
    else log_status=pass
    fi
    add_check "Recent Hive logs" "$log_status" "$errors error(s), $timeouts timeout(s), $disconnects disconnect(s)" "$log_data"
  fi
fi

finished_epoch_ms="$(python3 -c 'import time; print(int(time.time()*1000))')"
if [[ -z "$output_path" ]]; then
  output_path="$PWD/qsdm-hive-acceptance-$(date -u +%Y%m%d-%H%M%S).json"
fi
mkdir -p "$(dirname "$output_path")"
python3 - "$checks_file" "$output_path" "$expected_version" "$expected_commit" "$started_epoch_ms" "$finished_epoch_ms" <<'PY'
import datetime, json, platform, sys
checks_path, output_path, expected_version, expected_commit, started, finished = sys.argv[1:]
checks = [json.loads(line) for line in open(checks_path, encoding="utf-8") if line.strip()]
summary = {"passed":0,"warnings":0,"failed":0,"skipped":0}
mapping = {"pass":"passed","warn":"warnings","fail":"failed","skip":"skipped"}
for check in checks: summary[mapping[check["status"]]] += 1
report = {
    "schema":"qsdm.hive.production-acceptance.v1",
    "generated_at":datetime.datetime.now(datetime.timezone.utc).isoformat(),
    "expected_version":expected_version,
    "expected_commit":expected_commit,
    "operating_system":platform.platform(),
    "architecture":platform.machine(),
    "duration_ms":int(finished)-int(started),
    "read_only":True,
    "summary":summary,
    "checks":checks,
}
with open(output_path, "w", encoding="utf-8") as handle:
    json.dump(report, handle, indent=2)
    handle.write("\n")
print(json.dumps(summary))
PY

summary="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["summary"]["passed"],json.load(open(sys.argv[1]))["summary"]["warnings"],json.load(open(sys.argv[1]))["summary"]["failed"],json.load(open(sys.argv[1]))["summary"]["skipped"])' "$output_path")"
read -r passed warnings failed skipped <<<"$summary"
echo
echo "Evidence: $output_path"
echo "Passed $passed; warnings $warnings; failed $failed; skipped $skipped"
if [[ $failed -gt 0 || ($strict_warnings -eq 1 && $warnings -gt 0) ]]; then
  exit 1
fi
