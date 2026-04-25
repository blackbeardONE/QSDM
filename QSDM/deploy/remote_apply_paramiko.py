# Apply local source + config to VPS (Major Update edition).
#
# Usage:
#   $env:QSDM_VPS_PASS="..."; python QSDM/deploy/remote_apply_paramiko.py
#
# What changed vs the pre-Major-Update version of this script:
#
#   The previous revision sftp'd four specific files (main.go, libp2p.go,
#   go.mod, go.sum) and relied on the rest of /root/QSDM already being in
#   sync. That was fine for single-file hotfixes, but the Major Update
#   rebrand touches pkg/api (new trust_peer_provider.go), pkg/config (new
#   [trust] section), pkg/audit (new checklist items), pkg/monitoring
#   (prefix dual-emit), deploy/landing/ (live trust widget), and many
#   other paths. Shipping only the old four files would leave the server
#   in a half-upgraded state and the wired TrustAggregator would never
#   compile against the missing pkg/api/trust_peer_provider.go.
#
# What this script now does:
#
#   1. Packs a tarball of the local QSDM tree (same exclude list as
#      remote_bootstrap_paramiko.py) so every file touched by the
#      Major Update is mirrored to the server, including new files,
#      deletions, and renames.
#   2. Uploads the tarball to /root/qsdm-local.tar.gz and extracts it
#      over /root/QSDM with --overwrite (no rm -rf; preserves server-only
#      files such as liboqs build caches, backup databases, and any
#      locally-edited qsdm.toml).
#   3. Rebuilds, installs, and restarts the validator systemd unit.
#      Uses the validator_only build tag because the production VPS is
#      a CPU-only validator per the Major Update two-tier node model.
#   4. Ensures the server's /opt/qsdm/qsdm.toml has a [trust]
#      section (non-destructively appended if missing) so the newly
#      wired transparency endpoints publish on first restart.
#   5. Reloads the Caddyfile if present so the trust.html page + new
#      /api/v1/trust/attestations/* routes are reachable via the public
#      hostname without a manual step.
#   6. Smoke-probes the new trust endpoints locally on the server and
#      prints the HTTP codes + small JSON excerpt, so the operator does
#      not need to run remote_verify_paramiko.py separately just to
#      confirm the trust surface is live.
#
# Rollback plan: the previous binary is copied to
# /opt/qsdm/qsdm.prev before `install` overwrites it. If the new
# build refuses to start (systemctl is-active != active), the script
# restores the previous binary and re-enables the unit before exiting
# non-zero.
import os
import socket
import sys
import tarfile
import tempfile
from pathlib import Path

import paramiko
from paramiko import Transport

from _deploy_host import host as _host, user as _user

HOST = _host()
USER = _user()
BASE = Path(__file__).resolve().parent.parent  # -> QSDM/

EXCLUDE_DIR_NAMES = frozenset(
    {
        ".git",
        "liboqs_build",
        "liboqs_install",
        "__pycache__",
        ".pytest_cache",
        "node_modules",
        "target",
        ".cursor",
        ".vscode",
        ".idea",
        "testdata",  # kept on server from bootstrap; avoid huge re-uploads
    }
)

# Binary file types excluded: server builds from source, so there is no
# reason to push pre-built artefacts, and they bloat the tarball.
EXCLUDE_SUFFIXES = (".exe", ".db", ".pdb", ".zip", ".tar.gz", ".whl")


