#!/usr/bin/env bash
# bring-up-validator.sh — Stand up an additional QSDM validator on a Linux host.
#
# This script is the companion of install-ubuntu-vps.sh (which stands up the
# *first* validator). It assumes your first validator is already running and
# that you have its libp2p bootstrap multiaddr handy.
#
# What it does (idempotent; safe to re-run):
#   1. Pre-flight: OS + user + disk + RAM checks.
#   2. Install build deps (apt, no-op if already present).
#   3. Build liboqs once under $QSDM_HOME/liboqs_install, or reuse the cache.
#   4. Build the qsdm-validator binary (CGO_ENABLED=1).
#   5. Materialize a per-validator install dir under
#      /opt/qsdm-validator-<INDEX> with its own TOML config, systemd user,
#      data dir, and port offsets derived from --index.
#   6. Render + enable a systemd unit named qsdm-validator-<INDEX>.service
#      that points at the per-validator config.
#   7. Open the firewall for the libp2p port only (NOT the API/dashboard --
#      those should stay behind Caddy / the VPN).
#   8. Wait up to $HEALTH_TIMEOUT_SEC seconds for /api/v1/health/ready and
#      for the new node to list at least one peer.
#
# Usage:
#   sudo bash bring-up-validator.sh \
#       --index 2 \
#       --bootstrap /ip4/203.0.113.10/tcp/4001/p2p/12D3KooW...
#
# Flags:
#   --index N             Second/third/... validator on this host. N>=2.
#                         Derives port offsets: libp2p=4000+N, api=8080+(N-1)*10,
#                         dashboard=8081+(N-1)*10. Default: 2.
#   --bootstrap ADDR      libp2p multiaddr of the primary (or any existing)
#                         validator to bootstrap from. REQUIRED.
#                         Example: /ip4/1.2.3.4/tcp/4001/p2p/12D3KooWabc...
#   --install-dir DIR     Where to place the binary + config + db.
#                         Default: /opt/qsdm-validator-<INDEX>
#   --service-name NAME   systemd unit name (without .service).
#                         Default: qsdm-validator-<INDEX>
#   --qsdm-home DIR       Source checkout to build from.
#                         Default: $HOME/QSDM (git-cloned if missing)
#   --skip-liboqs         Re-use an existing $QSDM_HOME/liboqs_install.
#   --skip-build          Re-use an existing $QSDM_HOME/qsdmplus binary.
#   --skip-firewall       Do not touch ufw.
#   --health-timeout SEC  How long to wait for ready + peer-count>=1.
#                         Default: 180
#   --dry-run             Print what would happen; do not mutate the system.
#   -h, --help            Show this help and exit.
#
# Safety:
#   * This script never touches your *first* validator's config/data dir.
#   * /var/lib/qsdm-validator-<N> and /opt/qsdm-validator-<N> are scoped per
#     index -- wiping one validator does not affect any other.
#   * DO NOT copy a validator's data dir from another host. Two validators
#     with the same ML-DSA-87 identity will both be slashed once slashing is
#     enabled. Each run generates a fresh key on first boot.

set -euo pipefail

# --- colors / log helpers ---------------------------------------------------
if [[ -t 1 ]]; then
    C_RED=$'\033[0;31m'; C_YEL=$'\033[1;33m'; C_GRN=$'\033[0;32m'; C_NC=$'\033[0m'
else
    C_RED=""; C_YEL=""; C_GRN=""; C_NC=""
fi
log()  { printf "%s==>%s %s\n"  "$C_GRN" "$C_NC" "$*"; }
warn() { printf "%s!! %s%s\n"   "$C_YEL" "$*" "$C_NC" >&2; }
die()  { printf "%sxx %s%s\n"   "$C_RED" "$*" "$C_NC" >&2; exit 1; }

run() {
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        printf "  [dry-run] %s\n" "$*"
    else
        "$@"
    fi
}

