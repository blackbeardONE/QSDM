#!/usr/bin/env bash
# install-full-node.sh: one command from a fresh Linux server to a running, verified QSDM full node.
#
# It carries out QSDM/docs/docs/RUN_A_FULL_NODE.md step by step and keeps every check the guide makes.
# The values it trusts come from https://qsdm.tech/fullnode.html, never from a download: the SHA-256 of
# the qsdm binary, the release key_id and the snapshot checkpoint (height, block hash, state root and
# producer). Give them as flags, or let --from-website read them from that page.
#
# It stops at the first check that fails, before it installs anything, and says what did not match.
# Running it again carries on where it stopped. It never deletes chain data or files it did not make.
#
# When the block maker changes (PRODUCER_TRANSITION.md), the page also shows the new block-maker schedule:
# the exact [consensus.producer_transition] table every node must carry. A new install takes it from the
# page; a node that is already running gets it with --update-schedule (RUN_A_FULL_NODE.md, "When the block
# maker changes"), which keeps the old file, restarts the node and checks it against the network.
#
# Run it as root or as a user who can use sudo. "install-full-node.sh --help" lists the options.
# Exit status: 0 done; 1 stopped (a check failed or a step went wrong); 2 bad options; 3 installed and
# started, but the node did not answer in time for the last check (run the same command again later).

set -euo pipefail
umask 022
export LC_ALL=C
unset TAR_OPTIONS GZIP CDPATH POSIXLY_CORRECT
case ":$PATH:" in
  *:/usr/sbin:*) ;;
  *) PATH=$PATH:/usr/sbin:/sbin ;;
esac

PROG=${0##*/}

# The guide's paths, service user and files.
SERVICE=qsdm-follower
STATE_DIR=/var/lib/qsdm-follower
OPT_DIR=/opt/qsdm-follower
ETC_DIR=/etc/qsdm-follower
SNAP_PARENT=/srv/qsdm-snapshot
UNIT_FILE=/etc/systemd/system/qsdm-follower.service
IMPORT_RECORD=qsdm_snapshot_import.json
# The files `qsdm --snapshot-import` refuses to find in the state directory (it fills an empty one).
CHAIN_FILES=(qsdm_chain.ndjson qsdm_accounts.json qsdm_enrollment.json qsdm_staking.json
  qsdm_receipts.ndjson qsdm_receipts.json qsdm_governance.json)

DEFAULT_API=https://api.qsdm.tech/api/v1
DEFAULT_WEBSITE=https://qsdm.tech/fullnode.html
GITHUB_DOWNLOADS=https://github.com/blackbeardONE/QSDM/releases/download
# The memory a full-node release needs (RUN_A_FULL_NODE.md, "Memory"). fullnode-rc1019 loads the whole chain
# with its signatures: a start needs about 10.4 GiB at 1 020 000 blocks and about 1.6 GiB more for every
# 100 000 blocks after that. The releases after it keep verified signatures in the journal: the import and a
# start each peak at about 2 GiB at 880 376 blocks, and a running node holds about 0.5 GiB more for every
# 100 000 blocks it appends until its next restart. 5.5 GiB lets a "6 GB" server (about 5.8 GiB) install them.
RC1019_RELEASE=hardened-legacy-20261019-d7ffcd4-hl2-edge-go1269-ids-pqe-rc1019
RC1019_NEED_MEM_KB=$((11 * 1024 * 1024))
NEED_MEM_KB=$((11 * 1024 * 1024 / 2))
# Where MemTotal is read. The installer's test points QSDM_FULLNODE_MEMINFO at a made-up file.
MEMINFO=${QSDM_FULLNODE_MEMINFO:-/proc/meminfo}
# A producer restart takes the public API away for a few minutes (it answers 503 meanwhile), and a node's own
# API does not answer while it loads the chain. During the check against the network (step 8) a read that fails
# is tried again every RETRY_EVERY seconds for up to RETRY_FOR seconds before the installer stops. The
# installer's test shortens both.
RETRY_FOR=${QSDM_FULLNODE_RETRY_FOR:-900}
RETRY_EVERY=${QSDM_FULLNODE_RETRY_EVERY:-30}
NEED_DISK_KB=$((15 * 1000 * 1000))

# Options.
RELEASE='' RELEASE_TAG='' QSDM_SHA='' KEY_ID='' CP_HEIGHT='' CP_HASH='' CP_ROOT='' CP_PRODUCER=''
FROM_WEBSITE=0 WEBSITE_URL=$DEFAULT_WEBSITE API_BASE=$DEFAULT_API DOWNLOAD_BASE='' QSDMCLI=''
REQUIRE_SIGNATURE=0 WORK_DIR='' ROOT='' DRY_RUN=0 WAIT_MINUTES=30 SKIP_RESOURCE_CHECK=0
SCHEDULE_FLAG='' UPDATE_SCHEDULE=0

# State.
IS_ROOT=0 INSTALL_STARTED=0 IMPORT_DONE=0 LOCKED=0 RUN_TMP='' UNPACK_TMP='' HELPER=''
# The block-maker schedule: the file in use (from the page or --producer-schedule), where it came from,
# the core.toml to install (the release's, or the release's with that schedule) and what the step totals are.
SCHEDULE='' SCHEDULE_FROM='' PAGE_SCHEDULE='' TARGET_CONFIG='' TARGET_WHAT='' FIRST_NEW=0
STEP_TOTAL=8 CHECK_STEP=8 CHECK_LOCAL_TIP=0 CHECK_PUBLIC_TIP=0
BUNDLE='' BUNDLE_TGZ='' SNAP_NAME='' SNAP_ARCHIVE='' ARCHIVE_SHA='' LOCAL_API='' DL='' STATE=''
P_STATE='' P_OPT='' P_ETC='' P_SNAP_PARENT='' P_SNAP_DIR='' P_UNIT='' UNPACK_PROBLEM=''
R_AUTHORIZED='' R_STATE_DIR='' R_HEIGHT='' R_HASH='' R_ROOT='' R_PRODUCER='' R_SIGNED='' R_INSTALLED=''
PROTO=()
CURL_BIG=(--fail --location --retry 5 --retry-delay 10 --connect-timeout 30 --speed-limit 10240 --speed-time 120)

usage() {
  cat <<EOF
Usage: ${PROG} --from-website [options]
   or: ${PROG} --release NAME --release-tag TAG --qsdm-sha256 HEX --key-id HEX
           --checkpoint-height N --checkpoint-hash HEX --checkpoint-state-root HEX
           --checkpoint-producer HEX [options]
   or: ${PROG} --update-schedule --from-website [options]
   or: ${PROG} --update-schedule --producer-schedule FILE [options]

Sets up a QSDM full node on this Linux x86_64 server by following RUN_A_FULL_NODE.md: download and
verify the release, check the snapshot checkpoint, install, import the snapshot, start the
qsdm-follower service, and compare its blocks with the network.

Values to pin. Take them from https://qsdm.tech/fullnode.html, never from the downloaded files:
  --release NAME               the full-node release
  --release-tag TAG            the GitHub release tag (the page's Download link ends with it)
  --qsdm-sha256 HEX            the SHA-256 of the qsdm binary
  --key-id HEX                 the release key_id (release key v2)
  --checkpoint-height N        the snapshot checkpoint height
  --checkpoint-hash HEX        the checkpoint block hash
  --checkpoint-state-root HEX  the checkpoint state root
  --checkpoint-producer HEX    the checkpoint producer
  --from-website               read all of the above from https://qsdm.tech/fullnode.html and print
                               them. Values also given as flags must match the page. When the page
                               shows a block-maker schedule, it is read too.
  --producer-schedule FILE     the block-maker schedule shown on https://qsdm.tech/fullnode.html (the
                               [consensus.producer_transition] text, saved to a file). Only needed
                               after the block maker has changed. With --from-website it must be the
                               page's.

When the block maker changes (RUN_A_FULL_NODE.md, "When the block maker changes"):
  --update-schedule            for a node this installer set up: put the new block-maker schedule
                               (from --from-website or --producer-schedule) into its core.toml,
                               keep the old file next to it, restart the node and check it against
                               the network. The new schedule must continue the node's: the same
                               earlier block makers from the same blocks. Nothing else changes.

Options:
  --qsdmcli PATH               qsdmcli for the release signature check (default: qsdmcli on PATH).
                               Without qsdmcli the signature is not checked; the qsdm binary is
                               still checked against the SHA-256 from qsdm.tech.
  --require-signature          stop unless the release signature can be checked
  --work-dir DIR               where downloads and logs go (default: ~/qsdm-fullnode-install)
  --wait-minutes N             how long to wait for the node to catch up (default: 30)
  --skip-resource-check        do not stop when memory or free disk space look too small
  --dry-run                    print the plan and change nothing
  -h, --help                   show this help

For mirrors and tests:
  --download-base URL          where the release files are (default:
                               https://github.com/blackbeardONE/QSDM/releases/download/TAG)
  --api URL                    the public API to check against and to sync from
                               (default: https://api.qsdm.tech/api/v1)
  --website-url URL            the page that --from-website reads
                               (default: https://qsdm.tech/fullnode.html)
  --root DIR                   tests only: put every file under DIR. useradd, runuser and systemctl
                               still run, so use it only with the test's stand-ins.

Addresses must start with https:// (plain http:// only for 127.0.0.1, for tests).
Running the same command again carries on where it stopped, and repeats the check against the
network. Exit status: 0 done, 1 stopped, 2 bad options, 3 started but not yet checked.
EOF
}

# ---------------------------------------------------------------------------------------------------
# Output
# ---------------------------------------------------------------------------------------------------

say() { printf '%s\n' "$*"; }
ok() { printf '  ok: %s\n' "$*"; }
note() { printf '  note: %s\n' "$*"; }
step() { printf '\n[%s/%s] %s\n' "$1" "$STEP_TOTAL" "$2"; }

# stop WHAT [WHAT TO DO ...]: explain, then exit 1.
stop() {
  local line
  printf '\nSTOP: %s\n' "$1" >&2
  shift
  for line in "$@"; do
    printf '%s\n' "$line" >&2
  done
  if [[ $INSTALL_STARTED == 0 ]]; then
    printf 'This run installed nothing.\n' >&2
  else
    printf 'This run had started to install when it stopped. It deleted no chain data. Once the cause\n' >&2
    printf 'is fixed, run the same command again: it carries on safely.\n' >&2
  fi
  exit 1
}

usage_error() {
  printf '%s: %s\n' "$PROG" "$1" >&2
  printf 'Run %s --help to see the options.\n' "$PROG" >&2
  exit 2
}

# run_root CMD...: show a command, then run it as root. If it fails, stop and say which it was.
run_root() {
  local shown rc=0
  shown=$(printf ' %q' "$@")
  if [[ $IS_ROOT == 1 ]]; then
    printf '  +%s\n' "$shown"
  else
    printf '  + sudo%s\n' "$shown"
  fi
  as_root "$@" || rc=$?
  if [[ $rc != 0 ]]; then
    stop "This command failed (exit $rc):$shown" "Fix the cause, then run the same command again."
  fi
}

as_root() {
  if [[ $IS_ROOT == 1 ]]; then
    "$@"
  else
    sudo "$@"
  fi
}

on_exit() {
  local rc=$?
  if [[ -n $UNPACK_TMP && $UNPACK_TMP == "$P_SNAP_PARENT"/.unpack.* ]]; then
    as_root rm -rf -- "$UNPACK_TMP" 2>/dev/null || true
  fi
  if [[ -n $RUN_TMP ]]; then
    rm -rf -- "$RUN_TMP"
  fi
  if [[ $LOCKED == 1 ]]; then
    rm -rf -- "$WORK_DIR/.lock"
  fi
  exit "$rc"
}

# ---------------------------------------------------------------------------------------------------
# Options
# ---------------------------------------------------------------------------------------------------

need_arg() {
  if [[ $# -lt 2 || $2 == --* ]]; then
    usage_error "$1 needs a value"
  fi
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    if [[ $1 == --*=* ]]; then
      set -- "${1%%=*}" "${1#*=}" "${@:2}"
    fi
    case $1 in
      --release) need_arg "$@"; RELEASE=$2; shift 2 ;;
      --release-tag) need_arg "$@"; RELEASE_TAG=$2; shift 2 ;;
      --qsdm-sha256) need_arg "$@"; QSDM_SHA=${2,,}; shift 2 ;;
      --key-id) need_arg "$@"; KEY_ID=${2,,}; shift 2 ;;
      --checkpoint-height) need_arg "$@"; CP_HEIGHT=$2; shift 2 ;;
      --checkpoint-hash) need_arg "$@"; CP_HASH=${2,,}; shift 2 ;;
      --checkpoint-state-root) need_arg "$@"; CP_ROOT=${2,,}; shift 2 ;;
      --checkpoint-producer) need_arg "$@"; CP_PRODUCER=${2,,}; shift 2 ;;
      --from-website) FROM_WEBSITE=1; shift ;;
      --producer-schedule) need_arg "$@"; SCHEDULE_FLAG=$2; shift 2 ;;
      --update-schedule) UPDATE_SCHEDULE=1; shift ;;
      --website-url) need_arg "$@"; WEBSITE_URL=$2; shift 2 ;;
      --api) need_arg "$@"; API_BASE=$2; shift 2 ;;
      --download-base) need_arg "$@"; DOWNLOAD_BASE=$2; shift 2 ;;
      --qsdmcli) need_arg "$@"; QSDMCLI=$2; shift 2 ;;
      --require-signature) REQUIRE_SIGNATURE=1; shift ;;
      --work-dir) need_arg "$@"; WORK_DIR=$2; shift 2 ;;
      --wait-minutes) need_arg "$@"; WAIT_MINUTES=$2; shift 2 ;;
      --skip-resource-check) SKIP_RESOURCE_CHECK=1; shift ;;
      --root) need_arg "$@"; ROOT=$2; shift 2 ;;
      --dry-run) DRY_RUN=1; shift ;;
      -h | --help) usage; exit 0 ;;
      *) usage_error "unknown option: $1" ;;
    esac
  done
}

check_hex() {
  if [[ -n $2 && ! $2 =~ ^[0-9a-f]{64}$ ]]; then
    usage_error "$1 must be 64 hexadecimal characters: $2"
  fi
}

check_name() {
  if [[ -n $2 && ! $2 =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$ ]]; then
    usage_error "$1 has characters that a release name or tag does not have: $2"
  fi
}

