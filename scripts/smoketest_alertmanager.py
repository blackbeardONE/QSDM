#!/usr/bin/env python
"""End-to-end smoke test for the QSDM Alertmanager templates.

`amtool check-config` validates that the YAML structure parses, that
template references resolve, and that URL fields have valid schemes.
But it does NOT execute the routing tree, fan-out logic, or actual
template rendering. This script does — by spinning up a real
Alertmanager process, pointing every receiver at a localhost HTTP
listener, pushing synthetic alerts through `/api/v2/alerts`, and
asserting that the rendered notification body contains both
`runbook_url` and `dashboard_url`.

Two phases run in sequence:

  Phase 1 — webhook routing & annotation propagation
    Uses `webhook_configs:` receivers (which carry the entire alert
    payload as JSON, so we can inspect `commonAnnotations`).
    Verifies the routing tree dispatches correctly, that critical
    alerts fan out to BOTH pagerduty + slack-critical legs, and
    that every receiver sees both URLs in commonAnnotations.

  Phase 2 — slack_configs template rendering
    Uses real `slack_configs:` receivers (which apply the Go
    templates from `templates/qsdm.tmpl`). api_url is pointed at
    a localhost listener instead of api.slack.com, so we capture
    the actual rendered Slack JSON and assert the templates
    surface `*Runbook*:` / `*Dashboard*:` mrkdwn lines and the
    action buttons carry the resolved URLs.

Skip semantics
--------------
- `alertmanager` not on PATH (and `$ALERTMANAGER` env var unset):
  exits 0 with a "skipping" banner. The script is a quality bar,
  not a hard CI gate (yet); the CI workflow installs alertmanager
  and runs `amtool check-config` itself.
- All temporary state (alertmanager working dir, fake config) is
  written under `tempfile.mkdtemp()` and cleaned up on exit.

Exit
----
- 0 on success (all checks pass) or graceful skip.
- 1 on any failure; per-check `[FAIL]` lines plus alertmanager's
  stderr tail are printed for diagnosis.
"""
from __future__ import annotations

import json
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time
import urllib.request
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
SOURCE_TMPL = REPO / "QSDM" / "deploy" / "alertmanager" / "templates" / "qsdm.tmpl"

LISTENER_PORT = 18888
AM_API_PORT = 19093
LISTENER_PORT_SLACK = 18889
AM_API_PORT_SLACK = 19193


def find_alertmanager() -> Path | None:
    """Locate the `alertmanager` binary.

    Order:
      1. `$ALERTMANAGER` env var.
      2. `alertmanager` / `alertmanager.exe` on PATH.

    Returns None if neither is found, in which case the script
    exits 0 with a skip banner.
    """
    env = os.environ.get("ALERTMANAGER")
    if env and Path(env).exists():
        return Path(env)
    for cand in ("alertmanager", "alertmanager.exe"):
        found = shutil.which(cand)
        if found:
            return Path(found)
    return None


# ---------------------------------------------------------------
# Phase 1 config (webhook_configs everywhere)
# ---------------------------------------------------------------


def make_webhook_test_config(tmp_dir: Path) -> Path:
    tmpl_dir = tmp_dir / "templates"
    tmpl_dir.mkdir()
    shutil.copy(SOURCE_TMPL, tmpl_dir / "qsdm.tmpl")

    listener_url = f"http://127.0.0.1:{LISTENER_PORT}/webhook"

    cfg = f"""
global:
  resolve_timeout: 5m

templates:
  - 'templates/qsdm.tmpl'

route:
  receiver: 'wh-default'
  group_by: ['alertname', 'cluster', 'instance']
  group_wait: 0s
  group_interval: 1s
  repeat_interval: 1m
  routes:
    - match:
        severity: critical
      receiver: 'wh-critical-pd'
      group_wait: 0s
      continue: true
    - match:
        severity: critical
      receiver: 'wh-critical-slack'
      group_wait: 0s
    - match:
        severity: warning
      receiver: 'wh-warning'
      group_wait: 0s
    - match:
        severity: info
      receiver: 'wh-info'
      group_wait: 0s

receivers:
  - name: 'wh-default'
    webhook_configs:
      - url: '{listener_url}/default'
        send_resolved: false
  - name: 'wh-critical-pd'
    webhook_configs:
      - url: '{listener_url}/critical-pd'
        send_resolved: false
  - name: 'wh-critical-slack'
    webhook_configs:
      - url: '{listener_url}/critical-slack'
        send_resolved: false
  - name: 'wh-warning'
    webhook_configs:
      - url: '{listener_url}/warning'
        send_resolved: false
  - name: 'wh-info'
    webhook_configs:
      - url: '{listener_url}/info'
        send_resolved: false
"""
    cfg_path = tmp_dir / "alertmanager.yml"
    cfg_path.write_text(cfg, encoding="utf-8")
    return cfg_path


