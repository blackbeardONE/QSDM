#!/usr/bin/env python3
"""
Sitemap lastmod freshness lint.

Enforces the contract documented in the QSDM sitemap.xml header
comment (lines 46-50):

    "the date here MUST be no older than the file's last meaningful
     content change, otherwise crawlers will skip the re-crawl and
     the change won't be re-indexed."

For each <url> in QSDM/deploy/landing/sitemap.xml, this script:

    1. Parses the declared <lastmod> date.
    2. Issues HEAD <origin><path> against the live web origin
       (default: https://qsdm.tech).
    3. Parses the served Last-Modified header into a date.
    4. Fails if the sitemap's <lastmod> is strictly older than the
       served Last-Modified date.

Plain English: if Caddy says the file at <url> was updated on
2026-05-18 but the sitemap claims it was last modified 2026-05-13,
a polite crawler (Googlebot, Bingbot) treats the sitemap as
authoritative, observes "no new content past 2026-05-13", and
skips the re-crawl. The change ships but never gets re-indexed.
This is exactly the failure mode the sitemap header comment warns
against and that the one-off ops fix in commit 6927f9b cleaned up
ad-hoc.

Acceptable states:
    sitemap <lastmod>  ==  served Last-Modified.date()    pass
    sitemap <lastmod>  >   served Last-Modified.date()    pass (the
                                                          sitemap is
                                                          "ahead"; a
                                                          crawler
                                                          re-fetches
                                                          and observes
                                                          Last-Modified
                                                          is still ≤
                                                          today)
    sitemap <lastmod>  <   served Last-Modified.date()    FAIL

Skips:
    URLs that don't return HTTP 200 are reported, not failed (a 4xx
    means the URL itself is broken and is a separate class of issue
    handled by upstream link-coverage; we don't compound the
    failure).

    URLs that respond 200 but with no Last-Modified header are
    reported, not failed (some endpoints — e.g. dynamic API badge
    SVGs — intentionally omit Last-Modified; the sitemap should
    not list those, but the lint doesn't enforce that here).

Exit codes:
    0  all URLs pass the contract
    1  any URL failed (sitemap older than served Last-Modified)
    2  argument or setup error (sitemap missing, origin unreachable,
       sitemap XML malformed)

Usage:
    python3 scripts/check_sitemap_freshness.py
    python3 scripts/check_sitemap_freshness.py --quiet
    python3 scripts/check_sitemap_freshness.py --origin https://qsdm.tech
    python3 scripts/check_sitemap_freshness.py --repo /path/to/repo

Designed for operator verification post-deploy and for periodic
CI cron runs. Same script also doubles as evidence for audit row
infra-05 (sitemap lastmod freshness contract).

Stdlib-only by deliberate choice — mirrors check_runbook_coverage.py
and avoids adding a `requests` dependency for a script that does one
HEAD per URL.
"""

from __future__ import annotations

import argparse
import sys
from datetime import date, datetime
from email.utils import parsedate_to_datetime
from pathlib import Path
from typing import List, Optional, Tuple
from urllib.error import HTTPError, URLError
from urllib.parse import urlparse
from urllib.request import Request, urlopen
from xml.etree import ElementTree as ET


REPO_ROOT_DEFAULT = Path(__file__).resolve().parent.parent
SITEMAP_RELPATH = Path("QSDM/deploy/landing/sitemap.xml")
DEFAULT_ORIGIN = "https://qsdm.tech"
HEAD_TIMEOUT_SECONDS = 10
USER_AGENT = "QSDM-sitemap-freshness-lint/1.0 (+https://qsdm.tech/.well-known/security.txt)"

SITEMAP_NS = "{http://www.sitemaps.org/schemas/sitemap/0.9}"


def parse_sitemap(path: Path) -> List[Tuple[str, date]]:
    """Return [(loc, lastmod_date), ...] for every <url> in the sitemap.

    Raises ValueError on malformed XML or missing required fields. The
    sitemap spec allows <lastmod> to be in W3C Datetime format (full
    timestamp or date-only); we accept both and reduce to date.
    """
    try:
        tree = ET.parse(path)
    except ET.ParseError as e:
        raise ValueError(f"sitemap XML malformed: {e}") from e
    root = tree.getroot()
    out: List[Tuple[str, date]] = []
    for url_el in root.findall(f"{SITEMAP_NS}url"):
        loc_el = url_el.find(f"{SITEMAP_NS}loc")
        lm_el = url_el.find(f"{SITEMAP_NS}lastmod")
        if loc_el is None or not (loc_el.text or "").strip():
            raise ValueError("sitemap contains a <url> without a <loc>")
        if lm_el is None or not (lm_el.text or "").strip():
            raise ValueError(
                f"sitemap <url> {loc_el.text!r} is missing <lastmod>"
            )
        loc = loc_el.text.strip()
        raw = lm_el.text.strip()
        try:
            if "T" in raw:
                lm_date = datetime.fromisoformat(raw.replace("Z", "+00:00")).date()
            else:
                lm_date = date.fromisoformat(raw)
        except ValueError as e:
            raise ValueError(
                f"sitemap <lastmod> for {loc!r} is not a valid W3C Datetime: "
                f"{raw!r} ({e})"
            ) from e
        out.append((loc, lm_date))
    return out