check_url() {
  local what=$1 url=$2
  if [[ $url =~ ^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?(/[A-Za-z0-9._~%/+=-]*)?$ ]]; then
    return 0
  fi
  if [[ $url =~ ^http://127\.0\.0\.1(:[0-9]{1,5})?(/[A-Za-z0-9._~%/+=-]*)?$ ]]; then
    return 0
  fi
  usage_error "$what must start with https:// (plain http:// only for 127.0.0.1, for tests): $url"
}

validate_args() {
  check_name --release "$RELEASE"
  check_name --release-tag "$RELEASE_TAG"
  check_hex --qsdm-sha256 "$QSDM_SHA"
  check_hex --key-id "$KEY_ID"
  check_hex --checkpoint-hash "$CP_HASH"
  check_hex --checkpoint-state-root "$CP_ROOT"
  check_hex --checkpoint-producer "$CP_PRODUCER"
  if [[ -n $CP_HEIGHT && ! $CP_HEIGHT =~ ^[1-9][0-9]{0,18}$ ]]; then
    usage_error "--checkpoint-height must be a whole number above 0: $CP_HEIGHT"
  fi
  API_BASE=${API_BASE%/}
  DOWNLOAD_BASE=${DOWNLOAD_BASE%/}
  check_url --api "$API_BASE"
  check_url --website-url "$WEBSITE_URL"
  if [[ -n $DOWNLOAD_BASE ]]; then
    check_url --download-base "$DOWNLOAD_BASE"
  fi
  if [[ ! $WAIT_MINUTES =~ ^[0-9]{1,4}$ ]]; then
    usage_error "--wait-minutes must be a whole number of minutes: $WAIT_MINUTES"
  fi
  if [[ ! $RETRY_FOR =~ ^[0-9]{1,5}$ || ! $RETRY_EVERY =~ ^[1-9][0-9]{0,3}$ ]]; then
    usage_error "QSDM_FULLNODE_RETRY_FOR and QSDM_FULLNODE_RETRY_EVERY must be whole numbers of seconds (RETRY_EVERY at least 1): $RETRY_FOR $RETRY_EVERY"
  fi
  if [[ -z $WORK_DIR ]]; then
    if [[ -z ${HOME:-} ]]; then
      usage_error "HOME is not set; give a folder with --work-dir"
    fi
    WORK_DIR=$HOME/qsdm-fullnode-install
  fi
  if [[ $WORK_DIR != /* ]]; then
    usage_error "--work-dir must be an absolute path: $WORK_DIR"
  fi
  WORK_DIR=${WORK_DIR%/}
  if [[ -n $ROOT ]]; then
    if [[ $ROOT != /* || $ROOT == / || ! -d $ROOT ]]; then
      usage_error "--root must be an existing folder other than /: $ROOT"
    fi
    ROOT=${ROOT%/}
  fi
  if [[ -n $SCHEDULE_FLAG && ! ( -f $SCHEDULE_FLAG && -r $SCHEDULE_FLAG ) ]]; then
    usage_error "--producer-schedule is not a file this user can read: $SCHEDULE_FLAG"
  fi
  if [[ $UPDATE_SCHEDULE == 1 && $FROM_WEBSITE == 0 && -z $SCHEDULE_FLAG ]]; then
    usage_error "--update-schedule needs the new schedule: --from-website (reads it from $DEFAULT_WEBSITE) or --producer-schedule FILE"
  fi
  if [[ -n $QSDMCLI ]]; then
    if [[ ! -f $QSDMCLI || ! -x $QSDMCLI ]]; then
      usage_error "--qsdmcli is not an executable file: $QSDMCLI"
    fi
    if [[ $QSDMCLI != /* ]]; then
      QSDMCLI=$(cd -- "$(dirname -- "$QSDMCLI")" && pwd -P)/${QSDMCLI##*/}
    fi
  else
    QSDMCLI=$(command -v qsdmcli || true)
  fi
}

require_values() {
  local missing=()
  [[ -n $RELEASE ]] || missing+=(--release)
  [[ -n $RELEASE_TAG || -n $DOWNLOAD_BASE ]] || missing+=(--release-tag)
  [[ -n $QSDM_SHA ]] || missing+=(--qsdm-sha256)
  [[ -n $KEY_ID ]] || missing+=(--key-id)
  [[ -n $CP_HEIGHT ]] || missing+=(--checkpoint-height)
  [[ -n $CP_HASH ]] || missing+=(--checkpoint-hash)
  [[ -n $CP_ROOT ]] || missing+=(--checkpoint-state-root)
  [[ -n $CP_PRODUCER ]] || missing+=(--checkpoint-producer)
  if [[ ${#missing[@]} -gt 0 ]]; then
    usage_error "missing values to pin: ${missing[*]}. Take them from $DEFAULT_WEBSITE, or use --from-website."
  fi
}

derive_names() {
  BUNDLE_TGZ=qsdm-follower-$RELEASE.tar.gz
  SNAP_NAME=qsdm-snapshot-$CP_HEIGHT
  SNAP_ARCHIVE=$SNAP_NAME.tar.gz
  if [[ -z $DOWNLOAD_BASE ]]; then
    DOWNLOAD_BASE=$GITHUB_DOWNLOADS/$RELEASE_TAG
  fi
  P_STATE=$ROOT$STATE_DIR
  P_OPT=$ROOT$OPT_DIR
  P_ETC=$ROOT$ETC_DIR
  P_SNAP_PARENT=$ROOT$SNAP_PARENT
  P_SNAP_DIR=$P_SNAP_PARENT/$SNAP_NAME
  P_UNIT=$ROOT$UNIT_FILE
}

# ---------------------------------------------------------------------------------------------------
# Downloads, checksums and the strict readers
# ---------------------------------------------------------------------------------------------------

# proto_args URL: curl may only use the URL's own scheme, also after a redirect.
proto_args() {
  if [[ $1 == https://* ]]; then
    PROTO=(--proto '=https' --proto-redir '=https' --tlsv1.2)
  else
    PROTO=(--proto '=http' --proto-redir '=http')
  fi
}

# fetch URL DEST MAXBYTES: download one small file (DEST.partial, then DEST).
fetch() {
  local url=$1 dest=$2 max=$3
  proto_args "$url"
  rm -f -- "$dest.partial"
  if curl -q --fail --silent --show-error --location "${PROTO[@]}" --retry 3 --retry-delay 3 \
    --connect-timeout 30 --max-time 900 --max-filesize "$max" --output "$dest.partial" "$url"; then
    mv -f -- "$dest.partial" "$dest"
    return 0
  fi
  rm -f -- "$dest.partial"
  return 1
}

# api_json URL DEST: one GET to an API.
api_json() {
  proto_args "$1"
  curl -q --fail --silent --show-error "${PROTO[@]}" --connect-timeout 20 --max-time 60 \
    --max-filesize 16777216 --output "$2" "$1"
}

# download_part URL DEST SHA256: a big file, resumable. 0 = done and checked, 1 = download failed
# (DEST.partial is kept, so the next run resumes), 2 = the file did not match SHA256 twice.
download_part() {
  local url=$1 dest=$2 want=$3 partial=$2.partial attempt
  proto_args "$url"
  for attempt in 1 2; do
    if [[ -s $partial ]]; then
      if [[ $(sha256_of "$partial") == "$want" ]]; then
        mv -f -- "$partial" "$dest"
        return 0
      fi
      say "    resuming an earlier download (attempt $attempt)"
      if ! curl -q "${CURL_BIG[@]}" "${PROTO[@]}" --continue-at - --output "$partial" "$url"; then
        say "    could not resume; downloading it from the start"
        rm -f -- "$partial"
        curl -q "${CURL_BIG[@]}" "${PROTO[@]}" --output "$partial" "$url" || return 1
      fi
    else
      curl -q "${CURL_BIG[@]}" "${PROTO[@]}" --output "$partial" "$url" || return 1
    fi
    if [[ $(sha256_of "$partial") == "$want" ]]; then
      mv -f -- "$partial" "$dest"
      return 0
    fi
    say "    it does not match SNAPSHOT-SHA256SUMS; deleting it and downloading it again"
    rm -f -- "$partial"
  done
  return 2
}

sha256_of() {
  local out
  out=$(sha256sum -- "$1") || return 1
  printf '%s' "${out%% *}"
}

# py COMMAND ARGS...: the strict readers below. On failure, helper_error says why.
py() {
  python3 -I "$HELPER" "$@" 2>"$RUN_TMP/helper.err" | tr -d '\r'
}

helper_error() {
  tr -d '\r' <"$RUN_TMP/helper.err" | head -c 1000
}

write_helper() {
  HELPER=$RUN_TMP/helper.py
  cat >"$HELPER" <<'PYTHON'
"""Strict readers for install-full-node.sh: each prints plain lines, or exits 1 and says why."""
import hashlib
import json
import re
import sys
from html.parser import HTMLParser

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(newline="\n")

HEX64 = re.compile(r"[0-9a-f]{64}")
NAME = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]{0,199}")
HEIGHT = re.compile(r"[1-9][0-9]{0,18}")
PLAIN = re.compile(r"[A-Za-z0-9._-]+")
SUMS_LINE = re.compile(r"([0-9a-f]{64}) [ *]([A-Za-z0-9._-]+)")
LIMIT = 16 * 1024 * 1024


class Bad(Exception):
    pass


def read_text(path, limit=LIMIT):
    with open(path, "rb") as f:
        data = f.read(limit + 1)
    if len(data) > limit:
        raise Bad(f"it is larger than {limit} bytes")
    try:
        return data.decode("utf-8")
    except UnicodeDecodeError:
        raise Bad("it is not UTF-8 text")


def read_json(path):
    try:
        return json.loads(read_text(path))
    except ValueError as e:
        raise Bad(f"it is not valid JSON ({e})")


def hex64(value, what):
    if not isinstance(value, str) or not HEX64.fullmatch(value):
        raise Bad(f"{what} is not 64 lowercase hexadecimal characters: {value!r}")
    return value


def whole(value, what):
    if type(value) is not int or value < 0:
        raise Bad(f"{what} is not a whole number: {value!r}")
    return value


# --- qsdm.tech/fullnode.html: the table of values to pin and the GitHub release link -------------

PIN_ROWS = {
    "Full-node release": "release",
    "qsdm binary SHA-256": "qsdm_sha256",
    "Release signing key (release key v2) key_id": "key_id",
    "Snapshot checkpoint height": "checkpoint_height",
    "Checkpoint block hash": "checkpoint_hash",
    "Checkpoint state root": "checkpoint_state_root",
    "Checkpoint producer": "checkpoint_producer",
}
RELEASE_LINK = re.compile(r"https://github\.com/blackbeardONE/QSDM/releases/tag/([A-Za-z0-9][A-Za-z0-9._-]{0,99})")


class PinPage(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.tables, self.table, self.row, self.cell, self.cell_classes = [], None, None, None, []
        self.links, self.problem = [], None

    def complain(self, what):
        if self.problem is None:
            self.problem = what

    def handle_starttag(self, tag, attrs):
        attrs = {k: (v or "") for k, v in attrs}
        classes = attrs.get("class", "").split()
        if tag == "a" and "href" in attrs:
            self.links.append(attrs["href"].strip())
        if tag == "table":
            if self.table is not None:
                self.complain("the pin table holds another table")
            elif "pin-table" in classes:
                self.table = []
        elif self.table is None:
            return
        elif tag == "tr":
            if self.row is not None:
                self.complain("a row of the pin table is not closed")
            self.row = []
        elif tag in ("td", "th"):
            if tag == "th" or self.row is None or self.cell is not None:
                self.complain("the pin table has a header cell, or a cell outside a row")
            self.cell, self.cell_classes = [], classes

    def handle_endtag(self, tag):
        if self.table is None:
            return
        if tag in ("td", "th"):
            if self.cell is None or self.row is None:
                self.complain("the pin table has a cell that is not opened")
                return
            self.row.append(("".join(self.cell), self.cell_classes))
            self.cell = None
        elif tag == "tr":
            if self.row is None:
                self.complain("the pin table has a row that is not opened")
                return
            self.table.append(self.row)
            self.row = None
        elif tag == "table":
            self.tables.append(self.table)
            self.table = None

    def handle_data(self, data):
        if self.cell is not None:
            self.cell.append(data)


def cmd_website(path):
    page = PinPage()
    page.feed(read_text(path, 2 * 1024 * 1024))
    page.close()
    if page.problem:
        raise Bad(page.problem)
    if page.table is not None:
        raise Bad("the pin table is not closed")
    if len(page.tables) != 1:
        raise Bad(f"expected one table of values to pin, found {len(page.tables)}")
    values = {}
    for row in page.tables[0]:
        if len(row) != 2:
            raise Bad(f"a row of the pin table has {len(row)} cells instead of 2")
        label = " ".join(row[0][0].split())
        key = PIN_ROWS.get(label)
        if key is None:
            raise Bad(f"the pin table has a row this installer does not know: {label!r}")
        if key in values:
            raise Bad(f"the pin table has the row {label!r} twice")
        if "pin" not in row[1][1]:
            raise Bad(f"the value of {label!r} is not in a pin cell")
        values[key] = row[1][0].strip()
    missing = [label for label, key in PIN_ROWS.items() if key not in values]
    if missing:
        raise Bad("the pin table lacks: " + ", ".join(missing))
    if not NAME.fullmatch(values["release"]):
        raise Bad(f"the release name has an unexpected shape: {values['release']!r}")
    if not HEIGHT.fullmatch(values["checkpoint_height"]):
        raise Bad(f"the checkpoint height is not a whole number: {values['checkpoint_height']!r}")
    for label, key in PIN_ROWS.items():
        if key not in ("release", "checkpoint_height"):
            hex64(values[key], label)
    tags = sorted({m.group(1) for m in (RELEASE_LINK.fullmatch(h) for h in page.links) if m})
    if len(tags) != 1:
        raise Bad(f"expected one link to a GitHub release of blackbeardONE/QSDM, found {len(tags)}")
    values["release_tag"] = tags[0]
    for key in ("release", "release_tag", "qsdm_sha256", "key_id", "checkpoint_height",
                "checkpoint_hash", "checkpoint_state_root", "checkpoint_producer"):
        print(f"{key}={values[key]}")


# --- the public API and the node's own API ---------------------------------------------------------

def cmd_block(path, height, with_producer):
    data = read_json(path)
    blocks = data.get("blocks") if isinstance(data, dict) else None
    if not isinstance(blocks, list) or len(blocks) != 1 or not isinstance(blocks[0], dict):
        raise Bad("the answer does not hold exactly one block")
    block = blocks[0]
    if whole(block.get("height"), "the block height") != int(height):
        raise Bad(f"asked for block {height}, the answer is block {block.get('height')}")
    fields = [str(block["height"]), hex64(block.get("hash"), "the block hash"),
              hex64(block.get("state_root"), "the state root")]
    if with_producer == "1":
        fields.append(hex64(block.get("producer_id"), "the producer"))
    print(" ".join(fields))


def cmd_tip(path):
    data = read_json(path)
    if not isinstance(data, dict):
        raise Bad("the answer is not a JSON object")
    print(whole(data.get("chain_tip"), "chain_tip"))


# --- fullnode-core.toml: [snapshot_bootstrap] and [api] --------------------------------------------

SNAPSHOT_KEYS = ("enabled", "checkpoint_height", "checkpoint_block_hash", "checkpoint_state_root",
                 "checkpoint_producer_id")
TABLE = re.compile(r"\[\s*([A-Za-z0-9_.-]+)\s*\]")
KEY_VALUE = re.compile(r"([A-Za-z0-9_-]+)\s*=\s*(.*)")
VALUE = re.compile(r'("[^"\\]*"|[^#"]*?)\s*(#.*)?')


def toml_value(raw, where):
    m = VALUE.fullmatch(raw.strip())
    if not m:
        raise Bad(f"{where}: cannot read the value {raw.strip()!r}")
    text = m.group(1).strip()
    if len(text) >= 2 and text[0] == '"' and text[-1] == '"':
        return text[1:-1]
    if text in ("true", "false"):
        return text == "true"
    if re.fullmatch(r"[0-9]+", text):
        return int(text)
    raise Bad(f"{where}: unexpected value {text!r}")


def cmd_config(path):
    tables = {"snapshot_bootstrap": {}, "api": {}}
    seen, table = set(), None
    for n, raw in enumerate(read_text(path, 1024 * 1024).split("\n"), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("["):
            m = TABLE.fullmatch(line.split("#", 1)[0].strip())
            if not m:
                if "snapshot_bootstrap" in line:
                    raise Bad(f"line {n}: cannot read the table header {line!r}")
                table = "?"
                continue
            table = m.group(1)
            if table in seen:
                raise Bad(f"line {n}: the table [{table}] appears twice")
            seen.add(table)
            if table.startswith("snapshot_bootstrap."):
                raise Bad(f"line {n}: unexpected table [{table}]")
            continue
        if table is None and re.match(r'"?(snapshot_bootstrap|api)"?\s*[.=]', line):
            raise Bad(f"line {n}: {line!r} sets {line.split('.')[0].split('=')[0].strip()} outside its table")
        if table in tables:
            m = KEY_VALUE.fullmatch(line)
            if not m:
                raise Bad(f"line {n}: cannot read {line!r} in [{table}]")
            key = m.group(1)
            if key in tables[table]:
                raise Bad(f"line {n}: {key} appears twice in [{table}]")
            tables[table][key] = toml_value(m.group(2), f"line {n}")
    sb, api = tables["snapshot_bootstrap"], tables["api"]
    if "snapshot_bootstrap" not in seen:
        raise Bad("it has no [snapshot_bootstrap] table")
    unknown = sorted(set(sb) - set(SNAPSHOT_KEYS))
    if unknown:
        raise Bad("[snapshot_bootstrap] has settings this installer does not know: " + ", ".join(unknown))
    missing = [k for k in SNAPSHOT_KEYS if k not in sb]
    if missing:
        raise Bad("[snapshot_bootstrap] lacks: " + ", ".join(missing))
    if sb["enabled"] is not True:
        raise Bad("[snapshot_bootstrap] enabled is not true")
    height = sb["checkpoint_height"]
    if type(height) is not int or height <= 0:
        raise Bad("[snapshot_bootstrap] checkpoint_height is not a whole number above 0")
    port, bind = api.get("port"), api.get("bind_address")
    if type(port) is not int or not 1 <= port <= 65535:
        raise Bad("[api] port is missing or is not a port number")
    if bind != "127.0.0.1":
        raise Bad(f"[api] bind_address is {bind!r}, not 127.0.0.1: the guide keeps the API on loopback")
    print(f"height={height}")
    print("hash=" + hex64(sb["checkpoint_block_hash"], "checkpoint_block_hash"))
    print("root=" + hex64(sb["checkpoint_state_root"], "checkpoint_state_root"))
    print("producer=" + hex64(sb["checkpoint_producer_id"], "checkpoint_producer_id"))
    print(f"api_port={port}")


# --- the block-maker schedule: [consensus.producer_transition] -------------------------------------
#
# PRODUCER_TRANSITION.md: the table that tells a node which key may sign the block at each height. A
# release's fullnode-core.toml carries the schedule as it was when the release was made. When the block
# maker changes, qsdm.tech/fullnode.html shows the new schedule as the exact text of the table
# (<pre class="producer-schedule">), and every node must carry it.

SCHEDULE = "consensus.producer_transition"
EPOCHS = SCHEDULE + ".epochs"
V1_KEYS = ("version", "historical_signature_height", "historical_producer", "checkpoint_height", "checkpoint_hash",
           "effective_height", "replacement_producer", "historical_prefix_bytes", "historical_prefix_sha256")
V2_KEYS = ("version", "historical_prefix_bytes", "historical_prefix_sha256")
EPOCH_KEYS = ("from_height", "producer_id", "checkpoint_hash")
MAX_EPOCHS = 64
ZERO64 = "0" * 64
HEADER = re.compile(r"(\[\[?)([A-Za-z0-9_.-]+)(\]\]?)")
SCHEDULE_VALUE = re.compile(r'("[0-9a-z]*"|[0-9]+)\s*(#.*)?')


def depth_after(line, depth, n):
    """The bracket depth after LINE (strings and comments skipped): above 0 inside a multi-line array."""
    quote, i = None, 0
    while i < len(line):
        c = line[i]
        if quote:
            if quote == '"' and c == "\\":
                i += 2
                continue
            if c == quote:
                quote = None
        elif c in "\"'":
            quote = c
        elif c == "#":
            break
        elif c == "[":
            depth += 1
        elif c == "]":
            depth -= 1
            if depth < 0:
                raise Bad(f"line {n}: a ']' without its '['")
        i += 1
    if quote:
        raise Bad(f"line {n}: a string is not closed on its line")
    return depth


def config_tables(lines):
    """Each table header of a TOML file as (line index, name, is an array table), in order."""
    headers, depth = [], 0
    for i, raw in enumerate(lines):
        line = raw.strip()
        if depth == 0 and line.startswith("["):
            m = HEADER.fullmatch(line.split("#", 1)[0].strip())
            if not m or len(m.group(1)) != len(m.group(3)):
                raise Bad(f"line {i + 1}: cannot read the table header {line!r}")
            headers.append((i, m.group(2), len(m.group(1)) == 2))
            continue
        if line and not line.startswith("#"):
            depth = depth_after(line, depth, i + 1)
    if depth:
        raise Bad("a multi-line array is not closed")
    return headers


def schedule_span(lines):
    """(first, end) of the schedule's lines in a core.toml: its [consensus.producer_transition] header to its
    last setting, with every [[consensus.producer_transition.epochs]] table inside. None if there is none."""
    headers = config_tables(lines)
    mine = [k for k, (_, name, array) in enumerate(headers)
            if name == SCHEDULE or name.startswith(SCHEDULE + ".")]
    for k in mine:
        i, name, array = headers[k]
        if not (name == SCHEDULE and not array) and not (name == EPOCHS and array):
            raise Bad(f"line {i + 1}: unexpected table [{name}] in the producer schedule")
    for i, name, array in headers:
        if name == "consensus" and not array:
            continue
        if name == "consensus" or (array and name in (SCHEDULE,)):
            raise Bad(f"line {i + 1}: unexpected table header for [{name}]")
    # The producer schedule may not also be set as keys of [consensus] (an inline or dotted table).
    table = None
    for i, raw in enumerate(lines):
        line = raw.strip()
        found = [h for h in headers if h[0] == i]
        if found:
            table = found[0][1]
            continue
        if table == "consensus" and re.match(r'"?producer_transition"?\s*[.=]', line):
            raise Bad(f"line {i + 1}: the producer schedule is written as keys of [consensus]; "
                      "this installer reads it only as the [consensus.producer_transition] table")
    if not mine:
        return None
    if headers[mine[0]][1] != SCHEDULE or sum(1 for k in mine if headers[k][1] == SCHEDULE) != 1:
        raise Bad("the [consensus.producer_transition] table must come once, before its epochs")
    if mine != list(range(mine[0], mine[-1] + 1)):
        raise Bad("the producer schedule is not in one piece: another table sits between its parts")
    first = headers[mine[0]][0]
    end = headers[mine[-1] + 1][0] if mine[-1] + 1 < len(headers) else len(lines)
    while end > first + 1 and (not lines[end - 1].strip() or lines[end - 1].strip().startswith("#")):
        end -= 1
    return first, end


def schedule_from_lines(lines, where):
    """The schedule in LINES (a [consensus.producer_transition] table and its epochs, nothing else), checked
    with the node's rules (PRODUCER_TRANSITION.md): {version, prefix_bytes, prefix_sha256, epochs}."""
    table, epochs, current = {}, [], None
    for n, raw in enumerate(lines, 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("["):
            m = HEADER.fullmatch(line.split("#", 1)[0].strip())
            name = m.group(2) if m and len(m.group(1)) == len(m.group(3)) else None
            array = bool(m) and len(m.group(1)) == 2
            if name == SCHEDULE and not array and current is None and not table:
                current = table
            elif name == EPOCHS and array and current is not None:
                current = {}
                epochs.append(current)
            else:
                raise Bad(f"{where} line {n}: unexpected {line!r}")
            continue
        if current is None:
            raise Bad(f"{where} line {n}: a setting before the [consensus.producer_transition] header")
        m = KEY_VALUE.fullmatch(line)
        if not m:
            raise Bad(f"{where} line {n}: cannot read {line!r}")
        key, raw_value = m.group(1), m.group(2).strip()
        allowed = EPOCH_KEYS if current is not table else V1_KEYS
        if key not in allowed:
            raise Bad(f"{where} line {n}: the producer schedule has a setting this installer does not know: {key}")
        if key in current:
            raise Bad(f"{where} line {n}: {key} appears twice")
        v = SCHEDULE_VALUE.fullmatch(raw_value)
        if not v:
            raise Bad(f"{where} line {n}: unexpected value {raw_value!r}")
        text = v.group(1)
        current[key] = text[1:-1] if text.startswith('"') else int(text)
    if current is None:
        raise Bad(f"{where}: there is no [consensus.producer_transition] table")
    return checked_schedule(table, epochs, where)


def checked_schedule(t, raw_epochs, where):
    def number(key, value):
        if type(value) is not int or value <= 0:
            raise Bad(f"{where}: {key} must be a whole number above 0")
        return value

    def key_hex(key, value):
        if not isinstance(value, str) or not HEX64.fullmatch(value) or value == ZERO64:
            raise Bad(f"{where}: {key} must be 64 lowercase hexadecimal characters, not all zero")
        return value

    version = t.get("version")
    if version == 1:
        if raw_epochs:
            raise Bad(f"{where}: a version 1 table must not have epochs")
        missing = [k for k in V1_KEYS if k not in t]
        if missing:
            raise Bad(f"{where}: the version 1 table lacks " + ", ".join(missing))
        cp = number("checkpoint_height", t["checkpoint_height"])
        if number("effective_height", t["effective_height"]) != cp + 1:
            raise Bad(f"{where}: effective_height must be checkpoint_height + 1")
        if number("historical_signature_height", t["historical_signature_height"]) > cp:
            raise Bad(f"{where}: historical_signature_height must be at most checkpoint_height")
        old = key_hex("historical_producer", t["historical_producer"])
        new = key_hex("replacement_producer", t["replacement_producer"])
        if old == new:
            raise Bad(f"{where}: the two producers of a version 1 table must differ")
        epochs = [{"from_height": t["historical_signature_height"], "producer_id": old, "checkpoint_hash": ""},
                  {"from_height": t["effective_height"], "producer_id": new,
                   "checkpoint_hash": key_hex("checkpoint_hash", t["checkpoint_hash"])}]
    elif version == 2:
        extra = sorted(set(t) - set(V2_KEYS))
        if extra:
            raise Bad(f"{where}: a version 2 table must not set " + ", ".join(extra))
        if not 2 <= len(raw_epochs) <= MAX_EPOCHS:
            raise Bad(f"{where}: a version 2 table needs 2 to {MAX_EPOCHS} epochs, it has {len(raw_epochs)}")
        epochs, seen = [], {}
        for i, e in enumerate(raw_epochs):
            if "from_height" not in e or "producer_id" not in e:
                raise Bad(f"{where}: epoch {i} needs from_height and producer_id")
            height = number(f"epoch {i} from_height", e["from_height"])
            producer = key_hex(f"epoch {i} producer_id", e["producer_id"])
            if producer in seen:
                raise Bad(f"{where}: epoch {i} reuses the key of epoch {seen[producer]}: a key is never used twice")
            seen[producer] = i
            if i == 0:
                if "checkpoint_hash" in e:
                    raise Bad(f"{where}: epoch 0 must not have a checkpoint_hash")
                checkpoint = ""
            else:
                if height <= epochs[-1]["from_height"]:
                    raise Bad(f"{where}: epoch {i} must start above epoch {i - 1}")
                checkpoint = key_hex(f"epoch {i} checkpoint_hash", e.get("checkpoint_hash"))
            epochs.append({"from_height": height, "producer_id": producer, "checkpoint_hash": checkpoint})
    else:
        raise Bad(f"{where}: version must be 1 or 2, not {version!r}")
    return {"version": version, "prefix_bytes": number("historical_prefix_bytes", t.get("historical_prefix_bytes")),
            "prefix_sha256": key_hex("historical_prefix_sha256", t.get("historical_prefix_sha256")), "epochs": epochs}


def schedule_digest(s):
    """SHA-256 of the schedule's canonical JSON: two nodes with the same digest enforce the same schedule."""
    return hashlib.sha256(json.dumps(s, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def snapshot_pins(lines):
    """checkpoint_height and checkpoint_producer_id of [snapshot_bootstrap], as cmd_config reads them."""
    table, pins = None, {}
    for raw in lines:
        line = raw.strip()
        if line.startswith("["):
            table = line.split("#", 1)[0].strip()
            continue
        m = KEY_VALUE.fullmatch(line)
        if table == "[snapshot_bootstrap]" and m and m.group(1) in ("checkpoint_height", "checkpoint_producer_id"):
            pins[m.group(1)] = toml_value(m.group(2), "[snapshot_bootstrap]")
    return pins.get("checkpoint_height"), pins.get("checkpoint_producer_id")


def producer_at(s, height):
    found = None
    for e in s["epochs"]:
        if e["from_height"] <= height:
            found = e["producer_id"]
    return found


def print_schedule(s):
    for i, e in enumerate(s["epochs"]):
        after = f" after block {e['from_height'] - 1} {e['checkpoint_hash']}" if e["checkpoint_hash"] else ""
        print(f"epoch={i} from {e['from_height']} {e['producer_id']}{after}")
    print("digest=" + schedule_digest(s))


def schedule_text(path):
    """A schedule file (the text of qsdm.tech's <pre class="producer-schedule">): the lines, without a BOM,
    carriage returns, trailing spaces or blank lines around them."""
    text = read_text(path, 64 * 1024)
    if text.startswith("﻿"):
        text = text[1:]
    lines = [line.rstrip() for line in text.replace("\r\n", "\n").split("\n")]
    while lines and not lines[0]:
        lines.pop(0)
    while lines and not lines[-1]:
        lines.pop()
    if not lines:
        raise Bad("the schedule is empty")
    if any("\r" in line or "\t" in line for line in lines):
        raise Bad("the schedule holds a tab or a stray carriage return")
    return lines


class SchedulePage(HTMLParser):
    """The text of every <pre class="producer-schedule"> on the page."""

    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.found, self.inside, self.nested = [], None, False

    def handle_starttag(self, tag, attrs):
        if self.inside is not None:
            self.nested = True
        elif tag == "pre" and "producer-schedule" in (dict(attrs).get("class") or "").split():
            self.inside = []

    def handle_endtag(self, tag):
        if tag == "pre" and self.inside is not None:
            self.found.append("".join(self.inside))
            self.inside = None

    def handle_data(self, data):
        if self.inside is not None:
            self.inside.append(data)


def cmd_page_schedule(path, out):
    """The schedule shown on fullnode.html, written to OUT: prints "schedule=yes" or "schedule=no"."""
    page = SchedulePage()
    page.feed(read_text(path, 2 * 1024 * 1024))
    page.close()
    if page.inside is not None or page.nested:
        raise Bad("the producer schedule on the page is not one plain <pre> block")
    if len(page.found) > 1:
        raise Bad(f"the page shows {len(page.found)} producer schedules instead of one")
    if not page.found:
        print("schedule=no")
        return
    with open(out, "w", encoding="utf-8", newline="\n") as f:
        f.write(page.found[0])
    lines = schedule_text(out)
    schedule_from_lines(lines, "the schedule on the page")
    with open(out, "w", encoding="utf-8", newline="\n") as f:
        f.write("\n".join(lines) + "\n")
    print("schedule=yes")


def cmd_schedule_same(first, second):
    """Two schedule files hold the same lines (after schedule_text)."""
    if schedule_text(first) != schedule_text(second):
        raise Bad("the two producer schedules differ")


def cmd_schedule_apply(config, schedule, out):
    """CONFIG (a core.toml) with its producer schedule replaced by SCHEDULE, written to OUT.

    SCHEDULE must continue CONFIG's schedule: the same journal prefix pin and the same earlier block makers,
    each from the same height, with new block makers only added at the end. authorized_block_producers is set
    to the last block maker (PRIVATE_P2P.md section 4). Nothing else in the file changes. Prints
    "status=current" (OUT not written: CONFIG already enforces SCHEDULE) or "status=added N", then
    "first_new=H" (the first block of the first added block maker; 0 when current), the epochs and the digest."""
    text = read_text(config, 1024 * 1024)
    lines = text.split("\n")
    span = schedule_span(lines)
    if span is None:
        raise Bad("the node's configuration has no [consensus.producer_transition] table")
    old = schedule_from_lines(lines[span[0]:span[1]], "the node's configuration")
    new_lines = schedule_text(schedule)
    new = schedule_from_lines(new_lines, "the new schedule")
    if new["version"] != 2:
        raise Bad("a new schedule from qsdm.tech is always a version 2 table")
    if (new["prefix_bytes"], new["prefix_sha256"]) != (old["prefix_bytes"], old["prefix_sha256"]):
        raise Bad("the new schedule pins another journal prefix (historical_prefix_bytes or "
                  "historical_prefix_sha256) than this node's configuration")
    n = len(old["epochs"])
    if len(new["epochs"]) < n or new["epochs"][:n] != old["epochs"]:
        raise Bad("the new schedule does not continue this node's: an earlier block maker, its first block or "
                  "its checkpoint hash is different, or one is missing")
    height, producer = snapshot_pins(lines)
    if type(height) is int and isinstance(producer, str) and producer_at(new, height) != producer:
        raise Bad(f"the new schedule names another block maker for the snapshot checkpoint (block {height}) "
                  f"than [snapshot_bootstrap] does ({producer})")
    added = len(new["epochs"]) - n
    if added == 0:
        print("status=current")
        print("first_new=0")
        print_schedule(old)
        return
    final = new["epochs"][-1]["producer_id"]
    body = lines[:span[0]] + new_lines + lines[span[1]:]
    for i in range(span[0]):
        if re.match(r"authorized_block_producers\s*=", body[i].strip()):
            table = [h for h in config_tables(lines) if h[0] < i]
            if not table or table[-1][1] != "consensus" or table[-1][2]:
                continue
            if not re.fullmatch(r'authorized_block_producers\s*=\s*\[[^\]]*\]\s*(#.*)?', body[i].strip()):
                raise Bad(f"line {i + 1}: authorized_block_producers is not written on one line")
            body[i] = f'authorized_block_producers = ["{final}"]'
    result = "\n".join(body)
    check = result.split("\n")
    span2 = schedule_span(check)
    if span2 is None or schedule_from_lines(check[span2[0]:span2[1]], "the written file") != new:
        raise Bad("internal error: the written file does not hold the new schedule")
    kept = [x for k, x in enumerate(check) if not span2[0] <= k < span2[1]]
    before = [x for k, x in enumerate(lines) if not span[0] <= k < span[1]]
    changed = [k for k, (a, b) in enumerate(zip(kept, before)) if a != b]
    if len(kept) != len(before) or len(changed) > 1:
        raise Bad("internal error: the written file changes more than the schedule")
    with open(out, "w", encoding="utf-8", newline="\n") as f:
        f.write(result)
    print(f"status=added {added}")
    print(f"first_new={new['epochs'][n]['from_height']}")
    print_schedule(new)


# --- the release key, the signed manifest and checksum lists ---------------------------------------

def cmd_key(path):
    data = read_json(path)
    pk = data.get("public_key") if isinstance(data, dict) else None
    if not isinstance(pk, str) or not re.fullmatch(r"[0-9a-fA-F]{5184}", pk.strip()):
        raise Bad("public_key is not a hexadecimal ML-DSA-87 public key (2592 bytes)")
    pk = pk.strip().lower()
    print(hashlib.sha256(bytes.fromhex(pk)).hexdigest())
    print(pk)


def cmd_manifest(path):
    data = read_json(path)
    if not isinstance(data, dict) or data.get("format") != "qsdm-follower-release/v1":
        raise Bad("it is not in the format qsdm-follower-release/v1")
    files = data.get("files")
    if not isinstance(files, dict):
        raise Bad("it has no list of files")
    release = data.get("release")
    if not isinstance(release, str) or not NAME.fullmatch(release):
        raise Bad("it names no valid release")
    print(f"release={release}")
    for name, key in (("qsdm", "qsdm"), ("verify-follower-release.py", "verifier")):
        entry = files.get(name)
        if not isinstance(entry, dict):
            raise Bad(f"it does not list {name}")
        print(f"{key}=" + hex64(entry.get("sha256"), f"the SHA-256 of {name}"))


def read_sums(path):
    entries, seen = [], set()
    for n, line in enumerate(read_text(path, 1024 * 1024).split("\n"), 1):
        if line.endswith("\r"):
            line = line[:-1]
        if not line:
            continue
        m = SUMS_LINE.fullmatch(line)
        if not m:
            raise Bad(f"line {n} is not a plain sha256sum line: {line!r}")
        if m.group(2) in seen:
            raise Bad(f"{m.group(2)} is listed twice")
        seen.add(m.group(2))
        entries.append((m.group(1), m.group(2)))
    if not entries:
        raise Bad("it lists no file")
    return entries


def cmd_sums(path):
    for digest, name in read_sums(path):
        print(digest, name)


def cmd_snapshot_sums(path, archive):
    whole_sum, parts = None, {}
    part = re.compile(re.escape(archive) + r"\.part-([0-9]{2,4})")
    for digest, name in read_sums(path):
        if name == archive:
            whole_sum = digest
            continue
        m = part.fullmatch(name)
        if not m:
            raise Bad(f"it lists a file that is not part of {archive}: {name}")
        index = int(m.group(1))
        if index in parts:
            raise Bad(f"part {index} of {archive} is listed twice")
        parts[index] = (digest, name)
    if whole_sum is None:
        raise Bad(f"it does not list {archive}")
    if sorted(parts) != list(range(len(parts))):
        raise Bad("the snapshot parts are not numbered 00, 01, ... without a gap")
    print(whole_sum, archive)
    for index in sorted(parts):
        print(*parts[index])


# --- the record `qsdm --snapshot-import` writes into the state directory ---------------------------

def cmd_record(path):
    data = read_json(path)
    if not isinstance(data, dict):
        raise Bad("the import record is not a JSON object")
    cp = data.get("checkpoint")
    if not isinstance(cp, dict):
        raise Bad("the import record has no checkpoint")
    installed = data.get("installed")
    if not isinstance(installed, list):
        raise Bad("the import record has no list of installed files")
    names = []
    for entry in installed:
        if not isinstance(entry, dict) or not isinstance(entry.get("name"), str) \
                or not PLAIN.fullmatch(entry["name"]):
            raise Bad("the import record lists an installed file without a plain name")
        hex64(entry.get("sha256"), f"the SHA-256 of {entry['name']}")
        whole(entry.get("size_bytes"), f"the size of {entry['name']}")
        names.append(entry["name"])
    for key in ("authorized_by", "state_dir"):
        if not isinstance(data.get(key), str) or "\n" in data[key]:
            raise Bad(f"the import record has no {key}")
    if type(cp.get("tip_signed")) is not bool:
        raise Bad("the import record does not say whether the checkpoint block is signed")
    print("authorized_by=" + data["authorized_by"])
    print("state_dir=" + data["state_dir"])
    print(f"height={whole(cp.get('height'), 'the checkpoint height')}")
    print("hash=" + hex64(cp.get("block_hash"), "the checkpoint block hash"))
    print("root=" + hex64(cp.get("state_root"), "the checkpoint state root"))
    print("producer=" + hex64(cp.get("producer_id"), "the checkpoint producer"))
    print("tip_signed=" + ("true" if cp["tip_signed"] else "false"))
    print("installed=" + " ".join(names))


COMMANDS = {
    "website": (cmd_website, 1), "block": (cmd_block, 3), "tip": (cmd_tip, 1),
    "config": (cmd_config, 1), "key": (cmd_key, 1), "manifest": (cmd_manifest, 1),
    "sums": (cmd_sums, 1), "snapshot-sums": (cmd_snapshot_sums, 2), "record": (cmd_record, 1),
    "page-schedule": (cmd_page_schedule, 2), "schedule-same": (cmd_schedule_same, 2),
    "schedule-apply": (cmd_schedule_apply, 3),
}


def main(argv):
    if not argv or argv[0] not in COMMANDS or len(argv) - 1 != COMMANDS[argv[0]][1]:
        print("internal error: bad helper command", file=sys.stderr)
        return 2
    try:
        COMMANDS[argv[0]][0](*argv[1:])
    except (Bad, OSError) as e:
        print(e, file=sys.stderr)
        return 1
    return 0


sys.exit(main(sys.argv[1:]))
PYTHON
}

# ---------------------------------------------------------------------------------------------------
# Before anything is changed
# ---------------------------------------------------------------------------------------------------

check_tools() {
  local tool missing=()
  for tool in curl python3 sha256sum tar gzip install find cmp df mktemp sed awk tee tr head; do
    command -v "$tool" >/dev/null 2>&1 || missing+=("$tool")
  done
  if [[ $DRY_RUN == 0 ]]; then
    for tool in id systemctl useradd runuser; do
      command -v "$tool" >/dev/null 2>&1 || missing+=("$tool")
    done
    if [[ $IS_ROOT == 0 ]]; then
      command -v sudo >/dev/null 2>&1 || missing+=(sudo)
    fi
  fi
  if [[ ${#missing[@]} -gt 0 ]]; then
    stop "These tools are missing: ${missing[*]}." \
      "On Ubuntu or Debian, install them with: sudo apt-get install -y curl python3 (the others come with the system)."
  fi
  if ! python3 -c 'import sys; sys.exit(0 if sys.version_info >= (3, 7) else 1)'; then
    stop "Python 3.7 or later is needed." "On Ubuntu or Debian: sudo apt-get install -y python3"
  fi
}

read_website() {
  local page=$RUN_TMP/fullnode.html out key value
  fetch "$WEBSITE_URL" "$page" 2097152 ||
    stop "Could not read $WEBSITE_URL." \
      "Check this server's internet connection, or give the values as flags (see --help)."
  out=$(py website "$page") ||
    stop "The page $WEBSITE_URL does not have the expected shape, so none of its values were used: $(helper_error)" \
      "Give the values as flags instead (see --help), copying them from the page in your browser."
  while IFS='=' read -r key value; do
    case $key in
      release) merge_value RELEASE "$value" --release ;;
      release_tag) merge_value RELEASE_TAG "$value" --release-tag ;;
      qsdm_sha256) merge_value QSDM_SHA "$value" --qsdm-sha256 ;;
      key_id) merge_value KEY_ID "$value" --key-id ;;
      checkpoint_height) merge_value CP_HEIGHT "$value" --checkpoint-height ;;
      checkpoint_hash) merge_value CP_HASH "$value" --checkpoint-hash ;;
      checkpoint_state_root) merge_value CP_ROOT "$value" --checkpoint-state-root ;;
      checkpoint_producer) merge_value CP_PRODUCER "$value" --checkpoint-producer ;;
      *) stop "Internal error: the page reader returned $key." ;;
    esac
  done <<<"$out"
  out=$(py page-schedule "$page" "$RUN_TMP/page-schedule.toml") ||
    stop "The block-maker schedule on $WEBSITE_URL cannot be used, so nothing was installed or changed: $(helper_error)" \
      "Try again later. If it stays so, report it (SECURITY.md in the QSDM repository)."
  if [[ $out == schedule=yes ]]; then
    PAGE_SCHEDULE=$RUN_TMP/page-schedule.toml
  fi
}

# resolve_schedule: the block-maker schedule to use, from the page and/or --producer-schedule (which must agree).
resolve_schedule() {
  if [[ -n $SCHEDULE_FLAG && -n $PAGE_SCHEDULE ]]; then
    py schedule-same "$PAGE_SCHEDULE" "$SCHEDULE_FLAG" >/dev/null ||
      stop "--producer-schedule $SCHEDULE_FLAG is not the schedule on $WEBSITE_URL." \
        "Use the schedule on the page, or find out where yours came from."
  fi
  if [[ -n $PAGE_SCHEDULE ]]; then
    SCHEDULE=$PAGE_SCHEDULE SCHEDULE_FROM="from $WEBSITE_URL"
  elif [[ -n $SCHEDULE_FLAG ]]; then
    if [[ $FROM_WEBSITE == 1 ]]; then
      stop "--producer-schedule was given, but $WEBSITE_URL shows no block-maker schedule." \
        "Use the schedule the page shows, or none if it shows none."
    fi
    SCHEDULE=$SCHEDULE_FLAG SCHEDULE_FROM="in $SCHEDULE_FLAG"
  fi
}

# show_schedule HELPER_OUTPUT: the epochs that schedule-apply printed, one block maker per line.
show_schedule() {
  local line
  while IFS= read -r line; do
    case $line in
      epoch=*) say "    block maker ${line#epoch=}" ;;
      digest=*) say "    schedule digest ${line#digest=}" ;;
    esac
  done <<<"$1"
}

# value_of KEY HELPER_OUTPUT: the value of the line KEY=value.
value_of() {
  local line
  while IFS= read -r line; do
    if [[ $line == "$1="* ]]; then
      printf '%s' "${line#*=}"
      return 0
    fi
  done <<<"$2"
  return 1
}

# merge_value VARIABLE PAGE_VALUE FLAG: a value given as a flag must equal the page's.
merge_value() {
  local var=$1 site=$2 flag=$3 given
  given=${!var}
  if [[ -n $given && $given != "$site" ]]; then
    stop "$flag $given is not the value on $WEBSITE_URL ($site)." \
      "Use the value on the page, or find out where yours came from."
  fi
  printf -v "$var" '%s' "$site"
}

show_values() {
  local signature makers
  if [[ -n $QSDMCLI ]]; then
    signature="checked with $QSDMCLI"
  else
    signature="not checked: qsdmcli not found (see --qsdmcli)"
  fi
  if [[ -n $SCHEDULE ]]; then
    makers="the schedule $SCHEDULE_FROM"
  elif [[ $FROM_WEBSITE == 1 ]]; then
    makers="the release's fullnode-core.toml (the page shows no newer schedule)"
  else
    makers="the release's fullnode-core.toml (no --producer-schedule given)"
  fi
  say "QSDM full-node installer (RUN_A_FULL_NODE.md in one command)"
  say ""
  if [[ $FROM_WEBSITE == 1 ]]; then
    say "Values to pin, read from $WEBSITE_URL:"
  else
    say "Values to pin, given as flags (they must be the ones on $DEFAULT_WEBSITE):"
  fi
  printf '  %-19s %s\n' \
    "release" "$RELEASE" \
    "release tag" "${RELEASE_TAG:-(not needed: --download-base given)}" \
    "qsdm SHA-256" "$QSDM_SHA" \
    "release key_id" "$KEY_ID" \
    "checkpoint height" "$CP_HEIGHT" \
    "checkpoint hash" "$CP_HASH" \
    "state root" "$CP_ROOT" \
    "producer" "$CP_PRODUCER"
  say ""
  printf '  %-19s %s\n' \
    "downloads from" "$DOWNLOAD_BASE" \
    "public API" "$API_BASE" \
    "work folder" "$WORK_DIR" \
    "release signature" "$signature" \
    "block makers" "$makers"
  if [[ -n $ROOT ]]; then
    printf '  %-19s %s\n' "root (tests only)" "$ROOT"
  fi
}

unit_text() {
  cat <<EOF
# $UNIT_FILE
[Unit]
Description=QSDM full node (HTTP sync)
After=network-online.target
Wants=network-online.target

[Service]
User=$SERVICE
Group=$SERVICE
WorkingDirectory=$STATE_DIR
EnvironmentFile=$ETC_DIR/core.env
Environment=LOG_FILE=$STATE_DIR/qsdm.log
ExecStart=$OPT_DIR/qsdm
Restart=on-failure
RestartSec=30
RestartPreventExitStatus=78 86

[Install]
WantedBy=multi-user.target
EOF
}

core_env_text() {
  printf 'CONFIG_FILE=%s\nQSDM_CHAIN_SYNC_URLS=%s\n' "$ETC_DIR/core.toml" "$API_BASE"
}

schedule_plan() {
  if [[ -n $SCHEDULE ]]; then
    printf '\n      Then put the block-maker schedule %s into the [consensus.producer_transition]\n' "$SCHEDULE_FROM"
    printf '      table of fullnode-core.toml (it must continue the release'"'"'s schedule), and name its last\n'
    printf '      block maker in authorized_block_producers. Step 5 installs that file as core.toml.'
  fi
}

print_plan() {
  local s='' signature
  if [[ $IS_ROOT == 0 ]]; then
    s='sudo '
  fi
  if [[ -n $QSDMCLI ]]; then
    signature="python3 verify-follower-release.py with qsdmcli $QSDMCLI and key_id $KEY_ID"
  else
    signature="not checked (qsdmcli not found)"
  fi
  cat <<EOF

Dry run: this is the plan. Nothing is downloaded, installed or changed.

[1/8] Download into $WORK_DIR/downloads:
        $DOWNLOAD_BASE/$BUNDLE_TGZ
        $DOWNLOAD_BASE/fullnode-core.toml
        $DOWNLOAD_BASE/qsdm-release-key-v2.json
        $DOWNLOAD_BASE/SNAPSHOT-SHA256SUMS
[2/8] Verify the node release: every line of its SHA256SUMS must be OK, and the qsdm
      SHA-256 must be $QSDM_SHA.
      Release signature: $signature.
[3/8] Check the checkpoint (height $CP_HEIGHT, block hash $CP_HASH,
      state root $CP_ROOT, producer $CP_PRODUCER)
      against $API_BASE and fullnode-core.toml.$(schedule_plan)
[4/8] Download the snapshot parts listed in SNAPSHOT-SHA256SUMS, join them into $SNAP_ARCHIVE,
      check it, unpack it into $SNAP_PARENT/$SNAP_NAME and check its SHA256SUMS.
      (Skipped if this node already imported the snapshot.)
[5/8] Install, as the guide does:
        ${s}useradd --system --home-dir $STATE_DIR --shell /usr/sbin/nologin $SERVICE  (if the user is missing)
        ${s}install -d -o $SERVICE -g $SERVICE -m 0700 $STATE_DIR
        ${s}install -d -m 0755 $OPT_DIR $ETC_DIR
        ${s}install -m 0755 qsdm-follower-$RELEASE/qsdm $OPT_DIR/qsdm
        ${s}install -m 0640 -o root -g $SERVICE fullnode-core.toml $ETC_DIR/core.toml
        ${s}install -m 0640 -o root -g $SERVICE core.env $ETC_DIR/core.env
        ${s}chmod -R a+rX $SNAP_PARENT/$SNAP_NAME
      core.env holds:
$(core_env_text | sed 's/^/        /')
[6/8] Import the snapshot as the service user, with a clean environment:
        ${s}runuser -u $SERVICE -- sh -c 'cd $STATE_DIR && exec env -i PATH=/usr/bin:/bin CONFIG_FILE=$ETC_DIR/core.toml LOG_FILE=$STATE_DIR/import.log $OPT_DIR/qsdm --snapshot-import --from $SNAP_PARENT/$SNAP_NAME'
      then check its record ($STATE_DIR/$IMPORT_RECORD), and delete the unpacked snapshot
      and the downloaded archive.
[7/8] Write $UNIT_FILE:
$(unit_text | sed 's/^/        /')
      then: ${s}systemctl daemon-reload && ${s}systemctl enable --now $SERVICE
[8/8] Wait up to $WAIT_MINUTES minutes for the node to catch up, then compare blocks T-100 and T-1
      (T is your node's tip) with $API_BASE.
EOF
  if [[ -n $ROOT ]]; then
    say "Every path above would be under $ROOT (--root, tests only)."
  fi
}

check_system() {
  if [[ -n $ROOT ]]; then
    return 0
  fi
  if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
    stop "This installer is for Linux on x86_64. This server is $(uname -s) $(uname -m)."
  fi
  if [[ ! -d /run/systemd/system ]]; then
    stop "This server does not run systemd, which the full node service needs."
  fi
}

prepare_sudo() {
  if [[ $IS_ROOT == 1 ]]; then
    return 0
  fi
  say ""
  say "The system steps run with sudo; it may ask for your password."
  sudo -v || stop "sudo did not work." "Run the installer as root, or as a user who can use sudo."
}

prepare_work_dir() {
  if [[ -e $WORK_DIR && ! -d $WORK_DIR ]]; then
    stop "$WORK_DIR exists and is not a folder." "Choose another folder with --work-dir."
  fi
  if [[ -d $WORK_DIR && ! -f $WORK_DIR/.qsdm-fullnode-install ]] &&
    [[ -n $(find "$WORK_DIR" -mindepth 1 -maxdepth 1 -print -quit) ]]; then
    stop "$WORK_DIR is not empty, and this installer did not make it." \
      "Choose an empty or new folder with --work-dir."
  fi
  if ! mkdir -p -- "$WORK_DIR/downloads" "$WORK_DIR/logs" "$WORK_DIR/state" ||
    ! chmod 0700 "$WORK_DIR" || ! touch -- "$WORK_DIR/.qsdm-fullnode-install"; then
    stop "Could not set up the work folder $WORK_DIR." "Choose another folder with --work-dir."
  fi
  DL=$WORK_DIR/downloads
  STATE=$WORK_DIR/state
  take_lock
}

take_lock() {
  local pid
  if ! mkdir -- "$WORK_DIR/.lock" 2>/dev/null; then
    pid=$(cat -- "$WORK_DIR/.lock/pid" 2>/dev/null || true)
    if [[ $pid =~ ^[0-9]+$ ]] && kill -0 "$pid" 2>/dev/null; then
      stop "Another run of the installer (process $pid) is using $WORK_DIR." \
        "Wait for it to finish. If no other run is going on, delete $WORK_DIR/.lock and run the same command again."
    fi
    rm -rf -- "$WORK_DIR/.lock"
    mkdir -- "$WORK_DIR/.lock" || stop "Could not lock $WORK_DIR."
  fi
  LOCKED=1
  printf '%s\n' "$$" >"$WORK_DIR/.lock/pid"
}

# read_import_record: R_* from the record in the state directory. 1 = no record, 2 = unreadable.
read_import_record() {
  local out key value
  as_root test -f "$P_STATE/$IMPORT_RECORD" || return 1
  as_root cat -- "$P_STATE/$IMPORT_RECORD" >"$RUN_TMP/import-record.json" || return 2
  out=$(py record "$RUN_TMP/import-record.json") || return 2
  R_AUTHORIZED='' R_STATE_DIR='' R_HEIGHT='' R_HASH='' R_ROOT='' R_PRODUCER='' R_SIGNED='' R_INSTALLED=''
  while IFS='=' read -r key value; do
    case $key in
      authorized_by) R_AUTHORIZED=$value ;;
      state_dir) R_STATE_DIR=$value ;;
      height) R_HEIGHT=$value ;;
      hash) R_HASH=$value ;;
      root) R_ROOT=$value ;;
      producer) R_PRODUCER=$value ;;
      tip_signed) R_SIGNED=$value ;;
      installed) R_INSTALLED=$value ;;
    esac
  done <<<"$out"
}

