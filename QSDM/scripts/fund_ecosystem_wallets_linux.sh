#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
PLAN_PATH="$SCRIPT_DIR/../deploy/canonical-pilot-funding-plan.json"
QSDMCLI_PATH="${QSDMCLI:-}"
KEYSTORE_PATH="${QSDM_KEYSTORE:-$HOME/.qsdm/wallet.json}"
PASSPHRASE_FILE="${QSDM_PASSPHRASE_FILE:-}"
SUBMIT=false

usage() {
  cat <<'EOF'
Usage: fund_ecosystem_wallets_linux.sh [options]

Dry-runs the canonical pilot treasury funding plan by default. On --submit,
the script requires the exact source wallet, asks for a typed confirmation,
signs one transfer at a time, and waits for canonical balance confirmation.

Options:
  --plan PATH             Public funding plan JSON.
  --qsdmcli PATH          qsdmcli executable (or set QSDMCLI).
  --keystore PATH         Funding-wallet keystore JSON.
  --passphrase-file PATH  Funding-wallet passphrase file; required to submit.
  --submit                Sign and submit missing target balances.
  -h, --help              Show this help.
EOF
}

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

need_command() {
  command -v "$1" >/dev/null 2>&1 || die "Required command is missing: $1"
}

while (($# > 0)); do
  case "$1" in
    --plan)
      (($# >= 2)) || die "--plan needs a path"
      PLAN_PATH="$2"
      shift 2
      ;;
    --qsdmcli)
      (($# >= 2)) || die "--qsdmcli needs a path"
      QSDMCLI_PATH="$2"
      shift 2
      ;;
    --keystore)
      (($# >= 2)) || die "--keystore needs a path"
      KEYSTORE_PATH="$2"
      shift 2
      ;;
    --passphrase-file)
      (($# >= 2)) || die "--passphrase-file needs a path"
      PASSPHRASE_FILE="$2"
      shift 2
      ;;
    --submit)
      SUBMIT=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "Unknown option: $1"
      ;;
  esac
done

need_command curl
need_command python3
[[ -f "$PLAN_PATH" ]] || die "Funding plan not found: $PLAN_PATH"
[[ -f "$KEYSTORE_PATH" ]] || die "Source keystore not found: $KEYSTORE_PATH"

if [[ -z "$QSDMCLI_PATH" ]]; then
  for candidate in \
    "$(command -v qsdmcli 2>/dev/null || true)" \
    "$SCRIPT_DIR/../source/qsdmcli" \
    "/opt/qsdm/bin/qsdmcli" \
    "$HOME/.local/bin/qsdmcli"; do
    if [[ -n "$candidate" && -x "$candidate" ]]; then
      QSDMCLI_PATH="$candidate"
      break
    fi
  done
fi
[[ -n "$QSDMCLI_PATH" && -x "$QSDMCLI_PATH" ]] || \
  die "qsdmcli was not found. Pass its executable path with --qsdmcli."

mapfile -t plan_meta < <(python3 - "$PLAN_PATH" <<'PY' | tr -d '\r'
import json
import re
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    plan = json.load(handle)

if plan.get("schema_version") != 1:
    raise SystemExit("unsupported funding-plan schema")
source = str(plan.get("expected_source_address", "")).lower()
if not re.fullmatch(r"[0-9a-f]{64}", source):
    raise SystemExit("invalid expected_source_address")
api = str(plan.get("api_url", "")).rstrip("/")
if api != "https://api.qsdm.tech":
    raise SystemExit("canonical pilot plan must use https://api.qsdm.tech")
fee = str(plan.get("fee_cell", ""))
confirmation = str(plan.get("submit_confirmation", ""))
if not confirmation:
    raise SystemExit("submit confirmation is missing")
print(str(plan.get("network", "")))
print(api)
print(source)
print(fee)
print(confirmation)
PY
)

[[ ${#plan_meta[@]} -eq 5 ]] || die "Funding plan metadata is incomplete."
NETWORK="${plan_meta[0]}"
API_URL="${plan_meta[1]}"
EXPECTED_SOURCE="${plan_meta[2]}"
FEE_CELL="${plan_meta[3]}"
CONFIRMATION="${plan_meta[4]}"

wallet_json="$("$QSDMCLI_PATH" wallet show --json --in "$KEYSTORE_PATH")" || \
  die "qsdmcli could not inspect the source keystore."
SENDER="$(python3 -c 'import json,sys; print(str(json.load(sys.stdin).get("address", "")).lower())' <<<"$wallet_json")"
[[ "$SENDER" == "$EXPECTED_SOURCE" ]] || \
  die "Wrong funding wallet. Expected $EXPECTED_SOURCE but the keystore contains $SENDER."

get_balance() {
  local address="$1"
  curl --fail --silent --show-error --max-time 20 \
    "$API_URL/api/v1/wallet/balance?address=$address" |
    python3 -c 'import json,sys; print(json.load(sys.stdin)["balance"])'
}

decimal_delta() {
  python3 - "$1" "$2" <<'PY'
from decimal import Decimal, InvalidOperation
import sys

try:
    current = Decimal(sys.argv[1])
    target = Decimal(sys.argv[2])
except InvalidOperation as exc:
    raise SystemExit(f"invalid decimal value: {exc}")
delta = target - current
if delta < 0:
    delta = Decimal(0)
print(format(delta, "f"))
PY
}

decimal_add() {
  python3 - "$1" "$2" <<'PY'
from decimal import Decimal
import sys
print(format(Decimal(sys.argv[1]) + Decimal(sys.argv[2]), "f"))
PY
}

decimal_le() {
  python3 - "$1" "$2" <<'PY'
from decimal import Decimal
import sys
raise SystemExit(0 if Decimal(sys.argv[1]) <= Decimal(sys.argv[2]) else 1)
PY
}

roles=()
addresses=()
targets=()
deltas=()
total_transfer="0"

while IFS=$'\t' read -r role address target maximum; do
  [[ -n "$role" ]] || continue
  current="$(get_balance "$address")" || die "Could not read canonical balance for $role."
  delta="$(decimal_delta "$current" "$target")"
  decimal_le "$delta" "$maximum" || \
    die "$role needs $delta CELL, above its approved maximum of $maximum CELL."
  printf '%-24s current=%-14s target=%-14s missing=%s CELL\n' \
    "$role" "$current" "$target" "$delta"
  if [[ "$delta" != "0" && "$delta" != "0.0" && "$delta" != "0.00000000" ]]; then
    roles+=("$role")
    addresses+=("$address")
    targets+=("$target")
    deltas+=("$delta")
    total_transfer="$(decimal_add "$total_transfer" "$delta")"
  fi
done < <(python3 - "$PLAN_PATH" <<'PY' | tr -d '\r'
from decimal import Decimal, InvalidOperation
import json
import re
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    plan = json.load(handle)

seen_roles = set()
seen_addresses = set()
for entry in plan.get("transfers", []):
    if not entry.get("enabled", False):
        continue
    role = str(entry.get("role", ""))
    address = str(entry.get("address", "")).lower()
    if not role or role in seen_roles:
        raise SystemExit("duplicate or empty enabled role")
    if not re.fullmatch(r"[0-9a-f]{64}", address) or address in seen_addresses:
        raise SystemExit(f"invalid or duplicate address for {role}")
    try:
        target = Decimal(str(entry["target_balance_cell"]))
        maximum = Decimal(str(entry["maximum_transfer_cell"]))
    except (KeyError, InvalidOperation) as exc:
        raise SystemExit(f"invalid amount for {role}: {exc}")
    if target <= 0 or maximum <= 0 or target > maximum:
        raise SystemExit(f"unsafe target or maximum for {role}")
    seen_roles.add(role)
    seen_addresses.add(address)
    print(f"{role}\t{address}\t{target:.8f}\t{maximum:.8f}")
PY
)

source_balance="$(get_balance "$SENDER")" || die "Could not read the source balance."
fee_total="$(python3 - "$FEE_CELL" "${#roles[@]}" <<'PY'
from decimal import Decimal
import sys
print(format(Decimal(sys.argv[1]) * Decimal(sys.argv[2]), "f"))
PY
)"
total_needed="$(decimal_add "$total_transfer" "$fee_total")"
decimal_le "$total_needed" "$source_balance" || \
  die "Source balance is $source_balance CELL but this plan needs $total_needed CELL including fees."

printf '\nQSDM ecosystem funding preflight\n'
printf '  Network:       %s\n' "$NETWORK"
printf '  Canonical API: %s\n' "$API_URL"
printf '  Source:        %s\n' "$SENDER"
printf '  Source balance:%s CELL\n' "$source_balance"
printf '  Missing funds: %s CELL\n' "$total_transfer"
printf '  Fees:          %s CELL\n' "$fee_total"
printf '  Transfers:     %s\n' "${#roles[@]}"

if ((${#roles[@]} == 0)); then
  printf '\nAll enabled wallets already meet their target balances. Nothing to submit.\n'
  exit 0
fi

if [[ "$SUBMIT" != true ]]; then
  printf '\nDry run only. Re-run with --submit after reviewing this output.\n'
  exit 0
fi

[[ -n "$PASSPHRASE_FILE" && -f "$PASSPHRASE_FILE" ]] || \
  die "--passphrase-file is required for submission."
file_mode="$(stat -c '%a' "$PASSPHRASE_FILE")"
file_mode="${file_mode: -3}"
(( (8#$file_mode & 077) == 0 )) || \
  die "Passphrase file permissions are too broad ($file_mode). Run: chmod 600 '$PASSPHRASE_FILE'"
[[ -r /dev/tty ]] || die "Submission needs an interactive terminal for typed confirmation."
printf '\nType %s to authorize these bounded transfers: ' "$CONFIRMATION" > /dev/tty
IFS= read -r typed_confirmation < /dev/tty
[[ "$typed_confirmation" == "$CONFIRMATION" ]] || die "Confirmation did not match; nothing was submitted."

CACHE_DIR="$HOME/.qsdm/treasury-funding"
mkdir -p "$CACHE_DIR"
chmod 700 "$CACHE_DIR"

for index in "${!roles[@]}"; do
  role="${roles[$index]}"
  address="${addresses[$index]}"
  target="${targets[$index]}"
  current="$(get_balance "$address")"
  delta="$(decimal_delta "$current" "$target")"
  if [[ "$delta" == "0" || "$delta" == "0.0" || "$delta" == "0.00000000" ]]; then
    printf '\n%s already reached its target; skipping.\n' "$role"
    continue
  fi

  timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
  random="$(od -An -N5 -tx1 /dev/urandom | tr -d ' \n')"
  tx_id="treasury_${role}_${timestamp}_${random}"
  unsigned_path="$CACHE_DIR/$tx_id.unsigned.json"
  signed_path="$CACHE_DIR/$tx_id.signed.json"
  response_path="$CACHE_DIR/$tx_id.response.json"

  python3 - "$unsigned_path" "$tx_id" "$SENDER" "$address" "$delta" "$FEE_CELL" "$role" <<'PY'
from decimal import Decimal
from datetime import datetime, timezone
import json
import sys

path, tx_id, sender, recipient, amount_raw, fee_raw, role = sys.argv[1:]
payload = {
    "id": tx_id,
    "sender": sender,
    "recipient": recipient,
    "amount": "__AMOUNT__",
    "fee": "__FEE__",
    "geotag": f"qsdm-treasury-{role}",
    "parent_cells": [],
    "timestamp": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
}
raw = json.dumps(payload, separators=(",", ":"))
raw = raw.replace('"__AMOUNT__"', format(Decimal(amount_raw), "f"))
raw = raw.replace('"__FEE__"', format(Decimal(fee_raw), "f"))
with open(path, "w", encoding="utf-8") as handle:
    handle.write(raw)
PY
  chmod 600 "$unsigned_path"

  printf '\nSubmitting %s: %s CELL -> %s\n' "$role" "$delta" "$address"
  "$QSDMCLI_PATH" wallet sign-tx \
    --in "$KEYSTORE_PATH" \
    --envelope-file "$unsigned_path" \
    --auto-nonce \
    --api-url "$API_URL" \
    --passphrase-file "$PASSPHRASE_FILE" > "$signed_path" || \
    die "Signing failed for $role; no later transfers were attempted."
  chmod 600 "$signed_path"

  python3 - "$signed_path" "$SENDER" "$address" "$delta" <<'PY'
from decimal import Decimal
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    envelope = json.load(handle)
if str(envelope.get("sender", "")).lower() != sys.argv[2]:
    raise SystemExit("signed sender mismatch")
if str(envelope.get("recipient", "")).lower() != sys.argv[3]:
    raise SystemExit("signed recipient mismatch")
if Decimal(str(envelope.get("amount"))) != Decimal(sys.argv[4]):
    raise SystemExit("signed amount mismatch")
PY

  curl --fail --silent --show-error --max-time 30 \
    -X POST \
    -H 'Content-Type: application/json' \
    --data-binary "@$signed_path" \
    "$API_URL/api/v1/wallet/submit-signed" > "$response_path" || \
    die "Canonical submission failed for $role. Inspect $response_path before retrying."
  chmod 600 "$response_path"

  tx_result="$(python3 - "$response_path" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    response = json.load(handle)
print(f"{response.get('transaction_id', '-')}/{response.get('status', '-')}")
PY
)"
  printf '  Accepted: %s\n' "$tx_result"

  confirmed=false
  for _ in $(seq 1 36); do
    current="$(get_balance "$address")" || true
    if [[ -n "$current" ]] && decimal_le "$target" "$current"; then
      confirmed=true
      break
    fi
    sleep 5
  done
  [[ "$confirmed" == true ]] || \
    die "$role was submitted but its target balance was not confirmed within 180 seconds. Stop and inspect before retrying."
  printf '  Confirmed canonical balance: %s CELL\n' "$current"
done

printf '\nAll enabled treasury targets are confirmed on the canonical ledger.\n'
