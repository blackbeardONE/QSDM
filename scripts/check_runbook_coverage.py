#!/usr/bin/env python3
"""
Runbook coverage lint.

Enforces six invariants on every push/PR that
touches QSDM/deploy/, QSDM/docs/docs/runbooks/, or
this script. The first three protect the alerts ↔
runbooks pair; the next three protect the in-runbook
navigation mesh (cross-runbook links + source-file
references + intra-file anchors).

Alerts ↔ runbooks invariants:

  1. Every Prometheus alert in
     QSDM/deploy/prometheus/alerts_qsdm.example.yml
     carries a non-empty `runbook_url` annotation.
  2. Every `runbook_url` resolves to an existing
     markdown file under QSDM/docs/docs/runbooks/.
  3. Every `runbook_url` anchor (the `#fragment`
     part) exists in its target markdown file.
     Anchors are computed from each markdown
     heading using GitHub's slug rules.

In-runbook link invariants:

  4. Every relative `[text](OTHER.md)` cross-runbook
     link in any runbook resolves to an existing
     markdown file.
  5. Every `[text](OTHER.md#anchor)` or
     `[text](#anchor)` anchor target exists as a
     heading in the target file (intra-file anchors
     are checked against the same file's headings).
  6. Every `[text](../path/to/source.go)` source-
     file reference in any runbook resolves to an
     existing path under the repo root. This covers
     references to Go source files, deploy
     manifests, scripts, and other repo artifacts.

External links (http://, https://, mailto:) are
always skipped because the lint is offline-only.
Links inside fenced code blocks (```...```) are
skipped because they're documentation-of-syntax, not
navigation.

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
_LINK_RE = re.compile(r"\[([^\]]+)\]\(([^)]+)\)")
_FENCE_RE = re.compile(r"^\s*```")
# Inline code spans: `...` (single backtick) or ``...`` (double, for code
# containing single backticks). Greedy-but-shortest non-newline content
# between matching backtick runs.
_INLINE_CODE_RE = re.compile(r"(`+)(?:(?!\1).)+\1")
_EXTERNAL_PREFIXES = ("http://", "https://", "mailto:")


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
    in_fence = False
    for line in text.splitlines():
        if _FENCE_RE.match(line):
            in_fence = not in_fence
            continue
        if in_fence:
            continue
        m = _HEADING_RE.match(line)
        if not m:
            continue
        heading = m.group(2)
        anchors.add(slugify_github(heading))
    return anchors


def _mask_inline_code(line: str) -> str:
    """Replace inline-code spans with same-length placeholders so the link
    regex doesn't match `[example](syntax)` shown inside backticks (e.g.
    in this script's own README §5 documenting the link syntax itself).
    """
    return _INLINE_CODE_RE.sub(lambda m: " " * len(m.group(0)), line)


def extract_links(md_path: Path) -> List[Tuple[int, str, str]]:
    """Return [(line_number, label, target), ...] for every markdown link.

    Skips:
      * fenced code blocks (``` ... ```) — entire blocks ignored
      * inline code spans (`...` or ``...``) — masked per-line before
        the link regex runs, so `[demo](syntax)` shown inside backticks
        for documentation-of-syntax purposes is not treated as
        navigation.

    Doesn't try to handle reference-style links (`[label][ref]`) because
    the runbook tree doesn't use them; if introduced, they'd be silently
    skipped (false negative, not a coverage regression).
    """
    out: List[Tuple[int, str, str]] = []
    in_fence = False
    text = md_path.read_text(encoding="utf-8")
    for lineno, line in enumerate(text.splitlines(), start=1):
        if _FENCE_RE.match(line):
            in_fence = not in_fence
            continue
        if in_fence:
            continue
        cleaned = _mask_inline_code(line)
        for m in _LINK_RE.finditer(cleaned):
            label = m.group(1)
            target = m.group(2).strip()
            out.append((lineno, label, target))
    return out


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
    runbook_files = sorted(runbooks_dir.glob("*.md"))

    if not args.quiet:
        print(f"==> Alerts file: {alerts_path.relative_to(repo)}")
        print(f"==> Runbooks dir: {runbooks_dir.relative_to(repo)}")
        print(f"==> Total alerts: {len(alerts)}")
        print(f"==> Total runbook files: {len(runbook_files)}")
        print()

    # Cache anchor sets per markdown file (read each at most once).
    anchor_cache: Dict[Path, Set[str]] = {}

    def anchors_for(p: Path) -> Set[str]:
        if p not in anchor_cache:
            anchor_cache[p] = collect_anchors(p)
        return anchor_cache[p]

    violations: List[str] = []

    def fail(msg: str) -> None:
        violations.append(msg)
        print(f"  FAIL: {msg}", file=sys.stderr)

    # ------------------------------------------------------------------
    # Pass 1: alerts ↔ runbooks (invariants 1–3)
    # ------------------------------------------------------------------
    if not args.quiet:
        print("==> Pass 1: alerts → runbook URLs (invariants 1–3)")

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

    # ------------------------------------------------------------------
    # Pass 2: in-runbook links (invariants 4–6)
    # ------------------------------------------------------------------
    if not args.quiet:
        print()
        print(
            "==> Pass 2: in-runbook navigation links (invariants 4–6)"
        )

    repo_resolved = repo.resolve()
    link_count = 0
    skipped_external = 0
    skipped_anchor = 0
    skipped_anchor_invalid = 0  # anchors that fail; not skipped, just tallied
    checked_path_links = 0

    for md_path in runbook_files:
        rel_md = md_path.relative_to(repo)
        for lineno, label, target in extract_links(md_path):
            link_count += 1

            if target.startswith(_EXTERNAL_PREFIXES):
                skipped_external += 1
                continue

            # Pure anchor link (#section). Validate against the same file.
            if target.startswith("#"):
                anchor = target.lstrip("#")
                if not anchor:
                    fail(
                        f"{rel_md}:{lineno} [{label!r}] empty anchor "
                        f"target: {target!r}"
                    )
                    continue
                existing = anchors_for(md_path)
                if anchor not in existing:
                    fail(
                        f"{rel_md}:{lineno} [{label!r}] intra-file anchor "
                        f"#{anchor} not found; nearby anchors: "
                        f"{sorted(a for a in existing if a.startswith(anchor[:3]))[:5]}"
                    )
                continue

            # Path link (optionally with #anchor). Resolve relative to
            # the source file's parent directory.
            if "#" in target:
                path_part, anchor = target.split("#", 1)
            else:
                path_part, anchor = target, ""

            if not path_part:
                fail(
                    f"{rel_md}:{lineno} [{label!r}] empty path in link "
                    f"target: {target!r}"
                )
                continue

            try:
                resolved = (md_path.parent / path_part).resolve()
            except (ValueError, OSError) as e:
                fail(
                    f"{rel_md}:{lineno} [{label!r}] failed to resolve "
                    f"path {path_part!r}: {e}"
                )
                continue

            # Repo containment check: catch escapes via excessive ../
            try:
                resolved_rel = resolved.relative_to(repo_resolved)
            except ValueError:
                fail(
                    f"{rel_md}:{lineno} [{label!r}] link target "
                    f"{path_part!r} escapes repo root (resolved to "
                    f"{resolved})"
                )
                continue

            if not resolved.exists():
                fail(
                    f"{rel_md}:{lineno} [{label!r}] missing file: "
                    f"{path_part!r} (resolved to {resolved_rel})"
                )
                continue

            checked_path_links += 1

            # If an anchor fragment is present and the target is markdown,
            # verify the anchor exists in that file.
            if anchor and resolved.suffix.lower() == ".md":
                existing = anchors_for(resolved)
                if anchor not in existing:
                    fail(
                        f"{rel_md}:{lineno} [{label!r}] anchor #{anchor} "
                        f"not found in {resolved_rel}; nearby anchors: "
                        f"{sorted(a for a in existing if a.startswith(anchor[:3]))[:5]}"
                    )

    if not args.quiet:
        print(
            f"  scanned {link_count} link(s) across {len(runbook_files)} "
            f"runbook file(s)"
        )
        print(
            f"    external (skipped):       {skipped_external}"
        )
        print(
            f"    intra-file anchors:       "
            f"{link_count - skipped_external - checked_path_links - sum(1 for v in violations if 'intra-file anchor' in v or 'empty anchor' in v)}"
            f" (validated)"
        )
        print(f"    path links (validated):   {checked_path_links}")

    # ------------------------------------------------------------------
    # Summary
    # ------------------------------------------------------------------
    print()
    if violations:
        print(
            f"FAIL: {len(violations)} violation(s) across "
            f"{len(alerts)} alert(s) and {len(runbook_files)} runbook(s)",
            file=sys.stderr,
        )
        return 1

    print(
        f"OK: {len(alerts)}/{len(alerts)} alerts have resolvable "
        f"runbook_url anchors; {link_count - skipped_external} in-runbook "
        f"link(s) across {len(runbook_files)} file(s) all resolve."
    )
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