REBUILD = r"""
set -e
export PATH="/usr/local/go/bin:$PATH"
export CGO_ENABLED=1
cd /root/QSDM

echo '===[ git-describe / tree hash (local snapshot) ]==='
find . -path ./.git -prune -o -type f -printf '%s %P\n' 2>/dev/null | md5sum | awk '{print "tree-md5 "$1}' || true

echo '===[ go build (production profile, CGO+liboqs) ]==='
# The production VPS runs the full profile with CGO=1 + liboqs for
# quantum-safe signatures. We stay on that profile here to minimise the
# blast radius of the redeploy — the Major Update does not require a
# profile switch, and flipping to validator_only introduces a startup
# check on [node] role / mining_enabled that we do not want to surface
# in the same window as the rebrand.
#
# Operators who want to cut over to the Major Update §4 validator_only
# profile can re-run this script with QSDM_BUILD_TAGS=validator_only in
# the environment; that tag drops the miner code paths (cmd/qsdmminer,
# GPU-bound PoW hooks) from the compiled binary.
if [ -n "${QSDM_BUILD_TAGS:-}" ]; then
  echo "  custom build tags: $QSDM_BUILD_TAGS"
  cd source
  go build -trimpath -ldflags="-s -w" -tags "$QSDM_BUILD_TAGS" -o ../qsdm ./cmd/qsdm
  cd -
else
  ./scripts/build.sh
fi

echo '===[ install + systemd restart ]==='
systemctl stop qsdm || true
if [ -f /opt/qsdm/qsdm ]; then
  install -m0755 -o qsdm -g qsdm /opt/qsdm/qsdm /opt/qsdm/qsdm.prev || true
fi
install -m0755 -o qsdm -g qsdm ./qsdm /opt/qsdm/qsdm
install -m0644 -o qsdm -g qsdm ./config/qsdm.service /etc/systemd/system/qsdm.service

echo '===[ non-destructive [trust] block patch on server config ]==='
# The wired TrustAggregator serves /api/v1/trust/attestations/* on first
# start even without a [trust] block (defaults: enabled, 15m freshness,
# 10s refresh). We still append an explicit block when missing so the
# operator can edit it in place later rather than hunting the env path.
CFG=/opt/qsdm/qsdm.toml
if [ -f "$CFG" ] && ! grep -Eq '^\[trust\]' "$CFG"; then
  cat >> "$CFG" <<'TRUST'

# Attestation transparency (Major Update §8.5). Transparency signal only;
# not a consensus rule. See docs/docs/NVIDIA_LOCK_CONSENSUS_SCOPE.md.
[trust]
disabled = false
fresh_within = "15m"
refresh_interval = "10s"
region_hint = ""
TRUST
  echo '  appended [trust] defaults to '"$CFG"
fi

# Legacy knob: slow the demo tx generator so logs are not spammed.
if [ -f "$CFG" ]; then
  if grep -Eq '^\s*transaction_interval\s*=' "$CFG"; then
    sed -i 's|^\s*transaction_interval\s*=.*|transaction_interval = "1h"|' "$CFG"
  fi
fi

systemctl daemon-reload
systemctl start qsdm

# Backup cron (idempotent).
(crontab -l 2>/dev/null | grep -v vps-sqlite-backup || true; \
 echo "0 3 * * * /opt/qsdm/vps-sqlite-backup.sh") | crontab -

sleep 3

echo '===[ systemctl is-active ]==='
if ! systemctl is-active --quiet qsdm; then
  echo 'FAIL: qsdm did not come up; rolling back to previous binary'
  if [ -f /opt/qsdm/qsdm.prev ]; then
    install -m0755 -o qsdm -g qsdm /opt/qsdm/qsdm.prev /opt/qsdm/qsdm
    systemctl restart qsdm || true
  fi
  systemctl status qsdm --no-pager -l | sed -n '1,40p'
  journalctl -u qsdm -n 50 --no-pager
  exit 1
fi
echo 'active'

echo '===[ landing sync (deploy/landing/ -> /var/www/qsdm/) ]==='
# Caddy serves the corporate site from /var/www/qsdm (see Caddyfile).
# The tarball extract puts the latest HTML under /root/QSDM/deploy/landing/,
# but Caddy does not read from there. Mirror the files over with
# install -m0644 (preserves mode, owner = root) so updates to
# landing/index.html + landing/trust.html appear on qsdm.tech without a
# separate scp step. Idempotent: re-running copies the same bytes.
if [ -d /root/QSDM/deploy/landing ] && [ -d /var/www/qsdm ]; then
  for src in /root/QSDM/deploy/landing/*.html /root/QSDM/deploy/landing/*.css /root/QSDM/deploy/landing/*.js /root/QSDM/deploy/landing/*.svg; do
    [ -f "$src" ] || continue
    base=$(basename "$src")
    install -m0644 -o root -g root "$src" "/var/www/qsdm/$base"
    echo "  synced $base"
  done
else
  echo "  (skipped: /var/www/qsdm or /root/QSDM/deploy/landing missing)"
fi

echo '===[ Caddy reload (if installed) ]==='
if systemctl list-unit-files | grep -q '^caddy\.service'; then
  # Caddy's "reload" returns non-zero on this host whenever its admin
  # API is disabled ("admin off" in Caddyfile) even though the config
  # update itself succeeds, because systemd interprets the
  # reload-via-exec-ReloadCmd result as failed. We suppress stderr/stdout
  # from reload specifically, fall back to a full restart, and only
  # surface the final is-active state. This keeps the deploy log quiet
  # when everything is healthy, and still shows a red is-active=failed
  # signal if the service is genuinely down.
  systemctl reload caddy >/dev/null 2>&1 || systemctl restart caddy >/dev/null 2>&1 || true
  caddy_state=$(systemctl is-active caddy 2>&1 || true)
  echo "  caddy: $caddy_state"
fi

echo '===[ listening sockets ]==='
ss -tlnp | head -n 20 || true

echo '===[ local HTTP probes ]==='
# Port map for this server (see Caddyfile):
#   :8080  -> log viewer (Basic-Auth gated; NOT the JSON API)
#   :8081  -> dashboard
#   :8443  -> JSON API (HTTP; Caddy terminates TLS on :443 and proxies here)
# Health + trust endpoints are on :8443 and are public by design (§8.5).
#
# TrustAggregator warm-up: the /api/v1/trust/attestations/* routes answer
# 503 {"error":"trust aggregator warming up"} until the first refresh tick
# fires (~10s cadence, configurable in [trust] refresh_interval). The
# earlier probe invocation landed inside that window and reported 503 in
# the deploy log even though the redeploy was healthy. Wait out the tick
# once here; real requests through Caddy get the right answer without a
# synthetic sleep because the edge only sees traffic after DNS propagates
# + user clicks.
sleep 15
for url in \
  http://127.0.0.1:8443/api/v1/health/live \
  http://127.0.0.1:8443/api/v1/health/ready \
  http://127.0.0.1:8443/api/v1/trust/attestations/summary \
  "http://127.0.0.1:8443/api/v1/trust/attestations/recent?limit=5" \
  http://127.0.0.1:8081/ ; do
  code=$(curl -sS -o /tmp/qsdm_probe.out -m 5 -w '%{http_code}' "$url" || echo 000)
  echo "  $url -> $code"
  case "$url" in
    *trust/attestations*) head -c 400 /tmp/qsdm_probe.out ; echo ;;
  esac
done

echo '===[ Caddy edge probes (loopback via public virtual hosts) ]==='
# Hit Caddy through the public hostnames to confirm the edge is routing
# /api/v1/trust/attestations/* correctly. We resolve via --resolve so the
# probe doesn't depend on external DNS being reachable from the VPS.
for host in api.qsdm.tech qsdm.tech ; do
  code=$(curl -sSk -o /tmp/qsdm_edge.out -m 5 -w '%{http_code}' \
    --resolve "$host:443:127.0.0.1" \
    "https://$host/api/v1/trust/attestations/summary" || echo 000)
  echo "  https://$host/api/v1/trust/attestations/summary -> $code"
  case "$code" in
    200) head -c 200 /tmp/qsdm_edge.out ; echo ;;
  esac
done

echo '===[ recent log ]==='
journalctl -u qsdm -n 25 --no-pager
"""


