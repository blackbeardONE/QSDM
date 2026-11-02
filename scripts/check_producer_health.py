#!/usr/bin/env python3
"""Check that the QSDM producer serves the public API, extends the chain and has mining open.

Exit 0 when healthy. Exit 1 after three failed attempts, one minute apart; the reason is
printed and, with --github-output, written as `reason=...` for the incident job.
"""

from __future__ import annotations

import argparse
import json
import sys
import time
import urllib.error
import urllib.request
from collections.abc import Callable
from dataclasses import dataclass


API = "https://api.qsdm.tech/api/v1"
USER_AGENT = "qsdm-producer-health-check/1"
ATTEMPTS = 3
ATTEMPT_PAUSE_S = 60
# Blocks are about 10 s apart, so the tip must move within this window.
TIP_WAIT_S = 40


@dataclass(frozen=True)
class Response:
    status: int  # 0 when the request failed (timeout, DNS, TLS, connection)
    body: bytes


Fetcher = Callable[[str], Response]
Sleeper = Callable[[float], None]


def fetch(url: str) -> Response:
    request = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})
    try:
        with urllib.request.urlopen(request, timeout=20) as reply:
            return Response(reply.status, reply.read(1 << 20))
    except urllib.error.HTTPError as error:
        return Response(error.code, b"")
    except (urllib.error.URLError, OSError, TimeoutError):
        return Response(0, b"")


def chain_tip(response: Response) -> int | None:
    if response.status != 200:
        return None
    try:
        tip = json.loads(response.body).get("chain_tip")
    except (ValueError, AttributeError):
        return None
    if isinstance(tip, bool) or not isinstance(tip, int):
        return None
    return tip


def check_once(fetch: Fetcher, sleep: Sleeper) -> str | None:
    """Return None when healthy, else a short reason."""
    first = fetch(f"{API}/status")
    tip1 = chain_tip(first)
    if tip1 is None:
        return f"API status unreachable or invalid (HTTP {first.status})"
    sleep(TIP_WAIT_S)
    second = fetch(f"{API}/status")
    tip2 = chain_tip(second)
    if tip2 is None:
        return f"API status failed on the second read (HTTP {second.status})"
    if tip2 <= tip1:
        return f"chain not advancing (tip {tip1} -> {tip2} in {TIP_WAIT_S} s)"
    work = fetch(f"{API}/mining/work")
    if work.status != 200:
        return f"mining closed (GET /mining/work HTTP {work.status}, chain at {tip2})"
    return None


def check(
    fetch: Fetcher = fetch, sleep: Sleeper = time.sleep, attempts: int = ATTEMPTS
) -> str | None:
    reason: str | None = None
    for attempt in range(1, attempts + 1):
        reason = check_once(fetch, sleep)
        print(f"attempt {attempt}: {reason or 'healthy'}")
        if reason is None:
            return None
        if attempt < attempts:
            sleep(ATTEMPT_PAUSE_S)
    return reason


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--github-output", default="")
    args = parser.parse_args()

    reason = check()
    if args.github_output:
        with open(args.github_output, "a", encoding="utf-8") as output:
            output.write(f"reason={reason or 'healthy'}\n")
    if reason:
        print(f"QSDM producer unhealthy: {reason}", file=sys.stderr)
        return 1
    print("QSDM producer healthy")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
