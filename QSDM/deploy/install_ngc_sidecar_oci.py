#!/usr/bin/env python3
"""Install the CPU-fallback NGC attestation sidecar on an Oracle Cloud VM.

Background
----------
`install_ngc_sidecar_vps.py` assumes the target is the reference
validator: login is `root`, the `qsdm` service is already running
locally, and the installer can pull `QSDM_NGC_INGEST_SECRET` out
of its systemd drop-in. None of that is true on a secondary
attestation source like an OCI Always-Free / E-series VM, which:

  * logs in as `ubuntu` and relies on `sudo` for privileged steps,
  * does NOT run `qsdm` locally — the sidecar's only job is to
    POST proof bundles to the main validator's public API,
  * therefore needs the ingest secret injected out-of-band (from
    the operator's local `vps.txt` or an env var).

This script mirrors the reference installer's output — sidecar at
/opt/qsdm/ngc-sidecar, systemd oneshot + 10 min timer, one-shot
sanity attestation in the journal — but takes the ingest secret
through `--secret-env` (preferred) or `--secret` (argv, last resort)
and points the POST at the public API hostname rather than loopback.

Run
---
    $env:QSDM_INGEST_SECRET = "<32-byte hex>"
    python QSDM/deploy/install_ngc_sidecar_oci.py `
        --host 140.245.97.30 `
        --user ubuntu `
        --key $HOME/.ssh/id_ed25519 `
        --node-id vps-oci-sgp1-attest `
        --secret-env QSDM_INGEST_SECRET

Idempotent: re-running overwrites ngc.env and re-registers the unit.

Requires paramiko and an ed25519 key authorised on the target.
"""
from __future__ import annotations
import argparse
import os
import shlex
import sys

import paramiko

LOCAL_SIDECAR = "apps/qsdm-nvidia-ngc/validator_phase1.py"

SERVICE_UNIT = """\
[Unit]
Description=QSDM CPU-fallback NGC attestation sidecar ({node_label})
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
EnvironmentFile=/opt/qsdm/ngc-sidecar/ngc.env
ExecStart=/usr/bin/python3 /opt/qsdm/ngc-sidecar/validator_phase1.py
User=root
WorkingDirectory=/opt/qsdm/ngc-sidecar
StandardOutput=journal
StandardError=journal
TimeoutStartSec=120
Restart=no
"""

TIMER_UNIT = """\
[Unit]
Description=Refresh QSDM NGC attestation every 10 minutes ({node_label})

[Timer]
OnBootSec=2min
OnUnitActiveSec=10min
RandomizedDelaySec=30s
Persistent=true
Unit=qsdm-ngc-attest.service

[Install]
WantedBy=timers.target
"""


def ssh_run(c: paramiko.SSHClient, cmd: str, check: bool = True) -> str:
    _, stdout, stderr = c.exec_command(cmd, timeout=180)
    out = stdout.read().decode("utf-8", "replace")
    err = stderr.read().decode("utf-8", "replace")
    ec = stdout.channel.recv_exit_status()
    if check and ec != 0:
        raise SystemExit(
            f"ssh cmd failed (rc={ec}): {cmd}\n--stdout--\n{out}\n--stderr--\n{err}"
        )
    return out + (err if err.strip() else "")