# ---------------------------------------------------------------
# Phase 2 config (real slack_configs with templates)
# ---------------------------------------------------------------


def make_slack_test_config(tmp_dir: Path) -> Path:
    tmpl_dir = tmp_dir / "templates"
    tmpl_dir.mkdir()
    shutil.copy(SOURCE_TMPL, tmpl_dir / "qsdm.tmpl")

    listener = f"http://127.0.0.1:{LISTENER_PORT_SLACK}/slack"

    cfg = f"""
global:
  resolve_timeout: 5m
  smtp_from: 'qsdm-test@example.com'
  smtp_smarthost: 'localhost:25'
  smtp_auth_username: 'u'
  smtp_auth_password: 'p'
  smtp_require_tls: false

templates:
  - 'templates/qsdm.tmpl'

route:
  receiver: 'slack-warning'
  group_by: ['alertname', 'instance']
  group_wait: 0s
  group_interval: 1s
  repeat_interval: 1m
  routes:
    - match:
        severity: critical
      receiver: 'slack-critical'
      group_wait: 0s
    - match:
        severity: warning
      receiver: 'slack-warning'
      group_wait: 0s

receivers:
  - name: 'slack-critical'
    slack_configs:
      - api_url: '{listener}/critical'
        channel: '#qsdm-incidents'
        send_resolved: true
        title: '{{{{ template "qsdm.slack.title" . }}}}'
        title_link: '{{{{ template "qsdm.slack.titlelink" . }}}}'
        text: '{{{{ template "qsdm.text" . }}}}'
        color: '{{{{ template "qsdm.slack.color" . }}}}'
        actions:
          - type: button
            text: '📖 Runbook'
            url: '{{{{ .CommonAnnotations.runbook_url }}}}'
          - type: button
            text: '📊 Dashboard'
            url: '{{{{ .CommonAnnotations.dashboard_url }}}}'
  - name: 'slack-warning'
    slack_configs:
      - api_url: '{listener}/warning'
        channel: '#qsdm-warnings'
        send_resolved: true
        title: '{{{{ template "qsdm.slack.title" . }}}}'
        title_link: '{{{{ template "qsdm.slack.titlelink" . }}}}'
        text: '{{{{ template "qsdm.text" . }}}}'
        color: '{{{{ template "qsdm.slack.color" . }}}}'
        actions:
          - type: button
            text: '📖 Runbook'
            url: '{{{{ .CommonAnnotations.runbook_url }}}}'
          - type: button
            text: '📊 Dashboard'
            url: '{{{{ .CommonAnnotations.dashboard_url }}}}'
"""
    cfg_path = tmp_dir / "alertmanager.yml"
    cfg_path.write_text(cfg, encoding="utf-8")
    return cfg_path


# ---------------------------------------------------------------
# Listener
# ---------------------------------------------------------------

received: list[dict] = []
received_lock = threading.Lock()
received_slack: list[dict] = []
received_slack_lock = threading.Lock()


def _make_handler(bucket: list[dict], lock: threading.Lock):
    class _H(BaseHTTPRequestHandler):
        def do_POST(self):  # noqa: N802
            length = int(self.headers.get("Content-Length") or "0")
            body = self.rfile.read(length) if length else b""
            text = body.decode("utf-8", errors="replace")
            try:
                payload = json.loads(text)
            except json.JSONDecodeError:
                payload = {"_raw": text}
            with lock:
                bucket.append({"path": self.path, "payload": payload, "raw": text})
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"ok")

        def log_message(self, format, *args):  # noqa: A002
            pass

    return _H


def _start_listener(port: int, bucket: list[dict], lock: threading.Lock) -> HTTPServer:
    server = HTTPServer(("127.0.0.1", port), _make_handler(bucket, lock))
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server


# ---------------------------------------------------------------
# Alertmanager helpers
# ---------------------------------------------------------------


def wait_port(port: int, timeout: float = 30.0) -> bool:
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=1):
                return True
        except OSError:
            time.sleep(0.25)
    return False