# --- defaults ---------------------------------------------------------------
INDEX=2
BOOTSTRAP=""
INSTALL_DIR=""
SERVICE_NAME=""
QSDM_HOME="${QSDM_HOME:-${HOME:-/root}/QSDM}"
QSDM_GIT="${QSDM_GIT:-https://github.com/blackbeardONE/QSDM.git}"
SKIP_LIBOQS=0
SKIP_BUILD=0
SKIP_FIREWALL=0
HEALTH_TIMEOUT_SEC=180
DRY_RUN=0

# --- arg parsing ------------------------------------------------------------
while [[ $# -gt 0 ]]; do
    case "$1" in
        --index)            INDEX="$2"; shift 2 ;;
        --bootstrap)        BOOTSTRAP="$2"; shift 2 ;;
        --install-dir)      INSTALL_DIR="$2"; shift 2 ;;
        --service-name)     SERVICE_NAME="$2"; shift 2 ;;
        --qsdm-home)        QSDM_HOME="$2"; shift 2 ;;
        --skip-liboqs)      SKIP_LIBOQS=1; shift ;;
        --skip-build)       SKIP_BUILD=1; shift ;;
        --skip-firewall)    SKIP_FIREWALL=1; shift ;;
        --health-timeout)   HEALTH_TIMEOUT_SEC="$2"; shift 2 ;;
        --dry-run)          DRY_RUN=1; shift ;;
        -h|--help)
            # Print everything between the shebang and the first blank line
            # after "Safety:" -- i.e. the leading help block above.
            sed -n '2,/^set -euo pipefail/{/^set -euo pipefail/d;s/^# \{0,1\}//;p}' "$0"
            exit 0
            ;;
        *) die "Unknown flag: $1 (try --help)" ;;
    esac
done

# --- pre-flight -------------------------------------------------------------
if [[ "$(id -u)" -ne 0 ]]; then
    if [[ "$DRY_RUN" == "1" ]]; then
        warn "Not running as root, but --dry-run was requested -- continuing without actually writing anything."
    else
        die "Run as root (e.g. sudo bash $0 ...)"
    fi
fi
[[ "$INDEX" =~ ^[0-9]+$ ]] || die "--index must be a positive integer"
[[ "$INDEX" -ge 2 ]] || die "--index must be >= 2 (index 1 is install-ubuntu-vps.sh)"
[[ -n "$BOOTSTRAP" ]] || die "--bootstrap is required (see --help for format)"
case "$BOOTSTRAP" in
    /ip4/*|/ip6/*|/dns*/*) ;;
    *) die "--bootstrap does not look like a libp2p multiaddr (got: $BOOTSTRAP)" ;;
esac

INSTALL_DIR="${INSTALL_DIR:-/opt/qsdm-validator-${INDEX}}"
SERVICE_NAME="${SERVICE_NAME:-qsdm-validator-${INDEX}}"
SERVICE_USER="qsdm-validator-${INDEX}"
DATA_DIR="/var/lib/qsdm-validator-${INDEX}"
P2P_PORT=$((4000 + INDEX))
API_PORT=$((8080 + (INDEX - 1) * 10))
DASHBOARD_PORT=$((8081 + (INDEX - 1) * 10))

log "Config plan:"
printf "    index            = %s\n"  "$INDEX"
printf "    install_dir      = %s\n"  "$INSTALL_DIR"
printf "    data_dir         = %s\n"  "$DATA_DIR"
printf "    service          = %s.service (user: %s)\n" "$SERVICE_NAME" "$SERVICE_USER"
printf "    libp2p port      = %s/tcp\n" "$P2P_PORT"
printf "    api port         = %s/tcp (loopback only)\n" "$API_PORT"
printf "    dashboard port   = %s/tcp (loopback only)\n" "$DASHBOARD_PORT"
printf "    bootstrap        = %s\n" "$BOOTSTRAP"
printf "    qsdm_home        = %s\n" "$QSDM_HOME"
printf "    dry_run          = %s\n" "$DRY_RUN"

