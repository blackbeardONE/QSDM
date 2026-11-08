#!/usr/bin/env bash
# test-install-full-node.sh: runs install-full-node.sh end to end in a sandbox.
#
# A local HTTP server (Python) serves synthetic release files, a synthetic qsdm.tech/fullnode.html,
# a fake public API and, on the same port, a fake API of the installed node. The synthetic release
# holds a fake qsdm that imports a snapshot the way the real one reports it, and a fake qsdmcli
# checks a made-up signature. Stand-ins on PATH replace sudo, useradd, id, install, runuser,
# systemctl, journalctl and df, and --root keeps every file in a temporary folder, so nothing on
# this machine changes. A curl stand-in refuses every address other than 127.0.0.1, so the test
# never touches the internet.
#
# Usage: bash QSDM/scripts/fullnode/test-install-full-node.sh   (KEEP=1 keeps the temporary folder)
# Needs bash 4.4 or later, Python 3.7 or later, curl, GNU coreutils, tar and gzip.
# Runs on Linux and in Git Bash on Windows.

set -euo pipefail
export LC_ALL=C

HERE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
INSTALLER=$HERE/install-full-node.sh
GUIDE=$(cd -- "$HERE/../../docs/docs" && pwd -P)/RUN_A_FULL_NODE.md

abort() {
  printf 'test-install-full-node: %s\n' "$*" >&2
  exit 2
}

PY=$(command -v python3 || command -v python || true)
[[ -n $PY ]] || abort "Python 3 is needed"
"$PY" -c 'import sys; sys.exit(0 if sys.version_info >= (3, 7) else 1)' || abort "Python 3.7 or later is needed"
for tool in curl sha256sum tar gzip split install find cmp sed awk seq; do
  command -v "$tool" >/dev/null 2>&1 || abort "$tool is needed"
done
[[ -f $INSTALLER && -f $GUIDE ]] || abort "cannot find the installer or the guide next to this test"

# Short names: Python on Windows cannot open paths longer than 260 characters.
T=$(mktemp -d "${TMPDIR:-/tmp}/qfn.XXXXXX")
T=$(cd -- "$T" && pwd -P)
WWW=$T/www
SERVER_LOG=$T/server.log
SERVER_PID=''
mkdir -p "$T/shims" "$T/tools" "$T/fixtures" "$T/build" "$T/control" "$T/cases" "$T/tmp" "$WWW/site"
: >"$SERVER_LOG"

cleanup() {
  touch "$T/control/stop"
  if [[ -n $SERVER_PID ]]; then
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  if [[ ${KEEP:-0} == 1 ]]; then
    printf 'kept %s\n' "$T"
  else
    rm -rf -- "$T"
  fi
}
trap cleanup EXIT

pyrun() {
  "$PY" "$@" | tr -d '\r'
}

sha() {
  local out
  out=$(printf '%s' "$1" | sha256sum)
  printf '%s' "${out%% *}"
}

sha_file() {
  local out
  out=$(sha256sum <"$1")
  printf '%s' "${out%% *}"
}

size_of() {
  wc -c <"$1" | tr -d ' '
}

# ---------------------------------------------------------------------------------------------------
# Stand-ins on PATH
# ---------------------------------------------------------------------------------------------------

REAL_CURL=$(command -v curl)
REAL_INSTALL=$(command -v install)
REAL_ID=$(command -v id)
REAL_DF=$(command -v df)
export REAL_CURL REAL_INSTALL REAL_ID REAL_DF
export SHIM_LOG=$T/shim.log SHIM_STATE=$T/shim-state

shim() {
  cat >"$T/shims/$1"
  chmod +x "$T/shims/$1"
}

shim sudo <<'SH'
#!/usr/bin/env bash
printf 'sudo %s\n' "$*" >>"$SHIM_LOG"
if [[ ${1:-} == -v ]]; then
  exit 0
fi
exec "$@"
SH

shim useradd <<'SH'
#!/usr/bin/env bash
printf 'useradd %s\n' "$*" >>"$SHIM_LOG"
if [[ -f $SHIM_STATE/useradd-fails ]]; then
  echo "useradd: cannot lock /etc/passwd; try again later (test)" >&2
  exit 1
fi
user=${!#}
if grep -qx -- "$user" "$SHIM_STATE/users" 2>/dev/null; then
  echo "useradd: user '$user' already exists" >&2
  exit 9
fi
printf '%s\n' "$user" >>"$SHIM_STATE/users"
SH

shim id <<'SH'
#!/usr/bin/env bash
if [[ $# -eq 2 && $1 == -u ]]; then
  if grep -qx -- "$2" "$SHIM_STATE/users" 2>/dev/null; then
    echo 990
    exit 0
  fi
  if [[ $2 == qsdm-follower ]]; then
    echo "id: '$2': no such user" >&2
    exit 1
  fi
fi
exec "$REAL_ID" "$@"
SH

# Drops -o and -g (the test runs as an ordinary user). Folders are made first and given their mode
# after, because Git Bash's install cannot make a folder with mode 0700 (chmod can).
shim install <<'SH'
#!/usr/bin/env bash
printf 'install %s\n' "$*" >>"$SHIM_LOG"
options=() operands=() mode='' dirs=0
while [[ $# -gt 0 ]]; do
  case $1 in
    -o | -g) shift 2 ;;
    -m) mode=$2; options+=(-m "$2"); shift 2 ;;
    -d) dirs=1; options+=(-d); shift ;;
    --) shift; operands+=("$@"); break ;;
    -*) options+=("$1"); shift ;;
    *) operands+=("$1"); shift ;;
  esac
done
if [[ $dirs == 1 && -n $mode ]]; then
  "$REAL_INSTALL" -d "${operands[@]}" && exec chmod "$mode" "${operands[@]}"
  exit 1
fi
exec "$REAL_INSTALL" "${options[@]}" "${operands[@]}"
SH

shim runuser <<'SH'
#!/usr/bin/env bash
printf 'runuser %s\n' "$*" >>"$SHIM_LOG"
if [[ ${1:-} != -u || ${3:-} != -- ]]; then
  echo "runuser stand-in: unexpected arguments" >&2
  exit 2
fi
shift 3
exec "$@"
SH

shim systemctl <<'SH'
#!/usr/bin/env bash
printf 'systemctl %s\n' "$*" >>"$SHIM_LOG"
case ${1:-} in
  daemon-reload) ;;
  enable)
    if [[ ${2:-} == --now ]]; then
      touch "$SHIM_STATE/active"
    fi
    ;;
  is-active) [[ -f $SHIM_STATE/active ]] || exit 3 ;;
  is-failed) [[ -f $SHIM_STATE/failed ]] || exit 1 ;;
  stop) rm -f "$SHIM_STATE/active" ;;
  *)
    echo "systemctl stand-in: unexpected arguments: $*" >&2
    exit 2
    ;;
esac
SH

shim journalctl <<'SH'
#!/usr/bin/env bash
printf 'journalctl %s\n' "$*" >>"$SHIM_LOG"
echo "(test journal) qsdm-follower: no entries"
SH

shim curl <<'SH'
#!/usr/bin/env bash
for arg in "$@"; do
  case $arg in
    http://127.0.0.1:*) ;;
    *://*)
      printf 'curl BLOCKED %s\n' "$arg" >>"$SHIM_LOG"
      echo "test: refusing to contact $arg" >&2
      exit 7
      ;;
  esac
done
exec "$REAL_CURL" "$@"
SH

