#!/usr/bin/env python3
"""Maintain the deduplicated GitHub incident for Hive release-feed health."""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import re
import subprocess
import sys
from collections.abc import Callable, Sequence


INCIDENT_TITLE = "[Production] QSDM Hive updater feed unhealthy"
INCIDENT_LABEL = "production-incident"
REPOSITORY_RE = re.compile(r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")
GhRunner = Callable[[Sequence[str]], str]


class IncidentReportError(RuntimeError):
    """Raised when incident state cannot be read or reconciled."""


def _run_gh(arguments: Sequence[str]) -> str:
    try:
        completed = subprocess.run(
            ["gh", *arguments],
            check=True,
            capture_output=True,
            text=True,
        )
    except (OSError, subprocess.CalledProcessError) as error:
        detail = getattr(error, "stderr", "") or str(error)
        raise IncidentReportError(f"GitHub CLI failed: {detail.strip()}") from error
    return completed.stdout


def _find_incident(repository: str, run_gh: GhRunner) -> int | None:
    output = run_gh(
        [
            "issue",
            "list",
            "--repo",
            repository,
            "--state",
            "open",
            "--label",
            INCIDENT_LABEL,
            "--limit",
            "100",
            "--json",
            "number,title",
        ]
    )
    try:
        issues = json.loads(output)
    except json.JSONDecodeError as error:
        raise IncidentReportError("GitHub CLI returned invalid issue JSON") from error
    if not isinstance(issues, list):
        raise IncidentReportError("GitHub CLI issue response was not a list")

    for issue in issues:
        if (
            isinstance(issue, dict)
            and issue.get("title") == INCIDENT_TITLE
            and isinstance(issue.get("number"), int)
        ):
            return issue["number"]
    return None


def reconcile_incident(
    *,
    result: str,
    repository: str,
    run_url: str,
    run_gh: GhRunner = _run_gh,
    checked_at: str | None = None,
) -> str:
    if result not in {"success", "failure"}:
        return "ignored"
    if not REPOSITORY_RE.fullmatch(repository):
        raise IncidentReportError("repository must use owner/name format")
    if not run_url.startswith("https://github.com/"):
        raise IncidentReportError("run URL must be an HTTPS github.com URL")

    run_gh(
        [
            "label",
            "create",
            INCIDENT_LABEL,
            "--repo",
            repository,
            "--color",
            "B60205",
            "--description",
            "Automated production service incident",
            "--force",
        ]
    )
    issue_number = _find_incident(repository, run_gh)
    checked_at = checked_at or dt.datetime.now(dt.timezone.utc).strftime(
        "%Y-%m-%dT%H:%M:%SZ"
    )

    if result == "failure":
        message = (
            f"Production updater-feed verification failed at {checked_at}.\n\n"
            f"Run: {run_url}\n\n"
            "Installed Hive clients may be unable to discover or authenticate "
            "the approved release. Do not bypass the verifier; restore the "
            "approved atomic release or publish a new version."
        )
        if issue_number is None:
            run_gh(
                [
                    "issue",
                    "create",
                    "--repo",
                    repository,
                    "--title",
                    INCIDENT_TITLE,
                    "--label",
                    INCIDENT_LABEL,
                    "--body",
                    message,
                ]
            )
            return "opened"
        run_gh(
            [
                "issue",
                "comment",
                str(issue_number),
                "--repo",
                repository,
                "--body",
                message,
            ]
        )
        return "updated"

    if issue_number is None:
        return "healthy"
    run_gh(
        [
            "issue",
            "close",
            str(issue_number),
            "--repo",
            repository,
            "--reason",
            "completed",
            "--comment",
            f"Production updater-feed verification recovered at {checked_at}. "
            f"Run: {run_url}",
        ]
    )
    return "closed"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--result", required=True)
    parser.add_argument(
        "--repository", default=os.environ.get("GITHUB_REPOSITORY", "")
    )
    parser.add_argument("--run-url", required=True)
    args = parser.parse_args()

    try:
        action = reconcile_incident(
            result=args.result,
            repository=args.repository,
            run_url=args.run_url,
        )
    except IncidentReportError as error:
        print(f"Hive feed incident reporting failed: {error}", file=sys.stderr)
        return 1

    print(f"Hive feed incident action: {action}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