def push_alerts(port: int, alerts: list[dict]) -> None:
    body = json.dumps(alerts).encode("utf-8")
    req = urllib.request.Request(
        f"http://127.0.0.1:{port}/api/v2/alerts",
        data=body,
        method="POST",
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=5) as resp:
        if resp.status not in (200, 201):
            raise RuntimeError(f"alertmanager returned {resp.status}")


def fixture_alert(severity: str, alertname: str) -> dict:
    now = datetime.now(timezone.utc)
    return {
        "startsAt": now.isoformat(),
        "endsAt": (now + timedelta(minutes=5)).isoformat(),
        "generatorURL": "http://prometheus.local/graph",
        "labels": {
            "alertname": alertname,
            "severity": severity,
            "subsystem": "v2-test",
            "instance": "smoke-test:9090",
            "cluster": "smoke",
        },
        "annotations": {
            "summary": f"smoke test {alertname}",
            "description": (
                f"synthetic alert produced by smoketest_alertmanager.py "
                f"(severity={severity})"
            ),
            "runbook_url": (
                f"https://github.com/blackbeardONE/QSDM/blob/main/"
                f"QSDM/docs/docs/runbooks/SMOKE_TEST.md#{alertname.lower()}"
            ),
            "dashboard_url": (
                f"https://github.com/blackbeardONE/QSDM/blob/main/"
                f"QSDM/deploy/grafana/dashboards/qsdm-runbook-smoke-test.json"
            ),
        },
    }


def expect(label: str, ok: bool) -> bool:
    badge = "PASS" if ok else "FAIL"
    print(f"  [{badge}] {label}")
    return ok


# ---------------------------------------------------------------
# Phases
# ---------------------------------------------------------------