shim df <<'SH'
#!/usr/bin/env bash
if [[ -f $SHIM_STATE/tiny-disk ]]; then
  printf 'Filesystem 1024-blocks Used Available Capacity Mounted on\n'
  printf '/dev/test 20000000 19000000 1000000 95%% /\n'
  exit 0
fi
exec "$REAL_DF" "$@"
SH

if ! command -v python3 >/dev/null 2>&1; then
  printf '#!/usr/bin/env bash\nexec %q "$@"\n' "$PY" >"$T/shims/python3"
  chmod +x "$T/shims/python3"
fi

# On Windows, Python cannot run a bash script directly: the fake verifier runs qsdmcli through this bash.
if command -v cygpath >/dev/null 2>&1; then
  QSDM_TEST_BASH=$(cygpath -m "$BASH")
  export QSDM_TEST_BASH
fi

# The installer must not find a real qsdmcli: drop every PATH folder that has one.
CLEAN_PATH=''
IFS=: read -r -a path_dirs <<<"$PATH"
for dir in "${path_dirs[@]}"; do
  if [[ -z $dir || -x $dir/qsdmcli ]]; then
    continue
  fi
  CLEAN_PATH+=${CLEAN_PATH:+:}$dir
done
TEST_PATH=$T/shims:$CLEAN_PATH
for tool in sudo useradd id install runuser systemctl journalctl curl df; do
  [[ $(PATH=$TEST_PATH && command -v "$tool") == "$T/shims/$tool" ]] || abort "the $tool stand-in is not first on PATH"
done

# ---------------------------------------------------------------------------------------------------
# The fake qsdm, qsdmcli and verify-follower-release.py
# ---------------------------------------------------------------------------------------------------