# Guard against running against the index-1 primary by mistake.
if [[ "$INSTALL_DIR" == "/opt/qsdmplus" ]]; then
    die "Refusing to overwrite the primary validator at /opt/qsdmplus"
fi
if systemctl is-active --quiet qsdmplus 2>/dev/null; then
    log "Primary validator (qsdmplus.service) is active -- proceeding as a second node."
fi

# Check the requested ports are not in use by another process.
for p in "$P2P_PORT" "$API_PORT" "$DASHBOARD_PORT"; do
    if ss -ltn "sport = :$p" 2>/dev/null | grep -q LISTEN; then
        die "Port $p is already in LISTEN state -- either another validator index is using it, or --index $INDEX collides with something else."
    fi
done

# --- 1. build deps ----------------------------------------------------------
log "Installing build dependencies (apt)..."
export DEBIAN_FRONTEND=noninteractive
run apt-get update -y
run apt-get install -y --no-install-recommends \
    build-essential cmake git curl wget ufw jq ca-certificates pkg-config \
    libssl-dev libsqlite3-dev

# --- 2. Go toolchain --------------------------------------------------------
GO_TGZ="${GO_TGZ:-https://go.dev/dl/go1.23.4.linux-amd64.tar.gz}"
if [[ ! -x /usr/local/go/bin/go ]]; then
    log "Installing Go from $GO_TGZ"
    TMP_TGZ="$(mktemp)"
    run wget -qO "$TMP_TGZ" "$GO_TGZ"
    run rm -rf /usr/local/go
    run tar -C /usr/local -xzf "$TMP_TGZ"
    run rm -f "$TMP_TGZ"
fi
export PATH="/usr/local/go/bin:${PATH:-}"
log "Go: $(go version 2>/dev/null || echo '(dry-run)')"

# --- 3. clone / pull source -------------------------------------------------
if [[ -f "$QSDM_HOME/scripts/rebuild_liboqs.sh" && -d "$QSDM_HOME/source" ]]; then
    log "Using existing QSDM tree at $QSDM_HOME"
elif [[ ! -d "$QSDM_HOME/.git" ]]; then
    log "Cloning QSDM from $QSDM_GIT into $QSDM_HOME"
    run git clone --depth 1 "$QSDM_GIT" "$QSDM_HOME"
else
    log "Updating existing clone at $QSDM_HOME"
    run git -C "$QSDM_HOME" pull --ff-only || warn "git pull --ff-only failed; continuing with current HEAD"
fi
if [[ -d "$QSDM_HOME" ]]; then
    cd "$QSDM_HOME"
    run chmod +x scripts/rebuild_liboqs.sh scripts/build.sh 2>/dev/null || true
elif [[ "$DRY_RUN" == "1" ]]; then
    warn "[dry-run] would cd to $QSDM_HOME (does not exist yet -- skipping subsequent fs-relative steps)"
    log "Dry-run complete (stopped before liboqs / build because source tree is absent)."
    exit 0
else
    die "$QSDM_HOME does not exist after clone/pull -- this should not happen"
fi

# --- 4. liboqs --------------------------------------------------------------
if [[ "$SKIP_LIBOQS" == "1" && -d "$QSDM_HOME/liboqs_install" ]]; then
    log "Skipping liboqs build (cached at $QSDM_HOME/liboqs_install)"
elif [[ -d "$QSDM_HOME/liboqs_install" ]]; then
    log "Reusing existing liboqs_install ($QSDM_HOME/liboqs_install)"
else
    log "Building liboqs (this can take 10-30 minutes on a small VPS)..."
    run ./scripts/rebuild_liboqs.sh
fi

