"""Push fixed qsdm.service and restart (uses QSDM_VPS_PASS)."""
import os
import sys
from pathlib import Path

import paramiko

from _deploy_host import host as _host, user as _user

HOST = _host()
USER = _user()
SERVICE = Path(__file__).resolve().parent.parent / "config" / "qsdm.service"


def main() -> int:
    pw = os.environ.get("QSDM_VPS_PASS") or (sys.argv[1] if len(sys.argv) > 1 else "")
    if not pw:
        print("Set QSDM_VPS_PASS", file=sys.stderr)
        return 1
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(HOST, username=USER, password=pw, timeout=30, allow_agent=False, look_for_keys=False)
    raw = SERVICE.read_bytes().replace(b"\r\n", b"\n").replace(b"\r", b"\n")
    sftp = c.open_sftp()
    try:
        with sftp.open("/etc/systemd/system/qsdm.service", "wb") as f:
            f.write(raw)
    finally:
        sftp.close()
    for cmd in (
        "systemctl daemon-reload",
        "systemctl restart qsdm",
        "sleep 2",
        "systemctl status qsdm --no-pager -l",
        "ldd /opt/qsdm/qsdm 2>&1 | head -20",
    ):
        _, out, err = c.exec_command(cmd, timeout=60)
        o = out.read().decode() + err.read().decode()
        if o.strip():
            print(o)
    st = c.exec_command("systemctl is-active qsdm", timeout=10)[1].read().decode().strip()
    c.close()
    print("--- is-active:", st, "---")
    return 0 if st == "active" else 1


if __name__ == "__main__":
    raise SystemExit(main())
