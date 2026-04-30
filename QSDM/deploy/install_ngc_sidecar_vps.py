#!/usr/bin/env python3
"""Install the CPU-fallback NGC attestation sidecar on a QSDM VPS.

What this does
--------------
On the target VPS (default: `api.qsdm.tech`, root over ed25519) we:

  1. Create /opt/qsdm/ngc-sidecar/ (mode 0750) and upload
     apps/qsdm-nvidia-ngc/validator_phase1.py into it.
  2. Read the existing `QSDM_NGC_INGEST_SECRET` (preferred) or the
     legacy `QSDMPLUS_NGC_INGEST_SECRET` from the qsdm systemd service
     environment so the sidecar posts with the same HMAC the ingest
     endpoint already trusts. The legacy name is still accepted for the
     deprecation window described in `pkg/branding/branding.go`; pick
     whichever the validator was deployed with. The secret is never
     logged to the local shell.
  3. Write /opt/qsdm/ngc-sidecar/ngc.env (mode 0600, root-owned)
     with that secret, the loopback ingest URL, and a free-form node-id
     label for operator bookkeeping.
  4. Install systemd units `qsdm-ngc-attest.service` (oneshot) and
     `qsdm-ngc-attest.timer` (fires every 10 min, Persistent=true),
     enable + start the timer, and run a one-shot sanity attestation
     through journalctl.
  5. Print the updated /api/v1/trust/attestations/summary + /recent so
     you can see the fresh attestation land.

Why
---
The original attestation refresher is the Windows Scheduled Task on
the operator's dev PC (apps/qsdm-nvidia-ngc/scripts/
attest-from-env-file.ps1). When that PC goes offline, the trust pill
on qsdm.tech degrades to `attested=0/N` within 15 min. This sidecar
ensures the VPS keeps self-attesting even when no external operator
machine is online — a real uptime win for the transparency signal.

Run
---
    python QSDM/deploy/install_ngc_sidecar_vps.py           # api.qsdm.tech
    python QSDM/deploy/install_ngc_sidecar_vps.py --host other.qsdm.tech

Requires paramiko locally (pip install paramiko) and an ed25519 key
in ~/.ssh/id_ed25519 authorized on the VPS.

Idempotent: re-running regenerates ngc.env from the current service
secret, re-writes the systemd units, and restarts the timer.
"""
from __future__ import annotations
import argparse
import os
import sys

import paramiko

LOCAL_SIDECAR = "apps/qsdm-nvidia-ngc/validator_phase1.py"

SERVICE_UNIT = """\
[Unit]
Description=QSDM CPU-fallback NGC attestation sidecar
After=network-online.target qsdm.service
Wants=network-online.target
Requires=qsdm.service

[Service]
Type=oneshot
EnvironmentFile=/opt/qsdm/ngc-sidecar/ngc.env
ExecStart=/usr/bin/python3 /opt/qsdm/ngc-sidecar/validator_phase1.py
User=root
WorkingDirectory=/opt/qsdm/ngc-sidecar
StandardOutput=journal
StandardError=journal
TimeoutStartSec=90
Restart=no
"""

TIMER_UNIT = """\
[Unit]
Description=Refresh QSDM NGC attestation every 10 minutes

[Timer]
OnBootSec=2min
OnUnitActiveSec=10min
Persistent=true
Unit=qsdm-ngc-attest.service

[Install]
WantedBy=timers.target
"""