# record_problems: what in R_* differs from the guide's "exit 0" and the pinned checkpoint.
record_problems() {
  local files
  read -r -a files <<<"$R_INSTALLED"
  [[ $R_AUTHORIZED == producer_transition ]] ||
    printf '    authorized_by is "%s", not "producer_transition"\n' "$R_AUTHORIZED"
  [[ $R_HEIGHT == "$CP_HEIGHT" ]] || printf '    checkpoint height %s, not %s\n' "$R_HEIGHT" "$CP_HEIGHT"
  [[ $R_HASH == "$CP_HASH" ]] || printf '    checkpoint block hash %s, not %s\n' "$R_HASH" "$CP_HASH"
  [[ $R_ROOT == "$CP_ROOT" ]] || printf '    checkpoint state root %s, not %s\n' "$R_ROOT" "$CP_ROOT"
  [[ $R_PRODUCER == "$CP_PRODUCER" ]] || printf '    checkpoint producer %s, not %s\n' "$R_PRODUCER" "$CP_PRODUCER"
  [[ $R_SIGNED == true ]] || printf '    the checkpoint block is not marked as signed\n'
  [[ ${#files[@]} == 3 ]] || printf '    it installed %s files, not three: %s\n' "${#files[@]}" "$R_INSTALLED"
  [[ $R_STATE_DIR == "$P_STATE" ]] || printf '    state directory %s, not %s\n' "$R_STATE_DIR" "$P_STATE"
}

detect_import_state() {
  local rc=0 problems found
  read_import_record || rc=$?
  if [[ $rc == 0 ]]; then
    problems=$(record_problems)
    if [[ -n $problems ]]; then
      stop "This server already has a node in $STATE_DIR, and its import record does not match:" \
        "$problems" \
        "This installer sets up new nodes. To update an existing one, follow \"Keeping up to date\" in RUN_A_FULL_NODE.md."
    fi
    IMPORT_DONE=1
    return 0
  fi
  if [[ $rc == 2 ]]; then
    stop "$STATE_DIR/$IMPORT_RECORD exists but cannot be read: $(helper_error)" \
      "Look at $STATE_DIR yourself. The installer never deletes chain data."
  fi
  if as_root test -d "$P_STATE"; then
    found=$(as_root find "$P_STATE" -mindepth 1 -maxdepth 1 \( -name "${CHAIN_FILES[0]}" \
      -o -name "${CHAIN_FILES[1]}" -o -name "${CHAIN_FILES[2]}" -o -name "${CHAIN_FILES[3]}" \
      -o -name "${CHAIN_FILES[4]}" -o -name "${CHAIN_FILES[5]}" -o -name "${CHAIN_FILES[6]}" \) \
      -printf '%f ') || stop "Could not look into $STATE_DIR."
    if [[ -n $found ]]; then
      stop "$STATE_DIR already holds chain files ($found) but no import record." \
        "An earlier node or an interrupted import left them there. The installer never deletes chain data:" \
        "if you do not need them, move them away yourself, then run the same command again."
    fi
  fi
}

check_resources() {
  local mem_kb avail_kb need_kb=$NEED_MEM_KB
  local why="Importing the snapshot and starting the node each need about 2 GB at 880 376 blocks, and a running node holds about 0.5 GB more for every 100 000 blocks it adds until it restarts."
  if [[ $SKIP_RESOURCE_CHECK == 1 ]]; then
    return 0
  fi
  if [[ $RELEASE == "$RC1019_RELEASE" ]]; then
    need_kb=$RC1019_NEED_MEM_KB
    why="This release loads the whole chain into memory: a start needs about 10.4 GB at 1 020 000 blocks, and about 1.6 GB more for every 100 000 blocks after that."
  fi
  mem_kb=$(awk '/^MemTotal:/ { print $2; exit }' "$MEMINFO" 2>/dev/null || true)
  if [[ $mem_kb =~ ^[0-9]+$ ]] && ((mem_kb < need_kb)); then
    stop "This server has about $((mem_kb / 1024 / 1024)) GB of memory. A full node needs more. $why" \
      "Use a server with more memory (\"What you need\" in RUN_A_FULL_NODE.md has the figures). If you are sure, run the same command with --skip-resource-check."
  fi
  if [[ $IMPORT_DONE == 1 ]] || compgen -G "$DL/qsdm-snapshot-*" >/dev/null; then
    return 0
  fi
  avail_kb=$(df -Pk "$WORK_DIR" | awk 'NR == 2 { print $4 }')
  if [[ $avail_kb =~ ^[0-9]+$ ]] && ((avail_kb < NEED_DISK_KB)); then
    stop "There are about $((avail_kb / 1000 / 1000)) GB free where the installer works ($WORK_DIR). Setting up a full node needs about 15 GB." \
      "Free some space, or choose a folder on a bigger disk with --work-dir. If you are sure, run the same command with --skip-resource-check."
  fi
}

# ---------------------------------------------------------------------------------------------------
# The guide's steps
# ---------------------------------------------------------------------------------------------------

step_download() {
  local name max
  step 1 "Download the release files"
  say "  from $DOWNLOAD_BASE"
  for name in "$BUNDLE_TGZ" fullnode-core.toml qsdm-release-key-v2.json SNAPSHOT-SHA256SUMS; do
    max=1048576
    if [[ $name == "$BUNDLE_TGZ" ]]; then
      max=536870912
    fi
    fetch "$DOWNLOAD_BASE/$name" "$DL/$name" "$max" ||
      stop "Could not download $name from $DOWNLOAD_BASE." \
        "Check that this release is the one named on qsdm.tech/fullnode.html and that this server can reach the address, then run the same command again."
    ok "downloaded $name"
  done
}

# check_release_tree DIR: DIR holds only the folder qsdm-follower-RELEASE, and it holds only plain files.
check_release_tree() {
  local dir=$1 names bad
  names=$(find "$dir" -mindepth 1 -maxdepth 1 -printf '%f\n') || names='?'
  if [[ $names != "qsdm-follower-$RELEASE" ]]; then
    UNPACK_PROBLEM="it does not hold just the folder qsdm-follower-$RELEASE"
    return 1
  fi
  bad=$(find "$dir/qsdm-follower-$RELEASE" -mindepth 1 \( ! -type f -o -perm /7000 -o -links +1 \) \
    -printf '%P\n' -quit) || bad='(the release folder could not be read)'
  if [[ -n $bad ]]; then
    UNPACK_PROBLEM="it holds something that is not a plain file: $bad"
    return 1
  fi
}

step_verify_release() {
  local unpacked=$RUN_TMP/release sums got
  step 2 "Verify the node release"
  mkdir -- "$unpacked"
  tar --extract --gzip --no-same-owner --no-same-permissions --directory "$unpacked" --file "$DL/$BUNDLE_TGZ" ||
    stop "Could not unpack $BUNDLE_TGZ: the download is damaged, or is not this release." \
      "Run the same command again to download it again."
  check_release_tree "$unpacked" ||
    stop "$BUNDLE_TGZ is not laid out like a full-node release: $UNPACK_PROBLEM." \
      "Do not use this download. Check the release name on qsdm.tech/fullnode.html."
  BUNDLE=$unpacked/qsdm-follower-$RELEASE
  sums=$(py sums "$BUNDLE/SHA256SUMS") ||
    stop "The release's SHA256SUMS is not a plain checksum list: $(helper_error)" "Do not use this download."
  grep -Eq '^[0-9a-f]{64} qsdm$' <<<"$sums" || stop "The release's SHA256SUMS does not list qsdm." "Do not use this download."
  say "  sha256sum -c SHA256SUMS"
  if ! (cd -- "$BUNDLE" && sha256sum --strict --check SHA256SUMS) 2>&1 | sed 's/^/    /'; then
    stop "A file in the release does not match its line in SHA256SUMS (see above)." \
      "The download is damaged or was changed. Run the same command again to download it again. If it fails again, do not use this release, and report it (SECURITY.md in the QSDM repository)."
  fi
  ok "every line of SHA256SUMS is OK"
  got=$(sha256_of "$BUNDLE/qsdm")
  say "  sha256sum qsdm"
  say "    release:   $got"
  say "    qsdm.tech: $QSDM_SHA"
  if [[ $got != "$QSDM_SHA" ]]; then
    stop "The qsdm binary in the release does not match the SHA-256 published on qsdm.tech." \
      "Do not use this download. Check that you took the value from https://qsdm.tech/fullnode.html for this release. If it still differs, report it (SECURITY.md in the QSDM repository)."
  fi
  ok "the qsdm binary matches the SHA-256 published on qsdm.tech"
  if [[ -z $QSDMCLI ]]; then
    note "qsdmcli was not found, so the release signature was not checked. The qsdm binary was checked against the SHA-256 published on qsdm.tech. To check the signature too, pass --qsdmcli PATH (qsdmcli comes with QSDM Hive, or build it from source)."
    return 0
  fi
  verify_signature
}

verify_signature() {
  local key key_id out manifest key_name value m_release='' m_qsdm='' m_verifier='' last
  key=$(py key "$DL/qsdm-release-key-v2.json") ||
    stop "qsdm-release-key-v2.json is not a release key file: $(helper_error)" "Do not use this download."
  key_id=${key%%$'\n'*}
  if [[ $key_id != "$KEY_ID" ]]; then
    stop "The release key in the download is not the key published on qsdm.tech." \
      "key_id of the download: $key_id" "key_id on qsdm.tech:   $KEY_ID" "Do not use this download."
  fi
  printf '%s\n' "${key#*$'\n'}" >"$RUN_TMP/release-key.hex"
  say "  qsdmcli wallet verify (the signature over follower-manifest.json)"
  if ! out=$("$QSDMCLI" wallet verify --public-key-file "$RUN_TMP/release-key.hex" \
    --message-file "$BUNDLE/follower-manifest.json" --signature-file "$BUNDLE/follower-manifest.json.sig" 2>&1) ||
    ! grep -Fq 'OK: ML-DSA-87 signature verified' <<<"$out"; then
    stop "The release signature does not verify with the key published on qsdm.tech." \
      "qsdmcli said: $(printf '%s' "$out" | tr -d '\r' | head -c 600)" "Do not use this download."
  fi
  manifest=$(py manifest "$BUNDLE/follower-manifest.json") ||
    stop "The signed follower-manifest.json cannot be read: $(helper_error)" "Do not use this download."
  while IFS='=' read -r key_name value; do
    case $key_name in
      release) m_release=$value ;;
      qsdm) m_qsdm=$value ;;
      verifier) m_verifier=$value ;;
    esac
  done <<<"$manifest"
  if [[ $m_release != "$RELEASE" || $m_qsdm != "$QSDM_SHA" ]]; then
    stop "The signed manifest names release $m_release with qsdm $m_qsdm, not the release and SHA-256 from qsdm.tech." \
      "Do not use this download."
  fi
  if [[ $(sha256_of "$BUNDLE/verify-follower-release.py") != "$m_verifier" ]]; then
    stop "verify-follower-release.py is not the one the signed manifest names." "Do not use this download."
  fi
  ok "the signature verifies with key_id $KEY_ID, and the signed manifest names this qsdm"
  say "  python3 verify-follower-release.py (the guide's check)"
  if ! out=$(cd -- "$RUN_TMP" && python3 -I "$BUNDLE/verify-follower-release.py" "$BUNDLE" \
    --public-key-json "$DL/qsdm-release-key-v2.json" --expected-key-id "$KEY_ID" --qsdmcli "$QSDMCLI" 2>&1); then
    printf '%s\n' "$out" | tr -d '\r' | sed 's/^/    /'
    stop "verify-follower-release.py refused the release (see above)." "Do not use this download."
  fi
  out=$(printf '%s\n' "$out" | tr -d '\r')
  printf '%s\n' "$out" | sed 's/^/    /'
  last=$(printf '%s\n' "$out" | sed '/^$/d' | tail -n 1)
  if [[ $last != "verify-follower-release: VERIFIED $RELEASE qsdm $QSDM_SHA" ]]; then
    stop "verify-follower-release.py did not end with the line the guide expects." \
      "It printed:   $last" "The guide expects: verify-follower-release: VERIFIED $RELEASE qsdm $QSDM_SHA"
  fi
  ok "$last"
}