def url_path_from_loc(loc: str, expected_origin: str) -> str:
    """Reduce an absolute <loc> to its path component, validating the origin.

    The sitemap spec requires absolute URLs in <loc>, but the lint runs
    against a single origin at a time. If a <loc> points at a different
    origin (e.g. a CDN host), it's flagged here as a setup error rather
    than silently skipped.
    """
    parsed = urlparse(loc)
    expected = urlparse(expected_origin)
    if parsed.scheme != expected.scheme or parsed.netloc != expected.netloc:
        raise ValueError(
            f"<loc> {loc!r} does not match --origin {expected_origin!r}; "
            f"re-run with --origin {parsed.scheme}://{parsed.netloc}"
        )
    path = parsed.path or "/"
    return path


def head_last_modified(
    origin: str, path: str, timeout: float = HEAD_TIMEOUT_SECONDS
) -> Tuple[int, Optional[date], str]:
    """Issue HEAD <origin><path> and return (status, last_modified_date, raw_header).

    Returns (status, None, "") if no Last-Modified header is present.
    Raises URLError on network failure (the caller treats this as a
    setup error and exits with code 2 rather than 1).
    """
    url = origin.rstrip("/") + path
    req = Request(url, method="HEAD", headers={"User-Agent": USER_AGENT})
    try:
        with urlopen(req, timeout=timeout) as resp:
            status = resp.status
            raw = resp.headers.get("Last-Modified", "")
    except HTTPError as e:
        # 4xx/5xx: still return the status so the caller can decide.
        status = e.code
        raw = e.headers.get("Last-Modified", "") if e.headers else ""
    if not raw:
        return (status, None, "")
    try:
        lm = parsedate_to_datetime(raw)
    except (TypeError, ValueError):
        return (status, None, raw)
    return (status, lm.date(), raw)


def main(argv: List[str]) -> int:
    parser = argparse.ArgumentParser(
        description="QSDM sitemap lastmod freshness lint",
    )
    parser.add_argument(
        "--repo",
        type=Path,
        default=REPO_ROOT_DEFAULT,
        help="Repository root (default: parent of script's directory)",
    )
    parser.add_argument(
        "--origin",
        default=DEFAULT_ORIGIN,
        help=f"Live web origin to HEAD against (default: {DEFAULT_ORIGIN})",
    )
    parser.add_argument(
        "--quiet",
        action="store_true",
        help="Suppress per-URL success lines; only print summary + violations",
    )
    parser.add_argument(
        "--timeout",
        type=float,
        default=HEAD_TIMEOUT_SECONDS,
        help=f"HEAD request timeout in seconds (default: {HEAD_TIMEOUT_SECONDS})",
    )
    args = parser.parse_args(argv)

    repo = args.repo.resolve()
    sitemap_path = repo / SITEMAP_RELPATH

    if not sitemap_path.is_file():
        print(f"ERROR: sitemap not found: {sitemap_path}", file=sys.stderr)
        return 2

    try:
        entries = parse_sitemap(sitemap_path)
    except ValueError as e:
        print(f"ERROR: {e}", file=sys.stderr)
        return 2

    if not args.quiet:
        print(f"==> Sitemap: {sitemap_path.relative_to(repo)}")
        print(f"==> Origin:  {args.origin}")
        print(f"==> URLs:    {len(entries)}")
        print()

    violations: List[str] = []
    skipped_no_header: List[str] = []
    skipped_non200: List[Tuple[str, int]] = []

    for loc, lm_date in entries:
        try:
            path = url_path_from_loc(loc, args.origin)
        except ValueError as e:
            print(f"ERROR: {e}", file=sys.stderr)
            return 2

        try:
            status, served_date, raw = head_last_modified(
                args.origin, path, timeout=args.timeout
            )
        except URLError as e:
            print(
                f"ERROR: HEAD {loc} failed: {e.reason}",
                file=sys.stderr,
            )
            return 2

        if status != 200:
            skipped_non200.append((loc, status))
            if not args.quiet:
                print(f"  skip {loc}  (HTTP {status}; not asserting freshness)")
            continue

        if served_date is None:
            skipped_no_header.append(loc)
            if not args.quiet:
                print(
                    f"  skip {loc}  (no Last-Modified header on served response)"
                )
            continue

        if lm_date < served_date:
            msg = (
                f"{loc}: sitemap <lastmod>={lm_date.isoformat()} is OLDER than "
                f"served Last-Modified={served_date.isoformat()} "
                f"(raw: {raw!r}). Bump the sitemap entry to "
                f"{served_date.isoformat()} (or later) in the same commit "
                f"that touched the underlying file."
            )
            violations.append(msg)
            print(f"  FAIL {msg}", file=sys.stderr)
            continue

        if not args.quiet:
            tag = "==" if lm_date == served_date else ">"
            print(
                f"  ok   {loc}  (sitemap={lm_date.isoformat()} {tag} "
                f"served={served_date.isoformat()})"
            )

    print()
    if violations:
        print(
            f"FAIL: {len(violations)} sitemap entr{'y' if len(violations)==1 else 'ies'} "
            f"older than served Last-Modified across "
            f"{len(entries)} total URL(s)",
            file=sys.stderr,
        )
        return 1

    passed = (
        len(entries) - len(skipped_no_header) - len(skipped_non200)
    )
    summary = (
        f"OK: {passed}/{len(entries)} URL(s) pass the freshness contract"
    )
    if skipped_no_header:
        summary += f"; {len(skipped_no_header)} skipped (no Last-Modified)"
    if skipped_non200:
        summary += f"; {len(skipped_non200)} skipped (non-200)"
    print(summary)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