def should_skip(p: Path, root: Path) -> bool:
    try:
        rel = p.relative_to(root)
    except ValueError:
        return True
    for part in rel.parts:
        if part in EXCLUDE_DIR_NAMES:
            return True
    if p.suffix.lower() in EXCLUDE_SUFFIXES:
        return True
    return False


def make_tarball(root: Path) -> Path:
    """Pack everything under QSDM/ into a gzipped tarball.

    We use a tarball rather than per-file sftp.put because Paramiko's
    SFTP throughput on Windows is painfully slow (~1 MiB/s with default
    buffers) and the Major Update pushes several thousand files.
    """
    files = [p for p in root.rglob("*") if p.is_file() and not should_skip(p, root)]
    tmp = tempfile.NamedTemporaryFile(delete=False, suffix=".tar.gz")
    tmp.close()
    out = Path(tmp.name)
    print(f"--- Packing {len(files)} files from {root} ---", flush=True)
    with tarfile.open(out, "w:gz") as tf:
        for i, p in enumerate(files, 1):
            if i % 500 == 0:
                print(f"    ... {i}/{len(files)}", flush=True)
            arc = p.relative_to(root).as_posix()
            tf.add(p, arcname=arc, recursive=False)
    kib = out.stat().st_size // 1024
    print(f"--- Tarball: {kib} KiB ({len(files)} files) ---", flush=True)
    return out


