#!/usr/bin/env python3
"""
Runbook coverage lint.

Enforces three invariants on every push/PR that
touches QSDM/deploy/ or this script:

  1. Every Prometheus alert in
     QSDM/deploy/prometheus/alerts_qsdm.example.yml
     carries a non-empty `runbook_url` annotation.
  2. Every `runbook_url` resolves to an existing
     markdown file under QSDM/docs/docs/runbooks/.
  3. Every `runbook_url` anchor (the `#fragment`
     part) exists in its target markdown file.
     Anchors are computed from each markdown
     heading using GitHub's slug rules.

Exit codes:
  0  all invariants hold
  1  any invariant violated (CI failure)
  2  argument or setup error (uncommon)

Usage:
  python3 scripts/check_runbook_coverage.py
  python3 scripts/check_runbook_coverage.py --quiet
  python3 scripts/check_runbook_coverage.py --repo /path/to/repo

Designed for CI; prints a human-friendly per-violation
trace by default and a single summary line at the end.
The --quiet mode is useful for pre-commit hooks where
the user only cares about pass/fail.

GitHub anchor-slug rules (verified empirically against
existing anchors in the runbook directory):
  - lowercase
  - drop characters that aren't [a-z0-9 \\- _]
    (so periods and backticks both vanish)
  - convert spaces to '-'
  - leave consecutive hyphens collapsed AS-IS
    (this matters for headings like
    `### 3.1 Mode A — \\`QSDMFoo\\`` which becomes
    "31-mode-a--qsdmfoo" — the double-hyphen is
    intentional and matches GitHub's renderer)
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path
from typing import Dict, Iterable, List, Set, Tuple
from urllib.parse import urlparse

try:
    import yaml
except ImportError:
    print(
        "ERROR: PyYAML not installed. Install with: pip install PyYAML",
        file=sys.stderr,
    )
    sys.exit(2)


REPO_ROOT_DEFAULT = Path(__file__).resolve().parent.parent
ALERTS_RELPATH = Path("QSDM/deploy/prometheus/alerts_qsdm.example.yml")
RUNBOOKS_RELDIR = Path("QSDM/docs/docs/runbooks")
RUNBOOK_URL_PREFIX_GITHUB = "https://github.com/"


_HEADING_RE = re.compile(r"^(#{1,6})\s+(.+?)\s*$")


def slugify_github(heading_text: str) -> str:
    """Compute the GitHub-flavoured anchor slug for a heading.

    GitHub's renderer:
      1. lowercases
      2. strips characters not in [a-z0-9 _-]
         (after lowercasing); spaces left alone
      3. converts internal spaces to '-'
      4. preserves consecutive hyphens
    The em-dash (U+2014) used in QSDM headings ("Mode A — `Foo`")
    is dropped at step 2; the surrounding spaces collapse to '--'
    at step 3, which is the canonical anchor pattern in the
    runbook tree.
    """
    s = heading_text.lower()
    out = []
    for ch in s:
        if ch.isalnum() or ch in "-_ ":
            out.append(ch)
    s = "".join(out)
    s = s.replace(" ", "-")
    return s


def collect_anchors(md_path: Path) -> Set[str]:
    """Return the set of anchor slugs reachable in this markdown file."""
    text = md_path.read_text(encoding="utf-8")
    anchors: Set[str] = set()
    for line in text.splitlines():
        m = _HEADING_RE.match(line)
        if not m:
            continue
        heading = m.group(2)
        anchors.add(slugify_github(heading))
    return anchors


def parse_alerts(path: Path) -> List[Tuple[str, str, str]]:
    """Load alerts file and return [(group, alert_name, runbook_url), ...]."""
    with path.open("r", encoding="utf-8") as f:
        data = yaml.safe_load(f)
    out: List[Tuple[str, str, str]] = []
    for group in data.get("groups", []) or []:
        gname = group.get("name", "<unnamed-group>")
        for rule in group.get("rules", []) or []:
            if "alert" not in rule:
                continue
            ann = rule.get("annotations") or {}
            url = ann.get("runbook_url", "") or ""
            out.append((gname, rule["alert"], url))
    return out


def parse_runbook_url(url: str) -> Tuple[str, str]:
    """Extract (filename, anchor) from a GitHub blob URL.

    Returns ("", "") if the URL doesn't match the
    canonical shape (which is itself a violation).
    """
    if not url.startswith(RUNBOOK_URL_PREFIX_GITHUB):
        return ("", "")
    parsed = urlparse(url)
    path_parts = parsed.path.split("/runbooks/", 1)
    if len(path_parts) != 2:
        return ("", "")
    filename = path_parts[1]
    anchor = parsed.fragment
    return (filename, anchor)


def main(argv: List[str]) -> int:
    parser = argparse.ArgumentParser(
        description="QSDM runbook coverage lint",
    )
    parser.add_argument(
        "--repo",
        type=Path,
        default=REPO_ROOT_DEFAULT,
        help="Repository root (default: parent of script's directory)",
    )
    parser.add_argument(
        "--quiet",
        action="store_true",
        help="Suppress per-alert success lines; only print summary + violations",
    )
    args = parser.parse_args(argv)

    repo = args.repo.resolve()
    alerts_path = repo / ALERTS_RELPATH
    runbooks_dir = repo / RUNBOOKS_RELDIR

    if not alerts_path.is_file():
        print(f"ERROR: alerts file not found: {alerts_path}", file=sys.stderr)
        return 2
    if not runbooks_dir.is_dir():
        print(
            f"ERROR: runbooks directory not found: {runbooks_dir}",
            file=sys.stderr,
        )
        return 2

    alerts = parse_alerts(alerts_path)

    if not args.quiet:
        print(f"==> Alerts file: {alerts_path.relative_to(repo)}")
        print(f"==> Runbooks dir: {runbooks_dir.relative_to(repo)}")
        print(f"==> Total alerts: {len(alerts)}")
        print()

    # Cache anchor sets per runbook file (read each at most once).
    anchor_cache: Dict[Path, Set[str]] = {}

    def anchors_for(p: Path) -> Set[str]:
        if p not in anchor_cache:
            anchor_cache[p] = collect_anchors(p)
        return anchor_cache[p]

    violations: List[str] = []

    def fail(msg: str) -> None:
        violations.append(msg)
        print(f"  FAIL: {msg}", file=sys.stderr)

    for gname, alert_name, url in alerts:
        if not url:
            fail(
                f"[{gname}] alert {alert_name!r} has no runbook_url annotation"
            )
            continue

        filename, anchor = parse_runbook_url(url)
        if not filename:
            fail(
                f"[{gname}] alert {alert_name!r} has runbook_url that isn't a "
                f"github.com /runbooks/ URL: {url!r}"
            )
            continue

        runbook_path = runbooks_dir / filename
        if not runbook_path.is_file():
            fail(
                f"[{gname}] alert {alert_name!r} runbook_url points at missing "
                f"file: {filename!r} (resolved to {runbook_path})"
            )
            continue

        if not anchor:
            fail(
                f"[{gname}] alert {alert_name!r} runbook_url is missing the "
                f"#anchor fragment: {url!r}"
            )
            continue

        existing = anchors_for(runbook_path)
        if anchor not in existing:
            fail(
                f"[{gname}] alert {alert_name!r} anchor #{anchor} not found in "
                f"{filename}; available anchors include: "
                f"{sorted(a for a in existing if a.startswith(anchor[:3]))}"
            )
            continue

        if not args.quiet:
            print(f"  ok   {alert_name}  ->  {filename}#{anchor}")

    print()
    if violations:
        print(
            f"FAIL: {len(violations)} violation(s) across {len(alerts)} alert(s)",
            file=sys.stderr,
        )
        return 1

    print(f"OK: {len(alerts)}/{len(alerts)} alerts have resolvable runbook_url anchors")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