def run_phase1(am_bin: Path) -> tuple[list[bool], str]:
    """Webhook routing + commonAnnotations propagation."""
    tmp = Path(tempfile.mkdtemp(prefix="qsdm-am-phase1-"))
    print(f"Phase 1 temp dir: {tmp}")

    server = _start_listener(LISTENER_PORT, received, received_lock)
    print(f"Phase 1 listener up on :{LISTENER_PORT}")

    cfg = make_webhook_test_config(tmp)
    proc = subprocess.Popen(
        [
            str(am_bin),
            f"--config.file={cfg}",
            f"--storage.path={tmp / 'data'}",
            f"--web.listen-address=:{AM_API_PORT}",
            "--cluster.listen-address=",
            "--log.level=warn",
        ],
        cwd=str(tmp),
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    stderr_tail = ""
    try:
        if not wait_port(AM_API_PORT):
            stdout, stderr = proc.communicate(timeout=2)
            stderr_tail = stderr.decode(errors="replace")[-1500:]
            print("Phase 1 alertmanager failed to start.")
            return [False], stderr_tail
        print(f"Phase 1 alertmanager up on :{AM_API_PORT}")

        push_alerts(AM_API_PORT, [
            fixture_alert("critical", "SmokeCritical"),
            fixture_alert("warning", "SmokeWarning"),
            fixture_alert("info", "SmokeInfo"),
            {
                **fixture_alert("warning", "SmokeNoSeverity"),
                "labels": {
                    "alertname": "SmokeNoSeverity",
                    "instance": "smoke-test:9090",
                    "cluster": "smoke",
                },
            },
        ])
        time.sleep(8)

        with received_lock:
            seen = list(received)

        results: list[bool] = []
        paths = {r["path"] for r in seen}
        results.append(expect(
            f"got {len(seen)} webhook deliveries (expected >=5)",
            len(seen) >= 5,
        ))
        for label, p in [
            ("critical-pd path", "/webhook/critical-pd"),
            ("critical-slack path", "/webhook/critical-slack"),
            ("warning path", "/webhook/warning"),
            ("info path", "/webhook/info"),
            ("default path (unlabelled)", "/webhook/default"),
        ]:
            results.append(expect(f"{label} delivered", p in paths))

        for r in seen:
            path = r["path"]
            if path == "/webhook/default":
                continue
            common = r["payload"].get("commonAnnotations", {})
            results.append(expect(
                f"[{path}] commonAnnotations has runbook_url",
                common.get("runbook_url", "").startswith("https://github.com/"),
            ))
            results.append(expect(
                f"[{path}] commonAnnotations has dashboard_url",
                common.get("dashboard_url", "").startswith("https://github.com/"),
            ))

        return results, ""
    finally:
        proc.terminate()
        try:
            stdout, stderr = proc.communicate(timeout=5)
            if not stderr_tail and stderr:
                stderr_tail = stderr.decode(errors="replace")[-1500:]
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.communicate()
        server.shutdown()
        shutil.rmtree(tmp, ignore_errors=True)


def run_phase2(am_bin: Path) -> tuple[list[bool], str]:
    """slack_configs template rendering verification."""
    tmp = Path(tempfile.mkdtemp(prefix="qsdm-am-phase2-"))
    print(f"Phase 2 temp dir: {tmp}")

    server = _start_listener(LISTENER_PORT_SLACK, received_slack, received_slack_lock)
    print(f"Phase 2 listener up on :{LISTENER_PORT_SLACK}")

    cfg = make_slack_test_config(tmp)
    proc = subprocess.Popen(
        [
            str(am_bin),
            f"--config.file={cfg}",
            f"--storage.path={tmp / 'data'}",
            f"--web.listen-address=:{AM_API_PORT_SLACK}",
            "--cluster.listen-address=",
            "--log.level=warn",
        ],
        cwd=str(tmp),
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    stderr_tail = ""
    try:
        if not wait_port(AM_API_PORT_SLACK):
            stdout, stderr = proc.communicate(timeout=2)
            stderr_tail = stderr.decode(errors="replace")[-1500:]
            print("Phase 2 alertmanager failed to start.")
            return [False], stderr_tail
        print(f"Phase 2 alertmanager up on :{AM_API_PORT_SLACK}")

        push_alerts(AM_API_PORT_SLACK, [
            fixture_alert("critical", "SmokeCriticalSlack"),
            fixture_alert("warning", "SmokeWarningSlack"),
        ])
        time.sleep(8)

        with received_slack_lock:
            seen = list(received_slack)

        results: list[bool] = [
            expect(f"got {len(seen)} slack deliveries (expected >=2)", len(seen) >= 2),
        ]
        for r in seen:
            raw = r["raw"]
            results.append(expect(
                f"[{r['path']}] body contains runbook URL",
                "SMOKE_TEST.md" in raw and "github.com/blackbeardONE/QSDM" in raw,
            ))
            results.append(expect(
                f"[{r['path']}] body contains dashboard URL",
                "qsdm-runbook-smoke-test.json" in raw,
            ))
            results.append(expect(
                f"[{r['path']}] rendered *Runbook*: link",
                "*Runbook*:" in raw,
            ))
            results.append(expect(
                f"[{r['path']}] rendered *Dashboard*: link",
                "*Dashboard*:" in raw,
            ))
            payload = r["payload"]
            attachments = payload.get("attachments", [])
            if attachments:
                actions = attachments[0].get("actions", [])
                results.append(expect(
                    f"[{r['path']}] payload has 2 buttons",
                    len(actions) == 2,
                ))
                if len(actions) >= 2:
                    btn_urls = [a.get("url", "") for a in actions]
                    results.append(expect(
                        f"[{r['path']}] buttons carry resolved URLs",
                        all(u.startswith("https://github.com/") for u in btn_urls),
                    ))

        return results, ""
    finally:
        proc.terminate()
        try:
            stdout, stderr = proc.communicate(timeout=5)
            if not stderr_tail and stderr:
                stderr_tail = stderr.decode(errors="replace")[-1500:]
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.communicate()
        server.shutdown()
        shutil.rmtree(tmp, ignore_errors=True)


# ---------------------------------------------------------------
# Driver
# ---------------------------------------------------------------


def main() -> int:
    am_bin = find_alertmanager()
    if am_bin is None:
        print("alertmanager not found on PATH (and $ALERTMANAGER unset).")
        print("Skipping smoke test. Install from:")
        print("  https://github.com/prometheus/alertmanager/releases")
        return 0

    print(f"Using alertmanager binary: {am_bin}")
    print()
    print("--- Phase 1: webhook routing + commonAnnotations ---")
    p1_results, p1_tail = run_phase1(am_bin)

    print()
    print("--- Phase 2: slack_configs template rendering ---")
    p2_results, p2_tail = run_phase2(am_bin)

    results = p1_results + p2_results
    failed = [r for r in results if not r]

    print()
    if not failed:
        print(f"OK  {len(results)}/{len(results)} smoke checks passed.")
        return 0

    print(f"FAIL  {len(failed)}/{len(results)} smoke checks failed.")
    if p1_tail or p2_tail:
        print()
        print("--- alertmanager stderr (tail) ---")
        for tail in (p1_tail, p2_tail):
            for line in tail.splitlines()[-30:]:
                print(line)
    return 1


if __name__ == "__main__":
    sys.exit(main())
