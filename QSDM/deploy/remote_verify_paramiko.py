"""Verify QSDM deployment on VPS.

Usage:
    QSDM_VPS_PASS=... python remote_verify_paramiko.py

Optionally set QSDM_VPS_HOST to target a different node than the
reference validator (defaults to 206.189.132.232 / api.qsdm.tech).
"""
import os
import socket
import sys
import paramiko
from paramiko import Transport

from _deploy_host import host as _host

HOST = _host()

CHECKS = r"""
set +e
echo '===[ uname / date ]==='
uname -a
date -u
echo

echo '===[ memory / disk ]==='
free -h
echo
df -h / /opt 2>/dev/null
echo

echo '===[ qsdmplus.service ]==='
systemctl is-enabled qsdmplus
systemctl is-active qsdmplus
systemctl status qsdmplus --no-pager -l | sed -n '1,25p'
echo

echo '===[ systemd unit file ]==='
cat /etc/systemd/system/qsdmplus.service
echo

echo '===[ ldd qsdmplus (outside unit env) ]==='
ldd /opt/qsdmplus/qsdmplus 2>&1 | sed -n '1,25p'
echo
echo '===[ ldd qsdmplus (with LD_LIBRARY_PATH) ]==='
LD_LIBRARY_PATH=/opt/qsdmplus/liboqs_install/lib:/opt/qsdmplus/liboqs_install/lib64 ldd /opt/qsdmplus/qsdmplus 2>&1 | sed -n '1,25p'
echo

echo '===[ /opt/qsdmplus tree ]==='
ls -lah /opt/qsdmplus | sed -n '1,40p'
echo
echo '===[ liboqs files ]==='
ls -lah /opt/qsdmplus/liboqs_install/lib 2>/dev/null | sed -n '1,10p'
ls -lah /opt/qsdmplus/liboqs_install/lib64 2>/dev/null | sed -n '1,10p'
echo

echo '===[ listening sockets ]==='
ss -tlnp 2>/dev/null | sed -n '1,40p'
echo

echo '===[ ufw status ]==='
ufw status verbose 2>&1 | sed -n '1,40p'
echo

echo '===[ local HTTP probes ]==='
for url in \
  http://127.0.0.1:8080/ \
  http://127.0.0.1:8080/api/v1/health/live \
  http://127.0.0.1:8080/api/v1/health/ready \
  http://127.0.0.1:8081/ \
  http://127.0.0.1:8081/api/health \
  http://127.0.0.1:8081/api/metrics/prometheus \
  http://127.0.0.1:8443/ ; do
  code=$(curl -sS -o /dev/null -m 5 -w '%{http_code}' "$url")
  echo "  $url -> $code"
done
echo

echo '===[ trust transparency endpoints (Major Update §8.5) ]==='
# Expected steady-state codes:
#   200  -> wired, aggregator warm, returning JSON summary / recent list
#   503  -> wired but still inside the warmup window (retry in ~5s)
#   404  -> operator opted out via [trust] disabled=true
#
# Any 5xx other than 503, or 401/403, means the route itself is
# misconfigured (auth/rate-limit leaked into a public endpoint) and the
# landing-page widget will fail closed.
for url in \
  http://127.0.0.1:8080/api/v1/trust/attestations/summary \
  "http://127.0.0.1:8080/api/v1/trust/attestations/recent?limit=5" ; do
  code=$(curl -sS -o /tmp/qsdm_trust.out -m 5 -w '%{http_code}' "$url")
  echo "  $url -> $code"
  case "$code" in
    200|503) head -c 400 /tmp/qsdm_trust.out ; echo ;;
  esac
done
echo

echo '===[ public HTTP probes (via ifconfig.me) ]==='
IP=$(curl -s ifconfig.me || echo "")
echo "public IP: $IP"
for p in 8080 8081 8443; do
  code=$(curl -sS -o /dev/null -m 5 -w '%{http_code}' "http://$IP:$p/")
  echo "  public :$p -> $code"
done
echo

echo '===[ journal (last 25 lines) ]==='
journalctl -u qsdmplus -n 25 --no-pager
echo

echo '===[ errors in journal (last 10 min) ]==='
journalctl -u qsdmplus --since '-10 min' -p err --no-pager | sed -n '1,40p'
echo

echo '===[ config: /opt/qsdmplus/qsdmplus.toml ]==='
cat /opt/qsdmplus/qsdmplus.toml
echo

echo '===[ cron for backups ]==='
crontab -l 2>/dev/null | grep -E 'vps-sqlite-backup' || echo '  (no backup cron installed)'
ls -lah /opt/qsdmplus/vps-sqlite-backup.sh 2>/dev/null || echo '  (backup script not present)'
echo

echo '===[ ssh hardening ]==='
grep -E '^(PermitRootLogin|PasswordAuthentication|PubkeyAuthentication|ChallengeResponseAuthentication|KbdInteractiveAuthentication)[[:space:]]' /etc/ssh/sshd_config 2>/dev/null
echo '----'
grep -E '^(PermitRootLogin|PasswordAuthentication|PubkeyAuthentication|ChallengeResponseAuthentication|KbdInteractiveAuthentication)[[:space:]]' /etc/ssh/sshd_config.d/*.conf 2>/dev/null
echo

echo '===[ authorized_keys ]==='
wc -l /root/.ssh/authorized_keys 2>/dev/null
sed -e 's/\(AAAA[A-Za-z0-9+/]\{1,12\}\).*\( [^ ]*$\)/\1...\2/' /root/.ssh/authorized_keys 2>/dev/null
echo
"""


def main() -> int:
    pw = os.environ.get("QSDM_VPS_PASS") or (sys.argv[1] if len(sys.argv) > 1 else "")
    if not pw:
        print("Set QSDM_VPS_PASS", file=sys.stderr)
        return 1
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.settimeout(30)
    sock.connect((HOST, 22))
    t = Transport(sock)
    t.start_client(timeout=30)
    t.auth_password("root", pw)
    ch = t.open_session()
    ch.exec_command("bash -s")
    ch.sendall(CHECKS.encode())
    ch.shutdown_write()
    out = b""
    while True:
        if ch.recv_ready():
            out += ch.recv(65536)
        if ch.recv_stderr_ready():
            out += ch.recv_stderr(65536)
        if ch.exit_status_ready():
            break
    while ch.recv_ready():
        out += ch.recv(65536)
    while ch.recv_stderr_ready():
        out += ch.recv_stderr(65536)
    sys.stdout.buffer.write(out)
    sys.stdout.buffer.flush()
    st = ch.recv_exit_status()
    t.close()
    sock.close()
    return 0 if st == 0 else 1


if __name__ == "__main__":
    raise SystemExit(main())
