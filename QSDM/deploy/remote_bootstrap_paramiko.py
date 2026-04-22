# One-time: password auth to append SSH pubkey, upload local QSDM tree, run install-ubuntu-vps.sh.
# Usage: set QSDM_VPS_PASS, then: python remote_bootstrap_paramiko.py
# Do not commit secrets.

import os
import sys
import tarfile
import tempfile
import time
from pathlib import Path

import paramiko

from _deploy_host import host as _host, user as _user

HOST = _host()
USER = _user()
REPO = Path(__file__).resolve().parent.parent
INSTALL_SH = REPO / "deploy" / "install-ubuntu-vps.sh"
PUB = Path(os.environ.get("USERPROFILE", ""), ".ssh", "id_ed25519.pub")

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
    }
)


def should_skip_file(p: Path, root: Path) -> bool:
    try:
        rel = p.relative_to(root)
    except ValueError:
        return True
    for part in rel.parts:
        if part in EXCLUDE_DIR_NAMES:
            return True
    if p.suffix.lower() in (".exe", ".db", ".pdb", ".zip"):
        return True
    return False


def iter_source_files(root: Path):
    for p in root.rglob("*"):
        if not p.is_file():
            continue
        if should_skip_file(p, root):
            continue
        yield p


def make_qsdm_tarball(root: Path) -> Path:
    files = list(iter_source_files(root))
    tmp = tempfile.NamedTemporaryFile(delete=False, suffix=".tar.gz")
    tmp.close()
    path = Path(tmp.name)
    print(f"--- Packing {len(files)} files from {root} ---", flush=True)
    with tarfile.open(path, "w:gz") as tf:
        for i, p in enumerate(files):
            if i and i % 500 == 0:
                print(f"    ... {i}/{len(files)}", flush=True)
            arc = p.relative_to(root).as_posix()
            tf.add(p, arcname=arc, recursive=False)
    print(f"--- Tarball: {path.stat().st_size // 1024} KiB ---", flush=True)
    return path


def main() -> int:
    pw = os.environ.get("QSDM_VPS_PASS")
    if not pw:
        print("Set QSDM_VPS_PASS (or pass as argv[1] for this run only).", file=sys.stderr)
        if len(sys.argv) >= 2:
            pw = sys.argv[1]
        else:
            return 1
    if not PUB.is_file():
        print(f"Missing {PUB}", file=sys.stderr)
        return 1
    pub = PUB.read_text().strip() + "\n"
    if not INSTALL_SH.is_file():
        print(f"Missing {INSTALL_SH}", file=sys.stderr)
        return 1

    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(
        HOST,
        username=USER,
        password=pw,
        timeout=30,
        banner_timeout=30,
        auth_timeout=30,
        allow_agent=False,
        look_for_keys=False,
    )
    t = c.get_transport()
    if t:
        t.set_keepalive(30)

    sftp = c.open_sftp()
    try:
        c.exec_command("mkdir -p /root/.ssh && chmod 700 /root/.ssh", timeout=60)
        time.sleep(0.2)
        try:
            with sftp.open("/root/.ssh/authorized_keys", "r") as rf:
                existing = rf.read().decode()
        except OSError:
            existing = ""
        if pub.strip() not in existing:
            with sftp.open("/root/.ssh/authorized_keys", "a") as wf:
                wf.write(pub.encode())
        c.exec_command("chmod 600 /root/.ssh/authorized_keys", timeout=30)
    finally:
        sftp.close()

    tball = make_qsdm_tarball(REPO)
    try:
        print("--- Uploading tarball ---", flush=True)
        c.exec_command("rm -rf /root/QSDM", timeout=60)
        sftp = c.open_sftp()
        try:
            sftp.put(str(tball), "/root/qsdm-local.tar.gz")
        finally:
            sftp.close()
        print("--- Extracting on server ---", flush=True)
        _stdin, stdout, stderr = c.exec_command(
            "bash -lc 'set -e; mkdir -p /root/QSDM; tar -xzf /root/qsdm-local.tar.gz -C /root/QSDM; test -f /root/QSDM/scripts/rebuild_liboqs.sh'",
            timeout=120,
        )
        out = stdout.read().decode() + stderr.read().decode()
        if out.strip():
            print(out, flush=True)
        st = stdout.channel.recv_exit_status()
        if st != 0:
            print(f"Extract failed: exit {st}", file=sys.stderr, flush=True)
            return st
    finally:
        try:
            tball.unlink(missing_ok=True)  # type: ignore
        except OSError:
            pass

    raw = INSTALL_SH.read_bytes().replace(b"\r\n", b"\n").replace(b"\r", b"\n")
    sftp2 = c.open_sftp()
    try:
        with sftp2.open("/root/install-ubuntu-vps.sh", "wb") as rf:
            rf.write(raw)
    finally:
        sftp2.close()
    c.exec_command("chmod +x /root/install-ubuntu-vps.sh", timeout=10)

    print("--- Running install-ubuntu-vps.sh (liboqs build can take 20-50+ min) ---", flush=True)
    chan = c.get_transport().open_session()
    chan.get_pty()
    chan.exec_command("bash -lc 'set -e; cd /root && bash /root/install-ubuntu-vps.sh'")
    while True:
        if chan.recv_ready():
            d = chan.recv(65536)
            if d:
                sys.stdout.buffer.write(d)
                sys.stdout.buffer.flush()
        if chan.exit_status_ready():
            break
        if chan.recv_stderr_ready():
            e = chan.recv_stderr(65536)
            if e:
                sys.stderr.buffer.write(e)
                sys.stderr.buffer.flush()
        time.sleep(0.1)
    while chan.recv_ready():
        d = chan.recv(65536)
        if d:
            sys.stdout.buffer.write(d)
            sys.stdout.buffer.flush()
    st = chan.recv_exit_status()
    c.close()
    return st


if __name__ == "__main__":
    raise SystemExit(main())