# same_checkpoint WHAT SITE API CONFIG
same_checkpoint() {
  if [[ $2 == "$3" && $2 == "$4" ]]; then
    return 0
  fi
  stop "The checkpoint $1 does not match." \
    "  qsdm.tech/fullnode.html: $2" "  public API:              $3" "  fullnode-core.toml:      $4" \
    "Do not continue. Check that you copied the values from https://qsdm.tech/fullnode.html. If they are right, report the difference (SECURITY.md in the QSDM repository)."
}

step_checkpoint() {
  local answer=$RUN_TMP/checkpoint-block.json line config key value
  local a_height a_hash a_root a_producer c_height='' c_hash='' c_root='' c_producer='' c_port=''
  step 3 "Check the snapshot checkpoint three ways"
  api_json "$API_BASE/chain/blocks?from=$CP_HEIGHT&to=$CP_HEIGHT&limit=1" "$answer" ||
    stop "Could not read block $CP_HEIGHT from the public API ($API_BASE)." \
      "Check this server's internet connection, then run the same command again."
  line=$(py block "$answer" "$CP_HEIGHT" 1) ||
    stop "The public API's answer for block $CP_HEIGHT cannot be used: $(helper_error)" "Run the same command again later."
  read -r a_height a_hash a_root a_producer <<<"$line"
  config=$(py config "$DL/fullnode-core.toml") ||
    stop "The release's fullnode-core.toml cannot be used: $(helper_error)" \
      "Do not use this download. Check the release name on qsdm.tech/fullnode.html."
  while IFS='=' read -r key value; do
    case $key in
      height) c_height=$value ;;
      hash) c_hash=$value ;;
      root) c_root=$value ;;
      producer) c_producer=$value ;;
      api_port) c_port=$value ;;
    esac
  done <<<"$config"
  say "  height, block hash, state root and producer:"
  say "    qsdm.tech           $CP_HEIGHT $CP_HASH $CP_ROOT $CP_PRODUCER"
  say "    public API          $a_height $a_hash $a_root $a_producer"
  say "    fullnode-core.toml  $c_height $c_hash $c_root $c_producer"
  same_checkpoint height "$CP_HEIGHT" "$a_height" "$c_height"
  same_checkpoint "block hash" "$CP_HASH" "$a_hash" "$c_hash"
  same_checkpoint "state root" "$CP_ROOT" "$a_root" "$c_root"
  same_checkpoint producer "$CP_PRODUCER" "$a_producer" "$c_producer"
  ok "all three name the same height, block hash, state root and producer"
  LOCAL_API=http://127.0.0.1:$c_port/api/v1
  release_schedule
}