# --- 5. binary --------------------------------------------------------------
if [[ "$SKIP_BUILD" == "1" && -x "$QSDM_HOME/qsdmplus" ]]; then
    log "Skipping Go build (reusing $QSDM_HOME/qsdmplus)"
else
    log "Building qsdm-validator binary..."
    run ./scripts/build.sh
    [[ "$DRY_RUN" == "1" ]] || test -x "./qsdmplus" || die "Build produced no binary at $QSDM_HOME/qsdmplus"
fi

# --- 6. install dir + user --------------------------------------------------
log "Provisioning service user + dirs..."
if ! getent passwd "$SERVICE_USER" >/dev/null 2>&1; then
    run useradd -r -s /usr/sbin/nologin -d "$INSTALL_DIR" "$SERVICE_USER"
fi
run mkdir -p "$INSTALL_DIR" "$DATA_DIR"
run chown -R "$SERVICE_USER:$SERVICE_USER" "$INSTALL_DIR" "$DATA_DIR"

run install -m 0755 "$QSDM_HOME/qsdmplus" "$INSTALL_DIR/qsdm-validator"
if [[ -d "$QSDM_HOME/liboqs_install" ]]; then
    run cp -a "$QSDM_HOME/liboqs_install" "$INSTALL_DIR/"
    run chown -R "$SERVICE_USER:$SERVICE_USER" "$INSTALL_DIR/liboqs_install"
fi

# --- 7. config --------------------------------------------------------------
CONFIG_FILE="$INSTALL_DIR/config.toml"
log "Writing $CONFIG_FILE"
if [[ "$DRY_RUN" != "1" ]]; then
    cat > "$CONFIG_FILE" <<EOF
# QSDM validator config -- generated by bring-up-validator.sh (index=${INDEX}).
# Do not hand-edit without also updating the bring-up script -- re-running
# the script will REWRITE this file.

[node]
role            = "validator"
mining_enabled  = false

[network]
port            = ${P2P_PORT}
bootstrap_peers = [
    "${BOOTSTRAP}",
]

[storage]
type        = "sqlite"
sqlite_path = "${DATA_DIR}/qsdm.db"

[monitoring]
dashboard_port = ${DASHBOARD_PORT}
log_viewer_port = 0
log_file       = "${DATA_DIR}/qsdm.log"
log_level      = "INFO"

[api]
port        = ${API_PORT}
enable_tls  = false
tls_cert_file = ""
tls_key_file  = ""

[performance]
transaction_interval = "1h"
health_check_interval = "30s"
EOF
    chown "$SERVICE_USER:$SERVICE_USER" "$CONFIG_FILE"
    chmod 0640 "$CONFIG_FILE"
fi

# --- 8. systemd unit --------------------------------------------------------
UNIT_PATH="/etc/systemd/system/${SERVICE_NAME}.service"
log "Writing $UNIT_PATH"
if [[ "$DRY_RUN" != "1" ]]; then
    cat > "$UNIT_PATH" <<EOF
[Unit]
Description=QSDM validator (index ${INDEX})
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${SERVICE_USER}
Group=${SERVICE_USER}
WorkingDirectory=${INSTALL_DIR}
ExecStart=${INSTALL_DIR}/qsdm-validator
Restart=always
RestartSec=10

Environment="CGO_ENABLED=1"
Environment="LD_LIBRARY_PATH=${INSTALL_DIR}/liboqs_install/lib:${INSTALL_DIR}/liboqs_install/lib64:/usr/local/lib64:/usr/local/lib"
Environment="CONFIG_FILE=${CONFIG_FILE}"
# Dual-name for the rebrand window; pick whichever the binary prefers.
Environment="QSDM_CONFIG_FILE=${CONFIG_FILE}"

NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=${INSTALL_DIR} ${DATA_DIR}

LimitNOFILE=65536
LimitNPROC=4096

StandardOutput=journal
StandardError=journal
SyslogIdentifier=${SERVICE_NAME}