def _connect() -> Transport:
    """Open an authenticated Transport to the VPS.

    Prefers the local ~/.ssh/id_ed25519 key (deployed to the VPS by
    remote_bootstrap_paramiko.py during the first provision). Falls back
    to password auth if the key is missing or rejected. This mirrors
    the ordinary interactive workflow — operators don't want to re-enter
    a password on every redeploy just because the pubkey path was not
    obvious to the script.
    """
    pw = os.environ.get("QSDM_VPS_PASS") or (sys.argv[1] if len(sys.argv) > 1 else "")
    key_path = Path(os.environ.get("USERPROFILE", os.environ.get("HOME", ""))) / ".ssh" / "id_ed25519"

    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.settimeout(30)
    sock.connect((HOST, 22))
    t = Transport(sock)
    t.start_client(timeout=30)

    authed = False
    if key_path.is_file():
        try:
            pk = paramiko.Ed25519Key.from_private_key_file(str(key_path))
            t.auth_publickey(USER, pk)
            authed = True
            print(f"--- Authenticated with ed25519 key: {key_path} ---", flush=True)
        except paramiko.PasswordRequiredException:
            print(f"--- {key_path} is passphrase-protected; trying password auth ---", flush=True)
        except paramiko.AuthenticationException as e:
            print(f"--- key auth rejected ({e}); trying password auth ---", flush=True)

    if not authed:
        if not pw:
            print(
                "Key auth unavailable and QSDM_VPS_PASS not set; cannot authenticate.",
                file=sys.stderr,
            )
            sys.exit(1)
        t.auth_password(USER, pw)
        print("--- Authenticated with password ---", flush=True)

    t.set_keepalive(30)
    return t


def main() -> int:
    t = _connect()

    tball = make_tarball(BASE)
    try:
        sftp = paramiko.SFTPClient.from_transport(t)
        try:
            print("--- Uploading tarball ---", flush=True)
            sftp.put(str(tball), "/root/qsdm-local.tar.gz")
            # Systemd unit and the one-shot backup script are outside
            # the QSDM/ tree in the server layout (they live in /etc and
            # /opt respectively), so sftp them explicitly.
            raw_backup = (BASE / "deploy/vps-sqlite-backup.sh").read_bytes().replace(b"\r\n", b"\n").replace(b"\r", b"\n")
            with sftp.open("/opt/qsdm/vps-sqlite-backup.sh", "wb") as f:
                f.write(raw_backup)
        finally:
            sftp.close()
    finally:
        try:
            tball.unlink(missing_ok=True)  # type: ignore[call-arg]
        except OSError:
            pass

    # Extract the tarball over /root/QSDM. --keep-old-files=no means we
    # overwrite server copies; --no-same-owner avoids propagating local
    # user ids that don't exist on the VPS.
    ch_extract = t.open_session()
    ch_extract.exec_command(
        "bash -lc 'set -e; mkdir -p /root/QSDM; "
        "tar -xzf /root/qsdm-local.tar.gz -C /root/QSDM --no-same-owner --overwrite; "
        # Restore exec bit on all *.sh + python deploy scripts: tar
        # preserves modes from the local filesystem, but Windows does
        # not carry Unix executable bits so every .sh lands as 0644 on
        # the server. Do this once, here, rather than scattering
        # chmod calls throughout the REBUILD block.
        "find /root/QSDM -type f \\( -name \"*.sh\" -o -name \"*.py\" \\) -exec chmod +x {} +; "
        "chmod +x /opt/qsdm/vps-sqlite-backup.sh; "
        "echo extraction-ok'"
    )
    ext_out = b""
    while True:
        if ch_extract.recv_ready():
            ext_out += ch_extract.recv(65536)
        if ch_extract.recv_stderr_ready():
            ext_out += ch_extract.recv_stderr(65536)
        if ch_extract.exit_status_ready():
            break
    sys.stdout.buffer.write(ext_out)
    sys.stdout.buffer.flush()
    if ch_extract.recv_exit_status() != 0:
        t.close()
        return 2

    print("--- Rebuilding on server (may take several minutes) ---", flush=True)
    ch = t.open_session()
    ch.exec_command("bash -s")
    ch.sendall(REBUILD.encode())
    ch.shutdown_write()
    while True:
        if ch.recv_ready():
            chunk = ch.recv(65536)
            sys.stdout.buffer.write(chunk)
            sys.stdout.buffer.flush()
        if ch.recv_stderr_ready():
            chunk = ch.recv_stderr(65536)
            sys.stderr.buffer.write(chunk)
            sys.stderr.buffer.flush()
        if ch.exit_status_ready():
            break
    while ch.recv_ready():
        sys.stdout.buffer.write(ch.recv(65536))
    while ch.recv_stderr_ready():
        sys.stderr.buffer.write(ch.recv_stderr(65536))
    sys.stdout.buffer.flush()
    sys.stderr.buffer.flush()
    st = ch.recv_exit_status()
    t.close()
    return st


if __name__ == "__main__":
    raise SystemExit(main())