# release_schedule: the core.toml to install: the release's fullnode-core.toml, with the block-maker schedule
# from the page (or --producer-schedule) when there is one that continues the release's.
release_schedule() {
  local out status
  TARGET_CONFIG=$DL/fullnode-core.toml
  TARGET_WHAT="the release's fullnode-core.toml"
  if [[ -z $SCHEDULE ]]; then
    return 0
  fi
  out=$(py schedule-apply "$DL/fullnode-core.toml" "$SCHEDULE" "$RUN_TMP/core.toml") ||
    stop "The block-maker schedule $SCHEDULE_FROM cannot be used with this release: $(helper_error)" \
      "Do not continue. Check that it is the schedule on https://qsdm.tech/fullnode.html. If it is, report the difference (SECURITY.md in the QSDM repository)."
  say "  the block-maker schedule $SCHEDULE_FROM:"
  show_schedule "$out"
  status=$(value_of status "$out")
  if [[ $status == current ]]; then
    ok "the release's fullnode-core.toml already has this schedule"
    return 0
  fi
  TARGET_CONFIG=$RUN_TMP/core.toml
  TARGET_WHAT="the release's fullnode-core.toml with the block-maker schedule $SCHEDULE_FROM"
  ok "it continues the release's schedule with ${status#added } more block maker(s); core.toml will carry it"
}