[Install]
WantedBy=multi-user.target
EOF
fi

run systemctl daemon-reload
run systemctl enable "${SERVICE_NAME}"
run systemctl restart "${SERVICE_NAME}"

# --- 9. firewall ------------------------------------------------------------
if [[ "$SKIP_FIREWALL" == "1" ]]; then
    log "Skipping ufw (per --skip-firewall)"
elif command -v ufw >/dev/null 2>&1; then
    log "Opening libp2p port ${P2P_PORT}/tcp in ufw (API/dashboard stay on loopback)"
    run ufw allow "${P2P_PORT}/tcp" || warn "ufw allow failed; continuing"
fi

# --- 10. health wait --------------------------------------------------------
if [[ "$DRY_RUN" == "1" ]]; then
    log "Dry-run complete."
    exit 0
fi

log "Waiting up to ${HEALTH_TIMEOUT_SEC}s for the validator to reach READY + peer-count>=1..."
deadline=$(( $(date +%s) + HEALTH_TIMEOUT_SEC ))
ready=0
peered=0
while [[ "$(date +%s)" -lt "$deadline" ]]; do
    if [[ "$ready" -eq 0 ]]; then
        code="$(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:${API_PORT}/api/v1/health/ready" || true)"
        if [[ "$code" == "200" ]]; then
            log "READY (/api/v1/health/ready -> 200)"
            ready=1
        fi
    fi
    if [[ "$ready" -eq 1 && "$peered" -eq 0 ]]; then
        # The status endpoint exposes peer counts; fall back to 'status' if
        # the specific peer field is not present.
        peers="$(curl -sS "http://127.0.0.1:${API_PORT}/api/v1/status" 2>/dev/null \
                 | jq -r '.network.peer_count // .peer_count // 0' 2>/dev/null || echo 0)"
        if [[ "$peers" =~ ^[0-9]+$ && "$peers" -ge 1 ]]; then
            log "Connected to ${peers} peer(s) via ${BOOTSTRAP}"
            peered=1
            break
        fi
    fi
    sleep 3
done

if [[ "$ready" -ne 1 ]]; then
    warn "Validator did not report READY within ${HEALTH_TIMEOUT_SEC}s -- check 'journalctl -u ${SERVICE_NAME} -n 100'"
    systemctl --no-pager -l status "${SERVICE_NAME}" || true
    exit 2
fi
if [[ "$peered" -ne 1 ]]; then
    warn "Validator is READY but has 0 peers. Common causes:"
    warn "  * Firewall on the bootstrap host is blocking ${BOOTSTRAP} (check its ufw/sec-group)."
    warn "  * Bootstrap multiaddr is stale (peer-id changes on key rotation)."
    warn "  * Primary validator is not actually listening on the tcp port in the multiaddr."
    exit 3
fi

# --- 11. done ---------------------------------------------------------------
log "Validator ${INDEX} is up and peered."
cat <<EOF

${C_GRN}Summary${C_NC}
  service:        systemctl status ${SERVICE_NAME}
  journal:        journalctl -u ${SERVICE_NAME} -f
  config:         ${CONFIG_FILE}
  data dir:       ${DATA_DIR}
  libp2p:         tcp ${P2P_PORT} (open on firewall)
  API:            http://127.0.0.1:${API_PORT}          (loopback; front with Caddy for TLS)
  Dashboard:      http://127.0.0.1:${DASHBOARD_PORT}    (loopback; keep private)
  bootstrap:      ${BOOTSTRAP}

Next steps:
  * Front the API with Caddy / nginx (see docs/docs/VALIDATOR_QUICKSTART.md §6).
  * Back up ${DATA_DIR} *before* exposing this node to the public network.
    The ML-DSA-87 signing key lives there and is unique to this validator.
  * (Optional) Enable NGC attestation -- see apps/qsdmplus-nvidia-ngc/QUICKSTART.md.

EOF