def sudo_write_file(c: paramiko.SSHClient, path: str, body: str, mode: str) -> None:
    """Write a file on the remote via `sudo bash -c 'cat > ...'` so the
    contents never appear in argv / shell history."""
    heredoc = f"umask 0077; cat > {path}"
    chan = c.get_transport().open_session()
    chan.exec_command(f"sudo bash -c {shlex.quote(heredoc)}")
    chan.sendall(body.encode("utf-8"))
    chan.shutdown_write()
    out = chan.makefile("r", 4096).read().decode("utf-8", "replace")
    err = chan.makefile_stderr("r", 4096).read().decode("utf-8", "replace")
    ec = chan.recv_exit_status()
    if ec != 0:
        raise SystemExit(f"sudo write {path} failed (rc={ec}) {out} {err}")
    ssh_run(c, f"sudo chmod {mode} {path} && sudo chown root:root {path}")


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--host", required=True, help="Target IP or DNS of the second attestation box.")
    p.add_argument("--user", default="ubuntu", help="SSH login user (default: ubuntu).")
    p.add_argument("--key", default=os.path.expanduser("~/.ssh/id_ed25519"))
    p.add_argument("--node-id", required=True,
                   help="Free-form QSDM_NGC_PROOF_NODE_ID. MUST differ from every other sidecar.")
    p.add_argument("--region-label",
                   help="Optional: free-text region label for the node id comment in ngc.env (not wired to region_hint).")
    p.add_argument("--report-url",
                   default="https://api.qsdm.tech/api/v1/monitoring/ngc-proof",
                   help="POST target; default is the reference validator's public API.")
    p.add_argument("--secret-env",
                   help="Environment variable name that holds the ingest secret. Preferred.")
    p.add_argument("--secret",
                   help="Ingest secret value (argv; visible to `ps`; last resort).")
    args = p.parse_args()

    secret = ""
    if args.secret_env:
        secret = os.environ.get(args.secret_env, "").strip()
        if not secret:
            raise SystemExit(f"--secret-env {args.secret_env} is empty or unset")
    elif args.secret:
        secret = args.secret.strip()
    else:
        raise SystemExit("Either --secret-env or --secret is required.")
    if len(secret) < 16:
        raise SystemExit("ingest secret is suspiciously short (<16 chars); refusing")

    key = paramiko.Ed25519Key.from_private_key_file(args.key)
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(args.host, username=args.user, pkey=key, timeout=20, banner_timeout=20)

    try:
        print(f"=== 1. /opt/qsdm/ngc-sidecar (sudo) ===")
        ssh_run(c,
            "sudo mkdir -p /opt/qsdm/ngc-sidecar && "
            "sudo chmod 0750 /opt/qsdm/ngc-sidecar && "
            "sudo chown root:root /opt/qsdm/ngc-sidecar")

        print("\n=== 2. upload validator_phase1.py ===")
        sftp = c.open_sftp()
        try:
            sftp.put(LOCAL_SIDECAR, "/tmp/validator_phase1.py")
        finally:
            sftp.close()
        ssh_run(c,
            "sudo install -o root -g root -m 0755 "
            "/tmp/validator_phase1.py /opt/qsdm/ngc-sidecar/validator_phase1.py && "
            "rm -f /tmp/validator_phase1.py")

        print("\n=== 3. write ngc.env (mode 0600, root:root) ===")
        region_comment = (
            f"# region label (operator free-text): {args.region_label}\n"
            if args.region_label else ""
        )
        env_body = (
            "# /opt/qsdm/ngc-sidecar/ngc.env — CPU-fallback NGC attestation.\n"
            "# Generated by QSDM/deploy/install_ngc_sidecar_oci.py.\n"
            f"{region_comment}"
            f"QSDM_NGC_REPORT_URL={args.report_url}\n"
            f"QSDM_NGC_INGEST_SECRET={secret}\n"
            f"QSDM_NGC_PROOF_NODE_ID={args.node_id}\n"
        )
        sudo_write_file(c, "/opt/qsdm/ngc-sidecar/ngc.env", env_body, "0600")
        ssh_run(c, "sudo ls -la /opt/qsdm/ngc-sidecar/ngc.env")

        print("\n=== 4. install systemd units ===")
        label = args.region_label or args.node_id
        sudo_write_file(c, "/etc/systemd/system/qsdm-ngc-attest.service",
                        SERVICE_UNIT.format(node_label=label), "0644")
        sudo_write_file(c, "/etc/systemd/system/qsdm-ngc-attest.timer",
                        TIMER_UNIT.format(node_label=label), "0644")
        ssh_run(c, "sudo systemctl daemon-reload")

        print("\n=== 5. one-shot sanity attestation ===")
        print(ssh_run(c,
            "sudo systemctl start qsdm-ngc-attest.service; "
            "sleep 3; "
            "sudo journalctl -u qsdm-ngc-attest.service -n 60 --no-pager"))

        print("\n=== 6. enable + start timer ===")
        ssh_run(c, "sudo systemctl enable --now qsdm-ngc-attest.timer")
        print(ssh_run(c, "sudo systemctl status qsdm-ngc-attest.timer --no-pager | head -10"))

        print("\n=== 7. verify live summary ===")
        # 12 s exceeds the aggregator's default 10 s refresh interval, so
        # the row produced in step 5 should already be visible.
        ssh_run(c, "sleep 12")
        print(ssh_run(c,
            f"curl -sS --max-time 10 "
            f"https://api.qsdm.tech/api/v1/trust/attestations/summary"))
        print()
        print(ssh_run(c,
            f"curl -sS --max-time 10 "
            f"'https://api.qsdm.tech/api/v1/trust/attestations/recent?limit=10'"))
    finally:
        c.close()
    print("\n[install-ngc-sidecar-oci] done.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
