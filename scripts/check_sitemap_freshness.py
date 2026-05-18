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

Two modes:

    --mode online (default)
        HEAD each <url> against `--origin`; compare sitemap <lastmod>
        to served Last-Modified. The post-deploy verification mode;
        catches drift between what's deployed and what crawlers will
        be told. Requires outbound network reach to the origin.

    --mode offline
        For each <url>, map it to the source file in
        `QSDM/deploy/landing/` (URL convention: `/` -> `index.html`,
        `/foo/` -> `foo/index.html`, `/foo.html` -> `foo.html`,
        nested paths preserved verbatim) and use
        `git log -1 --format=%cI -- <path>` as the source-of-truth
        last-modified date. Designed for CI: deterministic,
        no outbound network, runs in a clean git checkout. Maps
        cleanly to the same sitemap >= source contract because the
        source-of-truth becomes the file's git-history committer
        date instead of Caddy's served Last-Modified.

        A divergence between modes is informational, not a failure:
        operators can use `touch(1)` on the served file to rotate
        Caddy's Last-Modified forward without changing git content
        (e.g. ops's 29bbdff for /docs/), which makes the served
        date strictly newer than the git date. The sitemap-vs-source
        contract is satisfied as long as sitemap >= source under
        whichever backend is providing "source".

Designed for operator verification post-deploy (online mode) and
for periodic CI cron runs (offline mode). Same script also doubles
as evidence for audit row infra-05 (sitemap lastmod freshness
contract). Online and offline modes can both be run from the same
checkout against the same sitemap — they are independent strict
checks against different ground truths.

Stdlib-only by deliberate choice — mirrors check_runbook_coverage.py
and avoids adding a `requests` dependency for a script that does one
HEAD or one `git log` per URL.
"""

from __future__ import annotations

import argparse
import subprocess
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
LANDING_DIR_RELPATH = Path("QSDM/deploy/landing")
DEFAULT_ORIGIN = "https://qsdm.tech"
HEAD_TIMEOUT_SECONDS = 10
GIT_TIMEOUT_SECONDS = 10
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


def url_to_repo_path(loc: str, repo: Path, landing_dir: Path) -> Path:
    """Map a sitemap <loc> URL to its source file in the repo.

    Convention is intentionally simple and 1-to-1 with how Caddy
    serves files out of QSDM/deploy/landing/ on BLR1:

        /                   -> landing_dir/index.html
        /foo.html           -> landing_dir/foo.html
        /foo/               -> landing_dir/foo/index.html
        /a/b.txt            -> landing_dir/a/b.txt

    The mapping is path-only; we have already validated origin in
    url_path_from_loc() before calling this. No URL-decoding because
    sitemap <loc>s in this project are all ASCII.

    Returns the absolute Path; existence is NOT checked here (the
    caller decides whether a missing source is a failure or a skip,
    same as how the online mode handles non-200).
    """
    parsed = urlparse(loc)
    path = parsed.path or "/"
    rel = path.lstrip("/")
    if path.endswith("/") or path == "":
        rel = rel + "index.html"
    return (repo / landing_dir / rel).resolve()


def git_last_committed_date(
    repo: Path, file_path: Path, timeout: float = GIT_TIMEOUT_SECONDS
) -> Tuple[Optional[date], str]:
    """Return (date, raw_iso_string) for the last commit touching `file_path`.

    Uses `git log -1 --format=%cI -- <path>`. The %cI format is
    committer ISO 8601 with the committer's timezone, e.g.
    "2026-05-18T20:23:21+08:00"; we reduce to date for comparison
    with the sitemap <lastmod>.

    Returns (None, "") when the file is untracked or has never been
    committed (git log produces empty output, exit 0). The caller
    treats this the same way the online mode treats 4xx — skip,
    don't fail.

    Raises RuntimeError on git invocation failure (binary missing,
    repo not a git checkout, etc) — the caller treats this as a
    setup error and exits with code 2.
    """
    try:
        rel = file_path.relative_to(repo)
    except ValueError:
        rel = file_path
    try:
        result = subprocess.run(
            ["git", "log", "-1", "--format=%cI", "--", str(rel)],
            capture_output=True,
            text=True,
            cwd=str(repo),
            timeout=timeout,
            check=False,
        )
    except (FileNotFoundError, subprocess.TimeoutExpired) as e:
        raise RuntimeError(
            f"git log invocation failed for {rel}: {e}"
        ) from e
    if result.returncode != 0:
        raise RuntimeError(
            f"git log returned exit {result.returncode} for {rel}: "
            f"{result.stderr.strip() or result.stdout.strip()}"
        )
    raw = result.stdout.strip()
    if not raw:
        return (None, "")
    try:
        return (datetime.fromisoformat(raw).date(), raw)
    except ValueError as e:
        raise RuntimeError(
            f"git log returned invalid ISO date for {rel}: {raw!r} ({e})"
        ) from e


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
        "--mode",
        choices=("online", "offline"),
        default="online",
        help=(
            "Source-of-truth backend. 'online' (default) issues HEAD "
            "requests against --origin; 'offline' uses git log against "
            "the source files under QSDM/deploy/landing/ (CI-friendly, "
            "no network)."
        ),
    )
    parser.add_argument(
        "--origin",
        default=DEFAULT_ORIGIN,
        help=(
            f"Live web origin to HEAD against in --mode online "
            f"(default: {DEFAULT_ORIGIN}). Ignored in --mode offline "
            f"except for validating that <loc> entries match it."
        ),
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
        help=(
            f"HEAD request timeout in seconds (default: "
            f"{HEAD_TIMEOUT_SECONDS}); also used for `git log` invocation "
            f"in --mode offline."
        ),
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
        print(f"==> Mode:    {args.mode}")
        if args.mode == "online":
            print(f"==> Origin:  {args.origin}")
        else:
            print(f"==> Source:  git log on files under {LANDING_DIR_RELPATH}")
        print(f"==> URLs:    {len(entries)}")
        print()

    violations: List[str] = []
    skipped_no_header: List[str] = []
    skipped_non200: List[Tuple[str, int]] = []
    skipped_no_source: List[str] = []
    skipped_untracked: List[str] = []
    landing_dir = repo / LANDING_DIR_RELPATH

    for loc, lm_date in entries:
        try:
            url_path_from_loc(loc, args.origin)
        except ValueError as e:
            print(f"ERROR: {e}", file=sys.stderr)
            return 2

        if args.mode == "online":
            served_date, raw, status = _online_lookup(
                loc, args.origin, args.timeout
            )
            if served_date == "_setup_error":
                # _online_lookup printed the error itself.
                return 2
            if status is not None and status != 200:
                skipped_non200.append((loc, status))
                if not args.quiet:
                    print(
                        f"  skip {loc}  (HTTP {status}; not asserting freshness)"
                    )
                continue
            if served_date is None:
                skipped_no_header.append(loc)
                if not args.quiet:
                    print(
                        f"  skip {loc}  (no Last-Modified header on served response)"
                    )
                continue
            comparator_label = "served Last-Modified"
            comparator_short = "served"
            served_repr = served_date.isoformat()
            raw_repr = raw or served_repr
        else:  # offline
            source_path = url_to_repo_path(loc, repo, LANDING_DIR_RELPATH)
            if not source_path.is_file():
                skipped_no_source.append(loc)
                if not args.quiet:
                    print(
                        f"  skip {loc}  (no source file at "
                        f"{source_path.relative_to(repo)}; "
                        f"not asserting freshness)"
                    )
                continue
            try:
                served_date, raw = git_last_committed_date(
                    repo, source_path, timeout=args.timeout
                )
            except RuntimeError as e:
                print(f"ERROR: {e}", file=sys.stderr)
                return 2
            if served_date is None:
                skipped_untracked.append(loc)
                if not args.quiet:
                    print(
                        f"  skip {loc}  (source file "
                        f"{source_path.relative_to(repo)} is untracked / "
                        f"never committed)"
                    )
                continue
            comparator_label = "git last-committed date"
            comparator_short = "git"
            served_repr = served_date.isoformat()
            raw_repr = raw

        if lm_date < served_date:
            msg = (
                f"{loc}: sitemap <lastmod>={lm_date.isoformat()} is OLDER than "
                f"{comparator_label}={served_repr} "
                f"(raw: {raw_repr!r}). Bump the sitemap entry to "
                f"{served_repr} (or later) in the same commit "
                f"that touched the underlying file."
            )
            violations.append(msg)
            print(f"  FAIL {msg}", file=sys.stderr)
            continue

        if not args.quiet:
            tag = "==" if lm_date == served_date else ">"
            print(
                f"  ok   {loc}  (sitemap={lm_date.isoformat()} {tag} "
                f"{comparator_short}={served_repr})"
            )

    print()
    if violations:
        print(
            f"FAIL: {len(violations)} sitemap entr{'y' if len(violations)==1 else 'ies'} "
            f"older than source-of-truth across "
            f"{len(entries)} total URL(s) (mode={args.mode})",
            file=sys.stderr,
        )
        return 1

    skipped_total = (
        len(skipped_no_header)
        + len(skipped_non200)
        + len(skipped_no_source)
        + len(skipped_untracked)
    )
    passed = len(entries) - skipped_total
    summary = (
        f"OK: {passed}/{len(entries)} URL(s) pass the freshness contract "
        f"(mode={args.mode})"
    )
    if skipped_no_header:
        summary += f"; {len(skipped_no_header)} skipped (no Last-Modified)"
    if skipped_non200:
        summary += f"; {len(skipped_non200)} skipped (non-200)"
    if skipped_no_source:
        summary += f"; {len(skipped_no_source)} skipped (no source file)"
    if skipped_untracked:
        summary += f"; {len(skipped_untracked)} skipped (source untracked)"
    print(summary)
    return 0


def _online_lookup(
    loc: str, origin: str, timeout: float
) -> Tuple[Optional[date], str, Optional[int]]:
    """Online-mode wrapper around head_last_modified.

    Returns (served_date, raw_header, http_status). On URLError
    (network failure), prints to stderr and returns
    ("_setup_error", "", None) — the sentinel string is checked by
    the caller to short-circuit to exit code 2. We deliberately
    don't raise here because main() owns the I/O reporting.
    """
    parsed = urlparse(loc)
    path = parsed.path or "/"
    try:
        status, served_date, raw = head_last_modified(
            origin, path, timeout=timeout
        )
    except URLError as e:
        print(
            f"ERROR: HEAD {loc} failed: {e.reason}",
            file=sys.stderr,
        )
        return ("_setup_error", "", None)  # type: ignore[return-value]
    return (served_date, raw, status)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