# check_inner_sums: the unpacked snapshot against its own SHA256SUMS.
check_inner_sums() {
  local copy=$RUN_TMP/snapshot-SHA256SUMS
  if ! as_root cat -- "$P_SNAP_DIR/SHA256SUMS" >"$copy" 2>/dev/null; then
    say "    the snapshot has no SHA256SUMS"
    return 1
  fi
  if ! py sums "$copy" >/dev/null; then
    say "    the snapshot's SHA256SUMS is not a plain checksum list: $(helper_error)"
    return 1
  fi
  say "  ( cd $SNAP_PARENT/$SNAP_NAME && sha256sum -c SHA256SUMS )"
  # shellcheck disable=SC2016 # $1 is expanded by the inner shell.
  as_root sh -c 'cd "$1" && sha256sum --strict --check SHA256SUMS' sh "$P_SNAP_DIR" 2>&1 | sed 's/^/    /'
}

remove_created_parent() {
  if [[ -f $STATE/created-snapshot-parent ]]; then
    if as_root rmdir -- "$P_SNAP_PARENT" 2>/dev/null; then
      say "  removed the empty $SNAP_PARENT"
    fi
    rm -f -- "$STATE/created-snapshot-parent"
  fi
}

discard_unpack_tmp() {
  if [[ -n $UNPACK_TMP ]]; then
    as_root rm -rf -- "$UNPACK_TMP" || true
    UNPACK_TMP=''
  fi
  remove_created_parent
}