cat >"$T/fixtures/qsdm" <<'FAKE'
#!/usr/bin/env bash
# Test stand-in for qsdm: "--version" and "--snapshot-import --from DIR", as RUN_A_FULL_NODE.md uses
# them. Like the real import it refuses a state directory that holds chain files, refuses a snapshot
# whose snapshot-checkpoint.json differs from the pinned checkpoint (exit 78), installs three files
# and writes qsdm_snapshot_import.json.
set -u
root=$(cd -- "$(dirname -- "$0")/../.." && pwd)
{
  printf 'argv:'
  printf ' %s' "$@"
  printf '\ncwd: %s\n' "$PWD"
  env | sort | sed 's/^/env: /'
} >>"$root/fake-qsdm.log"
fatal() {
  echo "FATAL (exit 78): snapshot bootstrap: $*" >&2
  echo "ERROR: snapshot bootstrap: $*" >>"${LOG_FILE:-/dev/null}"
  exit 78
}
pin() {
  awk -v key="$1" '/^\[/ { table = $0; next }
    table == "[snapshot_bootstrap]" && $1 == key { value = $3; gsub(/"/, "", value); print value; exit }' "$CONFIG_FILE"
}
if [[ $# -eq 1 && $1 == --version ]]; then
  echo "qsdm (test stand-in)"
  exit 0
fi
if [[ $# -ne 3 || $1 != --snapshot-import || $2 != --from ]]; then
  fatal "usage: qsdm --snapshot-import --from <snapshot dir>"
fi
from=$3
state=$PWD
for name in qsdm_chain.ndjson qsdm_accounts.json qsdm_enrollment.json qsdm_staking.json qsdm_receipts.ndjson \
  qsdm_receipts.json qsdm_governance.json qsdm_snapshot_import.json; do
  if [[ -e $state/$name ]]; then
    fatal "state directory already holds $name: import only fills an empty follower state directory"
  fi
done
height=$(pin checkpoint_height)
hash=$(pin checkpoint_block_hash)
root_hash=$(pin checkpoint_state_root)
producer=$(pin checkpoint_producer_id)
want="{\"height\": $height, \"block_hash\": \"$hash\", \"state_root\": \"$root_hash\", \"producer_id\": \"$producer\"}"
if [[ $(cat "$from/snapshot-checkpoint.json") != "$want" ]]; then
  fatal "the snapshot's snapshot-checkpoint.json names another checkpoint, not the trusted checkpoint"
fi
echo "INFO: snapshot import: the journal links to genesis and ends at the pinned checkpoint" | tee -a "$LOG_FILE"
cp "$from/qsdm_enrollment.json" "$from/qsdm_accounts.json" "$from/qsdm_chain.ndjson" "$state/"
authorized_by=producer_transition
if [[ -f $root/fake-authorized-by ]]; then
  authorized_by=$(cat "$root/fake-authorized-by")
fi
installed=''
for name in qsdm_enrollment.json qsdm_accounts.json qsdm_chain.ndjson; do
  sum=$(sha256sum <"$state/$name")
  size=$(wc -c <"$state/$name")
  installed="$installed${installed:+, }{\"name\": \"$name\", \"size_bytes\": ${size//[!0-9]/}, \"sha256\": \"${sum%% *}\"}"
done
record="{\"state_dir\": \"$state\", \"checkpoint\": {\"height\": $height, \"block_hash\": \"$hash\", \"state_root\": \"$root_hash\", \"producer_id\": \"$producer\", \"tip_signed\": true, \"blocks\": $((height + 1)), \"journal_bytes\": 1}, \"authorized_by\": \"$authorized_by\", \"enrollment_root_checked\": true, \"installed\": [$installed], \"imported_at\": \"2026-11-03T00:00:00Z\", \"journal_load_seconds\": 0.1, \"restore_seconds\": 0.1, \"total_seconds\": 0.2}"
printf '%s\n' "$record" >"$state/qsdm_snapshot_import.json"
printf '%s\n' "$record"
FAKE
{
  cat "$T/fixtures/qsdm"
  printf '# a changed copy: not the binary qsdm.tech names\n'
} >"$T/fixtures/qsdm-tampered"

cat >"$T/tools/qsdmcli" <<'FAKE'
#!/usr/bin/env bash
# Test stand-in for qsdmcli: "wallet verify" with the test's made-up signature scheme.
set -euo pipefail
if [[ ${1:-} != wallet || ${2:-} != verify ]]; then
  echo "qsdmcli (test): unsupported command" >&2
  exit 2
fi
shift 2
pk_file='' msg_file='' sig_file=''
while [[ $# -gt 0 ]]; do
  case $1 in
    --public-key-file) pk_file=$2 ;;
    --message-file) msg_file=$2 ;;
    --signature-file) sig_file=$2 ;;
    *) echo "qsdmcli (test): unexpected $1" >&2; exit 2 ;;
  esac
  shift 2
done
pk=$(tr -d ' \r\n' <"$pk_file")
msg=$(sha256sum <"$msg_file")
seed=$(printf '%s:%s' "$pk" "${msg%% *}" | sha256sum)
want=''
while [[ ${#want} -lt 9254 ]]; do
  want+=${seed%% *}
done
if [[ $(tr -d ' \r\n' <"$sig_file") == "${want:0:9254}" ]]; then
  echo "OK: ML-DSA-87 signature verified"
else
  echo "FAIL: ML-DSA-87 signature does not verify"
  exit 1
fi
FAKE
chmod +x "$T/tools/qsdmcli"

cat >"$T/fixtures/verify-follower-release.py" <<'FAKE'
#!/usr/bin/env python3
"""Test stand-in for verify-follower-release.py: the same command line and the same last line."""
import argparse
import hashlib
import json
import os
import re
import subprocess
import sys
import tempfile


def fail(message):
    print(f"verify-follower-release: FAIL: {message}", file=sys.stderr)
    sys.exit(1)


def sha(path):
    with open(path, "rb") as f:
        return hashlib.sha256(f.read()).hexdigest()


ap = argparse.ArgumentParser()
ap.add_argument("bundle")
ap.add_argument("--public-key-json", required=True)
ap.add_argument("--expected-key-id", required=True)
ap.add_argument("--qsdmcli", required=True)
a = ap.parse_args()
sums = {}
with open(os.path.join(a.bundle, "SHA256SUMS"), encoding="utf-8") as f:
    for line in f.read().splitlines():
        m = re.fullmatch(r"([0-9a-f]{64}) [ *](.+)", line)
        if not m:
            fail(f"not a sha256sum line: {line!r}")
        sums[m.group(2)] = m.group(1)
for name, digest in sums.items():
    if sha(os.path.join(a.bundle, name)) != digest:
        fail(f"{name}: SHA-256 does not match SHA256SUMS")
with open(a.public_key_json, encoding="utf-8") as f:
    public_key = json.load(f)["public_key"]
if hashlib.sha256(bytes.fromhex(public_key)).hexdigest() != a.expected_key_id:
    fail("the public key's key_id is not --expected-key-id")
fd, key_path = tempfile.mkstemp(suffix=".hex")
with os.fdopen(fd, "w") as f:
    f.write(public_key + "\n")
command = [a.qsdmcli, "wallet", "verify", "--public-key-file", key_path.replace("\\", "/"),
           "--message-file", os.path.join(a.bundle, "follower-manifest.json"),
           "--signature-file", os.path.join(a.bundle, "follower-manifest.json.sig")]
if os.name == "nt":
    command.insert(0, os.environ.get("QSDM_TEST_BASH", "bash"))
result = subprocess.run(command, capture_output=True, text=True)
os.unlink(key_path)
if result.returncode != 0 or "OK: ML-DSA-87 signature verified" not in result.stdout:
    fail(f"qsdmcli wallet verify failed: {result.stdout}{result.stderr}")
with open(os.path.join(a.bundle, "follower-manifest.json"), encoding="utf-8") as f:
    release = json.load(f)["release"]
print("sha256sums: OK: " + ", ".join(sorted(sums)))
print("signature: OK (test stand-in)")
print(f"verify-follower-release: VERIFIED {release} qsdm {sums['qsdm']}")
FAKE

# ---------------------------------------------------------------------------------------------------
# The local server: files under www/, the public API under /public/api/v1, the node's API under /api/v1
# ---------------------------------------------------------------------------------------------------

M=1000
TIP=1300
RELEASE=hl-test-rc9999
TAG=fullnode-rc9999
PRODUCER=$(sha producer)
CP_HASH=$(sha "block-$M")
CP_ROOT=$(sha "root-$M")

cat >"$T/server.py" <<'PY'
import hashlib
import http.server
import json
import os
import sys
import threading
import time
import urllib.parse

root, control, tip, producer, portfile, logfile = sys.argv[1:7]
TIP = int(tip)


def sha(text):
    return hashlib.sha256(text.encode()).hexdigest()


def flag(name):
    return os.path.exists(os.path.join(control, name))


LOCK = threading.Lock()


def take(name):
    """True while the number in control/NAME is above 0; each call takes one."""
    path = os.path.join(control, name)
    with LOCK:
        try:
            with open(path, encoding="utf-8") as f:
                left = int(f.read().strip() or 0)
        except (OSError, ValueError):
            return False
        if left <= 0:
            return False
        with open(path, "w", encoding="utf-8") as f:
            f.write(str(left - 1))
        return True


def block(height, side):
    block_hash = sha(f"block-{height}")
    override = os.path.join(control, "public-hash-override")
    if side == "public" and os.path.exists(override):
        with open(override, encoding="utf-8") as f:
            at, value = f.read().split()
        if int(at) == height:
            block_hash = value
    if side == "local" and flag("local-mismatch"):
        block_hash = sha(f"other-{height}")
    return {"height": height, "hash": block_hash, "prev_hash": sha(f"block-{height - 1}"),
            "state_root": sha(f"root-{height}"), "producer_id": producer, "transactions": []}


class Handler(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=root, **kwargs)

    def log_message(self, fmt, *args):
        with open(logfile, "a", encoding="utf-8") as f:
            f.write(self.path + "\n")

    def send_json(self, code, body):
        data = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        url = urllib.parse.urlsplit(self.path)
        for prefix, side in (("/public/api/v1", "public"), ("/api/v1", "local")):
            if url.path.startswith(prefix + "/"):
                rest = url.path[len(prefix):]
                if side == "local" and flag("local-down"):
                    return self.send_json(503, {"error": "starting"})
                if side == "public" and rest == "/status" and take("public-status-503"):
                    return self.send_json(503, {"status": "temporarily_unavailable",
                                                "message": "QSDM services are being migrated. Please try again later."})
                if rest == "/status":
                    return self.send_json(200, {"chain_tip": TIP, "node_role": "validator"})
                if rest == "/chain/blocks":
                    query = urllib.parse.parse_qs(url.query)
                    first, last = int(query["from"][0]), int(query["to"][0])
                    blocks = [block(h, side) for h in range(first, min(last, TIP) + 1)]
                    return self.send_json(200, {"blocks": blocks, "from": first, "to": last, "tip": TIP})
                return self.send_json(404, {"error": "not found"})
        return super().do_GET()


server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
threading.Thread(target=server.serve_forever, daemon=True).start()
with open(portfile + ".tmp", "w", encoding="utf-8") as f:
    f.write(str(server.server_address[1]))
os.replace(portfile + ".tmp", portfile)
deadline = time.time() + 3600
while time.time() < deadline and not flag("stop"):
    time.sleep(0.2)
server.shutdown()
PY

"$PY" -I "$T/server.py" "$WWW" "$T/control" "$TIP" "$PRODUCER" "$T/port" "$SERVER_LOG" &
SERVER_PID=$!
for _ in $(seq 1 100); do
  [[ -s $T/port ]] && break
  sleep 0.1
done
[[ -s $T/port ]] || abort "the local server did not start"
PORT=$(tr -d '\r\n' <"$T/port")
BASE=http://127.0.0.1:$PORT

# ---------------------------------------------------------------------------------------------------
# Synthetic releases
# ---------------------------------------------------------------------------------------------------

PK=''
for i in $(seq 0 80); do
  PK+=$(sha "key-$i")
done
KEY_ID=$(pyrun -c 'import hashlib, sys; print(hashlib.sha256(bytes.fromhex(sys.argv[1])).hexdigest())' "$PK")
QSDM_SHA=$(sha_file "$T/fixtures/qsdm")

# fake_sig PUBLIC_KEY_HEX MESSAGE_FILE: the made-up signature the fake qsdmcli accepts.
fake_sig() {
  local seed want=''
  seed=$(sha "$1:$(sha_file "$2")")
  while [[ ${#want} -lt 9254 ]]; do
    want+=$seed
  done
  printf '%s\n' "${want:0:9254}"
}

# make_bundle DIR QSDM_FILE [bad-signature]: qsdm-follower-RELEASE.tar.gz in DIR.
make_bundle() {
  local out=$1 qsdm_file=$2 signature=${3:-good} build bundle name
  build=$(mktemp -d "$T/build/bundle.XXXXXX")
  bundle=$build/qsdm-follower-$RELEASE
  mkdir -p "$bundle"
  cp -- "$qsdm_file" "$bundle/qsdm"
  chmod 0644 "$bundle/qsdm"
  cp -- "$T/fixtures/verify-follower-release.py" "$bundle/verify-follower-release.py"
  printf '{"format": "qsdm-follower-release/v1", "release": "%s", "files": {"qsdm": {"sha256": "%s", "size": %s}, "verify-follower-release.py": {"sha256": "%s", "size": %s}}}\n' \
    "$RELEASE" "$(sha_file "$bundle/qsdm")" "$(size_of "$bundle/qsdm")" \
    "$(sha_file "$bundle/verify-follower-release.py")" "$(size_of "$bundle/verify-follower-release.py")" \
    >"$bundle/follower-manifest.json"
  if [[ $signature == bad-signature ]]; then
    fake_sig "$PK" "$bundle/qsdm" >"$bundle/follower-manifest.json.sig"
  else
    fake_sig "$PK" "$bundle/follower-manifest.json" >"$bundle/follower-manifest.json.sig"
  fi
  for name in follower-manifest.json qsdm verify-follower-release.py; do
    printf '%s  %s\n' "$(sha_file "$bundle/$name")" "$name"
  done >"$bundle/SHA256SUMS"
  tar -C "$build" -czf "$out/qsdm-follower-$RELEASE.tar.gz" "qsdm-follower-$RELEASE"
}

# make_config DIR [STATE_ROOT]: fullnode-core.toml in DIR, shaped like the release's.
make_config() {
  cat >"$1/fullnode-core.toml" <<EOF
# Full node (HTTP sync) core.toml: a test fixture shaped like the release's fullnode-core.toml.
# HTTP sync: core.env QSDM_CHAIN_SYNC_URLS=https://api.qsdm.tech/api/v1

[node]
role = "validator"
mining_enabled = false

[network]
port = 14001
bind_address = "127.0.0.1"
bootstrap_peers = []

[storage]
type = "sqlite"
sqlite_path = "/var/lib/qsdm-follower/qsdm.db"

[api]
port = $PORT
bind_address = "127.0.0.1"

[consensus]
authorized_block_producers = ["$PRODUCER"]
require_signed_votes = true

[snapshot_bootstrap]
enabled = true
checkpoint_height = $M
checkpoint_block_hash = "$CP_HASH"
checkpoint_state_root = "${2:-$CP_ROOT}"
checkpoint_producer_id = "$PRODUCER"
EOF
}

# make_snapshot DIR [CHECKPOINT_HASH]: the snapshot in two parts, and SNAPSHOT-SHA256SUMS, in DIR.
make_snapshot() {
  local out=$1 checkpoint_hash=${2:-$CP_HASH} build snap archive half name part
  build=$(mktemp -d "$T/build/snapshot.XXXXXX")
  snap=$build/qsdm-snapshot-$M
  archive=qsdm-snapshot-$M.tar.gz
  mkdir -p "$snap"
  printf '{"schema_version": 1, "files": []}\n' >"$snap/ledger-snapshot.json"
  printf '{"accounts": []}\n' >"$snap/qsdm_accounts.json"
  printf '{"records": []}\n' >"$snap/qsdm_enrollment.json"
  printf '{"v": 1, "bonds": [], "unbond": []}\n' >"$snap/qsdm_staking.json"
  seq 0 "$M" | sed 's/.*/{"height": &}/' >"$snap/qsdm_chain.ndjson"
  printf '{"height": %s, "block_hash": "%s", "state_root": "%s", "producer_id": "%s"}\n' \
    "$M" "$checkpoint_hash" "$CP_ROOT" "$PRODUCER" >"$snap/snapshot-checkpoint.json"
  printf 'QSDM verified snapshot (test fixture)\n' >"$snap/SNAPSHOT.txt"
  for name in SNAPSHOT.txt ledger-snapshot.json qsdm_accounts.json qsdm_chain.ndjson qsdm_enrollment.json \
    qsdm_staking.json snapshot-checkpoint.json; do
    printf '%s  %s\n' "$(sha_file "$snap/$name")" "$name"
  done >"$snap/SHA256SUMS"
  tar -C "$build" -czf "$build/$archive" "qsdm-snapshot-$M"
  half=$((($(size_of "$build/$archive") + 1) / 2))
  (cd "$build" && split -b "$half" -d -a 2 "$archive" "$archive.part-")
  {
    printf '%s *%s\n' "$(sha_file "$build/$archive")" "$archive"
    for part in 00 01; do
      cp -- "$build/$archive.part-$part" "$out/"
      printf '%s *%s\n' "$(sha_file "$build/$archive.part-$part")" "$archive.part-$part"
    done
  } >"$out/SNAPSHOT-SHA256SUMS"
}

# make_page FILE [CHECKPOINT_HASH] [EXTRA_ROW]: a fullnode.html shaped like qsdm.tech's.
make_page() {
  cat >"$1" <<EOF
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <title>Run a full node — QSDM (test fixture)</title>
</head>
<body data-page="fullnode">
  <main id="main">
    <section class="page-hero" aria-labelledby="fullnode-title">
      <div class="q-actions">
        <a class="site-btn site-btn-solid ext" href="https://github.com/blackbeardONE/QSDM/releases/tag/$TAG" rel="noopener">Download (GitHub release)<span class="visually-hidden"> (external link)</span></a>
        <a class="site-btn site-btn-ghost" href="/docs/#/run-a-full-node">Step-by-step guide</a>
      </div>
    </section>
    <section class="q-section" aria-labelledby="pins-title">
      <div class="q-wrap fn-body">
        <h2 id="pins-title">The values to pin</h2>
        <table class="pin-table">
          <tbody>
            <tr><td>Full-node release</td><td class="pin">$RELEASE</td></tr>
            <tr><td><code>qsdm</code> binary SHA-256</td><td class="pin">$QSDM_SHA</td></tr>
            <tr><td>Release signing key (release key v2) <code>key_id</code></td><td class="pin">$KEY_ID</td></tr>
            <tr><td>Snapshot checkpoint height</td><td class="pin">$M</td></tr>
            <tr><td>Checkpoint block hash</td><td class="pin">${2:-$CP_HASH}</td></tr>
            <tr><td>Checkpoint state root</td><td class="pin">$CP_ROOT</td></tr>
            <tr><td>Checkpoint producer</td><td class="pin">$PRODUCER</td></tr>${3:-}
          </tbody>
        </table>
        <p>
          Check the checkpoint with the public API:
          <code class="pin">curl -s "https://api.qsdm.tech/api/v1/chain/blocks?from=$M&amp;to=$M&amp;limit=1"</code>
        </p>
      </div>
    </section>
  </main>
</body>
</html>
EOF
}

GOOD=$WWW/download/good
mkdir -p "$GOOD"
make_bundle "$GOOD" "$T/fixtures/qsdm"
make_config "$GOOD"
printf '{"schema": "qsdm.release-trust-key.v1", "key_id": "%s", "algorithm": "ML-DSA-87", "public_key": "%s"}\n' \
  "$KEY_ID" "$PK" >"$GOOD/qsdm-release-key-v2.json"
make_snapshot "$GOOD"

variant() {
  cp -R -- "$GOOD" "$WWW/download/$1"
  printf '%s' "$WWW/download/$1"
}
d=$(variant tampered-binary)
make_bundle "$d" "$T/fixtures/qsdm-tampered"
d=$(variant bad-part)
printf 'X' >>"$d/qsdm-snapshot-$M.tar.gz.part-01"
d=$(variant bad-archive)
sed -i "1s/^[0-9a-f]\{64\}/$(sha not-the-archive)/" "$d/SNAPSHOT-SHA256SUMS"
d=$(variant wrong-config)
make_config "$d" "$(sha another-state-root)"
d=$(variant bad-signature)
make_bundle "$d" "$T/fixtures/qsdm" bad-signature
d=$(variant import-refused)
rm -f -- "$d"/qsdm-snapshot-*
make_snapshot "$d" "$(sha another-checkpoint)"

make_page "$WWW/site/fullnode.html"
make_page "$WWW/site/bad-shape.html" "${CP_HASH:0:63}"
make_page "$WWW/site/extra-row.html" "$CP_HASH" $'\n            <tr><td>Installer SHA-256</td><td class="pin">'"$CP_HASH"'</td></tr>'

# What the guide says, to compare the installer with.
awk '/^```ini$/ { inside = 1; next } inside && /^```$/ { exit } inside' "$GUIDE" >"$T/guide-unit"
[[ -s $T/guide-unit ]] || abort "cannot find the systemd unit in $GUIDE"
core_env_format=$(sed -n "s/^printf '\(CONFIG_FILE=[^']*\)'.*/\1/p" "$GUIDE" | head -n 1)
[[ -n $core_env_format ]] || abort "cannot find the core.env line in $GUIDE"
# shellcheck disable=SC2059 # the guide's printf format, on purpose.
printf "$core_env_format" >"$T/guide-core.env"
sed "s#https://api.qsdm.tech/api/v1#$BASE/public/api/v1#" "$T/guide-core.env" >"$T/test-core.env"

# ---------------------------------------------------------------------------------------------------
# Cases
# ---------------------------------------------------------------------------------------------------

PASS=0
FAIL=0
FAILED=()
CURRENT=''
CASE_START=0
OUT=''
RC=0
SERVER_MARK=0
ROOT=''
WORKD=''
ANCHORS=(--release "$RELEASE" --release-tag "$TAG" --qsdm-sha256 "$QSDM_SHA" --key-id "$KEY_ID"
  --checkpoint-height "$M" --checkpoint-hash "$CP_HASH" --checkpoint-state-root "$CP_ROOT"
  --checkpoint-producer "$PRODUCER")
LOCAL=()

begin() {
  CURRENT=$1
  CASE_START=$FAIL
  printf '\n== %s: %s\n' "$1" "$2"
}

# sandbox NAME: a fresh --root folder, work folder and stand-in state.
sandbox() {
  local dir=$T/sb/$1
  mkdir -p "$dir/root" "$dir/state"
  ROOT=$dir/root
  WORKD=$dir/work
  SHIM_STATE=$dir/state
}

# local_args VARIANT: the sandbox and local-server options most cases use.
local_args() {
  LOCAL=(--root "$ROOT" --work-dir "$WORKD" --download-base "$BASE/download/$1"
    --api "$BASE/public/api/v1" --skip-resource-check --wait-minutes 1)
}

# run NAME ARGS...: run the installer with the stand-ins first on PATH.
run() {
  OUT=$T/cases/$1.out
  shift
  : >"$SHIM_LOG"
  SERVER_MARK=$(wc -l <"$SERVER_LOG")
  RC=0
  PATH=$TEST_PATH TMPDIR=$T/tmp "$BASH" "$INSTALLER" "$@" >"$OUT" 2>&1 || RC=$?
}

expect() {
  local what=$1
  shift
  if "$@"; then
    PASS=$((PASS + 1))
    printf '  ok    %s\n' "$what"
  else
    FAIL=$((FAIL + 1))
    FAILED+=("$CURRENT: $what")
    printf '  FAIL  %s\n' "$what"
  fi
}

finish() {
  if ((FAIL > CASE_START)); then
    printf '  --- the installer printed (last 60 lines of %s):\n' "$OUT"
    tail -n 60 "$OUT" | sed 's/^/  | /'
  fi
}

rc_is() { [[ $RC == "$1" ]]; }
says() { grep -Fq -- "$1" "$OUT"; }
logged() { grep -Fxq -- "$1" "$SHIM_LOG"; }
logged_re() { grep -Eq -- "$1" "$SHIM_LOG"; }
not() { ! "$@"; }
exists() { [[ -e $1 ]]; }
missing() { [[ ! -e $1 && ! -L $1 ]]; }
same_file() { cmp -s -- "$1" "$2"; }
root_untouched() { [[ -z $(find "$ROOT" -mindepth 1 -print -quit) ]]; }
nothing_installed() { ! grep -Eq '^(useradd|install|runuser|systemctl) ' "$SHIM_LOG"; }
offline() { ! grep -q '^curl BLOCKED' "$SHIM_LOG"; }
served_count() { tail -n +"$((SERVER_MARK + 1))" "$SERVER_LOG" | grep -Fc -- "$1" || true; }
served() { [[ $(served_count "$1") -gt 0 ]]; }
served_times() { [[ $(served_count "$1") == "$2" ]]; }
nothing_served() { [[ $(wc -l <"$SERVER_LOG") == "$SERVER_MARK" ]]; }
imports_run() { [[ $(grep -c '^argv: --snapshot-import' "$ROOT/fake-qsdm.log" 2>/dev/null || true) == "$1" ]]; }
fake_logged() { grep -Fxq -- "$1" "$ROOT/fake-qsdm.log"; }
in_guide() { grep -Fq -- "$1" "$GUIDE"; }
plan_shows() {
  local line
  while IFS= read -r line; do
    grep -Fxq -- "        $line" "$OUT" || return 1
  done <"$1"
}

# refused MESSAGE: the checks every refusal before installing shares.
refused() {
  expect "exits 1" rc_is 1
  expect "says: STOP: $1" says "STOP: $1"
  expect "says that this run installed nothing" says "This run installed nothing."
  expect "nothing under --root" root_untouched
  expect "no install step ran (useradd, install, runuser, systemctl)" nothing_installed
  expect "contacted nothing but 127.0.0.1" offline
}

SNAP_PARTS=qsdm-snapshot-$M.tar.gz.part-
STATE_IN_ROOT=/var/lib/qsdm-follower

begin dry-run "--dry-run with the default addresses prints the plan and changes nothing"
sandbox dry-run
run dry-run --dry-run --root "$ROOT" --work-dir "$WORKD" "${ANCHORS[@]}"
expect "exits 0" rc_is 0
expect "says it changes nothing" says "Dry run: this is the plan. Nothing is downloaded, installed or changed."
expect "plans the download from the GitHub release" \
  says "https://github.com/blackbeardONE/QSDM/releases/download/$TAG/qsdm-follower-$RELEASE.tar.gz"
expect "plans the guide's core.env" plan_shows "$T/guide-core.env"
expect "plans the guide's systemd unit" plan_shows "$T/guide-unit"
expect "plans the guide's import command" says "runuser -u qsdm-follower -- sh -c 'cd /var/lib/qsdm-follower && exec env -i PATH=/usr/bin:/bin CONFIG_FILE=/etc/qsdm-follower/core.toml LOG_FILE=/var/lib/qsdm-follower/import.log /opt/qsdm-follower/qsdm --snapshot-import --from /srv/qsdm-snapshot/qsdm-snapshot-$M'"
expect "made no work folder" missing "$WORKD"
expect "nothing under --root" root_untouched
expect "ran no sudo and no system command" not grep -q . "$SHIM_LOG"
expect "downloaded nothing" nothing_served
finish

begin install "a fresh install with the values as flags, and the signature check"
sandbox main
local_args good
run install "${LOCAL[@]}" "${ANCHORS[@]}" --qsdmcli "$T/tools/qsdmcli"
expect "exits 0" rc_is 0
expect "checks every line of the release's SHA256SUMS" says "ok: every line of SHA256SUMS is OK"
expect "checks the qsdm binary against qsdm.tech" says "ok: the qsdm binary matches the SHA-256 published on qsdm.tech"
expect "checks the signature with qsdmcli" says "ok: the signature verifies with key_id $KEY_ID"
expect "runs the guide's verify-follower-release.py" says "ok: verify-follower-release: VERIFIED $RELEASE qsdm $QSDM_SHA"
expect "checks the checkpoint three ways" says "ok: all three name the same height, block hash, state root and producer"
expect "checks each snapshot part" says "ok: ${SNAP_PARTS}01 matches SNAPSHOT-SHA256SUMS"
expect "checks the joined snapshot" says "ok: qsdm-snapshot-$M.tar.gz matches SNAPSHOT-SHA256SUMS"
expect "checks the unpacked snapshot" says "ok: the unpacked snapshot matches its SHA256SUMS"
expect "useradd as in the guide" logged "useradd --system --home-dir /var/lib/qsdm-follower --shell /usr/sbin/nologin qsdm-follower"
expect "  (the guide's useradd)" in_guide "sudo useradd --system --home-dir /var/lib/qsdm-follower --shell /usr/sbin/nologin qsdm-follower"
expect "the state folder as in the guide" logged "install -d -o qsdm-follower -g qsdm-follower -m 0700 $ROOT/var/lib/qsdm-follower"
expect "  (the guide's state folder)" in_guide "sudo install -d -o qsdm-follower -g qsdm-follower -m 0700 /var/lib/qsdm-follower"
expect "the program and configuration folders as in the guide" logged "install -d -m 0755 $ROOT/opt/qsdm-follower $ROOT/etc/qsdm-follower"
expect "  (the guide's folders)" in_guide "sudo install -d -m 0755 /opt/qsdm-follower /etc/qsdm-follower"
expect "the binary with mode 0755" logged_re "^install -m 0755 .*/qsdm-follower-$RELEASE/qsdm $ROOT/opt/qsdm-follower/qsdm\$"
expect "  (the guide's binary install)" in_guide "sudo install -m 0755 ~/qsdm-release/qsdm-follower-<release>/qsdm /opt/qsdm-follower/qsdm"
expect "core.toml with mode 0640, root:qsdm-follower" logged_re "^install -m 0640 -o root -g qsdm-follower .*/fullnode-core.toml $ROOT/etc/qsdm-follower/core.toml\$"
expect "  (the guide's core.toml install)" in_guide "sudo install -m 0640 -o root -g qsdm-follower fullnode-core.toml /etc/qsdm-follower/core.toml"
expect "core.env with mode 0640, root:qsdm-follower" logged_re "^install -m 0640 -o root -g qsdm-follower .*/core.env $ROOT/etc/qsdm-follower/core.env\$"
expect "  (the guide's core.env install)" in_guide "sudo install -m 0640 -o root -g qsdm-follower /dev/stdin /etc/qsdm-follower/core.env"
expect "the installed qsdm is the release's" same_file "$ROOT/opt/qsdm-follower/qsdm" "$T/fixtures/qsdm"
expect "the installed core.toml is the release's" same_file "$ROOT/etc/qsdm-follower/core.toml" "$GOOD/fullnode-core.toml"
expect "core.env is the guide's, with this API" same_file "$ROOT/etc/qsdm-follower/core.env" "$T/test-core.env"
expect "the systemd unit is the guide's, byte for byte" same_file "$ROOT/etc/systemd/system/qsdm-follower.service" "$T/guide-unit"
expect "the import ran once" imports_run 1
expect "  with the guide's arguments" fake_logged "argv: --snapshot-import --from $ROOT/srv/qsdm-snapshot/qsdm-snapshot-$M"
expect "  in the state folder" fake_logged "cwd: $ROOT$STATE_IN_ROOT"
expect "  with PATH=/usr/bin:/bin" fake_logged "env: PATH=/usr/bin:/bin"
expect "  with the guide's CONFIG_FILE" fake_logged "env: CONFIG_FILE=$ROOT/etc/qsdm-follower/core.toml"
expect "  with the guide's LOG_FILE" fake_logged "env: LOG_FILE=$ROOT$STATE_IN_ROOT/import.log"
expect "  without producer or mining settings" \
  not grep -Eq '^env: (QSDM_NETWORK_BLOCK_PRODUCER|QSDM_SOLO_VALIDATOR_MODE|QSDM_LEGACY_MINING_MODE)=' "$ROOT/fake-qsdm.log"
expect "  as the service user" logged_re '^runuser -u qsdm-follower -- sh -c '
expect "the import installed the chain files" exists "$ROOT$STATE_IN_ROOT/qsdm_chain.ndjson"
expect "the import record is in place" exists "$ROOT$STATE_IN_ROOT/qsdm_snapshot_import.json"
expect "deleted the unpacked snapshot, as the guide says" missing "$ROOT/srv/qsdm-snapshot"
expect "deleted the downloaded archive and parts" not compgen -G "$WORKD/downloads/qsdm-snapshot-*"
expect "systemctl daemon-reload" logged "systemctl daemon-reload"
expect "systemctl enable --now qsdm-follower" logged "systemctl enable --now qsdm-follower"
expect "  (the guide's start command)" in_guide "sudo systemctl daemon-reload && sudo systemctl enable --now qsdm-follower"
expect "compared blocks T-100 and T-1 with the network" says "ok: your node is at block $TIP, the network at $TIP, and the blocks compared are identical"
expect "contacted nothing but 127.0.0.1" offline
finish

begin rerun "the same install again, with --from-website: it carries on and changes nothing"
local_args good
run rerun "${LOCAL[@]}" --from-website --website-url "$BASE/site/fullnode.html" --qsdmcli "$T/tools/qsdmcli"
expect "exits 0" rc_is 0
expect "prints where the values were read" says "Values to pin, read from $BASE/site/fullnode.html:"
expect "prints the values read" says "$QSDM_SHA"
expect "skips the snapshot" says "ok: skipped: this node already imported the snapshot at height $M"
expect "does not download the snapshot again" not served "$SNAP_PARTS"
expect "does not import again" imports_run 1
expect "makes no user" not logged_re '^useradd '
expect "installs no file again" not logged_re '^install -m '
expect "finds the binary in place" says "ok: /opt/qsdm-follower/qsdm is already in place"
expect "finds the unit in place" says "ok: /etc/systemd/system/qsdm-follower.service is already in place"
expect "finds the node running" says "ok: the node is already running"
expect "checks the network again" says "ok: your node is at block $TIP"
expect "contacted nothing but 127.0.0.1" offline
finish

begin network-restart "the public API answers 503 for a while (a producer restart): the check waits, then passes"
echo 2 >"$T/control/public-status-503"
export QSDM_FULLNODE_RETRY_EVERY=1 QSDM_FULLNODE_RETRY_FOR=60
run network-restart "${LOCAL[@]}" "${ANCHORS[@]}"
unset QSDM_FULLNODE_RETRY_EVERY QSDM_FULLNODE_RETRY_FOR
rm -f "$T/control/public-status-503"
expect "exits 0" rc_is 0
expect "says it waits for the network's API" says "the network's API did not answer when asked for its tip; a producer restart pauses it for a few minutes."
expect "says how long it tries again" says "trying again every 1 seconds for up to 1 minutes"
expect "says when it answers again" says "the network's API answers again"
expect "asked for the network's status three times" served_times "/public/api/v1/status" 3
expect "checks the network" says "ok: your node is at block $TIP"
expect "leaves the node running" not logged "systemctl stop qsdm-follower"
finish

begin network-down "the public API stays down past the retry time: it stops, and the node keeps running"
echo 1000 >"$T/control/public-status-503"
export QSDM_FULLNODE_RETRY_EVERY=1 QSDM_FULLNODE_RETRY_FOR=3
run network-down "${LOCAL[@]}" "${ANCHORS[@]}"
unset QSDM_FULLNODE_RETRY_EVERY QSDM_FULLNODE_RETRY_FOR
rm -f "$T/control/public-status-503"
expect "exits 1" rc_is 1
expect "says why" says "STOP: Could not read the network's tip from $BASE/public/api/v1."
expect "says how long it tried" says "trying again every 1 seconds for up to 3 seconds"
expect "asked more than once" not served_times "/public/api/v1/status" 1
expect "says a new run carries on safely" says "is fixed, run the same command again: it carries on safely."
expect "leaves the node running" not logged "systemctl stop qsdm-follower"
finish

begin bad-retry "a retry interval of 0 seconds is refused before anything runs"
export QSDM_FULLNODE_RETRY_EVERY=0
run bad-retry "${LOCAL[@]}" "${ANCHORS[@]}"
unset QSDM_FULLNODE_RETRY_EVERY
expect "exits 2" rc_is 2
expect "says why" says "QSDM_FULLNODE_RETRY_FOR and QSDM_FULLNODE_RETRY_EVERY must be whole numbers of seconds"
expect "contacted nothing" nothing_served
finish

begin network-mismatch "the node's blocks differ from the network's: it stops the node"
touch "$T/control/local-mismatch"
run network-mismatch "${LOCAL[@]}" "${ANCHORS[@]}"
rm -f "$T/control/local-mismatch"
expect "exits 1" rc_is 1
expect "says the blocks do not match" says "STOP: Your node's blocks do not match the network's (see above)."
expect "stops the node, as the guide says" logged "systemctl stop qsdm-follower"
expect "keeps the chain data" exists "$ROOT$STATE_IN_ROOT/qsdm_chain.ndjson"
finish

begin not-ready "the node does not answer yet: exit 3 and run it again later"
touch "$T/control/local-down"
run not-ready "${LOCAL[@]}" "${ANCHORS[@]}" --wait-minutes 0
rm -f "$T/control/local-down"
expect "exits 3" rc_is 3
expect "says to run it again later" says "Then run the same command again to finish the check against the network."
expect "started the node again" logged "systemctl enable --now qsdm-follower"
finish

begin wrong-binary "the release's qsdm is not the binary qsdm.tech names"
sandbox wrong-binary
local_args tampered-binary
run wrong-binary "${LOCAL[@]}" "${ANCHORS[@]}"
refused "The qsdm binary in the release does not match the SHA-256 published on qsdm.tech."
expect "even though its SHA256SUMS agreed" says "ok: every line of SHA256SUMS is OK"
expect "downloaded no snapshot" not served "$SNAP_PARTS"
finish

begin bad-part "a snapshot part does not match SNAPSHOT-SHA256SUMS"
sandbox resume
local_args bad-part
run bad-part "${LOCAL[@]}" "${ANCHORS[@]}"
refused "${SNAP_PARTS}01 does not match its line in SNAPSHOT-SHA256SUMS (downloaded twice)."
expect "downloaded the bad part twice" served_times "${SNAP_PARTS}01" 2
expect "kept the good part for the next run" exists "$WORKD/downloads/${SNAP_PARTS}00"
expect "deleted the bad part" missing "$WORKD/downloads/${SNAP_PARTS}01"
expect "deleted the bad part's partial download" missing "$WORKD/downloads/${SNAP_PARTS}01.partial"
finish

begin resume "the same command once the release is fixed: it carries on"
local_args good
run resume "${LOCAL[@]}" "${ANCHORS[@]}"
expect "exits 0" rc_is 0
expect "keeps the part it already had" says "ok: ${SNAP_PARTS}00 was downloaded by an earlier run"
expect "does not download that part again" not served "${SNAP_PARTS}00"
expect "downloads the missing part" served_times "${SNAP_PARTS}01" 1
expect "imports" imports_run 1
finish

begin bad-archive "the joined snapshot does not match SNAPSHOT-SHA256SUMS"
sandbox bad-archive
local_args bad-archive
run bad-archive "${LOCAL[@]}" "${ANCHORS[@]}"
refused "qsdm-snapshot-$M.tar.gz does not match its line in SNAPSHOT-SHA256SUMS."
expect "does not keep the joined archive" missing "$WORKD/downloads/qsdm-snapshot-$M.tar.gz"
finish

begin api-differs "the public API names another block hash at the checkpoint"
sandbox api-differs
local_args good
printf '%s %s\n' "$M" "$(sha another-block)" >"$T/control/public-hash-override"
run api-differs "${LOCAL[@]}" "${ANCHORS[@]}"
rm -f "$T/control/public-hash-override"
refused "The checkpoint block hash does not match."
expect "shows each source's value" says "  public API:              $(sha another-block)"
expect "checked it before downloading the snapshot" not served "$SNAP_PARTS"
finish

begin config-differs "the release's fullnode-core.toml pins another state root"
sandbox config-differs
local_args wrong-config
run config-differs "${LOCAL[@]}" "${ANCHORS[@]}"
refused "The checkpoint state root does not match."
expect "shows the configuration's value" says "  fullnode-core.toml:      $(sha another-state-root)"
finish

begin bad-signature "the release signature does not verify"
sandbox bad-signature
local_args bad-signature
run bad-signature "${LOCAL[@]}" "${ANCHORS[@]}" --qsdmcli "$T/tools/qsdmcli"
refused "The release signature does not verify with the key published on qsdm.tech."
finish

begin require-signature "--require-signature without qsdmcli"
sandbox require-signature
local_args good
run require-signature "${LOCAL[@]}" "${ANCHORS[@]}" --require-signature
refused "--require-signature was given, but qsdmcli was not found."
expect "downloaded nothing" nothing_served
finish

begin page-shape "--from-website refuses a page whose values have the wrong shape"
sandbox page
local_args good
run page-shape "${LOCAL[@]}" --from-website --website-url "$BASE/site/bad-shape.html"
refused "The page $BASE/site/bad-shape.html does not have the expected shape, so none of its values were used"
expect "says which value" says "Checkpoint block hash is not 64 lowercase hexadecimal characters"
expect "read only the page" served_times /site/bad-shape.html 1
expect "downloaded nothing else" not served /download/
finish

begin page-row "--from-website refuses a page with a row it does not know"
run page-row "${LOCAL[@]}" --from-website --website-url "$BASE/site/extra-row.html"
refused "The page $BASE/site/extra-row.html does not have the expected shape"
expect "says which row" says "the pin table has a row this installer does not know: 'Installer SHA-256'"
finish

begin page-flag "a value given as a flag that differs from the page"
run page-flag "${LOCAL[@]}" --from-website --website-url "$BASE/site/fullnode.html" --checkpoint-height 999
refused "--checkpoint-height 999 is not the value on $BASE/site/fullnode.html ($M)."
finish

begin import-refused "the import refuses the snapshot (exit 78)"
sandbox import
local_args import-refused
run import-refused "${LOCAL[@]}" "${ANCHORS[@]}"
expect "exits 1" rc_is 1
expect "says the import refused" says "STOP: The import refused the snapshot (exit 78) and installed nothing."
expect "shows the import's reason" says "FATAL (exit 78): snapshot bootstrap: the snapshot's snapshot-checkpoint.json names another checkpoint"
expect "says it had started to install" says "This run had started to install when it stopped."
expect "no chain file was installed" missing "$ROOT$STATE_IN_ROOT/qsdm_chain.ndjson"
expect "the node was not started" not logged_re '^systemctl '
finish

begin import-fixed "the same command with a good release: it carries on"
local_args good
run import-fixed "${LOCAL[@]}" "${ANCHORS[@]}"
expect "exits 0" rc_is 0
expect "replaces the snapshot it unpacked before" \
  says "the snapshot unpacked by an earlier run is not this archive, or is damaged: unpacking it again"
expect "finds the files it installed before" says "ok: /etc/qsdm-follower/core.toml is already in place"
expect "imports" imports_run 2
expect "starts the node" logged "systemctl enable --now qsdm-follower"
finish

begin bad-record "the import exits 0, but its record names another authority"
sandbox record
local_args good
printf 'authorized_block_producers\n' >"$ROOT/fake-authorized-by"
run bad-record "${LOCAL[@]}" "${ANCHORS[@]}"
expect "exits 1" rc_is 1
expect "says the record is not what the guide expects" \
  says "STOP: The import ended with exit 0, but its record does not say what the guide expects:"
expect "says what differs" says 'authorized_by is "authorized_block_producers", not "producer_transition"'
expect "the node was not started" not logged_re '^systemctl (daemon-reload|enable)'
finish

begin step-fails "a system command fails while installing: it stops and says which"
sandbox step-fails
local_args good
touch "$SHIM_STATE/useradd-fails"
run step-fails "${LOCAL[@]}" "${ANCHORS[@]}"
rm -f "$SHIM_STATE/useradd-fails"
expect "exits 1" rc_is 1
expect "names the command that failed" \
  says "STOP: This command failed (exit 1): useradd --system --home-dir /var/lib/qsdm-follower --shell /usr/sbin/nologin qsdm-follower"
expect "says it had started to install" says "This run had started to install when it stopped."
expect "the node was not started" not logged_re '^systemctl '
finish

begin step-fixed "the same command once the cause is fixed: it carries on"
run step-fixed "${LOCAL[@]}" "${ANCHORS[@]}"
expect "exits 0" rc_is 0
expect "reuses the snapshot it downloaded" says "ok: qsdm-snapshot-$M.tar.gz was downloaded by an earlier run"
expect "reuses the snapshot it unpacked" \
  says "ok: /srv/qsdm-snapshot/qsdm-snapshot-$M was unpacked by an earlier run and matches its SHA256SUMS"
expect "downloads no snapshot part again" not served "$SNAP_PARTS"
expect "imports" imports_run 1
expect "starts the node" logged "systemctl enable --now qsdm-follower"
finish

# MemTotal for the resource check (the installer reads QSDM_FULLNODE_MEMINFO instead of /proc/meminfo).
meminfo() {
  printf 'MemTotal:       %s kB\nMemFree:        1000 kB\n' "$1" >"$T/fixtures/meminfo-$1"
  export QSDM_FULLNODE_MEMINFO=$T/fixtures/meminfo-$1
}

begin small-disk "too little free disk space: it stops before downloading"
sandbox disk
touch "$SHIM_STATE/tiny-disk"
meminfo 33554432
run small-disk --root "$ROOT" --work-dir "$WORKD" --download-base "$BASE/download/good" \
  --api "$BASE/public/api/v1" "${ANCHORS[@]}"
refused "There are about 1 GB free where the installer works"
expect "says how to go on anyway" says "--skip-resource-check"
expect "downloaded nothing" nothing_served
unset QSDM_FULLNODE_MEMINFO
finish

begin small-memory "a release after fullnode-rc1019 on a server below 5.5 GiB: it stops before downloading"
sandbox mem-small
meminfo 5000000
run small-memory --root "$ROOT" --work-dir "$WORKD" --download-base "$BASE/download/good" \
  --api "$BASE/public/api/v1" "${ANCHORS[@]}"
refused "This server has about 4 GB of memory. A full node needs more. Importing the snapshot and starting the node each need about 2 GB at 880 376 blocks"
expect "says how to go on anyway" says "--skip-resource-check"
expect "downloaded nothing" nothing_served
unset QSDM_FULLNODE_MEMINFO
finish

begin six-gb "a release after fullnode-rc1019 on a 6 GB server (about 5.8 GiB): the memory check passes"
sandbox mem-six
touch "$SHIM_STATE/tiny-disk"
meminfo 6094848
run six-gb --root "$ROOT" --work-dir "$WORKD" --download-base "$BASE/download/good" \
  --api "$BASE/public/api/v1" "${ANCHORS[@]}"
expect "passes the memory check" not says "GB of memory"
refused "There are about 1 GB free where the installer works"
unset QSDM_FULLNODE_MEMINFO
finish

begin rc1019-memory "fullnode-rc1019 on a server below 11 GiB: it stops before downloading"
sandbox mem-rc1019
meminfo 8388608
RC1019_ANCHORS=("${ANCHORS[@]}")
RC1019_ANCHORS[1]=hardened-legacy-20261019-d7ffcd4-hl2-edge-go1269-ids-pqe-rc1019
run rc1019-memory --root "$ROOT" --work-dir "$WORKD" --download-base "$BASE/download/good" \
  --api "$BASE/public/api/v1" "${RC1019_ANCHORS[@]}"
refused "This server has about 8 GB of memory. A full node needs more. This release loads the whole chain into memory"
expect "downloaded nothing" nothing_served
unset QSDM_FULLNODE_MEMINFO
finish

begin foreign-work-dir "a work folder that holds someone else's files"
sandbox foreign
mkdir -p "$WORKD"
printf 'mine\n' >"$WORKD/notes.txt"
local_args good
run foreign-work-dir "${LOCAL[@]}" "${ANCHORS[@]}"
refused "$WORKD is not empty, and this installer did not make it."
expect "left the file alone" exists "$WORKD/notes.txt"
finish

begin bad-options "bad options exit 2 before doing anything"
sandbox options
run options-missing --root "$ROOT" --work-dir "$WORKD" --release "$RELEASE"
expect "missing values: exits 2" rc_is 2
expect "missing values: names them" says "missing values to pin: --release-tag --qsdm-sha256 --key-id"
run options-hex --root "$ROOT" --work-dir "$WORKD" "${ANCHORS[@]}" --qsdm-sha256 not-a-hash
expect "a malformed SHA-256: exits 2" rc_is 2
expect "a malformed SHA-256: says so" says "--qsdm-sha256 must be 64 hexadecimal characters: not-a-hash"
run options-http --root "$ROOT" --work-dir "$WORKD" "${ANCHORS[@]}" --api http://api.example.test/api/v1
expect "plain http: exits 2" rc_is 2
expect "plain http: says https is needed" says "--api must start with https://"
expect "plain http: contacted nothing" offline
expect "nothing under --root" root_untouched
finish

printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
if ((FAIL > 0)); then
  printf 'Failed:\n'
  printf '  %s\n' "${FAILED[@]}"
  exit 1
fi
