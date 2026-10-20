#!/usr/bin/env python3
"""Check the QSDM producer every 15 minutes for one long job, and keep the incident issue current.

GitHub starts scheduled workflows late when it is busy: on 2026-10-20 the */15 schedule ran
about every five hours. So each scheduled run keeps watching for up to five hours and forty
minutes, and the workflow's concurrency group queues the next run behind it. The checker and
the incident reporter are the ones the one-shot job uses; the reporter is idempotent (one
issue per incident, closed on recovery), so calling it after every check is safe.

Exit 0 at the end of the window, 1 if any incident update failed.
"""

from __future__ import annotations

import argparse
import datetime as dt
import os
import sys
import time
from collections.abc import Callable

if __package__:
    from . import check_producer_health as health
    from .report_producer_incident import IncidentReportError, reconcile_incident
else:
    import check_producer_health as health
    from report_producer_incident import IncidentReportError, reconcile_incident


INTERVAL_S = 15 * 60
DURATION_S = 5 * 3600 + 40 * 60

Checker = Callable[[], "str | None"]
Reconciler = Callable[[str, str], str]


def _stamp() -> str:
    return dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def watch(
    *,
    check: Checker,
    reconcile: Reconciler,
    duration: float = DURATION_S,
    interval: float = INTERVAL_S,
    now: Callable[[], float] = time.monotonic,
    sleep: Callable[[float], None] = time.sleep,
    log: Callable[[str], None] = print,
) -> int:
    """Run check and reconcile every interval until the next run would start after duration."""
    deadline = now() + duration
    report_errors = 0
    while True:
        started = now()
        reason = check()
        result = "failure" if reason else "success"
        try:
            action = reconcile(result, reason or "healthy")
        except IncidentReportError as error:
            report_errors += 1
            action = f"report failed: {error}"
        log(f"{_stamp()} {reason or 'healthy'} -> {action}")
        next_start = started + interval
        if next_start >= deadline:
            return 1 if report_errors else 0
        sleep(max(0.0, next_start - now()))


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--duration", type=float, default=DURATION_S)
    parser.add_argument("--interval", type=float, default=INTERVAL_S)
    parser.add_argument(
        "--repository", default=os.environ.get("GITHUB_REPOSITORY", "")
    )
    parser.add_argument("--run-url", required=True)
    args = parser.parse_args()

    def reconcile(result: str, reason: str) -> str:
        return reconcile_incident(
            result=result,
            reason=reason,
            repository=args.repository,
            run_url=args.run_url,
        )

    return watch(
        check=health.check,
        reconcile=reconcile,
        duration=args.duration,
        interval=args.interval,
        log=lambda line: print(line, flush=True),
    )


if __name__ == "__main__":
    sys.exit(main())