unpack_snapshot() {
  local record=$STATE/unpacked names bad
  if as_root test -e "$P_SNAP_DIR"; then
    if [[ ! -f $record ]]; then
      stop "$SNAP_PARENT/$SNAP_NAME already exists, and this installer did not make it." \
        "Move it away or delete it yourself, then run the same command again."
    fi
    if [[ $(cat -- "$record") == "$ARCHIVE_SHA" ]] && check_inner_sums; then
      ok "$SNAP_PARENT/$SNAP_NAME was unpacked by an earlier run and matches its SHA256SUMS"
      return 0
    fi
    say "  the snapshot unpacked by an earlier run is not this archive, or is damaged: unpacking it again"
    run_root rm -rf -- "$P_SNAP_DIR"
    rm -f -- "$record"
  fi
  if ! as_root test -d "$P_SNAP_PARENT"; then
    run_root install -d -m 0755 "$P_SNAP_PARENT"
    touch -- "$STATE/created-snapshot-parent"
  fi
  UNPACK_TMP=$(as_root mktemp -d "$P_SNAP_PARENT/.unpack.XXXXXXXX") ||
    stop "Could not make a folder in $SNAP_PARENT."
  say "  unpacking $SNAP_ARCHIVE into $SNAP_PARENT (this takes a few minutes)"
  if ! as_root tar --extract --gzip --no-same-owner --no-same-permissions --directory "$UNPACK_TMP" \
    --file "$DL/$SNAP_ARCHIVE"; then
    discard_unpack_tmp
    stop "Could not unpack $SNAP_ARCHIVE." "Check the free disk space under $SNAP_PARENT, then run the same command again."
  fi
  names=$(as_root find "$UNPACK_TMP" -mindepth 1 -maxdepth 1 -printf '%f\n') || names='?'
  if [[ $names != "$SNAP_NAME" ]]; then
    discard_unpack_tmp
    stop "$SNAP_ARCHIVE does not hold just the folder $SNAP_NAME." "Do not use this download; report it (SECURITY.md in the QSDM repository)."
  fi
  bad=$(as_root find "$UNPACK_TMP/$SNAP_NAME" -mindepth 1 \( ! -type f -o -perm /7000 -o -links +1 \) \
    -printf '%P\n' -quit) || bad='(the snapshot folder could not be read)'
  if [[ -n $bad ]]; then
    discard_unpack_tmp
    stop "The snapshot holds something that is not a plain file: $bad" "Do not use this download; report it (SECURITY.md in the QSDM repository)."
  fi
  printf '%s\n' "$ARCHIVE_SHA" >"$record"
  run_root mv -- "$UNPACK_TMP/$SNAP_NAME" "$P_SNAP_DIR"
  as_root rmdir -- "$UNPACK_TMP" || true
  UNPACK_TMP=''
  if ! check_inner_sums; then
    run_root rm -rf -- "$P_SNAP_DIR"
    rm -f -- "$record"
    remove_created_parent
    stop "A file in the unpacked snapshot does not match the snapshot's SHA256SUMS (see above)." \
      "The snapshot archive matched SNAPSHOT-SHA256SUMS, so the release itself is wrong. Do not use it; report it (SECURITY.md in the QSDM repository)."
  fi
  ok "the unpacked snapshot matches its SHA256SUMS"
}

step_snapshot() {
  local sums lines line want name rc parts=()
  step 4 "Get and verify the snapshot"
  if [[ $IMPORT_DONE == 1 ]]; then
    ok "skipped: this node already imported the snapshot at height $CP_HEIGHT"
    return 0
  fi
  sums=$(py snapshot-sums "$DL/SNAPSHOT-SHA256SUMS" "$SNAP_ARCHIVE") ||
    stop "SNAPSHOT-SHA256SUMS cannot be used for the snapshot at height $CP_HEIGHT: $(helper_error)" \
      "Check that this release is the one named on qsdm.tech/fullnode.html."
  mapfile -t lines <<<"$sums"
  ARCHIVE_SHA=${lines[0]%% *}
  if [[ -f $DL/$SNAP_ARCHIVE && $(sha256_of "$DL/$SNAP_ARCHIVE") == "$ARCHIVE_SHA" ]]; then
    ok "$SNAP_ARCHIVE was downloaded by an earlier run"
  else
    rm -f -- "$DL/$SNAP_ARCHIVE" "$DL/$SNAP_ARCHIVE.joining"
    if [[ ${#lines[@]} -gt 1 ]]; then
      for line in "${lines[@]:1}"; do
        want=${line%% *}
        name=${line#* }
        parts+=("$DL/$name")
        if [[ -f $DL/$name && $(sha256_of "$DL/$name") == "$want" ]]; then
          ok "$name was downloaded by an earlier run"
          continue
        fi
        rm -f -- "$DL/$name"
        say "  downloading $name"
        rc=0
        download_part "$DOWNLOAD_BASE/$name" "$DL/$name" "$want" || rc=$?
        if [[ $rc == 1 ]]; then
          stop "Downloading $name failed." "Run the same command again: it carries on with the download."
        elif [[ $rc != 0 ]]; then
          stop "$name does not match its line in SNAPSHOT-SHA256SUMS (downloaded twice)." \
            "The release file is damaged or was changed. Run the same command again later. If it fails again, do not use this release, and report it (SECURITY.md in the QSDM repository)."
        fi
        ok "$name matches SNAPSHOT-SHA256SUMS"
      done
      say "  cat $SNAP_ARCHIVE.part-* > $SNAP_ARCHIVE"
      cat -- "${parts[@]}" >"$DL/$SNAP_ARCHIVE.joining" ||
        stop "Could not join the snapshot parts in $DL." "Check the free disk space there, then run the same command again."
      mv -f -- "$DL/$SNAP_ARCHIVE.joining" "$DL/$SNAP_ARCHIVE"
    else
      say "  downloading $SNAP_ARCHIVE"
      rc=0
      download_part "$DOWNLOAD_BASE/$SNAP_ARCHIVE" "$DL/$SNAP_ARCHIVE" "$ARCHIVE_SHA" || rc=$?
      if [[ $rc == 1 ]]; then
        stop "Downloading $SNAP_ARCHIVE failed." "Run the same command again: it carries on with the download."
      fi
    fi
    if [[ $(sha256_of "$DL/$SNAP_ARCHIVE") != "$ARCHIVE_SHA" ]]; then
      rm -f -- "$DL/$SNAP_ARCHIVE"
      stop "$SNAP_ARCHIVE does not match its line in SNAPSHOT-SHA256SUMS." \
        "The release files do not agree with each other. Run the same command again later. If it fails again, do not use this release, and report it (SECURITY.md in the QSDM repository)."
    fi
    # The parts are only needed to make the archive; free their space.
    for line in "${lines[@]:1}"; do
      rm -f -- "$DL/${line#* }"
    done
  fi
  ok "$SNAP_ARCHIVE matches SNAPSHOT-SHA256SUMS"
  unpack_snapshot
}

# put_file SOURCE DEST MODE GROUP WHAT: install a file as the guide does, or accept an identical one.
put_file() {
  local source=$1 dest=$2 mode=$3 group=$4 what=$5 hint=${6:-}
  if as_root test -e "$dest"; then
    if as_root cmp -s -- "$source" "$dest"; then
      ok "${dest#"$ROOT"} is already in place"
      return 0
    fi
    if [[ -n $hint ]]; then
      stop "${dest#"$ROOT"} already exists and is not $what." "$hint"
    fi
    stop "${dest#"$ROOT"} already exists and is not $what." \
      "The installer does not overwrite it. If it is from an older install, follow \"Keeping up to date\" in RUN_A_FULL_NODE.md. Otherwise move it away, then run the same command again."
  fi
  if [[ -n $group ]]; then
    run_root install -m "$mode" -o root -g "$group" "$source" "$dest"
  else
    run_root install -m "$mode" "$source" "$dest"
  fi
}

step_install() {
  local uid
  step 5 "Install"
  INSTALL_STARTED=1
  if uid=$(id -u "$SERVICE" 2>/dev/null); then
    if [[ $uid == 0 ]]; then
      stop "The user $SERVICE exists and has uid 0, like root." "A full node must run as an unprivileged user. Fix the user, then run the same command again."
    fi
    ok "the user $SERVICE already exists"
  else
    run_root useradd --system --home-dir "$STATE_DIR" --shell /usr/sbin/nologin "$SERVICE"
  fi
  run_root install -d -o "$SERVICE" -g "$SERVICE" -m 0700 "$P_STATE"
  run_root install -d -m 0755 "$P_OPT" "$P_ETC"
  put_file "$BUNDLE/qsdm" "$P_OPT/qsdm" 0755 '' "the qsdm binary of this release"
  put_file "$TARGET_CONFIG" "$P_ETC/core.toml" 0640 "$SERVICE" "$TARGET_WHAT" \
    "The installer does not overwrite it. If the block maker changed since this node was set up (qsdm.tech/fullnode.html shows a block-maker schedule), run: $PROG --update-schedule --from-website. If it is from an older release, follow \"Keeping up to date\" in RUN_A_FULL_NODE.md. Otherwise move it away, then run the same command again."
  core_env_text >"$RUN_TMP/core.env"
  put_file "$RUN_TMP/core.env" "$P_ETC/core.env" 0640 "$SERVICE" "the guide's core.env for this API"
  if [[ $IMPORT_DONE == 0 ]]; then
    run_root chmod -R a+rX "$P_SNAP_DIR"
  fi
}

cleanup_snapshot_files() {
  if [[ -f $STATE/unpacked ]]; then
    run_root rm -rf -- "$P_SNAP_DIR"
    rm -f -- "$STATE/unpacked"
  fi
  remove_created_parent
  rm -f -- "$DL/$SNAP_ARCHIVE" "$DL/$SNAP_ARCHIVE".part-* "$DL/$SNAP_ARCHIVE".partial "$DL/$SNAP_ARCHIVE".joining
  ok "deleted the unpacked snapshot and the downloaded archive, as the guide says"
}

step_import() {
  local log rc problems s=''
  step 6 "Import the snapshot"
  if [[ $IMPORT_DONE == 1 ]]; then
    ok "already done: $STATE_DIR/$IMPORT_RECORD records the import at height $CP_HEIGHT"
    return 0
  fi
  if [[ $IS_ROOT == 0 ]]; then
    s='sudo '
  fi
  log=$WORK_DIR/logs/import-$(date -u +%Y%m%dT%H%M%SZ).log
  say "  The import checks the whole chain again against the pinned checkpoint: the hash chain from"
  say "  genesis, the producer's signature, the producer transition and the state root. It takes"
  say "  several minutes and about 9 GB of memory (more for a later snapshot). Its output also goes to $log."
  say "  + ${s}runuser -u $SERVICE -- sh -c 'cd $STATE_DIR && exec env -i PATH=/usr/bin:/bin CONFIG_FILE=$ETC_DIR/core.toml LOG_FILE=$STATE_DIR/import.log $OPT_DIR/qsdm --snapshot-import --from $SNAP_PARENT/$SNAP_NAME'"
  rc=0
  # shellcheck disable=SC2016 # $1..$5 are expanded by the inner shell.
  as_root runuser -u "$SERVICE" -- sh -c \
    'cd "$1" && exec env -i PATH=/usr/bin:/bin CONFIG_FILE="$2" LOG_FILE="$3" "$4" --snapshot-import --from "$5"' \
    sh "$P_STATE" "$P_ETC/core.toml" "$P_STATE/import.log" "$P_OPT/qsdm" "$P_SNAP_DIR" 2>&1 |
    tee -- "$log" | sed 's/^/    /' || rc=$?
  case $rc in
    0) ;;
    78)
      stop "The import refused the snapshot (exit 78) and installed nothing. The reason is in its FATAL line above." \
        "Usually it is a wrong checkpoint, the wrong release or a damaged download. Fix that, then run the same command again."
      ;;
    137)
      stop "The import was killed (exit 137). Usually this means the server ran out of memory: the import needs about 9 GB (more for a later snapshot)." \
        "Add memory or swap, then run the same command again."
      ;;
    *)
      stop "The import failed (exit $rc). Its output is in $log." "Fix the cause, then run the same command again."
      ;;
  esac
  rc=0
  read_import_record || rc=$?
  if [[ $rc != 0 ]]; then
    stop "The import ended with exit 0, but its record $STATE_DIR/$IMPORT_RECORD cannot be read: $(helper_error)" \
      "Keep $STATE_DIR as it is, and report it (SECURITY.md in the QSDM repository)."
  fi
  problems=$(record_problems)
  if [[ -n $problems ]]; then
    stop "The import ended with exit 0, but its record does not say what the guide expects:" "$problems" \
      "Do not start this node. Keep $STATE_DIR as it is, and report it (SECURITY.md in the QSDM repository)."
  fi
  ok "imported: checkpoint $R_HEIGHT authorized by $R_AUTHORIZED; installed $R_INSTALLED"
  cleanup_snapshot_files
}