def ssh_run(c: paramiko.SSHClient, cmd: str, check: bool = True) -> str:
    _, stdout, stderr = c.exec_command(cmd, timeout=120)
    out = stdout.read().decode("utf-8", "replace")
    err = stderr.read().decode("utf-8", "replace")
    ec = stdout.channel.recv_exit_status()
    if check and ec != 0:
        raise SystemExit(
            f"ssh cmd failed (rc={ec}): {cmd}\n--stdout--\n{out}\n--stderr--\n{err}"
        )
    return out + (err if err.strip() else "")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--host", default="api.qsdm.tech")
    parser.add_argument("--user", default="root")
    parser.add_argument("--key",  default=os.path.expanduser("~/.ssh/id_ed25519"))
    parser.add_argument("--node-id", default="vps-blr1-validator",
                        help="Free-form label (QSDM_NGC_PROOF_NODE_ID).")
    parser.add_argument("--report-url",
                        default="http://127.0.0.1:8443/api/v1/monitoring/ngc-proof",
                        help="POST target; default is loopback on the VPS.")
    args = parser.parse_args()

    key = paramiko.Ed25519Key.from_private_key_file(args.key)
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(args.host, username=args.user, pkey=key, timeout=20, banner_timeout=20)

    try:
        print("=== 1. /opt/qsdm/ngc-sidecar ===")
        ssh_run(c, "mkdir -p /opt/qsdm/ngc-sidecar && "
                   "chmod 0750 /opt/qsdm/ngc-sidecar")

        print("\n=== 2. upload validator_phase1.py ===")
        sftp = c.open_sftp()
        try:
            sftp.put(LOCAL_SIDECAR, "/opt/qsdm/ngc-sidecar/validator_phase1.py")
        finally:
            sftp.close()
        ssh_run(c, "chmod 0755 /opt/qsdm/ngc-sidecar/validator_phase1.py")

        print("\n=== 3. read existing NGC ingest secret from systemd env ===")
        # Match BOTH the preferred (QSDM_NGC_INGEST_SECRET) and the
        # legacy (QSDMPLUS_NGC_INGEST_SECRET) names in one grep so the
        # installer keeps working on validators that haven't yet been
        # rotated to the new env-var name. The over-eager qsdmplus->qsdm
        # rebrand previously collapsed both branches of the regex into
        # the same alternative, silently dropping legacy support; the
        # docstring on this function still mentioned both, so the
        # collapse was unambiguously a search-and-replace bug.
        envline = ssh_run(c,
            "systemctl show qsdm --property=Environment --value | tr ' ' '\\n' "
            "| grep -E '^QSDM_NGC_INGEST_SECRET=|^QSDMPLUS_NGC_INGEST_SECRET='")
        secret = ""
        for line in envline.splitlines():
            if "=" in line:
                k, v = line.split("=", 1)
                if "NGC_INGEST_SECRET" in k and v.strip():
                    secret = v.strip()
                    break
        if not secret:
            raise SystemExit(
                "could not find QSDM_NGC_INGEST_SECRET (or legacy "
                "QSDMPLUS_NGC_INGEST_SECRET) on the qsdm service. "
                "Set it in /etc/systemd/system/qsdm.service.d/secrets.conf "
                "first, then re-run this installer."
            )
        print(f"  got secret (len={len(secret)}, not logged)")

        print("\n=== 4. write ngc.env (mode 0600) ===")
        env_body = (
            "# /opt/qsdm/ngc-sidecar/ngc.env — CPU-fallback NGC attestation.\n"
            "# Generated by QSDM/deploy/install_ngc_sidecar_vps.py.\n"
            "# Re-running the installer regenerates this file from the current\n"
            "# qsdm service secret, so a rotation only needs one touch.\n"
            f"QSDM_NGC_REPORT_URL={args.report_url}\n"
            f"QSDM_NGC_INGEST_SECRET={secret}\n"
            f"QSDM_NGC_PROOF_NODE_ID={args.node_id}\n"
        )
        ssh_run(c,
            "umask 0077 && cat > /opt/qsdm/ngc-sidecar/ngc.env <<'QSDM_EOF_ENV'\n"
            + env_body
            + "QSDM_EOF_ENV\n"
            "chmod 0600 /opt/qsdm/ngc-sidecar/ngc.env")
        ssh_run(c, "ls -la /opt/qsdm/ngc-sidecar/ngc.env")

        print("\n=== 5. install systemd units ===")
        ssh_run(c,
            "cat > /etc/systemd/system/qsdm-ngc-attest.service <<'QSDM_EOF_SVC'\n"
            + SERVICE_UNIT + "QSDM_EOF_SVC")
        ssh_run(c,
            "cat > /etc/systemd/system/qsdm-ngc-attest.timer <<'QSDM_EOF_TIM'\n"
            + TIMER_UNIT + "QSDM_EOF_TIM")
        ssh_run(c, "systemctl daemon-reload")

        print("\n=== 6. one-shot sanity run ===")
        print(ssh_run(c,
            "systemctl start qsdm-ngc-attest.service && "
            "sleep 2 && "
            "journalctl -u qsdm-ngc-attest.service -n 40 --no-pager"))

        print("\n=== 7. enable + start timer ===")
        ssh_run(c, "systemctl enable --now qsdm-ngc-attest.timer")
        print(ssh_run(c, "systemctl list-timers qsdm-ngc-attest.timer --no-pager"))

        print("\n=== 8. live summary ===")
        ssh_run(c, "sleep 3")
        print(ssh_run(c,
            "curl -s https://api.qsdm.tech/api/v1/trust/attestations/summary | "
            "python3 -m json.tool"))
        print(ssh_run(c,
            "curl -s 'https://api.qsdm.tech/api/v1/trust/attestations/recent?limit=5' | "
            "python3 -m json.tool"))
    finally:
        c.close()
    print("\n[install-ngc-sidecar] done.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
