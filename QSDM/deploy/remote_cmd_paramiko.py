"""Run arbitrary bash via stdin on VPS. Usage: QSDM_VPS_PASS=... python remote_cmd_paramiko.py < script.sh
Or: echo 'some commands' | python remote_cmd_paramiko.py"""
import os
import socket
import sys
from paramiko import Transport

from _deploy_host import host as _host, user as _user

HOST = _host()
USER = _user()


def main() -> int:
    pw = os.environ.get("QSDM_VPS_PASS") or (sys.argv[1] if len(sys.argv) > 1 else "")
    if not pw:
        print("Set QSDM_VPS_PASS", file=sys.stderr)
        return 1
    script = sys.stdin.read()
    if not script.strip():
        print("provide bash on stdin", file=sys.stderr)
        return 1
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.settimeout(30)
    sock.connect((HOST, 22))
    t = Transport(sock)
    t.start_client(timeout=30)
    t.auth_password(USER, pw)
    ch = t.open_session()
    ch.exec_command("bash -s")
    ch.sendall(script.encode())
    ch.shutdown_write()
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
    st = ch.recv_exit_status()
    t.close()
    sock.close()
    return st


if __name__ == "__main__":
    raise SystemExit(main())
