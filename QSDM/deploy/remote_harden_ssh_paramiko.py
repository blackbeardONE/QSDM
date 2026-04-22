"""Upload hardening drop-in + script and run it on the VPS.

Usage (Powershell):
    $env:QSDM_VPS_PASS = '...'
    python deploy/remote_harden_ssh_paramiko.py
"""
from __future__ import annotations

import os
import socket
import sys
from pathlib import Path

import paramiko
from paramiko import Transport

HOST = "206.189.132.232"
BASE = Path(__file__).resolve().parent.parent
REMOTE_DIR = "/root/qsdm-deploy"


def upload_text(sftp: paramiko.SFTPClient, local: Path, remote: str) -> None:
    raw = local.read_bytes().replace(b"\r\n", b"\n").replace(b"\r", b"\n")
    with sftp.open(remote, "wb") as f:
        f.write(raw)


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
    t.set_keepalive(30)

    sftp = paramiko.SFTPClient.from_transport(t)
    assert sftp is not None
    try:
        try:
            sftp.mkdir(REMOTE_DIR)
        except IOError:
            pass
        upload_text(sftp, BASE / "deploy/99-qsdm-hardening.conf", f"{REMOTE_DIR}/99-qsdm-hardening.conf")
        upload_text(sftp, BASE / "deploy/harden-ssh.sh", f"{REMOTE_DIR}/harden-ssh.sh")
    finally:
        sftp.close()

    ch0 = t.open_session()
    ch0.exec_command(f"chmod +x {REMOTE_DIR}/harden-ssh.sh")
    ch0.recv_exit_status()

    print("--- Running harden-ssh.sh ---", flush=True)
    ch = t.open_session()
    ch.exec_command(f"bash {REMOTE_DIR}/harden-ssh.sh")
    while True:
        if ch.recv_ready():
            sys.stdout.buffer.write(ch.recv(65536))
            sys.stdout.buffer.flush()
        if ch.recv_stderr_ready():
            sys.stderr.buffer.write(ch.recv_stderr(65536))
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
    sock.close()
    return st


if __name__ == "__main__":
    raise SystemExit(main())