step_start() {
  step 7 "Start the node"
  unit_text >"$RUN_TMP/qsdm-follower.service"
  if as_root test -e "$P_UNIT"; then
    if ! as_root cmp -s -- "$RUN_TMP/qsdm-follower.service" "$P_UNIT"; then
      stop "$UNIT_FILE already exists and is not the guide's unit." \
        "The installer does not overwrite it. Move it away, then run the same command again."
    fi
    ok "$UNIT_FILE is already in place"
  else
    if ! as_root test -d "${P_UNIT%/*}"; then
      run_root install -d -m 0755 "${P_UNIT%/*}"
    fi
    run_root install -m 0644 "$RUN_TMP/qsdm-follower.service" "$P_UNIT"
  fi
  run_root systemctl daemon-reload
  if systemctl is-active --quiet "$SERVICE"; then
    ok "the node is already running"
  else
    run_root systemctl enable --now "$SERVICE"
  fi
}

# tip_of API: the chain_tip that API reports.
tip_of() {
  local answer=$RUN_TMP/status.json
  api_json "$1/status" "$answer" 2>/dev/null || return 1
  py tip "$answer"
}

# block_line API HEIGHT: "height hash state_root" of one block, as the guide prints it.
block_line() {
  local answer=$RUN_TMP/block-$2.json
  api_json "$1/chain/blocks?from=$2&to=$2&limit=1" "$answer" 2>/dev/null || return 1
  py block "$answer" "$2" 0
}

# retry_read WHO WHAT CMD...: run CMD until it succeeds and set GOT to what it printed. WHO is "network" (the
# public API) or "node" (this node's API); WHAT names the read in the message. After the first failure it says
# once that it waits, then tries again every RETRY_EVERY seconds for up to RETRY_FOR seconds. Waiting for the
# node ends early when systemd reports the service as failed. Returns 1 when it gives up.
GOT=''
retry_read() {
  local who=$1 what=$2 api="your node's API" give_up=0 said=0
  shift 2
  if [[ $who == network ]]; then
    api="the network's API"
  fi
  while :; do
    if GOT=$("$@"); then
      if ((said == 1)); then
        say "  $api answers again"
      fi
      return 0
    fi
    if [[ $who == node ]] && systemctl is-failed --quiet "$SERVICE"; then
      return 1
    fi
    if ((give_up == 0)); then
      give_up=$(($(date +%s) + RETRY_FOR))
    fi
    if (($(date +%s) >= give_up)); then
      return 1
    fi
    if ((said == 0)); then
      if [[ $who == network ]]; then
        say "  $api did not answer when asked for $what; a producer restart pauses it for a few minutes."
      else
        say "  $api did not answer when asked for $what."
      fi
      if ((RETRY_FOR % 60 == 0)); then
        say "  trying again every $RETRY_EVERY seconds for up to $((RETRY_FOR / 60)) minutes"
      else
        say "  trying again every $RETRY_EVERY seconds for up to $RETRY_FOR seconds"
      fi
      said=1
    fi
    sleep "$RETRY_EVERY"
  done
}

not_ready() {
  printf '\nThe node is installed and started, but it did not answer on %s within %s minutes.\n' "$LOCAL_API" "$WAIT_MINUTES"
  printf 'The first start loads the whole chain into memory, which can take a while. Watch it with:\n'
  printf '  journalctl -u %s -f\n' "$SERVICE"
  printf 'Then run the same command again to finish the check against the network.\n'
  exit 3
}

show_journal() {
  as_root journalctl -u "$SERVICE" -n 30 --no-pager 2>/dev/null | sed 's/^/    /' || true
}

step_check_network() {
  local deadline local_tip='' public_tip behind height mine theirs mismatch=0 waiting=0
  step "$CHECK_STEP" "Check it against the network"
  deadline=$(($(date +%s) + WAIT_MINUTES * 60))
  say "  your node's API: $LOCAL_API (loopback only)"
  until local_tip=$(tip_of "$LOCAL_API"); do
    if systemctl is-failed --quiet "$SERVICE"; then
      show_journal
      stop "The node stopped: systemd reports $SERVICE as failed." \
        "Read its log with: journalctl -u $SERVICE -n 200. Fix the cause, then run the same command again."
    fi
    if (($(date +%s) >= deadline)); then
      not_ready
    fi
    if [[ $waiting == 0 ]]; then
      say "  waiting for the node to answer (the first start loads the whole chain; this can take several minutes)"
      waiting=1
    fi
    sleep 10
  done
  while :; do
    retry_read network "its tip" tip_of "$API_BASE" ||
      stop "Could not read the network's tip from $API_BASE." "Run the same command again later to repeat the check."
    public_tip=$GOT
    behind=$((public_tip - local_tip))
    if ((behind <= 2)) || (($(date +%s) >= deadline)); then
      break
    fi
    say "  catching up: block $local_tip of $public_tip ($behind behind)"
    sleep 30
    retry_read node "its tip" tip_of "$LOCAL_API" ||
      stop "The node stopped answering on $LOCAL_API." "Read its log with: journalctl -u $SERVICE -n 200."
    local_tip=$GOT
  done
  say "  blocks T-100 and T-1 (T = your tip, $local_tip): height, hash, state root"
  for height in $((local_tip - 100)) $((local_tip - 1)); do
    if ((height < 0)); then
      continue
    fi
    retry_read node "block $height" block_line "$LOCAL_API" "$height" ||
      stop "Your node did not return block $height." "Read its log with: journalctl -u $SERVICE -n 200."
    mine=$GOT
    retry_read network "block $height" block_line "$API_BASE" "$height" ||
      stop "The public API did not return block $height." "Run the same command again later to repeat the check."
    theirs=$GOT
    say "    yours    $mine"
    say "    network  $theirs"
    if [[ $mine != "$theirs" ]]; then
      mismatch=1
    fi
  done
  CHECK_LOCAL_TIP=$local_tip CHECK_PUBLIC_TIP=$public_tip
  if [[ $mismatch == 1 ]]; then
    run_root systemctl stop "$SERVICE"
    stop "Your node's blocks do not match the network's (see above). The installer stopped the node, as the guide says." \
      "Keep its state directory as it is. Collect: $OPT_DIR/qsdm --version, GET /api/v1/status from both sides, the height with both hashes and state roots, and the last few hundred log lines (journalctl -u $SERVICE -n 300)." \
      "Report them through the security contact in SECURITY.md (QSDM repository). Your node checks every block itself: if it refuses a block, it may be the one that is right."
  fi
  if ((behind <= 2)); then
    ok "your node is at block $local_tip, the network at $public_tip, and the blocks compared are identical"
  else
    ok "the blocks compared are identical"
    note "your node is still catching up (block $local_tip of $public_tip). It carries on by itself. Run the same command again later to repeat this check."
  fi
}

# ---------------------------------------------------------------------------------------------------
# --update-schedule: a new block maker, for a node this installer set up
# ---------------------------------------------------------------------------------------------------

print_update_plan() {
  local s=''
  if [[ $IS_ROOT == 0 ]]; then
    s='sudo '
  fi
  cat <<EOF

Dry run: this is the plan. Nothing is installed or changed.

[2/3] Keep the node's configuration and put in the new schedule:
        ${s}install -m 0640 -o root -g $SERVICE $ETC_DIR/core.toml $ETC_DIR/core.toml.before-<UTC time>
        ${s}install -m 0640 -o root -g $SERVICE <core.toml with the new schedule> $ETC_DIR/core.toml
        ${s}systemctl restart $SERVICE
      If systemd then reports $SERVICE as failed, the old file goes back and the node is restarted with it.
[3/3] Wait up to $WAIT_MINUTES minutes for the node to catch up, then compare blocks T-100 and T-1
      (T is your node's tip) with $API_BASE. From block $FIRST_NEW on, the new block maker signs.
EOF
  if [[ -n $ROOT ]]; then
    say "Every path above would be under $ROOT (--root, tests only)."
  fi
}

# restore_config BACKUP: put the kept configuration back and restart the node with it.
restore_config() {
  say "  putting the old configuration back:"
  run_root install -m 0640 -o root -g "$SERVICE" "$1" "$P_ETC/core.toml"
  run_root systemctl restart "$SERVICE"
}

update_schedule() {
  local installed=$RUN_TMP/core.toml.installed new=$RUN_TMP/core.toml.new out status port deadline backup
  STEP_TOTAL=3 CHECK_STEP=3
  say "QSDM full-node installer: a new block-maker schedule for this node (RUN_A_FULL_NODE.md, \"When the block maker changes\")"
  step 1 "Compare the new schedule with this node's"
  say "  the new schedule is $SCHEDULE_FROM"
  if ! as_root test -f "$P_ETC/core.toml" || ! as_root test -f "$P_UNIT"; then
    stop "This server has no full node set up by this installer ($ETC_DIR/core.toml or $UNIT_FILE is missing)." \
      "Set one up first: run this installer without --update-schedule."
  fi
  as_root cat -- "$P_ETC/core.toml" >"$installed" || stop "Could not read $ETC_DIR/core.toml."
  out=$(py config "$installed") ||
    stop "$ETC_DIR/core.toml cannot be read: $(helper_error)" "Was it changed by hand? Compare it with the release's fullnode-core.toml."
  port=$(value_of api_port "$out")
  LOCAL_API=http://127.0.0.1:$port/api/v1
  out=$(py schedule-apply "$installed" "$SCHEDULE" "$new") ||
    stop "The new schedule cannot be used for this node, so nothing was changed: $(helper_error)" \
      "Check that it is the schedule on https://qsdm.tech/fullnode.html. If it is, report the difference (SECURITY.md in the QSDM repository)."
  show_schedule "$out"
  status=$(value_of status "$out")
  FIRST_NEW=$(value_of first_new "$out")
  if [[ $status == current ]]; then
    ok "this node already has this schedule: nothing to change"
    if [[ $DRY_RUN == 1 ]] || ! systemctl is-active --quiet "$SERVICE"; then
      return 0
    fi
    STEP_TOTAL=2 CHECK_STEP=2
    step_check_network
    return 0
  fi
  ok "it continues this node's schedule with ${status#added } more block maker(s), the first from block $FIRST_NEW"
  if [[ $DRY_RUN == 1 ]]; then
    print_update_plan
    exit 0
  fi
  check_system
  prepare_sudo
  prepare_work_dir
  step 2 "Put in the new schedule and restart the node"
  INSTALL_STARTED=1
  backup=$P_ETC/core.toml.before-$(date -u +%Y%m%dT%H%M%SZ)
  run_root install -m 0640 -o root -g "$SERVICE" "$P_ETC/core.toml" "$backup"
  run_root install -m 0640 -o root -g "$SERVICE" "$new" "$P_ETC/core.toml"
  ok "the old configuration is kept as ${backup#"$ROOT"}"
  run_root systemctl restart "$SERVICE"
  deadline=$(($(date +%s) + WAIT_MINUTES * 60))
  say "  waiting for the node to answer (a start loads the whole chain; this can take several minutes)"
  until tip_of "$LOCAL_API" >/dev/null; do
    if systemctl is-failed --quiet "$SERVICE"; then
      show_journal
      restore_config "$backup"
      stop "The node did not start with the new schedule: systemd reports $SERVICE as failed. The old configuration is back in place and the node was restarted with it." \
        "Read its log with: journalctl -u $SERVICE -n 200. Check that the schedule is the one on https://qsdm.tech/fullnode.html; if it is, report the log (SECURITY.md in the QSDM repository)."
    fi
    if (($(date +%s) >= deadline)); then
      not_ready
    fi
    sleep 10
  done
  ok "the node answers with the new schedule"
  step_check_network
  if ((CHECK_PUBLIC_TIP >= FIRST_NEW && CHECK_LOCAL_TIP < FIRST_NEW)); then
    note "your node has not reached block $FIRST_NEW, the new block maker's first, yet. It carries on by itself: run the same command again later to repeat the check."
  elif ((CHECK_LOCAL_TIP >= FIRST_NEW)); then
    ok "your node follows the new block maker (block $FIRST_NEW and later)"
  fi
}

show_summary() {
  cat <<EOF

Done: the QSDM full node runs as the $SERVICE service.
  status and log   systemctl status $SERVICE ; journalctl -u $SERVICE -f
  binary           $OPT_DIR/qsdm
  configuration    $ETC_DIR/core.toml and $ETC_DIR/core.env
  chain data       $STATE_DIR (keep it at mode 0700)
  your node's API  $LOCAL_API (loopback only: keep it so, or put an authenticating proxy in front)
On its first start the node made its own consensus key: a local identity that holds no funds. Keep it
private. Run the same command again at any time to repeat the check against the network.
The work folder $WORK_DIR holds the small release files and the logs; you may delete it.
EOF
}

main() {
  parse_args "$@"
  validate_args
  if [[ $(id -u) == 0 ]]; then
    IS_ROOT=1
  fi
  if [[ -t 2 ]]; then
    CURL_BIG+=(--progress-bar)
  else
    CURL_BIG+=(--silent --show-error)
  fi
  trap on_exit EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  check_tools
  RUN_TMP=$(mktemp -d "${TMPDIR:-/tmp}/qsdm-fullnode.XXXXXXXX") || stop "Could not make a temporary folder."
  write_helper
  if [[ $FROM_WEBSITE == 1 ]]; then
    read_website
  fi
  resolve_schedule
  if [[ $UPDATE_SCHEDULE == 1 ]]; then
    derive_names
    if [[ -z $SCHEDULE ]]; then
      say "$WEBSITE_URL shows no block-maker schedule: the block maker has not changed since the release, so this node needs no update."
      exit 0
    fi
    update_schedule
    say ""
    say "Done: the node carries the block-maker schedule $SCHEDULE_FROM."
    exit 0
  fi
  require_values
  derive_names
  show_values
  if [[ $REQUIRE_SIGNATURE == 1 && -z $QSDMCLI ]]; then
    stop "--require-signature was given, but qsdmcli was not found." \
      "Install qsdmcli (it comes with QSDM Hive, or build it from source) and pass --qsdmcli PATH."
  fi
  if [[ $DRY_RUN == 1 ]]; then
    print_plan
    exit 0
  fi
  check_system
  prepare_sudo
  prepare_work_dir
  detect_import_state
  check_resources
  step_download
  step_verify_release
  step_checkpoint
  step_snapshot
  step_install
  step_import
  step_start
  step_check_network
  show_summary
}

main "$@"
